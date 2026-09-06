package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ManagedStateProvider reads one managed Session's agent-host state. It
// reports ErrManagedHostUnreachable when the host cannot be reached, so the
// caller reports the Session as unobservable with the daemon named — never as
// dead. Anything the token does not confirm is foreign and is treated the
// same way: neither adopted nor killed.
type ManagedStateProvider func(ctx context.Context, session Session) (AgentHostState, error)

// ObserveSessions is the production observation pass: tmux Sessions are read
// from tmux, managed Sessions from their agent hosts. The host records are
// read once per pass rather than once per managed Session; each host is still
// dialed individually. No tmux command is issued for a managed Session, and
// an unreachable daemon reads as unobservable, never as dead.
func ObserveSessions(ctx context.Context, sessions []Session) ObservationSnapshot {
	records, err := NewManagedHostRegistry().Records()
	endpoints := make(map[SessionID]ManagedHostRecord, len(records))
	if err == nil {
		for _, record := range records {
			endpoints[record.SessionID] = record
		}
	}
	return ObserveWithManaged(ctx, sessions, Observe, func(_ context.Context, session Session) (AgentHostState, error) {
		if err != nil {
			return AgentHostState{}, fmtManagedHostUnreachable(session, err)
		}
		record, ok := endpoints[session.ID]
		if !ok {
			return AgentHostState{}, fmtManagedHostUnreachable(session, errors.New("kein Host verzeichnet"))
		}
		state, dialErr := QueryAgentHostState(record.SocketPath, record.Token)
		if dialErr != nil {
			return AgentHostState{}, fmtManagedHostUnreachable(session, dialErr)
		}
		return state, nil
	})
}

// ObserveWithManaged reads tmux Sessions from tmux and managed Sessions from
// their agent hosts. No tmux command is issued for a managed Session. Sessions
// whose runtime is neither tmux nor managed are observed the tmux way, so an
// unknown runtime never silently becomes managed.
func ObserveWithManaged(ctx context.Context, sessions []Session, tmuxObserve observationReader, provider ManagedStateProvider) ObservationSnapshot {
	if tmuxObserve == nil {
		tmuxObserve = Observe
	}
	var tmuxSessions, managedSessions []Session
	for _, session := range sessions {
		if session.SessionRuntime() == RuntimeManaged {
			managedSessions = append(managedSessions, session)
		} else {
			tmuxSessions = append(tmuxSessions, session)
		}
	}
	var merged ObservationSnapshot
	merged.ObservedAt = time.Now().UTC()
	merged.Availability = ObservationAvailable
	byID := map[SessionID]SessionObservation{}

	if len(tmuxSessions) > 0 {
		tmuxSnapshot := tmuxObserve(ctx, tmuxSessions)
		merged.ObservedAt = tmuxSnapshot.ObservedAt
		merged.Problems = append(merged.Problems, tmuxSnapshot.Problems...)
		if tmuxSnapshot.Availability != ObservationAvailable {
			merged.Availability = ObservationPartial
		}
		for _, observed := range tmuxSnapshot.Sessions {
			byID[observed.SessionID] = observed
		}
	}
	for _, session := range managedSessions {
		observed := observeManagedSession(ctx, session, provider)
		if observed.Availability != ObservationAvailable {
			merged.Availability = ObservationPartial
		}
		byID[session.ID] = observed
	}
	merged.Sessions = make([]SessionObservation, 0, len(sessions))
	for _, session := range sessions {
		if observed, ok := byID[session.ID]; ok {
			merged.Sessions = append(merged.Sessions, observed)
			continue
		}
		merged.Sessions = append(merged.Sessions, SessionObservation{
			SessionID: session.ID, Availability: ObservationUnavailable,
			Presence: SessionPresenceUnknown, Status: StatusUnknown,
			Attention: AttentionUnknown, WorktreePath: session.Dir, Worktree: session.Worktree,
			Occupancy: OccupancyUnknown,
		})
		merged.Availability = ObservationPartial
	}
	if len(sessions) == 0 {
		merged.Availability = ObservationAvailable
	}
	sortObservationProblems(merged.Problems)
	return merged
}

// observeManagedSession derives one managed Session's reading from daemon
// facts and protocol events only: host liveness, the running or last turn,
// and the open permission requests. Terminal content is never scraped.
func observeManagedSession(ctx context.Context, session Session, provider ManagedStateProvider) SessionObservation {
	if provider == nil {
		return unobservableManagedSession(session)
	}
	state, err := provider(ctx, session)
	if err != nil {
		return unobservableManagedSession(session)
	}
	observed := SessionObservation{
		SessionID: session.ID, WorktreePath: session.Dir, Worktree: session.Worktree,
	}
	observed.Availability = ObservationAvailable
	if state.Alive {
		observed.Presence = SessionPresencePresent
		observed.Occupancy = OccupancyOccupied
	} else {
		observed.Presence = SessionPresenceAbsent
		observed.Occupancy = OccupancyVacant
	}
	observed.Tool = string(session.Vendor)
	status, source, detail, activity, known := managedStatusFromHostState(session, state)
	observed.Status = status
	observed.StatusSource = source
	observed.Detail = detail
	observed.Activity = activity
	observed.ActivityKnown = known
	observed.Attention = observationAttention(status)
	observed.Unread = observationUnread(status, session.SeenAt, activity, known)
	return observed
}

// managedStatusFromHostState applies the one precedence a managed Session
// has: an open permission request first, then liveness, then the turn. A long
// silent stretch changes nothing — only protocol facts end a turn.
func managedStatusFromHostState(session Session, state AgentHostState) (AgentStatus, StatusSource, string, time.Time, bool) {
	if len(state.OpenPermissions) > 0 {
		open := state.OpenPermissions[0]
		return StatusAwaitingDecision, StatusSourceSnapshot, open.Asked, open.RaisedAt, true
	}
	if !state.Alive {
		reason := state.ExitReason
		if reason == "" {
			reason = "der Agent-Prozess ist beendet"
		}
		var at time.Time
		var known bool
		if state.TurnKnown {
			at, known = state.Turn.EndedAt, !state.Turn.EndedAt.IsZero()
		}
		return StatusExited, StatusSourcePresence, reason, at, known
	}
	if state.TurnKnown && state.Turn.Running {
		at := state.Turn.StartedAt
		return StatusRunning, StatusSourceSnapshot, "", at, !at.IsZero()
	}
	if state.TurnKnown && !state.Turn.Running && !state.Turn.EndedAt.IsZero() {
		switch state.Turn.EndReason {
		case TurnEndCompleted:
			return StatusDone, StatusSourceSnapshot, "", state.Turn.EndedAt, true
		case TurnEndInterrupted:
			return StatusIdle, StatusSourceSnapshot, "Turn unterbrochen", state.Turn.EndedAt, true
		case TurnEndFailed:
			detail := state.Turn.FailReason
			if detail == "" {
				detail = "der Turn scheiterte"
			}
			return StatusIdle, StatusSourceSnapshot, detail, state.Turn.EndedAt, true
		}
	}
	return StatusIdle, StatusSourceSnapshot, "", time.Time{}, false
}

// unobservableManagedSession reports a managed Session whose host cannot be
// read as unobservable with the daemon named — never as dead and never with
// a plausible-looking status (ADR 0004).
func unobservableManagedSession(session Session) SessionObservation {
	return SessionObservation{
		SessionID: session.ID, Availability: ObservationUnavailable,
		Presence: SessionPresenceUnknown, Status: StatusUnknown,
		Attention: AttentionUnknown, WorktreePath: session.Dir, Worktree: session.Worktree,
		Occupancy: OccupancyUnknown,
	}
}

// DefaultManagedStateProvider reads a managed Session's host through the
// durably recorded socket path and token. A missing record, an unreachable
// socket, or a foreign answer all read as unreachable: the Session is
// unobservable with the daemon named, never dead.
func DefaultManagedStateProvider(ctx context.Context, session Session) (AgentHostState, error) {
	_ = ctx
	socketPath, token, err := ManagedHostEndpoint(session.ID)
	if err != nil {
		return AgentHostState{}, fmtManagedHostUnreachable(session, err)
	}
	state, err := QueryAgentHostState(socketPath, token)
	if err != nil {
		return AgentHostState{}, fmtManagedHostUnreachable(session, err)
	}
	return state, nil
}

func fmtManagedHostUnreachable(session Session, err error) error {
	return fmt.Errorf("%w: Session %q: %v", ErrManagedHostUnreachable, session.ID, err)
}
