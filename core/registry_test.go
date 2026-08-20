package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRegistryMigratesStableIdentitiesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	legacy := []byte(`{
  "projects": [{"name":"magentic","path":"/workspace/magentic"}],
  "agents": [{"name":"hera","project":"magentic","dir":"/workspace/magentic","session_id":"claude-run"}]
}
`)
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	registry := OpenRegistry(path)
	first, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := first.State()
	if state.Schema != registrySchemaVersion || state.Revision == 0 {
		t.Fatalf("legacy Registry was not migrated: schema=%d revision=%d", state.Schema, state.Revision)
	}
	if len(state.Projects) != 1 || state.Projects[0].ID == "" {
		t.Fatalf("ProjectID missing: %+v", state.Projects)
	}
	if len(state.Agents) != 1 || state.Agents[0].ID == "" || state.Agents[0].ProjectID != state.Projects[0].ID {
		t.Fatalf("stable Session association missing: %+v", state.Agents)
	}
	session := state.Agents[0]
	if session.RuntimeName != SessionName("hera") || session.SessionKind != SessionKindCodingAgent || session.Purpose != SessionPurposeWork {
		t.Fatalf("legacy overloaded fields were not normalized: %+v", session)
	}
	if run, ok := session.AgentRun(AgentVendorClaude); !ok || run.ExternalID != "claude-run" {
		t.Fatalf("legacy provider ID was not qualified: %+v", session.AgentRuns)
	}
	backup, err := os.ReadFile(path + ".pre-registry-v2.bak")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, legacy) {
		t.Fatal("migration backup does not preserve the exact legacy bytes")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("Registry permissions=%v err=%v", info, err)
	}

	second, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	again := second.State()
	if again.Projects[0].ID != state.Projects[0].ID || again.Agents[0].ID != session.ID || second.Revision() != first.Revision() {
		t.Fatalf("migration is not stable: first=%+v second=%+v", state, again)
	}
}

func TestRegistrySemanticChangesPreserveUnrelatedConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	registryA := OpenRegistry(path)
	registryB := OpenRegistry(path)
	project := Project{ID: ProjectID("project-1"), Name: "project", Path: "/workspace/project"}
	if _, err := registryA.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	sessions := []Session{
		{ID: SessionID("session-a"), Name: "hera", ProjectID: project.ID, Project: project.Name, Dir: project.Path},
		{ID: SessionID("session-b"), Name: "atlas", ProjectID: project.ID, Project: project.Name, Dir: project.Path},
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(sessions))
	for i, session := range sessions {
		wg.Add(1)
		go func(i int, session Session) {
			defer wg.Done()
			registry := registryA
			if i%2 == 1 {
				registry = registryB
			}
			_, err := registry.Change(context.Background(), RegisterSession(session))
			errs <- err
		}(i, session)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := registryA.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.State().Agents) != 2 {
		t.Fatalf("unrelated semantic changes were lost: %+v", snapshot.State().Agents)
	}
}

func TestRegistryMutableCompatibilityMergesDifferentFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	registry := OpenRegistry(path)
	project := Project{ID: ProjectID("project-1"), Name: "project", Path: "/workspace/project"}
	session := Session{ID: SessionID("session-1"), Name: "hera", ProjectID: project.ID, Project: project.Name, Dir: project.Path}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}
	one, _ := registry.Snapshot(context.Background())
	two, _ := registry.Snapshot(context.Background())
	first := one.MutableState()
	second := two.MutableState()
	first.Agents[0].SeenAt = time.Unix(100, 0)
	second.Agents[0].LaterAt = time.Unix(200, 0)
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}
	if err := second.Save(); err != nil {
		t.Fatal(err)
	}
	latest, _ := registry.Snapshot(context.Background())
	got := latest.State().Agents[0]
	if got.SeenAt.Unix() != 100 || got.LaterAt.Unix() != 200 {
		t.Fatalf("field-level compatibility merge lost an update: %+v", got)
	}
}

func TestRegistryCoordinatesIndependentProcesses(t *testing.T) {
	if os.Getenv("MAGENTIC_REGISTRY_HELPER") != "" {
		path := os.Getenv("MAGENTIC_REGISTRY_HELPER")
		name := os.Getenv("MAGENTIC_REGISTRY_PROJECT")
		_, err := OpenRegistry(path).Change(context.Background(), RegisterProject(Project{
			Name: name, Path: filepath.Join("/workspace", name),
		}))
		if err != nil {
			os.Exit(2)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "state.json")
	type helperProcess struct {
		cmd *exec.Cmd
		out bytes.Buffer
	}
	commands := make([]*helperProcess, 0, 2)
	for _, name := range []string{"alpha", "beta"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRegistryCoordinatesIndependentProcesses$")
		cmd.Env = append(os.Environ(), "MAGENTIC_REGISTRY_HELPER="+path, "MAGENTIC_REGISTRY_PROJECT="+name)
		process := &helperProcess{cmd: cmd}
		cmd.Stdout = &process.out
		cmd.Stderr = &process.out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, process)
	}
	for _, process := range commands {
		if err := process.cmd.Wait(); err != nil {
			t.Fatalf("Registry helper failed: %v\n%s", err, process.out.String())
		}
	}
	snapshot, err := OpenRegistry(path).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.State().Projects) != 2 {
		data, _ := json.Marshal(snapshot.State())
		t.Fatalf("interprocess change was lost: %s", data)
	}
}
