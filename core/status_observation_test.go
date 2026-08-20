package core

import (
	"reflect"
	"testing"
	"time"
)

func TestProjectLegacyObservationPreservesUnavailableAsUnknown(t *testing.T) {
	sessions := []Session{{ID: "session-1", Name: "one", RuntimeName: "mgt-one"}}
	snapshot := ObservationSnapshot{
		Availability: ObservationUnavailable,
		Sessions: []SessionObservation{{
			SessionID:    "session-1",
			Availability: ObservationUnavailable,
			Presence:     SessionPresenceUnknown,
			Status:       StatusUnknown,
			Attention:    AttentionUnknown,
			Occupancy:    OccupancyUnknown,
		}},
	}

	got := ProjectLegacyObservation(snapshot, sessions)
	if got.Statuses["one"] != StatusUnknown {
		t.Fatalf("unavailable tmux projected as %v, want unknown", got.Statuses["one"])
	}
	if len(got.Activity) != 0 || len(got.Tools) != 0 || got.Contents["one"] != "" {
		t.Fatalf("unknown facts were invented: %#v", got)
	}
}

func TestProjectLegacyObservationJoinsByStableSessionID(t *testing.T) {
	activity := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	sessions := []Session{
		{ID: "session-1", Name: "one", RuntimeName: "mgt-one"},
		{ID: "session-2", Name: "two", RuntimeName: "mgt-two"},
	}
	snapshot := ObservationSnapshot{Sessions: []SessionObservation{
		{
			SessionID: "session-2", Status: StatusBlocked, Content: "second",
			ContentKnown: true, Activity: activity, ActivityKnown: true, Tool: AgentToolCodex,
		},
		{SessionID: "session-1", Status: StatusRunning, Content: "first", ContentKnown: true},
	}}

	got := ProjectLegacyObservation(snapshot, sessions)
	if got.Statuses["one"] != StatusRunning || got.Contents["one"] != "first" {
		t.Fatalf("session-1 projection mismatch: %#v", got)
	}
	if got.Statuses["two"] != StatusBlocked || got.Contents["two"] != "second" ||
		!got.Activity["two"].Equal(activity) || got.Tools["two"] != AgentToolCodex {
		t.Fatalf("session-2 projection mismatch: %#v", got)
	}
	if tools := CollectAgentTools(sessions); !reflect.DeepEqual(tools, got.Tools) {
		t.Fatalf("legacy tool caller did not reuse coherent projection: got %#v want %#v", tools, got.Tools)
	}
}

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
	snapshot := ObservationSnapshot{Sessions: []SessionObservation{
		{SessionID: prepared[0].ID, Status: StatusIdle},
		{SessionID: prepared[1].ID, Status: StatusDead},
	}}
	got := ProjectLegacyObservation(snapshot, prepared)
	if got.Statuses["one"] != StatusIdle || got.Statuses["two"] != StatusDead {
		t.Fatalf("fixture projection mismatch: %#v", got.Statuses)
	}
}
