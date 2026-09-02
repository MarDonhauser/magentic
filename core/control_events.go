package core

import (
	"sync"
	"time"
)

// ControlEvent is one observed change of a Session. Events derive from the
// observation pass the serving process already runs; the control API starts no
// second observation loop and never polls tmux for a subscriber.
type ControlEvent struct {
	SessionID            SessionID               `json:"sessionId"`
	ProjectID            ProjectID               `json:"projectId,omitempty"`
	RuntimeName          string                  `json:"runtimeName,omitempty"`
	PreviousStatus       ControlStatus           `json:"previousStatus,omitempty"`
	Status               ControlStatus           `json:"status,omitempty"`
	PreviousAvailability ObservationAvailability `json:"previousAvailability,omitempty"`
	Availability         ObservationAvailability `json:"availability"`
	ObservedAt           time.Time               `json:"observedAt"`
}

// ControlEventMessage is one line of the event stream.
type ControlEventMessage struct {
	ID    string       `json:"id,omitempty"`
	Event ControlEvent `json:"event"`
}

// ControlEventFilter narrows a subscription. An empty filter watches every
// Session.
type ControlEventFilter struct {
	ProjectID ProjectID
	SessionID SessionID
}

func (f ControlEventFilter) matches(event ControlEvent) bool {
	if f.SessionID != "" && f.SessionID != event.SessionID {
		return false
	}
	if f.ProjectID != "" && f.ProjectID != event.ProjectID {
		return false
	}
	return true
}

// controlEventBuffer bounds one subscription. A subscriber that stops reading
// is dropped rather than allowed to stall the observation pass the interfaces
// also depend on.
const controlEventBuffer = 64

// ControlSubscription is one client's view of the stream.
type ControlSubscription struct {
	events  chan ControlEvent
	filter  ControlEventFilter
	mu      sync.Mutex
	stalled bool
	closed  bool
}

// Events yields the subscription's events. The channel closes when the
// subscription is released or dropped.
func (s *ControlSubscription) Events() <-chan ControlEvent { return s.events }

// Stalled reports whether the subscription was dropped for not being read.
func (s *ControlSubscription) Stalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stalled
}

func (s *ControlSubscription) close(stalled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.stalled = stalled
	close(s.events)
}

// controlObserved is the last state seen per Session, the baseline every new
// pass is diffed against.
type controlObserved struct {
	status       ControlStatus
	availability ObservationAvailability
}

// ControlEvents is the fan-out. It also feeds the pending waits, so a wait and
// a subscriber see the same observation passes.
type ControlEvents struct {
	mu            sync.Mutex
	last          map[SessionID]controlObserved
	subscriptions map[*ControlSubscription]struct{}
	waiters       map[chan ObservationSnapshot]SessionID
}

func NewControlEvents() *ControlEvents {
	return &ControlEvents{
		last:          map[SessionID]controlObserved{},
		subscriptions: map[*ControlSubscription]struct{}{},
		waiters:       map[chan ObservationSnapshot]SessionID{},
	}
}

// Subscribe registers interest in the stream.
func (e *ControlEvents) Subscribe(filter ControlEventFilter) *ControlSubscription {
	subscription := &ControlSubscription{events: make(chan ControlEvent, controlEventBuffer), filter: filter}
	e.mu.Lock()
	e.subscriptions[subscription] = struct{}{}
	e.mu.Unlock()
	return subscription
}

// Release ends a subscription so no further events are produced for it.
func (e *ControlEvents) Release(subscription *ControlSubscription) {
	e.mu.Lock()
	_, present := e.subscriptions[subscription]
	delete(e.subscriptions, subscription)
	e.mu.Unlock()
	if present {
		subscription.close(false)
	}
}

// Publish diffs one observation pass against the last observed state and emits
// one event per real change. An unchanged pass emits nothing.
func (e *ControlEvents) Publish(sessions []Session, snapshot ObservationSnapshot) []ControlEvent {
	registered := make(map[SessionID]Session, len(sessions))
	for _, session := range sessions {
		registered[session.ID] = session
	}
	e.mu.Lock()
	var events []ControlEvent
	for _, reading := range snapshot.Sessions {
		current := controlObserved{availability: reading.Availability}
		// An unavailable Observation is never emitted as a concrete status.
		if reading.Availability == ObservationAvailable {
			current.status = controlStatus(reading.Status)
		}
		previous, seen := e.last[reading.SessionID]
		e.last[reading.SessionID] = current
		if seen && previous == current {
			continue
		}
		session := registered[reading.SessionID]
		events = append(events, ControlEvent{
			SessionID: reading.SessionID, ProjectID: session.ProjectID, RuntimeName: session.RuntimeName,
			PreviousStatus: previous.status, Status: current.status,
			PreviousAvailability: previous.availability, Availability: current.availability,
			ObservedAt: snapshot.ObservedAt,
		})
	}
	subscriptions := make([]*ControlSubscription, 0, len(e.subscriptions))
	for subscription := range e.subscriptions {
		subscriptions = append(subscriptions, subscription)
	}
	waiters := make(map[chan ObservationSnapshot]SessionID, len(e.waiters))
	for waiter, id := range e.waiters {
		waiters[waiter] = id
	}
	e.mu.Unlock()

	var stalled []*ControlSubscription
	for _, subscription := range subscriptions {
		for _, event := range events {
			if !subscription.filter.matches(event) {
				continue
			}
			select {
			case subscription.events <- event:
			default:
				// The pass never awaits a consumer.
				stalled = append(stalled, subscription)
			}
		}
	}
	for _, subscription := range stalled {
		e.mu.Lock()
		delete(e.subscriptions, subscription)
		e.mu.Unlock()
		subscription.close(true)
	}
	for waiter, id := range waiters {
		if _, present := controlObservationFor(snapshot, id); !present {
			continue
		}
		// A waiter reads whole passes and only needs the newest one, so a
		// pending pass is replaced rather than queued behind.
		select {
		case <-waiter:
		default:
		}
		select {
		case waiter <- snapshot:
		default:
		}
	}
	return events
}

// Observations feeds one pending wait from the same fan-out.
func (e *ControlEvents) Observations(id SessionID) (<-chan ObservationSnapshot, func()) {
	waiter := make(chan ObservationSnapshot, 1)
	e.mu.Lock()
	e.waiters[waiter] = id
	e.mu.Unlock()
	return waiter, func() {
		e.mu.Lock()
		delete(e.waiters, waiter)
		e.mu.Unlock()
	}
}
