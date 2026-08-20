package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSessionRejectsStaleProjectIDBeforeExternalCommands(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	t.Setenv("MAGENTIC_STATE", statePath)
	registry := OpenRegistry(statePath)
	replacement := Project{ID: "project-current", Name: "reused", Path: filepath.Join(root, "current")}
	if _, err := registry.Change(context.Background(), RegisterProject(replacement)); err != nil {
		t.Fatal(err)
	}
	stale := State{Projects: []Project{{ID: "project-stale", Name: replacement.Name, Path: filepath.Join(root, "removed")}}}

	bin := t.TempDir()
	marker := filepath.Join(root, "external-command")
	script := "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"$MAGENTIC_EXTERNAL_COMMAND_MARKER\"\nexit 99\n"
	for _, command := range []string{"git", "tmux"} {
		if err := os.WriteFile(filepath.Join(bin, command), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MAGENTIC_EXTERNAL_COMMAND_MARKER", marker)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := CreateAgentSession(&stale, "project-stale", true, "topic")
	if err == nil || !strings.Contains(err.Error(), "ProjectID") {
		t.Fatalf("stale ProjectID resolved through reused name: %v", err)
	}
	_, err = StartSkillAgent(&stale, "project-stale", "/removed", "/deploy ", "deploy", "deploy reused")
	if err == nil || !strings.Contains(err.Error(), "ProjectID") {
		t.Fatalf("stale skill ProjectID resolved through reused name: %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("stale ProjectID crossed an external Adapter: %v", statErr)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.State(); len(got.Agents) != 0 || len(got.Projects) != 1 || got.Projects[0].ID != replacement.ID {
		t.Fatalf("stale ProjectID mutated Registry: %#v", got)
	}
}

func TestCreateTermSessionForIDReloadsRegistryBeforeResolvingSource(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", statePath)
	registry := OpenRegistry(statePath)
	project := Project{ID: "project-current", Name: "project", Path: t.TempDir()}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	replacement := Session{
		ID: "session-current", Name: "reused", RuntimeName: "opaque-current",
		ProjectID: project.ID, Project: project.Name, Dir: project.Path,
	}
	if _, err := registry.Change(context.Background(), RegisterSession(replacement)); err != nil {
		t.Fatal(err)
	}
	stale := State{
		Projects: []Project{project},
		Agents: []Session{{
			ID: "session-stale", Name: replacement.Name, RuntimeName: "opaque-stale",
			ProjectID: project.ID, Project: project.Name, Dir: filepath.Join(project.Path, "removed"),
		}},
	}

	_, err := CreateTermSessionForID(&stale, "session-stale", "terminal")
	if err == nil || !strings.Contains(err.Error(), "SessionID") {
		t.Fatalf("stale SessionID resolved through reused name: %v", err)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.State().Agents) != 1 || snapshot.State().Agents[0].ID != replacement.ID {
		t.Fatalf("stale terminal action changed current Registry: %#v", snapshot.State().Agents)
	}
}
