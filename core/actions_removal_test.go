package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestWorktreeRemovalRejectsAbsenceFromMalformedPaneList(t *testing.T) {
	session := Agent{ID: "session-1", Name: "topic", RuntimeName: "mgt-topic"}
	snapshot := malformedListPanesObservation(t, []Session{session})

	err := validateWorktreeRemovalObservations([]Agent{session}, snapshot)
	if err == nil || !strings.Contains(err.Error(), "nicht verlässlich") {
		t.Fatalf("malformed pane list authorized removal: %v (snapshot %#v)", err, snapshot)
	}
}

func TestWorktreeRemovalRejectsUnterminatedUnrelatedPaneList(t *testing.T) {
	session := Agent{ID: "session-1", Name: "topic", RuntimeName: "mgt-topic"}
	runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
		if args[0] == "list-panes" {
			return "unrelated\t%4\tclaude\t1787227200\t1\t1", nil
		}
		return "", errors.New("capture-pane must not run")
	}}
	snapshot := observeWithRunner(context.Background(), []Session{session}, runner, testObservationConfig(time.Now()))

	err := validateWorktreeRemovalObservations([]Agent{session}, snapshot)
	if err == nil || !strings.Contains(err.Error(), "nicht verlässlich") {
		t.Fatalf("unterminated listing authorized removal: %v (snapshot %#v)", err, snapshot)
	}
}

func TestWorktreeRemovalRejectsValidIdlePaneTaintedByMalformedRow(t *testing.T) {
	session := Agent{ID: "session-1", Name: "topic", RuntimeName: "mgt-topic"}
	runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
		switch args[0] {
		case "list-panes":
			return "mgt-topic\t%4\tclaude\t1787227200\t1\t1\n" +
				"mgt-topic\tnot-a-pane\tclaude\t1787227200\t1\t1\n", nil
		case "capture-pane":
			return "Ready\nshift+tab to cycle\n", nil
		default:
			return "", errors.New("unexpected command")
		}
	}}
	snapshot := observeWithRunner(context.Background(), []Session{session}, runner, testObservationConfig(time.Now()))

	err := validateWorktreeRemovalObservations([]Agent{session}, snapshot)
	if err == nil || !strings.Contains(err.Error(), "nicht verlässlich") {
		t.Fatalf("malformed duplicate row left idle status destructive-safe: %v (snapshot %#v)", err, snapshot)
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
