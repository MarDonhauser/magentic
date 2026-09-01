package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeHookPayloadsBecomeReports(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		event      string
		payload    string
		wantState  HookReportState
		wantDetail string
	}{
		{"UserPromptSubmit", `{"session_id":"run-a","hook_event_name":"UserPromptSubmit"}`, HookStateWorking, ""},
		{"PreToolUse", `{"session_id":"run-a","hook_event_name":"PreToolUse"}`, HookStateWorking, ""},
		{"PostToolUse", `{"session_id":"run-a","hook_event_name":"PostToolUse"}`, HookStateRefresh, ""},
		{
			"Notification",
			`{"session_id":"run-a","hook_event_name":"Notification","message":"Claude needs your permission to use Bash"}`,
			HookStateBlocked, "Claude needs your permission to use Bash",
		},
		{"Stop", `{"session_id":"run-a","hook_event_name":"Stop"}`, HookStateDone, ""},
		{"SessionEnd", `{"session_id":"run-a","hook_event_name":"SessionEnd"}`, HookStateIdle, ""},
	}
	for _, test := range tests {
		t.Run(test.event, func(t *testing.T) {
			report, err := HookReportFromClaudePayload(test.event, []byte(test.payload), "mgt-eins", now)
			if err != nil {
				t.Fatalf("Nutzlast abgelehnt: %v", err)
			}
			if report.State != test.wantState || report.Detail != test.wantDetail {
				t.Fatalf("Meldung = %q / %q, want %q / %q",
					report.State, report.Detail, test.wantState, test.wantDetail)
			}
			if report.Vendor != AgentVendorClaude || report.RuntimeName != "mgt-eins" || report.RunRef != "run-a" {
				t.Fatalf("Adressierung falsch: %#v", report)
			}
		})
	}
	if _, err := HookReportFromClaudePayload("SubagentStop", []byte(`{}`), "mgt-eins", now); err == nil {
		t.Fatal("ein nicht abgebildetes Ereignis wurde übersetzt")
	}
}

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Einstellungen lesen: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("Einstellungen sind kein JSON: %v", err)
	}
	return settings
}

const existingClaudeSettings = `{
  "model": "opus",
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "mein-eigener-hook"}]}
    ]
  }
}`

func TestClaudeHookInstallIsIdempotentAndPreservesForeignHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(existingClaudeSettings), 0o600); err != nil {
		t.Fatalf("Einstellungen schreiben: %v", err)
	}

	written, err := InstallClaudeHooks(path)
	if err != nil {
		t.Fatalf("Installation: %v", err)
	}
	if len(written) != len(ClaudeHookDefinitions()) {
		t.Fatalf("%d Definitionen geschrieben, want %d", len(written), len(ClaudeHookDefinitions()))
	}
	settings := readSettings(t, path)
	if settings["model"] != "opus" {
		t.Fatal("fremde Einstellungen wurden verworfen")
	}
	hooks := settings["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop hat %d Gruppen, want den eigenen Hook plus unseren", len(stop))
	}
	if claudeHookGroupCommands(stop[0])[0] != "mein-eigener-hook" {
		t.Fatalf("der eigene Hook wurde verschoben: %#v", stop[0])
	}

	// Ein zweiter Lauf ändert nichts.
	before := readSettings(t, path)
	again, err := InstallClaudeHooks(path)
	if err != nil {
		t.Fatalf("zweite Installation: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("die zweite Installation schrieb %d Definitionen", len(again))
	}
	if !jsonEqual(before, readSettings(t, path)) {
		t.Fatal("die zweite Installation hat die Datei verändert")
	}

	// Das Entfernen führt zur Ausgangslage zurück.
	removed, err := UninstallClaudeHooks(path)
	if err != nil {
		t.Fatalf("Entfernen: %v", err)
	}
	if len(removed) != len(ClaudeHookDefinitions()) {
		t.Fatalf("%d Definitionen entfernt, want %d", len(removed), len(ClaudeHookDefinitions()))
	}
	var original map[string]any
	if err := json.Unmarshal([]byte(existingClaudeSettings), &original); err != nil {
		t.Fatalf("Ausgangslage lesen: %v", err)
	}
	if !jsonEqual(original, readSettings(t, path)) {
		t.Fatalf("das Entfernen hat mehr angefasst als unsere Hooks: %#v", readSettings(t, path))
	}
}

func TestClaudeHookInstallCreatesMissingSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude", "settings.json")
	if _, err := InstallClaudeHooks(path); err != nil {
		t.Fatalf("Installation: %v", err)
	}
	hooks := readSettings(t, path)["hooks"].(map[string]any)
	for _, definition := range ClaudeHookDefinitions() {
		groups, ok := hooks[definition.Event].([]any)
		if !ok || !claudeHookGroupsContain(groups, definition.Command) {
			t.Fatalf("Hook für %q fehlt", definition.Event)
		}
	}
}

func jsonEqual(a, b map[string]any) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && string(left) == string(right)
}
