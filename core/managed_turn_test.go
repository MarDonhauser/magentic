package core

import (
	"testing"
	"time"
)

func testManagedTurns(t *testing.T) *ManagedTurns {
	t.Helper()
	turns := NewManagedTurns(SessionID("session-1"))
	clock := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	turns.now = func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	return turns
}

// 4.3: a turn ends only on an explicit reason, and a second start while one
// runs leaves the running turn untouched.
func TestManagedTurnStartsOnceAndEndsWithItsReason(t *testing.T) {
	turns := testManagedTurns(t)
	if _, known := turns.TurnState(); known {
		t.Fatal("a Session with no turn must report none")
	}
	if err := turns.StartTurn("msg-1"); err != nil {
		t.Fatal(err)
	}
	if err := turns.StartTurn("msg-2"); err == nil {
		t.Fatal("a second start while a turn runs must be refused")
	}
	turn, known := turns.TurnState()
	if !known || turn.MessageID != "msg-1" || !turn.Running {
		t.Fatalf("turn = %+v, want the first turn still running", turn)
	}

	ended, ok := turns.EndTurn(TurnEndFailed, "api_error")
	if !ok || ended.EndReason != TurnEndFailed || ended.FailReason != "api_error" {
		t.Fatalf("ended = %+v ok=%v, want a failed turn with the vendor's reason", ended, ok)
	}
	if _, ok := turns.EndTurn(TurnEndCompleted, ""); ok {
		t.Fatal("ending a Session with no running turn must change nothing")
	}
	if turns.TurnRunning() {
		t.Fatal("the turn must no longer be running")
	}
}

// 4.4: interrupting refuses when no turn runs, and otherwise ends the turn
// with the interrupted reason.
func TestManagedTurnInterrupt(t *testing.T) {
	turns := testManagedTurns(t)
	if _, err := turns.InterruptTurn(); err == nil {
		t.Fatal("interrupting with no running turn must be refused")
	}
	if err := turns.StartTurn("msg-1"); err != nil {
		t.Fatal(err)
	}
	ended, err := turns.InterruptTurn()
	if err != nil {
		t.Fatal(err)
	}
	if ended.EndReason != TurnEndInterrupted || ended.Running {
		t.Fatalf("ended = %+v, want an interrupted, ended turn", ended)
	}
}

// 4.2: the queue advances on the echo of the delivered prompt and on nothing
// else; a delivery failure leaves the prompt with its reason and resends
// nothing.
func TestManagedTurnAdvancesOnlyOnTheEcho(t *testing.T) {
	turns := testManagedTurns(t)
	turns.MarkInflight("msg-1", "hallo")
	inflight, ok := turns.Inflight()
	if !ok || inflight.MessageID != "msg-1" || inflight.Text != "hallo" {
		t.Fatalf("inflight = %+v ok=%v, want the delivered prompt in flight", inflight, ok)
	}

	if turns.ConfirmEcho("msg-2") {
		t.Fatal("an echo for another message must advance nothing")
	}
	if _, ok := turns.ConfirmEchoByText("etwas anderes"); ok {
		t.Fatal("an echo of another text must advance nothing")
	}
	if turns.TurnRunning() {
		t.Fatal("no turn may start without a matching echo")
	}

	messageID, ok := turns.ConfirmEchoByText("  hallo  ")
	if !ok || messageID != "msg-1" {
		t.Fatalf("ConfirmEchoByText = %q, %v, want the in-flight message confirmed", messageID, ok)
	}
	if !turns.TurnRunning() {
		t.Fatal("the echo must start the turn")
	}
	if _, ok := turns.Inflight(); ok {
		t.Fatal("the echo must take the prompt out of flight")
	}
}

func TestManagedTurnFailedDeliveryKeepsItsReason(t *testing.T) {
	turns := testManagedTurns(t)
	turns.MarkInflight("msg-1", "hallo")
	turns.FailDelivery("msg-1", "broken pipe")

	if _, ok := turns.Inflight(); ok {
		t.Fatal("a failed delivery must not leave a prompt in flight")
	}
	reason, at, ok := turns.DeliveryFailure()
	if !ok || reason != "broken pipe" || at.IsZero() {
		t.Fatalf("failure = %q at %v ok=%v, want the stated reason", reason, at, ok)
	}
	if turns.TurnRunning() {
		t.Fatal("a failed delivery starts no turn")
	}
}

// 4.5: a streamed then completed message leaves exactly one Item in its final
// form.
func TestManagedTurnStreamedMessageIsSupersededInPlace(t *testing.T) {
	turns := testManagedTurns(t)
	item := Item{ID: "managed-stream-msg-1", Role: ItemRoleAgent, Kind: ItemKindAgentMessage, Detail: "Hi"}

	streamed := turns.PublishChunk(item)
	if !streamed.InProgress {
		t.Fatal("a streamed chunk must be marked in progress")
	}
	if streamed.OccurredAt.IsZero() {
		t.Fatal("a published chunk must carry the time it occurred")
	}
	item.Detail = "Hi da"
	turns.PublishChunk(item)

	completed := turns.CompleteMessage(item)
	if completed.InProgress {
		t.Fatal("the completed message must not be marked in progress")
	}
	items := turns.StreamedConversation()
	if len(items) != 1 || items[0].Detail != "Hi da" || items[0].InProgress {
		t.Fatalf("conversation = %+v, want exactly one completed message", items)
	}

	turns.ApplyStreamedItems([]Item{{ID: "other", Detail: "später"}})
	if len(turns.StreamedConversation()) != 2 {
		t.Fatal("reported Items must fold into the held conversation")
	}
}

// The tracker is only the daemon's index: one Session's state is one
// ManagedTurns, and asking twice for the same Session yields the same one.
func TestManagedTurnTrackerHoldsOneStatePerSession(t *testing.T) {
	tracker := NewManagedTurnTracker()
	first := tracker.For(SessionID("session-1"))
	if tracker.For(SessionID("session-1")) != first {
		t.Fatal("the tracker must hold one state per Session, not a new one per call")
	}
	if err := first.StartTurn("msg-1"); err != nil {
		t.Fatal(err)
	}
	if tracker.For(SessionID("session-2")).TurnRunning() {
		t.Fatal("one Session's turn must not appear on another")
	}
	if first.SessionID() != SessionID("session-1") {
		t.Fatalf("SessionID = %q, want session-1", first.SessionID())
	}
}
