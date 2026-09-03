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

// LifecycleTransitionKind separates state convergence from a durable runtime
// rename. The empty value is the state-transition format written by schema-v1
// ledgers before rename intents existed.
type LifecycleTransitionKind string

const LifecycleTransitionRename LifecycleTransitionKind = "rename"

// LifecycleTransitionResume marks a resume-after-restart: desired running for
// an existing Session in a freshly minted runtime. Reconciliation completes an
// interrupted resume through advanceResume, never through advanceRunning, so a
// retried resume cannot adopt a foreign runtime or resurrect a removed Session.
const LifecycleTransitionResume LifecycleTransitionKind = "resume"

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
	WorktreeReady        bool `json:"worktreeReady"`
	BaselineKnown        bool `json:"baselineKnown"`
	RuntimePresent       bool `json:"runtimePresent"`
	RuntimeRenameSettled bool `json:"runtimeRenameSettled,omitempty"`
	RuntimeRenamed       bool `json:"runtimeRenamed,omitempty"`
	RegistryUpdated      bool `json:"registryUpdated"`
}

// LifecycleRecord is the compact desired/applied ledger entry for one
// Session. A newer transition replaces the older entry for the same SessionID.
type LifecycleRecord struct {
	TransitionID    string                  `json:"transitionId"`
	SessionID       SessionID               `json:"sessionId"`
	TransitionKind  LifecycleTransitionKind `json:"transitionKind,omitempty"`
	Desired         SessionDesiredState     `json:"desired"`
	Phase           LifecyclePhase          `json:"phase"`
	Session         Session                 `json:"session"`
	RenameTo        string                  `json:"renameTo,omitempty"`
	RenameRuntimeTo string                  `json:"renameRuntimeTo,omitempty"`
	Project         Project                 `json:"project,omitempty"`
	CreateWorktree  bool                    `json:"createWorktree,omitempty"`
	StartMode       string                  `json:"startMode,omitempty"`
	InitialPrompt   string                  `json:"initialPrompt,omitempty"`
	PromptDelivery  InitialPromptDelivery   `json:"promptDelivery"`
	Applied         LifecycleAppliedState   `json:"applied"`
	Attempts        int                     `json:"attempts"`
	MayHaveApplied  bool                    `json:"mayHaveApplied,omitempty"`
	LastError       string                  `json:"lastError,omitempty"`
	CreatedAt       time.Time               `json:"createdAt"`
	UpdatedAt       time.Time               `json:"updatedAt"`
}

type LifecycleSnapshot struct {
	Revision uint64            `json:"revision"`
	Records  []LifecycleRecord `json:"records"`
}

type SessionProvision struct {
	ProjectID        ProjectID
	Name             string
	Directory        string
	Worktree         bool
	CreateWorktree   bool
	Kind             SessionKind
	Presentation     SessionPresentation
	Purpose          SessionPurpose
	SpecificationRef SpecificationRef
	InitialPrompt    string
	Vendor           AgentVendor
	Runtime          AgentRuntime
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
	Rename(context.Context, Session, string) error
	DeliverInitial(context.Context, Session, string) (bool, error)
}

// exactLifecycleRuntime is the mandatory internal Adapter at the runtime
// Seam. Durable RuntimeNames are opaque identities: no Lifecycle path may use
// Session.TmuxName's legacy fallback or normalize malformed stored state before
// addressing an external process.
type exactLifecycleRuntime struct {
	delegate lifecycleRuntime
}

func (r exactLifecycleRuntime) validate(session Session) error {
	if !validRuntimeIdentity(session.RuntimeName) {
		return fmt.Errorf("Session %q has no exact RuntimeName", session.Name)
	}
	return nil
}

func (r exactLifecycleRuntime) Exists(ctx context.Context, session Session) (bool, error) {
	if err := r.validate(session); err != nil {
		return false, err
	}
	return r.delegate.Exists(ctx, session)
}

func (r exactLifecycleRuntime) Start(ctx context.Context, session Session, mode string) error {
	if err := r.validate(session); err != nil {
		return err
	}
	return r.delegate.Start(ctx, session, mode)
}

func (r exactLifecycleRuntime) Stop(ctx context.Context, session Session) error {
	if err := r.validate(session); err != nil {
		return err
	}
	return r.delegate.Stop(ctx, session)
}

func (r exactLifecycleRuntime) Rename(ctx context.Context, session Session, targetRuntime string) error {
	if err := r.validate(session); err != nil {
		return err
	}
	if !validRuntimeIdentity(targetRuntime) {
		return fmt.Errorf("Session %q has no exact target RuntimeName", session.Name)
	}
	return r.delegate.Rename(ctx, session, targetRuntime)
}

func (r exactLifecycleRuntime) DeliverInitial(ctx context.Context, session Session, prompt string) (bool, error) {
	if err := r.validate(session); err != nil {
		return false, err
	}
	return r.delegate.DeliverInitial(ctx, session, prompt)
}

type lifecycleRepositories interface {
	Change(context.Context, ManagedWorktreeChange) (ManagedWorktreeChangeResult, error)
	Inspect(context.Context, RepositoryInspectRequest) (RepositoryInspection, error)
}

type SessionLifecycle struct {
	registry     lifecycleRegistry
	runtime      lifecycleRuntime
	repositories lifecycleRepositories
	projects     projectTransitionCoordinator
	worktrees    worktreeTransitionCoordinator
	runtimes     runtimeTransitionCoordinator
	observe      func(context.Context, []Session) ObservationSnapshot
	discover     func(context.Context, *State) RegistryDiscovery
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
	lifecycle := newSessionLifecycle(
		OpenRegistry(config.RegistryPath),
		tmuxLifecycleRuntime{},
		NewRepositories(),
		config.LedgerPath,
	)
	lifecycle.worktrees = newWorktreeTransitionCoordinator(config.RegistryPath)
	lifecycle.projects = newProjectTransitionCoordinator(config.RegistryPath)
	lifecycle.runtimes = newRuntimeTransitionCoordinator(config.RegistryPath)
	return lifecycle
}

func newSessionLifecycle(registry lifecycleRegistry, runtime lifecycleRuntime, repositories lifecycleRepositories, ledgerPath string) *SessionLifecycle {
	return &SessionLifecycle{
		registry: registry, runtime: exactLifecycleRuntime{delegate: runtime}, repositories: repositories,
		projects: newProjectTransitionCoordinator(ledgerPath), worktrees: newWorktreeTransitionCoordinator(ledgerPath),
		runtimes: newRuntimeTransitionCoordinator(ledgerPath),
		observe:  Observe, discover: DiscoverNew,
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
	if request.ProjectID == "" && strings.TrimSpace(request.Directory) == "" {
		return SessionLifecycleResult{}, errors.New("Session directory is required")
	}
	if request.CreateWorktree && request.ProjectID == "" {
		return SessionLifecycleResult{}, errors.New("a managed Worktree requires a ProjectID")
	}
	project, err := l.resolveProject(ctx, request.ProjectID)
	if err != nil {
		return SessionLifecycleResult{}, err
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
	runtime := request.Runtime
	if runtime == "" {
		runtime = RuntimeTmux
	}
	if runtime == RuntimeManaged && kind == SessionKindTerminal {
		return SessionLifecycleResult{}, errors.New("eine Terminal-Session kann nur den tmux-Runtime nutzen")
	}
	now := l.now()
	session := Session{
		ID: SessionID(NewUUID()), Name: name, ProjectID: project.ID,
		Project: project.Name, Dir: filepath.Clean(request.Directory),
		Worktree: request.Worktree || request.CreateWorktree, SessionKind: kind,
		Presentation: presentation, Purpose: purpose, SpecificationRef: request.SpecificationRef,
		RuntimeName: SessionName(name), Runtime: runtime, CreatedAt: now,
	}
	if request.CreateWorktree {
		session.Dir, _ = managedWorktreeTarget(project, name)
	} else if request.Directory == "" {
		session.Dir = filepath.Clean(project.Path)
	}
	if kind == SessionKindTerminal {
		if presentation == SessionPresentationDock {
			session.Kind = KindDock
		} else {
			session.Kind = KindTerm
		}
	} else {
		vendor := request.Vendor
		if vendor == "" {
			vendor = AgentVendorClaude
		}
		provider, known := providerForVendor(vendor)
		if !known {
			return SessionLifecycleResult{}, fmt.Errorf("unbekannter Agent-Vendor %q", vendor)
		}
		if runtime == RuntimeManaged && !SupportsRuntime(provider, RuntimeManaged) {
			return SessionLifecycleResult{}, fmt.Errorf("Agent-Vendor %q kann nicht headless betrieben werden und unterstützt den managed Runtime nicht", vendor)
		}
		session.Vendor = vendor
		if runID := provider.NewRunID(); runID != "" {
			if vendor == AgentVendorClaude {
				// SessionID is the legacy Claude-only run field and stays in
				// step with the canonical AgentRunRef.
				session.SessionID = runID
			}
			session.AgentRuns = []AgentRunRef{{Vendor: vendor, ExternalID: runID}}
		}
	}
	record := LifecycleRecord{
		TransitionID: NewUUID(), SessionID: session.ID,
		Desired: SessionDesiredRunning, Phase: LifecyclePlanned,
		Session: session, Project: project,
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
	provision := func() error {
		// The Project transition lock makes the fresh ProjectID resolution and
		// every later Worktree/runtime/Registry crossing one indivisible scoped
		// transition relative to Project removal.
		freshProject, resolveErr := l.resolveProject(ctx, request.ProjectID)
		if resolveErr != nil {
			return resolveErr
		}
		freshSession := session
		freshSession.ProjectID = freshProject.ID
		freshSession.Project = freshProject.Name
		if request.CreateWorktree {
			freshSession.Dir, _ = managedWorktreeTarget(freshProject, name)
		} else if request.Directory == "" {
			freshSession.Dir = filepath.Clean(freshProject.Path)
		}
		target, managed := l.provisionWorktreeTarget(freshProject, request, freshSession)
		run := func() error {
			return l.withSessionTransition(ctx, session.ID, session.Name, func() error {
				// Re-resolve under Project -> Worktree -> Session coordination.
				// The RuntimeName lock below then covers the final availability
				// revalidation through runtime and Registry convergence.
				resolved, resolveErr := l.resolveProject(ctx, request.ProjectID)
				if resolveErr != nil {
					return resolveErr
				}
				resolvedTarget, resolvedManaged := l.provisionWorktreeTarget(resolved, request, freshSession)
				if managed != resolvedManaged || managed && canonicalWorktreeTransitionPath(target) != canonicalWorktreeTransitionPath(resolvedTarget) {
					return errors.New("Project Worktree identity changed during Session provisioning")
				}
				freshSession.ProjectID = resolved.ID
				freshSession.Project = resolved.Name
				if request.CreateWorktree {
					freshSession.Dir = resolvedTarget
				} else if request.Directory == "" {
					freshSession.Dir = filepath.Clean(resolved.Path)
				}
				record.Project = resolved
				record.Session = freshSession
				advanced = record
				return l.runtimes.with(ctx, []string{freshSession.RuntimeName}, func() error {
					if availabilityErr := l.requireProvisionTargetAvailable(ctx, freshSession); availabilityErr != nil {
						return availabilityErr
					}
					if _, putErr := l.putRecord(ctx, record, false); putErr != nil {
						return putErr
					}
					var advanceErr error
					advanced, advanceErr = l.advanceRunning(ctx, record)
					return advanceErr
				})
			})
		}
		if managed {
			return l.worktrees.with(ctx, freshProject, target, run)
		}
		return run()
	}
	err = l.projects.with(ctx, request.ProjectID, provision)
	return SessionLifecycleResult{Session: advanced.Session, Record: advanced}, err
}

func (l *SessionLifecycle) resolveProject(ctx context.Context, id ProjectID) (Project, error) {
	if id == "" {
		return Project{}, nil
	}
	snapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return Project{}, err
	}
	state := snapshot.State()
	project := state.ProjectByID(id)
	if project == nil {
		return Project{}, fmt.Errorf("ProjectID %q not found", id)
	}
	return *project, nil
}

func (l *SessionLifecycle) provisionWorktreeTarget(project Project, request SessionProvision, session Session) (string, bool) {
	if request.CreateWorktree {
		return managedWorktreeTarget(project, session.Name)
	}
	return managedWorktreeForDirectory(project, session.Dir)
}

// requireProvisionTargetAvailable is the single authority for a new Session's
// display-name and RuntimeName availability. Callers may suggest a friendly
// name from a Registry snapshot, but only this fail-closed probe runs under the
// Lifecycle transition locks immediately before the durable intent and every
// Worktree, runtime, or Registry mutation.
func (l *SessionLifecycle) requireProvisionTargetAvailable(ctx context.Context, candidate Session) error {
	if !validRuntimeIdentity(candidate.RuntimeName) {
		return fmt.Errorf("RuntimeName %q is not an exact runtime identity", candidate.RuntimeName)
	}
	snapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("verify Session provisioning target in Registry: %w", err)
	}
	state := snapshot.State()
	for _, registered := range state.Agents {
		if registered.Name == candidate.Name {
			return fmt.Errorf("Session %q already exists", candidate.Name)
		}
		if registered.RuntimeName == candidate.RuntimeName {
			return fmt.Errorf("RuntimeName %q is already registered", candidate.RuntimeName)
		}
	}
	exists, err := l.runtime.Exists(ctx, candidate)
	if err != nil {
		return fmt.Errorf("RuntimeName %q availability is unavailable: %w", candidate.RuntimeName, err)
	}
	if exists {
		return fmt.Errorf("RuntimeName %q is already occupied", candidate.RuntimeName)
	}
	return nil
}

func (l *SessionLifecycle) Park(ctx context.Context, id SessionID, name string) (SessionLifecycleResult, error) {
	return l.planExisting(ctx, id, name, SessionDesiredLater)
}

// SwitchVendor moves a Session to another coding-agent vendor. The target's
// binary is checked before anything is stopped, so a missing program leaves
// the running Session untouched. AgentRuns of every vendor survive the
// switch; the target resumes when it already has one.
func (l *SessionLifecycle) SwitchVendor(ctx context.Context, sessionID SessionID, vendor AgentVendor) (Session, error) {
	provider, known := providerForVendor(vendor)
	if !known {
		return Session{}, fmt.Errorf("unbekannter Agent-Vendor %q", vendor)
	}
	if !providerBinaryAvailable(provider) {
		return Session{}, fmt.Errorf("%s ist nicht installiert (%s nicht im PATH)", vendor, provider.Binary())
	}
	snapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return Session{}, err
	}
	state := snapshot.State()
	session := state.SessionByID(sessionID)
	if session == nil {
		return Session{}, fmt.Errorf("Session %q nicht gefunden", sessionID)
	}
	if session.IsTerm() {
		return Session{}, fmt.Errorf("Session %q ist ein Terminal und hat keinen Agent-Vendor", session.Name)
	}
	current := *session
	if current.SessionVendor() == vendor {
		return current, nil
	}
	var switched Session
	err = l.withSessionTransition(ctx, current.ID, current.Name, func() error {
		fresh, err := l.registry.Snapshot(ctx)
		if err != nil {
			return err
		}
		freshState := fresh.State()
		resolved := freshState.SessionByID(current.ID)
		if resolved == nil {
			return fmt.Errorf("Session %q wurde während des Wechsels entfernt", current.Name)
		}
		exists, err := l.runtime.Exists(ctx, *resolved)
		if err != nil {
			return err
		}
		if exists {
			if err := l.runtime.Stop(ctx, *resolved); err != nil {
				return err
			}
		}
		result, err := l.registry.Change(ctx, SetSessionVendor(resolved.ID, resolved.Name, vendor))
		if err != nil {
			return err
		}
		updated := result.Snapshot.State()
		target := updated.SessionByID(resolved.ID)
		if target == nil {
			return fmt.Errorf("Session %q wurde während des Wechsels entfernt", resolved.Name)
		}
		// Intent only: startCommandForSession asks the vendor's storage whether
		// the stored run really exists and corrects the form accordingly.
		mode := "new"
		if _, hasRun := target.AgentRun(vendor); hasRun {
			mode = "resume"
		}
		if err := l.runtime.Start(ctx, *target, mode); err != nil {
			return err
		}
		switched = *target
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	return switched, nil
}

func (l *SessionLifecycle) Remove(ctx context.Context, id SessionID, name string) (SessionLifecycleResult, error) {
	return l.planExisting(ctx, id, name, SessionDesiredRemoved)
}

// RemoveProject serializes Project deletion with every scoped Session
// transition, then resolves the stable ProjectID again under that coordination.
func (l *SessionLifecycle) RemoveProject(ctx context.Context, projectID ProjectID) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if projectID == "" {
		return errors.New("ProjectID is required")
	}
	return l.projects.with(ctx, projectID, func() error {
		project, err := l.resolveProject(ctx, projectID)
		if err != nil {
			return err
		}
		_, err = l.registry.Change(ctx, removeProjectChange(project.ID, project.Name))
		return err
	})
}

// RemoveManagedWorktree owns the complete destructive transition. Provision,
// Resume, reconciliation, and discovered-Session adoption contend on the same
// canonical per-Worktree lock. Under that coordination this method rereads the
// Registry, takes a fresh Observation, removes only known-safe Sessions, then
// repeats both reads immediately before crossing the repository removal Seam.
func (l *SessionLifecycle) RemoveManagedWorktree(ctx context.Context, projectID ProjectID, target string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return l.projects.with(ctx, projectID, func() error {
		project, resolveErr := l.resolveProject(ctx, projectID)
		if resolveErr != nil {
			return resolveErr
		}
		managedTarget, managed := managedWorktreeForDirectory(project, target)
		if !managed || canonicalWorktreeTransitionPath(managedTarget) != canonicalWorktreeTransitionPath(target) {
			return errors.New("Worktree path is not managed by the Project")
		}
		target = canonicalWorktreeTransitionPath(target)
		return l.worktrees.with(ctx, project, target, func() error {
			freshProject, resolveErr := l.resolveProject(ctx, projectID)
			if resolveErr != nil {
				return resolveErr
			}
			freshTarget, freshManaged := managedWorktreeForDirectory(freshProject, target)
			if !freshManaged || canonicalWorktreeTransitionPath(freshTarget) != target {
				return errors.New("Project Worktree identity changed before removal")
			}
			if discoveryErr := l.adoptDiscoveredSessionsInWorktree(ctx, target); discoveryErr != nil {
				return discoveryErr
			}

			sessions, readErr := l.registeredSessionsInWorktree(ctx, target)
			if readErr != nil {
				return readErr
			}
			observation := l.observe(ctx, sessions)
			if observeErr := validateWorktreeRemovalObservations(sessions, observation); observeErr != nil {
				return observeErr
			}
			for _, session := range sessions {
				if _, removeErr := l.Remove(ctx, session.ID, session.Name); removeErr != nil {
					return removeErr
				}
			}

			// These reads are deliberately fresh and adjacent to the destructive
			// repository call. Discovery covers external runtimes that have not yet
			// crossed the Registry Seam; the second Registry read then covers every
			// process-coordinated adoption path.
			if discoveryErr := l.adoptDiscoveredSessionsInWorktree(ctx, target); discoveryErr != nil {
				return discoveryErr
			}
			remaining, readErr := l.registeredSessionsInWorktree(ctx, target)
			if readErr != nil {
				return readErr
			}
			finalObservation := l.observe(ctx, remaining)
			if observeErr := validateWorktreeRemovalObservations(remaining, finalObservation); observeErr != nil {
				return observeErr
			}
			if len(remaining) != 0 {
				return errors.New("a Session occupied the Worktree during removal")
			}
			_, changeErr := l.repositories.Change(ctx, RemoveManagedWorktreeChange(freshProject, target))
			return changeErr
		})
	})
}

func (l *SessionLifecycle) adoptDiscoveredSessionsInWorktree(ctx context.Context, target string) error {
	snapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return err
	}
	state := snapshot.State()
	discovery := l.discover(ctx, &state)
	if discovery.Availability != RegistryDiscoveryAvailable {
		if discoveryErr := discovery.Err(); discoveryErr != nil {
			return fmt.Errorf("Session discovery is not reliable enough for Worktree removal: %w", discoveryErr)
		}
		return errors.New("Session discovery is not reliable enough for Worktree removal")
	}
	var relevant []Session
	for _, session := range discovery.Sessions {
		if sessionBelongsToWorktree(session, target) {
			relevant = append(relevant, session)
		}
	}
	if len(relevant) == 0 {
		return nil
	}
	_, err = l.registry.Change(ctx, addDiscoveredSessionsChange(relevant))
	return err
}

func (l *SessionLifecycle) registeredSessionsInWorktree(ctx context.Context, target string) ([]Session, error) {
	snapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	state := snapshot.State()
	sessions := make([]Session, 0)
	for _, session := range state.Agents {
		if sessionBelongsToWorktree(session, target) {
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

func (l *SessionLifecycle) Resume(ctx context.Context, id SessionID, name string) (SessionLifecycleResult, error) {
	return l.planExisting(ctx, id, name, SessionDesiredRunning)
}

// ResumeAfterRestart resumes a Session whose runtime is observed absent —
// after a reboot, a tmux server restart, or a killed runtime — in a freshly
// minted runtime in its recorded working directory, issuing the agent kind's
// resume command for the recorded conversation reference. The Session keeps
// its durable identity, name, Project association and conversation reference;
// only the runtime is new.
//
// The resume records its durable intent before any runtime is touched and is
// advanced idempotently. It is always a deliberate developer action: no
// startup or observation pass calls it. The Outbox is left untouched —
// prompt delivery is not idempotent, so queued messages wait for the
// developer instead of being replayed into the resumed agent.
func (l *SessionLifecycle) ResumeAfterRestart(ctx context.Context, id SessionID, name string) (SessionLifecycleResult, error) {
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
	if session.IsTerm() {
		return SessionLifecycleResult{}, fmt.Errorf("Session %q ist eine Terminal-Session — sie wird als Shell neu gestartet, nicht fortgesetzt", session.Name)
	}
	provider, err := resolveSessionProvider(session)
	if err != nil {
		return SessionLifecycleResult{}, err
	}
	return l.resumeAfterRestartWithProvider(ctx, id, name, provider)
}

// resumeAfterRestartWithProvider runs a resume with an explicit provider so
// tests can stub the vendor's memory of the recorded conversation.
func (l *SessionLifecycle) resumeAfterRestartWithProvider(ctx context.Context, id SessionID, name string, provider AgentProvider) (SessionLifecycleResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if provider == nil {
		return SessionLifecycleResult{}, fmt.Errorf("Session %q hat einen unbekannten Agent-Vendor", name)
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
	if session.IsTerm() {
		return SessionLifecycleResult{}, fmt.Errorf("Session %q ist eine Terminal-Session — sie wird als Shell neu gestartet, nicht fortgesetzt", session.Name)
	}
	if session.SessionRuntime() != RuntimeTmux {
		return SessionLifecycleResult{}, fmt.Errorf("Session %q hat eine verwaltete Runtime — sie wird nicht über tmux fortgesetzt", session.Name)
	}
	if behavior := provider.ResumeBehavior(); behavior == ResumeUnsupported {
		return SessionLifecycleResult{}, fmt.Errorf("%s kann keine Konversation wiederaufnehmen", provider.Tool())
	}
	project, err := lifecycleProjectForSession(state, session)
	if err != nil {
		return SessionLifecycleResult{}, err
	}
	if err := checkResumeDirectory(session, project); err != nil {
		return SessionLifecycleResult{}, err
	}
	if provider.ResumeBehavior() == ResumeByRunRef {
		run, ok := session.AgentRun(provider.Vendor())
		if !ok {
			return SessionLifecycleResult{}, fmt.Errorf("Session %q hat keine gespeicherte Konversation für %s", session.Name, provider.Tool())
		}
		if !provider.RunExists(run.ExternalID) {
			return SessionLifecycleResult{}, fmt.Errorf("Konversation %q ist bei %s nicht mehr vorhanden — starte die Session frisch in %s", run.ExternalID, provider.Tool(), ShortPath(session.Dir))
		}
	}
	advanced := LifecycleRecord{SessionID: session.ID, Session: session, Desired: SessionDesiredRunning}
	run := func() error {
		return l.withSessionTransition(ctx, session.ID, session.Name, func() error {
			if settleErr := l.settleCrossedRename(ctx, session.ID); settleErr != nil {
				return settleErr
			}
			currentSnapshot, snapshotErr := l.registry.Snapshot(ctx)
			if snapshotErr != nil {
				return snapshotErr
			}
			currentState := currentSnapshot.State()
			currentIndex := sessionIndex(&currentState, session.ID, session.Name)
			if currentIndex < 0 {
				return fmt.Errorf("Session %q not found", session.Name)
			}
			current := currentState.Agents[currentIndex]
			fresh, mintErr := l.mintResumeRuntimeName(ctx, currentState, current)
			if mintErr != nil {
				return mintErr
			}
			resumed := current
			resumed.RuntimeName = fresh
			record, recordErr := l.newResumeTransitionRecord(currentState, resumed)
			if recordErr != nil {
				return recordErr
			}
			return l.withRecordRuntimeTransition(ctx, record, func() error {
				if _, putErr := l.putRecord(ctx, record, false); putErr != nil {
					return putErr
				}
				var advanceErr error
				advanced, advanceErr = l.advanceResume(ctx, record)
				return advanceErr
			})
		})
	}
	err = l.withRunningWorktreeTransition(ctx, project, session, false, run)
	return SessionLifecycleResult{Session: advanced.Session, Record: advanced}, err
}

// checkResumeDirectory verifies the recorded working directory still exists
// and still resolves inside its Project before any resume intent is written.
// Its absence fails the resume: a stale record pointing at a missing or
// reused directory must never start an agent there.
func checkResumeDirectory(session Session, project Project) error {
	if strings.TrimSpace(session.Dir) == "" || session.Dir == "." {
		return fmt.Errorf("Arbeitsverzeichnis von Session %q ist nicht verfügbar", session.Name)
	}
	if info, err := os.Stat(session.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("Arbeitsverzeichnis %q von Session %q ist nicht verfügbar", ShortPath(session.Dir), session.Name)
	}
	if session.ProjectID != "" && !discoveredDirectoryBelongsToProject(project, session.Dir) {
		return fmt.Errorf("Verzeichnis gehört nicht zu ProjectID %q", session.ProjectID)
	}
	return nil
}

// mintResumeRuntimeName mints a fresh mgt- RuntimeName for a resume: the first
// candidate that is neither the recorded name nor registered nor observed.
// The recorded name is never reused and never addressed, so a resume cannot
// reach a runtime Magentic did not create. Probing a candidate reads the
// runtime; the durable intent is written only afterwards, mirroring how
// provisioning verifies its target before persisting.
func (l *SessionLifecycle) mintResumeRuntimeName(ctx context.Context, state State, session Session) (string, error) {
	taken := map[string]bool{session.RuntimeName: true}
	for _, registered := range state.Agents {
		taken[registered.RuntimeName] = true
	}
	base := SessionName(SanitizeName(session.Name))
	if !validRuntimeIdentity(base) {
		return "", fmt.Errorf("Session %q hat keinen gültigen RuntimeName", session.Name)
	}
	for i := 0; i < 100; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i+1)
		}
		if taken[candidate] {
			continue
		}
		probe := session
		probe.RuntimeName = candidate
		exists, err := l.runtime.Exists(ctx, probe)
		if err != nil {
			return "", fmt.Errorf("RuntimeName %q availability is unavailable: %w", candidate, err)
		}
		if exists {
			taken[candidate] = true
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("kein freier RuntimeName für Session %q", session.Name)
}

func (l *SessionLifecycle) newResumeTransitionRecord(state State, session Session) (LifecycleRecord, error) {
	record, err := l.newStateTransitionRecord(state, session, SessionDesiredRunning)
	if err != nil {
		return record, err
	}
	record.TransitionKind = LifecycleTransitionResume
	record.CreateWorktree = false
	return record, nil
}

// Rename durably coordinates the display-name change with the optional tmux
// rename. RuntimeName is changed only after the target runtime postcondition
// has been observed; an absent runtime keeps its existing opaque address.
func (l *SessionLifecycle) Rename(ctx context.Context, id SessionID, name, newName string) (SessionLifecycleResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	newName = SanitizeName(newName)
	if newName == "" {
		return SessionLifecycleResult{}, errors.New("Session name is required")
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
	advanced := LifecycleRecord{
		SessionID: session.ID, Session: session, TransitionKind: LifecycleTransitionRename,
		RenameTo: newName, RenameRuntimeTo: SessionName(newName),
	}
	err = l.withSessionTransition(ctx, session.ID, session.Name, func() error {
		if settleErr := l.settleCrossedRename(ctx, session.ID); settleErr != nil {
			return settleErr
		}
		currentSnapshot, snapshotErr := l.registry.Snapshot(ctx)
		if snapshotErr != nil {
			return snapshotErr
		}
		currentState := currentSnapshot.State()
		currentIndex := sessionIndex(&currentState, session.ID, session.Name)
		if currentIndex < 0 {
			return fmt.Errorf("Session %q not found", session.Name)
		}
		current := currentState.Agents[currentIndex]
		if current.Name == newName {
			latest, ok, readErr := l.recordForSession(ctx, current.ID)
			if readErr != nil {
				return readErr
			}
			if ok && latest.TransitionKind == LifecycleTransitionRename && latest.RenameTo == newName {
				if latest.Phase != LifecycleConverged {
					return l.withRecordRuntimeTransition(ctx, latest, func() error {
						advanced, readErr = l.advanceRename(ctx, latest)
						return readErr
					})
				}
				advanced = latest
				return nil
			}
			advanced = convergedRenameRecord(current, newName, l.now())
			return nil
		}
		if other := currentState.AgentByName(newName); other != nil && other.ID != current.ID {
			return fmt.Errorf("Session %q already exists", newName)
		}
		desired := SessionDesiredRunning
		if !current.LaterAt.IsZero() {
			desired = SessionDesiredLater
		}
		now := l.now()
		record := LifecycleRecord{
			TransitionID: NewUUID(), SessionID: current.ID,
			TransitionKind: LifecycleTransitionRename, Desired: desired,
			Phase: LifecyclePlanned, Session: current,
			RenameTo: newName, RenameRuntimeTo: SessionName(newName),
			PromptDelivery: InitialPromptNotRequested,
			Applied:        LifecycleAppliedState{WorktreeReady: true},
			CreatedAt:      now, UpdatedAt: now,
		}
		return l.withRecordRuntimeTransition(ctx, record, func() error {
			if _, putErr := l.putRecord(ctx, record, false); putErr != nil {
				return putErr
			}
			advanced, snapshotErr = l.advanceRename(ctx, record)
			return snapshotErr
		})
	})
	return SessionLifecycleResult{Session: advanced.Session, Record: advanced}, err
}

func convergedRenameRecord(session Session, name string, now time.Time) LifecycleRecord {
	desired := SessionDesiredRunning
	if !session.LaterAt.IsZero() {
		desired = SessionDesiredLater
	}
	return LifecycleRecord{
		TransitionID: NewUUID(), SessionID: session.ID,
		TransitionKind: LifecycleTransitionRename,
		Desired:        desired, Phase: LifecycleConverged,
		Session: session, RenameTo: name, RenameRuntimeTo: session.RuntimeName,
		PromptDelivery: InitialPromptNotRequested,
		Applied: LifecycleAppliedState{
			WorktreeReady: true, RuntimeRenameSettled: true, RegistryUpdated: true,
		},
		CreatedAt: now, UpdatedAt: now,
	}
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
	run := func() error {
		return l.withSessionTransition(ctx, session.ID, session.Name, func() error {
			if settleErr := l.settleCrossedRename(ctx, session.ID); settleErr != nil {
				return settleErr
			}
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
	}
	if desired == SessionDesiredRunning {
		project, projectErr := lifecycleProjectForSession(state, session)
		if projectErr != nil {
			return SessionLifecycleResult{Session: advanced.Session, Record: advanced}, projectErr
		}
		err = l.withRunningWorktreeTransition(ctx, project, session, false, run)
	} else {
		err = run()
	}
	return SessionLifecycleResult{Session: advanced.Session, Record: advanced}, err
}

// settleCrossedRename prevents a later state transition from discarding a
// rename that may already have crossed the runtime Seam. A collision rejected
// before that Seam remains safely supersedable by a newer user intent.
func (l *SessionLifecycle) settleCrossedRename(ctx context.Context, id SessionID) error {
	latest, ok, err := l.recordForSession(ctx, id)
	if err != nil || !ok || latest.TransitionKind != LifecycleTransitionRename || latest.Phase == LifecycleConverged {
		return err
	}
	if !latest.MayHaveApplied && !latest.Applied.RuntimeRenameSettled {
		return nil
	}
	err = l.withRecordRuntimeTransition(ctx, latest, func() error {
		_, advanceErr := l.advanceRename(ctx, latest)
		return advanceErr
	})
	if err != nil {
		return fmt.Errorf("finish pending Session rename: %w", err)
	}
	return nil
}

// planSessionLocked persists intent before calling either external
// Implementation. The caller must hold this Session's transition lock.
func (l *SessionLifecycle) planSessionLocked(ctx context.Context, state State, session Session, desired SessionDesiredState) (LifecycleRecord, error) {
	record, err := l.newStateTransitionRecord(state, session, desired)
	if err != nil {
		return record, err
	}
	advanced := record
	err = l.withRecordRuntimeTransition(ctx, record, func() error {
		var advanceErr error
		advanced, advanceErr = l.persistAndAdvanceStateTransition(ctx, record)
		return advanceErr
	})
	return advanced, err
}

func (l *SessionLifecycle) newStateTransitionRecord(state State, session Session, desired SessionDesiredState) (LifecycleRecord, error) {
	now := l.now()
	record := LifecycleRecord{
		TransitionID: NewUUID(), SessionID: session.ID, Desired: desired,
		Phase: LifecyclePlanned, Session: session,
		StartMode: "resume", PromptDelivery: InitialPromptNotRequested,
		Applied: LifecycleAppliedState{
			WorktreeReady: true,
			BaselineKnown: strings.TrimSpace(session.BaseCommit) != "",
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if session.ProjectID != "" {
		project := state.ProjectByID(session.ProjectID)
		if project == nil {
			return record, fmt.Errorf("ProjectID %q not found", session.ProjectID)
		}
		record.Project = *project
		record.Session.ProjectID = project.ID
		record.Session.Project = project.Name
	}
	return record, nil
}

// persistAndAdvanceStateTransition requires both the Session and RuntimeName
// transition locks. Keeping the Registry decision, durable intent, runtime
// crossing, and Registry convergence under one RuntimeName lock prevents a
// different Project from adopting the same external process.
func (l *SessionLifecycle) persistAndAdvanceStateTransition(ctx context.Context, record LifecycleRecord) (LifecycleRecord, error) {
	if _, err := l.putRecord(ctx, record, false); err != nil {
		return record, err
	}
	if record.TransitionKind == LifecycleTransitionResume {
		return l.advanceResume(ctx, record)
	}
	if record.Desired == SessionDesiredRunning {
		return l.advanceRunning(ctx, record)
	}
	return l.advanceStopped(ctx, record)
}

func (l *SessionLifecycle) withRecordRuntimeTransition(ctx context.Context, record LifecycleRecord, fn func() error) error {
	runtimeNames := []string{record.Session.RuntimeName}
	if record.TransitionKind == LifecycleTransitionRename {
		runtimeNames = append(runtimeNames, record.RenameRuntimeTo)
	}
	return l.runtimes.with(ctx, runtimeNames, fn)
}

func lifecycleProjectForSession(state State, session Session) (Project, error) {
	if session.ProjectID == "" {
		return Project{}, nil
	}
	project := state.ProjectByID(session.ProjectID)
	if project == nil {
		return Project{}, fmt.Errorf("ProjectID %q not found", session.ProjectID)
	}
	return *project, nil
}

func (l *SessionLifecycle) withRunningWorktreeTransition(
	ctx context.Context,
	project Project,
	session Session,
	createWorktree bool,
	fn func() error,
) error {
	return l.projects.with(ctx, session.ProjectID, func() error {
		freshProject, err := l.resolveProject(ctx, session.ProjectID)
		if err != nil {
			return err
		}
		if session.ProjectID == "" {
			freshProject = project
		}
		target, managed := runningWorktreeTarget(freshProject, session, createWorktree)
		if !managed {
			return fn()
		}
		return l.worktrees.with(ctx, freshProject, target, func() error {
			resolved, resolveErr := l.resolveProject(ctx, session.ProjectID)
			if resolveErr != nil {
				return resolveErr
			}
			resolvedTarget, resolvedManaged := runningWorktreeTarget(resolved, session, createWorktree)
			if !resolvedManaged || canonicalWorktreeTransitionPath(resolvedTarget) != canonicalWorktreeTransitionPath(target) {
				return errors.New("Project Worktree identity changed during Session transition")
			}
			return fn()
		})
	})
}

func runningWorktreeTarget(project Project, session Session, createWorktree bool) (string, bool) {
	if createWorktree {
		return managedWorktreeTarget(project, session.Name)
	}
	return managedWorktreeForDirectory(project, session.Dir)
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
			if advanced.TransitionKind != LifecycleTransitionRename && advanced.Desired == SessionDesiredRunning && !beforeRuntime && advanced.Applied.RuntimePresent {
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
	run := func() error {
		return l.withSessionTransition(ctx, expected.SessionID, expected.Session.Name, func() error {
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
			return l.withRecordRuntimeTransition(ctx, latest, func() error {
				var advanceErr error
				if latest.TransitionKind == LifecycleTransitionRename {
					advanced, advanceErr = l.advanceRename(ctx, latest)
				} else if latest.TransitionKind == LifecycleTransitionResume {
					advanced, advanceErr = l.advanceResume(ctx, latest)
				} else if latest.Desired == SessionDesiredRunning {
					advanced, advanceErr = l.advanceRunning(ctx, latest)
				} else {
					advanced, advanceErr = l.advanceStopped(ctx, latest)
				}
				return advanceErr
			})
		})
	}
	var err error
	if expected.TransitionKind != LifecycleTransitionRename && expected.Desired == SessionDesiredRunning {
		project, resolveErr := l.resolveProject(ctx, expected.Session.ProjectID)
		if resolveErr != nil {
			return advanced, resolveErr
		}
		err = l.withRunningWorktreeTransition(ctx, project, expected.Session, expected.CreateWorktree, run)
	} else {
		err = run()
	}
	return advanced, err
}

// reconcileRegisteredSession takes its observation and decision under the
// Session transition lock. An explicit Park or Resume that was already in
// flight therefore cannot be undone using a stale Registry snapshot.
func (l *SessionLifecycle) reconcileRegisteredSession(ctx context.Context, id SessionID, name string) (examined, restored bool, err error) {
	initialSnapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return false, false, err
	}
	initialState := initialSnapshot.State()
	initialIndex := sessionIndex(&initialState, id, name)
	if initialIndex < 0 {
		return false, false, nil
	}
	initialSession := initialState.Agents[initialIndex]
	project, err := lifecycleProjectForSession(initialState, initialSession)
	if err != nil {
		return false, false, err
	}
	run := func() error {
		return l.withSessionTransition(ctx, id, name, func() error {
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
			return l.runtimes.with(ctx, []string{session.RuntimeName}, func() error {
				// Registry identity is revalidated after waiting for RuntimeName
				// coordination so the observation and any repair share one fact.
				freshSnapshot, freshErr := l.registry.Snapshot(ctx)
				if freshErr != nil {
					return freshErr
				}
				freshState := freshSnapshot.State()
				freshIndex := sessionIndex(&freshState, id, name)
				if freshIndex < 0 {
					return nil
				}
				freshSession := freshState.Agents[freshIndex]
				if freshSession.RuntimeName != session.RuntimeName {
					return errors.New("Session RuntimeName changed during Lifecycle reconciliation")
				}
				exists, observeErr := l.runtime.Exists(ctx, freshSession)
				if observeErr != nil {
					return observeErr
				}
				desired := SessionDesiredRunning
				if !freshSession.LaterAt.IsZero() {
					desired = SessionDesiredLater
				}
				latest, hasRecord, readErr := l.recordForSession(ctx, freshSession.ID)
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
				record, recordErr := l.newStateTransitionRecord(freshState, freshSession, desired)
				if recordErr != nil {
					return recordErr
				}
				advanced, advanceErr := l.persistAndAdvanceStateTransition(ctx, record)
				if advanceErr != nil {
					return advanceErr
				}
				restored = advanced.Desired == SessionDesiredRunning && advanced.Applied.RuntimePresent
				return nil
			})
		})
	}
	err = l.withRunningWorktreeTransition(ctx, project, initialSession, false, run)
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

func (l *SessionLifecycle) advanceRename(ctx context.Context, expected LifecycleRecord) (LifecycleRecord, error) {
	record, err := l.currentRecord(ctx, expected)
	if err != nil {
		return expected, err
	}
	record.Attempts++
	record.LastError = ""
	if record.TransitionKind != LifecycleTransitionRename || strings.TrimSpace(record.RenameTo) == "" {
		return l.failRecord(ctx, record, errors.New("invalid Session rename intent"))
	}
	oldRuntime := record.Session.RuntimeName
	targetRuntime := record.RenameRuntimeTo
	if !validRuntimeIdentity(oldRuntime) || !validRuntimeIdentity(targetRuntime) {
		return l.failRecord(ctx, record, errors.New("Session rename requires exact runtime identities"))
	}

	registrySnapshot, err := l.registry.Snapshot(ctx)
	if err != nil {
		return l.failRecord(ctx, record, err)
	}
	state := registrySnapshot.State()
	idx := sessionIndex(&state, record.SessionID, record.Session.Name)
	if idx < 0 {
		return l.failRecord(ctx, record, fmt.Errorf("Session %q not found", record.Session.Name))
	}
	registered := state.Agents[idx]
	if registered.Name != record.Session.Name && registered.Name != record.RenameTo {
		return l.failRecord(ctx, record, fmt.Errorf("Session display name changed to %q during rename", registered.Name))
	}
	if registered.RuntimeName != oldRuntime && registered.RuntimeName != targetRuntime {
		return l.failRecord(ctx, record, fmt.Errorf("Session RuntimeName changed to %q during rename", registered.RuntimeName))
	}
	if other := state.AgentByName(record.RenameTo); other != nil && other.ID != record.SessionID {
		return l.failRecord(ctx, record, fmt.Errorf("Session %q already exists", record.RenameTo))
	}

	if !record.Applied.RuntimeRenameSettled {
		oldSession := record.Session
		oldSession.RuntimeName = oldRuntime
		oldExists, existsErr := l.runtime.Exists(ctx, oldSession)
		if existsErr != nil {
			return l.failRecord(ctx, record, existsErr)
		}

		targetExists := oldExists
		if targetRuntime != oldRuntime && (oldExists || record.MayHaveApplied) {
			targetSession := record.Session
			targetSession.RuntimeName = targetRuntime
			targetExists, existsErr = l.runtime.Exists(ctx, targetSession)
			if existsErr != nil {
				return l.failRecord(ctx, record, existsErr)
			}
		}

		switch {
		case targetRuntime == oldRuntime:
			record.Applied.RuntimePresent = oldExists
			record.Applied.RuntimeRenameSettled = true
		case !oldExists:
			// A prior attempt persisted MayHaveApplied before crossing the
			// runtime Seam. If its target is present, the rename is proven; if
			// both names are absent, retain the intended target because the
			// external outcome is no longer distinguishable. On the first
			// attempt, absence means there is no runtime to rename and the opaque
			// old RuntimeName must remain unchanged.
			record.Applied.RuntimePresent = record.MayHaveApplied && targetExists
			record.Applied.RuntimeRenamed = record.MayHaveApplied
			record.Applied.RuntimeRenameSettled = true
		default:
			if targetExists {
				return l.failRecord(ctx, record, fmt.Errorf("tmux target %q already exists", targetRuntime))
			}
			for _, session := range state.Agents {
				if session.ID != record.SessionID && session.RuntimeName == targetRuntime {
					return l.failRecord(ctx, record, fmt.Errorf("RuntimeName %q belongs to Session %q", targetRuntime, session.Name))
				}
			}

			// Persist ambiguity before crossing the external rename Seam. A
			// crash after tmux applies the rename is reconciled by observing the
			// old and target postconditions, never by blindly replaying it.
			record.MayHaveApplied = true
			if record, err = l.putRecord(ctx, record, true); err != nil {
				return record, err
			}
			renameErr := l.runtime.Rename(ctx, oldSession, targetRuntime)

			targetSession := record.Session
			targetSession.RuntimeName = targetRuntime
			targetExists, targetErr := l.runtime.Exists(ctx, targetSession)
			oldExists, oldErr := l.runtime.Exists(ctx, oldSession)
			if targetErr != nil || oldErr != nil {
				postconditionErr := errors.Join(targetErr, oldErr)
				if renameErr != nil {
					postconditionErr = fmt.Errorf("rename failed: %v; postcondition unknown: %w", renameErr, postconditionErr)
				}
				return l.failRecord(ctx, record, postconditionErr)
			}
			if !targetExists || oldExists {
				if renameErr == nil {
					renameErr = fmt.Errorf("tmux rename postcondition not met (old=%t target=%t)", oldExists, targetExists)
				}
				return l.failRecord(ctx, record, renameErr)
			}
			record.Applied.RuntimePresent = true
			record.Applied.RuntimeRenamed = true
			record.Applied.RuntimeRenameSettled = true
		}
		record.Phase = LifecycleRuntimeReady
		if record, err = l.putRecord(ctx, record, true); err != nil {
			return record, err
		}
	}

	registryRuntime := oldRuntime
	if record.Applied.RuntimeRenamed {
		registryRuntime = targetRuntime
	}
	changeResult, err := l.registry.Change(ctx, RenameRegisteredSessionRuntime(
		record.SessionID, registered.Name, record.RenameTo, registryRuntime,
	))
	if err != nil {
		return l.failRecord(ctx, record, err)
	}
	changedState := changeResult.Snapshot.State()
	changedIndex := sessionIndex(&changedState, record.SessionID, record.RenameTo)
	if changedIndex < 0 {
		return l.failRecord(ctx, record, fmt.Errorf("Session %q was not present after Registry rename", record.RenameTo))
	}
	changed := changedState.Agents[changedIndex]
	if changed.Name != record.RenameTo || changed.RuntimeName != registryRuntime {
		return l.failRecord(ctx, record, fmt.Errorf("Registry rename postcondition not met for Session %q", record.RenameTo))
	}
	record.Session = changed
	record.Applied.RegistryUpdated = true
	record.Phase = LifecycleConverged
	record.LastError = ""
	return l.putRecord(ctx, record, true)
}

func (l *SessionLifecycle) advanceRunning(ctx context.Context, record LifecycleRecord) (LifecycleRecord, error) {
	current, err := l.currentRecord(ctx, record)
	if err != nil {
		return record, err
	}
	record = current
	record.Attempts++
	record.LastError = ""
	if record.Session.ProjectID != "" {
		freshProject, resolveErr := l.resolveProject(ctx, record.Session.ProjectID)
		if resolveErr != nil {
			return l.failRecord(ctx, record, resolveErr)
		}
		oldTarget, oldManaged := runningWorktreeTarget(record.Project, record.Session, record.CreateWorktree)
		freshTarget, freshManaged := runningWorktreeTarget(freshProject, record.Session, record.CreateWorktree)
		if oldManaged != freshManaged || oldManaged && canonicalWorktreeTransitionPath(oldTarget) != canonicalWorktreeTransitionPath(freshTarget) {
			return l.failRecord(ctx, record, errors.New("Project Worktree identity changed during Session transition"))
		}
		record.Project = freshProject
		record.Session.ProjectID = freshProject.ID
		record.Session.Project = freshProject.Name
		if record.CreateWorktree {
			record.Session.Dir = freshTarget
		}
	}
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
		if record.Session.Worktree && (inspectErr != nil || inspection.Presence != RepositoryKnown) {
			if inspectErr != nil {
				return l.failRecord(ctx, record, fmt.Errorf("verify managed Worktree before runtime start: %w", inspectErr))
			}
			message := "managed Worktree is unavailable before runtime start"
			if inspection.Problem != nil && strings.TrimSpace(inspection.Problem.Message) != "" {
				message += ": " + inspection.Problem.Message
			}
			return l.failRecord(ctx, record, errors.New(message))
		}
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
		if !record.Session.IsTerm() && record.StartMode != "new" {
			// A vendor that assigns its own run id can only be resumed once
			// its identity was found. A failure here is not fatal: the
			// provider falls back to its own continuation form.
			if resolved, resolveErr := resolveMissingAgentRun(ctx, record.Session); resolveErr == nil {
				record.Session = resolved
			}
		}
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
		// postcondition. Commit the newly captured baseline and the reopen as one
		// semantic Registry change so a retry cannot expose or overwrite a
		// partially updated Session record.
		registryResult, err = l.registry.Change(ctx, ReopenRegisteredSessionWithBaseline(
			record.Session.ID, record.Session.Name, record.Session.BaseCommit, record.Session.BaseDirty,
		))
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

// advanceResume converges a resume-after-restart intent. It mirrors
// advanceRunning minus worktree creation: the recorded directory is used
// as-is, no initial prompt is ever replayed, and the Outbox is never touched.
// Two guards make it a resume rather than a reopen: a runtime Magentic did not
// create under the fresh name is never adopted, and a Session removed while
// the resume was in flight is never resurrected.
func (l *SessionLifecycle) advanceResume(ctx context.Context, record LifecycleRecord) (LifecycleRecord, error) {
	current, err := l.currentRecord(ctx, record)
	if err != nil {
		return record, err
	}
	record = current
	record.Attempts++
	record.LastError = ""
	if record.TransitionKind != LifecycleTransitionResume {
		return l.failRecord(ctx, record, errors.New("invalid Session resume intent"))
	}
	if record.Session.IsTerm() {
		return l.failRecord(ctx, record, fmt.Errorf("Session %q ist eine Terminal-Session", record.Session.Name))
	}
	if record.Session.ProjectID != "" {
		freshProject, resolveErr := l.resolveProject(ctx, record.Session.ProjectID)
		if resolveErr != nil {
			return l.failRecord(ctx, record, resolveErr)
		}
		oldTarget, oldManaged := runningWorktreeTarget(record.Project, record.Session, record.CreateWorktree)
		freshTarget, freshManaged := runningWorktreeTarget(freshProject, record.Session, record.CreateWorktree)
		if oldManaged != freshManaged || oldManaged && canonicalWorktreeTransitionPath(oldTarget) != canonicalWorktreeTransitionPath(freshTarget) {
			return l.failRecord(ctx, record, errors.New("Project Worktree identity changed during Session transition"))
		}
		record.Project = freshProject
		record.Session.ProjectID = freshProject.ID
		record.Session.Project = freshProject.Name
	}
	if !record.Applied.BaselineKnown {
		inspection, inspectErr := l.repositories.Inspect(ctx, RepositoryInspectRequest{
			Directory: record.Session.Dir, MainBranch: record.Project.MainBranch,
		})
		if record.Session.Worktree && (inspectErr != nil || inspection.Presence != RepositoryKnown) {
			if inspectErr != nil {
				return l.failRecord(ctx, record, fmt.Errorf("verify managed Worktree before runtime start: %w", inspectErr))
			}
			message := "managed Worktree is unavailable before runtime start"
			if inspection.Problem != nil && strings.TrimSpace(inspection.Problem.Message) != "" {
				message += ": " + inspection.Problem.Message
			}
			return l.failRecord(ctx, record, errors.New(message))
		}
		if inspectErr == nil && inspection.Baseline.Known() {
			record.Session.BaseCommit = inspection.Baseline.Value.Head
			record.Session.BaseDirty = append([]string(nil), inspection.Baseline.Value.DirtyPaths...)
			record.Applied.BaselineKnown = true
			if record, err = l.putRecord(ctx, record, true); err != nil {
				return record, err
			}
		}
	}
	if strings.TrimSpace(record.Session.Dir) == "" || record.Session.Dir == "." {
		return l.failRecord(ctx, record, fmt.Errorf("Arbeitsverzeichnis von Session %q ist nicht verfügbar", record.Session.Name))
	}
	if info, err := os.Stat(record.Session.Dir); err != nil || !info.IsDir() {
		return l.failRecord(ctx, record, fmt.Errorf("Arbeitsverzeichnis %q von Session %q ist nicht verfügbar", ShortPath(record.Session.Dir), record.Session.Name))
	}
	// The vendor may have dropped the recorded conversation after the resume
	// was classified or attempted. Fail with that reason instead of silently
	// starting fresh; the record stays intact and the fresh start remains an
	// explicit alternative.
	if provider, resolveErr := resolveSessionProvider(record.Session); resolveErr != nil {
		return l.failRecord(ctx, record, resolveErr)
	} else if provider.ResumeBehavior() == ResumeByRunRef {
		run, ok := record.Session.AgentRun(provider.Vendor())
		if !ok {
			return l.failRecord(ctx, record, fmt.Errorf("Session %q hat keine gespeicherte Konversation für %s", record.Session.Name, provider.Tool()))
		}
		if !provider.RunExists(run.ExternalID) {
			return l.failRecord(ctx, record, fmt.Errorf("Konversation %q ist bei %s nicht mehr vorhanden — starte die Session frisch in %s", run.ExternalID, provider.Tool(), ShortPath(record.Session.Dir)))
		}
	}
	exists, err := l.runtime.Exists(ctx, record.Session)
	if err != nil {
		return l.failRecord(ctx, record, err)
	}
	if exists && !record.MayHaveApplied {
		return l.failRecord(ctx, record, fmt.Errorf("RuntimeName %q ist bereits belegt", record.Session.RuntimeName))
	}
	if !exists {
		// Persist ambiguity before crossing the runtime Seam. A crash after the
		// start is reconciled by observing the fresh name's postcondition,
		// never by blindly starting a second runtime.
		record.MayHaveApplied = true
		if record, err = l.putRecord(ctx, record, true); err != nil {
			return record, err
		}
		startErr := l.runtime.Start(ctx, record.Session, record.StartMode)
		if startErr != nil {
			exists, err = l.runtime.Exists(ctx, record.Session)
			if err != nil || !exists {
				if err != nil {
					startErr = fmt.Errorf("Start fehlgeschlagen: %v; Ergebnis unbekannt: %w", startErr, err)
				}
				return l.failRecord(ctx, record, startErr)
			}
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
	if idx < 0 {
		return l.failRecord(ctx, record, fmt.Errorf("Session %q wurde entfernt", record.Session.Name))
	} else if state.Agents[idx].ID != record.Session.ID {
		return l.failRecord(ctx, record, fmt.Errorf("Session name %q belongs to another SessionID", record.Session.Name))
	}
	// Resume carries the fresh RuntimeName into the Registry together with the
	// reopen: one semantic change, so a retry cannot expose a partially
	// updated Session record.
	registryResult, err := l.registry.Change(ctx, resumeRegisteredSession(
		record.Session.ID, record.Session.Name, record.Session.RuntimeName, record.Session.BaseCommit, record.Session.BaseDirty,
	))
	if err != nil {
		return l.failRecord(ctx, record, err)
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
	if record.Desired == SessionDesiredRemoved {
		forgetPromptTargetQueue(record.Session.TmuxName())
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
	message := strings.TrimSuffix(string(output), "\n")
	if !singleLineTmuxDiagnostic(message) {
		return false
	}
	if tmuxServerKnownAbsent(message) {
		return true
	}
	detail, found := strings.CutPrefix(message, "can't find session: ")
	return found && detail != "" && strings.TrimSpace(detail) == detail
}

// tmux meldet einen fehlenden Server je nach errno verschieden: bei ECONNREFUSED
// (Socket-Datei ohne Server) "no server running on …", bei ENOENT (Socket-Datei
// weg, etwa nach einem Reboot) "error connecting to … (No such file or directory)".
func tmuxServerKnownAbsent(output string) bool {
	message := strings.TrimSuffix(output, "\n")
	if !singleLineTmuxDiagnostic(message) {
		return false
	}
	if socket, found := strings.CutPrefix(message, "no server running on "); found {
		return socket != "" && strings.TrimSpace(socket) == socket
	}
	if detail, found := strings.CutPrefix(message, "error connecting to "); found {
		socket, found := strings.CutSuffix(detail, " (No such file or directory)")
		return found && socket != "" && strings.TrimSpace(socket) == socket
	}
	return false
}

func singleLineTmuxDiagnostic(message string) bool {
	return message != "" && !strings.ContainsAny(message, "\r\n") && strings.TrimSpace(message) == message
}

func (tmuxLifecycleRuntime) Start(ctx context.Context, session Session, mode string) error {
	if info, err := os.Stat(session.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("Session directory %q is unavailable", session.Dir)
	}
	if !session.IsTerm() {
		// The binary check happens before the tmux Session exists, so a
		// missing program leaves nothing behind to clean up.
		provider, err := resolveSessionProvider(session)
		if err != nil {
			return err
		}
		if !providerBinaryAvailable(provider) {
			return fmt.Errorf("%s ist nicht installiert (%s nicht im PATH)", provider.Vendor(), provider.Binary())
		}
	}
	args := tmuxNewSessionArgs(session)
	if out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	TmuxConfigureUX()
	if session.IsTerm() {
		return nil
	}
	command, err := startCommandForSession(session, mode)
	if err != nil {
		return err
	}
	if _, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", TargetPane(session.TmuxName()), "-l", command).CombinedOutput(); err != nil {
		return fmt.Errorf("start coding agent: %w", err)
	}
	if _, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", TargetPane(session.TmuxName()), "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("submit coding-agent command: %w", err)
	}
	return nil
}

// tmuxNewSessionArgs builds the command that creates a Session runtime. Every
// provisioned runtime carries the Magentic environment marker.
func tmuxNewSessionArgs(session Session) []string {
	args := []string{"new-session", "-d", "-s", session.TmuxName(), "-c", session.Dir, "-x", "220", "-y", "50"}
	return append(args, controlEnvironmentArgs(session)...)
}

func (tmuxLifecycleRuntime) Stop(ctx context.Context, session Session) error {
	out, err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", TargetSession(session.TmuxName())).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux kill-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (tmuxLifecycleRuntime) Rename(ctx context.Context, session Session, targetRuntime string) error {
	out, err := exec.CommandContext(
		ctx, "tmux", "rename-session", "-t", TargetSession(session.TmuxName()), targetRuntime,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux rename-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (tmuxLifecycleRuntime) DeliverInitial(_ context.Context, session Session, prompt string) (bool, error) {
	if session.IsTerm() {
		return false, errors.New("initial coding prompt cannot be delivered to a terminal Session")
	}
	provider, err := resolveSessionProvider(session)
	if err != nil {
		return false, err
	}
	// enqueuePrompt confirms only in-process scheduling. The durable state
	// therefore remains delivery_unknown until a future observation can prove
	// acceptance; reconciliation intentionally does not submit it again.
	if err := enqueuePrompt(session.TmuxName(), prompt, true, provider.Tool(), true, true, false, nil); err != nil {
		return false, err
	}
	return false, nil
}
