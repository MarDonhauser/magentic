package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func claudeScanFor(t *testing.T) ConversationScan {
	t.Helper()
	return claudeConversationNormalizer{root: t.TempDir()}.NewScan()
}

// claudeRecords joins record lines into the byte range a reading covers.
func claudeRecords(lines ...string) []byte {
	return []byte(strings.Join(lines, "\n") + "\n")
}

func normalizeClaude(t *testing.T, lines ...string) []Item {
	t.Helper()
	items, consumed := claudeScanFor(t).Normalize(claudeRecords(lines...))
	if want := len(claudeRecords(lines...)); consumed != want {
		t.Fatalf("consumed = %d, want %d", consumed, want)
	}
	return items
}

func TestAnUnknownRecordTypeBecomesOneVisibleItem(t *testing.T) {
	items := normalizeClaude(t,
		`{"type":"weather-report","sessionId":"run-1","conditions":"bewölkt"}`)
	if len(items) != 1 {
		t.Fatalf("%d Items, want 1", len(items))
	}
	if items[0].Kind != ItemKindUnknown {
		t.Errorf("Kind = %q, want %q", items[0].Kind, ItemKindUnknown)
	}
	if items[0].VendorLabel != "weather-report" {
		t.Errorf("VendorLabel = %q, want %q", items[0].VendorLabel, "weather-report")
	}
	if !strings.Contains(items[0].Title, "weather-report") {
		t.Errorf("Titel %q nennt das Label des Vendors nicht", items[0].Title)
	}
}

func TestKnownMetadataRecordsProduceNoItem(t *testing.T) {
	for _, recordType := range []string{
		"attachment", "last-prompt", "mode", "permission-mode",
		"atis-latch", "ai-title", "queue-operation",
	} {
		items := normalizeClaude(t, `{"type":"`+recordType+`","sessionId":"run-1"}`)
		if len(items) != 0 {
			t.Errorf("Record-Typ %q ergibt %d Items, want 0", recordType, len(items))
		}
	}
}

func TestClaudeKindMappingTable(t *testing.T) {
	tests := []struct {
		name   string
		record string
		want   ItemKind
	}{
		{"Entwickler-Prompt", `{"type":"user","uuid":"u1","message":{"role":"user","content":"Bau das ein"}}`, ItemKindDeveloperPrompt},
		{"Agent-Antwort", `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"Erledigt"}]}}`, ItemKindAgentMessage},
		{"Überlegung", `{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Erst lesen"}]}}`, ItemKindReasoning},
		{"Bash", `{"type":"assistant","uuid":"a3","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls core"}}]}}`, ItemKindCommandExecution},
		{"Edit", `{"type":"assistant","uuid":"a4","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"core/timeline.go"}}]}}`, ItemKindFileChange},
		{"Write", `{"type":"assistant","uuid":"a5","message":{"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"Write","input":{"file_path":"core/x.go"}}]}}`, ItemKindFileChange},
		{"MultiEdit", `{"type":"assistant","uuid":"a6","message":{"role":"assistant","content":[{"type":"tool_use","id":"t4","name":"MultiEdit","input":{"file_path":"core/y.go"}}]}}`, ItemKindFileChange},
		{"NotebookEdit", `{"type":"assistant","uuid":"a7","message":{"role":"assistant","content":[{"type":"tool_use","id":"t5","name":"NotebookEdit","input":{"notebook_path":"nb.ipynb"}}]}}`, ItemKindFileChange},
		{"Read", `{"type":"assistant","uuid":"a8","message":{"role":"assistant","content":[{"type":"tool_use","id":"t6","name":"Read","input":{"file_path":"README.md"}}]}}`, ItemKindFileRead},
		{"Glob", `{"type":"assistant","uuid":"a9","message":{"role":"assistant","content":[{"type":"tool_use","id":"t7","name":"Glob","input":{"pattern":"core/*.go"}}]}}`, ItemKindFileRead},
		{"Grep", `{"type":"assistant","uuid":"a10","message":{"role":"assistant","content":[{"type":"tool_use","id":"t8","name":"Grep","input":{"pattern":"ItemKind"}}]}}`, ItemKindFileRead},
		{"WebSearch", `{"type":"assistant","uuid":"a11","message":{"role":"assistant","content":[{"type":"tool_use","id":"t9","name":"WebSearch","input":{"query":"go json lines"}}]}}`, ItemKindWebSearch},
		{"WebFetch", `{"type":"assistant","uuid":"a12","message":{"role":"assistant","content":[{"type":"tool_use","id":"t10","name":"WebFetch","input":{"url":"https://go.dev"}}]}}`, ItemKindWebSearch},
		{"Task", `{"type":"assistant","uuid":"a13","message":{"role":"assistant","content":[{"type":"tool_use","id":"t11","name":"Task","input":{"description":"Repo prüfen"}}]}}`, ItemKindDelegatedTask},
		{"Agent", `{"type":"assistant","uuid":"a14","message":{"role":"assistant","content":[{"type":"tool_use","id":"t12","name":"Agent","input":{"description":"Repo prüfen"}}]}}`, ItemKindDelegatedTask},
		{"TodoWrite", `{"type":"assistant","uuid":"a15","message":{"role":"assistant","content":[{"type":"tool_use","id":"t13","name":"TodoWrite","input":{"todos":[]}}]}}`, ItemKindPlan},
		{"MCP-Werkzeug", `{"type":"assistant","uuid":"a16","message":{"role":"assistant","content":[{"type":"tool_use","id":"t14","name":"mcp__chrome__navigate","input":{}}]}}`, ItemKindToolCall},
		{"Unbekanntes Werkzeug", `{"type":"assistant","uuid":"a17","message":{"role":"assistant","content":[{"type":"tool_use","id":"t15","name":"Skill","input":{}}]}}`, ItemKindToolCall},
		{"Verdichtung", `{"type":"system","uuid":"s1","subtype":"compact_boundary"}`, ItemKindContextCompaction},
		{"Sonstiges", `{"type":"system","uuid":"s2","subtype":"turn_duration"}`, ItemKindUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := normalizeClaude(t, tt.record)
			if len(items) != 1 {
				t.Fatalf("%d Items, want 1", len(items))
			}
			if items[0].Kind != tt.want {
				t.Fatalf("Kind = %q, want %q", items[0].Kind, tt.want)
			}
		})
	}
}

func TestMCPToolCallNamesItsServer(t *testing.T) {
	items := normalizeClaude(t,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"mcp__claude-in-chrome__navigate","input":{}}]}}`)
	if got := items[0].Title; !strings.Contains(got, "claude-in-chrome") || !strings.Contains(got, "navigate") {
		t.Fatalf("Titel = %q, want Server und Werkzeug", got)
	}
}

func TestToolResultAttachesToItsToolUse(t *testing.T) {
	call := `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls core"}}]}}`
	result := `{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"timeline.go"}]}}`

	items := normalizeClaude(t, call, result)
	if len(items) != 2 {
		t.Fatalf("%d Items, want 2", len(items))
	}
	if items[0].ID != items[1].ID {
		t.Fatalf("das Ergebnis ergibt eine neue Identität %q statt %q", items[1].ID, items[0].ID)
	}
	if items[1].AwaitingResult {
		t.Error("das abgeschlossene Item wartet weiter auf sein Ergebnis")
	}
	if items[1].Detail != "timeline.go" {
		t.Errorf("Detail = %q, want die Ausgabe des Werkzeugs", items[1].Detail)
	}
	if items[1].Failed {
		t.Error("ein erfolgreiches Ergebnis darf nicht als Fehler gelten")
	}
}

func TestToolResultInALaterReadingSupersedesInPlace(t *testing.T) {
	scan := claudeScanFor(t)
	first, _ := scan.Normalize(claudeRecords(
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls core"}}]}}`))
	conversation := Conversation{}
	conversation.Apply(first...)
	conversation.Apply(Item{ID: "spaeter", Kind: ItemKindAgentMessage, Title: "danach"})

	second, _ := scan.Normalize(claudeRecords(
		`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"kein Verzeichnis"}]}}`))
	if len(second) != 1 {
		t.Fatalf("%d Items in der zweiten Lesung, want 1", len(second))
	}
	conversation.Apply(second...)

	if len(conversation.Items) != 2 {
		t.Fatalf("Conversation hält %d Items, want 2", len(conversation.Items))
	}
	if conversation.Items[0].ID != first[0].ID {
		t.Fatalf("das ersetzte Item hat seine Stelle verlassen: %+v", conversation.Items)
	}
	if !conversation.Items[0].Failed || conversation.Items[0].AwaitingResult {
		t.Errorf("das Ergebnis ist nicht angekommen: %+v", conversation.Items[0])
	}
}

func TestAToolCallWithoutAResultStaysUnfinished(t *testing.T) {
	items := normalizeClaude(t,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"sleep 900"}}]}}`)
	if len(items) != 1 || !items[0].AwaitingResult {
		t.Fatalf("ein Werkzeugaufruf ohne Ergebnis muss unabgeschlossen bleiben: %+v", items)
	}
	if items[0].Failed {
		t.Error("ein ausstehendes Ergebnis ist kein Fehler")
	}
}

func TestIdentityIsStableAndPerBlock(t *testing.T) {
	record := `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"thinking","thinking":"nachdenken"},{"type":"text","text":"gleich"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`
	first := normalizeClaude(t, record)
	second := normalizeClaude(t, record)
	if len(first) != 3 {
		t.Fatalf("%d Items, want 3", len(first))
	}
	seen := map[string]bool{}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Errorf("Identität %d ist nicht stabil: %q gegen %q", i, first[i].ID, second[i].ID)
		}
		if seen[first[i].ID] {
			t.Errorf("Identität %q kommt mehrfach vor", first[i].ID)
		}
		seen[first[i].ID] = true
		if !strings.HasPrefix(first[i].ID, "a1#") {
			t.Errorf("Identität %q leitet sich nicht aus der uuid des Records ab", first[i].ID)
		}
	}
}

func TestDelegatedRecordsCarryTheirParentTask(t *testing.T) {
	items := normalizeClaude(t,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Task","input":{"description":"Repo prüfen"}}]}}`,
		`{"type":"assistant","uuid":"a2","isSidechain":true,"parentToolUseID":"t1","message":{"role":"assistant","content":[{"type":"text","text":"gefunden"}]}}`)
	if len(items) != 2 {
		t.Fatalf("%d Items, want 2", len(items))
	}
	if items[0].Delegated {
		t.Error("die delegierte Aufgabe selbst ist keine delegierte Arbeit")
	}
	if !items[1].Delegated {
		t.Error("ein isSidechain-Record muss als delegiert gelten")
	}
	if items[1].ParentTaskID != items[0].ID {
		t.Errorf("ParentTaskID = %q, want %q", items[1].ParentTaskID, items[0].ID)
	}
}

func TestDelegatedRecordWithoutAParentKeepsItUnknown(t *testing.T) {
	items := normalizeClaude(t,
		`{"type":"assistant","uuid":"a2","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"gefunden"}]}}`)
	if len(items) != 1 || !items[0].Delegated {
		t.Fatalf("Items = %+v, want ein delegiertes Item", items)
	}
	if items[0].ParentTaskID != "" {
		t.Errorf("ParentTaskID = %q, want unbekannt", items[0].ParentTaskID)
	}
}

func TestEveryItemCarriesATitle(t *testing.T) {
	items := normalizeClaude(t,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"Bau die Timeline"}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"Mache ich."},{"type":"thinking","thinking":""}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./core/"}}]}}`,
		`{"type":"assistant","uuid":"a3","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"core/timeline.go"}}]}}`,
		`{"type":"assistant","uuid":"a4","message":{"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"Read","input":{"file_path":"README.md"}}]}}`,
		`{"type":"assistant","uuid":"a5","message":{"role":"assistant","content":[{"type":"tool_use","id":"t4","name":"WebSearch","input":{"query":"jsonl anhängen"}}]}}`,
		`{"type":"assistant","uuid":"a6","message":{"role":"assistant","content":[{"type":"tool_use","id":"t5","name":"Task","input":{"description":"Repo prüfen"}}]}}`,
		`{"type":"assistant","uuid":"a7","message":{"role":"assistant","content":[{"type":"tool_use","id":"t6","name":"TodoWrite","input":{}}]}}`,
		`{"type":"assistant","uuid":"a8","message":{"role":"assistant","content":[{"type":"tool_use","id":"t7","name":"Skill","input":{}}]}}`,
		`{"type":"system","uuid":"s1","subtype":"compact_boundary"}`,
		`{"type":"cost-state","total":3}`,
	)
	titles := map[ItemKind]string{}
	for _, item := range items {
		if strings.TrimSpace(item.Title) == "" {
			t.Errorf("Item %q (%q) hat keinen Titel", item.ID, item.Kind)
		}
		if _, seen := titles[item.Kind]; !seen {
			titles[item.Kind] = item.Title
		}
	}
	want := map[ItemKind]string{
		ItemKindDeveloperPrompt:   "Bau die Timeline",
		ItemKindAgentMessage:      "Mache ich.",
		ItemKindReasoning:         "Überlegung",
		ItemKindCommandExecution:  "go test ./core/",
		ItemKindFileChange:        "core/timeline.go",
		ItemKindFileRead:          "README.md",
		ItemKindWebSearch:         "jsonl anhängen",
		ItemKindDelegatedTask:     "Repo prüfen",
		ItemKindPlan:              "Plan aktualisiert",
		ItemKindToolCall:          "Skill",
		ItemKindContextCompaction: "Kontext verdichtet",
	}
	for kind, wantTitle := range want {
		if got := titles[kind]; got != wantTitle {
			t.Errorf("Titel für %q = %q, want %q", kind, got, wantTitle)
		}
	}
	if titles[ItemKindUnknown] == "" {
		t.Error("ein unbekannter Record-Typ muss einen Titel tragen")
	}
}

func TestAPartiallyWrittenTrailingRecordIsDeferred(t *testing.T) {
	complete := `{"type":"user","uuid":"u1","message":{"role":"user","content":"fertig"}}` + "\n"
	partial := `{"type":"user","uuid":"u2","message":{"role":"user","con`

	scan := claudeScanFor(t)
	items, consumed := scan.Normalize([]byte(complete + partial))
	if len(items) != 1 {
		t.Fatalf("%d Items, want 1", len(items))
	}
	if consumed != len(complete) {
		t.Fatalf("consumed = %d, want %d — nur vollständige Records zählen", consumed, len(complete))
	}

	rest := `{"type":"user","uuid":"u2","message":{"role":"user","content":"jetzt vollständig"}}` + "\n"
	later, laterConsumed := scan.Normalize([]byte(rest))
	if len(later) != 1 || later[0].ID != "u2#0" {
		t.Fatalf("die zweite Lesung ergibt %+v, want genau das nachgetragene Item", later)
	}
	if laterConsumed != len(rest) {
		t.Fatalf("consumed = %d, want %d", laterConsumed, len(rest))
	}

	conversation := Conversation{}
	conversation.Apply(items...)
	conversation.Apply(later...)
	if len(conversation.Items) != 2 {
		t.Fatalf("Conversation hält %d Items, want 2", len(conversation.Items))
	}
}

func TestInjectedMetaTextIsNoDeveloperPrompt(t *testing.T) {
	items := normalizeClaude(t,
		`{"type":"user","uuid":"u1","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>…</local-command-caveat>"}}`)
	if len(items) != 0 {
		t.Fatalf("%d Items, want 0", len(items))
	}
}

func TestItemsSerializeForTransport(t *testing.T) {
	items := normalizeClaude(t,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`)
	encoded, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"awaitingResult":true`) {
		t.Errorf("der unabgeschlossene Aufruf überträgt seinen Zustand nicht: %s", encoded)
	}
}
