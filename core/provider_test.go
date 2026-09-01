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

func TestVendorStartCommands(t *testing.T) {
	session := Session{Name: "navi", RuntimeName: "mgt-navi"}
	tests := []struct {
		name   string
		vendor AgentVendor
		runID  string
		mode   string
		want   string
	}{
		{name: "codex neu", vendor: AgentVendorCodex, mode: "new", want: "codex"},
		{name: "codex resume mit Run", vendor: AgentVendorCodex, runID: "abc-123", mode: "resume", want: "codex resume 'abc-123'"},
		{name: "codex resume ohne Run", vendor: AgentVendorCodex, mode: "resume", want: "codex resume --last"},
		{name: "codex neu mit Run", vendor: AgentVendorCodex, runID: "abc-123", mode: "new", want: "codex"},
		{name: "copilot neu", vendor: AgentVendorCopilot, mode: "new", want: "copilot --name 'mgt-navi'"},
		{name: "copilot neu mit Run", vendor: AgentVendorCopilot, runID: "abc-123", mode: "new", want: "copilot --name 'mgt-navi' --session-id='abc-123'"},
		{name: "copilot resume mit Run", vendor: AgentVendorCopilot, runID: "abc-123", mode: "resume", want: "copilot --name 'mgt-navi' --resume='abc-123'"},
		{name: "copilot resume ohne Run", vendor: AgentVendorCopilot, mode: "resume", want: "copilot --name 'mgt-navi' --continue"},
		{name: "gemini neu", vendor: AgentVendorGemini, mode: "new", want: "gemini"},
		{name: "gemini resume", vendor: AgentVendorGemini, runID: "abc-123", mode: "resume", want: "gemini"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, ok := providerForVendor(tt.vendor)
			if !ok {
				t.Fatalf("kein Provider für %q", tt.vendor)
			}
			var run *AgentRunRef
			if tt.runID != "" {
				run = &AgentRunRef{Vendor: tt.vendor, ExternalID: tt.runID}
			}
			got, err := provider.StartCommand(session, run, tt.mode)
			if err != nil {
				t.Fatalf("StartCommand: %v", err)
			}
			if got != tt.want {
				t.Fatalf("StartCommand = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunIDOrigin(t *testing.T) {
	supplied := map[AgentVendor]bool{
		AgentVendorClaude:  true,
		AgentVendorCopilot: true,
		AgentVendorCodex:   false,
		AgentVendorGemini:  false,
	}
	for vendor, want := range supplied {
		provider, ok := providerForVendor(vendor)
		if !ok {
			t.Fatalf("kein Provider für %q", vendor)
		}
		if got := provider.NewRunID() != ""; got != want {
			t.Fatalf("%q liefert vorgegebene Run-ID = %v, want %v", vendor, got, want)
		}
	}
}

func TestCopilotMatchesGithubCopilot(t *testing.T) {
	provider, ok := providerForPaneCommand("github-copilot")
	if !ok || provider.Vendor() != AgentVendorCopilot {
		t.Fatal("github-copilot muss als Copilot erkannt werden")
	}
}
