package core

import (
	"strings"
	"testing"
	"time"
)

func mustParseAgentKind(t *testing.T, manifest string) *agentKind {
	t.Helper()
	kind, err := parseAgentKind("test.yaml", []byte(manifest), AgentKindUser)
	if err != nil {
		t.Fatalf("Manifest abgelehnt: %v", err)
	}
	return kind
}

// Die Reihenfolge steht im Format, nicht in der Datei: auch wenn das Manifest
// blocked zuerst nennt, gewinnt working.
const orderedManifest = `
kind: acme
label: Acme
pane_commands:
  - literal: acme
states:
  blocked:
    - literal: 'frage offen'
  idle:
    - literal: 'ruhezustand'
  done:
    - literal: 'runde beendet'
  working:
    - literal: 'arbeitet'
`

func TestEvaluationOrderIsFixedByTheFormat(t *testing.T) {
	kind := mustParseAgentKind(t, orderedManifest)
	tests := []struct {
		name    string
		content string
		want    AgentStatus
	}{
		{"working schlägt blocked", "arbeitet\nfrage offen", StatusRunning},
		{"done schlägt idle", "runde beendet\nruhezustand", StatusDone},
		{"blocked schlägt done", "frage offen\nrunde beendet", StatusBlocked},
		{"nichts passt", "ein fremder Bildschirm", StatusUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluated := evaluateAgentKind(kind, test.content)
			if evaluated.Status != test.want {
				t.Fatalf("Status = %v, want %v", evaluated.Status.Label(), test.want.Label())
			}
		})
	}
}

// Innerhalb eines Zustands gewinnt das erste Muster in Dateireihenfolge.
func TestFirstMatchWinsWithinOneState(t *testing.T) {
	kind := mustParseAgentKind(t, `
kind: acme
label: Acme
pane_commands:
  - literal: acme
states:
  blocked:
    - literal: 'dialog'
details:
  blocked:
    - label: Erste
      patterns:
        - literal: 'dialog'
    - label: Zweite
      patterns:
        - literal: 'dialog'
`)
	if evaluated := evaluateAgentKind(kind, "dialog"); evaluated.Detail != "Erste" {
		t.Fatalf("Detail = %q, want die erste Regel", evaluated.Detail)
	}
}

// Ein Merkmal oberhalb des erklärten Ausschnitts zählt nicht.
func TestMarkerAboveTheDeclaredTailDoesNotMatch(t *testing.T) {
	kind := mustParseAgentKind(t, `
kind: acme
label: Acme
tail: 3
pane_commands:
  - literal: acme
states:
  blocked:
    - literal: 'frage offen'
  idle:
    - literal: 'ruhezustand'
`)
	content := "frage offen\n" + strings.Repeat("nichts\n", 10) + "ruhezustand\n"
	if evaluated := evaluateAgentKind(kind, content); evaluated.Status != StatusIdle {
		t.Fatalf("Status = %v, want idle", evaluated.Status.Label())
	}
}

func TestDetailNeverChangesTheResolvedStatus(t *testing.T) {
	kind := mustParseAgentKind(t, `
kind: acme
label: Acme
pane_commands:
  - literal: acme
states:
  working:
    - literal: 'arbeitet'
  blocked:
    - literal: 'dialog'
details:
  blocked:
    - label: Shell-Freigabe
      patterns:
        - literal: 'run this command'
  working:
    - capture: '(?i)waiting for (\d+) helpers'
      singular: 'wartet auf %d Helfer'
      plural: 'wartet auf %d Helfer'
`)
	tests := []struct {
		name       string
		content    string
		wantStatus AgentStatus
		wantDetail string
	}{
		{"erkannte Freigabe", "dialog\nrun this command", StatusBlocked, "Shell-Freigabe"},
		{"unerkannte Freigabe", "dialog\nirgendetwas anderes", StatusBlocked, ""},
		{"gezählte Helfer", "arbeitet\nWaiting for 3 helpers", StatusRunning, "wartet auf 3 Helfer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluated := evaluateAgentKind(kind, test.content)
			if evaluated.Status != test.wantStatus || evaluated.Detail != test.wantDetail {
				t.Fatalf("Status = %v, Detail = %q, want %v / %q",
					evaluated.Status.Label(), evaluated.Detail, test.wantStatus.Label(), test.wantDetail)
			}
		})
	}
}

// Eine Agent-Art ohne Composer-Muster gilt nie als eingabebereit.
func TestKindWithoutComposerPatternsIsNeverReady(t *testing.T) {
	withUserAgentKinds(t, map[string]string{"acme.yaml": `
kind: acme
label: Acme
tool: acme
pane_commands:
  - literal: acme
states:
  idle:
    - literal: 'ruhezustand'
`})
	kind, _ := agentKindForID("acme")
	if agentKindComposerReady(kind, "ruhezustand") {
		t.Fatal("eine Agent-Art ohne Composer-Muster wurde eingabebereit gemeldet")
	}
	observed := SessionObservation{
		Availability: ObservationAvailable, Presence: SessionPresencePresent, ContentKnown: true,
		Tool: "acme", Status: StatusIdle, Content: "ruhezustand",
	}
	if got := promptInputStateFromObservation(observed); got != promptInputUnknown {
		t.Fatalf("Eingabezustand = %q, want %q", got, promptInputUnknown)
	}
}

// Ein teures Manifest kostet eine Session einen Zyklus, nicht den Zyklus.
func TestOverBudgetEvaluationIsAbandonedForThatSessionOnly(t *testing.T) {
	var patterns strings.Builder
	for i := 0; i < 400; i++ {
		patterns.WriteString("    - regex: '(?s)(a+)+(b+)+(c+)+(d+)+z'\n")
	}
	expensive := mustParseAgentKind(t, "kind: acme\nlabel: Acme\npane_commands:\n  - literal: acme\nstates:\n  working:\n"+patterns.String())
	content := strings.Repeat("aaaaaaaaaaaaaaaabbbbbbbbbbbbbbbbccccccccccccccccdddddddddddddddd\n", 25)

	evaluated := evaluateAgentKind(expensive, content)
	if !evaluated.Abandoned {
		t.Skip("die Auswertung blieb im Budget; das Muster ist auf dieser Maschine zu billig")
	}
	if evaluated.Matched || evaluated.Status != StatusUnknown {
		t.Fatalf("eine abgebrochene Auswertung lieferte einen Status: %#v", evaluated)
	}
	// Die anderen Sessions werden weiter ausgewertet.
	claude, _ := agentKindForID("claude")
	if other := evaluateAgentKind(claude, "✳ Puttering… (esc to interrupt)"); other.Status != StatusRunning {
		t.Fatalf("die übrigen Sessions wurden mitgerissen: %v", other.Status.Label())
	}
	resolved := resolveSessionStatus(statusInput{
		session: Session{ID: "s"}, present: true, paneCommand: "acme",
		content: content, contentKnown: true, now: time.Now(),
	})
	if resolved.Status != StatusUnknown {
		t.Fatalf("abgebrochene Auswertung = %v, want unbekannt", resolved.Status.Label())
	}
}

// Der ganze mitgelieferte Satz muss pro Session weit unter dem Budget bleiben.
func BenchmarkEvaluateShippedManifests(b *testing.B) {
	content := strings.Repeat("irgendeine Zeile mit Text und Zahlen 1234\n", 200)
	kinds := []*agentKind{}
	for _, id := range []string{"claude", "codex", "copilot", "gemini"} {
		if kind, ok := agentKindForID(id); ok {
			kinds = append(kinds, kind)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, kind := range kinds {
			evaluateAgentKind(kind, content)
		}
	}
}

func TestShippedManifestEvaluationStaysInsideTheBudget(t *testing.T) {
	content := strings.Repeat("irgendeine Zeile mit Text und Zahlen 1234\n", 200)
	for _, id := range []string{"claude", "codex", "copilot", "gemini"} {
		kind, ok := agentKindForID(id)
		if !ok {
			t.Fatalf("Manifest %q fehlt", id)
		}
		started := time.Now()
		for i := 0; i < 20; i++ {
			evaluateAgentKind(kind, content)
		}
		if per := time.Since(started) / 20; per > agentKindBudget {
			t.Fatalf("%s braucht %v pro Snapshot, Budget ist %v", id, per, agentKindBudget)
		}
	}
}
