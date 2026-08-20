package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkHistoryNormalizesAllProviderAdapters(t *testing.T) {
	history, home, indexDir, codexHome := openTestWorkHistory(t)
	const cwd = "/work/demo"

	claudePath := filepath.Join(home, ".claude", "projects", "-work-demo", "claude-1.jsonl")
	writeHistoryTestFile(t, claudePath, strings.Join([]string{
		`{"type":"user","uuid":"cl-u","timestamp":"2026-08-19T10:00:00Z","cwd":"/work/demo","sessionId":"claude-1","message":{"role":"user","content":"Claude prompt"}}`,
		`{"type":"assistant","uuid":"cl-a","timestamp":"2026-08-19T10:00:01Z","cwd":"/work/demo","sessionId":"claude-1","message":{"role":"assistant","model":"claude-opus","content":"Claude output https://example.test/shared","usage":{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}}`,
	}, "\n")+"\n")

	codexPath := filepath.Join(codexHome, "sessions", "2026", "rollout-codex-1.jsonl")
	writeHistoryTestFile(t, codexPath, strings.Join([]string{
		`{"type":"session_meta","timestamp":"2026-08-19T11:00:00Z","payload":{"id":"codex-1","cwd":"/work/demo","model":"gpt-5"}}`,
		`{"type":"event_msg","timestamp":"2026-08-19T11:00:01Z","payload":{"type":"user_message","message":"Codex prompt"}}`,
		`{"type":"response_item","timestamp":"2026-08-19T11:00:01Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Codex prompt"}]}}`,
		`{"type":"response_item","timestamp":"2026-08-19T11:00:02Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Codex output"}]}}`,
		`{"type":"event_msg","timestamp":"2026-08-19T11:00:03Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":13,"output_tokens":5,"cached_input_tokens":2}}}}`,
	}, "\n")+"\n")

	geminiPath := filepath.Join(home, ".gemini", "tmp", "project-hash", "chats", "session-gemini-1.json")
	writeHistoryTestFile(t, geminiPath, `{
  "sessionId":"gemini-1",
  "messages":[
    {"id":"ge-u","type":"user","timestamp":"2026-08-19T12:00:00Z","content":"Gemini prompt"},
    {"id":"ge-a","type":"gemini","timestamp":"2026-08-19T12:00:01Z","content":"Gemini output","model":"gemini-2.5-pro","usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":9,"cachedContentTokenCount":4}}
  ]
}`)

	copilotDir := filepath.Join(home, ".copilot", "session-state", "copilot-1")
	writeHistoryTestFile(t, filepath.Join(copilotDir, "workspace.yaml"), "cwd: /work/demo\n")
	writeHistoryTestFile(t, filepath.Join(copilotDir, "events.jsonl"), strings.Join([]string{
		`{"id":"co-u","type":"user.message","timestamp":"2026-08-19T13:00:00Z","data":{"content":"Copilot prompt"}}`,
		`{"id":"co-a","type":"assistant.response","timestamp":"2026-08-19T13:00:01Z","data":{"content":"Copilot output https://example.test/shared","model":"gpt-4.1","usage":{"inputTokens":19,"outputTokens":8}}}`,
	}, "\n")+"\n")

	associations := HistoryAssociations{
		Revision: "registry-7",
		Projects: []HistoryProjectAssociation{{Key: "project-1", Name: "Demo", Path: cwd}},
		Sessions: []HistorySessionAssociation{
			{Key: "session-claude", Name: "Claude", ProjectKey: "project-1", Dir: cwd, Provider: HistoryProviderClaude, ConversationID: "claude-1"},
			{Key: "session-codex", Name: "Codex", ProjectKey: "project-1", Dir: cwd, Provider: HistoryProviderCodex, ConversationID: "codex-1"},
			{Key: "session-gemini", Name: "Gemini", ProjectKey: "project-1", Dir: cwd, Provider: HistoryProviderGemini, ConversationID: "gemini-1"},
			{Key: "session-copilot", Name: "Copilot", ProjectKey: "project-1", Dir: cwd, Provider: HistoryProviderCopilot, ConversationID: "copilot-1"},
		},
	}

	page, err := history.Events(context.Background(), associations, HistoryEventQuery{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 9 {
		t.Fatalf("normalized events = %d, want 9: %#v", page.Total, page.Events)
	}
	if page.Meta.AssociationRevision != "registry-7" {
		t.Fatalf("association revision = %q", page.Meta.AssociationRevision)
	}
	for _, provider := range historyProviders {
		coverage := historyTestCoverage(t, page.Meta, provider)
		if coverage.State != HistorySourceAvailable || coverage.ParsedFiles != 1 || coverage.IndexedFiles != 1 {
			t.Fatalf("%s coverage = %#v", provider, coverage)
		}
		if countHistoryEvents(page.Events, provider, HistoryEventPrompt) != 1 {
			t.Fatalf("%s prompt normalization did not deduplicate to one event", provider)
		}
	}
	for _, event := range page.Events {
		if event.Attribution.ProjectKey.State != HistoryFactKnown || event.Attribution.ProjectKey.Value != "project-1" {
			t.Fatalf("event %s project attribution = %#v", event.ID, event.Attribution)
		}
		if event.Attribution.SessionKey.State != HistoryFactKnown {
			t.Fatalf("event %s has no provider-qualified Session attribution: %#v", event.ID, event.Attribution)
		}
		if !strings.HasPrefix(event.ID, string(event.Provider)+":event:") || !strings.HasPrefix(event.SourceID, string(event.Provider)+":") {
			t.Fatalf("event identity is not provider-qualified: %#v", event)
		}
	}
	claudeOutput := findHistoryEvent(t, page.Events, HistoryProviderClaude, HistoryEventOutput)
	if claudeOutput.Model.State != HistoryFactKnown || claudeOutput.Model.Value != "claude-opus" {
		t.Fatalf("Claude model = %#v", claudeOutput.Model)
	}
	if claudeOutput.Usage.InputTokens.State != HistoryFactKnown || claudeOutput.Usage.InputTokens.Value != 11 {
		t.Fatalf("Claude input usage = %#v", claudeOutput.Usage.InputTokens)
	}

	links, err := history.Links(context.Background(), associations, HistoryLinkQuery{Distinct: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if links.Total != 1 || len(links.Links) != 1 || links.Links[0].URL != "https://example.test/shared" {
		t.Fatalf("distinct links = %#v", links)
	}

	indexInfo, err := os.Stat(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	if indexInfo.Mode().Perm() != 0o700 {
		t.Fatalf("index directory mode = %o, want 700", indexInfo.Mode().Perm())
	}
	for _, name := range []string{"index.json", "index.lock"} {
		info, err := os.Stat(filepath.Join(indexDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
	}
	indexData, err := os.ReadFile(filepath.Join(indexDir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sourcePath := range []string{claudePath, codexPath, geminiPath, filepath.Join(copilotDir, "events.jsonl")} {
		if strings.Contains(string(indexData), sourcePath) {
			t.Fatalf("private index persisted source path %q", sourcePath)
		}
	}
}

func TestWorkHistoryIncrementalCheckpointAndSourceDeletion(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	source := filepath.Join(home, ".claude", "projects", "demo", "conversation.jsonl")
	first := `{"type":"user","timestamp":"2026-08-19T10:00:00Z","sessionId":"conversation","message":{"content":"first prompt"}}` + "\n"
	writeHistoryTestFile(t, source, first)

	page1, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	cov1 := historyTestCoverage(t, page1.Meta, HistoryProviderClaude)
	if page1.Total != 1 || cov1.ParsedFiles != 1 || cov1.ReusedFiles != 0 {
		t.Fatalf("initial refresh = total %d, coverage %#v", page1.Total, cov1)
	}

	page2, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	cov2 := historyTestCoverage(t, page2.Meta, HistoryProviderClaude)
	if page2.Meta.Revision != page1.Meta.Revision || cov2.ParsedFiles != 0 || cov2.ReusedFiles != 1 {
		t.Fatalf("unchanged refresh reparsed source: revision %d -> %d, coverage %#v", page1.Meta.Revision, page2.Meta.Revision, cov2)
	}

	file, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(`{"type":"user","timestamp":"2026-08-19T10:01:00Z","sessionId":"conversation","message":{"content":"second prompt"}}` + "\n")
	closeErr := file.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	page3, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	cov3 := historyTestCoverage(t, page3.Meta, HistoryProviderClaude)
	if page3.Total != 2 || page3.Meta.Revision <= page2.Meta.Revision || cov3.ParsedFiles != 1 || cov3.ReusedFiles != 0 {
		t.Fatalf("changed refresh = total %d, revision %d, coverage %#v", page3.Total, page3.Meta.Revision, cov3)
	}

	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	page4, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	cov4 := historyTestCoverage(t, page4.Meta, HistoryProviderClaude)
	if page4.Total != 0 || page4.Meta.Revision <= page3.Meta.Revision || cov4.State != HistorySourceAvailable || cov4.IndexedFiles != 0 {
		t.Fatalf("deleted source remained indexed: total %d, revision %d, coverage %#v", page4.Total, page4.Meta.Revision, cov4)
	}
}

func TestWorkHistoryReattributesWithoutReparsing(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	source := filepath.Join(home, ".claude", "projects", "demo", "conversation.jsonl")
	writeHistoryTestFile(t, source, `{"type":"user","timestamp":"2026-08-19T10:00:00Z","cwd":"/work/demo","sessionId":"conversation","message":{"content":"rename-safe prompt"}}`+"\n")

	oldAssociations := HistoryAssociations{
		Revision: "before",
		Projects: []HistoryProjectAssociation{{Key: "project-id", Name: "Old project", Path: "/work/demo"}},
		Sessions: []HistorySessionAssociation{{Key: "session-id", Name: "Old session", ProjectKey: "project-id", Dir: "/work/demo", Provider: HistoryProviderClaude, ConversationID: "conversation"}},
	}
	before, err := history.Events(context.Background(), oldAssociations, HistoryEventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	newAssociations := oldAssociations
	newAssociations.Revision = "after"
	newAssociations.Projects = []HistoryProjectAssociation{{Key: "project-id", Name: "Renamed project", Path: "/work/demo"}}
	newAssociations.Sessions = []HistorySessionAssociation{{Key: "session-id", Name: "Renamed session", ProjectKey: "project-id", Dir: "/work/demo", Provider: HistoryProviderClaude, ConversationID: "conversation"}}
	after, err := history.Events(context.Background(), newAssociations, HistoryEventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	cov := historyTestCoverage(t, after.Meta, HistoryProviderClaude)
	if after.Meta.Revision != before.Meta.Revision || cov.ParsedFiles != 0 || cov.ReusedFiles != 1 {
		t.Fatalf("Registry-only rename invalidated source index: %#v", cov)
	}
	if after.Events[0].Attribution.ProjectName.Value != "Renamed project" || after.Events[0].Attribution.SessionName.Value != "Renamed session" {
		t.Fatalf("query-time attribution was stale: %#v", after.Events[0].Attribution)
	}
}

func TestWorkHistorySummaryPreservesUnknownUsage(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	writeHistoryTestFile(t, filepath.Join(home, ".claude", "projects", "demo", "known.jsonl"), `{"type":"assistant","timestamp":"2026-08-19T10:00:00Z","sessionId":"known","message":{"model":"claude-opus","content":"known https://example.test/one","usage":{"input_tokens":11,"output_tokens":7}}}`+"\n")
	copilotDir := filepath.Join(home, ".copilot", "session-state", "unknown")
	writeHistoryTestFile(t, filepath.Join(copilotDir, "events.jsonl"), `{"id":"unknown","type":"assistant.response","timestamp":"2026-08-19T11:00:00Z","data":{"content":"unknown usage https://example.test/two","model":"gpt-4.1"}}`+"\n")

	summary, err := history.Summarize(context.Background(), HistoryAssociations{}, HistorySummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	input := summary.Totals.Usage.InputTokens
	if input.Value != 11 || input.KnownEvents != 1 || input.UnknownEvents != 1 || input.Coverage != HistoryCoveragePartial {
		t.Fatalf("input usage collapsed unknown into zero: %#v", input)
	}
	cache := summary.Totals.Usage.CacheReadTokens
	if cache.Value != 0 || cache.KnownEvents != 0 || cache.UnknownEvents != 2 || cache.Coverage != HistoryCoverageNone {
		t.Fatalf("unknown cache usage = %#v", cache)
	}
	if summary.Totals.Outputs != 2 || len(summary.Models) != 2 {
		t.Fatalf("summary did not retain provider/model facts: %#v", summary)
	}
}

func TestWorkHistoryMalformedRecordsArePartialNotEmpty(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	source := filepath.Join(home, ".claude", "projects", "demo", "partial.jsonl")
	writeHistoryTestFile(t, source, "{not-json}\n"+`{"type":"user","timestamp":"2026-08-19T10:00:00Z","sessionId":"partial","message":{"content":"valid prompt"}}`+"\n")

	page, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	coverage := historyTestCoverage(t, page.Meta, HistoryProviderClaude)
	if page.Total != 1 || coverage.State != HistorySourcePartial || len(coverage.Problems) != 1 || coverage.Problems[0].Kind != "malformed-records" {
		t.Fatalf("partial provider was represented as empty or available: total %d, coverage %#v", page.Total, coverage)
	}
}

func TestHistoryAssociationsFromStateUsesDurableIDsAndAgentRuns(t *testing.T) {
	state := &State{
		Projects: []Project{{ID: ProjectID("project-id"), Name: "Display project", Path: "/work/demo"}},
		Agents: []Session{{
			ID: SessionID("session-id"), Name: "Display session", ProjectID: ProjectID("project-id"), Project: "legacy-project", Dir: "/work/demo",
			AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "claude-run"}, {Vendor: AgentVendorCodex, ExternalID: "codex-run"}},
		}},
	}
	associations := HistoryAssociationsFromState(state)
	if len(associations.Projects) != 1 || associations.Projects[0].Key != "project-id" {
		t.Fatalf("project association did not use durable ID: %#v", associations.Projects)
	}
	if len(associations.Sessions) != 2 {
		t.Fatalf("provider run associations = %#v", associations.Sessions)
	}
	for _, association := range associations.Sessions {
		if association.Key != "session-id" || association.ProjectKey != "project-id" {
			t.Fatalf("session association did not use durable IDs: %#v", association)
		}
	}
	if associations.Sessions[0].Provider == associations.Sessions[1].Provider || associations.Sessions[0].ConversationID == associations.Sessions[1].ConversationID {
		t.Fatalf("AgentRuns were not kept provider-qualified: %#v", associations.Sessions)
	}

	legacy := HistoryAssociationsFromState(&State{
		Projects: []Project{{Name: "legacy-project", Path: "/legacy"}},
		Agents:   []Session{{Name: "legacy-session", Project: "legacy-project", Dir: "/legacy", SessionID: "legacy-claude"}},
	})
	if legacy.Projects[0].Key != "legacy-project" || legacy.Sessions[0].Key != "legacy-session" || legacy.Sessions[0].ProjectKey != "legacy-project" || legacy.Sessions[0].Provider != HistoryProviderClaude {
		t.Fatalf("legacy association fallback = %#v", legacy)
	}
}

func TestHistoryLocationFallbackTreatsMultiProviderRunsAsOneSession(t *testing.T) {
	associations := HistoryAssociations{
		Projects: []HistoryProjectAssociation{{Key: "project-id", Name: "Project", Path: "/work/demo"}},
		Sessions: []HistorySessionAssociation{
			{Key: "session-id", Name: "Session", ProjectKey: "project-id", Dir: "/work/demo", Provider: HistoryProviderClaude, ConversationID: "claude-run"},
			{Key: "session-id", Name: "Session", ProjectKey: "project-id", Dir: "/work/demo", Provider: HistoryProviderCodex, ConversationID: "codex-run"},
		},
	}
	resolver := newHistoryAssociationResolver(associations)

	got := resolver.resolve(historyRecord{
		Provider: HistoryProviderCodex,
		// A source can know its CWD while its conversation ID is absent or does
		// not yet match the Registry binding.
		ConversationID: "unbound-run",
		CWD:            "/work/demo/subdirectory",
	})
	if got.SessionKey.State != HistoryFactKnown || got.SessionKey.Value != "session-id" ||
		got.ProjectKey.State != HistoryFactKnown || got.ProjectKey.Value != "project-id" {
		t.Fatalf("multi-provider location attribution = %#v", got)
	}
}

func openTestWorkHistory(t *testing.T) (*WorkHistory, string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	indexDir := filepath.Join(root, "private-index")
	codexHome := filepath.Join(root, "codex-home")
	history, err := OpenWorkHistory(WorkHistoryConfig{HomeDir: home, IndexDir: indexDir, CodexHome: codexHome})
	if err != nil {
		t.Fatal(err)
	}
	return history, home, indexDir, codexHome
}

func writeHistoryTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func historyTestCoverage(t *testing.T, meta HistoryMeta, provider HistoryProvider) HistoryProviderCoverage {
	t.Helper()
	for _, coverage := range meta.Coverage {
		if coverage.Provider == provider {
			return coverage
		}
	}
	t.Fatalf("coverage for %s missing: %#v", provider, meta.Coverage)
	return HistoryProviderCoverage{}
}

func countHistoryEvents(events []HistoryEvent, provider HistoryProvider, kind HistoryEventKind) int {
	count := 0
	for _, event := range events {
		if event.Provider == provider && event.Kind == kind {
			count++
		}
	}
	return count
}

func findHistoryEvent(t *testing.T, events []HistoryEvent, provider HistoryProvider, kind HistoryEventKind) HistoryEvent {
	t.Helper()
	for _, event := range events {
		if event.Provider == provider && event.Kind == kind {
			return event
		}
	}
	t.Fatalf("event %s/%s missing", provider, kind)
	return HistoryEvent{}
}
