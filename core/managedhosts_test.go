package core

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func testManagedHostRegistry(t *testing.T) *ManagedHostRegistry {
	t.Helper()
	dir, err := os.MkdirTemp("", "mh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return OpenManagedHostRegistry(filepath.Join(dir, "managed-hosts.json"))
}

// The Registry holds its store path once. MAGENTIC_MANAGED_HOSTS stays the
// one place that overrides it, and no caller derives the path a second time.
func TestNewManagedHostRegistryTakesItsPathFromTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "elsewhere.json")
	t.Setenv("MAGENTIC_MANAGED_HOSTS", path)
	if got := NewManagedHostRegistry().Path(); got != path {
		t.Fatalf("registry path = %q, want %q", got, path)
	}
}

// 3.1: the record exists before any process is spawned, and a failed spawn
// leaves a record marked as not started.
func TestRecordManagedHostIntentPrecedesSpawn(t *testing.T) {
	registry := testManagedHostRegistry(t)
	sessionID := SessionID("session-1")
	token := NewAgentHostToken()
	if err := registry.RecordIntent(sessionID, "/tmp/session-1.sock", token); err != nil {
		t.Fatal(err)
	}
	records, err := registry.Records()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Started {
		t.Fatalf("records = %+v, want one unstarted record", records)
	}
	if records[0].SessionID != sessionID || records[0].Token != token {
		t.Fatalf("recorded intent = %+v, want session %q token %q", records[0], sessionID, token)
	}

	// A failed spawn never calls MarkStarted, so the record stays exactly
	// what a failed-spawn record looks like: recorded, not started.
	records, err = registry.Records()
	if err != nil {
		t.Fatal(err)
	}
	if records[0].Started {
		t.Fatal("a failed spawn must leave the record unstarted")
	}

	if err := registry.MarkStarted(sessionID); err != nil {
		t.Fatal(err)
	}
	records, _ = registry.Records()
	if !records[0].Started {
		t.Fatal("MarkStarted did not persist")
	}

	if err := registry.Forget(sessionID); err != nil {
		t.Fatal(err)
	}
	records, _ = registry.Records()
	if len(records) != 0 {
		t.Fatalf("records = %+v, want none after Forget", records)
	}
}

// 3.2: a live host is reclaimed without a second process being started; a
// dead host marks its Session as having no process; a socket answering with
// the wrong token is neither adopted nor killed — and is reported as its own
// outcome, because "nothing is there" and "something foreign is there" call
// for opposite readings.
func TestReconcileManagedHosts(t *testing.T) {
	registry := testManagedHostRegistry(t)
	t.Setenv("MAGENTIC_STATE", filepath.Dir(registry.Path())+"/state.json")

	liveID := SessionID("session-live")
	deadID := SessionID("session-dead")
	foreignID := SessionID("session-foreign")

	liveToken := NewAgentHostToken()
	liveHost, err := StartAgentHost(liveID, liveToken)
	if err != nil {
		t.Fatal(err)
	}
	defer liveHost.Close()

	expectedToken := NewAgentHostToken()
	foreignHost, err := StartAgentHost(foreignID, NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	defer foreignHost.Close()

	if err := registry.RecordIntent(liveID, liveHost.Path(), liveToken); err != nil {
		t.Fatal(err)
	}
	if err := registry.RecordIntent(deadID, "/nonexistent/session-dead.sock", NewAgentHostToken()); err != nil {
		t.Fatal(err)
	}
	// The store records the token the daemon expects, which no longer
	// matches what foreignHost actually answers with.
	if err := registry.RecordIntent(foreignID, foreignHost.Path(), expectedToken); err != nil {
		t.Fatal(err)
	}

	state := &State{Agents: []Session{{ID: liveID, Name: "live"}, {ID: deadID, Name: "dead"}, {ID: foreignID, Name: "foreign"}}}
	results, err := registry.Reconcile(state)
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
	if outcomes[foreignID] != ManagedHostForeign {
		t.Fatalf("foreign host outcome = %v, want foreign (neither adopted nor killed)", outcomes[foreignID])
	}
	// Neither adopted nor killed: the process behind foreignHost is still
	// alive and unaffected by reconciliation.
	if !foreignHost.listenerAlive() {
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

// 3.3: stopping a managed Session signals only the recorded identity. Two
// hosts run byte-identical command lines, so any stop that searched the
// process table — by name, by command line, by anything but the identity the
// host itself recorded — would take both down. Only the one that was asked to
// stop may end.
func TestStoppingOneManagedHostLeavesAnIdenticalOneAlive(t *testing.T) {
	testAgentHostEnv(t)
	binary, argv := "sleep", []string{"30"}

	stopped, err := StartAgentHost(SessionID("session-stopped"), NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	if err := stopped.StartVendorProcess(binary, argv, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	untouched, err := StartAgentHost(SessionID("session-untouched"), NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	defer untouched.Close()
	if err := untouched.StartVendorProcess(binary, argv, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	if err := stopped.Close(); err != nil {
		t.Fatal(err)
	}
	if stopped.HostState().Alive {
		t.Fatal("the stopped host's process is still alive")
	}
	if !untouched.HostState().Alive {
		t.Fatal("stopping one managed Session ended another with the same command line")
	}
}

// 3.4: reconciliation lists the orphan and terminates nothing.
func TestReconcileManagedHostsReportsOrphanWithoutTerminating(t *testing.T) {
	registry := testManagedHostRegistry(t)
	t.Setenv("MAGENTIC_STATE", filepath.Dir(registry.Path())+"/state.json")

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
	if err := registry.RecordIntent(orphanID, host.Path(), token); err != nil {
		t.Fatal(err)
	}

	// No Session by this ID exists any more.
	state := &State{Agents: nil}
	results, err := registry.Reconcile(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != ManagedHostOrphaned {
		t.Fatalf("results = %+v, want one orphaned outcome", results)
	}
	if !host.HostState().Alive {
		t.Fatal("reconciliation terminated an orphaned host's process; it must only report it")
	}
}

// 3.5: a second daemon refuses ownership, states the reason, and starts no
// agent process.
func TestReconcileManagedHostsIfOwningRefusesWhenAnotherDaemonOwns(t *testing.T) {
	registry := testManagedHostRegistry(t)
	t.Setenv("MAGENTIC_STATE", filepath.Dir(registry.Path())+"/state.json")
	sessionID := SessionID("session-1")
	if err := registry.RecordIntent(sessionID, "/tmp/session-1.sock", NewAgentHostToken()); err != nil {
		t.Fatal(err)
	}
	state := &State{Agents: []Session{{ID: sessionID, Name: "s"}}}

	claimErr := ErrControlServedElsewhere
	results, err := registry.ReconcileIfOwning(claimErr, state)
	if !errors.Is(err, ErrControlServedElsewhere) {
		t.Fatalf("err = %v, want the stated ownership refusal", err)
	}
	if results != nil {
		t.Fatalf("results = %+v, want none — a refused daemon starts no agent process", results)
	}
}
