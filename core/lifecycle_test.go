package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeLifecycleRuntime struct {
	present      map[SessionID]bool
	startErr     error
	stopErr      error
	deliverCalls int
	onStart      func(Session)
	onStop       func(Session)
}

func (f *fakeLifecycleRuntime) Exists(_ context.Context, session Session) (bool, error) {
	return f.present[session.ID], nil
}

func (f *fakeLifecycleRuntime) Start(_ context.Context, session Session, _ string) error {
	if f.onStart != nil {
		f.onStart(session)
	}
	if f.startErr != nil {
		return f.startErr
	}
	f.present[session.ID] = true
	return nil
}

func (f *fakeLifecycleRuntime) Stop(_ context.Context, session Session) error {
	if f.onStop != nil {
		f.onStop(session)
	}
	if f.stopErr != nil {
		return f.stopErr
	}
	delete(f.present, session.ID)
	return nil
}

func (f *fakeLifecycleRuntime) DeliverInitial(_ context.Context, _ Session, _ string) (bool, error) {
	f.deliverCalls++
	return false, nil
}

type fakeLifecycleRepositories struct {
	worktreePath string
}

func (f fakeLifecycleRepositories) Change(_ context.Context, change ManagedWorktreeChange) (ManagedWorktreeChangeResult, error) {
	return ManagedWorktreeChangeResult{
		Kind: change.Kind, Project: change.Project.Name, Path: f.worktreePath,
		Branch: "agent/" + change.Name, State: RepositoryKnown, Changed: true,
	}, nil
}

func (fakeLifecycleRepositories) Inspect(_ context.Context, request RepositoryInspectRequest) (RepositoryInspection, error) {
	return RepositoryInspection{
		Directory: request.Directory, Presence: RepositoryKnown,
		Baseline: repositoryKnownFact(RepositoryBaseline{Directory: request.Directory, Head: "abc123"}),
	}, nil
}

func lifecycleHarness(t *testing.T) (*SessionLifecycle, *fakeLifecycleRuntime, *Registry, string) {
	t.Helper()
	dir := t.TempDir()
	registry := OpenRegistry(filepath.Join(dir, "state.json"))
	runtime := &fakeLifecycleRuntime{present: map[SessionID]bool{}}
	ledgerPath := filepath.Join(dir, "lifecycle.json")
	lifecycle := newSessionLifecycle(registry, runtime, fakeLifecycleRepositories{worktreePath: filepath.Join(dir, "project-agents", "hera")}, ledgerPath)
	return lifecycle, runtime, registry, ledgerPath
}

func registerLifecycleProject(t *testing.T, registry *Registry) Project {
	t.Helper()
	project := Project{ID: ProjectID("project-1"), Name: "project", Path: "/workspace/project", MainBranch: "main"}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	return project
}

func TestLifecyclePersistsIntentBeforeRuntimeAndConverges(t *testing.T) {
	lifecycle, runtime, registry, ledgerPath := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	var observed LifecycleRecord
	runtime.onStart = func(session Session) {
		snapshot, err := lifecycle.Snapshot(context.Background())
		if err != nil {
			t.Errorf("read planned transition: %v", err)
			return
		}
		for _, record := range snapshot.Records {
			if record.SessionID == session.ID {
				observed = record
			}
		}
	}

	result, err := lifecycle.Provision(context.Background(), SessionProvision{
		Project: project, Name: "hera", Directory: project.Path,
		Kind: SessionKindCodingAgent, InitialPrompt: "inspect the change",
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Desired != SessionDesiredRunning || observed.Phase == LifecycleConverged {
		t.Fatalf("runtime started without a durable pending intent: %+v", observed)
	}
	if result.Record.Phase != LifecycleConverged || !result.Record.Applied.RuntimePresent || !result.Record.Applied.RegistryUpdated {
		t.Fatalf("unexpected applied state: %+v", result.Record)
	}
	if result.Record.PromptDelivery != InitialPromptUnknown {
		t.Fatalf("asynchronous prompt delivery must remain explicit, got %q", result.Record.PromptDelivery)
	}
	if result.Record.InitialPrompt != "" {
		t.Fatal("prompt content must not remain in the durable ledger after delivery was attempted")
	}
	if runtime.deliverCalls != 1 {
		t.Fatalf("initial prompt delivered %d times", runtime.deliverCalls)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	if len(state.Agents) != 1 || state.Agents[0].ID != result.Session.ID || state.Agents[0].ProjectID != project.ID {
		t.Fatalf("Registry did not retain stable associations: %+v", state.Agents)
	}
	if info, err := os.Stat(ledgerPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("Lifecycle ledger permissions = %v, err=%v", info, err)
	}
}

func TestLifecycleRetainsFailureAndReconcilesForward(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	runtime.startErr = errors.New("runtime unavailable")
	result, err := lifecycle.Provision(context.Background(), SessionProvision{
		Project: project, Name: "atlas", Directory: project.Path,
	})
	if err == nil {
		t.Fatal("expected runtime failure")
	}
	if result.Record.Phase != LifecycleFailed || result.Record.LastError == "" {
		t.Fatalf("partial failure was not retained: %+v", result.Record)
	}
	snapshot, _ := registry.Snapshot(context.Background())
	if len(snapshot.State().Agents) != 0 {
		t.Fatal("failed runtime must not appear as a registered Session")
	}

	runtime.startErr = nil
	reconciled, err := lifecycle.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Converged != 1 || reconciled.Restored != 1 || len(reconciled.Problems) != 0 {
		t.Fatalf("unexpected reconciliation: %+v", reconciled)
	}
	snapshot, _ = registry.Snapshot(context.Background())
	if len(snapshot.State().Agents) != 1 || snapshot.State().Agents[0].Name != "atlas" {
		t.Fatalf("reconciliation did not roll intent forward: %+v", snapshot.State().Agents)
	}
}

func TestLifecycleParkRecordsDesiredStateBeforeStopping(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	created, err := lifecycle.Provision(context.Background(), SessionProvision{
		Project: project, Name: "nyx", Directory: project.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	var observed LifecycleRecord
	runtime.onStop = func(session Session) {
		snapshot, err := lifecycle.Snapshot(context.Background())
		if err != nil {
			t.Errorf("read stop intent: %v", err)
			return
		}
		for _, record := range snapshot.Records {
			if record.SessionID == session.ID {
				observed = record
			}
		}
	}
	parked, err := lifecycle.Park(context.Background(), created.Session.ID, created.Session.Name)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Desired != SessionDesiredLater || observed.Phase != LifecyclePlanned {
		t.Fatalf("stop crossed runtime Seam before intent was durable: %+v", observed)
	}
	if parked.Record.Phase != LifecycleConverged || runtime.present[created.Session.ID] {
		t.Fatalf("park did not converge: %+v", parked.Record)
	}
	snapshot, _ := registry.Snapshot(context.Background())
	if snapshot.State().Agents[0].LaterAt.IsZero() {
		t.Fatal("Registry was not updated with later intent")
	}
}

func TestLifecycleManagedWorktreeIsOwnedByProvisioning(t *testing.T) {
	lifecycle, _, registry, _ := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	result, err := lifecycle.Provision(context.Background(), SessionProvision{
		Project: project, Name: "selene", CreateWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Dir == project.Path || !result.Record.Applied.WorktreeReady {
		t.Fatalf("managed Worktree was not provisioned: %+v", result.Record)
	}
}
