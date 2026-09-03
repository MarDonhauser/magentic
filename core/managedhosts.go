package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ManagedHostRecord is what the daemon durably records about one managed
// Session's agent host: the socket path and the handshake token it needs to
// reclaim that host, and whether the process was actually confirmed to
// start. It is written before the agent-host process is started, per ADR
// 0003, so a crash between recording and spawning always leaves a record the
// daemon can see and report on, never a silently orphaned process.
type ManagedHostRecord struct {
	SessionID  SessionID      `json:"session_id"`
	SocketPath string         `json:"socket_path"`
	Token      AgentHostToken `json:"token"`
	Started    bool           `json:"started"`
	RecordedAt time.Time      `json:"recorded_at"`
}

const managedHostStoreSchema = 1

type managedHostStore struct {
	Schema  int                          `json:"schema"`
	Records map[string]ManagedHostRecord `json:"records"`
}

// ManagedHostStorePath is where the daemon durably records managed Session
// hosts, under the state directory.
func ManagedHostStorePath() string {
	if p := os.Getenv("MAGENTIC_MANAGED_HOSTS"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(StatePath()), "managed-hosts.json")
}

func readManagedHostStore(path string) (*managedHostStore, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &managedHostStore{Schema: managedHostStoreSchema, Records: map[string]ManagedHostRecord{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var store managedHostStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("decode managed-host store: %w", err)
	}
	if store.Records == nil {
		store.Records = map[string]ManagedHostRecord{}
	}
	return &store, nil
}

func writeManagedHostStore(path string, store *managedHostStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".managed-hosts-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keep = true
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// RecordManagedHostIntent durably records that a managed Session's host is
// about to be started, before any process exists (ADR 0003). It must be
// called, and observed to have returned, before StartAgentHost is called.
func RecordManagedHostIntent(storePath string, sessionID SessionID, socketPath string, token AgentHostToken) error {
	return withRegistryFileLock(context.Background(), storePath, func() error {
		store, err := readManagedHostStore(storePath)
		if err != nil {
			return err
		}
		store.Records[string(sessionID)] = ManagedHostRecord{
			SessionID: sessionID, SocketPath: socketPath, Token: token,
			Started: false, RecordedAt: time.Now(),
		}
		return writeManagedHostStore(storePath, store)
	})
}

// MarkManagedHostStarted confirms a recorded host's process was actually
// spawned. A failed spawn leaves the record with Started still false, which
// is what a failed-spawn record looks like.
func MarkManagedHostStarted(storePath string, sessionID SessionID) error {
	return withRegistryFileLock(context.Background(), storePath, func() error {
		store, err := readManagedHostStore(storePath)
		if err != nil {
			return err
		}
		record, ok := store.Records[string(sessionID)]
		if !ok {
			return fmt.Errorf("kein Host-Intent für Session %q verzeichnet", sessionID)
		}
		record.Started = true
		store.Records[string(sessionID)] = record
		return writeManagedHostStore(storePath, store)
	})
}

// ForgetManagedHost removes a Session's recorded host, once its process is
// confirmed stopped.
func ForgetManagedHost(storePath string, sessionID SessionID) error {
	return withRegistryFileLock(context.Background(), storePath, func() error {
		store, err := readManagedHostStore(storePath)
		if err != nil {
			return err
		}
		delete(store.Records, string(sessionID))
		return writeManagedHostStore(storePath, store)
	})
}

// ManagedHostRecords lists every recorded host, ordered by SessionID for a
// stable read.
func ManagedHostRecords(storePath string) ([]ManagedHostRecord, error) {
	store, err := readManagedHostStore(storePath)
	if err != nil {
		return nil, err
	}
	records := make([]ManagedHostRecord, 0, len(store.Records))
	for _, record := range store.Records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].SessionID < records[j].SessionID })
	return records, nil
}

// ManagedHostOutcome is one recorded host's fate at reconciliation.
type ManagedHostOutcome string

const (
	// ManagedHostReclaimed means the process answered the handshake with the
	// recorded token: it is the daemon's own host, still running.
	ManagedHostReclaimed ManagedHostOutcome = "reclaimed"
	// ManagedHostGone means nothing answered, or something answered without
	// the recorded token. Neither case adopts or kills anything: the
	// process holding the socket may not be this host at all.
	ManagedHostGone ManagedHostOutcome = "gone"
	// ManagedHostOrphaned means the recorded Session no longer exists.
	// Reconciliation reports this rather than sweeping the record or
	// terminating anything.
	ManagedHostOrphaned ManagedHostOutcome = "orphaned"
)

// ManagedHostReconcileResult is one recorded host's outcome at daemon
// startup.
type ManagedHostReconcileResult struct {
	SessionID SessionID          `json:"session_id"`
	Outcome   ManagedHostOutcome `json:"outcome"`
	Record    ManagedHostRecord  `json:"record"`
}

// ReconcileManagedHosts confirms, for every durably recorded managed host,
// whether it is still alive and identity-confirmable. It never identifies a
// process by matching a command line, a path, or a Session name — only the
// recorded socket path and token decide reclaim vs. gone.
func ReconcileManagedHosts(storePath string, state *State) ([]ManagedHostReconcileResult, error) {
	records, err := ManagedHostRecords(storePath)
	if err != nil {
		return nil, err
	}
	results := make([]ManagedHostReconcileResult, 0, len(records))
	for _, record := range records {
		if state.SessionByID(record.SessionID) == nil {
			results = append(results, ManagedHostReconcileResult{SessionID: record.SessionID, Outcome: ManagedHostOrphaned, Record: record})
			continue
		}
		if err := ConnectAgentHost(record.SocketPath, record.Token); err != nil {
			results = append(results, ManagedHostReconcileResult{SessionID: record.SessionID, Outcome: ManagedHostGone, Record: record})
			continue
		}
		results = append(results, ManagedHostReconcileResult{SessionID: record.SessionID, Outcome: ManagedHostReclaimed, Record: record})
	}
	return results, nil
}

// ReconcileManagedHostsIfOwning reconciles managed hosts only when claimErr
// is nil — i.e. only when this process actually won ownership of the
// control socket, reusing that existing single-owner handling. A process
// that lost that race states the reason (claimErr) and touches no managed
// process: reconciliation itself only ever confirms a handshake, never
// starts one.
func ReconcileManagedHostsIfOwning(claimErr error, storePath string, state *State) ([]ManagedHostReconcileResult, error) {
	if claimErr != nil {
		return nil, claimErr
	}
	return ReconcileManagedHosts(storePath, state)
}
