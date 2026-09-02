package core

import (
	"context"
	"testing"
	"time"
)

func controlEventSessions() []Session {
	return []Session{
		{ID: "session-a", Name: "eins", ProjectID: "projekt-a", RuntimeName: "mgt-eins"},
		{ID: "session-b", Name: "zwei", ProjectID: "projekt-b", RuntimeName: "mgt-zwei"},
	}
}

func controlEventPass(at time.Time, readings ...SessionObservation) ObservationSnapshot {
	return ObservationSnapshot{ObservedAt: at, Availability: ObservationAvailable, Sessions: readings}
}

func controlEventReading(id SessionID, status AgentStatus, availability ObservationAvailability) SessionObservation {
	return SessionObservation{SessionID: id, Availability: availability, Presence: SessionPresencePresent, Status: status}
}

func TestControlEventsEmitOnlyRealChanges(t *testing.T) {
	events := NewControlEvents()
	sessions := controlEventSessions()
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

	first := events.Publish(sessions, controlEventPass(now,
		controlEventReading("session-a", StatusRunning, ObservationAvailable),
		controlEventReading("session-b", StatusRunning, ObservationAvailable),
	))
	if len(first) != 2 {
		t.Fatalf("erste Beobachtung = %d Ereignisse, want 2", len(first))
	}

	unchanged := events.Publish(sessions, controlEventPass(now.Add(time.Second),
		controlEventReading("session-a", StatusRunning, ObservationAvailable),
		controlEventReading("session-b", StatusRunning, ObservationAvailable),
	))
	if len(unchanged) != 0 {
		t.Fatalf("unveränderte Beobachtung = %+v, want keine Ereignisse", unchanged)
	}

	changed := events.Publish(sessions, controlEventPass(now.Add(2*time.Second),
		controlEventReading("session-a", StatusIdle, ObservationAvailable),
		controlEventReading("session-b", StatusRunning, ObservationAvailable),
	))
	if len(changed) != 1 {
		t.Fatalf("eine Änderung = %d Ereignisse, want 1", len(changed))
	}
	event := changed[0]
	if event.SessionID != "session-a" || event.PreviousStatus != ControlStatusRunning || event.Status != ControlStatusIdle {
		t.Fatalf("Ereignis = %+v", event)
	}
	if event.ProjectID != "projekt-a" || event.RuntimeName != "mgt-eins" {
		t.Fatalf("Ereignis nennt Projekt und Runtime nicht: %+v", event)
	}

	// An availability change is its own event and carries no concrete status.
	unreadable := events.Publish(sessions, controlEventPass(now.Add(3*time.Second),
		controlEventReading("session-a", StatusIdle, ObservationUnavailable),
		controlEventReading("session-b", StatusRunning, ObservationAvailable),
	))
	if len(unreadable) != 1 {
		t.Fatalf("Verfügbarkeitswechsel = %d Ereignisse, want 1", len(unreadable))
	}
	if unreadable[0].Availability != ObservationUnavailable || unreadable[0].Status != "" {
		t.Fatalf("unlesbare Beobachtung wurde als Zustand gemeldet: %+v", unreadable[0])
	}
	if unreadable[0].PreviousAvailability != ObservationAvailable {
		t.Fatalf("vorherige Verfügbarkeit = %q", unreadable[0].PreviousAvailability)
	}
}

func TestControlEventsFilterAndRelease(t *testing.T) {
	events := NewControlEvents()
	sessions := controlEventSessions()
	now := time.Now()
	byProject := events.Subscribe(ControlEventFilter{ProjectID: "projekt-a"})
	bySession := events.Subscribe(ControlEventFilter{SessionID: "session-b"})

	events.Publish(sessions, controlEventPass(now,
		controlEventReading("session-a", StatusRunning, ObservationAvailable),
		controlEventReading("session-b", StatusRunning, ObservationAvailable),
	))
	event := <-byProject.Events()
	if event.SessionID != "session-a" {
		t.Fatalf("Projektfilter lieferte %q", event.SessionID)
	}
	if len(byProject.Events()) != 0 {
		t.Fatal("der Projektfilter ließ ein fremdes Ereignis durch")
	}
	event = <-bySession.Events()
	if event.SessionID != "session-b" {
		t.Fatalf("Sessionfilter lieferte %q", event.SessionID)
	}

	events.Release(byProject)
	if _, open := <-byProject.Events(); open {
		t.Fatal("die freigegebene Anmeldung liefert weiter Ereignisse")
	}
	events.Publish(sessions, controlEventPass(now.Add(time.Second),
		controlEventReading("session-a", StatusIdle, ObservationAvailable),
	))
	if byProject.Stalled() {
		t.Fatal("die freigegebene Anmeldung gilt fälschlich als überlaufen")
	}
}

func TestControlEventsDropStalledSubscriber(t *testing.T) {
	events := NewControlEvents()
	sessions := controlEventSessions()
	stalled := events.Subscribe(ControlEventFilter{SessionID: "session-a"})
	reading := events.Subscribe(ControlEventFilter{SessionID: "session-a"})

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range reading.Events() {
		}
	}()

	now := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < controlEventBuffer+5; i++ {
			status := StatusRunning
			if i%2 == 1 {
				status = StatusIdle
			}
			events.Publish(sessions, controlEventPass(now.Add(time.Duration(i)*time.Second),
				controlEventReading("session-a", status, ObservationAvailable),
			))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("die Beobachtung wurde von einem nicht lesenden Abonnenten blockiert")
	}
	if !stalled.Stalled() {
		t.Fatal("der nicht lesende Abonnent wurde nicht beendet")
	}
	if _, open := <-stalled.Events(); open {
		// The buffered events are still readable; the channel closes after them.
		for range stalled.Events() {
		}
	}
	// The subscriber that keeps reading is unaffected.
	if reading.Stalled() {
		t.Fatal("ein lesender Abonnent wurde mitbeendet")
	}
	events.Release(reading)
	<-drained
}

func TestControlWaitIsDrivenByObservationEvents(t *testing.T) {
	service, _, _ := controlTestService(controlWaitState())
	service.events = NewControlEvents()
	service.observations = func(_ context.Context, session Session) (<-chan ObservationSnapshot, func()) {
		return service.events.Observations(session.ID)
	}
	service.observe = func(context.Context, []Session) ObservationSnapshot {
		return controlEventPass(time.Now(), controlEventReading("session-w", StatusRunning, ObservationAvailable))
	}
	sessions := []Session{controlWaitSession()}
	go func() {
		time.Sleep(20 * time.Millisecond)
		service.Observed(sessions, controlEventPass(time.Now(),
			controlEventReading("session-w", StatusRunning, ObservationAvailable)))
		time.Sleep(20 * time.Millisecond)
		service.Observed(sessions, controlEventPass(time.Now(),
			controlEventReading("session-w", StatusIdle, ObservationAvailable)))
	}()
	response := service.Dispatch(context.Background(), ControlRequest{
		Verb: ControlSessionWait, Args: ControlArgs{Session: "session-w", Until: "done", TimeoutMS: 3000},
	})
	if response.Outcome != ControlWaitDone {
		t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
	}
}
