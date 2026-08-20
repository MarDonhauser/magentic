package main

import "testing"

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

func TestBackgroundShellCount(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{"keine shell", "❯ ", 0},
		{"still running singular", "✻ Churned for 4m 0s · 1 shell still running", 1},
		{"statusleiste singular", "  ⏵⏵ auto mode on · ← for agents · 1 shell", 1},
		{"statusleiste plural", "  ⏵⏵ auto mode on · ← for agents · 3 shells", 3},
		{"synchroner befehl zählt nicht", "⏺ running 1 shell command…", 0},
	}
	for _, c := range cases {
		if got := backgroundShellCount(c.content); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
