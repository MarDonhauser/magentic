package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type rowKind int

const (
	rowProject rowKind = iota
	rowAgent
)

const orphanKey = "\x00orphans"

type treeRow struct {
	kind    rowKind
	project *Project
	agent   Agent
}

type inputKind int

const (
	inputNone inputKind = iota
	inputNewSession
	inputNewWorktree
	inputAddProject
	inputRename
)

type pollResult struct {
	statuses   map[string]AgentStatus
	git        map[string]GitInfo
	session    map[string]SessionChanges
	preview    string
	discovered []Agent
	diskMain   map[string]string
}

type tickMsg time.Time
type pollMsg pollResult
type attachDoneMsg struct{ err error }
type webStartedMsg struct{ url string }

type model struct {
	state          *State
	cursor         int
	collapsed      map[string]bool
	input          textinput.Model
	inputKind      inputKind
	pendingProject string
	renameFrom     string
	confirmKill    bool
	confirmRmProj  bool
	poll           pollResult
	flash          string
	flashIsErr     bool
	flashTime      time.Time
	width          int
	height         int
	webRunning     bool
	focusAgent     string
	focusPreview   string
}

func newModel(s *State) model {
	reconcile(s)
	return model{state: s, collapsed: map[string]bool{}}
}

func discoverNew(s *State) []Agent {
	known := map[string]bool{}
	for _, a := range s.Agents {
		known[tmuxSessionName(a.Name)] = true
	}
	var out []Agent
	for _, sess := range TmuxListSessions() {
		if known[sess] {
			continue
		}
		name := strings.TrimPrefix(sess, sessionPrefix)
		dir, _ := tmux("display-message", "-p", "-t", targetPane(sess), "#{pane_current_path}")
		dir = strings.TrimSpace(dir)
		createdRaw, _ := tmux("display-message", "-p", "-t", targetPane(sess), "#{session_created}")
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
			if wtBase := filepath.Dir(p.Path) + string(os.PathSeparator) + filepath.Base(p.Path) + "-agents"; strings.HasPrefix(dir, wtBase+string(os.PathSeparator)) {
				proj = p.Name
				worktree = true
				break
			}
		}
		out = append(out, Agent{Name: name, Project: proj, Dir: dir, Worktree: worktree, CreatedAt: time.Unix(ts, 0)})
	}
	return out
}

func reconcile(s *State) {
	if agents := discoverNew(s); len(agents) > 0 {
		for _, a := range agents {
			s.AddAgent(a)
		}
		s.Save()
	}
}

func (m model) orphanAgents() []Agent {
	var out []Agent
	for _, a := range m.state.Agents {
		if a.Project == "" || m.state.ProjectByName(a.Project) == nil {
			out = append(out, a)
		}
	}
	return out
}

func (m model) rows() []treeRow {
	var rows []treeRow
	for i := range m.state.Projects {
		p := &m.state.Projects[i]
		rows = append(rows, treeRow{kind: rowProject, project: p})
		if m.collapsed[p.Name] {
			continue
		}
		for _, a := range m.state.AgentsFor(p.Name) {
			rows = append(rows, treeRow{kind: rowAgent, agent: a, project: p})
		}
	}
	if orphans := m.orphanAgents(); len(orphans) > 0 {
		rows = append(rows, treeRow{kind: rowProject, project: nil})
		if !m.collapsed[orphanKey] {
			for _, a := range orphans {
				rows = append(rows, treeRow{kind: rowAgent, agent: a})
			}
		}
	}
	return rows
}

func (m model) selectedRow() *treeRow {
	rows := m.rows()
	if len(rows) == 0 || m.cursor >= len(rows) {
		return nil
	}
	r := rows[m.cursor]
	return &r
}

func (m model) selectedAgent() *Agent {
	if r := m.selectedRow(); r != nil && r.kind == rowAgent {
		return &r.agent
	}
	return nil
}

func (m model) contextProject() *Project {
	r := m.selectedRow()
	if r == nil {
		return nil
	}
	if r.project != nil {
		return r.project
	}
	if r.kind == rowAgent && r.agent.Project != "" {
		return m.state.ProjectByName(r.agent.Project)
	}
	return nil
}

func (m *model) clampCursor() {
	n := len(m.rows())
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *model) selectAgent(name string) {
	for i, r := range m.rows() {
		if r.kind == rowAgent && r.agent.Name == name {
			m.cursor = i
			return
		}
	}
}

func (m *model) setFlash(msg string, isErr bool) {
	m.flash = msg
	m.flashIsErr = isErr
	m.flashTime = time.Now()
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func pollCmd(state State, selected *Agent) tea.Cmd {
	return func() tea.Msg {
		res := pollResult{git: map[string]GitInfo{}, session: map[string]SessionChanges{}}
		statuses, contents := CollectStatuses(state.Agents)
		res.statuses = statuses
		for _, a := range state.Agents {
			if selected != nil && a.Name == selected.Name {
				res.preview = contents[a.Name]
			}
			if _, ok := res.git[a.Dir]; !ok {
				res.git[a.Dir] = CollectGitInfo(a.Dir)
			}
			if gi := res.git[a.Dir]; gi.IsRepo {
				res.session[a.Name] = CollectSessionChanges(a, gi)
			}
		}
		for _, p := range state.Projects {
			if _, ok := res.git[p.Path]; !ok {
				res.git[p.Path] = CollectGitInfo(p.Path)
			}
		}
		res.discovered = discoverNew(&state)
		if disk, err := LoadState(); err == nil {
			res.diskMain = map[string]string{}
			for _, p := range disk.Projects {
				res.diskMain[p.Name] = p.MainBranch
			}
		}
		return pollMsg(res)
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.pollNow(), tick())
}

func (m model) pollNow() tea.Cmd {
	return pollCmd(*m.state, m.selectedAgent())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeFocusedWindow()
		return m, nil
	case focusTickMsg:
		if m.focusAgent == "" {
			return m, nil
		}
		return m, tea.Batch(focusPollCmd(m.focusAgent), focusTick())
	case focusPreviewMsg:
		m.focusPreview = string(msg)
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.pollNow(), tick())
	case pollMsg:
		m.poll = pollResult(msg)
		if m.poll.diskMain != nil {
			for i := range m.state.Projects {
				if mb, ok := m.poll.diskMain[m.state.Projects[i].Name]; ok {
					m.state.Projects[i].MainBranch = mb
				}
			}
		}
		if m.focusAgent != "" {
			if st := m.poll.statuses[m.focusAgent]; st == StatusDead {
				return m.exitFocus()
			}
		}
		if len(m.poll.discovered) > 0 {
			changed := false
			for _, a := range m.poll.discovered {
				if !m.state.HasAgent(a.Name) {
					m.state.AddAgent(a)
					changed = true
				}
			}
			if changed {
				m.state.Save()
			}
		}
		return m, nil
	case attachDoneMsg:
		return m, m.pollNow()
	case webStartedMsg:
		m.webRunning = true
		m.setFlash("Übersicht: "+msg.url, false)
		return m, nil
	case tea.MouseMsg:
		if m.inputKind != inputNone || m.confirmKill || m.confirmRmProj {
			return m, nil
		}
		return m.updateMouse(msg)
	case tea.KeyMsg:
		if m.focusAgent != "" {
			return m.updateFocusKey(msg)
		}
		if m.inputKind != inputNone {
			return m.updateInput(msg)
		}
		if m.confirmKill || m.confirmRmProj {
			return m.updateConfirm(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.focusAgent != "" {
			return m, nil
		}
		if m.cursor > 0 {
			m.cursor--
		}
		return m, m.pollNow()
	case tea.MouseButtonWheelDown:
		if m.focusAgent != "" {
			return m, nil
		}
		if m.cursor < len(m.rows())-1 {
			m.cursor++
		}
		return m, m.pollNow()
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.handleClick(msg.X, msg.Y)
	}
	return m, nil
}

func (m model) handleClick(x, y int) (tea.Model, tea.Cmd) {
	treeW := m.treeWidth()
	rows := m.rows()
	idx := y - 2
	if x < treeW {
		if m.focusAgent != "" {
			next, _ := m.exitFocus()
			m = next.(model)
		}
		if idx < 0 || idx >= len(rows) {
			return m, m.pollNow()
		}
		r := rows[idx]
		if r.kind == rowProject {
			m.cursor = idx
			key := orphanKey
			if r.project != nil {
				key = r.project.Name
			}
			m.collapsed[key] = !m.collapsed[key]
			m.clampCursor()
			return m, m.pollNow()
		}
		if m.cursor == idx {
			return m.enterFocus()
		}
		m.cursor = idx
		return m, m.pollNow()
	}
	if m.focusAgent != "" {
		return m, nil
	}
	if m.selectedAgent() != nil {
		_, detailW, innerH := m.layout()
		_, previewStart := m.detailContent(detailW-4, innerH)
		if previewStart >= 0 && idx >= previewStart && idx < innerH {
			return m.enterFocus()
		}
	}
	return m, nil
}

func (m model) maxAgentNameLen() int {
	n := 8
	for _, a := range m.state.Agents {
		if l := len([]rune(a.Name)); l > n {
			n = l
		}
	}
	if n > 18 {
		n = 18
	}
	return n
}

func (m model) treeWidth() int {
	w := m.maxAgentNameLen() + 27
	for _, p := range m.state.Projects {
		if l := len([]rune(p.Name)) + 14; l > w {
			w = l
		}
	}
	if w < 32 {
		w = 32
	}
	if cap := m.width * 55 / 100; m.width > 0 && w > cap {
		w = cap
	}
	return w
}

func (m model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, m.pollNow()
	case "down", "j":
		if m.cursor < len(m.rows())-1 {
			m.cursor++
		}
		return m, m.pollNow()
	case "enter", " ", "a":
		r := m.selectedRow()
		if r == nil {
			return m, nil
		}
		if r.kind == rowProject {
			key := orphanKey
			if r.project != nil {
				key = r.project.Name
			}
			m.collapsed[key] = !m.collapsed[key]
			m.clampCursor()
			return m, nil
		}
		if msg.String() == "a" {
			return m.attach()
		}
		return m.enterFocus()
	case "n":
		return m.startInput(inputNewSession)
	case "w":
		return m.startInput(inputNewWorktree)
	case "r":
		if a := m.selectedAgent(); a != nil {
			m.renameFrom = a.Name
			return m.startInput(inputRename)
		}
		return m, nil
	case "x":
		r := m.selectedRow()
		if r == nil {
			return m, nil
		}
		if r.kind == rowAgent {
			m.confirmKill = true
		} else if r.project != nil {
			m.confirmRmProj = true
		}
		return m, nil
	case "p":
		return m.startInput(inputAddProject)
	case "o":
		return m.openWeb()
	case "d":
		return m.sendSkillToSelected("/done ")
	case "D":
		return m.sendSkillToSelected("/deploy ")
	case "g":
		return m, m.pollNow()
	}
	return m, nil
}

func (m model) sendSkillToSelected(cmd string) (tea.Model, tea.Cmd) {
	label := strings.TrimSpace(cmd)
	a := m.selectedAgent()
	if a == nil {
		if label == "/deploy" {
			if p := m.contextProject(); p != nil {
				return m.startSkillSession(p, cmd)
			}
		}
		m.setFlash("Erst einen Agent auswählen ("+label+" läuft in dessen Session)", true)
		return m, nil
	}
	sn := tmuxSessionName(a.Name)
	st := m.poll.statuses[a.Name]
	if !TmuxHasSession(sn) || st == StatusExited || st == StatusDead {
		m.setFlash("Claude läuft in dieser Session nicht mehr", true)
		return m, nil
	}
	if st == StatusBlocked {
		m.setFlash(a.Name+" wartet auf eine Antwort — erst den Dialog beantworten (⏎)", true)
		return m, nil
	}
	sendSlashCommand(sn, cmd)
	m.setFlash(label+" an "+a.Name+" gesendet", false)
	return m, m.pollNow()
}

func (m model) startSkillSession(p *Project, cmd string) (tea.Model, tea.Cmd) {
	name := PickAgentName(m.state)
	session := tmuxSessionName(name)
	if err := TmuxNewClaudeSession(session, p.Path, ""); err != nil {
		m.setFlash("tmux: "+err.Error(), true)
		return m, nil
	}
	baseCommit, baseDirty := CaptureBaseline(p.Path)
	m.state.AddAgent(Agent{Name: name, Project: p.Name, Dir: p.Path, CreatedAt: time.Now(), BaseCommit: baseCommit, BaseDirty: baseDirty})
	m.state.Save()
	go sendPromptWhenReady(session, cmd)
	m.collapsed[p.Name] = false
	m.selectAgent(name)
	m.setFlash(fmt.Sprintf("Session %q gestartet — %s wird getippt", name, strings.TrimSpace(cmd)), false)
	return m, m.pollNow()
}

func (m model) openWeb() (tea.Model, tea.Cmd) {
	url := fmt.Sprintf("http://localhost:%d", webPort)
	if m.webRunning || portInUse(webPort) {
		openBrowser(url)
		m.setFlash("Übersicht: "+url, false)
		return m, nil
	}
	state := m.state
	return m, func() tea.Msg {
		go ServeWeb(state, webPort)
		time.Sleep(300 * time.Millisecond)
		openBrowser(url)
		return webStartedMsg{url: url}
	}
}

func (m model) startInput(kind inputKind) (tea.Model, tea.Cmd) {
	if kind == inputNewSession || kind == inputNewWorktree {
		p := m.contextProject()
		if p == nil {
			m.setFlash("Kein Projekt gewählt — erst mit p ein Projekt anlegen bzw. eins auswählen", true)
			return m, nil
		}
		m.pendingProject = p.Name
	}
	ti := textinput.New()
	ti.CharLimit = 200
	switch kind {
	case inputNewSession:
		ti.Prompt = fmt.Sprintf("Neuer Agent in %s (leer = auto): ", m.pendingProject)
	case inputNewWorktree:
		ti.Prompt = fmt.Sprintf("Neuer Agent im Worktree von %s (leer = auto): ", m.pendingProject)
	case inputAddProject:
		ti.Prompt = "Projektpfad: "
		ti.SetValue("~/Projects/")
	case inputRename:
		ti.Prompt = "Neuer Name: "
		ti.SetValue(m.renameFrom)
	}
	ti.Focus()
	m.input = ti
	m.inputKind = kind
	return m, textinput.Blink
}

func (m model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inputKind = inputNone
		return m, nil
	case "enter":
		kind := m.inputKind
		value := strings.TrimSpace(m.input.Value())
		m.inputKind = inputNone
		return m.commitInput(kind, value)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, ":", "-")
	return name
}

func (m model) commitInput(kind inputKind, value string) (tea.Model, tea.Cmd) {
	switch kind {
	case inputNewSession, inputNewWorktree:
		return m.createAgent(kind == inputNewWorktree, value)
	case inputAddProject:
		return m.addProject(value)
	case inputRename:
		return m.renameAgent(value)
	}
	return m, nil
}

func (m model) createAgent(worktree bool, name string) (tea.Model, tea.Cmd) {
	proj := m.state.ProjectByName(m.pendingProject)
	if proj == nil {
		m.setFlash("Projekt nicht gefunden", true)
		return m, nil
	}
	if name == "" {
		name = PickAgentName(m.state)
	} else {
		name = sanitizeName(name)
	}
	if name == "" || m.state.HasAgent(name) || TmuxHasSession(tmuxSessionName(name)) {
		m.setFlash(fmt.Sprintf("Name %q ist ungültig oder schon vergeben", name), true)
		return m, nil
	}
	dir := proj.Path
	if worktree {
		wt, err := CreateWorktree(proj.Path, name)
		if err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		dir = wt
	}
	if err := TmuxNewClaudeSession(tmuxSessionName(name), dir, ""); err != nil {
		m.setFlash("tmux: "+err.Error(), true)
		return m, nil
	}
	baseCommit, baseDirty := CaptureBaseline(dir)
	m.state.AddAgent(Agent{Name: name, Project: proj.Name, Dir: dir, Worktree: worktree, CreatedAt: time.Now(), BaseCommit: baseCommit, BaseDirty: baseDirty})
	m.state.Save()
	m.collapsed[proj.Name] = false
	m.selectAgent(name)
	kind := "Session"
	if worktree {
		kind = "Worktree-Session"
	}
	m.setFlash(fmt.Sprintf("Agent %q gestartet (%s in %s)", name, kind, proj.Name), false)
	return m, m.pollNow()
}

func (m model) addProject(path string) (tea.Model, tea.Cmd) {
	if path == "" {
		return m, nil
	}
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		m.setFlash("Verzeichnis nicht gefunden: "+path, true)
		return m, nil
	}
	name := filepath.Base(path)
	if m.state.ProjectByName(name) != nil {
		m.setFlash(fmt.Sprintf("Projekt %q existiert schon", name), true)
		return m, nil
	}
	m.state.Projects = append(m.state.Projects, Project{Name: name, Path: path})
	m.state.Save()
	for i, r := range m.rows() {
		if r.kind == rowProject && r.project != nil && r.project.Name == name {
			m.cursor = i
		}
	}
	m.setFlash(fmt.Sprintf("Projekt %q hinzugefügt", name), false)
	return m, m.pollNow()
}

func (m model) renameAgent(newName string) (tea.Model, tea.Cmd) {
	newName = sanitizeName(newName)
	if newName == "" || newName == m.renameFrom {
		return m, nil
	}
	if m.state.HasAgent(newName) || TmuxHasSession(tmuxSessionName(newName)) {
		m.setFlash(fmt.Sprintf("Name %q ist schon vergeben", newName), true)
		return m, nil
	}
	old := tmuxSessionName(m.renameFrom)
	if TmuxHasSession(old) {
		if _, err := tmux("rename-session", "-t", targetSession(old), tmuxSessionName(newName)); err != nil {
			m.setFlash("tmux rename: "+err.Error(), true)
			return m, nil
		}
	}
	m.state.RenameAgent(m.renameFrom, newName)
	m.state.Save()
	delete(m.poll.statuses, m.renameFrom)
	m.setFlash(fmt.Sprintf("%s → %s", m.renameFrom, newName), false)
	return m, m.pollNow()
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	yes := msg.String() == "y" || msg.String() == "enter"
	if m.confirmKill {
		m.confirmKill = false
		if !yes {
			return m, nil
		}
		a := m.selectedAgent()
		if a == nil {
			return m, nil
		}
		sn := tmuxSessionName(a.Name)
		if TmuxHasSession(sn) {
			TmuxKillSession(sn)
		}
		note := ""
		if a.Worktree {
			note = " — Worktree bleibt unter " + shortPath(a.Dir)
		}
		m.state.RemoveAgent(a.Name)
		m.state.Save()
		delete(m.poll.statuses, a.Name)
		m.clampCursor()
		m.setFlash(fmt.Sprintf("Agent %q beendet%s", a.Name, note), false)
		return m, m.pollNow()
	}
	if m.confirmRmProj {
		m.confirmRmProj = false
		if !yes {
			return m, nil
		}
		r := m.selectedRow()
		if r == nil || r.project == nil {
			return m, nil
		}
		p := r.project
		if len(m.state.AgentsFor(p.Name)) > 0 {
			m.setFlash("Projekt hat noch Agents — erst alle beenden (x)", true)
			return m, nil
		}
		out := m.state.Projects[:0]
		for _, pr := range m.state.Projects {
			if pr.Name != p.Name {
				out = append(out, pr)
			}
		}
		m.state.Projects = out
		m.state.Save()
		m.clampCursor()
		m.setFlash(fmt.Sprintf("Projekt %q entfernt (Dateien bleiben unberührt)", p.Name), false)
		return m, m.pollNow()
	}
	return m, nil
}

func (m model) attach() (tea.Model, tea.Cmd) {
	a := m.selectedAgent()
	if a == nil {
		return m, nil
	}
	sn := tmuxSessionName(a.Name)
	if !TmuxHasSession(sn) {
		m.setFlash("Session existiert nicht mehr — mit x entfernen oder n neu starten", true)
		return m, nil
	}
	tmux("set-option", "-w", "-t", targetPane(sn), "window-size", "latest")
	if os.Getenv("TMUX") != "" {
		if err := exec.Command("tmux", "switch-client", "-t", targetSession(sn)).Run(); err != nil {
			m.setFlash("tmux switch-client: "+err.Error(), true)
		}
		return m, nil
	}
	cmd := exec.Command("tmux", "attach-session", "-t", targetSession(sn))
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return attachDoneMsg{err} })
}

func shortPath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "jetzt"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
