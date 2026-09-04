package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// 5.5: two concurrent answers result in one delivered decision and one
// refusal, and the waiting agent sees exactly the delivered one.
func TestPermissionAnsweredExactlyOnce(t *testing.T) {
	store := NewPermissionStore()
	request := store.Open(SessionID("session-1"), "darf ich rm ausführen?")

	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	for _, decidedBy := range []string{"tui", "desktop"} {
		wg.Add(1)
		go func(by string) {
			defer wg.Done()
			err := store.Answer(request.ID, PermissionAllow, by)
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}(decidedBy)
	}
	wg.Wait()

	delivered, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			delivered++
		case errors.Is(err, ErrPermissionClosed):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if delivered != 1 || refused != 1 {
		t.Fatalf("delivered=%d refused=%d, want exactly one of each", delivered, refused)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	decision, outcome, err := store.Wait(ctx, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if decision != PermissionAllow || outcome != PermissionAllowed {
		t.Fatalf("decision=%q outcome=%q, want the one delivered decision", decision, outcome)
	}
	if open := store.OpenRequests(); len(open) != 0 {
		t.Fatalf("open requests = %+v, want none after the decision", open)
	}
}

// 5.6: an open request closes as no longer answerable when the agent process
// ends — never as allowed or denied — and every blocked waiter wakes.
func TestPermissionClosesUnanswerableWhenTheProcessEnds(t *testing.T) {
	store := NewPermissionStore()
	sessionID := SessionID("session-1")
	request := store.Open(sessionID, "darf ich rm ausführen?")

	waited := make(chan PermissionOutcome, 1)
	go func() {
		_, outcome, err := store.Wait(context.Background(), request.ID)
		if err != nil {
			t.Error(err)
		}
		waited <- outcome
	}()

	closed := store.CloseUnanswerable(sessionID, "Prozess endete: signal: killed")
	if len(closed) != 1 || closed[0].Outcome != PermissionUnanswerable {
		t.Fatalf("closed = %+v, want one unanswerable request", closed)
	}
	if closed[0].DecidedBy != "" {
		t.Fatalf("DecidedBy = %q, want empty — nobody decided this", closed[0].DecidedBy)
	}
	if closed[0].CloseReason == "" {
		t.Fatal("an unanswerable request must carry the process's exit reason")
	}

	select {
	case outcome := <-waited:
		if outcome != PermissionUnanswerable {
			t.Fatalf("waiter saw %q, want %q", outcome, PermissionUnanswerable)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closing the request did not wake the blocked waiter")
	}

	if err := store.Answer(request.ID, PermissionAllow, "tui"); !errors.Is(err, ErrPermissionClosed) {
		t.Fatalf("answering a closed request: err = %v, want ErrPermissionClosed", err)
	}
	item := PermissionOutcomeItem(closed[0])
	if item.Title != "Berechtigungsanfrage verfallen" {
		t.Fatalf("outcome item title = %q, want the unanswerable wording", item.Title)
	}
}

// 5.7: nothing answers a request on the developer's behalf. With no interface
// connected the request stays open however long the wait runs, and the closed
// set of decisions holds nothing Magentic could apply on its own.
func TestPermissionIsNeverDecidedAutomatically(t *testing.T) {
	store := NewPermissionStore()
	request := store.Open(SessionID("session-1"), "darf ich rm ausführen?")

	// The wait is bounded for the test only. Giving up on the answer must
	// not be an answer: the request is still open afterwards.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	decision, outcome, err := store.Wait(ctx, request.ID)
	if !errors.Is(err, ErrPermissionWaitAbandoned) {
		t.Fatalf("err = %v, want ErrPermissionWaitAbandoned", err)
	}
	if decision != "" || outcome != "" {
		t.Fatalf("decision=%q outcome=%q, want neither — nobody decided", decision, outcome)
	}
	open := store.OpenRequests()
	if len(open) != 1 || !open[0].Answerable() {
		t.Fatalf("open = %+v, want the request still open and answerable", open)
	}

	modes := PermissionDecisionModes()
	if len(modes) != 2 {
		t.Fatalf("decision modes = %v, want exactly the two explicit developer decisions", modes)
	}
	for _, mode := range modes {
		if mode != PermissionAllow && mode != PermissionDeny {
			t.Fatalf("mode %q is neither allow nor deny; a settable mode must never answer a request", mode)
		}
	}

	if err := store.Answer(request.ID, PermissionDecision("auto"), "settings"); err == nil {
		t.Fatal("a decision outside allow/deny must be refused")
	}
	if err := store.Answer("never-opened", PermissionAllow, "tui"); !errors.Is(err, ErrPermissionUnknown) {
		t.Fatalf("answering an unknown ID: err = %v, want ErrPermissionUnknown", err)
	}
}

// A denied request records the denial and its decider, and the Session's
// activity carries request and outcome in the order they occurred.
func TestPermissionDeniedRendersAsItems(t *testing.T) {
	store := NewPermissionStore()
	sessionID := SessionID("session-1")
	request := store.Open(sessionID, "darf ich rm ausführen?")
	if _, ok := store.RequestForSession(sessionID); !ok {
		t.Fatal("the open request must be findable by its Session")
	}
	if err := store.Answer(request.ID, PermissionDeny, "kai"); err != nil {
		t.Fatal(err)
	}
	all := store.Requests(false)
	if len(all) != 1 || all[0].Outcome != PermissionDenied || all[0].DecidedBy != "kai" {
		t.Fatalf("requests = %+v, want one denied request decided by kai", all)
	}
	asked := PermissionRequestItem(all[0])
	decided := PermissionOutcomeItem(all[0])
	if asked.Kind != ItemKindPermissionRequest || decided.Kind != ItemKindPermissionDecision {
		t.Fatalf("item kinds = %q, %q", asked.Kind, decided.Kind)
	}
	if decided.OccurredAt.Before(asked.OccurredAt) {
		t.Fatal("the decision must not be recorded before the request it answers")
	}
	if decided.Title != "Berechtigung verweigert" || decided.Detail != "entschieden von kai" {
		t.Fatalf("decision item = %+v, want the denial and its decider", decided)
	}
}
