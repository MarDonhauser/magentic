package core

import (
	"errors"
	"os"
	"path/filepath"
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

// waitFor polls until condition holds, so a test never depends on how fast a
// spawned shell reached its next line.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout beim Warten auf %s", what)
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

// 2.2: a matching token confirms identity; a mismatched token is refused —
// and refused as foreign rather than as unreachable, because something is
// answering there.
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
	err = ConnectAgentHost(host.Path(), NewAgentHostToken())
	if !errors.Is(err, ErrAgentHostForeign) {
		t.Fatalf("mismatched token: err = %v, want ErrAgentHostForeign", err)
	}
}

// A path nothing listens on is unreachable, not foreign. The two must stay
// distinguishable: nothing to adopt is a different fact from something alive
// that this token does not own.
func TestConnectAgentHostDistinguishesUnreachableFromForeign(t *testing.T) {
	testAgentHostEnv(t)
	err := ConnectAgentHost(filepath.Join(t.TempDir(), "nobody.sock"), NewAgentHostToken())
	if !errors.Is(err, ErrAgentHostUnreachable) {
		t.Fatalf("err = %v, want ErrAgentHostUnreachable", err)
	}
	if errors.Is(err, ErrAgentHostForeign) {
		t.Fatal("an unreachable path must not read as a foreign host")
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
	pidBefore := host.HostState().PID

	// First "daemon" connects and disconnects.
	if err := ConnectAgentHost(host.Path(), token); err != nil {
		t.Fatal(err)
	}
	if !host.HostState().Alive {
		t.Fatal("vendor process died on the first daemon's disconnect")
	}

	// A later reconnect finds the same process, not a new one.
	if err := ConnectAgentHost(host.Path(), token); err != nil {
		t.Fatalf("reconnect was refused: %v", err)
	}
	state := host.HostState()
	if !state.Alive || state.PID != pidBefore {
		t.Fatalf("reconnect did not leave the same process running: alive=%v pid=%d, want %d",
			state.Alive, state.PID, pidBefore)
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
	pgid := host.HostState().PID

	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if host.HostState().Alive {
		t.Fatal("owned process still alive after Close")
	}
	// Signalling the recorded process group must find nobody left: ESRCH.
	if err := syscall.Kill(-pgid, 0); err == nil {
		t.Fatal("a process in the group is still alive after stop")
	}
}

// The host itself speaks the turn protocol: a delivered prompt is in flight
// until the vendor echoes it, the echo starts the turn, and interrupting ends
// that turn while the process stays alive for the next prompt.
func TestAgentHostTurnFromDeliveryToInterrupt(t *testing.T) {
	testAgentHostEnv(t)
	host, err := StartAgentHost(SessionID("session-1"), NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	// A stand-in vendor that replays every line it is given — which is
	// exactly what --replay-user-messages does — and ignores SIGINT, so the
	// test can prove an interrupt is not a stop.
	if err := host.StartVendorProcess("sh",
		[]string{"-c", `trap "" INT; while IFS= read -r line; do printf '%s\n' "$line"; done`},
		t.TempDir()); err != nil {
		t.Fatal(err)
	}

	if _, err := host.Interrupt(); err == nil {
		t.Fatal("interrupting a Session with no running turn must be refused")
	}

	if err := host.Deliver("msg-1", "hallo"); err != nil {
		t.Fatal(err)
	}
	if inflight := host.HostState().Inflight; inflight.MessageID != "msg-1" {
		t.Fatalf("inflight = %+v, want the delivered prompt in flight", inflight)
	}

	waitFor(t, "den Echo, der den Turn startet", func() bool {
		state := host.HostState()
		return state.TurnKnown && state.Turn.Running
	})
	state := host.HostState()
	if state.Turn.MessageID != "msg-1" {
		t.Fatalf("turn = %+v, want the delivered message's turn", state.Turn)
	}
	if state.Inflight.MessageID != "" {
		t.Fatal("the echo must take the prompt out of flight")
	}

	ended, err := host.Interrupt()
	if err != nil {
		t.Fatal(err)
	}
	if ended.Running || ended.EndReason != TurnEndInterrupted {
		t.Fatalf("ended turn = %+v, want an interrupted, ended turn", ended)
	}
	if !host.HostState().Alive {
		t.Fatal("an interrupt must leave the process alive for the next prompt")
	}
}

// The host reads turn end and streamed output out of the vendor's protocol
// rather than guessing them: a streamed then completed message leaves exactly
// one Item in its final form, and the turn ends with its stated reason.
func TestAgentHostReadsStreamAndTurnEndFromProtocol(t *testing.T) {
	testAgentHostEnv(t)
	host, err := StartAgentHost(SessionID("session-1"), NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()

	script := filepath.Join(t.TempDir(), "protocol.jsonl")
	lines := `{"type":"user","message":{"role":"user","content":"hallo"}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":" da"}}}
{"type":"result","subtype":"success"}
`
	if err := os.WriteFile(script, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	// The stand-in vendor waits for the delivered prompt before replying, so
	// the echo can never arrive before the prompt is in flight.
	if err := host.StartVendorProcess("sh",
		[]string{"-c", `IFS= read -r _; cat "$0"; sleep 30`, script},
		t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := host.Deliver("msg-1", "hallo"); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "das Turn-Ende aus dem Protokoll", func() bool {
		state := host.HostState()
		return state.TurnKnown && !state.Turn.Running
	})
	state := host.HostState()
	if state.Turn.EndReason != TurnEndCompleted {
		t.Fatalf("end reason = %q, want %q", state.Turn.EndReason, TurnEndCompleted)
	}
	if len(state.StreamedItems) != 1 {
		t.Fatalf("streamed items = %+v, want exactly one", state.StreamedItems)
	}
	item := state.StreamedItems[0]
	if item.InProgress {
		t.Fatal("the completed message must supersede its in-progress form")
	}
	if item.Detail != "Hi da" {
		t.Fatalf("detail = %q, want the whole streamed message", item.Detail)
	}
}

// The agent's blocked tool call stays blocked: closing the process closes its
// open request as no longer answerable, never as allowed or denied.
func TestAgentHostClosesOpenPermissionsWhenTheProcessEnds(t *testing.T) {
	testAgentHostEnv(t)
	host, err := StartAgentHost(SessionID("session-1"), NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	if err := host.StartVendorProcess("sleep", []string{"30"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	request := host.OpenPermission("darf ich rm ausführen?")
	if open := host.HostState().OpenPermissions; len(open) != 1 || open[0].ID != request.ID {
		t.Fatalf("open permissions = %+v, want the raised request", open)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if open := host.HostState().OpenPermissions; len(open) != 0 {
		t.Fatalf("open permissions = %+v, want none after the process ended", open)
	}
	closed := host.permissions.Requests(false)
	if len(closed) != 1 || closed[0].Outcome != PermissionUnanswerable {
		t.Fatalf("closed request = %+v, want the unanswerable outcome", closed)
	}
}
