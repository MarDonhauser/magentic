package core

import (
	"strings"
	"testing"
)

func TestWorktreeRemovalRejectsPartialCaptureKnowledge(t *testing.T) {
	session := Agent{ID: "session-1", Name: "topic"}
	snapshot := ObservationSnapshot{
		Availability: ObservationPartial,
		Sessions: []SessionObservation{{
			SessionID: session.ID, Availability: ObservationPartial,
			Presence: SessionPresencePresent, Status: StatusUnknown,
			Occupancy: OccupancyOccupied,
		}},
	}
	err := validateWorktreeRemovalObservations([]Agent{session}, snapshot)
	if err == nil || !strings.Contains(err.Error(), "nicht verlässlich") {
		t.Fatalf("partial capture knowledge authorized removal: %v", err)
	}
}

func TestWorktreeRemovalRequiresKnownSafeSessionState(t *testing.T) {
	session := Agent{ID: "session-1", Name: "topic"}
	tests := []struct {
		name        string
		observation SessionObservation
		wantErr     bool
	}{
		{name: "known absent", observation: SessionObservation{SessionID: session.ID, Availability: ObservationAvailable, Presence: SessionPresenceAbsent, Status: StatusDead}},
		{name: "known idle", observation: SessionObservation{SessionID: session.ID, Availability: ObservationAvailable, Presence: SessionPresencePresent, Status: StatusIdle}},
		{name: "unknown status", observation: SessionObservation{SessionID: session.ID, Availability: ObservationAvailable, Presence: SessionPresencePresent, Status: StatusUnknown}, wantErr: true},
		{name: "live terminal", observation: SessionObservation{SessionID: session.ID, Availability: ObservationAvailable, Presence: SessionPresencePresent, Status: StatusTerm}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWorktreeRemovalObservations([]Agent{session}, ObservationSnapshot{
				Availability: ObservationAvailable,
				Sessions:     []SessionObservation{tt.observation},
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("validation error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
