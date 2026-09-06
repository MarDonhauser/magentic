package core

import (
	"testing"
)

// Die Host-Methoden sind über den Socket erreichbar: state liest, interrupt
// beendet den Turn und answer entscheidet genau einmal.
func TestAgentHostSocketExposesStateInterruptAnswer(t *testing.T) {
	testAgentHostEnv(t)
	token := NewAgentHostToken()
	host, err := StartAgentHost(SessionID("session-1"), token)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()

	state, err := QueryAgentHostState(host.Path(), token)
	if err != nil {
		t.Fatalf("state über Socket: %v", err)
	}
	if state.SessionID != "session-1" {
		t.Fatalf("SessionID = %q", state.SessionID)
	}

	if _, err := InterruptAgentHostTurn(host.Path(), token); err == nil {
		t.Fatal("ohne Turn muss interrupt über Socket verweigert werden")
	}

	request := host.OpenPermission("Darf ls laufen?")
	decided, err := AnswerAgentHostPermission(host.Path(), token, request.ID, PermissionAllow, "Test")
	if err != nil {
		t.Fatalf("answer über Socket: %v", err)
	}
	if decided.Outcome != PermissionAllowed || decided.Open {
		t.Fatalf("Anfrage = %+v, want geschlossen erlaubt", decided)
	}
	if _, err := AnswerAgentHostPermission(host.Path(), token, request.ID, PermissionDeny, "Test"); err == nil {
		t.Fatal("eine zweite Antwort muss auch über Socket verweigert werden")
	}
}
