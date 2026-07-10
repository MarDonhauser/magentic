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
	ctx           context.Context
	mu            sync.Mutex
	terms         map[string]*ptyTerm
	activeTerm    string
	dsMu          sync.Mutex
	dsPrev        *DeployStatus
	statusMu      sync.Mutex
	statusCache   map[string]core.AgentStatus
	contentCache  map[string]string
	activityCache map[string]time.Time
	statusAt      time.Time
}

type ptyTerm struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

func NewApp() *App {
	return &App{terms: map[string]*ptyTerm{}}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	installNativeNotifier()
	runtime.OnFileDrop(ctx, a.onFileDrop)
	if st, err := core.LoadState(); err == nil {
		if n := core.RestoreSessions(st); n > 0 {
			word := "Sessions"
			if n == 1 {
				word = "Session"
			}
			core.NotifyDesktop("magentic", fmt.Sprintf("%d %s wiederhergestellt", n, word), "")
		}
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
	for _, ag := range core.DiscoverNew(st) {
		st.AddAgent(ag)
	}
	var statuses map[string]core.AgentStatus
	var contents map[string]string
	var activity map[string]time.Time
	if fresh {
		core.FlushGitMemo()
		statuses, contents, activity = core.CollectStatuses(st.Agents)
		a.storeStatuses(statuses, contents, activity)
	} else {
		statuses, contents, activity = a.statusesFor(st.Agents)
	}
	return core.BuildOverviewFrom(st, statuses, contents, activity), nil
}

type TodoInfo struct {
	Index   int    `json:"index"`
	Text    string `json:"text"`
	Project string `json:"project"`
	Age     string `json:"age"`
}

func (a *App) Todos() ([]TodoInfo, error) {
	st, err := core.LoadState()
	if err != nil {
		return nil, err
	}
	out := []TodoInfo{}
	for i, t := range st.Todos {
		out = append(out, TodoInfo{Index: i, Text: t.Text, Project: t.Project, Age: core.FormatAgeWord(t.CreatedAt)})
	}
	return out, nil
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
	st, err := core.LoadState()
	if err != nil {
		return "", err
	}
	name := filepath.Base(abs)
	if st.ProjectByName(name) != nil {
		return "", fmt.Errorf("Projekt %q existiert schon", name)
	}
	st.Projects = append(st.Projects, core.Project{Name: name, Path: abs})
	if err := st.Save(); err != nil {
		return "", err
	}
	return name, nil
}

func (a *App) RemoveProject(name string) error {
	st, err := core.LoadState()
	if err != nil {
		return err
	}
	kept := st.Projects[:0]
	found := false
	for _, p := range st.Projects {
		if p.Name == name {
			found = true
			continue
		}
		kept = append(kept, p)
	}
	if !found {
		return fmt.Errorf("Projekt %q nicht gefunden", name)
	}
	st.Projects = kept
	return st.Save()
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

func (a *App) AddTodo(text, project string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("leeres Todo")
	}
	st, err := core.LoadState()
	if err != nil {
		return err
	}
	st.Todos = append(st.Todos, core.Todo{Text: text, Project: project, CreatedAt: time.Now()})
	return st.Save()
}

func (a *App) UpdateTodo(idx int, text, project string) error {
	st, err := core.LoadState()
	if err != nil {
		return err
	}
	if idx < 0 || idx >= len(st.Todos) {
		return fmt.Errorf("Todo nicht gefunden")
	}
	st.Todos[idx].Text = strings.TrimSpace(text)
	st.Todos[idx].Project = project
	return st.Save()
}

func (a *App) DeleteTodo(idx int) error {
	st, err := core.LoadState()
	if err != nil {
		return err
	}
	if idx < 0 || idx >= len(st.Todos) {
		return fmt.Errorf("Todo nicht gefunden")
	}
	st.Todos = append(st.Todos[:idx], st.Todos[idx+1:]...)
	return st.Save()
}

func (a *App) StartTodoSession(idx int) (string, error) {
	st, err := core.LoadState()
	if err != nil {
		return "", err
	}
	return core.StartTodoSession(st, idx)
}

func (a *App) NewSession(project string, worktree bool, name string) (string, error) {
	st, err := core.LoadState()
	if err != nil {
		return "", err
	}
	return core.CreateAgentSession(st, project, worktree, name)
}

func (a *App) DoneAgent(name string) error {
	return core.DoneAgent(name)
}

func (a *App) SendSkill(name, cmd string) error {
	if !strings.HasPrefix(cmd, "/") {
		return fmt.Errorf("nur Slash-Kommandos erlaubt")
	}
	if err := core.SendSkill(name, cmd); err != nil {
		return err
	}
	if strings.Contains(cmd, "/deploy") {
		if st, err := core.LoadState(); err == nil {
			st.MarkDeploy(name)
			st.Save()
		}
	}
	return nil
}

func (a *App) Cleanup(path, mainBranch string) (string, error) {
	st, err := core.LoadState()
	if err != nil {
		return "", err
	}
	name, err := core.StartCleanup(st, path, mainBranch)
	if err != nil {
		return "", err
	}
	st.Save()
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
	st.Save()
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
	st.Save()
	return name, nil
}

func (a *App) RemoveWorktree(project, path string) error {
	st, err := core.LoadState()
	if err != nil {
		return err
	}
	proj := st.ProjectByName(project)
	if proj == nil {
		return fmt.Errorf("unbekanntes Projekt: %s", project)
	}
	if err := core.RemoveWorktree(st, proj, path); err != nil {
		return err
	}
	return st.Save()
}

func (a *App) SetMainBranch(project, main string) error {
	st, err := core.LoadState()
	if err != nil {
		return err
	}
	proj := st.ProjectByName(project)
	if proj == nil {
		return fmt.Errorf("unbekanntes Projekt: %s", project)
	}
	proj.MainBranch = strings.TrimSpace(main)
	return st.Save()
}

func (a *App) KillSession(name string) error {
	sn := core.SessionName(name)
	a.CloseTerm(name)
	if core.TmuxHasSession(sn) {
		if err := core.TmuxKillSession(sn); err != nil {
			return err
		}
	}
	if st, err := core.LoadState(); err == nil {
		st.RemoveAgent(name)
		st.Save()
	}
	return nil
}

func (a *App) OpenTerm(name string, cols, rows int) error {
	session := core.SessionName(name)
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
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
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
