package core

import (
	"path/filepath"
	"testing"
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

	ids, err := store.sourceIDsByProvider(HistoryProviderClaude)
	if err != nil || !ids["claude:a"] || len(ids) != 1 {
		t.Fatalf("ids = %#v err = %v", ids, err)
	}

	problems, count, err := store.sourceProblems(HistoryProviderClaude)
	if err != nil || count != 1 || len(problems) != 1 {
		t.Fatalf("problems = %#v count = %d err = %v", problems, count, err)
	}

	if err := store.deleteSources([]string{"claude:a"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.source("claude:a"); ok || err != nil {
		t.Fatalf("after delete: ok=%v err=%v", ok, err)
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
