package core

import (
	"bytes"
	"encoding/json"
	"strings"
)

// claudeRecord is the vendor-neutral decoding of one Claude Code JSONL
// record — ~/.claude/projects/<encoded-cwd>/<run-id>.jsonl and the sibling
// records Claude files per delegated task. WorkHistory's adapter projects a
// HistoryEvent from it for attribution, and the Agent Timeline's normalizer
// projects an Item from it for display; those are two different targets, but
// Claude Code's own grammar — field names, the meta rule, the sidechain
// rule, and the rule for text Claude injected itself rather than a developer
// typing it — is decided exactly once here so the two projections cannot
// drift apart on what the record actually says.
type claudeRecord struct {
	Type            string
	Subtype         string
	UUID            string
	Timestamp       string
	CWD             string
	SessionID       string
	IsMeta          bool
	IsSidechain     bool
	ParentToolUseID string
	ToolUseResult   json.RawMessage
	Model           string
	Content         json.RawMessage
	Usage           json.RawMessage
}

// decodeClaudeRecord parses one line of a Claude Code JSONL record. A line
// that is not valid JSON carries no record identity, so callers report it as
// malformed rather than deriving a partial record from it.
func decodeClaudeRecord(line []byte) (claudeRecord, bool) {
	var raw struct {
		Type            string          `json:"type"`
		Subtype         string          `json:"subtype"`
		UUID            string          `json:"uuid"`
		Timestamp       string          `json:"timestamp"`
		CWD             string          `json:"cwd"`
		SessionID       string          `json:"sessionId"`
		IsMeta          bool            `json:"isMeta"`
		IsSidechain     bool            `json:"isSidechain"`
		ParentToolUseID string          `json:"parentToolUseID"`
		ToolUseResult   json.RawMessage `json:"toolUseResult"`
		Message         struct {
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
			Usage   json.RawMessage `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &raw) != nil {
		return claudeRecord{}, false
	}
	return claudeRecord{
		Type: raw.Type, Subtype: raw.Subtype, UUID: raw.UUID, Timestamp: raw.Timestamp,
		CWD: raw.CWD, SessionID: raw.SessionID, IsMeta: raw.IsMeta, IsSidechain: raw.IsSidechain,
		ParentToolUseID: raw.ParentToolUseID, ToolUseResult: raw.ToolUseResult,
		Model: raw.Message.Model, Content: raw.Message.Content, Usage: raw.Message.Usage,
	}, true
}

// claudeContentBlock is one element of a Claude message's content when it is
// an array of typed blocks rather than one plain string.
type claudeContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// claudeTextBlocks reads a message content that is either an array of typed
// blocks or one plain string. A nil block slice means the content was plain.
func claudeTextBlocks(raw json.RawMessage) ([]claudeContentBlock, string) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, ""
	}
	if trimmed[0] == '[' {
		var blocks []claudeContentBlock
		if json.Unmarshal(trimmed, &blocks) != nil {
			return nil, ""
		}
		return blocks, ""
	}
	var text string
	_ = json.Unmarshal(trimmed, &text)
	return nil, text
}

// claudeJoinText joins a message's own text blocks. Thinking and tool blocks
// carry no prose meant to be read as a message — the Agent Timeline turns
// those into their own Items instead — so they contribute nothing here.
func claudeJoinText(blocks []claudeContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}
