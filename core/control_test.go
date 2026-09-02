package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestControlRequestRoundTripsEveryVerb(t *testing.T) {
	args := ControlArgs{
		Session: "session-1", Project: "projekt", Worktree: "wt_abc", NewWorktree: true,
		Directory: "/tmp/projekt", Name: "review", Kind: SessionKindCodingAgent,
		Vendor: AgentVendorClaude, Prompt: "Bitte prüfen", Text: "weiter",
		Lines: 40, Until: "done", TimeoutMS: 5000,
		Marker: ControlMarker{SessionID: "session-1", ProjectID: "projekt-1"},
	}
	for _, verb := range ControlVerbs() {
		t.Run(string(verb), func(t *testing.T) {
			want := ControlRequest{ID: "req-1", Verb: verb, Args: args}
			encoded, err := json.Marshal(want)
			if err != nil {
				t.Fatalf("Anfrage nicht kodierbar: %v", err)
			}
			if strings.ContainsAny(string(encoded), "\n") {
				t.Fatalf("Anfrage enthält einen Zeilenumbruch: %s", encoded)
			}
			got, err := DecodeControlRequest(encoded)
			if err != nil {
				t.Fatalf("Anfrage nicht dekodierbar: %v", err)
			}
			if got != want {
				t.Fatalf("Anfrage = %+v, want %+v", got, want)
			}
		})
	}
}

func TestControlResponseRoundTrips(t *testing.T) {
	want := ControlResponse{
		ID: "req-2", Outcome: ControlWaitOccupantReplaced, Message: "Belegung wurde ersetzt",
		Result: &ControlResult{
			SessionID: "session-1",
			Occupant:  &ControlOccupant{SessionID: "session-1", RuntimeName: "mgt-eins", Run: AgentRunRef{Vendor: AgentVendorClaude, ExternalID: "run-a"}},
			Observed:  &ControlOccupant{SessionID: "session-1", RuntimeName: "mgt-zwei"},
		},
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Antwort nicht kodierbar: %v", err)
	}
	var got ControlResponse
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Antwort nicht dekodierbar: %v", err)
	}
	if got.ID != want.ID || got.Outcome != want.Outcome || got.Message != want.Message {
		t.Fatalf("Antwort-Kopf = %+v, want %+v", got, want)
	}
	if got.Result == nil || !got.Result.Occupant.Same(*want.Result.Occupant) {
		t.Fatalf("Belegung ging verloren: %+v", got.Result)
	}
}

func TestDecodeControlRequestRejectsMalformed(t *testing.T) {
	for _, line := range []string{"", "{", "nicht json", `{"id":"a"}`, `{"verb":"  "}`} {
		if _, err := DecodeControlRequest([]byte(line)); err == nil {
			t.Fatalf("%q wurde als Anfrage akzeptiert", line)
		}
	}
}

func TestControlOutcomeCodesAreUniqueAndStable(t *testing.T) {
	// The codes are a contract with agents parsing our output: a rename is a
	// breaking change, so the expected set is spelled out literally here.
	want := []string{
		"ok", "not-found", "ambiguous", "no-target", "containment", "refused",
		"unavailable", "not-managed", "unauthorized", "invalid-request",
		"unknown-verb", "failed", "subscriber-stalled",
		"done", "waiting", "blocked", "occupant-replaced", "session-gone",
		"timeout", "cancelled", "no-occupant",
	}
	outcomes := ControlOutcomes()
	if len(outcomes) != len(want) {
		t.Fatalf("Ergebnis-Codes = %v, want %v", outcomes, want)
	}
	seen := map[ControlOutcome]bool{}
	for i, outcome := range outcomes {
		if seen[outcome] {
			t.Fatalf("Ergebnis-Code %q kommt doppelt vor", outcome)
		}
		seen[outcome] = true
		if string(outcome) != want[i] {
			t.Fatalf("Ergebnis-Code %d = %q, want %q", i, outcome, want[i])
		}
	}
	for _, outcome := range ControlWaitOutcomes() {
		if !seen[outcome] {
			t.Fatalf("Warte-Ergebnis %q fehlt im Gesamtsatz", outcome)
		}
	}
	for _, outcome := range []ControlOutcome{ControlNotFound, ControlAmbiguous, ControlNoTarget, ControlContainment} {
		if !ControlAddressingOutcome(outcome) {
			t.Fatalf("%q zählt nicht als Adressierungsfehler", outcome)
		}
	}
	for _, outcome := range []ControlOutcome{ControlOK, ControlRefused, ControlUnavailable, ControlFailed} {
		if ControlAddressingOutcome(outcome) {
			t.Fatalf("%q zählt fälschlich als Adressierungsfehler", outcome)
		}
	}
}

func TestKnownControlVerb(t *testing.T) {
	for _, verb := range ControlVerbs() {
		if !KnownControlVerb(verb) {
			t.Fatalf("%q gilt nicht als bekannter Verb", verb)
		}
	}
	if KnownControlVerb("session.restart") {
		t.Fatal("unbekannter Verb wurde akzeptiert")
	}
}
