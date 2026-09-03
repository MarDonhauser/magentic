package core

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// testAgentHostEnv points MAGENTIC_STATE at a short-named temp directory.
// t.TempDir() embeds the test name, which routinely blows past the ~104
// byte sockaddr_un limit once "agent-hosts/<session-id>.sock" is appended.
func testAgentHostEnv(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ah")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("MAGENTIC_STATE", dir+"/state.json")
}

// 2.1: the socket is created with restrictive permissions and removed on
// clean exit.
func TestStartAgentHostSocketPermissionsAndCleanup(t *testing.T) {
	testAgentHostEnv(t)
	sessionID := SessionID("session-1")
	host, err := StartAgentHost(sessionID, NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(host.Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != agentHostSocketMode {
		t.Fatalf("socket permissions = %o, want %o", perm, agentHostSocketMode)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(host.Path()); !os.IsNotExist(err) {
		t.Fatalf("socket still exists after clean Close: %v", err)
	}
}

// 2.2: a matching token confirms identity; a mismatched token is refused.
func TestAgentHostHandshakeConfirmsOrRefusesByToken(t *testing.T) {
	testAgentHostEnv(t)
	token := NewAgentHostToken()
	host, err := StartAgentHost(SessionID("session-1"), token)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()

	if err := ConnectAgentHost(host.Path(), token); err != nil {
		t.Fatalf("matching token was refused: %v", err)
	}
	if err := ConnectAgentHost(host.Path(), NewAgentHostToken()); err == nil {
		t.Fatal("mismatched token was confirmed")
	}
}

// 2.3: the exact argv is built for a fresh Session and for a continued one.
func TestClaudeManagedArgv(t *testing.T) {
	run := &AgentRunRef{Vendor: AgentVendorClaude, ExternalID: "run-123"}
	want := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--replay-user-messages",
		"--permission-prompts", "host",
		"--permission-prompt-tool", ClaudeApprovalMCPToolName,
		"--mcp-config", "/state/agent-hosts/session-1/mcp.json",
	}

	fresh, err := ClaudeManagedArgv("/state/agent-hosts/session-1/mcp.json", run, "new")
	if err != nil {
		t.Fatal(err)
	}
	wantFresh := append(append([]string{}, want...), "--session-id", "run-123")
	if !stringSlicesEqual(fresh, wantFresh) {
		t.Fatalf("fresh argv = %v, want %v", fresh, wantFresh)
	}

	resumed, err := ClaudeManagedArgv("/state/agent-hosts/session-1/mcp.json", run, "resume")
	if err != nil {
		t.Fatal(err)
	}
	wantResumed := append(append([]string{}, want...), "--resume", "run-123")
	if !stringSlicesEqual(resumed, wantResumed) {
		t.Fatalf("resumed argv = %v, want %v", resumed, wantResumed)
	}

	if _, err := ClaudeManagedArgv("/state/agent-hosts/session-1/mcp.json", nil, "new"); err == nil {
		t.Fatal("a managed start without a stored run reference must be refused")
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 2.4: a verified version is accepted, an unverified one is refused with a
// stated reason.
func TestVerifyClaudeManagedRuntimeVersion(t *testing.T) {
	if ok, reason := VerifyClaudeManagedRuntimeVersion("2.1.259"); !ok || reason != "" {
		t.Fatalf("verified version rejected: ok=%v reason=%q", ok, reason)
	}
	ok, reason := VerifyClaudeManagedRuntimeVersion("1.0.0")
	if ok {
		t.Fatal("unverified version was accepted")
	}
	if reason == "" {
		t.Fatal("an unverified version must state a reason")
	}
}

// 2.5: disconnecting and reconnecting the daemon leaves the vendor process
// running and the Session state (its socket path and PID) intact.
func TestAgentHostSurvivesDaemonDisconnectAndReconnect(t *testing.T) {
	testAgentHostEnv(t)
	token := NewAgentHostToken()
	host, err := StartAgentHost(SessionID("session-1"), token)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if err := host.StartVendorProcess("sleep", []string{"30"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	pidBefore := host.VendorProcessPID()

	// First "daemon" connects and disconnects.
	if err := ConnectAgentHost(host.Path(), token); err != nil {
		t.Fatal(err)
	}
	if !host.VendorProcessAlive() {
		t.Fatal("vendor process died on the first daemon's disconnect")
	}

	// A later reconnect finds the same process, not a new one.
	if err := ConnectAgentHost(host.Path(), token); err != nil {
		t.Fatalf("reconnect was refused: %v", err)
	}
	if !host.VendorProcessAlive() || host.VendorProcessPID() != pidBefore {
		t.Fatalf("reconnect did not leave the same process running: alive=%v pid=%d, want %d",
			host.VendorProcessAlive(), host.VendorProcessPID(), pidBefore)
	}
}

// 2.6: no child of the owned process survives an explicit stop.
func TestAgentHostStopLeavesNoChildAlive(t *testing.T) {
	testAgentHostEnv(t)
	host, err := StartAgentHost(SessionID("session-1"), NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	// The parent spawns a child of its own, so the test can prove the whole
	// process group is stopped rather than just the direct child.
	if err := host.StartVendorProcess("sh", []string{"-c", "sleep 30 & wait"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Give the shell a moment to fork its child before we stop the group.
	time.Sleep(200 * time.Millisecond)
	pgid := host.VendorProcessPID()

	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if host.VendorProcessAlive() {
		t.Fatal("owned process still alive after Close")
	}
	// Signalling the recorded process group must find nobody left: ESRCH.
	if err := syscall.Kill(-pgid, 0); err == nil {
		t.Fatal("a process in the group is still alive after stop")
	}
}
