package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const claudeStatusPayload = `{
  "session_id": "3349b7d8-d157-4777-a70d-735136bcc216",
  "session_name": "mgt-dev-pilot-3",
  "cwd": "/Users/dev/Projects/dev.pilot",
  "effort": {"level": "xhigh"},
  "model": {"id": "claude-fable-5-1", "display_name": "Fable 5.1"},
  "version": "2.1.258",
  "output_style": {"name": "Fokus"},
  "cost": {"total_cost_usd": 2.3826445},
  "context_window": {
    "context_window_size": 1000000,
    "current_usage": {"input_tokens": 128, "output_tokens": 982, "cache_creation_input_tokens": 2902, "cache_read_input_tokens": 100246},
    "used_percentage": 10,
    "remaining_percentage": 90
  },
  "fast_mode": false
}`

func TestClaudeStatusPayloadBecomesReport(t *testing.T) {
	now := time.Date(2026, 9, 2, 13, 0, 0, 0, time.UTC)
	report, err := StatusReportFromClaudePayload([]byte(claudeStatusPayload), now)
	if err != nil {
		t.Fatalf("Nutzlast lesen: %v", err)
	}
	want := StatusReport{
		Vendor: AgentVendorClaude, RuntimeName: "mgt-dev-pilot-3",
		RunRef: "3349b7d8-d157-4777-a70d-735136bcc216", At: now, UID: os.Getuid(),
		Model: "Fable 5.1", ModelID: "claude-fable-5-1", Effort: "xhigh",
		ContextPercent: 10, ContextWindow: 1000000, ContextTokens: 103276,
		CostUSD: 2.3826445, Version: "2.1.258", OutputStyle: "Fokus", Dir: "/Users/dev/Projects/dev.pilot",
	}
	if report != want {
		t.Fatalf("Report %+v, want %+v", report, want)
	}
	if _, err := StatusReportFromClaudePayload(nil, now); err == nil {
		t.Fatal("leere Nutzlast wurde akzeptiert")
	}
	if _, err := StatusReportFromClaudePayload([]byte("{"), now); err == nil {
		t.Fatal("kaputtes JSON wurde akzeptiert")
	}
}

func TestStatusReportFallsBackToModelID(t *testing.T) {
	report, err := StatusReportFromClaudePayload([]byte(`{"session_name":"mgt-x","model":{"id":"claude-opus-5"}}`), time.Now())
	if err != nil {
		t.Fatalf("Nutzlast lesen: %v", err)
	}
	if report.Model != "claude-opus-5" {
		t.Fatalf("Modell %q, want die ID als Ersatz", report.Model)
	}
}

func TestStatusReportRoundTripsThroughDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "status-reports")
	now := time.Date(2026, 9, 2, 13, 0, 0, 0, time.UTC)
	report, _ := StatusReportFromClaudePayload([]byte(claudeStatusPayload), now)
	if err := WriteStatusReport(dir, report); err != nil {
		t.Fatalf("schreiben: %v", err)
	}
	report.ContextPercent = 42
	report.At = now.Add(time.Second)
	if err := WriteStatusReport(dir, report); err != nil {
		t.Fatalf("erneut schreiben: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kaputt.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteStatusReport(dir, StatusReport{}); err == nil {
		t.Fatal("Report ohne Laufzeit wurde geschrieben")
	}
	reports := ReadStatusReports(dir)
	if len(reports) != 1 {
		t.Fatalf("%d Reports gelesen, want genau den letzten Stand: %+v", len(reports), reports)
	}
	if reports[0].ContextPercent != 42 || !reports[0].At.Equal(now.Add(time.Second)) {
		t.Fatalf("alter Stand überlebte: %+v", reports[0])
	}
	if got := statusReportFileName("mgt/dev pilot:3"); got != "mgt_dev_pilot_3.json" {
		t.Fatalf("Dateiname %q", got)
	}
}

func TestStatusReportsResolveToSessionsByRuntimeAndVendor(t *testing.T) {
	sessions := []Session{
		{ID: "s1", RuntimeName: "mgt-a", Vendor: AgentVendorClaude, AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-a"}}},
		{ID: "s2", RuntimeName: "mgt-b", Vendor: AgentVendorClaude, AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-b"}}},
		{ID: "s3", RuntimeName: "mgt-c", Vendor: AgentVendorCopilot},
	}
	now := time.Now()
	reports := []StatusReport{
		{Vendor: AgentVendorClaude, RuntimeName: "mgt-a", RunRef: "run-a", At: now, Model: "Fable 5.1"},
		{Vendor: AgentVendorClaude, RuntimeName: "mgt-b", RunRef: "run-nach-clear", At: now, Model: "Opus 5"},
		{Vendor: AgentVendorClaude, RuntimeName: "mgt-c", RunRef: "run-c", At: now, Model: "vor dem Vendor-Wechsel"},
		{Vendor: AgentVendorClaude, RuntimeName: "mgt-unbekannt", RunRef: "run-x", At: now},
		{Vendor: AgentVendorClaude, RuntimeName: "", RunRef: "run-y", At: now},
	}
	resolved := StatusReportsForSessions(reports, sessions)
	if len(resolved) != 2 {
		t.Fatalf("%d Zuordnungen, want die beiden Claude-Panes: %+v", len(resolved), resolved)
	}
	if resolved["s1"].Model != "Fable 5.1" {
		t.Fatalf("Session s1 bekam %+v", resolved["s1"])
	}
	if resolved["s2"].Model != "Opus 5" {
		t.Fatalf("der Lauf nach /clear wurde verworfen: %+v", resolved["s2"])
	}
	if _, leaked := resolved["s3"]; leaked {
		t.Fatalf("der Claude-Report landete bei der Copilot-Session: %+v", resolved["s3"])
	}
}

func TestStatusLineTextShowsMeterModelEffortAndCost(t *testing.T) {
	report, err := StatusReportFromClaudePayload([]byte(claudeStatusPayload), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := StatusLineText(report)
	for _, want := range []string{"\x1b[32m🧠 ▰▱▱▱▱▱▱▱▱▱ 10%", "🤖 Fable 5.1", "⚡⚡⚡⚡ xhigh", "$2.38"} {
		if !strings.Contains(got, want) {
			t.Errorf("Statuszeile %q enthält %q nicht", got, want)
		}
	}
	if strings.Contains(got, "\n") {
		t.Errorf("Statuszeile %q ist mehrzeilig", got)
	}
}

func TestStatusLineTextColorsByContextAndSkipsUnknownFacts(t *testing.T) {
	warn := StatusLineText(StatusReport{ContextPercent: 64.6, Model: "Opus 5", FastMode: true, Effort: "turbo"})
	if !strings.HasPrefix(warn, "\x1b[33m🧠 ▰▰▰▰▰▰▰▱▱▱ 65%") {
		t.Errorf("Statuszeile bei 65%% beginnt mit %q", warn)
	}
	for _, want := range []string{"🤖 Opus 5 · fast", "⚡ turbo"} {
		if !strings.Contains(warn, want) {
			t.Errorf("Statuszeile %q enthält %q nicht", warn, want)
		}
	}
	if strings.Contains(warn, "$") {
		t.Errorf("Statuszeile %q nennt Kosten ohne Kosten", warn)
	}
	full := StatusLineText(StatusReport{ContextPercent: 140})
	if full != "\x1b[31m🧠 ▰▰▰▰▰▰▰▰▰▰ 100%\x1b[0m" {
		t.Errorf("Statuszeile ohne Modell = %q", full)
	}
}
