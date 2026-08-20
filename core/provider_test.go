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
		Agents: []Agent{
			{Name: "term-navi", Project: "NAVI", Dir: "/work/navi", Kind: KindTerm, CreatedAt: time.Now()},
			{Name: "shell-navi", Project: "NAVI", Dir: "/work/navi", Kind: KindTerm, CreatedAt: time.Now()},
		},
	}
	statuses := map[string]AgentStatus{"term-navi": StatusIdle, "shell-navi": StatusTerm}
	tools := map[string]string{"term-navi": AgentToolCodex, "shell-navi": AgentToolBash}

	got := BuildOverviewWithToolsFrom(s, statuses, map[string]string{}, map[string]time.Time{}, tools)
	if len(got.Projects) != 1 || len(got.Projects[0].Worktrees) != 1 || len(got.Projects[0].Worktrees[0].Agents) != 2 {
		t.Fatalf("unerwartete Overview-Struktur: %#v", got.Projects)
	}
	agent := got.Projects[0].Worktrees[0].Agents[0]
	if tool := agent.Tool; tool != AgentToolCodex {
		t.Fatalf("Overview tool = %q, want codex", tool)
	}
	if !agent.Term || !agent.HandoffSource || agent.HandoffTarget {
		t.Fatalf("Codex in Term-Session muss Quelle sein, darf ohne bekannte Readiness aber kein Ziel sein: %#v", agent)
	}
	shell := got.Projects[0].Worktrees[0].Agents[1]
	if shell.Tool != AgentToolBash || shell.HandoffSource || shell.HandoffTarget {
		t.Fatalf("reine Shell darf nicht Handoff-fähig sein: %#v", shell)
	}
}
