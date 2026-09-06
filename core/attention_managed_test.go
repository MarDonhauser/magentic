package core

import (
	"testing"
	"time"
)

// Eine verwaltete Session mit offener Freigabe plant ihre Aufmerksamkeit,
// bevor irgendeine Benachrichtigung emittiert wird (ADR 0007): der Intent
// steht im Plan, nicht erst in der Ausführung.
func TestAttentionPlansManagedPermissionBeforeNotify(t *testing.T) {
	start := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	planner := NewAttentionPlanner(AttentionPlannerConfig{Now: func() time.Time { return start }})
	working := SessionObservation{
		SessionID: "session-m", Availability: ObservationAvailable,
		Presence: SessionPresencePresent, Status: StatusRunning,
		StatusSource: StatusSourceSnapshot, Attention: AttentionWorking,
		Activity: start, ActivityKnown: true,
	}
	planner.Plan(AttentionInput{
		Now:         start,
		Observation: ObservationSnapshot{Availability: ObservationAvailable, Sessions: []SessionObservation{working}, ObservedAt: start},
		SessionLabels: map[SessionID]string{"session-m": "managed"},
	})
	observed := SessionObservation{
		SessionID: "session-m", Availability: ObservationAvailable,
		Presence: SessionPresencePresent, Status: StatusAwaitingDecision,
		StatusSource: StatusSourceSnapshot, Attention: AttentionNeedsInput,
		Activity: start.Add(time.Second), ActivityKnown: true,
	}
	plan := planner.Plan(AttentionInput{
		Now:         start.Add(time.Second),
		Observation: ObservationSnapshot{Availability: ObservationAvailable, Sessions: []SessionObservation{observed}, ObservedAt: start},
		SessionLabels: map[SessionID]string{"session-m": "managed"},
	})
	found := false
	for _, intent := range plan.Notifications {
		if intent.Kind == AttentionIntentNeedsInput && intent.SessionID == "session-m" {
			found = true
		}
	}
	if !found {
		t.Fatalf("kein NeedsInput-Intent für wartet-auf-Entscheidung geplant: %+v", plan.Notifications)
	}
	if len(plan.Inbox.Entries) != 1 || plan.Inbox.Entries[0].Kind != AttentionWaitingInput {
		t.Fatalf("Inbox = %+v, want einen needs-input-Eintrag", plan.Inbox)
	}
}
