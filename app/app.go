package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"magentic/core"

	"github.com/creack/pty"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx              context.Context
	mu               sync.Mutex
	terms            map[string]*ptyTerm
	activeTerm       string
	dsMu             sync.Mutex
	dsPrev           *DeployStatus
	deployments      []core.AttentionDeploymentOutcome
	deploySequence   uint64
	attentionOnce    sync.Once
	attention        *core.AttentionPlanner
	attentionEventMu sync.Mutex
	attentionEvents  []core.AttentionEvent
	observationMu    sync.Mutex
	observation      core.ObservationSnapshot
	observationAt    time.Time
	observeSessions  func(context.Context, []core.Session) core.ObservationSnapshot
	startTerm        func(*exec.Cmd, *pty.Winsize) (*os.File, error)
}

type ptyTerm struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

func NewApp() *App {
	return &App{terms: map[string]*ptyTerm{}}
}

func (a *App) attentionPlanner() *core.AttentionPlanner {
	a.attentionOnce.Do(func() {
		a.attention = core.NewAttentionPlanner(core.AttentionPlannerConfig{})
	})
	return a.attention
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	installNativeNotifier()
	runtime.OnFileDrop(ctx, a.onFileDrop)
	core.Logf("startup: pid %d", os.Getpid())
	core.TmuxConfigureUX()
	if st, err := core.LoadState(); err == nil {
		if n := core.RestoreSessions(st); n > 0 {
			a.executeAttentionEvents(core.AttentionEvent{
				Key: "startup:restore", Kind: core.AttentionEventStartupRestored, Count: n,
			})
		}
	} else {
		core.Logf("startup: state laden fehlgeschlagen: %v", err)
		a.executeAttentionEvents(core.AttentionEvent{
			Key: "startup:restore", Kind: core.AttentionEventStartupFailed,
		})
	}
	go a.watchLoop()
}

func (a *App) onFileDrop(x, y int, paths []string) {
	name := a.getActiveTerm()
	if name == "" || len(paths) == 0 {
		return
	}
	a.mu.Lock()
	t := a.terms[name]
	a.mu.Unlock()
	if t == nil {
		return
	}
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(escapeTermPath(p))
		b.WriteByte(' ')
	}
	t.ptmx.Write([]byte(b.String()))
}

func escapeTermPath(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch r {
		case ' ', '\t', '\\', '\'', '"', '`', '$', '(', ')', '&', ';', '|', '<', '>', '*', '?', '[', ']':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for name, t := range a.terms {
		t.close()
		delete(a.terms, name)
	}
}

func (a *App) Overview(fresh bool) (core.Overview, error) {
	st, err := core.LoadState()
	if err != nil {
		return core.Overview{}, err
	}
	if fresh {
		core.FlushGitMemo()
	}
	snapshot := a.observationFor(st.Agents, fresh)
	return core.BuildOverviewFromObservation(st, snapshot), nil
}

func (a *App) Projects() ([]string, error) {
	st, err := core.LoadState()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, p := range st.Projects {
		names = append(names, p.Name)
	}
	return names, nil
}

func loadSessionByID(rawID string) (*core.State, core.Session, error) {
	id := core.SessionID(strings.TrimSpace(rawID))
	if id == "" {
		return nil, core.Session{}, fmt.Errorf("SessionID fehlt")
	}
	st, err := core.LoadState()
	if err != nil {
		return nil, core.Session{}, err
	}
	session := st.SessionByID(id)
	if session == nil {
		return nil, core.Session{}, fmt.Errorf("unbekannte SessionID: %s", id)
	}
	return st, *session, nil
}

// loadSessionTarget resolves every identified browser action exclusively by
// SessionID. The name fallback exists only for persisted legacy Dock tabs,
// which predate IDs in the browser state; a supplied but stale ID never falls
// through to a newly-created Session that happens to reuse its old name.
func loadSessionTarget(rawID, legacyDockName string) (*core.State, core.Session, error) {
	if strings.TrimSpace(rawID) != "" {
		return loadSessionByID(rawID)
	}
	name := strings.TrimSpace(legacyDockName)
	if name == "" {
		return nil, core.Session{}, fmt.Errorf("SessionID fehlt")
	}
	st, err := core.LoadState()
	if err != nil {
		return nil, core.Session{}, err
	}
	session := st.AgentByName(name)
	if session == nil || !session.IsDock() {
		return nil, core.Session{}, fmt.Errorf("unbekannter Legacy-Dock-Tab: %s", name)
	}
	return st, *session, nil
}

func loadProjectByID(rawID string) (*core.State, core.Project, error) {
	id := core.ProjectID(strings.TrimSpace(rawID))
	if id == "" {
		return nil, core.Project{}, fmt.Errorf("ProjectID fehlt")
	}
	st, err := core.LoadState()
	if err != nil {
		return nil, core.Project{}, err
	}
	project := st.ProjectByID(id)
	if project == nil {
		return nil, core.Project{}, fmt.Errorf("unbekannte ProjectID: %s", id)
	}
	return st, *project, nil
}

func (a *App) PickFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Repository-Ordner wählen",
	})
}

func (a *App) AddProject(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("kein Pfad angegeben")
	}
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Verzeichnis nicht gefunden: %s", abs)
	}
	name := filepath.Base(abs)
	if _, err := core.OpenRegistry(core.StatePath()).Change(a.ctx, core.RegisterProject(core.Project{Name: name, Path: abs})); err != nil {
		return "", err
	}
	return name, nil
}

func (a *App) RemoveProject(projectID string) error {
	_, project, err := loadProjectByID(projectID)
	if err != nil {
		return err
	}
	_, err = core.OpenRegistry(core.StatePath()).Change(a.ctx, core.RemoveProject(project.ID, project.Name))
	return err
}

func (a *App) ReorderProjects(order []string) error {
	st, err := core.LoadState()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(order))
	seen := make(map[core.ProjectID]bool, len(order))
	for _, rawID := range order {
		id := core.ProjectID(strings.TrimSpace(rawID))
		if id == "" || seen[id] {
			return fmt.Errorf("ungültige ProjectID in Sortierung: %q", rawID)
		}
		project := st.ProjectByID(id)
		if project == nil {
			return fmt.Errorf("unbekannte ProjectID in Sortierung: %s", id)
		}
		seen[id] = true
		names = append(names, project.Name)
	}
	_, err = core.OpenRegistry(core.StatePath()).Change(a.ctx, core.ReorderProjects(names))
	return err
}

func (a *App) SaveImage(dataB64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("leeres Bild")
	}
	ext := ".png"
	switch {
	case len(data) > 2 && data[0] == 0xff && data[1] == 0xd8:
		ext = ".jpg"
	case len(data) > 3 && string(data[:4]) == "GIF8":
		ext = ".gif"
	case len(data) > 11 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		ext = ".webp"
	}
	dir := filepath.Join(os.TempDir(), "magentic-paste")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, fmt.Sprintf("paste-%d%s", time.Now().UnixNano(), ext))
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func (a *App) MarkSeen(name string) error {
	st, err := core.LoadState()
	if err != nil {
		return err
	}
	session := st.AgentByName(name)
	if session == nil {
		return fmt.Errorf("unbekannte Session: %s", name)
	}
	_, err = core.OpenRegistry(core.StatePath()).Change(a.ctx, core.MarkSessionSeen(session.ID, session.Name, time.Now()))
	return err
}

func (a *App) GitGraph(project string, limit int) (core.GitGraph, error) {
	st, err := core.LoadState()
	if err != nil {
		return core.GitGraph{}, err
	}
	return core.BuildGitGraph(st, project, limit), nil
}

func (a *App) Board(project string) (core.Board, error) {
	st, err := core.LoadState()
	if err != nil {
		return core.Board{}, err
	}
	return core.BuildBoard(st, project), nil
}

// BoardArchive is an explicit, bounded archive query. Board remains the fast
// current-work default used during normal navigation.
func (a *App) BoardArchive(project string, limit int) (core.Board, error) {
	st, err := core.LoadState()
	if err != nil {
		return core.Board{}, err
	}
	return core.BuildBoardWithQuery(st, project, core.SpecificationQuery{
		IncludeArchived: true,
		ArchiveLimit:    limit,
	}), nil
}

func (a *App) Stats(days int) (core.Stats, error) {
	st, err := core.LoadState()
	if err != nil {
		return core.Stats{}, err
	}
	return core.BuildStats(st, days), nil
}

func (a *App) StartBoardItem(project, token string) (string, error) {
	st, err := core.LoadState()
	if err != nil {
		return "", err
	}
	proj := st.ProjectByName(project)
	if proj == nil {
		return "", fmt.Errorf("unbekanntes Projekt: %s", project)
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	intent, err := core.NewSpecifications().ResolveStart(ctx, *proj, core.SpecificationStartToken(token))
	if err != nil {
		return "", fmt.Errorf("Specification kann nicht gestartet werden: %w", err)
	}
	name, err := core.StartSpecificationSession(st, intent)
	if err != nil {
		return "", err
	}
	return name, nil
}

func (a *App) Breaks() (core.BreakAdvice, error) {
	st, err := core.LoadState()
	if err != nil {
		return core.BreakAdvice{}, err
	}
	return core.BreakStatusFromObservation(st, a.observationFor(st.Agents, false)), nil
}

func (a *App) BreakHeartbeat(active bool) core.BreakAdvice {
	return core.BreakHeartbeat(active)
}

func (a *App) TakeBreak() error {
	if err := core.TakeBreak(); err != nil {
		return err
	}
	a.executeAttentionEvents(core.AttentionEvent{Kind: core.AttentionEventBreakReset})
	return nil
}

func (a *App) EndBreak() error {
	if err := core.EndBreak(); err != nil {
		return err
	}
	a.executeAttentionEvents(core.AttentionEvent{Kind: core.AttentionEventBreakReset})
	return nil
}

func (a *App) SnoozeBreak() error {
	if err := core.SnoozeBreak(); err != nil {
		return err
	}
	a.executeAttentionEvents(core.AttentionEvent{Kind: core.AttentionEventBreakReset})
	return nil
}

func (a *App) BreakConfig() core.BreakConfig {
	return core.GetBreakConfig()
}

func (a *App) SetBreakConfig(c core.BreakConfig) error {
	return core.SetBreakConfig(c)
}

// Damit er während der Pause wirklich wegschauen kann, statt auf den Countdown
// zu starren.
func (a *App) BreakOver() {
	a.enqueueAttentionEvent(breakFinishedAttentionEvent(time.Now()))
}

// BuildInfo liefert den Zeitpunkt, zu dem die laufende Binary gebaut wurde —
// damit erkennbar ist, ob die App noch aus einem älteren Build stammt.
func (a *App) BuildInfo() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	info, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return info.ModTime().Format("02.01.2006, 15:04")
}

func (a *App) Zeitgeist() core.ZgInfo {
	return core.ZeitgeistInfo()
}

func (a *App) ZeitgeistStart(ref string) (core.ZgProject, error) {
	return core.ZeitgeistStart(ref)
}

func (a *App) ZeitgeistPause() error {
	return core.ZeitgeistPause()
}

func (a *App) ZeitgeistResume() error {
	return core.ZeitgeistResume()
}

func (a *App) ZeitgeistStop(note string) (core.ZgStopped, error) {
	return core.ZeitgeistStop(note)
}

func (a *App) NewSession(project string, worktree bool, name string) (string, error) {
	st, err := core.LoadState()
	if err != nil {
		return "", err
	}
	return core.CreateAgentSession(st, project, worktree, name)
}

func (a *App) NewTermSession(project string, worktree bool, name string) (string, error) {
	st, err := core.LoadState()
	if err != nil {
		return "", err
	}
	return core.CreateTermSession(st, project, worktree, name)
}

func (a *App) NewDockSession(project string) (string, error) {
	st, err := core.LoadState()
	if err != nil {
		return "", err
	}
	return core.CreateDockSession(st, project)
}

func (a *App) NewTermSessionFor(sessionID string) (string, error) {
	st, session, err := loadSessionByID(sessionID)
	if err != nil {
		return "", err
	}
	return core.CreateTermSessionForID(st, session.ID, "")
}

func (a *App) DoneAgent(sessionID string) error {
	return core.DoneSession(core.SessionID(strings.TrimSpace(sessionID)))
}

func (a *App) HandoffSession(sourceID, targetID string) error {
	st, err := core.LoadState()
	if err != nil {
		return err
	}
	snapshot := a.observationFor(st.Agents, true)
	return core.HandoffSession(st, snapshot, core.SessionID(sourceID), core.SessionID(targetID))
}

func (a *App) SendSkill(sessionID, cmd string) error {
	if !strings.HasPrefix(cmd, "/") {
		return fmt.Errorf("nur Slash-Kommandos erlaubt")
	}
	id := core.SessionID(strings.TrimSpace(sessionID))
	if err := core.SendSkillByID(id, cmd); err != nil {
		return err
	}
	if strings.Contains(cmd, "/deploy") {
		if st, err := core.LoadState(); err == nil {
			if session := st.SessionByID(id); session != nil {
				_, _ = core.OpenRegistry(core.StatePath()).Change(a.ctx, core.MarkSessionDeploy(session.ID, session.Name, time.Now()))
			}
		}
	}
	return nil
}

func (a *App) Cleanup(project, reference string) (string, error) {
	st, target, err := resolveWorktreeTarget(a.ctx, project, reference)
	if err != nil {
		return "", err
	}
	if target.Worktree.Main {
		return "", fmt.Errorf("Cleanup ist nur für verwaltete Worktrees verfügbar")
	}
	if !target.MainBranch.Known() || strings.TrimSpace(target.MainBranch.Value) == "" {
		return "", fmt.Errorf("Hauptbranch ist derzeit nicht verlässlich bekannt")
	}
	name, err := core.StartCleanup(st, target.Worktree.Path, target.MainBranch.Value)
	if err != nil {
		return "", err
	}
	return name, nil
}

func (a *App) Merge(project, source, target string) (string, error) {
	st, err := core.LoadState()
	if err != nil {
		return "", err
	}
	proj := st.ProjectByName(project)
	if proj == nil {
		return "", fmt.Errorf("unbekanntes Projekt: %s", project)
	}
	name, err := core.StartMerge(st, proj.Path, source, target)
	if err != nil {
		return "", err
	}
	return name, nil
}

func (a *App) Deploy(project string) (string, error) {
	st, err := core.LoadState()
	if err != nil {
		return "", err
	}
	proj := st.ProjectByName(project)
	if proj == nil {
		return "", fmt.Errorf("unbekanntes Projekt: %s", project)
	}
	name, err := core.StartDeploy(st, proj.Path)
	if err != nil {
		return "", err
	}
	return name, nil
}

func (a *App) RemoveWorktree(project, reference string) error {
	st, target, err := resolveWorktreeTarget(a.ctx, project, reference)
	if err != nil {
		return err
	}
	if err := core.RemoveWorktree(st, &target.Project, target.Worktree.Path); err != nil {
		return err
	}
	return nil
}

func (a *App) SetMainBranch(projectID, main string) error {
	_, project, err := loadProjectByID(projectID)
	if err != nil {
		return err
	}
	_, err = core.OpenRegistry(core.StatePath()).Change(a.ctx, core.SetProjectMainBranch(project.ID, project.Name, strings.TrimSpace(main)))
	return err
}

func (a *App) KillSession(sessionID, legacyDockName string) error {
	st, session, err := loadSessionTarget(sessionID, legacyDockName)
	if err != nil {
		return err
	}
	a.CloseTerm(session.Name)
	return core.RemoveRegisteredSession(st, session.Name)
}

func (a *App) LaterSession(sessionID string) error {
	st, session, err := loadSessionByID(sessionID)
	if err != nil {
		return err
	}
	a.CloseTerm(session.Name)
	return core.ParkSession(st, session.Name)
}

func (a *App) ReopenSession(sessionID string) error {
	st, session, err := loadSessionByID(sessionID)
	if err != nil {
		return err
	}
	return core.ReopenLater(st, session.Name)
}

func (a *App) OpenTerm(sessionID, legacyDockName string, cols, rows int) error {
	_, registered, err := loadSessionTarget(sessionID, legacyDockName)
	if err != nil {
		return err
	}
	name := registered.Name
	session := registered.TmuxName()
	if !core.TmuxHasSession(session) {
		return fmt.Errorf("Session %q existiert nicht mehr", name)
	}
	a.mu.Lock()
	if _, ok := a.terms[name]; ok {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	if cols < 20 || cols > 999 {
		cols = 220
	}
	if rows < 5 || rows > 999 {
		rows = 50
	}
	core.Tmux("set-option", "-t", session, "status", "off")
	core.Tmux("set-option", "-w", "-t", session+":", "window-size", "latest")
	cmd := exec.Command("tmux", "attach-session", "-t", core.TargetSession(session))
	cmd.Env = append(core.EnvWithout("TMUX"), "TERM=xterm-256color")
	startTerm := pty.StartWithSize
	if a.startTerm != nil {
		startTerm = a.startTerm
	}
	ptmx, err := startTerm(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return fmt.Errorf("tmux attach: %w", err)
	}
	t := &ptyTerm{ptmx: ptmx, cmd: cmd}
	a.mu.Lock()
	a.terms[name] = t
	a.mu.Unlock()

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				runtime.EventsEmit(a.ctx, "term:data:"+name, base64.StdEncoding.EncodeToString(buf[:n]))
			}
			if err != nil {
				break
			}
		}
		a.mu.Lock()
		if a.terms[name] == t {
			delete(a.terms, name)
		}
		a.mu.Unlock()
		t.close()
		runtime.EventsEmit(a.ctx, "term:closed:"+name)
	}()
	return nil
}

func (a *App) WriteTerm(name, dataB64 string) {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil || len(data) == 0 {
		return
	}
	a.mu.Lock()
	t := a.terms[name]
	a.mu.Unlock()
	if t != nil {
		t.ptmx.Write(data)
	}
}

func (a *App) ResizeTerm(name string, cols, rows int) {
	if cols < 1 || rows < 1 || cols > 999 || rows > 999 {
		return
	}
	a.mu.Lock()
	t := a.terms[name]
	a.mu.Unlock()
	if t != nil {
		pty.Setsize(t.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	}
}

func (a *App) CloseTerm(name string) {
	a.mu.Lock()
	t := a.terms[name]
	delete(a.terms, name)
	a.mu.Unlock()
	if t != nil {
		t.close()
	}
}

func (t *ptyTerm) close() {
	t.ptmx.Close()
	if t.cmd.Process != nil {
		t.cmd.Process.Kill()
	}
	go t.cmd.Wait()
}
