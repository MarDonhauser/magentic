# WorkHistory-Index Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** WorkHistory bekommt einen abfragbaren SQLite-Index statt einer vollständig geladenen 609-MB-JSON-Datei, ein 14-Tage-Fenster für Roh-Events mit dauerhaften Aktivitätsaggregaten und einen Indexaufbau im Hintergrund, der Abfragen sofort mit Teilergebnissen bedient.

**Architecture:** `core/workhistory.go` behält seine öffentlichen Schnittstellen. Darunter tritt ein neuer Speicher (`core/workhistory_store.go`) an die Stelle von `historyIndex`, und ein Indexerlauf (`core/workhistory_indexer.go`) übernimmt Entdeckung, Parsen, Aufbewahrung und Aggregation. Die Provider-Adapter bleiben unverändert.

**Tech Stack:** Go 1.26, `modernc.org/sqlite` (reines Go, kein cgo), SQLite mit WAL und FTS5, Wails v2 im Frontend, Vanilla-JS ohne Framework.

**Spec:** `docs/superpowers/specs/2026-09-01-workhistory-index-design.md`

## Global Constraints

- Aufbewahrung der Roh-Events: **14 Tage**. Voreinstellung als Konstante `historyRetentionWindow = 14 * 24 * time.Hour`, in `WorkHistoryConfig.Retention` überschreibbar.
- Die SQLite-Abhängigkeit ist **`modernc.org/sqlite`**, Treibername `"sqlite"`. Kein cgo, keine weitere externe Abhängigkeit in `core`.
- Datenbankpfad: `<IndexDir>/history.db`, Rechte `0600`, Verzeichnis `0700` über das bestehende `ensurePrivateHistoryDir` (`core/workhistory.go:455`).
- Die öffentlichen Typen `HistoryEvent`, `HistoryEventPage`, `HistoryLinkPage`, `HistorySummary`, `HistoryMeta`, `HistoryProviderCoverage`, `HistoryProblem` und `HistoryFact[T]` ändern ihre Bedeutung nicht. `HistoryMeta` bekommt genau ein neues Feld `Progress`.
- Die Schnittstelle `historyProviderAdapter` (`core/workhistory_adapters.go:22`) und alle vier Adapter bleiben unverändert. Kein Task darf `core/workhistory_adapters.go` anfassen.
- Alle Oberflächentexte sind deutsch, ganze Sätze. Keine Pillen mit Punkt davor, keine Mikrolabels in Ecken (siehe globale UI-Regeln).
- Tests laufen mit `go test ./core/...` bzw. `go test ./...` im jeweiligen Modul. Der Entwickler führt die Suite selbst aus; einzelne Tests im Rahmen eines Tasks laufen zu lassen ist ausdrücklich erwünscht.
- Jeder Task endet mit einem Commit. Commit-Botschaften auf Deutsch, ohne Erwähnung von Werkzeugherstellern.

## File Structure

**Neu:**

- `core/workhistory_store.go` — SQLite-Schema, Öffnen, Lesen und Schreiben von `sources`, `events`, `events_fts`, `activity`. Kennt keine Adapter und keine Attribution.
- `core/workhistory_store_test.go` — Tests gegen den Speicher direkt.
- `core/workhistory_indexer.go` — Indexerlauf: Entdeckung, Wiederverwendungsprüfung, Parsen, Aufbewahrung, Aggregatschreiben, Fortschritt.
- `core/workhistory_indexer_test.go` — Tests für Lauf, Aufbewahrung, Idempotenz, Teilergebnis.
- `core/workhistory_legacy.go` — einmalige Übernahme der Aggregate aus `index.json`.
- `core/workhistory_legacy_test.go`

**Geändert:**

- `core/workhistory.go` — `historyIndex`, `loadHistoryIndex`, `saveHistoryIndex`, `refreshIndex` entfallen. `Events`, `Links`, `Summarize` lesen aus dem Speicher; `Activity` und `Conversations` kommen dazu.
- `core/stats.go` — `buildStats` speist sich aus Aktivitätsbuckets.
- `app/tools.go`, `core/provider_run.go` — gemeinsame Instanz statt eigener.
- `app/frontend/index.html`, `app/frontend/src/main.js` — Teilergebnis, Fortschritt, sichtbarer Fehler.
- `CONTEXT.md` — drei neue Begriffe.
- `go.mod`, `go.sum` — `modernc.org/sqlite`.

---

### Task 1: SQLite-Speicher anlegen

**Files:**
- Create: `core/workhistory_store.go`
- Create: `core/workhistory_store_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: nichts.
- Produces: `openHistoryStore(path string) (*historyStore, error)`, `(*historyStore).Close() error`, `(*historyStore).db *sql.DB`, Konstante `historySchemaVersion = 1`.

- [ ] **Step 1: Abhängigkeit hinzufügen**

```bash
go get modernc.org/sqlite@latest
go mod tidy
```

- [ ] **Step 2: Write the failing test**

`core/workhistory_store_test.go`:

```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./core/ -run TestHistoryStore -v`
Expected: FAIL, `undefined: openHistoryStore`

- [ ] **Step 4: Write minimal implementation**

`core/workhistory_store.go`:

```go
package core

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

const historySchemaVersion = 1

type historyStore struct {
	path string
	db   *sql.DB
}

const historySchemaSQL = `
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS sources (
	source_id      TEXT PRIMARY KEY,
	provider       TEXT NOT NULL,
	path           TEXT NOT NULL,
	adapter_version INTEGER NOT NULL,
	digest         TEXT NOT NULL,
	size           INTEGER NOT NULL,
	mod_time       INTEGER NOT NULL,
	indexed_at     INTEGER NOT NULL,
	problems       TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS sources_provider ON sources(provider);

CREATE TABLE IF NOT EXISTS events (
	event_id        TEXT PRIMARY KEY,
	source_id       TEXT NOT NULL,
	provider        TEXT NOT NULL,
	conversation_id TEXT NOT NULL DEFAULT '',
	occurred_at     INTEGER,
	timestamp_raw   TEXT NOT NULL DEFAULT '',
	role            TEXT NOT NULL,
	kind            TEXT NOT NULL,
	lineage         TEXT NOT NULL,
	text            TEXT NOT NULL DEFAULT '',
	model           TEXT NOT NULL DEFAULT '',
	input_tokens    INTEGER, output_tokens INTEGER,
	cache_read_tokens INTEGER, cache_write_tokens INTEGER,
	cwd             TEXT NOT NULL DEFAULT '',
	project_alias   TEXT NOT NULL DEFAULT '',
	native_id       TEXT NOT NULL DEFAULT '',
	links           TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS events_occurred ON events(occurred_at);
CREATE INDEX IF NOT EXISTS events_source ON events(source_id);
CREATE INDEX IF NOT EXISTS events_conversation ON events(provider, conversation_id);

CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(
	text, content='events', content_rowid='rowid', tokenize='unicode61'
);

CREATE TABLE IF NOT EXISTS activity (
	agg_key         TEXT NOT NULL,
	day             TEXT NOT NULL,
	hour            INTEGER NOT NULL,
	provider        TEXT NOT NULL,
	model           TEXT NOT NULL DEFAULT '',
	source_id       TEXT NOT NULL,
	written_from_mod_time INTEGER NOT NULL,
	conversation_id TEXT NOT NULL DEFAULT '',
	cwd             TEXT NOT NULL DEFAULT '',
	project_alias   TEXT NOT NULL DEFAULT '',
	prompts         INTEGER NOT NULL DEFAULT 0,
	turns           INTEGER NOT NULL DEFAULT 0,
	input_tokens    INTEGER NOT NULL DEFAULT 0,
	output_tokens   INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	cost            REAL NOT NULL DEFAULT 0,
	priced_events   INTEGER NOT NULL DEFAULT 0,
	unpriced_events INTEGER NOT NULL DEFAULT 0,
	known_usage_events   INTEGER NOT NULL DEFAULT 0,
	unknown_usage_events INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (agg_key, day, hour, provider, model)
);
CREATE INDEX IF NOT EXISTS activity_day ON activity(day);
`

func openHistoryStore(path string) (*historyStore, error) {
	store, err := openHistoryStoreOnce(path)
	if err == nil {
		return store, nil
	}
	if !isHistorySchemaMismatch(err) {
		return nil, err
	}
	// Der Index ist vollständig aus den Transkripten reproduzierbar. Eine
	// unbekannte Fassung wird deshalb verworfen statt migriert.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if removeErr := os.Remove(path + suffix); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, fmt.Errorf("work history reset: %w", removeErr)
		}
	}
	return openHistoryStoreOnce(path)
}

type historySchemaMismatch struct{ found int }

func (e historySchemaMismatch) Error() string {
	return fmt.Sprintf("work history schema version %d unsupported", e.found)
}

func isHistorySchemaMismatch(err error) bool {
	_, ok := err.(historySchemaMismatch)
	return ok
}

func openHistoryStoreOnce(path string) (*historyStore, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open work history: %w", err)
	}
	// Ein einziger Schreibpfad; SQLite serialisiert Schreiber ohnehin.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(historySchemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("create work history schema: %w", err)
	}
	var version int
	err = db.QueryRow(`SELECT version FROM schema_version`).Scan(&version)
	switch {
	case err == sql.ErrNoRows:
		if _, err := db.Exec(`INSERT INTO schema_version(version) VALUES(?)`, historySchemaVersion); err != nil {
			db.Close()
			return nil, fmt.Errorf("stamp work history schema: %w", err)
		}
	case err != nil:
		db.Close()
		return nil, fmt.Errorf("read work history schema: %w", err)
	case version != historySchemaVersion:
		db.Close()
		return nil, historySchemaMismatch{found: version}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("protect work history index: %w", err)
	}
	return &historyStore{path: path, db: db}, nil
}

func (s *historyStore) Close() error { return s.db.Close() }
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./core/ -run TestHistoryStore -v`
Expected: PASS für beide Tests.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum core/workhistory_store.go core/workhistory_store_test.go
git commit -m "feat(core): SQLite-Speicher für den Arbeitsverlauf anlegen"
```

---

### Task 2: Quellen speichern und wiederverwenden

**Files:**
- Modify: `core/workhistory_store.go`
- Modify: `core/workhistory_store_test.go`

**Interfaces:**
- Consumes: `openHistoryStore`, `historyStore.db` aus Task 1.
- Produces:
  - `type historySourceRow struct { SourceID string; Provider HistoryProvider; Path string; AdapterVersion int; Digest string; Size, ModTime, IndexedAt int64; Problems []HistoryProblem }`
  - `(*historyStore).source(sourceID string) (historySourceRow, bool, error)`
  - `(*historyStore).sourceIDsByProvider(provider HistoryProvider) (map[string]bool, error)`
  - `(*historyStore).deleteSources(ids []string) error`
  - `(*historyStore).sourceProblems(provider HistoryProvider) ([]HistoryProblem, int, error)` — Probleme aller Quellen des Providers plus deren Anzahl

- [ ] **Step 1: Write the failing test**

An `core/workhistory_store_test.go` anhängen:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run TestHistoryStoreSourceRoundTrip -v`
Expected: FAIL, `undefined: historySourceRow`

- [ ] **Step 3: Write minimal implementation**

An `core/workhistory_store.go` anhängen:

```go
import (
	"encoding/json"
	"strings"
)

type historySourceRow struct {
	SourceID       string
	Provider       HistoryProvider
	Path           string
	AdapterVersion int
	Digest         string
	Size           int64
	ModTime        int64
	IndexedAt      int64
	Problems       []HistoryProblem
}

func (s *historyStore) writeSourceRow(row historySourceRow) error {
	problems, err := json.Marshal(historyProblemsOrEmpty(row.Problems))
	if err != nil {
		return fmt.Errorf("encode source problems: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO sources
		(source_id, provider, path, adapter_version, digest, size, mod_time, indexed_at, problems)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(source_id) DO UPDATE SET
			provider=excluded.provider, path=excluded.path,
			adapter_version=excluded.adapter_version, digest=excluded.digest,
			size=excluded.size, mod_time=excluded.mod_time,
			indexed_at=excluded.indexed_at, problems=excluded.problems`,
		row.SourceID, string(row.Provider), row.Path, row.AdapterVersion, row.Digest,
		row.Size, row.ModTime, row.IndexedAt, string(problems))
	if err != nil {
		return fmt.Errorf("write source: %w", err)
	}
	return nil
}

func historyProblemsOrEmpty(problems []HistoryProblem) []HistoryProblem {
	if problems == nil {
		return []HistoryProblem{}
	}
	return problems
}

func (s *historyStore) source(sourceID string) (historySourceRow, bool, error) {
	row := historySourceRow{SourceID: sourceID}
	var provider, problems string
	err := s.db.QueryRow(`SELECT provider, path, adapter_version, digest, size, mod_time, indexed_at, problems
		FROM sources WHERE source_id = ?`, sourceID).
		Scan(&provider, &row.Path, &row.AdapterVersion, &row.Digest, &row.Size, &row.ModTime, &row.IndexedAt, &problems)
	if err == sql.ErrNoRows {
		return historySourceRow{}, false, nil
	}
	if err != nil {
		return historySourceRow{}, false, fmt.Errorf("read source: %w", err)
	}
	row.Provider = HistoryProvider(provider)
	if err := json.Unmarshal([]byte(problems), &row.Problems); err != nil {
		return historySourceRow{}, false, fmt.Errorf("decode source problems: %w", err)
	}
	return row, true, nil
}

func (s *historyStore) sourceIDsByProvider(provider HistoryProvider) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT source_id FROM sources WHERE provider = ?`, string(provider))
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list sources: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *historyStore) sourceProblems(provider HistoryProvider) ([]HistoryProblem, int, error) {
	rows, err := s.db.Query(`SELECT problems FROM sources WHERE provider = ?`, string(provider))
	if err != nil {
		return nil, 0, fmt.Errorf("read source problems: %w", err)
	}
	defer rows.Close()
	var out []HistoryProblem
	count := 0
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, 0, fmt.Errorf("read source problems: %w", err)
		}
		count++
		var problems []HistoryProblem
		if err := json.Unmarshal([]byte(encoded), &problems); err != nil {
			return nil, 0, fmt.Errorf("decode source problems: %w", err)
		}
		out = append(out, problems...)
	}
	return out, count, rows.Err()
}

func (s *historyStore) deleteSources(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("delete sources: %w", err)
	}
	defer tx.Rollback()
	for _, id := range ids {
		if err := deleteHistorySourceTx(tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func deleteHistorySourceTx(tx *sql.Tx, sourceID string) error {
	if _, err := tx.Exec(`DELETE FROM events_fts WHERE rowid IN (SELECT rowid FROM events WHERE source_id = ?)`, sourceID); err != nil {
		return fmt.Errorf("delete source text: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM events WHERE source_id = ?`, sourceID); err != nil {
		return fmt.Errorf("delete source events: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM sources WHERE source_id = ?`, sourceID); err != nil {
		return fmt.Errorf("delete source: %w", err)
	}
	return nil
}

var _ = strings.TrimSpace
```

Die letzte Zeile fällt weg, sobald `strings` in Task 4 wirklich benutzt wird; entferne sie dort.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run TestHistoryStore -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/workhistory_store.go core/workhistory_store_test.go
git commit -m "feat(core): Quellen des Arbeitsverlaufs im Speicher ablegen"
```

---

### Task 3: Events schreiben und zusammenführen

**Files:**
- Modify: `core/workhistory_store.go`
- Modify: `core/workhistory_store_test.go`

**Interfaces:**
- Consumes: `historySourceRow`, `deleteHistorySourceTx` aus Task 2; `historyRecord` (`core/workhistory.go:552`), `mergeHistoryRecord` (`core/workhistory.go:818`).
- Produces:
  - `(*historyStore).replaceSource(row historySourceRow, records []historyRecord) error` — löscht die Events der Quelle und schreibt die neuen in einer Transaktion, führt Kollisionen auf `event_id` über `mergeHistoryRecord` zusammen.
  - `(*historyStore).recordsBySource(sourceID string) ([]historyRecord, error)` — nur für Tests und die Aggregatprüfung.

Die Zusammenführung ist nötig, weil `stableHistoryRecordID` (`core/workhistory.go:799`) die Quelle aus der Identität weglässt, sobald eine Conversation bekannt ist. Zwei Codex-Dateien (`sessions` und `archived_sessions`) können dasselbe Ereignis liefern.

- [ ] **Step 1: Write the failing test**

```go
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
		Timestamp: "2026-08-30T10:00:00Z", Role: HistoryRoleUser, Kind: HistoryEventPrompt,
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run TestHistoryStoreMerges -v`
Expected: FAIL, `store.replaceSource undefined`

- [ ] **Step 3: Write minimal implementation**

```go
func (s *historyStore) replaceSource(row historySourceRow, records []historyRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("write source: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM events_fts WHERE rowid IN (SELECT rowid FROM events WHERE source_id = ?)`, row.SourceID); err != nil {
		return fmt.Errorf("clear source text: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM events WHERE source_id = ?`, row.SourceID); err != nil {
		return fmt.Errorf("clear source events: %w", err)
	}
	for _, record := range records {
		if err := insertHistoryRecordTx(tx, record); err != nil {
			return err
		}
	}
	problems, err := json.Marshal(historyProblemsOrEmpty(row.Problems))
	if err != nil {
		return fmt.Errorf("encode source problems: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO sources
		(source_id, provider, path, adapter_version, digest, size, mod_time, indexed_at, problems)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(source_id) DO UPDATE SET
			provider=excluded.provider, path=excluded.path,
			adapter_version=excluded.adapter_version, digest=excluded.digest,
			size=excluded.size, mod_time=excluded.mod_time,
			indexed_at=excluded.indexed_at, problems=excluded.problems`,
		row.SourceID, string(row.Provider), row.Path, row.AdapterVersion, row.Digest,
		row.Size, row.ModTime, row.IndexedAt, string(problems)); err != nil {
		return fmt.Errorf("write source: %w", err)
	}
	return tx.Commit()
}

func insertHistoryRecordTx(tx *sql.Tx, record historyRecord) error {
	existing, found, err := historyRecordByIDTx(tx, record.ID)
	if err != nil {
		return err
	}
	if found {
		// Die vorhandene Fassung führt; fehlende Tatsachen kommen aus der neuen.
		record = mergeHistoryRecord(existing, record)
		if _, err := tx.Exec(`DELETE FROM events_fts WHERE rowid IN (SELECT rowid FROM events WHERE event_id = ?)`, record.ID); err != nil {
			return fmt.Errorf("clear merged text: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM events WHERE event_id = ?`, record.ID); err != nil {
			return fmt.Errorf("clear merged event: %w", err)
		}
	}
	links, err := json.Marshal(historyLinksOrEmpty(record.Links))
	if err != nil {
		return fmt.Errorf("encode links: %w", err)
	}
	result, err := tx.Exec(`INSERT INTO events
		(event_id, source_id, provider, conversation_id, occurred_at, timestamp_raw, role, kind, lineage,
		 text, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		 cwd, project_alias, native_id, links)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.ID, record.SourceID, string(record.Provider), record.ConversationID,
		historyOccurredAtValue(record.Timestamp), record.Timestamp,
		string(record.Role), string(record.Kind), string(record.Lineage),
		record.Text, record.Model,
		historyNullableInt(record.Usage.Input, record.Usage.InputKnown),
		historyNullableInt(record.Usage.Output, record.Usage.OutputKnown),
		historyNullableInt(record.Usage.CacheRead, record.Usage.CacheReadKnown),
		historyNullableInt(record.Usage.CacheWrite, record.Usage.CacheWriteKnown),
		record.CWD, record.ProjectAlias, record.NativeID, string(links))
	if err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	rowID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO events_fts(rowid, text) VALUES(?, ?)`, rowID, record.Text); err != nil {
		return fmt.Errorf("write event text: %w", err)
	}
	return nil
}

func historyLinksOrEmpty(links []string) []string {
	if links == nil {
		return []string{}
	}
	return links
}

func historyOccurredAtValue(timestamp string) any {
	if timestamp == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return nil
	}
	return parsed.UTC().UnixNano()
}

func historyNullableInt(value int64, known bool) any {
	if !known {
		return nil
	}
	return value
}
```

`historyRecordByIDTx` und `recordsBySource` teilen sich das Auslesen:

```go
const historyRecordColumns = `event_id, source_id, provider, conversation_id, timestamp_raw, role, kind, lineage,
	text, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	cwd, project_alias, native_id, links`

func scanHistoryRecord(scan func(...any) error) (historyRecord, error) {
	var record historyRecord
	var provider, role, kind, lineage, links string
	var input, output, cacheRead, cacheWrite sql.NullInt64
	err := scan(&record.ID, &record.SourceID, &provider, &record.ConversationID, &record.Timestamp,
		&role, &kind, &lineage, &record.Text, &record.Model,
		&input, &output, &cacheRead, &cacheWrite,
		&record.CWD, &record.ProjectAlias, &record.NativeID, &links)
	if err != nil {
		return historyRecord{}, err
	}
	record.Provider = HistoryProvider(provider)
	record.Role = HistoryRole(role)
	record.Kind = HistoryEventKind(kind)
	record.Lineage = HistoryLineage(lineage)
	record.Usage = historyUsageRecord{
		Input: input.Int64, InputKnown: input.Valid,
		Output: output.Int64, OutputKnown: output.Valid,
		CacheRead: cacheRead.Int64, CacheReadKnown: cacheRead.Valid,
		CacheWrite: cacheWrite.Int64, CacheWriteKnown: cacheWrite.Valid,
	}
	if err := json.Unmarshal([]byte(links), &record.Links); err != nil {
		return historyRecord{}, fmt.Errorf("decode links: %w", err)
	}
	if len(record.Links) == 0 {
		record.Links = nil
	}
	return record, nil
}

func historyRecordByIDTx(tx *sql.Tx, id string) (historyRecord, bool, error) {
	row := tx.QueryRow(`SELECT `+historyRecordColumns+` FROM events WHERE event_id = ?`, id)
	record, err := scanHistoryRecord(row.Scan)
	if err == sql.ErrNoRows {
		return historyRecord{}, false, nil
	}
	if err != nil {
		return historyRecord{}, false, fmt.Errorf("read event: %w", err)
	}
	return record, true, nil
}

func (s *historyStore) recordsBySource(sourceID string) ([]historyRecord, error) {
	rows, err := s.db.Query(`SELECT `+historyRecordColumns+` FROM events WHERE source_id = ? ORDER BY event_id`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	defer rows.Close()
	var out []historyRecord
	for rows.Next() {
		record, err := scanHistoryRecord(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("read events: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}
```

Ergänze `"time"` im Import-Block.

Beachte im ersten Test: `replaceSource` für `codex:archiv` löscht nur Events **dieser** Quelle. Das zusammengeführte Ereignis trägt nach dem Einfügen die `source_id` der zuerst geschriebenen Quelle, weil `mergeHistoryRecord(existing, record)` das vorhandene `SourceID`-Feld behält. Genau das erwartet der Test.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run TestHistoryStore -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/workhistory_store.go core/workhistory_store_test.go
git commit -m "feat(core): Events im Speicher ablegen und über Quellen hinweg zusammenführen"
```

---

### Task 4: Events lesen, Suche über FTS mit Teilstringfilter

**Files:**
- Modify: `core/workhistory_store.go`
- Modify: `core/workhistory_store_test.go`

**Interfaces:**
- Consumes: `scanHistoryRecord`, `historyRecordColumns` aus Task 3.
- Produces:
  - `type historyRecordFilter struct { Since, Before time.Time; IncludeUnknownTime bool; Providers []HistoryProvider; Roles []HistoryRole; Kinds []HistoryEventKind; Lineages []HistoryLineage; Text string; SourceIDs []string }`
  - `(*historyStore).records(ctx context.Context, filter historyRecordFilter) ([]historyRecord, error)`
  - `historyFTSExpression(query string) (string, bool)`

Die Attributionsfilter `ProjectKeys` und `SessionKeys` bleiben bewusst außerhalb: sie hängen an `HistoryAssociations` und werden weiterhin in Go aufgelöst (Task 6).

- [ ] **Step 1: Write the failing test**

```go
func TestHistoryFTSExpression(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		usable  bool
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
			Role: HistoryRoleUser, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: "Kubernetes aufsetzen"},
		{ID: "e2", SourceID: "claude:a", Provider: HistoryProviderClaude, Timestamp: "2026-08-20T10:00:00Z",
			Role: HistoryRoleAssistant, Kind: HistoryEventOutput, Lineage: HistoryLineagePrimary, Text: "fertig"},
		{ID: "e3", SourceID: "claude:a", Provider: HistoryProviderClaude, Timestamp: "",
			Role: HistoryRoleUser, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: "ohne Zeit"},
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run "TestHistoryFTSExpression|TestHistoryStoreRecordsFilter" -v`
Expected: FAIL, `undefined: historyFTSExpression`

- [ ] **Step 3: Write minimal implementation**

```go
// historyFTSExpression baut aus einer Nutzeranfrage einen FTS5-Präfixausdruck.
// Er dient nur der Vorauswahl; die genaue Trefferentscheidung fällt danach über
// einen Teilstringvergleich, damit sich die Suche wie bisher verhält.
func historyFTSExpression(query string) (string, bool) {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		terms = append(terms, `"`+strings.ReplaceAll(field, `"`, `""`)+`"*`)
	}
	if len(terms) == 0 {
		return "", false
	}
	return strings.Join(terms, " "), true
}

func (s *historyStore) records(ctx context.Context, filter historyRecordFilter) ([]historyRecord, error) {
	where := []string{"1 = 1"}
	var args []any

	appendIn := func(column string, values []string) {
		if len(values) == 0 {
			return
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
		where = append(where, column+" IN ("+placeholders+")")
		for _, value := range values {
			args = append(args, value)
		}
	}
	appendIn("provider", historyStrings(filter.Providers))
	appendIn("role", historyStrings(filter.Roles))
	appendIn("kind", historyStrings(filter.Kinds))
	appendIn("lineage", historyStrings(filter.Lineages))
	appendIn("source_id", filter.SourceIDs)

	if !filter.Since.IsZero() || !filter.Before.IsZero() {
		var window []string
		if !filter.Since.IsZero() {
			window = append(window, "occurred_at >= ?")
			args = append(args, filter.Since.UTC().UnixNano())
		}
		if !filter.Before.IsZero() {
			window = append(window, "occurred_at < ?")
			args = append(args, filter.Before.UTC().UnixNano())
		}
		clause := "(" + strings.Join(window, " AND ") + ")"
		if filter.IncludeUnknownTime {
			clause = "(" + clause + " OR occurred_at IS NULL)"
		} else {
			clause = "(occurred_at IS NOT NULL AND " + clause + ")"
		}
		where = append(where, clause)
	}

	needle := strings.TrimSpace(filter.Text)
	if needle != "" {
		expression, usable := historyFTSExpression(needle)
		if !usable {
			// Aus reinen Satzzeichen entsteht kein FTS-Ausdruck. Der
			// Teilstringvergleich unten bleibt trotzdem verbindlich, deshalb
			// wird hier nur nicht vorausgewählt.
			where = append(where, "instr(lower(text), lower(?)) > 0")
			args = append(args, needle)
		} else {
			where = append(where, "rowid IN (SELECT rowid FROM events_fts WHERE events_fts MATCH ?)")
			args = append(args, expression)
		}
	}

	query := `SELECT ` + historyRecordColumns + ` FROM events WHERE ` + strings.Join(where, " AND ")
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	defer rows.Close()
	lowered := strings.ToLower(needle)
	var out []historyRecord
	for rows.Next() {
		record, err := scanHistoryRecord(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("read events: %w", err)
		}
		if lowered != "" && !strings.Contains(strings.ToLower(record.Text), lowered) {
			continue
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func historyStrings[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

type historyRecordFilter struct {
	Since, Before      time.Time
	IncludeUnknownTime bool
	Providers          []HistoryProvider
	Roles              []HistoryRole
	Kinds              []HistoryEventKind
	Lineages           []HistoryLineage
	Text               string
	SourceIDs          []string
}
```

Ergänze `"context"` und `"unicode"` im Import-Block und entferne die Zeile `var _ = strings.TrimSpace` aus Task 2.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run "TestHistoryFTSExpression|TestHistoryStoreRecordsFilter" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/workhistory_store.go core/workhistory_store_test.go
git commit -m "feat(core): Events aus dem Speicher lesen, Suche über FTS mit Teilstringprüfung"
```

---

### Task 5: Aufbewahrungsfenster

**Files:**
- Modify: `core/workhistory_store.go`
- Modify: `core/workhistory_store_test.go`

**Interfaces:**
- Consumes: Schema aus Task 1.
- Produces:
  - Konstante `historyRetentionWindow = 14 * 24 * time.Hour`
  - `(*historyStore).pruneEvents(ctx context.Context, cutoff time.Time) (int, error)` — löscht Events mit `occurred_at < cutoff` sowie Events ohne Zeitpunkt, deren Quelle älter als `cutoff` geändert wurde. Gibt die Anzahl gelöschter Zeilen zurück.

- [ ] **Step 1: Write the failing test**

```go
func TestHistoryStorePruneDropsOldEventsAndKeepsActivity(t *testing.T) {
	store := openTestHistoryStore(t)
	oldSource := historySourceRow{SourceID: "claude:alt", Provider: HistoryProviderClaude, Path: "/alt.jsonl", ModTime: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).UnixNano()}
	newSource := historySourceRow{SourceID: "claude:neu", Provider: HistoryProviderClaude, Path: "/neu.jsonl", ModTime: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC).UnixNano()}

	if err := store.replaceSource(oldSource, []historyRecord{
		{ID: "alt-1", SourceID: "claude:alt", Provider: HistoryProviderClaude, Timestamp: "2026-07-01T10:00:00Z",
			Role: HistoryRoleUser, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: "alt"},
		{ID: "alt-2", SourceID: "claude:alt", Provider: HistoryProviderClaude, Timestamp: "",
			Role: HistoryRoleUser, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: "alt ohne Zeit"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.replaceSource(newSource, []historyRecord{
		{ID: "neu-1", SourceID: "claude:neu", Provider: HistoryProviderClaude, Timestamp: "2026-08-31T10:00:00Z",
			Role: HistoryRoleUser, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: "neu"},
		{ID: "neu-2", SourceID: "claude:neu", Provider: HistoryProviderClaude, Timestamp: "",
			Role: HistoryRoleUser, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: "neu ohne Zeit"},
	}); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	removed, err := store.pruneEvents(context.Background(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	remaining, err := store.records(context.Background(), historyRecordFilter{IncludeUnknownTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining = %#v", remaining)
	}
	for _, record := range remaining {
		if strings.HasPrefix(record.ID, "alt") {
			t.Fatalf("old record survived: %#v", record)
		}
	}
	// Die Quellen selbst bleiben stehen, damit ihre Aggregate nicht neu entstehen müssen.
	if _, ok, err := store.source("claude:alt"); !ok || err != nil {
		t.Fatalf("source removed: ok=%v err=%v", ok, err)
	}
	var ftsCount int
	if err := store.db.QueryRow(`SELECT count(*) FROM events_fts`).Scan(&ftsCount); err != nil {
		t.Fatal(err)
	}
	if ftsCount != 2 {
		t.Fatalf("fts rows = %d, want 2", ftsCount)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run TestHistoryStorePrune -v`
Expected: FAIL, `store.pruneEvents undefined`

- [ ] **Step 3: Write minimal implementation**

```go
const historyRetentionWindow = 14 * 24 * time.Hour

// pruneEvents entfernt Roh-Events außerhalb des Aufbewahrungsfensters. Quellen
// und Aggregate bleiben stehen: die Aggregate sind dauerhaft, und die Quellen
// verhindern, dass eine unveränderte Datei erneut geparst wird.
func (s *historyStore) pruneEvents(ctx context.Context, cutoff time.Time) (int, error) {
	bound := cutoff.UTC().UnixNano()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("prune events: %w", err)
	}
	defer tx.Rollback()
	const condition = `(occurred_at IS NOT NULL AND occurred_at < ?)
		OR (occurred_at IS NULL AND source_id IN (SELECT source_id FROM sources WHERE mod_time < ?))`
	if _, err := tx.ExecContext(ctx, `DELETE FROM events_fts WHERE rowid IN (SELECT rowid FROM events WHERE `+condition+`)`, bound, bound); err != nil {
		return 0, fmt.Errorf("prune event text: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM events WHERE `+condition, bound, bound)
	if err != nil {
		return 0, fmt.Errorf("prune events: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("prune events: %w", err)
	}
	return int(removed), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run TestHistoryStorePrune -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/workhistory_store.go core/workhistory_store_test.go
git commit -m "feat(core): Roh-Events außerhalb des Aufbewahrungsfensters entfernen"
```

---

### Task 6: Aktivitätsaggregate schreiben

**Files:**
- Modify: `core/workhistory_store.go`
- Modify: `core/workhistory_store_test.go`

**Interfaces:**
- Consumes: `historyRecord`, `modelCost` (`core/stats.go`), `statsUsageFactState`-Logik.
- Produces:
  - `type historyActivityRow struct { AggKey, Day string; Hour int; Provider HistoryProvider; Model, SourceID, ConversationID, CWD, ProjectAlias string; WrittenFromModTime int64; Prompts, Turns int; Input, Output, CacheRead, CacheWrite int64; Cost float64; PricedEvents, UnpricedEvents, KnownUsageEvents, UnknownUsageEvents int }`
  - `historyActivityRowsFor(records []historyRecord, sourceID string, modTime int64, loc *time.Location) []historyActivityRow`
  - `(*historyStore).writeActivity(ctx context.Context, rows []historyActivityRow) error`

Die Schreibregel gegen Dopplung über Quellen hinweg steht in der Spezifikation unter „Dieselbe Conversation in zwei Quellen": `agg_key` ist die `conversation_id`, sonst die `source_id`; geschrieben wird nur, wenn `written_from_mod_time` der Quelle größer oder gleich dem höchsten bereits gespeicherten Wert dieses `agg_key` ist, und dann werden alle Zeilen des `agg_key` ersetzt.

- [ ] **Step 1: Write the failing test**

```go
func TestHistoryActivityRowsAggregateByHourAndModel(t *testing.T) {
	records := []historyRecord{
		{ID: "p1", Provider: HistoryProviderClaude, ConversationID: "conv-1", Timestamp: "2026-08-30T10:05:00Z",
			Role: HistoryRoleUser, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: "frage", CWD: "/work/demo"},
		{ID: "a1", Provider: HistoryProviderClaude, ConversationID: "conv-1", Timestamp: "2026-08-30T10:06:00Z",
			Role: HistoryRoleAssistant, Kind: HistoryEventOutput, Lineage: HistoryLineagePrimary, Text: "antwort",
			Model: "claude-opus", CWD: "/work/demo",
			Usage: historyUsageRecord{Input: 10, InputKnown: true, Output: 5, OutputKnown: true}},
		{ID: "a2", Provider: HistoryProviderClaude, ConversationID: "conv-1", Timestamp: "2026-08-30T11:00:00Z",
			Role: HistoryRoleAssistant, Kind: HistoryEventOutput, Lineage: HistoryLineagePrimary, Text: "später",
			Model: "claude-opus", CWD: "/work/demo"},
	}
	rows := historyActivityRowsFor(records, "claude:a", 500, time.UTC)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (zwei Stunden): %#v", len(rows), rows)
	}
	byHour := map[int]historyActivityRow{}
	for _, row := range rows {
		byHour[row.Hour] = row
		if row.AggKey != "conv-1" || row.WrittenFromModTime != 500 || row.Day != "2026-08-30" {
			t.Fatalf("row = %#v", row)
		}
	}
	if byHour[10].KnownUsageEvents != 1 || byHour[10].UnknownUsageEvents != 0 {
		t.Fatalf("hour 10 usage coverage = %#v", byHour[10])
	}
	if byHour[10].Prompts != 1 || byHour[10].Turns != 1 || byHour[10].Input != 10 || byHour[10].PricedEvents != 1 {
		t.Fatalf("hour 10 = %#v", byHour[10])
	}
	// Eine Ausgabe ohne Tokenwerte ist unbepreisbar, zählt aber als Turn.
	if byHour[11].Turns != 1 || byHour[11].PricedEvents != 0 || byHour[11].UnpricedEvents != 1 {
		t.Fatalf("hour 11 = %#v", byHour[11])
	}
}

func TestHistoryActivityRowsUseSourceIDWithoutConversation(t *testing.T) {
	records := []historyRecord{
		{ID: "p1", Provider: HistoryProviderCopilot, Timestamp: "2026-08-30T10:05:00Z",
			Role: HistoryRoleUser, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: "frage"},
	}
	rows := historyActivityRowsFor(records, "copilot:a", 5, time.UTC)
	if len(rows) != 1 || rows[0].AggKey != "copilot:a" {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestHistoryStoreActivityNewerSourceWinsAndOlderIsIgnored(t *testing.T) {
	store := openTestHistoryStore(t)
	ctx := context.Background()
	full := []historyActivityRow{{
		AggKey: "conv-1", Day: "2026-08-30", Hour: 10, Provider: HistoryProviderCodex,
		SourceID: "codex:live", WrittenFromModTime: 200, ConversationID: "conv-1", Prompts: 3, Turns: 3,
	}}
	partial := []historyActivityRow{{
		AggKey: "conv-1", Day: "2026-08-30", Hour: 10, Provider: HistoryProviderCodex,
		SourceID: "codex:archiv", WrittenFromModTime: 100, ConversationID: "conv-1", Prompts: 1, Turns: 1,
	}}
	if err := store.writeActivity(ctx, full); err != nil {
		t.Fatal(err)
	}
	if err := store.writeActivity(ctx, partial); err != nil {
		t.Fatal(err)
	}
	var prompts int
	if err := store.db.QueryRow(`SELECT prompts FROM activity WHERE agg_key = 'conv-1'`).Scan(&prompts); err != nil {
		t.Fatal(err)
	}
	if prompts != 3 {
		t.Fatalf("prompts = %d, want 3 — die ältere Quelle darf nicht überschreiben", prompts)
	}

	// Dieselbe Quelle erneut zu schreiben ersetzt, statt zu addieren.
	if err := store.writeActivity(ctx, full); err != nil {
		t.Fatal(err)
	}
	var rowCount int
	if err := store.db.QueryRow(`SELECT count(*) FROM activity`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT prompts FROM activity WHERE agg_key = 'conv-1'`).Scan(&prompts); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 || prompts != 3 {
		t.Fatalf("rows = %d prompts = %d, want 1 and 3", rowCount, prompts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run "TestHistoryActivityRows|TestHistoryStoreActivity" -v`
Expected: FAIL, `undefined: historyActivityRowsFor`

- [ ] **Step 3: Write minimal implementation**

```go
type historyActivityRow struct {
	AggKey             string
	Day                string
	Hour               int
	Provider           HistoryProvider
	Model              string
	SourceID           string
	WrittenFromModTime int64
	ConversationID     string
	CWD                string
	ProjectAlias       string
	Prompts            int
	Turns              int
	Input              int64
	Output             int64
	CacheRead          int64
	CacheWrite         int64
	Cost               float64
	PricedEvents       int
	UnpricedEvents     int
	// Trägt HistoryMeasure.Coverage: wie viele Ereignisse Tokenwerte kannten.
	KnownUsageEvents   int
	UnknownUsageEvents int
}

type historyActivityKey struct {
	aggKey   string
	day      string
	hour     int
	provider HistoryProvider
	model    string
}

// historyActivityRowsFor verdichtet die Datensätze einer Quelle zu dauerhaften
// Kennzahlen. Namen von Projekten und Sessions bleiben bewusst außen vor; sie
// werden bei jeder Abfrage neu aufgelöst, damit Umbenennungen wirken.
func historyActivityRowsFor(records []historyRecord, sourceID string, modTime int64, loc *time.Location) []historyActivityRow {
	if loc == nil {
		loc = time.Local
	}
	byKey := map[historyActivityKey]*historyActivityRow{}
	order := make([]historyActivityKey, 0, len(records))
	for _, record := range records {
		if record.Timestamp == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, record.Timestamp)
		if err != nil {
			continue
		}
		local := parsed.In(loc)
		aggKey := record.ConversationID
		if aggKey == "" {
			aggKey = sourceID
		}
		key := historyActivityKey{
			aggKey: aggKey, day: local.Format(statsDateLayout), hour: local.Hour(),
			provider: record.Provider, model: record.Model,
		}
		row := byKey[key]
		if row == nil {
			row = &historyActivityRow{
				AggKey: aggKey, Day: key.day, Hour: key.hour, Provider: record.Provider,
				Model: record.Model, SourceID: sourceID, WrittenFromModTime: modTime,
				ConversationID: record.ConversationID, CWD: record.CWD, ProjectAlias: record.ProjectAlias,
			}
			byKey[key] = row
			order = append(order, key)
		}
		if row.CWD == "" {
			row.CWD = record.CWD
		}
		if row.ProjectAlias == "" {
			row.ProjectAlias = record.ProjectAlias
		}
		switch record.Kind {
		case HistoryEventPrompt:
			row.Prompts++
			continue
		case HistoryEventOutput:
			row.Turns++
		case HistoryEventUsage:
		default:
			continue
		}
		usage := publicHistoryUsage(record.Usage)
		known, unknown := statsUsageFactState(usage)
		row.Input += knownHistoryValue(usage.InputTokens)
		row.Output += knownHistoryValue(usage.OutputTokens)
		row.CacheRead += knownHistoryValue(usage.CacheReadTokens)
		row.CacheWrite += knownHistoryValue(usage.CacheWriteTokens)
		priced := false
		if record.Provider == HistoryProviderClaude && known {
			cost, ok := modelCost(record.Provider, record.Model,
				knownHistoryValue(usage.InputTokens), knownHistoryValue(usage.OutputTokens),
				knownHistoryValue(usage.CacheReadTokens), knownHistoryValue(usage.CacheWriteTokens))
			row.Cost += cost
			priced = ok
		}
		// Dieselbe Regel wie in statsAcc.addEvent: nur Claude mit bekannten
		// Tokenwerten und passender Preistabelle gilt als bepreist.
		if known {
			row.KnownUsageEvents++
		}
		if unknown {
			row.UnknownUsageEvents++
		}
		if priced {
			row.PricedEvents++
		}
		if record.Provider != HistoryProviderClaude || unknown || (known && !priced) ||
			(record.Kind == HistoryEventOutput && !known) {
			row.UnpricedEvents++
		}
	}
	out := make([]historyActivityRow, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	return out
}

func (s *historyStore) writeActivity(ctx context.Context, rows []historyActivityRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("write activity: %w", err)
	}
	defer tx.Rollback()

	byAggKey := map[string][]historyActivityRow{}
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		if _, seen := byAggKey[row.AggKey]; !seen {
			order = append(order, row.AggKey)
		}
		byAggKey[row.AggKey] = append(byAggKey[row.AggKey], row)
	}

	for _, aggKey := range order {
		group := byAggKey[aggKey]
		var stored sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT max(written_from_mod_time) FROM activity WHERE agg_key = ?`, aggKey).Scan(&stored); err != nil {
			return fmt.Errorf("read activity watermark: %w", err)
		}
		if stored.Valid && group[0].WrittenFromModTime < stored.Int64 {
			// Eine ältere Fassung derselben Conversation. Die vorhandene ist
			// mindestens so vollständig; nichts zu tun.
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM activity WHERE agg_key = ?`, aggKey); err != nil {
			return fmt.Errorf("clear activity: %w", err)
		}
		for _, row := range group {
			if _, err := tx.ExecContext(ctx, `INSERT INTO activity
				(agg_key, day, hour, provider, model, source_id, written_from_mod_time,
				 conversation_id, cwd, project_alias, prompts, turns,
				 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
				 cost, priced_events, unpriced_events, known_usage_events, unknown_usage_events)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				row.AggKey, row.Day, row.Hour, string(row.Provider), row.Model, row.SourceID,
				row.WrittenFromModTime, row.ConversationID, row.CWD, row.ProjectAlias,
				row.Prompts, row.Turns, row.Input, row.Output, row.CacheRead, row.CacheWrite,
				row.Cost, row.PricedEvents, row.UnpricedEvents,
				row.KnownUsageEvents, row.UnknownUsageEvents); err != nil {
				return fmt.Errorf("write activity: %w", err)
			}
		}
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run "TestHistoryActivityRows|TestHistoryStoreActivity" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/workhistory_store.go core/workhistory_store_test.go
git commit -m "feat(core): dauerhafte Aktivitätsaggregate je Conversation schreiben"
```

---

### Task 7: Indexerlauf mit Fortschritt

**Files:**
- Create: `core/workhistory_indexer.go`
- Create: `core/workhistory_indexer_test.go`
- Modify: `core/workhistory.go` (entfernt `historyIndex`, `historyIndexFile`, `loadHistoryIndex`, `saveHistoryIndex`, `protectHistoryIndex`, `refresh`, `refreshIndex`, `indexPath`; `WorkHistory` bekommt neue Felder)

**Interfaces:**
- Consumes: alles aus den Tasks 1–6, `discoverHistoryFiles` (`core/workhistory_adapters.go:48`), `normalizeHistoryRecords` (`core/workhistory.go:768`), `historySourceID` (`core/workhistory.go:759`), `withWorkHistoryFileLock`.
- Produces:
  - `type HistoryIndexProgress struct { Active bool; PendingFiles, CompletedFiles int; StartedAt time.Time }` mit JSON-Namen `active`, `pendingFiles`, `completedFiles`, `startedAt`
  - `HistoryMeta.Progress HistoryIndexProgress` mit JSON-Name `progress`
  - `WorkHistoryConfig.Retention time.Duration`, `WorkHistoryConfig.SynchronousIndex bool`
  - `(*WorkHistory).indexOnce(ctx context.Context) error` — ein vollständiger Durchlauf, synchron
  - `(*WorkHistory).ensureIndexing(ctx context.Context)` — startet einen Lauf im Hintergrund, wenn keiner läuft und der letzte länger als eine Minute her ist
  - `(*WorkHistory).snapshotMeta(ctx context.Context) (HistoryMeta, error)` — Abdeckung je Provider aus `sources` plus Fortschritt

- [ ] **Step 1: Write the failing test**

`core/workhistory_indexer_test.go`:

```go
package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	history, home, _, _ := openTestWorkHistory(t)
	history.config.Retention = 24 * time.Hour
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
```

Ergänze in `core/workhistory_test.go` zwei Helfer und stelle `openTestWorkHistory` auf synchrones Indexieren um:

```go
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
```

In `openTestWorkHistory` (`core/workhistory_test.go:378`) die Konfiguration ergänzen:

```go
	history, err := OpenWorkHistory(WorkHistoryConfig{
		HomeDir: home, IndexDir: indexDir, CodexHome: codexHome,
		// Tests arbeiten mit festen Zeitstempeln in der Vergangenheit und
		// erwarten vollständige, sofort sichtbare Ergebnisse.
		Retention: 100 * 365 * 24 * time.Hour, SynchronousIndex: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { history.Close() })
```

Ergänze `"time"` in den Importen von `core/workhistory_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run TestWorkHistoryIndexer -v`
Expected: FAIL, `history.indexOnce undefined`

- [ ] **Step 3: Write minimal implementation**

Zuerst `core/workhistory.go` aufräumen: `historyIndex`, `historyIndexFile`, `workHistoryIndexVersion`, `loadHistoryIndex`, `saveHistoryIndex`, `protectHistoryIndex`, `refresh`, `refreshIndex` und `indexPath` löschen. `WorkHistory` und `OpenWorkHistory` werden zu:

```go
type WorkHistory struct {
	config   WorkHistoryConfig
	files    workHistoryFS
	adapters []historyProviderAdapter
	now      func() time.Time
	store    *historyStore

	mu        sync.Mutex
	indexing  bool
	lastRunAt time.Time
	progress  HistoryIndexProgress
}

type WorkHistoryConfig struct {
	IndexDir  string
	HomeDir   string
	CodexHome string
	// Retention begrenzt, wie weit Roh-Events zurückreichen. Null bedeutet
	// historyRetentionWindow.
	Retention time.Duration
	// SynchronousIndex lässt Abfragen auf einen vollständigen Lauf warten.
	// Nur für Tests und einmalige Werkzeuge gedacht.
	SynchronousIndex bool
}

func (h *WorkHistory) dbPath() string   { return filepath.Join(h.config.IndexDir, "history.db") }
func (h *WorkHistory) lockPath() string { return filepath.Join(h.config.IndexDir, "index.lock") }
func (h *WorkHistory) Close() error     { return h.store.Close() }

func (h *WorkHistory) retention() time.Duration {
	if h.config.Retention > 0 {
		return h.config.Retention
	}
	return historyRetentionWindow
}
```

In `OpenWorkHistory` nach `h.adapters = builtinHistoryAdapters(config)` ergänzen:

```go
	store, err := openHistoryStore(h.dbPath())
	if err != nil {
		return nil, err
	}
	h.store = store
```

`core/workhistory_indexer.go`:

```go
package core

import (
	"context"
	"sort"
	"time"
)

type HistoryIndexProgress struct {
	Active         bool      `json:"active"`
	PendingFiles   int       `json:"pendingFiles"`
	CompletedFiles int       `json:"completedFiles"`
	StartedAt      time.Time `json:"startedAt,omitempty"`
}

// ensureIndexing stößt einen Hintergrundlauf an, sofern keiner läuft und der
// letzte länger als eine Minute abgeschlossen ist. Abfragen warten nie darauf.
func (h *WorkHistory) ensureIndexing(ctx context.Context) {
	if h.config.SynchronousIndex {
		// Werkzeuge und Tests wollen ein vollständiges Ergebnis.
		_ = h.indexOnce(ctx)
		return
	}
	h.mu.Lock()
	if h.indexing || time.Since(h.lastRunAt) < time.Minute {
		h.mu.Unlock()
		return
	}
	h.indexing = true
	h.progress = HistoryIndexProgress{Active: true, StartedAt: h.now()}
	h.mu.Unlock()
	go func() {
		// Der Lauf überlebt die auslösende Abfrage bewusst.
		_ = h.runIndex(context.Background())
	}()
}

func (h *WorkHistory) indexOnce(ctx context.Context) error {
	h.mu.Lock()
	if h.indexing {
		h.mu.Unlock()
		return nil
	}
	h.indexing = true
	h.progress = HistoryIndexProgress{Active: true, StartedAt: h.now()}
	h.mu.Unlock()
	return h.runIndex(ctx)
}

func (h *WorkHistory) runIndex(ctx context.Context) error {
	defer func() {
		h.mu.Lock()
		h.indexing = false
		h.lastRunAt = h.now()
		h.progress.Active = false
		h.progress.PendingFiles = 0
		h.mu.Unlock()
	}()
	// Der Dateilock hält zwei Magentic-Prozesse davon ab, dieselben
	// Transkripte gleichzeitig zu parsen. Lesende Abfragen brauchen ihn nicht.
	return withWorkHistoryFileLock(ctx, h.lockPath(), func() error {
		return h.indexAllProviders(ctx)
	})
}

type historyIndexCandidate struct {
	adapter historyProviderAdapter
	path    string
	modTime int64
}

func (h *WorkHistory) indexAllProviders(ctx context.Context) error {
	var candidates []historyIndexCandidate
	for _, adapter := range h.adapters {
		if err := ctx.Err(); err != nil {
			return err
		}
		inventory := discoverHistoryFiles(ctx, h.files, adapter)
		seen := map[string]bool{}
		for _, path := range inventory.files {
			sourceID := historySourceID(adapter.Provider(), path)
			seen[sourceID] = true
			info, err := h.files.Stat(path)
			modTime := int64(0)
			if err == nil {
				modTime = info.ModTime().UnixNano()
			}
			candidates = append(candidates, historyIndexCandidate{adapter: adapter, path: path, modTime: modTime})
		}
		if err := h.forgetVanishedSources(adapter, inventory, seen); err != nil {
			return err
		}
		if err := h.rememberDiscoveryProblems(adapter, inventory); err != nil {
			return err
		}
	}
	// Neueste zuerst: der Verlauf zeigt genau diese Einträge.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modTime > candidates[j].modTime })

	h.mu.Lock()
	h.progress.PendingFiles = len(candidates)
	h.progress.CompletedFiles = 0
	h.mu.Unlock()

	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		h.indexOneFile(ctx, candidate)
		h.mu.Lock()
		h.progress.PendingFiles--
		h.progress.CompletedFiles++
		h.mu.Unlock()
	}
	cutoff := h.now().Add(-h.retention())
	if _, err := h.store.pruneEvents(ctx, cutoff); err != nil {
		return err
	}
	return nil
}
```

Der Kern je Datei folgt der bisherigen Logik aus `refreshIndex` (`core/workhistory.go:589`), schreibt aber in den Speicher und legt zusätzlich Aggregate an:

```go
func (h *WorkHistory) indexOneFile(ctx context.Context, candidate historyIndexCandidate) {
	adapter := candidate.adapter
	sourceID := historySourceID(adapter.Provider(), candidate.path)
	record := func(problem HistoryProblem) {
		problem.Provider = adapter.Provider()
		problem.SourceID = sourceID
		row, found, err := h.store.source(sourceID)
		if err != nil {
			return
		}
		if !found {
			row = historySourceRow{SourceID: sourceID, Provider: adapter.Provider(), Path: candidate.path}
		}
		row.Problems = append(row.Problems, problem)
		row.IndexedAt = h.now().UnixNano()
		_ = h.store.writeSourceRow(row)
	}

	data, err := h.files.ReadFile(candidate.path)
	if err != nil {
		record(HistoryProblem{Kind: "file-unreadable", Message: err.Error()})
		return
	}
	digest, err := adapter.Fingerprint(h.files, candidate.path, data)
	if err != nil {
		record(HistoryProblem{Kind: "dependency-unreadable", Message: err.Error()})
		return
	}
	info, err := h.files.Stat(candidate.path)
	if err != nil {
		record(HistoryProblem{Kind: "file-unavailable", Message: err.Error()})
		return
	}
	existing, found, err := h.store.source(sourceID)
	if err != nil {
		return
	}
	if found && existing.Provider == adapter.Provider() &&
		existing.AdapterVersion == adapter.Version() && existing.Digest == digest {
		if existing.Size != info.Size() || existing.ModTime != info.ModTime().UnixNano() {
			existing.Size, existing.ModTime = info.Size(), info.ModTime().UnixNano()
			existing.IndexedAt = h.now().UnixNano()
			_ = h.store.writeSourceRow(existing)
		}
		h.noteReused(adapter.Provider())
		return
	}
	records, problems, parseErr := adapter.Parse(ctx, h.files, candidate.path, data)
	for i := range problems {
		problems[i].Provider = adapter.Provider()
		problems[i].SourceID = sourceID
	}
	if parseErr != nil {
		record(HistoryProblem{Kind: "parse-failed", Message: parseErr.Error()})
		return
	}
	records = normalizeHistoryRecords(adapter.Provider(), sourceID, records)
	row := historySourceRow{
		SourceID: sourceID, Provider: adapter.Provider(), Path: candidate.path,
		AdapterVersion: adapter.Version(), Digest: digest, Size: info.Size(),
		ModTime: info.ModTime().UnixNano(), IndexedAt: h.now().UnixNano(), Problems: problems,
	}
	// Erst die Aggregate, dann die Events: der Verfall darf keine Kennzahlen
	// verschlucken, die es sonst nie in activity geschafft hätten.
	if err := h.store.writeActivity(ctx, historyActivityRowsFor(records, sourceID, row.ModTime, time.Local)); err != nil {
		return
	}
	if err := h.store.replaceSource(row, records); err != nil {
		return
	}
	h.noteParsed(adapter.Provider())
}
```

Wiederverwendungs- und Parse-Zähler sowie Entdeckungsprobleme leben für die Dauer eines Laufs im Speicher der Instanz und fließen in `snapshotMeta` ein:

```go
type historyRunCounters struct {
	parsed, reused int
	discovery      historyDiscovery
}

// In WorkHistory ergänzen: counters map[HistoryProvider]*historyRunCounters
func (h *WorkHistory) noteParsed(provider HistoryProvider) { h.bumpCounter(provider, 1, 0) }
func (h *WorkHistory) noteReused(provider HistoryProvider) { h.bumpCounter(provider, 0, 1) }

func (h *WorkHistory) bumpCounter(provider HistoryProvider, parsed, reused int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.counters == nil {
		h.counters = map[HistoryProvider]*historyRunCounters{}
	}
	counter := h.counters[provider]
	if counter == nil {
		counter = &historyRunCounters{}
		h.counters[provider] = counter
	}
	counter.parsed += parsed
	counter.reused += reused
}

func (h *WorkHistory) rememberDiscoveryProblems(adapter historyProviderAdapter, inventory historyDiscovery) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.counters == nil {
		h.counters = map[HistoryProvider]*historyRunCounters{}
	}
	counter := h.counters[adapter.Provider()]
	if counter == nil {
		counter = &historyRunCounters{}
		h.counters[adapter.Provider()] = counter
	}
	counter.parsed, counter.reused = 0, counter.reused
	counter.discovery = inventory
	return nil
}

func (h *WorkHistory) forgetVanishedSources(adapter historyProviderAdapter, inventory historyDiscovery, seen map[string]bool) error {
	state := inventory.coverage.State
	if state != HistorySourceAvailable && state != HistorySourceAbsent {
		// Eine unvollständige Entdeckung darf keine Quellen löschen.
		return nil
	}
	known, err := h.store.sourceIDsByProvider(adapter.Provider())
	if err != nil {
		return err
	}
	var gone []string
	for sourceID := range known {
		if !seen[sourceID] {
			gone = append(gone, sourceID)
		}
	}
	return h.store.deleteSources(gone)
}

func (h *WorkHistory) snapshotMeta(ctx context.Context) (HistoryMeta, error) {
	h.mu.Lock()
	progress := h.progress
	counters := map[HistoryProvider]historyRunCounters{}
	for provider, counter := range h.counters {
		counters[provider] = *counter
	}
	h.mu.Unlock()

	meta := HistoryMeta{ObservedAt: h.now().UTC(), Progress: progress}
	for _, adapter := range h.adapters {
		provider := adapter.Provider()
		counter := counters[provider]
		coverage := counter.discovery.coverage
		coverage.Provider = provider
		problems, indexed, err := h.store.sourceProblems(provider)
		if err != nil {
			return HistoryMeta{}, err
		}
		coverage.IndexedFiles = indexed
		coverage.ParsedFiles = counter.parsed
		coverage.ReusedFiles = counter.reused
		coverage.Problems = append(coverage.Problems, problems...)
		if progress.Active {
			coverage.State = HistorySourcePartial
			coverage.Problems = append(coverage.Problems, HistoryProblem{
				Provider: provider, Kind: "indexing",
				Message: "Der Verlauf wird gerade gelesen",
			})
		} else if coverage.State == "" {
			coverage.State = HistorySourceAbsent
		}
		meta.Coverage = append(meta.Coverage, coverage)
	}
	return meta, nil
}
```

Ergänze das Feld `counters map[HistoryProvider]*historyRunCounters` in `WorkHistory` und `Progress HistoryIndexProgress \`json:"progress"\`` in `HistoryMeta` (`core/workhistory.go:290`).

- [ ] **Step 4: Run test to verify it fails an anderer Stelle**

Run: `go test ./core/ -run TestWorkHistory -v`
Expected: FAIL — `Events`, `Links` und `Summarize` rufen noch das entfernte `refresh` auf. Das ist der Einstieg in Task 8.

- [ ] **Step 5: Commit**

```bash
git add core/workhistory.go core/workhistory_indexer.go core/workhistory_indexer_test.go core/workhistory_test.go
git commit -m "feat(core): Indexerlauf mit Fortschritt und Aufbewahrung"
```

Der Commit lässt das Paket bewusst unübersetzbar zurück; Task 8 schließt die Lücke unmittelbar. Wenn du das vermeiden willst, führe Task 7 und 8 in einem Commit zusammen.

---

### Task 8: Events, Links und Summarize auf den Speicher umstellen

**Files:**
- Modify: `core/workhistory.go`

**Interfaces:**
- Consumes: `(*historyStore).records`, `snapshotMeta`, `ensureIndexing`.
- Produces: `queryHistoryRecords(records []historyRecord, associations HistoryAssociations, query HistoryEventQuery, paginate bool) ([]HistoryEvent, int)` — die bisherige Nachbearbeitung aus `queryHistoryEvents` (`core/workhistory.go:851`), losgelöst vom Speicher.

- [ ] **Step 1: Write the failing test**

Es braucht keinen neuen Test: die vorhandenen Tests in `core/workhistory_test.go`, `core/workhistory_usage_test.go` und `core/workhistory_discovery_test.go` sind der Nachweis. Sie dürfen inhaltlich nicht angefasst werden.

Run: `go test ./core/ -run TestWorkHistory -v`
Expected: FAIL mit Übersetzungsfehlern aus Task 7.

- [ ] **Step 2: Write minimal implementation**

`queryHistoryEvents` in zwei Teile zerlegen. Der Kopf, der über `index.Files` läuft und nach Provider, Rolle, Art und Abstammung filtert, entfällt — das erledigt jetzt SQL. Der Rest bleibt Wort für Wort erhalten:

```go
func queryHistoryRecords(records []historyRecord, associations HistoryAssociations, query HistoryEventQuery, paginate bool) ([]HistoryEvent, int) {
	resolver := newHistoryAssociationResolver(associations)
	projects := historySet(query.ProjectKeys)
	sessions := historySet(query.SessionKeys)
	var events []HistoryEvent
	for _, record := range records {
		event := historyEventFromRecord(record, resolver.resolve(record))
		if len(projects) > 0 && (event.Attribution.ProjectKey.State != HistoryFactKnown || !projects[event.Attribution.ProjectKey.Value]) {
			continue
		}
		if len(sessions) > 0 && (event.Attribution.SessionKey.State != HistoryFactKnown || !sessions[event.Attribution.SessionKey.Value]) {
			continue
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.OccurredAt.State != b.OccurredAt.State {
			return a.OccurredAt.State == HistoryFactKnown
		}
		if a.OccurredAt.State == HistoryFactKnown && !a.OccurredAt.Value.Equal(b.OccurredAt.Value) {
			return a.OccurredAt.Value.After(b.OccurredAt.Value)
		}
		return a.ID < b.ID
	})
	total := len(events)
	if paginate {
		limit := query.Limit
		if limit <= 0 {
			limit = 150
		}
		if limit > 1000 {
			limit = 1000
		}
		events = historyPage(events, query.Offset, limit)
	}
	return events, total
}

func historyFilterFor(query HistoryEventQuery) historyRecordFilter {
	return historyRecordFilter{
		Since: query.Since, Before: query.Before, IncludeUnknownTime: query.IncludeUnknownTime,
		Providers: query.Providers, Roles: query.Roles, Kinds: query.Kinds,
		Lineages: query.Lineages, Text: query.Text,
	}
}

func (h *WorkHistory) read(ctx context.Context, query HistoryEventQuery) ([]historyRecord, HistoryMeta, error) {
	h.ensureIndexing(ctx)
	records, err := h.store.records(ctx, historyFilterFor(query))
	if err != nil {
		return nil, HistoryMeta{}, err
	}
	meta, err := h.snapshotMeta(ctx)
	if err != nil {
		return nil, HistoryMeta{}, err
	}
	return records, meta, nil
}
```

`Events` wird zu:

```go
func (h *WorkHistory) Events(ctx context.Context, associations HistoryAssociations, query HistoryEventQuery) (HistoryEventPage, error) {
	records, meta, err := h.read(ctx, query)
	if err != nil {
		return HistoryEventPage{}, err
	}
	meta.AssociationRevision = associations.Revision
	events, total := queryHistoryRecords(records, associations, query, true)
	if events == nil {
		events = []HistoryEvent{}
	}
	return HistoryEventPage{Events: events, Total: total, Meta: meta}, nil
}
```

`Links` und `Summarize` ändern sich analog: statt `h.refresh(ctx)` und `queryHistoryEvents(index, …)` rufen sie `h.read(ctx, eventQuery)` und `queryHistoryRecords(records, …, false)`. Der übrige Rumpf beider Funktionen bleibt unverändert.

`HistoryMeta.Revision` wird nicht mehr aus einem Zähler in der Indexdatei gespeist, sondern aus der Anzahl abgeschlossener Läufe: setze in `snapshotMeta` `meta.Revision = uint64(progress.CompletedFiles)`. `coherentStatsHistory` (`core/stats.go:579`) vergleicht diesen Wert nur auf Gleichheit zwischen zwei Aufrufen und funktioniert damit weiter, solange kein Lauf dazwischenfällt.

- [ ] **Step 3: Run tests to verify they pass**

Run: `go test ./core/ -run TestWorkHistory -v`
Expected: PASS für alle vorhandenen WorkHistory-Tests.

- [ ] **Step 4: Commit**

```bash
git add core/workhistory.go
git commit -m "refactor(core): Verlaufsabfragen gegen den SQLite-Speicher"
```

---

### Task 9: Aktivitätsabfrage mit Attribution

**Files:**
- Modify: `core/workhistory.go`
- Modify: `core/workhistory_store.go`
- Create: Tests in `core/workhistory_indexer_test.go` anhängen

**Interfaces:**
- Consumes: `historyActivityRow`, `newHistoryAssociationResolver`.
- Produces:
  - `type HistoryActivityQuery struct { Since, Before time.Time; Providers []HistoryProvider; Location *time.Location }`
  - `type HistoryActivityBucket struct { Day string; Hour int; Provider HistoryProvider; ConversationID, Model string; Prompts, Turns int; Usage HistoryUsage; Cost float64; PricedEvents, UnpricedEvents, KnownUsageEvents, UnknownUsageEvents int; Attribution HistoryAttribution }`
  - `type HistoryActivityPage struct { Buckets []HistoryActivityBucket; Meta HistoryMeta }`
  - `(*WorkHistory).Activity(ctx context.Context, associations HistoryAssociations, query HistoryActivityQuery) (HistoryActivityPage, error)`
  - `(*historyStore).activityRows(ctx context.Context, since, before time.Time, providers []HistoryProvider, loc *time.Location) ([]historyActivityRow, error)`

- [ ] **Step 1: Write the failing test**

```go
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
	if len(page.Buckets) != 1 {
		t.Fatalf("buckets = %#v", page.Buckets)
	}
	bucket := page.Buckets[0]
	if bucket.Prompts != 1 || bucket.Turns != 1 {
		t.Fatalf("counts = %#v", bucket)
	}
	if bucket.Attribution.ProjectName.State != HistoryFactKnown || bucket.Attribution.ProjectName.Value != "Neuer Name" {
		t.Fatalf("project attribution = %#v", bucket.Attribution.ProjectName)
	}
	if bucket.Attribution.SessionKey.Value != "session-1" {
		t.Fatalf("session attribution = %#v", bucket.Attribution.SessionKey)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run TestWorkHistoryActivity -v`
Expected: FAIL, `history.Activity undefined`

- [ ] **Step 3: Write minimal implementation**

In `core/workhistory_store.go`:

```go
func (s *historyStore) activityRows(ctx context.Context, since, before time.Time, providers []HistoryProvider, loc *time.Location) ([]historyActivityRow, error) {
	if loc == nil {
		loc = time.Local
	}
	where := []string{"1 = 1"}
	var args []any
	if !since.IsZero() {
		where = append(where, "day >= ?")
		args = append(args, since.In(loc).Format(statsDateLayout))
	}
	if !before.IsZero() {
		where = append(where, "day < ?")
		args = append(args, before.In(loc).Format(statsDateLayout))
	}
	if len(providers) > 0 {
		names := historyStrings(providers)
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
		where = append(where, "provider IN ("+placeholders+")")
		for _, name := range names {
			args = append(args, name)
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT agg_key, day, hour, provider, model, source_id,
		written_from_mod_time, conversation_id, cwd, project_alias, prompts, turns,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		cost, priced_events, unpriced_events, known_usage_events, unknown_usage_events
		FROM activity WHERE `+strings.Join(where, " AND ")+` ORDER BY day, hour`, args...)
	if err != nil {
		return nil, fmt.Errorf("read activity: %w", err)
	}
	defer rows.Close()
	var out []historyActivityRow
	for rows.Next() {
		var row historyActivityRow
		var provider string
		if err := rows.Scan(&row.AggKey, &row.Day, &row.Hour, &provider, &row.Model, &row.SourceID,
			&row.WrittenFromModTime, &row.ConversationID, &row.CWD, &row.ProjectAlias,
			&row.Prompts, &row.Turns, &row.Input, &row.Output, &row.CacheRead, &row.CacheWrite,
			&row.Cost, &row.PricedEvents, &row.UnpricedEvents,
			&row.KnownUsageEvents, &row.UnknownUsageEvents); err != nil {
			return nil, fmt.Errorf("read activity: %w", err)
		}
		row.Provider = HistoryProvider(provider)
		out = append(out, row)
	}
	return out, rows.Err()
}
```

In `core/workhistory.go`:

```go
type HistoryActivityQuery struct {
	Since, Before time.Time
	Providers     []HistoryProvider
	Location      *time.Location
}

type HistoryActivityBucket struct {
	Day            string             `json:"day"`
	Hour           int                `json:"hour"`
	Provider       HistoryProvider    `json:"provider"`
	ConversationID string             `json:"conversationId"`
	Model          string             `json:"model"`
	Prompts        int                `json:"prompts"`
	Turns          int                `json:"turns"`
	Usage          HistoryUsage       `json:"usage"`
	Cost           float64            `json:"cost"`
	PricedEvents   int                `json:"pricedEvents"`
	UnpricedEvents int                `json:"unpricedEvents"`
	KnownUsageEvents   int            `json:"knownUsageEvents"`
	UnknownUsageEvents int            `json:"unknownUsageEvents"`
	Attribution    HistoryAttribution `json:"attribution"`
}

type HistoryActivityPage struct {
	Buckets []HistoryActivityBucket `json:"buckets"`
	Meta    HistoryMeta             `json:"meta"`
}

// Activity liefert die dauerhaften Kennzahlen. Sie überdauern das
// Aufbewahrungsfenster der Roh-Events und tragen die Merkmale, aus denen
// dieselbe Attribution entsteht wie bei Events.
func (h *WorkHistory) Activity(ctx context.Context, associations HistoryAssociations, query HistoryActivityQuery) (HistoryActivityPage, error) {
	h.ensureIndexing(ctx)
	rows, err := h.store.activityRows(ctx, query.Since, query.Before, query.Providers, query.Location)
	if err != nil {
		return HistoryActivityPage{}, err
	}
	meta, err := h.snapshotMeta(ctx)
	if err != nil {
		return HistoryActivityPage{}, err
	}
	meta.AssociationRevision = associations.Revision
	resolver := newHistoryAssociationResolver(associations)
	buckets := make([]HistoryActivityBucket, 0, len(rows))
	for _, row := range rows {
		attribution := resolver.resolve(historyRecord{
			Provider: row.Provider, ConversationID: row.ConversationID,
			CWD: row.CWD, ProjectAlias: row.ProjectAlias,
		})
		buckets = append(buckets, HistoryActivityBucket{
			Day: row.Day, Hour: row.Hour, Provider: row.Provider,
			ConversationID: row.ConversationID, Model: row.Model,
			Prompts: row.Prompts, Turns: row.Turns,
			Usage: HistoryUsage{
				InputTokens:      historyKnown(row.Input),
				OutputTokens:     historyKnown(row.Output),
				CacheReadTokens:  historyKnown(row.CacheRead),
				CacheWriteTokens: historyKnown(row.CacheWrite),
			},
			Cost: row.Cost, PricedEvents: row.PricedEvents, UnpricedEvents: row.UnpricedEvents,
			KnownUsageEvents: row.KnownUsageEvents, UnknownUsageEvents: row.UnknownUsageEvents,
			Attribution: attribution,
		})
	}
	return HistoryActivityPage{Buckets: buckets, Meta: meta}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run TestWorkHistoryActivity -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/workhistory.go core/workhistory_store.go core/workhistory_indexer_test.go
git commit -m "feat(core): Aktivitätsabfrage mit frisch aufgelöster Attribution"
```

---

### Task 10: Statistik auf Aktivitätsbuckets umstellen

**Files:**
- Modify: `core/stats.go:427-470`, `core/stats.go:579`
- Modify: `core/stats_test.go` (Datei existiert nicht — dann `core/status_test.go` unberührt lassen und einen neuen Test in `core/workhistory_indexer_test.go` anlegen)

**Interfaces:**
- Consumes: `HistoryActivityPage`, `HistoryActivityBucket` aus Task 9.
- Produces: `(*statsAcc).addBucket(bucket HistoryActivityBucket)`; `buildStats` liest Buckets statt Events.

- [ ] **Step 1: Write the failing test**

An `core/workhistory_indexer_test.go` anhängen:

```go
func TestBuildStatsMatchesBetweenEventsAndBuckets(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	path := filepath.Join(home, ".claude", "projects", "-work-demo", "a.jsonl")
	writeHistoryTestFile(t, path, strings.Join([]string{
		`{"type":"user","uuid":"u-1","timestamp":"2026-08-30T10:00:00Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"user","content":"Prompt"}}`,
		`{"type":"assistant","uuid":"a-1","timestamp":"2026-08-30T10:00:05Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"assistant","model":"claude-opus","content":"Antwort","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3}}}`,
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
		Since: time.Date(2026, 8, 25, 0, 0, 0, 0, time.Local),
		Before: time.Date(2026, 9, 2, 0, 0, 0, 0, time.Local),
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run TestBuildStatsMatches -v`
Expected: FAIL, `fromBuckets.addBucket undefined`

- [ ] **Step 3: Write minimal implementation**

In `core/stats.go` neben `addEvent` (`core/stats.go:268`):

```go
// addBucket ist der dauerhafte Weg in dieselben Kennzahlen. Ein Bucket fasst
// die Ereignisse einer Stunde zusammen; Zuordnung und Kostenzustand entstehen
// aus denselben Merkmalen wie bei addEvent.
func (a *statsAcc) addBucket(bucket HistoryActivityBucket) {
	day := statsSlotFor(a.days, bucket.Day)
	project := statsBucketProject(bucket)
	projectSlot := statsSlotFor(a.projects, project)
	if session := statsBucketSession(bucket); session != "" {
		day.sessions[session] = true
		projectSlot.sessions[session] = true
	}
	if bucket.Prompts > 0 {
		a.hours[bucket.Hour] += bucket.Prompts
		if weekday := statsWeekdayIndex(bucket.Day); weekday >= 0 {
			a.heatmap[weekday][bucket.Hour] += bucket.Prompts
		}
	}
	activity := statsAgg{
		Prompts: bucket.Prompts, Turns: bucket.Turns,
		Input:     knownHistoryValue(bucket.Usage.InputTokens),
		Output:    knownHistoryValue(bucket.Usage.OutputTokens),
		CacheRead: knownHistoryValue(bucket.Usage.CacheReadTokens),
		CacheWrite: knownHistoryValue(bucket.Usage.CacheWriteTokens),
		Cost:       bucket.Cost,
		costPriced: bucket.PricedEvents > 0,
		costUnknown: bucket.UnpricedEvents > 0,
	}
	day.add(activity)
	projectSlot.add(activity)
}

func statsBucketProject(bucket HistoryActivityBucket) string {
	if bucket.Attribution.ProjectName.State == HistoryFactKnown && bucket.Attribution.ProjectName.Value != "" {
		return bucket.Attribution.ProjectName.Value
	}
	return statsOtherProject
}

func statsBucketSession(bucket HistoryActivityBucket) string {
	if bucket.Attribution.SessionKey.State == HistoryFactKnown {
		return bucket.Attribution.SessionKey.Value
	}
	if bucket.ConversationID != "" {
		return string(bucket.Provider) + "\x00" + bucket.ConversationID
	}
	return ""
}
```

In `buildStatsWithRepositories` (`core/stats.go:431`) die Beschaffung ersetzen:

```go
	var buckets []HistoryActivityBucket
	var activityCoverage []HistoryProviderCoverage
	if historyErr == nil && history != nil {
		var page HistoryActivityPage
		page, historyErr = history.Activity(ctx, NewHistoryAssociations(*state), HistoryActivityQuery{
			Since: first, Before: before, Location: time.Local,
		})
		if historyErr == nil {
			buckets = page.Buckets
			activityCoverage = page.Meta.Coverage
		}
	}
```

und die Schleife (`core/stats.go:470`):

```go
	acc := newStatsAcc()
	for _, bucket := range buckets {
		acc.addBucket(bucket)
	}
```

`coherentStatsHistory` und `pagedStatsHistory` werden dadurch unbenutzt. Lösche beide Funktionen samt ihrer Tests, falls es welche gibt — sie sind durch die gemeinsame Lesetransaktion des Speichers ersetzt.

Achtung: `Summarize` liest weiterhin Roh-Events und deckt damit nur 14 Tage ab. Die Summen in `StatsTotals` (`core/stats.go:509-517`) müssen daher ebenfalls aus den Buckets kommen. Ersetze diesen Block durch:

```go
	var bucketTotals statsAgg
	sessionKeys := map[string]bool{}
	for _, bucket := range buckets {
		bucketTotals.add(statsAgg{
			Prompts: bucket.Prompts, Turns: bucket.Turns,
			Input:     knownHistoryValue(bucket.Usage.InputTokens),
			Output:    knownHistoryValue(bucket.Usage.OutputTokens),
			CacheRead: knownHistoryValue(bucket.Usage.CacheReadTokens),
			CacheWrite: knownHistoryValue(bucket.Usage.CacheWriteTokens),
		})
		if key := statsBucketSession(bucket); key != "" {
			sessionKeys[key] = true
		}
	}
	totals.Prompts = bucketTotals.Prompts
	totals.Turns = bucketTotals.Turns
	totals.Sessions = len(sessionKeys)
	totals.Input = bucketTotals.Input
	totals.Output = bucketTotals.Output
	totals.CacheRead = bucketTotals.CacheRead
	totals.CacheWrite = bucketTotals.CacheWrite
	totals.Tokens = totals.Input + totals.Output + totals.CacheRead + totals.CacheWrite
```

`buildStatsModels` (`core/stats.go:785`) und `buildStatsProviders` (`core/stats.go:839`) lesen bisher aus `summary` und deckten damit nur das Fenster ab. Ersetze beide durch Fassungen über Buckets:

```go
func buildStatsModelsFromBuckets(buckets []HistoryActivityBucket) []StatsModel {
	type key struct {
		provider HistoryProvider
		model    string
	}
	acc := map[key]*StatsModel{}
	priced := map[key]bool{}
	unpriced := map[key]bool{}
	var order []key
	for _, bucket := range buckets {
		name := bucket.Model
		if name == "unknown" || name == "" {
			name = "unbekannt"
		}
		id := key{provider: bucket.Provider, model: name}
		model := acc[id]
		if model == nil {
			model = &StatsModel{Model: name, Provider: string(bucket.Provider), Source: bucket.Provider.Label()}
			acc[id] = model
			order = append(order, id)
		}
		model.Turns += bucket.Turns
		model.Input += knownHistoryValue(bucket.Usage.InputTokens)
		model.Output += knownHistoryValue(bucket.Usage.OutputTokens)
		model.CacheRead += knownHistoryValue(bucket.Usage.CacheReadTokens)
		model.CacheWrite += knownHistoryValue(bucket.Usage.CacheWriteTokens)
		model.Cost += bucket.Cost
		priced[id] = priced[id] || bucket.PricedEvents > 0
		unpriced[id] = unpriced[id] || bucket.UnpricedEvents > 0
	}
	models := make([]StatsModel, 0, len(order))
	for _, id := range order {
		model := *acc[id]
		switch {
		case priced[id] && unpriced[id]:
			model.CostState = StatsCostPartial
		case priced[id]:
			model.CostState = StatsCostPriced
		case unpriced[id] || model.Turns > 0:
			model.CostState = StatsCostUnpriced
		default:
			model.CostState = StatsCostNone
		}
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Cost != models[j].Cost {
			return models[i].Cost > models[j].Cost
		}
		return models[i].Model < models[j].Model
	})
	return models
}

func buildStatsProvidersFromBuckets(buckets []HistoryActivityBucket, coverage []HistoryProviderCoverage) []StatsProvider {
	states := map[HistoryProvider]HistorySourceState{}
	problems := map[HistoryProvider][]HistoryProblem{}
	for _, entry := range coverage {
		states[entry.Provider] = entry.State
		problems[entry.Provider] = append([]HistoryProblem(nil), entry.Problems...)
	}
	acc := map[HistoryProvider]*StatsProvider{}
	var order []HistoryProvider
	for _, bucket := range buckets {
		provider := acc[bucket.Provider]
		if provider == nil {
			provider = &StatsProvider{
				Provider: string(bucket.Provider), Source: bucket.Provider.Label(),
				State: states[bucket.Provider], Problems: problems[bucket.Provider],
			}
			acc[bucket.Provider] = provider
			order = append(order, bucket.Provider)
		}
		provider.Prompts += bucket.Prompts
		provider.Turns += bucket.Turns
		for _, measure := range []struct {
			target *HistoryMeasure
			value  HistoryFact[int64]
		}{
			{&provider.Usage.InputTokens, bucket.Usage.InputTokens},
			{&provider.Usage.OutputTokens, bucket.Usage.OutputTokens},
			{&provider.Usage.CacheReadTokens, bucket.Usage.CacheReadTokens},
			{&provider.Usage.CacheWriteTokens, bucket.Usage.CacheWriteTokens},
		} {
			measure.target.Value += knownHistoryValue(measure.value)
			measure.target.KnownEvents += bucket.KnownUsageEvents
			measure.target.UnknownEvents += bucket.UnknownUsageEvents
		}
	}
	providers := make([]StatsProvider, 0, len(order))
	for _, id := range order {
		provider := *acc[id]
		for _, measure := range []*HistoryMeasure{
			&provider.Usage.InputTokens, &provider.Usage.OutputTokens,
			&provider.Usage.CacheReadTokens, &provider.Usage.CacheWriteTokens,
		} {
			// Dieselbe Abstufung wie historyMeasureResult: erst vollständig,
			// wenn kein Ereignis ohne Tokenwerte beigetragen hat.
			switch {
			case measure.KnownEvents > 0 && measure.UnknownEvents == 0:
				measure.Coverage = HistoryCoverageComplete
			case measure.KnownEvents > 0:
				measure.Coverage = HistoryCoveragePartial
			default:
				measure.Coverage = HistoryCoverageNone
			}
		}
		provider.Tokens = provider.Usage.InputTokens.Value + provider.Usage.OutputTokens.Value +
			provider.Usage.CacheReadTokens.Value + provider.Usage.CacheWriteTokens.Value
		providers = append(providers, provider)
	}
	return providers
}
```

Vergleiche die Abstufung mit `historyMeasureResult` (`core/workhistory.go:1195`); sie muss dieselbe Bedeutung ergeben, sonst meldet die Statistik eine Vollständigkeit, die es nicht gibt.

Ersetze die beiden Aufrufe:

```go
	result.Models = buildStatsModelsFromBuckets(buckets)
	result.Providers = buildStatsProvidersFromBuckets(buckets, activityCoverage)
```

Damit wird `summary` nirgends mehr gebraucht. Entferne den `Summarize`-Aufruf aus `buildStatsWithRepositories`, halte stattdessen `activityCoverage := page.Meta.Coverage` fest und speise `statsHistoryCoverageIncomplete(activityCoverage)` daraus.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run "TestBuildStats|TestStats" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/stats.go core/workhistory_indexer_test.go
git commit -m "feat(core): Statistik aus dauerhaften Aktivitätsaggregaten"
```

---

### Task 11: Conversations-Abfrage

**Files:**
- Modify: `core/workhistory.go`, `core/workhistory_store.go`
- Modify: `core/workhistory_indexer_test.go`

**Interfaces:**
- Consumes: `historyStore`, `newHistoryAssociationResolver`.
- Produces: `HistoryConversationQuery`, `HistoryConversation`, `HistoryConversationPage`, `(*WorkHistory).Conversations(...)` genau wie in der Spezifikation beschrieben.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run TestWorkHistoryConversations -v`
Expected: FAIL, `history.Conversations undefined`

- [ ] **Step 3: Write minimal implementation**

```go
type HistoryConversationQuery struct {
	Since, Before time.Time
	Providers     []HistoryProvider
	ProjectKeys   []string
	SessionKeys   []string
	Limit, Offset int
}

type HistoryConversation struct {
	Provider       HistoryProvider        `json:"provider"`
	ConversationID string                 `json:"conversationId"`
	StartedAt      HistoryFact[time.Time] `json:"startedAt"`
	LastActivityAt HistoryFact[time.Time] `json:"lastActivityAt"`
	Turns          int                    `json:"turns"`
	LastPrompt     HistoryFact[string]    `json:"lastPrompt"`
	Attribution    HistoryAttribution     `json:"attribution"`
}

type HistoryConversationPage struct {
	Conversations []HistoryConversation `json:"conversations"`
	Total         int                   `json:"total"`
	Meta          HistoryMeta           `json:"meta"`
}

// Conversations fasst die Roh-Events des Aufbewahrungsfensters zu Chats
// zusammen. Ältere Chats erscheinen hier nicht mehr; ihre Kennzahlen leben in
// Activity weiter.
func (h *WorkHistory) Conversations(ctx context.Context, associations HistoryAssociations, query HistoryConversationQuery) (HistoryConversationPage, error) {
	eventQuery := HistoryEventQuery{
		Since: query.Since, Before: query.Before, Providers: query.Providers,
		ProjectKeys: query.ProjectKeys, SessionKeys: query.SessionKeys,
		Lineages: []HistoryLineage{HistoryLineagePrimary},
	}
	records, meta, err := h.read(ctx, eventQuery)
	if err != nil {
		return HistoryConversationPage{}, err
	}
	meta.AssociationRevision = associations.Revision
	events, _ := queryHistoryRecords(records, associations, eventQuery, false)

	type key struct {
		provider       HistoryProvider
		conversationID string
	}
	byKey := map[key]*HistoryConversation{}
	var order []key
	for _, event := range events {
		if event.ConversationID.State != HistoryFactKnown || event.ConversationID.Value == "" {
			continue
		}
		id := key{provider: event.Provider, conversationID: event.ConversationID.Value}
		conversation := byKey[id]
		if conversation == nil {
			conversation = &HistoryConversation{
				Provider: event.Provider, ConversationID: event.ConversationID.Value,
				StartedAt:      historyUnknown[time.Time]("kein bekannter Zeitpunkt"),
				LastActivityAt: historyUnknown[time.Time]("kein bekannter Zeitpunkt"),
				LastPrompt:     historyUnknown[string]("kein Prompt im Fenster"),
				Attribution:    event.Attribution,
			}
			byKey[id] = conversation
			order = append(order, id)
		}
		if event.Kind == HistoryEventOutput {
			conversation.Turns++
		}
		if event.OccurredAt.State != HistoryFactKnown {
			continue
		}
		when := event.OccurredAt.Value
		if conversation.StartedAt.State != HistoryFactKnown || when.Before(conversation.StartedAt.Value) {
			conversation.StartedAt = historyKnown(when)
		}
		if conversation.LastActivityAt.State != HistoryFactKnown || when.After(conversation.LastActivityAt.Value) {
			conversation.LastActivityAt = historyKnown(when)
		}
		if event.Kind == HistoryEventPrompt && event.Text.State == HistoryFactKnown {
			// queryHistoryRecords liefert absteigend; der erste Prompt ist der jüngste.
			if conversation.LastPrompt.State != HistoryFactKnown {
				conversation.LastPrompt = historyKnown(event.Text.Value)
			}
		}
	}
	out := make([]HistoryConversation, 0, len(order))
	for _, id := range order {
		out = append(out, *byKey[id])
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.LastActivityAt.State != b.LastActivityAt.State {
			return a.LastActivityAt.State == HistoryFactKnown
		}
		if a.LastActivityAt.State == HistoryFactKnown && !a.LastActivityAt.Value.Equal(b.LastActivityAt.Value) {
			return a.LastActivityAt.Value.After(b.LastActivityAt.Value)
		}
		return a.ConversationID < b.ConversationID
	})
	total := len(out)
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	out = historyPage(out, query.Offset, limit)
	if out == nil {
		out = []HistoryConversation{}
	}
	return HistoryConversationPage{Conversations: out, Total: total, Meta: meta}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run TestWorkHistoryConversations -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/workhistory.go core/workhistory_indexer_test.go
git commit -m "feat(core): Chats über Provider hinweg als Conversations abfragen"
```

---

### Task 12: Eine gemeinsame Instanz je Prozess

**Files:**
- Modify: `core/workhistory.go`
- Modify: `app/tools.go:120`, `app/tools.go:186`, `app/tools.go:345`
- Modify: `core/stats.go:422`, `core/provider_run.go:61`

**Interfaces:**
- Consumes: `OpenWorkHistory`.
- Produces: `SharedWorkHistory() (*WorkHistory, error)` — öffnet beim ersten Aufruf, gibt danach dieselbe Instanz zurück.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run TestSharedWorkHistory -v`
Expected: FAIL, `undefined: SharedWorkHistory`

- [ ] **Step 3: Write minimal implementation**

```go
var (
	sharedWorkHistoryOnce sync.Once
	sharedWorkHistory     *WorkHistory
	sharedWorkHistoryErr  error
)

// SharedWorkHistory hält eine Instanz je Prozess. Der Index ist eine
// gemeinsame Datenbank; jede Abfrage eine eigene Instanz zu öffnen würde
// Verbindungen und Hintergrundläufe vervielfachen.
func SharedWorkHistory() (*WorkHistory, error) {
	sharedWorkHistoryOnce.Do(func() {
		sharedWorkHistory, sharedWorkHistoryErr = OpenWorkHistory(WorkHistoryConfig{})
	})
	return sharedWorkHistory, sharedWorkHistoryErr
}

func resetSharedWorkHistoryForTest() {
	sharedWorkHistoryOnce = sync.Once{}
	sharedWorkHistory, sharedWorkHistoryErr = nil, nil
}
```

Ersetze in `core/stats.go:423`, `core/provider_run.go:61`, `app/tools.go:120`, `app/tools.go:186` und `app/tools.go:345` jeweils `OpenWorkHistory(WorkHistoryConfig{})` bzw. `core.OpenWorkHistory(core.WorkHistoryConfig{})` durch `SharedWorkHistory()` bzw. `core.SharedWorkHistory()`. Die Fehlerbehandlung an jeder Stelle bleibt unverändert.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run TestSharedWorkHistory -v` und `go build ./... && (cd app && go build ./...)`
Expected: PASS und beide Builds ohne Fehler.

- [ ] **Step 5: Commit**

```bash
git add core/workhistory.go core/stats.go core/provider_run.go app/tools.go
git commit -m "refactor: eine gemeinsame Verlaufsinstanz je Prozess"
```

---

### Task 13: Übergang vom alten Index

**Files:**
- Create: `core/workhistory_legacy.go`
- Create: `core/workhistory_legacy_test.go`
- Modify: `core/workhistory_indexer.go` (Aufruf am Anfang von `indexAllProviders`)

**Interfaces:**
- Consumes: `(*historyStore).writeActivity`, `historyActivityRowsFor`.
- Produces: `(*WorkHistory).adoptLegacyIndex(ctx context.Context) error` — liest ein vorhandenes `index.json`, leitet ausschließlich Aggregate ab, löscht die Datei danach. Bei Fehler wird sie nach `index.json.rejected` umbenannt und ein `HistoryProblem` der Art `legacy-index` vermerkt.

- [ ] **Step 1: Write the failing test**

```go
func TestAdoptLegacyIndexDerivesAggregatesAndRemovesFile(t *testing.T) {
	history, _, indexDir, _ := openTestWorkHistory(t)
	legacy := filepath.Join(indexDir, "index.json")
	writeHistoryTestFile(t, legacy, `{"version":1,"revision":3,"files":{"claude:alt":{
		"provider":"claude","adapterVersion":1,"digest":"d","size":10,"modTime":12345,
		"records":[
			{"id":"alt-1","sourceId":"claude:alt","provider":"claude","conversationId":"conv-alt",
			 "timestamp":"2026-01-05T10:00:00Z","role":"user","kind":"prompt","lineage":"primary","text":"sehr alt","cwd":"/work/demo"}
		]}}}`)

	if err := history.adoptLegacyIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	var prompts int
	if err := history.store.db.QueryRow(`SELECT sum(prompts) FROM activity WHERE agg_key = 'conv-alt'`).Scan(&prompts); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 {
		t.Fatalf("adopted prompts = %d, want 1", prompts)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy index still present: %v", err)
	}
	var events int
	if err := history.store.db.QueryRow(`SELECT count(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("events adopted = %d, want 0 — nur Aggregate werden übernommen", events)
	}
}

func TestAdoptLegacyIndexRenamesBrokenFile(t *testing.T) {
	history, _, indexDir, _ := openTestWorkHistory(t)
	legacy := filepath.Join(indexDir, "index.json")
	writeHistoryTestFile(t, legacy, `{ kaputt`)
	if err := history.adoptLegacyIndex(context.Background()); err != nil {
		t.Fatalf("adoption must not fail the run: %v", err)
	}
	if _, err := os.Stat(legacy + ".rejected"); err != nil {
		t.Fatalf("rejected file missing: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run TestAdoptLegacyIndex -v`
Expected: FAIL, `history.adoptLegacyIndex undefined`

- [ ] **Step 3: Write minimal implementation**

`core/workhistory_legacy.go`:

```go
package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// legacyHistoryIndex beschreibt nur so viel von der abgelösten index.json, wie
// für die Ableitung der dauerhaften Kennzahlen nötig ist.
type legacyHistoryIndex struct {
	Version int `json:"version"`
	Files   map[string]struct {
		Provider HistoryProvider `json:"provider"`
		ModTime  int64           `json:"modTime"`
		Records  []historyRecord `json:"records"`
	} `json:"files"`
}

func (h *WorkHistory) legacyIndexPath() string {
	return filepath.Join(h.config.IndexDir, "index.json")
}

// adoptLegacyIndex übernimmt einmalig die Kennzahlen aus der abgelösten
// Indexdatei. Roh-Events werden nicht übernommen: sie sind aus den
// Transkripten reproduzierbar und fallen größtenteils aus dem
// Aufbewahrungsfenster.
func (h *WorkHistory) adoptLegacyIndex(ctx context.Context) error {
	path := h.legacyIndexPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return h.rejectLegacyIndex(path, err)
	}
	var index legacyHistoryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return h.rejectLegacyIndex(path, err)
	}
	for sourceID, file := range index.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		records := normalizeHistoryRecords(file.Provider, sourceID, file.Records)
		rows := historyActivityRowsFor(records, sourceID, file.ModTime, time.Local)
		if err := h.store.writeActivity(ctx, rows); err != nil {
			return h.rejectLegacyIndex(path, err)
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return h.rejectLegacyIndex(path, err)
	}
	return nil
}

func (h *WorkHistory) rejectLegacyIndex(path string, cause error) error {
	// Ein fehlgeschlagener Übergang darf den Indexlauf nicht aufhalten. Die
	// Datei bleibt zur Nachschau liegen, meldet sich aber nie wieder.
	if err := os.Rename(path, path+".rejected"); err != nil && !os.IsNotExist(err) {
		return err
	}
	Logf("work history: alter Index nicht übernommen (%v)", cause)
	return nil
}
```

In `indexAllProviders` als erste Anweisung ergänzen:

```go
	if err := h.adoptLegacyIndex(ctx); err != nil {
		return err
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run TestAdoptLegacyIndex -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/workhistory_legacy.go core/workhistory_legacy_test.go core/workhistory_indexer.go
git commit -m "feat(core): Kennzahlen aus dem alten Index einmalig übernehmen"
```

---

### Task 14: Verlauf im Frontend

**Files:**
- Modify: `app/frontend/index.html:110`
- Modify: `app/frontend/src/main.js:2229-2295`
- Modify: `app/tools.go:340` (`TimelineResult` bekommt den Fortschritt)

**Interfaces:**
- Consumes: `HistoryMeta.Progress` aus Task 7.
- Produces: `TimelineResult.Progress` als `{active: bool, pendingFiles: int, completedFiles: int}` im JSON.

- [ ] **Step 1: Write the failing test**

`app/frontend/src/timeline-progress.test.js` neu anlegen (die vorhandenen Frontend-Tests liegen als `*.test.js` neben ihren Modulen):

```js
import { describe, expect, it } from 'vitest';
import { timelineProgressNotice } from './timeline-progress.js';

describe('timelineProgressNotice', () => {
  it('sagt nichts, wenn kein Lauf aktiv ist', () => {
    expect(timelineProgressNotice({ active: false, pendingFiles: 0 })).toBe('');
    expect(timelineProgressNotice(undefined)).toBe('');
  });

  it('nennt die Zahl der noch zu lesenden Dateien', () => {
    const html = timelineProgressNotice({ active: true, pendingFiles: 42, completedFiles: 8 });
    expect(html).toContain('42');
    expect(html).toContain('noch');
  });

  it('kommt ohne Zahl aus, solange der Lauf noch nichts gezählt hat', () => {
    const html = timelineProgressNotice({ active: true, pendingFiles: 0, completedFiles: 0 });
    expect(html).toContain('gelesen');
    expect(html).not.toContain('0');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd app/frontend && npx vitest run src/timeline-progress.test.js`
Expected: FAIL, Modul nicht gefunden.

- [ ] **Step 3: Write minimal implementation**

`app/frontend/src/timeline-progress.js`:

```js
// Der Verlauf zeigt an, dass noch gelesen wird, ohne das Ergebnis zu verstecken.
export function timelineProgressNotice(progress) {
  if (!progress || !progress.active) return '';
  const pending = Number(progress.pendingFiles) || 0;
  if (pending > 0) {
    return `<div class="tl-progress">Der Verlauf wird noch gelesen, ${pending} Dateien fehlen.</div>`;
  }
  return '<div class="tl-progress">Der Verlauf wird gerade gelesen.</div>';
}
```

In `app/tools.go` das Feld ergänzen:

```go
type TimelineResult struct {
	Entries  []TimelineEntry            `json:"entries"`
	Sources  []TimelineSource           `json:"sources"`
	Progress core.HistoryIndexProgress  `json:"progress"`
}
```

und in `Timeline()` mit `page.Meta.Progress` füllen.

In `app/frontend/index.html:110` den festen Ladezustand entfernen:

```html
        <div id="tl-body"></div>
```

In `app/frontend/src/main.js`:

```js
import { timelineProgressNotice } from './timeline-progress.js';

let tlProgress = null;
```

`refreshTimeline` (ab `app/frontend/src/main.js:2257`) ergänzen:

```js
async function refreshTimeline() {
  if (tlLoading) return;
  tlLoading = true;
  try {
    const result = (await Timeline()) || {};
    tlEntries = Array.isArray(result.entries) ? result.entries : [];
    tlSources = Array.isArray(result.sources) ? result.sources : [];
    tlProgress = result.progress || null;
    renderTimeline();
    scheduleTimelineRefresh();
  } catch (err) {
    $('tl-body').innerHTML = `<div class="none">Der Verlauf konnte nicht gelesen werden: ${esc(err)}</div>`;
  }
  tlLoading = false;
}

// Solange der Index noch gelesen wird, lohnt häufigeres Nachfragen.
function scheduleTimelineRefresh() {
  clearInterval(tlTimer);
  if (!document.body.classList.contains('tl-open')) {
    tlTimer = null;
    return;
  }
  const interval = tlProgress && tlProgress.active ? 3000 : 60000;
  tlTimer = setInterval(() => { if (!document.hidden) refreshTimeline(); }, interval);
}
```

`tlToggle` ruft statt des festen Intervalls nur noch `refreshTimeline()` auf; das Nachfassen übernimmt `scheduleTimelineRefresh`.

In `renderTimeline` den Hinweis voranstellen — sowohl im leeren als auch im gefüllten Fall:

```js
function renderTimeline() {
  const body = $('tl-body');
  const coverage = historyCoverageNotice(tlSources, 'Verlauf teilweise verfügbar.');
  const progress = timelineProgressNotice(tlProgress);
  if (!tlEntries.length) {
    const empty = tlProgress && tlProgress.active
      ? 'Es ist noch nichts gelesen.'
      : (coverage.degraded.length
        ? 'keine Prompts in den lesbaren Quellen'
        : 'keine Prompts aus unterstützten Sessions in den letzten 14 Tagen');
    body.innerHTML = progress + coverage.html + `<div class="none">${empty}</div>`;
    return;
  }
  // … unveränderter Aufbau von html …
  const st = body.scrollTop;
  body.innerHTML = progress + coverage.html + html;
  body.scrollTop = st;
}
```

In `app/frontend/src/overview.css` (oder der Datei, die `.tl-day` definiert) eine schlichte Regel ergänzen, die dem Hinweis dieselbe Ruhe gibt wie den übrigen Randtexten:

```css
.tl-progress {
  padding: 8px 12px;
  color: var(--muted);
  font-size: 12px;
}
```

Beachte die UI-Regeln: keine Pille mit Punkt davor, kein Mikrolabel in einer Ecke — ein ganzer Satz im Fluss des Panels.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd app/frontend && npx vitest run src/timeline-progress.test.js`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add app/frontend/index.html app/frontend/src/main.js app/frontend/src/timeline-progress.js app/frontend/src/timeline-progress.test.js app/frontend/src/overview.css app/tools.go
git commit -m "feat(app): Verlauf zeigt Teilergebnis, Lesefortschritt und Fehler"
```

---

### Task 15: Begriffe und Zeitraum-Schalter nachziehen

**Files:**
- Modify: `CONTEXT.md`
- Modify: `app/frontend/src/main.js:2270` (Text „letzten 7 Tagen")
- Modify: `app/tools.go:347` (`Since: time.Now().AddDate(0, 0, -7)`)

**Interfaces:**
- Consumes: nichts.
- Produces: nichts.

- [ ] **Step 1: Zeitraum des Verlaufs auf das Fenster heben**

`app/tools.go:347` von `-7` auf `-14` ändern, damit der Verlauf das gesamte vorgehaltene Fenster zeigt. Der leere Zustand in `renderTimeline` nennt bereits 14 Tage (Task 14).

- [ ] **Step 2: Begriffe ergänzen**

In `CONTEXT.md` unter „Work History" nach `HistoryEvent` einfügen:

```markdown
**HistoryRetentionWindow**:
Der Zeitraum, für den WorkHistory einzelne Prompts und Ausgaben vorhält;
außerhalb davon existieren nur noch Aktivitätsaggregate.
_Avoid_: Cache-Dauer, TTL, Aufräumfrist

**HistoryActivityAggregate**:
Die je Quelle, Tag, Stunde, Provider, Conversation und Modell fortgeschriebenen
Kennzahlen — Prompts, Turns, Tokenwerte und Kosten — die den Verfall der
Roh-Events überdauern.
_Avoid_: Statistik, Zusammenfassung, Summary

**HistoryIndexProgress**:
Der beobachtbare Zustand eines laufenden Indexaufbaus, den eine Abfrage
zusammen mit ihrem Teilergebnis zurückgibt.
_Avoid_: Ladezustand, Spinner, Fortschrittsbalken

**HistoryConversation**:
Ein über Provider hinweg einheitlich beschriebener Chat, gebildet aus den
Ereignissen einer Conversation innerhalb des Aufbewahrungsfensters.
_Avoid_: Session, Transkript, Thread
```

- [ ] **Step 3: Bauen und prüfen**

Run: `go build ./... && (cd app && go build ./...)`
Expected: beide Builds ohne Fehler.

- [ ] **Step 4: Commit**

```bash
git add CONTEXT.md app/tools.go
git commit -m "docs: Begriffe des Arbeitsverlaufs und 14-Tage-Fenster im Verlauf"
```

---

## Nach dem Plan

Der Verlauf ist damit sofort benutzbar, die Statistik behält ihre Zeiträume, und `Conversations` steht als Grundlage bereit. Die Chat-Übersicht samt Drag & Drop ist ein eigenes Vorhaben und braucht einen eigenen Entwurf: sie muss unter anderem klären, was das Hineinziehen eines Chats in eine Session tatsächlich auslöst.
