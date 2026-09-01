package core

import (
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
}

func builtinAgentProviders() []AgentProvider {
	return []AgentProvider{claudeProvider{}}
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

func paneCommandMatches(command, binary string) bool {
	return command == binary || strings.HasPrefix(command, binary+"-")
}

type claudeProvider struct{}

func (claudeProvider) Vendor() AgentVendor { return AgentVendorClaude }
func (claudeProvider) Tool() string        { return AgentToolClaude }
func (claudeProvider) Binary() string      { return "claude" }
func (claudeProvider) NewRunID() string    { return NewUUID() }

func (claudeProvider) Matches(paneCommand string) bool {
	return paneCommandMatches(paneCommand, "claude")
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
