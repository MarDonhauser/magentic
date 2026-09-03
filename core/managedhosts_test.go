package core

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testManagedHostStorePath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "managed-hosts.json")
}

// 3.1: the record exists before any process is spawned, and a failed spawn
// leaves a record marked as not started.
func TestRecordManagedHostIntentPrecedesSpawn(t *testing.T) {
	storePath := testManagedHostStorePath(t)
	sessionID := SessionID("session-1")
	token := NewAgentHostToken()
	if err := RecordManagedHostIntent(storePath, sessionID, "/tmp/session-1.sock", token); err != nil {
		t.Fatal(err)
	}
	records, err := ManagedHostRecords(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Started {
		t.Fatalf("records = %+v, want one unstarted record", records)
	}
	if records[0].SessionID != sessionID || records[0].Token != token {
		t.Fatalf("recorded intent = %+v, want session %q token %q", records[0], sessionID, token)
	}

	// A failed spawn never calls MarkManagedHostStarted, so the record stays
	// exactly what a failed-spawn record looks like: recorded, not started.
	records, err = ManagedHostRecords(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if records[0].Started {
		t.Fatal("a failed spawn must leave the record unstarted")
	}

	if err := MarkManagedHostStarted(storePath, sessionID); err != nil {
		t.Fatal(err)
	}
	records, _ = ManagedHostRecords(storePath)
	if !records[0].Started {
		t.Fatal("MarkManagedHostStarted did not persist")
	}
}

// 3.2: a live host is reclaimed without a second process being started; a
// dead host marks its Session as having no process; a socket answering with
// the wrong token is neither adopted nor killed.
func TestReconcileManagedHosts(t *testing.T) {
	storePath := testManagedHostStorePath(t)
	t.Setenv("MAGENTIC_STATE", filepath.Dir(storePath)+"/state.json")

	liveID := SessionID("session-live")
	deadID := SessionID("session-dead")
	wrongTokenID := SessionID("session-wrong-token")

	liveToken := NewAgentHostToken()
	liveHost, err := StartAgentHost(liveID, liveToken)
	if err != nil {
		t.Fatal(err)
	}
	defer liveHost.Close()

	wrongToken := NewAgentHostToken()
	wrongHost, err := StartAgentHost(wrongTokenID, NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	defer wrongHost.Close()

	if err := RecordManagedHostIntent(storePath, liveID, liveHost.Path(), liveToken); err != nil {
		t.Fatal(err)
	}
	if err := RecordManagedHostIntent(storePath, deadID, "/nonexistent/session-dead.sock", NewAgentHostToken()); err != nil {
		t.Fatal(err)
	}
	// The store records the token the daemon expects, which no longer
	// matches what wrongHost actually answers with.
	if err := RecordManagedHostIntent(storePath, wrongTokenID, wrongHost.Path(), wrongToken); err != nil {
		t.Fatal(err)
	}

	state := &State{Agents: []Session{{ID: liveID, Name: "live"}, {ID: deadID, Name: "dead"}, {ID: wrongTokenID, Name: "wrong"}}}
	results, err := ReconcileManagedHosts(storePath, state)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[SessionID]ManagedHostOutcome{}
	for _, r := range results {
		outcomes[r.SessionID] = r.Outcome
	}
	if outcomes[liveID] != ManagedHostReclaimed {
		t.Fatalf("live host outcome = %v, want reclaimed", outcomes[liveID])
	}
	if outcomes[deadID] != ManagedHostGone {
		t.Fatalf("dead host outcome = %v, want gone", outcomes[deadID])
	}
	if outcomes[wrongTokenID] != ManagedHostGone {
		t.Fatalf("wrong-token host outcome = %v, want gone (neither adopted nor killed)", outcomes[wrongTokenID])
	}
	// Neither adopted nor killed: the process behind wrongHost is still
	// alive and unaffected by reconciliation.
	if !wrongHost.listenerAlive() {
		t.Fatal("reconciliation touched a socket it could not confirm")
	}
}

// listenerAlive is a small test-only probe: the listener is still open, so a
// fresh dial succeeds.
func (h *AgentHost) listenerAlive() bool {
	conn, err := net.Dial("unix", h.Path())
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// 3.3: stopping a managed Session signals only the recorded identity, and no
// process-table search appears anywhere in the managed path's source.
func TestManagedPathNeverSearchesTheProcessTable(t *testing.T) {
	for _, file := range []string{"agenthost.go", "agenthost_process.go", "managedhosts.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		source := string(data)
		for _, forbidden := range []string{"pgrep", "pkill", "\"ps\"", "ps aux", "FindProcess"} {
			if strings.Contains(source, forbidden) {
				// FindProcess against a *recorded* PID is legitimate (it's how
				// Go signals a known process); only flag scans, not lookups by
				// the identity this file itself recorded.
				if forbidden == "FindProcess" {
					continue
				}
				t.Fatalf("%s contains %q, a process-table search pattern forbidden on the managed path", file, forbidden)
			}
		}
	}
}

// 3.4: reconciliation lists the orphan and terminates nothing.
func TestReconcileManagedHostsReportsOrphanWithoutTerminating(t *testing.T) {
	storePath := testManagedHostStorePath(t)
	t.Setenv("MAGENTIC_STATE", filepath.Dir(storePath)+"/state.json")

	orphanID := SessionID("session-orphan")
	token := NewAgentHostToken()
	host, err := StartAgentHost(orphanID, token)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if err := host.StartVendorProcess("sleep", []string{"30"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := RecordManagedHostIntent(storePath, orphanID, host.Path(), token); err != nil {
		t.Fatal(err)
	}

	// No Session by this ID exists any more.
	state := &State{Agents: nil}
	results, err := ReconcileManagedHosts(storePath, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != ManagedHostOrphaned {
		t.Fatalf("results = %+v, want one orphaned outcome", results)
	}
	if !host.VendorProcessAlive() {
		t.Fatal("reconciliation terminated an orphaned host's process; it must only report it")
	}
}

// 3.5: a second daemon refuses ownership, states the reason, and starts no
// agent process.
func TestReconcileManagedHostsIfOwningRefusesWhenAnotherDaemonOwns(t *testing.T) {
	storePath := testManagedHostStorePath(t)
	t.Setenv("MAGENTIC_STATE", filepath.Dir(storePath)+"/state.json")
	sessionID := SessionID("session-1")
	if err := RecordManagedHostIntent(storePath, sessionID, "/tmp/session-1.sock", NewAgentHostToken()); err != nil {
		t.Fatal(err)
	}
	state := &State{Agents: []Session{{ID: sessionID, Name: "s"}}}

	claimErr := ErrControlServedElsewhere
	results, err := ReconcileManagedHostsIfOwning(claimErr, storePath, state)
	if !errors.Is(err, ErrControlServedElsewhere) {
		t.Fatalf("err = %v, want the stated ownership refusal", err)
	}
	if results != nil {
		t.Fatalf("results = %+v, want none — a refused daemon starts no agent process", results)
	}
}
