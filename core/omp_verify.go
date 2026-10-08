package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OmpVerifyOutcome names the result of a behavioral verification run of the
// approval gate (approval-gate spec, "The gate mechanism is verified against
// the installed omp before it is relied upon").
type OmpVerifyOutcome string

const (
	// OmpVerifyOutcomeVerified means a write-tier and an exec-tier tool each
	// raised an approval request that was denied.
	OmpVerifyOutcomeVerified OmpVerifyOutcome = "verified"
	// OmpVerifyOutcomeFailed means a write or exec tool reached
	// tool_execution_end without an approval request ever having been raised
	// for it — the unsafe direction.
	OmpVerifyOutcomeFailed OmpVerifyOutcome = "failed"
	// OmpVerifyOutcomeInconclusive means the run completed without omp
	// calling a gated tool at all; it must never be treated as verified or
	// as failed.
	OmpVerifyOutcomeInconclusive OmpVerifyOutcome = "inconclusive"
	// OmpVerifyOutcomeNotAttempted means omp could not be run or used at
	// all: binary missing, handshake refused, a login was needed, or no
	// ready frame arrived. Reason carries the actual obstacle.
	OmpVerifyOutcomeNotAttempted OmpVerifyOutcome = "not_attempted"
)

// OmpVerifyResult is the typed outcome of one behavioral verification run.
// Reason is German and names the obstacle, the tool, or the omp version as
// appropriate; OmpVersion is set whenever the installed omp answered
// --version, even when verification could not proceed further.
type OmpVerifyResult struct {
	Outcome    OmpVerifyOutcome
	Reason     string
	OmpVersion string
}

// ompVerifyWriteTierTools and ompVerifyExecTierTools name the tools the
// approval-gate spec scopes the gate to: tools that write or execute. Only
// requests attributable to one of these tools count toward verification.
var (
	ompVerifyWriteTierTools = map[string]bool{"write": true, "edit": true}
	ompVerifyExecTierTools  = map[string]bool{"bash": true}
)

// OmpVerifyProcess is the seam a verification run talks to: NDJSON lines in
// and out, plus version and close. Tests replay scripted frames against a
// fake implementation without spawning omp; ompExecProcess is the real one.
type OmpVerifyProcess interface {
	// Version reports the installed omp's `omp --version` output.
	Version(ctx context.Context) (string, error)
	// Start launches the process with argv, running in dir.
	Start(ctx context.Context, argv []string, dir string) error
	// ReadLine returns the next NDJSON line omp emitted, or an error —
	// including ctx's deadline — when none arrives.
	ReadLine(ctx context.Context) ([]byte, error)
	// WriteLine sends one NDJSON line to omp's stdin.
	WriteLine(ctx context.Context, line []byte) error
	// Close ends the process, killing it if still running.
	Close() error
}

// ompVerifyState is carried across both prompts a verification run sends, so
// an approval seen for the write tool during the first turn still counts
// once the second turn asks for the exec tool.
type ompVerifyState struct {
	// openTools maps each running tool call to its tool name. omp can run
	// tool calls in parallel, so more than one may be open at a time.
	openTools map[string]string
	// approved holds the open tool calls an approval request was raised for.
	approved         map[string]bool
	sawWriteApproval bool
	sawExecApproval  bool
	failedTool       string
	loginReason      string
}

// ompVerifyFrame reads the subset of omp's event vocabulary a verification
// run needs to attribute requests to tools and to drive turns.
type ompVerifyFrame struct {
	Type       string   `json:"type"`
	ID         string   `json:"id"`
	Method     string   `json:"method"`
	Options    []string `json:"options"`
	ToolCallID string   `json:"toolCallId"`
	ToolName   string   `json:"toolName"`
	Title      string   `json:"title"`
	Message    *struct {
		Role         string `json:"role"`
		StopReason   string `json:"stopReason"`
		ErrorMessage string `json:"errorMessage"`
	} `json:"message"`
}

// VerifyOmpApprovalGate drives one throwaway ephemeral omp session and
// reports whether it can prove the approval gate: a write-tier and an
// exec-tier tool each raised an extension_ui_request that was answered
// Deny. It creates and removes a Magentic-owned temporary directory itself
// and refuses to run inside any of roots (a Project or worktree path).
func VerifyOmpApprovalGate(ctx context.Context, proc OmpVerifyProcess, roots []string, mkTempDir func() (string, error)) (OmpVerifyResult, error) {
	if mkTempDir == nil {
		mkTempDir = func() (string, error) { return os.MkdirTemp("", "magentic-omp-verify-") }
	}
	dir, err := mkTempDir()
	if err != nil {
		return OmpVerifyResult{}, fmt.Errorf("Verify-Verzeichnis konnte nicht angelegt werden: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := ompVerifyDirIsOutsideRoots(dir, roots); err != nil {
		return OmpVerifyResult{}, err
	}

	version, err := proc.Version(ctx)
	if err != nil {
		return OmpVerifyResult{
			Outcome: OmpVerifyOutcomeNotAttempted,
			Reason:  fmt.Sprintf("omp ist nicht installiert oder --version schlug fehl: %v", err),
		}, nil
	}

	argv := append(ompApprovalGateArgv(), "--no-session", "--cwd", dir)
	if err := proc.Start(ctx, argv, dir); err != nil {
		_ = proc.Close()
		return OmpVerifyResult{
			Outcome:    OmpVerifyOutcomeNotAttempted,
			OmpVersion: version,
			Reason:     fmt.Sprintf("omp konnte nicht gestartet werden: %v", err),
		}, nil
	}
	defer proc.Close()

	readyLine, err := proc.ReadLine(ctx)
	if err != nil {
		return OmpVerifyResult{
			Outcome:    OmpVerifyOutcomeNotAttempted,
			OmpVersion: version,
			Reason:     fmt.Sprintf("keine Ready-Antwort von omp innerhalb des Zeitlimits: %v", err),
		}, nil
	}
	if _, err := ParseOmpReady(readyLine); err != nil {
		return OmpVerifyResult{
			Outcome:    OmpVerifyOutcomeNotAttempted,
			OmpVersion: version,
			Reason:     fmt.Sprintf("Handshake von omp wurde abgelehnt: %v", err),
		}, nil
	}

	state := &ompVerifyState{openTools: map[string]string{}, approved: map[string]bool{}}
	if err := ompVerifyRunPrompt(ctx, proc, state, "verify-write",
		"Call the write tool exactly once to create verify.txt with content hi. Do not call any other tool. Do not read or list anything."); err != nil {
		return OmpVerifyResult{
			Outcome:    OmpVerifyOutcomeNotAttempted,
			OmpVersion: version,
			Reason:     fmt.Sprintf("omp konnte während der Prüfung nicht angesprochen werden: %v", err),
		}, nil
	}
	if state.loginReason == "" && state.failedTool == "" {
		if err := ompVerifyRunPrompt(ctx, proc, state, "verify-bash",
			"Run the bash command `echo omp-verify-ok` exactly once. Do not call any other tool."); err != nil {
			return OmpVerifyResult{
				Outcome:    OmpVerifyOutcomeNotAttempted,
				OmpVersion: version,
				Reason:     fmt.Sprintf("omp konnte während der Prüfung nicht angesprochen werden: %v", err),
			}, nil
		}
	}

	// A gated tool that ran unasked is the unsafe direction and outranks
	// every other obstacle seen in the same run.
	if state.failedTool != "" {
		return OmpVerifyResult{
			Outcome:    OmpVerifyOutcomeFailed,
			OmpVersion: version,
			Reason:     fmt.Sprintf("Tool %q lief zu Ende, ohne dass omp (%s) eine Genehmigung angefragt hätte", state.failedTool, version),
		}, nil
	}
	if state.loginReason != "" {
		return OmpVerifyResult{Outcome: OmpVerifyOutcomeNotAttempted, OmpVersion: version, Reason: state.loginReason}, nil
	}
	if state.sawWriteApproval && state.sawExecApproval {
		return OmpVerifyResult{
			Outcome:    OmpVerifyOutcomeVerified,
			OmpVersion: version,
			Reason:     "Schreib- und Ausführungs-Tool haben je eine Genehmigungsanfrage ausgelöst, die verweigert wurde",
		}, nil
	}
	return OmpVerifyResult{
		Outcome:    OmpVerifyOutcomeInconclusive,
		OmpVersion: version,
		Reason:     "innerhalb des Zeitlimits wurde kein gegatetes Tool aufgerufen",
	}, nil
}

// ompVerifyDirIsOutsideRoots refuses a verification directory that resolves
// inside any of roots, comparing EvalSymlinks-resolved paths so a symlinked
// temp dir cannot slip past the check.
func ompVerifyDirIsOutsideRoots(dir string, roots []string) error {
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("Verify-Verzeichnis %s konnte nicht aufgelöst werden: %w", dir, err)
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if resolvedDir == resolvedRoot || strings.HasPrefix(resolvedDir, resolvedRoot+string(filepath.Separator)) {
			return fmt.Errorf("Verify-Verzeichnis %s liegt innerhalb des Projekt- oder Worktree-Pfads %s; die Genehmigungs-Prüfung darf dort nicht laufen", dir, root)
		}
	}
	return nil
}

// ompVerifyRunPrompt sends one prompt and drives frames until the agent ends,
// the run is derailed by a login request, or a gated tool is found to have
// run unapproved. It never blocks past ctx's deadline.
func ompVerifyRunPrompt(ctx context.Context, proc OmpVerifyProcess, state *ompVerifyState, id, message string) error {
	cmd, err := EncodeOmpCommand(id, "prompt", map[string]any{"message": message})
	if err != nil {
		return err
	}
	if err := proc.WriteLine(ctx, cmd); err != nil {
		return err
	}
	for {
		if state.failedTool != "" || state.loginReason != "" {
			return nil
		}
		line, err := proc.ReadLine(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		ended, err := ompVerifyProcessFrame(ctx, proc, line, state)
		if err != nil {
			return err
		}
		if ended || state.failedTool != "" || state.loginReason != "" {
			return nil
		}
	}
}

// ompVerifyProcessFrame updates state from one decoded frame, answering any
// extension_ui_request it must answer, and reports whether the agent ended.
// A prompt spans several turns — one per tool round — so only agent_end
// means the next prompt will not arrive during a running turn.
func ompVerifyProcessFrame(ctx context.Context, proc OmpVerifyProcess, raw []byte, state *ompVerifyState) (agentEnded bool, err error) {
	var frame ompVerifyFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		// An unparsable line is not this verifier's business to fail on;
		// ParseOmpReady already gated the handshake itself.
		return false, nil
	}
	switch frame.Type {
	case "tool_execution_start":
		state.openTools[frame.ToolCallID] = frame.ToolName
	case "tool_execution_end":
		name := state.openTools[frame.ToolCallID]
		if name == "" {
			name = frame.ToolName
		}
		gated := ompVerifyWriteTierTools[name] || ompVerifyExecTierTools[name]
		if gated && !state.approved[frame.ToolCallID] {
			state.failedTool = name
		}
		delete(state.openTools, frame.ToolCallID)
		delete(state.approved, frame.ToolCallID)
	case "extension_ui_request":
		switch frame.Method {
		case "select":
			hasDeny := false
			for _, option := range frame.Options {
				if option == "Deny" {
					hasDeny = true
					break
				}
			}
			if !hasDeny {
				break
			}
			if callID, name, ok := ompVerifyAttributeRequest(state, frame.Title); ok {
				state.approved[callID] = true
				if ompVerifyWriteTierTools[name] {
					state.sawWriteApproval = true
				}
				if ompVerifyExecTierTools[name] {
					state.sawExecApproval = true
				}
			}
			resp, marshalErr := json.Marshal(map[string]any{"type": "extension_ui_response", "id": frame.ID, "value": "Deny"})
			if marshalErr != nil {
				return false, marshalErr
			}
			if writeErr := proc.WriteLine(ctx, resp); writeErr != nil {
				return false, writeErr
			}
		case "input", "editor":
			resp, marshalErr := json.Marshal(map[string]any{"type": "extension_ui_response", "id": frame.ID, "cancelled": true})
			if marshalErr != nil {
				return false, marshalErr
			}
			if writeErr := proc.WriteLine(ctx, resp); writeErr != nil {
				return false, writeErr
			}
			if frame.Method == "input" {
				state.loginReason = "omp fragte während der Prüfung nach einer Anmeldung (Eingabeaufforderung); das ist ein Login-Problem, kein Fehlschlag des Genehmigungs-Gates"
			}
		default:
			// setWidget, notify, … — no answer expected.
		}
	case "message_end":
		// A provider that cannot serve the turn — missing login, unreachable
		// endpoint — ends the assistant message with stopReason "error". That
		// blocks verification; it says nothing about the gate. What the model
		// writes as text is never read as an obstacle.
		if frame.Message != nil && frame.Message.Role == "assistant" && frame.Message.StopReason == "error" {
			state.loginReason = fmt.Sprintf("der Provider konnte die Prüfung nicht bedienen (%s); das ist kein Fehlschlag des Genehmigungs-Gates", frame.Message.ErrorMessage)
		}
	case "agent_end":
		return true, nil
	}
	return false, nil
}

// ompVerifyAttributeRequest finds the open tool call an approval request
// belongs to. omp titles the request "Allow tool: <name>\n…"; the request is
// attributed to an open call of that tool, or to the only open call when the
// title names none.
func ompVerifyAttributeRequest(state *ompVerifyState, title string) (string, string, bool) {
	firstLine, _, _ := strings.Cut(title, "\n")
	named := strings.TrimSpace(strings.TrimPrefix(firstLine, "Allow tool:"))
	for callID, name := range state.openTools {
		if name == named && !state.approved[callID] {
			return callID, name, true
		}
	}
	if len(state.openTools) == 1 {
		for callID, name := range state.openTools {
			return callID, name, true
		}
	}
	return "", "", false
}

// ompExecProcess is the real OmpVerifyProcess: omp from PATH, driven over
// its own stdin/stdout.
type ompExecProcess struct {
	binary string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

// newOmpExecProcess creates a process seam that runs binary ("omp" if
// empty).
func newOmpExecProcess(binary string) *ompExecProcess {
	if binary == "" {
		binary = "omp"
	}
	return &ompExecProcess{binary: binary}
}

func (p *ompExecProcess) Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, p.binary, "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (p *ompExecProcess) Start(ctx context.Context, argv []string, dir string) error {
	cmd := exec.CommandContext(ctx, p.binary, argv...)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd = cmd
	p.stdin = stdin
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	p.stdout = scanner
	return nil
}

func (p *ompExecProcess) ReadLine(ctx context.Context) ([]byte, error) {
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		if p.stdout.Scan() {
			ch <- result{line: append([]byte(nil), p.stdout.Bytes()...)}
			return
		}
		err := p.stdout.Err()
		if err == nil {
			err = io.EOF
		}
		ch <- result{err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.line, r.err
	}
}

func (p *ompExecProcess) WriteLine(ctx context.Context, line []byte) error {
	_, err := p.stdin.Write(append(line, '\n'))
	return err
}

func (p *ompExecProcess) Close() error {
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
	return nil
}
