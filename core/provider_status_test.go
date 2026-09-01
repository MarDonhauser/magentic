package core

import (
	"os"
	"path/filepath"
	"testing"
)

func paneFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "panes", name))
	if err != nil {
		t.Fatalf("Pane-Mitschnitt %q: %v", name, err)
	}
	return string(data)
}

// Die Mitschnitte stammen aus echten tmux-Panes von Codex 0.151.0 und
// GitHub Copilot 1.0.82, aufgenommen am 2026-09-01.
func TestVendorStatusFromRecordedPanes(t *testing.T) {
	tests := []struct {
		fixture string
		vendor  AgentVendor
		want    AgentStatus
	}{
		{fixture: "codex-idle.txt", vendor: AgentVendorCodex, want: StatusIdle},
		{fixture: "codex-idle-after.txt", vendor: AgentVendorCodex, want: StatusIdle},
		{fixture: "codex-running.txt", vendor: AgentVendorCodex, want: StatusRunning},
		{fixture: "codex-blocked-trust.txt", vendor: AgentVendorCodex, want: StatusBlocked},
		{fixture: "copilot-idle.txt", vendor: AgentVendorCopilot, want: StatusIdle},
		{fixture: "copilot-idle-after.txt", vendor: AgentVendorCopilot, want: StatusIdle},
		{fixture: "copilot-running.txt", vendor: AgentVendorCopilot, want: StatusRunning},
		{fixture: "copilot-blocked-trust.txt", vendor: AgentVendorCopilot, want: StatusBlocked},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			provider, ok := providerForVendor(tt.vendor)
			if !ok {
				t.Fatalf("kein Provider für %q", tt.vendor)
			}
			content := LastLines(paneFixture(t, tt.fixture), 25)
			if got := provider.Status(content); got != tt.want {
				t.Fatalf("Status = %v, want %v", got.Label(), tt.want.Label())
			}
		})
	}
}

// Ein fremder Bildschirm darf nie als idle durchgehen: Magentic würde ihn
// sonst als arbeitsbereit melden und Nachrichten hineinschicken.
func TestUnfamiliarPaneStaysUnknown(t *testing.T) {
	for _, vendor := range []AgentVendor{AgentVendorCodex, AgentVendorCopilot, AgentVendorGemini} {
		provider, _ := providerForVendor(vendor)
		if got := provider.Status("irgendein fremder Bildschirm ohne bekannte Merkmale"); got != StatusUnknown {
			t.Fatalf("%q: Status = %v, want unbekannt", vendor, got.Label())
		}
	}
}

func TestGeminiStatusStaysUnknown(t *testing.T) {
	provider, _ := providerForVendor(AgentVendorGemini)
	if got := provider.Status("› Type your message"); got != StatusUnknown {
		t.Fatalf("Gemini-Status = %v, want unbekannt", got.Label())
	}
}

func TestObservationUsesProviderStatus(t *testing.T) {
	content := paneFixture(t, "codex-running.txt")
	if got := statusForAgentRuntime(true, AgentToolCodex, "codex", content); got != StatusRunning {
		t.Fatalf("statusForAgentRuntime = %v, want läuft", got.Label())
	}
	idle := paneFixture(t, "copilot-idle.txt")
	if got := statusForAgentRuntime(true, AgentToolCopilot, "copilot", idle); got != StatusIdle {
		t.Fatalf("statusForAgentRuntime = %v, want idle", got.Label())
	}
}

func TestComposerReadyFromRecordedPanes(t *testing.T) {
	tests := []struct {
		fixture string
		vendor  AgentVendor
		want    bool
	}{
		{fixture: "codex-idle.txt", vendor: AgentVendorCodex, want: true},
		{fixture: "codex-running.txt", vendor: AgentVendorCodex, want: true},
		{fixture: "copilot-idle.txt", vendor: AgentVendorCopilot, want: true},
		{fixture: "copilot-blocked-trust.txt", vendor: AgentVendorCopilot, want: false},
		{fixture: "codex-blocked-trust.txt", vendor: AgentVendorCodex, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			provider, _ := providerForVendor(tt.vendor)
			content := LastLines(paneFixture(t, tt.fixture), 25)
			if got := provider.ComposerReady(content); got != tt.want {
				t.Fatalf("ComposerReady = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPromptInputUsesVendorComposer(t *testing.T) {
	observed := SessionObservation{
		Availability: ObservationAvailable, Presence: SessionPresencePresent, ContentKnown: true,
		Tool: AgentToolCodex, Status: StatusIdle, Content: paneFixture(t, "codex-idle.txt"),
	}
	if got := promptInputStateFromObservation(observed); got != promptInputReady {
		t.Fatalf("Codex-Eingabe = %q, want %q", got, promptInputReady)
	}
	observed.Content = "irgendein fremder Bildschirm"
	observed.Status = StatusUnknown
	if got := promptInputStateFromObservation(observed); got != promptInputUnknown {
		t.Fatalf("fremder Bildschirm = %q, want %q", got, promptInputUnknown)
	}
	gemini := SessionObservation{
		Availability: ObservationAvailable, Presence: SessionPresencePresent, ContentKnown: true,
		Tool: AgentToolGemini, Status: StatusIdle, Content: "› Type your message",
	}
	if got := promptInputStateFromObservation(gemini); got != promptInputUnknown {
		t.Fatalf("Gemini-Eingabe = %q, want %q", got, promptInputUnknown)
	}
}
