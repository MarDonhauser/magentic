package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func managedTestState() State {
	state := controlAddressState()
	for i, session := range state.Agents {
		if session.ID == "session-c" {
			session.Runtime = RuntimeManaged
			state.Agents[i] = session
		}
	}
	state.Agents = append(state.Agents, Session{
		ID: "session-m", Name: "managed", ProjectID: "projekt-a", Project: "alpha",
		RuntimeName: "mgt-managed", Dir: "/tmp/alpha", Vendor: AgentVendorClaude,
		Runtime: RuntimeManaged, SessionKind: SessionKindCodingAgent,
	})
	return state
}

func TestControlInterruptDispatchableForManaged(t *testing.T) {
	service, _, _ := controlTestService(managedTestState())
	ended := ManagedTurn{SessionID: "session-m", Running: false, EndReason: TurnEndInterrupted}
	service.managedInterrupt = func(context.Context, Session) (ManagedTurn, error) { return ended, nil }
	response := service.Dispatch(context.Background(), ControlRequest{
		ID: "r1", Verb: ControlSessionInterrupt, Args: ControlArgs{Session: "session-m"},
	})
	if response.Outcome != ControlOK {
		t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
	}
	if response.Result.Turn == nil || response.Result.Turn.EndReason != TurnEndInterrupted {
		t.Fatalf("Turn = %+v", response.Result)
	}
}

func TestControlInterruptRefusedForTmux(t *testing.T) {
	service, _, _ := controlTestService(controlDispatchState())
	service.managedInterrupt = func(context.Context, Session) (ManagedTurn, error) {
		t.Fatal("tmux darf den Host nie erreichen")
		return ManagedTurn{}, nil
	}
	response := service.Dispatch(context.Background(), ControlRequest{
		ID: "r2", Verb: ControlSessionInterrupt, Args: ControlArgs{Session: "session-c"},
	})
	if response.Outcome != ControlRefused {
		t.Fatalf("tmux-Interrupt = %q, want refused", response.Outcome)
	}
}

func TestControlInterruptRefusedWithoutRunningTurn(t *testing.T) {
	service, _, _ := controlTestService(managedTestState())
	service.managedInterrupt = func(context.Context, Session) (ManagedTurn, error) {
		return ManagedTurn{}, ErrManagedNoTurn
	}
	response := service.Dispatch(context.Background(), ControlRequest{
		ID: "r3", Verb: ControlSessionInterrupt, Args: ControlArgs{Session: "session-m"},
	})
	if response.Outcome != ControlRefused {
		t.Fatalf("ohne Turn = %q, want refused", response.Outcome)
	}
}

func TestControlAnswerPermissionDeliversOnce(t *testing.T) {
	service, _, _ := controlTestService(managedTestState())
	closed := PermissionRequest{ID: "perm-1", SessionID: "session-m", Open: false, Outcome: PermissionAllowed}
	service.managedAnswer = func(_ context.Context, _ Session, id string, decision PermissionDecision, by string) (PermissionRequest, error) {
		if id != "perm-1" || decision != PermissionAllow || by == "" {
			t.Fatalf("Antwort = %q/%q/%q", id, decision, by)
		}
		return closed, nil
	}
	response := service.Dispatch(context.Background(), ControlRequest{
		ID: "r4", Verb: ControlSessionAnswerPermission,
		Args: ControlArgs{Session: "session-m", RequestID: "perm-1", Decision: "allow"},
	})
	if response.Outcome != ControlOK || response.Result.Permission == nil {
		t.Fatalf("Antwort = %q/%+v (%s)", response.Outcome, response.Result, response.Message)
	}
}

func TestControlAnswerPermissionSecondAnswerRefused(t *testing.T) {
	service, _, _ := controlTestService(managedTestState())
	service.managedAnswer = func(context.Context, Session, string, PermissionDecision, string) (PermissionRequest, error) {
		return PermissionRequest{}, ErrPermissionClosed
	}
	response := service.Dispatch(context.Background(), ControlRequest{
		ID: "r5", Verb: ControlSessionAnswerPermission,
		Args: ControlArgs{Session: "session-m", RequestID: "perm-1", Decision: "deny"},
	})
	if response.Outcome != ControlRefused {
		t.Fatalf("zweite Antwort = %q, want refused", response.Outcome)
	}
}

func TestControlAnswerPermissionUnknownRequestNotFound(t *testing.T) {
	service, _, _ := controlTestService(managedTestState())
	service.managedAnswer = func(context.Context, Session, string, PermissionDecision, string) (PermissionRequest, error) {
		return PermissionRequest{}, ErrPermissionUnknown
	}
	response := service.Dispatch(context.Background(), ControlRequest{
		ID: "r6", Verb: ControlSessionAnswerPermission,
		Args: ControlArgs{Session: "session-m", RequestID: "fremd", Decision: "allow"},
	})
	if response.Outcome != ControlNotFound {
		t.Fatalf("unbekannt = %q, want not-found", response.Outcome)
	}
}

func TestControlAnswerPermissionRefusedForTmux(t *testing.T) {
	service, _, _ := controlTestService(controlDispatchState())
	service.managedAnswer = func(context.Context, Session, string, PermissionDecision, string) (PermissionRequest, error) {
		t.Fatal("tmux darf den Host nie erreichen")
		return PermissionRequest{}, nil
	}
	response := service.Dispatch(context.Background(), ControlRequest{
		ID: "r7", Verb: ControlSessionAnswerPermission,
		Args: ControlArgs{Session: "session-c", RequestID: "perm-1", Decision: "allow"},
	})
	if response.Outcome != ControlRefused {
		t.Fatalf("tmux-Antwort = %q, want refused", response.Outcome)
	}
}

func TestControlAnswerPermissionNeedsExplicitDecision(t *testing.T) {
	service, _, _ := controlTestService(managedTestState())
	called := false
	service.managedAnswer = func(context.Context, Session, string, PermissionDecision, string) (PermissionRequest, error) {
		called = true
		return PermissionRequest{}, nil
	}
	for _, decision := range []string{"", "maybe", "auto"} {
		response := service.Dispatch(context.Background(), ControlRequest{
			ID: "r8", Verb: ControlSessionAnswerPermission,
			Args: ControlArgs{Session: "session-m", RequestID: "perm-1", Decision: decision},
		})
		if response.Outcome != ControlInvalidRequest {
			t.Fatalf("Entscheidung %q = %q, want invalid-request", decision, response.Outcome)
		}
	}
	if called {
		t.Fatal("ohne explizite Entscheidung darf der Host nie erreicht werden")
	}
}

func TestControlInterruptUnreachableIsUnavailable(t *testing.T) {
	service, _, _ := controlTestService(managedTestState())
	service.managedInterrupt = func(context.Context, Session) (ManagedTurn, error) {
		return ManagedTurn{}, ErrManagedHostUnreachable
	}
	response := service.Dispatch(context.Background(), ControlRequest{
		ID: "r9", Verb: ControlSessionInterrupt, Args: ControlArgs{Session: "session-m"},
	})
	if response.Outcome != ControlUnavailable {
		t.Fatalf("unerreichbar = %q, want unavailable", response.Outcome)
	}
}

func TestManagedVerbsAreKnown(t *testing.T) {
	if !KnownControlVerb(ControlSessionInterrupt) || !KnownControlVerb(ControlSessionAnswerPermission) {
		t.Fatal("neue Verben sind nicht dispatchbar")
	}
	_ = time.Now
	_ = errors.Is
}
