package main

import "testing"

func TestDetectClaudeStatus(t *testing.T) {
	cases := []struct {
		name    string
		exists  bool
		cmd     string
		content string
		want    AgentStatus
	}{
		{"session weg", false, "", "", StatusDead},
		{"shell nach exit", true, "zsh", "❯ ", StatusExited},
		{"spinner aktiv", true, "2.1.198", "  Antwort läuft\n✽ Hatching… (6s · thinking with xhigh effort)\n❯ ", StatusRunning},
		{"spinner aktiv kurz", true, "2.1.198", "· Hatching…\n❯ ", StatusRunning},
		{"spinner aktiv stern", true, "2.1.198", "✳ Hatching…", StatusRunning},
		{"esc to interrupt legacy", true, "node", "✳ Puttering… (esc to interrupt)", StatusRunning},
		{"fertig-zeile ist idle", true, "2.1.198", "  - Nächster Schritt: Keiner nötig\n✻ Crunched for 21s\n❯ \n  🌿 main", StatusIdle},
		{"fertig-zeile baked", true, "2.1.198", "✻ Baked for 24s\n❯ ", StatusIdle},
		{"trust dialog", true, "2.1.198", " Quick safety check\n ❯ 1. Yes, I trust this folder\n   2. No, exit\n Enter to confirm · Esc to cancel", StatusBlocked},
		{"permission prompt", true, "node", "Do you want to proceed?\n❯ 1. Yes\n  2. No", StatusBlocked},
		{"leerer prompt idle", true, "2.1.198", "❯ \n  🌿 main  📝 uncommitted", StatusIdle},
		{"background agents laufen", true, "2.1.198", "✻ Waiting for 2 background agents to finish\n❯ \n  🌿 main", StatusAgents},
		{"background agent singular", true, "2.1.198", "✻ Waiting for 1 background agent to finish\n❯ ", StatusAgents},
		{"agents aber hauptloop läuft", true, "node", "✻ Waiting for 2 background agents to finish\n✳ Puttering… (esc to interrupt)", StatusRunning},
		{"spinner mehrwortig", true, "2.1.198", "✢ Suche toten Code… (9m 24s · ↓ 23.9k tokens)\n❯ ", StatusRunning},
		{"agent tree läuft", true, "2.1.198", "❯ \n  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n\n  ⏺ main\n  ◯ Explore  Comparing llm-classifier, llm-plan   3m 45s · ↓ 51.4k tokens\n  ◯ Explore  Reading .env.example   3m 36s · ↓ 104.0k tokens", StatusAgents},
		{"agent tree wartend", true, "2.1.198", "❯ \n  ⏺ main\n  ◌ Explore  Queued", StatusAgents},
		{"agent tree fertig ist idle", true, "2.1.198", "❯ \n  ⏺ main\n  ◉ Explore  Comparing llm-classifier   4m 2s · ↓ 51.4k tokens", StatusIdle},
		{"launched-zeile allein ist idle", true, "2.1.198", "⏺ 2 background agents launched (↓ to manage)\n❯ ", StatusIdle},
		{"tree aber hauptloop läuft", true, "2.1.198", "✢ Suche toten Code… (9m 24s)\n❯ \n  ⏺ main\n  ◯ Explore  Dead-Code-Suche   1m 2s", StatusRunning},
	}
	for _, c := range cases {
		got := DetectClaudeStatus(c.exists, c.cmd, c.content)
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got.Label(), c.want.Label())
		}
	}
}

func TestBackgroundAgentCount(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{"kein agent", "❯ ", 0},
		{"legacy text", "✻ Waiting for 2 background agents to finish", 2},
		{"tree zwei laufend", "  ⏺ main\n  ◯ Explore  A   3m · ↓ 51k tokens\n  ◯ Explore  B   3m · ↓ 104k tokens", 2},
		{"tree gemischt zählt nur aktive", "  ⏺ main\n  ◉ Explore  fertig\n  ◯ Explore  läuft\n  ◌ Explore  wartet", 2},
	}
	for _, c := range cases {
		if got := backgroundAgentCount(c.content); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
