package core

import (
	"testing"
	"time"
)

func TestAttentionInboxStampsTheWaitAtTheObservedChange(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionWorking),
	)))

	blocked := attentionTestStart.Add(30 * time.Second)
	plan := planner.Plan(attentionTestInput(blocked, attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionNeedsInput),
	)))

	entry := onlyInboxEntry(t, plan)
	if entry.SessionID != "one" || entry.Kind != AttentionWaitingInput {
		t.Fatalf("entry = %#v", entry)
	}
	if !entry.WaitingSinceKnown || !entry.WaitingSince.Equal(blocked) {
		t.Fatalf("wait start = %v (known %t), want the moment of the change %v",
			entry.WaitingSince, entry.WaitingSinceKnown, blocked)
	}

	// A later cycle without a change keeps the original start.
	later := planner.Plan(attentionTestInput(blocked.Add(2*time.Minute), attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionNeedsInput),
	)))
	if kept := onlyInboxEntry(t, later); !kept.WaitingSince.Equal(blocked) || !kept.WaitingSinceKnown {
		t.Fatalf("unchanged wait was re-stamped: %#v", kept)
	}
}

func TestAttentionInboxReportsAWaitFoundAtStartupAsLowerBound(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	plan := planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionNeedsInput),
	)))

	entry := onlyInboxEntry(t, plan)
	if entry.WaitingSinceKnown {
		t.Fatalf("a wait that predates the planner was reported as a fresh wait: %#v", entry)
	}
	if !entry.WaitingSince.Equal(attentionTestStart) {
		t.Fatalf("lower bound = %v, want the first cycle %v", entry.WaitingSince, attentionTestStart)
	}
	if len(plan.Notifications) != 0 || !hasAttentionSuppression(plan, AttentionSuppressedInitialState) {
		t.Fatalf("initial state was noisy: %#v", plan)
	}

	// The lower bound survives the cycles that follow it.
	next := planner.Plan(attentionTestInput(attentionTestStart.Add(time.Minute), attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionNeedsInput),
	)))
	if kept := onlyInboxEntry(t, next); kept.WaitingSinceKnown {
		t.Fatalf("lower bound turned into a known start: %#v", kept)
	}
}

func TestAttentionInboxRestartsTheWaitWhenTheKindChanges(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionWorking),
	)))
	blocked := attentionTestStart.Add(time.Minute)
	planner.Plan(attentionTestInput(blocked, attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionNeedsInput),
	)))

	reviewed := blocked.Add(5 * time.Minute)
	plan := planner.Plan(attentionTestInput(reviewed, attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionReview),
	)))

	entry := onlyInboxEntry(t, plan)
	if entry.Kind != AttentionWaitingReview {
		t.Fatalf("waiting kind = %q, want review", entry.Kind)
	}
	if !entry.WaitingSinceKnown || !entry.WaitingSince.Equal(reviewed) {
		t.Fatalf("changed kind did not restart the wait: %#v", entry)
	}
}

func TestAttentionInboxSkipsSessionsWithoutSufficientFacts(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	plan := planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable,
		SessionObservation{
			SessionID: "unreadable", Availability: ObservationUnavailable,
			Presence: SessionPresenceUnknown, Attention: AttentionUnknown,
		},
		SessionObservation{
			SessionID: "unknown-attention", Availability: ObservationAvailable,
			Presence: SessionPresencePresent, Status: StatusUnknown, Attention: AttentionUnknown,
		},
	)))

	if len(plan.Inbox.Entries) != 0 {
		t.Fatalf("Sessions without facts were listed as waiting: %#v", plan.Inbox.Entries)
	}
	if plan.Inbox.State != AttentionInboxIncomplete {
		t.Fatalf("inbox state = %q, want incomplete — the missing Sessions are not a claim that they are not waiting", plan.Inbox.State)
	}
	if !hasAttentionSuppression(plan, AttentionSuppressedInsufficientFacts) {
		t.Fatalf("missing facts were not surfaced: %#v", plan.Suppressions)
	}
}

func TestAttentionInboxOrdersLongestWaitFirstAndStaysStable(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	// "startup" is already waiting when the planner starts, "early" and "late"
	// enter their wait under observation, "tie-a" and "tie-b" at the same moment.
	planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable,
		attentionObserved("startup", AttentionNeedsInput),
		attentionObserved("early", AttentionWorking),
		attentionObserved("late", AttentionWorking),
		attentionObserved("tie-b", AttentionWorking),
		attentionObserved("tie-a", AttentionWorking),
	)))
	planner.Plan(attentionTestInput(attentionTestStart.Add(time.Minute), attentionSnapshot(
		ObservationAvailable,
		attentionObserved("startup", AttentionNeedsInput),
		attentionObserved("early", AttentionNeedsInput),
		attentionObserved("late", AttentionWorking),
		attentionObserved("tie-b", AttentionWorking),
		attentionObserved("tie-a", AttentionWorking),
	)))
	waiting := attentionSnapshot(
		ObservationAvailable,
		attentionObserved("startup", AttentionNeedsInput),
		attentionObserved("early", AttentionNeedsInput),
		attentionObserved("late", AttentionNeedsInput),
		attentionObserved("tie-b", AttentionNeedsInput),
		attentionObserved("tie-a", AttentionNeedsInput),
	)
	first := planner.Plan(attentionTestInput(attentionTestStart.Add(10*time.Minute), waiting))

	want := []SessionID{"startup", "early", "late", "tie-a", "tie-b"}
	if got := inboxOrder(first); !sessionIDsEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	second := planner.Plan(attentionTestInput(attentionTestStart.Add(11*time.Minute), waiting))
	if got := inboxOrder(second); !sessionIDsEqual(got, want) {
		t.Fatalf("unchanged facts produced a different order: %v", got)
	}
}

func TestAttentionInboxStatesHowMuchOfTheListIsKnown(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	complete := planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable, attentionObserved("one", AttentionNeedsInput),
	)))
	if complete.Inbox.State != AttentionInboxComplete || len(complete.Inbox.Entries) != 1 {
		t.Fatalf("available Observation = %#v", complete.Inbox)
	}

	partial := planner.Plan(AttentionInput{
		Now: attentionTestStart.Add(time.Minute),
		Observation: attentionSnapshot(
			ObservationPartial, attentionObserved("one", AttentionNeedsInput),
		),
	})
	if partial.Inbox.State != AttentionInboxIncomplete {
		t.Fatalf("partial Observation state = %q, want incomplete", partial.Inbox.State)
	}
	if len(partial.Inbox.Entries) != 1 || partial.Inbox.Entries[0].SessionID != "one" {
		t.Fatalf("partial Observation dropped the Sessions it does know: %#v", partial.Inbox.Entries)
	}

	unavailable := planner.Plan(AttentionInput{
		Now:         attentionTestStart.Add(2 * time.Minute),
		Observation: ObservationSnapshot{Availability: ObservationUnavailable},
	})
	if unavailable.Inbox.State != AttentionInboxUnavailable {
		t.Fatalf("unavailable Observation state = %q", unavailable.Inbox.State)
	}
	if len(unavailable.Inbox.Entries) != 0 {
		t.Fatalf("unavailable Observation produced entries: %#v", unavailable.Inbox.Entries)
	}
	if unavailable.Inbox.State == AttentionInboxComplete {
		t.Fatal("unavailable Observation produced an empty-but-complete inbox")
	}
}

func TestAttentionInboxAgreesWithTheBadgeAndTheNotificationsOfTheSameCycle(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable,
		attentionObserved("one", AttentionWorking),
		attentionObserved("two", AttentionWorking),
	)))
	plan := planner.Plan(attentionTestInput(attentionTestStart.Add(time.Minute), attentionSnapshot(
		ObservationAvailable,
		attentionObserved("one", AttentionNeedsInput),
		attentionObserved("two", AttentionWorking),
	)))

	blocked := 0
	for _, entry := range plan.Inbox.Entries {
		if entry.Kind == AttentionWaitingInput {
			blocked++
		}
	}
	if !plan.DockBadge.Update || !plan.DockBadge.Complete || plan.DockBadge.Count != blocked {
		t.Fatalf("badge %#v disagrees with %d blocked inbox entries", plan.DockBadge, blocked)
	}
	assertSingleAttentionKind(t, plan, AttentionIntentNeedsInput)
	if !inboxLists(plan, plan.Notifications[0].SessionID) {
		t.Fatalf("notified Session %q is missing from the inbox of the same cycle: %#v",
			plan.Notifications[0].SessionID, plan.Inbox.Entries)
	}
}

func TestAttentionInboxCarriesTheContentTailAndSaysWhenItIsUnknown(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	known := attentionObserved("one", AttentionNeedsInput)
	known.Content = "npm install ausgeführt\n\nDarf ich die Datei schreiben?\n❯ 1. Ja\n\n"
	known.ContentKnown = true
	known.StatusSource = StatusSourceHook
	unknown := attentionObserved("two", AttentionNeedsInput)
	unknown.Content = ""

	plan := planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(ObservationAvailable, known, unknown)))

	entries := map[SessionID]AttentionInboxEntry{}
	for _, entry := range plan.Inbox.Entries {
		entries[entry.SessionID] = entry
	}
	first := entries["one"]
	if !first.ExcerptKnown || first.Excerpt != "npm install ausgeführt\n\nDarf ich die Datei schreiben?\n❯ 1. Ja" {
		t.Fatalf("content tail = %q (known %t)", first.Excerpt, first.ExcerptKnown)
	}
	if first.StatusSource != StatusSourceHook {
		t.Fatalf("status source = %q, want the source the Observation reported", first.StatusSource)
	}
	second := entries["two"]
	if second.ExcerptKnown || second.Excerpt != "" {
		t.Fatalf("unknown content was reported as a reason: %#v", second)
	}
}

func TestAttentionInboxClearsWhenTheSessionMovesOn(t *testing.T) {
	planner := NewAttentionPlanner(AttentionPlannerConfig{})
	planner.Plan(attentionTestInput(attentionTestStart, attentionSnapshot(
		ObservationAvailable,
		attentionObserved("resumes", AttentionWorking),
		attentionObserved("vanishes", AttentionWorking),
		attentionObserved("switches", AttentionWorking),
	)))
	blocked := attentionTestStart.Add(time.Minute)
	waiting := planner.Plan(attentionTestInput(blocked, attentionSnapshot(
		ObservationAvailable,
		attentionObserved("resumes", AttentionNeedsInput),
		attentionObserved("vanishes", AttentionNeedsInput),
		attentionObserved("switches", AttentionNeedsInput),
	)))
	if len(waiting.Inbox.Entries) != 3 {
		t.Fatalf("entries = %#v, want three waiting Sessions", waiting.Inbox.Entries)
	}

	moved := blocked.Add(time.Minute)
	plan := planner.Plan(attentionTestInput(moved, attentionSnapshot(
		ObservationAvailable,
		attentionObserved("resumes", AttentionWorking),
		attentionAbsent("vanishes"),
		attentionObserved("switches", AttentionReview),
	)))

	if inboxLists(plan, "resumes") {
		t.Fatalf("a Session that resumed work stayed in the inbox: %#v", plan.Inbox.Entries)
	}
	if inboxLists(plan, "vanishes") {
		t.Fatalf("an absent runtime stayed in the inbox: %#v", plan.Inbox.Entries)
	}
	switched := onlyInboxEntry(t, plan)
	if switched.SessionID != "switches" || switched.Kind != AttentionWaitingReview {
		t.Fatalf("entry = %#v, want one review entry for switches", switched)
	}
	if !switched.WaitingSinceKnown || !switched.WaitingSince.Equal(moved) {
		t.Fatalf("the new waiting kind kept the old wait: %#v", switched)
	}
}

func onlyInboxEntry(t *testing.T, plan AttentionPlan) AttentionInboxEntry {
	t.Helper()
	if len(plan.Inbox.Entries) != 1 {
		t.Fatalf("inbox = %#v, want exactly one entry", plan.Inbox.Entries)
	}
	return plan.Inbox.Entries[0]
}

func inboxOrder(plan AttentionPlan) []SessionID {
	order := make([]SessionID, 0, len(plan.Inbox.Entries))
	for _, entry := range plan.Inbox.Entries {
		order = append(order, entry.SessionID)
	}
	return order
}

func inboxLists(plan AttentionPlan, id SessionID) bool {
	for _, entry := range plan.Inbox.Entries {
		if entry.SessionID == id {
			return true
		}
	}
	return false
}

func sessionIDsEqual(got, want []SessionID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
