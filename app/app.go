package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"magentic/core"

	"github.com/creack/pty"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx   context.Context
	mu    sync.Mutex
	terms map[string]*ptyTerm
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
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for name, t := range a.terms {
		t.close()
		delete(a.terms, name)
	}
}

func (a *App) Overview() (core.Overview, error) {
	st, err := core.LoadState()
	if err != nil {
		return core.Overview{}, err
	}
	return core.BuildOverview(st), nil
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
	return core.SendSkill(name, cmd)
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
	if !core.TmuxHasSession(sn) {
		return fmt.Errorf("Session läuft nicht")
	}
	a.CloseTerm(name)
	if err := core.TmuxKillSession(sn); err != nil {
		return err
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
