package core

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPromptTargetQueueSyncDeadlineIncludesAcquire(t *testing.T) {
	q := newPromptTargetQueue()
	q.sendSlot <- struct{}{}

	var delivered atomic.Bool
	started := time.Now()
	err := q.enqueue("magentic-busy", "next", true, time.Now().Add(40*time.Millisecond), func(time.Time) error {
		delivered.Store(true)
		return nil
	})
	elapsed := time.Since(started)
	<-q.sendSlot

	if !errors.Is(err, errPromptDeliveryDeadline) {
		t.Fatalf("acquire error = %v, want delivery deadline", err)
	}
	if delivered.Load() {
		t.Fatal("delivery ran without acquiring the target slot")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("deadline-bound acquire took %s", elapsed)
	}
}

func TestPromptTargetQueuePassesAbsoluteDeadlineToDelivery(t *testing.T) {
	q := newPromptTargetQueue()
	deadline := time.Now().Add(time.Second)
	var deliveredWith time.Time
	err := q.enqueue("magentic-busy", "next", true, deadline, func(deliveryDeadline time.Time) error {
		deliveredWith = deliveryDeadline
		return nil
	})
	if err != nil {
		t.Fatalf("delivery error: %v", err)
	}
	if !deliveredWith.Equal(deadline) {
		t.Fatalf("delivery deadline = %v, want exact queue deadline %v", deliveredWith, deadline)
	}
}

func TestForgetPromptTargetQueueDropsRemovedSession(t *testing.T) {
	target := promptTargetForSession(Session{ID: "forget-test", Name: "forget-test", RuntimeName: "magentic-forget-test"})
	first := queueForPromptTarget(target)
	t.Cleanup(func() { forgetPromptTargetQueue(target) })

	if _, ok := promptTargetQueues.Load(target.key()); !ok {
		t.Fatal("queue for session was not stored")
	}

	forgetPromptTargetQueue(target)
	if _, ok := promptTargetQueues.Load(target.key()); ok {
		t.Fatal("queue for removed session is still stored")
	}

	second := queueForPromptTarget(target)
	if second == first {
		t.Fatal("reused runtime address inherited the removed session's queue")
	}
}

// TestPromptTargetQueueKeyFollowsStableIdentity hält fest, warum die
// Warteschlange nach SessionID schlüsselt: eine neue Session, die die
// Laufzeitadresse einer entfernten erbt, darf deren Einträge nicht erben.
func TestPromptTargetQueueKeyFollowsStableIdentity(t *testing.T) {
	removed := promptTargetForSession(Session{ID: "session-a", Name: "hera", RuntimeName: "magentic-hera"})
	successor := promptTargetForSession(Session{ID: "session-b", Name: "hera", RuntimeName: "magentic-hera"})
	if removed.key() == successor.key() {
		t.Fatalf("zwei Sessions teilen den Warteschlangenschlüssel %q", removed.key())
	}
	t.Cleanup(func() {
		forgetPromptTargetQueue(removed)
		forgetPromptTargetQueue(successor)
	})
	if queueForPromptTarget(removed) == queueForPromptTarget(successor) {
		t.Fatal("Nachfolger erbte die Warteschlange der entfernten Session")
	}
}

func TestPromptDeliveryKeySeparatesHandoffValidationPolicy(t *testing.T) {
	generic := promptDeliveryKey("same", true, AgentToolClaude, true, false, true, nil)
	handoff := promptDeliveryKey(
		"same", true, AgentToolClaude, true, false, true,
		func(promptTargetObservation) error { return nil },
	)
	if generic == handoff {
		t.Fatal("strict Handoff delivery must not deduplicate against an unvalidated prompt")
	}
}

func TestPromptTargetQueueDeduplicatedSyncWaitsForSharedFailure(t *testing.T) {
	q := newPromptTargetQueue()
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseDelivery := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseDelivery)

	wantErr := errors.New("delivery failed")
	var deliveries atomic.Int32
	ownerResult := make(chan error, 1)
	go func() {
		ownerResult <- q.enqueue("magentic-target", "same", true, time.Now().Add(time.Second), func(time.Time) error {
			deliveries.Add(1)
			close(started)
			<-release
			return wantErr
		})
	}()
	<-started

	duplicateResult := make(chan error, 1)
	go func() {
		duplicateResult <- q.enqueue("magentic-target", "same", true, time.Now().Add(time.Second), func(time.Time) error {
			deliveries.Add(1)
			return nil
		})
	}()

	select {
	case err := <-duplicateResult:
		t.Fatalf("deduplicated delivery returned before the owner completed: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	releaseDelivery()

	if err := <-ownerResult; !errors.Is(err, wantErr) {
		t.Fatalf("owner error = %v, want %v", err, wantErr)
	}
	if err := <-duplicateResult; !errors.Is(err, wantErr) {
		t.Fatalf("deduplicated error = %v, want shared %v", err, wantErr)
	}
	if got := deliveries.Load(); got != 1 {
		t.Fatalf("delivery calls = %d, want 1", got)
	}
}

func TestPromptTargetQueueAsyncDuplicateReportsPending(t *testing.T) {
	q := newPromptTargetQueue()
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	var duplicateDelivered atomic.Bool
	var releaseOnce sync.Once
	releaseDelivery := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseDelivery)

	if err := q.enqueue("magentic-target", "same", false, time.Now().Add(time.Second), func(time.Time) error {
		close(started)
		<-release
		close(finished)
		return nil
	}); err != nil {
		t.Fatalf("enqueue owner: %v", err)
	}
	<-started

	err := q.enqueue("magentic-target", "same", false, time.Now().Add(time.Second), func(time.Time) error {
		duplicateDelivered.Store(true)
		return nil
	})
	if !errors.Is(err, errPromptDeliveryPending) {
		t.Fatalf("duplicate enqueue error = %v, want pending result", err)
	}

	releaseDelivery()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("owner delivery did not finish")
	}
	if duplicateDelivered.Load() {
		t.Fatal("duplicate delivery ran")
	}
}

func TestPromptTargetQueueSerializesDifferentKeys(t *testing.T) {
	q := newPromptTargetQueue()
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	secondRelease := make(chan struct{})
	secondFinished := make(chan struct{})
	var releaseFirstOnce sync.Once
	var releaseSecondOnce sync.Once
	releaseFirst := func() { releaseFirstOnce.Do(func() { close(firstRelease) }) }
	releaseSecond := func() { releaseSecondOnce.Do(func() { close(secondRelease) }) }
	t.Cleanup(releaseFirst)
	t.Cleanup(releaseSecond)

	deadline := time.Now().Add(2 * time.Second)
	if err := q.enqueue("magentic-target", "first", false, deadline, func(time.Time) error {
		close(firstStarted)
		<-firstRelease
		return nil
	}); err != nil {
		t.Fatalf("enqueue first delivery: %v", err)
	}
	<-firstStarted
	if err := q.enqueue("magentic-target", "second", false, deadline, func(time.Time) error {
		close(secondStarted)
		<-secondRelease
		close(secondFinished)
		return nil
	}); err != nil {
		t.Fatalf("enqueue second delivery: %v", err)
	}

	select {
	case <-secondStarted:
		t.Fatal("different keys delivered concurrently to one target")
	case <-time.After(40 * time.Millisecond):
	}
	releaseFirst()
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second delivery did not start after the target slot was released")
	}
	releaseSecond()
	select {
	case <-secondFinished:
	case <-time.After(time.Second):
		t.Fatal("second delivery did not finish")
	}
}

func TestPromptTargetQueuePanicStillFinishesPendingDelivery(t *testing.T) {
	q := newPromptTargetQueue()
	const panicValue = "delivery panic"

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = q.enqueue("magentic-target", "same", true, time.Now().Add(time.Second), func(time.Time) error {
			panic(panicValue)
		})
	}()
	if recovered != panicValue {
		t.Fatalf("recovered panic = %v, want %q", recovered, panicValue)
	}

	q.pendingMu.Lock()
	pending := len(q.pending)
	q.pendingMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending deliveries after panic = %d, want 0", pending)
	}

	if err := q.enqueue("magentic-target", "same", true, time.Now().Add(time.Second), func(time.Time) error {
		return nil
	}); err != nil {
		t.Fatalf("retry after panic: %v", err)
	}
}
