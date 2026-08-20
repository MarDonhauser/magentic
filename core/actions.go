package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func lifecycleForState(st *State) *SessionLifecycle {
	registryPath := StatePath()
	if st != nil && st.registryPath != "" {
		registryPath = st.registryPath
	}
	ledgerPath := SessionLifecyclePath()
	if os.Getenv("MAGENTIC_LIFECYCLE") == "" && registryPath != StatePath() {
		ledgerPath = filepath.Join(filepath.Dir(registryPath), "lifecycle.json")
	}
	return OpenSessionLifecycle(SessionLifecycleConfig{RegistryPath: registryPath, LedgerPath: ledgerPath})
}

func refreshState(st *State) error {
	if st == nil {
		return fmt.Errorf("Session Registry is unavailable")
	}
	path := st.registryPath
	if path == "" {
		path = StatePath()
	}
	snapshot, err := OpenRegistry(path).Snapshot(context.Background())
	if err != nil {
		return err
	}
	*st = *snapshot.MutableState()
	return nil
}

func registerDiscovered(st *State) error {
	discovered := DiscoverNew(st)
	if len(discovered) == 0 {
		return nil
	}
	path := st.registryPath
	if path == "" {
		path = StatePath()
	}
	result, err := OpenRegistry(path).Change(context.Background(), AddDiscoveredSessions(discovered))
	if err != nil {
		return err
	}
	*st = *result.Snapshot.MutableState()
	return nil
}

func DiscoverNew(s *State) []Agent {
	known := map[string]bool{}
	for _, a := range s.Agents {
		known[a.TmuxName()] = true
	}
	var out []Agent
	for _, sess := range TmuxListSessions() {
		if known[sess] {
			continue
		}
		name := strings.TrimPrefix(sess, SessionPrefix)
		dir, _ := Tmux("display-message", "-p", "-t", TargetPane(sess), "#{pane_current_path}")
		dir = strings.TrimSpace(dir)
		createdRaw, _ := Tmux("display-message", "-p", "-t", TargetPane(sess), "#{session_created}")
		ts, _ := strconv.ParseInt(strings.TrimSpace(createdRaw), 10, 64)
		if ts == 0 {
			ts = time.Now().Unix()
		}
		proj := ""
		worktree := false
		for _, p := range s.Projects {
			if dir == p.Path || strings.HasPrefix(dir, p.Path+string(os.PathSeparator)) {
				proj = p.Name
				worktree = dir != p.Path
				break
			}
			base := p.Path + "-agents" + string(os.PathSeparator)
			if strings.HasPrefix(dir, base) {
				proj = p.Name
				worktree = true
				break
			}
		}
		out = append(out, Agent{Name: name, Project: proj, Dir: dir, Worktree: worktree, RuntimeName: sess, CreatedAt: time.Unix(ts, 0)})
	}
	return out
}

func RestoreSessions(st *State) int {
	result, err := lifecycleForState(st).Reconcile(context.Background())
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
	if _, err := lifecycleForState(st).Resume(context.Background(), a.ID, a.Name); err != nil {
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

func inspectLivePromptTarget(session, expectedTool string) (promptTargetObservation, error) {
	name := strings.TrimPrefix(session, SessionPrefix)
	observed := observePromptTarget(context.Background(), session)
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

func deliverPrompt(session, prompt string, submit bool, expectedTool string, waitForReady, tolerateStartup bool, validate promptTargetValidator, deadline time.Time) error {
	if waitForReady {
		for {
			if err := waitForPromptDeadline(deadline, time.Second, session); err != nil {
				return err
			}
			observed, err := inspectLivePromptTarget(session, expectedTool)
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
			return sendPromptLiteralValidated(session, prompt, submit, expectedTool, validate)
		}
	}
	if !time.Now().Before(deadline) {
		return promptDeadlineError(session)
	}
	return sendPromptLiteralValidated(session, prompt, submit, expectedTool, validate)
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
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("Prompt ist leer")
	}
	key := promptDeliveryKey(prompt, submit, expectedTool, waitForReady, tolerateStartup, preferSync, validate)
	q := queueForPromptTarget(session)
	deadline := time.Now().Add(promptDeliveryTimeout)
	return q.enqueue(session, key, preferSync, deadline, func(deadline time.Time) error {
		return deliverPrompt(session, prompt, submit, expectedTool, waitForReady, tolerateStartup, validate, deadline)
	})
}

func SendPromptWhenReady(session, prompt string, submit bool) {
	_ = enqueuePrompt(session, prompt, submit, AgentToolClaude, true, true, false, nil)
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

// sendPromptLiteral passes the prompt as one tmux argument. In particular, it
// must never be interpolated into a shell command: handoff metadata can contain
// paths and names that have meaning to a shell.
func sendPromptLiteral(session, prompt string, submit bool, expectedTool string) error {
	return sendPromptLiteralValidated(session, prompt, submit, expectedTool, nil)
}

func sendPromptLiteralValidated(session, prompt string, submit bool, expectedTool string, validate promptTargetValidator) error {
	observed, err := inspectLivePromptTarget(session, expectedTool)
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
	observed, err = inspectLivePromptTarget(session, expectedTool)
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

// SendPromptToSession sends immediately when the detected agent's input is
// ready. While Claude is working, it reuses the established readiness loop and
// submits the prompt once the composer is available again.
func SendPromptToSession(session, prompt string) error {
	observed, err := inspectLivePromptTarget(session, "")
	if err != nil {
		return err
	}
	return enqueuePromptForObservedTarget(session, prompt, observed)
}

func enqueuePromptForObservedTarget(session, prompt string, observed promptTargetObservation) error {
	ready := observed.Tool != AgentToolClaude || observed.Input == promptInputReady
	return enqueuePrompt(session, prompt, true, observed.Tool, !ready, false, ready, nil)
}

func SendSlashCommand(session, cmd string) {
	_ = SendPromptToSession(session, cmd)
}

func StartSkillAgent(st *State, dir, prompt, kind, nameHint string) (string, error) {
	return startSkillAgent(st, dir, prompt, kind, nameHint, "")
}

func startSkillAgent(st *State, dir, prompt, kind, nameHint string, specificationRef SpecificationRef) (string, error) {
	if err := registerDiscovered(st); err != nil {
		return "", err
	}
	name := PickAgentName(st, nameHint)
	project := Project{}
	for _, p := range st.Projects {
		managedRoot := filepath.Join(filepath.Dir(p.Path), filepath.Base(p.Path)+"-agents")
		if dir == p.Path || strings.HasPrefix(dir, p.Path+string(os.PathSeparator)) ||
			dir == managedRoot || strings.HasPrefix(dir, managedRoot+string(os.PathSeparator)) {
			project = p
			break
		}
	}
	purpose := SessionPurposeWork
	switch kind {
	case "cleanup":
		purpose = SessionPurposeCleanup
	case "merge":
		purpose = SessionPurposeMerge
	case "deploy":
		purpose = SessionPurposeDeploy
	}
	result, err := lifecycleForState(st).Provision(context.Background(), SessionProvision{
		Project: project, Name: name, Directory: dir,
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

func SendSkill(name, cmd string) error {
	st, err := LoadState()
	if err != nil {
		return err
	}
	session := st.AgentByName(name)
	if session == nil {
		return fmt.Errorf("unbekannte Session: %s", name)
	}
	return sendSkillToSession(*session, cmd)
}

// SendSkillByID resolves the action target through its durable Registry
// identity. Name-based SendSkill remains a compatibility Adapter for the TUI.
func SendSkillByID(id SessionID, cmd string) error {
	st, err := LoadState()
	if err != nil {
		return err
	}
	session := st.SessionByID(id)
	if session == nil {
		return fmt.Errorf("unbekannte SessionID: %s", id)
	}
	return sendSkillToSession(*session, cmd)
}

func sendSkillToSession(session Session, cmd string) error {
	name := session.Name
	sn := session.TmuxName()
	if session.IsTerm() {
		return fmt.Errorf("%s ist eine Terminal-Session — dort läuft kein Claude", name)
	}
	observed, err := inspectLivePromptTarget(sn, AgentToolClaude)
	if err != nil {
		return err
	}
	return enqueuePromptForObservedTarget(sn, cmd, observed)
}

func DoneAgent(name string) error {
	return SendSkill(name, "/done ")
}

func DoneSession(id SessionID) error {
	return SendSkillByID(id, "/done ")
}

func validatePromptTargetStatus(name string, status AgentStatus) error {
	switch status {
	case StatusRunning, StatusAgents, StatusShell, StatusIdle:
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
	if observed.Status == StatusUnknown && observed.Tool != AgentToolClaude {
		// The non-Claude prompt Adapters support literal queued input, but do not
		// claim UI-phase semantics that Observation cannot establish.
		return nil
	}
	return validatePromptTargetStatus(name, observed.Status)
}

func StartCleanup(st *State, path, mainBranch string) (string, error) {
	if mainBranch == "" {
		mainBranch = "main"
	}
	prompt := fmt.Sprintf("Diese Session wurde von magentic zum Aufräumen dieses Worktrees gestartet. "+
		"Sichte die uncommitteten Änderungen und die Commits auf diesem Branch, committe sinnvoll und bringe die Arbeit nach %s. "+
		"Zeige mir zuerst deinen Plan, bevor du etwas ausführst. Sag am Ende Bescheid, wenn der Worktree entfernt werden kann.", mainBranch)
	return StartSkillAgent(st, path, prompt, "cleanup", "cleanup "+filepath.Base(path))
}

func StartMerge(st *State, projPath, source, target string) (string, error) {
	prompt := fmt.Sprintf("Merge den Branch %q nach %q in diesem Repository. "+
		"Hole vorher den aktuellen Stand (git fetch). Falls Konflikte auftreten, löse sie sinnvoll und erkläre mir deine Entscheidungen. "+
		"Zeige mir zuerst deinen Plan, bevor du etwas ausführst, und frage mich, bevor du pushst.", source, target)
	return StartSkillAgent(st, projPath, prompt, "merge", "merge "+source)
}

func StartBoardSession(st *State, projPath, id, itemPath string) (string, error) {
	return StartSpecificationSession(st, SpecificationStartIntent{
		ID:                     id,
		ProjectDirectory:       projPath,
		SpecificationDirectory: itemPath,
		WorkInstructions: SpecificationWorkInstructions{
			ReadInOrder:      []SpecificationDocumentKind{SpecificationDocumentProposal, SpecificationDocumentSpecification, SpecificationDocumentDesign, SpecificationDocumentPlan, SpecificationDocumentTasks},
			KeepTasksUpdated: true,
			ReviewBeforeWork: true,
		},
	})
}

// StartSpecificationSession is the controlled handoff from Specifications to
// Session Lifecycle. Callers must obtain intent through Specifications.ResolveStart;
// the compatibility StartBoardSession Adapter above exists only for older Go
// callers and is not exposed by the desktop facade.
func StartSpecificationSession(st *State, intent SpecificationStartIntent) (string, error) {
	if strings.TrimSpace(intent.ID) == "" || strings.TrimSpace(intent.ProjectDirectory) == "" || strings.TrimSpace(intent.SpecificationDirectory) == "" {
		return "", fmt.Errorf("unvollst\u00e4ndiger Specification-Start")
	}
	prompt := specificationWorkPrompt(intent)
	return startSkillAgent(st, intent.ProjectDirectory, prompt, "", intent.ID, intent.Reference)
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

func StartDeploy(st *State, projPath string) (string, error) {
	return StartSkillAgent(st, projPath, "/deploy ", "deploy", "deploy "+filepath.Base(projPath))
}

func RemoveWorktree(st *State, proj *Project, path string) error {
	ctx := context.Background()
	repositories := NewRepositories()
	survey, err := repositories.Survey(ctx, []Project{*proj})
	if err != nil {
		return err
	}
	if len(survey.Projects) != 1 || !survey.Projects[0].Worktrees.Known() {
		return fmt.Errorf("Worktree-Status ist derzeit nicht verlässlich verfügbar")
	}
	var target *RepositoryWorktree
	for i := range survey.Projects[0].Worktrees.Value {
		worktree := &survey.Projects[0].Worktrees.Value[i]
		if sameRepositoryPath(worktree.Path, path) {
			target = worktree
			break
		}
	}
	if target == nil {
		return fmt.Errorf("Pfad gehört nicht zu diesem Projekt")
	}
	if target.Main {
		return fmt.Errorf("Haupt-Worktree kann nicht entfernt werden")
	}
	if !target.Changes.Known() {
		return fmt.Errorf("Worktree-Änderungen sind derzeit nicht verlässlich verfügbar")
	}
	if !target.Changes.Value.Clean() {
		return fmt.Errorf("Worktree hat uncommittete Änderungen — erst aufräumen")
	}
	if err := registerDiscovered(st); err != nil {
		return err
	}
	var onPath []Agent
	for _, a := range st.Agents {
		if sameRepositoryPath(a.Dir, path) {
			onPath = append(onPath, a)
		}
	}
	observations := Observe(ctx, onPath)
	if err := validateWorktreeRemovalObservations(onPath, observations); err != nil {
		return err
	}
	lifecycle := lifecycleForState(st)
	for _, a := range onPath {
		if _, err := lifecycle.Remove(ctx, a.ID, a.Name); err != nil {
			return err
		}
	}
	if _, err := repositories.Change(ctx, RemoveManagedWorktreeChange(*proj, path)); err != nil {
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

func CreateAgentSession(st *State, projName string, worktree bool, name string) (string, error) {
	return createSession(st, projName, worktree, name, "")
}

func CreateTermSession(st *State, projName string, worktree bool, name string) (string, error) {
	return createSession(st, projName, worktree, name, KindTerm)
}

func CreateDockSession(st *State, projName string) (string, error) {
	return createSession(st, projName, false, "", KindDock)
}

func CreateTermSessionFor(st *State, agentName, name string) (string, error) {
	a := st.AgentByName(agentName)
	if a == nil {
		return "", fmt.Errorf("Session %q nicht gefunden", agentName)
	}
	return createTermSessionFor(st, *a, name)
}

func CreateTermSessionForID(st *State, sessionID SessionID, name string) (string, error) {
	a := st.SessionByID(sessionID)
	if a == nil {
		return "", fmt.Errorf("SessionID %q nicht gefunden", sessionID)
	}
	return createTermSessionFor(st, *a, name)
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
	return startSession(st, name, a.Dir, a.Project, a.Worktree, KindTerm)
}

func pickSessionName(st *State, name, hint, kind string) (string, error) {
	if name == "" {
		if kind == KindTerm || kind == KindDock {
			hint = "term " + hint
		}
		name = PickAgentName(st, hint)
	} else {
		name = SanitizeName(name)
	}
	if name == "" || st.HasAgent(name) || TmuxHasSession(SessionName(name)) {
		return "", fmt.Errorf("Name %q ist ungültig oder schon vergeben", name)
	}
	return name, nil
}

func createSession(st *State, projName string, worktree bool, name, kind string) (string, error) {
	proj := st.ProjectByName(projName)
	if proj == nil {
		return "", fmt.Errorf("Projekt nicht gefunden")
	}
	hint := projName
	if !worktree {
		hint = SessionNameHint(proj.Path, projName)
	}
	name, err := pickSessionName(st, name, hint, kind)
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
	result, err := lifecycleForState(st).Provision(context.Background(), SessionProvision{
		Project: *proj, Name: name, Directory: proj.Path,
		CreateWorktree: worktree, Worktree: worktree,
		Kind: sessionKind, Presentation: presentation, Purpose: SessionPurposeWork,
	})
	if err != nil {
		return "", err
	}
	if err := refreshState(st); err != nil {
		return "", err
	}
	return result.Session.Name, nil
}

func startSession(st *State, name, dir, project string, worktree bool, kind string) (string, error) {
	proj := Project{Name: project}
	if registered := st.ProjectByName(project); registered != nil {
		proj = *registered
	}
	sessionKind := SessionKindCodingAgent
	presentation := SessionPresentationListed
	if kind == KindTerm || kind == KindDock {
		sessionKind = SessionKindTerminal
	}
	if kind == KindDock {
		presentation = SessionPresentationDock
	}
	result, err := lifecycleForState(st).Provision(context.Background(), SessionProvision{
		Project: proj, Name: name, Directory: dir, Worktree: worktree,
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
	if _, err := lifecycleForState(st).Park(context.Background(), session.ID, session.Name); err != nil {
		return err
	}
	return refreshState(st)
}

func RemoveRegisteredSession(st *State, name string) error {
	session := st.AgentByName(name)
	if session == nil {
		return nil
	}
	if _, err := lifecycleForState(st).Remove(context.Background(), session.ID, session.Name); err != nil {
		return err
	}
	return refreshState(st)
}
