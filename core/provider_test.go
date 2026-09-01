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
			{ID: "term-navi", Name: "term-navi", RuntimeName: "mgt-term-navi", Project: "NAVI", Dir: "/work/navi", Kind: KindTerm, CreatedAt: time.Now()},
			{ID: "shell-navi", Name: "shell-navi", RuntimeName: "mgt-shell-navi", Project: "NAVI", Dir: "/work/navi", Kind: KindTerm, CreatedAt: time.Now()},
		},
	}
	snapshot := ObservationSnapshot{
		ObservedAt:   time.Now(),
		Availability: ObservationAvailable,
		Sessions: []SessionObservation{
			{
				SessionID: "term-navi", Availability: ObservationAvailable,
				Presence: SessionPresencePresent, Status: StatusIdle, Tool: AgentToolCodex,
			},
			{
				SessionID: "shell-navi", Availability: ObservationAvailable,
				Presence: SessionPresencePresent, Status: StatusTerm, Tool: AgentToolBash,
			},
		},
	}

	got := BuildOverviewFromObservation(s, snapshot)
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

func TestClaudeProviderStartCommand(t *testing.T) {
	session := Session{Name: "navi", RuntimeName: "mgt-navi"}
	provider, ok := providerForVendor(AgentVendorClaude)
	if !ok {
		t.Fatal("kein Claude-Provider registriert")
	}
	run := AgentRunRef{Vendor: AgentVendorClaude, ExternalID: "abc-123"}
	tests := []struct {
		name string
		run  *AgentRunRef
		mode string
		want string
	}{
		{name: "neu ohne Run", mode: "new", want: "claude --name 'mgt-navi'"},
		{name: "neu mit Run", run: &run, mode: "new", want: "claude --name 'mgt-navi' --session-id 'abc-123'"},
		{name: "resume mit Run", run: &run, mode: "resume", want: "claude --name 'mgt-navi' --resume 'abc-123'"},
		{name: "resume ohne Run", mode: "resume", want: "claude --name 'mgt-navi' --continue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := provider.StartCommand(session, tt.run, tt.mode)
			if err != nil {
				t.Fatalf("StartCommand: %v", err)
			}
			if got != tt.want {
				t.Fatalf("StartCommand = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderForPaneCommand(t *testing.T) {
	tests := []struct {
		command string
		want    AgentVendor
		found   bool
	}{
		{command: "claude", want: AgentVendorClaude, found: true},
		{command: "-claude", want: AgentVendorClaude, found: true},
		{command: "CLAUDE", want: AgentVendorClaude, found: true},
		{command: "claude-code", want: AgentVendorClaude, found: true},
		{command: "node", found: false},
		{command: "", found: false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			provider, ok := providerForPaneCommand(tt.command)
			if ok != tt.found {
				t.Fatalf("providerForPaneCommand(%q) gefunden = %v, want %v", tt.command, ok, tt.found)
			}
			if ok && provider.Vendor() != tt.want {
				t.Fatalf("providerForPaneCommand(%q) = %q, want %q", tt.command, provider.Vendor(), tt.want)
			}
		})
	}
}

func TestClaudeProviderSuppliesRunID(t *testing.T) {
	provider, _ := providerForVendor(AgentVendorClaude)
	if provider.NewRunID() == "" {
		t.Fatal("Claude nimmt eine vorgegebene Run-Identität an")
	}
}
