package main

import (
	"testing"

	"magentic/core"
)

// The desktop polls Overview every few seconds, but a Git Survey spawns one
// process per Project plus one per Worktree. The cache must serve the polls in
// between from the recent Survey, refresh it off the caller's path on explicit
// refresh, and survey on the caller's path only when the Project set changed.
func TestCachedSurveyReusesRecentSurvey(t *testing.T) {
	var pending []func()
	app := &App{resurvey: func(f func()) { pending = append(pending, f) }}
	projects := []core.Project{{ID: "p1", Name: "proj", Path: "/tmp/magentic-no-such-dir"}}

	first, err := app.cachedSurvey(projects, false)
	if err != nil {
		t.Fatalf("initial survey: %v", err)
	}
	if len(first.Projects) != 1 {
		t.Fatalf("surveyed projects = %d, want 1", len(first.Projects))
	}

	second, err := app.cachedSurvey(projects, false)
	if err != nil {
		t.Fatalf("cached survey: %v", err)
	}
	if !second.ObservedAt.Equal(first.ObservedAt) || len(pending) != 0 {
		t.Fatal("second poll resurveyed instead of reusing the recent Survey")
	}

	third, err := app.cachedSurvey(projects, true)
	if err != nil {
		t.Fatalf("fresh survey: %v", err)
	}
	if !third.ObservedAt.Equal(first.ObservedAt) {
		t.Fatal("explicit refresh waited for a new Survey instead of answering from the cache")
	}
	if len(pending) != 1 {
		t.Fatalf("explicit refresh started %d background Surveys, want 1", len(pending))
	}
	if _, err := app.cachedSurvey(projects, true); err != nil || len(pending) != 1 {
		t.Fatalf("a second refresh started another Survey while one runs: %d", len(pending))
	}
	pending[0]()

	fourth, err := app.cachedSurvey(projects, false)
	if err != nil {
		t.Fatalf("refreshed survey: %v", err)
	}
	if fourth.ObservedAt.Equal(first.ObservedAt) {
		t.Fatal("the background Survey did not replace the cached one")
	}

	moved := []core.Project{{ID: "p1", Name: "proj", Path: "/tmp/magentic-no-such-dir-moved"}}
	fifth, err := app.cachedSurvey(moved, false)
	if err != nil {
		t.Fatalf("moved survey: %v", err)
	}
	if fifth.ObservedAt.Equal(fourth.ObservedAt) {
		t.Fatal("changed Project set reused the stale Survey")
	}
}
