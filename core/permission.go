package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// PermissionDecision is what a developer answers to a PermissionRequest. Only
// these two exist: there is no mode, setting or timeout that answers on the
// developer's behalf (see PermissionDecisionModes).
type PermissionDecision string

const (
	PermissionAllow PermissionDecision = "allow"
	PermissionDeny  PermissionDecision = "deny"
)

// PermissionDecisionModes enumerates every decision the agent can receive.
// The set is closed over exactly the two explicit developer decisions, which
// is what the no-automatic-answer guarantee is tested against: enumerating
// the modes must never yield one Magentic could apply on its own.
func PermissionDecisionModes() []PermissionDecision {
	return []PermissionDecision{PermissionAllow, PermissionDeny}
}

// PermissionOutcome names how a PermissionRequest closed: allowed or denied
// by an explicit developer decision, or unanswerable because the agent
// process ended first. A closed request is never presented as allowed or
// denied unless a person decided it.
type PermissionOutcome string

const (
	PermissionAllowed      PermissionOutcome = "allowed"
	PermissionDenied       PermissionOutcome = "denied"
	PermissionUnanswerable PermissionOutcome = "unanswerable"
)

// PermissionRequest is a vendor permission prompt delivered to Magentic,
// carrying what is asked, the Session it belongs to and the time it was
// raised. It is readable by every connected interface and survives
// interfaces connecting and disconnecting while it is open, because the
// agent host — not an interface — holds it.
type PermissionRequest struct {
	ID        string    `json:"id"`
	SessionID SessionID `json:"sessionId"`
	// Asked states what the agent wants to do, in the vendor's own words.
	Asked    string    `json:"asked"`
	RaisedAt time.Time `json:"raisedAt"`
	// Open is false once the request closed for any outcome.
	Open bool `json:"open"`
	// Outcome and DecidedAt describe how it closed. DecidedBy names the
	// explicit developer action for allowed/denied; it is empty for an
	// unanswerable request, which nobody decided.
	Outcome   PermissionOutcome `json:"outcome,omitempty"`
	DecidedAt time.Time         `json:"decidedAt,omitzero"`
	DecidedBy string            `json:"decidedBy,omitempty"`
	// CloseReason states why an unanswerable request closed — the exit
	// reason of the agent process that ended before anyone decided.
	CloseReason string `json:"closeReason,omitempty"`
}

// Answerable reports whether the request can still be answered.
func (r PermissionRequest) Answerable() bool { return r.Open }

// ErrPermissionClosed refuses an answer for a request that already closed.
// The second answer to one request is refused with this reason and delivers
// nothing to the agent.
var ErrPermissionClosed = errors.New("die Berechtigungsanfrage ist bereits geschlossen")

// ErrPermissionUnknown answers an ID the store never held.
var ErrPermissionUnknown = errors.New("unbekannte Berechtigungsanfrage")

// PermissionStore holds one agent host's open and recently closed
// PermissionRequests. Answering blocks the agent's tool call until a
// decision arrives: Wait blocks, Answer delivers exactly once, and
// CloseUnanswerable closes whatever is still open when the agent process
// ends. All methods are safe for concurrent use; two interfaces answering
// the same request concurrently yield one delivered decision and one
// refusal.
type PermissionStore struct {
	mu       sync.Mutex
	requests map[string]*permissionEntry
	now      func() time.Time
	newID    func() string
}

type permissionEntry struct {
	request PermissionRequest
	// decided carries the delivered decision to exactly one waiter. It is
	// buffered so Answer never blocks on nobody waiting.
	decided chan PermissionDecision
	// answered closes once the first decision was delivered.
	answered chan struct{}
}

// NewPermissionStore creates an empty store.
func NewPermissionStore() *PermissionStore {
	return &PermissionStore{
		requests: map[string]*permissionEntry{},
		now:      time.Now,
		newID:    NewUUID,
	}
}

// Open registers a vendor permission prompt as an open request and reports
// it. The request stays open until Answer or CloseUnanswerable closes it —
// however long no interface is connected.
func (s *PermissionStore) Open(sessionID SessionID, asked string) PermissionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	request := PermissionRequest{
		ID: s.newID(), SessionID: sessionID,
		Asked: asked, RaisedAt: s.now(), Open: true,
	}
	s.requests[request.ID] = &permissionEntry{
		request:  request,
		decided:  make(chan PermissionDecision, 1),
		answered: make(chan struct{}),
	}
	return request
}

// OpenRequests lists every still-open request, oldest first.
func (s *PermissionStore) OpenRequests() []PermissionRequest {
	return s.Requests(true)
}

// Requests lists open requests, or all requests including closed ones when
// openOnly is false. Closed requests stay listed so a second answer can be
// refused with its reason rather than reported as unknown.
func (s *PermissionStore) Requests(openOnly bool) []PermissionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PermissionRequest
	for _, entry := range s.requests {
		if openOnly && !entry.request.Open {
			continue
		}
		out = append(out, entry.request)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RaisedAt.Equal(out[j].RaisedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].RaisedAt.Before(out[j].RaisedAt)
	})
	return out
}

// RequestForSession returns the open request of one Session, if it holds one.
func (s *PermissionStore) RequestForSession(sessionID SessionID) (PermissionRequest, bool) {
	for _, request := range s.OpenRequests() {
		if request.SessionID == sessionID {
			return request, true
		}
	}
	return PermissionRequest{}, false
}

// ErrPermissionWaitAbandoned reports that the caller stopped waiting before a
// decision arrived. The request itself is untouched by that: it stays open,
// still waiting for a person. Giving up on an answer is never an answer.
var ErrPermissionWaitAbandoned = errors.New("das Warten auf die Entscheidung wurde abgebrochen")

// Wait blocks until the request is answered or closed, then reports how it
// closed. It is what the agent's permission tool call waits in: the agent
// stays blocked rather than being answered on anyone's behalf. ctx bounds the
// wait for this caller only — a cancelled wait leaves the request open, which
// is what makes the wait testable without making it decidable.
func (s *PermissionStore) Wait(ctx context.Context, id string) (PermissionDecision, PermissionOutcome, error) {
	s.mu.Lock()
	entry, known := s.requests[id]
	s.mu.Unlock()
	if !known {
		return "", "", ErrPermissionUnknown
	}
	select {
	case decision, ok := <-entry.decided:
		if !ok {
			return "", s.closedOutcome(id), nil
		}
		return decision, s.decisionOutcome(decision), nil
	case <-ctx.Done():
		return "", "", fmt.Errorf("%w: %v", ErrPermissionWaitAbandoned, ctx.Err())
	}
}

// Answer delivers a developer's decision exactly once and closes the
// request. A second answer — including a concurrent one from another
// interface — is refused and delivers nothing.
func (s *PermissionStore) Answer(id string, decision PermissionDecision, decidedBy string) error {
	if decision != PermissionAllow && decision != PermissionDeny {
		return fmt.Errorf("unbekannte Entscheidung %q", decision)
	}
	s.mu.Lock()
	entry, known := s.requests[id]
	if !known {
		s.mu.Unlock()
		return ErrPermissionUnknown
	}
	if !entry.request.Open {
		s.mu.Unlock()
		return ErrPermissionClosed
	}
	entry.request.Open = false
	if decision == PermissionAllow {
		entry.request.Outcome = PermissionAllowed
	} else {
		entry.request.Outcome = PermissionDenied
	}
	entry.request.DecidedAt = s.now()
	entry.request.DecidedBy = decidedBy
	close(entry.answered)
	s.mu.Unlock()
	entry.decided <- decision
	close(entry.decided)
	return nil
}

// CloseUnanswerable closes every open request of a Session as no longer
// answerable, with that reason, when the agent process ends. It never
// records allowed or denied, and it wakes every blocked waiter.
func (s *PermissionStore) CloseUnanswerable(sessionID SessionID, reason string) []PermissionRequest {
	s.mu.Lock()
	var entries []*permissionEntry
	for _, entry := range s.requests {
		if entry.request.Open && entry.request.SessionID == sessionID {
			entry.request.Open = false
			entry.request.Outcome = PermissionUnanswerable
			entry.request.DecidedAt = s.now()
			entry.request.CloseReason = reason
			close(entry.answered)
			entries = append(entries, entry)
		}
	}
	closed := make([]PermissionRequest, 0, len(entries))
	for _, entry := range entries {
		closed = append(closed, entry.request)
	}
	s.mu.Unlock()
	for _, entry := range entries {
		close(entry.decided)
	}
	sort.Slice(closed, func(i, j int) bool { return closed[i].ID < closed[j].ID })
	return closed
}

func (s *PermissionStore) closedOutcome(id string) PermissionOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, known := s.requests[id]; known {
		return entry.request.Outcome
	}
	return PermissionUnanswerable
}

func (s *PermissionStore) decisionOutcome(decision PermissionDecision) PermissionOutcome {
	if decision == PermissionAllow {
		return PermissionAllowed
	}
	return PermissionDenied
}

// PermissionRequestItem renders an opened request as an Item in the Session's
// activity, at the point it occurred.
func PermissionRequestItem(request PermissionRequest) Item {
	return Item{
		ID:         "permission-request-" + request.ID,
		OccurredAt: request.RaisedAt,
		Role:       ItemRoleAgent,
		Kind:       ItemKindPermissionRequest,
		Title:      "Berechtigung angefragt",
		Detail:     request.Asked,
	}
}

// PermissionOutcomeItem renders how a request closed — allowed, denied, or
// no longer answerable — as the Item following its request.
func PermissionOutcomeItem(request PermissionRequest) Item {
	title := "Berechtigung erteilt"
	if request.Outcome == PermissionDenied {
		title = "Berechtigung verweigert"
	} else if request.Outcome == PermissionUnanswerable {
		title = "Berechtigungsanfrage verfallen"
	}
	detail := ""
	if request.DecidedBy != "" {
		detail = "entschieden von " + request.DecidedBy
	} else if request.Outcome == PermissionUnanswerable {
		detail = "der Agent-Prozess endete vor der Entscheidung"
		if request.CloseReason != "" {
			detail += ": " + request.CloseReason
		}
	}
	occurred := request.DecidedAt
	if occurred.IsZero() {
		occurred = request.RaisedAt
	}
	return Item{
		ID:         "permission-decision-" + request.ID,
		OccurredAt: occurred,
		Role:       ItemRoleDeveloper,
		Kind:       ItemKindPermissionDecision,
		Title:      title,
		Detail:     detail,
	}
}
