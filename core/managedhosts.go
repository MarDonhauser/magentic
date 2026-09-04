package core

import (
	"context"
	"encoding/json"
	"errors"
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

// ManagedHostRegistry is the daemon's durable record of every managed
// Session's agent host. It holds the store path once, so no caller derives it
// again and no method takes it as an argument: one Registry is one store.
type ManagedHostRegistry struct {
	path string
}

// NewManagedHostRegistry opens the Registry at the configured store path.
func NewManagedHostRegistry() *ManagedHostRegistry {
	return &ManagedHostRegistry{path: ManagedHostStorePath()}
}

// OpenManagedHostRegistry opens the Registry at an explicit path.
func OpenManagedHostRegistry(path string) *ManagedHostRegistry {
	return &ManagedHostRegistry{path: path}
}

// Path is the store this Registry writes.
func (r *ManagedHostRegistry) Path() string { return r.path }

// update reads the store, hands it to mutate, and writes it back — all under
// the same cross-process lock. Every change to a recorded host goes through
// here, so read-modify-write is stated once instead of per verb.
func (r *ManagedHostRegistry) update(mutate func(*managedHostStore) error) error {
	return withRegistryFileLock(context.Background(), r.path, func() error {
		store, err := readManagedHostStore(r.path)
		if err != nil {
			return err
		}
		if err := mutate(store); err != nil {
			return err
		}
		return writeManagedHostStore(r.path, store)
	})
}

// RecordIntent durably records that a managed Session's host is about to be
// started, before any process exists (ADR 0003). It must be called, and
// observed to have returned, before StartAgentHost is called.
func (r *ManagedHostRegistry) RecordIntent(sessionID SessionID, socketPath string, token AgentHostToken) error {
	return r.update(func(store *managedHostStore) error {
		store.Records[string(sessionID)] = ManagedHostRecord{
			SessionID: sessionID, SocketPath: socketPath, Token: token,
			Started: false, RecordedAt: time.Now(),
		}
		return nil
	})
}

// MarkStarted confirms a recorded host's process was actually spawned. A
// failed spawn leaves the record with Started still false, which is what a
// failed-spawn record looks like.
func (r *ManagedHostRegistry) MarkStarted(sessionID SessionID) error {
	return r.update(func(store *managedHostStore) error {
		record, ok := store.Records[string(sessionID)]
		if !ok {
			return fmt.Errorf("kein Host-Intent für Session %q verzeichnet", sessionID)
		}
		record.Started = true
		store.Records[string(sessionID)] = record
		return nil
	})
}

// Forget removes a Session's recorded host, once its process is confirmed
// stopped.
func (r *ManagedHostRegistry) Forget(sessionID SessionID) error {
	return r.update(func(store *managedHostStore) error {
		delete(store.Records, string(sessionID))
		return nil
	})
}

// Records lists every recorded host, ordered by SessionID for a stable read.
func (r *ManagedHostRegistry) Records() ([]ManagedHostRecord, error) {
	store, err := readManagedHostStore(r.path)
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
	// ManagedHostGone means nothing answered on the recorded socket. The
	// Session has no process; nothing was there to adopt or to kill.
	ManagedHostGone ManagedHostOutcome = "gone"
	// ManagedHostForeign means something answered but did not confirm the
	// recorded token. It is deliberately not the same outcome as gone: a
	// process is alive on that path, and precisely because it could not be
	// confirmed it is neither adopted nor terminated.
	ManagedHostForeign ManagedHostOutcome = "foreign"
	// ManagedHostOrphaned means the recorded Session no longer exists.
	// Reconciliation reports this rather than sweeping the record or
	// terminating anything.
	ManagedHostOrphaned ManagedHostOutcome = "orphaned"
)

// ManagedHostReconcileResult is one recorded host's outcome at daemon
// startup. Reason carries the handshake refusal for a gone or foreign host.
type ManagedHostReconcileResult struct {
	SessionID SessionID          `json:"session_id"`
	Outcome   ManagedHostOutcome `json:"outcome"`
	Record    ManagedHostRecord  `json:"record"`
	Reason    string             `json:"reason,omitempty"`
}

// Reconcile confirms, for every durably recorded managed host, whether it is
// still alive and identity-confirmable. It never identifies a process by
// matching a command line, a path, or a Session name — only the recorded
// socket path and token decide the outcome.
func (r *ManagedHostRegistry) Reconcile(state *State) ([]ManagedHostReconcileResult, error) {
	records, err := r.Records()
	if err != nil {
		return nil, err
	}
	results := make([]ManagedHostReconcileResult, 0, len(records))
	for _, record := range records {
		result := ManagedHostReconcileResult{SessionID: record.SessionID, Record: record}
		switch {
		case state.SessionByID(record.SessionID) == nil:
			result.Outcome = ManagedHostOrphaned
		default:
			err := ConnectAgentHost(record.SocketPath, record.Token)
			switch {
			case err == nil:
				result.Outcome = ManagedHostReclaimed
			case errors.Is(err, ErrAgentHostForeign):
				result.Outcome, result.Reason = ManagedHostForeign, err.Error()
			default:
				result.Outcome, result.Reason = ManagedHostGone, err.Error()
			}
		}
		results = append(results, result)
	}
	return results, nil
}

// ReconcileIfOwning reconciles managed hosts only when claimErr is nil — i.e.
// only when this process actually won ownership of the control socket,
// reusing that existing single-owner handling. A process that lost that race
// states the reason (claimErr) and touches no managed process: reconciliation
// itself only ever confirms a handshake, never starts one.
func (r *ManagedHostRegistry) ReconcileIfOwning(claimErr error, state *State) ([]ManagedHostReconcileResult, error) {
	if claimErr != nil {
		return nil, claimErr
	}
	return r.Reconcile(state)
}
