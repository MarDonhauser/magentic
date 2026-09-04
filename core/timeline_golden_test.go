package core

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "die Golden-Dateien neu schreiben")

// TestGoldenClaudeConversation normalizes real Claude records and pins the
// full Item sequence. It is the regression net for a Claude format change:
// the golden output must keep showing what the agent did, and an unrecognized
// record must keep showing up as an unknown row rather than disappearing.
func TestGoldenClaudeConversation(t *testing.T) {
	root := filepath.Join("testdata", "timeline")
	normalizer := claudeConversationNormalizer{root: root}
	sources, ok := normalizer.Locate(ConversationRef{Vendor: AgentVendorClaude, RunID: "claude-run"}, nil)
	if !ok {
		t.Fatal("das Fixture-Record wurde nicht gefunden")
	}
	if len(sources) != 2 {
		t.Fatalf("%d Quellen, want 2 (Lauf und ein Subagent): %+v", len(sources), sources)
	}

	conversation := Conversation{Ref: ConversationRef{Vendor: AgentVendorClaude, RunID: "claude-run"}}
	scan := normalizer.NewScan()
	for _, source := range sources {
		data, err := os.ReadFile(source.Path)
		if err != nil {
			t.Fatalf("%s: %v", source.Path, err)
		}
		items, consumed := scan.Normalize(source, data)
		if consumed != len(data) {
			t.Fatalf("%s: consumed = %d, want %d", source.Path, consumed, len(data))
		}
		conversation.Apply(items...)
	}

	encoded, err := json.MarshalIndent(conversation.Items, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	encoded = append(encoded, '\n')
	golden := filepath.Join(root, "claude-run.golden.json")
	if *updateGolden {
		if err := os.WriteFile(golden, encoded, 0o644); err != nil {
			t.Fatalf("Golden schreiben: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("Golden lesen: %v (mit -update neu schreiben)", err)
	}
	if string(encoded) != string(want) {
		t.Errorf("die normalisierte Item-Folge weicht vom Golden ab:\n%s", encoded)
	}

	assertGoldenCoverage(t, conversation.Items)
}

// assertGoldenCoverage states what the fixture has to exercise, so shrinking
// it later fails loudly instead of quietly weakening the regression net.
func assertGoldenCoverage(t *testing.T, items []Item) {
	t.Helper()
	kinds := map[ItemKind]int{}
	completedTool, delegatedWithParent := 0, 0
	for _, item := range items {
		kinds[item.Kind]++
		if item.Detail != "" && !item.AwaitingResult &&
			(item.Kind == ItemKindCommandExecution || item.Kind == ItemKindFileChange) {
			completedTool++
		}
		if item.Delegated && item.ParentTaskID != "" {
			delegatedWithParent++
		}
	}
	for _, kind := range []ItemKind{
		ItemKindDeveloperPrompt, ItemKindAgentMessage, ItemKindReasoning,
		ItemKindCommandExecution, ItemKindFileChange, ItemKindDelegatedTask,
	} {
		if kinds[kind] == 0 {
			t.Errorf("das Fixture deckt %q nicht ab", kind)
		}
	}
	if completedTool == 0 {
		t.Error("das Fixture enthält keinen Werkzeugaufruf mit angekommenem Ergebnis")
	}
	if delegatedWithParent == 0 {
		t.Error("das Fixture enthält keine delegierte Arbeit unter ihrer Aufgabe")
	}

	// The metadata record in the fixture must produce no Item at all.
	data, err := os.ReadFile(filepath.Join("testdata", "timeline", "claude-run.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	metadata := 0
	_ = historyJSONLines(data, func(line []byte, _ int) {
		var record struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &record) == nil && claudeMetadataRecordTypes[record.Type] {
			metadata++
		}
	})
	if metadata == 0 {
		t.Error("das Fixture enthält keinen Metadaten-Record")
	}
}
