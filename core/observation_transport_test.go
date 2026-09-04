package core

import (
	"encoding/json"
	"testing"
	"time"
)

// Der Transport-Träger ändert nichts an lokalen Snapshots: leere Herkunft,
// unverändertes JSON, dasselbe Gate-Verhalten.
func TestObservationTransportDefaultsLocal(t *testing.T) {
	snapshot := ObservationSnapshot{
		ObservedAt:   time.Now().UTC(),
		Availability: ObservationAvailable,
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ObservationSnapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Transport != "" {
		t.Errorf("lokaler Snapshot trägt Transport %q", decoded.Transport)
	}
	if err := RequireFreshObservation(snapshot); err != nil {
		t.Errorf("frischer Snapshot verweigert: %v", err)
	}
}

func TestRequireFreshObservationRefusesStale(t *testing.T) {
	unavailable := ObservationSnapshot{Availability: ObservationUnavailable}
	if err := RequireFreshObservation(unavailable); err == nil {
		t.Error("unavailable Snapshot als frisch akzeptiert")
	}
	partial := ObservationSnapshot{
		Availability:     ObservationPartial,
		TransportProblem: "Host ist nicht erreichbar",
	}
	err := RequireFreshObservation(partial)
	if err == nil {
		t.Error("partial Snapshot als frisch akzeptiert")
	} else if got := err.Error(); got != "keine frischen Fakten: Host ist nicht erreichbar" {
		t.Errorf("falsche Begründung: %q", got)
	}
	hostFailure := ObservationSnapshot{
		Availability: ObservationUnavailable,
		Problems:     []ObservationProblem{{Operation: "list-panes", Message: "timed out"}},
	}
	if err := RequireFreshObservation(hostFailure); err == nil {
		t.Error("Host-Beobachtungsfehler als frisch akzeptiert")
	}
}
