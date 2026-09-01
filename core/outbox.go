package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// outboxAttempts records the queued messages this process run marked as
// attempted and has not resolved yet. A persisted AttemptedAt without an entry
// here belongs to an earlier run: the delivery outcome is unknown after a crash
// and must never be resent automatically (same principle as the
// delivery_unknown reasoning in the Session Lifecycle). An entry that is
// present means a delivery is in flight right now, so a concurrent tick must
// leave that message alone as well.
type outboxAttempts struct {
	mu  sync.Mutex
	ids map[string]bool
}

var ownOutboxAttempts = &outboxAttempts{ids: map[string]bool{}}

// begin claims a message for delivery. It reports false when this run is
// already delivering it.
func (a *outboxAttempts) begin(messageID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ids[messageID] {
		return false
	}
	a.ids[messageID] = true
	return true
}

func (a *outboxAttempts) markedHere(messageID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ids[messageID]
}

func (a *outboxAttempts) finish(messageID string) {
	a.mu.Lock()
	delete(a.ids, messageID)
	a.mu.Unlock()
}

// sendQueuedPrompt is the delivery Seam. Production reuses the per-session
// promptTargetQueue, so a queued message serializes with initial-prompt and
// handoff delivery to the same runtime through its sendSlot. Readiness was
// already decided by the dispatcher, so delivery never waits for it here.
// Tests replace this function instead of driving a real tmux.
var sendQueuedPrompt = func(runtimeName, text, tool string, validate promptTargetValidator, observe observationReader) error {
	return enqueuePromptUsing(runtimeName, text, true, tool, false, false, true, validate, observe)
}

// DispatchOutbox delivers the head of every Session Outbox that the given
// Observation shows as ready. Sessions that are busy, blocked, absent or
// unknown keep their queue untouched and are retried on the next tick.
func DispatchOutbox(ctx context.Context, st *State, snapshot ObservationSnapshot) {
	DispatchOutboxWithObserver(ctx, st, snapshot, nil)
}

// DispatchOutboxWithObserver keeps a caller's coherent Observation Adapter in
// use through delivery-time revalidation. A nil Observer uses the production
// Observation Module.
func DispatchOutboxWithObserver(ctx context.Context, st *State, snapshot ObservationSnapshot, observe observationReader) {
	if st == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	observed := make(map[SessionID]SessionObservation, len(snapshot.Sessions))
	for _, session := range snapshot.Sessions {
		observed[session.SessionID] = session
	}
	for _, session := range st.Agents {
		if len(session.Outbox) == 0 {
			continue
		}
		target, found := observed[session.ID]
		if !found {
			continue
		}
		dispatchOutboxHead(ctx, session, promptTargetObservationFromSession(target), observe)
	}
}

// dispatchOutboxHead attempts the head message of one Session, strictly FIFO:
// the second message is only ever considered after the first left the Outbox.
func dispatchOutboxHead(ctx context.Context, session Session, target promptTargetObservation, observe observationReader) {
	if len(session.Outbox) == 0 || session.IsTerm() || !validRuntimeIdentity(session.TmuxName()) {
		return
	}
	head := session.Outbox[0]
	if !head.AttemptedAt.IsZero() && !ownOutboxAttempts.markedHere(head.ID) {
		// Stuck: an earlier process run started this delivery and never finished
		// it. The UI offers retry or discard; automatic resending could duplicate.
		return
	}
	validate, ready := outboxDeliveryPolicy(session.Name, head.Kind, target)
	if !ready {
		return
	}
	if !ownOutboxAttempts.begin(head.ID) {
		return
	}
	registry := OpenRegistry(StatePath())
	result, err := registry.Change(ctx, MarkQueuedMessageAttempt(session.ID, session.Name, head.ID, time.Now()))
	if err != nil || !result.Applied {
		ownOutboxAttempts.finish(head.ID)
		if err != nil {
			Logf("Outbox %s: Zustellversuch nicht vermerkt: %v", session.Name, err)
		}
		return
	}
	if err := sendQueuedPrompt(session.TmuxName(), head.Text, target.Tool, validate, observe); err != nil {
		ownOutboxAttempts.finish(head.ID)
		if _, resetErr := registry.Change(ctx, ResetQueuedMessageAttempt(session.ID, session.Name, head.ID)); resetErr != nil {
			Logf("Outbox %s: Zustellversuch nicht zurückgesetzt: %v", session.Name, resetErr)
		}
		Logf("Outbox %s: %v", session.TmuxName(), err)
		return
	}
	if _, err := registry.Change(ctx, DequeueSessionMessage(session.ID, session.Name, head.ID)); err != nil {
		// The message was delivered but stays queued with its attempt marker. It
		// is now stuck on purpose: only the user may resend it.
		ownOutboxAttempts.finish(head.ID)
		Logf("Outbox %s: zugestellte Nachricht nicht entfernt: %v", session.Name, err)
		return
	}
	ownOutboxAttempts.finish(head.ID)
}

// outboxDeliveryPolicy decides whether the observed target accepts this kind of
// message right now, and which delivery-time validator delivery must keep.
func outboxDeliveryPolicy(name string, kind QueuedMessageKind, target promptTargetObservation) (promptTargetValidator, bool) {
	if err := validatePromptTargetObservation(name, target); err != nil {
		return nil, false
	}
	if kind == QueuedMessageKindHandoff {
		// Handoff is Claude-only and keeps its strict readiness contract at
		// delivery time.
		if err := validateHandoffDeliveryReady(name, target); err != nil {
			return nil, false
		}
		return handoffLiveTargetValidator(name), true
	}
	// Every vendor whose screens were recorded is held to the same bar: the
	// message goes out only when that vendor's own composer is visible. A tool
	// without recorded semantics keeps the queued literal path.
	if _, known := providerForPaneCommand(target.Tool); known && target.Input != promptInputReady {
		return nil, false
	}
	return nil, true
}

// SendQueuedMessage durably queues a message for a Session and immediately
// tries to deliver it, so a ready Session sees it without waiting for the next
// watch tick.
func SendQueuedMessage(sessionID SessionID, kind QueuedMessageKind, text string) error {
	return SendQueuedMessageWithObserver(sessionID, kind, text, nil)
}

// SendQueuedMessageWithObserver keeps a caller's coherent Observation Adapter
// in use for the immediate delivery attempt.
func SendQueuedMessageWithObserver(sessionID SessionID, kind QueuedMessageKind, text string, observe func(context.Context, []Session) ObservationSnapshot) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("Prompt ist leer")
	}
	session, err := queuedMessageSession(sessionID)
	if err != nil {
		return err
	}
	if session.IsTerm() {
		return fmt.Errorf("%s ist eine Terminal-Session — dort läuft kein Claude", session.Name)
	}
	message := QueuedMessage{ID: NewUUID(), Kind: kind, Text: text, EnqueuedAt: time.Now()}
	ctx := context.Background()
	if _, err := OpenRegistry(StatePath()).Change(ctx, EnqueueSessionMessage(session.ID, session.Name, message)); err != nil {
		return err
	}
	kickOutboxForSession(ctx, session.ID, observe)
	return nil
}

// DiscardQueuedMessage removes a queued message the user no longer wants.
func DiscardQueuedMessage(sessionID SessionID, messageID string) error {
	session, err := queuedMessageSession(sessionID)
	if err != nil {
		return err
	}
	if _, err := OpenRegistry(StatePath()).Change(
		context.Background(), DequeueSessionMessage(session.ID, session.Name, messageID),
	); err != nil {
		return err
	}
	ownOutboxAttempts.finish(messageID)
	return nil
}

// RetryQueuedMessage clears the attempt marker of a stuck message so the
// dispatcher picks it up again. The in-memory attempt set stays untouched: a
// stuck message was marked by another process run, and a message this run is
// currently delivering must keep its in-flight claim.
func RetryQueuedMessage(sessionID SessionID, messageID string) error {
	session, err := queuedMessageSession(sessionID)
	if err != nil {
		return err
	}
	_, err = OpenRegistry(StatePath()).Change(
		context.Background(), ResetQueuedMessageAttempt(session.ID, session.Name, messageID),
	)
	return err
}

func queuedMessageSession(sessionID SessionID) (Session, error) {
	st, err := LoadState()
	if err != nil {
		return Session{}, err
	}
	session := st.SessionByID(sessionID)
	if session == nil {
		return Session{}, fmt.Errorf("unbekannte SessionID: %s", sessionID)
	}
	return *session, nil
}

// kickOutboxForSession runs one dispatch attempt for a single Session against a
// fresh Observation of just that runtime.
func kickOutboxForSession(ctx context.Context, sessionID SessionID, observe observationReader) {
	st, err := LoadState()
	if err != nil {
		Logf("Outbox: Registry nicht lesbar: %v", err)
		return
	}
	session := st.SessionByID(sessionID)
	if session == nil || len(session.Outbox) == 0 || !validRuntimeIdentity(session.TmuxName()) {
		return
	}
	dispatchOutboxHead(ctx, *session, observePromptTarget(ctx, session.TmuxName(), observe), observe)
}
