package core

import (
	"errors"
	"fmt"
	"strings"
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

// ManagedTurns holds exactly one managed Session's turn, its in-flight prompt
// and its streamed conversation. One Session is the whole unit of this module:
// an agent host owns one Session, so nothing here is addressed by a Session
// identity a caller has to keep repeating. All methods are safe for
// concurrent use.
type ManagedTurns struct {
	mu           sync.Mutex
	sessionID    SessionID
	turn         *ManagedTurn
	inflight     *ManagedInflight
	conversation Conversation
	now          func() time.Time
}

// NewManagedTurns creates the turn state of one managed Session.
func NewManagedTurns(sessionID SessionID) *ManagedTurns {
	return &ManagedTurns{sessionID: sessionID, now: time.Now}
}

// SessionID is the managed Session this state belongs to.
func (t *ManagedTurns) SessionID() SessionID { return t.sessionID }

// StartTurn records that a turn began for the delivered Outbox message. A
// second start while a turn is running is a caller error and leaves the
// running turn untouched.
func (t *ManagedTurns) StartTurn(messageID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.turn != nil && t.turn.Running {
		return fmt.Errorf("für Session %q läuft bereits ein Turn", t.sessionID)
	}
	t.beginTurn(messageID)
	return nil
}

// beginTurn replaces the held turn with a fresh running one. The caller holds
// the lock.
func (t *ManagedTurns) beginTurn(messageID string) {
	t.turn = &ManagedTurn{
		SessionID: t.sessionID, Running: true, MessageID: messageID, StartedAt: t.now(),
	}
}

// EndTurn records the turn's end with its explicit reason. A failed turn
// carries the vendor's own reason. Ending with no running turn reports false
// and changes nothing.
func (t *ManagedTurns) EndTurn(reason TurnEndReason, failReason string) (ManagedTurn, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.turn == nil || !t.turn.Running {
		return ManagedTurn{SessionID: t.sessionID}, false
	}
	t.turn.Running = false
	t.turn.EndedAt = t.now()
	t.turn.EndReason = reason
	t.turn.FailReason = failReason
	return *t.turn, true
}

// InterruptTurn ends the running turn with the interrupted reason and leaves
// everything else — the Session, its process, its conversation — untouched.
// Interrupting with no running turn is refused and stops nothing.
func (t *ManagedTurns) InterruptTurn() (ManagedTurn, error) {
	ended, ok := t.EndTurn(TurnEndInterrupted, "")
	if !ok {
		return ManagedTurn{SessionID: t.sessionID},
			fmt.Errorf("für Session %q läuft kein Turn, der unterbrochen werden könnte", t.sessionID)
	}
	return ended, nil
}

// TurnRunning reports whether a turn is running.
func (t *ManagedTurns) TurnRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.turn != nil && t.turn.Running
}

// TurnState returns the current or last turn, or false when no turn was ever
// recorded.
func (t *ManagedTurns) TurnState() (ManagedTurn, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.turn == nil {
		return ManagedTurn{SessionID: t.sessionID}, false
	}
	return *t.turn, true
}

// MarkInflight records that a prompt was sent and its acknowledgement is
// still outstanding. A second prompt is never sent while one is in flight:
// the Outbox keeps its strict FIFO through this claim.
func (t *ManagedTurns) MarkInflight(messageID, text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight = &ManagedInflight{
		SessionID: t.sessionID, MessageID: messageID, Text: text, SentAt: t.now(),
	}
}

// Inflight reports the unacknowledged prompt, if any.
func (t *ManagedTurns) Inflight() (ManagedInflight, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight == nil || t.inflight.MessageID == "" {
		return ManagedInflight{SessionID: t.sessionID}, false
	}
	return *t.inflight, true
}

// ConfirmEcho advances the Outbox on the protocol's echo of the delivered
// prompt: the matching in-flight prompt leaves the queue and its turn starts.
// Anything else — a missing echo, an echo for another message — advances
// nothing and resends nothing.
func (t *ManagedTurns) ConfirmEcho(messageID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight == nil || t.inflight.MessageID == "" || t.inflight.MessageID != messageID {
		return false
	}
	t.inflight = nil
	t.beginTurn(messageID)
	return true
}

// ConfirmEchoByText advances the Outbox on an echo that carries the replayed
// prompt rather than a message identity — which is what the vendor's protocol
// actually sends. Only the in-flight prompt's own text matches; an echo of
// anything else advances nothing.
func (t *ManagedTurns) ConfirmEchoByText(text string) (string, bool) {
	t.mu.Lock()
	pending := t.inflight
	if pending == nil || pending.MessageID == "" || strings.TrimSpace(pending.Text) != strings.TrimSpace(text) {
		t.mu.Unlock()
		return "", false
	}
	messageID := pending.MessageID
	t.inflight = nil
	t.beginTurn(messageID)
	t.mu.Unlock()
	return messageID, true
}

// FailDelivery records that delivering the prompt failed. The prompt stays
// queued with its reason — it is reset to queued, never dropped and never
// retried here.
func (t *ManagedTurns) FailDelivery(messageID, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pending := t.inflight
	if pending == nil || pending.MessageID != messageID {
		pending = &ManagedInflight{SessionID: t.sessionID, MessageID: messageID}
	}
	pending.MessageID = ""
	pending.FailReason = reason
	pending.FailedAt = t.now()
	t.inflight = pending
}

// DeliveryFailure reports the last failed delivery, if any.
func (t *ManagedTurns) DeliveryFailure() (reason string, at time.Time, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight == nil || t.inflight.FailReason == "" {
		return "", time.Time{}, false
	}
	return t.inflight.FailReason, t.inflight.FailedAt, true
}

// PublishChunk publishes streamed output as it arrives, marked as still being
// produced. Repeated chunks for the same message supersede it in place.
func (t *ManagedTurns) PublishChunk(item Item) Item {
	t.mu.Lock()
	defer t.mu.Unlock()
	item.InProgress = true
	if item.OccurredAt.IsZero() {
		item.OccurredAt = t.now()
	}
	t.conversation.Apply(item)
	return item
}

// CompleteMessage supersedes the streamed message with its final form. After
// this call the conversation holds the message exactly once, not marked as in
// progress.
func (t *ManagedTurns) CompleteMessage(item Item) Item {
	t.mu.Lock()
	defer t.mu.Unlock()
	item.InProgress = false
	if item.OccurredAt.IsZero() {
		item.OccurredAt = t.now()
	}
	t.conversation.Apply(item)
	return item
}

// StreamedConversation returns the streamed Items in order. It is what the
// conversation surface renders; completed messages appear in their final
// form, in-progress ones marked as such.
func (t *ManagedTurns) StreamedConversation() []Item {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Item(nil), t.conversation.Items...)
}

// ApplyStreamedItems folds Items the host reported (for example after a
// daemon restart) into the held conversation, superseding by identity.
func (t *ManagedTurns) ApplyStreamedItems(items []Item) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.conversation.Apply(items...)
}

// ManagedTurnTracker is the daemon's index of what every known managed
// Session's host reported. It holds no turn logic of its own: one Session's
// state is a ManagedTurns, and this is the map from Session identity to it.
type ManagedTurnTracker struct {
	mu       sync.Mutex
	sessions map[SessionID]*ManagedTurns
}

// NewManagedTurnTracker creates an empty tracker.
func NewManagedTurnTracker() *ManagedTurnTracker {
	return &ManagedTurnTracker{sessions: map[SessionID]*ManagedTurns{}}
}

// For returns the Session's turn state, creating an empty one on first use.
func (t *ManagedTurnTracker) For(sessionID SessionID) *ManagedTurns {
	t.mu.Lock()
	defer t.mu.Unlock()
	turns := t.sessions[sessionID]
	if turns == nil {
		turns = NewManagedTurns(sessionID)
		t.sessions[sessionID] = turns
	}
	return turns
}

// ErrManagedHostUnreachable reports a managed Session whose host cannot be
// reached. Callers report the Session as unobservable with the daemon named,
// never as dead.
var ErrManagedHostUnreachable = errors.New("der Agent-Host ist nicht erreichbar")
