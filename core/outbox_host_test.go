package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeHostOutbox replaces the host Seam and records what the Outbox did.
type fakeHostOutbox struct {
	record    ManagedHostRecord
	state     AgentHostState
	delivered []string
	changes   []registryChangeKind
}

func installFakeHostOutbox(t *testing.T, fake *fakeHostOutbox) {
	t.Helper()
	previous := hostOutbox
	hostOutbox.record = func(SessionID) (ManagedHostRecord, bool, error) { return fake.record, true, nil }
	hostOutbox.state = func(ManagedHostRecord) (AgentHostState, error) { return fake.state, nil }
	hostOutbox.deliver = func(_ ManagedHostRecord, id, _ string) error {
		fake.delivered = append(fake.delivered, id)
		return nil
	}
	hostOutbox.change = func(_ context.Context, change RegistryChange) (RegistryChangeResult, error) {
		fake.changes = append(fake.changes, change.kind)
		return RegistryChangeResult{Applied: true}, nil
	}
	t.Cleanup(func() {
		hostOutbox = previous
		ownOutboxAttempts = &outboxAttempts{ids: map[string]bool{}}
	})
}

func ompOutboxSession(attempted bool) Session {
	message := QueuedMessage{ID: "msg-1", Text: "hallo"}
	if attempted {
		message.AttemptedAt = time.Now()
	}
	return Session{ID: "session-o", Name: "omp", Runtime: RuntimeOmp, Vendor: AgentVendorOmp,
		SessionKind: SessionKindCodingAgent, Outbox: []QueuedMessage{message}}
}

func provenOmpHost(t *testing.T) (ManagedHostRecord, AgentHostState) {
	argv, err := OmpArgv(Session{Dir: "/p"}, nil, "new")
	if err != nil {
		t.Fatal(err)
	}
	return ManagedHostRecord{SessionID: "session-o", LaunchArgv: argv},
		AgentHostState{SessionID: "session-o", Alive: true, LaunchArgv: argv}
}

var idleObservation = SessionObservation{SessionID: "session-o", Availability: ObservationAvailable, Status: StatusIdle}

// 2.9: a Session whose launch provenance is not established is never
// delivered to, and its message stays queued.
func TestHostOutboxRefusesWithoutProvenance(t *testing.T) {
	record, state := provenOmpHost(t)
	state.LaunchArgv = []string{"--mode", "rpc-ui"}
	fake := &fakeHostOutbox{record: record, state: state}
	installFakeHostOutbox(t, fake)

	dispatchHostOutboxHead(context.Background(), ompOutboxSession(false), idleObservation)
	if len(fake.delivered) != 0 || len(fake.changes) != 0 {
		t.Fatalf("delivered %v, changes %v: nothing may happen without provenance", fake.delivered, fake.changes)
	}
}

// 3.8: sending marks an attempt but does not dequeue; the message leaves the
// Outbox only when the host shows the turn its echo opened.
func TestHostOutboxDequeuesOnlyOnTheEcho(t *testing.T) {
	record, state := provenOmpHost(t)
	fake := &fakeHostOutbox{record: record, state: state}
	installFakeHostOutbox(t, fake)

	dispatchHostOutboxHead(context.Background(), ompOutboxSession(false), idleObservation)
	if len(fake.delivered) != 1 || len(fake.changes) != 1 || fake.changes[0] != registryMarkMessageAttempt {
		t.Fatalf("delivered %v, changes %v: want one send and only the attempt marked", fake.delivered, fake.changes)
	}

	fake.state.Inflight = ManagedInflight{MessageID: "msg-1"}
	dispatchHostOutboxHead(context.Background(), ompOutboxSession(true), idleObservation)
	if len(fake.changes) != 1 {
		t.Fatalf("changes %v: an unacknowledged prompt must stay queued", fake.changes)
	}

	fake.state.Inflight = ManagedInflight{}
	fake.state.Turn, fake.state.TurnKnown = ManagedTurn{MessageID: "msg-1", Running: true}, true
	dispatchHostOutboxHead(context.Background(), ompOutboxSession(true), idleObservation)
	if len(fake.changes) != 2 || fake.changes[1] != registryDequeueMessage {
		t.Fatalf("changes %v: want the message dequeued on its echo", fake.changes)
	}
	if len(fake.delivered) != 1 {
		t.Fatalf("delivered %v: nothing may be resent", fake.delivered)
	}
}

// 3.8: a prompt the process refused stays queued with its attempt reset.
func TestHostOutboxKeepsARefusedPromptQueued(t *testing.T) {
	record, state := provenOmpHost(t)
	state.FailedMessageID, state.DeliveryFailure = "msg-1", "Agent is already processing."
	fake := &fakeHostOutbox{record: record, state: state}
	installFakeHostOutbox(t, fake)

	dispatchHostOutboxHead(context.Background(), ompOutboxSession(true), idleObservation)
	if len(fake.changes) != 1 || fake.changes[0] != registryResetMessageAttempt {
		t.Fatalf("changes %v: want the attempt reset, never a dequeue", fake.changes)
	}
}

// Delivery waits for a ready Session and never sends into a running turn,
// an open decision, or an unknown status.
func TestHostOutboxWaitsForAReadySession(t *testing.T) {
	record, state := provenOmpHost(t)
	for _, status := range []AgentStatus{StatusRunning, StatusAwaitingDecision, StatusUnknown} {
		fake := &fakeHostOutbox{record: record, state: state}
		installFakeHostOutbox(t, fake)
		observed := idleObservation
		observed.Status = status
		dispatchHostOutboxHead(context.Background(), ompOutboxSession(false), observed)
		if len(fake.delivered) != 0 {
			t.Fatalf("status %v: delivered %v, want nothing", status.Label(), fake.delivered)
		}
	}
}

// A failed send leaves the message queued with the attempt reset.
func TestHostOutboxResetsTheAttemptWhenTheSendFails(t *testing.T) {
	record, state := provenOmpHost(t)
	fake := &fakeHostOutbox{record: record, state: state}
	installFakeHostOutbox(t, fake)
	hostOutbox.deliver = func(ManagedHostRecord, string, string) error { return errors.New("socket weg") }

	dispatchHostOutboxHead(context.Background(), ompOutboxSession(false), idleObservation)
	if len(fake.changes) != 2 || fake.changes[1] != registryResetMessageAttempt {
		t.Fatalf("changes %v: want attempt marked then reset", fake.changes)
	}
}

// 3.7: an unobservable Session — its host unreachable — is never delivered to.
func TestHostOutboxRefusesAnUnobservableSession(t *testing.T) {
	record, state := provenOmpHost(t)
	fake := &fakeHostOutbox{record: record, state: state}
	installFakeHostOutbox(t, fake)
	observed := SessionObservation{SessionID: "session-o", Availability: ObservationUnavailable, Status: StatusUnknown}
	dispatchHostOutboxHead(context.Background(), ompOutboxSession(false), observed)
	if len(fake.delivered) != 0 || len(fake.changes) != 0 {
		t.Fatalf("delivered %v, changes %v: want nothing", fake.delivered, fake.changes)
	}
}
