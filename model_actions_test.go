package main

import (
	"errors"
	"testing"

	"magentic/core"
)

func TestSendSkillToSelectedReportsActionFailureInsteadOfSuccess(t *testing.T) {
	previous := sendSkillByID
	t.Cleanup(func() { sendSkillByID = previous })
	var gotID core.SessionID
	sendSkillByID = func(id core.SessionID, _ string) error {
		gotID = id
		return errors.New("Session-Beobachtung ist nicht verfügbar")
	}
	m := model{
		state: &State{
			Projects: []Project{{ID: "project-id", Name: "Project"}},
			Agents:   []Agent{{ID: "session-id", Name: "Session", ProjectID: "project-id", Project: "Project"}},
		},
		collapsed: map[string]bool{}, cursor: 1,
	}

	next, command := m.sendSkillToSelected("/review")
	got := next.(model)
	if command != nil || gotID != "session-id" {
		t.Fatalf("action target/command = %q, %#v", gotID, command)
	}
	if !got.flashIsErr || got.flash != "Session-Beobachtung ist nicht verfügbar" {
		t.Fatalf("failed action was presented as success: flash=%q error=%v", got.flash, got.flashIsErr)
	}
}

func TestSendSkillToSelectedReportsSuccessOnlyAfterNilActionResult(t *testing.T) {
	previous := sendSkillByID
	t.Cleanup(func() { sendSkillByID = previous })
	sendSkillByID = func(id core.SessionID, command string) error {
		if id != "session-id" || command != "/review" {
			t.Fatalf("SendSkillByID(%q, %q)", id, command)
		}
		return nil
	}
	m := model{
		state: &State{
			Projects: []Project{{ID: "project-id", Name: "Project"}},
			Agents:   []Agent{{ID: "session-id", Name: "Session", ProjectID: "project-id", Project: "Project"}},
		},
		collapsed: map[string]bool{}, cursor: 1,
	}

	next, command := m.sendSkillToSelected("/review")
	got := next.(model)
	if command == nil || got.flashIsErr || got.flash != "/review an Session gesendet" {
		t.Fatalf("successful action feedback = flash %q error=%v command=%#v", got.flash, got.flashIsErr, command)
	}
}
