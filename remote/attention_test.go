package remote

import (
	"testing"
	"time"

	"magentic/core"
)

func waitingSnapshot() core.ObservationSnapshot {
	moment := time.Now().UTC()
	return core.ObservationSnapshot{
		ObservedAt:   moment,
		Availability: core.ObservationAvailable,
		Transport:    core.ObservationTransportRemote,
		Sessions: []core.SessionObservation{
			{
				SessionID: "s-wait", Availability: core.ObservationAvailable,
				Presence: core.SessionPresencePresent, Status: core.StatusBlocked,
				StatusSource: core.StatusSourceSnapshot, ContentKnown: true,
				Attention: core.AttentionNeedsInput,
				Activity:  moment, ActivityKnown: true,
			},
		},
	}
}

// Eine wartende Remote-Session hebt clientseitig Attention — Mitteilung,
// Badge und native Attention laufen auf der Maschine des Entwicklers.
func TestRemoteWaitingRaisesClientAttention(t *testing.T) {
	attention := NewClientAttention()
	plan := attention.PlanFromStream(waitingSnapshot(), "", map[core.SessionID]string{"s-wait": "hera"}, time.Now())
	if len(plan.Notifications) == 0 && len(plan.Inbox.Entries) == 0 && !plan.BringToFront &&
		plan.NativeAttention == core.NativeAttentionUnchanged && !plan.DockBadge.Update {
		t.Error("wartende Remote-Session hebt keine clientseitige Attention")
	}
	for _, notification := range plan.Notifications {
		if notification.SessionID != "" && notification.SessionID != "s-wait" {
			t.Errorf("Intent für falsche Session: %q", notification.SessionID)
		}
	}
}

// Abriss mit wartender Session in der letzten bekannten Sicht: kein neuer
// Pro-Session-Intent aus veralteten Fakten — die Auskunft ist der
// Verbindungszustand.
func TestNoAttentionFromStaleFacts(t *testing.T) {
	attention := NewClientAttention()
	stale := core.ObservationSnapshot{
		Availability:     core.ObservationUnavailable,
		Transport:        core.ObservationTransportRemote,
		TransportProblem: "Host nicht erreichbar",
	}
	plan := attention.PlanFromStream(stale, "", map[core.SessionID]string{"s-wait": "hera"}, time.Now())
	for _, notification := range plan.Notifications {
		if notification.SessionID != "" {
			t.Errorf("Pro-Session-Intent aus veralteter Sicht: %+v", notification)
		}
	}
	if len(plan.Inbox.Entries) != 0 {
		t.Errorf("Inbox aus veralteter Sicht: %+v", plan.Inbox.Entries)
	}
}
