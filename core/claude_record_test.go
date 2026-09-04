package core

import "testing"

func TestDecodeClaudeRecordReadsTheFieldsBothProjectionsNeed(t *testing.T) {
	line := []byte(`{"type":"user","subtype":"","uuid":"u1","timestamp":"2026-01-02T03:04:05Z","cwd":"/work","sessionId":"run-1","isMeta":true,"isSidechain":true,"parentToolUseID":"t1","toolUseResult":{"ok":true},"message":{"model":"claude-sonnet-5","content":"hallo","usage":{"input_tokens":3}}}`)
	record, ok := decodeClaudeRecord(line)
	if !ok {
		t.Fatal("ein gültiges Record muss dekodiert werden")
	}
	if record.Type != "user" || record.UUID != "u1" || record.Timestamp != "2026-01-02T03:04:05Z" {
		t.Errorf("Grundfelder falsch dekodiert: %+v", record)
	}
	if record.CWD != "/work" || record.SessionID != "run-1" {
		t.Errorf("CWD/SessionID falsch dekodiert: %+v", record)
	}
	if !record.IsMeta || !record.IsSidechain {
		t.Errorf("Meta-/Sidechain-Flags falsch dekodiert: %+v", record)
	}
	if record.ParentToolUseID != "t1" {
		t.Errorf("ParentToolUseID = %q, want %q", record.ParentToolUseID, "t1")
	}
	if len(record.ToolUseResult) == 0 {
		t.Error("ToolUseResult muss erhalten bleiben")
	}
	if record.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q, want %q", record.Model, "claude-sonnet-5")
	}
	if string(record.Content) != `"hallo"` {
		t.Errorf("Content = %s, want den rohen JSON-String", record.Content)
	}
	if len(record.Usage) == 0 {
		t.Error("Usage muss erhalten bleiben")
	}
}

func TestDecodeClaudeRecordRejectsAMalformedLine(t *testing.T) {
	if _, ok := decodeClaudeRecord([]byte(`{"type":"user",`)); ok {
		t.Fatal("eine kaputte Zeile darf kein Record ergeben")
	}
}

func TestDecodeClaudeRecordAcceptsMissingFields(t *testing.T) {
	record, ok := decodeClaudeRecord([]byte(`{"type":"summary"}`))
	if !ok {
		t.Fatal("ein Record ohne die meisten Felder ist noch immer gültiges JSON")
	}
	if record.Type != "summary" {
		t.Errorf("Type = %q, want %q", record.Type, "summary")
	}
	if record.UUID != "" || record.IsMeta || record.IsSidechain || record.CWD != "" {
		t.Errorf("fehlende Felder müssen ihre Nullwerte behalten: %+v", record)
	}
}

func TestClaudeTextBlocksReadsPlainStringContent(t *testing.T) {
	blocks, plain := claudeTextBlocks([]byte(`"Bau das ein"`))
	if blocks != nil {
		t.Fatalf("Blocks = %+v, want nil für reinen Text", blocks)
	}
	if plain != "Bau das ein" {
		t.Errorf("plain = %q, want %q", plain, "Bau das ein")
	}
}

func TestClaudeTextBlocksReadsAnArrayOfTypedBlocks(t *testing.T) {
	blocks, plain := claudeTextBlocks([]byte(`[{"type":"thinking","thinking":"erst lesen"},{"type":"text","text":"fertig"}]`))
	if plain != "" {
		t.Errorf("plain = %q, want leer für Block-Content", plain)
	}
	if len(blocks) != 2 || blocks[0].Type != "thinking" || blocks[1].Type != "text" {
		t.Fatalf("Blocks = %+v, want zwei typisierte Blöcke", blocks)
	}
}

func TestClaudeJoinTextKeepsOnlyTextBlocks(t *testing.T) {
	blocks, _ := claudeTextBlocks([]byte(`[{"type":"thinking","thinking":"erst lesen"},{"type":"text","text":"fertig"},{"type":"tool_use","name":"Bash"}]`))
	if got := claudeJoinText(blocks); got != "fertig" {
		t.Errorf("claudeJoinText = %q, want nur den Text-Block", got)
	}
}

// Der Regel für injizierten Prompt: WorkHistory und Agent Timeline müssen
// dieselbe Entscheidung treffen, damit ein Prompt entweder überall oder
// nirgends erscheint. historyInjectedText trägt diese Regel für Claude wie
// für die anderen Vendoren gemeinsam.
func TestInjectedPromptRuleAppliesToClaudeText(t *testing.T) {
	blocks, plain := claudeTextBlocks([]byte(`"<local-command-stdout>ls</local-command-stdout>"`))
	if blocks != nil {
		t.Fatalf("Blocks = %+v, want nil für reinen Text", blocks)
	}
	if !historyInjectedText(plain) {
		t.Error("von Claude injizierter Text muss als solcher erkannt werden")
	}
}
