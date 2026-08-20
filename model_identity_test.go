package main

import (
	"context"
	"strings"
	"testing"

	"magentic/core"
)

func identityTestModel(session Agent) model {
	project := Project{ID: session.ProjectID, Name: session.Project, Path: session.Dir}
	return model{
		state: &State{Projects: []Project{project}, Agents: []Agent{session}},
		collapsed: map[string]bool{}, cursor: 1,
	}
}

func TestCreateTermAgentUsesStableSessionID(t *testing.T) {
	previous := createTermSessionForID
	t.Cleanup(func() { createTermSessionForID = previous })
	session := Agent{ID: "session-id", Name: "display", ProjectID: "project-id", Project: "project", Dir: "/old"}
	var gotID core.SessionID
	createTermSessionForID = func(_ *State, id core.SessionID, name string) (string, error) {
		gotID = id
		if name != "terminal" {
			t.Fatalf("terminal name = %q", name)
		}
		return "terminal", nil
	}

	next, _ := identityTestModel(session).createTermAgent("terminal")
	if gotID != session.ID || next.(model).flashIsErr {
		t.Fatalf("terminal source = %q, model=%#v", gotID, next)
	}
}

func TestAttachRejectsStaleIDAfterNameReuse(t *testing.T) {
	previousLoad, previousObserve := LoadState, observeSessions
	t.Cleanup(func() { LoadState, observeSessions = previousLoad, previousObserve })
	stale := Agent{ID: "session-stale", Name: "reused", RuntimeName: "opaque-stale", ProjectID: "project-id", Project: "project", Dir: "/old"}
	replacement := Agent{ID: "session-current", Name: stale.Name, RuntimeName: "opaque-current", ProjectID: stale.ProjectID, Project: stale.Project, Dir: "/current"}
	LoadState = func() (*State, error) {
		return &State{Projects: []Project{{ID: "project-id", Name: "project"}}, Agents: []Agent{replacement}}, nil
	}
	observeSessions = func(context.Context, []core.Session) core.ObservationSnapshot {
		t.Fatal("stale SessionID crossed the Observation Seam")
		return core.ObservationSnapshot{}
	}

	next, command := identityTestModel(stale).attach()
	got := next.(model)
	if command != nil || !got.flashIsErr || !strings.Contains(got.flash, "existiert nicht mehr") {
		t.Fatalf("stale attach result: command=%#v flash=%q error=%v", command, got.flash, got.flashIsErr)
	}
}

func TestAttachReResolvesRuntimeNameBySessionID(t *testing.T) {
	previousLoad, previousObserve := LoadState, observeSessions
	t.Cleanup(func() { LoadState, observeSessions = previousLoad, previousObserve })
	stale := Agent{ID: "session-id", Name: "display", RuntimeName: "opaque-stale", ProjectID: "project-id", Project: "project", Dir: "/old"}
	current := stale
	current.RuntimeName = "opaque-current"
	current.Dir = "/current"
	LoadState = func() (*State, error) {
		return &State{Projects: []Project{{ID: "project-id", Name: "project"}}, Agents: []Agent{current}}, nil
	}
	observeSessions = func(_ context.Context, sessions []core.Session) core.ObservationSnapshot {
		if len(sessions) != 1 || sessions[0].ID != current.ID || sessions[0].RuntimeName != current.RuntimeName {
			t.Fatalf("attach observed stale runtime identity: %#v", sessions)
		}
		return core.ObservationSnapshot{
			Availability: core.ObservationAvailable,
			Sessions: []core.SessionObservation{{
				SessionID: current.ID, Availability: core.ObservationAvailable,
				Presence: core.SessionPresenceAbsent, Status: core.StatusDead,
			}},
		}
	}

	next, command := identityTestModel(stale).attach()
	got := next.(model)
	if command != nil || !got.flashIsErr || !strings.Contains(got.flash, "existiert nicht mehr") {
		t.Fatalf("fresh runtime attach result: command=%#v flash=%q error=%v", command, got.flash, got.flashIsErr)
	}
}
