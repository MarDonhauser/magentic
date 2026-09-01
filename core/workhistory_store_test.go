package core

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryStoreCreatesSchemaAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := openHistoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := store.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != historySchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, historySchemaVersion)
	}
	for _, table := range []string{"sources", "events", "events_fts", "activity"} {
		var name string
		err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
	var mode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal mode = %q, want wal", mode)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openHistoryStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryStoreRebuildsOnUnknownSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := openHistoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO sources(source_id, provider, path, adapter_version, digest, size, mod_time, indexed_at, problems)
		VALUES('claude:src', 'claude', '/x.jsonl', 1, 'd', 1, 1, 1, '[]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE schema_version SET version = 999`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := openHistoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	var count int
	if err := rebuilt.db.QueryRow(`SELECT count(*) FROM sources`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("sources after rebuild = %d, want 0", count)
	}
}

func TestHistoryStoreSourceRoundTripAndDeletion(t *testing.T) {
	store := openTestHistoryStore(t)
	row := historySourceRow{
		SourceID: "claude:a", Provider: HistoryProviderClaude, Path: "/a.jsonl",
		AdapterVersion: 3, Digest: "digest-a", Size: 120, ModTime: 900, IndexedAt: 1000,
		Problems: []HistoryProblem{{Provider: HistoryProviderClaude, SourceID: "claude:a", Kind: "malformed", Message: "1 Zeile"}},
	}
	if err := store.writeSourceRow(row); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.source("claude:a")
	if err != nil || !ok {
		t.Fatalf("source: ok=%v err=%v", ok, err)
	}
	if got.Digest != "digest-a" || got.AdapterVersion != 3 || got.ModTime != 900 {
		t.Fatalf("source = %#v", got)
	}
	if len(got.Problems) != 1 || got.Problems[0].Kind != "malformed" {
		t.Fatalf("problems = %#v", got.Problems)
	}

	// Zweite, gesunde Quelle desselben Providers: sourceProblems und
	// countSources müssen für Probleme und Anzahl unterschiedliche Zahlen
	// liefern, und Problems: nil muss ohne Decode-Fehler als leer zurückkommen.
	healthy := historySourceRow{
		SourceID: "claude:b", Provider: HistoryProviderClaude, Path: "/b.jsonl",
		AdapterVersion: 3, Digest: "digest-b", Size: 80, ModTime: 950, IndexedAt: 1000,
		Problems: nil,
	}
	if err := store.writeSourceRow(healthy); err != nil {
		t.Fatal(err)
	}
	gotHealthy, ok, err := store.source("claude:b")
	if err != nil || !ok {
		t.Fatalf("source: ok=%v err=%v", ok, err)
	}
	if len(gotHealthy.Problems) != 0 {
		t.Fatalf("problems for nil-write = %#v", gotHealthy.Problems)
	}

	ids, err := store.sourceIDsByProvider(HistoryProviderClaude)
	if err != nil || !ids["claude:a"] || !ids["claude:b"] || len(ids) != 2 {
		t.Fatalf("ids = %#v err = %v", ids, err)
	}

	problems, err := store.sourceProblems(HistoryProviderClaude)
	if err != nil || len(problems) != 1 {
		t.Fatalf("problems = %#v err = %v", problems, err)
	}

	count, err := store.countSources(HistoryProviderClaude)
	if err != nil || count != 2 {
		t.Fatalf("count = %d err = %v", count, err)
	}

	if err := store.deleteSources([]string{"claude:a", "claude:b"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.source("claude:a"); ok || err != nil {
		t.Fatalf("after delete: ok=%v err=%v", ok, err)
	}
}

func TestHistoryStoreMergesEventsAcrossSources(t *testing.T) {
	store := openTestHistoryStore(t)
	shared := historyRecord{
		ID: "codex:event:abc", Provider: HistoryProviderCodex, ConversationID: "conv-1",
		Timestamp: "2026-08-30T10:00:00Z", Role: HistoryRoleAssistant,
		Kind: HistoryEventOutput, Lineage: HistoryLineagePrimary, Text: "Antwort",
	}
	withText := shared
	withText.SourceID = "codex:live"

	withUsage := shared
	withUsage.SourceID = "codex:archiv"
	withUsage.Text = ""
	withUsage.Model = "gpt-5"
	withUsage.Usage = historyUsageRecord{Input: 10, InputKnown: true, Output: 4, OutputKnown: true}

	if err := store.replaceSource(historySourceRow{SourceID: "codex:live", Provider: HistoryProviderCodex, Path: "/live.jsonl", ModTime: 100}, []historyRecord{withText}); err != nil {
		t.Fatal(err)
	}
	if err := store.replaceSource(historySourceRow{SourceID: "codex:archiv", Provider: HistoryProviderCodex, Path: "/archiv.jsonl", ModTime: 90}, []historyRecord{withUsage}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("events = %d, want 1", count)
	}
	var text, model string
	var input sql.NullInt64
	if err := store.db.QueryRow(`SELECT text, model, input_tokens FROM events`).Scan(&text, &model, &input); err != nil {
		t.Fatal(err)
	}
	if text != "Antwort" {
		t.Fatalf("text = %q, want the known one to survive", text)
	}
	if model != "gpt-5" || !input.Valid || input.Int64 != 10 {
		t.Fatalf("merge lost facts: model=%q input=%#v", model, input)
	}
}

func TestHistoryStoreReplaceSourceRemovesVanishedEvents(t *testing.T) {
	store := openTestHistoryStore(t)
	row := historySourceRow{SourceID: "claude:a", Provider: HistoryProviderClaude, Path: "/a.jsonl", ModTime: 10}
	first := historyRecord{
		ID: "claude:event:1", SourceID: "claude:a", Provider: HistoryProviderClaude,
		Timestamp: "2026-08-30T10:00:00Z", Role: HistoryRoleDeveloper, Kind: HistoryEventPrompt,
		Lineage: HistoryLineagePrimary, Text: "alt",
	}
	if err := store.replaceSource(row, []historyRecord{first}); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = "claude:event:2"
	second.Text = "neu"
	if err := store.replaceSource(row, []historyRecord{second}); err != nil {
		t.Fatal(err)
	}
	records, err := store.recordsBySource("claude:a")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Text != "neu" {
		t.Fatalf("records = %#v", records)
	}
}

func TestHistoryFTSExpression(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		usable bool
	}{
		{in: "hallo welt", want: `"hallo"* "welt"*`, usable: true},
		{in: "  Fehler ", want: `"Fehler"*`, usable: true},
		{in: `such"e`, want: `"such""e"*`, usable: true},
		{in: "...", want: "", usable: false},
		{in: "", want: "", usable: false},
	}
	for _, c := range cases {
		got, usable := historyFTSExpression(c.in)
		if got != c.want || usable != c.usable {
			t.Fatalf("historyFTSExpression(%q) = (%q, %v), want (%q, %v)", c.in, got, usable, c.want, c.usable)
		}
	}
}

func TestHistoryStoreRecordsFilterAndSubstringSearch(t *testing.T) {
	store := openTestHistoryStore(t)
	base := historySourceRow{SourceID: "claude:a", Provider: HistoryProviderClaude, Path: "/a.jsonl", ModTime: 10}
	records := []historyRecord{
		{ID: "e1", SourceID: "claude:a", Provider: HistoryProviderClaude, Timestamp: "2026-08-30T10:00:00Z",
			Role: HistoryRoleDeveloper, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: "Kubernetes aufsetzen"},
		{ID: "e2", SourceID: "claude:a", Provider: HistoryProviderClaude, Timestamp: "2026-08-20T10:00:00Z",
			Role: HistoryRoleAssistant, Kind: HistoryEventOutput, Lineage: HistoryLineagePrimary, Text: "fertig"},
		{ID: "e3", SourceID: "claude:a", Provider: HistoryProviderClaude, Timestamp: "",
			Role: HistoryRoleDeveloper, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: "ohne Zeit"},
	}
	if err := store.replaceSource(base, records); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	got, err := store.records(ctx, historyRecordFilter{Kinds: []HistoryEventKind{HistoryEventPrompt}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("prompts = %d, want 2", len(got))
	}

	since := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	got, err = store.records(ctx, historyRecordFilter{Since: since})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "e1" {
		t.Fatalf("since = %#v", got)
	}

	got, err = store.records(ctx, historyRecordFilter{Since: since, IncludeUnknownTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("since with unknown time = %d, want 2", len(got))
	}

	// Teilstring innerhalb eines Wortes: FTS allein fände das nicht.
	got, err = store.records(ctx, historyRecordFilter{Text: "bernet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "e1" {
		t.Fatalf("substring search = %#v", got)
	}

	got, err = store.records(ctx, historyRecordFilter{Text: "..."})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("punctuation-only search = %#v, want none", got)
	}
}

func openTestHistoryStore(t *testing.T) *historyStore {
	t.Helper()
	store, err := openHistoryStore(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}
