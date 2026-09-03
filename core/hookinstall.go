package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// claudeHookMarker identifies the hook definitions Magentic wrote. Everything
// else in the developer's settings is left exactly as it was.
const claudeHookMarker = "magentic hook-report"

// ClaudeHookDefinition is one shipped hook: the Claude Code event and the
// command that turns it into a status report.
type ClaudeHookDefinition struct {
	Event   string `json:"event"`
	Command string `json:"command"`
}

// ClaudeHookDefinitions is what installation writes. The mapping is the whole
// vendor-specific part of the hook channel: a second vendor needs its own
// definitions, not a second transport.
func ClaudeHookDefinitions() []ClaudeHookDefinition {
	events := []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "SessionEnd"}
	definitions := make([]ClaudeHookDefinition, 0, len(events))
	for _, event := range events {
		definitions = append(definitions, ClaudeHookDefinition{
			Event: event, Command: claudeHookMarker + " --event " + event,
		})
	}
	return definitions
}

// ClaudeStatusLineDefinition is the statusLine entry. Claude Code runs it on
// every change with model, effort and context facts on stdin, draws what it
// prints under its prompt, and Magentic keeps a copy for the overview.
func ClaudeStatusLineDefinition() ClaudeHookDefinition {
	return ClaudeHookDefinition{Event: ClaudeStatusEvent, Command: claudeHookMarker + " --event " + ClaudeStatusEvent}
}

// hookStateForClaudeEvent maps one Claude Code lifecycle event onto the
// vendor-neutral report vocabulary. PostToolUse only refreshes the freshness
// window: a long tool call is still the same turn.
func hookStateForClaudeEvent(event string) (HookReportState, bool) {
	switch event {
	case "UserPromptSubmit", "PreToolUse":
		return HookStateWorking, true
	case "Notification":
		return HookStateBlocked, true
	case "Stop":
		return HookStateDone, true
	case "SessionEnd":
		return HookStateIdle, true
	case "PostToolUse":
		return HookStateRefresh, true
	}
	return "", false
}

// HookReportFromClaudePayload translates one hook invocation into a report.
// Claude Code hands the payload on stdin; the runtime name comes from the pane
// the agent runs in, because Claude does not echo the name Magentic started it
// under.
func HookReportFromClaudePayload(event string, payload []byte, runtimeName string, now time.Time) (HookReport, error) {
	state, known := hookStateForClaudeEvent(event)
	if !known {
		return HookReport{}, fmt.Errorf("Hook-Ereignis %q ist nicht abgebildet", event)
	}
	var body struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
		Event     string `json:"hook_event_name"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &body); err != nil {
			return HookReport{}, fmt.Errorf("Hook-Nutzlast ist kein gültiges JSON: %v", err)
		}
	}
	report := HookReport{
		State:       state,
		At:          now.UTC(),
		Vendor:      AgentVendorClaude,
		RuntimeName: runtimeName,
		RunRef:      strings.TrimSpace(body.SessionID),
		UID:         os.Getuid(),
	}
	if state == HookStateBlocked {
		report.Detail = strings.TrimSpace(body.Message)
	}
	return report, nil
}

// ClaudeSettingsPath is the settings file the hook definitions are written to.
func ClaudeSettingsPath() string {
	if path := os.Getenv("MAGENTIC_CLAUDE_SETTINGS"); path != "" {
		return path
	}
	return filepath.Join(userHomeDir(), ".claude", "settings.json")
}

// HookRuntimeName resolves the tmux Session the hook is running inside. It is
// the RuntimeName Magentic addresses the Session by; without it the report
// cannot be correlated and is dropped.
func HookRuntimeName() string {
	if name := strings.TrimSpace(os.Getenv("MAGENTIC_RUNTIME")); name != "" {
		return name
	}
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// InstallClaudeHooks adds Magentic's hook definitions to the settings file and
// leaves every other definition untouched. Running it twice changes nothing.
func InstallClaudeHooks(path string) ([]ClaudeHookDefinition, error) {
	settings, err := readClaudeSettings(path)
	if err != nil {
		return nil, err
	}
	hooks := claudeHookSection(settings)
	var written []ClaudeHookDefinition
	for _, definition := range ClaudeHookDefinitions() {
		groups, _ := hooks[definition.Event].([]any)
		if claudeHookGroupsContain(groups, definition.Command) {
			continue
		}
		hooks[definition.Event] = append(groups, map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": definition.Command}},
		})
		written = append(written, definition)
	}
	settings["hooks"] = hooks
	// A statusLine the developer configured stays: it is their line, and
	// replacing it silently would take their status line away.
	if claudeStatusLineCommand(settings) == "" {
		definition := ClaudeStatusLineDefinition()
		settings["statusLine"] = map[string]any{"type": "command", "command": definition.Command}
		written = append(written, definition)
	}
	if len(written) == 0 {
		return nil, nil
	}
	return written, writeClaudeSettings(path, settings)
}

// ClaudeStatusLineCommand reports which statusLine command the settings file
// names and whether Magentic wrote it.
func ClaudeStatusLineCommand(path string) (command string, ours bool, err error) {
	settings, err := readClaudeSettings(path)
	if err != nil {
		return "", false, err
	}
	command = claudeStatusLineCommand(settings)
	return command, strings.Contains(command, claudeHookMarker), nil
}

func claudeStatusLineCommand(settings map[string]any) string {
	entry, ok := settings["statusLine"].(map[string]any)
	if !ok {
		return ""
	}
	command, _ := entry["command"].(string)
	return strings.TrimSpace(command)
}

// UninstallClaudeHooks removes only the definitions Magentic wrote. Affected
// Sessions fall back to snapshot-inferred status.
func UninstallClaudeHooks(path string) ([]ClaudeHookDefinition, error) {
	settings, err := readClaudeSettings(path)
	if err != nil {
		return nil, err
	}
	hooks := claudeHookSection(settings)
	var removed []ClaudeHookDefinition
	for event, value := range hooks {
		groups, ok := value.([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(groups))
		for _, group := range groups {
			if commands := claudeHookGroupCommands(group); len(commands) > 0 &&
				claudeHookCommandsAreOurs(commands) {
				removed = append(removed, ClaudeHookDefinition{Event: event, Command: commands[0]})
				continue
			}
			kept = append(kept, group)
		}
		if len(kept) == 0 {
			delete(hooks, event)
			continue
		}
		hooks[event] = kept
	}
	if command := claudeStatusLineCommand(settings); command != "" && strings.Contains(command, claudeHookMarker) {
		delete(settings, "statusLine")
		removed = append(removed, ClaudeHookDefinition{Event: ClaudeStatusEvent, Command: command})
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	return removed, writeClaudeSettings(path, settings)
}

func claudeHookSection(settings map[string]any) map[string]any {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return hooks
}

func claudeHookGroupCommands(group any) []string {
	entry, ok := group.(map[string]any)
	if !ok {
		return nil
	}
	inner, ok := entry["hooks"].([]any)
	if !ok {
		return nil
	}
	var commands []string
	for _, item := range inner {
		hook, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if command, ok := hook["command"].(string); ok {
			commands = append(commands, command)
		}
	}
	return commands
}

func claudeHookCommandsAreOurs(commands []string) bool {
	for _, command := range commands {
		if !strings.Contains(command, claudeHookMarker) {
			return false
		}
	}
	return true
}

func claudeHookGroupsContain(groups []any, command string) bool {
	for _, group := range groups {
		for _, existing := range claudeHookGroupCommands(group) {
			if existing == command {
				return true
			}
		}
	}
	return false
}

func readClaudeSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("%s ist kein gültiges JSON: %v", path, err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, nil
}

func writeClaudeSettings(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
