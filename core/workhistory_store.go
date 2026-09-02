package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const historySchemaVersion = 3

// historyRetentionWindow begrenzt, wie lange Roh-Events vorgehalten werden.
// Die dauerhaften Tagesaggregate in der Tabelle activity sind davon nicht
// betroffen (Task 6).
const historyRetentionWindow = 14 * 24 * time.Hour

type historyStore struct {
	path string
	db   *sql.DB
}

const historySchemaSQL = `
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);

-- index_state hält die Revision des Index: einen Zähler, der genau dann steigt,
-- wenn ein Lauf tatsächlich Quellen geschrieben oder gelöscht hat.
CREATE TABLE IF NOT EXISTS index_state (revision INTEGER NOT NULL);

-- Der Pfad des Transkripts wird bewusst nicht gespeichert. Die Quelle wird über
-- die gehashte source_id geführt; die Entdeckung läuft ohnehin bei jedem Lauf
-- erneut über das Dateisystem.
CREATE TABLE IF NOT EXISTS sources (
	source_id      TEXT PRIMARY KEY,
	provider       TEXT NOT NULL,
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
	known_input_events         INTEGER NOT NULL DEFAULT 0,
	unknown_input_events       INTEGER NOT NULL DEFAULT 0,
	known_output_events        INTEGER NOT NULL DEFAULT 0,
	unknown_output_events      INTEGER NOT NULL DEFAULT 0,
	known_cache_read_events    INTEGER NOT NULL DEFAULT 0,
	unknown_cache_read_events  INTEGER NOT NULL DEFAULT 0,
	known_cache_write_events   INTEGER NOT NULL DEFAULT 0,
	unknown_cache_write_events INTEGER NOT NULL DEFAULT 0,
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
	if err := stampHistoryRevision(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := protectHistoryFiles(path); err != nil {
		db.Close()
		return nil, err
	}
	return &historyStore{path: path, db: db}, nil
}

// protectHistoryFiles beschränkt die Datenbank und ihre Begleitdateien auf den
// eigenen Benutzer. Das Write-Ahead-Log trägt denselben Inhalt wie die
// Datenbank und wird von SQLite nicht mit deren Rechten angelegt.
func protectHistoryFiles(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("protect work history index: %w", err)
		}
	}
	return nil
}

// stampHistoryRevision legt die einzige Zeile der Tabelle index_state an, falls
// sie noch fehlt.
func stampHistoryRevision(db *sql.DB) error {
	var revision int64
	err := db.QueryRow(`SELECT revision FROM index_state`).Scan(&revision)
	if err == sql.ErrNoRows {
		if _, err := db.Exec(`INSERT INTO index_state(revision) VALUES(0)`); err != nil {
			return fmt.Errorf("stamp work history revision: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read work history revision: %w", err)
	}
	return nil
}

// revision liefert den aktuellen Zählerstand des Index.
func (s *historyStore) revision() (uint64, error) {
	var revision int64
	if err := s.db.QueryRow(`SELECT revision FROM index_state`).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read work history revision: %w", err)
	}
	return uint64(revision), nil
}

// bumpRevision erhöht den Zähler um eins. Der Indexer ruft dies einmal am Ende
// eines Laufs auf, der tatsächlich etwas geändert hat, damit Leser einen
// zwischenzeitlichen Neuaufbau erkennen können.
func (s *historyStore) bumpRevision() error {
	if _, err := s.db.Exec(`UPDATE index_state SET revision = revision + 1`); err != nil {
		return fmt.Errorf("bump work history revision: %w", err)
	}
	return nil
}

func (s *historyStore) Close() error { return s.db.Close() }

// historySourceRow spiegelt eine Zeile der Tabelle sources wider. Der Pfad des
// Transkripts gehört bewusst nicht dazu: der Index soll ihn nicht festhalten.
type historySourceRow struct {
	SourceID       string
	Provider       HistoryProvider
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
		(source_id, provider, adapter_version, digest, size, mod_time, indexed_at, problems)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(source_id) DO UPDATE SET
			provider=excluded.provider,
			adapter_version=excluded.adapter_version, digest=excluded.digest,
			size=excluded.size, mod_time=excluded.mod_time,
			indexed_at=excluded.indexed_at, problems=excluded.problems`,
		row.SourceID, string(row.Provider), row.AdapterVersion, row.Digest,
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
	err := s.db.QueryRow(`SELECT provider, adapter_version, digest, size, mod_time, indexed_at, problems
		FROM sources WHERE source_id = ?`, sourceID).
		Scan(&provider, &row.AdapterVersion, &row.Digest, &row.Size, &row.ModTime, &row.IndexedAt, &problems)
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

// deleteHistorySourceTx löscht eine Quelle samt ihrer Events innerhalb einer
// bestehenden Transaktion.
func deleteHistorySourceTx(tx *sql.Tx, sourceID string) error {
	if _, err := tx.Exec(`DELETE FROM events WHERE source_id = ?`, sourceID); err != nil {
		return fmt.Errorf("delete source events: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM sources WHERE source_id = ?`, sourceID); err != nil {
		return fmt.Errorf("delete source: %w", err)
	}
	return nil
}

// pruneEvents entfernt Roh-Events außerhalb des Aufbewahrungsfensters. Quellen
// und Aggregate bleiben stehen: die Aggregate sind dauerhaft, und die Quellen
// verhindern, dass eine unveränderte Datei erneut geparst wird.
func (s *historyStore) pruneEvents(ctx context.Context, cutoff time.Time) (int, error) {
	bound := cutoff.UTC().UnixNano()
	const condition = `(occurred_at IS NOT NULL AND occurred_at < ?)
		OR (occurred_at IS NULL AND source_id IN (SELECT source_id FROM sources WHERE mod_time < ?))`
	result, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE `+condition, bound, bound)
	if err != nil {
		return 0, fmt.Errorf("prune events: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune events: %w", err)
	}
	return int(removed), nil
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
		if _, err := tx.Exec(`DELETE FROM events WHERE event_id = ?`, record.ID); err != nil {
			return fmt.Errorf("clear merged event: %w", err)
		}
	}
	links, err := json.Marshal(historyLinksOrEmpty(record.Links))
	if err != nil {
		return fmt.Errorf("encode links: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO events
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
		record.CWD, record.ProjectAlias, record.NativeID, string(links)); err != nil {
		return fmt.Errorf("write event: %w", err)
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
		// Die Vorauswahl bleibt ein einfacher Teilstringvergleich in SQL; der
		// Go-Teilstringvergleich unten entscheidet ohnehin über jeden Treffer
		// verbindlich.
		where = append(where, "instr(lower(text), lower(?)) > 0")
		args = append(args, needle)
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

// historyActivityRow ist eine dauerhafte Zeile der Tabelle activity: eine
// Kennzahl je Conversation (oder Quelle, wenn keine Conversation bekannt ist),
// Tag, Stunde, Provider und Modell. Anders als events überlebt sie das
// Aufbewahrungsfenster der Roh-Events.
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
	// Trägt HistoryMeasure.KnownEvents/UnknownEvents je Tokenfeld: gezählt wird
	// wie in historyMeasureAcc.add, ein Paar je Feld, weil ein einzelnes
	// Ereignis für verschiedene Felder unterschiedliche Abdeckung haben kann.
	KnownInputEvents        int
	UnknownInputEvents      int
	KnownOutputEvents       int
	UnknownOutputEvents     int
	KnownCacheReadEvents    int
	UnknownCacheReadEvents  int
	KnownCacheWriteEvents   int
	UnknownCacheWriteEvents int
}

// historyActivityKey identifiziert eine Aggregatzeile eindeutig; sie
// entspricht dem Primärschlüssel der Tabelle activity. Ein Prompt (ohne
// Modellangabe) und die Ausgabe derselben Conversation-Stunde (mit Modell)
// fallen deshalb bewusst in unterschiedliche Zeilen; addBucket (Task 9/10)
// summiert Prompts und Turns ohnehin über alle Zeilen eines Tages.
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
		if record.Lineage != HistoryLineagePrimary {
			continue
		}
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
		// Gezählt wird je Tokenfeld, genau wie historyMeasureAcc.add: known++ bei
		// HistoryFactKnown, unknown++ bei HistoryFactUnknown, sonst nichts. Ein
		// Ereignis kann so für ein Feld bekannt und für ein anderes unbekannt sein.
		countHistoryFactCoverage(usage.InputTokens, &row.KnownInputEvents, &row.UnknownInputEvents)
		countHistoryFactCoverage(usage.OutputTokens, &row.KnownOutputEvents, &row.UnknownOutputEvents)
		countHistoryFactCoverage(usage.CacheReadTokens, &row.KnownCacheReadEvents, &row.UnknownCacheReadEvents)
		countHistoryFactCoverage(usage.CacheWriteTokens, &row.KnownCacheWriteEvents, &row.UnknownCacheWriteEvents)
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

// countHistoryFactCoverage zählt ein einzelnes Tokenfeld in known oder unknown
// ein, exakt wie historyMeasureAcc.add (core/workhistory.go:1165): bekannt
// erhöht known, unbekannt erhöht unknown, ein nicht anwendbares Feld (z. B. bei
// Prompts) erhöht keins von beiden.
func countHistoryFactCoverage(fact HistoryFact[int64], known, unknown *int) {
	switch fact.State {
	case HistoryFactKnown:
		*known++
	case HistoryFactUnknown:
		*unknown++
	}
}

// writeActivity schreibt Aktivitätszeilen dauerhaft fest. Damit dieselbe
// Conversation aus zwei Quellen (z. B. Codex' sessions und archived_sessions)
// nicht doppelt zählt, gilt je agg_key ein Wasserzeichen: geschrieben wird nur,
// wenn written_from_mod_time mindestens so groß ist wie das bereits
// gespeicherte Maximum dieses agg_key, und dann ersetzen die neuen Zeilen alle
// vorhandenen Zeilen dieses agg_key vollständig statt sie zu addieren.
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
				 cost, priced_events, unpriced_events,
				 known_input_events, unknown_input_events,
				 known_output_events, unknown_output_events,
				 known_cache_read_events, unknown_cache_read_events,
				 known_cache_write_events, unknown_cache_write_events)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				row.AggKey, row.Day, row.Hour, string(row.Provider), row.Model, row.SourceID,
				row.WrittenFromModTime, row.ConversationID, row.CWD, row.ProjectAlias,
				row.Prompts, row.Turns, row.Input, row.Output, row.CacheRead, row.CacheWrite,
				row.Cost, row.PricedEvents, row.UnpricedEvents,
				row.KnownInputEvents, row.UnknownInputEvents,
				row.KnownOutputEvents, row.UnknownOutputEvents,
				row.KnownCacheReadEvents, row.UnknownCacheReadEvents,
				row.KnownCacheWriteEvents, row.UnknownCacheWriteEvents); err != nil {
				return fmt.Errorf("write activity: %w", err)
			}
		}
	}
	return tx.Commit()
}

// activityRows liest die dauerhaften Aggregatzeilen aus der Tabelle activity.
// Namen von Projekten und Sessions sind hier bewusst nicht enthalten; sie
// werden vom Aufrufer frisch aufgelöst (siehe (*WorkHistory).Activity).
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
		cost, priced_events, unpriced_events,
		known_input_events, unknown_input_events, known_output_events, unknown_output_events,
		known_cache_read_events, unknown_cache_read_events, known_cache_write_events, unknown_cache_write_events
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
			&row.KnownInputEvents, &row.UnknownInputEvents,
			&row.KnownOutputEvents, &row.UnknownOutputEvents,
			&row.KnownCacheReadEvents, &row.UnknownCacheReadEvents,
			&row.KnownCacheWriteEvents, &row.UnknownCacheWriteEvents); err != nil {
			return nil, fmt.Errorf("read activity: %w", err)
		}
		row.Provider = HistoryProvider(provider)
		out = append(out, row)
	}
	return out, rows.Err()
}
