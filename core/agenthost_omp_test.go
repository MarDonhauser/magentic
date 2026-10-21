package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const ompTestReady = `{"type":"ready","protocolVersion":1,"supportedProtocolVersions":[1,2],"maxFrameBytes":1048576}`

// startScriptedOmpHost starts a host whose "omp" is sh running script. The
// script's stdin is the host's command stream; received lines are appended to
// the returned log so a test can assert what reached omp.
func startScriptedOmpHost(t *testing.T, script string) (*AgentHost, string, error) {
	t.Helper()
	testAgentHostEnv(t)
	previous := ompBinary
	ompBinary = "sh"
	t.Cleanup(func() { ompBinary = previous })
	// The scripted stand-in never anticipates the get_state query
	// StartOmpProcess otherwise sends; tests that specifically exercise it
	// set this back to true themselves.
	previousGetState := ompSendInitialGetState
	ompSendInitialGetState = false
	t.Cleanup(func() { ompSendInitialGetState = previousGetState })
	host, err := StartAgentHost(SessionID("session-1"), NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	received := filepath.Join(t.TempDir(), "received.ndjson")
	_, err = host.StartOmpProcess([]string{"-c", script, "sh", received}, t.TempDir())
	return host, received, err
}

func readReceived(t *testing.T, path string) string {
	t.Helper()
	data, _ := os.ReadFile(path)
	return string(data)
}

// 2.4: a missing omp binary refuses the runtime with a reason naming omp and
// starts nothing else in its place.
func TestOmpHostRefusesMissingBinary(t *testing.T) {
	testAgentHostEnv(t)
	previous := ompBinary
	ompBinary = "omp-does-not-exist-" + NewUUID()
	t.Cleanup(func() { ompBinary = previous })
	host, err := StartAgentHost(SessionID("session-1"), NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	_, err = host.StartOmpProcess(nil, t.TempDir())
	if !errors.Is(err, ErrOmpUnavailable) || !strings.Contains(err.Error(), "nicht installiert") {
		t.Fatalf("err = %v, want ErrOmpUnavailable naming the missing binary", err)
	}
	if host.HostState().Alive {
		t.Fatal("no process may be running after a refused start")
	}
}

// 2.3/2.4: omp printing a plain-text reason and exiting is refused with that
// reason visible, and no process is left.
func TestOmpHostRefusesImmediateExitWithItsReason(t *testing.T) {
	host, _, err := startScriptedOmpHost(t, `echo 'Model "x/y" not found. Run "omp models" to see available models.'; exit 0`)
	if !errors.Is(err, ErrOmpUnavailable) || !strings.Contains(err.Error(), `Model "x/y" not found`) {
		t.Fatalf("err = %v, want ErrOmpUnavailable carrying omp's own text", err)
	}
	if host.HostState().Alive {
		t.Fatal("no process may be running after a refused start")
	}
}

// 2.3: an unsupported active protocol version is refused at start.
func TestOmpHostRefusesUnsupportedHandshake(t *testing.T) {
	_, _, err := startScriptedOmpHost(t, `echo '{"type":"ready","protocolVersion":7,"supportedProtocolVersions":[7]}'; sleep 30`)
	if !errors.Is(err, ErrOmpUnavailable) || !strings.Contains(err.Error(), "7") {
		t.Fatalf("err = %v, want a refusal naming version 7", err)
	}
}

// 3.8: a prompt is delivered only on omp's user-message echo, and the turn
// ends on agent_end — not on the first success response.
func TestOmpHostDeliversOnEchoAndEndsOnAgentEnd(t *testing.T) {
	host, received, err := startScriptedOmpHost(t, `echo '`+ompTestReady+`'
IFS= read -r line; printf '%s\n' "$line" >> "$1"
echo '{"id":"deliver-msg-1","type":"response","command":"prompt","success":true}'
sleep 0.2
echo '{"type":"agent_start"}'
echo '{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"hallo"}]}}'
echo '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hi"}}'
echo '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":" da"}}'
echo '{"type":"message_end","message":{"role":"assistant","stopReason":"stop"}}'
echo '{"type":"agent_end","isTerminal":true}'
sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Deliver("msg-1", "hallo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "das Turn-Ende", func() bool {
		state := host.HostState()
		return state.TurnKnown && !state.Turn.Running
	})
	state := host.HostState()
	if state.Turn.EndReason != TurnEndCompleted || state.Turn.MessageID != "msg-1" {
		t.Fatalf("turn = %+v, want msg-1 completed", state.Turn)
	}
	if state.Inflight.MessageID != "" {
		t.Fatalf("inflight = %+v, want the echoed prompt dequeued", state.Inflight)
	}
	if len(state.StreamedItems) != 1 || state.StreamedItems[0].Detail != "Hi da" || state.StreamedItems[0].InProgress {
		t.Fatalf("streamed = %+v, want one completed message", state.StreamedItems)
	}
	got := readReceived(t, received)
	if !strings.Contains(got, `"type":"prompt"`) || !strings.Contains(got, `"id":"deliver-msg-1"`) || strings.Contains(got, "\x1b[200~") {
		t.Fatalf("omp received %q, want one plain prompt command", got)
	}
}

// 3.8: a success followed by a refusal on the same id leaves the prompt
// queued with omp's reason.
func TestOmpHostKeepsPromptQueuedOnLateRefusal(t *testing.T) {
	host, _, err := startScriptedOmpHost(t, `echo '`+ompTestReady+`'
IFS= read -r line
echo '{"id":"deliver-msg-1","type":"response","command":"prompt","success":true}'
echo '{"id":"deliver-msg-1","type":"response","command":"prompt","success":false,"error":"Agent is already processing."}'
sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Deliver("msg-1", "hallo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "die Ablehnung", func() bool {
		_, _, failed := host.turns.DeliveryFailure()
		return failed
	})
	reason, _, _ := host.turns.DeliveryFailure()
	state := host.HostState()
	if !strings.Contains(reason, "already processing") || state.TurnKnown && state.Turn.Running {
		t.Fatalf("reason %q, state %+v: want omp's refusal and no turn", reason, state)
	}
}

// 3.4: a prompt resolved locally is delivered and leaves no running turn.
func TestOmpHostLocalCommandDoesNotLeaveTurnRunning(t *testing.T) {
	host, _, err := startScriptedOmpHost(t, `echo '`+ompTestReady+`'
IFS= read -r line
echo '{"type":"command_output","text":"Context window"}'
echo '{"id":"deliver-msg-1","type":"response","command":"prompt","success":true,"data":{"agentInvoked":false}}'
sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Deliver("msg-1", "/context"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "die lokale Erledigung", func() bool {
		state := host.HostState()
		return state.TurnKnown && !state.Turn.Running
	})
	if host.HostState().Inflight.MessageID != "" {
		t.Fatal("a locally resolved prompt must be dequeued")
	}
}

// 2.14/3.5: an approval request becomes an open PermissionRequest that only a
// person answers, and the answer reaches omp exactly once.
func TestOmpHostRelaysApprovalOnlyOnAPersonsDecision(t *testing.T) {
	host, received, err := startScriptedOmpHost(t, `echo '`+ompTestReady+`'
printf '%s\n' '{"type":"extension_ui_request","id":"ui-1","method":"select","title":"Allow tool: write\\nPath: a.txt","options":["Approve","Deny"]}'
IFS= read -r line; printf '%s\n' "$line" >> "$1"
sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "die offene Anfrage", func() bool { return len(host.HostState().OpenPermissions) == 1 })
	request := host.HostState().OpenPermissions[0]
	if !strings.HasPrefix(request.Asked, "Allow tool: write") {
		t.Fatalf("asked = %q, want omp's own request text", request.Asked)
	}
	if got := readReceived(t, received); got != "" {
		t.Fatalf("omp received %q before anyone decided", got)
	}
	if err := host.Answer(request.ID, PermissionDeny, "Test"); err != nil {
		t.Fatal(err)
	}
	if err := host.Answer(request.ID, PermissionAllow, "Test"); err == nil {
		t.Fatal("a second answer must be refused")
	}
	waitFor(t, "die Antwort an omp", func() bool { return readReceived(t, received) != "" })
	got := readReceived(t, received)
	if !strings.Contains(got, `"id":"ui-1"`) || !strings.Contains(got, `"value":"Deny"`) {
		t.Fatalf("omp received %q, want Deny for ui-1", got)
	}
}

// 2.10: a login asking for a key is cancelled without collecting anything.
func TestOmpHostCancelsSecretInput(t *testing.T) {
	host, received, err := startScriptedOmpHost(t, `echo '`+ompTestReady+`'
echo '{"type":"extension_ui_request","id":"ui-key","method":"input","title":"Paste your Z.AI API key"}'
IFS= read -r line; printf '%s\n' "$line" >> "$1"
sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "den Abbruch", func() bool { return readReceived(t, received) != "" })
	got := readReceived(t, received)
	if !strings.Contains(got, `"id":"ui-key"`) || !strings.Contains(got, `"cancelled":true`) || strings.Contains(got, "value") {
		t.Fatalf("omp received %q, want a bare cancellation", got)
	}
	if len(host.HostState().OpenPermissions) != 0 {
		t.Fatal("a secret input is not a permission request")
	}
}

// 3.3/3.10: agent_end with isTerminal:false keeps the turn running; an abort
// ends it as interrupted only when omp reports the aborted turn.
func TestOmpHostTurnEndFollowsTheProtocol(t *testing.T) {
	host, received, err := startScriptedOmpHost(t, `echo '`+ompTestReady+`'
IFS= read -r line
echo '{"type":"message_start","message":{"role":"user","content":"hallo"}}'
echo '{"type":"agent_end","isTerminal":false}'
IFS= read -r line; printf '%s\n' "$line" >> "$1"
sleep 0.3
echo '{"type":"message_end","message":{"role":"assistant","stopReason":"aborted","errorMessage":"Interrupted by user"}}'
echo '{"type":"agent_end","isTerminal":true}'
sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Deliver("msg-1", "hallo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "den laufenden Turn", func() bool { return host.HostState().Turn.Running })
	turn, err := host.Interrupt()
	if err != nil {
		t.Fatal(err)
	}
	if !turn.Running {
		t.Fatal("the request alone must not end the turn")
	}
	waitFor(t, "das gemeldete Abbruchende", func() bool {
		state := host.HostState()
		return state.TurnKnown && !state.Turn.Running
	})
	if reason := host.HostState().Turn.EndReason; reason != TurnEndInterrupted {
		t.Fatalf("end reason = %q, want interrupted", reason)
	}
	if got := readReceived(t, received); !strings.Contains(got, `"type":"abort"`) {
		t.Fatalf("omp received %q, want an abort command", got)
	}
}

// 2.5: a recorded launch is accepted only when it carries the gate exactly,
// and the host reports the argv it actually started with.
func TestOmpLaunchArgvHasGate(t *testing.T) {
	good, err := OmpArgv(Session{Dir: "/p"}, nil, "new")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		argv []string
		want bool
	}{
		"built by OmpArgv":   {good, true},
		"missing profile":    {[]string{"--mode", "rpc-ui", "--approval-mode", "always-ask"}, false},
		"yolo":               {[]string{"--mode", "rpc-ui", "--approval-mode", "yolo", "--profile", "magentic"}, false},
		"gate given twice":   {append(append([]string(nil), good...), "--approval-mode", "yolo"), false},
		"auto-approve added": {append(append([]string(nil), good...), "--auto-approve"), false},
	}
	for name, c := range cases {
		if got := OmpLaunchArgvHasGate(c.argv); got != c.want {
			t.Errorf("%s: OmpLaunchArgvHasGate = %v, want %v", name, got, c.want)
		}
	}
}

func TestOmpHostReportsItsOwnLaunchArgv(t *testing.T) {
	host, _, err := startScriptedOmpHost(t, `echo '`+ompTestReady+`'; sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	state := host.HostState()
	if len(state.LaunchArgv) == 0 || state.LaunchArgv[0] != "-c" || state.ProtocolVersion != 1 {
		t.Fatalf("state = %+v, want the argv the host started with and protocol 1", state)
	}
}

// 2.5: a reclaimed omp host carries the launch it reports for itself, and
// only when that is the recorded one; a host whose identity is not confirmed
// yields no provenance and is neither adopted nor killed.
func TestReconcileGivesProvenanceOnlyToAConfirmedMatchingHost(t *testing.T) {
	gate, err := OmpArgv(Session{Dir: "/p"}, nil, "new")
	if err != nil {
		t.Fatal(err)
	}
	// The scripted host runs sh; its recorded argv carries the gate so the
	// comparison is about identity and equality, not about sh.
	scripted := append([]string{"-c", `echo '` + ompTestReady + `'; sleep 30`, "sh"}, gate...)
	host, _, startErr := startScriptedOmpHostArgv(t, scripted)
	if startErr != nil {
		t.Fatal(startErr)
	}
	registry := OpenManagedHostRegistry(filepath.Join(filepath.Dir(StatePath()), "managed-hosts.json"))
	state := &State{Agents: []Session{{ID: host.SessionID(), Name: "omp"}}}

	if err := registry.RecordLaunchIntent(host.SessionID(), host.Path(), host.token, scripted); err != nil {
		t.Fatal(err)
	}
	results, err := registry.Reconcile(state)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Outcome != ManagedHostReclaimed || !slicesEqual(results[0].Provenance, scripted) {
		t.Fatalf("result = %+v, want reclaimed with the host's own launch", results[0])
	}

	if err := registry.RecordLaunchIntent(host.SessionID(), host.Path(), host.token, gate); err != nil {
		t.Fatal(err)
	}
	results, _ = registry.Reconcile(state)
	if len(results[0].Provenance) != 0 || results[0].Reason == "" {
		t.Fatalf("result = %+v, want no provenance when the host reports a different launch", results[0])
	}

	if err := registry.RecordLaunchIntent(host.SessionID(), host.Path(), NewAgentHostToken(), scripted); err != nil {
		t.Fatal(err)
	}
	results, _ = registry.Reconcile(state)
	if results[0].Outcome != ManagedHostForeign || len(results[0].Provenance) != 0 {
		t.Fatalf("result = %+v, want foreign without provenance", results[0])
	}
	if !host.HostState().Alive {
		t.Fatal("reconciliation must not kill a host it could not confirm")
	}
}

func startScriptedOmpHostArgv(t *testing.T, argv []string) (*AgentHost, string, error) {
	t.Helper()
	testAgentHostEnv(t)
	previous := ompBinary
	ompBinary = "sh"
	t.Cleanup(func() { ompBinary = previous })
	host, err := StartAgentHost(SessionID("session-1"), NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	_, err = host.StartOmpProcess(argv, t.TempDir())
	return host, "", err
}

func slicesEqual(a, b []string) bool {
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

// 2.10: omp's actual login-secret-input exchange (core/testdata/omp/login-secret-input.ndjson)
// is cancelled without collecting anything, and reported to the developer
// naming what was asked, not attempted headlessly.
func TestOmpHostReportsLoginRequiredFromRealFixture(t *testing.T) {
	frames := readOmpFixture(t, "login-secret-input.ndjson")
	var script strings.Builder
	script.WriteString("echo '" + ompTestReady + "'\n")
	for _, f := range frames {
		if f.Dir != "in" {
			continue
		}
		line, err := f.Frame.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		script.WriteString("printf '%s\\n' ")
		script.WriteString(shellQuote(string(line)))
		script.WriteString("\n")
	}
	script.WriteString(`IFS= read -r line; printf '%s\n' "$line" >> "$1"` + "\n")
	script.WriteString("sleep 30\n")

	host, received, err := startScriptedOmpHost(t, script.String())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "die gemeldete Login-Anforderung", func() bool {
		return host.HostState().LoginRequired != ""
	})
	state := host.HostState()
	if !strings.Contains(state.LoginRequired, "API key") {
		t.Fatalf("LoginRequired = %q, want omp's own request text", state.LoginRequired)
	}
	waitFor(t, "die Antwort an omp", func() bool { return readReceived(t, received) != "" })
	got := readReceived(t, received)
	if !strings.Contains(got, `"cancelled":true`) || strings.Contains(got, "sk-") || strings.Contains(got, `"value"`) {
		t.Fatalf("omp received %q, want a bare cancellation and no collected value", got)
	}
	if len(host.HostState().OpenPermissions) != 0 {
		t.Fatal("a secret-input request is not a permission request")
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// 2.15: opening a route into omp's own interface withdraws the Session's
// proven-gate claim; nothing re-asserts it by querying the session, and a
// fresh RecordLaunchIntent — a new process Magentic itself starts under the
// gate — begins a new, not-yet-withdrawn claim.
func TestWithdrawGateProofFlipsTheClaimAndDoesNotReassertItself(t *testing.T) {
	testAgentHostEnv(t)
	registry := OpenManagedHostRegistry(filepath.Join(filepath.Dir(StatePath()), "managed-hosts.json"))
	argv, err := OmpArgv(Session{Dir: "/p"}, nil, "new")
	if err != nil {
		t.Fatal(err)
	}
	sessionID := SessionID("session-route")
	if err := registry.RecordLaunchIntent(sessionID, "/tmp/x.sock", NewAgentHostToken(), argv); err != nil {
		t.Fatal(err)
	}
	record, _, _ := registry.RecordFor(sessionID)
	stateMatching := AgentHostState{Alive: true, LaunchArgv: argv}
	if !hostProvenanceEstablished(record, stateMatching) {
		t.Fatal("provenance must be established before the route is opened")
	}

	if err := registry.WithdrawGateProof(sessionID, "omp's own interface"); err != nil {
		t.Fatal(err)
	}
	record, _, _ = registry.RecordFor(sessionID)
	if !record.GateWithdrawn || record.GateWithdrawnReason != "omp's own interface" || record.GateWithdrawnAt.IsZero() {
		t.Fatalf("record = %+v, want the withdrawal recorded with its reason", record)
	}
	// Nothing re-asserts it: the same, unchanged process state that proved
	// the gate before still reads as withdrawn now — the omp process itself
	// was never asked and cannot answer.
	if hostProvenanceEstablished(record, stateMatching) {
		t.Fatal("provenance must read as withdrawn even though the process's own reported launch is unchanged")
	}
	if err := registry.WithdrawGateProof(sessionID, "a second, different route"); err != nil {
		t.Fatal(err)
	}
	record, _, _ = registry.RecordFor(sessionID)
	if record.GateWithdrawnReason != "omp's own interface" {
		t.Fatalf("reason = %q, want the first withdrawal's reason kept", record.GateWithdrawnReason)
	}

	// A fresh process Magentic itself starts under the gate is its own claim.
	if err := registry.RecordLaunchIntent(sessionID, "/tmp/y.sock", NewAgentHostToken(), argv); err != nil {
		t.Fatal(err)
	}
	record, _, _ = registry.RecordFor(sessionID)
	if record.GateWithdrawn {
		t.Fatal("a fresh RecordLaunchIntent must start an unwithdrawn claim")
	}
	if !hostProvenanceEstablished(record, stateMatching) {
		t.Fatal("the fresh process's provenance must be established")
	}
}

// WithdrawGateProof for a Session with no recorded host is a no-op.
func TestWithdrawGateProofNoOpWithoutARecord(t *testing.T) {
	testAgentHostEnv(t)
	registry := OpenManagedHostRegistry(filepath.Join(filepath.Dir(StatePath()), "managed-hosts.json"))
	if err := registry.WithdrawGateProof(SessionID("nobody"), "route"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := registry.RecordFor(SessionID("nobody")); found {
		t.Fatal("withdrawing for an unrecorded Session must not create a record")
	}
}

// 3.2: turn_start/turn_end map onto working/idle within a running agent run,
// independent of Turn.Running (which spans the whole run).
func TestOmpHostProtocolTurnRunningTracksTurnStartAndEnd(t *testing.T) {
	host, _, err := startScriptedOmpHost(t, `echo '`+ompTestReady+`'
IFS= read -r line
echo '{"type":"turn_start"}'
echo '{"type":"message_start","message":{"role":"user","content":"hallo"}}'
sleep 0.2
echo '{"type":"turn_end"}'
sleep 0.2
echo '{"type":"turn_start"}'
sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Deliver("msg-1", "hallo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "protocolTurnRunning=true nach turn_start", func() bool {
		state := host.HostState()
		return state.Turn.Running && state.ProtocolTurnRunning
	})
	waitFor(t, "protocolTurnRunning=false nach turn_end", func() bool {
		state := host.HostState()
		return state.Turn.Running && !state.ProtocolTurnRunning
	})
	waitFor(t, "protocolTurnRunning=true nach dem zweiten turn_start", func() bool {
		state := host.HostState()
		return state.Turn.Running && state.ProtocolTurnRunning
	})
}

// 3.9's primitive: WatchAgentHostState returns as soon as the host's state
// changes, not after some fixed interval, and reports Changed=false when the
// bounded wait simply timed out with nothing new.
func TestWatchAgentHostStateReturnsOnChangeNotOnATimer(t *testing.T) {
	host, _, err := startScriptedOmpHost(t, `echo '`+ompTestReady+`'; sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	socketPath := host.Path()
	token := host.token

	state, revision, changed, err := WatchAgentHostState(socketPath, token, 0, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("the very first call has nothing to compare since=0 against and must report a change")
	}
	if !state.Alive {
		t.Fatalf("state = %+v, want the live process reflected", state)
	}

	// No timeout: a second watch call from the current revision blocks until
	// something actually changes.
	done := make(chan struct{})
	var gotState AgentHostState
	var gotChanged bool
	go func() {
		defer close(done)
		gotState, _, gotChanged, err = WatchAgentHostState(socketPath, token, revision, 5*time.Second)
	}()
	select {
	case <-done:
		t.Fatal("watch returned before anything changed")
	case <-time.After(200 * time.Millisecond):
	}
	if err := host.Deliver("msg-1", "hallo"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	<-done
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("watch took %s to notice the change, want near-instant", elapsed)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !gotChanged {
		t.Fatal("want the change reported")
	}
	if gotState.Inflight.MessageID != "msg-1" {
		t.Fatalf("state = %+v, want the delivered prompt reflected", gotState)
	}

	// A bounded wait against the current revision, with nothing changing,
	// times out and reports no change rather than blocking forever.
	_, _, timedOutChanged, err := WatchAgentHostState(socketPath, token, revisionAfterDeliver(t, socketPath, token, revision), 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if timedOutChanged {
		t.Fatal("a wait with nothing new must report no change, not a spurious one")
	}
}

// revisionAfterDeliver reads the current revision once, for a wait that
// should then find nothing new.
func revisionAfterDeliver(t *testing.T, socketPath string, token AgentHostToken, sinceAtLeast uint64) uint64 {
	t.Helper()
	_, revision, _, err := WatchAgentHostState(socketPath, token, sinceAtLeast, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

// omp's own sessionId, when get_state answers with one, is captured as
// OmpSessionID — the run reference resume support (section 7) will need.
func TestOmpHostDiscoversOwnSessionIDFromGetState(t *testing.T) {
	testAgentHostEnv(t)
	previous := ompBinary
	ompBinary = "sh"
	t.Cleanup(func() { ompBinary = previous })
	// This test specifically exercises the get_state query the other omp
	// host tests disable.
	host, err := StartAgentHost(SessionID("session-1"), NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	script := `echo '` + ompTestReady + `'
IFS= read -r line
echo '{"id":"init-get-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"01a0ca7c-72de-73ea-ac9e-5bd35a7de617"}}'
sleep 30`
	if _, err := host.StartOmpProcess([]string{"-c", script, "sh"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "die entdeckte omp-Session-ID", func() bool { return host.HostState().OmpSessionID != "" })
	if got := host.HostState().OmpSessionID; got != "01a0ca7c-72de-73ea-ac9e-5bd35a7de617" {
		t.Fatalf("OmpSessionID = %q", got)
	}
}
