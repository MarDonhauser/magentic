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
			Turn:      ManagedTurn{SessionID: session.ID, Running: true, MessageID: "msg-1"},
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

// StatusAwaitingDecision hängt hinten an: keine bereits gespeicherte Session
// wird umnummeriert, und die Darstellung ist von blocked unterscheidbar.
func TestAwaitingDecisionIsAppendedAndDistinct(t *testing.T) {
	if int(StatusAwaitingDecision) != int(StatusDone)+1 {
		t.Fatalf("StatusAwaitingDecision = %d, want eins nach StatusDone (%d)",
			int(StatusAwaitingDecision), int(StatusDone))
	}
	if StatusAwaitingDecision.Label() == StatusBlocked.Label() {
		t.Fatal("wartet-auf-Entscheidung muss sich von wartet unterscheiden lesen")
	}
	if StatusAwaitingDecision.Icon() == StatusBlocked.Icon() {
		t.Fatal("wartet-auf-Entscheidung braucht ein eigenes Zeichen")
	}
	if got := AgentStatusFromPersistedLabel(StatusAwaitingDecision.PersistedLabel()); got != StatusAwaitingDecision {
		t.Fatalf("PersistedLabel-Roundtrip = %v", got.Label())
	}
	if got := controlStatus(StatusAwaitingDecision); got != ControlStatusAwaitingDecision {
		t.Fatalf("Kontrollstatus = %q, want %q", got, ControlStatusAwaitingDecision)
	}
}

// ObserveSessions ist der Produktionseinstieg: ohne verzeichneten Host liest
// eine verwaltete Session unverfügbar — mit benanntem Dämon, nicht als tot.
func TestObserveSessionsReadsManagedWithoutTmux(t *testing.T) {
	t.Setenv("MAGENTIC_MANAGED_HOSTS", t.TempDir()+"/managed-hosts.json")
	managed := Session{ID: "session-m", Name: "managed", RuntimeName: "mgt-managed",
		Dir: "/tmp/alpha", Vendor: AgentVendorClaude, Runtime: RuntimeManaged,
		SessionKind: SessionKindCodingAgent}
	snapshot := ObserveSessions(context.Background(), []Session{managed})
	if len(snapshot.Sessions) != 1 {
		t.Fatalf("Sessions = %+v", snapshot.Sessions)
	}
	observed := snapshot.Sessions[0]
	if observed.Availability != ObservationUnavailable {
		t.Fatalf("Verfügbarkeit = %q, want unavailable", observed.Availability)
	}
	if observed.Status == StatusDead || observed.Presence == SessionPresenceAbsent {
		t.Fatalf("ohne Host darf nichts als tot lesen: %+v", observed)
	}
}

// 3.1/3.5/3.6: an omp Session is observed through its host only, and its
// status carries the omp protocol as its source. An open approval request
// outranks the running turn.
func TestObserveOmpSessionThroughItsHostOnly(t *testing.T) {
	session := Session{ID: "session-o", Name: "omp", RuntimeName: "mgt-omp", Dir: "/tmp/alpha",
		Vendor: AgentVendorOmp, Runtime: RuntimeOmp, SessionKind: SessionKindCodingAgent}
	running := AgentHostState{SessionID: session.ID, Alive: true, ProtocolTurnRunning: true,
		Turn: ManagedTurn{SessionID: session.ID, Running: true, StartedAt: time.Now()}, TurnKnown: true}
	noTmux := func(_ context.Context, _ []Session) ObservationSnapshot {
		t.Fatal("für eine omp-Session darf kein tmux-Befehl laufen")
		return ObservationSnapshot{}
	}
	observe := func(state AgentHostState) SessionObservation {
		snapshot := ObserveWithManaged(context.Background(), []Session{session}, noTmux,
			func(context.Context, Session) (AgentHostState, error) { return state, nil })
		return snapshot.Sessions[0]
	}

	observed := observe(running)
	if observed.Status != StatusRunning || observed.StatusSource != StatusSourceOmpProtocol {
		t.Fatalf("observed = %v/%q, want running from the omp protocol", observed.Status.Label(), observed.StatusSource)
	}
	asking := running
	asking.OpenPermissions = []PermissionRequest{{ID: "p", SessionID: session.ID, Asked: "Allow tool: bash", Open: true, RaisedAt: time.Now()}}
	if observed := observe(asking); observed.Status != StatusAwaitingDecision || observed.StatusSource != StatusSourceOmpProtocol {
		t.Fatalf("observed = %v/%q, want awaiting a decision from the omp protocol", observed.Status.Label(), observed.StatusSource)
	}
	if observed := observe(running); observed.Status != StatusRunning {
		t.Fatalf("after the answer the Session must read as running again, got %v", observed.Status.Label())
	}
}

// 3.7: an omp Session whose host cannot be reached is unobservable, not dead.
func TestObserveOmpSessionUnreachableHostIsUnobservable(t *testing.T) {
	session := Session{ID: "session-o", Name: "omp", Vendor: AgentVendorOmp, Runtime: RuntimeOmp,
		SessionKind: SessionKindCodingAgent}
	snapshot := ObserveWithManaged(context.Background(), []Session{session},
		func(_ context.Context, _ []Session) ObservationSnapshot {
			t.Fatal("für eine omp-Session darf kein tmux-Befehl laufen")
			return ObservationSnapshot{}
		},
		func(context.Context, Session) (AgentHostState, error) {
			return AgentHostState{}, ErrManagedHostUnreachable
		})
	observed := snapshot.Sessions[0]
	if observed.Availability != ObservationUnavailable || observed.Status != StatusUnknown || observed.Status == StatusExited {
		t.Fatalf("observed = %+v, want unobservable with unknown status", observed)
	}
}

// 3.2: between a turn_end and the next turn_start within a running agent
// run, the Session reads as idle rather than working — the agent run has
// not ended (no agent_end yet), but nothing is currently running either.
func TestObserveOmpSessionBetweenTurnsReadsAsIdle(t *testing.T) {
	session := Session{ID: "session-o", Name: "omp", Vendor: AgentVendorOmp, Runtime: RuntimeOmp,
		SessionKind: SessionKindCodingAgent}
	betweenTurns := AgentHostState{SessionID: session.ID, Alive: true, ProtocolTurnRunning: false,
		Turn: ManagedTurn{SessionID: session.ID, Running: true, StartedAt: time.Now()}, TurnKnown: true}
	snapshot := ObserveWithManaged(context.Background(), []Session{session},
		func(_ context.Context, _ []Session) ObservationSnapshot {
			t.Fatal("für eine omp-Session darf kein tmux-Befehl laufen")
			return ObservationSnapshot{}
		},
		func(context.Context, Session) (AgentHostState, error) { return betweenTurns, nil })
	observed := snapshot.Sessions[0]
	if observed.Status != StatusIdle {
		t.Fatalf("Status = %v, want idle between turn_end and the next turn_start", observed.Status.Label())
	}
}
