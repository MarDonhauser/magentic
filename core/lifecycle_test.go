package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	renameCalls          int
	lastRenameFrom       string
	lastRenameTo         string
	deliverCalls         int
	onStart              func(Session)
	onStop               func(Session)
	onRename             func(Session, string)
}

func (f *fakeLifecycleRuntime) Exists(_ context.Context, session Session) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	if f.runtimeNames != nil {
		return f.runtimeNames[session.TmuxName()], nil
	}
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
	if f.runtimeNames != nil {
		f.runtimeNames[session.TmuxName()] = true
	}
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
	runtime := &fakeLifecycleRuntime{present: map[SessionID]bool{}, runtimeNames: map[string]bool{}}
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

type responseLostLifecycleRegistry struct {
	registry       *Registry
	loseRenameOnce bool
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

func TestLifecycleResumeReopensRegistryAndRemainsRunningAfterReconcile(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	created, err := lifecycle.Provision(context.Background(), SessionProvision{
		Project: project, Name: "iris", Directory: project.Path,
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

func TestLifecycleSerializesParkAndResumeAcrossInstances(t *testing.T) {
	lifecycle, runtime, registry, ledgerPath := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	created, err := lifecycle.Provision(context.Background(), SessionProvision{
		Project: project, Name: "rhea", Directory: project.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	resumeLifecycle := newSessionLifecycle(
		registry,
		runtime,
		fakeLifecycleRepositories{worktreePath: filepath.Join(filepath.Dir(ledgerPath), "project-agents", "rhea")},
		ledgerPath,
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
		{name: "permission denied", output: "error connecting to /tmp/tmux-501/default (Permission denied)\n"},
		{name: "server failure", output: "server exited unexpectedly\n"},
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
		Project: project, Name: "selene", CreateWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Dir == project.Path || !result.Record.Applied.WorktreeReady {
		t.Fatalf("managed Worktree was not provisioned: %+v", result.Record)
	}
}
