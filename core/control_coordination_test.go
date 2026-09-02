package core

import (
	"context"
	"sync"
	"testing"
	"time"
)

// controlCoordinatedService wires the control surface onto a real Registry and
// a real Session Lifecycle, so its mutations take the same interprocess
// coordination the interfaces take.
func controlCoordinatedService(t *testing.T) (*ControlService, *SessionLifecycle, *fakeLifecycleRuntime, *Registry) {
	t.Helper()
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	service := &ControlService{
		registry: registry, lifecycle: lifecycle,
		repositories: controlWorktreeRepositories(),
		observe:      controlObserver(),
		installed:    func(AgentProvider) bool { return true },
		events:       NewControlEvents(),
		now:          time.Now,
	}
	service.deliver = service.deliverThroughOutbox
	return service, lifecycle, runtime, registry
}

func TestControlMutationAndInterfaceActionAreCoordinated(t *testing.T) {
	service, _, runtime, registry := controlCoordinatedService(t)
	project := registerLifecycleProject(t, registry)
	session := registerLifecycleSession(t, registry, runtime, Session{
		Name: "hera", ProjectID: project.ID, Project: project.Name, Dir: project.Path,
		RuntimeName: "mgt-hera", Vendor: AgentVendorClaude,
	}, true)

	seen := time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)
	var group sync.WaitGroup
	group.Add(2)
	// A control mutation and an interface action on the same Session at the
	// same time: both are semantic changes, so neither discards the other.
	go func() {
		defer group.Done()
		response := service.Dispatch(context.Background(), ControlRequest{
			Verb: ControlSessionSend, Args: ControlArgs{Session: string(session.ID), Text: "weiter"},
		})
		if response.Outcome != ControlOK {
			t.Errorf("Steuer-Mutation = %q (%s)", response.Outcome, response.Message)
		}
	}()
	go func() {
		defer group.Done()
		if _, err := registry.Change(context.Background(), MarkSessionSeen(session.ID, session.Name, seen)); err != nil {
			t.Errorf("Oberflächen-Aktion: %v", err)
		}
	}()
	group.Wait()

	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Registry nicht lesbar: %v", err)
	}
	state := snapshot.State()
	current := state.SessionByID(session.ID)
	if current == nil {
		t.Fatal("die Session ging verloren")
	}
	if len(current.Outbox) != 1 || current.Outbox[0].Text != "weiter" {
		t.Fatalf("die Steuer-Mutation ging verloren: %+v", current.Outbox)
	}
	if !current.SeenAt.Equal(seen) {
		t.Fatalf("die Oberflächen-Aktion ging verloren: %v", current.SeenAt)
	}
}

func TestControlReadsAnswerDuringLongMutation(t *testing.T) {
	service, lifecycle, runtime, registry := controlCoordinatedService(t)
	project := registerLifecycleProject(t, registry)
	session := registerLifecycleSession(t, registry, runtime, Session{
		Name: "hera", ProjectID: project.ID, Project: project.Name, Dir: project.Path,
		RuntimeName: "mgt-hera", Vendor: AgentVendorClaude,
	}, true)
	service.observe = controlObserver(SessionObservation{
		SessionID: session.ID, Availability: ObservationAvailable, Presence: SessionPresencePresent,
		Status: StatusRunning, Content: "arbeitet", ContentKnown: true,
	})

	// The transition is held for as long as the runtime takes to stop.
	holding := make(chan struct{})
	release := make(chan struct{})
	runtime.onStop = func(Session) {
		close(holding)
		<-release
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		lifecycle.Park(context.Background(), session.ID, session.Name)
	}()
	<-holding

	answered := make(chan ControlOutcome, 2)
	go func() {
		answered <- service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionList}).Outcome
	}()
	go func() {
		answered <- service.Dispatch(context.Background(), ControlRequest{
			Verb: ControlSessionOutput, Args: ControlArgs{Session: string(session.ID)},
		}).Outcome
	}()
	for i := 0; i < 2; i++ {
		select {
		case outcome := <-answered:
			if outcome != ControlOK {
				t.Fatalf("Leseanfrage = %q", outcome)
			}
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatal("eine Leseanfrage wartete auf die laufende Mutation")
		}
	}
	close(release)
	<-done
}
