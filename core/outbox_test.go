package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const outboxReadyContent = "Ready\nshift+tab to cycle"

type outboxSend struct {
	runtimeName string
	text        string
	tool        string
	validated   bool
}

// outboxSendRecorder replaces the tmux-backed delivery Seam for a test.
type outboxSendRecorder struct {
	mu    sync.Mutex
	sends []outboxSend
	err   error
}

func (r *outboxSendRecorder) install(t *testing.T) {
	t.Helper()
	previous := sendQueuedPrompt
	sendQueuedPrompt = func(runtimeName, text, tool string, validate promptTargetValidator, observe observationReader) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.sends = append(r.sends, outboxSend{runtimeName: runtimeName, text: text, tool: tool, validated: validate != nil})
		return r.err
	}
	t.Cleanup(func() { sendQueuedPrompt = previous })
}

func (r *outboxSendRecorder) delivered() []outboxSend {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]outboxSend(nil), r.sends...)
}

func (r *outboxSendRecorder) fail(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

func newOutboxDispatchSession(t *testing.T) (*Registry, Session, *outboxSendRecorder) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", statePath)
	registry := OpenRegistry(statePath)
	session := Session{
		ID: SessionID("session-1"), Name: "hera", RuntimeName: "mgt-hera",
		Dir: "/workspace/project", SessionKind: SessionKindCodingAgent,
	}
	if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}
	recorder := &outboxSendRecorder{}
	recorder.install(t)
	return registry, session, recorder
}

func outboxQueue(t *testing.T, registry *Registry, session Session, messages ...QueuedMessage) {
	t.Helper()
	for _, message := range messages {
		result, err := registry.Change(context.Background(), EnqueueSessionMessage(session.ID, session.Name, message))
		if err != nil || !result.Applied {
			t.Fatalf("enqueue %s = %+v, %v", message.ID, result, err)
		}
	}
}

func outboxState(t *testing.T, registry *Registry) *State {
	t.Helper()
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	return &state
}

func outboxSnapshot(observations ...SessionObservation) ObservationSnapshot {
	return ObservationSnapshot{
		ObservedAt: time.Now(), Availability: ObservationAvailable, Sessions: observations,
	}
}

func outboxObservation(sessionID SessionID, status AgentStatus, content string) SessionObservation {
	return SessionObservation{
		SessionID: sessionID, Availability: ObservationAvailable, Presence: SessionPresencePresent,
		Status: status, Tool: AgentToolClaude, Content: content, ContentKnown: true,
	}
}

func outboxReadyObservation(sessionID SessionID) SessionObservation {
	return outboxObservation(sessionID, StatusIdle, outboxReadyContent)
}

func TestDispatchOutboxDeliversReadyHeadAndDequeues(t *testing.T) {
	registry, session, recorder := newOutboxDispatchSession(t)
	outboxQueue(t, registry, session, queuedMessage("d1", QueuedMessageKindMessage, "bitte weiter"))

	DispatchOutbox(context.Background(), outboxState(t, registry), outboxSnapshot(outboxReadyObservation(session.ID)))

	sends := recorder.delivered()
	if len(sends) != 1 || sends[0].runtimeName != session.RuntimeName || sends[0].text != "bitte weiter" {
		t.Fatalf("delivery = %+v", sends)
	}
	if sends[0].tool != AgentToolClaude || sends[0].validated {
		t.Fatalf("plain message must deliver unvalidated to the observed tool: %+v", sends[0])
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 0 {
		t.Fatalf("delivered message stayed queued: %+v", got)
	}
}

func TestDispatchOutboxHoldsWhenTargetIsNotReady(t *testing.T) {
	notReady := map[string]SessionObservation{
		"busy":                          outboxObservation("session-1", StatusRunning, "arbeitet"),
		"blocked":                       outboxObservation("session-1", StatusBlocked, "Do you want to proceed?"),
		"idle without a ready composer": outboxObservation("session-1", StatusIdle, "kein Composer"),
		"absent": {
			SessionID: "session-1", Availability: ObservationAvailable, Presence: SessionPresenceAbsent,
			Status: StatusDead, Tool: AgentToolClaude, ContentKnown: true,
		},
		"unknown": {
			SessionID: "session-1", Availability: ObservationPartial, Presence: SessionPresenceUnknown,
			Status: StatusUnknown, ContentKnown: false,
		},
	}
	for name, observation := range notReady {
		t.Run(name, func(t *testing.T) {
			registry, session, recorder := newOutboxDispatchSession(t)
			outboxQueue(t, registry, session, queuedMessage("h1", QueuedMessageKindMessage, "wartet"))

			DispatchOutbox(context.Background(), outboxState(t, registry), outboxSnapshot(observation))

			if sends := recorder.delivered(); len(sends) != 0 {
				t.Fatalf("message was sent to a target that is not ready: %+v", sends)
			}
			queued := outboxOf(t, registry, session.Name)
			if len(queued) != 1 || !queued[0].AttemptedAt.IsZero() {
				t.Fatalf("queue was disturbed while holding: %+v", queued)
			}
		})
	}

	t.Run("session missing from the snapshot", func(t *testing.T) {
		registry, session, recorder := newOutboxDispatchSession(t)
		outboxQueue(t, registry, session, queuedMessage("h2", QueuedMessageKindMessage, "wartet"))

		DispatchOutbox(context.Background(), outboxState(t, registry), outboxSnapshot())

		if sends := recorder.delivered(); len(sends) != 0 {
			t.Fatalf("unobserved session received a message: %+v", sends)
		}
		if got := outboxOf(t, registry, session.Name); len(got) != 1 {
			t.Fatalf("queue was disturbed: %+v", got)
		}
	})
}

func TestDispatchOutboxDeliversStrictlyFIFO(t *testing.T) {
	registry, session, recorder := newOutboxDispatchSession(t)
	outboxQueue(t, registry, session,
		queuedMessage("f1", QueuedMessageKindMessage, "eins"),
		queuedMessage("f2", QueuedMessageKindMessage, "zwei"),
	)
	snapshot := outboxSnapshot(outboxReadyObservation(session.ID))

	DispatchOutbox(context.Background(), outboxState(t, registry), snapshot)
	sends := recorder.delivered()
	if len(sends) != 1 || sends[0].text != "eins" {
		t.Fatalf("first tick delivered %+v, want only the head", sends)
	}
	queued := outboxOf(t, registry, session.Name)
	if len(queued) != 1 || queued[0].ID != "f2" {
		t.Fatalf("queue after the first tick: %+v", queued)
	}

	DispatchOutbox(context.Background(), outboxState(t, registry), snapshot)
	sends = recorder.delivered()
	if len(sends) != 2 || sends[1].text != "zwei" {
		t.Fatalf("second tick delivered %+v", sends)
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 0 {
		t.Fatalf("queue after the second tick: %+v", got)
	}
}

func TestDispatchOutboxNeverResendsAMessageStuckFromAnEarlierRun(t *testing.T) {
	registry, session, recorder := newOutboxDispatchSession(t)
	outboxQueue(t, registry, session, queuedMessage("s1", QueuedMessageKindMessage, "unklar zugestellt"))
	if _, err := registry.Change(
		context.Background(), MarkQueuedMessageAttempt(session.ID, session.Name, "s1", time.Unix(500, 0)),
	); err != nil {
		t.Fatal(err)
	}
	snapshot := outboxSnapshot(outboxReadyObservation(session.ID))

	DispatchOutbox(context.Background(), outboxState(t, registry), snapshot)
	DispatchOutbox(context.Background(), outboxState(t, registry), snapshot)

	if sends := recorder.delivered(); len(sends) != 0 {
		t.Fatalf("stuck message was resent: %+v", sends)
	}
	queued := outboxOf(t, registry, session.Name)
	if len(queued) != 1 || queued[0].AttemptedAt.IsZero() {
		t.Fatalf("stuck message lost its attempt marker: %+v", queued)
	}

	// The user's retry clears the marker and makes the message deliverable again.
	if err := RetryQueuedMessage(session.ID, "s1"); err != nil {
		t.Fatalf("RetryQueuedMessage: %v", err)
	}
	DispatchOutbox(context.Background(), outboxState(t, registry), snapshot)
	if sends := recorder.delivered(); len(sends) != 1 || sends[0].text != "unklar zugestellt" {
		t.Fatalf("retried message was not delivered: %+v", sends)
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 0 {
		t.Fatalf("retried message stayed queued: %+v", got)
	}
}

func TestDispatchOutboxResetsAttemptAfterAFailedSendAndRetriesLater(t *testing.T) {
	registry, session, recorder := newOutboxDispatchSession(t)
	outboxQueue(t, registry, session, queuedMessage("r1", QueuedMessageKindMessage, "erneut"))
	snapshot := outboxSnapshot(outboxReadyObservation(session.ID))
	sendFailure := errors.New("Prompt an tmux senden: kaputt")
	recorder.fail(sendFailure)

	DispatchOutbox(context.Background(), outboxState(t, registry), snapshot)
	if sends := recorder.delivered(); len(sends) != 1 {
		t.Fatalf("failed tick sends = %+v", sends)
	}
	queued := outboxOf(t, registry, session.Name)
	if len(queued) != 1 || !queued[0].AttemptedAt.IsZero() {
		t.Fatalf("failed send did not reset the attempt marker: %+v", queued)
	}

	recorder.fail(nil)
	DispatchOutbox(context.Background(), outboxState(t, registry), snapshot)
	if sends := recorder.delivered(); len(sends) != 2 || sends[1].text != "erneut" {
		t.Fatalf("later tick did not retry: %+v", sends)
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 0 {
		t.Fatalf("retried message stayed queued: %+v", got)
	}
}

func TestDispatchOutboxHandoffRequiresDeliveryReadyTarget(t *testing.T) {
	registry, session, recorder := newOutboxDispatchSession(t)
	outboxQueue(t, registry, session, queuedMessage("g1", QueuedMessageKindHandoff, "Übergabe an hera"))

	// StatusAgents passes the generic prompt-target check but never the strict
	// handoff readiness contract.
	working := outboxObservation(session.ID, StatusAgents, outboxReadyContent)
	DispatchOutbox(context.Background(), outboxState(t, registry), outboxSnapshot(working))
	if sends := recorder.delivered(); len(sends) != 0 {
		t.Fatalf("handoff was delivered to a working target: %+v", sends)
	}

	codex := outboxObservation(session.ID, StatusIdle, outboxReadyContent)
	codex.Tool = AgentToolCodex
	DispatchOutbox(context.Background(), outboxState(t, registry), outboxSnapshot(codex))
	if sends := recorder.delivered(); len(sends) != 0 {
		t.Fatalf("handoff was delivered to a non-Claude target: %+v", sends)
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 1 {
		t.Fatalf("held handoff left the queue: %+v", got)
	}

	DispatchOutbox(context.Background(), outboxState(t, registry), outboxSnapshot(outboxReadyObservation(session.ID)))
	sends := recorder.delivered()
	if len(sends) != 1 || sends[0].text != "Übergabe an hera" {
		t.Fatalf("ready handoff was not delivered: %+v", sends)
	}
	if !sends[0].validated {
		t.Fatal("handoff delivery lost its delivery-time validator")
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 0 {
		t.Fatalf("delivered handoff stayed queued: %+v", got)
	}
}

func TestSendQueuedMessageValidatesQueuesAndDeliversImmediately(t *testing.T) {
	registry, session, recorder := newOutboxDispatchSession(t)
	// The immediate kick observes the runtime through a synthetic prompt target.
	observe := func(_ context.Context, sessions []Session) ObservationSnapshot {
		if len(sessions) != 1 || sessions[0].RuntimeName != session.RuntimeName {
			t.Fatalf("unexpected observation request: %+v", sessions)
		}
		return outboxSnapshot(outboxReadyObservation(sessions[0].ID))
	}

	if err := SendQueuedMessageWithObserver("session-x", QueuedMessageKindMessage, "hallo", observe); err == nil ||
		!strings.Contains(err.Error(), "SessionID") {
		t.Fatalf("unknown SessionID = %v, want an error", err)
	}
	if err := SendQueuedMessageWithObserver(session.ID, QueuedMessageKindMessage, "  \n ", observe); err == nil ||
		!strings.Contains(err.Error(), "leer") {
		t.Fatalf("empty text = %v, want an error", err)
	}

	if err := SendQueuedMessageWithObserver(session.ID, QueuedMessageKindMessage, "hallo", observe); err != nil {
		t.Fatalf("SendQueuedMessage: %v", err)
	}
	sends := recorder.delivered()
	if len(sends) != 1 || sends[0].text != "hallo" || sends[0].runtimeName != session.RuntimeName {
		t.Fatalf("immediate delivery = %+v", sends)
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 0 {
		t.Fatalf("delivered message stayed queued: %+v", got)
	}

	// A busy target keeps the message queued, and the double click is deduped.
	busy := func(_ context.Context, sessions []Session) ObservationSnapshot {
		return outboxSnapshot(outboxObservation(sessions[0].ID, StatusRunning, "arbeitet"))
	}
	for range 2 {
		if err := SendQueuedMessageWithObserver(session.ID, QueuedMessageKindSkill, "/done ", busy); err != nil {
			t.Fatalf("queueing for a busy session: %v", err)
		}
	}
	queued := outboxOf(t, registry, session.Name)
	if len(queued) != 1 || queued[0].Text != "/done " || queued[0].Kind != QueuedMessageKindSkill {
		t.Fatalf("busy queue = %+v, want one deduplicated message", queued)
	}
	if queued[0].ID == "" || queued[0].EnqueuedAt.IsZero() {
		t.Fatalf("queued message lacks identity or timestamp: %+v", queued[0])
	}
	if len(recorder.delivered()) != 1 {
		t.Fatalf("busy target received a send: %+v", recorder.delivered())
	}

	if err := DiscardQueuedMessage(session.ID, queued[0].ID); err != nil {
		t.Fatalf("DiscardQueuedMessage: %v", err)
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 0 {
		t.Fatalf("discarded message stayed queued: %+v", got)
	}
}

// A skill for a busy Session is no longer a failed action: it waits in the
// Outbox until the Session shows a ready composer.
func TestSendSkillByIDQueuesForBusySessionAndDeliversWhenReady(t *testing.T) {
	registry, session, recorder := newOutboxDispatchSession(t)
	busy := func(_ context.Context, sessions []Session) ObservationSnapshot {
		return outboxSnapshot(outboxObservation(sessions[0].ID, StatusRunning, "arbeitet"))
	}

	if err := SendSkillByIDWithObserver(session.ID, "/review ", busy); err != nil {
		t.Fatalf("SendSkillByIDWithObserver() error = %v", err)
	}
	queued := outboxOf(t, registry, session.Name)
	if len(queued) != 1 || queued[0].Kind != QueuedMessageKindSkill || queued[0].Text != "/review " {
		t.Fatalf("busy queue = %+v, want one queued skill", queued)
	}
	if sends := recorder.delivered(); len(sends) != 0 {
		t.Fatalf("busy Session received a send: %+v", sends)
	}

	DispatchOutbox(context.Background(), outboxState(t, registry), outboxSnapshot(outboxReadyObservation(session.ID)))
	sends := recorder.delivered()
	if len(sends) != 1 || sends[0].text != "/review " || sends[0].runtimeName != session.RuntimeName {
		t.Fatalf("delivery after readiness = %+v", sends)
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 0 {
		t.Fatalf("delivered skill stayed queued: %+v", got)
	}
}

// A busy handoff target keeps the eagerly built source context queued as kind
// "handoff" and receives it under the strict delivery-time contract.
func TestHandoffSessionQueuesForBusyTargetAsHandoffKind(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", statePath)
	registry := OpenRegistry(statePath)
	source := Session{
		ID: "source-id", Name: "source", RuntimeName: "mgt-source", Dir: "/work/source",
		SessionKind: SessionKindCodingAgent,
		AgentRuns:   []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "source-run"}},
	}
	target := Session{
		ID: "target-id", Name: "target", RuntimeName: "mgt-target", Dir: "/work/target",
		SessionKind: SessionKindCodingAgent,
	}
	for _, session := range []Session{source, target} {
		if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
			t.Fatal(err)
		}
	}
	recorder := &outboxSendRecorder{}
	recorder.install(t)

	busy := func(_ context.Context, sessions []Session) ObservationSnapshot {
		return outboxSnapshot(outboxObservation(sessions[0].ID, StatusRunning, "arbeitet"))
	}
	snapshot := outboxSnapshot(
		outboxObservation(source.ID, StatusRunning, "arbeitet"),
		outboxObservation(target.ID, StatusRunning, "arbeitet"),
	)
	if err := HandoffSessionWithObserver(outboxState(t, registry), snapshot, source.ID, target.ID, busy); err != nil {
		t.Fatalf("HandoffSessionWithObserver() error = %v", err)
	}
	queued := outboxOf(t, registry, target.Name)
	if len(queued) != 1 || queued[0].Kind != QueuedMessageKindHandoff {
		t.Fatalf("target Outbox = %+v, want one queued handoff", queued)
	}
	if !strings.Contains(queued[0].Text, `Magentic-SessionID: "source-id"`) {
		t.Fatalf("queued handoff lost its source context:\n%s", queued[0].Text)
	}
	if sends := recorder.delivered(); len(sends) != 0 {
		t.Fatalf("busy target received a send: %+v", sends)
	}

	DispatchOutbox(context.Background(), outboxState(t, registry), outboxSnapshot(outboxReadyObservation(target.ID)))
	sends := recorder.delivered()
	if len(sends) != 1 || sends[0].runtimeName != target.RuntimeName || !sends[0].validated {
		t.Fatalf("handoff delivery = %+v", sends)
	}
	if got := outboxOf(t, registry, target.Name); len(got) != 0 {
		t.Fatalf("delivered handoff stayed queued: %+v", got)
	}
}

func TestSendQueuedMessageRejectsTerminalSessions(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", statePath)
	registry := OpenRegistry(statePath)
	terminal := Session{
		ID: SessionID("session-term"), Name: "term-hera", RuntimeName: "mgt-term-hera",
		Dir: "/workspace/project", SessionKind: SessionKindTerminal,
	}
	if _, err := registry.Change(context.Background(), RegisterSession(terminal)); err != nil {
		t.Fatal(err)
	}
	recorder := &outboxSendRecorder{}
	recorder.install(t)

	err := SendQueuedMessage(terminal.ID, QueuedMessageKindMessage, "hallo")
	if err == nil || !strings.Contains(err.Error(), "Terminal-Session") {
		t.Fatalf("terminal target = %v, want a rejection", err)
	}
	if got := outboxOf(t, registry, terminal.Name); len(got) != 0 {
		t.Fatalf("terminal session was queued: %+v", got)
	}
	if sends := recorder.delivered(); len(sends) != 0 {
		t.Fatalf("terminal session received a send: %+v", sends)
	}
}
