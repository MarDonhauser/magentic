package main

import (
	"fmt"
	"strings"

	"magentic/core"
)

// ManagedPermissionView is the one open PermissionRequest the conversation
// surface offers for a decision. A closed request is never offered: answering
// it a second time is refused by the host.
type ManagedPermissionView struct {
	ID          string `json:"id"`
	Asked       string `json:"asked"`
	RaisedAt    string `json:"raisedAt"`
	Open        bool   `json:"open"`
	Outcome     string `json:"outcome,omitempty"`
	CloseReason string `json:"closeReason,omitempty"`
}

// ManagedSessionStateResult is what the Agentenansicht reads for its control
// surface. Availability not-managed means this Session has no managed control
// at all; unavailable means the host cannot be reached and names its reason.
// Only available with an open permission or a running turn offers actions.
type ManagedSessionStateResult struct {
	SessionID   string                 `json:"sessionId"`
	Availability string                `json:"availability"`
	Reason      string                 `json:"reason,omitempty"`
	TurnRunning bool                   `json:"turnRunning"`
	Permission  *ManagedPermissionView `json:"permission,omitempty"`
}

// ManagedSessionState answers the control facts of one Session. It reads
// only; the Session's runtime is not touched.
func (a *App) ManagedSessionState(sessionID string) ManagedSessionStateResult {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return ManagedSessionStateResult{SessionID: sessionID, Availability: "unavailable", Reason: err.Error()}
	}
	if session.SessionRuntime() != core.RuntimeManaged {
		return ManagedSessionStateResult{SessionID: sessionID, Availability: "not-managed"}
	}
	registry := core.NewManagedHostRegistry()
	records, err := registry.Records()
	if err != nil {
		return ManagedSessionStateResult{SessionID: sessionID, Availability: "unavailable", Reason: err.Error()}
	}
	for _, record := range records {
		if record.SessionID != session.ID {
			continue
		}
		state, err := core.QueryAgentHostState(record.SocketPath, record.Token)
		if err != nil {
			return ManagedSessionStateResult{SessionID: sessionID, Availability: "unavailable", Reason: err.Error()}
		}
		result := ManagedSessionStateResult{
			SessionID: sessionID, Availability: "available",
			TurnRunning: state.TurnKnown && state.Turn.Running,
		}
		if len(state.OpenPermissions) > 0 {
			open := state.OpenPermissions[0]
			result.Permission = &ManagedPermissionView{
				ID: open.ID, Asked: open.Asked,
				RaisedAt: open.RaisedAt.Format("15:04:05"),
				Open: true,
			}
		}
		return result
	}
	return ManagedSessionStateResult{
		SessionID: sessionID, Availability: "unavailable",
		Reason: fmt.Sprintf("für Session %q ist kein Agent-Host verzeichnet", session.Name),
	}
}

// InterruptManagedTurn ends the running turn of a managed Session, leaving
// its process alive for the next prompt.
func (a *App) InterruptManagedTurn(sessionID string) error {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return err
	}
	if session.SessionRuntime() != core.RuntimeManaged {
		return fmt.Errorf("%s läuft nicht verwaltet — Interrupts gibt es nur für verwaltete Sessions", session.Name)
	}
	registry := core.NewManagedHostRegistry()
	records, err := registry.Records()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.SessionID != session.ID {
			continue
		}
		_, err := core.InterruptAgentHostTurn(record.SocketPath, record.Token)
		return err
	}
	return fmt.Errorf("für Session %q ist kein Agent-Host verzeichnet", session.Name)
}

// AnswerManagedPermission delivers a developer's explicit decision to one open
// PermissionRequest, exactly once. Decision must be allow or deny; anything
// else — including empty — is refused and never reaches the agent.
func (a *App) AnswerManagedPermission(sessionID, requestID, decision string) error {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return err
	}
	if session.SessionRuntime() != core.RuntimeManaged {
		return fmt.Errorf("%s läuft nicht verwaltet — Freigaben gibt es nur für verwaltete Sessions", session.Name)
	}
	trimmed := strings.TrimSpace(strings.ToLower(decision))
	var parsed core.PermissionDecision
	switch core.PermissionDecision(trimmed) {
	case core.PermissionAllow:
		parsed = core.PermissionAllow
	case core.PermissionDeny:
		parsed = core.PermissionDeny
	default:
		return fmt.Errorf("die Entscheidung muss allow oder deny sein")
	}
	if strings.TrimSpace(requestID) == "" {
		return fmt.Errorf("die Kennung der Berechtigungsanfrage fehlt")
	}
	registry := core.NewManagedHostRegistry()
	records, err := registry.Records()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.SessionID != session.ID {
			continue
		}
		_, err := core.AnswerAgentHostPermission(record.SocketPath, record.Token, strings.TrimSpace(requestID), parsed, "Desktop-App")
		return err
	}
	return fmt.Errorf("für Session %q ist kein Agent-Host verzeichnet", session.Name)
}
