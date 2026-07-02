package main

import (
	"regexp"
	"strings"
)

type AgentStatus int

const (
	StatusUnknown AgentStatus = iota
	StatusRunning
	StatusBlocked
	StatusIdle
	StatusExited
	StatusDead
)

func (s AgentStatus) Label() string {
	switch s {
	case StatusRunning:
		return "läuft"
	case StatusBlocked:
		return "wartet"
	case StatusIdle:
		return "idle"
	case StatusExited:
		return "beendet"
	case StatusDead:
		return "tot"
	}
	return "?"
}

func (s AgentStatus) Icon() string {
	switch s {
	case StatusRunning:
		return "●"
	case StatusBlocked:
		return "◆"
	case StatusIdle:
		return "○"
	case StatusExited:
		return "▪"
	case StatusDead:
		return "✗"
	}
	return "?"
}

var spinnerRe = regexp.MustCompile(`(?m)^\s*[·✢✳✶✻✽✺✹✸✷+*]\s+\p{L}+…`)

var runningPatterns = []string{
	"esc to interrupt",
	"ctrl+b to run in background",
	"· thinking with",
}

var blockedPatterns = []string{
	"do you want",
	"would you like",
	"❯ 1.",
	"(y/n)",
	"[y/n]",
	"enter to confirm",
	"waiting for your",
	"press enter to",
	"trust this folder",
}

var shellCommands = map[string]bool{
	"zsh": true, "bash": true, "fish": true, "sh": true,
	"-zsh": true, "-bash": true, "login": true,
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func CollectStatuses(agents []Agent) (map[string]AgentStatus, map[string]string) {
	cmds := TmuxPaneCommands()
	statuses := map[string]AgentStatus{}
	contents := map[string]string{}
	for _, a := range agents {
		sn := tmuxSessionName(a.Name)
		cmd, exists := cmds[sn]
		var content string
		if exists {
			content = TmuxCapturePane(sn, 0)
		}
		contents[a.Name] = content
		statuses[a.Name] = DetectClaudeStatus(exists, cmd, lastLines(content, 25))
	}
	return statuses, contents
}

func DetectClaudeStatus(sessionExists bool, paneCommand, paneContent string) AgentStatus {
	if !sessionExists {
		return StatusDead
	}
	if shellCommands[paneCommand] {
		return StatusExited
	}
	if spinnerRe.MatchString(paneContent) {
		return StatusRunning
	}
	content := strings.ToLower(paneContent)
	for _, p := range runningPatterns {
		if strings.Contains(content, p) {
			return StatusRunning
		}
	}
	for _, p := range blockedPatterns {
		if strings.Contains(content, p) {
			return StatusBlocked
		}
	}
	return StatusIdle
}
