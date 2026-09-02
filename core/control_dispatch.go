package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// controlRegistry and controlLifecycle narrow the modules the control surface
// uses to the calls it actually makes. The control API is a client of these
// modules: it owns no state of its own and has no second path to tmux.
type controlRegistry interface {
	Snapshot(context.Context) (RegistrySnapshot, error)
	Change(context.Context, RegistryChange) (RegistryChangeResult, error)
}

type controlLifecycle interface {
	Provision(context.Context, SessionProvision) (SessionLifecycleResult, error)
	Park(context.Context, SessionID, string) (SessionLifecycleResult, error)
}

// ControlService dispatches the control vocabulary. Both front doors — the CLI
// and the socket — go through this one dispatcher, so a verb cannot drift.
type ControlService struct {
	registry     controlRegistry
	lifecycle    controlLifecycle
	repositories controlRepositories
	observe      observationReader
	// deliver is the outbox Seam. It reports the explicit applied fact of one
	// message rather than a hopeful boolean.
	deliver func(context.Context, Session, string) (ControlDelivery, string, error)
	// installed is the fail-closed vendor-binary check, kept as a Seam so the
	// dispatch tests do not depend on what this machine has on its PATH.
	installed func(AgentProvider) bool
	// observations is the Seam a pending wait is fed from. The serving process
	// wires it to the event fan-out so no second observation loop exists.
	observations func(context.Context, Session) (<-chan ObservationSnapshot, func())
	events       *ControlEvents
	now          func() time.Time
}

type ControlServiceConfig struct {
	RegistryPath string
	LedgerPath   string
}

// NewControlService wires the production modules.
func NewControlService(config ControlServiceConfig) *ControlService {
	if config.RegistryPath == "" {
		config.RegistryPath = StatePath()
	}
	if config.LedgerPath == "" {
		config.LedgerPath = SessionLifecyclePath()
	}
	service := &ControlService{
		registry:     OpenRegistry(config.RegistryPath),
		lifecycle:    OpenSessionLifecycle(SessionLifecycleConfig{RegistryPath: config.RegistryPath, LedgerPath: config.LedgerPath}),
		repositories: NewRepositories(),
		observe:      Observe,
		installed:    providerBinaryAvailable,
		events:       NewControlEvents(),
		now:          time.Now,
	}
	service.deliver = service.deliverThroughOutbox
	service.observations = func(_ context.Context, session Session) (<-chan ObservationSnapshot, func()) {
		return service.events.Observations(session.ID)
	}
	return service
}

// Events is the fan-out the serving process feeds its observation pass into.
func (s *ControlService) Events() *ControlEvents { return s.events }

// Observed hands one observation pass to the fan-out. The serving process
// already runs this pass for its own interface; the control API adds none.
func (s *ControlService) Observed(sessions []Session, snapshot ObservationSnapshot) {
	if s.events != nil {
		s.events.Publish(sessions, snapshot)
	}
}

// Dispatch answers exactly one request.
func (s *ControlService) Dispatch(ctx context.Context, request ControlRequest) ControlResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	switch request.Verb {
	case ControlSessionList:
		return s.sessionList(ctx, request)
	case ControlSessionStart:
		return s.sessionStart(ctx, request)
	case ControlSessionSend:
		return s.sessionSend(ctx, request)
	case ControlSessionOutput:
		return s.sessionOutput(ctx, request)
	case ControlSessionKill:
		return s.sessionKill(ctx, request)
	case ControlSessionWait:
		return s.sessionWait(ctx, request)
	case ControlSessionWhoami:
		return s.sessionWhoami(ctx, request)
	case ControlSessionWatch:
		return controlFailure(request.ID, ControlInvalidRequest,
			"session.watch meldet eine Verbindung am Ereignisstrom an und wird nicht als einzelne Anfrage beantwortet.")
	}
	return controlFailure(request.ID, ControlUnknownVerb,
		fmt.Sprintf("Unbekanntes Verb %q — bekannt sind: %s.", request.Verb, controlVerbList()))
}

func controlVerbList() string {
	names := make([]string, 0, len(ControlVerbs()))
	for _, verb := range ControlVerbs() {
		names = append(names, string(verb))
	}
	return strings.Join(names, ", ")
}

func (s *ControlService) state(ctx context.Context) (State, *controlError) {
	snapshot, err := s.registry.Snapshot(ctx)
	if err != nil {
		return State{}, controlRefusal(ControlUnavailable, "Die Registry ist nicht lesbar: %v", err)
	}
	return snapshot.State(), nil
}

// sessionList reports the registered Sessions together with one Observation.
// A Session whose runtime could not be read is reported as unreadable, never
// as a concrete status (ADR 0004).
func (s *ControlService) sessionList(ctx context.Context, request ControlRequest) ControlResponse {
	state, failure := s.state(ctx)
	if failure != nil {
		return failure.response(request.ID)
	}
	sessions := state.Agents
	if strings.TrimSpace(request.Args.Project) != "" {
		project, projectFailure := resolveControlProject(state, request.Args.Project)
		if projectFailure != nil {
			return projectFailure.response(request.ID)
		}
		var scoped []Session
		for _, session := range sessions {
			if controlSessionInProject(session, project) {
				scoped = append(scoped, session)
			}
		}
		sessions = scoped
		if strings.TrimSpace(request.Args.Worktree) != "" || strings.TrimSpace(request.Args.Directory) != "" {
			scope, scopeFailure := resolveControlWorktree(ctx, s.repositories, project, request.Args)
			if scopeFailure != nil {
				return scopeFailure.response(request.ID)
			}
			var inWorktree []Session
			for _, session := range sessions {
				if sameRepositoryPath(session.Dir, scope.Directory) {
					inWorktree = append(inWorktree, session)
				}
			}
			sessions = inWorktree
		}
	}
	views := controlSessionViews(state, sessions)
	s.applyObservation(ctx, state, sessions, views)
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return ControlResponse{ID: request.ID, Outcome: ControlOK, Result: &ControlResult{Sessions: views}}
}

// applyObservation fills the availability and status of every view from one
// Observation pass. A status is only carried when the Observation is available.
func (s *ControlService) applyObservation(ctx context.Context, _ State, sessions []Session, views []ControlSessionView) {
	if len(sessions) == 0 {
		return
	}
	snapshot := s.observe(ctx, sessions)
	observed := make(map[SessionID]SessionObservation, len(snapshot.Sessions))
	for _, session := range snapshot.Sessions {
		observed[session.SessionID] = session
	}
	for i := range views {
		reading, ok := observed[views[i].SessionID]
		if !ok {
			views[i].Availability = ObservationUnavailable
			continue
		}
		views[i].Availability = reading.Availability
		views[i].StatusSource = reading.StatusSource
		if reading.Availability == ObservationAvailable {
			views[i].Status = controlStatus(reading.Status)
		}
	}
}

// sessionStart provisions a Session through the durable desired-state
// lifecycle. Nothing is provisioned before the Project, the Worktree scope, and
// the vendor have all resolved.
func (s *ControlService) sessionStart(ctx context.Context, request ControlRequest) ControlResponse {
	state, failure := s.state(ctx)
	if failure != nil {
		return failure.response(request.ID)
	}
	project, failure := resolveControlProject(state, request.Args.Project)
	if failure != nil {
		return failure.response(request.ID)
	}
	kind := request.Args.Kind
	if kind == "" {
		kind = SessionKindCodingAgent
	}
	vendor := request.Args.Vendor
	switch kind {
	case SessionKindTerminal:
		if vendor != "" {
			return controlFailure(request.ID, ControlRefused,
				"Eine Terminal-Session führt keinen Coding-Agent — bitte ohne Agent-Art starten.")
		}
		if strings.TrimSpace(request.Args.Prompt) != "" {
			return controlFailure(request.ID, ControlRefused,
				"Eine Terminal-Session nimmt keinen Prompt entgegen.")
		}
	case SessionKindCodingAgent:
		if vendor == "" {
			vendor = AgentVendorClaude
		}
		provider, known := providerForVendor(vendor)
		if !known {
			return controlFailure(request.ID, ControlRefused, fmt.Sprintf(
				"Agent-Art %q wird nicht unterstützt — möglich sind: %s.", vendor, controlSupportedVendors()))
		}
		if !s.installed(provider) {
			return controlFailure(request.ID, ControlUnavailable, fmt.Sprintf(
				"%s ist nicht installiert (%s liegt nicht im PATH).", vendor, provider.Binary()))
		}
	default:
		return controlFailure(request.ID, ControlRefused, fmt.Sprintf(
			"Unbekannte Session-Art %q — möglich sind %q und %q.", kind, SessionKindCodingAgent, SessionKindTerminal))
	}
	scope, failure := resolveControlWorktree(ctx, s.repositories, project, request.Args)
	if failure != nil {
		return failure.response(request.ID)
	}
	name := strings.TrimSpace(request.Args.Name)
	if name == "" {
		name = registrySessionNameCandidate(&state, project.Name)
	}
	result, err := s.lifecycle.Provision(ctx, SessionProvision{
		ProjectID: project.ID, Name: name, Directory: scope.Directory,
		Worktree: scope.Create || scope.Reference != "", CreateWorktree: scope.Create,
		Kind: kind, InitialPrompt: request.Args.Prompt, Vendor: vendor,
	})
	if err != nil {
		return controlFailure(request.ID, ControlFailed, fmt.Sprintf("Session konnte nicht gestartet werden: %v", err))
	}
	view := controlSessionView(result.Session)
	response := &ControlResult{
		SessionID: result.Session.ID, Session: &view, Dir: result.Session.Dir,
		Vendor: vendor, Delivery: controlPromptDelivery(result.Record.PromptDelivery),
	}
	if result.Session.Worktree {
		// Only a Session that actually runs in a Worktree reports one.
		response.Worktree, response.WorktreeRef = result.Session.Dir, scope.Reference
	}
	return ControlResponse{ID: request.ID, Outcome: ControlOK, Result: response}
}

func controlSupportedVendors() string {
	vendors := make([]string, 0, 4)
	for _, provider := range builtinAgentProviders() {
		vendors = append(vendors, string(provider.Vendor()))
	}
	return strings.Join(vendors, ", ")
}

// controlPromptDelivery projects Lifecycle's durable delivery fact. A pending
// or unknown delivery is reported as unknown and is never resent automatically.
func controlPromptDelivery(delivery InitialPromptDelivery) ControlDelivery {
	switch delivery {
	case InitialPromptDelivered:
		return ControlDeliveryDelivered
	case InitialPromptPending, InitialPromptUnknown:
		return ControlDeliveryUnknown
	case InitialPromptFailed:
		return ControlDeliveryFailed
	}
	return ControlDeliveryNone
}

// sessionSend submits text to a coding-agent Session through the Outbox, so a
// control message serializes with every other prompt to the same runtime.
func (s *ControlService) sessionSend(ctx context.Context, request ControlRequest) ControlResponse {
	state, failure := s.state(ctx)
	if failure != nil {
		return failure.response(request.ID)
	}
	session, failure := resolveControlSession(state, request.Args)
	if failure != nil {
		return failure.response(request.ID)
	}
	if session.IsTerm() {
		return controlFailure(request.ID, ControlRefused, fmt.Sprintf(
			"%s ist eine Terminal-Session — dort läuft kein Coding-Agent.", session.Name))
	}
	if strings.TrimSpace(request.Args.Text) == "" {
		return controlFailure(request.ID, ControlInvalidRequest, "Der zu sendende Text ist leer.")
	}
	delivery, messageID, err := s.deliver(ctx, session, request.Args.Text)
	if err != nil {
		return controlFailure(request.ID, ControlFailed, fmt.Sprintf("Der Text konnte nicht zugestellt werden: %v", err))
	}
	return ControlResponse{ID: request.ID, Outcome: ControlOK, Result: &ControlResult{
		SessionID: session.ID, Delivery: delivery, MessageID: messageID,
	}}
}

// deliverThroughOutbox queues the message durably and runs one delivery attempt
// against a fresh Observation. Whether it left the queue is the applied fact.
func (s *ControlService) deliverThroughOutbox(ctx context.Context, session Session, text string) (ControlDelivery, string, error) {
	message := QueuedMessage{ID: NewUUID(), Kind: QueuedMessageKindMessage, Text: text, EnqueuedAt: s.now()}
	if _, err := s.registry.Change(ctx, EnqueueSessionMessage(session.ID, session.Name, message)); err != nil {
		return ControlDeliveryNone, "", err
	}
	kickOutboxForSession(ctx, session.ID, s.observe)
	snapshot, err := s.registry.Snapshot(ctx)
	if err != nil {
		// The message is durably queued; only its current position is unknown.
		return ControlDeliveryQueued, message.ID, nil
	}
	state := snapshot.State()
	if current := state.SessionByID(session.ID); current != nil {
		for _, queued := range current.Outbox {
			if queued.ID == message.ID {
				return ControlDeliveryQueued, message.ID, nil
			}
		}
	}
	return ControlDeliveryDelivered, message.ID, nil
}

// sessionOutput returns the visible pane content with terminal control
// sequences removed, together with the availability that produced it.
func (s *ControlService) sessionOutput(ctx context.Context, request ControlRequest) ControlResponse {
	state, failure := s.state(ctx)
	if failure != nil {
		return failure.response(request.ID)
	}
	session, failure := resolveControlSession(state, request.Args)
	if failure != nil {
		return failure.response(request.ID)
	}
	snapshot := s.observe(ctx, []Session{session})
	result := &ControlResult{SessionID: session.ID, Availability: ObservationUnavailable}
	for _, observed := range snapshot.Sessions {
		if observed.SessionID != session.ID {
			continue
		}
		result.Availability = observed.Availability
		if observed.Availability == ObservationAvailable {
			result.Status = controlStatus(observed.Status)
		}
		if !observed.ContentKnown {
			// An unread pane is not an empty Session.
			break
		}
		// Normalization is idempotent; applying it here makes the removal of
		// control sequences a property of this surface rather than an
		// inherited accident.
		content := normalizeObservedContent(observed.Content)
		if request.Args.Lines > 0 {
			content = LastLines(content, request.Args.Lines)
		}
		result.Content = content
		break
	}
	return ControlResponse{ID: request.ID, Outcome: ControlOK, Result: result}
}

// sessionKill ends a Session's runtime through the durable desired-state
// lifecycle. The Worktree stays where it is: kill removes no work.
func (s *ControlService) sessionKill(ctx context.Context, request ControlRequest) ControlResponse {
	state, failure := s.state(ctx)
	if failure != nil {
		return failure.response(request.ID)
	}
	session, failure := resolveControlSession(state, request.Args)
	if failure != nil {
		return failure.response(request.ID)
	}
	alreadyGone := false
	snapshot := s.observe(ctx, []Session{session})
	for _, observed := range snapshot.Sessions {
		if observed.SessionID == session.ID && observed.Presence == SessionPresenceAbsent {
			alreadyGone = true
		}
	}
	if _, err := s.lifecycle.Park(ctx, session.ID, session.Name); err != nil {
		return controlFailure(request.ID, ControlFailed, fmt.Sprintf("Session konnte nicht beendet werden: %v", err))
	}
	return ControlResponse{ID: request.ID, Outcome: ControlOK, Result: &ControlResult{
		SessionID: session.ID, Stopped: true, AlreadyGone: alreadyGone, Dir: session.Dir,
	}}
}

// sessionWhoami resolves the caller's own marker facts against the Registry, so
// an agent learns its Project, Worktree, and SessionID without parsing state
// files. The facts are a claim: a copied environment that resolves to nothing
// is answered not-managed rather than with somebody else's identity.
func (s *ControlService) sessionWhoami(ctx context.Context, request ControlRequest) ControlResponse {
	state, failure := s.state(ctx)
	if failure != nil {
		return failure.response(request.ID)
	}
	marker := request.Args.Marker
	session := state.SessionByID(marker.SessionID)
	if marker.SessionID == "" || session == nil {
		return controlFailure(request.ID, ControlNotManaged, fmt.Sprintf(
			"%s löst keine registrierte Session auf — dieser Aufruf läuft nicht in einer von Magentic verwalteten Session.",
			controlMarkerDescription(marker)))
	}
	if marker.ProjectID != "" && session.ProjectID != marker.ProjectID {
		return controlFailure(request.ID, ControlNotManaged, fmt.Sprintf(
			"Die Marker-Angaben widersprechen sich: Session %s gehört nicht zu ProjectID %s.",
			marker.SessionID, marker.ProjectID))
	}
	view := controlSessionViews(state, []Session{*session})[0]
	result := &ControlResult{SessionID: session.ID, Session: &view, Dir: session.Dir, Vendor: view.Vendor}
	if session.Worktree {
		result.Worktree = session.Dir
	}
	return ControlResponse{ID: request.ID, Outcome: ControlOK, Result: result}
}
