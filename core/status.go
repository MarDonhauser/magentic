package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	case StatusAgents:
		return "◍"
	case StatusShell:
		return "⚙"
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

var bgAgentsRe = regexp.MustCompile(`(?i)waiting for (\d+) background agent`)

var agentTreeRe = regexp.MustCompile(`(?m)^\s*[◯○◌]\s+\S+`)

func BackgroundAgentCount(content string) int {
	if n := len(agentTreeRe.FindAllString(content, -1)); n > 0 {
		return n
	}
	ms := bgAgentsRe.FindAllStringSubmatch(content, -1)
	if len(ms) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(ms[len(ms)-1][1])
	return n
}

func AgentsDetail(n int) string {
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return "wartet auf 1 Agent"
	}
	return fmt.Sprintf("wartet auf %d Agents", n)
}

var bgShellRe = regexp.MustCompile(`(?i)(\d+)\s+shells?\s+still\s+running`)
var bgShellBarRe = regexp.MustCompile(`(?im)·\s+(\d+)\s+shells?\s*$`)

func BackgroundShellCount(content string) int {
	if m := bgShellRe.FindStringSubmatch(content); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if m := bgShellBarRe.FindStringSubmatch(content); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func ShellDetail(n int) string {
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return "1 Shell läuft"
	}
	return fmt.Sprintf("%d Shells laufen", n)
}

var spinnerRe = regexp.MustCompile(`(?m)^\s*[·✢✳✶✻✽✺✹✸✷+*]\s+[^\n…]{1,80}…`)

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

var permissionDetails = []struct {
	label    string
	patterns []string
}{
	{"Ordner-Freigabe", []string{"trust this folder", "do you trust the files"}},
	{"Datei-Freigabe", []string{"make this edit", "edit this file", "edit file", "create this file", "create file", "write this file", "write file", "apply this change"}},
	{"Shell-Freigabe", []string{"bash command", "shell command", "run this command", "run the following command", "execute this command"}},
}

func BlockedDetail(content string) string {
	lc := strings.ToLower(content)
	for _, d := range permissionDetails {
		for _, p := range d.patterns {
			if strings.Contains(lc, p) {
				return d.label
			}
		}
	}
	if strings.Contains(lc, "don't ask again") || strings.Contains(lc, "dont ask again") {
		return "Freigabe"
	}
	return ""
}

func LastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func CollectStatuses(agents []Agent) (map[string]AgentStatus, map[string]string, map[string]time.Time) {
	infos := TmuxPaneInfos()
	statuses := map[string]AgentStatus{}
	contents := map[string]string{}
	activity := map[string]time.Time{}
	for _, a := range agents {
		sn := SessionName(a.Name)
		info, exists := infos[sn]
		var content string
		if exists {
			content = TmuxCapturePane(sn, 0)
			if !info.Activity.IsZero() {
				activity[a.Name] = info.Activity
			}
		}
		contents[a.Name] = content
		statuses[a.Name] = DetectClaudeStatus(exists, info.Command, LastLines(content, 25))
	}
	return statuses, contents, activity
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
	if bgAgentsRe.MatchString(paneContent) || agentTreeRe.MatchString(paneContent) {
		return StatusAgents
	}
	if BackgroundShellCount(paneContent) > 0 {
		return StatusShell
	}
	return StatusIdle
}
