package core

import (
	"strings"
	"testing"
	"time"
)

var attentionTestStart = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

func TestAttentionDistinguishesUnavailableObservationFromAllDead(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionWorking),
	)))

	unavailable := planner.Plan(attentionTestInput(attentionTestStart.Add(time.Second), ObservationSnapshot{
		Availability: ObservationUnavailable,
		Sessions: []SessionObservation{{
			SessionID: "one", Availability: ObservationUnavailable,
			Presence: SessionPresenceUnknown, Attention: AttentionUnknown,
		}},
	}))
	if unavailable.Observation != AttentionObservationUnavailable || unavailable.DockBadge.Update {
		t.Fatalf("unavailable Observation was treated as a negative fact: %#v", unavailable)
	}
	if !hasAttentionSuppression(unavailable, AttentionSuppressedUnavailable) {
		t.Fatalf("unavailable suppression missing: %#v", unavailable.Suppressions)
	}

	// Unavailable knowledge must not overwrite the last known working state.
	blocked := planner.Plan(attentionTestInput(attentionTestStart.Add(2*time.Second), attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionNeedsInput),
	)))
	if len(blocked.Notifications) != 1 || blocked.Notifications[0].Kind != AttentionIntentNeedsInput {
		t.Fatalf("known transition after outage was lost: %#v", blocked)
	}

	allDeadPlanner := NewAttentionPlanner(AttentionPlannerConfig{})
	dead := allDeadPlanner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable, attentionAbsent("one"), attentionAbsent("two"),
	)))
	if dead.Observation != AttentionObservationAllDead || !dead.DockBadge.Update || !dead.DockBadge.Complete || dead.DockBadge.Label != "" {
		t.Fatalf("all-dead exact knowledge = %#v", dead)
	}
	if !hasAttentionSuppression(dead, AttentionSuppressedAllDead) {
		t.Fatalf("all-dead reason missing: %#v", dead.Suppressions)
	}
}

func TestAttentionDoesNotTreatMalformedPaneListAsAllDead(t *testing.T) {
	session := Session{ID: "session-1", Name: "one", RuntimeName: "mgt-one"}
	snapshot := malformedListPanesObservation(t, []Session{session})
	plan := NewAttentionPlanner(AttentionPlannerConfig{}).Plan(attentionTestInput(attentionTestStart, snapshot))

	if plan.Observation != AttentionObservationPartial || plan.Observation == AttentionObservationAllDead {
		t.Fatalf("malformed pane list became all-dead: %#v", plan)
	}
	if plan.DockBadge.Update || plan.DockBadge.Complete {
		t.Fatalf("partial runtime knowledge replaced the exact badge: %#v", plan.DockBadge)
	}
	if !hasAttentionSuppression(plan, AttentionSuppressedInsufficientFacts) {
		t.Fatalf("unknown Session was not surfaced: %#v", plan.Suppressions)
	}
}

func TestAttentionNeedsInputTransitionsAreQuietAndActiveAware(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	labels := map[SessionID]string{"one": "Renamed Session"}
	base := attentionTestInput(attentionTestStart, attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionWorking)))
	base.SessionLabels = labels
	planner.Plan(base)

	active := attentionTestInput(attentionTestStart.Add(time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionNeedsInput)))
	active.ActiveSession = "one"
	active.SessionLabels = labels
	activePlan := planner.Plan(active)
	if len(activePlan.Notifications) != 0 || !hasAttentionSuppression(activePlan, AttentionSuppressedActiveSession) {
		t.Fatalf("active Session was noisy: %#v", activePlan)
	}
	if !activePlan.DockBadge.Update || activePlan.DockBadge.Count != 1 || activePlan.DockBadge.Label != "1" {
		t.Fatalf("active suppression incorrectly hid badge state: %#v", activePlan.DockBadge)
	}

	unchanged := active
	unchanged.Now = attentionTestStart.Add(2 * time.Second)
	unchanged.ActiveSession = ""
	unchangedPlan := planner.Plan(unchanged)
	if len(unchangedPlan.Notifications) != 0 || !hasAttentionSuppression(unchangedPlan, AttentionSuppressedUnchanged) {
		t.Fatalf("unchanged blocked Session re-notified: %#v", unchangedPlan)
	}

	running := attentionTestInput(attentionTestStart.Add(3*time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionWorking)))
	running.SessionLabels = labels
	planner.Plan(running)
	next := attentionTestInput(attentionTestStart.Add(4*time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionNeedsInput)))
	next.SessionLabels = labels
	nextPlan := planner.Plan(next)
	if len(nextPlan.Notifications) != 1 || nextPlan.NativeAttention != NativeAttentionCritical {
		t.Fatalf("needs-input transition = %#v", nextPlan)
	}
	intent := nextPlan.Notifications[0]
	if intent.Title != "magentic · Renamed Session" || intent.SessionID != "one" || !strings.HasSuffix(intent.DedupeKey, ":2") {
		t.Fatalf("needs-input intent identity = %#v", intent)
	}
}

func TestAttentionRequiresConfirmedCompletionAndSuppressesActiveConfirmation(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionWorking))))

	firstReview := planner.Plan(attentionTestInput(attentionTestStart.Add(time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionReview))))
	if len(firstReview.Notifications) != 0 || !hasAttentionSuppression(firstReview, AttentionSuppressedUnconfirmed) {
		t.Fatalf("single idle sample announced completion: %#v", firstReview)
	}
	confirmed := planner.Plan(attentionTestInput(attentionTestStart.Add(2*time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionReview))))
	if len(confirmed.Notifications) != 1 || confirmed.Notifications[0].Kind != AttentionIntentSessionComplete || confirmed.NativeAttention != NativeAttentionInformational {
		t.Fatalf("confirmed completion = %#v", confirmed)
	}
	repeated := planner.Plan(attentionTestInput(attentionTestStart.Add(3*time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionReview))))
	if len(repeated.Notifications) != 0 {
		t.Fatalf("stable review state re-notified: %#v", repeated)
	}

	planner.Plan(attentionTestInput(attentionTestStart.Add(4*time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionWorking))))
	planner.Plan(attentionTestInput(attentionTestStart.Add(5*time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionReview))))
	active := attentionTestInput(attentionTestStart.Add(6*time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionReview)))
	active.ActiveSession = "one"
	activePlan := planner.Plan(active)
	if len(activePlan.Notifications) != 0 || !hasAttentionSuppression(activePlan, AttentionSuppressedActiveSession) {
		t.Fatalf("active completion confirmation was noisy: %#v", activePlan)
	}
	active.ActiveSession = ""
	active.Now = attentionTestStart.Add(7 * time.Second)
	if late := planner.Plan(active); len(late.Notifications) != 0 {
		t.Fatalf("suppressed completion was replayed late: %#v", late)
	}
}

func TestAttentionUsesKnownFactsFromPartialObservation(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable,
		attentionObserved("known", AttentionWorking),
		attentionObserved("unknown", AttentionWorking),
	)))
	partialUnknown := SessionObservation{
		SessionID: "unknown", Availability: ObservationUnavailable,
		Presence: SessionPresenceUnknown, Attention: AttentionUnknown,
	}
	plan := planner.Plan(attentionTestInput(attentionTestStart.Add(time.Second), attentionSnapshot(
		ObservationPartial,
		attentionObserved("known", AttentionNeedsInput),
		partialUnknown,
	)))
	if plan.Observation != AttentionObservationPartial || len(plan.Notifications) != 1 || plan.Notifications[0].SessionID != "known" {
		t.Fatalf("healthy partial fact was discarded: %#v", plan)
	}
	if !plan.DockBadge.Update || plan.DockBadge.Complete || plan.DockBadge.Count != 1 || plan.DockBadge.Label != "1+" {
		t.Fatalf("partial badge must be an actionable lower bound: %#v", plan.DockBadge)
	}
	if !hasAttentionSuppression(plan, AttentionSuppressedInsufficientFacts) {
		t.Fatalf("unknown Session was not explicit: %#v", plan.Suppressions)
	}

	noKnownAttention := attentionSnapshot(ObservationPartial, partialUnknown)
	badge := planner.Plan(attentionTestInput(attentionTestStart.Add(2*time.Second), noKnownAttention)).DockBadge
	if badge.Update || badge.Complete {
		t.Fatalf("zero from partial knowledge cleared an exact badge: %#v", badge)
	}
}

func TestAttentionNeverCallsPartialObservationAllDead(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	input := attentionTestInput(attentionTestStart, ObservationSnapshot{
		Availability: ObservationPartial,
		Sessions: []SessionObservation{{
			SessionID: "known-absent", Availability: ObservationAvailable,
			Presence: SessionPresenceAbsent, Status: StatusDead,
			Attention: AttentionNone, Occupancy: OccupancyVacant,
		}},
	})
	plan := planner.Plan(input)
	if plan.Observation != AttentionObservationPartial {
		t.Fatalf("partial Observation state = %q, want %q", plan.Observation, AttentionObservationPartial)
	}
}

func TestAttentionPreservesBreakCadenceAndEscalation(t *testing.T) {
	clock := attentionTestStart
	planner := NewAttentionPlanner(AttentionPlannerConfig{Now: func() time.Time { return clock }})
	input := AttentionInput{
		Observation: attentionSnapshot(ObservationAvailable),
		Break:       BreakAdvice{Enabled: true, Level: BreakLevelDue, GoodMoment: true},
	}

	first := planner.Plan(input)
	assertSingleAttentionKind(t, first, AttentionIntentBreakReminder)
	if first.NativeAttention != NativeAttentionInformational || first.BringToFront {
		t.Fatalf("first break reminder escalation = %#v", first)
	}
	clock = clock.Add(7 * time.Minute)
	early := planner.Plan(input)
	if len(early.Notifications) != 0 || !hasAttentionSuppression(early, AttentionSuppressedCadence) {
		t.Fatalf("break cadence fired early: %#v", early)
	}
	clock = clock.Add(time.Minute)
	second := planner.Plan(input)
	assertSingleAttentionKind(t, second, AttentionIntentBreakReminder)
	if second.NativeAttention != NativeAttentionCritical || second.BringToFront {
		t.Fatalf("second break reminder did not insist: %#v", second)
	}
	clock = clock.Add(8 * time.Minute)
	third := planner.Plan(input)
	assertSingleAttentionKind(t, third, AttentionIntentBreakReminder)
	if third.NativeAttention != NativeAttentionCritical || !third.BringToFront {
		t.Fatalf("third good-moment reminder did not escalate to front: %#v", third)
	}

	input.Break.Level = BreakLevelHint
	reset := planner.Plan(input)
	if reset.NativeAttention != NativeAttentionCancel || len(reset.Notifications) != 0 {
		t.Fatalf("resolved break did not cancel native attention: %#v", reset)
	}
	input.Break.Level = BreakLevelOverdue
	input.Break.GoodMoment = false
	overdue := planner.Plan(input)
	assertSingleAttentionKind(t, overdue, AttentionIntentBreakReminder)
	if overdue.NativeAttention != NativeAttentionCritical || overdue.BringToFront {
		t.Fatalf("overdue escalation = %#v", overdue)
	}
}

func TestAttentionDefersBreakForMeetingAndQuiet(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	input := attentionTestInput(attentionTestStart, attentionSnapshot(ObservationAvailable))
	input.Break = BreakAdvice{Enabled: true, Level: BreakLevelDue, GoodMoment: true}
	input.Quiet = AttentionQuietMeeting
	meeting := planner.Plan(input)
	if len(meeting.Notifications) != 0 || !hasAttentionSuppression(meeting, AttentionSuppressedMeeting) {
		t.Fatalf("meeting did not defer break: %#v", meeting)
	}
	input.Quiet = AttentionQuietNone
	input.Now = attentionTestStart.Add(time.Second)
	afterMeeting := planner.Plan(input)
	assertSingleAttentionKind(t, afterMeeting, AttentionIntentBreakReminder)

	quietPlanner := NewAttentionPlanner(AttentionPlannerConfig{})
	input.Now = attentionTestStart
	input.Quiet = AttentionQuietAll
	quiet := quietPlanner.Plan(input)
	if len(quiet.Notifications) != 0 || !hasAttentionSuppression(quiet, AttentionSuppressedQuiet) {
		t.Fatalf("quiet signal did not suppress intent: %#v", quiet)
	}
	input.Quiet = AttentionQuietNone
	input.Now = attentionTestStart.Add(time.Second)
	afterQuiet := quietPlanner.Plan(input)
	assertSingleAttentionKind(t, afterQuiet, AttentionIntentBreakReminder)
}

func TestAttentionPlansBreakEventsWithDedupeAndReset(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	input := attentionTestInput(attentionTestStart, attentionSnapshot(ObservationAvailable))
	input.Events = []AttentionEvent{{Key: "break:42", Kind: AttentionEventBreakFinished}}
	finished := planner.Plan(input)
	assertSingleAttentionKind(t, finished, AttentionIntentBreakFinished)
	intent := finished.Notifications[0]
	if intent.Title != "magentic" || intent.Message != "Pause vorbei — nichts drängt." || intent.Sound != "Purr" || intent.DedupeKey != "event:break:42" {
		t.Fatalf("break-finished intent = %#v", intent)
	}
	duplicate := planner.Plan(input)
	if len(duplicate.Notifications) != 0 || !hasAttentionSuppression(duplicate, AttentionSuppressedDuplicate) {
		t.Fatalf("break-finished event was not deduped: %#v", duplicate)
	}

	input.Events = nil
	input.Break = BreakAdvice{Enabled: true, Level: BreakLevelDue, GoodMoment: true}
	reminder := planner.Plan(input)
	if reminder.NativeAttention != NativeAttentionInformational {
		t.Fatalf("break reminder did not establish native attention: %#v", reminder)
	}
	reset := attentionTestInput(attentionTestStart.Add(time.Second), attentionSnapshot(ObservationAvailable))
	reset.Events = []AttentionEvent{{Kind: AttentionEventBreakReset}}
	resetPlan := planner.Plan(reset)
	if resetPlan.NativeAttention != NativeAttentionCancel || len(resetPlan.Notifications) != 0 {
		t.Fatalf("explicit break reset = %#v", resetPlan)
	}
}

func TestAttentionOwnsStartupNotificationPolicy(t *testing.T) {
	tests := []struct {
		name    string
		event   AttentionEvent
		kind    AttentionIntentKind
		message string
	}{
		{
			name: "one restored Session", event: AttentionEvent{Key: "startup", Kind: AttentionEventStartupRestored, Count: 1},
			kind: AttentionIntentStartupRestored, message: "1 Session wiederhergestellt",
		},
		{
			name: "multiple restored Sessions", event: AttentionEvent{Key: "startup", Kind: AttentionEventStartupRestored, Count: 3},
			kind: AttentionIntentStartupRestored, message: "3 Sessions wiederhergestellt",
		},
		{
			name: "restore failed", event: AttentionEvent{Key: "startup", Kind: AttentionEventStartupFailed},
			kind: AttentionIntentStartupFailed, message: "State konnte nicht geladen werden — Sessions wurden nicht wiederhergestellt",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planner := NewAttentionPlanner(AttentionPlannerConfig{})
			input := attentionTestInput(attentionTestStart, attentionSnapshot(ObservationUnavailable))
			input.Events = []AttentionEvent{test.event}
			plan := planner.Plan(input)
			assertSingleAttentionKind(t, plan, test.kind)
			if plan.Notifications[0].Message != test.message {
				t.Fatalf("message = %q, want %q", plan.Notifications[0].Message, test.message)
			}
			duplicate := planner.Plan(input)
			if len(duplicate.Notifications) != 0 || !hasAttentionSuppression(duplicate, AttentionSuppressedDuplicate) {
				t.Fatalf("startup event was not deduped: %#v", duplicate)
			}
		})
	}

	invalid := attentionTestInput(attentionTestStart, attentionSnapshot(ObservationUnavailable))
	invalid.Events = []AttentionEvent{{Kind: AttentionEventStartupRestored}}
	plan := NewAttentionPlanner(AttentionPlannerConfig{}).Plan(invalid)
	if len(plan.Notifications) != 0 || !hasAttentionSuppression(plan, AttentionSuppressedInsufficientFacts) {
		t.Fatalf("zero restored count should be suppressed: %#v", plan)
	}
}

func TestAttentionPrioritizesSessionInputOverBreakAndDeployment(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionWorking))))

	input := attentionTestInput(attentionTestStart.Add(time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionNeedsInput)))
	input.Break = BreakAdvice{Enabled: true, Level: BreakLevelDue, GoodMoment: true}
	input.Deployments = []AttentionDeploymentOutcome{
		{Key: "build-1:failed", Kind: AttentionDeploymentBuildFailed, Name: "api", Detail: "(main)"},
		{Key: "build-2:ready", Kind: AttentionDeploymentBuildReady, Name: "web", Detail: "✓ (main)"},
	}
	input.Events = []AttentionEvent{{Key: "break-finished:priority", Kind: AttentionEventBreakFinished}}
	plan := planner.Plan(input)
	assertSingleAttentionKind(t, plan, AttentionIntentNeedsInput)
	if plan.NativeAttention != NativeAttentionCritical || !hasAttentionSuppression(plan, AttentionSuppressedLowerPriority) {
		t.Fatalf("priority plan = %#v", plan)
	}

	// A priority-suppressed break has not consumed its cadence slot. Deployment
	// transitions, however, are deduped and are not replayed after the user acts.
	input.Now = attentionTestStart.Add(2 * time.Second)
	next := planner.Plan(input)
	assertSingleAttentionKind(t, next, AttentionIntentBreakReminder)
	if !hasAttentionSuppression(next, AttentionSuppressedDuplicate) {
		t.Fatalf("deployment transitions replayed or lost dedupe evidence: %#v", next)
	}
}

func TestAttentionQuietConsumesSessionTransitionWithoutLateNoise(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionWorking))))
	input := attentionTestInput(attentionTestStart.Add(time.Second), attentionSnapshot(ObservationAvailable, attentionObserved("one", AttentionNeedsInput)))
	input.Quiet = AttentionQuietAll
	quiet := planner.Plan(input)
	if len(quiet.Notifications) != 0 || !hasAttentionSuppression(quiet, AttentionSuppressedQuiet) {
		t.Fatalf("quiet transition = %#v", quiet)
	}
	input.Now = attentionTestStart.Add(2 * time.Second)
	input.Quiet = AttentionQuietNone
	late := planner.Plan(input)
	if len(late.Notifications) != 0 || !hasAttentionSuppression(late, AttentionSuppressedUnchanged) {
		t.Fatalf("quiet transition replayed after quiet ended: %#v", late)
	}
}

func attentionSnapshot(availability ObservationAvailability, sessions ...SessionObservation) ObservationSnapshot {
	return ObservationSnapshot{Availability: availability, Sessions: sessions, ObservedAt: attentionTestStart}
}

func attentionObserved(id SessionID, attention AttentionState) SessionObservation {
	status := StatusIdle
	switch attention {
	case AttentionWorking:
		status = StatusRunning
	case AttentionNeedsInput:
		status = StatusBlocked
	case AttentionNone:
		status = StatusDead
	}
	return SessionObservation{
		SessionID: id, Availability: ObservationAvailable, Presence: SessionPresencePresent,
		Status: status, Attention: attention,
	}
}

func attentionAbsent(id SessionID) SessionObservation {
	return SessionObservation{
		SessionID: id, Availability: ObservationAvailable, Presence: SessionPresenceAbsent,
		Status: StatusDead, Attention: AttentionNone,
	}
}

func attentionTestInput(now time.Time, snapshot ObservationSnapshot) AttentionInput {
	return AttentionInput{Now: now, Observation: snapshot}
}

func hasAttentionSuppression(plan AttentionPlan, reason AttentionSuppressionReason) bool {
	for _, suppression := range plan.Suppressions {
		if suppression.Reason == reason {
			return true
		}
	}
	return false
}

func assertSingleAttentionKind(t *testing.T, plan AttentionPlan, kind AttentionIntentKind) {
	t.Helper()
	if len(plan.Notifications) != 1 || plan.Notifications[0].Kind != kind {
		t.Fatalf("notifications = %#v, want one %s", plan.Notifications, kind)
	}
}

// TestMutedQuietSignalSilencesEveryIntentAndSaysWhy hält fest, dass
// ausgeschaltete Benachrichtigungen im Plan wirken und als AttentionSuppression
// verbucht werden. Vorher beschnitt der Desktop-Adapter den fertigen Plan, die
// TUI tat es nicht, und keine der beiden Oberflächen verbuchte die
// Unterdrückung.
func TestMutedQuietSignalSilencesEveryIntentAndSaysWhy(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	now := time.Now()
	labels := map[SessionID]string{"s1": "hera"}
	// Der Planner bestätigt einen Übergang, bevor er ihn meldet: erst laufend,
	// dann blockiert ergibt eine Absicht, die stumm unterdrückt werden kann.
	planner.Plan(AttentionInput{
		Observation: mutedTestSnapshot(StatusRunning), SessionLabels: labels,
		Quiet: AttentionQuietMuted, Now: now,
	})
	plan := planner.Plan(AttentionInput{
		Observation: mutedTestSnapshot(StatusBlocked), SessionLabels: labels,
		Quiet: AttentionQuietMuted, Now: now.Add(time.Second),
	})
	if len(plan.Notifications) != 0 {
		t.Errorf("stummer Plan trägt %d Benachrichtigungen", len(plan.Notifications))
	}
	if plan.BringToFront {
		t.Error("stummer Plan holt das Fenster nach vorn")
	}
	if plan.NativeAttention == NativeAttentionInformational || plan.NativeAttention == NativeAttentionCritical {
		t.Errorf("stummer Plan fordert native Attention: %v", plan.NativeAttention)
	}

	var muted bool
	for _, suppression := range plan.Suppressions {
		if suppression.Reason == AttentionSuppressedMuted {
			muted = true
		}
	}
	if !muted {
		t.Errorf("keine Unterdrückung mit Grund %q verbucht: %+v", AttentionSuppressedMuted, plan.Suppressions)
	}
}

// TestMutedQuietSignalKeepsBadgeAndInbox hält die andere Hälfte fest: stumm
// heißt nicht blind. Badge und Posteingang räumen weiter auf.
func TestMutedQuietSignalKeepsBadgeAndInbox(t *testing.T) {
	loud := NewAttentionPlanner(AttentionPlannerConfig{})
	quiet := NewAttentionPlanner(AttentionPlannerConfig{})
	now := time.Now()
	labels := map[SessionID]string{"s1": "hera"}
	running := mutedTestSnapshot(StatusRunning)
	blocked := mutedTestSnapshot(StatusBlocked)

	loud.Plan(AttentionInput{Observation: running, SessionLabels: labels, Now: now})
	quiet.Plan(AttentionInput{
		Observation: running, SessionLabels: labels, Quiet: AttentionQuietMuted, Now: now,
	})
	loudPlan := loud.Plan(AttentionInput{
		Observation: blocked, SessionLabels: labels, Now: now.Add(time.Second),
	})
	quietPlan := quiet.Plan(AttentionInput{
		Observation: blocked, SessionLabels: labels, Quiet: AttentionQuietMuted, Now: now.Add(time.Second),
	})

	if quietPlan.DockBadge != loudPlan.DockBadge {
		t.Errorf("Badge unterscheidet sich: stumm %+v, laut %+v", quietPlan.DockBadge, loudPlan.DockBadge)
	}
	if len(quietPlan.Inbox.Entries) != len(loudPlan.Inbox.Entries) {
		t.Errorf("Posteingang unterscheidet sich: stumm %d, laut %d Einträge",
			len(quietPlan.Inbox.Entries), len(loudPlan.Inbox.Entries))
	}
}

// mutedTestSnapshot baut eine Beobachtung einer Session mit dem gegebenen Status.
func mutedTestSnapshot(status AgentStatus) ObservationSnapshot {
	return ObservationSnapshot{
		Availability: ObservationAvailable,
		Sessions: []SessionObservation{{
			SessionID: "s1", Presence: SessionPresencePresent,
			Availability: ObservationAvailable, ContentKnown: true, Status: status,
		}},
	}
}
