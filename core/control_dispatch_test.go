package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// controlFakeRegistry answers from an in-memory State and records the changes
// the dispatcher applied.
type controlFakeRegistry struct {
	state   State
	applied []RegistryChange
	err     error
}

func (r *controlFakeRegistry) Snapshot(context.Context) (RegistrySnapshot, error) {
	if r.err != nil {
		return RegistrySnapshot{}, r.err
	}
	return RegistrySnapshot{state: cloneState(&r.state)}, nil
}

func (r *controlFakeRegistry) Change(_ context.Context, change RegistryChange) (RegistryChangeResult, error) {
	if r.err != nil {
		return RegistryChangeResult{}, r.err
	}
	r.applied = append(r.applied, change)
	if _, _, _, err := applyRegistryChange(&r.state, change); err != nil {
		return RegistryChangeResult{}, err
	}
	return RegistryChangeResult{Applied: true, Snapshot: RegistrySnapshot{state: cloneState(&r.state)}}, nil
}

// controlFakeLifecycle records the provisioning and parking intents without
// touching Git, tmux, or the filesystem.
type controlFakeLifecycle struct {
	provisions []SessionProvision
	parked     []SessionID
	session    Session
	delivery   InitialPromptDelivery
	err        error
}

func (l *controlFakeLifecycle) Provision(_ context.Context, request SessionProvision) (SessionLifecycleResult, error) {
	l.provisions = append(l.provisions, request)
	if l.err != nil {
		return SessionLifecycleResult{}, l.err
	}
	session := l.session
	session.Name = request.Name
	session.ProjectID = request.ProjectID
	session.Dir = request.Directory
	session.Worktree = request.Worktree || request.CreateWorktree
	session.SessionKind = request.Kind
	session.Vendor = request.Vendor
	if session.ID == "" {
		session.ID = "session-neu"
	}
	return SessionLifecycleResult{Session: session, Record: LifecycleRecord{PromptDelivery: l.delivery}}, nil
}

func (l *controlFakeLifecycle) Park(_ context.Context, id SessionID, _ string) (SessionLifecycleResult, error) {
	if l.err != nil {
		return SessionLifecycleResult{}, l.err
	}
	l.parked = append(l.parked, id)
	return SessionLifecycleResult{}, nil
}

func controlObserver(observations ...SessionObservation) observationReader {
	return func(context.Context, []Session) ObservationSnapshot {
		snapshot := ObservationSnapshot{ObservedAt: time.Now(), Availability: ObservationAvailable}
		snapshot.Sessions = append(snapshot.Sessions, observations...)
		return snapshot
	}
}

func controlTestService(state State) (*ControlService, *controlFakeRegistry, *controlFakeLifecycle) {
	registry := &controlFakeRegistry{state: state}
	lifecycle := &controlFakeLifecycle{}
	service := &ControlService{
		registry: registry, lifecycle: lifecycle,
		repositories: controlWorktreeRepositories(),
		observe:      controlObserver(),
		installed:    func(AgentProvider) bool { return true },
		now:          func() time.Time { return time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC) },
	}
	service.deliver = func(context.Context, Session, string) (ControlDelivery, string, error) {
		return ControlDeliveryDelivered, "message-1", nil
	}
	return service, registry, lifecycle
}

func controlDispatchState() State {
	state := controlAddressState()
	state.Agents = append(state.Agents, Session{
		ID: "session-term", Name: "shell", ProjectID: "projekt-a", Project: "alpha",
		SessionKind: SessionKindTerminal, RuntimeName: "mgt-shell", Dir: "/tmp/alpha",
	})
	return state
}

func TestControlSessionListReportsUnavailableObservation(t *testing.T) {
	service, _, _ := controlTestService(controlDispatchState())
	service.observe = func(context.Context, []Session) ObservationSnapshot {
		return ObservationSnapshot{Availability: ObservationUnavailable, Sessions: []SessionObservation{
			{SessionID: "session-a", Availability: ObservationUnavailable, Presence: SessionPresenceUnknown, Status: StatusIdle},
			{SessionID: "session-c", Availability: ObservationAvailable, Presence: SessionPresencePresent, Status: StatusRunning, StatusSource: StatusSourceSnapshot},
		}}
	}
	response := service.Dispatch(context.Background(), ControlRequest{
		ID: "r1", Verb: ControlSessionList, Args: ControlArgs{Project: "alpha"},
	})
	if response.Outcome != ControlOK {
		t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
	}
	byID := map[SessionID]ControlSessionView{}
	for _, view := range response.Result.Sessions {
		byID[view.SessionID] = view
	}
	if len(byID) != 3 {
		t.Fatalf("Projektfilter greift nicht: %+v", response.Result.Sessions)
	}
	if _, listed := byID["session-b"]; listed {
		t.Fatal("eine Session aus dem anderen Projekt wurde gelistet")
	}
	unreadable := byID["session-a"]
	if unreadable.Availability != ObservationUnavailable {
		t.Fatalf("Verfügbarkeit = %q, want %q", unreadable.Availability, ObservationUnavailable)
	}
	if unreadable.Status != "" {
		t.Fatalf("ein unlesbarer Runtime wurde als %q gemeldet", unreadable.Status)
	}
	if byID["session-c"].Status != ControlStatusRunning {
		t.Fatalf("Status = %q, want %q", byID["session-c"].Status, ControlStatusRunning)
	}
	// A Session the Observation did not answer for stays explicitly unreadable.
	if byID["session-term"].Availability != ObservationUnavailable || byID["session-term"].Status != "" {
		t.Fatalf("fehlende Beobachtung = %+v", byID["session-term"])
	}
}

func TestControlSessionStartScopes(t *testing.T) {
	tests := []struct {
		name          string
		args          ControlArgs
		wantDirectory string
		wantCreate    bool
	}{
		{"Projektverzeichnis", ControlArgs{Project: "alpha"}, "/tmp/alpha", false},
		{"frischer Worktree", ControlArgs{Project: "alpha", NewWorktree: true}, "", true},
		{"bestehender Worktree", ControlArgs{Project: "alpha", Worktree: "wt_review"}, "/tmp/alpha-agents/review", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _, lifecycle := controlTestService(controlDispatchState())
			args := test.args
			args.Name = "neu"
			args.Vendor = AgentVendorClaude
			service.observe = controlObserver()
			response := service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionStart, Args: args})
			if response.Outcome != ControlOK {
				t.Fatalf("Start = %q (%s)", response.Outcome, response.Message)
			}
			if len(lifecycle.provisions) != 1 {
				t.Fatalf("Provisionierungen = %d, want 1", len(lifecycle.provisions))
			}
			provision := lifecycle.provisions[0]
			if provision.Directory != test.wantDirectory || provision.CreateWorktree != test.wantCreate {
				t.Fatalf("Provisionierung = %+v", provision)
			}
			if provision.ProjectID != "projekt-a" {
				t.Fatalf("ProjectID = %q", provision.ProjectID)
			}
		})
	}
}

func TestControlSessionStartTerminalAndUnsupportedVendor(t *testing.T) {
	service, _, lifecycle := controlTestService(controlDispatchState())
	response := service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionStart, Args: ControlArgs{
		Project: "alpha", Name: "terminal", Kind: SessionKindTerminal,
	}})
	if response.Outcome != ControlOK {
		t.Fatalf("Terminal-Session = %q (%s)", response.Outcome, response.Message)
	}
	if lifecycle.provisions[0].Kind != SessionKindTerminal || lifecycle.provisions[0].Vendor != "" {
		t.Fatalf("Terminal-Provisionierung = %+v", lifecycle.provisions[0])
	}

	response = service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionStart, Args: ControlArgs{
		Project: "alpha", Name: "terminal", Kind: SessionKindTerminal, Prompt: "arbeite",
	}})
	if response.Outcome != ControlRefused {
		t.Fatalf("Prompt an eine Terminal-Session = %q", response.Outcome)
	}

	before := len(lifecycle.provisions)
	response = service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionStart, Args: ControlArgs{
		Project: "alpha", Name: "neu", Vendor: AgentVendor("cursor"),
	}})
	if response.Outcome != ControlRefused {
		t.Fatalf("unbekannte Agent-Art = %q", response.Outcome)
	}
	if !strings.Contains(response.Message, string(AgentVendorClaude)) {
		t.Fatalf("Begründung nennt die möglichen Agent-Arten nicht: %q", response.Message)
	}
	if len(lifecycle.provisions) != before {
		t.Fatal("trotz unbekannter Agent-Art wurde provisioniert")
	}
}

func TestControlSessionStartReportsUnknownPromptDelivery(t *testing.T) {
	service, _, lifecycle := controlTestService(controlDispatchState())
	lifecycle.delivery = InitialPromptUnknown
	response := service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionStart, Args: ControlArgs{
		Project: "alpha", Name: "neu", Kind: SessionKindTerminal,
	}})
	if response.Outcome != ControlOK {
		t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
	}
	if response.Result.Delivery != ControlDeliveryUnknown {
		t.Fatalf("Zustellung = %q, want %q", response.Result.Delivery, ControlDeliveryUnknown)
	}
	// The dispatcher owns no retry: a second delivery needs a new request.
	if len(lifecycle.provisions) != 1 {
		t.Fatalf("Provisionierungen = %d, want 1", len(lifecycle.provisions))
	}
	if got := controlPromptDelivery(InitialPromptPending); got != ControlDeliveryUnknown {
		t.Fatalf("ausstehende Zustellung = %q, want %q", got, ControlDeliveryUnknown)
	}
}

func TestControlSessionSend(t *testing.T) {
	service, _, _ := controlTestService(controlDispatchState())
	response := service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionSend, Args: ControlArgs{
		Session: "session-c", Text: "weiter",
	}})
	if response.Outcome != ControlOK || response.Result.Delivery != ControlDeliveryDelivered {
		t.Fatalf("Zustellung = %q/%v (%s)", response.Outcome, response.Result, response.Message)
	}

	service.deliver = func(context.Context, Session, string) (ControlDelivery, string, error) {
		return ControlDeliveryQueued, "message-2", nil
	}
	response = service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionSend, Args: ControlArgs{
		Session: "session-c", Text: "weiter",
	}})
	if response.Result.Delivery != ControlDeliveryQueued || response.Result.MessageID != "message-2" {
		t.Fatalf("Warteschlange = %+v", response.Result)
	}

	response = service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionSend, Args: ControlArgs{
		Session: "session-term", Text: "weiter",
	}})
	if response.Outcome != ControlRefused {
		t.Fatalf("Terminal-Session = %q", response.Outcome)
	}

	service.deliver = func(context.Context, Session, string) (ControlDelivery, string, error) {
		return ControlDeliveryNone, "", errors.New("tmux weg")
	}
	response = service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionSend, Args: ControlArgs{
		Session: "session-c", Text: "weiter",
	}})
	if response.Outcome != ControlFailed {
		t.Fatalf("gescheiterte Zustellung = %q", response.Outcome)
	}
}

func TestControlSessionSendQueuesThroughOutbox(t *testing.T) {
	service, registry, _ := controlTestService(controlDispatchState())
	service.deliver = service.deliverThroughOutbox
	response := service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionSend, Args: ControlArgs{
		Session: "session-c", Text: "weiter",
	}})
	if response.Outcome != ControlOK {
		t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
	}
	session := registry.state.SessionByID("session-c")
	if session == nil || len(session.Outbox) != 1 || session.Outbox[0].Text != "weiter" {
		t.Fatalf("Outbox = %+v", session)
	}
	if response.Result.Delivery != ControlDeliveryQueued {
		t.Fatalf("Zustellung = %q, want %q", response.Result.Delivery, ControlDeliveryQueued)
	}
}

func TestControlSessionOutput(t *testing.T) {
	service, _, _ := controlTestService(controlDispatchState())
	service.observe = controlObserver(SessionObservation{
		SessionID: "session-c", Availability: ObservationAvailable, Presence: SessionPresencePresent,
		Status: StatusRunning, Content: "eins\n\x1b[31mzwei\x1b[0m\ndrei", ContentKnown: true,
	})
	response := service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionOutput, Args: ControlArgs{
		Session: "session-c",
	}})
	if response.Result.Content != "eins\nzwei\ndrei" {
		t.Fatalf("Inhalt = %q — Steuersequenzen wurden nicht entfernt", response.Result.Content)
	}
	if response.Result.Availability != ObservationAvailable {
		t.Fatalf("Verfügbarkeit = %q", response.Result.Availability)
	}

	response = service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionOutput, Args: ControlArgs{
		Session: "session-c", Lines: 2,
	}})
	if response.Result.Content != "zwei\ndrei" {
		t.Fatalf("Zeilengrenze = %q", response.Result.Content)
	}

	service.observe = controlObserver(SessionObservation{
		SessionID: "session-c", Availability: ObservationUnavailable, Presence: SessionPresenceUnknown,
	})
	response = service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionOutput, Args: ControlArgs{
		Session: "session-c",
	}})
	if response.Result.Availability != ObservationUnavailable {
		t.Fatalf("Verfügbarkeit = %q, want %q", response.Result.Availability, ObservationUnavailable)
	}
	if response.Result.Content != "" || response.Result.Status != "" {
		t.Fatalf("unlesbarer Runtime wurde als leere Session dargestellt: %+v", response.Result)
	}
}

func TestControlSessionKill(t *testing.T) {
	state := controlDispatchState()
	worktree := "/tmp/alpha-agents/review"
	state.Agents = append(state.Agents, Session{
		ID: "session-wt", Name: "wt", ProjectID: "projekt-a", Project: "alpha",
		RuntimeName: "mgt-wt", Dir: worktree, Worktree: true,
	})
	service, registry, lifecycle := controlTestService(state)
	service.observe = controlObserver(SessionObservation{
		SessionID: "session-wt", Availability: ObservationAvailable, Presence: SessionPresencePresent, Status: StatusRunning,
	})
	response := service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionKill, Args: ControlArgs{Session: "session-wt"}})
	if response.Outcome != ControlOK || !response.Result.Stopped || response.Result.AlreadyGone {
		t.Fatalf("laufende Session = %q/%+v", response.Outcome, response.Result)
	}
	if len(lifecycle.parked) != 1 || lifecycle.parked[0] != "session-wt" {
		t.Fatalf("Lifecycle-Absicht = %+v", lifecycle.parked)
	}
	// kill removes no work: the Session keeps its Worktree directory.
	if response.Result.Dir != worktree {
		t.Fatalf("Worktree = %q, want %q", response.Result.Dir, worktree)
	}
	if session := registry.state.SessionByID("session-wt"); session == nil || session.Dir != worktree {
		t.Fatalf("Session verlor ihren Worktree: %+v", session)
	}

	service.observe = controlObserver(SessionObservation{
		SessionID: "session-wt", Availability: ObservationAvailable, Presence: SessionPresenceAbsent, Status: StatusDead,
	})
	response = service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionKill, Args: ControlArgs{Session: "session-wt"}})
	if response.Outcome != ControlOK || !response.Result.AlreadyGone {
		t.Fatalf("bereits beendete Session = %q/%+v", response.Outcome, response.Result)
	}
}

func TestControlDispatchUnknownVerb(t *testing.T) {
	service, _, _ := controlTestService(controlDispatchState())
	response := service.Dispatch(context.Background(), ControlRequest{ID: "r9", Verb: "session.restart"})
	if response.Outcome != ControlUnknownVerb || response.ID != "r9" {
		t.Fatalf("Antwort = %+v", response)
	}
	if !strings.Contains(response.Message, string(ControlSessionStart)) {
		t.Fatalf("Begründung nennt die bekannten Verben nicht: %q", response.Message)
	}
}
