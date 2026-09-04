package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkHistoryIndexerKeepsTranscriptPathsOutOfTheStore(t *testing.T) {
	history, home, indexDir, _ := openTestWorkHistory(t)
	sessionDir := filepath.Join(home, ".copilot", "session-state", "kaputt")
	events := filepath.Join(sessionDir, "events.jsonl")
	writeHistoryTestFile(t, events, `{"id":"co-u","type":"user.message","timestamp":"2026-08-30T13:00:00Z","data":{"content":"Prompt"}}`+"\n")
	// workspace.yaml als Verzeichnis: das Lesen der Abhängigkeit scheitert
	// unabhängig von Rechten und ausführendem Benutzer.
	if err := os.MkdirAll(filepath.Join(sessionDir, "workspace.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	meta, err := history.snapshotMeta(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	coverage := historyTestCoverage(t, meta, HistoryProviderCopilot)
	if coverage.State != HistorySourcePartial || len(coverage.Problems) != 1 ||
		coverage.Problems[0].Kind != "dependency-unreadable" {
		t.Fatalf("unlesbare Abhängigkeit wurde nicht als Problem gemeldet: %#v", coverage)
	}
	if err := history.Close(); err != nil {
		t.Fatal(err)
	}
	assertHistoryStoreOmits(t, indexDir, events, sessionDir)
}

func TestWorkHistoryIndexerParsesNewestFirstAndReportsProgress(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	for i, name := range []string{"alt.jsonl", "neu.jsonl"} {
		path := filepath.Join(home, ".claude", "projects", "-work-demo", name)
		writeHistoryTestFile(t, path, `{"type":"user","uuid":"u-`+name+`","timestamp":"2026-08-30T10:0`+string(rune('0'+i))+`:00Z","cwd":"/work/demo","sessionId":"conv-`+name+`","message":{"role":"user","content":"Prompt `+name+`"}}`+"\n")
	}
	// Die neuere Datei muss zuerst indexiert werden.
	touchHistoryTestFile(t, filepath.Join(home, ".claude", "projects", "-work-demo", "alt.jsonl"), time.Now().Add(-48*time.Hour))
	touchHistoryTestFile(t, filepath.Join(home, ".claude", "projects", "-work-demo", "neu.jsonl"), time.Now())

	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{Limit: 100, IncludeUnknownTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("events = %d, want 2", page.Total)
	}
	if page.Meta.Progress.Active {
		t.Fatalf("progress still active after a synchronous run: %#v", page.Meta.Progress)
	}
	if page.Meta.Progress.CompletedFiles != 2 {
		t.Fatalf("completed = %d, want 2", page.Meta.Progress.CompletedFiles)
	}
}

func TestSortHistoryCandidatesPutsNewestFirst(t *testing.T) {
	candidates := []historyIndexCandidate{
		{path: "/mittel.jsonl", modTime: 200},
		{path: "/alt.jsonl", modTime: 100},
		{path: "/neu.jsonl", modTime: 300},
		// Gleiche Änderungszeit: der Pfad entscheidet, damit die Reihenfolge
		// eines Laufs reproduzierbar bleibt.
		{path: "/b-gleich.jsonl", modTime: 200},
		{path: "/a-gleich.jsonl", modTime: 200},
	}
	sortHistoryCandidates(candidates)
	var got []string
	for _, candidate := range candidates {
		got = append(got, candidate.path)
	}
	want := []string{"/neu.jsonl", "/a-gleich.jsonl", "/b-gleich.jsonl", "/mittel.jsonl", "/alt.jsonl"}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestWorkHistoryIndexerReusesUnchangedSources(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	path := filepath.Join(home, ".claude", "projects", "-work-demo", "a.jsonl")
	writeHistoryTestFile(t, path, `{"type":"user","uuid":"u-1","timestamp":"2026-08-30T10:00:00Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"user","content":"Prompt"}}`+"\n")

	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	meta, err := history.snapshotMeta(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if coverage := historyTestCoverage(t, meta, HistoryProviderClaude); coverage.ParsedFiles != 1 {
		t.Fatalf("first run parsed = %d, want 1", coverage.ParsedFiles)
	}
	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	meta, err = history.snapshotMeta(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	coverage := historyTestCoverage(t, meta, HistoryProviderClaude)
	if coverage.ParsedFiles != 0 || coverage.ReusedFiles != 1 || coverage.IndexedFiles != 1 {
		t.Fatalf("second run coverage = %#v, want reuse", coverage)
	}
}

func TestWorkHistoryIndexerRemovesVanishedSources(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	path := filepath.Join(home, ".claude", "projects", "-work-demo", "a.jsonl")
	writeHistoryTestFile(t, path, `{"type":"user","uuid":"u-1","timestamp":"2026-08-30T10:00:00Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"user","content":"Prompt"}}`+"\n")
	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	removeHistoryTestFile(t, path)
	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("events after deletion = %d, want 0", page.Total)
	}
}

func TestWorkHistoryIndexerKeepsAggregatesBeyondRetention(t *testing.T) {
	history, home, _, _ := openTestWorkHistoryWith(t, WorkHistoryConfig{Retention: 24 * time.Hour})
	path := filepath.Join(home, ".claude", "projects", "-work-demo", "alt.jsonl")
	writeHistoryTestFile(t, path, `{"type":"user","uuid":"u-1","timestamp":"2026-01-05T10:00:00Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"user","content":"sehr alt"}}`+"\n")

	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("events outside retention = %d, want 0", page.Total)
	}
	var prompts int
	if err := history.store.db.QueryRow(`SELECT sum(prompts) FROM activity`).Scan(&prompts); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 {
		t.Fatalf("aggregated prompts = %d, want 1 — Aggregate müssen den Verfall überleben", prompts)
	}
}

func TestWorkHistoryActivityResolvesAttributionAfterRename(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	path := filepath.Join(home, ".claude", "projects", "-work-demo", "a.jsonl")
	writeHistoryTestFile(t, path, strings.Join([]string{
		`{"type":"user","uuid":"u-1","timestamp":"2026-08-30T10:00:00Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"user","content":"Prompt"}}`,
		`{"type":"assistant","uuid":"a-1","timestamp":"2026-08-30T10:00:05Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"assistant","model":"claude-opus","content":"Antwort","usage":{"input_tokens":10,"output_tokens":5}}}`,
	}, "\n")+"\n")
	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	associations := HistoryAssociations{
		Revision: "registry-1",
		Projects: []HistoryProjectAssociation{{Key: "project-1", Name: "Neuer Name", Path: "/work/demo"}},
		Sessions: []HistorySessionAssociation{{
			Key: "session-1", Name: "Claude", ProjectKey: "project-1", Dir: "/work/demo",
			Provider: HistoryProviderClaude, ConversationID: "conv-1",
		}},
	}
	page, err := history.Activity(context.Background(), associations, HistoryActivityQuery{Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	// Prompt (ohne Modell) und Ausgabe (mit Modell) derselben Conversation-Stunde
	// liegen bewusst in getrennten Zeilen (siehe core/workhistory_store.go,
	// historyActivityKey); Summieren über den Tag ist Aufgabe von addBucket
	// (Task 10), nicht dieser Abfrage.
	if len(page.Buckets) != 2 {
		t.Fatalf("buckets = %#v", page.Buckets)
	}
	var prompts, turns int
	for _, bucket := range page.Buckets {
		prompts += bucket.Prompts
		turns += bucket.Turns
		if bucket.Attribution.ProjectName.State != HistoryFactKnown || bucket.Attribution.ProjectName.Value != "Neuer Name" {
			t.Fatalf("project attribution = %#v", bucket.Attribution.ProjectName)
		}
		if bucket.Attribution.SessionKey.Value != "session-1" {
			t.Fatalf("session attribution = %#v", bucket.Attribution.SessionKey)
		}
	}
	if prompts != 1 || turns != 1 {
		t.Fatalf("counts across buckets = prompts=%d turns=%d", prompts, turns)
	}
}

func TestBuildStatsMatchesBetweenEventsAndBuckets(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	path := filepath.Join(home, ".claude", "projects", "-work-demo", "a.jsonl")
	writeHistoryTestFile(t, path, strings.Join([]string{
		`{"type":"user","uuid":"u-1","timestamp":"2026-08-30T10:00:00Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"user","content":"Prompt"}}`,
		`{"type":"assistant","uuid":"a-1","timestamp":"2026-08-30T10:00:05Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"assistant","model":"claude-opus-4-8","content":"Antwort","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3}}}`,
		`{"type":"user","uuid":"u-2","timestamp":"2026-08-31T14:00:00Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"user","content":"Noch ein Prompt"}}`,
	}, "\n")+"\n")
	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := &State{
		Projects: []Project{{ID: "project-1", Name: "Demo", Path: "/work/demo"}},
		Agents: []Session{{
			ID: "session-1", Name: "Claude", ProjectID: "project-1", Project: "Demo", Dir: "/work/demo",
			SessionKind: SessionKindCodingAgent,
			AgentRuns:   []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "conv-1"}},
		}},
	}
	associations := NewHistoryAssociations(*state)
	query := HistoryEventQuery{
		Since:    time.Date(2026, 8, 25, 0, 0, 0, 0, time.Local),
		Before:   time.Date(2026, 9, 2, 0, 0, 0, 0, time.Local),
		Lineages: []HistoryLineage{HistoryLineagePrimary},
	}
	events, err := history.Events(context.Background(), associations, query)
	if err != nil {
		t.Fatal(err)
	}
	fromEvents := newStatsAcc()
	for _, event := range events.Events {
		fromEvents.addEvent(event)
	}
	activity, err := history.Activity(context.Background(), associations, HistoryActivityQuery{
		Since: query.Since, Before: query.Before, Location: time.Local,
	})
	if err != nil {
		t.Fatal(err)
	}
	fromBuckets := newStatsAcc()
	for _, bucket := range activity.Buckets {
		fromBuckets.addBucket(bucket)
	}
	for day, slot := range fromEvents.days {
		other := fromBuckets.days[day]
		if other == nil {
			t.Fatalf("bucket path is missing day %s", day)
		}
		if slot.Prompts != other.Prompts || slot.Turns != other.Turns ||
			slot.Input != other.Input || slot.Output != other.Output ||
			slot.CacheRead != other.CacheRead || slot.costState() != other.costState() ||
			len(slot.sessions) != len(other.sessions) {
			t.Fatalf("day %s: events %#v vs buckets %#v", day, slot, other)
		}
	}
	if fromEvents.hours != fromBuckets.hours {
		t.Fatalf("hours: events %#v vs buckets %#v", fromEvents.hours, fromBuckets.hours)
	}
	if fromEvents.heatmap != fromBuckets.heatmap {
		t.Fatalf("heatmap differs")
	}
}

func TestWorkHistoryConversationsGroupsByConversation(t *testing.T) {
	history, home, _, codexHome := openTestWorkHistory(t)
	writeHistoryTestFile(t, filepath.Join(home, ".claude", "projects", "-work-demo", "a.jsonl"), strings.Join([]string{
		`{"type":"user","uuid":"u-1","timestamp":"2026-08-30T10:00:00Z","cwd":"/work/demo","sessionId":"conv-claude","message":{"role":"user","content":"Erster Prompt"}}`,
		`{"type":"assistant","uuid":"a-1","timestamp":"2026-08-30T10:00:05Z","cwd":"/work/demo","sessionId":"conv-claude","message":{"role":"assistant","model":"claude-opus","content":"Antwort"}}`,
		`{"type":"user","uuid":"u-2","timestamp":"2026-08-30T12:00:00Z","cwd":"/work/demo","sessionId":"conv-claude","message":{"role":"user","content":"Letzter Prompt"}}`,
	}, "\n")+"\n")
	writeHistoryTestFile(t, filepath.Join(codexHome, "sessions", "2026", "rollout-codex-1.jsonl"), strings.Join([]string{
		`{"type":"session_meta","timestamp":"2026-08-29T11:00:00Z","payload":{"id":"conv-codex","cwd":"/work/demo","model":"gpt-5"}}`,
		`{"type":"event_msg","timestamp":"2026-08-29T11:00:01Z","payload":{"type":"user_message","message":"Codex Prompt"}}`,
	}, "\n")+"\n")
	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	associations := HistoryAssociations{
		Projects: []HistoryProjectAssociation{{Key: "project-1", Name: "Demo", Path: "/work/demo"}},
		Sessions: []HistorySessionAssociation{{
			Key: "session-1", Name: "Claude", ProjectKey: "project-1", Dir: "/work/demo",
			Provider: HistoryProviderClaude, ConversationID: "conv-claude",
		}},
	}
	page, err := history.Conversations(context.Background(), associations, HistoryConversationQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Conversations) != 2 {
		t.Fatalf("conversations = %d/%d, want 2", page.Total, len(page.Conversations))
	}
	first := page.Conversations[0]
	if first.ConversationID != "conv-claude" {
		t.Fatalf("newest first expected, got %#v", first)
	}
	if first.Turns != 1 || first.LastPrompt.Value != "Letzter Prompt" {
		t.Fatalf("conversation = %#v", first)
	}
	if first.Attribution.SessionName.Value != "Claude" {
		t.Fatalf("attribution = %#v", first.Attribution)
	}
	if page.Conversations[1].Provider != HistoryProviderCodex {
		t.Fatalf("second = %#v", page.Conversations[1])
	}
}
