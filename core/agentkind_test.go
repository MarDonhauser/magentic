package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withUserAgentKinds points the user manifest directory at a temporary folder
// and writes the given files into it.
func withUserAgentKinds(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("MAGENTIC_STATE", filepath.Join(root, "state.json"))
	dir := filepath.Join(root, "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("Manifest-Verzeichnis: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("Manifest %q: %v", name, err)
		}
	}
	ReloadAgentKinds()
	t.Cleanup(ReloadAgentKinds)
	return dir
}

const handwrittenManifest = `
kind: acme
label: Acme Agent
tool: acme
vendor: acme
observed_version: "3.1.4"
tail: 12
pane_commands:
  - regex: '^acme(-.*)?$'
  - literal: 'acme-cli'
states:
  working:
    - literal: 'thinking'
  blocked:
    - regex: '(?m)^\s*\? '
  done:
    - literal: 'turn complete'
  idle:
    - literal: 'ask acme anything'
composer:
  - literal: 'ask acme anything'
details:
  blocked:
    - label: Shell-Freigabe
      patterns:
        - literal: 'run this command'
  working:
    - capture: '(?i)(\d+) helpers'
      singular: 'wartet auf %d Helfer'
      plural: 'wartet auf %d Helfer'
`

func TestManifestDecodesIntoExpectedStructure(t *testing.T) {
	kind, err := parseAgentKind("acme.yaml", []byte(handwrittenManifest), AgentKindUser)
	if err != nil {
		t.Fatalf("Manifest abgelehnt: %v", err)
	}
	if kind.id != "acme" || kind.label != "Acme Agent" || kind.tool != "acme" {
		t.Fatalf("Kopf falsch gelesen: %#v", kind)
	}
	if kind.vendor != AgentVendor("acme") || kind.observedVersion != "3.1.4" || !kind.screensRecorded {
		t.Fatalf("Herkunftsangaben falsch gelesen: %#v", kind)
	}
	if kind.tail != 12 {
		t.Fatalf("tail = %d, want 12", kind.tail)
	}
	if len(kind.paneCommands) != 2 {
		t.Fatalf("Pane-Kommandos = %d, want 2", len(kind.paneCommands))
	}
	if len(kind.working) != 1 || len(kind.blocked) != 1 || len(kind.done) != 1 || len(kind.idle) != 1 {
		t.Fatalf("Status-Regeln unvollständig: %#v", kind)
	}
	if len(kind.composer) != 1 || len(kind.blockedDetails) != 1 || len(kind.workingDetails) != 1 {
		t.Fatalf("Composer oder Details unvollständig: %#v", kind)
	}
	if kind.source != AgentKindUser || kind.path != "acme.yaml" {
		t.Fatalf("Quelle falsch vermerkt: %#v", kind)
	}
}

func TestManifestValidationRejectsWithStatedReason(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{"ohne Agent-Art", "label: Acme\npane_commands:\n  - literal: acme\n", "keine Agent-Art"},
		{"ohne Bezeichnung", "kind: acme\npane_commands:\n  - literal: acme\n", "keine Bezeichnung"},
		{"ohne Pane-Kommando", "kind: acme\nlabel: Acme\n", "kein Pane-Kommando"},
		{
			"unbekannter Zustand",
			"kind: acme\nlabel: Acme\npane_commands:\n  - literal: acme\nstates:\n  dead:\n    - literal: weg\n",
			"gehört nicht zum Vokabular",
		},
		{
			"nicht übersetzbarer Ausdruck",
			"kind: acme\nlabel: Acme\npane_commands:\n  - literal: acme\nstates:\n  working:\n    - regex: '('\n",
			"lässt sich nicht übersetzen",
		},
		{
			"Muster ohne Inhalt",
			"kind: acme\nlabel: Acme\npane_commands:\n  - literal: acme\nstates:\n  working:\n    - {}\n",
			"weder literal noch regex",
		},
		{
			"tail über dem Rückblick",
			"kind: acme\nlabel: Acme\ntail: 5000\npane_commands:\n  - literal: acme\nstates:\n  idle:\n    - literal: ruhe\n",
			"überschreitet den beobachteten Rückblick",
		},
		{
			"aufgenommene Art ohne Regeln",
			"kind: acme\nlabel: Acme\npane_commands:\n  - literal: acme\nstates: {}\n",
			"keine Status-Regel",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseAgentKind("acme.yaml", []byte(test.manifest), AgentKindUser)
			if err == nil {
				t.Fatal("Manifest wurde angenommen")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Begründung = %q, want etwas mit %q", err, test.want)
			}
		})
	}
}

func TestUserManifestReplacesShippedKindInFull(t *testing.T) {
	withUserAgentKinds(t, map[string]string{"claude.yaml": `
kind: claude
label: Claude Code (eigenes Manifest)
tool: claude
vendor: claude
pane_commands:
  - literal: 'claude'
states:
  idle:
    - literal: 'mein eigener ruhezustand'
`})
	kind, ok := agentKindForID("claude")
	if !ok {
		t.Fatal("Claude fehlt nach dem Überschreiben")
	}
	if kind.source != AgentKindUser || kind.label != "Claude Code (eigenes Manifest)" {
		t.Fatalf("das mitgelieferte Manifest gewinnt weiter: %#v", kind.label)
	}
	// Kein Teil-Merge: die mitgelieferten Regeln sind vollständig weg.
	if len(kind.working) != 0 || len(kind.blockedDetails) != 0 {
		t.Fatalf("mitgelieferte Regeln wurden zusammengeführt: %#v", kind)
	}
	evaluated := evaluateAgentKind(kind, "mein eigener Ruhezustand")
	if evaluated.Status != StatusIdle {
		t.Fatalf("eigene Regel greift nicht: %v", evaluated.Status.Label())
	}
}

func TestInvalidUserManifestLeavesShippedOneInEffect(t *testing.T) {
	withUserAgentKinds(t, map[string]string{"claude.yaml": "kind: claude\nlabel: Kaputt\npane_commands:\n  - literal: claude\nstates:\n  working:\n    - regex: '('\n"})
	kind, ok := agentKindForID("claude")
	if !ok {
		t.Fatal("Claude fehlt, obwohl nur das Benutzer-Manifest kaputt ist")
	}
	if kind.source != AgentKindShipped {
		t.Fatalf("Quelle = %q, want das mitgelieferte Manifest", kind.source)
	}
	evaluated := evaluateAgentKind(kind, "✳ Puttering… (esc to interrupt)")
	if evaluated.Status != StatusRunning {
		t.Fatalf("mitgelieferte Regeln wirken nicht mehr: %v", evaluated.Status.Label())
	}
}

func TestManifestReportNamesRejectionAndSurvivingKind(t *testing.T) {
	withUserAgentKinds(t, map[string]string{"claude.yaml": "kind: claude\nlabel: Kaputt\npane_commands:\n  - literal: claude\nstates:\n  working:\n    - regex: '('\n"})
	reports := ValidateAgentKinds()
	var rejected, shipped bool
	for _, report := range reports {
		if report.Kind != "claude" {
			continue
		}
		switch report.Source {
		case AgentKindUser:
			rejected = !report.Accepted && strings.Contains(report.Reason, "lässt sich nicht übersetzen")
		case AgentKindShipped:
			shipped = report.Accepted && report.Reason == ""
		}
	}
	if !rejected {
		t.Fatalf("das abgelehnte Benutzer-Manifest fehlt im Bericht: %#v", reports)
	}
	if !shipped {
		t.Fatalf("die überlebende mitgelieferte Agent-Art fehlt im Bericht: %#v", reports)
	}
}

func TestUserManifestIntroducesUnsupportedKind(t *testing.T) {
	withUserAgentKinds(t, map[string]string{"acme.yaml": handwrittenManifest})
	kind, ok := agentKindForPaneCommand("acme-cli")
	if !ok || kind.id != "acme" {
		t.Fatalf("neue Agent-Art wird nicht erkannt: %#v", kind)
	}
	// Erkennung heißt nicht Startfähigkeit: Acme bleibt kein startbarer Agent.
	if _, startable := providerForVendor(AgentVendor("acme")); startable {
		t.Fatal("eine Agent-Art aus einem Benutzer-Manifest wurde startbar")
	}
}

func TestShippedManifestsLoadAndValidate(t *testing.T) {
	for _, id := range []string{"claude", "codex", "copilot", "gemini"} {
		kind, ok := agentKindForID(id)
		if !ok {
			t.Fatalf("mitgeliefertes Manifest %q fehlt", id)
		}
		if kind.label == "" || len(kind.paneCommands) == 0 {
			t.Fatalf("mitgeliefertes Manifest %q ist unvollständig: %#v", id, kind)
		}
	}
	for _, report := range ValidateAgentKinds() {
		if report.Source == AgentKindShipped && !report.Accepted {
			t.Fatalf("mitgeliefertes Manifest %q wurde abgelehnt: %s", report.Path, report.Reason)
		}
	}
}

func TestPaneCommandRecognitionComesFromManifests(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{"claude", "claude"},
		{"claude-code", "claude"},
		{"2.1.241", "claude"},
		{"codex", "codex"},
		{"github-copilot", "copilot"},
		{"gemini", "gemini"},
		{"node", ""},
		{"zsh", ""},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			kind, ok := agentKindForPaneCommand(test.command)
			if test.want == "" {
				if ok {
					t.Fatalf("%q wurde %q zugeordnet", test.command, kind.id)
				}
				return
			}
			if !ok || kind.id != test.want {
				t.Fatalf("%q = %#v, want %q", test.command, kind, test.want)
			}
		})
	}
}
