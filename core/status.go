package core

import (
	"fmt"
	"strings"
)

type AgentStatus int

const (
	StatusUnknown AgentStatus = iota
	StatusRunning
	StatusAgents
	StatusShell
	StatusBlocked
	StatusIdle
	StatusExited
	StatusDead
	StatusTerm
	// StatusDone is appended rather than inserted: AgentStatus is serialized by
	// position, and an inserted member would silently renumber every Session
	// the desktop app already holds.
	StatusDone
)

func (s AgentStatus) Label() string {
	switch s {
	case StatusRunning:
		return "läuft"
	case StatusAgents:
		return "Agents"
	case StatusShell:
		return "Shell läuft"
	case StatusBlocked:
		return "wartet"
	case StatusDone:
		return "fertig"
	case StatusIdle:
		return "idle"
	case StatusExited:
		return "beendet"
	case StatusDead:
		return "tot"
	case StatusTerm:
		return "Terminal"
	}
	return "?"
}

func (s AgentStatus) Icon() string {
	switch s {
	case StatusRunning:
		return "●"
	case StatusAgents:
		return "◍"
	case StatusShell:
		return "⚙"
	case StatusBlocked:
		return "◆"
	case StatusDone:
		return "✓"
	case StatusIdle:
		return "○"
	case StatusExited:
		return "▪"
	case StatusDead:
		return "✗"
	case StatusTerm:
		return "⌨"
	}
	return "?"
}

var shellCommands = map[string]bool{
	"zsh": true, "bash": true, "fish": true, "sh": true,
	"-zsh": true, "-bash": true, "login": true,
}

const (
	AgentToolClaude  = "claude"
	AgentToolCodex   = "codex"
	AgentToolGemini  = "gemini"
	AgentToolCopilot = "copilot"
	AgentToolBash    = "bash"
)

// DetectAgentTool translates the command tmux reports for a pane into the
// stable frontend identity used by developer-icons. Unknown commands stay
// neutral instead of being mislabeled as Claude.
func DetectAgentTool(paneCommand string, term bool) string {
	if term {
		return AgentToolBash
	}
	if kind, ok := agentKindForPaneCommand(paneCommand); ok {
		return kind.tool
	}
	return ""
}

// observationSessions returns copy-only ephemeral IDs for legacy fixtures.
// Observe itself remains strict: production Sessions must have durable IDs.
func observationSessions(sessions []Session) []Session {
	prepared := append([]Session(nil), sessions...)
	used := make(map[SessionID]bool, len(prepared))
	for _, session := range prepared {
		if session.ID != "" {
			used[session.ID] = true
		}
	}
	for i := range prepared {
		if prepared[i].ID != "" {
			continue
		}
		for suffix := 0; ; suffix++ {
			candidate := SessionID(fmt.Sprintf("__observation_fixture_%d_%d", i, suffix))
			if !used[candidate] {
				prepared[i].ID = candidate
				used[candidate] = true
				break
			}
		}
	}
	return prepared
}

func LastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func DetectTermStatus(sessionExists bool) AgentStatus {
	if !sessionExists {
		return StatusDead
	}
	return StatusTerm
}
