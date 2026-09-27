package core

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeOmpVerifyProcess replays a fixed sequence of NDJSON lines as ReadLine
// results — the ready frame first, then whatever a scripted turn needs —
// regardless of what was written to it, and records every WriteLine call for
// assertion. No real omp process is ever spawned.
type fakeOmpVerifyProcess struct {
	version    string
	versionErr error
	startErr   error
	lines      [][]byte
	idx        int
	written    []map[string]any
	writtenAt  []int // lines read when each write happened
	argv       []string
	dir        string
}

func (f *fakeOmpVerifyProcess) Version(ctx context.Context) (string, error) {
	return f.version, f.versionErr
}

func (f *fakeOmpVerifyProcess) Start(ctx context.Context, argv []string, dir string) error {
	f.argv = argv
	f.dir = dir
	return f.startErr
}

func (f *fakeOmpVerifyProcess) ReadLine(ctx context.Context) ([]byte, error) {
	if f.idx >= len(f.lines) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, io.EOF
		}
	}
	line := f.lines[f.idx]
	f.idx++
	return line, nil
}

func (f *fakeOmpVerifyProcess) WriteLine(ctx context.Context, line []byte) error {
	var decoded map[string]any
	if err := json.Unmarshal(line, &decoded); err != nil {
		return err
	}
	f.written = append(f.written, decoded)
	f.writtenAt = append(f.writtenAt, f.idx)
	return nil
}

func (f *fakeOmpVerifyProcess) Close() error { return nil }

func ompVerifyReadyLine() []byte {
	return []byte(`{"type":"ready","protocolVersion":1,"supportedProtocolVersions":[1,2],"maxFrameBytes":1048576}`)
}

// ompVerifyFixtureLines extracts the "in" frames of an ndjson fixture as raw
// lines, stopping after (and including) the first frame whose type matches
// stopAfterType when stopAfterType is non-empty.
func ompVerifyFixtureLines(t *testing.T, fixture, stopAfterType string) [][]byte {
	t.Helper()
	frames := readOmpFixture(t, fixture)
	var lines [][]byte
	for _, f := range frames {
		if f.Dir != "in" {
			continue
		}
		lines = append(lines, append([]byte(nil), f.Frame...))
		if stopAfterType != "" {
			var envelope struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(f.Frame, &envelope); err == nil && envelope.Type == stopAfterType {
				break
			}
		}
	}
	return lines
}

// 2.6: write and bash each raised a select request that was denied — the
// only case that verifies.
func TestVerifyOmpApprovalGateVerified(t *testing.T) {
	writeTurn := ompVerifyFixtureLines(t, "approval-deny.ndjson", "turn_end")
	bashTurn := [][]byte{
		[]byte(`{"type":"tool_execution_start","toolCallId":"call_bash1","toolName":"bash","args":{"command":"echo omp-verify-ok"}}`),
		[]byte(`{"type":"extension_ui_request","id":"bash-req-1","method":"select","title":"Allow tool: bash\nCommand: echo omp-verify-ok","options":["Approve","Deny"]}`),
		[]byte(`{"type":"tool_execution_end","toolCallId":"call_bash1","toolName":"bash","result":{"content":[{"type":"text","text":"denied"}]},"isError":true}`),
		[]byte(`{"type":"turn_end"}`),
	}
	lines := append([][]byte{ompVerifyReadyLine()}, writeTurn...)
	lines = append(lines, bashTurn...)

	proc := &fakeOmpVerifyProcess{version: "omp/18.2.8", lines: lines}
	dir := t.TempDir()
	result, err := VerifyOmpApprovalGate(context.Background(), proc, nil, func() (string, error) { return dir, nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != OmpVerifyOutcomeVerified {
		t.Fatalf("outcome = %v, want verified (reason: %s)", result.Outcome, result.Reason)
	}

	var denies []map[string]any
	for _, w := range proc.written {
		if w["type"] == "extension_ui_response" && w["value"] == "Deny" {
			denies = append(denies, w)
		}
	}
	if len(denies) != 2 {
		t.Fatalf("expected 2 Deny responses (write + bash), got %d: %v", len(denies), proc.written)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("verify directory %s was not removed", dir)
	}
}

// 2.6: a bash tool_execution_start followed by tool_execution_end with no
// select in between must fail — the unsafe direction.
func TestVerifyOmpApprovalGateFailed(t *testing.T) {
	lines := [][]byte{
		ompVerifyReadyLine(),
		[]byte(`{"type":"tool_execution_start","toolCallId":"call_bash1","toolName":"bash","args":{"command":"echo hi"}}`),
		[]byte(`{"type":"tool_execution_end","toolCallId":"call_bash1","toolName":"bash","result":{"content":[{"type":"text","text":"hi"}]}}`),
		[]byte(`{"type":"turn_end"}`),
	}
	proc := &fakeOmpVerifyProcess{version: "omp/18.2.8", lines: lines}
	result, err := VerifyOmpApprovalGate(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != OmpVerifyOutcomeFailed {
		t.Fatalf("outcome = %v, want failed (reason: %s)", result.Outcome, result.Reason)
	}
	if !strings.Contains(result.Reason, "bash") || !strings.Contains(result.Reason, "omp/18.2.8") {
		t.Fatalf("failed reason %q must name the tool and the omp version", result.Reason)
	}
}

// 2.6: both turns end without any gated tool ever being called — inconclusive,
// never verified and never failed.
func TestVerifyOmpApprovalGateInconclusive(t *testing.T) {
	lines := [][]byte{
		ompVerifyReadyLine(),
		[]byte(`{"type":"turn_start"}`),
		[]byte(`{"type":"turn_end"}`),
		[]byte(`{"type":"turn_start"}`),
		[]byte(`{"type":"turn_end"}`),
	}
	proc := &fakeOmpVerifyProcess{version: "omp/18.2.8", lines: lines}
	result, err := VerifyOmpApprovalGate(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != OmpVerifyOutcomeInconclusive {
		t.Fatalf("outcome = %v, want inconclusive (reason: %s)", result.Outcome, result.Reason)
	}
}

// 2.6: a plain-text first line instead of a ready frame is not attempted,
// not failed.
func TestVerifyOmpApprovalGateNotAttemptedHandshakePlaintext(t *testing.T) {
	proc := &fakeOmpVerifyProcess{
		version: "omp/18.2.8",
		lines:   [][]byte{[]byte(`Model "ollama/qwen3.5:9b" not found. Run "omp models" to see available models.`)},
	}
	result, err := VerifyOmpApprovalGate(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != OmpVerifyOutcomeNotAttempted {
		t.Fatalf("outcome = %v, want not attempted (reason: %s)", result.Outcome, result.Reason)
	}
	if !strings.Contains(result.Reason, "not found") {
		t.Fatalf("reason %q must carry omp's own text", result.Reason)
	}
}

// 2.6: a login input request during the run must be cancelled — never
// supplied with input — and the outcome is not attempted, naming the login
// obstacle rather than a gate failure.
func TestVerifyOmpApprovalGateNotAttemptedLogin(t *testing.T) {
	lines := [][]byte{
		ompVerifyReadyLine(),
	}
	lines = append(lines, ompVerifyFixtureLines(t, "login-secret-input.ndjson", "")...)
	proc := &fakeOmpVerifyProcess{version: "omp/18.2.8", lines: lines}
	result, err := VerifyOmpApprovalGate(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != OmpVerifyOutcomeNotAttempted {
		t.Fatalf("outcome = %v, want not attempted (reason: %s)", result.Outcome, result.Reason)
	}

	var responses []map[string]any
	for _, w := range proc.written {
		if w["type"] == "extension_ui_response" {
			responses = append(responses, w)
		}
	}
	if len(responses) != 1 {
		t.Fatalf("expected exactly one extension_ui_response (the cancel), got %d: %v", len(responses), responses)
	}
	if responses[0]["cancelled"] != true {
		t.Fatalf("response = %v, want cancelled:true", responses[0])
	}
	if _, hasValue := responses[0]["value"]; hasValue {
		t.Fatalf("response %v must never carry a value (the login secret)", responses[0])
	}
}

// 2.6: the verifier refuses to run inside a supplied Project root, and
// leaves no directory behind either way.
func TestVerifyOmpApprovalGateRefusesProjectRoot(t *testing.T) {
	root := t.TempDir()
	var created string
	mkTempDir := func() (string, error) {
		dir, err := os.MkdirTemp(root, "magentic-omp-verify-")
		created = dir
		return dir, err
	}
	proc := &fakeOmpVerifyProcess{version: "omp/18.2.8"}
	_, err := VerifyOmpApprovalGate(context.Background(), proc, []string{root}, mkTempDir)
	if err == nil {
		t.Fatal("verification inside a Project root must be refused")
	}
	if created == "" {
		t.Fatal("test setup did not create a directory")
	}
	if _, statErr := os.Stat(created); !os.IsNotExist(statErr) {
		t.Fatalf("verify directory %s was not removed after refusal", created)
	}
	if proc.argv != nil {
		t.Fatal("omp must never be started once the directory is refused")
	}
}

// 2.6: a missing omp binary is not attempted, not failed.
func TestVerifyOmpApprovalGateNotAttemptedMissingBinary(t *testing.T) {
	proc := &fakeOmpVerifyProcess{versionErr: &os.PathError{Op: "exec", Path: "omp", Err: os.ErrNotExist}}
	result, err := VerifyOmpApprovalGate(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != OmpVerifyOutcomeNotAttempted {
		t.Fatalf("outcome = %v, want not attempted (reason: %s)", result.Outcome, result.Reason)
	}
}

// 2.7: a second start of the same build skips verification.
func TestEnsureOmpApprovalGateVerifiedSkipsSameBuild(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "omp-verify-cache.json")
	identity := OmpBuildIdentity{Version: "omp/18.2.8", Path: "/usr/local/bin/omp", Size: 1000, ModTime: time.Unix(1000, 0)}
	if err := SaveOmpVerifyCache(cachePath, identity); err != nil {
		t.Fatal(err)
	}
	proc := &fakeOmpVerifyProcess{versionErr: os.ErrNotExist} // would fail if actually run
	result, err := EnsureOmpApprovalGateVerified(context.Background(), proc, nil, nil, identity, cachePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != OmpVerifyOutcomeVerified {
		t.Fatalf("outcome = %v, want verified from cache", result.Outcome)
	}
	if proc.argv != nil {
		t.Fatal("a cache hit must never start the process")
	}
}

// 2.7: a changed version string forces verification again.
func TestEnsureOmpApprovalGateVerifiedRepeatsOnChangedVersion(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "omp-verify-cache.json")
	old := OmpBuildIdentity{Version: "omp/18.2.8", Path: "/usr/local/bin/omp", Size: 1000, ModTime: time.Unix(1000, 0)}
	if err := SaveOmpVerifyCache(cachePath, old); err != nil {
		t.Fatal(err)
	}
	changed := OmpBuildIdentity{Version: "omp/18.3.0", Path: "/usr/local/bin/omp", Size: 1000, ModTime: time.Unix(1000, 0)}

	lines := [][]byte{ompVerifyReadyLine(), []byte(`{"type":"turn_start"}`), []byte(`{"type":"turn_end"}`), []byte(`{"type":"turn_start"}`), []byte(`{"type":"turn_end"}`)}
	proc := &fakeOmpVerifyProcess{version: changed.Version, lines: lines}
	result, err := EnsureOmpApprovalGateVerified(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil }, changed, cachePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proc.idx == 0 {
		t.Fatal("a changed version string must force the verifier to actually run")
	}
	if result.Outcome != OmpVerifyOutcomeInconclusive {
		t.Fatalf("outcome = %v, want inconclusive", result.Outcome)
	}
}

// 2.7: a changed binary — same version, different size or mtime — forces
// verification again too.
func TestEnsureOmpApprovalGateVerifiedRepeatsOnChangedBinary(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "omp-verify-cache.json")
	old := OmpBuildIdentity{Version: "omp/18.2.8", Path: "/usr/local/bin/omp", Size: 1000, ModTime: time.Unix(1000, 0)}
	if err := SaveOmpVerifyCache(cachePath, old); err != nil {
		t.Fatal(err)
	}
	rebuilt := OmpBuildIdentity{Version: "omp/18.2.8", Path: "/usr/local/bin/omp", Size: 2048, ModTime: time.Unix(2000, 0)}

	lines := [][]byte{ompVerifyReadyLine(), []byte(`{"type":"turn_start"}`), []byte(`{"type":"turn_end"}`), []byte(`{"type":"turn_start"}`), []byte(`{"type":"turn_end"}`)}
	proc := &fakeOmpVerifyProcess{version: rebuilt.Version, lines: lines}
	result, err := EnsureOmpApprovalGateVerified(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil }, rebuilt, cachePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proc.idx == 0 {
		t.Fatal("a rebuilt binary with the same version string must force the verifier to actually run")
	}
	if result.Outcome != OmpVerifyOutcomeInconclusive {
		t.Fatalf("outcome = %v, want inconclusive", result.Outcome)
	}
}

// 2.7: a failed or inconclusive result is never cached.
func TestEnsureOmpApprovalGateVerifiedDoesNotCacheFailureOrInconclusive(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "omp-verify-cache.json")
	identity := OmpBuildIdentity{Version: "omp/18.2.8", Path: "/usr/local/bin/omp", Size: 1000, ModTime: time.Unix(1000, 0)}

	inconclusiveLines := [][]byte{ompVerifyReadyLine(), []byte(`{"type":"turn_start"}`), []byte(`{"type":"turn_end"}`), []byte(`{"type":"turn_start"}`), []byte(`{"type":"turn_end"}`)}
	proc := &fakeOmpVerifyProcess{version: identity.Version, lines: inconclusiveLines}
	result, err := EnsureOmpApprovalGateVerified(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil }, identity, cachePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != OmpVerifyOutcomeInconclusive {
		t.Fatalf("outcome = %v, want inconclusive", result.Outcome)
	}
	if _, statErr := os.Stat(cachePath); !os.IsNotExist(statErr) {
		t.Fatal("an inconclusive result must not be cached")
	}

	failedLines := [][]byte{
		ompVerifyReadyLine(),
		[]byte(`{"type":"tool_execution_start","toolCallId":"call_bash1","toolName":"bash","args":{"command":"echo hi"}}`),
		[]byte(`{"type":"tool_execution_end","toolCallId":"call_bash1","toolName":"bash","result":{"content":[]}}`),
		[]byte(`{"type":"turn_end"}`),
	}
	proc2 := &fakeOmpVerifyProcess{version: identity.Version, lines: failedLines}
	result2, err := EnsureOmpApprovalGateVerified(context.Background(), proc2, nil, func() (string, error) { return t.TempDir(), nil }, identity, cachePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result2.Outcome != OmpVerifyOutcomeFailed {
		t.Fatalf("outcome = %v, want failed", result2.Outcome)
	}
	if _, statErr := os.Stat(cachePath); !os.IsNotExist(statErr) {
		t.Fatal("a failed result must not be cached")
	}
}

// 2.6: a prompt spans several turns. The bash prompt must not be sent at the
// first turn_end (after a read tool) but only after agent_end, or it would
// arrive while the agent is still running.
func TestVerifyOmpApprovalGateWaitsForAgentEndBetweenPrompts(t *testing.T) {
	lines := [][]byte{
		ompVerifyReadyLine(),
		[]byte(`{"type":"tool_execution_start","toolCallId":"r1","toolName":"read"}`),
		[]byte(`{"type":"tool_execution_end","toolCallId":"r1","toolName":"read"}`),
		[]byte(`{"type":"turn_end"}`),
		[]byte(`{"type":"tool_execution_start","toolCallId":"w1","toolName":"write"}`),
		[]byte(`{"type":"extension_ui_request","id":"u1","method":"select","title":"Allow tool: write\nPath: verify.txt","options":["Approve","Deny"]}`),
		[]byte(`{"type":"tool_execution_end","toolCallId":"w1","toolName":"write","isError":true}`),
		[]byte(`{"type":"turn_end"}`),
		[]byte(`{"type":"agent_end","isTerminal":true}`),
		[]byte(`{"type":"tool_execution_start","toolCallId":"b1","toolName":"bash"}`),
		[]byte(`{"type":"extension_ui_request","id":"u2","method":"select","title":"Allow tool: bash\nCommand: echo omp-verify-ok","options":["Approve","Deny"]}`),
		[]byte(`{"type":"tool_execution_end","toolCallId":"b1","toolName":"bash","isError":true}`),
		[]byte(`{"type":"agent_end","isTerminal":true}`),
	}
	agentEndIdx := 9 // lines read once the first agent_end has been consumed
	proc := &fakeOmpVerifyProcess{version: "omp/18.2.8", lines: lines}
	result, err := VerifyOmpApprovalGate(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OmpVerifyOutcomeVerified {
		t.Fatalf("outcome = %v, want verified (reason: %s)", result.Outcome, result.Reason)
	}
	for i, w := range proc.written {
		if w["id"] == "verify-bash" && proc.writtenAt[i] < agentEndIdx {
			t.Fatalf("bash prompt written after %d lines, before the first agent_end", proc.writtenAt[i])
		}
	}
}

// 2.6: parallel tool calls are attributed by the request title, so an
// approval for write does not cover a bash call that ran unasked.
func TestVerifyOmpApprovalGateAttributesParallelToolCalls(t *testing.T) {
	lines := [][]byte{
		ompVerifyReadyLine(),
		[]byte(`{"type":"tool_execution_start","toolCallId":"w1","toolName":"write"}`),
		[]byte(`{"type":"tool_execution_start","toolCallId":"b1","toolName":"bash"}`),
		[]byte(`{"type":"extension_ui_request","id":"u1","method":"select","title":"Allow tool: write\nPath: verify.txt","options":["Approve","Deny"]}`),
		[]byte(`{"type":"tool_execution_end","toolCallId":"b1","toolName":"bash"}`),
		[]byte(`{"type":"tool_execution_end","toolCallId":"w1","toolName":"write","isError":true}`),
		[]byte(`{"type":"agent_end","isTerminal":true}`),
	}
	proc := &fakeOmpVerifyProcess{version: "omp/18.2.8", lines: lines}
	result, err := VerifyOmpApprovalGate(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OmpVerifyOutcomeFailed || !strings.Contains(result.Reason, "bash") {
		t.Fatalf("outcome = %v (%s), want failed naming bash", result.Outcome, result.Reason)
	}
}

// 2.6: a provider error ends verification as not attempted; words in the
// model's own text are never read as a login problem.
func TestVerifyOmpApprovalGateProviderErrorIsNotAttemptedButTextIsNot(t *testing.T) {
	errLines := [][]byte{
		ompVerifyReadyLine(),
		[]byte(`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"401 Unauthorized"}}`),
		[]byte(`{"type":"agent_end","isTerminal":true}`),
	}
	proc := &fakeOmpVerifyProcess{version: "omp/18.2.8", lines: errLines}
	result, _ := VerifyOmpApprovalGate(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil })
	if result.Outcome != OmpVerifyOutcomeNotAttempted || !strings.Contains(result.Reason, "401 Unauthorized") {
		t.Fatalf("outcome = %v (%s), want not attempted naming the provider error", result.Outcome, result.Reason)
	}

	textLines := [][]byte{
		ompVerifyReadyLine(),
		[]byte(`{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"I need no API key for this."}]}}`),
		[]byte(`{"type":"agent_end","isTerminal":true}`),
	}
	proc = &fakeOmpVerifyProcess{version: "omp/18.2.8", lines: textLines}
	result, _ = VerifyOmpApprovalGate(context.Background(), proc, nil, func() (string, error) { return t.TempDir(), nil })
	if result.Outcome != OmpVerifyOutcomeInconclusive {
		t.Fatalf("outcome = %v (%s), want inconclusive", result.Outcome, result.Reason)
	}
}

// 2.8/6.13: each unverified outcome refuses with its own cause; a failed gate
// names the omp version, and only a verified result refuses nothing.
func TestOmpGateRefusalNamesEachCauseDistinctly(t *testing.T) {
	failed := OmpGateRefusal(OmpVerifyResult{Outcome: OmpVerifyOutcomeFailed, OmpVersion: "omp/18.2.8", Reason: "Tool \"bash\" lief"})
	inconclusive := OmpGateRefusal(OmpVerifyResult{Outcome: OmpVerifyOutcomeInconclusive, OmpVersion: "omp/18.2.8"})
	notAttempted := OmpGateRefusal(OmpVerifyResult{Outcome: OmpVerifyOutcomeNotAttempted, Reason: "omp wurde nicht gefunden"})
	if !strings.Contains(failed, "omp/18.2.8") || !strings.Contains(failed, "nicht verifizieren") {
		t.Fatalf("failed = %q, want the version and the unverified gate", failed)
	}
	if !strings.Contains(inconclusive, "ohne Ergebnis") || strings.Contains(inconclusive, "nicht verifizieren") {
		t.Fatalf("inconclusive = %q, must not read as a failed gate", inconclusive)
	}
	if !strings.Contains(notAttempted, "nicht gefunden") || strings.Contains(notAttempted, "nicht verifizieren") {
		t.Fatalf("not attempted = %q, must name the obstacle, not the gate", notAttempted)
	}
	if OmpGateRefusal(OmpVerifyResult{Outcome: OmpVerifyOutcomeVerified}) != "" {
		t.Fatal("a verified gate refuses nothing")
	}
}
