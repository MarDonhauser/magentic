package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// controlWait is one pending wait. It carries the occupant identity pinned at
// resolution and never re-resolves the address: a Session that merely carries
// the same name again is a stranger, not the awaited work.
type controlWait struct {
	pinned    ControlOccupant
	name      string
	projectID ProjectID
	until     ControlOutcome
	// last is the most recent status actually observed for the pinned
	// occupant, reported when the wait times out.
	last ControlStatus
}

// resolveControlOccupant pins the triple (SessionID, RuntimeName, AgentRunRef)
// of the run occupying a Session. SessionID alone survives a runtime being
// recreated, RuntimeName alone is replaceable by design, and an AgentRunRef
// alone is vendor-scoped — together they answer whether the thing being awaited
// is still the thing that is there (ADR 0001).
func resolveControlOccupant(session Session) (ControlOccupant, *controlError) {
	if session.IsTerm() || session.SessionKind == SessionKindTerminal {
		return ControlOccupant{}, controlRefusal(ControlWaitNoOccupant,
			"%s ist eine Terminal-Session — dort läuft kein Coding-Agent, auf den gewartet werden könnte.", session.Name)
	}
	if strings.TrimSpace(session.RuntimeName) == "" {
		return ControlOccupant{}, controlRefusal(ControlWaitNoOccupant,
			"%s hat keinen adressierbaren Runtime, auf den gewartet werden könnte.", session.Name)
	}
	run, ok := session.AgentRun(session.SessionVendor())
	if !ok || strings.TrimSpace(run.ExternalID) == "" {
		return ControlOccupant{}, controlRefusal(ControlWaitNoOccupant,
			"%s führt gerade keinen auflösbaren Coding-Agent-Lauf.", session.Name)
	}
	return ControlOccupant{SessionID: session.ID, RuntimeName: session.RuntimeName, Run: run}, nil
}

// controlWaitCondition validates the requested condition. An empty condition
// means `done`.
func controlWaitCondition(until string) (ControlOutcome, *controlError) {
	switch strings.TrimSpace(until) {
	case "", string(ControlWaitDone):
		return ControlWaitDone, nil
	case string(ControlWaitWaiting):
		return ControlWaitWaiting, nil
	}
	return "", controlRefusal(ControlInvalidRequest,
		"Unbekannte Wartebedingung %q — möglich sind %q und %q.", until, ControlWaitDone, ControlWaitWaiting)
}

// controlWaitVerdict is one evaluation of a pending wait. Ended is false while
// nothing terminal has been observed.
type controlWaitVerdict struct {
	Ended    bool
	Outcome  ControlOutcome
	Observed *ControlOccupant
}

// evaluate compares one Registry state and one Observation against the pinned
// identity. It is the only place a wait may end with a result.
func (w *controlWait) evaluate(state State, observed SessionObservation, hasObservation bool) controlWaitVerdict {
	session := state.SessionByID(w.pinned.SessionID)
	if session == nil {
		// The pinned Session is gone. If its name is carried again in the same
		// Project, a stranger took its place; the wait must never resolve to it.
		for _, candidate := range state.Agents {
			if candidate.ID != w.pinned.SessionID && candidate.Name == w.name && candidate.ProjectID == w.projectID {
				replacement := ControlOccupant{SessionID: candidate.ID, RuntimeName: candidate.RuntimeName}
				if run, ok := candidate.AgentRun(candidate.SessionVendor()); ok {
					replacement.Run = run
				}
				return controlWaitVerdict{Ended: true, Outcome: ControlWaitOccupantReplaced, Observed: &replacement}
			}
		}
		return controlWaitVerdict{Ended: true, Outcome: ControlWaitSessionGone}
	}
	current := ControlOccupant{SessionID: session.ID, RuntimeName: session.RuntimeName}
	if run, ok := session.AgentRun(session.SessionVendor()); ok {
		current.Run = run
	}
	if !current.Same(w.pinned) {
		observedCopy := current
		return controlWaitVerdict{Ended: true, Outcome: ControlWaitOccupantReplaced, Observed: &observedCopy}
	}
	if !hasObservation {
		return controlWaitVerdict{}
	}
	// An unavailable or partial reading is never a result: a tmux read that
	// timed out must not be reported as an idle agent (ADR 0004).
	if observed.Availability != ObservationAvailable {
		return controlWaitVerdict{}
	}
	status := controlStatus(observed.Status)
	w.last = status
	if observed.Presence == SessionPresenceAbsent || status == ControlStatusDead || status == ControlStatusExited {
		return controlWaitVerdict{Ended: true, Outcome: ControlWaitSessionGone}
	}
	switch w.until {
	case ControlWaitDone:
		switch status {
		case ControlStatusIdle, ControlStatusDone:
			return controlWaitVerdict{Ended: true, Outcome: ControlWaitDone}
		case ControlStatusWaiting:
			// The common failure of unattended delegation is a permission
			// prompt. Absorbing it silently would make the verb untrustworthy.
			return controlWaitVerdict{Ended: true, Outcome: ControlWaitBlocked}
		}
	case ControlWaitWaiting:
		if status == ControlStatusWaiting {
			return controlWaitVerdict{Ended: true, Outcome: ControlWaitWaiting}
		}
	}
	return controlWaitVerdict{}
}

// sessionWait blocks until the pinned occupant reaches the requested condition
// or another terminal outcome applies. It holds no Registry coordination and no
// Session transition: it only reads a snapshot per evaluation.
func (s *ControlService) sessionWait(ctx context.Context, request ControlRequest) ControlResponse {
	state, failure := s.state(ctx)
	if failure != nil {
		return failure.response(request.ID)
	}
	session, failure := resolveControlSession(state, request.Args)
	if failure != nil {
		return failure.response(request.ID)
	}
	until, failure := controlWaitCondition(request.Args.Until)
	if failure != nil {
		return failure.response(request.ID)
	}
	pinned, failure := resolveControlOccupant(session)
	if failure != nil {
		return failure.response(request.ID)
	}
	wait := &controlWait{pinned: pinned, name: session.Name, projectID: session.ProjectID, until: until}

	var timeout <-chan time.Time
	if request.Args.TimeoutMS > 0 {
		timer := time.NewTimer(time.Duration(request.Args.TimeoutMS) * time.Millisecond)
		defer timer.Stop()
		timeout = timer.C
	}
	passes, release := s.waitObservations(ctx, session)
	defer release()

	// The first evaluation reads the runtime once, so a condition that already
	// holds answers immediately. Every later evaluation is driven by an
	// observation pass that happened anyway.
	evaluate := func(snapshot ObservationSnapshot) (ControlResponse, bool) {
		fresh, stateFailure := s.state(ctx)
		if stateFailure != nil {
			return stateFailure.response(request.ID), true
		}
		observed, hasObservation := controlObservationFor(snapshot, pinned.SessionID)
		if verdict := wait.evaluate(fresh, observed, hasObservation); verdict.Ended {
			return wait.response(request.ID, verdict), true
		}
		return ControlResponse{}, false
	}
	if response, ended := evaluate(s.observe(ctx, []Session{session})); ended {
		return response
	}
	for {
		select {
		case <-ctx.Done():
			return wait.response(request.ID, controlWaitVerdict{Ended: true, Outcome: ControlWaitCancelled})
		case <-timeout:
			return wait.response(request.ID, controlWaitVerdict{Ended: true, Outcome: ControlWaitTimeout})
		case snapshot, open := <-passes:
			if !open {
				return wait.response(request.ID, controlWaitVerdict{Ended: true, Outcome: ControlWaitCancelled})
			}
			if response, ended := evaluate(snapshot); ended {
				return response
			}
		}
	}
}

// waitObservations yields the observation passes a pending wait is evaluated
// from. Production feeds it from the event fan-out; without one, the wait falls
// back to its own paced re-reading of the addressed runtime.
func (s *ControlService) waitObservations(ctx context.Context, session Session) (<-chan ObservationSnapshot, func()) {
	if s.observations != nil {
		return s.observations(ctx, session)
	}
	passes := make(chan ObservationSnapshot)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(controlWaitInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				close(passes)
				return
			case <-ticker.C:
				snapshot := s.observe(ctx, []Session{session})
				select {
				case passes <- snapshot:
				case <-done:
					return
				case <-ctx.Done():
					close(passes)
					return
				}
			}
		}
	}()
	return passes, func() { close(done) }
}

// controlWaitInterval paces the fallback re-reading. It is deliberately not a
// setting: a wait without a serving process is the exception, not the norm.
const controlWaitInterval = 2 * time.Second

func controlObservationFor(snapshot ObservationSnapshot, id SessionID) (SessionObservation, bool) {
	for _, observed := range snapshot.Sessions {
		if observed.SessionID == id {
			return observed, true
		}
	}
	return SessionObservation{}, false
}

func (w *controlWait) response(id string, verdict controlWaitVerdict) ControlResponse {
	pinned := w.pinned
	result := &ControlResult{SessionID: w.pinned.SessionID, Occupant: &pinned, Status: w.last, Observed: verdict.Observed}
	return ControlResponse{
		ID: id, Outcome: verdict.Outcome, Result: result,
		Message: controlWaitMessage(w, verdict),
	}
}

func controlWaitMessage(wait *controlWait, verdict controlWaitVerdict) string {
	switch verdict.Outcome {
	case ControlWaitDone:
		return fmt.Sprintf("%s ist fertig und wartet auf einen neuen Prompt.", wait.name)
	case ControlWaitWaiting:
		return fmt.Sprintf("%s braucht eine Antwort.", wait.name)
	case ControlWaitBlocked:
		return fmt.Sprintf("%s hängt an einer Rückfrage und muss von Hand entsperrt werden.", wait.name)
	case ControlWaitOccupantReplaced:
		observed := "einer anderen Belegung"
		if verdict.Observed != nil {
			observed = fmt.Sprintf("%s/%s", verdict.Observed.RuntimeName, verdict.Observed.Run.ExternalID)
		}
		return fmt.Sprintf(
			"Die gepinnte Belegung %s/%s wurde durch %s ersetzt.",
			wait.pinned.RuntimeName, wait.pinned.Run.ExternalID, observed)
	case ControlWaitSessionGone:
		return fmt.Sprintf("Der Runtime von %s existiert nicht mehr.", wait.name)
	case ControlWaitTimeout:
		return fmt.Sprintf("Die Wartezeit lief ab, zuletzt beobachtet: %s.", controlStatusOrUnknown(wait.last))
	case ControlWaitCancelled:
		return "Das Warten wurde abgebrochen."
	}
	return ""
}

func controlStatusOrUnknown(status ControlStatus) ControlStatus {
	if status == "" {
		return ControlStatusUnknown
	}
	return status
}
