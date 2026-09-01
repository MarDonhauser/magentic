package core

import (
	"database/sql"
	"encoding/json"
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

// historySourceRow spiegelt eine Zeile der Tabelle sources wider.
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

// writeSourceRow schreibt eine Quelle in einer eigenen Transaktion.
func (s *historyStore) writeSourceRow(row historySourceRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("write source: %w", err)
	}
	defer tx.Rollback()
	if err := writeSourceRowTx(tx, row); err != nil {
		return err
	}
	return tx.Commit()
}

// writeSourceRowTx legt eine Quelle innerhalb einer bestehenden Transaktion an
// oder aktualisiert sie. Task 3 ruft dies innerhalb von replaceSource auf, damit
// der Upsert nicht doppelt vorgehalten werden muss.
func writeSourceRowTx(tx *sql.Tx, row historySourceRow) error {
	problems, err := json.Marshal(historyProblemsOrEmpty(row.Problems))
	if err != nil {
		return fmt.Errorf("encode source problems: %w", err)
	}
	_, err = tx.Exec(`INSERT INTO sources
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

// historyProblemsOrEmpty sorgt dafür, dass eine nil-Slice als "[]" statt als
// "null" serialisiert wird.
func historyProblemsOrEmpty(problems []HistoryProblem) []HistoryProblem {
	if problems == nil {
		return []HistoryProblem{}
	}
	return problems
}

// source liest eine einzelne Quelle anhand ihrer ID.
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

// sourceIDsByProvider liefert alle bekannten Quellen-IDs eines Providers, um
// verwaiste Quellen (gelöschte Dateien) erkennen zu können.
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

// sourceProblems liefert alle Probleme der Quellen eines Providers sowie die
// Anzahl der betroffenen Quellen.
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

// deleteSources entfernt Quellen samt ihrer Events und Volltextindex-Einträge.
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

// deleteHistorySourceTx löscht eine Quelle samt ihrer Events und
// Volltextindex-Einträge innerhalb einer bestehenden Transaktion.
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
