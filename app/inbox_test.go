package main

import (
	"testing"
	"time"

	"magentic/core"
)

func TestInboxBeforeTheFirstCycleReportsUnavailableFacts(t *testing.T) {
	inbox := NewApp().Inbox()
	if inbox.State != core.AttentionInboxUnavailable {
		t.Fatalf("state = %q, want unavailable before the first attention cycle", inbox.State)
	}
	if len(inbox.Entries) != 0 {
		t.Fatalf("entries = %#v, want none", inbox.Entries)
	}
}

func TestInboxReturnsTheLastPlannedList(t *testing.T) {
	app := NewApp()
	app.storeInbox(core.BuildInbox(
		&core.State{Agents: []core.Session{{ID: "one", Name: "auth-fix", Project: "magentic"}}},
		core.AttentionInbox{State: core.AttentionInboxComplete, Entries: []core.AttentionInboxEntry{{
			SessionID: "one", Kind: core.AttentionWaitingInput,
			WaitingSince: time.Now().Add(-3 * time.Minute), WaitingSinceKnown: true,
		}}},
	))

	inbox := app.Inbox()
	if inbox.State != core.AttentionInboxComplete || len(inbox.Entries) != 1 {
		t.Fatalf("inbox = %#v", inbox)
	}
	if inbox.Entries[0].Session != "auth-fix" || inbox.Entries[0].Project != "magentic" {
		t.Fatalf("entry = %#v", inbox.Entries[0])
	}
}
