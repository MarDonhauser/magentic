package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

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

// sourceProblems liefert die gemeldeten Probleme aller Quellen eines Providers.
func (s *historyStore) sourceProblems(provider HistoryProvider) ([]HistoryProblem, error) {
	rows, err := s.db.Query(`SELECT problems FROM sources WHERE provider = ?`, string(provider))
	if err != nil {
		return nil, fmt.Errorf("read source problems: %w", err)
	}
	defer rows.Close()
	var out []HistoryProblem
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("read source problems: %w", err)
		}
		var problems []HistoryProblem
		if err := json.Unmarshal([]byte(encoded), &problems); err != nil {
			return nil, fmt.Errorf("decode source problems: %w", err)
		}
		out = append(out, problems...)
	}
	return out, rows.Err()
}

// countSources zählt die indexierten Quellen eines Providers, unabhängig davon,
// ob sie Probleme gemeldet haben.
func (s *historyStore) countSources(provider HistoryProvider) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT count(*) FROM sources WHERE provider = ?`, string(provider)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count sources: %w", err)
	}
	return count, nil
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

// replaceSource ersetzt die Events einer Quelle vollständig und legt die Quelle
// selbst an oder aktualisiert sie, alles in einer Transaktion. Kollisionen auf
// event_id (z. B. wenn Codex dieselbe Konversation in sessions und
// archived_sessions ablegt) werden über mergeHistoryRecord zusammengeführt.
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
	if err := writeSourceRowTx(tx, row); err != nil {
		return err
	}
	return tx.Commit()
}

// insertHistoryRecordTx schreibt einen Datensatz. Kollidiert die event_id mit
// einem bereits gespeicherten Ereignis, führt die vorhandene Fassung; fehlende
// Tatsachen kommen aus der neuen Fassung hinzu.
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

// historyLinksOrEmpty sorgt dafür, dass eine nil-Slice als "[]" statt als
// "null" serialisiert wird.
func historyLinksOrEmpty(links []string) []string {
	if links == nil {
		return []string{}
	}
	return links
}

// historyOccurredAtValue wandelt den rohen RFC3339-Zeitstempel in eine
// sortierbare Unix-Nanosekundenzahl um, oder liefert nil, wenn der Zeitstempel
// fehlt oder unlesbar ist.
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

// historyNullableInt liefert nil, solange der Wert nicht bekannt ist, damit
// unbekannte Nutzungswerte nicht als 0 gespeichert werden.
func historyNullableInt(value int64, known bool) any {
	if !known {
		return nil
	}
	return value
}

// historyRecordColumns listet die Spalten, die einen historyRecord vollständig
// rekonstruieren; historyRecordByIDTx und recordsBySource teilen sie sich.
const historyRecordColumns = `event_id, source_id, provider, conversation_id, timestamp_raw, role, kind, lineage,
	text, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	cwd, project_alias, native_id, links`

// scanHistoryRecord liest eine Zeile im Format von historyRecordColumns in
// einen historyRecord ein.
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

// historyRecordByIDTx liest ein Ereignis anhand seiner event_id innerhalb
// einer bestehenden Transaktion.
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

// recordsBySource liest alle Ereignisse einer Quelle. Dient Tests und der
// Aggregatprüfung.
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

// historyRecordFilter grenzt eine Leseabfrage über records ein. Die
// Attributionsfilter ProjectKeys und SessionKeys bleiben bewusst außen vor:
// sie hängen an HistoryAssociations und werden nach dem Lesen in Go aufgelöst.
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

// historyFTSExpression baut aus einer Nutzeranfrage einen FTS5-Präfixausdruck.
// Er dient nur der Vorauswahl; die genaue Trefferentscheidung fällt danach über
// einen Teilstringvergleich, damit sich die Suche wie bisher verhält.
func historyFTSExpression(query string) (string, bool) {
	fields := strings.Fields(query)
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		if !strings.ContainsFunc(field, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			// Ein Feld ohne Buchstaben oder Ziffern (z. B. reine Satzzeichen)
			// trägt nichts zur Vorauswahl bei.
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(field, `"`, `""`)+`"*`)
	}
	if len(terms) == 0 {
		return "", false
	}
	return strings.Join(terms, " "), true
}

// records liest Ereignisse anhand des übergebenen Filters. Sortierung und
// Paginierung bleiben in Go, weil die Sortierreihenfolge von Tatsachen
// abhängt, die erst nach dem Lesen aufgelöst werden.
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
			// Aus reinen Satzzeichen entsteht kein FTS-Ausdruck; ein MATCH mit
			// leerem Ausdruck würde die Abfrage scheitern lassen. Der
			// Teilstringvergleich unten bleibt ohnehin verbindlich, deshalb
			// wird hier nur über instr() vorausgewählt.
			where = append(where, "instr(lower(text), lower(?)) > 0")
			args = append(args, needle)
		} else {
			// FTS matcht nur ganze Wörter bzw. Wortanfänge und würde einen
			// Treffer wie "bernet" in "Kubernetes" verpassen. instr() bleibt
			// deshalb als zweiter Zweig bestehen; der Teilstringvergleich in Go
			// entscheidet ohnehin über jeden Treffer verbindlich.
			where = append(where, "(rowid IN (SELECT rowid FROM events_fts WHERE events_fts MATCH ?) OR instr(lower(text), lower(?)) > 0)")
			args = append(args, expression, needle)
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

// historyStrings wandelt eine Slice benannter Stringtypen in reine Strings um,
// damit sie als IN-Parameter an SQLite gehen können.
func historyStrings[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}
