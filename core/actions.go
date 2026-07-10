package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func DiscoverNew(s *State) []Agent {
	known := map[string]bool{}
	for _, a := range s.Agents {
		known[SessionName(a.Name)] = true
	}
	var out []Agent
	for _, sess := range TmuxListSessions() {
		if known[sess] {
			continue
		}
		name := strings.TrimPrefix(sess, SessionPrefix)
		dir, _ := Tmux("display-message", "-p", "-t", TargetPane(sess), "#{pane_current_path}")
		dir = strings.TrimSpace(dir)
		createdRaw, _ := Tmux("display-message", "-p", "-t", TargetPane(sess), "#{session_created}")
		ts, _ := strconv.ParseInt(strings.TrimSpace(createdRaw), 10, 64)
		if ts == 0 {
			ts = time.Now().Unix()
		}
		proj := ""
		worktree := false
		for _, p := range s.Projects {
			if dir == p.Path || strings.HasPrefix(dir, p.Path+string(os.PathSeparator)) {
				proj = p.Name
				worktree = dir != p.Path
				break
			}
			base := p.Path + "-agents" + string(os.PathSeparator)
			if strings.HasPrefix(dir, base) {
				proj = p.Name
				worktree = true
				break
			}
		}
		out = append(out, Agent{Name: name, Project: proj, Dir: dir, Worktree: worktree, CreatedAt: time.Unix(ts, 0)})
	}
	return out
}

func RestoreSessions(st *State) int {
	restored := 0
	changed := false
	kept := st.Agents[:0]
	for _, a := range st.Agents {
		sn := SessionName(a.Name)
		if TmuxHasSession(sn) {
			kept = append(kept, a)
			continue
		}
		if info, err := os.Stat(a.Dir); err != nil || !info.IsDir() {
			changed = true
			continue
		}
		extraArgs := "--continue"
		if a.SessionID != "" {
			extraArgs = "--resume " + a.SessionID
		}
		if err := TmuxNewClaudeSession(sn, a.Dir, extraArgs); err != nil {
			kept = append(kept, a)
			continue
		}
		restored++
		kept = append(kept, a)
	}
	st.Agents = kept
	if changed {
		st.Save()
	}
	return restored
}

func SendPromptWhenReady(session, prompt string, submit bool) {
	for i := 0; i < 180; i++ {
		time.Sleep(1 * time.Second)
		content := strings.ToLower(TmuxCapturePane(session, 0))
		if content == "" {
			return
		}
		if strings.Contains(content, "trust this folder") {
			continue
		}
		if strings.Contains(content, "shift+tab to cycle") {
			time.Sleep(500 * time.Millisecond)
			Tmux("send-keys", "-t", TargetPane(session), "-l", prompt)
			if submit {
				time.Sleep(300 * time.Millisecond)
				Tmux("send-keys", "-t", TargetPane(session), "Enter")
			}
			return
		}
	}
}

func SendSlashCommand(session, cmd string) {
	content := strings.ToLower(TmuxCapturePane(session, 0))
	if strings.Contains(content, "shift+tab to cycle") {
		Tmux("send-keys", "-t", TargetPane(session), "-l", cmd)
		Tmux("send-keys", "-t", TargetPane(session), "Enter")
		return
	}
	go SendPromptWhenReady(session, cmd, true)
}

func StartSkillAgent(st *State, dir, prompt, kind, nameHint string) (string, error) {
	for _, a := range DiscoverNew(st) {
		st.AddAgent(a)
	}
	name := PickAgentName(st, nameHint)
	session := SessionName(name)
	sid := NewUUID()
	if err := TmuxNewClaudeSession(session, dir, "--session-id "+sid); err != nil {
		return "", err
	}
	proj := ""
	for _, p := range st.Projects {
		if dir == p.Path || strings.HasPrefix(dir, p.Path+string(os.PathSeparator)) {
			proj = p.Name
			break
		}
	}
	baseCommit, baseDirty := CaptureBaseline(dir)
	st.AddAgent(Agent{Name: name, Project: proj, Dir: dir, Kind: kind, CreatedAt: time.Now(), BaseCommit: baseCommit, BaseDirty: baseDirty, SessionID: sid})
	go SendPromptWhenReady(session, prompt, true)
	return name, nil
}

func SendSkill(name, cmd string) error {
	sn := SessionName(name)
	if name == "" || !TmuxHasSession(sn) {
		return fmt.Errorf("Session läuft nicht mehr")
	}
	infos := TmuxPaneInfos()
	status := DetectClaudeStatus(true, infos[sn].Command, LastLines(TmuxCapturePane(sn, 0), 25))
	switch status {
	case StatusBlocked:
		return fmt.Errorf("%s wartet auf eine Antwort — erst den offenen Dialog beantworten", name)
	case StatusExited, StatusDead:
		return fmt.Errorf("Claude läuft in dieser Session nicht mehr")
	}
	SendSlashCommand(sn, cmd)
	return nil
}

func DoneAgent(name string) error {
	return SendSkill(name, "/done ")
}

func StartCleanup(st *State, path, mainBranch string) (string, error) {
	if mainBranch == "" {
		mainBranch = "main"
	}
	prompt := fmt.Sprintf("Diese Session wurde von magentic zum Aufräumen dieses Worktrees gestartet. "+
		"Sichte die uncommitteten Änderungen und die Commits auf diesem Branch, committe sinnvoll und bringe die Arbeit nach %s. "+
		"Zeige mir zuerst deinen Plan, bevor du etwas ausführst. Sag am Ende Bescheid, wenn der Worktree entfernt werden kann.", mainBranch)
	return StartSkillAgent(st, path, prompt, "cleanup", "cleanup "+filepath.Base(path))
}

func StartMerge(st *State, projPath, source, target string) (string, error) {
	prompt := fmt.Sprintf("Merge den Branch %q nach %q in diesem Repository. "+
		"Hole vorher den aktuellen Stand (git fetch). Falls Konflikte auftreten, löse sie sinnvoll und erkläre mir deine Entscheidungen. "+
		"Zeige mir zuerst deinen Plan, bevor du etwas ausführst, und frage mich, bevor du pushst.", source, target)
	return StartSkillAgent(st, projPath, prompt, "merge", "merge "+source)
}

func StartDeploy(st *State, projPath string) (string, error) {
	return StartSkillAgent(st, projPath, "/deploy ", "deploy", "deploy "+filepath.Base(projPath))
}

func RemoveWorktree(st *State, proj *Project, path string) error {
	if path == proj.Path {
		return fmt.Errorf("Haupt-Worktree kann nicht entfernt werden")
	}
	wts := CollectWorktrees(proj.Path)
	if len(wts) > 0 && wts[0].Path == path {
		return fmt.Errorf("Haupt-Worktree kann nicht entfernt werden")
	}
	valid := false
	for _, wt := range wts {
		if wt.Path == path {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("Pfad gehört nicht zu diesem Projekt")
	}
	for _, a := range DiscoverNew(st) {
		st.AddAgent(a)
	}
	var onPath []Agent
	for _, a := range st.Agents {
		if a.Dir == path {
			onPath = append(onPath, a)
		}
	}
	statuses, _, _ := CollectStatuses(onPath)
	for _, a := range onPath {
		if statuses[a.Name] == StatusRunning || statuses[a.Name] == StatusBlocked {
			return fmt.Errorf("Agent %q arbeitet gerade in diesem Worktree", a.Name)
		}
	}
	if gi := CollectGitInfo(path); !gi.Clean() {
		return fmt.Errorf("Worktree hat uncommittete Änderungen — erst aufräumen")
	}
	for _, a := range onPath {
		sn := SessionName(a.Name)
		if TmuxHasSession(sn) {
			TmuxKillSession(sn)
		}
	}
	if _, err := GitCmd(proj.Path, "worktree", "remove", path); err != nil {
		return fmt.Errorf("git worktree remove: %w", err)
	}
	return nil
}

func StartTodoSession(st *State, idx int) (string, error) {
	if idx < 0 || idx >= len(st.Todos) {
		return "", fmt.Errorf("Todo nicht gefunden")
	}
	todo := st.Todos[idx]
	proj := st.ProjectByName(todo.Project)
	if proj == nil {
		return "", fmt.Errorf("Todo hat kein Projekt — erst ein Projekt zuweisen")
	}
	name := PickAgentName(st, todo.Text)
	session := SessionName(name)
	sid := NewUUID()
	if err := TmuxNewClaudeSession(session, proj.Path, "--session-id "+sid); err != nil {
		return "", fmt.Errorf("tmux: %w", err)
	}
	baseCommit, baseDirty := CaptureBaseline(proj.Path)
	st.AddAgent(Agent{Name: name, Project: proj.Name, Dir: proj.Path, CreatedAt: time.Now(), BaseCommit: baseCommit, BaseDirty: baseDirty, SessionID: sid})
	st.Todos = append(st.Todos[:idx], st.Todos[idx+1:]...)
	if err := st.Save(); err != nil {
		return "", err
	}
	go SendPromptWhenReady(session, todo.Text, false)
	return name, nil
}

func CreateAgentSession(st *State, projName string, worktree bool, name string) (string, error) {
	proj := st.ProjectByName(projName)
	if proj == nil {
		return "", fmt.Errorf("Projekt nicht gefunden")
	}
	if name == "" {
		name = PickAgentName(st, projName)
	} else {
		name = SanitizeName(name)
	}
	if name == "" || st.HasAgent(name) || TmuxHasSession(SessionName(name)) {
		return "", fmt.Errorf("Name %q ist ungültig oder schon vergeben", name)
	}
	dir := proj.Path
	if worktree {
		wt, err := CreateWorktree(proj.Path, name)
		if err != nil {
			return "", err
		}
		dir = wt
	}
	sid := NewUUID()
	if err := TmuxNewClaudeSession(SessionName(name), dir, "--session-id "+sid); err != nil {
		return "", fmt.Errorf("tmux: %w", err)
	}
	baseCommit, baseDirty := CaptureBaseline(dir)
	st.AddAgent(Agent{Name: name, Project: proj.Name, Dir: dir, Worktree: worktree, CreatedAt: time.Now(), BaseCommit: baseCommit, BaseDirty: baseDirty, SessionID: sid})
	if err := st.Save(); err != nil {
		return "", err
	}
	return name, nil
}
