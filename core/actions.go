package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

func defaultSessionLifecycle() *SessionLifecycle {
	return OpenSessionLifecycle(SessionLifecycleConfig{})
}

func refreshState(st *State) error {
	if st == nil {
		return fmt.Errorf("Session Registry is unavailable")
	}
	snapshot, err := OpenRegistry(StatePath()).Snapshot(context.Background())
	if err != nil {
		return err
	}
	*st = snapshot.State()
	return nil
}

func registerDiscovered(st *State) error {
	return registerDiscoveredWithRuntime(context.Background(), st, tmuxRegistryDiscoveryRuntime{})
}

type RegistryDiscoveryAvailability string

const (
	RegistryDiscoveryAvailable   RegistryDiscoveryAvailability = "available"
	RegistryDiscoveryPartial     RegistryDiscoveryAvailability = "partial"
	RegistryDiscoveryUnavailable RegistryDiscoveryAvailability = "unavailable"
)

type RegistryDiscoveryProblem struct {
	RuntimeName string `json:"runtimeName,omitempty"`
	Operation   string `json:"operation"`
	Message     string `json:"message"`
}

// RegistryDiscovery is evidence for adopting external runtimes into the
// Registry. Sessions contains only complete facts; Problems retains every
// runtime that could not be identified without inventing a directory or
// creation time.
type RegistryDiscovery struct {
	ObservedAt   time.Time                     `json:"observedAt"`
	Availability RegistryDiscoveryAvailability `json:"availability"`
	Sessions     []Session                     `json:"sessions"`
	Problems     []RegistryDiscoveryProblem    `json:"problems,omitempty"`
}

func (d RegistryDiscovery) Err() error {
	if len(d.Problems) == 0 {
		return nil
	}
	problems := make([]string, 0, len(d.Problems))
	for _, problem := range d.Problems {
		target := ""
		if problem.RuntimeName != "" {
			target = " " + strconv.Quote(problem.RuntimeName)
		}
		problems = append(problems, problem.Operation+target+": "+problem.Message)
	}
	return errors.New(strings.Join(problems, "; "))
}

// registryRuntimeSessionFact is raw Adapter evidence. It intentionally keeps
// the timestamp textual: DiscoverNew must validate all fields before a fact
// crosses the Registry Seam and becomes a durable Session.
type registryRuntimeSessionFact struct {
	RuntimeName string
	Directory   string
	CreatedUnix string
}

type registryDiscoveryRuntime interface {
	ListSessions(context.Context) ([]string, error)
	InspectSession(context.Context, string) (registryRuntimeSessionFact, error)
}

type tmuxRegistryDiscoveryRuntime struct{}

func (tmuxRegistryDiscoveryRuntime) ListSessions(ctx context.Context) ([]string, error) {
	out, err := runRegistryDiscoveryTmux(ctx, "list-sessions", "-F", "#{session_name}")
	if errors.Is(err, errTmuxServerAbsent) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseRegistryDiscoverySessionList(out)
}

func (tmuxRegistryDiscoveryRuntime) InspectSession(ctx context.Context, runtimeName string) (registryRuntimeSessionFact, error) {
	fact := registryRuntimeSessionFact{RuntimeName: runtimeName}
	directory, err := runRegistryDiscoveryTmux(
		ctx, "display-message", "-p", "-t", TargetPane(runtimeName), "#{pane_current_path}",
	)
	if err != nil {
		return fact, fmt.Errorf("read pane directory: %w", err)
	}
	fact.Directory, err = parseRegistryDiscoveryScalar(directory)
	if err != nil {
		return fact, fmt.Errorf("parse pane directory: %w", err)
	}
	created, err := runRegistryDiscoveryTmux(
		ctx, "display-message", "-p", "-t", TargetPane(runtimeName), "#{session_created}",
	)
	if err != nil {
		return fact, fmt.Errorf("read creation time: %w", err)
	}
	fact.CreatedUnix, err = parseRegistryDiscoveryScalar(created)
	if err != nil {
		return fact, fmt.Errorf("parse creation time: %w", err)
	}
	return fact, nil
}

func parseRegistryDiscoverySessionList(output string) ([]string, error) {
	if output == "" {
		return nil, errors.New("successful tmux response is empty")
	}
	if !strings.HasSuffix(output, "\n") {
		return nil, errors.New("successful tmux response is unterminated")
	}
	rows := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	for _, row := range rows {
		if !validRegistryDiscoveryScalar(row) {
			return nil, errors.New("successful tmux response contains a malformed session name")
		}
	}
	return rows, nil
}

func parseRegistryDiscoveryScalar(output string) (string, error) {
	if output == "" {
		return "", errors.New("successful tmux response is empty")
	}
	if !strings.HasSuffix(output, "\n") {
		return "", errors.New("successful tmux response is unterminated")
	}
	value := strings.TrimSuffix(output, "\n")
	if !validRegistryDiscoveryScalar(value) {
		return "", errors.New("successful tmux response contains a malformed scalar")
	}
	return value, nil
}

func validRegistryDiscoveryScalar(value string) bool {
	return value != "" && strings.TrimSpace(value) == value &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func runRegistryDiscoveryTmux(ctx context.Context, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if tmuxServerKnownAbsent(string(out)) {
			return "", fmt.Errorf("%w: %s", errTmuxServerAbsent, message)
		}
		if message != "" {
			return "", fmt.Errorf("%w: %s", err, message)
		}
		return "", err
	}
	return string(out), nil
}

var errTmuxServerAbsent = errors.New("tmux server is not running")

// DiscoverNew observes externally-created tmux runtimes without mutating the
// Registry. Failed or malformed probes remain explicitly unknown and never
// turn into durable Sessions.
func DiscoverNew(ctx context.Context, s *State) RegistryDiscovery {
	return discoverNewWithRuntime(ctx, s, tmuxRegistryDiscoveryRuntime{})
}

func discoverNewWithRuntime(ctx context.Context, s *State, runtime registryDiscoveryRuntime) RegistryDiscovery {
	discovery := RegistryDiscovery{
		ObservedAt:   time.Now().UTC(),
		Availability: RegistryDiscoveryAvailable,
	}
	if s == nil {
		discovery.Availability = RegistryDiscoveryUnavailable
		discovery.Problems = append(discovery.Problems, RegistryDiscoveryProblem{
			Operation: "read-registry", Message: "Registry snapshot is unavailable",
		})
		return discovery
	}
	if ctx == nil {
		ctx = context.Background()
	}
	known := map[string]bool{}
	for _, a := range s.Agents {
		known[a.TmuxName()] = true
	}
	listed, err := runtime.ListSessions(ctx)
	if err != nil {
		discovery.Availability = RegistryDiscoveryUnavailable
		discovery.Problems = append(discovery.Problems, RegistryDiscoveryProblem{
			Operation: "list-sessions", Message: err.Error(),
		})
		return discovery
	}
	seen := make(map[string]bool, len(listed))
	for _, runtimeName := range listed {
		if !strings.HasPrefix(runtimeName, SessionPrefix) {
			continue
		}
		if !validRegistryDiscoveryScalar(runtimeName) {
			discovery.Problems = append(discovery.Problems, RegistryDiscoveryProblem{
				RuntimeName: runtimeName, Operation: "parse-list-sessions", Message: "runtime name is malformed",
			})
			continue
		}
		if seen[runtimeName] {
			discovery.Problems = append(discovery.Problems, RegistryDiscoveryProblem{
				RuntimeName: runtimeName, Operation: "parse-list-sessions", Message: "duplicate runtime name",
			})
			continue
		}
		seen[runtimeName] = true
		if known[runtimeName] {
			continue
		}
		name := strings.TrimPrefix(runtimeName, SessionPrefix)
		if strings.TrimSpace(name) == "" {
			discovery.Problems = append(discovery.Problems, RegistryDiscoveryProblem{
				RuntimeName: runtimeName, Operation: "parse-session-name", Message: "display name is empty",
			})
			continue
		}
		if registered := s.AgentByName(name); registered != nil {
			discovery.Problems = append(discovery.Problems, RegistryDiscoveryProblem{
				RuntimeName: runtimeName, Operation: "resolve-session-identity",
				Message: fmt.Sprintf("display name conflicts with registered RuntimeName %q", registered.TmuxName()),
			})
			continue
		}
		fact, err := runtime.InspectSession(ctx, runtimeName)
		if err != nil {
			discovery.Problems = append(discovery.Problems, RegistryDiscoveryProblem{
				RuntimeName: runtimeName, Operation: "inspect-session", Message: err.Error(),
			})
			continue
		}
		if fact.RuntimeName != runtimeName {
			discovery.Problems = append(discovery.Problems, RegistryDiscoveryProblem{
				RuntimeName: runtimeName, Operation: "validate-session-fact", Message: "runtime identity changed during inspection",
			})
			continue
		}
		directory := filepath.Clean(fact.Directory)
		if !validRegistryDiscoveryScalar(fact.Directory) || !filepath.IsAbs(directory) || directory != fact.Directory {
			discovery.Problems = append(discovery.Problems, RegistryDiscoveryProblem{
				RuntimeName: runtimeName, Operation: "validate-pane-directory", Message: "pane directory is not an exact clean absolute path",
			})
			continue
		}
		createdUnix, err := parseRegistryDiscoveryUnixTime(fact.CreatedUnix)
		if err != nil || createdUnix <= 0 {
			message := "creation time is not a positive Unix timestamp"
			if err != nil {
				message += ": " + err.Error()
			}
			discovery.Problems = append(discovery.Problems, RegistryDiscoveryProblem{
				RuntimeName: runtimeName, Operation: "parse-session-created", Message: message,
			})
			continue
		}
		project, worktree := discoveredSessionProject(s.Projects, directory)
		discovery.Sessions = append(discovery.Sessions, Session{
			Name: name, ProjectID: project.ID, Project: project.Name, Dir: directory, Worktree: worktree,
			RuntimeName: runtimeName, CreatedAt: time.Unix(createdUnix, 0),
		})
	}
	if len(discovery.Problems) > 0 {
		discovery.Availability = RegistryDiscoveryPartial
	}
	return discovery
}

func parseRegistryDiscoveryUnixTime(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("timestamp is empty")
	}
	for _, digit := range []byte(value) {
		if digit < '0' || digit > '9' {
			return 0, errors.New("timestamp must contain decimal digits only")
		}
	}
	return strconv.ParseInt(value, 10, 64)
}

func discoveredSessionProject(projects []Project, directory string) (Project, bool) {
	var matched Project
	worktree := false
	matchLength := -1
	for _, project := range projects {
		projectPath := filepath.Clean(project.Path)
		if directory == projectPath || strings.HasPrefix(directory, projectPath+string(os.PathSeparator)) {
			if len(projectPath) > matchLength {
				matched = project
				worktree = directory != projectPath
				matchLength = len(projectPath)
			}
		}
		managedRoot := filepath.Clean(project.Path + "-agents")
		if strings.HasPrefix(directory, managedRoot+string(os.PathSeparator)) && len(managedRoot) > matchLength {
			matched = project
			worktree = true
			matchLength = len(managedRoot)
		}
	}
	return matched, worktree
}

func discoveredDirectoryBelongsToProject(project Project, directory string) bool {
	if project.ID == "" || strings.TrimSpace(project.Path) == "" || strings.TrimSpace(directory) == "" {
		return false
	}
	projectRoot := canonicalWorktreeTransitionPath(project.Path)
	directory = canonicalWorktreeTransitionPath(directory)
	relative, err := filepath.Rel(projectRoot, directory)
	if err == nil && !filepath.IsAbs(relative) && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	_, managed := managedWorktreeForDirectory(project, directory)
	return managed
}

func registerDiscoveredWithRuntime(ctx context.Context, st *State, runtime registryDiscoveryRuntime) error {
	discovery := discoverNewWithRuntime(ctx, st, runtime)
	if err := discovery.Err(); err != nil {
		return err
	}
	if len(discovery.Sessions) > 0 {
		result, err := OpenRegistry(StatePath()).AdoptDiscoveredSessions(ctx, discovery.Sessions)
		if err != nil {
			return err
		}
		*st = result.Snapshot.State()
	}
	return nil
}

func RestoreSessions(st *State) int {
	result, err := defaultSessionLifecycle().Reconcile(context.Background())
	if err != nil {
		Logf("Session Lifecycle reconcile: %v", err)
		return 0
	}
	for _, problem := range result.Problems {
		Logf("restore %s: %s", problem.Name, problem.Message)
	}
	if err := refreshState(st); err != nil {
		Logf("Session Lifecycle Registry refresh: %v", err)
	}
	return result.Restored
}

func ReopenLater(st *State, name string) error {
	a := st.AgentByName(name)
	if a == nil {
		return fmt.Errorf("unbekannte Session: %s", name)
	}
	if _, err := defaultSessionLifecycle().Resume(context.Background(), a.ID, a.Name); err != nil {
		return err
	}
	return refreshState(st)
}

type promptTargetQueue struct {
	sendSlot  chan struct{}
	pendingMu sync.Mutex
	pending   map[string]*pendingPrompt
}

var promptTargetQueues sync.Map

type promptTargetValidator func(promptTargetObservation) error

type pendingPrompt struct {
	done chan struct{}
	err  error
}

var (
	errPromptDeliveryPending  = errors.New("identische Prompt-Zustellung läuft bereits")
	errPromptDeliveryDeadline = errors.New("Zeitlimit für Prompt-Zustellung überschritten")
)

const promptDeliveryTimeout = 180 * time.Second

func newPromptTargetQueue() *promptTargetQueue {
	return &promptTargetQueue{
		sendSlot: make(chan struct{}, 1),
		pending:  map[string]*pendingPrompt{},
	}
}

func queueForPromptTarget(session string) *promptTargetQueue {
	q, _ := promptTargetQueues.LoadOrStore(session, newPromptTargetQueue())
	return q.(*promptTargetQueue)
}

func (q *promptTargetQueue) begin(key string) (*pendingPrompt, bool) {
	q.pendingMu.Lock()
	defer q.pendingMu.Unlock()
	if pending, exists := q.pending[key]; exists {
		return pending, false
	}
	pending := &pendingPrompt{done: make(chan struct{})}
	q.pending[key] = pending
	return pending, true
}

func (q *promptTargetQueue) finish(key string, pending *pendingPrompt, err error) {
	q.pendingMu.Lock()
	pending.err = err
	close(pending.done)
	if q.pending[key] == pending {
		delete(q.pending, key)
	}
	q.pendingMu.Unlock()
}

func promptDeadlineError(session string) error {
	return fmt.Errorf("%w: Ziel-Session %q", errPromptDeliveryDeadline, strings.TrimPrefix(session, SessionPrefix))
}

func waitForPromptDeadline(deadline time.Time, delay time.Duration, session string) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return promptDeadlineError(session)
	}
	if delay < remaining {
		remaining = delay
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	<-timer.C
	if !time.Now().Before(deadline) {
		return promptDeadlineError(session)
	}
	return nil
}

func (q *promptTargetQueue) acquire(deadline time.Time, session string) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return promptDeadlineError(session)
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case q.sendSlot <- struct{}{}:
		if !time.Now().Before(deadline) {
			<-q.sendSlot
			return promptDeadlineError(session)
		}
		return nil
	case <-timer.C:
		return promptDeadlineError(session)
	}
}

func (q *promptTargetQueue) waitForPending(pending *pendingPrompt, deadline time.Time, session string) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return promptDeadlineError(session)
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-pending.done:
		return pending.err
	case <-timer.C:
		return promptDeadlineError(session)
	}
}

func (q *promptTargetQueue) enqueue(session, key string, synchronous bool, deadline time.Time, deliver func(time.Time) error) error {
	pending, owner := q.begin(key)
	if !owner {
		if synchronous {
			return q.waitForPending(pending, deadline, session)
		}
		return fmt.Errorf("%w für Ziel-Session %q", errPromptDeliveryPending, strings.TrimPrefix(session, SessionPrefix))
	}
	run := func() (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				q.finish(key, pending, fmt.Errorf("Prompt-Zustellung abgebrochen: %v", recovered))
				panic(recovered)
			}
			q.finish(key, pending, err)
		}()
		if err = q.acquire(deadline, session); err != nil {
			return err
		}
		defer func() { <-q.sendSlot }()
		return deliver(deadline)
	}
	if synchronous {
		return run()
	}
	go func() {
		if err := run(); err != nil {
			Logf("Prompt-Queue %s: %v", session, err)
		}
	}()
	return nil
}

func inspectLivePromptTargetUsing(session, expectedTool string, observe observationReader) (promptTargetObservation, error) {
	name := strings.TrimPrefix(session, SessionPrefix)
	observed := observePromptTarget(context.Background(), session, observe)
	if err := validatePromptTargetObservation(name, observed); err != nil {
		return promptTargetObservation{}, err
	}
	if expectedTool != "" && observed.Tool != expectedTool {
		return promptTargetObservation{}, fmt.Errorf(
			"KI-Tool in Ziel-Session %q wechselte von %s zu %s", name, expectedTool, observed.Tool,
		)
	}
	return observed, nil
}

func deliverPrompt(session, prompt string, submit bool, expectedTool string, waitForReady, tolerateStartup bool, validate promptTargetValidator, deadline time.Time, observe observationReader) error {
	if waitForReady {
		for {
			if err := waitForPromptDeadline(deadline, time.Second, session); err != nil {
				return err
			}
			observed, err := inspectLivePromptTargetUsing(session, expectedTool, observe)
			if err != nil {
				if tolerateStartup {
					continue
				}
				return err
			}
			if observed.Input != promptInputReady {
				continue
			}
			if validate != nil {
				if err := validate(observed); err != nil {
					continue
				}
			}
			if err := waitForPromptDeadline(deadline, 500*time.Millisecond, session); err != nil {
				return err
			}
			return sendPromptLiteralValidated(session, prompt, submit, expectedTool, validate, observe)
		}
	}
	if !time.Now().Before(deadline) {
		return promptDeadlineError(session)
	}
	return sendPromptLiteralValidated(session, prompt, submit, expectedTool, validate, observe)
}

func promptDeliveryKey(prompt string, submit bool, expectedTool string, waitForReady, tolerateStartup, preferSync bool, validate promptTargetValidator) string {
	validationPolicy := "unvalidated"
	if validate != nil {
		// Handoff is currently the sole caller with a validator. Keep its strict
		// delivery contract out of generic/lifecycle deduplication.
		validationPolicy = "handoff-validated"
	}
	return strings.Join([]string{
		expectedTool,
		strconv.FormatBool(submit),
		strconv.FormatBool(waitForReady),
		strconv.FormatBool(tolerateStartup),
		strconv.FormatBool(preferSync),
		validationPolicy,
		prompt,
	}, "\x00")
}

func enqueuePrompt(session, prompt string, submit bool, expectedTool string, waitForReady, tolerateStartup, preferSync bool, validate promptTargetValidator) error {
	return enqueuePromptUsing(session, prompt, submit, expectedTool, waitForReady, tolerateStartup, preferSync, validate, nil)
}

func enqueuePromptUsing(session, prompt string, submit bool, expectedTool string, waitForReady, tolerateStartup, preferSync bool, validate promptTargetValidator, observe observationReader) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("Prompt ist leer")
	}
	key := promptDeliveryKey(prompt, submit, expectedTool, waitForReady, tolerateStartup, preferSync, validate)
	q := queueForPromptTarget(session)
	deadline := time.Now().Add(promptDeliveryTimeout)
	return q.enqueue(session, key, preferSync, deadline, func(deadline time.Time) error {
		return deliverPrompt(session, prompt, submit, expectedTool, waitForReady, tolerateStartup, validate, deadline, observe)
	})
}

func promptTerminalInput(prompt string) string {
	if !strings.ContainsAny(prompt, "\r\n") {
		return prompt
	}
	// A raw newline is an Enter key to terminal UIs and would submit only the
	// first paragraph. Claude, Codex, Gemini and Copilot understand bracketed
	// paste, which keeps a multi-line handoff together until the final Enter.
	normalized := strings.ReplaceAll(prompt, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\n", "\r")
	return "\x1b[200~" + normalized + "\x1b[201~"
}

// sendPromptLiteralValidated passes the prompt as one tmux argument. In
// particular, it must never be interpolated into a shell command: handoff
// metadata can contain paths and names that have meaning to a shell.
func sendPromptLiteralValidated(session, prompt string, submit bool, expectedTool string, validate promptTargetValidator, observe observationReader) error {
	observed, err := inspectLivePromptTargetUsing(session, expectedTool, observe)
	if err != nil {
		return err
	}
	if validate != nil {
		if err := validate(observed); err != nil {
			return err
		}
	}
	if _, err := Tmux("send-keys", "-t", TargetPane(session), "-l", promptTerminalInput(prompt)); err != nil {
		return fmt.Errorf("Prompt an tmux senden: %w", err)
	}
	if !submit {
		return nil
	}
	observed, err = inspectLivePromptTargetUsing(session, expectedTool, observe)
	if err != nil {
		return err
	}
	if validate != nil {
		if err := validate(observed); err != nil {
			return err
		}
	}
	if _, err := Tmux("send-keys", "-t", TargetPane(session), "Enter"); err != nil {
		return fmt.Errorf("Prompt in tmux absenden: %w", err)
	}
	return nil
}

func StartSkillAgent(st *State, projectID ProjectID, dir, prompt, kind, nameHint string) (string, error) {
	return startSkillAgent(st, projectID, dir, prompt, kind, nameHint, "")
}

func startSkillAgent(st *State, projectID ProjectID, dir, prompt, kind, nameHint string, specificationRef SpecificationRef) (string, error) {
	if st == nil {
		return "", errors.New("Session Registry is unavailable")
	}
	current, err := LoadState()
	if err != nil {
		return "", err
	}
	if projectID != "" && current.ProjectByID(projectID) == nil {
		return "", fmt.Errorf("ProjectID %q nicht gefunden", projectID)
	}
	*st = *current
	if err := registerDiscovered(st); err != nil {
		return "", err
	}
	current, err = LoadState()
	if err != nil {
		return "", err
	}
	*st = *current
	project := Project{}
	if projectID != "" {
		resolved := st.ProjectByID(projectID)
		if resolved == nil {
			return "", fmt.Errorf("ProjectID %q nicht gefunden", projectID)
		}
		project = *resolved
		if strings.TrimSpace(dir) == "" {
			dir = project.Path
		}
		if !discoveredDirectoryBelongsToProject(project, filepath.Clean(dir)) {
			return "", fmt.Errorf("Verzeichnis gehört nicht zu ProjectID %q", projectID)
		}
	}
	name := registrySessionNameCandidate(st, nameHint)
	purpose := SessionPurposeWork
	switch kind {
	case "cleanup":
		purpose = SessionPurposeCleanup
	case "merge":
		purpose = SessionPurposeMerge
	case "deploy":
		purpose = SessionPurposeDeploy
	}
	result, err := defaultSessionLifecycle().Provision(context.Background(), SessionProvision{
		ProjectID: project.ID, Name: name, Directory: dir,
		Worktree: project.Path != "" && filepath.Clean(dir) != filepath.Clean(project.Path),
		Kind:     SessionKindCodingAgent, Purpose: purpose, SpecificationRef: specificationRef, InitialPrompt: prompt,
	})
	if err != nil {
		return "", err
	}
	if err := refreshState(st); err != nil {
		return "", err
	}
	name = result.Session.Name
	return name, nil
}

// SwitchSessionVendor changes which coding agent a Session runs.
func SwitchSessionVendor(sessionID SessionID, vendor string) error {
	_, err := defaultSessionLifecycle().SwitchVendor(context.Background(), sessionID, AgentVendor(strings.TrimSpace(vendor)))
	return err
}

// SendSkillByID resolves the action target through its durable Registry
// identity.
func SendSkillByID(id SessionID, cmd string) error {
	return SendSkillByIDWithObserver(id, cmd, nil)
}

// SendSkillByIDWithObserver keeps a caller's coherent Observation Adapter in
// use through every delivery-time revalidation. A nil Observer uses the
// production Observation Module.
func SendSkillByIDWithObserver(id SessionID, cmd string, observe func(context.Context, []Session) ObservationSnapshot) error {
	st, err := LoadState()
	if err != nil {
		return err
	}
	session := st.SessionByID(id)
	if session == nil {
		return fmt.Errorf("unbekannte SessionID: %s", id)
	}
	return sendSkillToSession(*session, cmd, observe)
}

// sendSkillToSession queues the skill durably. A busy or blocked Session keeps
// the message in its Outbox instead of failing the action; the Outbox
// dispatcher delivers it as soon as the Session is input-ready again.
func sendSkillToSession(session Session, cmd string, observe observationReader) error {
	if session.IsTerm() {
		return fmt.Errorf("%s ist eine Terminal-Session — dort läuft kein Coding-Agent", session.Name)
	}
	return SendQueuedMessageWithObserver(session.ID, QueuedMessageKindSkill, cmd, observe)
}

func DoneSession(id SessionID) error {
	return SendSkillByID(id, "/done ")
}

func validatePromptTargetStatus(name string, status AgentStatus) error {
	switch status {
	case StatusRunning, StatusAgents, StatusShell, StatusIdle, StatusDone:
		return nil
	case StatusBlocked:
		return fmt.Errorf("Ziel-Session %q wartet auf eine Antwort — erst den offenen Dialog beantworten", name)
	case StatusExited:
		return fmt.Errorf("KI in Ziel-Session %q ist beendet", name)
	case StatusDead:
		return fmt.Errorf("Ziel-Session %q läuft nicht mehr", name)
	default:
		return fmt.Errorf("Ziel-Session %q ist nicht als laufende KI-Session verfügbar", name)
	}
}

func validatePromptTargetObservation(name string, observed promptTargetObservation) error {
	if observed.Availability != ObservationAvailable {
		return fmt.Errorf("Observation der Ziel-Session %q ist nicht vollständig verfügbar", name)
	}
	if observed.Presence != SessionPresencePresent {
		if observed.Presence == SessionPresenceAbsent {
			return fmt.Errorf("Ziel-Session %q läuft nicht mehr", name)
		}
		return fmt.Errorf("Laufzeit-Präsenz der Ziel-Session %q ist unbekannt", name)
	}
	if !observed.ContentKnown {
		return fmt.Errorf("Terminalinhalt der Ziel-Session %q ist nicht bekannt", name)
	}
	switch observed.Tool {
	case AgentToolClaude, AgentToolCodex, AgentToolGemini, AgentToolCopilot:
	default:
		return fmt.Errorf("in Ziel-Session %q läuft kein unterstütztes KI-Tool mehr", name)
	}
	// Unknown is fail-closed: a kind whose screens were never recorded is just
	// as unproven as an unfamiliar screen, and nothing is typed into either.
	return validatePromptTargetStatus(name, observed.Status)
}

func StartCleanup(st *State, projectID ProjectID, path, mainBranch string) (string, error) {
	if mainBranch == "" {
		mainBranch = "main"
	}
	prompt := fmt.Sprintf("Diese Session wurde von magentic zum Aufräumen dieses Worktrees gestartet. "+
		"Sichte die uncommitteten Änderungen und die Commits auf diesem Branch, committe sinnvoll und bringe die Arbeit nach %s. "+
		"Zeige mir zuerst deinen Plan, bevor du etwas ausführst. Sag am Ende Bescheid, wenn der Worktree entfernt werden kann.", mainBranch)
	return StartSkillAgent(st, projectID, path, prompt, "cleanup", "cleanup "+filepath.Base(path))
}

func StartMerge(st *State, projectID ProjectID, projPath, source, target string) (string, error) {
	prompt := fmt.Sprintf("Merge den Branch %q nach %q in diesem Repository. "+
		"Hole vorher den aktuellen Stand (git fetch). Falls Konflikte auftreten, löse sie sinnvoll und erkläre mir deine Entscheidungen. "+
		"Zeige mir zuerst deinen Plan, bevor du etwas ausführst, und frage mich, bevor du pushst.", source, target)
	return StartSkillAgent(st, projectID, projPath, prompt, "merge", "merge "+source)
}

// StartSpecificationSession is the controlled handoff from Specifications to
// Session Lifecycle. Callers must obtain intent through
// Specifications.ResolveStart.
func StartSpecificationSession(st *State, intent SpecificationStartIntent) (string, error) {
	if intent.ProjectID == "" || strings.TrimSpace(intent.ID) == "" || strings.TrimSpace(intent.ProjectDirectory) == "" || strings.TrimSpace(intent.SpecificationDirectory) == "" {
		return "", fmt.Errorf("unvollst\u00e4ndiger Specification-Start")
	}
	prompt := specificationWorkPrompt(intent)
	return startSkillAgent(st, intent.ProjectID, intent.ProjectDirectory, prompt, "", intent.ID, intent.Reference)
}

func specificationWorkPrompt(intent SpecificationStartIntent) string {
	documents := make([]string, 0, len(intent.WorkInstructions.ReadInOrder))
	for _, document := range intent.WorkInstructions.ReadInOrder {
		if label := specificationDocumentPromptLabel(document); label != "" {
			documents = append(documents, label)
		}
	}
	if len(documents) == 0 {
		documents = []string{"die vorhandenen Spezifikationsdokumente", "tasks.md"}
	}

	steps := []string{
		fmt.Sprintf("Arbeite am Change %q.", intent.ID),
		fmt.Sprintf("Die Spezifikation liegt unter %q.", intent.SpecificationDirectory),
		"Lies in dieser Reihenfolge: " + strings.Join(documents, ", ") + ".",
	}
	if intent.WorkInstructions.ReviewBeforeWork {
		steps = append(steps, "Zeige mir zuerst deinen Plan, bevor du etwas ausf\u00fchrst.")
	}
	steps = append(steps, "Arbeite danach die offenen Aufgaben ab.")
	if intent.WorkInstructions.KeepTasksUpdated {
		steps = append(steps, "Halte den Aufgabenstatus in der Spezifikation w\u00e4hrend der Arbeit aktuell.")
	}
	if intent.WorkInstructions.ArchiveAfterWork {
		steps = append(steps, "Schlage nach abgeschlossener Abnahme die Archivierung nach den Regeln des Spec-Systems vor.")
	}
	return strings.Join(steps, " ")
}

func specificationDocumentPromptLabel(document SpecificationDocumentKind) string {
	switch document {
	case SpecificationDocumentProposal:
		return "proposal"
	case SpecificationDocumentRequirements:
		return "requirements"
	case SpecificationDocumentSpecification:
		return "spec"
	case SpecificationDocumentDesign:
		return "design"
	case SpecificationDocumentPlan:
		return "plan"
	case SpecificationDocumentTasks:
		return "tasks"
	case SpecificationDocumentShape:
		return "shape"
	case SpecificationDocumentStandards:
		return "standards"
	case SpecificationDocumentReferences:
		return "references"
	case SpecificationDocumentOverview:
		return "overview"
	case SpecificationDocumentSupporting:
		return "supporting documents"
	default:
		return ""
	}
}

func StartDeploy(st *State, projectID ProjectID, projPath string) (string, error) {
	return StartSkillAgent(st, projectID, projPath, "/deploy ", "deploy", "deploy "+filepath.Base(projPath))
}

func RemoveWorktree(st *State, proj *Project, path string) error {
	if proj == nil || proj.ID == "" {
		return fmt.Errorf("ProjectID ist für die Worktree-Entfernung erforderlich")
	}
	if err := defaultSessionLifecycle().RemoveManagedWorktree(context.Background(), proj.ID, path); err != nil {
		return err
	}
	return refreshState(st)
}

func validateWorktreeRemovalObservations(sessions []Agent, snapshot ObservationSnapshot) error {
	byID := make(map[SessionID]SessionObservation, len(snapshot.Sessions))
	for _, observation := range snapshot.Sessions {
		byID[observation.SessionID] = observation
	}
	for _, session := range sessions {
		observation, found := byID[session.ID]
		if !found || observation.Availability != ObservationAvailable {
			return fmt.Errorf("Session %q kann derzeit nicht verlässlich geprüft werden", session.Name)
		}
		switch observation.Presence {
		case SessionPresenceAbsent:
			continue
		case SessionPresencePresent:
			if observation.Status == StatusIdle || observation.Status == StatusExited {
				continue
			}
			if observation.Status == StatusUnknown {
				return fmt.Errorf("Status von Session %q ist unbekannt", session.Name)
			}
			return fmt.Errorf("Agent %q arbeitet gerade in diesem Worktree", session.Name)
		default:
			return fmt.Errorf("Session %q kann derzeit nicht verlässlich geprüft werden", session.Name)
		}
	}
	return nil
}

func CreateAgentSession(st *State, projectID ProjectID, worktree bool, name string) (string, error) {
	return CreateAgentSessionWithVendor(st, projectID, worktree, name, "")
}

// CreateAgentSessionWithVendor starts a coding Session with an explicitly
// chosen coding agent. An empty vendor means Claude.
func CreateAgentSessionWithVendor(st *State, projectID ProjectID, worktree bool, name, vendor string) (string, error) {
	return createSession(st, projectID, worktree, name, "", AgentVendor(strings.TrimSpace(vendor)))
}

func CreateTermSession(st *State, projectID ProjectID, worktree bool, name string) (string, error) {
	return createSession(st, projectID, worktree, name, KindTerm, "")
}

func CreateDockSession(st *State, projectID ProjectID) (string, error) {
	return createSession(st, projectID, false, "", KindDock, "")
}

func CreateTermSessionForID(st *State, sessionID SessionID, name string) (string, error) {
	current, err := LoadState()
	if err != nil {
		return "", err
	}
	a := current.SessionByID(sessionID)
	if a == nil {
		return "", fmt.Errorf("SessionID %q nicht gefunden", sessionID)
	}
	created, err := createTermSessionFor(current, *a, name)
	if err != nil {
		return "", err
	}
	if st != nil {
		*st = *current
	}
	return created, nil
}

func createTermSessionFor(st *State, a Session, name string) (string, error) {
	hint := a.Project
	if hint == "" || a.Worktree {
		hint = filepath.Base(a.Dir)
	}
	name, err := pickSessionName(st, name, SessionNameHint(a.Dir, hint), KindTerm)
	if err != nil {
		return "", err
	}
	return startSession(st, name, a.Dir, a.ProjectID, a.Worktree, KindTerm)
}

func pickSessionName(st *State, name, hint, kind string) (string, error) {
	if name == "" {
		if kind == KindTerm || kind == KindDock {
			hint = "term " + hint
		}
		name = registrySessionNameCandidate(st, hint)
	} else {
		name = SanitizeName(name)
	}
	if name == "" {
		return "", fmt.Errorf("Name %q ist ungültig", name)
	}
	return name, nil
}

func createSession(st *State, projectID ProjectID, worktree bool, name, kind string, vendor AgentVendor) (string, error) {
	current, err := LoadState()
	if err != nil {
		return "", err
	}
	proj := current.ProjectByID(projectID)
	if proj == nil {
		return "", fmt.Errorf("ProjectID %q nicht gefunden", projectID)
	}
	if st != nil {
		*st = *current
	}
	hint := proj.Name
	if !worktree {
		hint = SessionNameHint(proj.Path, proj.Name)
	}
	name, err = pickSessionName(current, name, hint, kind)
	if err != nil {
		return "", err
	}
	sessionKind := SessionKindCodingAgent
	presentation := SessionPresentationListed
	if kind == KindTerm || kind == KindDock {
		sessionKind = SessionKindTerminal
	}
	if kind == KindDock {
		presentation = SessionPresentationDock
	}
	result, err := defaultSessionLifecycle().Provision(context.Background(), SessionProvision{
		ProjectID: proj.ID, Name: name, Directory: proj.Path,
		CreateWorktree: worktree, Worktree: worktree,
		Kind: sessionKind, Presentation: presentation, Purpose: SessionPurposeWork,
		Vendor: vendor,
	})
	if err != nil {
		return "", err
	}
	if err := refreshState(st); err != nil {
		return "", err
	}
	return result.Session.Name, nil
}

func startSession(st *State, name, dir string, projectID ProjectID, worktree bool, kind string) (string, error) {
	sessionKind := SessionKindCodingAgent
	presentation := SessionPresentationListed
	if kind == KindTerm || kind == KindDock {
		sessionKind = SessionKindTerminal
	}
	if kind == KindDock {
		presentation = SessionPresentationDock
	}
	result, err := defaultSessionLifecycle().Provision(context.Background(), SessionProvision{
		ProjectID: projectID, Name: name, Directory: dir, Worktree: worktree,
		Kind: sessionKind, Presentation: presentation,
	})
	if err != nil {
		return "", err
	}
	if err := refreshState(st); err != nil {
		return "", err
	}
	return result.Session.Name, nil
}

func ParkSession(st *State, name string) error {
	session := st.AgentByName(name)
	if session == nil {
		return fmt.Errorf("unbekannte Session: %s", name)
	}
	if _, err := defaultSessionLifecycle().Park(context.Background(), session.ID, session.Name); err != nil {
		return err
	}
	return refreshState(st)
}

func RemoveRegisteredSession(st *State, name string) error {
	session := st.AgentByName(name)
	if session == nil {
		return nil
	}
	if _, err := defaultSessionLifecycle().Remove(context.Background(), session.ID, session.Name); err != nil {
		return err
	}
	return refreshState(st)
}
