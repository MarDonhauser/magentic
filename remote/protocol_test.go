package remote

import (
	"encoding/json"
	"testing"

	"magentic/core"
)

// Jede Fehlergestalt übersteht den Draht unverändert: Der Client muss eine
// abgewiesene Anmeldung von einem Transportproblem unterscheiden können.
func TestWireErrorRoundTrip(t *testing.T) {
	cases := []WireError{
		{Code: ErrorAuth, Message: "HostToken unbekannt"},
		{Code: ErrorRestricted, Message: "RemoveWorktree ist beschränkt"},
		{Code: ErrorObservation, Message: "list-panes: timed out"},
		{Code: ErrorTransport, Message: "Verbindung abgebrochen"},
		{Code: ErrorVersion, Message: "Protokollversion passt nicht"},
	}
	for _, want := range cases {
		encoded, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var got WireError
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("Rundweg verändert den Fehler: %+v statt %+v", got, want)
		}
	}
}

func TestRequestResponseRoundTrip(t *testing.T) {
	request := Request{
		Version:  ProtocolVersion,
		ID:       "req-1",
		Method:   "SendMessage",
		Params:   EncodeParams(map[string]string{"sessionID": "abc", "text": "hallo"}),
		Identity: "transition-9",
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Request
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Identity != "transition-9" || decoded.Method != "SendMessage" {
		t.Errorf("Request-Rundweg verloren: %+v", decoded)
	}
	response := Response{
		Version: ProtocolVersion,
		ID:      "req-1",
		Result:  EncodeParams(map[string]string{"name": "hera"}),
	}
	encoded, err = json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decodedResponse Response
	if err := json.Unmarshal(encoded, &decodedResponse); err != nil {
		t.Fatal(err)
	}
	if decodedResponse.Error != nil || decodedResponse.ID != "req-1" {
		t.Errorf("Response-Rundweg verloren: %+v", decodedResponse)
	}
}

// Der Handshake verweigert alles außer der einen gemeinsamen Version.
func TestHandshakeRefusesMismatch(t *testing.T) {
	if _, wireErr := CheckHandshake(Hello{Version: ProtocolVersion}); wireErr != nil {
		t.Errorf("passender Handshake verweigert: %v", wireErr)
	}
	if _, wireErr := CheckHandshake(Hello{Version: ProtocolVersion + 1}); wireErr == nil {
		t.Error("fremde Version akzeptiert")
	} else if wireErr.Code != ErrorVersion {
		t.Errorf("falsche Fehlergestalt: %v", wireErr)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	term := TermFrame(41, []byte("hallo agent\r\n"))
	encoded, err := MarshalFrame(term)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalFrame(encoded)
	if err != nil {
		t.Fatal(err)
	}
	data, err := decoded.TermBytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hallo agent\r\n" || decoded.Seq != 41 {
		t.Errorf("Terminal-Rundweg verloren: %+v", decoded)
	}
}

// Die Lücke reist als eigene Rahmenart mit neuem Ursprung und Snapshot.
func TestGapFrameRoundTrip(t *testing.T) {
	gap := GapFrame(100, []byte("frischer pane-inhalt"))
	encoded, err := MarshalFrame(gap)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalFrame(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != FrameGap || decoded.Seq != 100 {
		t.Errorf("Lücken-Rahmen verloren: %+v", decoded)
	}
	snapshot, err := decoded.TermBytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshot) != "frischer pane-inhalt" {
		t.Errorf("Snapshot verloren: %q", snapshot)
	}
}

func TestUnmarshalFrameRefusesUnknownKind(t *testing.T) {
	if _, err := UnmarshalFrame([]byte(`{"kind":"pixel-strom","seq":1}`)); err == nil {
		t.Error("unbekannte Rahmenart akzeptiert — fail-closed verletzt")
	}
}

// Die hostseitige Unverfügbarkeits-Begründung übersteht die Serialisierung:
// „Host konnte tmux nicht beobachten" bleibt lesbar und von einem
// Transportausfall unterscheidbar.
func TestHostUnavailabilitySurvivesSerialization(t *testing.T) {
	want := core.ObservationSnapshot{
		Availability:      core.ObservationUnavailable,
		Transport:         core.ObservationTransportRemote,
		TransportProblem:  "",
		Problems: []core.ObservationProblem{
			{Operation: "list-panes", Message: "timed out", TimedOut: true},
		},
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got core.ObservationSnapshot
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Availability != core.ObservationUnavailable {
		t.Errorf("Verfügbarkeit verloren: %q", got.Availability)
	}
	if got.Transport != core.ObservationTransportRemote {
		t.Errorf("Transport-Herkunft verloren: %q", got.Transport)
	}
	if len(got.Problems) != 1 || got.Problems[0].Operation != "list-panes" {
		t.Errorf("Host-Begründung verloren: %+v", got.Problems)
	}
}
