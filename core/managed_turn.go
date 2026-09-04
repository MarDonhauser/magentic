package core

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// TurnEndReason names why a managed Session's turn ended. The reason is a
// protocol fact — completed, interrupted, or failed with the vendor's own
// reason — never a guess from a Session going quiet.
type TurnEndReason string

const (
	TurnEndCompleted   TurnEndReason = "completed"
	TurnEndInterrupted TurnEndReason = "interrupted"
	TurnEndFailed      TurnEndReason = "failed"
)

// ManagedTurn is what the daemon knows about one managed Session's turn: the
// running turn with the Outbox message that started it, or the last ended
// turn with its reason. A long silent stretch changes nothing — only a
// protocol event ends a turn.
type ManagedTurn struct {
	SessionID  SessionID     `json:"sessionId"`
	Running    bool          `json:"running"`
	MessageID  string        `json:"messageId,omitempty"`
	StartedAt  time.Time     `json:"startedAt,omitzero"`
	EndedAt    time.Time     `json:"endedAt,omitzero"`
	EndReason  TurnEndReason `json:"endReason,omitempty"`
	FailReason string        `json:"failReason,omitempty"`
}

// ManagedInflight is a prompt sent to a managed Session whose acknowledgement
// has not arrived. It is presented as in flight rather than as delivered, and
// it is never resent on the strength of the missing acknowledgement.
type ManagedInflight struct {
	SessionID SessionID `json:"sessionId"`
	MessageID string    `json:"messageId"`
	Text      string    `json:"text"`
	SentAt    time.Time `json:"sentAt"`
	// FailReason carries the last failed delivery for this Session, so the
	// reason a prompt is still queued is available to the developer.
	FailReason string    `json:"failReason,omitempty"`
	FailedAt   time.Time `json:"failedAt,omitzero"`
}

// ManagedTurnTracker holds one managed Session's turn, its in-flight prompt
// and its streamed conversation. The agent host owns the tracker for its own
// Session; the daemon holds one per known Session for what the host reported.
// All methods are safe for concurrent use.
type ManagedTurnTracker struct {
	mu            sync.Mutex
	turns         map[SessionID]*ManagedTurn
	inflight      map[SessionID]*ManagedInflight
	conversations map[SessionID]*Conversation
	now           func() time.Time
}

// NewManagedTurnTracker creates an empty tracker.
func NewManagedTurnTracker() *ManagedTurnTracker {
	return &ManagedTurnTracker{
		turns:         map[SessionID]*ManagedTurn{},
		inflight:      map[SessionID]*ManagedInflight{},
		conversations: map[SessionID]*Conversation{},
		now:           time.Now,
	}
}

// StartTurn records that a turn began for the delivered Outbox message. A
// second start while a turn is running is a caller error and leaves the
// running turn untouched.
func (t *ManagedTurnTracker) StartTurn(sessionID SessionID, messageID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if current := t.turns[sessionID]; current != nil && current.Running {
		return fmt.Errorf("für Session %q läuft bereits ein Turn", sessionID)
	}
	t.turns[sessionID] = &ManagedTurn{
		SessionID: sessionID, Running: true, MessageID: messageID, StartedAt: t.now(),
	}
	return nil
}

// EndTurn records the turn's end with its explicit reason. A failed turn
// carries the vendor's own reason. Ending a Session with no running turn
// reports false and changes nothing.
func (t *ManagedTurnTracker) EndTurn(sessionID SessionID, reason TurnEndReason, failReason string) (ManagedTurn, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	current := t.turns[sessionID]
	if current == nil || !current.Running {
		return ManagedTurn{SessionID: sessionID}, false
	}
	current.Running = false
	current.EndedAt = t.now()
	current.EndReason = reason
	current.FailReason = failReason
	return *current, true
}

// InterruptTurn ends the running turn with the interrupted reason and leaves
// everything else — the Session, its process, its conversation — untouched.
// Interrupting a Session with no running turn is refused and stops nothing.
func (t *ManagedTurnTracker) InterruptTurn(sessionID SessionID) (ManagedTurn, error) {
	ended, ok := t.EndTurn(sessionID, TurnEndInterrupted, "")
	if !ok {
		return ManagedTurn{SessionID: sessionID},
			fmt.Errorf("für Session %q läuft kein Turn, der unterbrochen werden könnte", sessionID)
	}
	return ended, nil
}

// TurnRunning reports whether the Session has a running turn.
func (t *ManagedTurnTracker) TurnRunning(sessionID SessionID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.turns[sessionID] != nil && t.turns[sessionID].Running
}

// TurnState returns the Session's current or last turn, or false when no turn
// was ever recorded for it.
func (t *ManagedTurnTracker) TurnState(sessionID SessionID) (ManagedTurn, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	current := t.turns[sessionID]
	if current == nil {
		return ManagedTurn{SessionID: sessionID}, false
	}
	return *current, true
}

// MarkInflight records that a prompt was sent and its acknowledgement is
// still outstanding. A second prompt is never sent while one is in flight:
// the Outbox keeps its strict FIFO through this claim.
func (t *ManagedTurnTracker) MarkInflight(sessionID SessionID, messageID, text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight[sessionID] = &ManagedInflight{
		SessionID: sessionID, MessageID: messageID, Text: text, SentAt: t.now(),
	}
}

// Inflight reports the Session's unacknowledged prompt, if any.
func (t *ManagedTurnTracker) Inflight(sessionID SessionID) (ManagedInflight, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pending := t.inflight[sessionID]
	if pending == nil || pending.MessageID == "" {
		return ManagedInflight{SessionID: sessionID}, false
	}
	return *pending, true
}

// ConfirmEcho advances the Outbox on the protocol's echo of the delivered
// prompt: the matching in-flight prompt leaves the queue and its turn starts.
// Anything else — a missing echo, an echo for another message — advances
// nothing and resends nothing.
func (t *ManagedTurnTracker) ConfirmEcho(sessionID SessionID, messageID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	pending := t.inflight[sessionID]
	if pending == nil || pending.MessageID == "" || pending.MessageID != messageID {
		return false
	}
	delete(t.inflight, sessionID)
	t.turns[sessionID] = &ManagedTurn{
		SessionID: sessionID, Running: true, MessageID: messageID, StartedAt: t.now(),
	}
	return true
}

// FailDelivery records that delivering the prompt failed. The prompt stays
// queued with its reason — it is reset to queued, never dropped and never
// retried here.
func (t *ManagedTurnTracker) FailDelivery(sessionID SessionID, messageID, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pending := t.inflight[sessionID]
	if pending == nil || pending.MessageID != messageID {
		pending = &ManagedInflight{SessionID: sessionID, MessageID: messageID}
	}
	pending.MessageID = ""
	pending.FailReason = reason
	pending.FailedAt = t.now()
	t.inflight[sessionID] = pending
}

// DeliveryFailure reports the last failed delivery for a Session, if any.
func (t *ManagedTurnTracker) DeliveryFailure(sessionID SessionID) (reason string, at time.Time, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pending := t.inflight[sessionID]
	if pending == nil || pending.FailReason == "" {
		return "", time.Time{}, false
	}
	return pending.FailReason, pending.FailedAt, true
}

// PublishChunk publishes streamed output as it arrives, marked as still
// being produced. Repeated chunks for the same message supersede it in
// place.
func (t *ManagedTurnTracker) PublishChunk(sessionID SessionID, item Item) Item {
	t.mu.Lock()
	defer t.mu.Unlock()
	item.InProgress = true
	if item.OccurredAt.IsZero() {
		item.OccurredAt = t.now()
	}
	t.conversationFor(sessionID).Apply(item)
	return item
}

// CompleteMessage supersedes the streamed message with its final form. After
// this call the conversation holds the message exactly once, not marked as
// in progress.
func (t *ManagedTurnTracker) CompleteMessage(sessionID SessionID, item Item) Item {
	t.mu.Lock()
	defer t.mu.Unlock()
	item.InProgress = false
	if item.OccurredAt.IsZero() {
		item.OccurredAt = t.now()
	}
	t.conversationFor(sessionID).Apply(item)
	return item
}

// StreamedConversation returns the streamed Items held for a Session in
// order. It is what the conversation surface renders; completed messages
// appear in their final form, in-progress ones marked as such.
func (t *ManagedTurnTracker) StreamedConversation(sessionID SessionID) []Item {
	t.mu.Lock()
	defer t.mu.Unlock()
	conversation := t.conversations[sessionID]
	if conversation == nil {
		return nil
	}
	return append([]Item(nil), conversation.Items...)
}

// ApplyStreamedItems folds Items the host reported (for example after a
// daemon restart) into the held conversation, superseding by identity.
func (t *ManagedTurnTracker) ApplyStreamedItems(sessionID SessionID, items []Item) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.conversationFor(sessionID).Apply(items...)
}

func (t *ManagedTurnTracker) conversationFor(sessionID SessionID) *Conversation {
	conversation := t.conversations[sessionID]
	if conversation == nil {
		conversation = &Conversation{}
		t.conversations[sessionID] = conversation
	}
	return conversation
}

// ErrManagedHostUnreachable reports a managed Session whose host cannot be
// reached. Callers report the Session as unobservable with the daemon named,
// never as dead.
var ErrManagedHostUnreachable = errors.New("der Agent-Host ist nicht erreichbar")
