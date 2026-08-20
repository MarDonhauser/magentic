package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionDesiredState is the durable outcome reconciled by SessionLifecycle.
// It describes Magentic intent, not a momentary tmux observation.
type SessionDesiredState string

const (
	SessionDesiredRunning SessionDesiredState = "running"
	SessionDesiredLater   SessionDesiredState = "later"
	SessionDesiredRemoved SessionDesiredState = "removed"
)

type LifecyclePhase string

const (
	LifecyclePlanned       LifecyclePhase = "planned"
	LifecycleWorktreeReady LifecyclePhase = "worktree_ready"
	LifecycleRuntimeReady  LifecyclePhase = "runtime_ready"
	LifecycleRegistered    LifecyclePhase = "registered"
	LifecycleConverged     LifecyclePhase = "converged"
	LifecycleFailed        LifecyclePhase = "failed"
)

// InitialPromptDelivery is deliberately separate from convergence. A tmux
// send and an agent accepting the prompt cannot be committed atomically; an
// unknown delivery must never be retried automatically.
type InitialPromptDelivery string

const (
	InitialPromptNotRequested InitialPromptDelivery = "not_requested"
	InitialPromptPending      InitialPromptDelivery = "pending"
	InitialPromptUnknown      InitialPromptDelivery = "delivery_unknown"
	InitialPromptDelivered    InitialPromptDelivery = "delivered"
	InitialPromptFailed       InitialPromptDelivery = "failed"
)

type LifecycleAppliedState struct {
	WorktreeReady   bool `json:"worktreeReady"`
	BaselineKnown   bool `json:"baselineKnown"`
	RuntimePresent  bool `json:"runtimePresent"`
	RegistryUpdated bool `json:"registryUpdated"`
}

// LifecycleRecord is the compact desired/applied ledger entry for one
// Session. A newer transition replaces the older entry for the same SessionID.
type LifecycleRecord struct {
	TransitionID   string                `json:"transitionId"`
	SessionID      SessionID             `json:"sessionId"`
	Desired        SessionDesiredState   `json:"desired"`
	Phase          LifecyclePhase        `json:"phase"`
	Session        Session               `json:"session"`
	Project        Project               `json:"project,omitempty"`
	CreateWorktree bool                  `json:"createWorktree,omitempty"`
	StartMode      string                `json:"startMode,omitempty"`
	InitialPrompt  string                `json:"initialPrompt,omitempty"`
	PromptDelivery InitialPromptDelivery `json:"promptDelivery"`
	Applied        LifecycleAppliedState `json:"applied"`
	Attempts       int                   `json:"attempts"`
	MayHaveApplied bool                  `json:"mayHaveApplied,omitempty"`
	LastError      string                `json:"lastError,omitempty"`
	CreatedAt      time.Time             `json:"createdAt"`
	UpdatedAt      time.Time             `json:"updatedAt"`
}

type LifecycleSnapshot struct {
	Revision uint64            `json:"revision"`
	Records  []LifecycleRecord `json:"records"`
}

type SessionProvision struct {
	Project        Project
	Name           string
	Directory      string
	Worktree       bool
	CreateWorktree bool
	Kind           SessionKind
	Presentation   SessionPresentation
	Purpose        SessionPurpose
	InitialPrompt  string
}

type SessionLifecycleResult struct {
	Session Session         `json:"session"`
	Record  LifecycleRecord `json:"record"`
}

type LifecycleProblem struct {
	SessionID SessionID `json:"sessionId,omitempty"`
	Name      string    `json:"name,omitempty"`
	Message   string    `json:"message"`
}

type LifecycleReconcileResult struct {
	Examined  int                `json:"examined"`
	Converged int                `json:"converged"`
	Restored  int                `json:"restored"`
	Problems  []LifecycleProblem `json:"problems,omitempty"`
}

type SessionLifecycleConfig struct {
	RegistryPath string
	LedgerPath   string
}

func SessionLifecyclePath() string {
	if path := os.Getenv("MAGENTIC_LIFECYCLE"); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(StatePath()), "lifecycle.json")
}

type lifecycleRegistry interface {
	Snapshot(context.Context) (RegistrySnapshot, error)
	Change(context.Context, RegistryChange) (RegistryChangeResult, error)
}

type lifecycleRuntime interface {
	Exists(context.Context, Session) (bool, error)
	Start(context.Context, Session, string) error
	Stop(context.Context, Session) error
	DeliverInitial(context.Context, Session, string) (bool, error)
}

type lifecycleRepositories interface {
	Change(context.Context, ManagedWorktreeChange) (ManagedWorktreeChangeResult, error)
	Inspect(context.Context, RepositoryInspectRequest) (RepositoryInspection, error)
}

type SessionLifecycle struct {
	registry     lifecycleRegistry
	runtime      lifecycleRuntime
	repositories lifecycleRepositories
	ledgerPath   string
	now          func() time.Time
}

func OpenSessionLifecycle(config SessionLifecycleConfig) *SessionLifecycle {
	if config.RegistryPath == "" {
		config.RegistryPath = StatePath()
	}
	if config.LedgerPath == "" {
		config.LedgerPath = SessionLifecyclePath()
	}
	return newSessionLifecycle(
		OpenRegistry(config.RegistryPath),
		tmuxLifecycleRuntime{},
		NewRepositories(),
		config.LedgerPath,
	)
}

func newSessionLifecycle(registry lifecycleRegistry, runtime lifecycleRuntime, repositories lifecycleRepositories, ledgerPath string) *SessionLifecycle {
	return &SessionLifecycle{
		registry: registry, runtime: runtime, repositories: repositories,
		ledgerPath: ledgerPath, now: time.Now,
	}
}

func (l *SessionLifecycle) Snapshot(ctx context.Context) (LifecycleSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var snapshot LifecycleSnapshot
	err := withRegistryFileLock(ctx, l.ledgerPath, func() error {
		ledger, err := readLifecycleLedger(l.ledgerPath)
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

func (l *SessionLifecycle) Provision(ctx context.Context, request SessionProvision) (SessionLifecycleResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	name := SanitizeName(request.Name)
	if name == "" {
		return SessionLifecycleResult{}, errors.New("Session name is required")
	}
	if request.Project.Path == "" && request.Directory == "" {
		return SessionLifecycleResult{}, errors.New("Session directory is required")
	}
	if request.CreateWorktree && request.Project.Path == "" {
		return SessionLifecycleResult{}, errors.New("a managed Worktree requires a Project")
	}

	kind := request.Kind
	if kind == "" {
		kind = SessionKindCodingAgent
	}
	presentation := request.Presentation
	if presentation == "" {
		presentation = SessionPresentationListed
	}
	purpose := request.Purpose
	if purpose == "" {
		purpose = SessionPurposeWork
	}
	now := l.now()
	session := Session{
		ID: SessionID(NewUUID()), Name: name, ProjectID: request.Project.ID,
		Project: request.Project.Name, Dir: filepath.Clean(request.Directory),
		Worktree: request.Worktree || request.CreateWorktree, SessionKind: kind,
		Presentation: presentation, Purpose: purpose,
		RuntimeName: SessionName(name), CreatedAt: now,
	}
	if request.Directory == "" && !request.CreateWorktree {
		session.Dir = filepath.Clean(request.Project.Path)
	}
	if kind == SessionKindTerminal {
		if presentation == SessionPresentationDock {
			session.Kind = KindDock
		} else {
			session.Kind = KindTerm
		}
	} else {
		runID := NewUUID()
		session.SessionID = runID
		session.AgentRuns = []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: runID}}
	}
	record := LifecycleRecord{
		TransitionID: NewUUID(), SessionID: session.ID,
		Desired: SessionDesiredRunning, Phase: LifecyclePlanned,
		Session: session, Project: request.Project,
		CreateWorktree: request.CreateWorktree, StartMode: "new",
		InitialPrompt: request.InitialPrompt, PromptDelivery: InitialPromptNotRequested,
		CreatedAt: now, UpdatedAt: now,
	}
	if request.InitialPrompt != "" {
		record.PromptDelivery = InitialPromptPending
	}
	if !request.CreateWorktree {
		record.Applied.WorktreeReady = true
	}
	advanced := record
	err := l.withSessionTransition(ctx, session.ID, session.Name, func() error {
		if _, putErr := l.putRecord(ctx, record, false); putErr != nil {
			return putErr
		}
		var advanceErr error
		advanced, advanceErr = l.advanceRunning(ctx, record)
		return advanceErr
	})
	return SessionLifecycleResult{Session: advanced.Session, Record: advanced}, err
}

func (l *SessionLifecycle) Park(ctx context.Context, id SessionID, name string) (SessionLifecycleResult, error) {
	return l.planExisting(ctx, id, name, SessionDesiredLater)
}

func (l *SessionLifecycle) Remove(ctx context.Context, id SessionID, name string) (SessionLifecycleResult, error) {
	return l.planExisting(ctx, id, name, SessionDesiredRemoved)
}

func (l *SessionLifecycle) Resume(ctx context.Context, id SessionID, name string) (SessionLifecycleResult, error) {
	return l.planExisting(ctx, id, name, SessionDesiredRunning)
}

func (l *SessionLifecycle) planExisting(ctx context.Context, id SessionID, name string, desired SessionDesiredState) (SessionLifecycleResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return SessionLifecycleResult{}, err
	}
	state := snapshot.State()
	idx := sessionIndex(&state, id, name)
	if idx < 0 {
		return SessionLifecycleResult{}, fmt.Errorf("Session %q not found", name)
	}
	session := state.Agents[idx]
	advanced := LifecycleRecord{SessionID: session.ID, Session: session, Desired: desired}
	err = l.withSessionTransition(ctx, session.ID, session.Name, func() error {
		// The first read resolves legacy name-only callers to a stable SessionID.
		// Re-read under the transition lock so a preceding transition's Registry
		// change is part of this transition's starting point.
		currentSnapshot, snapshotErr := l.registry.Snapshot(ctx)
		if snapshotErr != nil {
			return snapshotErr
		}
		currentState := currentSnapshot.State()
		currentIndex := sessionIndex(&currentState, session.ID, session.Name)
		if currentIndex < 0 {
			return fmt.Errorf("Session %q not found", session.Name)
		}
		var advanceErr error
		advanced, advanceErr = l.planSessionLocked(ctx, currentState, currentState.Agents[currentIndex], desired)
		return advanceErr
	})
	return SessionLifecycleResult{Session: advanced.Session, Record: advanced}, err
}

// planSessionLocked persists intent before calling either external
// Implementation. The caller must hold this Session's transition lock.
func (l *SessionLifecycle) planSessionLocked(ctx context.Context, state State, session Session, desired SessionDesiredState) (LifecycleRecord, error) {
	now := l.now()
	record := LifecycleRecord{
		TransitionID: NewUUID(), SessionID: session.ID, Desired: desired,
		Phase: LifecyclePlanned, Session: session,
		StartMode: "resume", PromptDelivery: InitialPromptNotRequested,
		Applied:   LifecycleAppliedState{WorktreeReady: true},
		CreatedAt: now, UpdatedAt: now,
	}
	if project := state.ProjectByID(session.ProjectID); project != nil {
		record.Project = *project
	} else if project := state.ProjectByName(session.Project); project != nil {
		record.Project = *project
	}
	if _, err := l.putRecord(ctx, record, false); err != nil {
		return record, err
	}
	var advanced LifecycleRecord
	var err error
	if desired == SessionDesiredRunning {
		advanced, err = l.advanceRunning(ctx, record)
	} else {
		advanced, err = l.advanceStopped(ctx, record)
	}
	return advanced, err
}

func (l *SessionLifecycle) Reconcile(ctx context.Context) (LifecycleReconcileResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := LifecycleReconcileResult{}
	snapshot, err := l.Snapshot(ctx)
	if err != nil {
		return result, err
	}
	for _, record := range snapshot.Records {
		if record.Phase == LifecycleConverged {
			continue
		}
		result.Examined++
		beforeRuntime := record.Applied.RuntimePresent
		advanced, advanceErr := l.advanceSerialized(ctx, record)
		err = advanceErr
		if err != nil {
			result.Problems = append(result.Problems, LifecycleProblem{SessionID: record.SessionID, Name: record.Session.Name, Message: err.Error()})
			continue
		}
		if advanced.Phase == LifecycleConverged {
			result.Converged++
			if advanced.Desired == SessionDesiredRunning && !beforeRuntime && advanced.Applied.RuntimePresent {
				result.Restored++
			}
		}
	}

	registrySnapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return result, err
	}
	for _, session := range registrySnapshot.State().Agents {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		examined, restored, reconcileErr := l.reconcileRegisteredSession(ctx, session.ID, session.Name)
		if reconcileErr != nil {
			result.Problems = append(result.Problems, LifecycleProblem{SessionID: session.ID, Name: session.Name, Message: reconcileErr.Error()})
			continue
		}
		if examined {
			result.Examined++
			result.Converged++
			if restored {
				result.Restored++
			}
		}
	}
	return result, nil
}

// advanceSerialized reconciles the newest record for a Session while holding
// the same process-coordinated lock used by explicit transitions. If the
// caller observed an older record, no stale side effect is allowed to cross
// the runtime Seam.
func (l *SessionLifecycle) advanceSerialized(ctx context.Context, expected LifecycleRecord) (LifecycleRecord, error) {
	advanced := expected
	err := l.withSessionTransition(ctx, expected.SessionID, expected.Session.Name, func() error {
		latest, ok, readErr := l.recordForSession(ctx, expected.SessionID)
		if readErr != nil {
			return readErr
		}
		if !ok {
			return ErrLifecycleSuperseded
		}
		if latest.Phase == LifecycleConverged {
			advanced = latest
			return nil
		}
		var advanceErr error
		if latest.Desired == SessionDesiredRunning {
			advanced, advanceErr = l.advanceRunning(ctx, latest)
		} else {
			advanced, advanceErr = l.advanceStopped(ctx, latest)
		}
		return advanceErr
	})
	return advanced, err
}

// reconcileRegisteredSession takes its observation and decision under the
// Session transition lock. An explicit Park or Resume that was already in
// flight therefore cannot be undone using a stale Registry snapshot.
func (l *SessionLifecycle) reconcileRegisteredSession(ctx context.Context, id SessionID, name string) (examined, restored bool, err error) {
	err = l.withSessionTransition(ctx, id, name, func() error {
		registrySnapshot, snapshotErr := l.registry.Snapshot(ctx)
		if snapshotErr != nil {
			return snapshotErr
		}
		state := registrySnapshot.State()
		idx := sessionIndex(&state, id, name)
		if idx < 0 {
			return nil
		}
		session := state.Agents[idx]
		exists, observeErr := l.runtime.Exists(ctx, session)
		if observeErr != nil {
			return observeErr
		}
		desired := SessionDesiredRunning
		if !session.LaterAt.IsZero() {
			desired = SessionDesiredLater
		}
		latest, hasRecord, readErr := l.recordForSession(ctx, session.ID)
		if readErr != nil {
			return readErr
		}
		if hasRecord && latest.Phase != LifecycleConverged {
			// The exact partial intent was already attempted in the first pass.
			return nil
		}
		consistent := (desired == SessionDesiredRunning && exists) || (desired == SessionDesiredLater && !exists)
		if consistent {
			return nil
		}
		examined = true
		advanced, advanceErr := l.planSessionLocked(ctx, state, session, desired)
		if advanceErr != nil {
			return advanceErr
		}
		restored = advanced.Desired == SessionDesiredRunning && advanced.Applied.RuntimePresent
		return nil
	})
	return examined, restored, err
}

func (l *SessionLifecycle) withSessionTransition(ctx context.Context, id SessionID, name string, fn func() error) error {
	key := string(id)
	if key == "" {
		key = "name:" + name
	}
	digest := sha256.Sum256([]byte(key))
	lockPath := filepath.Join(filepath.Dir(l.ledgerPath), ".lifecycle-session-locks", fmt.Sprintf("%x", digest[:]))
	return withRegistryFileLock(ctx, lockPath, fn)
}

func (l *SessionLifecycle) advanceRunning(ctx context.Context, record LifecycleRecord) (LifecycleRecord, error) {
	current, err := l.currentRecord(ctx, record)
	if err != nil {
		return record, err
	}
	record = current
	record.Attempts++
	record.LastError = ""
	if record.CreateWorktree && !record.Applied.WorktreeReady {
		change, changeErr := l.repositories.Change(ctx, CreateManagedWorktreeChange(record.Project, record.Session.Name))
		if change.Path != "" {
			record.Session.Dir = change.Path
		}
		if changeErr != nil {
			record.MayHaveApplied = change.MayHaveApplied
			return l.failRecord(ctx, record, changeErr)
		}
		record.Applied.WorktreeReady = change.State == RepositoryKnown
		record.Phase = LifecycleWorktreeReady
		if record, err = l.putRecord(ctx, record, true); err != nil {
			return record, err
		}
	}
	if strings.TrimSpace(record.Session.Dir) == "" || record.Session.Dir == "." {
		return l.failRecord(ctx, record, errors.New("Session directory is unavailable"))
	}
	if !record.Applied.BaselineKnown {
		inspection, inspectErr := l.repositories.Inspect(ctx, RepositoryInspectRequest{
			Directory: record.Session.Dir, MainBranch: record.Project.MainBranch,
		})
		if inspectErr == nil && inspection.Baseline.Known() {
			record.Session.BaseCommit = inspection.Baseline.Value.Head
			record.Session.BaseDirty = append([]string(nil), inspection.Baseline.Value.DirtyPaths...)
			record.Applied.BaselineKnown = true
			if record, err = l.putRecord(ctx, record, true); err != nil {
				return record, err
			}
		}
	}
	exists, err := l.runtime.Exists(ctx, record.Session)
	if err != nil {
		return l.failRecord(ctx, record, err)
	}
	if !exists {
		startErr := l.runtime.Start(ctx, record.Session, record.StartMode)
		if startErr != nil {
			exists, err = l.runtime.Exists(ctx, record.Session)
			if err != nil || !exists {
				if err != nil {
					startErr = fmt.Errorf("start failed: %v; postcondition unknown: %w", startErr, err)
				}
				return l.failRecord(ctx, record, startErr)
			}
			record.MayHaveApplied = true
		} else {
			exists = true
		}
	}
	record.Applied.RuntimePresent = exists
	record.Phase = LifecycleRuntimeReady
	if record, err = l.putRecord(ctx, record, true); err != nil {
		return record, err
	}

	registrySnapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return l.failRecord(ctx, record, err)
	}
	state := registrySnapshot.State()
	idx := sessionIndex(&state, record.Session.ID, record.Session.Name)
	var registryResult RegistryChangeResult
	if idx < 0 {
		registryResult, err = l.registry.Change(ctx, RegisterSession(record.Session))
		if err != nil {
			return l.failRecord(ctx, record, err)
		}
	} else if state.Agents[idx].ID != record.Session.ID {
		return l.failRecord(ctx, record, fmt.Errorf("Session name %q belongs to another SessionID", record.Session.Name))
	} else {
		// Running is a durable Registry intent as well as a runtime
		// postcondition. Reopening through the semantic Interface clears
		// LaterAt idempotently; merely observing the existing record would leave
		// a resumed runtime marked for the next reconciliation to stop again.
		registryResult, err = l.registry.Change(ctx, ReopenRegisteredSession(record.Session.ID, record.Session.Name))
		if err != nil {
			return l.failRecord(ctx, record, err)
		}
	}
	registeredState := registryResult.Snapshot.State()
	registeredIndex := sessionIndex(&registeredState, record.Session.ID, record.Session.Name)
	if registeredIndex < 0 {
		return l.failRecord(ctx, record, fmt.Errorf("Session %q was not present after Registry update", record.Session.Name))
	}
	record.Session = registeredState.Agents[registeredIndex]
	record.Applied.RegistryUpdated = true
	record.Phase = LifecycleRegistered
	if record, err = l.putRecord(ctx, record, true); err != nil {
		return record, err
	}

	if record.InitialPrompt != "" && record.PromptDelivery == InitialPromptPending {
		// Persist uncertainty before crossing the tmux Seam. A crash after the
		// send must not turn into a duplicate prompt during reconciliation.
		record.PromptDelivery = InitialPromptUnknown
		if record, err = l.putRecord(ctx, record, true); err != nil {
			return record, err
		}
		confirmed, deliverErr := l.runtime.DeliverInitial(ctx, record.Session, record.InitialPrompt)
		// The prompt is no longer actionable after crossing the delivery Seam.
		// Retain only the outcome, not durable prompt content.
		record.InitialPrompt = ""
		if deliverErr != nil {
			record.PromptDelivery = InitialPromptFailed
			return l.failRecord(ctx, record, deliverErr)
		}
		if confirmed {
			record.PromptDelivery = InitialPromptDelivered
		}
	}
	record.Phase = LifecycleConverged
	record.LastError = ""
	return l.putRecord(ctx, record, true)
}

func (l *SessionLifecycle) advanceStopped(ctx context.Context, record LifecycleRecord) (LifecycleRecord, error) {
	current, err := l.currentRecord(ctx, record)
	if err != nil {
		return record, err
	}
	record = current
	record.Attempts++
	record.LastError = ""
	exists, err := l.runtime.Exists(ctx, record.Session)
	if err != nil {
		return l.failRecord(ctx, record, err)
	}
	if exists {
		stopErr := l.runtime.Stop(ctx, record.Session)
		if stopErr != nil {
			exists, err = l.runtime.Exists(ctx, record.Session)
			if err != nil || exists {
				if err != nil {
					stopErr = fmt.Errorf("stop failed: %v; postcondition unknown: %w", stopErr, err)
				}
				return l.failRecord(ctx, record, stopErr)
			}
			record.MayHaveApplied = true
		} else {
			exists = false
		}
	}
	record.Applied.RuntimePresent = false
	record.Phase = LifecycleRuntimeReady
	if record, err = l.putRecord(ctx, record, true); err != nil {
		return record, err
	}
	var change RegistryChange
	if record.Desired == SessionDesiredRemoved {
		change = RemoveSession(record.Session.ID, record.Session.Name)
	} else {
		change = MarkSessionLater(record.Session.ID, record.Session.Name, record.CreatedAt)
	}
	if _, err = l.registry.Change(ctx, change); err != nil {
		return l.failRecord(ctx, record, err)
	}
	record.Applied.RegistryUpdated = true
	record.Phase = LifecycleConverged
	record.LastError = ""
	return l.putRecord(ctx, record, true)
}

var ErrLifecycleSuperseded = errors.New("Session Lifecycle transition was superseded")

func (l *SessionLifecycle) currentRecord(ctx context.Context, expected LifecycleRecord) (LifecycleRecord, error) {
	var record LifecycleRecord
	err := withRegistryFileLock(ctx, l.ledgerPath, func() error {
		ledger, err := readLifecycleLedger(l.ledgerPath)
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

func (l *SessionLifecycle) recordForSession(ctx context.Context, id SessionID) (LifecycleRecord, bool, error) {
	var record LifecycleRecord
	var ok bool
	err := withRegistryFileLock(ctx, l.ledgerPath, func() error {
		ledger, err := readLifecycleLedger(l.ledgerPath)
		if err != nil {
			return err
		}
		record, ok = ledger.Records[string(id)]
		return nil
	})
	return record, ok, err
}

func (l *SessionLifecycle) failRecord(ctx context.Context, record LifecycleRecord, cause error) (LifecycleRecord, error) {
	record.Phase = LifecycleFailed
	record.LastError = cause.Error()
	saved, saveErr := l.putRecord(ctx, record, true)
	if saveErr != nil {
		return record, errors.Join(cause, saveErr)
	}
	return saved, cause
}

func (l *SessionLifecycle) putRecord(ctx context.Context, record LifecycleRecord, requireCurrent bool) (LifecycleRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var saved LifecycleRecord
	err := withRegistryFileLock(ctx, l.ledgerPath, func() error {
		ledger, err := readLifecycleLedger(l.ledgerPath)
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
		record.UpdatedAt = l.now()
		ledger.Records[key] = record
		compactLifecycleLedger(ledger)
		ledger.Revision++
		if err := writeLifecycleLedger(l.ledgerPath, ledger); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lifecycle-*.tmp")
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

type lifecycleCommandRunner func(context.Context, string, ...string) ([]byte, error)

type tmuxLifecycleRuntime struct {
	command lifecycleCommandRunner
}

func (r tmuxLifecycleRuntime) combinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.command != nil {
		return r.command(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (r tmuxLifecycleRuntime) Exists(ctx context.Context, session Session) (bool, error) {
	out, err := r.combinedOutput(ctx, "tmux", "has-session", "-t", TargetSession(session.TmuxName()))
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && tmuxTargetKnownAbsent(out) {
		return false, nil
	}
	message := strings.TrimSpace(string(out))
	if message == "" {
		return false, fmt.Errorf("observe tmux Session %q: %w", session.TmuxName(), err)
	}
	return false, fmt.Errorf("observe tmux Session %q: %w: %s", session.TmuxName(), err, message)
}

func tmuxTargetKnownAbsent(output []byte) bool {
	message := strings.ToLower(strings.TrimSpace(string(output)))
	return strings.Contains(message, "can't find session:") ||
		strings.Contains(message, "no server running on ")
}

func (tmuxLifecycleRuntime) Start(ctx context.Context, session Session, mode string) error {
	if info, err := os.Stat(session.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("Session directory %q is unavailable", session.Dir)
	}
	args := []string{"new-session", "-d", "-s", session.TmuxName(), "-c", session.Dir, "-x", "220", "-y", "50"}
	if out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	TmuxConfigureUX()
	if session.IsTerm() {
		return nil
	}
	run, hasRun := session.AgentRun(AgentVendorClaude)
	command := "claude --name " + ShellQuote(session.TmuxName())
	if hasRun {
		flag := "--resume"
		if mode == "new" {
			flag = "--session-id"
		}
		command += " " + flag + " " + ShellQuote(run.ExternalID)
	} else if mode != "new" {
		command += " --continue"
	}
	if _, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", TargetPane(session.TmuxName()), "-l", command).CombinedOutput(); err != nil {
		return fmt.Errorf("start coding agent: %w", err)
	}
	if _, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", TargetPane(session.TmuxName()), "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("submit coding-agent command: %w", err)
	}
	return nil
}

func (tmuxLifecycleRuntime) Stop(ctx context.Context, session Session) error {
	out, err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", TargetSession(session.TmuxName())).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux kill-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (tmuxLifecycleRuntime) DeliverInitial(_ context.Context, session Session, prompt string) (bool, error) {
	if session.IsTerm() {
		return false, errors.New("initial coding prompt cannot be delivered to a terminal Session")
	}
	// enqueuePrompt confirms only in-process scheduling. The durable state
	// therefore remains delivery_unknown until a future observation can prove
	// acceptance; reconciliation intentionally does not submit it again.
	if err := enqueuePrompt(session.TmuxName(), prompt, true, AgentToolClaude, true, true, false); err != nil {
		return false, err
	}
	return false, nil
}
