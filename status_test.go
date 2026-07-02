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
	}
	for _, c := range cases {
		got := DetectClaudeStatus(c.exists, c.cmd, c.content)
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got.Label(), c.want.Label())
		}
	}
}
