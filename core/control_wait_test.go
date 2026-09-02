package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func controlWaitSession() Session {
	return Session{
		ID: "session-w", Name: "review", ProjectID: "projekt-a", Project: "alpha",
		RuntimeName: "mgt-review", Dir: "/tmp/alpha", Vendor: AgentVendorClaude,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-a"}},
	}
}

func controlWaitState() State {
	return State{
		Projects: []Project{{ID: "projekt-a", Name: "alpha", Path: "/tmp/alpha"}},
		Agents:   []Session{controlWaitSession()},
	}
}

func controlPinnedWait(until ControlOutcome) *controlWait {
	session := controlWaitSession()
	pinned, failure := resolveControlOccupant(session)
	if failure != nil {
		panic(failure.Message)
	}
	return &controlWait{pinned: pinned, name: session.Name, projectID: session.ProjectID, until: until}
}

func controlReading(status AgentStatus, availability ObservationAvailability) SessionObservation {
	return SessionObservation{
		SessionID: "session-w", Availability: availability, Presence: SessionPresencePresent, Status: status,
	}
}

func TestResolveControlOccupantPinsTheTriple(t *testing.T) {
	occupant, failure := resolveControlOccupant(controlWaitSession())
	if failure != nil {
		t.Fatalf("Belegung nicht auflösbar: %s", failure.Message)
	}
	want := ControlOccupant{
		SessionID: "session-w", RuntimeName: "mgt-review",
		Run: AgentRunRef{Vendor: AgentVendorClaude, ExternalID: "run-a"},
	}
	if !occupant.Same(want) {
		t.Fatalf("Belegung = %+v, want %+v", occupant, want)
	}
}

func TestResolveControlOccupantRefusesWithoutRun(t *testing.T) {
	tests := []struct {
		name    string
		session Session
	}{
		{"Terminal-Session", Session{ID: "s", Name: "shell", SessionKind: SessionKindTerminal, RuntimeName: "mgt-shell"}},
		{"ohne Runtime", Session{ID: "s", Name: "leer", Vendor: AgentVendorClaude}},
		{"ohne Agent-Lauf", Session{ID: "s", Name: "leer", RuntimeName: "mgt-leer", Vendor: AgentVendorClaude}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, failure := resolveControlOccupant(test.session)
			if failure == nil {
				t.Fatal("eine Belegung wurde erfunden")
			}
			if failure.Outcome != ControlWaitNoOccupant {
				t.Fatalf("Ergebnis = %q, want %q", failure.Outcome, ControlWaitNoOccupant)
			}
		})
	}
}

func TestControlWaitConditionsFromObservation(t *testing.T) {
	tests := []struct {
		name    string
		until   ControlOutcome
		reading SessionObservation
		want    ControlOutcome
		ended   bool
	}{
		{"fertig", ControlWaitDone, controlReading(StatusIdle, ObservationAvailable), ControlWaitDone, true},
		{"fertig gemeldet", ControlWaitDone, controlReading(StatusDone, ObservationAvailable), ControlWaitDone, true},
		{"läuft noch", ControlWaitDone, controlReading(StatusRunning, ObservationAvailable), "", false},
		{"Rückfrage bei done", ControlWaitDone, controlReading(StatusBlocked, ObservationAvailable), ControlWaitBlocked, true},
		{"Rückfrage erwartet", ControlWaitWaiting, controlReading(StatusBlocked, ObservationAvailable), ControlWaitWaiting, true},
		{"idle bei waiting", ControlWaitWaiting, controlReading(StatusIdle, ObservationAvailable), "", false},
		{"unlesbar", ControlWaitDone, controlReading(StatusIdle, ObservationUnavailable), "", false},
		{"teilweise lesbar", ControlWaitDone, controlReading(StatusIdle, ObservationPartial), "", false},
		{"teilweise lesbar bei waiting", ControlWaitWaiting, controlReading(StatusBlocked, ObservationPartial), "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wait := controlPinnedWait(test.until)
			verdict := wait.evaluate(controlWaitState(), test.reading, true)
			if verdict.Ended != test.ended || verdict.Outcome != test.want {
				t.Fatalf("Urteil = %+v, want %q (Ende %v)", verdict, test.want, test.ended)
			}
		})
	}
}

func TestControlWaitDetectsOccupantReplacement(t *testing.T) {
	replacedRuntime := controlWaitState()
	replacedRuntime.Agents[0].RuntimeName = "mgt-review-neu"

	replacedRun := controlWaitState()
	replacedRun.Agents[0].AgentRuns = []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-b"}}

	recreated := controlWaitState()
	recreated.Agents[0].ID = "session-neu"
	recreated.Agents[0].AgentRuns = []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-b"}}

	removed := controlWaitState()
	removed.Agents = nil

	tests := []struct {
		name    string
		state   State
		reading SessionObservation
		want    ControlOutcome
	}{
		{"neuer RuntimeName", replacedRuntime, controlReading(StatusIdle, ObservationAvailable), ControlWaitOccupantReplaced},
		{"anderer Agent-Lauf", replacedRun, controlReading(StatusIdle, ObservationAvailable), ControlWaitOccupantReplaced},
		{"neu angelegt unter demselben Namen", recreated, controlReading(StatusIdle, ObservationAvailable), ControlWaitOccupantReplaced},
		{"Session entfernt", removed, controlReading(StatusIdle, ObservationAvailable), ControlWaitSessionGone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wait := controlPinnedWait(ControlWaitDone)
			verdict := wait.evaluate(test.state, test.reading, true)
			if !verdict.Ended || verdict.Outcome != test.want {
				t.Fatalf("Urteil = %+v, want %q", verdict, test.want)
			}
			// A replacement reaching idle must never be reported as done.
			if verdict.Outcome == ControlWaitDone {
				t.Fatal("eine ersetzte Belegung hat das Warten erfüllt")
			}
		})
	}
}

func TestControlWaitReportsReplacementIdentities(t *testing.T) {
	state := controlWaitState()
	state.Agents[0].RuntimeName = "mgt-review-neu"
	wait := controlPinnedWait(ControlWaitDone)
	verdict := wait.evaluate(state, controlReading(StatusIdle, ObservationAvailable), true)
	response := wait.response("r1", verdict)
	if response.Result.Occupant.RuntimeName != "mgt-review" {
		t.Fatalf("gepinnte Belegung = %+v", response.Result.Occupant)
	}
	if response.Result.Observed == nil || response.Result.Observed.RuntimeName != "mgt-review-neu" {
		t.Fatalf("beobachtete Belegung = %+v", response.Result.Observed)
	}
	if !strings.Contains(response.Message, "mgt-review-neu") {
		t.Fatalf("Begründung nennt die beobachtete Belegung nicht: %q", response.Message)
	}
}

func TestControlWaitTerminalOutcomes(t *testing.T) {
	gone := controlReading(StatusDead, ObservationAvailable)
	gone.Presence = SessionPresenceAbsent
	wait := controlPinnedWait(ControlWaitDone)
	verdict := wait.evaluate(controlWaitState(), gone, true)
	if !verdict.Ended || verdict.Outcome != ControlWaitSessionGone {
		t.Fatalf("bestätigt verschwundener Runtime = %+v, want %q", verdict, ControlWaitSessionGone)
	}

	// Every terminal outcome is a member of the fixed set and appears once.
	seen := map[ControlOutcome]bool{}
	for _, outcome := range ControlWaitOutcomes() {
		if seen[outcome] {
			t.Fatalf("Warte-Ergebnis %q kommt doppelt vor", outcome)
		}
		seen[outcome] = true
	}
	for _, outcome := range []ControlOutcome{
		ControlWaitBlocked, ControlWaitSessionGone, ControlWaitTimeout, ControlWaitCancelled,
	} {
		if !seen[outcome] {
			t.Fatalf("%q fehlt im Satz der Warte-Ergebnisse", outcome)
		}
	}
}

// controlWaitService drives waits from an injected sequence of observation
// passes instead of a real runtime.
func controlWaitService(state State, passes ...ObservationSnapshot) (*ControlService, *controlFakeRegistry) {
	service, registry, _ := controlTestService(state)
	service.observe = func(context.Context, []Session) ObservationSnapshot {
		return ObservationSnapshot{Availability: ObservationAvailable, Sessions: []SessionObservation{
			controlReading(StatusRunning, ObservationAvailable),
		}}
	}
	service.observations = func(ctx context.Context, _ Session) (<-chan ObservationSnapshot, func()) {
		stream := make(chan ObservationSnapshot, len(passes))
		for _, pass := range passes {
			stream <- pass
		}
		return stream, func() {}
	}
	return service, registry
}

func controlPass(observations ...SessionObservation) ObservationSnapshot {
	return ObservationSnapshot{Availability: ObservationAvailable, Sessions: observations}
}

func TestControlWaitEndsOnObservedIdle(t *testing.T) {
	service, _ := controlWaitService(controlWaitState(),
		controlPass(controlReading(StatusRunning, ObservationAvailable)),
		controlPass(controlReading(StatusIdle, ObservationAvailable)),
	)
	response := service.Dispatch(context.Background(), ControlRequest{
		ID: "r1", Verb: ControlSessionWait, Args: ControlArgs{Session: "session-w", Until: "done", TimeoutMS: 2000},
	})
	if response.Outcome != ControlWaitDone {
		t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
	}
	if response.Result.Occupant == nil || response.Result.Occupant.Run.ExternalID != "run-a" {
		t.Fatalf("gepinnte Belegung fehlt: %+v", response.Result)
	}
}

func TestControlWaitTimesOutAndCancels(t *testing.T) {
	service, _ := controlWaitService(controlWaitState())
	response := service.Dispatch(context.Background(), ControlRequest{
		Verb: ControlSessionWait, Args: ControlArgs{Session: "session-w", TimeoutMS: 30},
	})
	if response.Outcome != ControlWaitTimeout {
		t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
	}
	if response.Result.Status != ControlStatusRunning {
		t.Fatalf("zuletzt beobachteter Zustand = %q", response.Result.Status)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	response = service.Dispatch(ctx, ControlRequest{Verb: ControlSessionWait, Args: ControlArgs{Session: "session-w"}})
	if response.Outcome != ControlWaitCancelled {
		t.Fatalf("Abbruch = %q (%s)", response.Outcome, response.Message)
	}
}

func TestControlWaitRefusesWithoutOccupant(t *testing.T) {
	state := controlWaitState()
	state.Agents = append(state.Agents, Session{
		ID: "session-term", Name: "shell", ProjectID: "projekt-a", Project: "alpha",
		SessionKind: SessionKindTerminal, RuntimeName: "mgt-shell",
	})
	service, _ := controlWaitService(state)
	started := time.Now()
	response := service.Dispatch(context.Background(), ControlRequest{
		Verb: ControlSessionWait, Args: ControlArgs{Session: "session-term", TimeoutMS: 5000},
	})
	if response.Outcome != ControlWaitNoOccupant {
		t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
	}
	if time.Since(started) > time.Second {
		t.Fatal("die Anfrage hat blockiert, statt sofort abzulehnen")
	}
}

func TestControlWaitHoldsNoCoordinationAndServesConcurrently(t *testing.T) {
	service, registry := controlWaitService(controlWaitState())
	// The wait is fed nothing, so it stays pending until its timeout.
	service.observations = func(ctx context.Context, _ Session) (<-chan ObservationSnapshot, func()) {
		stream := make(chan ObservationSnapshot)
		return stream, func() {}
	}
	var group sync.WaitGroup
	outcomes := make([]ControlOutcome, 3)
	for i := range outcomes {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			response := service.Dispatch(context.Background(), ControlRequest{
				Verb: ControlSessionWait, Args: ControlArgs{Session: "session-w", TimeoutMS: 200},
			})
			outcomes[index] = response.Outcome
		}(i)
	}
	// While the waits are pending, a read request is still served and the
	// Registry is not held by anyone.
	time.Sleep(30 * time.Millisecond)
	response := service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionList})
	if response.Outcome != ControlOK {
		t.Fatalf("Auflistung während laufender Waits = %q", response.Outcome)
	}
	if _, err := registry.Change(context.Background(), MarkSessionSeen("session-w", "review", time.Now())); err != nil {
		t.Fatalf("Registry-Änderung während laufender Waits: %v", err)
	}
	group.Wait()
	for i, outcome := range outcomes {
		if outcome != ControlWaitTimeout {
			t.Fatalf("Wait %d = %q, want %q", i, outcome, ControlWaitTimeout)
		}
	}
}
