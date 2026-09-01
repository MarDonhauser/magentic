package core

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
	// Matches recognizes the vendor from the pane command tmux reports. It
	// delegates to the vendor's detection manifest so recognition has one
	// source; the argument is already lowercased and stripped of a
	// login-shell dash.
	Matches(paneCommand string) bool
	// StartCommand builds the full command line for mode "new" or "resume".
	// run is this vendor's stored AgentRunRef, or nil when none exists.
	StartCommand(session Session, run *AgentRunRef, mode string) (string, error)
	// NewRunID returns a caller-supplied run identity when the vendor accepts
	// one, and "" when the identity can only be discovered afterwards.
	NewRunID() string
	// RunExists reports whether the vendor still holds the conversation behind
	// this run reference. A stored reference is only a promise: Magentic writes
	// it when it provisions a Session, while the vendor creates the
	// conversation only once work happens there.
	RunExists(externalID string) bool
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
		// The vendor's own storage decides which start form is valid. Claude
		// rejects --resume for a conversation it never created and --session-id
		// for one it already has; the other vendors are just as one-sided.
		if provider.RunExists(run.ExternalID) {
			mode = "resume"
		} else {
			mode = "new"
		}
	}
	return provider.StartCommand(session, runRef, mode)
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// vendorRunMatches walks a vendor's storage root for files that carry the run
// identity. An unreadable root yields no match, which keeps a fresh start the
// safe answer.
func vendorRunMatches(root, externalID string, matches func(name, id string) bool) []string {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(externalID) == "" {
		return nil
	}
	var found []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if matches(entry.Name(), externalID) {
			found = append(found, path)
		}
		return nil
	})
	return found
}

// paneCommandMatchesKind asks the vendor's detection manifest whether it
// recognizes this pane command. Recognition is a manifest fact, so a vendor
// that changes its process title needs a new marker, not a new build.
func paneCommandMatchesKind(kindID, paneCommand string) bool {
	kind, known := agentKindForID(kindID)
	return known && kind.matchesPaneCommand(normalizedAgentPaneCommand(paneCommand))
}

type claudeProvider struct{}

func (claudeProvider) Vendor() AgentVendor { return AgentVendorClaude }
func (claudeProvider) Tool() string        { return AgentToolClaude }
func (claudeProvider) Binary() string      { return "claude" }
func (claudeProvider) NewRunID() string    { return NewUUID() }

func (claudeProvider) Matches(paneCommand string) bool {
	return paneCommandMatchesKind("claude", paneCommand)
}

func (claudeProvider) RunExists(externalID string) bool {
	return len(vendorRunMatches(filepath.Join(userHomeDir(), ".claude", "projects"), externalID, func(name, id string) bool {
		return name == id+".jsonl"
	})) > 0
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

func (codexProvider) Matches(paneCommand string) bool {
	return paneCommandMatchesKind("codex", paneCommand)
}

func (codexProvider) RunExists(externalID string) bool {
	for _, root := range []string{"sessions", "archived_sessions"} {
		matches := vendorRunMatches(filepath.Join(codexHome(), root), externalID, func(name, id string) bool {
			return strings.HasPrefix(name, "rollout-") && strings.Contains(name, id) && strings.HasSuffix(name, ".jsonl")
		})
		if len(matches) > 0 {
			return true
		}
	}
	return false
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

func (copilotProvider) Matches(paneCommand string) bool {
	return paneCommandMatchesKind("copilot", paneCommand)
}

func (copilotProvider) RunExists(externalID string) bool {
	if strings.TrimSpace(externalID) == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(userHomeDir(), ".copilot", "session-state", externalID))
	return err == nil && info.IsDir()
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

func (geminiProvider) Matches(paneCommand string) bool {
	return paneCommandMatchesKind("gemini", paneCommand)
}

// Gemini CLI's storage layout was never verified, so no run can be proven to
// exist and every start is a fresh one.
func (geminiProvider) RunExists(string) bool { return false }

// Gemini CLI has no verified resume form. Starting fresh is the conservative
// contract; the run identity is discovered from ~/.gemini/tmp afterwards.
func (geminiProvider) StartCommand(Session, *AgentRunRef, string) (string, error) {
	return "gemini", nil
}
