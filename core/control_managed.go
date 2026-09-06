package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// interruptManagedTurnThroughHost ends the running turn of a managed Session
// through its durably recorded agent host. Only the recorded socket path and
// token address the host — never a process-table search.
func interruptManagedTurnThroughHost(_ context.Context, session Session) (ManagedTurn, error) {
	registry := NewManagedHostRegistry()
	records, err := registry.Records()
	if err != nil {
		return ManagedTurn{}, fmt.Errorf("%w: %v", ErrManagedHostUnreachable, err)
	}
	for _, record := range records {
		if record.SessionID != session.ID {
			continue
		}
		return InterruptAgentHostTurn(record.SocketPath, record.Token)
	}
	return ManagedTurn{}, fmt.Errorf("%w: für Session %q ist kein Host verzeichnet", ErrManagedHostUnreachable, session.ID)
}

// answerManagedPermissionThroughHost delivers a developer's explicit decision
// to one open PermissionRequest through its host, exactly once.
func answerManagedPermissionThroughHost(_ context.Context, session Session, requestID string, decision PermissionDecision, decidedBy string) (PermissionRequest, error) {
	registry := NewManagedHostRegistry()
	records, err := registry.Records()
	if err != nil {
		return PermissionRequest{}, fmt.Errorf("%w: %v", ErrManagedHostUnreachable, err)
	}
	for _, record := range records {
		if record.SessionID != session.ID {
			continue
		}
		return AnswerAgentHostPermission(record.SocketPath, record.Token, requestID, decision, decidedBy)
	}
	return PermissionRequest{}, fmt.Errorf("%w: für Session %q ist kein Host verzeichnet", ErrManagedHostUnreachable, session.ID)
}

// requireManagedSession refuses a managed-only verb against any other runtime
// with the reason stated. A tmux Session answers interrupts and permission
// prompts in its own pane — Magentic must not pretend otherwise.
func requireManagedSession(session Session, verb string) *controlError {
	if session.SessionRuntime() == RuntimeManaged {
		return nil
	}
	if session.IsTerm() {
		return controlRefusal(ControlRefused,
			"%s ist eine Terminal-Session — dort läuft kein verwalteter Turn.", session.Name)
	}
	return controlRefusal(ControlRefused,
		"%s läuft im tmux-Runtime und beantwortet %s in seinem eigenen Pane — "+
			"Interrupt und Freigaben gibt es nur für verwaltete Sessions.", session.Name, verb)
}

// sessionInterrupt stops the running turn of a managed Session, leaving its
// process alive for the next prompt. With no turn running it is refused.
func (s *ControlService) sessionInterrupt(ctx context.Context, request ControlRequest) ControlResponse {
	state, failure := s.state(ctx)
	if failure != nil {
		return failure.response(request.ID)
	}
	session, failure := resolveControlSession(state, request.Args)
	if failure != nil {
		return failure.response(request.ID)
	}
	if refusal := requireManagedSession(session, "einen Interrupt"); refusal != nil {
		return refusal.response(request.ID)
	}
	interrupt := s.managedInterrupt
	if interrupt == nil {
		interrupt = interruptManagedTurnThroughHost
	}
	ended, err := interrupt(ctx, session)
	if err != nil {
		switch {
		case errors.Is(err, ErrManagedNoTurn):
			return controlFailure(request.ID, ControlRefused,
				fmt.Sprintf("Für %s läuft kein Turn, der unterbrochen werden könnte.", session.Name))
		case errors.Is(err, ErrManagedHostUnreachable):
			return controlFailure(request.ID, ControlUnavailable,
				fmt.Sprintf("Der Agent-Host von %s ist nicht erreichbar — die Session ist unverfügbar, nicht tot.", session.Name))
		case errors.Is(err, ErrAgentHostForeign):
			return controlFailure(request.ID, ControlUnavailable,
				fmt.Sprintf("Der Agent-Host von %s bestätigte das verzeichnete Token nicht — weder übernommen noch beendet.", session.Name))
		}
		return controlFailure(request.ID, ControlFailed, fmt.Sprintf("Der Turn konnte nicht unterbrochen werden: %v", err))
	}
	return ControlResponse{ID: request.ID, Outcome: ControlOK, Result: &ControlResult{
		SessionID: session.ID, Turn: &ended,
	}}
}

// sessionAnswerPermission delivers a developer's explicit decision to one open
// PermissionRequest. There is no automatic answer: an empty decision, an
// unknown request, and a second answer are all refused.
func (s *ControlService) sessionAnswerPermission(ctx context.Context, request ControlRequest) ControlResponse {
	state, failure := s.state(ctx)
	if failure != nil {
		return failure.response(request.ID)
	}
	session, failure := resolveControlSession(state, request.Args)
	if failure != nil {
		return failure.response(request.ID)
	}
	if refusal := requireManagedSession(session, "eine Freigabe"); refusal != nil {
		return refusal.response(request.ID)
	}
	decision, ok := parsePermissionDecision(request.Args.Decision)
	if !ok {
		return controlFailure(request.ID, ControlInvalidRequest,
			"Die Entscheidung muss allow oder deny sein — automatisch wird nichts beantwortet.")
	}
	requestID := strings.TrimSpace(request.Args.RequestID)
	if requestID == "" {
		return controlFailure(request.ID, ControlInvalidRequest,
			"Die Kennung der Berechtigungsanfrage fehlt.")
	}
	decidedBy := strings.TrimSpace(request.Args.DecidedBy)
	if decidedBy == "" {
		decidedBy = "Steuer-API"
	}
	answer := s.managedAnswer
	if answer == nil {
		answer = answerManagedPermissionThroughHost
	}
	closed, err := answer(ctx, session, requestID, decision, decidedBy)
	if err != nil {
		switch {
		case errors.Is(err, ErrPermissionUnknown):
			return controlFailure(request.ID, ControlNotFound,
				fmt.Sprintf("Unbekannte Berechtigungsanfrage %q.", requestID))
		case errors.Is(err, ErrPermissionClosed):
			return controlFailure(request.ID, ControlRefused,
				"Die Berechtigungsanfrage ist bereits geschlossen — eine zweite Antwort wird nicht zugestellt.")
		case errors.Is(err, ErrManagedHostUnreachable):
			return controlFailure(request.ID, ControlUnavailable,
				fmt.Sprintf("Der Agent-Host von %s ist nicht erreichbar — die Anfrage bleibt offen.", session.Name))
		case errors.Is(err, ErrAgentHostForeign):
			return controlFailure(request.ID, ControlUnavailable,
				fmt.Sprintf("Der Agent-Host von %s bestätigte das verzeichnete Token nicht.", session.Name))
		}
		return controlFailure(request.ID, ControlFailed, fmt.Sprintf("Die Entscheidung konnte nicht zugestellt werden: %v", err))
	}
	return ControlResponse{ID: request.ID, Outcome: ControlOK, Result: &ControlResult{
		SessionID: session.ID, Permission: &closed,
	}}
}

// parsePermissionDecision accepts exactly the two explicit developer
// decisions. Anything else — including empty — is not a decision.
func parsePermissionDecision(raw string) (PermissionDecision, bool) {
	switch PermissionDecision(strings.TrimSpace(strings.ToLower(raw))) {
	case PermissionAllow:
		return PermissionAllow, true
	case PermissionDeny:
		return PermissionDeny, true
	}
	return "", false
}
