package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestRegistryBaselineReopenPreservesConcurrentSessionChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	registryA := OpenRegistry(path)
	registryB := OpenRegistry(path)
	laterAt := time.Unix(100, 0)
	seenAt := time.Unix(200, 0)
	session := Session{ID: "session-1", Name: "legacy", Dir: "/workspace/project", LaterAt: laterAt}
	if _, err := registryA.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}

	errs := make(chan error, 2)
	go func() {
		_, err := registryA.Change(context.Background(), ReopenRegisteredSessionWithBaseline(
			session.ID, session.Name, "abc123", []string{"dirty.txt"},
		))
		errs <- err
	}()
	go func() {
		_, err := registryB.Change(context.Background(), MarkSessionSeen(session.ID, session.Name, seenAt))
		errs <- err
	}()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := registryA.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	got := state.SessionByID(session.ID)
	if got == nil || got.BaseCommit != "abc123" || !equalStringSlice(got.BaseDirty, []string{"dirty.txt"}) ||
		!got.LaterAt.IsZero() || !got.SeenAt.Equal(seenAt) {
		t.Fatalf("atomic baseline reopen lost concurrent Registry truth: %+v", got)
	}
}

func TestRegistryBaselineReopenRejectsConflictingOverwriteAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	registry := OpenRegistry(path)
	laterAt := time.Unix(100, 0)
	session := Session{
		ID: "session-1", Name: "known", Dir: "/workspace/project", LaterAt: laterAt,
		BaseCommit: "original", BaseDirty: []string{"original.txt"},
	}
	if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}

	_, err := registry.Change(context.Background(), ReopenRegisteredSessionWithBaseline(
		session.ID, session.Name, "different", []string{"different.txt"},
	))
	if !errors.Is(err, ErrRegistryConflict) {
		t.Fatalf("conflicting baseline error = %v, want ErrRegistryConflict", err)
	}
	snapshot, snapshotErr := registry.Snapshot(context.Background())
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	state := snapshot.State()
	got := state.SessionByID(session.ID)
	if got == nil || got.BaseCommit != session.BaseCommit || !equalStringSlice(got.BaseDirty, session.BaseDirty) ||
		!got.LaterAt.Equal(laterAt) {
		t.Fatalf("conflicting baseline partially reopened Session: %+v", got)
	}

	result, err := registry.Change(context.Background(), ReopenRegisteredSessionWithBaseline(
		session.ID, session.Name, session.BaseCommit, session.BaseDirty,
	))
	if err != nil || !result.Applied {
		t.Fatalf("matching baseline reopen = %+v, %v", result, err)
	}
	retry, err := registry.Change(context.Background(), ReopenRegisteredSessionWithBaseline(
		session.ID, session.Name, session.BaseCommit, session.BaseDirty,
	))
	if err != nil || retry.Applied || retry.Revision != result.Revision {
		t.Fatalf("idempotent baseline reopen retry = %+v, %v", retry, err)
	}
}

func TestRegistryRenameRecordsExplicitCustomRuntimeTransition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	registry := OpenRegistry(path)
	session := Session{ID: "session-1", Name: "display", RuntimeName: "custom-runtime", Dir: "/workspace"}
	if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Change(context.Background(), RenameRegisteredSessionRuntime(
		session.ID, session.Name, "renamed", "mgt-renamed",
	)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := snapshot.State().Agents[0]
	if got.Name != "renamed" || got.RuntimeName != "mgt-renamed" || got.TmuxName() != "mgt-renamed" {
		t.Fatalf("runtime rename was not retained: %#v", got)
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
