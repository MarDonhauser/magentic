package core

import (
	"fmt"
	"os/exec"
	"strings"
)

// AgentProvider is the Adapter by which one coding-agent vendor is addressed
// as a process. It owns the vendor's command line and nothing else: status
// meaning and prompt semantics stay with Observation and Outbox.
type AgentProvider interface {
	Vendor() AgentVendor
	// Tool is the stable frontend identity already used by Observation and by
	// the developer-icon resolution in the frontend.
	Tool() string
	// Binary must be resolvable on PATH before a Session may start.
	Binary() string
	// Matches recognizes the vendor from the pane command tmux reports. The
	// argument is already lowercased and stripped of a login-shell dash.
	Matches(paneCommand string) bool
	// StartCommand builds the full command line for mode "new" or "resume".
	// run is this vendor's stored AgentRunRef, or nil when none exists.
	StartCommand(session Session, run *AgentRunRef, mode string) (string, error)
	// NewRunID returns a caller-supplied run identity when the vendor accepts
	// one, and "" when the identity can only be discovered afterwards.
	NewRunID() string
	// Status derives the vendor's activity from the tail of its pane. A screen
	// the vendor's Adapter does not recognize stays unknown: reporting an
	// unfamiliar UI as idle would let Magentic push prompts into it.
	Status(paneContent string) AgentStatus
	// ComposerReady reports whether the vendor's own input line is visible, so
	// a queued prompt typed now would land in it instead of in a dialog.
	ComposerReady(paneContent string) bool
	// ScreensRecorded reports whether this vendor's UI was ever observed. For a
	// vendor that was not, an unknown status means "never looked at" and the
	// literal queued-input path stays open; for one that was, unknown means
	// "unfamiliar screen" and nothing may be typed into it.
	ScreensRecorded() bool
}

// vendorStatus applies one vendor's markers in the only order that is safe:
// activity first, then a question that blocks, then the vendor's own resting
// screen. Nothing matched means nothing is known.
func vendorStatus(paneContent string, running, blocked, idle []string) AgentStatus {
	switch {
	case containsAnyMarker(paneContent, running):
		return StatusRunning
	case containsAnyMarker(paneContent, blocked):
		return StatusBlocked
	case containsAnyMarker(paneContent, idle):
		return StatusIdle
	}
	return StatusUnknown
}

func containsAnyMarker(paneContent string, markers []string) bool {
	content := strings.ToLower(paneContent)
	for _, marker := range markers {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

func builtinAgentProviders() []AgentProvider {
	return []AgentProvider{claudeProvider{}, codexProvider{}, geminiProvider{}, copilotProvider{}}
}

func providerForVendor(vendor AgentVendor) (AgentProvider, bool) {
	for _, provider := range builtinAgentProviders() {
		if provider.Vendor() == vendor {
			return provider, true
		}
	}
	return nil, false
}

func providerForPaneCommand(paneCommand string) (AgentProvider, bool) {
	command := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(paneCommand)), "-")
	if command == "" {
		return nil, false
	}
	for _, provider := range builtinAgentProviders() {
		if provider.Matches(command) {
			return provider, true
		}
	}
	return nil, false
}

// providerBinaryAvailable reports whether the vendor's executable is on PATH.
// A missing binary is a fail-closed condition: no Session may be started for
// it, and the UI offers it only as unavailable.
func providerBinaryAvailable(provider AgentProvider) bool {
	_, err := exec.LookPath(provider.Binary())
	return err == nil
}

// resolveSessionProvider is the single place that turns a Session into the
// Adapter that starts it. A terminal Session has no coding agent and is a
// caller error here, not a silent no-op.
func resolveSessionProvider(session Session) (AgentProvider, error) {
	vendor := session.SessionVendor()
	if vendor == "" {
		return nil, fmt.Errorf("Session %q hostet keinen Coding-Agenten", session.Name)
	}
	provider, known := providerForVendor(vendor)
	if !known {
		return nil, fmt.Errorf("Session %q hat einen unbekannten Agent-Vendor %q", session.Name, vendor)
	}
	return provider, nil
}

// startCommandForSession resolves the Session's own AgentRunRef and asks its
// provider for the command line.
func startCommandForSession(session Session, mode string) (string, error) {
	provider, err := resolveSessionProvider(session)
	if err != nil {
		return "", err
	}
	var runRef *AgentRunRef
	if run, ok := session.AgentRun(provider.Vendor()); ok {
		runRef = &run
	}
	return provider.StartCommand(session, runRef, mode)
}

func paneCommandMatches(command, binary string) bool {
	return command == binary || strings.HasPrefix(command, binary+"-")
}

type claudeProvider struct{}

func (claudeProvider) Vendor() AgentVendor { return AgentVendorClaude }
func (claudeProvider) Tool() string        { return AgentToolClaude }
func (claudeProvider) Binary() string      { return "claude" }
func (claudeProvider) NewRunID() string    { return NewUUID() }

func (claudeProvider) ScreensRecorded() bool { return true }

func (claudeProvider) Matches(paneCommand string) bool {
	// Claude Code has set its process title to the bare version number since
	// about 2.1.237, so tmux reports "2.1.241" instead of "claude".
	return paneCommandMatches(paneCommand, "claude") || looksLikeBareVersionNumber(paneCommand)
}

// Claude keeps its long-standing detection, including background agents and
// background shells, which no other vendor reports.
func (claudeProvider) Status(paneContent string) AgentStatus {
	return DetectClaudeStatus(true, "", paneContent)
}

func (claudeProvider) ComposerReady(paneContent string) bool {
	return strings.Contains(strings.ToLower(paneContent), "shift+tab to cycle")
}

func (claudeProvider) StartCommand(session Session, run *AgentRunRef, mode string) (string, error) {
	command := "claude --name " + ShellQuote(session.TmuxName())
	if run != nil && run.ExternalID != "" {
		flag := "--resume"
		if mode == "new" {
			flag = "--session-id"
		}
		return command + " " + flag + " " + ShellQuote(run.ExternalID), nil
	}
	if mode != "new" {
		command += " --continue"
	}
	return command, nil
}

type codexProvider struct{}

func (codexProvider) Vendor() AgentVendor { return AgentVendorCodex }
func (codexProvider) Tool() string        { return AgentToolCodex }
func (codexProvider) Binary() string      { return "codex" }

// Codex assigns its own session id, so the run identity can only be
// discovered from its rollout files after the fact.
func (codexProvider) NewRunID() string { return "" }

func (codexProvider) ScreensRecorded() bool { return true }

func (codexProvider) Matches(paneCommand string) bool {
	return paneCommandMatches(paneCommand, "codex")
}

// Markers recorded from Codex 0.151.0 on 2026-09-01: it spins as
// "• Working (12s • esc to interrupt)", asks with a numbered "› 1." list, and
// rests behind its composer placeholder.
var (
	codexRunningMarkers = []string{"esc to interrupt"}
	codexBlockedMarkers = []string{"› 1.", "❯ 1.", "press enter to continue", "do you trust the contents"}
	codexIdleMarkers    = []string{"ask codex to do anything"}
)

func (codexProvider) Status(paneContent string) AgentStatus {
	return vendorStatus(paneContent, codexRunningMarkers, codexBlockedMarkers, codexIdleMarkers)
}

func (codexProvider) ComposerReady(paneContent string) bool {
	return containsAnyMarker(paneContent, codexIdleMarkers)
}

func (codexProvider) StartCommand(_ Session, run *AgentRunRef, mode string) (string, error) {
	if mode == "new" {
		return "codex", nil
	}
	if run != nil && run.ExternalID != "" {
		return "codex resume " + ShellQuote(run.ExternalID), nil
	}
	return "codex resume --last", nil
}

type copilotProvider struct{}

func (copilotProvider) Vendor() AgentVendor { return AgentVendorCopilot }
func (copilotProvider) Tool() string        { return AgentToolCopilot }
func (copilotProvider) Binary() string      { return "copilot" }
func (copilotProvider) NewRunID() string    { return NewUUID() }

func (copilotProvider) ScreensRecorded() bool { return true }

func (copilotProvider) Matches(paneCommand string) bool {
	return paneCommandMatches(paneCommand, "copilot") || paneCommand == "github-copilot"
}

// Markers recorded from GitHub Copilot 1.0.82 on 2026-09-01. Its spinner line
// reads "● Working esc interrupt" without the "to" that Claude and Codex use,
// so the shared marker would miss it.
var (
	copilotRunningMarkers = []string{"working esc interrupt"}
	copilotBlockedMarkers = []string{"❯ 1.", "do you trust the files", "enter to select"}
	copilotIdleMarkers    = []string{"open sidebar"}
)

func (copilotProvider) Status(paneContent string) AgentStatus {
	return vendorStatus(paneContent, copilotRunningMarkers, copilotBlockedMarkers, copilotIdleMarkers)
}

func (copilotProvider) ComposerReady(paneContent string) bool {
	return containsAnyMarker(paneContent, copilotIdleMarkers)
}

func (copilotProvider) StartCommand(session Session, run *AgentRunRef, mode string) (string, error) {
	command := "copilot --name " + ShellQuote(session.TmuxName())
	if run != nil && run.ExternalID != "" {
		// Both flags accept the value only in "=" form without ambiguity:
		// --resume takes an optional value and would otherwise swallow the
		// next positional argument.
		flag := "--resume="
		if mode == "new" {
			flag = "--session-id="
		}
		return command + " " + flag + ShellQuote(run.ExternalID), nil
	}
	if mode != "new" {
		command += " --continue"
	}
	return command, nil
}

type geminiProvider struct{}

func (geminiProvider) Vendor() AgentVendor { return AgentVendorGemini }
func (geminiProvider) Tool() string        { return AgentToolGemini }
func (geminiProvider) Binary() string      { return "gemini" }
func (geminiProvider) NewRunID() string    { return "" }

func (geminiProvider) ScreensRecorded() bool { return false }

func (geminiProvider) Matches(paneCommand string) bool {
	return paneCommandMatches(paneCommand, "gemini")
}

// Gemini CLI could not be observed: it is not installed on the development
// machine. Its screens therefore stay unknown until someone records them.
func (geminiProvider) Status(string) AgentStatus { return StatusUnknown }

func (geminiProvider) ComposerReady(string) bool { return false }

// Gemini CLI has no verified resume form. Starting fresh is the conservative
// contract; the run identity is discovered from ~/.gemini/tmp afterwards.
func (geminiProvider) StartCommand(Session, *AgentRunRef, string) (string, error) {
	return "gemini", nil
}
