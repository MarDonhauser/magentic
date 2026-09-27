package core

import (
	"context"
	"time"
)

// hostOutbox is the Seam between the Outbox and an agent host. Production
// talks to the host over its recorded socket; tests replace the functions.
var hostOutbox = struct {
	record  func(SessionID) (ManagedHostRecord, bool, error)
	state   func(ManagedHostRecord) (AgentHostState, error)
	deliver func(ManagedHostRecord, string, string) error
	change  func(context.Context, RegistryChange) (RegistryChangeResult, error)
}{
	record: func(id SessionID) (ManagedHostRecord, bool, error) {
		return NewManagedHostRegistry().RecordFor(id)
	},
	state: func(record ManagedHostRecord) (AgentHostState, error) {
		return QueryAgentHostState(record.SocketPath, record.Token)
	},
	deliver: func(record ManagedHostRecord, messageID, text string) error {
		return DeliverAgentHostPrompt(record.SocketPath, record.Token, messageID, text)
	},
	change: func(ctx context.Context, change RegistryChange) (RegistryChangeResult, error) {
		return OpenRegistry(StatePath()).Change(ctx, change)
	},
}

// hostOutboxSettle bounds how long a kick waits for omp's echo, so a sender
// learns whether its message was delivered without a periodic tick.
var hostOutboxSettle = 30 * time.Second

// hostOutboxObserve observes one host-owned Session for a kick.
var hostOutboxObserve = func(ctx context.Context, session Session) SessionObservation {
	return ObserveSessions(ctx, []Session{session}).Sessions[0]
}

// kickHostOutbox tries the head of a host-owned Session's Outbox now and then
// keeps resolving it until omp echoed or refused it, or the settle time ran
// out. What it cannot resolve in time stays attempted and is resolved from
// the host's facts on a later pass.
func kickHostOutbox(ctx context.Context, session Session) {
	if len(session.Outbox) == 0 {
		return
	}
	head := session.Outbox[0].ID
	dispatchHostOutboxHead(ctx, session, hostOutboxObserve(ctx, session))
	if !ownOutboxAttempts.markedHere(head) {
		return
	}
	deadline := time.Now().Add(hostOutboxSettle)
	for time.Now().Before(deadline) && ownOutboxAttempts.markedHere(head) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
		st, err := LoadState()
		if err != nil {
			return
		}
		current := st.SessionByID(session.ID)
		if current == nil || len(current.Outbox) == 0 || current.Outbox[0].ID != head {
			ownOutboxAttempts.finish(head)
			return
		}
		dispatchHostOutboxHead(ctx, *current, hostOutboxObserve(ctx, *current))
	}
}

// dispatchHostOutboxHead advances the Outbox of a Session its agent host
// owns. A prompt leaves the Outbox only once the host's state shows the turn
// its echo opened; a refused prompt stays queued with the attempt reset; and
// nothing is sent to a host whose launch provenance is not established,
// because only a proven launch has the approval gate on.
func dispatchHostOutboxHead(ctx context.Context, session Session, observed SessionObservation) {
	if len(session.Outbox) == 0 || observed.Availability != ObservationAvailable {
		return
	}
	head := session.Outbox[0]
	record, found, err := hostOutbox.record(session.ID)
	if err != nil || !found {
		return
	}
	state, err := hostOutbox.state(record)
	if err != nil {
		return
	}
	if session.SessionRuntime() == RuntimeOmp && !hostProvenanceEstablished(record, state) {
		return
	}

	if !head.AttemptedAt.IsZero() {
		// The host remembers across a daemon restart what became of the
		// prompt, so an attempt from an earlier run is resolved from its
		// facts rather than left stuck.
		switch {
		case state.TurnKnown && state.Turn.MessageID == head.ID:
			if _, err := hostOutbox.change(ctx, DequeueSessionMessage(session.ID, session.Name, head.ID)); err != nil {
				Logf("Outbox %s: zugestellte Nachricht nicht entfernt: %v", session.Name, err)
				return
			}
			ownOutboxAttempts.finish(head.ID)
		case state.FailedMessageID == head.ID:
			if _, err := hostOutbox.change(ctx, ResetQueuedMessageAttempt(session.ID, session.Name, head.ID)); err != nil {
				Logf("Outbox %s: Zustellversuch nicht zurückgesetzt: %v", session.Name, err)
				return
			}
			ownOutboxAttempts.finish(head.ID)
			Logf("Outbox %s: omp lehnte die Nachricht ab: %s", session.Name, state.DeliveryFailure)
		}
		return
	}

	if observed.Status != StatusIdle && observed.Status != StatusDone {
		return
	}
	if state.Inflight.MessageID != "" {
		return
	}
	if !ownOutboxAttempts.begin(head.ID) {
		return
	}
	result, err := hostOutbox.change(ctx, MarkQueuedMessageAttempt(session.ID, session.Name, head.ID, time.Now()))
	if err != nil || !result.Applied {
		ownOutboxAttempts.finish(head.ID)
		return
	}
	if err := hostOutbox.deliver(record, head.ID, head.Text); err != nil {
		ownOutboxAttempts.finish(head.ID)
		if _, resetErr := hostOutbox.change(ctx, ResetQueuedMessageAttempt(session.ID, session.Name, head.ID)); resetErr != nil {
			Logf("Outbox %s: Zustellversuch nicht zurückgesetzt: %v", session.Name, resetErr)
		}
		Logf("Outbox %s: %v", session.Name, err)
	}
}

// hostProvenanceEstablished reports whether the host runs exactly the launch
// recorded for it, with the approval gate on. The host's identity is already
// confirmed by the recorded token the state was read with.
func hostProvenanceEstablished(record ManagedHostRecord, state AgentHostState) bool {
	if record.GateWithdrawn {
		return false
	}
	if len(record.LaunchArgv) == 0 || len(state.LaunchArgv) != len(record.LaunchArgv) {
		return false
	}
	for i := range record.LaunchArgv {
		if record.LaunchArgv[i] != state.LaunchArgv[i] {
			return false
		}
	}
	return OmpLaunchArgvHasGate(state.LaunchArgv)
}
