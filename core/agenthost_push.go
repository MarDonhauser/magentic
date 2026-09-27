package core

import (
	"context"
	"sync"
	"time"
)

// hostPushWatchTimeout bounds one long-poll round trip. It must stay well
// under agentHostWatchMaxTimeout's server-side ceiling so the client's own
// deadline never fires first.
const hostPushWatchTimeout = 25 * time.Second

// hostPushRetryBackoff bounds how long a push loop waits after a failed
// lookup or watch call before trying again, so a Session whose host is
// briefly unreachable does not spin.
const hostPushRetryBackoff = 2 * time.Second

// HostPushSupervisor keeps one background push loop running per Session an
// agent host owns, so a status transition reaches ControlEvents.Publish as
// soon as the host reports it — never waiting for the periodic observation
// cycle a serving process otherwise runs on (see status-and-lifecycle's
// "protocol-reported transition is visible without a polling cycle"). Every
// method is safe for concurrent use; Reconcile is cheap enough to call every
// observation pass.
type HostPushSupervisor struct {
	publish func(sessions []Session, snapshot ObservationSnapshot)
	// watch and records are Seams: tests replace them so a push loop can be
	// driven without a real agent-host process.
	watch   func(ctx context.Context, record ManagedHostRecord, since uint64) (AgentHostState, uint64, bool, error)
	records func(SessionID) (ManagedHostRecord, bool, error)

	mu      sync.Mutex
	running map[SessionID]context.CancelFunc
}

// NewHostPushSupervisor creates a supervisor that hands every fresh
// single-Session observation it derives to publish.
func NewHostPushSupervisor(publish func(sessions []Session, snapshot ObservationSnapshot)) *HostPushSupervisor {
	return &HostPushSupervisor{
		publish: publish,
		watch: func(ctx context.Context, record ManagedHostRecord, since uint64) (AgentHostState, uint64, bool, error) {
			return WatchAgentHostState(record.SocketPath, record.Token, since, hostPushWatchTimeout)
		},
		records: func(sessionID SessionID) (ManagedHostRecord, bool, error) {
			return NewManagedHostRegistry().RecordFor(sessionID)
		},
		running: map[SessionID]context.CancelFunc{},
	}
}

// Reconcile starts a push loop for every Session in sessions whose runtime is
// owned by an agent host and does not have one running yet, and stops one
// for a Session no longer in sessions or no longer host-owned. Safe to call
// repeatedly with the same or a changed set.
func (s *HostPushSupervisor) Reconcile(sessions []Session) {
	wanted := make(map[SessionID]Session, len(sessions))
	for _, session := range sessions {
		if RuntimeHasAgentHost(session.SessionRuntime()) {
			wanted[session.ID] = session
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cancel := range s.running {
		if _, ok := wanted[id]; !ok {
			cancel()
			delete(s.running, id)
		}
	}
	for id, session := range wanted {
		if _, ok := s.running[id]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.running[id] = cancel
		go s.run(ctx, session)
	}
}

// Stop ends every running push loop.
func (s *HostPushSupervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cancel := range s.running {
		cancel()
		delete(s.running, id)
	}
}

// run long-polls one Session's host and publishes a fresh single-Session
// observation each time the host reports a change, until ctx ends.
func (s *HostPushSupervisor) run(ctx context.Context, session Session) {
	var revision uint64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		record, found, err := s.records(session.ID)
		if err != nil || !found {
			if !sleepOrDone(ctx, hostPushRetryBackoff) {
				return
			}
			continue
		}
		state, newRevision, changed, err := s.watch(ctx, record, revision)
		if err != nil {
			if !sleepOrDone(ctx, hostPushRetryBackoff) {
				return
			}
			continue
		}
		revision = newRevision
		if !changed {
			// The bounded wait simply timed out; nothing to publish.
			continue
		}
		observed := observeManagedSession(ctx, session, func(context.Context, Session) (AgentHostState, error) { return state, nil })
		snapshot := ObservationSnapshot{
			Availability: ObservationAvailable, ObservedAt: time.Now().UTC(),
			Sessions: []SessionObservation{observed},
		}
		s.publish([]Session{session}, snapshot)
	}
}

// sleepOrDone waits d unless ctx ends first; it reports whether the wait
// actually completed the full duration (false means ctx ended).
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}
