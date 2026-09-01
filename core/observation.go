package core

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type ObservationAvailability string

const (
	ObservationAvailable   ObservationAvailability = "available"
	ObservationPartial     ObservationAvailability = "partial"
	ObservationUnavailable ObservationAvailability = "unavailable"
)

type SessionPresence string

const (
	SessionPresenceUnknown SessionPresence = "unknown"
	SessionPresencePresent SessionPresence = "present"
	SessionPresenceAbsent  SessionPresence = "absent"
)

type AttentionState string

const (
	AttentionUnknown    AttentionState = "unknown"
	AttentionNone       AttentionState = "none"
	AttentionWorking    AttentionState = "working"
	AttentionNeedsInput AttentionState = "needs-input"
	AttentionReview     AttentionState = "review"
)

// StatusSource names what produced a Session's status, so a consumer can tell
// a confirmed reading from an inferred one without re-deriving it.
type StatusSource string

const (
	StatusSourceNone     StatusSource = "none"
	StatusSourcePresence StatusSource = "presence"
	StatusSourceSnapshot StatusSource = "snapshot"
	StatusSourceHook     StatusSource = "hook"
)

type OccupancyState string

const (
	OccupancyUnknown  OccupancyState = "unknown"
	OccupancyOccupied OccupancyState = "occupied"
	OccupancyVacant   OccupancyState = "vacant"
)

// SessionObservation is one coherent reading of a Session's external runtime.
// Observation never mutates the Registry or queries Git.
type SessionObservation struct {
	SessionID     SessionID               `json:"sessionId"`
	Availability  ObservationAvailability `json:"availability"`
	Presence      SessionPresence         `json:"presence"`
	Status        AgentStatus             `json:"status"`
	StatusSource  StatusSource            `json:"statusSource"`
	Content       string                  `json:"content,omitempty"`
	ContentKnown  bool                    `json:"contentKnown"`
	Activity      time.Time               `json:"activity,omitzero"`
	ActivityKnown bool                    `json:"activityKnown"`
	Tool          string                  `json:"tool,omitempty"`
	Detail        string                  `json:"detail,omitempty"`
	Attention     AttentionState          `json:"attention"`
	Unread        bool                    `json:"unread"`
	WorktreePath  string                  `json:"worktreePath,omitempty"`
	Worktree      bool                    `json:"worktree"`
	Occupancy     OccupancyState          `json:"occupancy"`
}

type ObservationProblem struct {
	SessionID   SessionID `json:"sessionId,omitempty"`
	RuntimeName string    `json:"runtimeName,omitempty"`
	Operation   string    `json:"operation"`
	Message     string    `json:"message"`
	TimedOut    bool      `json:"timedOut,omitempty"`
}

type ObservationSnapshot struct {
	ObservedAt   time.Time               `json:"observedAt"`
	Availability ObservationAvailability `json:"availability"`
	Sessions     []SessionObservation    `json:"sessions"`
	Problems     []ObservationProblem    `json:"problems,omitempty"`
}

// promptInputState is the provider-aware input fact consumed by prompt
// delivery. Raw pane commands and content stay inside Observation; actions only
// decide from this projection and its explicit knowledge gates.
type promptInputState string

const (
	promptInputUnknown       promptInputState = "unknown"
	promptInputReady         promptInputState = "ready"
	promptInputBusy          promptInputState = "busy"
	promptInputNeedsResponse promptInputState = "needs-response"
	promptInputClosed        promptInputState = "closed"
)

type promptTargetObservation struct {
	SessionID    SessionID
	Availability ObservationAvailability
	Presence     SessionPresence
	Tool         string
	Status       AgentStatus
	ContentKnown bool
	Input        promptInputState
}

type observationReader func(context.Context, []Session) ObservationSnapshot

// observePromptTarget adapts a runtime-only prompt transport target to one
// fresh, targeted Observation. The scoped SessionID exists only to preserve
// Observation's stable join semantics for this single probe. The reader is an
// explicit seam so a caller's coherent Observation Adapter is also used for
// delivery-time revalidation.
func observePromptTarget(ctx context.Context, runtimeName string, observe observationReader) promptTargetObservation {
	if observe == nil {
		observe = Observe
	}
	target := Session{
		ID:          SessionID("prompt-target:" + runtimeName),
		Name:        strings.TrimPrefix(runtimeName, SessionPrefix),
		RuntimeName: runtimeName,
	}
	return promptTargetObservationFromSnapshot(target, observe(ctx, []Session{target}))
}

func promptTargetObservationFromSnapshot(target Session, snapshot ObservationSnapshot) promptTargetObservation {
	for _, observed := range snapshot.Sessions {
		if observed.SessionID == target.ID {
			return promptTargetObservationFromSession(observed)
		}
	}
	return promptTargetObservation{
		SessionID: target.ID, Availability: ObservationUnavailable,
		Presence: SessionPresenceUnknown, Status: StatusUnknown, Input: promptInputUnknown,
	}
}

func promptTargetObservationFromSession(observed SessionObservation) promptTargetObservation {
	return promptTargetObservation{
		SessionID: observed.SessionID, Availability: observed.Availability,
		Presence: observed.Presence, Tool: observed.Tool, Status: observed.Status,
		ContentKnown: observed.ContentKnown, Input: promptInputStateFromObservation(observed),
	}
}

func promptInputStateFromObservation(observed SessionObservation) promptInputState {
	if observed.Availability != ObservationAvailable ||
		observed.Presence != SessionPresencePresent || !observed.ContentKnown {
		return promptInputUnknown
	}
	// Only a kind whose composer markers were actually recorded may claim
	// readiness. An unfamiliar tool stays unknown rather than borrowing another
	// kind's composer markers.
	kind, known := agentKindForPaneCommand(observed.Tool)
	if !known {
		return promptInputUnknown
	}
	switch observed.Status {
	case StatusIdle, StatusDone:
		if agentKindComposerReady(kind, observed.Content) {
			return promptInputReady
		}
		return promptInputUnknown
	case StatusRunning, StatusAgents, StatusShell:
		return promptInputBusy
	case StatusBlocked:
		return promptInputNeedsResponse
	case StatusExited, StatusDead:
		return promptInputClosed
	default:
		return promptInputUnknown
	}
}

// Im Login-Sturm nach dem Boot (Autostart + ~20 frisch restaurierte claude-
// Prozesse) braucht tmux teils Sekunden pro Antwort; knappere Timeouts ließen
// minutenlang jede Beobachtung scheitern und die Sidebar leer erscheinen.
const (
	defaultObservationCycleTimeout = 6 * time.Second
	defaultObservationProbeTimeout = 2 * time.Second
	observationScrollbackLines     = 200
)

// Observe reads tmux once for Session presence and once per present pane for
// content. A tmux failure is represented in the returned snapshot rather than
// making every Session look absent or dead.
func Observe(ctx context.Context, sessions []Session) ObservationSnapshot {
	return observeWithRunner(ctx, sessions, execObservationRunner{}, observationConfig{})
}

type observationRunner interface {
	Run(context.Context, ...string) (string, error)
}

type execObservationRunner struct{}

func (execObservationRunner) Run(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	return string(out), err
}

type observationConfig struct {
	cycleTimeout time.Duration
	probeTimeout time.Duration
	now          func() time.Time
	reports      *HookReportStore
}

func (c observationConfig) normalized() observationConfig {
	if c.cycleTimeout <= 0 {
		c.cycleTimeout = defaultObservationCycleTimeout
	}
	if c.probeTimeout <= 0 {
		c.probeTimeout = defaultObservationProbeTimeout
	}
	if c.probeTimeout > c.cycleTimeout {
		c.probeTimeout = c.cycleTimeout
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.reports == nil {
		c.reports = defaultHookReports
	}
	return c
}

type observedPane struct {
	id            string
	command       string
	activity      time.Time
	activityKnown bool
	selected      bool
	partial       bool
}

type captureResult struct {
	index    int
	content  string
	err      error
	timedOut bool
}

func observeWithRunner(ctx context.Context, sessions []Session, runner observationRunner, config observationConfig) ObservationSnapshot {
	config = config.normalized()
	if ctx == nil {
		ctx = context.Background()
	}
	cycleCtx, cancel := context.WithTimeout(ctx, config.cycleTimeout)
	defer cancel()

	snapshot := ObservationSnapshot{
		Availability: ObservationAvailable,
		Sessions:     make([]SessionObservation, len(sessions)),
	}
	if len(sessions) == 0 {
		snapshot.ObservedAt = config.now().UTC()
		return snapshot
	}
	config.reports.DrainHookReportFile(HookReportPath(), sessions)

	valid := make([]bool, len(sessions))
	runtimeNames := make([]string, len(sessions))
	for i, session := range sessions {
		snapshot.Sessions[i] = SessionObservation{
			SessionID:    session.ID,
			Availability: ObservationUnavailable,
			Presence:     SessionPresenceUnknown,
			Status:       StatusUnknown,
			StatusSource: StatusSourceNone,
			Attention:    AttentionUnknown,
			WorktreePath: session.Dir,
			Worktree:     session.Worktree,
			Occupancy:    OccupancyUnknown,
		}
		runtimeNames[i] = session.RuntimeName
	}
	idCounts := make(map[SessionID]int, len(sessions))
	runtimeCounts := make(map[string]int, len(sessions))
	for i, session := range sessions {
		if session.ID != "" {
			idCounts[session.ID]++
		}
		if validRuntimeIdentity(runtimeNames[i]) {
			runtimeCounts[runtimeNames[i]]++
		}
	}
	validCount := 0
	for i, session := range sessions {
		switch {
		case session.ID == "":
			snapshot.Problems = append(snapshot.Problems, ObservationProblem{
				RuntimeName: runtimeNames[i], Operation: "validate-session", Message: "SessionID is required",
			})
		case runtimeNames[i] == "":
			snapshot.Problems = append(snapshot.Problems, ObservationProblem{
				SessionID: session.ID, Operation: "validate-session", Message: "RuntimeName is required",
			})
		case !validRuntimeIdentity(runtimeNames[i]):
			snapshot.Problems = append(snapshot.Problems, ObservationProblem{
				SessionID: session.ID, RuntimeName: runtimeNames[i],
				Operation: "validate-session", Message: "RuntimeName is malformed",
			})
		case idCounts[session.ID] > 1:
			snapshot.Problems = append(snapshot.Problems, ObservationProblem{
				SessionID: session.ID, RuntimeName: runtimeNames[i],
				Operation: "validate-session", Message: "duplicate SessionID",
			})
		case runtimeCounts[runtimeNames[i]] > 1:
			snapshot.Problems = append(snapshot.Problems, ObservationProblem{
				SessionID: session.ID, RuntimeName: runtimeNames[i],
				Operation: "validate-session", Message: "duplicate RuntimeName",
			})
		default:
			valid[i] = true
			validCount++
		}
	}
	if validCount == 0 {
		snapshot.Availability = ObservationUnavailable
		snapshot.ObservedAt = config.now().UTC()
		sortObservationProblems(snapshot.Problems)
		return snapshot
	}

	listArgs := []string{
		"list-panes", "-a", "-F",
		"#{session_name}\t#{pane_id}\t#{pane_current_command}\t#{window_activity}\t#{window_active}\t#{pane_active}",
	}
	listed, err, timedOut := runObservationCommand(cycleCtx, runner, config.probeTimeout, listArgs...)
	if err != nil {
		snapshot.Availability = ObservationUnavailable
		snapshot.Problems = append(snapshot.Problems, ObservationProblem{
			Operation: "list-panes", Message: observationErrorMessage(err), TimedOut: timedOut,
		})
		snapshot.ObservedAt = config.now().UTC()
		sortObservationProblems(snapshot.Problems)
		return snapshot
	}

	panes, parseProblems, presenceComplete := parseObservedPanes(listed)
	snapshot.Problems = append(snapshot.Problems, parseProblems...)
	jobs := make([]struct {
		index int
		pane  observedPane
	}, 0, validCount)
	for i, session := range sessions {
		if !valid[i] {
			continue
		}
		observed := &snapshot.Sessions[i]
		pane, present := panes[runtimeNames[i]]
		if !present {
			if !presenceComplete {
				observed.Availability = ObservationPartial
				continue
			}
			observed.Availability = ObservationAvailable
			observed.Presence = SessionPresenceAbsent
			observed.Status = StatusDead
			observed.StatusSource = StatusSourcePresence
			observed.Attention = AttentionNone
			observed.Occupancy = OccupancyVacant
			continue
		}

		observed.Availability = ObservationAvailable
		if pane.partial || !presenceComplete {
			observed.Availability = ObservationPartial
		}
		observed.Presence = SessionPresencePresent
		observed.Occupancy = OccupancyOccupied
		if !pane.selected {
			continue
		}
		observed.Tool = observedSessionTool(session, pane.command)
		observed.Activity = pane.activity
		observed.ActivityKnown = pane.activityKnown
		jobs = append(jobs, struct {
			index int
			pane  observedPane
		}{index: i, pane: pane})
	}

	results := make(chan captureResult, len(jobs))
	for _, job := range jobs {
		job := job
		go func() {
			args := []string{
				"capture-pane", "-p", "-t", job.pane.id,
				"-S", fmt.Sprintf("-%d", observationScrollbackLines),
			}
			content, captureErr, captureTimedOut := runObservationCommand(
				cycleCtx, runner, config.probeTimeout, args...,
			)
			results <- captureResult{
				index: job.index, content: content, err: captureErr, timedOut: captureTimedOut,
			}
		}()
	}

	for range jobs {
		result := <-results
		session := sessions[result.index]
		pane := panes[runtimeNames[result.index]]
		observed := &snapshot.Sessions[result.index]
		if result.err != nil {
			observed.Availability = ObservationPartial
			resolved := resolveSessionStatus(statusInput{
				session: session, present: true, paneCommand: pane.command,
				activity: observed.Activity, activityKnown: observed.ActivityKnown,
				now: config.now(), reports: config.reports,
			})
			observed.Status = resolved.Status
			observed.StatusSource = resolved.Source
			observed.Detail = resolved.Detail
			observed.Attention = observationAttention(observed.Status)
			observed.Unread = observationUnread(
				observed.Status, session.SeenAt, observed.Activity, observed.ActivityKnown,
			)
			snapshot.Problems = append(snapshot.Problems, ObservationProblem{
				SessionID: session.ID, RuntimeName: runtimeNames[result.index],
				Operation: "capture-pane", Message: observationErrorMessage(result.err), TimedOut: result.timedOut,
			})
			continue
		}

		observed.Content = normalizeObservedContent(result.content)
		observed.ContentKnown = true
		resolved := resolveSessionStatus(statusInput{
			session: session, present: true, paneCommand: pane.command,
			content: observed.Content, contentKnown: true,
			activity: observed.Activity, activityKnown: observed.ActivityKnown,
			now: config.now(), reports: config.reports,
		})
		observed.Status = resolved.Status
		observed.StatusSource = resolved.Source
		observed.Detail = resolved.Detail
		observed.Attention = observationAttention(observed.Status)
		observed.Unread = observationUnread(
			observed.Status, session.SeenAt, observed.Activity, observed.ActivityKnown,
		)
	}

	for _, observed := range snapshot.Sessions {
		if observed.Availability != ObservationAvailable {
			snapshot.Availability = ObservationPartial
			break
		}
	}
	if len(parseProblems) > 0 {
		snapshot.Availability = ObservationPartial
	}
	snapshot.ObservedAt = config.now().UTC()
	sortObservationProblems(snapshot.Problems)
	return snapshot
}

func runObservationCommand(ctx context.Context, runner observationRunner, timeout time.Duration, args ...string) (string, error, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := runner.Run(probeCtx, args...)
	if err == nil {
		return out, nil, false
	}
	timedOut := errors.Is(probeCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded)
	return out, err, timedOut
}

func parseObservedPanes(output string) (map[string]observedPane, []ObservationProblem, bool) {
	panes := map[string]observedPane{}
	ambiguousSelections := map[string]bool{}
	var problems []ObservationProblem
	presenceComplete := true
	if output == "" || !strings.HasSuffix(output, "\n") {
		return panes, []ObservationProblem{{
			Operation: "parse-list-panes", Message: "empty or unterminated output",
		}}, false
	}
	rows := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(rows) == 0 || len(rows) == 1 && rows[0] == "" {
		return panes, []ObservationProblem{{
			Operation: "parse-list-panes", Message: "empty output",
		}}, false
	}
	for lineNo, line := range rows {
		parts := strings.SplitN(line, "\t", 6)
		runtimeName := ""
		if len(parts) > 0 {
			runtimeName = parts[0]
		}
		if len(parts) != 6 || !validRuntimeIdentity(runtimeName) || !validObservedCommand(parts[2]) || !validObservedPaneID(parts[1]) ||
			!validObservedBinaryFact(parts[4]) || !validObservedBinaryFact(parts[5]) {
			// An unidentifiable row may belong to any registered RuntimeName. The
			// remaining parsed rows still prove presence, but their absence cannot
			// prove that a Session is gone.
			presenceComplete = false
			problems = append(problems, ObservationProblem{
				RuntimeName: runtimeName, Operation: "parse-list-panes",
				Message: fmt.Sprintf("malformed row %d", lineNo+1),
			})
			continue
		}
		pane := observedPane{
			id: parts[1], command: parts[2],
			selected: parts[4] == "1" && parts[5] == "1",
		}
		if stamp, valid := parseObservedPositiveDecimal(parts[3]); valid {
			pane.activity = time.Unix(stamp, 0).UTC()
			pane.activityKnown = true
		} else {
			pane.partial = true
			problems = append(problems, ObservationProblem{
				RuntimeName: parts[0], Operation: "parse-list-panes",
				Message: fmt.Sprintf("invalid activity in row %d", lineNo+1),
			})
		}
		current, exists := panes[runtimeName]
		switch {
		case !exists:
			panes[runtimeName] = pane
		case pane.selected && ambiguousSelections[runtimeName]:
			// Keep the Session known-present but refuse to select any pane.
		case pane.selected && !current.selected:
			panes[runtimeName] = pane
		case pane.selected && current.selected:
			presenceComplete = false
			ambiguousSelections[runtimeName] = true
			current.selected = false
			current.partial = true
			panes[runtimeName] = current
			problems = append(problems, ObservationProblem{
				RuntimeName: runtimeName, Operation: "parse-list-panes",
				Message: fmt.Sprintf("multiple active panes for Session in row %d", lineNo+1),
			})
		}
	}
	for runtimeName, pane := range panes {
		if pane.selected {
			continue
		}
		presenceComplete = false
		pane.partial = true
		panes[runtimeName] = pane
		problems = append(problems, ObservationProblem{
			RuntimeName: runtimeName, Operation: "parse-list-panes",
			Message: "Session has no unambiguous active pane",
		})
	}
	return panes, problems, presenceComplete
}

func validObservedBinaryFact(value string) bool {
	return value == "0" || value == "1"
}

func parseObservedPositiveDecimal(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil && parsed > 0
}

func validObservedPaneID(id string) bool {
	if len(id) < 2 || id[0] != '%' {
		return false
	}
	for _, character := range id[1:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validObservedCommand(command string) bool {
	if command == "" || strings.TrimSpace(command) != command {
		return false
	}
	for _, character := range command {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func observedSessionTool(session Session, paneCommand string) string {
	tool := DetectAgentTool(paneCommand, false)
	if tool == "" && session.IsTerm() {
		return AgentToolBash
	}
	return tool
}

// statusInput is one Session's evidence for status resolution: what the runtime
// proves about presence, what the vendor reported about itself, and what its
// pane currently shows.
type statusInput struct {
	session       Session
	present       bool
	paneCommand   string
	content       string
	contentKnown  bool
	activity      time.Time
	activityKnown bool
	now           time.Time
	reports       *HookReportStore
}

// statusOutcome is the resolved status together with the source that produced
// it, so a consumer can tell a confirmed reading from an inferred one.
type statusOutcome struct {
	Status AgentStatus
	Source StatusSource
	Detail string
}

// resolveSessionStatus applies the one precedence there is: runtime presence
// first, then a fresh hook report, then manifest inference over the pane
// snapshot, then unknown. Nothing that cannot be proven becomes idle or done.
func resolveSessionStatus(in statusInput) statusOutcome {
	tool := observedSessionTool(in.session, in.paneCommand)
	if !in.present {
		return statusOutcome{Status: StatusDead, Source: StatusSourcePresence}
	}
	if in.session.IsTerm() && tool == AgentToolBash {
		return statusOutcome{Status: StatusTerm, Source: StatusSourcePresence}
	}
	if shellCommands[normalizedPaneCommand(in.paneCommand)] {
		return statusOutcome{Status: StatusExited, Source: StatusSourcePresence}
	}
	if record, fresh := in.reports.fresh(in.session.ID, in.now); fresh {
		return statusOutcome{Status: record.status, Source: StatusSourceHook, Detail: record.detail}
	}
	if !in.contentKnown {
		return statusOutcome{Status: StatusUnknown, Source: StatusSourceNone}
	}
	kind, known := agentKindForPaneCommand(tool)
	if !known {
		return statusOutcome{Status: StatusUnknown, Source: StatusSourceNone}
	}
	evaluated := evaluateAgentKind(kind, in.content)
	if !evaluated.Matched {
		return statusOutcome{Status: StatusUnknown, Source: StatusSourceNone}
	}
	status := evaluated.Status
	// Most kinds have no distinguishable finished screen. A resting screen the
	// developer has not looked at since the agent last moved is what "done"
	// means for them; the signal is the one Unread already uses.
	if status == StatusIdle && len(kind.done) == 0 &&
		in.activityKnown && in.activity.After(in.session.SeenAt) {
		status = StatusDone
	}
	return statusOutcome{Status: status, Source: StatusSourceSnapshot, Detail: evaluated.Detail}
}

// InferStatusFromPane resolves what one agent kind's manifest can prove about a
// pane, without the presence and hook facts a full Observation carries.
func InferStatusFromPane(paneCommand, content string) (AgentStatus, string) {
	kind, known := agentKindForPaneCommand(paneCommand)
	if !known {
		return StatusUnknown, ""
	}
	evaluated := evaluateAgentKind(kind, content)
	if !evaluated.Matched {
		return StatusUnknown, ""
	}
	return evaluated.Status, evaluated.Detail
}

func normalizedPaneCommand(command string) string {
	return strings.ToLower(strings.TrimSpace(command))
}

func observationAttention(status AgentStatus) AttentionState {
	switch status {
	case StatusRunning, StatusAgents, StatusShell:
		return AttentionWorking
	case StatusBlocked:
		return AttentionNeedsInput
	case StatusDone, StatusIdle, StatusExited:
		return AttentionReview
	case StatusDead, StatusTerm:
		return AttentionNone
	default:
		return AttentionUnknown
	}
}

func observationUnread(status AgentStatus, seenAt, activity time.Time, activityKnown bool) bool {
	if !activityKnown {
		return false
	}
	switch status {
	case StatusDone, StatusIdle, StatusBlocked, StatusExited:
		return activity.After(seenAt)
	default:
		return false
	}
}

func normalizeObservedContent(content string) string {
	content = stripObservedANSI(content)
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\t':
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, content)
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func stripObservedANSI(content string) string {
	var normalized strings.Builder
	normalized.Grow(len(content))
	for i := 0; i < len(content); {
		if content[i] != 0x1b {
			normalized.WriteByte(content[i])
			i++
			continue
		}
		i++
		if i >= len(content) {
			break
		}
		switch content[i] {
		case '[': // Control Sequence Introducer, including colors and cursor movement.
			i++
			for i < len(content) {
				final := content[i]
				i++
				if final >= 0x40 && final <= 0x7e {
					break
				}
			}
		case ']': // Operating System Command, terminated by BEL or String Terminator.
			i++
			for i < len(content) {
				if content[i] == '\a' {
					i++
					break
				}
				if content[i] == 0x1b && i+1 < len(content) && content[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default:
			// Other escape sequences are two bytes long for the terminal controls
			// tmux capture output can contain.
			i++
		}
	}
	return normalized.String()
}

func observationErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return err.Error()
}

func sortObservationProblems(problems []ObservationProblem) {
	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].SessionID != problems[j].SessionID {
			return problems[i].SessionID < problems[j].SessionID
		}
		if problems[i].Operation != problems[j].Operation {
			return problems[i].Operation < problems[j].Operation
		}
		if problems[i].RuntimeName != problems[j].RuntimeName {
			return problems[i].RuntimeName < problems[j].RuntimeName
		}
		return problems[i].Message < problems[j].Message
	})
}
