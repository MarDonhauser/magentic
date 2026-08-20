package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"magentic/core"

	"github.com/creack/pty"
)

func registerFacadeIdentityFixture(t *testing.T, project core.Project, session *core.Session) (*core.Registry, string) {
	t.Helper()
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	t.Setenv("MAGENTIC_STATE", statePath)
	t.Setenv("MAGENTIC_LIFECYCLE", filepath.Join(root, "lifecycle.json"))
	registry := core.OpenRegistry(statePath)
	if _, err := registry.Change(context.Background(), core.RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	if session != nil {
		if _, err := registry.Change(context.Background(), core.RegisterSession(*session)); err != nil {
			t.Fatal(err)
		}
	}
	return registry, statePath
}

func TestSessionFacadeRejectsStaleIDWhenNameIsReused(t *testing.T) {
	actions := []struct {
		name string
		call func(*App, string) error
	}{
		{name: "done", call: func(app *App, id string) error { return app.DoneAgent(id) }},
		{name: "send skill", call: func(app *App, id string) error { return app.SendSkill(id, "/review ") }},
		{name: "new terminal for", call: func(app *App, id string) error {
			_, err := app.NewTermSessionFor(id)
			return err
		}},
		{name: "open", call: func(app *App, id string) error { return app.OpenTerm(id, "reused", 120, 40) }},
		{name: "kill", call: func(app *App, id string) error { return app.KillSession(id, "reused") }},
		{name: "later", call: func(app *App, id string) error { return app.LaterSession(id) }},
		{name: "reopen", call: func(app *App, id string) error { return app.ReopenSession(id) }},
		{name: "mark seen", call: func(app *App, id string) error { return app.MarkSeen(id) }},
	}

	for _, action := range actions {
		t.Run(action.name, func(t *testing.T) {
			logPath := installHandoffFakeTmux(t, "Ready\nshift+tab to cycle", "claude", "claude")
			project := core.Project{ID: "project-current", Name: "project", Path: t.TempDir(), MainBranch: "main"}
			laterAt := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
			replacement := core.Session{
				ID: "session-current", Name: "reused", RuntimeName: handoffSourceRuntime,
				ProjectID: project.ID, Project: project.Name, Dir: project.Path, LaterAt: laterAt,
				AgentRuns: []core.AgentRunRef{{Vendor: core.AgentVendorClaude, ExternalID: "current-run"}},
			}
			registry, _ := registerFacadeIdentityFixture(t, project, &replacement)
			before, err := registry.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ptyStarts := 0
			app := NewApp()
			app.ctx = context.Background()
			app.startTerm = func(*exec.Cmd, *pty.Winsize) (*os.File, error) {
				ptyStarts++
				return nil, errors.New("unexpected PTY start")
			}

			err = action.call(app, "session-stale")
			if err == nil || !strings.Contains(err.Error(), "SessionID") {
				t.Fatalf("stale SessionID action error = %v", err)
			}
			after, snapshotErr := registry.Snapshot(context.Background())
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			state := after.State()
			current := state.SessionByID(replacement.ID)
			if after.Revision() != before.Revision() || current == nil || current.Name != replacement.Name ||
				current.RuntimeName != replacement.RuntimeName || !current.LaterAt.Equal(laterAt) {
				t.Fatalf("stale action changed replacement Session: revision %d -> %d, Session=%+v", before.Revision(), after.Revision(), current)
			}
			if calls := parseFakeTmuxCalls(t, logPath); len(calls) != 0 || ptyStarts != 0 {
				t.Fatalf("stale action crossed tmux/PTY Seam: calls=%#v starts=%d", calls, ptyStarts)
			}
		})
	}
}

func TestSessionReadsRejectStaleIDWhenNameIsReused(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "replacement content https://replacement.test", "claude", "claude")
	project := core.Project{ID: "project-current", Name: "project", Path: t.TempDir(), MainBranch: "main"}
	replacement := core.Session{
		ID: "session-current", Name: "reused", RuntimeName: handoffSourceRuntime,
		ProjectID: project.ID, Project: project.Name, Dir: project.Path,
		AgentRuns: []core.AgentRunRef{{Vendor: core.AgentVendorClaude, ExternalID: "current-run"}},
	}
	registerFacadeIdentityFixture(t, project, &replacement)
	app := NewApp()

	if preview := app.SessionPreview("session-stale"); preview.ContentKnown || preview.Content != "" || preview.Source.State != string(core.HistorySourceUnavailable) {
		t.Fatalf("stale SessionPreview exposed replacement content: %#v", preview)
	}
	if links, err := app.SessionLinks("session-stale"); err == nil || !strings.Contains(err.Error(), "SessionID") || len(links.Links) != 0 {
		t.Fatalf("stale SessionLinks = %#v, %v", links, err)
	}
	if calls := parseFakeTmuxCalls(t, logPath); len(calls) != 0 {
		t.Fatalf("stale Session read crossed tmux Seam: %#v", calls)
	}
}

func TestOpenTermDoesNotReuseConnectionAfterNameReuse(t *testing.T) {
	installHandoffFakeTmux(t, "Ready\nshift+tab to cycle", "claude", "claude")
	project := core.Project{ID: "project-current", Name: "project", Path: t.TempDir(), MainBranch: "main"}
	replacement := core.Session{
		ID: "session-current", Name: "reused", RuntimeName: "opaque-current-runtime",
		ProjectID: project.ID, Project: project.Name, Dir: project.Path,
	}
	registerFacadeIdentityFixture(t, project, &replacement)

	oldConnection := &ptyTerm{}
	app := NewApp()
	app.ctx = context.Background()
	app.terms[sessionTermKey("session-old")] = oldConnection
	starts := 0
	var attachedRuntime string
	app.startTerm = func(command *exec.Cmd, _ *pty.Winsize) (*os.File, error) {
		starts++
		attachedRuntime = strings.Join(command.Args, " ")
		return nil, errors.New("injected PTY stop")
	}

	err := app.OpenTerm(string(replacement.ID), replacement.Name, 120, 40)
	if err == nil || !strings.Contains(err.Error(), "injected PTY stop") {
		t.Fatalf("OpenTerm() error = %v, want injected PTY stop", err)
	}
	if starts != 1 {
		t.Fatalf("PTY starts = %d, want a fresh connection for replacement SessionID", starts)
	}
	if !strings.Contains(attachedRuntime, core.TargetSession(replacement.RuntimeName)) {
		t.Fatalf("attach command = %q, want replacement RuntimeName %q", attachedRuntime, replacement.RuntimeName)
	}
	if app.terms[sessionTermKey("session-old")] != oldConnection {
		t.Fatal("opening replacement Session changed the old SessionID connection")
	}
}

func TestProjectFacadeRejectsStaleIDWhenNameIsReused(t *testing.T) {
	actions := []struct {
		name string
		call func(*App, string) error
	}{
		{name: "new session", call: func(app *App, id string) error { _, err := app.NewSession(id, false, ""); return err }},
		{name: "new terminal", call: func(app *App, id string) error { _, err := app.NewTermSession(id, false, ""); return err }},
		{name: "new dock", call: func(app *App, id string) error { _, err := app.NewDockSession(id); return err }},
		{name: "git graph", call: func(app *App, id string) error { _, err := app.GitGraph(id, 10); return err }},
		{name: "board", call: func(app *App, id string) error { _, err := app.Board(id); return err }},
		{name: "board archive", call: func(app *App, id string) error { _, err := app.BoardArchive(id, 10); return err }},
		{name: "start board item", call: func(app *App, id string) error { _, err := app.StartBoardItem(id, "stale-token"); return err }},
		{name: "cleanup", call: func(app *App, id string) error { _, err := app.Cleanup(id, "stale-worktree"); return err }},
		{name: "merge", call: func(app *App, id string) error { _, err := app.Merge(id, "feature", "main"); return err }},
		{name: "deploy", call: func(app *App, id string) error { _, err := app.Deploy(id); return err }},
		{name: "remove worktree", call: func(app *App, id string) error { return app.RemoveWorktree(id, "stale-worktree") }},
		{name: "worktree diff", call: func(app *App, id string) error { _, err := app.WorktreeDiff(id, "stale-worktree"); return err }},
		{name: "set main branch", call: func(app *App, id string) error { return app.SetMainBranch(id, "trunk") }},
		{name: "remove project", call: func(app *App, id string) error { return app.RemoveProject(id) }},
		{name: "reorder projects", call: func(app *App, id string) error { return app.ReorderProjects([]string{"project-other", id}) }},
	}

	for _, action := range actions {
		t.Run(action.name, func(t *testing.T) {
			projectPath := t.TempDir()
			replacement := core.Project{ID: "project-current", Name: "reused", Path: projectPath, MainBranch: "main"}
			registry, _ := registerFacadeIdentityFixture(t, replacement, nil)
			if _, err := registry.Change(context.Background(), core.RegisterProject(core.Project{
				ID: "project-other", Name: "other", Path: t.TempDir(), MainBranch: "main",
			})); err != nil {
				t.Fatal(err)
			}
			before, err := registry.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			app := NewApp()
			app.ctx = context.Background()

			err = action.call(app, "project-stale")
			if err == nil || !strings.Contains(err.Error(), "ProjectID") {
				t.Fatalf("stale ProjectID action error = %v", err)
			}
			after, snapshotErr := registry.Snapshot(context.Background())
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			state := after.State()
			current := state.ProjectByID(replacement.ID)
			if after.Revision() != before.Revision() || current == nil || current.Name != replacement.Name || current.MainBranch != "main" ||
				len(state.Projects) != 2 || state.Projects[0].ID != replacement.ID {
				t.Fatalf("stale action changed replacement Project: revision %d -> %d, Projects=%+v", before.Revision(), after.Revision(), state.Projects)
			}
		})
	}
}

func TestMigrateDockSessionsReturnsStableDockIdentitiesOnly(t *testing.T) {
	project := core.Project{ID: "project-current", Name: "project", Path: t.TempDir(), MainBranch: "main"}
	dock := core.Session{
		ID: "session-dock", Name: "term project", RuntimeName: "opaque-dock-runtime",
		ProjectID: project.ID, Project: project.Name, Dir: project.Path,
		SessionKind: core.SessionKindTerminal, Presentation: core.SessionPresentationDock,
	}
	registry, _ := registerFacadeIdentityFixture(t, project, &dock)
	listed := core.Session{
		ID: "session-listed", Name: "listed terminal", RuntimeName: "opaque-listed-runtime",
		ProjectID: project.ID, Project: project.Name, Dir: project.Path,
		SessionKind: core.SessionKindTerminal, Presentation: core.SessionPresentationListed,
	}
	if _, err := registry.Change(context.Background(), core.RegisterSession(listed)); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	refs, err := app.MigrateDockSessions([]string{dock.Name, listed.Name, "missing", dock.Name})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != dock.ID || refs[0].Name != dock.Name {
		t.Fatalf("MigrateDockSessions() = %#v, want stable Dock identity only", refs)
	}
}
