package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestObserveManagedIssuesNoTmuxCall(t *testing.T) {
	managed := Session{ID: "session-m", Name: "managed", RuntimeName: "mgt-managed",
		Dir: "/tmp/alpha", Vendor: AgentVendorClaude, Runtime: RuntimeManaged,
		SessionKind: SessionKindCodingAgent}
	tmux := Session{ID: "session-c", Name: "classic", RuntimeName: "mgt-classic",
		Dir: "/tmp/alpha", Vendor: AgentVendorClaude, SessionKind: SessionKindCodingAgent}
	calledWith := [][]Session{}
	tmuxObserve := func(_ context.Context, sessions []Session) ObservationSnapshot {
		calledWith = append(calledWith, append([]Session(nil), sessions...))
		return ObservationSnapshot{
			Availability: ObservationAvailable, ObservedAt: time.Now().UTC(),
			Sessions: []SessionObservation{{
				SessionID: "session-c", Availability: ObservationAvailable,
				Presence: SessionPresencePresent, Status: StatusRunning,
				StatusSource: StatusSourceSnapshot, Attention: AttentionWorking,
			}},
		}
	}
	provider := func(_ context.Context, session Session) (AgentHostState, error) {
		return AgentHostState{SessionID: session.ID, Alive: true,
			Turn: ManagedTurn{SessionID: session.ID, Running: true, MessageID: "msg-1"},
			TurnKnown: true}, nil
	}
	snapshot := ObserveWithManaged(context.Background(), []Session{tmux, managed}, tmuxObserve, provider)
	if len(calledWith) != 1 || len(calledWith[0]) != 1 || calledWith[0][0].ID != "session-c" {
		t.Fatalf("tmux-Beobachtung erhielt %+v, want nur die tmux-Session", calledWith)
	}
	byID := map[SessionID]SessionObservation{}
	for _, observed := range snapshot.Sessions {
		byID[observed.SessionID] = observed
	}
	if byID["session-m"].Status != StatusRunning {
		t.Fatalf("managed Status = %v, want läuft", byID["session-m"].Status.Label())
	}
	if byID["session-m"].Availability != ObservationAvailable {
		t.Fatalf("managed Verfügbarkeit = %q", byID["session-m"].Availability)
	}
}

func TestObserveManagedWaitingForDecisionIsOwnStatus(t *testing.T) {
	raised := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	managed := Session{ID: "session-m", Name: "managed", RuntimeName: "mgt-managed",
		Dir: "/tmp/alpha", Vendor: AgentVendorClaude, Runtime: RuntimeManaged,
		SessionKind: SessionKindCodingAgent, SeenAt: raised.Add(-time.Minute)}
	provider := func(_ context.Context, _ Session) (AgentHostState, error) {
		return AgentHostState{SessionID: "session-m", Alive: true,
			OpenPermissions: []PermissionRequest{{
				ID: "perm-1", SessionID: "session-m", Asked: "Darf npm publish laufen?",
				RaisedAt: raised, Open: true,
			}}}, nil
	}
	snapshot := ObserveWithManaged(context.Background(), []Session{managed},
		func(_ context.Context, _ []Session) ObservationSnapshot {
			t.Fatal("für eine verwaltete Session darf kein tmux-Befehl laufen")
			return ObservationSnapshot{}
		}, provider)
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("Sessions = %+v", snapshot.Sessions)
	}
	observed := snapshot.Sessions[0]
	if observed.Status != StatusAwaitingDecision {
		t.Fatalf("Status = %v, want wartet auf Entscheidung", observed.Status.Label())
	}
	if observed.Status == StatusRunning || observed.Status == StatusIdle || observed.Status == StatusBlocked {
		t.Fatal("wartet auf Entscheidung muss von läuft, idle und wartet unterscheidbar sein")
	}
	if observed.Attention != AttentionNeedsInput {
		t.Fatalf("Aufmerksamkeit = %q, want needs-input", observed.Attention)
	}
	if !observed.Unread {
		t.Fatal("eine neue Freigabe muss als ungelesen zählen")
	}
	if observed.Detail != "Darf npm publish laufen?" {
		t.Fatalf("Detail = %q", observed.Detail)
	}
}

func TestObserveManagedUnreachableIsUnobservableNotDead(t *testing.T) {
	managed := Session{ID: "session-m", Name: "managed", RuntimeName: "mgt-managed",
		Dir: "/tmp/alpha", Runtime: RuntimeManaged, SessionKind: SessionKindCodingAgent}
	provider := func(_ context.Context, _ Session) (AgentHostState, error) {
		return AgentHostState{}, ErrManagedHostUnreachable
	}
	snapshot := ObserveWithManaged(context.Background(), []Session{managed}, nil, provider)
	observed := snapshot.Sessions[0]
	if observed.Availability != ObservationUnavailable {
		t.Fatalf("Verfügbarkeit = %q, want unavailable", observed.Availability)
	}
	if observed.Status == StatusDead || observed.Presence == SessionPresenceAbsent {
		t.Fatalf("unerreichbar darf nicht als tot lesen: %+v", observed)
	}
}

func TestManagedUnexpectedExitIsFailedWithReason(t *testing.T) {
	managed := Session{ID: "session-m", Name: "managed", RuntimeName: "mgt-managed",
		Dir: "/tmp/alpha", Runtime: RuntimeManaged, SessionKind: SessionKindCodingAgent}
	provider := func(_ context.Context, _ Session) (AgentHostState, error) {
		return AgentHostState{SessionID: "session-m", Alive: false, ExitReason: "exit status 1"}, nil
	}
	snapshot := ObserveWithManaged(context.Background(), []Session{managed}, nil, provider)
	observed := snapshot.Sessions[0]
	if observed.Status != StatusExited {
		t.Fatalf("Status = %v, want beendet", observed.Status.Label())
	}
	if observed.Detail == "" {
		t.Fatal("ein unerwarteter Exit muss seinen Grund nennen")
	}
}

func TestPermissionRequestAndOutcomeAreOrderedItems(t *testing.T) {
	store := NewPermissionStore()
	request := store.Open("session-m", "Darf rm laufen?")
	if err := store.Answer(request.ID, PermissionDeny, "Steuer-API"); err != nil {
		t.Fatal(err)
	}
	closed := store.closedRequest(request.ID)
	items := []Item{PermissionRequestItem(request), PermissionOutcomeItem(closed)}
	if items[0].Kind != ItemKindPermissionRequest || items[1].Kind != ItemKindPermissionDecision {
		t.Fatalf("Items = %+v", items)
	}
	if !items[1].OccurredAt.After(items[0].OccurredAt) && !items[1].OccurredAt.Equal(items[0].OccurredAt) {
		t.Fatal("die Entscheidung muss nach der Frage eingeordnet sein")
	}
	turns := NewManagedTurns("session-m")
	turns.CompleteMessage(items[0])
	turns.CompleteMessage(items[1])
	conversation := turns.StreamedConversation()
	if len(conversation) != 2 {
		t.Fatalf("Konversation = %+v, want Frage und Antwort", conversation)
	}
}

func TestNoAutomaticPermissionDecision(t *testing.T) {
	for _, mode := range PermissionDecisionModes() {
		if mode != PermissionAllow && mode != PermissionDeny {
			t.Fatalf("Modus %q beantwortet ohne Entwickler", mode)
		}
	}
	store := NewPermissionStore()
	request := store.Open("session-m", "Darf etwas laufen?")
	time.Sleep(20 * time.Millisecond)
	if open := store.OpenRequests(); len(open) != 1 || open[0].ID != request.ID {
		t.Fatalf("ohne Interface muss die Anfrage offen bleiben: %+v", open)
	}
	_ = errors.Is
}
