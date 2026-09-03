package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"magentic/core"
)

// conversationAppFixture lays out a Session, its state file and — unless the
// caller suppresses it — the Claude record it resolves to.
func conversationAppFixture(t *testing.T, runID string, records ...string) (*App, core.Session, string) {
	t.Helper()
	return conversationAppFixtureFor(t, runID, func(s core.Session) core.Session { return s }, records...)
}

func conversationAppFixtureFor(t *testing.T, runID string, shape func(core.Session) core.Session, records ...string) (*App, core.Session, string) {
	t.Helper()
	home, _, projectPath, statePath := configureHistoryAppTest(t)
	session := shape(core.Session{
		ID: core.SessionID("s-" + runID), Name: "navi", Project: "NAVI", Dir: projectPath,
		RuntimeName: "magentic-navi", SessionKind: core.SessionKindCodingAgent,
		Vendor:    core.AgentVendorClaude,
		AgentRuns: []core.AgentRunRef{{Vendor: core.AgentVendorClaude, ExternalID: runID}},
	})
	writeAppState(t, statePath, core.State{
		Projects: []core.Project{{ID: "p1", Name: "NAVI", Path: projectPath}},
		Agents:   []core.Session{session},
	})
	record := filepath.Join(home, ".claude", "projects", "-project", runID+".jsonl")
	if len(records) > 0 {
		writeAppFixture(t, record, strings.Join(records, "\n")+"\n")
	}
	return NewApp(), session, record
}

func appendConversationRecords(t *testing.T, path string, records ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(records, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func developerPrompt(uuid, text string) string {
	return `{"type":"user","uuid":"` + uuid + `","message":{"role":"user","content":"` + text + `"}}`
}

func TestSessionConversationDeliversItems(t *testing.T) {
	app, session, _ := conversationAppFixture(t, "run-1", developerPrompt("u1", "erste Frage"))
	app.WatchConversation(string(session.ID))

	result := app.SessionConversation(string(session.ID))
	if result.Availability != string(core.ConversationAvailable) {
		t.Fatalf("Availability = %q, want %q", result.Availability, core.ConversationAvailable)
	}
	if !result.ItemsKnown || len(result.Items) != 1 {
		t.Fatalf("Items = %+v (bekannt: %v), want ein Item", result.Items, result.ItemsKnown)
	}
	if result.Items[0].Kind != core.ItemKindDeveloperPrompt {
		t.Errorf("Kind = %q, want %q", result.Items[0].Kind, core.ItemKindDeveloperPrompt)
	}
}

func TestEachUnavailableReadingHasItsOwnTransportValue(t *testing.T) {
	keep := func(s core.Session) core.Session { return s }
	tests := []struct {
		name    string
		shape   func(core.Session) core.Session
		records []string
		want    core.ConversationAvailability
	}{
		{
			name:  "Aufzeichnung fehlt",
			shape: keep,
			want:  core.ConversationRecordNotFound,
		},
		{
			name: "Vendor ohne Normalizer",
			shape: func(s core.Session) core.Session {
				s.Vendor = core.AgentVendorCodex
				s.AgentRuns = []core.AgentRunRef{{Vendor: core.AgentVendorCodex, ExternalID: "run-1"}}
				return s
			},
			records: []string{developerPrompt("u1", "erste Frage")},
			want:    core.ConversationNoNormalizer,
		},
		{
			name: "Session ohne Coding-Agenten",
			shape: func(s core.Session) core.Session {
				s.SessionKind = core.SessionKindTerminal
				s.Kind = core.KindTerm
				return s
			},
			records: []string{developerPrompt("u1", "erste Frage")},
			want:    core.ConversationNotApplicable,
		},
	}
	seen := map[string]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, session, _ := conversationAppFixtureFor(t, "run-1", tt.shape, tt.records...)
			result := app.SessionConversation(string(session.ID))
			if result.Availability != string(tt.want) {
				t.Fatalf("Availability = %q, want %q", result.Availability, tt.want)
			}
			if seen[result.Availability] {
				t.Fatalf("Lesung %q ist nicht von einer anderen unterscheidbar", result.Availability)
			}
			seen[result.Availability] = true
			if result.ItemsKnown {
				t.Error("eine nicht verfügbare Lesung darf keine Items behaupten")
			}
			if result.Reason == "" {
				t.Error("eine nicht verfügbare Lesung muss ihren Grund nennen")
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), `"items":[]`) {
				t.Errorf("eine nicht verfügbare Lesung serialisiert als leere Item-Liste: %s", encoded)
			}
		})
	}
}

func TestAnAvailableEmptyConversationCarriesAnEmptyItemList(t *testing.T) {
	app, session, _ := conversationAppFixture(t, "run-1", `{"type":"mode","mode":"normal"}`)
	result := app.SessionConversation(string(session.ID))
	if result.Availability != string(core.ConversationAvailable) || !result.ItemsKnown {
		t.Fatalf("Ergebnis = %+v, want verfügbar und leer", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"items":[]`) {
		t.Errorf("eine leere, verfügbare Conversation überträgt keine leere Item-Liste: %s", encoded)
	}
}

func TestOneObservationPassPublishesOnlyTheNewItems(t *testing.T) {
	app, session, record := conversationAppFixture(t, "run-1", developerPrompt("u1", "erste Frage"))
	app.WatchConversation(string(session.ID))

	first := app.publishConversationUpdates([]core.Session{session})
	if len(first) != 1 || len(first[0].Items) != 1 {
		t.Fatalf("erster Durchlauf = %+v, want ein Item", first)
	}

	if again := app.publishConversationUpdates([]core.Session{session}); len(again) != 0 {
		t.Fatalf("ein Durchlauf ohne neue Records veröffentlicht %+v, want nichts", again)
	}

	appendConversationRecords(t, record, developerPrompt("u2", "zweite Frage"))
	later := app.publishConversationUpdates([]core.Session{session})
	if len(later) != 1 || len(later[0].Items) != 1 {
		t.Fatalf("der Durchlauf nach dem Anhängen = %+v, want genau das neue Item", later)
	}
	if later[0].Items[0].ID != "u2#0" {
		t.Errorf("veröffentlicht wurde %q, want das angehängte Item", later[0].Items[0].ID)
	}
	if later[0].Replaced {
		t.Error("ein gewachsenes Record darf keine vollständige Neulesung auslösen")
	}
}

func TestAPassPublishesExactlyOneEventPerConversation(t *testing.T) {
	app, session, _ := conversationAppFixture(t, "run-1",
		developerPrompt("u1", "erste Frage"),
		developerPrompt("u2", "zweite Frage"))
	app.WatchConversation(string(session.ID))

	events := app.publishConversationUpdates([]core.Session{session})
	if len(events) != 1 {
		t.Fatalf("%d Ereignisse, want 1", len(events))
	}
	if len(events[0].Items) != 2 {
		t.Fatalf("das Ereignis trägt %d Items, want 2", len(events[0].Items))
	}
	if events[0].SessionID != string(session.ID) {
		t.Errorf("SessionID = %q, want %q", events[0].SessionID, session.ID)
	}
}

func TestAnUnwatchedSessionIsNotPublished(t *testing.T) {
	app, session, _ := conversationAppFixture(t, "run-1", developerPrompt("u1", "erste Frage"))
	if events := app.publishConversationUpdates([]core.Session{session}); len(events) != 0 {
		t.Fatalf("eine nicht betrachtete Session veröffentlicht %+v, want nichts", events)
	}
}
