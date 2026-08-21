package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newOutboxRegistry(t *testing.T) (*Registry, string, Session) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	registry := OpenRegistry(path)
	session := Session{ID: SessionID("session-1"), Name: "hera", Dir: "/workspace/project"}
	if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}
	return registry, path, session
}

func outboxOf(t *testing.T, registry *Registry, name string) []QueuedMessage {
	t.Helper()
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	session := state.AgentByName(name)
	if session == nil {
		t.Fatalf("Session %q nicht im Snapshot: %+v", name, state.Agents)
	}
	return session.Outbox
}

func queuedMessage(id string, kind QueuedMessageKind, text string) QueuedMessage {
	return QueuedMessage{ID: id, Kind: kind, Text: text, EnqueuedAt: time.Unix(100, 0)}
}

func TestRegistryOutboxEnqueueAppendsFIFOAndDedupes(t *testing.T) {
	registry, _, session := newOutboxRegistry(t)
	first := queuedMessage("m1", QueuedMessageKindMessage, "hallo")
	second := queuedMessage("m2", QueuedMessageKindSkill, "/deploy")
	for _, message := range []QueuedMessage{first, second} {
		result, err := registry.Change(context.Background(), EnqueueSessionMessage(session.ID, session.Name, message))
		if err != nil || !result.Applied {
			t.Fatalf("EnqueueSessionMessage(%s) = %+v, %v", message.ID, result, err)
		}
		if result.SessionID != session.ID {
			t.Fatalf("EnqueueSessionMessage reported SessionID %q, want %q", result.SessionID, session.ID)
		}
	}
	outbox := outboxOf(t, registry, session.Name)
	if len(outbox) != 2 || outbox[0].ID != "m1" || outbox[1].ID != "m2" {
		t.Fatalf("Outbox is not FIFO: %+v", outbox)
	}
	if outbox[0].Kind != QueuedMessageKindMessage || outbox[0].Text != "hallo" || !outbox[0].EnqueuedAt.Equal(time.Unix(100, 0)) {
		t.Fatalf("queued message was not preserved: %+v", outbox[0])
	}

	duplicate := queuedMessage("m3", QueuedMessageKindMessage, "hallo")
	result, err := registry.Change(context.Background(), EnqueueSessionMessage(session.ID, session.Name, duplicate))
	if err != nil {
		t.Fatalf("duplicate enqueue returned an error: %v", err)
	}
	if result.Applied {
		t.Fatalf("duplicate Kind+Text was queued: %+v", outboxOf(t, registry, session.Name))
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 2 {
		t.Fatalf("duplicate enqueue changed the Outbox: %+v", got)
	}

	// Same text under a different Kind is a different request.
	other := queuedMessage("m4", QueuedMessageKindHandoff, "hallo")
	if result, err := registry.Change(context.Background(), EnqueueSessionMessage(session.ID, session.Name, other)); err != nil || !result.Applied {
		t.Fatalf("enqueue of a different Kind = %+v, %v", result, err)
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 3 || got[2].ID != "m4" {
		t.Fatalf("different Kind was not appended: %+v", got)
	}
}

func TestRegistryOutboxAttemptMarkAndReset(t *testing.T) {
	registry, _, session := newOutboxRegistry(t)
	message := queuedMessage("m1", QueuedMessageKindMessage, "hallo")
	if _, err := registry.Change(context.Background(), EnqueueSessionMessage(session.ID, session.Name, message)); err != nil {
		t.Fatal(err)
	}
	attemptedAt := time.Unix(500, 0)
	result, err := registry.Change(context.Background(), MarkQueuedMessageAttempt(session.ID, session.Name, "m1", attemptedAt))
	if err != nil || !result.Applied {
		t.Fatalf("MarkQueuedMessageAttempt = %+v, %v", result, err)
	}
	outbox := outboxOf(t, registry, session.Name)
	if len(outbox) != 1 || !outbox[0].AttemptedAt.Equal(attemptedAt) {
		t.Fatalf("AttemptedAt was not recorded: %+v", outbox)
	}

	result, err = registry.Change(context.Background(), ResetQueuedMessageAttempt(session.ID, session.Name, "m1"))
	if err != nil || !result.Applied {
		t.Fatalf("ResetQueuedMessageAttempt = %+v, %v", result, err)
	}
	outbox = outboxOf(t, registry, session.Name)
	if len(outbox) != 1 || !outbox[0].AttemptedAt.IsZero() {
		t.Fatalf("AttemptedAt was not cleared: %+v", outbox)
	}

	result, err = registry.Change(context.Background(), ResetQueuedMessageAttempt(session.ID, session.Name, "m1"))
	if err != nil || result.Applied {
		t.Fatalf("repeated reset = %+v, %v, want no-op without error", result, err)
	}
	result, err = registry.Change(context.Background(), MarkQueuedMessageAttempt(session.ID, session.Name, "unknown", attemptedAt))
	if err != nil || result.Applied {
		t.Fatalf("attempt on an unknown message = %+v, %v, want no-op without error", result, err)
	}
}

func TestRegistryOutboxDequeueRemovesByID(t *testing.T) {
	registry, _, session := newOutboxRegistry(t)
	for _, message := range []QueuedMessage{
		queuedMessage("m1", QueuedMessageKindMessage, "eins"),
		queuedMessage("m2", QueuedMessageKindMessage, "zwei"),
		queuedMessage("m3", QueuedMessageKindMessage, "drei"),
	} {
		if _, err := registry.Change(context.Background(), EnqueueSessionMessage(session.ID, session.Name, message)); err != nil {
			t.Fatal(err)
		}
	}
	result, err := registry.Change(context.Background(), DequeueSessionMessage(session.ID, session.Name, "m1"))
	if err != nil || !result.Applied {
		t.Fatalf("dequeue of the head = %+v, %v", result, err)
	}
	outbox := outboxOf(t, registry, session.Name)
	if len(outbox) != 2 || outbox[0].ID != "m2" || outbox[1].ID != "m3" {
		t.Fatalf("head dequeue broke the order: %+v", outbox)
	}

	if _, err := registry.Change(context.Background(), EnqueueSessionMessage(session.ID, session.Name, queuedMessage("m4", QueuedMessageKindMessage, "vier"))); err != nil {
		t.Fatal(err)
	}
	result, err = registry.Change(context.Background(), DequeueSessionMessage(session.ID, session.Name, "m3"))
	if err != nil || !result.Applied {
		t.Fatalf("dequeue from the middle = %+v, %v", result, err)
	}
	outbox = outboxOf(t, registry, session.Name)
	if len(outbox) != 2 || outbox[0].ID != "m2" || outbox[1].ID != "m4" {
		t.Fatalf("middle dequeue broke the order: %+v", outbox)
	}

	result, err = registry.Change(context.Background(), DequeueSessionMessage(session.ID, session.Name, "m3"))
	if err != nil || result.Applied {
		t.Fatalf("repeated dequeue = %+v, %v, want no-op without error", result, err)
	}
}

func TestRegistryOutboxChangesRejectUnknownSession(t *testing.T) {
	registry, _, _ := newOutboxRegistry(t)
	changes := map[string]RegistryChange{
		"enqueue": EnqueueSessionMessage("session-x", "ghost", queuedMessage("m1", QueuedMessageKindMessage, "hallo")),
		"attempt": MarkQueuedMessageAttempt("session-x", "ghost", "m1", time.Unix(500, 0)),
		"dequeue": DequeueSessionMessage("session-x", "ghost", "m1"),
		"reset":   ResetQueuedMessageAttempt("session-x", "ghost", "m1"),
	}
	for name, change := range changes {
		result, err := registry.Change(context.Background(), change)
		if err == nil || result.Applied {
			t.Fatalf("%s against an unknown Session = %+v, %v, want an error", name, result, err)
		}
	}
}

func TestRegistryOutboxRoundTripsThroughRegistryFile(t *testing.T) {
	registry, path, session := newOutboxRegistry(t)
	message := queuedMessage("m1", QueuedMessageKindHandoff, "Übergabe an hera")
	if _, err := registry.Change(context.Background(), EnqueueSessionMessage(session.ID, session.Name, message)); err != nil {
		t.Fatal(err)
	}
	attemptedAt := time.Unix(900, 0).UTC()
	if _, err := registry.Change(context.Background(), MarkQueuedMessageAttempt(session.ID, session.Name, "m1", attemptedAt)); err != nil {
		t.Fatal(err)
	}

	reopened := OpenRegistry(path)
	snapshot, err := reopened.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	restored := state.AgentByName(session.Name)
	if restored == nil || len(restored.Outbox) != 1 {
		t.Fatalf("Outbox did not survive the Registry file: %+v", state.Agents)
	}
	queued := restored.Outbox[0]
	if queued.ID != "m1" || queued.Kind != QueuedMessageKindHandoff || queued.Text != message.Text {
		t.Fatalf("queued message was not restored: %+v", queued)
	}
	if !queued.EnqueuedAt.Equal(message.EnqueuedAt) || !queued.AttemptedAt.Equal(attemptedAt) {
		t.Fatalf("queued timestamps were not restored: %+v", queued)
	}

	// Snapshots must not alias the Outbox backing array.
	other := snapshot.State()
	state.AgentByName(session.Name).Outbox[0].Text = "mutiert"
	if other.AgentByName(session.Name).Outbox[0].Text != message.Text {
		t.Fatal("snapshot copies share the Outbox backing array")
	}
}
