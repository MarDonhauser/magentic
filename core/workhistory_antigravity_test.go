package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func writeAntigravityTranscript(t *testing.T, home, conversation, sidecar, content string) string {
	t.Helper()
	if sidecar != "" {
		writeHistoryTestFile(t, filepath.Join(home, ".gemini", "antigravity-cli", "history.jsonl"), sidecar)
	}
	path := filepath.Join(home, ".gemini", "antigravity-cli", "brain", conversation, ".system_generated", "logs", "transcript.jsonl")
	writeHistoryTestFile(t, path, content)
	return path
}

func antigravityTestEvents(t *testing.T, history *WorkHistory, provider HistoryProvider) []HistoryEvent {
	t.Helper()
	page, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{
		Providers: []HistoryProvider{provider}, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return page.Events
}

// Der Prompt steht in <USER_REQUEST>, umhüllt von Zeit- und
// Modellwechsel-Metadaten, die nicht in den Index gehören. Die Antwort steht
// in PLANNER_RESPONSE; Werkzeugergebnisse (RUN_COMMAND und Co.) sind keine
// Assistenzaussagen und bleiben draußen.
func TestAntigravityAdapterParsesPromptAndOutput(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	writeAntigravityTranscript(t, home, "agy-1",
		`{"conversationId":"agy-1","display":"Bitte deploy das Update","timestamp":1787205600000,"workspace":"/work/demo"}`+"\n",
		strings.Join([]string{
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-08-19T14:00:00Z","content":"<USER_REQUEST>\nBitte deploy das Update.\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is 2026-08-19.\n</ADDITIONAL_METADATA>\n<USER_SETTINGS_CHANGE>\nThe user changed setting ` + "`Model Selection`" + ` from None to Gemini. No need to comment.\n</USER_SETTINGS_CHANGE>"}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-08-19T14:00:01Z","content":"Verstanden, ich deploye jetzt."}`,
			`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-08-19T14:00:02Z","tool_calls":[{"name":"list_dir","args":{"DirectoryPath":"\"/work/demo\""}}]}`,
			`{"step_index":3,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-08-19T14:00:03Z","content":"tool output stays out of the index"}`,
			`{"step_index":4,"source":"SYSTEM","type":"CHECKPOINT","status":"DONE","created_at":"2026-08-19T14:00:04Z","content":"truncated context summary"}`,
		}, "\n")+"\n")

	events := antigravityTestEvents(t, history, HistoryProviderAntigravity)
	if len(events) != 2 {
		t.Fatalf("antigravity events = %d, want prompt plus output: %#v", len(events), events)
	}
	// Neueste zuerst: output, dann prompt.
	output, prompt := events[0], events[1]
	if prompt.Kind != HistoryEventPrompt || prompt.Role != HistoryRoleDeveloper {
		t.Fatalf("prompt = %#v", prompt)
	}
	if prompt.Text.State != HistoryFactKnown || prompt.Text.Value != "Bitte deploy das Update." {
		t.Fatalf("prompt text = %#v", prompt.Text)
	}
	if output.Kind != HistoryEventOutput || output.Role != HistoryRoleAssistant {
		t.Fatalf("output = %#v", output)
	}
	if output.Text.State != HistoryFactKnown || output.Text.Value != "Verstanden, ich deploye jetzt." {
		t.Fatalf("output text = %#v", output.Text)
	}
	for _, event := range events {
		if event.ConversationID.State != HistoryFactKnown || event.ConversationID.Value != "agy-1" {
			t.Fatalf("conversation = %#v", event.ConversationID)
		}
		if event.CWD.State != HistoryFactKnown || event.CWD.Value != "/work/demo" {
			t.Fatalf("cwd = %#v, want sidecar workspace", event.CWD)
		}
		if event.OccurredAt.State != HistoryFactKnown {
			t.Fatalf("occurredAt = %#v, want created_at", event.OccurredAt)
		}
	}
	if output.Model.State != HistoryFactUnknown {
		t.Fatalf("model = %#v, want unknown: agy reports none", output.Model)
	}
}

// Meldet der Sidecar die Konversation nicht, nennt das Transkript selbst den
// Arbeitsbereich: run_command trägt sein Cwd, einen Anführungszeichen-Layer
// zu viel inklusive.
func TestAntigravityAdapterFallsBackToTranscriptPaths(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	writeAntigravityTranscript(t, home, "agy-fallback", "",
		strings.Join([]string{
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-08-19T14:00:00Z","content":"<USER_REQUEST>\nSuche etwas.\n</USER_REQUEST>"}`,
			`{"step_index":1,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-08-19T14:00:01Z","content":"listing","tool_calls":[{"name":"run_command","args":{"Cwd":"\"/work/fallback\"","CommandLine":"ls"}}]}`,
		}, "\n")+"\n")

	events := antigravityTestEvents(t, history, HistoryProviderAntigravity)
	if len(events) != 1 {
		t.Fatalf("antigravity events = %d, want 1: %#v", len(events), events)
	}
	if events[0].CWD.State != HistoryFactKnown || events[0].CWD.Value != "/work/fallback" {
		t.Fatalf("cwd = %#v, want transcript run_command Cwd unquoted", events[0].CWD)
	}
}

// transcript_full.jsonl dubliert dieselben Schritte mit anders quotierten
// Tool-Argumenten und wird nie indexiert.
func TestAntigravityAdapterIgnoresTranscriptFull(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	lines := strings.Join([]string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-08-19T14:00:00Z","content":"<USER_REQUEST>\nEin Prompt.\n</USER_REQUEST>"}`,
	}, "\n") + "\n"
	path := writeAntigravityTranscript(t, home, "agy-dedup", "", lines)
	writeHistoryTestFile(t, filepath.Join(filepath.Dir(path), "transcript_full.jsonl"), lines)

	events := antigravityTestEvents(t, history, HistoryProviderAntigravity)
	if count := countHistoryEvents(events, HistoryProviderAntigravity, HistoryEventPrompt); count != 1 {
		t.Fatalf("antigravity prompts = %d, want 1: transcript_full must not double", count)
	}
}

// Kaputte Zeilen werden gezählt und gemeldet, gültige daneben gelesen.
func TestAntigravityAdapterReportsMalformedLines(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	writeAntigravityTranscript(t, home, "agy-broken", "",
		"{kein json}\n"+
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-08-19T14:00:00Z","content":"<USER_REQUEST>\nTrotzdem lesbar.\n</USER_REQUEST>"}`+"\n")

	events := antigravityTestEvents(t, history, HistoryProviderAntigravity)
	if len(events) != 1 {
		t.Fatalf("antigravity events = %d, want 1: %#v", len(events), events)
	}
	page, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{
		Providers: []HistoryProvider{HistoryProviderAntigravity}, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	coverage := historyTestCoverage(t, page.Meta, HistoryProviderAntigravity)
	if coverage.State != HistorySourcePartial || len(coverage.Problems) == 0 {
		t.Fatalf("coverage = %#v, want partial with malformed-records problem", coverage)
	}
}

// Die Startform folgt dem Anbieter-Speicher: mit Brain-Verzeichnis wird die
// gespeicherte Konversation fortgesetzt, ohne startet agy frisch.
func TestStartCommandFollowsAntigravityStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	run := "agy-run-1"
	session := Session{
		Name: "navi", RuntimeName: "mgt-navi", SessionKind: SessionKindCodingAgent,
		Vendor: AgentVendorAntigravity, AgentRuns: []AgentRunRef{{Vendor: AgentVendorAntigravity, ExternalID: run}},
	}

	got, err := startCommandForSession(session, "resume")
	if err != nil {
		t.Fatal(err)
	}
	if got != "agy" {
		t.Fatalf("StartCommand ohne Konversation = %q, want frisch", got)
	}

	writeAntigravityTranscript(t, home, run, "", "")
	got, err = startCommandForSession(session, "resume")
	if err != nil {
		t.Fatal(err)
	}
	if want := "agy --conversation '" + run + "'"; got != want {
		t.Fatalf("StartCommand = %q, want %q", got, want)
	}
}
