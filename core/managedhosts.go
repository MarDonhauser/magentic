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
	// LaunchArgv is the exact argument list an omp host was recorded to start
	// its process with. It is the Session's launch provenance: the host
	// launches exactly this, and the approval gate is proven from it only
	// while the host's identity is confirmed by Token.
	LaunchArgv []string `json:"launch_argv,omitempty"`
	// GateWithdrawn, GateWithdrawnReason and GateWithdrawnAt record that a
	// route into omp's own interface was opened for the process this record
	// describes: from that point Magentic's proof that this process's
	// approval gate is on no longer holds, and nothing re-establishes it by
	// asking the session (it cannot answer). It is scoped to the recorded
	// launch, not the Session's whole lifetime: a later RecordLaunchIntent
	// for the same Session — a genuinely new process Magentic itself starts
	// under the gate again — begins a fresh record with the claim not yet
	// withdrawn, because the withdrawal was true of the process someone
	// drove externally, which by then no longer exists.
	GateWithdrawn       bool      `json:"gate_withdrawn,omitempty"`
	GateWithdrawnReason string    `json:"gate_withdrawn_reason,omitempty"`
	GateWithdrawnAt     time.Time `json:"gate_withdrawn_at,omitzero"`
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

// RecordLaunchIntent records an omp host's intent together with the exact
// argument list its process will be launched with, before any process exists
// (ADR 0003). The host reads the argv back from this record rather than
// deriving its own, so what was recorded is what runs.
func (r *ManagedHostRegistry) RecordLaunchIntent(sessionID SessionID, socketPath string, token AgentHostToken, argv []string) error {
	return r.update(func(store *managedHostStore) error {
		store.Records[string(sessionID)] = ManagedHostRecord{
			SessionID: sessionID, SocketPath: socketPath, Token: token,
			Started: false, RecordedAt: time.Now(),
			LaunchArgv: append([]string(nil), argv...),
		}
		return nil
	})
}

// WithdrawGateProof records that a route into omp's own interface was
// opened for sessionID's current process, naming the route. From this call
// on, hostProvenanceEstablished reports false for this record until a fresh
// RecordLaunchIntent replaces it with a new process Magentic itself
// started. Nothing re-asserts the claim by querying the session — omp
// cannot report it, and nothing here tries. Calling it for a Session with
// no recorded host is a no-op: there is nothing to withdraw a claim about.
func (r *ManagedHostRegistry) WithdrawGateProof(sessionID SessionID, route string) error {
	return r.update(func(store *managedHostStore) error {
		record, ok := store.Records[string(sessionID)]
		if !ok {
			return nil
		}
		if record.GateWithdrawn {
			return nil
		}
		record.GateWithdrawn = true
		record.GateWithdrawnReason = route
		record.GateWithdrawnAt = time.Now()
		store.Records[string(sessionID)] = record
		return nil
	})
}

// RecordFor returns the recorded host of one Session, if any.
func (r *ManagedHostRegistry) RecordFor(sessionID SessionID) (ManagedHostRecord, bool, error) {
	records, err := r.Records()
	if err != nil {
		return ManagedHostRecord{}, false, err
	}
	for _, record := range records {
		if record.SessionID == sessionID {
			return record, true, nil
		}
	}
	return ManagedHostRecord{}, false, nil
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

// ManagedHostEndpoint resolves one managed Session to the socket path and
// handshake token its agent host was recorded with. Only these recorded facts
// ever address the host — never a process-table search. A missing record or
// an unreadable store reads as unreachable, so the Session is unobservable
// and never dead.
func ManagedHostEndpoint(sessionID SessionID) (socketPath string, token AgentHostToken, err error) {
	records, recErr := NewManagedHostRegistry().Records()
	if recErr != nil {
		return "", "", fmt.Errorf("%w: %v", ErrManagedHostUnreachable, recErr)
	}
	for _, record := range records {
		if record.SessionID == sessionID {
			return record.SocketPath, record.Token, nil
		}
	}
	return "", "", fmt.Errorf("%w: für Session %q ist kein Host verzeichnet", ErrManagedHostUnreachable, sessionID)
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
	// Provenance is the launch the reclaimed host reports for its own
	// process, set only when the host's identity was confirmed and its report
	// matches the recorded launch with the approval gate on. Any other
	// outcome carries none; a record is never provenance by itself.
	Provenance []string `json:"provenance,omitempty"`
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
				if len(record.LaunchArgv) > 0 {
					result.Provenance, result.Reason = reclaimedProvenance(record)
				}
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

// reclaimedProvenance asks a confirmed host which launch it is running and
// accepts it only when it is the recorded one and keeps the approval gate.
func reclaimedProvenance(record ManagedHostRecord) ([]string, string) {
	state, err := QueryAgentHostState(record.SocketPath, record.Token)
	if err != nil {
		return nil, fmt.Sprintf("Startargumente des Hosts nicht lesbar: %v", err)
	}
	if !hostProvenanceEstablished(record, state) {
		return nil, "der Host meldet nicht die verzeichneten Startargumente mit Freigabe-Gate"
	}
	return state.LaunchArgv, ""
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
