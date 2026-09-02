package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	// Das Write-Ahead-Log und der geteilte Speicher tragen denselben Inhalt wie
	// die Datenbank und müssen deshalb genauso geschützt sein.
	for _, name := range []string{"history.db", "history.db-wal", "history.db-shm", "index.lock"} {
		info, err := os.Stat(filepath.Join(indexDir, name))
		if os.IsNotExist(err) && name != "history.db" && name != "index.lock" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
	}
	// Erst schließen: solange der Speicher offen ist, steht das meiste im
	// Write-Ahead-Log und history.db enthielte fast nichts.
	if err := history.Close(); err != nil {
		t.Fatal(err)
	}
	assertHistoryStoreOmits(t, indexDir, claudePath, codexPath, geminiPath, filepath.Join(copilotDir, "events.jsonl"))
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

func TestNewHistoryAssociationsUsesDurableIDsAndAgentRuns(t *testing.T) {
	state := &State{
		Revision: 7,
		Projects: []Project{{ID: ProjectID("project-id"), Name: "Display project", Path: "/work/demo"}},
		Agents: []Session{{
			ID: SessionID("session-id"), Name: "Display session", ProjectID: ProjectID("project-id"), Project: "legacy-project", Dir: "/work/demo",
			AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "claude-run"}, {Vendor: AgentVendorCodex, ExternalID: "codex-run"}},
		}},
	}
	associations := NewHistoryAssociations(*state)
	if associations.Revision != "registry:7" {
		t.Fatalf("association revision = %q, want registry:7", associations.Revision)
	}
	if len(associations.Projects) != 1 || associations.Projects[0].Key != "project-id" {
		t.Fatalf("project association did not use durable ID: %#v", associations.Projects)
	}
	if len(associations.Sessions) != 2 {
		t.Fatalf("provider run associations = %#v", associations.Sessions)
	}
	for _, association := range associations.Sessions {
		if association.Key != "session-id" || association.ProjectKey != "project-id" || association.LocationEvidence != HistoryLocationProviderRun {
			t.Fatalf("session association did not use durable IDs: %#v", association)
		}
	}
	if associations.Sessions[0].Provider == associations.Sessions[1].Provider || associations.Sessions[0].ConversationID == associations.Sessions[1].ConversationID {
		t.Fatalf("AgentRuns were not kept provider-qualified: %#v", associations.Sessions)
	}

	legacy := NewHistoryAssociations(State{
		Projects: []Project{{Name: "legacy-project", Path: "/legacy"}},
		Agents:   []Session{{Name: "legacy-session", Project: "legacy-project", Dir: "/legacy", SessionID: "legacy-claude"}},
	})
	if legacy.Revision != "" || legacy.Projects[0].Key != "legacy-project" || legacy.Sessions[0].Key != "legacy-session" || legacy.Sessions[0].ProjectKey != "legacy-project" || legacy.Sessions[0].Provider != HistoryProviderClaude || legacy.Sessions[0].LocationEvidence != HistoryLocationProviderRun {
		t.Fatalf("legacy association fallback = %#v", legacy)
	}
}

func TestNewHistoryAssociationsExcludesTerminalAndAgentlessLocationClaims(t *testing.T) {
	state := State{
		Revision: 12,
		Projects: []Project{{ID: "project-id", Name: "Project", Path: "/work/project"}},
		Agents: []Session{
			{
				ID: "provider-parent", Name: "Provider parent", ProjectID: "project-id", Project: "Project", Dir: "/work/project",
				SessionKind: SessionKindCodingAgent, AgentRuns: []AgentRunRef{{Vendor: AgentVendorCodex, ExternalID: "parent-run"}},
			},
			{
				ID: "terminal-child", Name: "Terminal child", ProjectID: "project-id", Project: "Project", Dir: "/work/project/nested",
				SessionKind: SessionKindTerminal,
				// Even malformed legacy data must not turn a terminal into an
				// owner of coding-agent history.
				AgentRuns: []AgentRunRef{{Vendor: AgentVendorCodex, ExternalID: "terminal-run"}},
			},
			{
				ID: "agentless-child", Name: "Agentless child", ProjectID: "project-id", Project: "Project", Dir: "/work/project/nested/deeper",
				SessionKind: SessionKindCodingAgent,
			},
		},
	}
	associations := NewHistoryAssociations(state)
	if len(associations.Sessions) != 1 || associations.Sessions[0].Key != "provider-parent" {
		t.Fatalf("terminal or agentless Session emitted a location claim: %#v", associations.Sessions)
	}

	got := newHistoryAssociationResolver(associations).resolve(historyRecord{
		Provider: HistoryProviderCodex, ConversationID: "unbound-run",
		CWD: "/work/project/nested/deeper/source",
	})
	if got.SessionKey.State != HistoryFactKnown || got.SessionKey.Value != "provider-parent" {
		t.Fatalf("nested terminal or agentless child stole provider event: %#v", got)
	}

	state.Agents = state.Agents[1:]
	withoutQualifiedParent := NewHistoryAssociations(state)
	unknown := newHistoryAssociationResolver(withoutQualifiedParent).resolve(historyRecord{
		Provider: HistoryProviderCodex, CWD: "/work/project/nested/deeper/source",
	})
	if unknown.SessionKey.State != HistoryFactUnknown {
		t.Fatalf("agentless location fabricated Session attribution: %#v", unknown)
	}
}

func TestHistoryLocationFallbackTreatsMultiProviderRunsAsOneSession(t *testing.T) {
	associations := HistoryAssociations{
		Projects: []HistoryProjectAssociation{{Key: "project-id", Name: "Project", Path: "/work/demo"}},
		Sessions: []HistorySessionAssociation{
			{Key: "session-id", Name: "Session", ProjectKey: "project-id", Dir: "/work/demo", Provider: HistoryProviderClaude, ConversationID: "claude-run", LocationEvidence: HistoryLocationProviderRun},
			{Key: "session-id", Name: "Session", ProjectKey: "project-id", Dir: "/work/demo", Provider: HistoryProviderCodex, ConversationID: "codex-run", LocationEvidence: HistoryLocationProviderRun},
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

func TestHistoryLocationFallbackRanksOnlyProviderCompatibleSessions(t *testing.T) {
	resolver := newHistoryAssociationResolver(HistoryAssociations{
		Projects: []HistoryProjectAssociation{{Key: "project-id", Name: "Project", Path: "/work/project"}},
		Sessions: []HistorySessionAssociation{
			{Key: "claude-child", Name: "Claude child", ProjectKey: "project-id", Dir: "/work/project/sub", Provider: HistoryProviderClaude, ConversationID: "claude-run", LocationEvidence: HistoryLocationProviderRun},
			{Key: "unqualified-deep", Name: "No run evidence", ProjectKey: "project-id", Dir: "/work/project/sub/deeper"},
			{Key: "codex-parent", Name: "Codex parent", ProjectKey: "project-id", Dir: "/work/project", Provider: HistoryProviderCodex, ConversationID: "codex-run", LocationEvidence: HistoryLocationProviderRun},
		},
	})

	got := resolver.resolve(historyRecord{Provider: HistoryProviderCodex, CWD: "/work/project/sub/deeper"})
	if got.SessionKey.State != HistoryFactKnown || got.SessionKey.Value != "codex-parent" {
		t.Fatalf("nested incompatible Session won provider-qualified path ranking: %#v", got)
	}
}

func TestSharedWorkHistoryReturnsOneInstance(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAGENTIC_STATE", filepath.Join(dir, "state.json"))
	resetSharedWorkHistoryForTest()
	first, err := SharedWorkHistory()
	if err != nil {
		t.Fatal(err)
	}
	second, err := SharedWorkHistory()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("SharedWorkHistory returned two instances")
	}
}

func openTestWorkHistory(t *testing.T) (*WorkHistory, string, string, string) {
	t.Helper()
	return openTestWorkHistoryWith(t, WorkHistoryConfig{})
}

// openTestWorkHistoryWith legt die Verzeichnisse an und ergänzt die Vorgaben,
// die jeder Test braucht: Tests arbeiten mit festen Zeitstempeln in der
// Vergangenheit und erwarten vollständige, sofort sichtbare Ergebnisse. Ein
// Aufbewahrungsfenster darf der Aufrufer vorgeben; nachträglich an der
// Konfiguration zu drehen wäre ein Datenrennen mit dem Indexlauf.
func openTestWorkHistoryWith(t *testing.T, config WorkHistoryConfig) (*WorkHistory, string, string, string) {
	t.Helper()
	root := t.TempDir()
	config.HomeDir = filepath.Join(root, "home")
	config.IndexDir = filepath.Join(root, "private-index")
	config.CodexHome = filepath.Join(root, "codex-home")
	if config.Retention == 0 {
		config.Retention = 100 * 365 * 24 * time.Hour
	}
	config.SynchronousIndex = true
	history, err := OpenWorkHistory(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { history.Close() })
	return history, config.HomeDir, config.IndexDir, config.CodexHome
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

// assertHistoryStoreOmits prüft jede vorhandene Datei des Speichers darauf, dass
// sie die übergebenen Pfade nicht enthält. Das Write-Ahead-Log gehört
// ausdrücklich dazu: solange der Speicher offen ist, steht fast alles dort und
// nicht in history.db. Der Aufrufer schließt den Speicher deshalb vorher.
func assertHistoryStoreOmits(t *testing.T, indexDir string, paths ...string) {
	t.Helper()
	read := 0
	for _, name := range []string{"history.db", "history.db-wal", "history.db-shm"} {
		data, err := os.ReadFile(filepath.Join(indexDir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		read++
		for _, path := range paths {
			if strings.Contains(string(data), path) {
				t.Fatalf("private index persisted source path %q in %s", path, name)
			}
		}
	}
	if read == 0 {
		t.Fatalf("no work history store file found in %s", indexDir)
	}
}

func touchHistoryTestFile(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func removeHistoryTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
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
