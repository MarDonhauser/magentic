package core

import (
	"reflect"
	"testing"
	"time"
)

func TestObservationSessionsAssignsCopyOnlyFixtureIDs(t *testing.T) {
	fixtures := []Session{{Name: "one"}, {Name: "two"}}
	before := append([]Session(nil), fixtures...)

	prepared := observationSessions(fixtures)
	if !reflect.DeepEqual(fixtures, before) {
		t.Fatalf("fixture Sessions were mutated: before=%#v after=%#v", before, fixtures)
	}
	if prepared[0].ID == "" || prepared[1].ID == "" || prepared[0].ID == prepared[1].ID {
		t.Fatalf("copy-only IDs are not usable: %#v", prepared)
	}
}

// AgentStatus wird als Zahl in die Desktop-Oberfläche serialisiert. StatusDone
// hängt hinten an, damit keine bereits gespeicherte Session umnummeriert wird.
func TestStatusDoneIsAppendedAfterStatusTerm(t *testing.T) {
	existing := map[AgentStatus]int{
		StatusUnknown: 0, StatusRunning: 1, StatusAgents: 2, StatusShell: 3,
		StatusBlocked: 4, StatusIdle: 5, StatusExited: 6, StatusDead: 7, StatusTerm: 8,
	}
	for status, want := range existing {
		if int(status) != want {
			t.Fatalf("%v = %d, want %d", status.Label(), int(status), want)
		}
	}
	if int(StatusDone) != 9 {
		t.Fatalf("StatusDone = %d, want 9", int(StatusDone))
	}
	if StatusDone.Label() != "fertig" || StatusDone.Icon() == "?" {
		t.Fatalf("StatusDone hat keine eigene Darstellung: %q %q", StatusDone.Label(), StatusDone.Icon())
	}
}

// done gegen idle: derselbe ruhende Bildschirm, einmal seit der letzten
// Aktivität gesehen und einmal nicht.
func TestDoneIsDerivedFromRestingScreenAndLastLook(t *testing.T) {
	seen := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	resting := "❯ \n  🌿 main"
	unseen := resolveSessionStatus(statusInput{
		session: Session{ID: "s", SeenAt: seen}, present: true, paneCommand: "2.1.241",
		content: resting, contentKnown: true,
		activity: seen.Add(time.Minute), activityKnown: true, now: seen.Add(2 * time.Minute),
	})
	if unseen.Status != StatusDone {
		t.Fatalf("ungesehene Runde = %v, want fertig", unseen.Status.Label())
	}
	looked := resolveSessionStatus(statusInput{
		session: Session{ID: "s", SeenAt: seen.Add(2 * time.Minute)}, present: true, paneCommand: "2.1.241",
		content: resting, contentKnown: true,
		activity: seen.Add(time.Minute), activityKnown: true, now: seen.Add(3 * time.Minute),
	})
	if looked.Status != StatusIdle {
		t.Fatalf("gesehene Runde = %v, want idle", looked.Status.Label())
	}
}

func TestObservationNamesItsStatusSource(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	absent := resolveSessionStatus(statusInput{session: Session{ID: "s"}, present: false, now: now})
	if absent.Status != StatusDead || absent.Source != StatusSourcePresence {
		t.Fatalf("fehlende Laufzeit = %v aus %q", absent.Status.Label(), absent.Source)
	}
	snapshot := resolveSessionStatus(statusInput{
		session: Session{ID: "s"}, present: true, paneCommand: "2.1.241",
		content: "✳ Puttering… (esc to interrupt)", contentKnown: true, now: now,
	})
	if snapshot.Status != StatusRunning || snapshot.Source != StatusSourceSnapshot {
		t.Fatalf("Bildschirm = %v aus %q", snapshot.Status.Label(), snapshot.Source)
	}
	unreadable := resolveSessionStatus(statusInput{
		session: Session{ID: "s"}, present: true, paneCommand: "2.1.241", now: now,
	})
	if unreadable.Status != StatusUnknown || unreadable.Source != StatusSourceNone {
		t.Fatalf("unlesbarer Pane = %v aus %q", unreadable.Status.Label(), unreadable.Source)
	}
}

// Unbekannt bleibt unbekannt: es zählt nicht als idle, es weckt keine
// Aufmerksamkeit, die etwas behauptet, und es bekommt keine Eingabe.
func TestUnknownIsExplicitAndFailClosed(t *testing.T) {
	if got := observationAttention(StatusUnknown); got != AttentionUnknown {
		t.Fatalf("Aufmerksamkeit = %q, want %q", got, AttentionUnknown)
	}
	if observationUnread(StatusUnknown, time.Time{}, time.Now(), true) {
		t.Fatal("ein unbekannter Status wurde als ungelesen gezählt")
	}
	observed := SessionObservation{
		SessionID: "s", Availability: ObservationAvailable, Presence: SessionPresencePresent,
		ContentKnown: true, Tool: AgentToolClaude, Status: StatusUnknown,
		Content: "ein fremder Bildschirm",
	}
	if err := validatePromptTargetObservation("eins", promptTargetObservationFromSession(observed)); err == nil {
		t.Fatal("ein Prompt wurde für einen unbekannten Status freigegeben")
	}
}

// done ist eine eigene Aufmerksamkeit und ein eigener Zählschlüssel.
func TestDoneIsVisibleToConsumers(t *testing.T) {
	if got := observationAttention(StatusDone); got != AttentionReview {
		t.Fatalf("Aufmerksamkeit für fertig = %q, want %q", got, AttentionReview)
	}
	if got := statusKey(StatusDone); got != "done" {
		t.Fatalf("statusKey(fertig) = %q, want \"done\"", got)
	}
	if !agentAlive(StatusDone) {
		t.Fatal("eine fertige Session wurde als nicht mehr lebend gezählt")
	}
}
