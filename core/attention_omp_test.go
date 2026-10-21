package core

import (
	"testing"
	"time"
)

// 2.14: an omp Session's approval request plans attention through the same
// Attention Module a managed Session uses (ADR 0007) — no omp-specific
// branch exists in the planner, because observeManagedSession already
// reports it as AttentionNeedsInput before any notification is emitted.
func TestAttentionPlansOmpPermissionBeforeNotify(t *testing.T) {
	start := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	planner := NewAttentionPlanner(AttentionPlannerConfig{Now: func() time.Time { return start }})
	working := SessionObservation{
		SessionID: "session-o", Availability: ObservationAvailable,
		Presence: SessionPresencePresent, Status: StatusRunning,
		StatusSource: StatusSourceOmpProtocol, Attention: AttentionWorking,
		Activity: start, ActivityKnown: true,
	}
	planner.Plan(AttentionInput{
		Now:           start,
		Observation:   ObservationSnapshot{Availability: ObservationAvailable, Sessions: []SessionObservation{working}, ObservedAt: start},
		SessionLabels: map[SessionID]string{"session-o": "orbit"},
	})
	asking := SessionObservation{
		SessionID: "session-o", Availability: ObservationAvailable,
		Presence: SessionPresencePresent, Status: StatusAwaitingDecision,
		StatusSource: StatusSourceOmpProtocol, Attention: AttentionNeedsInput,
		Detail:   "Allow tool: write\nPath: a.txt",
		Activity: start.Add(time.Second), ActivityKnown: true,
	}
	plan := planner.Plan(AttentionInput{
		Now:           start.Add(time.Second),
		Observation:   ObservationSnapshot{Availability: ObservationAvailable, Sessions: []SessionObservation{asking}, ObservedAt: start},
		SessionLabels: map[SessionID]string{"session-o": "orbit"},
	})
	found := false
	for _, intent := range plan.Notifications {
		if intent.Kind == AttentionIntentNeedsInput && intent.SessionID == "session-o" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no NeedsInput intent planned for an omp Session awaiting a decision: %+v", plan.Notifications)
	}
	if len(plan.Inbox.Entries) != 1 || plan.Inbox.Entries[0].Kind != AttentionWaitingInput {
		t.Fatalf("Inbox = %+v, want one needs-input entry", plan.Inbox)
	}
}
