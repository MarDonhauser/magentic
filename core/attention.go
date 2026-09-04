package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	attentionBreakReminderEvery = 8 * time.Minute
	attentionBreakInsistAfter   = 2
	attentionExternalDedupeMax  = 512
)

type AttentionObservationState string

const (
	AttentionObservationAvailable   AttentionObservationState = "available"
	AttentionObservationPartial     AttentionObservationState = "partial"
	AttentionObservationUnavailable AttentionObservationState = "unavailable"
	AttentionObservationAllDead     AttentionObservationState = "all-dead"
)

type AttentionQuietSignal string

const (
	AttentionQuietNone    AttentionQuietSignal = "none"
	AttentionQuietMeeting AttentionQuietSignal = "meeting"
	AttentionQuietAll     AttentionQuietSignal = "quiet"
	// AttentionQuietMuted ist die ausgeschaltete Benachrichtigung. Sie kommt
	// als Signal in den Plan, statt ihn danach im Adapter zu beschneiden:
	// sonst hat jede Oberfläche ihre eigene Policy, und die Unterdrückung
	// wird nirgends verbucht.
	AttentionQuietMuted AttentionQuietSignal = "muted"
)

// silences sagt, ob ein Ruhesignal jede Absicht unterdrückt.
func (q AttentionQuietSignal) silences() bool {
	return q == AttentionQuietAll || q == AttentionQuietMuted
}

// suppressionReason nennt, warum ein Ruhesignal unterdrückt hat.
func (q AttentionQuietSignal) suppressionReason() AttentionSuppressionReason {
	if q == AttentionQuietMuted {
		return AttentionSuppressedMuted
	}
	return AttentionSuppressedQuiet
}

type AttentionIntentKind string

const (
	AttentionIntentNeedsInput       AttentionIntentKind = "session-needs-input"
	AttentionIntentSessionComplete  AttentionIntentKind = "session-complete"
	AttentionIntentBreakReminder    AttentionIntentKind = "break-reminder"
	AttentionIntentBreakFinished    AttentionIntentKind = "break-finished"
	AttentionIntentDeploymentFailed AttentionIntentKind = "deployment-failed"
	AttentionIntentDeploymentReady  AttentionIntentKind = "deployment-ready"
	AttentionIntentStartupRestored  AttentionIntentKind = "startup-restored"
	AttentionIntentStartupFailed    AttentionIntentKind = "startup-failed"
)

type NativeAttentionLevel string

const (
	NativeAttentionUnchanged     NativeAttentionLevel = "unchanged"
	NativeAttentionCancel        NativeAttentionLevel = "cancel"
	NativeAttentionInformational NativeAttentionLevel = "informational"
	NativeAttentionCritical      NativeAttentionLevel = "critical"
)

type AttentionSuppressionReason string

const (
	AttentionSuppressedUnavailable       AttentionSuppressionReason = "observation-unavailable"
	AttentionSuppressedInsufficientFacts AttentionSuppressionReason = "insufficient-facts"
	AttentionSuppressedInitialState      AttentionSuppressionReason = "initial-state"
	AttentionSuppressedActiveSession     AttentionSuppressionReason = "active-session"
	AttentionSuppressedUnconfirmed       AttentionSuppressionReason = "completion-unconfirmed"
	AttentionSuppressedUnchanged         AttentionSuppressionReason = "unchanged"
	AttentionSuppressedBreakDisabled     AttentionSuppressionReason = "break-disabled"
	AttentionSuppressedBreakSnoozed      AttentionSuppressionReason = "break-snoozed"
	AttentionSuppressedBadMoment         AttentionSuppressionReason = "break-bad-moment"
	AttentionSuppressedMeeting           AttentionSuppressionReason = "meeting"
	AttentionSuppressedQuiet             AttentionSuppressionReason = "quiet"
	AttentionSuppressedMuted             AttentionSuppressionReason = "notifications-muted"
	AttentionSuppressedCadence           AttentionSuppressionReason = "cadence"
	AttentionSuppressedDuplicate         AttentionSuppressionReason = "duplicate"
	AttentionSuppressedLowerPriority     AttentionSuppressionReason = "lower-priority"
	AttentionSuppressedAllDead           AttentionSuppressionReason = "all-sessions-dead"
)

type AttentionDeploymentKind string

const (
	AttentionDeploymentBuildFailed AttentionDeploymentKind = "build-failed"
	AttentionDeploymentBuildReady  AttentionDeploymentKind = "build-ready"
	AttentionDeploymentAppDegraded AttentionDeploymentKind = "app-degraded"
	AttentionDeploymentAppHealthy  AttentionDeploymentKind = "app-healthy"
)

// AttentionDeploymentOutcome is an already-observed transition. Key should be
// stable for one transition (for example build URL + terminal state).
type AttentionDeploymentOutcome struct {
	Key    string                  `json:"key"`
	Kind   AttentionDeploymentKind `json:"kind"`
	Name   string                  `json:"name"`
	Detail string                  `json:"detail,omitempty"`
}

type AttentionEventKind string

const (
	// AttentionEventBreakReset acknowledges a user break action and cancels
	// any outstanding break attention without producing a notification.
	AttentionEventBreakReset      AttentionEventKind = "break-reset"
	AttentionEventBreakFinished   AttentionEventKind = "break-finished"
	AttentionEventStartupRestored AttentionEventKind = "startup-restored"
	AttentionEventStartupFailed   AttentionEventKind = "startup-failed"
)

// AttentionEvent carries explicit local UI transitions into the same planner
// cycle as Observation, break, and deployment facts. Key identifies one event
// episode and is used for bounded deduplication.
type AttentionEvent struct {
	Key   string             `json:"key,omitempty"`
	Kind  AttentionEventKind `json:"kind"`
	Count int                `json:"count,omitempty"`
}

type AttentionInput struct {
	Observation   ObservationSnapshot          `json:"observation"`
	ActiveSession SessionID                    `json:"activeSession,omitempty"`
	SessionLabels map[SessionID]string         `json:"sessionLabels,omitempty"`
	Break         BreakAdvice                  `json:"break"`
	Deployments   []AttentionDeploymentOutcome `json:"deployments,omitempty"`
	Events        []AttentionEvent             `json:"events,omitempty"`
	Quiet         AttentionQuietSignal         `json:"quiet,omitempty"`
	Now           time.Time                    `json:"now,omitzero"`
}

type AttentionNotificationIntent struct {
	Kind      AttentionIntentKind `json:"kind"`
	Title     string              `json:"title"`
	Message   string              `json:"message"`
	Sound     string              `json:"sound,omitempty"`
	SessionID SessionID           `json:"sessionId,omitempty"`
	DedupeKey string              `json:"dedupeKey"`
	Priority  int                 `json:"priority"`
}

type AttentionSuppression struct {
	Kind      AttentionIntentKind        `json:"kind,omitempty"`
	SessionID SessionID                  `json:"sessionId,omitempty"`
	DedupeKey string                     `json:"dedupeKey,omitempty"`
	Reason    AttentionSuppressionReason `json:"reason"`
}

// DockBadge.Update is false when the Observation cannot safely replace the
// last exact badge. Complete distinguishes an exact count from a lower bound.
type AttentionDockBadge struct {
	Update   bool   `json:"update"`
	Complete bool   `json:"complete"`
	Count    int    `json:"count"`
	Label    string `json:"label"`
}

// AttentionWaitingKind names why a Session waits on the developer: it asks a
// question or a permission (needs-input), or it finished and its work has not
// been looked at (review).
type AttentionWaitingKind string

const (
	AttentionWaitingInput  AttentionWaitingKind = "needs-input"
	AttentionWaitingReview AttentionWaitingKind = "review"
)

// AttentionInboxState says how much of the waiting picture the list holds. An
// empty list is only a claim that nothing waits when the state is complete.
type AttentionInboxState string

const (
	AttentionInboxComplete    AttentionInboxState = "complete"
	AttentionInboxIncomplete  AttentionInboxState = "incomplete"
	AttentionInboxUnavailable AttentionInboxState = "unavailable"
)

// AttentionInboxEntry is one Session waiting on the developer. WaitingSinceKnown
// false means the wait had already started when the planner first saw the
// Session, so Since is a lower bound and never the moment the wait began.
type AttentionInboxEntry struct {
	SessionID         SessionID            `json:"sessionId"`
	Kind              AttentionWaitingKind `json:"kind"`
	WaitingSince      time.Time            `json:"waitingSince"`
	WaitingSinceKnown bool                 `json:"waitingSinceKnown"`
	Excerpt           string               `json:"excerpt,omitempty"`
	ExcerptKnown      bool                 `json:"excerptKnown"`
	StatusSource      StatusSource         `json:"statusSource"`
}

// AttentionInbox is the cross-Project list of waiting Sessions, ordered longest
// wait first, produced by the same cycle as the notifications.
type AttentionInbox struct {
	State   AttentionInboxState   `json:"state"`
	Entries []AttentionInboxEntry `json:"entries"`
}

type AttentionPlan struct {
	Observation     AttentionObservationState     `json:"observation"`
	Notifications   []AttentionNotificationIntent `json:"notifications"`
	DockBadge       AttentionDockBadge            `json:"dockBadge"`
	Inbox           AttentionInbox                `json:"inbox"`
	NativeAttention NativeAttentionLevel          `json:"nativeAttention"`
	BringToFront    bool                          `json:"bringToFront"`
	Suppressions    []AttentionSuppression        `json:"suppressions"`
}

type AttentionPlannerConfig struct {
	Now func() time.Time
}

type attentionSessionMemory struct {
	known             bool
	attention         AttentionState
	completionPending bool
	needsEpisode      uint64
	completeEpisode   uint64
	waitingSince      time.Time
	waitingSinceKnown bool
}

type attentionBreakMemory struct {
	level        string
	reminders    int
	lastReminder time.Time
	nativeActive bool
}

// AttentionPlanner is a stateful, deterministic policy Module. It plans only;
// notification, Dock, native-attention, and window APIs remain executor work.
type AttentionPlanner struct {
	mu            sync.Mutex
	now           func() time.Time
	sessions      map[SessionID]attentionSessionMemory
	breakState    attentionBreakMemory
	seenExternal  map[string]bool
	externalOrder []string
}

func NewAttentionPlanner(config AttentionPlannerConfig) *AttentionPlanner {
	clock := config.Now
	if clock == nil {
		clock = time.Now
	}
	return &AttentionPlanner{
		now: clock, sessions: map[SessionID]attentionSessionMemory{}, seenExternal: map[string]bool{},
	}
}

type attentionCandidate struct {
	intent       AttentionNotificationIntent
	native       NativeAttentionLevel
	bringToFront bool
	breakLevel   string
	breakCount   int
}

func (p *AttentionPlanner) Plan(input AttentionInput) AttentionPlan {
	if p == nil {
		return finishAttentionPlan(AttentionPlan{
			Observation:     AttentionObservationUnavailable,
			Inbox:           AttentionInbox{State: AttentionInboxUnavailable},
			NativeAttention: NativeAttentionUnchanged,
		})
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := input.Now
	if now.IsZero() {
		now = p.now()
	}
	plan := AttentionPlan{
		Observation:     attentionObservationState(input.Observation),
		NativeAttention: NativeAttentionUnchanged,
	}
	plan.DockBadge = attentionDockBadge(input.Observation)
	plan.Inbox = AttentionInbox{State: attentionInboxState(plan.Observation)}
	if plan.Observation == AttentionObservationUnavailable {
		plan.Suppressions = append(plan.Suppressions, AttentionSuppression{Reason: AttentionSuppressedUnavailable})
	} else if plan.Observation == AttentionObservationAllDead {
		plan.Suppressions = append(plan.Suppressions, AttentionSuppression{Reason: AttentionSuppressedAllDead})
	}

	candidates := p.sessionCandidates(input, now, &plan)
	candidates = append(candidates, p.eventCandidates(input.Events, &plan)...)
	breakCandidate := p.breakCandidate(input.Break, input.Quiet, now, &plan)
	if breakCandidate != nil {
		candidates = append(candidates, *breakCandidate)
	}
	candidates = append(candidates, p.deploymentCandidates(input.Deployments, &plan)...)

	if input.Quiet.silences() {
		reason := input.Quiet.suppressionReason()
		for _, candidate := range candidates {
			plan.Suppressions = append(plan.Suppressions, attentionCandidateSuppression(candidate, reason))
		}
		plan.BringToFront = false
		if p.breakState.nativeActive {
			plan.NativeAttention = NativeAttentionCancel
			p.breakState.nativeActive = false
		}
		return finishAttentionPlan(plan)
	}
	if len(candidates) == 0 {
		return finishAttentionPlan(plan)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].intent.Priority != candidates[j].intent.Priority {
			return candidates[i].intent.Priority > candidates[j].intent.Priority
		}
		return candidates[i].intent.DedupeKey < candidates[j].intent.DedupeKey
	})
	highest := candidates[0].intent.Priority
	for _, candidate := range candidates {
		if candidate.intent.Priority != highest {
			plan.Suppressions = append(plan.Suppressions, attentionCandidateSuppression(candidate, AttentionSuppressedLowerPriority))
			continue
		}
		plan.Notifications = append(plan.Notifications, candidate.intent)
		plan.NativeAttention = strongerNativeAttention(plan.NativeAttention, candidate.native)
		plan.BringToFront = plan.BringToFront || candidate.bringToFront
		if candidate.intent.Kind == AttentionIntentBreakReminder {
			p.breakState.level = candidate.breakLevel
			p.breakState.reminders = candidate.breakCount
			p.breakState.lastReminder = now
			p.breakState.nativeActive = candidate.native == NativeAttentionInformational || candidate.native == NativeAttentionCritical
		}
	}
	return finishAttentionPlan(plan)
}

func (p *AttentionPlanner) sessionCandidates(input AttentionInput, now time.Time, plan *AttentionPlan) []attentionCandidate {
	if input.Observation.Availability == ObservationUnavailable {
		return nil
	}
	observations := append([]SessionObservation(nil), input.Observation.Sessions...)
	sort.SliceStable(observations, func(i, j int) bool { return observations[i].SessionID < observations[j].SessionID })
	var candidates []attentionCandidate
	for _, observed := range observations {
		if observed.SessionID == "" || observed.Availability == ObservationUnavailable ||
			observed.Presence == SessionPresenceUnknown || observed.Attention == AttentionUnknown {
			plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
				SessionID: observed.SessionID, Reason: AttentionSuppressedInsufficientFacts,
			})
			// A Session whose facts do not carry is neither waiting nor not
			// waiting, so the list it is missing from is incomplete.
			if plan.Inbox.State == AttentionInboxComplete {
				plan.Inbox.State = AttentionInboxIncomplete
			}
			continue
		}
		memory := p.sessions[observed.SessionID]
		if observed.Presence == SessionPresenceAbsent {
			memory.known = true
			memory.attention = AttentionNone
			memory.completionPending = false
			memory.waitingSince = time.Time{}
			memory.waitingSinceKnown = false
			p.sessions[observed.SessionID] = memory
			continue
		}
		if !memory.known {
			memory.known = true
			memory.attention = observed.Attention
			memory.completionPending = false
			// The wait may be hours old; the planner only knows it exists now.
			memory.waitingSince, memory.waitingSinceKnown = now, false
			p.sessions[observed.SessionID] = memory
			if observed.Attention == AttentionNeedsInput || observed.Attention == AttentionReview {
				plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
					SessionID: observed.SessionID, Reason: AttentionSuppressedInitialState,
				})
				plan.Inbox.Entries = append(plan.Inbox.Entries, attentionInboxEntry(observed, memory))
			}
			continue
		}

		previous := memory.attention
		switch observed.Attention {
		case AttentionNeedsInput:
			memory.completionPending = false
			if previous != AttentionNeedsInput {
				memory.needsEpisode++
				candidate := attentionNeedsInputCandidate(observed.SessionID, input.SessionLabels[observed.SessionID], memory.needsEpisode)
				if observed.SessionID == input.ActiveSession {
					plan.Suppressions = append(plan.Suppressions, attentionCandidateSuppression(candidate, AttentionSuppressedActiveSession))
				} else {
					candidates = append(candidates, candidate)
				}
			} else {
				plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
					Kind: AttentionIntentNeedsInput, SessionID: observed.SessionID,
					DedupeKey: attentionSessionKey(observed.SessionID, "needs-input", memory.needsEpisode),
					Reason:    AttentionSuppressedUnchanged,
				})
			}
		case AttentionReview:
			switch {
			case previous == AttentionWorking:
				memory.completionPending = true
				plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
					Kind: AttentionIntentSessionComplete, SessionID: observed.SessionID,
					Reason: AttentionSuppressedUnconfirmed,
				})
			case previous == AttentionReview && memory.completionPending:
				memory.completeEpisode++
				memory.completionPending = false
				candidate := attentionCompletionCandidate(observed.SessionID, input.SessionLabels[observed.SessionID], memory.completeEpisode)
				if observed.SessionID == input.ActiveSession {
					plan.Suppressions = append(plan.Suppressions, attentionCandidateSuppression(candidate, AttentionSuppressedActiveSession))
				} else {
					candidates = append(candidates, candidate)
				}
			default:
				memory.completionPending = false
			}
		default:
			memory.completionPending = false
		}
		memory = attentionStampWaiting(memory, previous, observed.Attention, now)
		if attentionWaiting(observed.Attention) {
			plan.Inbox.Entries = append(plan.Inbox.Entries, attentionInboxEntry(observed, memory))
		}
		memory.attention = observed.Attention
		p.sessions[observed.SessionID] = memory
	}
	return candidates
}

func attentionWaiting(state AttentionState) bool {
	return state == AttentionNeedsInput || state == AttentionReview
}

// attentionStampWaiting records when the current wait began. A changed waiting
// kind is a new wait, so it re-stamps; an unchanged one keeps its start,
// including a start that is only a lower bound.
func attentionStampWaiting(memory attentionSessionMemory, previous, current AttentionState, now time.Time) attentionSessionMemory {
	if !attentionWaiting(current) {
		memory.waitingSince, memory.waitingSinceKnown = time.Time{}, false
		return memory
	}
	if previous != current || memory.waitingSince.IsZero() {
		memory.waitingSince, memory.waitingSinceKnown = now, previous != current
	}
	return memory
}

func attentionInboxEntry(observed SessionObservation, memory attentionSessionMemory) AttentionInboxEntry {
	kind := AttentionWaitingReview
	if observed.Attention == AttentionNeedsInput {
		kind = AttentionWaitingInput
	}
	entry := AttentionInboxEntry{
		SessionID:         observed.SessionID,
		Kind:              kind,
		WaitingSince:      memory.waitingSince,
		WaitingSinceKnown: memory.waitingSinceKnown,
		ExcerptKnown:      observed.ContentKnown,
		StatusSource:      observed.StatusSource,
	}
	if observed.ContentKnown {
		entry.Excerpt = attentionInboxExcerpt(observed.Content)
	}
	return entry
}

// attentionInboxExcerptLines keeps the tail long enough to carry a permission
// prompt and short enough to stay one entry in a list.
const attentionInboxExcerptLines = 6

// attentionInboxExcerpt takes the tail of the already-normalized pane content:
// the last lines that carry text, in reading order.
func attentionInboxExcerpt(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end - attentionInboxExcerptLines
	if start < 0 {
		start = 0
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func attentionInboxState(observation AttentionObservationState) AttentionInboxState {
	switch observation {
	case AttentionObservationUnavailable:
		return AttentionInboxUnavailable
	case AttentionObservationPartial:
		return AttentionInboxIncomplete
	default:
		return AttentionInboxComplete
	}
}

func attentionNeedsInputCandidate(id SessionID, label string, episode uint64) attentionCandidate {
	label = attentionSessionLabel(id, label)
	return attentionCandidate{
		intent: AttentionNotificationIntent{
			Kind: AttentionIntentNeedsInput, Title: "magentic · " + label,
			Message: "Agent wartet auf deine Eingabe", Sound: "Glass", SessionID: id,
			DedupeKey: attentionSessionKey(id, "needs-input", episode), Priority: 500,
		},
		native: NativeAttentionCritical,
	}
}

func attentionCompletionCandidate(id SessionID, label string, episode uint64) attentionCandidate {
	label = attentionSessionLabel(id, label)
	return attentionCandidate{
		intent: AttentionNotificationIntent{
			Kind: AttentionIntentSessionComplete, Title: "magentic · " + label,
			Message: "Agent ist fertig — bereit für den nächsten Prompt", Sound: "Ping", SessionID: id,
			DedupeKey: attentionSessionKey(id, "complete", episode), Priority: 400,
		},
		native: NativeAttentionInformational,
	}
}

func attentionSessionLabel(id SessionID, label string) string {
	if strings.TrimSpace(label) != "" {
		return strings.TrimSpace(label)
	}
	if id != "" {
		return string(id)
	}
	return "Session"
}

func attentionSessionKey(id SessionID, kind string, episode uint64) string {
	return "session:" + string(id) + ":" + kind + ":" + strconv.FormatUint(episode, 10)
}

func (p *AttentionPlanner) eventCandidates(events []AttentionEvent, plan *AttentionPlan) []attentionCandidate {
	events = append([]AttentionEvent(nil), events...)
	sort.SliceStable(events, func(i, j int) bool { return attentionEventKey(events[i]) < attentionEventKey(events[j]) })
	var candidates []attentionCandidate
	for _, event := range events {
		switch event.Kind {
		case AttentionEventBreakReset:
			p.resetBreakAttention(plan)
			continue
		case AttentionEventBreakFinished:
			key := attentionEventKey(event)
			if p.seenExternal[key] {
				plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
					Kind: AttentionIntentBreakFinished, DedupeKey: key, Reason: AttentionSuppressedDuplicate,
				})
				continue
			}
			p.rememberExternal(key)
			candidates = append(candidates, attentionCandidate{intent: AttentionNotificationIntent{
				Kind: AttentionIntentBreakFinished, Title: "magentic",
				Message: "Pause vorbei — nichts drängt.", Sound: "Purr",
				DedupeKey: key, Priority: 150,
			}})
		case AttentionEventStartupRestored:
			if event.Count < 1 {
				plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
					Kind: AttentionIntentStartupRestored, DedupeKey: attentionEventKey(event), Reason: AttentionSuppressedInsufficientFacts,
				})
				continue
			}
			key := attentionEventKey(event)
			if p.seenExternal[key] {
				plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
					Kind: AttentionIntentStartupRestored, DedupeKey: key, Reason: AttentionSuppressedDuplicate,
				})
				continue
			}
			p.rememberExternal(key)
			word := "Sessions"
			if event.Count == 1 {
				word = "Session"
			}
			candidates = append(candidates, attentionCandidate{intent: AttentionNotificationIntent{
				Kind: AttentionIntentStartupRestored, Title: "magentic",
				Message:   fmt.Sprintf("%d %s wiederhergestellt", event.Count, word),
				DedupeKey: key, Priority: 100,
			}})
		case AttentionEventStartupFailed:
			key := attentionEventKey(event)
			if p.seenExternal[key] {
				plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
					Kind: AttentionIntentStartupFailed, DedupeKey: key, Reason: AttentionSuppressedDuplicate,
				})
				continue
			}
			p.rememberExternal(key)
			candidates = append(candidates, attentionCandidate{intent: AttentionNotificationIntent{
				Kind: AttentionIntentStartupFailed, Title: "magentic",
				Message:   "State konnte nicht geladen werden — Sessions wurden nicht wiederhergestellt",
				DedupeKey: key, Priority: 300,
			}})
		default:
			plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
				DedupeKey: attentionEventKey(event), Reason: AttentionSuppressedInsufficientFacts,
			})
		}
	}
	return candidates
}

func attentionEventKey(event AttentionEvent) string {
	key := strings.TrimSpace(event.Key)
	if key == "" {
		key = string(event.Kind)
	}
	return "event:" + key
}

func (p *AttentionPlanner) breakCandidate(advice BreakAdvice, quiet AttentionQuietSignal, now time.Time, plan *AttentionPlan) *attentionCandidate {
	if !advice.Enabled {
		p.resetBreakAttention(plan)
		plan.Suppressions = append(plan.Suppressions, AttentionSuppression{Kind: AttentionIntentBreakReminder, Reason: AttentionSuppressedBreakDisabled})
		return nil
	}
	switch advice.Level {
	case BreakLevelNone, BreakLevelHint, BreakLevelResting, "":
		p.resetBreakAttention(plan)
		return nil
	case BreakLevelDue, BreakLevelOverdue:
		// Actionable break levels continue below.
	default:
		plan.Suppressions = append(plan.Suppressions, AttentionSuppression{Kind: AttentionIntentBreakReminder, Reason: AttentionSuppressedInsufficientFacts})
		return nil
	}
	if advice.Snoozed {
		p.resetBreakAttention(plan)
		plan.Suppressions = append(plan.Suppressions, AttentionSuppression{Kind: AttentionIntentBreakReminder, Reason: AttentionSuppressedBreakSnoozed})
		return nil
	}
	if advice.Level == BreakLevelDue && !advice.GoodMoment {
		plan.Suppressions = append(plan.Suppressions, AttentionSuppression{Kind: AttentionIntentBreakReminder, Reason: AttentionSuppressedBadMoment})
		return nil
	}
	if quiet == AttentionQuietMeeting {
		plan.Suppressions = append(plan.Suppressions, AttentionSuppression{Kind: AttentionIntentBreakReminder, Reason: AttentionSuppressedMeeting})
		return nil
	}

	first := advice.Level != p.breakState.level
	reminders := p.breakState.reminders
	if first {
		reminders = 0
	} else if !p.breakState.lastReminder.IsZero() && now.Sub(p.breakState.lastReminder) < attentionBreakReminderEvery {
		plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
			Kind:      AttentionIntentBreakReminder,
			DedupeKey: "break:" + advice.Level + ":" + strconv.Itoa(reminders),
			Reason:    AttentionSuppressedCadence,
		})
		return nil
	}
	reminders++
	insist := advice.Level == BreakLevelOverdue || reminders >= attentionBreakInsistAfter
	native := NativeAttentionInformational
	if insist {
		native = NativeAttentionCritical
	}
	return &attentionCandidate{
		intent: AttentionNotificationIntent{
			Kind: AttentionIntentBreakReminder, Title: "magentic · Zeit für eine Pause",
			Message: "Steh mal auf — ein paar Schritte reichen.", Sound: "Purr",
			DedupeKey: "break:" + advice.Level + ":" + strconv.Itoa(reminders), Priority: 250,
		},
		native: native, bringToFront: insist && advice.GoodMoment && reminders >= attentionBreakInsistAfter+1,
		breakLevel: advice.Level, breakCount: reminders,
	}
}

func (p *AttentionPlanner) resetBreakAttention(plan *AttentionPlan) {
	if p.breakState.nativeActive {
		plan.NativeAttention = NativeAttentionCancel
	}
	p.breakState = attentionBreakMemory{}
}

func (p *AttentionPlanner) deploymentCandidates(outcomes []AttentionDeploymentOutcome, plan *AttentionPlan) []attentionCandidate {
	outcomes = append([]AttentionDeploymentOutcome(nil), outcomes...)
	sort.SliceStable(outcomes, func(i, j int) bool { return attentionDeploymentKey(outcomes[i]) < attentionDeploymentKey(outcomes[j]) })
	var candidates []attentionCandidate
	for _, outcome := range outcomes {
		key := attentionDeploymentKey(outcome)
		if p.seenExternal[key] {
			plan.Suppressions = append(plan.Suppressions, AttentionSuppression{DedupeKey: key, Reason: AttentionSuppressedDuplicate})
			continue
		}
		candidate, ok := attentionDeploymentCandidate(outcome, key)
		if !ok {
			plan.Suppressions = append(plan.Suppressions, AttentionSuppression{DedupeKey: key, Reason: AttentionSuppressedInsufficientFacts})
			continue
		}
		p.rememberExternal(key)
		candidates = append(candidates, candidate)
	}
	return candidates
}

func attentionDeploymentKey(outcome AttentionDeploymentOutcome) string {
	key := strings.TrimSpace(outcome.Key)
	if key == "" {
		key = string(outcome.Kind) + ":" + strings.TrimSpace(outcome.Name) + ":" + strings.TrimSpace(outcome.Detail)
	}
	return "deploy:" + key
}

func attentionDeploymentCandidate(outcome AttentionDeploymentOutcome, key string) (attentionCandidate, bool) {
	name := strings.TrimSpace(outcome.Name)
	if name == "" {
		name = "Deployment"
	}
	message := name
	if detail := strings.TrimSpace(outcome.Detail); detail != "" {
		message += " " + detail
	}
	intent := AttentionNotificationIntent{DedupeKey: key, Message: message}
	candidate := attentionCandidate{intent: intent}
	switch outcome.Kind {
	case AttentionDeploymentBuildFailed:
		candidate.intent.Kind = AttentionIntentDeploymentFailed
		candidate.intent.Title = "magentic · Build failed"
		candidate.intent.Sound = "Basso"
		candidate.intent.Priority = 350
		candidate.native = NativeAttentionCritical
	case AttentionDeploymentAppDegraded:
		candidate.intent.Kind = AttentionIntentDeploymentFailed
		candidate.intent.Title = "magentic · Argo Degraded"
		candidate.intent.Sound = "Basso"
		candidate.intent.Priority = 350
		candidate.native = NativeAttentionCritical
	case AttentionDeploymentBuildReady:
		candidate.intent.Kind = AttentionIntentDeploymentReady
		candidate.intent.Title = "magentic · Build fertig"
		candidate.intent.Sound = "Ping"
		candidate.intent.Priority = 200
		candidate.native = NativeAttentionInformational
	case AttentionDeploymentAppHealthy:
		candidate.intent.Kind = AttentionIntentDeploymentReady
		candidate.intent.Title = "magentic · Argo Healthy"
		candidate.intent.Sound = "Ping"
		candidate.intent.Priority = 200
		candidate.native = NativeAttentionInformational
	default:
		return attentionCandidate{}, false
	}
	return candidate, true
}

func (p *AttentionPlanner) rememberExternal(key string) {
	p.seenExternal[key] = true
	p.externalOrder = append(p.externalOrder, key)
	if len(p.externalOrder) <= attentionExternalDedupeMax {
		return
	}
	drop := p.externalOrder[0]
	p.externalOrder = p.externalOrder[1:]
	delete(p.seenExternal, drop)
}

func attentionObservationState(snapshot ObservationSnapshot) AttentionObservationState {
	if snapshot.Availability == ObservationUnavailable {
		return AttentionObservationUnavailable
	}
	// All-dead is an exact claim. A partial snapshot may contain only known
	// absent Sessions while still lacking enough presence facts to make that
	// claim for the whole Observation.
	if snapshot.Availability == ObservationPartial {
		return AttentionObservationPartial
	}
	if len(snapshot.Sessions) > 0 {
		allAbsent := true
		for _, observed := range snapshot.Sessions {
			if observed.Availability == ObservationUnavailable || observed.Presence != SessionPresenceAbsent {
				allAbsent = false
				break
			}
		}
		if allAbsent {
			return AttentionObservationAllDead
		}
	}
	return AttentionObservationAvailable
}

func attentionDockBadge(snapshot ObservationSnapshot) AttentionDockBadge {
	if snapshot.Availability == ObservationUnavailable {
		return AttentionDockBadge{}
	}
	count := 0
	complete := snapshot.Availability == ObservationAvailable
	for _, observed := range snapshot.Sessions {
		if observed.Availability == ObservationUnavailable || observed.Presence == SessionPresenceUnknown || observed.Attention == AttentionUnknown {
			complete = false
			continue
		}
		if observed.Presence == SessionPresencePresent && observed.Attention == AttentionNeedsInput {
			count++
		}
	}
	if !complete && count == 0 {
		return AttentionDockBadge{Complete: false}
	}
	label := ""
	if count > 0 {
		label = strconv.Itoa(count)
		if !complete {
			label += "+"
		}
	}
	return AttentionDockBadge{Update: true, Complete: complete, Count: count, Label: label}
}

func attentionCandidateSuppression(candidate attentionCandidate, reason AttentionSuppressionReason) AttentionSuppression {
	return AttentionSuppression{
		Kind: candidate.intent.Kind, SessionID: candidate.intent.SessionID,
		DedupeKey: candidate.intent.DedupeKey, Reason: reason,
	}
}

func strongerNativeAttention(current, next NativeAttentionLevel) NativeAttentionLevel {
	rank := func(level NativeAttentionLevel) int {
		switch level {
		case NativeAttentionCritical:
			return 4
		case NativeAttentionInformational:
			return 3
		case NativeAttentionCancel:
			return 2
		default:
			return 1
		}
	}
	if rank(next) > rank(current) {
		return next
	}
	return current
}

func finishAttentionPlan(plan AttentionPlan) AttentionPlan {
	if plan.Notifications == nil {
		plan.Notifications = []AttentionNotificationIntent{}
	}
	if plan.Suppressions == nil {
		plan.Suppressions = []AttentionSuppression{}
	}
	if plan.Inbox.State == "" {
		plan.Inbox.State = AttentionInboxUnavailable
	}
	if plan.Inbox.Entries == nil {
		plan.Inbox.Entries = []AttentionInboxEntry{}
	}
	// Longest wait first. A wait whose start is only a lower bound is at least
	// as old as every known wait, so it sorts above them.
	sort.SliceStable(plan.Inbox.Entries, func(i, j int) bool {
		a, b := plan.Inbox.Entries[i], plan.Inbox.Entries[j]
		if a.WaitingSinceKnown != b.WaitingSinceKnown {
			return !a.WaitingSinceKnown
		}
		if !a.WaitingSince.Equal(b.WaitingSince) {
			return a.WaitingSince.Before(b.WaitingSince)
		}
		return a.SessionID < b.SessionID
	})
	sort.SliceStable(plan.Suppressions, func(i, j int) bool {
		a, b := plan.Suppressions[i], plan.Suppressions[j]
		left := fmt.Sprintf("%s\x00%s\x00%s\x00%s", a.Reason, a.Kind, a.SessionID, a.DedupeKey)
		right := fmt.Sprintf("%s\x00%s\x00%s\x00%s", b.Reason, b.Kind, b.SessionID, b.DedupeKey)
		return left < right
	})
	return plan
}
