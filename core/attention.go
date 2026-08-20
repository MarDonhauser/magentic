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
)

type AttentionIntentKind string

const (
	AttentionIntentNeedsInput       AttentionIntentKind = "session-needs-input"
	AttentionIntentSessionComplete  AttentionIntentKind = "session-complete"
	AttentionIntentBreakReminder    AttentionIntentKind = "break-reminder"
	AttentionIntentBreakFinished    AttentionIntentKind = "break-finished"
	AttentionIntentDeploymentFailed AttentionIntentKind = "deployment-failed"
	AttentionIntentDeploymentReady  AttentionIntentKind = "deployment-ready"
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
	AttentionEventBreakReset    AttentionEventKind = "break-reset"
	AttentionEventBreakFinished AttentionEventKind = "break-finished"
)

// AttentionEvent carries explicit local UI transitions into the same planner
// cycle as Observation, break, and deployment facts. Key identifies one event
// episode and is used for bounded deduplication.
type AttentionEvent struct {
	Key  string             `json:"key,omitempty"`
	Kind AttentionEventKind `json:"kind"`
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

type AttentionPlan struct {
	Observation     AttentionObservationState     `json:"observation"`
	Notifications   []AttentionNotificationIntent `json:"notifications"`
	DockBadge       AttentionDockBadge            `json:"dockBadge"`
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
		return finishAttentionPlan(AttentionPlan{Observation: AttentionObservationUnavailable, NativeAttention: NativeAttentionUnchanged})
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
	if plan.Observation == AttentionObservationUnavailable {
		plan.Suppressions = append(plan.Suppressions, AttentionSuppression{Reason: AttentionSuppressedUnavailable})
	} else if plan.Observation == AttentionObservationAllDead {
		plan.Suppressions = append(plan.Suppressions, AttentionSuppression{Reason: AttentionSuppressedAllDead})
	}

	candidates := p.sessionCandidates(input, &plan)
	candidates = append(candidates, p.eventCandidates(input.Events, &plan)...)
	breakCandidate := p.breakCandidate(input.Break, input.Quiet, now, &plan)
	if breakCandidate != nil {
		candidates = append(candidates, *breakCandidate)
	}
	candidates = append(candidates, p.deploymentCandidates(input.Deployments, &plan)...)

	if input.Quiet == AttentionQuietAll {
		for _, candidate := range candidates {
			plan.Suppressions = append(plan.Suppressions, attentionCandidateSuppression(candidate, AttentionSuppressedQuiet))
		}
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

func (p *AttentionPlanner) sessionCandidates(input AttentionInput, plan *AttentionPlan) []attentionCandidate {
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
			continue
		}
		memory := p.sessions[observed.SessionID]
		if observed.Presence == SessionPresenceAbsent {
			memory.known = true
			memory.attention = AttentionNone
			memory.completionPending = false
			p.sessions[observed.SessionID] = memory
			continue
		}
		if !memory.known {
			memory.known = true
			memory.attention = observed.Attention
			memory.completionPending = false
			p.sessions[observed.SessionID] = memory
			if observed.Attention == AttentionNeedsInput || observed.Attention == AttentionReview {
				plan.Suppressions = append(plan.Suppressions, AttentionSuppression{
					SessionID: observed.SessionID, Reason: AttentionSuppressedInitialState,
				})
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
		memory.attention = observed.Attention
		p.sessions[observed.SessionID] = memory
	}
	return candidates
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
	if snapshot.Availability == ObservationPartial {
		return AttentionObservationPartial
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
	sort.SliceStable(plan.Suppressions, func(i, j int) bool {
		a, b := plan.Suppressions[i], plan.Suppressions[j]
		left := fmt.Sprintf("%s\x00%s\x00%s\x00%s", a.Reason, a.Kind, a.SessionID, a.DedupeKey)
		right := fmt.Sprintf("%s\x00%s\x00%s\x00%s", b.Reason, b.Kind, b.SessionID, b.DedupeKey)
		return left < right
	})
	return plan
}
