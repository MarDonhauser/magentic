package core

import (
	"context"
	"sort"
	"time"
)

// HistoryIndexProgress berichtet, wie weit der laufende Indexdurchlauf ist.
// Abfragen liefern ihn mit, damit die Oberfläche einen unvollständigen Stand
// als solchen ausweisen kann statt ihn als leeres Ergebnis zu zeigen.
type HistoryIndexProgress struct {
	Active         bool      `json:"active"`
	PendingFiles   int       `json:"pendingFiles"`
	CompletedFiles int       `json:"completedFiles"`
	StartedAt      time.Time `json:"startedAt,omitempty"`
}

// historyRunCounters hält die Zähler eines einzelnen Laufs je Provider. Sie
// werden zu Beginn jedes Laufs zurückgesetzt, damit ParsedFiles und ReusedFiles
// den letzten Lauf beschreiben und nicht über die Zeit anwachsen.
type historyRunCounters struct {
	parsed, reused int
	// Nur die Abdeckung wird gehalten, nicht die Dateiliste der Entdeckung:
	// snapshotMeta braucht nichts weiter, und die Pfade sollen nicht länger im
	// Speicher stehen als der Lauf sie braucht.
	coverage HistoryProviderCoverage
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
	h.beginRunLocked()
	h.mu.Unlock()
	go func() {
		// Der Lauf überlebt die auslösende Abfrage bewusst.
		_ = h.runIndex(context.Background())
	}()
}

// indexOnce führt einen vollständigen Durchlauf synchron aus. Läuft bereits
// einer, kehrt der Aufruf sofort zurück.
func (h *WorkHistory) indexOnce(ctx context.Context) error {
	h.mu.Lock()
	if h.indexing {
		h.mu.Unlock()
		return nil
	}
	h.beginRunLocked()
	h.mu.Unlock()
	return h.runIndex(ctx)
}

// beginRunLocked markiert den Beginn eines Laufs. Der Aufrufer hält h.mu.
func (h *WorkHistory) beginRunLocked() {
	h.indexing = true
	h.progress = HistoryIndexProgress{Active: true, StartedAt: h.now()}
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
	if err := ensurePrivateHistoryDir(h.config.IndexDir); err != nil {
		return err
	}
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
	changed := false
	for _, adapter := range h.adapters {
		if err := ctx.Err(); err != nil {
			return err
		}
		inventory := discoverHistoryFiles(ctx, h.files, adapter)
		seen := map[string]bool{}
		for _, path := range inventory.files {
			sourceID := historySourceID(adapter.Provider(), path)
			seen[sourceID] = true
			modTime := int64(0)
			if info, err := h.files.Stat(path); err == nil {
				modTime = info.ModTime().UnixNano()
			}
			candidates = append(candidates, historyIndexCandidate{adapter: adapter, path: path, modTime: modTime})
		}
		vanished, err := h.forgetVanishedSources(adapter, inventory, seen)
		if err != nil {
			return err
		}
		changed = changed || vanished
		h.rememberCoverage(adapter, inventory.coverage)
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
		if h.indexOneFile(ctx, candidate) {
			changed = true
		}
		h.mu.Lock()
		h.progress.PendingFiles--
		h.progress.CompletedFiles++
		h.mu.Unlock()
	}
	if changed {
		// Ein Leser erkennt an dieser Zahl, dass zwischen zwei Abfragen neu
		// indexiert wurde und seine beiden Teilergebnisse nicht zusammenpassen.
		if err := h.store.bumpRevision(); err != nil {
			return err
		}
	}
	cutoff := h.now().Add(-h.retention())
	if _, err := h.store.pruneEvents(ctx, cutoff); err != nil {
		return err
	}
	return nil
}

// indexOneFile liest eine Datei ein und meldet, ob sie den Speicher verändert
// hat. Eine unveränderte Quelle wird wiederverwendet statt erneut geparst.
func (h *WorkHistory) indexOneFile(ctx context.Context, candidate historyIndexCandidate) bool {
	adapter := candidate.adapter
	sourceID := historySourceID(adapter.Provider(), candidate.path)
	record := func(problem HistoryProblem) bool {
		problem.Provider = adapter.Provider()
		problem.SourceID = sourceID
		row, found, err := h.store.source(sourceID)
		if err != nil {
			return false
		}
		if !found {
			row = historySourceRow{SourceID: sourceID, Provider: adapter.Provider()}
		}
		if historyContains(row.Problems, problem) {
			// Eine dauerhaft unlesbare Datei meldet in jedem Lauf dasselbe.
			// Weder die Problemliste noch die Revision dürfen davon wachsen.
			return false
		}
		row.Problems = append(row.Problems, problem)
		row.IndexedAt = h.now().UnixNano()
		return h.store.writeSourceRow(row) == nil
	}

	data, err := h.files.ReadFile(candidate.path)
	if err != nil {
		return record(HistoryProblem{Kind: "file-unreadable", Message: err.Error()})
	}
	digest, err := adapter.Fingerprint(h.files, candidate.path, data)
	if err != nil {
		return record(HistoryProblem{Kind: "dependency-unreadable", Message: err.Error()})
	}
	info, err := h.files.Stat(candidate.path)
	if err != nil {
		return record(HistoryProblem{Kind: "file-unavailable", Message: err.Error()})
	}
	existing, found, err := h.store.source(sourceID)
	if err != nil {
		return false
	}
	if found && existing.Provider == adapter.Provider() &&
		existing.AdapterVersion == adapter.Version() && existing.Digest == digest {
		h.noteReused(adapter.Provider())
		if existing.Size == info.Size() && existing.ModTime == info.ModTime().UnixNano() {
			return false
		}
		existing.Size, existing.ModTime = info.Size(), info.ModTime().UnixNano()
		existing.IndexedAt = h.now().UnixNano()
		return h.store.writeSourceRow(existing) == nil
	}
	records, problems, parseErr := adapter.Parse(ctx, h.files, candidate.path, data)
	for i := range problems {
		problems[i].Provider = adapter.Provider()
		problems[i].SourceID = sourceID
	}
	if parseErr != nil {
		return record(HistoryProblem{Kind: "parse-failed", Message: parseErr.Error()})
	}
	records = normalizeHistoryRecords(adapter.Provider(), sourceID, records)
	row := historySourceRow{
		SourceID: sourceID, Provider: adapter.Provider(),
		AdapterVersion: adapter.Version(), Digest: digest, Size: info.Size(),
		ModTime: info.ModTime().UnixNano(), IndexedAt: h.now().UnixNano(), Problems: problems,
	}
	// Erst die Aggregate, dann die Events: der Verfall darf keine Kennzahlen
	// verschlucken, die es sonst nie in activity geschafft hätten.
	if err := h.store.writeActivity(ctx, historyActivityRowsFor(records, sourceID, row.ModTime, time.Local)); err != nil {
		return false
	}
	if err := h.store.replaceSource(row, records); err != nil {
		return false
	}
	h.noteParsed(adapter.Provider())
	return true
}

func (h *WorkHistory) noteParsed(provider HistoryProvider) { h.bumpCounter(provider, 1, 0) }
func (h *WorkHistory) noteReused(provider HistoryProvider) { h.bumpCounter(provider, 0, 1) }

func (h *WorkHistory) bumpCounter(provider HistoryProvider, parsed, reused int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	counter := h.counterLocked(provider)
	counter.parsed += parsed
	counter.reused += reused
}

// rememberCoverage hält die Abdeckung eines Providers für die Dauer des Laufs
// fest und setzt dessen Zähler zurück, damit ParsedFiles und ReusedFiles den
// gerade beginnenden Lauf beschreiben.
func (h *WorkHistory) rememberCoverage(adapter historyProviderAdapter, coverage HistoryProviderCoverage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	counter := h.counterLocked(adapter.Provider())
	counter.parsed, counter.reused = 0, 0
	counter.coverage = coverage
}

// counterLocked liefert die Zähler eines Providers und legt sie bei Bedarf an.
// Der Aufrufer hält h.mu.
func (h *WorkHistory) counterLocked(provider HistoryProvider) *historyRunCounters {
	if h.counters == nil {
		h.counters = map[HistoryProvider]*historyRunCounters{}
	}
	counter := h.counters[provider]
	if counter == nil {
		counter = &historyRunCounters{}
		h.counters[provider] = counter
	}
	return counter
}

// forgetVanishedSources entfernt Quellen, deren Datei verschwunden ist, und
// meldet, ob dabei etwas gelöscht wurde.
func (h *WorkHistory) forgetVanishedSources(adapter historyProviderAdapter, inventory historyDiscovery, seen map[string]bool) (bool, error) {
	state := inventory.coverage.State
	if state != HistorySourceAvailable && state != HistorySourceAbsent {
		// Eine unvollständige Entdeckung darf keine Quellen löschen.
		return false, nil
	}
	known, err := h.store.sourceIDsByProvider(adapter.Provider())
	if err != nil {
		return false, err
	}
	var gone []string
	for sourceID := range known {
		if !seen[sourceID] {
			gone = append(gone, sourceID)
		}
	}
	if len(gone) == 0 {
		return false, nil
	}
	return true, h.store.deleteSources(gone)
}

// snapshotMeta beschreibt den Zustand des Index: Abdeckung je Provider aus den
// gespeicherten Quellen, ergänzt um den Fortschritt des laufenden Durchlaufs.
func (h *WorkHistory) snapshotMeta(ctx context.Context) (HistoryMeta, error) {
	h.mu.Lock()
	progress := h.progress
	counters := map[HistoryProvider]historyRunCounters{}
	for provider, counter := range h.counters {
		counters[provider] = *counter
	}
	h.mu.Unlock()

	revision, err := h.store.revision()
	if err != nil {
		return HistoryMeta{}, err
	}
	meta := HistoryMeta{Revision: revision, ObservedAt: h.now().UTC(), Progress: progress}
	for _, adapter := range h.adapters {
		provider := adapter.Provider()
		counter := counters[provider]
		coverage := counter.coverage
		coverage.Provider = provider
		problems, err := h.store.sourceProblems(provider)
		if err != nil {
			return HistoryMeta{}, err
		}
		indexed, err := h.store.countSources(provider)
		if err != nil {
			return HistoryMeta{}, err
		}
		coverage.IndexedFiles = indexed
		coverage.ParsedFiles = counter.parsed
		coverage.ReusedFiles = counter.reused
		coverage.Problems = append(coverage.Problems, problems...)
		// Ein Provider, der Probleme gemeldet hat, ist nie vollständig: sonst
		// läse sich ein unvollständiger Ausschnitt wie eine exakte Null.
		if len(coverage.Problems) > 0 {
			switch coverage.State {
			case HistorySourceAvailable:
				coverage.State = HistorySourcePartial
			case HistorySourceAbsent:
				coverage.State = HistorySourceUnavailable
			}
		}
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
