package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const webPort = 4321

//go:embed web/index.html
var indexHTML []byte

func ServeWeb(s *State, port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("/api/overview", func(w http.ResponseWriter, r *http.Request) {
		st, err := LoadState()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(BuildOverview(st))
	})
	mux.HandleFunc("/api/cleanup", handleCleanup)
	mux.HandleFunc("/api/worktree/remove", handleWorktreeRemove)
	mux.HandleFunc("/api/project/main", handleSetMain)
	mux.HandleFunc("/api/merge", handleMerge)
	mux.HandleFunc("/api/done", handleDone)
	mux.HandleFunc("/api/deploy", handleDeploy)
	return http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux)
}

type actionReq struct {
	Project string `json:"project"`
	Path    string `json:"path"`
	Main    string `json:"main"`
	Source  string `json:"source"`
	Target  string `json:"target"`
	Agent   string `json:"agent"`
}

func decodePost(w http.ResponseWriter, r *http.Request) (*actionReq, bool) {
	if r.Method != http.MethodPost {
		jsonError(w, "nur POST", 405)
		return nil, false
	}
	var req actionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, err.Error(), 400)
		return nil, false
	}
	return &req, true
}

func jsonOK(w http.ResponseWriter, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	if payload == nil {
		payload = map[string]any{}
	}
	payload["ok"] = true
	json.NewEncoder(w).Encode(payload)
}

func startSkillAgent(st *State, dir, prompt string) (string, error) {
	for _, a := range discoverNew(st) {
		st.AddAgent(a)
	}
	name := PickAgentName(st)
	session := tmuxSessionName(name)
	if err := TmuxNewClaudeSession(session, dir, ""); err != nil {
		return "", err
	}
	go sendPromptWhenReady(session, prompt, true)
	return name, nil
}

func handleSetMain(w http.ResponseWriter, r *http.Request) {
	req, ok := decodePost(w, r)
	if !ok {
		return
	}
	st, err := LoadState()
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	proj := st.ProjectByName(req.Project)
	if proj == nil {
		jsonError(w, "unbekanntes Projekt: "+req.Project, 400)
		return
	}
	proj.MainBranch = strings.TrimSpace(req.Main)
	if err := st.Save(); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, nil)
}

func handleMerge(w http.ResponseWriter, r *http.Request) {
	req, ok := decodePost(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Source) == "" || strings.TrimSpace(req.Target) == "" {
		jsonError(w, "source und target werden benötigt", 400)
		return
	}
	st, err := LoadState()
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	proj := st.ProjectByName(req.Project)
	if proj == nil {
		jsonError(w, "unbekanntes Projekt: "+req.Project, 400)
		return
	}
	prompt := fmt.Sprintf("Merge den Branch %q nach %q in diesem Repository. "+
		"Hole vorher den aktuellen Stand (git fetch). Falls Konflikte auftreten, löse sie sinnvoll und erkläre mir deine Entscheidungen. "+
		"Zeige mir zuerst deinen Plan, bevor du etwas ausführst, und frage mich, bevor du pushst.", req.Source, req.Target)
	name, err := startSkillAgent(st, proj.Path, prompt)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, map[string]any{"agent": name})
}

func handleDone(w http.ResponseWriter, r *http.Request) {
	req, ok := decodePost(w, r)
	if !ok {
		return
	}
	sn := tmuxSessionName(req.Agent)
	if req.Agent == "" || !TmuxHasSession(sn) {
		jsonError(w, "Session läuft nicht mehr", 404)
		return
	}
	cmds := TmuxPaneCommands()
	status := DetectClaudeStatus(true, cmds[sn], lastLines(TmuxCapturePane(sn, 0), 25))
	switch status {
	case StatusBlocked:
		jsonError(w, req.Agent+" wartet auf eine Antwort — erst den offenen Dialog beantworten", 409)
		return
	case StatusExited, StatusDead:
		jsonError(w, "Claude läuft in dieser Session nicht mehr", 409)
		return
	}
	sendSlashCommand(sn, "/done ")
	jsonOK(w, map[string]any{"agent": req.Agent})
}

func handleDeploy(w http.ResponseWriter, r *http.Request) {
	req, ok := decodePost(w, r)
	if !ok {
		return
	}
	st, err := LoadState()
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	proj := st.ProjectByName(req.Project)
	if proj == nil {
		jsonError(w, "unbekanntes Projekt: "+req.Project, 400)
		return
	}
	name, err := startSkillAgent(st, proj.Path, "/deploy ")
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, map[string]any{"agent": name})
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decodeAction(w http.ResponseWriter, r *http.Request) (*actionReq, *State, *Project, bool) {
	if r.Method != http.MethodPost {
		jsonError(w, "nur POST", 405)
		return nil, nil, nil, false
	}
	var req actionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, err.Error(), 400)
		return nil, nil, nil, false
	}
	st, err := LoadState()
	if err != nil {
		jsonError(w, err.Error(), 500)
		return nil, nil, nil, false
	}
	proj := st.ProjectByName(req.Project)
	if proj == nil {
		jsonError(w, "unbekanntes Projekt: "+req.Project, 400)
		return nil, nil, nil, false
	}
	valid := req.Path == proj.Path
	for _, wt := range CollectWorktrees(proj.Path) {
		if wt.Path == req.Path {
			valid = true
		}
	}
	if !valid {
		jsonError(w, "Pfad gehört nicht zu diesem Projekt", 400)
		return nil, nil, nil, false
	}
	return &req, st, proj, true
}

func handleCleanup(w http.ResponseWriter, r *http.Request) {
	req, st, _, ok := decodeAction(w, r)
	if !ok {
		return
	}
	for _, a := range discoverNew(st) {
		st.AddAgent(a)
	}
	mainBranch := req.Main
	if mainBranch == "" {
		mainBranch = "main"
	}
	prompt := fmt.Sprintf("Diese Session wurde von magentic zum Aufräumen dieses Worktrees gestartet. "+
		"Sichte die uncommitteten Änderungen und die Commits auf diesem Branch, committe sinnvoll und bringe die Arbeit nach %s. "+
		"Zeige mir zuerst deinen Plan, bevor du etwas ausführst. Sag am Ende Bescheid, wenn der Worktree entfernt werden kann.", mainBranch)
	name := PickAgentName(st)
	session := tmuxSessionName(name)
	if err := TmuxNewClaudeSession(session, req.Path, ""); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	go sendPromptWhenReady(session, prompt, true)
	jsonOK(w, map[string]any{"agent": name})
}

func sendSlashCommand(session, cmd string) {
	content := strings.ToLower(TmuxCapturePane(session, 0))
	if strings.Contains(content, "shift+tab to cycle") {
		tmux("send-keys", "-t", targetPane(session), "-l", cmd)
		tmux("send-keys", "-t", targetPane(session), "Enter")
		return
	}
	go sendPromptWhenReady(session, cmd, true)
}

func sendPromptWhenReady(session, prompt string, submit bool) {
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
			tmux("send-keys", "-t", targetPane(session), "-l", prompt)
			if submit {
				time.Sleep(300 * time.Millisecond)
				tmux("send-keys", "-t", targetPane(session), "Enter")
			}
			return
		}
	}
}

func handleWorktreeRemove(w http.ResponseWriter, r *http.Request) {
	req, st, proj, ok := decodeAction(w, r)
	if !ok {
		return
	}
	if req.Path == proj.Path {
		jsonError(w, "Haupt-Worktree kann nicht entfernt werden", 400)
		return
	}
	wts := CollectWorktrees(proj.Path)
	if len(wts) > 0 && wts[0].Path == req.Path {
		jsonError(w, "Haupt-Worktree kann nicht entfernt werden", 400)
		return
	}
	for _, a := range discoverNew(st) {
		st.AddAgent(a)
	}
	var onPath []Agent
	for _, a := range st.Agents {
		if a.Dir == req.Path {
			onPath = append(onPath, a)
		}
	}
	statuses, _ := CollectStatuses(onPath)
	for _, a := range onPath {
		if statuses[a.Name] == StatusRunning || statuses[a.Name] == StatusBlocked {
			jsonError(w, fmt.Sprintf("Agent %q arbeitet gerade in diesem Worktree", a.Name), 409)
			return
		}
	}
	if gi := CollectGitInfo(req.Path); !gi.Clean() {
		jsonError(w, "Worktree hat uncommittete Änderungen — erst aufräumen", 409)
		return
	}
	for _, a := range onPath {
		sn := tmuxSessionName(a.Name)
		if TmuxHasSession(sn) {
			TmuxKillSession(sn)
		}
	}
	if _, err := gitCmd(proj.Path, "worktree", "remove", req.Path); err != nil {
		jsonError(w, "git worktree remove: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func portInUse(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	case "linux":
		exec.Command("xdg-open", url).Start()
	}
}
