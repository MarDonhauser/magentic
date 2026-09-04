package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestItemKindLabelsAreStableAndRoundTrip(t *testing.T) {
	cases := []struct {
		kind  ItemKind
		label string
	}{
		{ItemKindDeveloperPrompt, "developer-prompt"},
		{ItemKindAgentMessage, "agent-message"},
		{ItemKindReasoning, "reasoning"},
		{ItemKindPlan, "plan"},
		{ItemKindCommandExecution, "command-execution"},
		{ItemKindFileChange, "file-change"},
		{ItemKindFileRead, "file-read"},
		{ItemKindToolCall, "tool-call"},
		{ItemKindWebSearch, "web-search"},
		{ItemKindDelegatedTask, "delegated-task"},
		{ItemKindContextCompaction, "context-compaction"},
		{ItemKindPermissionRequest, "permission-request"},
		{ItemKindPermissionDecision, "permission-decision"},
		{ItemKindUnknown, "unknown"},
	}
	if len(cases) != len(ItemKinds()) {
		t.Fatalf("ItemKinds() hat %d Einträge, der Test deckt %d ab", len(ItemKinds()), len(cases))
	}
	for _, tc := range cases {
		if string(tc.kind) != tc.label {
			t.Errorf("Kind %v serialisiert als %q, erwartet %q", tc.kind, string(tc.kind), tc.label)
		}
		if got := ParseItemKind(tc.label); got != tc.kind {
			t.Errorf("ParseItemKind(%q) = %v, erwartet %v", tc.label, got, tc.kind)
		}
	}
	if got := ParseItemKind("tool-invocation"); got != ItemKindUnknown {
		t.Errorf("unbekanntes Label ergibt %v, erwartet %v", got, ItemKindUnknown)
	}
}

func TestItemWithoutDetailOrParentSerializesWithoutEmptyValues(t *testing.T) {
	item := Item{
		ID:         "uuid-1#0",
		OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Role:       ItemRoleAgent,
		Kind:       ItemKindAgentMessage,
		Title:      "Antwort",
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, absent := range []string{"detail", "parentTaskId", "delegated", "failed", "awaitingResult", "vendorLabel"} {
		if strings.Contains(string(encoded), `"`+absent+`"`) {
			t.Errorf("Feld %q darf ohne Wert nicht serialisiert werden: %s", absent, encoded)
		}
	}
	var back Item
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != item {
		t.Errorf("Round-Trip verändert das Item: %+v gegen %+v", back, item)
	}
}

func TestAppendingAKnownIdentityLeavesTheConversationUnchanged(t *testing.T) {
	conversation := Conversation{Ref: ConversationRef{Vendor: AgentVendorClaude, RunID: "run-1"}}
	conversation.Append(Item{ID: "a", Kind: ItemKindAgentMessage, Title: "erst"})
	conversation.Append(Item{ID: "a", Kind: ItemKindAgentMessage, Title: "später"})
	if len(conversation.Items) != 1 {
		t.Fatalf("Conversation hält %d Items, erwartet 1", len(conversation.Items))
	}
	if conversation.Items[0].Title != "erst" {
		t.Errorf("Append hat das vorhandene Item überschrieben: %q", conversation.Items[0].Title)
	}
}

func TestApplySupersedesInPlace(t *testing.T) {
	conversation := Conversation{}
	conversation.Apply(
		Item{ID: "a", Kind: ItemKindCommandExecution, Title: "ls", AwaitingResult: true},
		Item{ID: "b", Kind: ItemKindAgentMessage, Title: "fertig"},
	)
	conversation.Apply(Item{ID: "a", Kind: ItemKindCommandExecution, Title: "ls", Detail: "core app"})
	if len(conversation.Items) != 2 {
		t.Fatalf("Conversation hält %d Items, erwartet 2", len(conversation.Items))
	}
	if conversation.Items[0].ID != "a" || conversation.Items[0].AwaitingResult || conversation.Items[0].Detail != "core app" {
		t.Errorf("Supersession ersetzt das Item nicht an seiner Stelle: %+v", conversation.Items[0])
	}
}

func TestEveryUnavailableReadingIsDistinguishableAndCarriesAReason(t *testing.T) {
	ref := ConversationRef{Vendor: AgentVendorCodex, RunID: "run-9"}
	empty := AvailableConversation(Conversation{Ref: ref})
	if empty.Availability != ConversationAvailable {
		t.Fatalf("leere Conversation liest als %q", empty.Availability)
	}
	if empty.Conversation == nil || len(empty.Conversation.Items) != 0 {
		t.Fatalf("leere Conversation trägt keine leere Item-Liste: %+v", empty.Conversation)
	}

	seen := map[ConversationAvailability]bool{ConversationAvailable: true}
	for _, availability := range []ConversationAvailability{
		ConversationNotApplicable, ConversationNoNormalizer,
		ConversationRecordNotFound, ConversationRecordUnreadable,
	} {
		reading := UnavailableConversation(availability, ref, "Grund für "+string(availability))
		if seen[reading.Availability] {
			t.Errorf("Lesung %q ist nicht von einer anderen unterscheidbar", reading.Availability)
		}
		seen[reading.Availability] = true
		if reading.Reason == "" {
			t.Errorf("Lesung %q trägt keinen Grund", reading.Availability)
		}
		if reading.Conversation != nil {
			t.Errorf("Lesung %q trägt eine Conversation, obwohl sie nicht verfügbar ist", reading.Availability)
		}
	}
}

func TestUnavailableConversationAlwaysStatesAReason(t *testing.T) {
	reading := UnavailableConversation(ConversationRecordNotFound, ConversationRef{}, "")
	if reading.Reason == "" {
		t.Error("eine Lesung ohne übergebenen Grund muss trotzdem einen nennen")
	}
}

// TestEveryItemKindCarriesItsPresentation hält fest, dass jede Art der
// geschlossenen Menge eine Beschriftung und eine Einklappbarkeit hat. Genau
// das konnte die frühere Aufteilung auf vier Tabellen in zwei Sprachen nicht:
// permission-request und permission-decision fehlten in beiden JS-Tabellen und
// wären ohne Beschriftung und nicht einklappbar dargestellt worden.
func TestEveryItemKindCarriesItsPresentation(t *testing.T) {
	prose := map[ItemKind]bool{ItemKindDeveloperPrompt: true, ItemKindAgentMessage: true}
	seen := map[string]ItemKind{}
	for _, kind := range ItemKinds() {
		label := ItemLabel(kind)
		if label == "" {
			t.Errorf("Art %q hat keine Beschriftung", kind)
		}
		if kind != ItemKindUnknown && label == ItemLabel(ItemKindUnknown) {
			t.Errorf("Art %q fällt auf die Beschriftung der unbekannten Art zurück", kind)
		}
		if other, doubled := seen[label]; doubled {
			t.Errorf("Arten %q und %q teilen die Beschriftung %q", other, kind, label)
		}
		seen[label] = kind

		if want := !prose[kind]; ItemCollapsible(kind) != want {
			t.Errorf("Art %q: Einklappbarkeit %v, erwartet %v", kind, ItemCollapsible(kind), want)
		}
	}
}

// TestConversationFillsPresentationOnEntry hält fest, dass die Darstellung am
// Eintritt in eine Conversation entsteht. Kein Erzeuger kann sie vergessen,
// und eine Ersetzung trägt sie genauso.
func TestConversationFillsPresentationOnEntry(t *testing.T) {
	var conversation Conversation
	conversation.Append(Item{ID: "t1", Kind: ItemKindCommandExecution, Title: "go build"})
	conversation.Append(Item{ID: "m1", Kind: ItemKindAgentMessage, Title: "fertig"})

	if got := conversation.Items[0]; got.Label != "Befehl" || !got.Collapsible {
		t.Errorf("Werkzeugarbeit: Label %q, Collapsible %v", got.Label, got.Collapsible)
	}
	if got := conversation.Items[1]; got.Label != "Antwort" || got.Collapsible {
		t.Errorf("Prosa: Label %q, Collapsible %v", got.Label, got.Collapsible)
	}

	conversation.Apply(Item{ID: "t1", Kind: ItemKindCommandExecution, Title: "go build", Detail: "ok"})
	if got := conversation.Items[0]; got.Label != "Befehl" || !got.Collapsible {
		t.Errorf("ersetztes Item verlor seine Darstellung: Label %q, Collapsible %v", got.Label, got.Collapsible)
	}
}
