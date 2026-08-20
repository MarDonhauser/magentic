package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

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
