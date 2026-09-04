package remote

import (
	"fmt"
	"time"

	"magentic/core"
)

// RemoteView trägt Host-Fakten so, dass sie ohne ihre Verfügbarkeit nicht
// erreichbar sind: payload und Alter sind unexportiert; wer liest, geht
// durch Fresh oder LastKnown. Ein direkter Griff (view.payload) kompiliert
// außerhalb dieses Pakets nicht — testdata/blindread beweist es, indem es
// nicht baut.
type RemoteView[T any] struct {
	payload      T
	availability core.ObservationAvailability
	age          time.Duration
	problem      string
}

// KnownView trägt frische bekannte Fakten.
func KnownView[T any](payload T) RemoteView[T] {
	return RemoteView[T]{payload: payload, availability: core.ObservationAvailable}
}

// LastKnownView trägt die letzte bekannte Sicht mit Alter und Grund.
func LastKnownView[T any](payload T, age time.Duration, problem string) RemoteView[T] {
	return RemoteView[T]{payload: payload, availability: core.ObservationUnavailable, age: age, problem: problem}
}

// Fresh liefert die Nutzlast nur, wenn sie frisch bekannt ist.
func (v RemoteView[T]) Fresh() (T, bool) {
	if v.availability == core.ObservationAvailable {
		return v.payload, true
	}
	var zero T
	return zero, false
}

// LastKnown liefert die letzte bekannte Sicht mit Alter — nie als frisch,
// nie als leer.
func (v RemoteView[T]) LastKnown() (payload T, age time.Duration, known bool) {
	return v.payload, v.age, true
}

// Availability nennt die Verfügbarkeit ohne die Nutzlast zu berühren.
func (v RemoteView[T]) Availability() core.ObservationAvailability {
	return v.availability
}

// Header etikettiert jede Sicht ehrlich: aktuell oder letztbekannt mit
// Alter. Kein View rendert ohne diese Zeile.
func (v RemoteView[T]) Header(host string) string {
	if v.availability == core.ObservationAvailable {
		return "Aktuell · " + host
	}
	age := "unbekanntes Alter"
	if v.age >= 0 {
		age = "vor " + formatAge(v.age)
	}
	reason := ""
	if v.problem != "" {
		reason = " — " + v.problem
	}
	return "Letztbekannt · " + age + reason
}

func formatAge(age time.Duration) string {
	seconds := int(age.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%dm", minutes/60, minutes%60)
}

// SessionDigest ist, was die Sidebar je Session zeigt — ohne je Abwesenheit
// zu erfinden.
type SessionDigest struct {
	ID     string
	Name   string
	Status string
}

// SidebarSessions rendert die Seitenleiste: verbunden die frische Liste,
// getrennt die letzte bekannte mit Etikett. Nie leer statt unbekannt, nie
// „beendet/tot/idle" aus einem Abriss.
func SidebarSessions(view RemoteView[[]SessionDigest], host string) (lines []string, notice string) {
	if fresh, ok := view.Fresh(); ok {
		for _, session := range fresh {
			lines = append(lines, session.Name+" · "+session.Status)
		}
		return lines, ""
	}
	payload, _, _ := view.LastKnown()
	for _, session := range payload {
		lines = append(lines, session.Name+" · "+session.Status)
	}
	return lines, view.Header(host)
}

// BoardColumns rendert das Board: getrennt heißt Spalten unbekannt, nicht
// leere Spalten als Befund.
func BoardColumns(view RemoteView[map[string][]string], host string) (columns map[string][]string, notice string) {
	if fresh, ok := view.Fresh(); ok {
		return fresh, ""
	}
	payload, _, _ := view.LastKnown()
	return payload, view.Header(host) + ": Spaltenstand unbekannt"
}

// StatsTotals rendert Statistik: getrennt keine Nullsummen als Befund.
func StatsTotals(view RemoteView[map[string]int], host string) (totals map[string]int, notice string) {
	if fresh, ok := view.Fresh(); ok {
		return fresh, ""
	}
	payload, _, _ := view.LastKnown()
	return payload, view.Header(host) + ": Summen unbekannt"
}

// RequireFreshFacts ist das Client-Gate für alles, was Arbeit entfernt,
// überschreibt, mergt oder beendet: frisch bekannte Fakten oder Blockade mit
// Begründung. Dieselbe Regel wie lokal (core.RequireFreshObservation, D4) —
// keine parallele Regel.
func RequireFreshFacts(snapshot core.ObservationSnapshot) error {
	return core.RequireFreshObservation(snapshot)
}

// ActionAvailable sagt, ob der Client eine Aktion anbietet: Policy-Stand plus
// Frische-Gate für Destruktives. Host-Verweigerung bleibt maßgeblich
// (Client.noteWireError).
func ActionAvailable(policy map[string]PolicyEntry, method string, fresh bool) (bool, string) {
	entry, known := policy[method]
	if !known {
		if configured, ok := Classify(method); ok {
			entry = configured
		} else {
			return false, "unbekannte Aktion — fail-closed"
		}
	}
	if entry.Class == ActionRestricted {
		return false, "Host beschränkt diese Aktion: " + entry.Reason
	}
	if !fresh && destructiveMethod(method) {
		return false, "braucht frische Fakten vom Host — derzeit nur letztbekannt"
	}
	return true, ""
}

func destructiveMethod(method string) bool {
	switch method {
	case "RemoveWorktree", "RemoveProject", "KillSession",
		"LaterSession", "DiscardSession", "Merge", "Deploy", "Cleanup":
		return true
	default:
		return false
	}
}
