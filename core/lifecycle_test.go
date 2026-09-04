package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeLifecycleRuntime struct {
	present              map[SessionID]bool
	runtimeNames         map[string]bool
	startErr             error
	stopErr              error
	existsErr            error
	renameErr            error
	renameAppliesOnError bool
	existsCalls          []string
	startCalls           int
	stopCalls            int
	lastStartMode        string
	renameCalls          int
	lastRenameFrom       string
	lastRenameTo         string
	deliverCalls         int
	onStart              func(Session)
	onStop               func(Session)
	onRename             func(Session, string)
}

func (f *fakeLifecycleRuntime) Exists(_ context.Context, session Session) (bool, error) {
	f.existsCalls = append(f.existsCalls, session.TmuxName())
	if f.existsErr != nil {
		return false, f.existsErr
	}
	if f.runtimeNames != nil {
		return f.runtimeNames[session.TmuxName()], nil
	}
	return f.present[session.ID], nil
}

func (f *fakeLifecycleRuntime) Start(_ context.Context, session Session, mode string) error {
	f.startCalls++
	f.lastStartMode = mode
	if f.onStart != nil {
		f.onStart(session)
	}
	if f.startErr != nil {
		return f.startErr
	}
	f.present[session.ID] = true
	if f.runtimeNames != nil {
		f.runtimeNames[session.TmuxName()] = true
	}
	return nil
}

func (f *fakeLifecycleRuntime) Stop(_ context.Context, session Session) error {
	f.stopCalls++
	if f.onStop != nil {
		f.onStop(session)
	}
	if f.stopErr != nil {
		return f.stopErr
	}
	delete(f.present, session.ID)
	if f.runtimeNames != nil {
		delete(f.runtimeNames, session.TmuxName())
	}
	return nil
}

func (f *fakeLifecycleRuntime) Rename(_ context.Context, session Session, targetRuntime string) error {
	f.renameCalls++
	f.lastRenameFrom = session.TmuxName()
	f.lastRenameTo = targetRuntime
	if f.renameErr == nil || f.renameAppliesOnError {
		delete(f.runtimeNames, session.TmuxName())
		f.runtimeNames[targetRuntime] = true
	}
	if f.onRename != nil {
		f.onRename(session, targetRuntime)
	}
	return f.renameErr
}

func (f *fakeLifecycleRuntime) DeliverInitial(_ context.Context, _ Session, _ string) (bool, error) {
	f.deliverCalls++
	return false, nil
}

func (f *fakeLifecycleRuntime) reset() {
	f.startCalls, f.stopCalls, f.lastStartMode = 0, 0, ""
	f.existsCalls = nil
}

type fakeLifecycleRepositories struct {
	worktreePath string
	changeCalls  *int
	inspectCalls *int
}

func (f fakeLifecycleRepositories) Change(_ context.Context, change ManagedWorktreeChange) (ManagedWorktreeChangeResult, error) {
	if f.changeCalls != nil {
		*f.changeCalls++
	}
	return ManagedWorktreeChangeResult{
		Kind: change.Kind, Project: change.Project.Name, Path: f.worktreePath,
		Branch: "agent/" + change.Name, State: RepositoryKnown, Changed: true,
	}, nil
}

func (f fakeLifecycleRepositories) Inspect(_ context.Context, request RepositoryInspectRequest) (RepositoryInspection, error) {
	if f.inspectCalls != nil {
		*f.inspectCalls++
	}
	return RepositoryInspection{
		Directory: request.Directory, Presence: RepositoryKnown,
		Baseline: repositoryKnownFact(RepositoryBaseline{Directory: request.Directory, Head: "abc123"}),
	}, nil
}

func lifecycleHarness(t *testing.T) (*SessionLifecycle, *fakeLifecycleRuntime, *Registry, string) {
	t.Helper()
	dir := t.TempDir()
	registry := OpenRegistry(filepath.Join(dir, "state.json"))
	runtime := &fakeLifecycleRuntime{present: map[SessionID]bool{}, runtimeNames: map[string]bool{}}
	ledgerPath := filepath.Join(dir, "lifecycle.json")
	lifecycle := newSessionLifecycle(registry, runtime, fakeLifecycleRepositories{worktreePath: filepath.Join(dir, "project-agents", "hera")}, ledgerPath, filepath.Dir(ledgerPath))
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

func registerLifecycleSession(t *testing.T, registry *Registry, runtime *fakeLifecycleRuntime, session Session, running bool) Session {
	t.Helper()
	if session.ID == "" {
		session.ID = SessionID(NewUUID())
	}
	if session.Dir == "" {
		session.Dir = "/workspace/project"
	}
	result, err := registry.Change(context.Background(), RegisterSession(session))
	if err != nil {
		t.Fatal(err)
	}
	state := result.Snapshot.State()
	registered := state.SessionByID(session.ID)
	if registered == nil {
		t.Fatalf("registered Session %q not found", session.Name)
	}
	if running {
		runtime.present[registered.ID] = true
		runtime.runtimeNames[registered.RuntimeName] = true
	}
	return *registered
}

func TestLifecycleProvisionRequiresKnownFreeExactRuntimeBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name      string
		collision bool
		probeErr  error
	}{
		{name: "occupied RuntimeName", collision: true},
		{name: "unavailable probe", probeErr: errors.New("tmux socket unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lifecycle, runtime, registry, ledgerPath := lifecycleHarness(t)
			project := registerLifecycleProject(t, registry)
			before, err := registry.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			changeCalls := 0
			inspectCalls := 0
			lifecycle.repositories = fakeLifecycleRepositories{
				worktreePath: filepath.Join(filepath.Dir(ledgerPath), "project-agents", "hera"),
				changeCalls:  &changeCalls, inspectCalls: &inspectCalls,
			}
			startCalls := 0
			runtime.onStart = func(Session) { startCalls++ }
			runtime.existsErr = test.probeErr
			if test.collision {
				runtime.runtimeNames[SessionName("hera")] = true
			}

			_, err = lifecycle.Provision(context.Background(), SessionProvision{
				ProjectID: project.ID, Name: "hera", CreateWorktree: true,
			})
			if err == nil {
				t.Fatal("Provision accepted a RuntimeName without known availability")
			}
			if len(runtime.existsCalls) != 1 || runtime.existsCalls[0] != SessionName("hera") {
				t.Fatalf("availability probes = %q, want exact candidate %q", runtime.existsCalls, SessionName("hera"))
			}
			if changeCalls != 0 || inspectCalls != 0 || startCalls != 0 {
				t.Fatalf("failed availability crossed an Adapter: repository change=%d inspect=%d runtime start=%d", changeCalls, inspectCalls, startCalls)
			}
			after, snapshotErr := registry.Snapshot(context.Background())
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			if after.Revision() != before.Revision() || len(after.State().Agents) != 0 {
				t.Fatalf("failed availability mutated Registry revision %d -> %d: %#v", before.Revision(), after.Revision(), after.State().Agents)
			}
			if _, statErr := os.Stat(ledgerPath); !os.IsNotExist(statErr) {
				t.Fatalf("failed availability wrote Lifecycle intent: %v", statErr)
			}
		})
	}
}

func TestLifecycleRuntimeSeamRejectsMalformedIdentityBeforeDelegate(t *testing.T) {
	lifecycle, runtime, _, _ := lifecycleHarness(t)
	malformed := Session{ID: "malformed", Name: "source", RuntimeName: " mgt-source"}
	startCalls := 0
	stopCalls := 0
	runtime.onStart = func(Session) { startCalls++ }
	runtime.onStop = func(Session) { stopCalls++ }

	if _, err := lifecycle.runtime.Exists(context.Background(), malformed); err == nil {
		t.Fatal("Exists accepted a malformed RuntimeName")
	}
	if err := lifecycle.runtime.Start(context.Background(), malformed, "new"); err == nil {
		t.Fatal("Start accepted a malformed RuntimeName")
	}
	if err := lifecycle.runtime.Stop(context.Background(), malformed); err == nil {
		t.Fatal("Stop accepted a malformed RuntimeName")
	}
	if err := lifecycle.runtime.Rename(context.Background(), malformed, "mgt-target"); err == nil {
		t.Fatal("Rename accepted a malformed RuntimeName")
	}
	if _, err := lifecycle.runtime.DeliverInitial(context.Background(), malformed, "prompt"); err == nil {
		t.Fatal("DeliverInitial accepted a malformed RuntimeName")
	}
	if len(runtime.existsCalls) != 0 || startCalls != 0 || stopCalls != 0 || runtime.renameCalls != 0 || runtime.deliverCalls != 0 {
		t.Fatalf("malformed RuntimeName crossed delegate: exists=%q start=%d stop=%d rename=%d deliver=%d",
			runtime.existsCalls, startCalls, stopCalls, runtime.renameCalls, runtime.deliverCalls)
	}
}

func TestLifecycleReconcileDoesNotReconstructMalformedRegisteredRuntime(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	registered := registerLifecycleSession(t, registry, runtime, Session{
		ID: "malformed-registered", Name: "source", RuntimeName: " mgt-source",
	}, false)
	runtime.runtimeNames[SessionName(registered.Name)] = true

	result, err := lifecycle.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Problems) != 1 {
		t.Fatalf("Reconcile problems = %#v, want malformed RuntimeName", result.Problems)
	}
	if len(runtime.existsCalls) != 0 || !runtime.runtimeNames[SessionName(registered.Name)] {
		t.Fatalf("Reconcile addressed trim-equivalent foreign runtime: calls=%q runtimes=%#v", runtime.existsCalls, runtime.runtimeNames)
	}
}

func TestLifecycleLedgerDoesNotReconstructEmptyRuntime(t *testing.T) {
	lifecycle, runtime, _, _ := lifecycleHarness(t)
	foreignRuntime := SessionName("source")
	runtime.runtimeNames[foreignRuntime] = true
	now := time.Now().UTC()
	record := LifecycleRecord{
		TransitionID: "malformed-ledger", SessionID: "malformed-ledger",
		Desired: SessionDesiredLater, Phase: LifecyclePlanned,
		Session:        Session{ID: "malformed-ledger", Name: "source", RuntimeName: "", Dir: "/workspace/project"},
		PromptDelivery: InitialPromptNotRequested,
		Applied:        LifecycleAppliedState{WorktreeReady: true},
		CreatedAt:      now, UpdatedAt: now,
	}
	if _, err := lifecycle.putRecord(context.Background(), record, false); err != nil {
		t.Fatal(err)
	}

	result, err := lifecycle.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Problems) != 1 {
		t.Fatalf("Reconcile problems = %#v, want empty RuntimeName", result.Problems)
	}
	if len(runtime.existsCalls) != 0 || !runtime.runtimeNames[foreignRuntime] {
		t.Fatalf("ledger fallback addressed foreign runtime: calls=%q runtimes=%#v", runtime.existsCalls, runtime.runtimeNames)
	}
}

type responseLostLifecycleRegistry struct {
	registry               *Registry
	loseRenameOnce         bool
	loseBaselineReopenOnce bool
}

func (r *responseLostLifecycleRegistry) Snapshot(ctx context.Context) (RegistrySnapshot, error) {
	return r.registry.Snapshot(ctx)
}

func (r *responseLostLifecycleRegistry) Change(ctx context.Context, change RegistryChange) (RegistryChangeResult, error) {
	result, err := r.registry.Change(ctx, change)
	if err == nil && r.loseRenameOnce && change.kind == registryRenameSession {
		r.loseRenameOnce = false
		return result, errors.New("Registry response lost")
	}
	if err == nil && r.loseBaselineReopenOnce && change.kind == registryReopenSession && change.baseCommit != "" {
		r.loseBaselineReopenOnce = false
		return result, errors.New("Registry baseline-reopen response lost")
	}
	return result, err
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
		ProjectID: project.ID, Name: "hera", Directory: project.Path,
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
		ProjectID: project.ID, Name: "atlas", Directory: project.Path,
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
		ProjectID: project.ID, Name: "nyx", Directory: project.Path,
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

func TestSessionRuntimeNeverChangesAcrossLifecycleTransitions(t *testing.T) {
	lifecycle, _, registry, _ := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	created, err := lifecycle.Provision(context.Background(), SessionProvision{
		ProjectID: project.ID, Name: "nova", Directory: project.Path,
		Kind: SessionKindCodingAgent, Vendor: AgentVendorClaude, Runtime: RuntimeManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Session.SessionRuntime() != RuntimeManaged {
		t.Fatalf("created Session runtime = %v, want managed", created.Session.SessionRuntime())
	}

	assertRuntimeStillManaged := func(step string) {
		t.Helper()
		snapshot, err := registry.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		state := snapshot.State()
		session := state.SessionByID(created.Session.ID)
		if session == nil {
			t.Fatalf("%s: Session verschwunden", step)
		}
		if session.SessionRuntime() != RuntimeManaged {
			t.Fatalf("%s: runtime = %v, want managed", step, session.SessionRuntime())
		}
	}

	if _, err := lifecycle.Park(context.Background(), created.Session.ID, created.Session.Name); err != nil {
		t.Fatal(err)
	}
	assertRuntimeStillManaged("nach Park")

	if _, err := lifecycle.Resume(context.Background(), created.Session.ID, created.Session.Name); err != nil {
		t.Fatal(err)
	}
	assertRuntimeStillManaged("nach Resume")

	if _, err := lifecycle.Rename(context.Background(), created.Session.ID, created.Session.Name, "nova-2"); err != nil {
		t.Fatal(err)
	}
	assertRuntimeStillManaged("nach Rename")
}

func TestLifecycleResumeReopensRegistryAndRemainsRunningAfterReconcile(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	created, err := lifecycle.Provision(context.Background(), SessionProvision{
		ProjectID: project.ID, Name: "iris", Directory: project.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.Park(context.Background(), created.Session.ID, created.Session.Name); err != nil {
		t.Fatal(err)
	}
	parkedSnapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if parkedSnapshot.State().Agents[0].LaterAt.IsZero() {
		t.Fatal("Park did not persist the later intent")
	}

	resumed, err := lifecycle.Resume(context.Background(), created.Session.ID, created.Session.Name)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Record.Phase != LifecycleConverged || !resumed.Record.Applied.RuntimePresent {
		t.Fatalf("Resume did not converge: %+v", resumed.Record)
	}
	if !resumed.Record.Session.LaterAt.IsZero() {
		t.Fatalf("resumed ledger Session still carries LaterAt: %+v", resumed.Record.Session)
	}
	resumedSnapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !resumedSnapshot.State().Agents[0].LaterAt.IsZero() {
		t.Fatal("Resume did not apply the semantic Registry reopen")
	}
	if !runtime.present[created.Session.ID] {
		t.Fatal("Resume did not restore the runtime")
	}

	runtime.onStop = func(Session) { t.Error("Reconcile stopped a reopened Session") }
	reconciled, err := lifecycle.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled.Problems) != 0 || !runtime.present[created.Session.ID] {
		t.Fatalf("reopened Session did not remain running: %+v", reconciled)
	}
}

func TestLifecycleResumePreservesDurableBaseline(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	session := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-baseline", Name: "baseline", RuntimeName: "runtime-baseline",
		ProjectID: project.ID, Project: project.Name, Dir: project.Path,
		BaseCommit: "old-head", BaseDirty: []string{"before.txt"}, LaterAt: time.Now(),
	}, false)

	resumed, err := lifecycle.Resume(context.Background(), session.ID, session.Name)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Session.BaseCommit != "old-head" || len(resumed.Session.BaseDirty) != 1 || resumed.Session.BaseDirty[0] != "before.txt" {
		t.Fatalf("Resume recaptured or changed durable baseline: %#v", resumed.Session)
	}
	if !resumed.Session.LaterAt.IsZero() || !runtime.present[session.ID] {
		t.Fatalf("Resume did not clear Later intent and start runtime: %#v", resumed.Session)
	}
}

func TestLifecycleResumePersistsCapturedBaselineForLegacySession(t *testing.T) {
	lifecycle, runtime, registry, ledgerPath := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	laterAt := time.Date(2026, 8, 20, 9, 30, 0, 0, time.UTC)
	legacy := registerLifecycleSession(t, registry, runtime, Session{
		ID: "legacy-resume", Name: "legacy", RuntimeName: "opaque-legacy-runtime",
		ProjectID: project.ID, Project: project.Name, Dir: project.Path, LaterAt: laterAt,
	}, false)
	if legacy.BaseCommit != "" {
		t.Fatalf("legacy fixture already has a baseline: %+v", legacy)
	}

	resumed, err := lifecycle.Resume(context.Background(), legacy.ID, legacy.Name)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Record.Phase != LifecycleConverged || !resumed.Record.Applied.BaselineKnown {
		t.Fatalf("Resume did not converge with a known baseline: %+v", resumed.Record)
	}
	if resumed.Session.BaseCommit != "abc123" || resumed.Record.Session.BaseCommit != "abc123" {
		t.Fatalf("captured baseline was lost from Resume result: %+v", resumed.Record.Session)
	}
	if !resumed.Session.LaterAt.IsZero() {
		t.Fatalf("Resume did not reopen the legacy Session: %+v", resumed.Session)
	}

	reloadedRegistry := OpenRegistry(registry.path)
	reloadedSnapshot, err := reloadedRegistry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reloadedState := reloadedSnapshot.State()
	reloaded := reloadedState.SessionByID(legacy.ID)
	if reloaded == nil || reloaded.BaseCommit != "abc123" || !reloaded.LaterAt.IsZero() {
		t.Fatalf("reloaded Registry lost the resumed baseline or running intent: %+v", reloaded)
	}
	if reloaded.RuntimeName != "opaque-legacy-runtime" {
		t.Fatalf("Resume changed the persisted runtime identity: %+v", reloaded)
	}

	reloadedLifecycle := newSessionLifecycle(
		reloadedRegistry,
		runtime,
		fakeLifecycleRepositories{worktreePath: filepath.Join(filepath.Dir(ledgerPath), "project-agents", "legacy")},
		ledgerPath,
		filepath.Dir(ledgerPath),
	)
	reconciled, err := reloadedLifecycle.Reconcile(context.Background())
	if err != nil || len(reconciled.Problems) != 0 {
		t.Fatalf("reloaded lifecycle did not reconcile cleanly: result=%+v err=%v", reconciled, err)
	}
	if !runtime.runtimeNames[legacy.RuntimeName] {
		t.Fatal("Reconcile stopped the resumed legacy runtime")
	}
	ledgerSnapshot, err := reloadedLifecycle.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ledgerSnapshot.Records) != 1 || ledgerSnapshot.Records[0].Session.BaseCommit != "abc123" ||
		!ledgerSnapshot.Records[0].Applied.BaselineKnown || ledgerSnapshot.Records[0].Phase != LifecycleConverged {
		t.Fatalf("reloaded lifecycle ledger lost the captured baseline: %+v", ledgerSnapshot.Records)
	}
	finalSnapshot, err := OpenRegistry(registry.path).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	finalState := finalSnapshot.State()
	finalSession := finalState.SessionByID(legacy.ID)
	if finalSession == nil || finalSession.BaseCommit != "abc123" || !finalSession.LaterAt.IsZero() {
		t.Fatalf("Reconcile changed persisted Registry truth: %+v", finalSession)
	}
}

func TestLifecycleResumeRetriesLostAtomicBaselineReopenResponse(t *testing.T) {
	_, runtime, registry, ledgerPath := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	laterAt := time.Date(2026, 8, 20, 9, 30, 0, 0, time.UTC)
	legacy := registerLifecycleSession(t, registry, runtime, Session{
		ID: "legacy-response-lost", Name: "legacy", RuntimeName: "opaque-legacy-runtime",
		ProjectID: project.ID, Project: project.Name, Dir: project.Path, LaterAt: laterAt,
	}, false)
	responseLost := &responseLostLifecycleRegistry{registry: registry, loseBaselineReopenOnce: true}
	lifecycle := newSessionLifecycle(
		responseLost,
		runtime,
		fakeLifecycleRepositories{worktreePath: filepath.Join(filepath.Dir(ledgerPath), "project-agents", "legacy")},
		ledgerPath,
		filepath.Dir(ledgerPath),
	)

	if _, err := lifecycle.Resume(context.Background(), legacy.ID, legacy.Name); err == nil ||
		!strings.Contains(err.Error(), "baseline-reopen response lost") {
		t.Fatalf("ambiguous Resume error = %v", err)
	}
	committed, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	committedState := committed.State()
	committedSession := committedState.SessionByID(legacy.ID)
	if committedSession == nil || committedSession.BaseCommit != "abc123" || !committedSession.LaterAt.IsZero() {
		t.Fatalf("lost response did not leave the atomic postcondition committed: %+v", committedSession)
	}

	deployAt := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	marked, err := registry.Change(context.Background(), MarkSessionDeploy(legacy.ID, legacy.Name, deployAt))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := lifecycle.Resume(context.Background(), legacy.ID, legacy.Name)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Session.BaseCommit != "abc123" || !resumed.Session.LaterAt.IsZero() || !resumed.Session.DeployAt.Equal(deployAt) ||
		resumed.Session.RuntimeName != legacy.RuntimeName {
		t.Fatalf("Resume retry lost current Registry fields: %+v", resumed.Session)
	}
	afterRetry, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if afterRetry.Revision() != marked.Revision {
		t.Fatalf("idempotent Resume retry rewrote Registry revision %d -> %d", marked.Revision, afterRetry.Revision())
	}
}

func TestLifecycleSerializesParkAndResumeAcrossInstances(t *testing.T) {
	lifecycle, runtime, registry, ledgerPath := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	created, err := lifecycle.Provision(context.Background(), SessionProvision{
		ProjectID: project.ID, Name: "rhea", Directory: project.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	resumeLifecycle := newSessionLifecycle(
		registry,
		runtime,
		fakeLifecycleRepositories{worktreePath: filepath.Join(filepath.Dir(ledgerPath), "project-agents", "rhea")},
		ledgerPath,
		filepath.Dir(ledgerPath),
	)

	stopEntered := make(chan struct{})
	releaseStop := make(chan struct{})
	runtime.onStop = func(Session) {
		close(stopEntered)
		<-releaseStop
	}
	type transitionOutcome struct {
		result SessionLifecycleResult
		err    error
	}
	parkDone := make(chan transitionOutcome, 1)
	go func() {
		result, parkErr := lifecycle.Park(context.Background(), created.Session.ID, created.Session.Name)
		parkDone <- transitionOutcome{result: result, err: parkErr}
	}()
	<-stopEntered

	resumeDone := make(chan transitionOutcome, 1)
	go func() {
		result, resumeErr := resumeLifecycle.Resume(context.Background(), created.Session.ID, created.Session.Name)
		resumeDone <- transitionOutcome{result: result, err: resumeErr}
	}()

	// Park still owns the per-Session transition lock while Stop is paused at
	// the runtime Seam. Resume must not be able to persist or converge a newer
	// record until that side effect and its Registry update are complete.
	returnedBeforeParkFinished := false
	select {
	case <-resumeDone:
		returnedBeforeParkFinished = true
	case <-time.After(250 * time.Millisecond):
	}
	close(releaseStop)
	parked := <-parkDone
	if parked.err != nil {
		t.Fatal(parked.err)
	}
	var resumed transitionOutcome
	if returnedBeforeParkFinished {
		// Consume a deterministic value for the assertions and ensure the Park
		// goroutine is released before failing the test.
		resumed = transitionOutcome{}
	} else {
		resumed = <-resumeDone
	}
	if returnedBeforeParkFinished {
		t.Fatal("Resume crossed the runtime Seam while Park still owned the transition")
	}
	if resumed.err != nil {
		t.Fatal(resumed.err)
	}
	if resumed.result.Record.Desired != SessionDesiredRunning || resumed.result.Record.Phase != LifecycleConverged {
		t.Fatalf("newest transition did not converge: %+v", resumed.result.Record)
	}
	runtime.onStop = nil
	if !runtime.present[created.Session.ID] {
		t.Fatal("superseded Park left the resumed runtime absent")
	}
	registrySnapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !registrySnapshot.State().Agents[0].LaterAt.IsZero() {
		t.Fatal("newest Resume intent did not reopen the Registry Session")
	}
	ledgerSnapshot, err := lifecycle.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ledgerSnapshot.Records) != 1 || ledgerSnapshot.Records[0].Desired != SessionDesiredRunning || ledgerSnapshot.Records[0].Phase != LifecycleConverged {
		t.Fatalf("ledger does not retain the newest running postcondition: %+v", ledgerSnapshot.Records)
	}
}

func TestLifecycleRenamePersistsIntentBeforeRenamingCustomRuntime(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	session := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-rename", Name: "display-name", RuntimeName: "opaque-runtime", Dir: "/workspace/project",
	}, true)
	var observed LifecycleRecord
	runtime.onRename = func(source Session, target string) {
		snapshot, err := lifecycle.Snapshot(context.Background())
		if err != nil {
			t.Errorf("read rename intent: %v", err)
			return
		}
		for _, record := range snapshot.Records {
			if record.SessionID == source.ID {
				observed = record
			}
		}
	}

	result, err := lifecycle.Rename(context.Background(), session.ID, session.Name, "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if observed.TransitionKind != LifecycleTransitionRename || observed.RenameTo != "renamed" ||
		observed.Phase != LifecyclePlanned || !observed.MayHaveApplied {
		t.Fatalf("tmux rename crossed the runtime Seam without durable intent: %+v", observed)
	}
	if runtime.renameCalls != 1 || runtime.lastRenameFrom != "opaque-runtime" || runtime.lastRenameTo != SessionName("renamed") {
		t.Fatalf("runtime rename = %d calls, %q -> %q", runtime.renameCalls, runtime.lastRenameFrom, runtime.lastRenameTo)
	}
	if result.Record.Phase != LifecycleConverged || !result.Record.Applied.RuntimeRenameSettled || !result.Record.Applied.RuntimeRenamed {
		t.Fatalf("rename did not converge: %+v", result.Record)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	renamed := state.SessionByID(session.ID)
	if renamed == nil || renamed.Name != "renamed" || renamed.RuntimeName != SessionName("renamed") {
		t.Fatalf("Registry rename = %+v", renamed)
	}
}

func TestLifecycleRenameKeepsOpaqueRuntimeWhenRuntimeIsAbsent(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	laterAt := time.Date(2026, 8, 20, 8, 30, 0, 0, time.UTC)
	session := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-offline", Name: "offline-display", RuntimeName: "opaque-offline-runtime",
		Dir: "/workspace/project", LaterAt: laterAt,
	}, false)

	result, err := lifecycle.Rename(context.Background(), session.ID, session.Name, "offline-renamed")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.renameCalls != 0 {
		t.Fatalf("absent runtime was renamed %d times", runtime.renameCalls)
	}
	if result.Record.Desired != SessionDesiredLater || !result.Record.Applied.RuntimeRenameSettled || result.Record.Applied.RuntimeRenamed {
		t.Fatalf("offline rename state = %+v", result.Record)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	renamed := state.SessionByID(session.ID)
	if renamed == nil || renamed.Name != "offline-renamed" || renamed.RuntimeName != "opaque-offline-runtime" || !renamed.LaterAt.Equal(laterAt) {
		t.Fatalf("offline Registry rename lost opaque runtime or LaterAt: %+v", renamed)
	}
	reconciled, err := lifecycle.Reconcile(context.Background())
	if err != nil || len(reconciled.Problems) != 0 || runtime.runtimeNames["opaque-offline-runtime"] {
		t.Fatalf("offline renamed Session was incorrectly restored: result=%+v err=%v", reconciled, err)
	}
}

func TestLifecycleRenameTreatsPersistedDefaultRuntimeAsOpaqueWhenAbsent(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	oldRuntime := SessionName("offline-default")
	session := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-offline-default", Name: "offline-default", RuntimeName: oldRuntime,
		Dir: "/workspace/project",
	}, false)

	result, err := lifecycle.Rename(context.Background(), session.ID, session.Name, "new-display")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.renameCalls != 0 || result.Record.Applied.RuntimeRenamed {
		t.Fatalf("absent default runtime was treated as externally renamed: %+v", result.Record)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	renamed := state.SessionByID(session.ID)
	if renamed == nil || renamed.Name != "new-display" || renamed.RuntimeName != oldRuntime {
		t.Fatalf("offline rename reconstructed RuntimeName from the new display name: %+v", renamed)
	}
}

func TestLifecycleRenameRejectsDisplayAndRuntimeCollisionsBeforeSideEffect(t *testing.T) {
	t.Run("display name", func(t *testing.T) {
		lifecycle, runtime, registry, _ := lifecycleHarness(t)
		source := registerLifecycleSession(t, registry, runtime, Session{
			ID: "source", Name: "source", RuntimeName: "source-runtime",
		}, true)
		registerLifecycleSession(t, registry, runtime, Session{
			ID: "target", Name: "taken", RuntimeName: "target-runtime",
		}, false)

		if _, err := lifecycle.Rename(context.Background(), source.ID, source.Name, "taken"); err == nil {
			t.Fatal("display-name collision was accepted")
		}
		if runtime.renameCalls != 0 {
			t.Fatal("display-name collision crossed the runtime Seam")
		}
	})

	t.Run("runtime target", func(t *testing.T) {
		lifecycle, runtime, registry, _ := lifecycleHarness(t)
		source := registerLifecycleSession(t, registry, runtime, Session{
			ID: "source", Name: "source", RuntimeName: "source-runtime",
		}, true)
		targetRuntime := SessionName("renamed")
		registerLifecycleSession(t, registry, runtime, Session{
			ID: "other", Name: "other", RuntimeName: targetRuntime,
		}, false)

		if _, err := lifecycle.Rename(context.Background(), source.ID, source.Name, "renamed"); err == nil {
			t.Fatal("runtime target collision was accepted")
		}
		if runtime.renameCalls != 0 || !runtime.runtimeNames["source-runtime"] {
			t.Fatal("runtime target collision crossed the runtime Seam")
		}
	})
}

func TestLifecycleRenameRejectsMalformedOpaqueRuntimeBeforeExternalProbe(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	session := registerLifecycleSession(t, registry, runtime, Session{
		ID: "malformed-runtime", Name: "source", RuntimeName: " foreign-runtime",
	}, false)
	// A distinct, trim-equivalent runtime exists. Normalizing the durable opaque
	// identity would target that unrelated process.
	runtime.runtimeNames["foreign-runtime"] = true

	if _, err := lifecycle.Rename(context.Background(), session.ID, session.Name, "renamed"); err == nil {
		t.Fatal("malformed opaque RuntimeName was accepted")
	}
	if len(runtime.existsCalls) != 0 || runtime.renameCalls != 0 {
		t.Fatalf("malformed RuntimeName crossed the runtime Seam: exists=%q rename=%d", runtime.existsCalls, runtime.renameCalls)
	}
	if !runtime.runtimeNames["foreign-runtime"] {
		t.Fatal("trim-equivalent foreign runtime was mutated")
	}
}

func TestLifecycleRenameReconcilesCrashAfterExternalRenameWithoutReplay(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	session := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-crash", Name: "before", RuntimeName: "custom-before",
	}, true)
	runtime.renameErr = errors.New("tmux response lost")
	runtime.renameAppliesOnError = true
	runtime.onRename = func(Session, string) {
		runtime.existsErr = errors.New("tmux observation unavailable")
	}

	result, err := lifecycle.Rename(context.Background(), session.ID, session.Name, "after")
	if err == nil {
		t.Fatal("expected unknown rename postcondition")
	}
	if result.Record.Phase != LifecycleFailed || !result.Record.MayHaveApplied {
		t.Fatalf("ambiguous rename was not retained: %+v", result.Record)
	}
	if runtime.runtimeNames[session.RuntimeName] || !runtime.runtimeNames[SessionName("after")] {
		t.Fatalf("fake external rename did not apply: %#v", runtime.runtimeNames)
	}
	snapshot, _ := registry.Snapshot(context.Background())
	state := snapshot.State()
	if got := state.SessionByID(session.ID); got == nil || got.Name != "before" || got.RuntimeName != "custom-before" {
		t.Fatalf("Registry changed before a verified runtime postcondition: %+v", got)
	}
	if _, parkErr := lifecycle.Park(context.Background(), session.ID, session.Name); parkErr == nil {
		t.Fatal("state transition superseded an ambiguous runtime rename")
	}
	pending, pendingErr := lifecycle.Snapshot(context.Background())
	if pendingErr != nil || len(pending.Records) != 1 || pending.Records[0].TransitionKind != LifecycleTransitionRename {
		t.Fatalf("ambiguous rename intent was discarded: %+v, %v", pending, pendingErr)
	}

	runtime.existsErr = nil
	runtime.renameErr = nil
	runtime.renameAppliesOnError = false
	runtime.onRename = nil
	reconciled, err := lifecycle.Reconcile(context.Background())
	if err != nil || len(reconciled.Problems) != 0 {
		t.Fatalf("reconcile = %+v, %v", reconciled, err)
	}
	if reconciled.Restored != 0 {
		t.Fatalf("rename was counted as a restored Session: %+v", reconciled)
	}
	if runtime.renameCalls != 1 {
		t.Fatalf("external rename replayed %d times", runtime.renameCalls)
	}
	snapshot, _ = registry.Snapshot(context.Background())
	state = snapshot.State()
	if got := state.SessionByID(session.ID); got == nil || got.Name != "after" || got.RuntimeName != SessionName("after") {
		t.Fatalf("reconciled Registry rename = %+v", got)
	}
}

func TestLifecycleRenameDirectRetrySettlesAmbiguousExternalRename(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	session := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-direct-retry", Name: "before", RuntimeName: "custom-before",
	}, true)
	runtime.renameErr = errors.New("tmux response lost")
	runtime.renameAppliesOnError = true
	runtime.onRename = func(Session, string) {
		runtime.existsErr = errors.New("tmux observation unavailable")
	}
	if _, err := lifecycle.Rename(context.Background(), session.ID, session.Name, "after"); err == nil {
		t.Fatal("expected unknown rename postcondition")
	}

	runtime.existsErr = nil
	runtime.renameErr = nil
	runtime.renameAppliesOnError = false
	runtime.onRename = nil
	retried, err := lifecycle.Rename(context.Background(), session.ID, session.Name, "after")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.renameCalls != 1 {
		t.Fatalf("direct retry replayed external rename %d times", runtime.renameCalls)
	}
	if retried.Session.Name != "after" || retried.Session.RuntimeName != SessionName("after") || retried.Record.Phase != LifecycleConverged ||
		!retried.Record.Applied.RuntimePresent || !retried.Record.Applied.RuntimeRenamed {
		t.Fatalf("direct retry did not converge pending intent: %+v", retried)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	if got := state.SessionByID(session.ID); got == nil || got.Name != "after" || got.RuntimeName != SessionName("after") {
		t.Fatalf("direct retry Registry state = %+v", got)
	}
}

func TestLifecycleRenameReconcilesLostRegistryResponse(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	session := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-registry", Name: "before", RuntimeName: "runtime-before",
	}, true)
	wrapper := &responseLostLifecycleRegistry{registry: registry, loseRenameOnce: true}
	lifecycle.registry = wrapper

	result, err := lifecycle.Rename(context.Background(), session.ID, session.Name, "after")
	if err == nil {
		t.Fatal("expected lost Registry response")
	}
	if result.Record.Phase != LifecycleFailed || !result.Record.Applied.RuntimeRenameSettled || !result.Record.Applied.RuntimeRenamed {
		t.Fatalf("verified runtime postcondition was not retained: %+v", result.Record)
	}
	snapshot, _ := registry.Snapshot(context.Background())
	state := snapshot.State()
	if got := state.SessionByID(session.ID); got == nil || got.Name != "after" || got.RuntimeName != SessionName("after") {
		t.Fatalf("Registry did not apply before response loss: %+v", got)
	}

	reconciled, err := lifecycle.Reconcile(context.Background())
	if err != nil || len(reconciled.Problems) != 0 {
		t.Fatalf("reconcile = %+v, %v", reconciled, err)
	}
	if runtime.renameCalls != 1 {
		t.Fatalf("verified runtime rename replayed %d times", runtime.renameCalls)
	}
	ledger, err := lifecycle.Snapshot(context.Background())
	if err != nil || len(ledger.Records) != 1 || ledger.Records[0].Phase != LifecycleConverged {
		t.Fatalf("rename ledger did not converge: %+v, %v", ledger, err)
	}
}

func TestTmuxLifecycleRuntimeDistinguishesAbsenceFromUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantAbsent bool
	}{
		{name: "missing target", output: "can't find session: mgt-iris\n", wantAbsent: true},
		{name: "no server", output: "no server running on /tmp/tmux-501/default\n", wantAbsent: true},
		{name: "missing socket", output: "error connecting to /private/tmp/tmux-503/default (No such file or directory)\n", wantAbsent: true},
		{name: "missing socket mixed with another diagnostic", output: "error connecting to /tmp/tmux-501/default (No such file or directory)\nserver exited unexpectedly\n"},
		{name: "missing socket without path", output: "error connecting to  (No such file or directory)\n"},
		{name: "permission denied", output: "error connecting to /tmp/tmux-501/default (Permission denied)\n"},
		{name: "server failure", output: "server exited unexpectedly\n"},
		{name: "absence phrase embedded in failure", output: "permission denied; can't find session: mgt-iris\n"},
		{name: "absence mixed with another diagnostic", output: "can't find session: mgt-iris\nserver exited unexpectedly\n"},
		{name: "truncated absence diagnostic", output: "can't find session:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := tmuxLifecycleRuntime{command: failingLifecycleCommand(test.output)}
			exists, err := runtime.Exists(context.Background(), Session{Name: "iris", RuntimeName: "mgt-iris"})
			if exists {
				t.Fatal("failed has-session command cannot prove presence")
			}
			if test.wantAbsent && err != nil {
				t.Fatalf("known target absence returned an error: %v", err)
			}
			if !test.wantAbsent {
				if err == nil {
					t.Fatal("tmux failure was fabricated as target absence")
				}
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("tmux execution failure was not preserved: %v", err)
				}
			}
		})
	}
}

func failingLifecycleCommand(output string) lifecycleCommandRunner {
	return func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLifecycleTmuxExitHelper$")
		command.Env = append(os.Environ(),
			"MAGENTIC_LIFECYCLE_TMUX_HELPER=1",
			"MAGENTIC_LIFECYCLE_TMUX_OUTPUT="+output,
		)
		return command.CombinedOutput()
	}
}

func TestLifecycleTmuxExitHelper(t *testing.T) {
	if os.Getenv("MAGENTIC_LIFECYCLE_TMUX_HELPER") != "1" {
		return
	}
	_, _ = os.Stderr.WriteString(os.Getenv("MAGENTIC_LIFECYCLE_TMUX_OUTPUT"))
	os.Exit(1)
}

func TestLifecycleManagedWorktreeIsOwnedByProvisioning(t *testing.T) {
	lifecycle, _, registry, _ := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	result, err := lifecycle.Provision(context.Background(), SessionProvision{
		ProjectID: project.ID, Name: "selene", CreateWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Dir == project.Path || !result.Record.Applied.WorktreeReady {
		t.Fatalf("managed Worktree was not provisioned: %+v", result.Record)
	}
}

// resumeHarness registers one coding Session with a real working directory
// inside a real temp Project, so resume pre-validation passes. For Claude the
// recorded conversation is also laid into a temp HOME, so RunExists answers
// from files the test owns.
func resumeHarness(t *testing.T, vendor AgentVendor, runID string) (*SessionLifecycle, *fakeLifecycleRuntime, *Registry, Project, Session) {
	t.Helper()
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	projDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := Project{ID: ProjectID("project-1"), Name: "project", Path: projDir, MainBranch: "main"}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	session := Session{
		ID: "session-hera", Name: "hera", RuntimeName: "custom-before",
		ProjectID: project.ID, Project: project.Name, Dir: projDir,
		SessionKind: SessionKindCodingAgent, Vendor: vendor,
		AgentRuns:  []AgentRunRef{{Vendor: vendor, ExternalID: runID}},
		BaseCommit: "old-head", BaseDirty: []string{"before.txt"},
		CreatedAt: time.Now().Add(-time.Hour).UTC(),
	}
	registered := registerLifecycleSession(t, registry, runtime, session, false)
	if vendor == AgentVendorClaude && runID != "" {
		claudeDir := filepath.Join(home, ".claude", "projects")
		if err := os.MkdirAll(claudeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(claudeDir, runID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return lifecycle, runtime, registry, project, registered
}

func TestResumeAfterRestartPersistsIntentBeforeRuntime(t *testing.T) {
	lifecycle, runtime, _, _, registered := resumeHarness(t, AgentVendorClaude, "run-1")
	var observed LifecycleRecord
	observedOK := false
	runtime.onStart = func(session Session) {
		snapshot, err := lifecycle.Snapshot(context.Background())
		if err != nil {
			t.Errorf("read planned transition: %v", err)
			return
		}
		for _, record := range snapshot.Records {
			if record.SessionID == session.ID {
				observed = record
				observedOK = true
			}
		}
	}
	result, err := lifecycle.ResumeAfterRestart(context.Background(), registered.ID, registered.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !observedOK {
		t.Fatal("runtime started without a readable planned intent")
	}
	if observed.Desired != SessionDesiredRunning || observed.TransitionKind != LifecycleTransitionResume || observed.StartMode != "resume" {
		t.Fatalf("intent not durable before runtime start: %+v", observed)
	}
	if observed.Phase == LifecycleConverged {
		t.Fatalf("intent already converged at runtime start: %+v", observed)
	}
	if result.Record.Phase != LifecycleConverged || !result.Record.Applied.RuntimePresent {
		t.Fatalf("resume did not converge: %+v", result.Record)
	}
	if result.Session.ID != registered.ID || result.Session.Name != registered.Name ||
		result.Session.ProjectID != registered.ProjectID ||
		len(result.Session.AgentRuns) != 1 || result.Session.AgentRuns[0] != registered.AgentRuns[0] {
		t.Fatalf("resume changed durable identity: %+v", result.Session)
	}
	if result.Session.BaseCommit != "old-head" || len(result.Session.BaseDirty) != 1 || result.Session.BaseDirty[0] != "before.txt" {
		t.Fatalf("resume recaptured or changed durable baseline: %+v", result.Session)
	}
}

func TestResumeAfterRestartMintsFreshRuntimeName(t *testing.T) {
	lifecycle, runtime, registry, _, registered := resumeHarness(t, AgentVendorClaude, "run-1")
	var started []Session
	runtime.onStart = func(session Session) { started = append(started, session) }
	result, err := lifecycle.ResumeAfterRestart(context.Background(), registered.ID, registered.Name)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range runtime.existsCalls {
		if call == registered.RuntimeName {
			t.Fatalf("recorded runtime name %q was addressed: %q", registered.RuntimeName, runtime.existsCalls)
		}
	}
	if len(started) != 1 {
		t.Fatalf("started runtimes = %d, want 1", len(started))
	}
	if started[0].RuntimeName == registered.RuntimeName {
		t.Fatalf("recorded runtime name was reused: %q", started[0].RuntimeName)
	}
	fresh := result.Session.RuntimeName
	if fresh == registered.RuntimeName || !strings.HasPrefix(fresh, SessionPrefix) {
		t.Fatalf("fresh RuntimeName = %q, want a new mgt- name", fresh)
	}
	// Atomar mit der Transition persistiert: Ledger-Satz und Registry stimmen
	// überein, die Outbox blieb unberührt.
	ledger, err := lifecycle.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ledgerOK := false
	for _, record := range ledger.Records {
		if record.SessionID == registered.ID && record.Session.RuntimeName == fresh {
			ledgerOK = true
		}
	}
	if !ledgerOK {
		t.Fatalf("fresh RuntimeName not persisted with the transition: %+v", ledger.Records)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	if got := state.SessionByID(registered.ID); got == nil || got.RuntimeName != fresh {
		t.Fatalf("Registry RuntimeName = %+v, want %q", got, fresh)
	}
}

func TestResumeAfterRestartStartsRecordedDirWithResumeCommand(t *testing.T) {
	lifecycle, runtime, _, project, registered := resumeHarness(t, AgentVendorClaude, "run-1")
	var started Session
	runtime.onStart = func(session Session) { started = session }
	result, err := lifecycle.ResumeAfterRestart(context.Background(), registered.ID, registered.Name)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.lastStartMode != "resume" {
		t.Fatalf("StartMode = %q, want resume", runtime.lastStartMode)
	}
	if started.Dir != project.Path {
		t.Fatalf("runtime directory = %q, want recorded %q", started.Dir, project.Path)
	}
	if started.RuntimeName != result.Session.RuntimeName {
		t.Fatalf("started runtime = %q, want persisted %q", started.RuntimeName, result.Session.RuntimeName)
	}
	command, err := startCommandForSession(result.Session, "resume")
	if err != nil {
		t.Fatal(err)
	}
	want := "claude --name " + ShellQuote(result.Session.RuntimeName) + " --resume 'run-1'"
	if command != want {
		t.Fatalf("resume command = %q, want %q", command, want)
	}
}

func TestResumeAfterRestartRefusesMissingDirectory(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	projDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := Project{ID: ProjectID("project-1"), Name: "project", Path: projDir, MainBranch: "main"}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	registered := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-hera", Name: "hera", RuntimeName: "custom-before",
		ProjectID: project.ID, Project: project.Name, Dir: filepath.Join(projDir, "weg"),
		SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-1"}},
	}, false)

	_, err := lifecycle.ResumeAfterRestart(context.Background(), registered.ID, registered.Name)
	if err == nil || !strings.Contains(err.Error(), "nicht verfügbar") {
		t.Fatalf("missing directory resumed: err = %v", err)
	}
	if runtime.startCalls != 0 {
		t.Fatalf("missing directory created %d runtimes", runtime.startCalls)
	}
	snapshot, _ := registry.Snapshot(context.Background())
	state := snapshot.State()
	if got := state.SessionByID(registered.ID); got == nil || got.RuntimeName != registered.RuntimeName {
		t.Fatalf("failed resume changed the record: %+v", got)
	}
	ledger, _ := lifecycle.Snapshot(context.Background())
	if len(ledger.Records) != 0 {
		t.Fatalf("failed resume left ledger records: %+v", ledger.Records)
	}
}

func TestResumeAfterRestartRefusesDirectoryOutsideProject(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	outside := t.TempDir()
	project := Project{ID: ProjectID("project-1"), Name: "project", Path: t.TempDir(), MainBranch: "main"}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	registered := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-hera", Name: "hera", RuntimeName: "custom-before",
		ProjectID: project.ID, Project: project.Name, Dir: outside,
		SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-1"}},
	}, false)

	_, err := lifecycle.ResumeAfterRestart(context.Background(), registered.ID, registered.Name)
	if err == nil || !strings.Contains(err.Error(), "gehört nicht zu") {
		t.Fatalf("outside directory resumed: err = %v", err)
	}
	if runtime.startCalls != 0 {
		t.Fatalf("outside directory created %d runtimes", runtime.startCalls)
	}
	snapshot, _ := registry.Snapshot(context.Background())
	state := snapshot.State()
	if got := state.SessionByID(registered.ID); got == nil || got.RuntimeName != registered.RuntimeName {
		t.Fatalf("failed resume changed the record: %+v", got)
	}
}

func TestResumeAfterRestartLeavesOutboxUntouched(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	projDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := Project{ID: ProjectID("project-1"), Name: "project", Path: projDir, MainBranch: "main"}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	queued := []QueuedMessage{
		{ID: "msg-1", Kind: QueuedMessageKindMessage, Text: "weiter machen", EnqueuedAt: time.Now().Add(-time.Hour).UTC()},
		{ID: "msg-2", Kind: QueuedMessageKindSkill, Text: "/done ", EnqueuedAt: time.Now().Add(-time.Hour).UTC()},
	}
	registered := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-hera", Name: "hera", RuntimeName: "custom-before",
		ProjectID: project.ID, Project: project.Name, Dir: projDir,
		SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-1"}},
		Outbox:    queued,
	}, false)
	claudeDir := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "run-1.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := lifecycle.ResumeAfterRestart(context.Background(), registered.ID, registered.Name)
	if err != nil {
		t.Fatal(err)
	}
	if result.Record.Phase != LifecycleConverged {
		t.Fatalf("resume did not converge: %+v", result.Record)
	}
	if len(result.Session.Outbox) != 2 || result.Session.Outbox[0].ID != "msg-1" || result.Session.Outbox[1].ID != "msg-2" {
		t.Fatalf("resume touched the Outbox: %+v", result.Session.Outbox)
	}
	if runtime.deliverCalls != 0 {
		t.Fatalf("resume delivered %d initial prompts", runtime.deliverCalls)
	}
}

func TestResumeAfterRestartFailsWhenVendorForgetsConversation(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	projDir := t.TempDir()
	project := Project{ID: ProjectID("project-1"), Name: "project", Path: projDir, MainBranch: "main"}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	registered := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-hera", Name: "hera", RuntimeName: "custom-before",
		ProjectID: project.ID, Project: project.Name, Dir: projDir,
		SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-9"}},
	}, false)
	provider := stubResumeProvider{
		vendor: AgentVendorClaude, behavior: ResumeByRunRef,
		exists: map[string]bool{"run-9": true},
	}
	absent := resumeAbsentObservation(registered.ID)
	if res := ClassifyResumability(registered, absent, provider, nil); !res.Resumable || res.FreshOnly {
		t.Fatalf("classification = %+v, want true resume", res)
	}
	provider.exists["run-9"] = false
	_, err := lifecycle.resumeAfterRestartWithProvider(context.Background(), registered.ID, registered.Name, provider)
	if err == nil || !strings.Contains(err.Error(), "nicht mehr vorhanden") {
		t.Fatalf("forgotten conversation resumed: err = %v", err)
	}
	if runtime.startCalls != 0 {
		t.Fatalf("failed resume created %d runtimes", runtime.startCalls)
	}
	snapshot, _ := registry.Snapshot(context.Background())
	state := snapshot.State()
	got := state.SessionByID(registered.ID)
	if got == nil || got.RuntimeName != registered.RuntimeName || len(got.AgentRuns) != 1 || got.AgentRuns[0].ExternalID != "run-9" {
		t.Fatalf("failed resume did not leave the record intact: %+v", got)
	}
	ledger, _ := lifecycle.Snapshot(context.Background())
	if len(ledger.Records) != 0 {
		t.Fatalf("failed resume left ledger records: %+v", ledger.Records)
	}
}

func plantResumeIntent(t *testing.T, lifecycle *SessionLifecycle, project Project, session Session, mayHaveApplied bool) {
	t.Helper()
	now := time.Now().UTC()
	record := LifecycleRecord{
		TransitionID: NewUUID(), SessionID: session.ID,
		TransitionKind: LifecycleTransitionResume, Desired: SessionDesiredRunning,
		Phase: LifecyclePlanned, Session: session, Project: project,
		StartMode: "resume", PromptDelivery: InitialPromptNotRequested,
		Applied:        LifecycleAppliedState{WorktreeReady: true, BaselineKnown: true},
		MayHaveApplied: mayHaveApplied,
		CreatedAt:      now, UpdatedAt: now,
	}
	if _, err := lifecycle.putRecord(context.Background(), record, false); err != nil {
		t.Fatal(err)
	}
}

func resumeReconcileHarness(t *testing.T, withConversation bool) (*SessionLifecycle, *fakeLifecycleRuntime, *Registry, Project, Session, string) {
	t.Helper()
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	projDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := Project{ID: ProjectID("project-1"), Name: "project", Path: projDir, MainBranch: "main"}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	old := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-hera", Name: "hera", RuntimeName: "custom-before",
		ProjectID: project.ID, Project: project.Name, Dir: projDir,
		SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
		AgentRuns:  []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-7"}},
		BaseCommit: "old-head", BaseDirty: []string{"before.txt"},
	}, false)
	if withConversation {
		claudeDir := filepath.Join(home, ".claude", "projects")
		if err := os.MkdirAll(claudeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(claudeDir, "run-7.jsonl"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fresh := "mgt-hera"
	resumed := old
	resumed.RuntimeName = fresh
	return lifecycle, runtime, registry, project, resumed, fresh
}

func TestResumeAfterRestartReconcilesInterruptedStart(t *testing.T) {
	lifecycle, runtime, registry, project, resumed, fresh := resumeReconcileHarness(t, true)
	plantResumeIntent(t, lifecycle, project, resumed, true)
	runtime.runtimeNames[fresh] = true

	result, err := lifecycle.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.startCalls != 0 {
		t.Fatalf("reconciliation started a second runtime: %d", runtime.startCalls)
	}
	snapshot, _ := registry.Snapshot(context.Background())
	state := snapshot.State()
	if got := state.SessionByID(resumed.ID); got == nil || got.RuntimeName != fresh {
		t.Fatalf("interrupted resume did not converge on the created runtime: %+v", got)
	}
	ledger, _ := lifecycle.Snapshot(context.Background())
	converged := false
	for _, record := range ledger.Records {
		if record.SessionID == resumed.ID && record.Phase == LifecycleConverged {
			converged = true
		}
	}
	if !converged || result.Converged < 1 {
		t.Fatalf("interrupted resume not reconciled: %+v", result)
	}
}

func TestResumeAfterRestartReconcilesBeforeStart(t *testing.T) {
	lifecycle, runtime, _, project, resumed, fresh := resumeReconcileHarness(t, true)
	plantResumeIntent(t, lifecycle, project, resumed, false)

	result, err := lifecycle.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.startCalls != 1 || runtime.lastStartMode != "resume" {
		t.Fatalf("interrupted intent not rolled forward: start=%d mode=%q", runtime.startCalls, runtime.lastStartMode)
	}
	if !runtime.runtimeNames[fresh] {
		t.Fatalf("reconciled resume did not create the fresh runtime: %#v", runtime.runtimeNames)
	}
	if result.Converged < 1 {
		t.Fatalf("resume intent not converged: %+v", result)
	}
}

func TestResumeAfterRestartReconcileRejectsForgottenConversation(t *testing.T) {
	lifecycle, runtime, registry, project, resumed, fresh := resumeReconcileHarness(t, false)
	plantResumeIntent(t, lifecycle, project, resumed, false)

	result, err := lifecycle.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.startCalls != 0 {
		t.Fatalf("forgotten conversation started %d runtimes", runtime.startCalls)
	}
	if len(result.Problems) != 1 || !strings.Contains(result.Problems[0].Message, "nicht mehr vorhanden") {
		t.Fatalf("reconcile problems = %+v, want vendor reason", result.Problems)
	}
	snapshot, _ := registry.Snapshot(context.Background())
	state := snapshot.State()
	if got := state.SessionByID(resumed.ID); got == nil || got.RuntimeName == fresh {
		t.Fatalf("failed resume rewrote the record: %+v", got)
	}
}
