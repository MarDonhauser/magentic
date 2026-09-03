package main

import (
	"testing"

	"magentic/core"
)

// The desktop polls Overview every few seconds, but a Git Survey spawns one
// process per Project plus one per Worktree. The cache must serve the polls in
// between from the recent Survey and only resurvey on explicit refresh or when
// the Project set changed.
func TestCachedSurveyReusesRecentSurvey(t *testing.T) {
	app := &App{}
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
	if !second.ObservedAt.Equal(first.ObservedAt) {
		t.Fatal("second poll resurveyed instead of reusing the recent Survey")
	}

	third, err := app.cachedSurvey(projects, true)
	if err != nil {
		t.Fatalf("fresh survey: %v", err)
	}
	if third.ObservedAt.Equal(first.ObservedAt) {
		t.Fatal("explicit refresh reused the cached Survey")
	}

	moved := []core.Project{{ID: "p1", Name: "proj", Path: "/tmp/magentic-no-such-dir-moved"}}
	fourth, err := app.cachedSurvey(moved, false)
	if err != nil {
		t.Fatalf("moved survey: %v", err)
	}
	if fourth.ObservedAt.Equal(third.ObservedAt) {
		t.Fatal("changed Project set reused the stale Survey")
	}
}
