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
