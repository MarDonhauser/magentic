package core

import (
	"testing"
	"time"
)

func TestDetectAgentTool(t *testing.T) {
	tests := []struct {
		name    string
		command string
		term    bool
		want    string
	}{
		{name: "Codex", command: "codex", want: "codex"},
		{name: "Claude", command: "claude", want: "claude"},
		{name: "Gemini", command: "gemini", want: "gemini"},
		{name: "GitHub Copilot", command: "copilot", want: "copilot"},
		{name: "Terminal gewinnt", command: "codex", term: true, want: "bash"},
		{name: "Unbekannte Agent-Session bleibt neutral", command: "node", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectAgentTool(tt.command, tt.term); got != tt.want {
				t.Fatalf("DetectAgentTool(%q, %v) = %q, want %q", tt.command, tt.term, got, tt.want)
			}
		})
	}
}

func TestOverviewCarriesDetectedAgentTool(t *testing.T) {
	s := &State{
		Projects: []Project{{Name: "NAVI", Path: "/work/navi", MainBranch: "main"}},
		Agents:   []Agent{{Name: "term-navi", Project: "NAVI", Dir: "/work/navi", CreatedAt: time.Now()}},
	}
	statuses := map[string]AgentStatus{"term-navi": StatusIdle}
	tools := map[string]string{"term-navi": "codex"}

	got := BuildOverviewWithToolsFrom(s, statuses, map[string]string{}, map[string]time.Time{}, tools)
	if len(got.Projects) != 1 || len(got.Projects[0].Worktrees) != 1 || len(got.Projects[0].Worktrees[0].Agents) != 1 {
		t.Fatalf("unerwartete Overview-Struktur: %#v", got.Projects)
	}
	if tool := got.Projects[0].Worktrees[0].Agents[0].Tool; tool != "codex" {
		t.Fatalf("Overview tool = %q, want codex", tool)
	}
}
