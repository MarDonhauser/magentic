package core

import (
	"testing"
	"time"
)

// Die Tabelle ist die alte Claude-Erkennung, jetzt gegen das Manifest gemessen.
// Hintergrund-Agents und Hintergrund-Shells sind kein eigener Status mehr,
// sondern laufende Arbeit mit einem Detail.
func TestClaudeStatusFromManifest(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		exists     bool
		cmd        string
		content    string
		want       AgentStatus
		wantDetail string
	}{
		{name: "session weg", exists: false, want: StatusDead},
		{name: "shell nach exit", exists: true, cmd: "zsh", content: "❯ ", want: StatusExited},
		{name: "spinner aktiv", exists: true, cmd: "2.1.198", content: "  Antwort läuft\n✽ Hatching… (6s · thinking with xhigh effort)\n❯ ", want: StatusRunning},
		{name: "spinner aktiv kurz", exists: true, cmd: "2.1.198", content: "· Hatching…\n❯ ", want: StatusRunning},
		{name: "spinner aktiv stern", exists: true, cmd: "2.1.198", content: "✳ Hatching…", want: StatusRunning},
		{name: "esc to interrupt legacy", exists: true, cmd: "2.1.198", content: "✳ Puttering… (esc to interrupt)", want: StatusRunning},
		{name: "fertig-zeile ist idle", exists: true, cmd: "2.1.198", content: "  - Nächster Schritt: Keiner nötig\n✻ Crunched for 21s\n❯ \n  🌿 main", want: StatusIdle},
		{name: "fertig-zeile baked", exists: true, cmd: "2.1.198", content: "✻ Baked for 24s\n❯ ", want: StatusIdle},
		{name: "trust dialog", exists: true, cmd: "2.1.198", content: " Quick safety check\n ❯ 1. Yes, I trust this folder\n   2. No, exit\n Enter to confirm · Esc to cancel", want: StatusBlocked, wantDetail: "Ordner-Freigabe"},
		{name: "permission prompt", exists: true, cmd: "2.1.198", content: "Do you want to proceed?\n❯ 1. Yes\n  2. No", want: StatusBlocked},
		{name: "leerer prompt idle", exists: true, cmd: "2.1.198", content: "❯ \n  🌿 main  📝 uncommitted", want: StatusIdle},
		{name: "background agents laufen", exists: true, cmd: "2.1.198", content: "✻ Waiting for 2 background agents to finish\n❯ \n  🌿 main", want: StatusRunning, wantDetail: "wartet auf 2 Agents"},
		{name: "background agent singular", exists: true, cmd: "2.1.198", content: "✻ Waiting for 1 background agent to finish\n❯ ", want: StatusRunning, wantDetail: "wartet auf 1 Agent"},
		{name: "agents aber hauptloop läuft", exists: true, cmd: "2.1.198", content: "✻ Waiting for 2 background agents to finish\n✳ Puttering… (esc to interrupt)", want: StatusRunning, wantDetail: "wartet auf 2 Agents"},
		{name: "spinner mehrwortig", exists: true, cmd: "2.1.198", content: "✢ Suche toten Code… (9m 24s · ↓ 23.9k tokens)\n❯ ", want: StatusRunning},
		{name: "agent tree läuft", exists: true, cmd: "2.1.198", content: "❯ \n  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n\n  ⏺ main\n  ◯ Explore  Comparing llm-classifier, llm-plan   3m 45s · ↓ 51.4k tokens\n  ◯ Explore  Reading .env.example   3m 36s · ↓ 104.0k tokens", want: StatusRunning, wantDetail: "wartet auf 2 Agents"},
		{name: "agent tree wartend", exists: true, cmd: "2.1.198", content: "❯ \n  ⏺ main\n  ◌ Explore  Queued", want: StatusRunning, wantDetail: "wartet auf 1 Agent"},
		{name: "agent tree fertig ist idle", exists: true, cmd: "2.1.198", content: "❯ \n  ⏺ main\n  ◉ Explore  Comparing llm-classifier   4m 2s · ↓ 51.4k tokens", want: StatusIdle},
		{name: "launched-zeile allein ist idle", exists: true, cmd: "2.1.198", content: "⏺ 2 background agents launched (↓ to manage)\n❯ ", want: StatusIdle},
		{name: "tree aber hauptloop läuft", exists: true, cmd: "2.1.198", content: "✢ Suche toten Code… (9m 24s)\n❯ \n  ⏺ main\n  ◯ Explore  Dead-Code-Suche   1m 2s", want: StatusRunning, wantDetail: "wartet auf 1 Agent"},
		{name: "background shell läuft", exists: true, cmd: "2.1.198", content: "✻ Churned for 4m 0s · 1 shell still running\n❯ warte auf das ergebnis\n  ⏵⏵ auto mode on · ← for agents · 1 shell", want: StatusRunning, wantDetail: "1 Shell läuft"},
		{name: "background shell nur statusleiste", exists: true, cmd: "2.1.198", content: "❯ \n  ⏵⏵ auto mode on · ← for agents · 2 shells", want: StatusRunning, wantDetail: "2 Shells laufen"},
		{name: "shell aber hauptloop läuft", exists: true, cmd: "2.1.198", content: "✳ Puttering… (esc to interrupt)\n❯ \n  ⏵⏵ auto mode on · ← for agents · 1 shell", want: StatusRunning, wantDetail: "1 Shell läuft"},
		{name: "synchroner shell-befehl ohne spinner ist idle", exists: true, cmd: "2.1.198", content: "⏺ running 1 shell command…\n❯ \n  🌿 main", want: StatusIdle},
		{name: "fremder bildschirm bleibt unbekannt", exists: true, cmd: "2.1.198", content: "irgendein fremder Bildschirm ohne bekannte Merkmale", want: StatusUnknown},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := resolveSessionStatus(statusInput{
				session:      Session{ID: "session-1", Name: "one"},
				present:      test.exists,
				paneCommand:  test.cmd,
				content:      test.content,
				contentKnown: true,
				now:          now,
			})
			if got.Status != test.want {
				t.Fatalf("Status = %v, want %v", got.Status.Label(), test.want.Label())
			}
			if got.Detail != test.wantDetail {
				t.Fatalf("Detail = %q, want %q", got.Detail, test.wantDetail)
			}
		})
	}
}

// Die Zählregeln, die früher BackgroundAgentCount und BackgroundShellCount
// waren, leben jetzt als Detailregeln im Claude-Manifest.
func TestClaudeWorkingDetailCounts(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"legacy text", "✻ Waiting for 2 background agents to finish\n❯ ", "wartet auf 2 Agents"},
		{"tree zwei laufend", "❯ \n  ⏺ main\n  ◯ Explore  A   3m · ↓ 51k tokens\n  ◯ Explore  B   3m · ↓ 104k tokens", "wartet auf 2 Agents"},
		{"tree gemischt zählt nur aktive", "❯ \n  ⏺ main\n  ◉ Explore  fertig\n  ◯ Explore  läuft\n  ◌ Explore  wartet", "wartet auf 2 Agents"},
		{"still running singular", "✻ Churned for 4m 0s · 1 shell still running\n❯ ", "1 Shell läuft"},
		{"statusleiste plural", "❯ \n  ⏵⏵ auto mode on · ← for agents · 3 shells", "3 Shells laufen"},
	}
	kind, ok := agentKindForID("claude")
	if !ok {
		t.Fatal("Claude-Manifest fehlt")
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			evaluated := evaluateAgentKind(kind, test.content)
			if evaluated.Status != StatusRunning {
				t.Fatalf("Status = %v, want läuft", evaluated.Status.Label())
			}
			if evaluated.Detail != test.want {
				t.Fatalf("Detail = %q, want %q", evaluated.Detail, test.want)
			}
		})
	}
}

// Ein synchron laufender Shell-Befehl ist keine Hintergrund-Shell.
func TestClaudeIgnoresSynchronousShellCommand(t *testing.T) {
	kind, _ := agentKindForID("claude")
	evaluated := evaluateAgentKind(kind, "⏺ running 1 shell command…\n❯ \n  🌿 main")
	if evaluated.Status != StatusIdle || evaluated.Detail != "" {
		t.Fatalf("Status = %v, Detail = %q, want idle ohne Detail", evaluated.Status.Label(), evaluated.Detail)
	}
}
