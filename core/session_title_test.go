package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanSessionTitleKeepsOnlyTheTitle(t *testing.T) {
	cases := map[string]string{
		"Hydra Sessions ausblenden":                       "Hydra Sessions ausblenden",
		"  „Zeitstempel validieren“.\n\n**Next:** fertig": "Zeitstempel validieren",
		"# Titel: Deploy-Pipeline reparieren!":            "Deploy-Pipeline reparieren",
		"**Kachel-Sichtbarkeit umschaltbar**":             "Kachel-Sichtbarkeit umschaltbar",
		"":                                                "",
		"Ein sehr langer Titel der weit über die Grenze geht": "Ein sehr langer Titel der weit über die",
	}
	for raw, want := range cases {
		if got := CleanSessionTitle(raw); got != want {
			t.Errorf("CleanSessionTitle(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestDigestConversationIgnoresDelegatedWork(t *testing.T) {
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	conversation := Conversation{Items: []Item{
		{ID: "1", OccurredAt: start, Role: ItemRoleDeveloper, Kind: ItemKindDeveloperPrompt, Title: "Hydra-Kacheln", Detail: "ausblendbar machen"},
		{ID: "2", OccurredAt: start.Add(time.Minute), Role: ItemRoleAgent, Kind: ItemKindAgentMessage, Title: "Erledigt"},
		{ID: "3", OccurredAt: start.Add(2 * time.Minute), Role: ItemRoleDeveloper, Kind: ItemKindDeveloperPrompt, Title: "Und noch etwas"},
		{ID: "4", OccurredAt: start.Add(3 * time.Minute), Role: ItemRoleAgent, Kind: ItemKindAgentMessage, Title: "Subagent", Delegated: true},
		{ID: "5", OccurredAt: start.Add(4 * time.Minute), Role: ItemRoleAgent, Kind: ItemKindAgentMessage, Title: "Streamt", InProgress: true},
	}}
	digest := DigestConversation(conversation)
	if digest.FirstPrompt != "Hydra-Kacheln\nausblendbar machen" {
		t.Fatalf("FirstPrompt = %q", digest.FirstPrompt)
	}
	if !digest.LastResult.OccurredAt.Equal(start.Add(time.Minute)) {
		t.Fatalf("LastResult = %v, want the agent's own last message", digest.LastResult.OccurredAt)
	}
}

func TestRegistryStoresTitleAndResultTime(t *testing.T) {
	registry := OpenRegistry(filepath.Join(t.TempDir(), "state.json"))
	session := Session{ID: "session-t", Name: "magentic-2", Dir: "/workspace/project"}
	ctx := context.Background()
	if _, err := registry.Change(ctx, RegisterSession(session)); err != nil {
		t.Fatal(err)
	}

	result, err := registry.Change(ctx, SetSessionTitle(session.ID, session.Name, "  Hydra Sessions ausblenden "))
	if err != nil || !result.Applied {
		t.Fatalf("SetSessionTitle = %+v, %v", result, err)
	}
	state := result.Snapshot.State()
	saved := state.SessionByID(session.ID)
	if saved.Title != "Hydra Sessions ausblenden" || saved.Name != "magentic-2" {
		t.Fatalf("title must be stored beside the unchanged name: %+v", saved)
	}
	if got := toOvAgent(*saved, SessionObservation{}, "", SessionResumability{}); got.Title != saved.Title {
		t.Fatalf("overview projection lost the title: %+v", got)
	}

	working := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	if _, err := registry.Change(ctx, RecordSessionStatus(session.ID, session.Name, StatusRunning, working)); err != nil {
		t.Fatal(err)
	}
	finished := working.Add(3 * time.Minute)
	result, err = registry.Change(ctx, RecordSessionStatus(session.ID, session.Name, StatusIdle, finished))
	if err != nil {
		t.Fatal(err)
	}
	state = result.Snapshot.State()
	if saved := state.SessionByID(session.ID); !saved.ResultAt.Equal(finished) {
		t.Fatalf("ResultAt = %v, want the end of the turn %v", saved.ResultAt, finished)
	}

	result, err = registry.Change(ctx, RecordSessionStatus(session.ID, session.Name, StatusIdle, finished.Add(5*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	state = result.Snapshot.State()
	if saved := state.SessionByID(session.ID); !saved.ResultAt.Equal(finished) {
		t.Fatalf("a resting Session observed again moved ResultAt to %v", saved.ResultAt)
	}

	result, err = registry.Change(ctx, RecordSessionResult(session.ID, session.Name, working))
	if err != nil || result.Applied {
		t.Fatalf("an older result read from the record must not rewind ResultAt: %+v, %v", result, err)
	}
}
