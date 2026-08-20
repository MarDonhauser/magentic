package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transitionTestRuntime struct {
	mu           sync.Mutex
	present      map[SessionID]bool
	startEntered chan struct{}
	startRelease chan struct{}
	startOnce    sync.Once
	adapterCalls int
	startCalls   int
	started      []Session
}

func (r *transitionTestRuntime) Exists(context.Context, Session) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapterCalls++
	for _, present := range r.present {
		if present {
			return true, nil
		}
	}
	return false, nil
}

func (r *transitionTestRuntime) Start(ctx context.Context, session Session, _ string) error {
	r.mu.Lock()
	r.adapterCalls++
	r.startCalls++
	r.started = append(r.started, session)
	r.mu.Unlock()
	if r.startEntered != nil {
		r.startOnce.Do(func() { close(r.startEntered) })
	}
	if r.startRelease != nil {
		select {
		case <-r.startRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.mu.Lock()
	if r.present == nil {
		r.present = make(map[SessionID]bool)
	}
	r.present[session.ID] = true
	r.mu.Unlock()
	return nil
}

func (r *transitionTestRuntime) Stop(_ context.Context, session Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapterCalls++
	delete(r.present, session.ID)
	return nil
}

func (r *transitionTestRuntime) Rename(context.Context, Session, string) error {
	r.mu.Lock()
	r.adapterCalls++
	r.mu.Unlock()
	return nil
}

func (r *transitionTestRuntime) DeliverInitial(context.Context, Session, string) (bool, error) {
	r.mu.Lock()
	r.adapterCalls++
	r.mu.Unlock()
	return false, nil
}

func (r *transitionTestRuntime) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.adapterCalls
}

func (r *transitionTestRuntime) starts() (int, []Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startCalls, append([]Session(nil), r.started...)
}

type transitionTestRepositories struct {
	mu           sync.Mutex
	target       string
	createCalls  int
	removeCalls  int
	inspectCalls int
}

func (r *transitionTestRepositories) Change(_ context.Context, change ManagedWorktreeChange) (ManagedWorktreeChangeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := ManagedWorktreeChangeResult{
		Kind: change.Kind, Project: change.Project.Name, State: RepositoryKnown, Changed: true,
	}
	switch change.Kind {
	case ManagedWorktreeCreate:
		r.createCalls++
		result.Path = r.target
		result.Branch = "agent/" + change.Name
	case ManagedWorktreeRemove:
		r.removeCalls++
		result.Path = change.Path
	}
	return result, nil
}

func (r *transitionTestRepositories) Inspect(_ context.Context, request RepositoryInspectRequest) (RepositoryInspection, error) {
	r.mu.Lock()
	r.inspectCalls++
	r.mu.Unlock()
	return RepositoryInspection{
		Directory: request.Directory, Presence: RepositoryKnown,
		Baseline: repositoryKnownFact(RepositoryBaseline{Directory: request.Directory, Head: "head"}),
	}, nil
}

func (r *transitionTestRepositories) calls() (create, remove, inspect int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.createCalls, r.removeCalls, r.inspectCalls
}

func transitionProject(t *testing.T, registry *Registry, root string) Project {
	t.Helper()
	project := Project{ID: "project-id", Name: "project", Path: root, MainBranch: "main"}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	return project
}

func availableDiscovery() RegistryDiscovery {
	return RegistryDiscovery{Availability: RegistryDiscoveryAvailable, ObservedAt: time.Now()}
}

func observationWithStatus(sessions []Session, status AgentStatus) ObservationSnapshot {
	snapshot := ObservationSnapshot{Availability: ObservationAvailable, ObservedAt: time.Now()}
	for _, session := range sessions {
		snapshot.Sessions = append(snapshot.Sessions, SessionObservation{
			SessionID: session.ID, Availability: ObservationAvailable,
			Presence: SessionPresencePresent, Status: status, Occupancy: OccupancyOccupied,
		})
	}
	return snapshot
}

func TestConcurrentProvisionWinsBeforeManagedWorktreeRemoval(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "state.json")
	ledgerPath := filepath.Join(dir, "lifecycle.json")
	registry := OpenRegistry(registryPath)
	project := transitionProject(t, registry, filepath.Join(dir, "project"))
	target, _ := managedWorktreeTarget(project, "topic")
	repositories := &transitionTestRepositories{target: target}
	runtime := &transitionTestRuntime{
		present: make(map[SessionID]bool), startEntered: make(chan struct{}), startRelease: make(chan struct{}),
	}
	provisioning := newSessionLifecycle(registry, runtime, repositories, ledgerPath)
	removing := newSessionLifecycle(registry, runtime, repositories, ledgerPath)
	// Isolate the assertion to Worktree coordination. Production instances also
	// share the outer Project lock; this test deliberately gives removal a
	// different Project-lock root so it must contend on the Worktree lock.
	removing.projects = newProjectTransitionCoordinator(filepath.Join(dir, "other", "state.json"))
	provisioning.discover = func(context.Context, *State) RegistryDiscovery { return availableDiscovery() }
	removing.discover = provisioning.discover
	removing.observe = func(_ context.Context, sessions []Session) ObservationSnapshot {
		return observationWithStatus(sessions, StatusRunning)
	}

	var worktreeAttempts atomic.Int32
	removalQueued := make(chan struct{})
	hook := func(Project, string) {
		if worktreeAttempts.Add(1) == 2 {
			close(removalQueued)
		}
	}
	provisioning.worktrees.beforeAcquire = hook
	removing.worktrees.beforeAcquire = hook

	provisionResult := make(chan error, 1)
	go func() {
		_, err := provisioning.Provision(context.Background(), SessionProvision{
			ProjectID: project.ID, Name: "topic", CreateWorktree: true,
		})
		provisionResult <- err
	}()
	<-runtime.startEntered
	removeResult := make(chan error, 1)
	go func() { removeResult <- removing.RemoveManagedWorktree(context.Background(), project.ID, target) }()
	<-removalQueued
	if _, removeCalls, _ := repositories.calls(); removeCalls != 0 {
		t.Fatalf("repository removal crossed while provisioning held the Worktree transition: calls=%d", removeCalls)
	}
	close(runtime.startRelease)
	if err := <-provisionResult; err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if err := <-removeResult; err == nil {
		t.Fatal("concurrent provision did not veto managed Worktree removal")
	}
	if _, removeCalls, _ := repositories.calls(); removeCalls != 0 {
		t.Fatalf("git Worktree removal calls = %d, want 0", removeCalls)
	}
}

func TestRuntimeNameLockSerializesProvisionAcrossProjects(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "state.json")
	ledgerPath := filepath.Join(dir, "lifecycle.json")
	registry := OpenRegistry(registryPath)
	projectA := Project{ID: "project-a", Name: "alpha", Path: filepath.Join(dir, "alpha"), MainBranch: "main"}
	projectB := Project{ID: "project-b", Name: "beta", Path: filepath.Join(dir, "beta"), MainBranch: "main"}
	for _, project := range []Project{projectA, projectB} {
		if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
			t.Fatal(err)
		}
	}
	targetA, _ := managedWorktreeTarget(projectA, "topic")
	repositories := &transitionTestRepositories{target: targetA}
	runtime := &transitionTestRuntime{
		present: make(map[SessionID]bool), startEntered: make(chan struct{}), startRelease: make(chan struct{}),
	}
	lifecycleA := newSessionLifecycle(registry, runtime, repositories, ledgerPath)
	lifecycleB := newSessionLifecycle(registry, runtime, repositories, ledgerPath)
	var acquisitions atomic.Int32
	secondQueued := make(chan struct{})
	hook := func(runtimeName string) {
		if runtimeName != SessionName("topic") {
			t.Errorf("RuntimeName lock used %q, want %q", runtimeName, SessionName("topic"))
		}
		if acquisitions.Add(1) == 2 {
			close(secondQueued)
		}
	}
	lifecycleA.runtimes.beforeAcquire = hook
	lifecycleB.runtimes.beforeAcquire = hook

	type provisionOutcome struct {
		result SessionLifecycleResult
		err    error
	}
	firstDone := make(chan provisionOutcome, 1)
	go func() {
		result, err := lifecycleA.Provision(context.Background(), SessionProvision{
			ProjectID: projectA.ID, Name: "topic", CreateWorktree: true,
		})
		firstDone <- provisionOutcome{result: result, err: err}
	}()
	<-runtime.startEntered

	secondDone := make(chan provisionOutcome, 1)
	go func() {
		result, err := lifecycleB.Provision(context.Background(), SessionProvision{
			ProjectID: projectB.ID, Name: "topic", CreateWorktree: true,
		})
		secondDone <- provisionOutcome{result: result, err: err}
	}()
	<-secondQueued
	if create, _, inspect := repositories.calls(); create != 1 || inspect != 1 {
		t.Fatalf("losing Project crossed repository Adapter while RuntimeName was locked: create=%d inspect=%d", create, inspect)
	}
	if starts, _ := runtime.starts(); starts != 1 {
		t.Fatalf("runtime starts while second Provision was queued = %d, want 1", starts)
	}

	close(runtime.startRelease)
	first := <-firstDone
	second := <-secondDone
	if first.err != nil {
		t.Fatalf("first Provision() error = %v", first.err)
	}
	if second.err == nil {
		t.Fatal("second Project adopted an already-claimed RuntimeName")
	}
	if create, _, inspect := repositories.calls(); create != 1 || inspect != 1 {
		t.Fatalf("external provisioning paths = create:%d inspect:%d, want exactly one", create, inspect)
	}
	starts, started := runtime.starts()
	if starts != 1 || len(started) != 1 || started[0].ProjectID != projectA.ID || started[0].Dir != targetA {
		t.Fatalf("runtime start was not coherent with winning Project: starts=%d %#v", starts, started)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	if len(state.Agents) != 1 || state.Agents[0].ID != first.result.Session.ID ||
		state.Agents[0].ProjectID != projectA.ID || state.Agents[0].RuntimeName != SessionName("topic") || state.Agents[0].Dir != targetA {
		t.Fatalf("Registry winner is incoherent: %#v", state.Agents)
	}
}

func TestExternalNestedRuntimeVetoesManagedWorktreeRemoval(t *testing.T) {
	dir := t.TempDir()
	registry := OpenRegistry(filepath.Join(dir, "state.json"))
	project := transitionProject(t, registry, filepath.Join(dir, "project"))
	target, _ := managedWorktreeTarget(project, "topic")
	nested := filepath.Join(target, "subdir")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	repositories := &transitionTestRepositories{target: target}
	lifecycle := newSessionLifecycle(
		registry, &transitionTestRuntime{present: make(map[SessionID]bool)}, repositories, filepath.Join(dir, "lifecycle.json"),
	)
	lifecycle.discover = func(context.Context, *State) RegistryDiscovery {
		discovery := availableDiscovery()
		discovery.Sessions = []Session{{
			Name: "external", RuntimeName: "mgt-external", ProjectID: project.ID,
			Project: project.Name, Dir: nested, Worktree: true, CreatedAt: time.Now(),
		}}
		return discovery
	}
	lifecycle.observe = func(_ context.Context, sessions []Session) ObservationSnapshot {
		return observationWithStatus(sessions, StatusRunning)
	}

	if err := lifecycle.RemoveManagedWorktree(context.Background(), project.ID, target); err == nil {
		t.Fatal("unregistered runtime nested in the Worktree did not veto removal")
	}
	if _, removeCalls, _ := repositories.calls(); removeCalls != 0 {
		t.Fatalf("repository removal calls = %d, want 0", removeCalls)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.State().Agents) != 1 {
		t.Fatalf("external runtime was not adopted under destructive coordination: %#v", snapshot.State().Agents)
	}
}

func TestNestedIdleSessionIsRemovedBeforeManagedWorktree(t *testing.T) {
	dir := t.TempDir()
	registry := OpenRegistry(filepath.Join(dir, "state.json"))
	project := transitionProject(t, registry, filepath.Join(dir, "project"))
	target, _ := managedWorktreeTarget(project, "topic")
	session := Session{
		ID: "nested-idle", Name: "nested-idle", RuntimeName: "runtime-nested-idle",
		ProjectID: project.ID, Project: project.Name, Dir: filepath.Join(target, "subdir"), Worktree: true,
	}
	if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}
	repositories := &transitionTestRepositories{target: target}
	runtime := &transitionTestRuntime{present: map[SessionID]bool{session.ID: true}}
	lifecycle := newSessionLifecycle(registry, runtime, repositories, filepath.Join(dir, "lifecycle.json"))
	lifecycle.discover = func(context.Context, *State) RegistryDiscovery { return availableDiscovery() }
	lifecycle.observe = func(_ context.Context, sessions []Session) ObservationSnapshot {
		return observationWithStatus(sessions, StatusIdle)
	}

	if err := lifecycle.RemoveManagedWorktree(context.Background(), project.ID, target); err != nil {
		t.Fatalf("RemoveManagedWorktree() error = %v", err)
	}
	if _, removeCalls, _ := repositories.calls(); removeCalls != 1 {
		t.Fatalf("repository removal calls = %d, want 1 after nested Session removal", removeCalls)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.State().Agents) != 0 {
		t.Fatalf("nested safe Session remained registered: %#v", snapshot.State().Agents)
	}
}

func TestDiscoveredAdoptionWaitsForManagedWorktreeTransition(t *testing.T) {
	dir := t.TempDir()
	registry := OpenRegistry(filepath.Join(dir, "state.json"))
	project := transitionProject(t, registry, filepath.Join(dir, "project"))
	target, _ := managedWorktreeTarget(project, "topic")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	coordinator := newWorktreeTransitionCoordinator(registry.path)
	held := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- coordinator.with(context.Background(), project, target, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := registry.AdoptDiscoveredSessions(ctx, []Session{{
		Name: "external", RuntimeName: "mgt-external", ProjectID: project.ID,
		Project: project.Name, Dir: target, Worktree: true, CreatedAt: time.Now(),
	}})
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("adoption did not wait on Worktree transition: %v", err)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.State().Agents) != 0 {
		t.Fatalf("timed-out adoption mutated Registry: %#v", snapshot.State().Agents)
	}
}

func TestProvisionRejectsStaleProjectIDBeforeAdapterCalls(t *testing.T) {
	dir := t.TempDir()
	registry := OpenRegistry(filepath.Join(dir, "state.json"))
	_ = transitionProject(t, registry, filepath.Join(dir, "project"))
	repositories := &transitionTestRepositories{}
	runtime := &transitionTestRuntime{present: make(map[SessionID]bool)}
	lifecycle := newSessionLifecycle(registry, runtime, repositories, filepath.Join(dir, "lifecycle.json"))

	_, err := lifecycle.Provision(context.Background(), SessionProvision{
		ProjectID: "stale-project-id", Name: "topic", CreateWorktree: true,
	})
	if err == nil {
		t.Fatal("stale ProjectID was accepted")
	}
	create, remove, inspect := repositories.calls()
	if create != 0 || remove != 0 || inspect != 0 || runtime.calls() != 0 {
		t.Fatalf("stale ProjectID crossed an Adapter: repositories=%d/%d/%d runtime=%d", create, remove, inspect, runtime.calls())
	}
}

func TestProjectRemovalWaitsForProvisionRegistryCommit(t *testing.T) {
	dir := t.TempDir()
	registry := OpenRegistry(filepath.Join(dir, "state.json"))
	project := transitionProject(t, registry, filepath.Join(dir, "project"))
	repositories := &transitionTestRepositories{}
	runtime := &transitionTestRuntime{
		present: make(map[SessionID]bool), startEntered: make(chan struct{}), startRelease: make(chan struct{}),
	}
	lifecycle := newSessionLifecycle(registry, runtime, repositories, filepath.Join(dir, "lifecycle.json"))
	var attempts atomic.Int32
	removalQueued := make(chan struct{})
	lifecycle.projects.beforeAcquire = func(ProjectID) {
		if attempts.Add(1) == 2 {
			close(removalQueued)
		}
	}

	provisionResult := make(chan error, 1)
	go func() {
		_, err := lifecycle.Provision(context.Background(), SessionProvision{
			ProjectID: project.ID, Name: "topic", Directory: project.Path,
		})
		provisionResult <- err
	}()
	<-runtime.startEntered
	removeResult := make(chan error, 1)
	go func() { removeResult <- lifecycle.RemoveProject(context.Background(), project.ID) }()
	<-removalQueued
	close(runtime.startRelease)
	if err := <-provisionResult; err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if err := <-removeResult; err == nil {
		t.Fatal("Project removal won between runtime start and Registry registration")
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	if state.ProjectByID(project.ID) == nil || len(state.Agents) != 1 {
		t.Fatalf("Project removal did not observe completed provisioning: %#v", state)
	}
}

func TestCanonicalWorktreeTransitionPathResolvesMissingSymlinkDescendant(t *testing.T) {
	dir := t.TempDir()
	realParent := filepath.Join(dir, "real")
	if err := os.MkdirAll(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(realParent, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	throughAlias := canonicalWorktreeTransitionPath(filepath.Join(alias, "project-agents", "topic"))
	physical := canonicalWorktreeTransitionPath(filepath.Join(realParent, "project-agents", "topic"))
	if throughAlias != physical {
		t.Fatalf("canonical missing Worktree differs through symlink: %q != %q", throughAlias, physical)
	}
}

func TestDiscoveredNameConflictIsNotAnIdempotentRegistryNoop(t *testing.T) {
	registry := OpenRegistry(filepath.Join(t.TempDir(), "state.json"))
	existing := Session{ID: "existing", Name: "topic", RuntimeName: "opaque-topic", Dir: "/work/topic"}
	if _, err := registry.Change(context.Background(), RegisterSession(existing)); err != nil {
		t.Fatal(err)
	}
	before, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Change(context.Background(), addDiscoveredSessionsChange([]Session{{
		Name: "topic", RuntimeName: "mgt-topic", Dir: "/work/topic", CreatedAt: time.Now(),
	}}))
	if !errors.Is(err, ErrRegistryConflict) {
		t.Fatalf("different RuntimeName with the same display name was not a Registry conflict: %v", err)
	}
	after, snapshotErr := registry.Snapshot(context.Background())
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if after.Revision() != before.Revision() || len(after.State().Agents) != 1 {
		t.Fatalf("conflicting discovery mutated Registry: before=%#v after=%#v", before.State(), after.State())
	}
}
