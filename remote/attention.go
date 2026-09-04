package remote

import (
	"time"

	"magentic/core"
)

// ClientAttention lässt den AttentionPlanner auf der Maschine des
// Entwicklers laufen (D6): gefüttert aus gestreamten Observations und
// Status-Events des Hosts. Der Host meldet Fakten, keine Mitteilungen.
// Während die Verbindung unavailable ist, bekommt der Planner eine explizit
// unavailable Observation — niemals die letzte bekannte Sicht, aus der er
// sonst neue Pro-Session-Intents ableiten würde.
type ClientAttention struct {
	planner *core.AttentionPlanner
}

// NewClientAttention startet einen clientseitigen Planner.
func NewClientAttention() *ClientAttention {
	return &ClientAttention{planner: core.NewAttentionPlanner(core.AttentionPlannerConfig{})}
}

// PlanFromStream plant aus einem gestreamten Snapshot: bekannt → normale
// Planung auf dem Client; unbekannt → keine Pro-Session-Intents, stattdessen
// ist der Verbindungszustand die Auskunft.
func (a *ClientAttention) PlanFromStream(snapshot core.ObservationSnapshot, active core.SessionID, labels map[core.SessionID]string, now time.Time) core.AttentionPlan {
	if snapshot.Availability != core.ObservationAvailable {
		return a.planner.Plan(core.AttentionInput{
			Observation: core.ObservationSnapshot{Availability: core.ObservationUnavailable},
			Now:         now,
		})
	}
	return a.planner.Plan(core.AttentionInput{
		Observation:   snapshot,
		ActiveSession: active,
		SessionLabels: labels,
		Now:           now,
	})
}
