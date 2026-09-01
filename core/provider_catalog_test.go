package core

import (
	"testing"
	"time"
)

func TestAgentVendorCatalog(t *testing.T) {
	catalog := AgentVendorCatalog()
	if len(catalog) != 4 {
		t.Fatalf("Katalog hat %d Einträge, want 4", len(catalog))
	}
	want := map[string]string{
		string(AgentVendorClaude):  "Claude Code",
		string(AgentVendorCodex):   "Codex",
		string(AgentVendorGemini):  "Gemini CLI",
		string(AgentVendorCopilot): "GitHub Copilot",
	}
	for _, option := range catalog {
		label, known := want[option.Vendor]
		if !known {
			t.Fatalf("unerwarteter Vendor %q", option.Vendor)
		}
		if option.Label != label {
			t.Fatalf("Label für %q = %q, want %q", option.Vendor, option.Label, label)
		}
		delete(want, option.Vendor)
	}
	if len(want) != 0 {
		t.Fatalf("fehlende Vendors: %v", want)
	}
	if catalog[0].Vendor != string(AgentVendorClaude) {
		t.Fatalf("Claude muss zuerst stehen, ist aber %q", catalog[0].Vendor)
	}
}

func TestOverviewCarriesSessionVendor(t *testing.T) {
	agent := Session{
		ID: "s1", Name: "navi", Dir: "/work/navi", SessionKind: SessionKindCodingAgent,
		Vendor: AgentVendorCodex, CreatedAt: time.Now(),
	}
	ov := toOvAgent(agent, SessionObservation{Status: StatusIdle, Tool: AgentToolCodex}, "main")
	if ov.Vendor != string(AgentVendorCodex) {
		t.Fatalf("OvAgent.Vendor = %q, want %q", ov.Vendor, AgentVendorCodex)
	}
	term := Session{ID: "s2", Name: "term-navi", Kind: KindTerm, CreatedAt: time.Now()}
	if got := toOvAgent(term, SessionObservation{Status: StatusTerm, Tool: AgentToolBash}, "main").Vendor; got != "" {
		t.Fatalf("Terminal-Vendor = %q, want leer", got)
	}
}
