package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

// Der durable Ledger der Session-Lifecycle-Transitionen. Er steht in einer
// eigenen Datei, weil er ein eigenes Modul ist: drei Methoden über einem
// prozessübergreifend koordinierten, atomar geschriebenen Datensatz. Die
// Ablösung — ErrLifecycleSuperseded — ist sein Begriff, nicht der der
// Konvergenz-Engine, die ihn benutzt.
//
// Das atomare Schreiben teilt er sich mit der Registry
// (writeJSONFileAtomically); vorher trug er eine zweite Abschrift davon.

type lifecycleLedgerStore struct {
	path string
	now  func() time.Time
}

func newLifecycleLedgerStore(path string, now func() time.Time) lifecycleLedgerStore {
	if now == nil {
		now = time.Now
	}
	return lifecycleLedgerStore{path: path, now: now}
}

// Records gibt alle Datensätze zurück, neueste zuerst, mit der Revision, unter
// der sie gelesen wurden.
func (s lifecycleLedgerStore) Records(ctx context.Context) (LifecycleSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var snapshot LifecycleSnapshot
	err := withRegistryFileLock(ctx, s.path, func() error {
		ledger, err := readLifecycleLedger(s.path)
		if err != nil {
			return err
		}
		snapshot.Revision = ledger.Revision
		for _, record := range ledger.Records {
			snapshot.Records = append(snapshot.Records, record)
		}
		sort.Slice(snapshot.Records, func(i, j int) bool {
			return snapshot.Records[i].UpdatedAt.After(snapshot.Records[j].UpdatedAt)
		})
		return nil
	})
	return snapshot, err
}

var ErrLifecycleSuperseded = errors.New("Session Lifecycle transition was superseded")

// Current liest den Datensatz, der die erwartete Transition noch trägt. Eine
// andere TransitionID heißt: eine neuere Absicht hat sie abgelöst.
func (s lifecycleLedgerStore) Current(ctx context.Context, expected LifecycleRecord) (LifecycleRecord, error) {
	var record LifecycleRecord
	err := withRegistryFileLock(ctx, s.path, func() error {
		ledger, err := readLifecycleLedger(s.path)
		if err != nil {
			return err
		}
		current, ok := ledger.Records[string(expected.SessionID)]
		if !ok || current.TransitionID != expected.TransitionID {
			return ErrLifecycleSuperseded
		}
		record = current
		return nil
	})
	return record, err
}

// ForSession liest den Datensatz einer Session, falls einer besteht.
func (s lifecycleLedgerStore) ForSession(ctx context.Context, id SessionID) (LifecycleRecord, bool, error) {
	var record LifecycleRecord
	var ok bool
	err := withRegistryFileLock(ctx, s.path, func() error {
		ledger, err := readLifecycleLedger(s.path)
		if err != nil {
			return err
		}
		record, ok = ledger.Records[string(id)]
		return nil
	})
	return record, ok, err
}

// Put schreibt einen Datensatz. requireCurrent hält die Absicht fest, nur die
// eigene, noch aktuelle Transition fortzuschreiben; sonst gewinnt der letzte
// Schreiber, und eine abgelöste Transition könnte eine neuere überschreiben.
func (s lifecycleLedgerStore) Put(ctx context.Context, record LifecycleRecord, requireCurrent bool) (LifecycleRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var saved LifecycleRecord
	err := withRegistryFileLock(ctx, s.path, func() error {
		ledger, err := readLifecycleLedger(s.path)
		if err != nil {
			return err
		}
		key := string(record.SessionID)
		if requireCurrent {
			current, ok := ledger.Records[key]
			if !ok || current.TransitionID != record.TransitionID {
				return ErrLifecycleSuperseded
			}
		}
		record.UpdatedAt = s.now()
		ledger.Records[key] = record
		compactLifecycleLedger(ledger)
		ledger.Revision++
		if err := writeLifecycleLedger(s.path, ledger); err != nil {
			return err
		}
		saved = record
		return nil
	})
	return saved, err
}

const maxConvergedRemovedLifecycleRecords = 256

func compactLifecycleLedger(ledger *lifecycleLedger) {
	type removedRecord struct {
		key string
		at  time.Time
	}
	var removed []removedRecord
	for key, record := range ledger.Records {
		if record.Desired == SessionDesiredRemoved && record.Phase == LifecycleConverged {
			removed = append(removed, removedRecord{key: key, at: record.UpdatedAt})
		}
	}
	if len(removed) <= maxConvergedRemovedLifecycleRecords {
		return
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].at.Before(removed[j].at) })
	for _, old := range removed[:len(removed)-maxConvergedRemovedLifecycleRecords] {
		delete(ledger.Records, old.key)
	}
}

const lifecycleLedgerVersion = 1

type lifecycleLedger struct {
	Schema   int                        `json:"schema"`
	Revision uint64                     `json:"revision"`
	Records  map[string]LifecycleRecord `json:"records"`
}

func readLifecycleLedger(path string) (*lifecycleLedger, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &lifecycleLedger{Schema: lifecycleLedgerVersion, Records: map[string]LifecycleRecord{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var ledger lifecycleLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return nil, fmt.Errorf("decode Session Lifecycle ledger: %w", err)
	}
	if ledger.Schema != lifecycleLedgerVersion {
		return nil, fmt.Errorf("unsupported Session Lifecycle schema %d", ledger.Schema)
	}
	if ledger.Records == nil {
		ledger.Records = map[string]LifecycleRecord{}
	}
	return &ledger, nil
}

func writeLifecycleLedger(path string, ledger *lifecycleLedger) error {
	return writeJSONFileAtomically(path, ".lifecycle-*.tmp", ledger)
}
