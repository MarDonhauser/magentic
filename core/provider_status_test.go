package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
func TestKindStatusFromRecordedPanes(t *testing.T) {
	tests := []struct {
		fixture    string
		kind       string
		want       AgentStatus
		wantDetail string
	}{
		{fixture: "codex-idle.txt", kind: "codex", want: StatusIdle},
		{fixture: "codex-idle-after.txt", kind: "codex", want: StatusIdle},
		{fixture: "codex-running.txt", kind: "codex", want: StatusRunning},
		{fixture: "codex-blocked-trust.txt", kind: "codex", want: StatusBlocked, wantDetail: "Ordner-Freigabe"},
		{fixture: "copilot-idle.txt", kind: "copilot", want: StatusIdle},
		{fixture: "copilot-idle-after.txt", kind: "copilot", want: StatusIdle},
		{fixture: "copilot-running.txt", kind: "copilot", want: StatusRunning},
		{fixture: "copilot-blocked-trust.txt", kind: "copilot", want: StatusBlocked, wantDetail: "Ordner-Freigabe"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			kind, ok := agentKindForID(tt.kind)
			if !ok {
				t.Fatalf("kein Manifest für %q", tt.kind)
			}
			evaluated := evaluateAgentKind(kind, paneFixture(t, tt.fixture))
			if evaluated.Status != tt.want {
				t.Fatalf("Status = %v, want %v", evaluated.Status.Label(), tt.want.Label())
			}
			if evaluated.Detail != tt.wantDetail {
				t.Fatalf("Detail = %q, want %q", evaluated.Detail, tt.wantDetail)
			}
		})
	}
}

// Codex erreicht dieselbe Tiefe wie Claude Code: alle vier Zustände, ein
// Detail am blockierten Bildschirm und eine eigene Eingabezeile.
func TestCodexReachesFirstClassFidelity(t *testing.T) {
	kind, ok := agentKindForID("codex")
	if !ok {
		t.Fatal("kein Codex-Manifest")
	}
	if kind.observedVersion == "" || !kind.screensRecorded {
		t.Fatalf("Codex-Manifest nennt keine beobachtete Version: %#v", kind.observedVersion)
	}
	if len(kind.working) == 0 || len(kind.blocked) == 0 || len(kind.idle) == 0 || len(kind.composer) == 0 {
		t.Fatal("Codex-Manifest deckt nicht alle Zustände ab")
	}
	// done wird aus dem ruhenden Bildschirm plus dem letzten Blick abgeleitet.
	seen := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	resolved := resolveSessionStatus(statusInput{
		session:       Session{ID: "session-1", Name: "one", SeenAt: seen},
		present:       true,
		paneCommand:   "codex",
		content:       paneFixture(t, "codex-idle.txt"),
		contentKnown:  true,
		activity:      seen.Add(time.Minute),
		activityKnown: true,
		now:           seen.Add(time.Minute),
	})
	if resolved.Status != StatusDone {
		t.Fatalf("ruhender, ungesehener Codex-Bildschirm = %v, want fertig", resolved.Status.Label())
	}
}

// Ein fremder Bildschirm darf nie als idle durchgehen: Magentic würde ihn
// sonst als arbeitsbereit melden und Nachrichten hineinschicken.
func TestUnfamiliarPaneStaysUnknown(t *testing.T) {
	for _, id := range []string{"codex", "copilot", "gemini"} {
		kind, _ := agentKindForID(id)
		evaluated := evaluateAgentKind(kind, "irgendein fremder Bildschirm ohne bekannte Merkmale")
		if evaluated.Matched {
			t.Fatalf("%q: Status = %v, want unbekannt", id, evaluated.Status.Label())
		}
	}
}

// Gemini CLI wurde nie beobachtet. Solange das so ist, bleibt der Status
// unbekannt und es geht nichts automatisch in die Session hinein.
func TestGeminiStaysUnknownAndRefusesInput(t *testing.T) {
	kind, ok := agentKindForID("gemini")
	if !ok {
		t.Fatal("kein Gemini-Manifest")
	}
	if kind.screensRecorded {
		t.Fatal("Gemini-Manifest behauptet aufgenommene Bildschirme")
	}
	resolved := resolveSessionStatus(statusInput{
		session:      Session{ID: "session-1", Name: "one"},
		present:      true,
		paneCommand:  "gemini",
		content:      "› Type your message\nDo you want to run this command?\n❯ 1. Yes\n",
		contentKnown: true,
		now:          time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	})
	if resolved.Status != StatusUnknown || resolved.Source != StatusSourceNone {
		t.Fatalf("Gemini-Status = %v aus %q, want unbekannt", resolved.Status.Label(), resolved.Source)
	}
	observed := SessionObservation{
		Availability: ObservationAvailable, Presence: SessionPresencePresent, ContentKnown: true,
		Tool: AgentToolGemini, Status: resolved.Status, Content: "› Type your message",
	}
	if got := promptInputStateFromObservation(observed); got != promptInputUnknown {
		t.Fatalf("Gemini-Eingabe = %q, want %q", got, promptInputUnknown)
	}
	if err := validatePromptTargetObservation("gemini", promptTargetObservationFromSession(observed)); err == nil {
		t.Fatal("eine unbeobachtete Agent-Art darf keinen Prompt bekommen")
	}
}

func TestObservationUsesManifestStatus(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	running := resolveSessionStatus(statusInput{
		session: Session{ID: "a"}, present: true, paneCommand: "codex",
		content: paneFixture(t, "codex-running.txt"), contentKnown: true, now: now,
	})
	if running.Status != StatusRunning || running.Source != StatusSourceSnapshot {
		t.Fatalf("Codex = %v aus %q, want läuft aus dem Bildschirm", running.Status.Label(), running.Source)
	}
	idle := resolveSessionStatus(statusInput{
		session: Session{ID: "b"}, present: true, paneCommand: "copilot",
		content: paneFixture(t, "copilot-idle.txt"), contentKnown: true, now: now,
	})
	if idle.Status != StatusIdle || idle.Source != StatusSourceSnapshot {
		t.Fatalf("Copilot = %v aus %q, want idle aus dem Bildschirm", idle.Status.Label(), idle.Source)
	}
}

func TestComposerReadyFromRecordedPanes(t *testing.T) {
	tests := []struct {
		fixture string
		kind    string
		want    bool
	}{
		{fixture: "codex-idle.txt", kind: "codex", want: true},
		{fixture: "codex-running.txt", kind: "codex", want: true},
		{fixture: "copilot-idle.txt", kind: "copilot", want: true},
		{fixture: "copilot-blocked-trust.txt", kind: "copilot", want: false},
		{fixture: "codex-blocked-trust.txt", kind: "codex", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			kind, _ := agentKindForID(tt.kind)
			if got := agentKindComposerReady(kind, paneFixture(t, tt.fixture)); got != tt.want {
				t.Fatalf("ComposerReady = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPromptInputUsesKindComposer(t *testing.T) {
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
}
