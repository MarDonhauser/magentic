package remote

import (
	"context"
	"encoding/json"
	"testing"

	"magentic/core"
)

func tempState(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/state.json"
	t.Setenv("MAGENTIC_STATE", path)
	return path
}

func seedProject(t *testing.T, name, dir string) core.ProjectID {
	t.Helper()
	result, err := core.OpenRegistry(core.StatePath()).Change(context.Background(),
		core.RegisterProject(core.Project{Name: name, Path: dir}))
	if err != nil {
		t.Fatal(err)
	}
	state := result.Snapshot.State()
	project := state.ProjectByName(name)
	if project == nil {
		t.Fatal("Project nicht registriert")
	}
	return project.ID
}

// Ein Remote-Read liefert dieselbe Nutzlast wie der direkte lokale Aufruf.
// Stats läuft bewusst nicht mit: BuildStats parst die echte
// Arbeitsgeschichte der Maschine und ist damit langsam und umgebungs-
// abhängig; die Verdrahtung sieht man im Switch von HandleCall.
func TestRemoteReadMatchesLocalCall(t *testing.T) {
	tempState(t)
	projectID := seedProject(t, "demo", t.TempDir())
	backend := NewCoreBackend()

	remoteBoard, err := backend.HandleCall(context.Background(), "Board",
		EncodeParams(map[string]string{"projectID": string(projectID)}), "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := core.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	localBoard := core.BuildBoard(st, projectID)
	if !jsonEqual(t, remoteBoard, localBoard) {
		t.Error("Remote-Board weicht vom lokalen Aufruf ab")
	}

	remoteGraph, err := backend.HandleCall(context.Background(), "GitGraph",
		EncodeParams(map[string]any{"projectID": string(projectID), "limit": 10}), "")
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, remoteGraph, core.BuildGitGraph(st, projectID, 10)) {
		t.Error("Remote-GitGraph weicht vom lokalen Aufruf ab")
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	encodedA, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	encodedB, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(encodedA) == string(encodedB)
}

// Eine beschränkte Aktion ohne Opt-in erreicht das Backend nie: keine
// Nebenwirkung, gemeldet als beschränkt statt als Fehlschlag.
func TestRestrictedActionHasNoSideEffect(t *testing.T) {
	calls := 0
	backend := &StubBackend{Calls: map[string]func(params json.RawMessage, identity string) (any, error){
		"RemoveWorktree": func(params json.RawMessage, identity string) (any, error) {
			calls++
			return nil, nil
		},
	}}
	host, token, client := testHost(t, backend)
	status, response := callHost(t, client, host.Addr(), token, Request{
		Version: ProtocolVersion, ID: "1", Method: "RemoveWorktree",
		Params: EncodeParams(map[string]string{"projectID": "p1", "reference": "wt_x"}),
	})
	if status != 200 || response.Error == nil || response.Error.Code != ErrorRestricted {
		t.Fatalf("keine beschränkte Auskunft: Status %d, %+v", status, response)
	}
	if calls != 0 {
		t.Error("beschränkte Aktion erreichte das Backend")
	}
}

// Der Client liest dieselbe Klassifikation, die der Server erzwingt.
func TestPolicyDocumentMatchesEnforcement(t *testing.T) {
	for _, doc := range PolicyDocument() {
		entry, known := Classify(doc.Method)
		want := PolicyEntry{Class: doc.Class, Reason: doc.Reason}
		if !known || entry != want {
			t.Errorf("Policy-Dokument weicht für %s ab", doc.Method)
		}
		if err := EnforceRemote(doc.Method, nil); (err == nil) != (doc.Class == ActionPermitted) {
			t.Errorf("Durchsetzung weicht für %s ab", doc.Method)
		}
	}
}

// Dieselbe Transition-Identität zweimal: eine Transition, eine Nebenwirkung.
func TestIdentityResubmissionIsIdempotent(t *testing.T) {
	executions := 0
	ledger := newIdentityLedger()
	run := func() (any, error) {
		executions++
		return map[string]int{"n": executions}, nil
	}
	first, err := ledger.submit("transition-7", "SendMessage", run)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ledger.submit("transition-7", "SendMessage", run)
	if err != nil {
		t.Fatal(err)
	}
	if executions != 1 {
		t.Errorf("Nebenwirkung lief %d-mal statt einmal", executions)
	}
	if !jsonEqual(t, first, second) {
		t.Error("Wiederholung lieferte anderes Ergebnis")
	}
}

// Nach Annahme kein automatischer Wiederanlauf: Der Host wiederholt einen
// Aktionsversuch nicht von sich aus; eine zweite Einreichung derselben
// Identität schreitet die gemerkte Transition voran statt eine neue zu
// starten. Unbekannte Prompt-Zustellung bleibt unbekannt.
func TestNoAutoReplayAfterAccept(t *testing.T) {
	executions := 0
	ledger := newIdentityLedger()
	accept := func() (any, error) {
		executions++
		return map[string]string{"delivery": string(core.InitialPromptUnknown)}, nil
	}
	accepted, err := ledger.submit("transition-9", "NewSession", accept)
	if err != nil {
		t.Fatal(err)
	}
	// Verbindung bricht vor der Antwort weg — der Client liest neu statt neu
	// zu senden; käme dieselbe Identität doch wieder, liefe nichts erneut.
	_ = accepted
	repeated, err := ledger.submit("transition-9", "NewSession", accept)
	if err != nil {
		t.Fatal(err)
	}
	if executions != 1 {
		t.Errorf("Host wiederholte von sich aus (%d Ausführungen)", executions)
	}
	if !jsonEqual(t, accepted, repeated) {
		t.Error("Wiederholung wich vom angenommenen Ergebnis ab")
	}
	// Unbekannt bleibt unbekannt und wird nie als zugestellt gemeldet.
	encoded, _ := json.Marshal(repeated)
	var result map[string]string
	_ = json.Unmarshal(encoded, &result)
	if result["delivery"] != string(core.InitialPromptUnknown) {
		t.Errorf("Zustellung umgedeutet: %q", result["delivery"])
	}
}

// Pfad statt Handle wird an der Grenze abgewiesen, ohne das Dateisystem zu
// berühren.
func TestClientPathRejectedAtBoundary(t *testing.T) {
	tempState(t)
	backend := NewCoreBackend()
	_, err := backend.HandleCall(context.Background(), "RemoveWorktree", EncodeParams(
		map[string]string{"projectID": "p1", "reference": "/etc/passwd"}), "")
	if err == nil {
		t.Fatal("Client-Pfad akzeptiert")
	}
}
