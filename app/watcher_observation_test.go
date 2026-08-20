package main

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"magentic/core"
)

func TestObservationCacheRequiresTheSameDurableSessions(t *testing.T) {
	sessions := []core.Session{{ID: "two", RuntimeName: "runtime-two"}, {ID: "one", RuntimeName: "runtime-one"}}
	snapshot := core.ObservationSnapshot{
		Availability: core.ObservationAvailable,
		ObservedAt:   time.Now(),
		Sessions: []core.SessionObservation{
			{SessionID: "one"},
			{SessionID: "two"},
		},
	}
	inputs := observationFingerprints(sessions)
	if !observationCovers(snapshot, inputs, sessions) {
		t.Fatal("cache rejected the same durable Session set")
	}
	if observationCovers(snapshot, inputs, []core.Session{{ID: "one"}, {ID: "other"}}) {
		t.Fatal("cache accepted a different durable Session set")
	}
	if observationCovers(snapshot, inputs, []core.Session{{Name: "legacy"}, {ID: "two"}}) {
		t.Fatal("cache matched a mutable display name as identity")
	}
	renamed := append([]core.Session(nil), sessions...)
	renamed[0].RuntimeName = "runtime-two-renamed"
	if observationCovers(snapshot, inputs, renamed) {
		t.Fatal("cache accepted a changed opaque RuntimeName for the same SessionID")
	}
	seen := append([]core.Session(nil), sessions...)
	seen[1].SeenAt = time.Now()
	if observationCovers(snapshot, inputs, seen) {
		t.Fatal("cache accepted changed unread input facts")
	}
}

func TestStoreObservationKeepsAnImmutableSnapshot(t *testing.T) {
	app := &App{}
	original := core.ObservationSnapshot{
		Availability: core.ObservationPartial,
		Sessions:     []core.SessionObservation{{SessionID: "one", Attention: core.AttentionNeedsInput}},
		Problems:     []core.ObservationProblem{{Operation: "capture", Message: "partial"}},
	}
	app.storeObservation(original, []core.Session{{ID: "one", RuntimeName: "runtime-one"}})
	original.Sessions[0].Attention = core.AttentionWorking
	original.Problems[0].Message = "changed"

	app.observationMu.Lock()
	cached := cloneObservation(app.observation)
	app.observationMu.Unlock()
	if cached.Sessions[0].Attention != core.AttentionNeedsInput || cached.Problems[0].Message != "partial" {
		t.Fatalf("cached Observation was mutated through caller slices: %#v", cached)
	}
}

func TestObservationCacheRefreshesAfterRuntimeRename(t *testing.T) {
	app := &App{}
	calls := 0
	app.observeSessions = func(_ context.Context, sessions []core.Session) core.ObservationSnapshot {
		calls++
		observed := make([]core.SessionObservation, 0, len(sessions))
		for _, session := range sessions {
			observed = append(observed, core.SessionObservation{SessionID: session.ID})
		}
		return core.ObservationSnapshot{Availability: core.ObservationAvailable, Sessions: observed}
	}
	original := []core.Session{{ID: "session-one", Name: "one", RuntimeName: "opaque-old"}}
	app.observationFor(original, false)
	app.observationFor(original, false)
	renamed := []core.Session{{ID: "session-one", Name: "one", RuntimeName: "opaque-new"}}
	app.observationFor(renamed, false)
	if calls != 2 {
		t.Fatalf("observer calls = %d, want initial probe plus RuntimeName refresh", calls)
	}
}

func TestAttentionExecutorAppliesPlanWithoutPolicy(t *testing.T) {
	var calls []string
	executor := attentionPlanExecutor{
		badge: func(label string) { calls = append(calls, "badge:"+label) },
		notify: func(title, message, sound string) {
			calls = append(calls, "notify:"+title+":"+message+":"+sound)
		},
		request: func(critical bool) {
			if critical {
				calls = append(calls, "request:critical")
			} else {
				calls = append(calls, "request:informational")
			}
		},
		cancel: func() { calls = append(calls, "cancel") },
		front:  func() { calls = append(calls, "front") },
	}
	executor.execute(core.AttentionPlan{
		DockBadge: core.AttentionDockBadge{Update: true, Label: "2+"},
		Notifications: []core.AttentionNotificationIntent{{
			Title: "title", Message: "message", Sound: "sound",
		}},
		NativeAttention: core.NativeAttentionCritical,
		BringToFront:    true,
	})
	want := []string{"badge:2+", "notify:title:message:sound", "request:critical", "front"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("executor calls = %v, want %v", calls, want)
	}

	calls = nil
	executor.execute(core.AttentionPlan{NativeAttention: core.NativeAttentionCancel})
	if !reflect.DeepEqual(calls, []string{"cancel"}) {
		t.Fatalf("cancel execution = %v", calls)
	}
}

func TestAttentionEventQueueIsThreadSafeAndDrainsOnce(t *testing.T) {
	app := &App{}
	event := core.AttentionEvent{Key: "same-episode", Kind: core.AttentionEventBreakFinished}
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			app.enqueueAttentionEvent(event)
		}()
	}
	wait.Wait()
	got := app.takeAttentionEvents()
	if len(got) != 1 || got[0] != event {
		t.Fatalf("queued events = %#v", got)
	}
	if replay := app.takeAttentionEvents(); len(replay) != 0 {
		t.Fatalf("drained queue replayed %#v", replay)
	}
}

func TestBreakOverQueuesOneExplicitAttentionEpisode(t *testing.T) {
	app := &App{}
	app.BreakOver()
	app.BreakOver()
	events := app.takeAttentionEvents()
	if len(events) != 1 || events[0].Kind != core.AttentionEventBreakFinished || events[0].Key == "" {
		t.Fatalf("BreakOver queue = %#v", events)
	}

	first := breakFinishedAttentionEvent(time.Date(2026, 8, 20, 12, 0, 59, 0, time.UTC))
	sameMinute := breakFinishedAttentionEvent(time.Date(2026, 8, 20, 12, 0, 1, 0, time.UTC))
	nextMinute := breakFinishedAttentionEvent(time.Date(2026, 8, 20, 12, 1, 0, 0, time.UTC))
	if first.Key != sameMinute.Key || first.Key == nextMinute.Key {
		t.Fatalf("break episode keys = %q / %q / %q", first.Key, sameMinute.Key, nextMinute.Key)
	}
}
