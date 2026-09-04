package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// controlE2E is one end-to-end setting: a real Git Project, a real Registry, a
// real Session Lifecycle over real Repositories, the socket, and a client. Only
// the tmux runtime and the pane reading are substituted, so the Worktree on
// disk and every coordinated change are the real thing.
type controlE2E struct {
	t       *testing.T
	service *ControlService
	client  *ControlClient

	mu       sync.Mutex
	readings map[SessionID]SessionObservation
}

func newControlE2E(t *testing.T) *controlE2E {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git ist nicht installiert")
	}
	root, err := os.MkdirTemp("/tmp", "mgte2e")
	if err != nil {
		t.Fatalf("Verzeichnis nicht anlegbar: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	projectPath := filepath.Join(root, "projekt")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		command := exec.Command("git", args...)
		command.Dir = projectPath
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}

	registry := OpenRegistry(filepath.Join(root, "state.json"))
	project := Project{ID: ProjectID("projekt-e2e"), Name: "projekt", Path: projectPath, MainBranch: "main"}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeLifecycleRuntime{present: map[SessionID]bool{}, runtimeNames: map[string]bool{}}
	lifecycle := newSessionLifecycle(registry, runtime, NewRepositories(), filepath.Join(root, "lifecycle.json"), root)

	harness := &controlE2E{t: t, readings: map[SessionID]SessionObservation{}}
	service := &ControlService{
		registry: registry, lifecycle: lifecycle, repositories: NewRepositories(),
		observe:   harness.observe,
		installed: func(AgentProvider) bool { return true },
		events:    NewControlEvents(),
		now:       time.Now,
	}
	service.deliver = service.deliverThroughOutbox
	service.observations = func(_ context.Context, session Session) (<-chan ObservationSnapshot, func()) {
		return service.events.Observations(session.ID)
	}
	harness.service = service

	server, err := ServeControl(service, filepath.Join(root, "c.sock"))
	if err != nil {
		t.Fatalf("Socket nicht bedienbar: %v", err)
	}
	t.Cleanup(func() { server.Close() })
	harness.client = NewControlClient(server.Path())
	return harness
}

func (e *controlE2E) observe(context.Context, []Session) ObservationSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	snapshot := ObservationSnapshot{ObservedAt: time.Now(), Availability: ObservationAvailable}
	for _, reading := range e.readings {
		snapshot.Sessions = append(snapshot.Sessions, reading)
	}
	return snapshot
}

// report publishes what the runtime looks like now, the way the serving
// process publishes its own observation pass.
func (e *controlE2E) report(id SessionID, status AgentStatus, content string) {
	e.mu.Lock()
	e.readings[id] = SessionObservation{
		SessionID: id, Availability: ObservationAvailable, Presence: SessionPresencePresent,
		Status: status, Content: content, ContentKnown: content != "",
	}
	e.mu.Unlock()
	state, err := controlE2EState(e.service)
	if err != nil {
		e.t.Fatalf("Registry nicht lesbar: %v", err)
	}
	e.service.Observed(state.Agents, e.observe(context.Background(), nil))
}

// controlE2EState reads the Registry the service is a client of.
func controlE2EState(service *ControlService) (State, error) {
	snapshot, err := service.registry.Snapshot(context.Background())
	if err != nil {
		return State{}, err
	}
	return snapshot.State(), nil
}

func (e *controlE2E) call(request ControlRequest) ControlResponse {
	e.t.Helper()
	return e.client.Call(context.Background(), request)
}

func TestControlDelegationEndToEnd(t *testing.T) {
	harness := newControlE2E(t)

	started := harness.call(ControlRequest{ID: "start", Verb: ControlSessionStart, Args: ControlArgs{
		Project: "projekt", Name: "review", Vendor: AgentVendorClaude, NewWorktree: true,
		Prompt: "Prüfe die Änderungen.",
	}})
	if started.Outcome != ControlOK {
		t.Fatalf("Start = %q (%s)", started.Outcome, started.Message)
	}
	session := started.Result.SessionID
	worktree := started.Result.Worktree
	if worktree == "" {
		t.Fatalf("Der frische Worktree wurde nicht gemeldet: %+v", started.Result)
	}
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		t.Fatalf("Der Worktree wurde nicht angelegt: %v", err)
	}

	// The reviewer works and then finishes; the wait is fed from the same
	// observation passes the interfaces publish.
	harness.report(session, StatusRunning, "arbeitet")
	go func() {
		time.Sleep(50 * time.Millisecond)
		harness.report(session, StatusIdle, "Fertig: zwei Anmerkungen zum Fehlerpfad.")
	}()
	waited := harness.call(ControlRequest{ID: "wait", Verb: ControlSessionWait, Args: ControlArgs{
		Session: string(session), Until: "done", TimeoutMS: 5000,
	}})
	if waited.Outcome != ControlWaitDone {
		t.Fatalf("Warten = %q (%s)", waited.Outcome, waited.Message)
	}
	if waited.Result.Occupant == nil || waited.Result.Occupant.SessionID != session {
		t.Fatalf("Die gepinnte Belegung fehlt: %+v", waited.Result)
	}

	output := harness.call(ControlRequest{ID: "output", Verb: ControlSessionOutput, Args: ControlArgs{
		Session: string(session), Lines: 10,
	}})
	if output.Outcome != ControlOK || output.Result.Content == "" {
		t.Fatalf("Ausgabe = %q (%+v)", output.Outcome, output.Result)
	}

	killed := harness.call(ControlRequest{ID: "kill", Verb: ControlSessionKill, Args: ControlArgs{
		Session: string(session),
	}})
	if killed.Outcome != ControlOK || !killed.Result.Stopped {
		t.Fatalf("Beenden = %q (%+v)", killed.Outcome, killed.Result)
	}
	// kill removes no work.
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		t.Fatalf("Der Worktree überlebte das Beenden nicht: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err != nil {
		t.Fatalf("Der Checkout im Worktree überlebte das Beenden nicht: %v", err)
	}
}

func TestControlWaitEndsOccupantReplacedEndToEnd(t *testing.T) {
	harness := newControlE2E(t)
	started := harness.call(ControlRequest{ID: "start", Verb: ControlSessionStart, Args: ControlArgs{
		Project: "projekt", Name: "review", Vendor: AgentVendorClaude, NewWorktree: true,
	}})
	if started.Outcome != ControlOK {
		t.Fatalf("Start = %q (%s)", started.Outcome, started.Message)
	}
	session := started.Result.SessionID
	harness.report(session, StatusRunning, "arbeitet")

	state, err := controlE2EState(harness.service)
	if err != nil {
		t.Fatal(err)
	}
	original := state.SessionByID(session)
	if original == nil {
		t.Fatal("die gestartete Session fehlt in der Registry")
	}

	// While the wait is pending the awaited Session is removed and a new one is
	// registered under the same name in the same Project.
	go func() {
		time.Sleep(80 * time.Millisecond)
		replacement := *original
		replacement.ID = SessionID(NewUUID())
		replacement.RuntimeName = original.RuntimeName + "-neu"
		replacement.AgentRuns = []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: NewUUID()}}
		if _, err := harness.service.registry.Change(context.Background(), RemoveSession(original.ID, original.Name)); err != nil {
			t.Errorf("Entfernen fehlgeschlagen: %v", err)
			return
		}
		if _, err := harness.service.registry.Change(context.Background(), RegisterSession(replacement)); err != nil {
			t.Errorf("Neuanlage fehlgeschlagen: %v", err)
			return
		}
		// The replacement is idle right away — it must not satisfy the wait.
		harness.report(replacement.ID, StatusIdle, "fertig")
		harness.report(session, StatusIdle, "fertig")
	}()

	waited := harness.call(ControlRequest{ID: "wait", Verb: ControlSessionWait, Args: ControlArgs{
		Session: string(session), Until: "done", TimeoutMS: 5000,
	}})
	if waited.Outcome != ControlWaitOccupantReplaced {
		t.Fatalf("Warten = %q (%s)", waited.Outcome, waited.Message)
	}
	if waited.Result.Observed == nil || waited.Result.Observed.SessionID == session {
		t.Fatalf("Die beobachtete Belegung wurde nicht genannt: %+v", waited.Result)
	}
}
