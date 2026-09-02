package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"magentic/core"
)

func inboxTestModel(inbox core.AttentionInbox) model {
	return model{
		state: &State{
			Projects: []Project{{ID: "project-id", Name: "Projekt"}},
			Agents: []Agent{
				{ID: "session-a", Name: "alpha", ProjectID: "project-id", Project: "Projekt"},
				{ID: "session-b", Name: "beta", ProjectID: "project-id", Project: "Projekt"},
				{ID: "session-c", Name: "gamma", ProjectID: "project-id", Project: "Projekt"},
			},
		},
		collapsed: map[string]bool{},
		inbox:     inbox,
		inboxOpen: true,
		width:     120,
		height:    30,
	}
}

func inboxTestEntry(id core.SessionID, waitingSince time.Time, known bool) core.AttentionInboxEntry {
	return core.AttentionInboxEntry{
		SessionID: id, Kind: core.AttentionWaitingInput,
		WaitingSince: waitingSince, WaitingSinceKnown: known,
		Excerpt: "Darf ich die Datei überschreiben?", ExcerptKnown: true,
	}
}

// The view prints the planner's order unchanged; it must not sort by name, by
// Project or by anything else it happens to know.
func TestInboxViewKeepsThePlannedOrder(t *testing.T) {
	now := time.Now()
	m := inboxTestModel(core.AttentionInbox{
		State: core.AttentionInboxComplete,
		Entries: []core.AttentionInboxEntry{
			inboxTestEntry("session-c", now.Add(-time.Minute), true),
			inboxTestEntry("session-a", now.Add(-30*time.Second), true),
			inboxTestEntry("session-b", now.Add(-10*time.Second), true),
		},
	})

	rendered := ansi.Strip(strings.Join(m.inboxLines(100, 30), "\n"))
	positions := make([]int, 0, 3)
	for _, name := range []string{"gamma", "alpha", "beta"} {
		index := strings.Index(rendered, name)
		if index < 0 {
			t.Fatalf("Session %q fehlt im Posteingang:\n%s", name, rendered)
		}
		positions = append(positions, index)
	}
	if positions[0] > positions[1] || positions[1] > positions[2] {
		t.Fatalf("die Ansicht sortiert um: %v\n%s", positions, rendered)
	}
	if !strings.Contains(rendered, "wartet seit") {
		t.Fatalf("Wartezeit fehlt:\n%s", rendered)
	}
}

// A wait whose start the planner never saw stays recognizable as a lower bound,
// and an unavailable list is never rendered as an empty one.
func TestInboxViewNamesLowerBoundsAndMissingFacts(t *testing.T) {
	m := inboxTestModel(core.AttentionInbox{
		State:   core.AttentionInboxComplete,
		Entries: []core.AttentionInboxEntry{inboxTestEntry("session-a", time.Now().Add(-2*time.Minute), false)},
	})
	m.inbox.Entries[0].ExcerptKnown = false
	rendered := ansi.Strip(strings.Join(m.inboxLines(100, 30), "\n"))
	if !strings.Contains(rendered, "wartet mindestens seit") {
		t.Fatalf("untere Schranke wird als frische Wartezeit gezeigt:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Der Grund ist nicht bekannt") {
		t.Fatalf("unbekannter Grund wird nicht benannt:\n%s", rendered)
	}

	unavailable := inboxTestModel(core.AttentionInbox{State: core.AttentionInboxUnavailable})
	text := ansi.Strip(strings.Join(unavailable.inboxLines(100, 30), "\n"))
	if !strings.Contains(text, "konnten gerade nicht gelesen werden") {
		t.Fatalf("unverfügbare Liste wird als leerer Posteingang gezeigt:\n%s", text)
	}

	partial := inboxTestModel(core.AttentionInbox{
		State:   core.AttentionInboxIncomplete,
		Entries: []core.AttentionInboxEntry{inboxTestEntry("session-b", time.Now(), true)},
	})
	text = ansi.Strip(strings.Join(partial.inboxLines(100, 30), "\n"))
	if !strings.Contains(text, "nicht bekannt, ob sie warten") || !strings.Contains(text, "beta") {
		t.Fatalf("unvollständige Liste nennt weder ihre Lücke noch ihre Einträge:\n%s", text)
	}
}

// Selecting an entry moves the tree to that Session and leaves the inbox.
func TestInboxSelectionMovesToTheSession(t *testing.T) {
	now := time.Now()
	m := inboxTestModel(core.AttentionInbox{
		State: core.AttentionInboxComplete,
		Entries: []core.AttentionInboxEntry{
			inboxTestEntry("session-c", now.Add(-time.Minute), true),
			inboxTestEntry("session-a", now.Add(-30*time.Second), true),
		},
	})
	m.inboxCursor = 1

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	after, ok := next.(model)
	if !ok {
		t.Fatalf("Update lieferte %T", next)
	}
	if after.inboxOpen {
		t.Fatalf("der Posteingang bleibt nach dem Sprung offen")
	}
	selected := after.selectedAgent()
	if selected == nil || selected.Name != "alpha" {
		t.Fatalf("Auswahl = %v, erwartet alpha", selected)
	}
}

// Both surfaces read one planner output. The TUI rows and the Overview
// projection the desktop app renders must therefore list the same Sessions in
// the same order.
func TestInboxMatchesTheDesktopProjection(t *testing.T) {
	planner := core.NewAttentionPlanner(core.AttentionPlannerConfig{})
	start := time.Now().Add(-10 * time.Minute)
	waiting := func(id core.SessionID, attention core.AttentionState) core.SessionObservation {
		return core.SessionObservation{
			SessionID: id, Availability: core.ObservationAvailable,
			Presence: core.SessionPresencePresent, Status: core.StatusBlocked, Attention: attention,
		}
	}
	working := func(id core.SessionID) core.SessionObservation {
		return core.SessionObservation{
			SessionID: id, Availability: core.ObservationAvailable,
			Presence: core.SessionPresencePresent, Status: core.StatusRunning, Attention: core.AttentionWorking,
		}
	}
	snapshot := func(sessions ...core.SessionObservation) core.ObservationSnapshot {
		return core.ObservationSnapshot{Availability: core.ObservationAvailable, Sessions: sessions}
	}

	planner.Plan(core.AttentionInput{Now: start, Observation: snapshot(
		working("session-a"), working("session-b"), working("session-c"),
	)})
	planner.Plan(core.AttentionInput{Now: start.Add(time.Minute), Observation: snapshot(
		working("session-a"), waiting("session-b", core.AttentionNeedsInput), working("session-c"),
	)})
	plan := planner.Plan(core.AttentionInput{Now: start.Add(5 * time.Minute), Observation: snapshot(
		waiting("session-a", core.AttentionReview),
		waiting("session-b", core.AttentionNeedsInput),
		working("session-c"),
	)})
	if len(plan.Inbox.Entries) != 2 {
		t.Fatalf("Plan-Posteingang = %#v", plan.Inbox)
	}

	m := inboxTestModel(plan.Inbox)
	desktop := core.BuildInbox(m.state, plan.Inbox)
	rows := m.inboxRows()
	if len(rows) != len(desktop.Entries) || desktop.State != plan.Inbox.State {
		t.Fatalf("TUI %d Einträge, Desktop %#v", len(rows), desktop)
	}
	for index, row := range rows {
		if row.entry.SessionID != desktop.Entries[index].SessionID {
			t.Fatalf("Reihenfolge weicht ab: TUI %v, Desktop %v",
				row.entry.SessionID, desktop.Entries[index].SessionID)
		}
		if row.entry.Kind != desktop.Entries[index].Kind {
			t.Fatalf("Wartegrund weicht ab: TUI %v, Desktop %v",
				row.entry.Kind, desktop.Entries[index].Kind)
		}
	}
}
