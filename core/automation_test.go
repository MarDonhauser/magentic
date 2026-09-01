package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func automationRegistry(t *testing.T) (*Registry, Session) {
	t.Helper()
	registry := OpenRegistry(filepath.Join(t.TempDir(), "state.json"))
	session := Session{ID: "session-auto", Name: "atlas", Dir: "/workspace/project"}
	if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}
	return registry, session
}

func TestRegistryAutomationQueuesDueInstructionAndAdvancesCadence(t *testing.T) {
	registry, session := automationRegistry(t)
	due := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	result, err := registry.Change(context.Background(), SetSessionAutomation(session.ID, session.Name, SessionAutomation{
		Name: "Repository prüfen", Instructions: "Prüfe den Build und behebe neue Fehler.",
		EveryMinutes: 15, NextRunAt: due, Enabled: true,
	}))
	if err != nil || !result.Applied {
		t.Fatalf("SetSessionAutomation = %+v, %v", result, err)
	}
	state := result.Snapshot.State()
	saved := state.SessionByID(session.ID)
	if saved == nil || saved.Automation == nil || saved.Automation.ID == "" {
		t.Fatalf("automation was not persisted with an ID: %+v", saved)
	}
	automationID := saved.Automation.ID

	now := due.Add(35 * time.Minute)
	result, err = registry.Change(context.Background(), QueueDueSessionAutomation(session.ID, automationID, now))
	if err != nil || !result.Applied {
		t.Fatalf("QueueDueSessionAutomation = %+v, %v", result, err)
	}
	state = result.Snapshot.State()
	saved = state.SessionByID(session.ID)
	if len(saved.Outbox) != 1 {
		t.Fatalf("due automation did not queue exactly one message: %+v", saved.Outbox)
	}
	message := saved.Outbox[0]
	if message.Kind != QueuedMessageKindAutomation || message.Text != "Automatisierung \"Repository prüfen\"\n\nPrüfe den Build und behebe neue Fehler." {
		t.Fatalf("queued automation prompt = %+v", message)
	}
	if !saved.Automation.LastRunAt.Equal(now) || !saved.Automation.NextRunAt.Equal(due.Add(45*time.Minute)) {
		t.Fatalf("cadence was not advanced beyond now: %+v", saved.Automation)
	}

	result, err = registry.Change(context.Background(), QueueDueSessionAutomation(session.ID, automationID, now))
	if err != nil || result.Applied {
		t.Fatalf("same occurrence was processed twice: %+v, %v", result, err)
	}
}

func TestRegistryAutomationCoalescesWhileEarlierRunIsQueued(t *testing.T) {
	registry, session := automationRegistry(t)
	due := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	result, err := registry.Change(context.Background(), SetSessionAutomation(session.ID, session.Name, SessionAutomation{
		ID: "automation-1", Name: "Status", Instructions: "Berichte den aktuellen Status.",
		EveryMinutes: 10, NextRunAt: due, Enabled: true,
	}))
	if err != nil || !result.Applied {
		t.Fatalf("SetSessionAutomation = %+v, %v", result, err)
	}
	if _, err := registry.Change(context.Background(), QueueDueSessionAutomation(session.ID, "automation-1", due)); err != nil {
		t.Fatal(err)
	}
	secondAt := due.Add(10 * time.Minute)
	result, err = registry.Change(context.Background(), QueueDueSessionAutomation(session.ID, "automation-1", secondAt))
	if err != nil || !result.Applied {
		t.Fatalf("second due occurrence = %+v, %v", result, err)
	}
	state := result.Snapshot.State()
	saved := state.SessionByID(session.ID)
	if len(saved.Outbox) != 1 {
		t.Fatalf("an undelivered recurring instruction accumulated duplicates: %+v", saved.Outbox)
	}
	if !saved.Automation.NextRunAt.Equal(due.Add(20 * time.Minute)) {
		t.Fatalf("coalesced occurrence did not preserve cadence: %+v", saved.Automation)
	}
}

func TestRegistryAutomationRejectsTerminalSession(t *testing.T) {
	registry := OpenRegistry(filepath.Join(t.TempDir(), "state.json"))
	terminal := Session{ID: "term-1", Name: "shell", Dir: "/workspace/project", SessionKind: SessionKindTerminal}
	if _, err := registry.Change(context.Background(), RegisterSession(terminal)); err != nil {
		t.Fatal(err)
	}
	_, err := registry.Change(context.Background(), SetSessionAutomation(terminal.ID, terminal.Name, SessionAutomation{
		Name: "Nicht möglich", Instructions: "echo hi", EveryMinutes: 60,
		NextRunAt: time.Now().Add(time.Hour), Enabled: true,
	}))
	if err == nil {
		t.Fatal("terminal Session accepted an automation")
	}
}
