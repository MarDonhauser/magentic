package main

import (
	"testing"
	"time"

	"magentic/core"
)

func TestObservationCacheRequiresTheSameDurableSessions(t *testing.T) {
	snapshot := core.ObservationSnapshot{
		Availability: core.ObservationAvailable,
		ObservedAt:   time.Now(),
		Sessions: []core.SessionObservation{
			{SessionID: "one"},
			{SessionID: "two"},
		},
	}
	if !observationCovers(snapshot, []core.Session{{ID: "two"}, {ID: "one"}}) {
		t.Fatal("cache rejected the same durable Session set")
	}
	if observationCovers(snapshot, []core.Session{{ID: "one"}, {ID: "other"}}) {
		t.Fatal("cache accepted a different durable Session set")
	}
	if observationCovers(snapshot, []core.Session{{Name: "legacy"}, {ID: "two"}}) {
		t.Fatal("cache matched a mutable display name as identity")
	}
}

func TestStoreObservationKeepsAnImmutableSnapshot(t *testing.T) {
	app := &App{}
	original := core.ObservationSnapshot{
		Availability: core.ObservationPartial,
		Sessions:     []core.SessionObservation{{SessionID: "one", Attention: core.AttentionNeedsInput}},
		Problems:     []core.ObservationProblem{{Operation: "capture", Message: "partial"}},
	}
	app.storeObservation(original)
	original.Sessions[0].Attention = core.AttentionWorking
	original.Problems[0].Message = "changed"

	app.observationMu.Lock()
	cached := cloneObservation(app.observation)
	app.observationMu.Unlock()
	if cached.Sessions[0].Attention != core.AttentionNeedsInput || cached.Problems[0].Message != "partial" {
		t.Fatalf("cached Observation was mutated through caller slices: %#v", cached)
	}
}

func TestAttentionDockBadgeLabelPreservesPartialCount(t *testing.T) {
	partial := core.AttentionDockBadge{Update: true, Complete: false, Count: 1, Label: "1"}
	if got := attentionDockBadgeLabel(partial); got != "1+" {
		t.Fatalf("partial Dock badge = %q, want lower bound", got)
	}
	exact := core.AttentionDockBadge{Update: true, Complete: true, Count: 1, Label: "1"}
	if got := attentionDockBadgeLabel(exact); got != "1" {
		t.Fatalf("exact Dock badge = %q", got)
	}
}
