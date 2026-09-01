package core

import (
	"testing"
	"time"
)

func TestBuildInboxJoinsNamesAndMarksAQueuedAnswerAsAwaitingDelivery(t *testing.T) {
	waitingSince := time.Now().Add(-12 * time.Minute)
	st := &State{Agents: []Session{
		{
			ID: "one", Name: "auth-fix", Project: "magentic",
			Outbox: []QueuedMessage{{
				ID: "msg-1", Kind: QueuedMessageKindMessage, Text: "ja, bitte schreiben",
				EnqueuedAt: time.Now().Add(-time.Minute),
			}},
		},
		{ID: "two", Name: "docs", Project: "magentic"},
	}}
	planned := AttentionInbox{State: AttentionInboxComplete, Entries: []AttentionInboxEntry{
		{
			SessionID: "one", Kind: AttentionWaitingInput, WaitingSince: waitingSince,
			WaitingSinceKnown: true, Excerpt: "Darf ich die Datei schreiben?", ExcerptKnown: true,
		},
		{SessionID: "two", Kind: AttentionWaitingReview, WaitingSince: waitingSince, WaitingSinceKnown: true},
	}}

	inbox := BuildInbox(st, planned)

	if inbox.State != AttentionInboxComplete || len(inbox.Entries) != 2 {
		t.Fatalf("inbox = %#v", inbox)
	}
	first := inbox.Entries[0]
	if first.Session != "auth-fix" || first.Project != "magentic" {
		t.Fatalf("names = %q / %q", first.Session, first.Project)
	}
	if first.Kind != AttentionWaitingInput || first.Excerpt != "Darf ich die Datei schreiben?" || !first.ExcerptKnown {
		t.Fatalf("planned facts were not carried through: %#v", first)
	}
	if !first.AwaitingDelivery || len(first.Queued) != 1 || first.Queued[0].ID != "msg-1" {
		t.Fatalf("queued answer was not marked as awaiting delivery: %#v", first)
	}
	if inbox.Entries[1].AwaitingDelivery {
		t.Fatalf("a Session without a queued message awaits nothing: %#v", inbox.Entries[1])
	}
}

func TestBuildInboxKeepsAnEntryWhoseSessionIsNoLongerRegistered(t *testing.T) {
	inbox := BuildInbox(&State{}, AttentionInbox{
		State:   AttentionInboxIncomplete,
		Entries: []AttentionInboxEntry{{SessionID: "gone", Kind: AttentionWaitingInput}},
	})

	if inbox.State != AttentionInboxIncomplete || len(inbox.Entries) != 1 {
		t.Fatalf("inbox = %#v", inbox)
	}
	if inbox.Entries[0].Session != "gone" || inbox.Entries[0].Project != "" {
		t.Fatalf("unknown Session was given invented names: %#v", inbox.Entries[0])
	}
}

func TestBuildInboxWithoutAPlannedStateReportsUnavailable(t *testing.T) {
	inbox := BuildInbox(nil, AttentionInbox{})
	if inbox.State != AttentionInboxUnavailable || len(inbox.Entries) != 0 {
		t.Fatalf("inbox = %#v, want an explicit unavailable list", inbox)
	}
}
