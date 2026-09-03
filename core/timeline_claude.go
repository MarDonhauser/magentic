package core

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// claudeConversationNormalizer reads Claude Code's own JSON Lines record —
// ~/.claude/projects/<encoded-cwd>/<run-id>.jsonl — and turns it into Items.
// It only ever reads: the record belongs to Claude Code.
type claudeConversationNormalizer struct{ root string }

func (claudeConversationNormalizer) Vendor() AgentVendor { return AgentVendorClaude }

func (n claudeConversationNormalizer) Locate(ref ConversationRef) (string, bool) {
	if ref.Vendor != AgentVendorClaude || strings.TrimSpace(ref.RunID) == "" {
		return "", false
	}
	matches := vendorRunMatches(n.root, ref.RunID, func(name, id string) bool {
		return name == id+".jsonl"
	})
	if len(matches) == 0 {
		return "", false
	}
	// A run identity is unique across projects; sorting keeps a duplicated
	// record deterministic rather than dependent on walk order.
	best := matches[0]
	for _, match := range matches[1:] {
		if filepath.ToSlash(match) < filepath.ToSlash(best) {
			best = match
		}
	}
	return best, true
}

func (claudeConversationNormalizer) NewScan() ConversationScan {
	return &claudeConversationScan{
		open:  map[string]Item{},
		tasks: map[string]string{},
	}
}

// claudeActivityRecordTypes are the record types that carry agent activity.
var claudeActivityRecordTypes = map[string]bool{
	"user":      true,
	"assistant": true,
	"system":    true,
}

// claudeMetadataRecordTypes are the record types Claude writes as session
// bookkeeping. They are known and deliberately produce no Item. A record type
// in neither list becomes an Item of unknown kind carrying its own label, so a
// new Claude record shows up as a visible row rather than as a gap.
var claudeMetadataRecordTypes = map[string]bool{
	"attachment":      true,
	"last-prompt":     true,
	"mode":            true,
	"permission-mode": true,
	"atis-latch":      true,
	"ai-title":        true,
	"queue-operation": true,
}

type claudeConversationRecord struct {
	Type            string `json:"type"`
	Subtype         string `json:"subtype"`
	UUID            string `json:"uuid"`
	Timestamp       string `json:"timestamp"`
	IsMeta          bool   `json:"isMeta"`
	IsSidechain     bool   `json:"isSidechain"`
	ParentToolUseID string `json:"parentToolUseID"`
	Message         struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

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

// claudeConversationScan normalizes one record file. It holds the tool calls
// still waiting for their result, so a result appended in a later reading
// completes the Item published earlier, and the tool-use ids of delegated
// tasks, so a subagent record can name the Item it belongs to.
type claudeConversationScan struct {
	open  map[string]Item
	tasks map[string]string
}

func (s *claudeConversationScan) Normalize(data []byte) ([]Item, int) {
	var items []Item
	consumed := 0
	for consumed < len(data) {
		end := bytes.IndexByte(data[consumed:], '\n')
		if end < 0 {
			// A trailing record without its newline is still being written.
			// It stays unconsumed and is normalized once complete.
			break
		}
		line := bytes.TrimSpace(data[consumed : consumed+end])
		consumed += end + 1
		if len(line) == 0 {
			continue
		}
		items = append(items, s.normalizeRecord(line)...)
	}
	return items, consumed
}

func (s *claudeConversationScan) normalizeRecord(line []byte) []Item {
	var record claudeConversationRecord
	if json.Unmarshal(line, &record) != nil {
		// A line that is not JSON carries no record identity, so no stable
		// Item could be derived from it.
		return nil
	}
	// Claude gives every activity record a uuid. The bookkeeping records do
	// not carry one, so an unrecognized record falls back to the hash of its
	// own bytes — still stable across readings, and still distinct from its
	// neighbours.
	recordID := record.UUID
	if recordID == "" {
		recordID = "line-" + basicHistoryFingerprint(line)[:16]
	}

	switch {
	case claudeMetadataRecordTypes[record.Type]:
		return nil
	case !claudeActivityRecordTypes[record.Type]:
		return []Item{s.unknownItem(record, recordID, record.Type, record.Type)}
	}

	switch record.Type {
	case "system":
		if record.Subtype == "compact_boundary" {
			item := s.baseItem(record, recordID, 0)
			item.Role = ItemRoleSystem
			item.Kind = ItemKindContextCompaction
			item.Title = "Kontext verdichtet"
			return []Item{item}
		}
		label := "system"
		if record.Subtype != "" {
			label += ":" + record.Subtype
		}
		return []Item{s.unknownItem(record, recordID, label, label)}
	case "user":
		return s.normalizeUserRecord(record, recordID)
	default:
		return s.normalizeAssistantRecord(record, recordID)
	}
}

func (s *claudeConversationScan) normalizeUserRecord(record claudeConversationRecord, recordID string) []Item {
	blocks, plain := claudeContentBlocks(record.Message.Content)
	if blocks == nil {
		// A user record whose content is plain text is a developer prompt,
		// unless Claude injected it as meta text of its own.
		if record.IsMeta || strings.TrimSpace(plain) == "" {
			return nil
		}
		return []Item{s.promptItem(record, recordID, 0, plain)}
	}
	var items []Item
	for index, block := range blocks {
		switch block.Type {
		case "tool_result":
			if completed, ok := s.completeToolCall(block); ok {
				items = append(items, completed)
			}
		case "text":
			if record.IsMeta || strings.TrimSpace(block.Text) == "" {
				continue
			}
			items = append(items, s.promptItem(record, recordID, index, block.Text))
		}
	}
	return items
}

func (s *claudeConversationScan) promptItem(record claudeConversationRecord, recordID string, index int, text string) Item {
	item := s.baseItem(record, recordID, index)
	item.Role = ItemRoleDeveloper
	item.Kind = ItemKindDeveloperPrompt
	item.Title = claudeTitleLine(text, "Eingabe")
	item.Detail = claudeDetailBeyond(item.Title, text)
	return item
}

func (s *claudeConversationScan) normalizeAssistantRecord(record claudeConversationRecord, recordID string) []Item {
	blocks, plain := claudeContentBlocks(record.Message.Content)
	if blocks == nil {
		if strings.TrimSpace(plain) == "" {
			return nil
		}
		item := s.baseItem(record, recordID, 0)
		item.Role = ItemRoleAgent
		item.Kind = ItemKindAgentMessage
		item.Title = claudeTitleLine(plain, "Antwort")
		item.Detail = claudeDetailBeyond(item.Title, plain)
		return []Item{item}
	}
	var items []Item
	for index, block := range blocks {
		item := s.baseItem(record, recordID, index)
		item.Role = ItemRoleAgent
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) == "" {
				continue
			}
			item.Kind = ItemKindAgentMessage
			item.Title = claudeTitleLine(block.Text, "Antwort")
			item.Detail = claudeDetailBeyond(item.Title, block.Text)
		case "thinking":
			item.Kind = ItemKindReasoning
			item.Title = claudeTitleLine(block.Thinking, "Überlegung")
			item.Detail = claudeDetailBeyond(item.Title, block.Thinking)
		case "tool_use":
			item = s.toolUseItem(item, block)
		default:
			label := block.Type
			if label == "" {
				label = "block"
			}
			item.Kind = ItemKindUnknown
			item.VendorLabel = label
			item.Title = "Unbekannter Eintrag: " + label
		}
		items = append(items, item)
	}
	return items
}

func (s *claudeConversationScan) toolUseItem(item Item, block claudeContentBlock) Item {
	input := claudeToolInput(block.Input)
	item.Kind, item.Title = claudeToolKindAndTitle(block.Name, input)
	item.AwaitingResult = true
	if block.ID != "" {
		s.open[block.ID] = item
		if item.Kind == ItemKindDelegatedTask {
			s.tasks[block.ID] = item.ID
		}
	}
	return item
}

// completeToolCall supersedes the tool-call Item the result names, supplying
// its detail and whether it failed. A result whose call was never seen — the
// call sits before the range this reading covers — completes nothing.
func (s *claudeConversationScan) completeToolCall(block claudeContentBlock) (Item, bool) {
	item, known := s.open[block.ToolUseID]
	if !known {
		return Item{}, false
	}
	delete(s.open, block.ToolUseID)
	item.AwaitingResult = false
	item.Failed = block.IsError
	if detail := claudeResultText(block.Content); detail != "" {
		item.Detail = detail
	}
	return item, true
}

func (s *claudeConversationScan) baseItem(record claudeConversationRecord, recordID string, index int) Item {
	item := Item{
		ID:         claudeItemID(recordID, index),
		OccurredAt: claudeRecordTime(record.Timestamp),
		Role:       ItemRoleAgent,
	}
	if record.IsSidechain {
		item.Delegated = true
		// An unnamed parent stays explicitly unknown rather than being
		// guessed from position.
		item.ParentTaskID = s.tasks[record.ParentToolUseID]
	}
	return item
}

func (s *claudeConversationScan) unknownItem(record claudeConversationRecord, recordID, label, title string) Item {
	item := s.baseItem(record, recordID, 0)
	item.Role = ItemRoleSystem
	item.Kind = ItemKindUnknown
	item.VendorLabel = label
	item.Title = "Unbekannter Eintrag: " + title
	return item
}

// claudeItemID extends the record's own identity by the block index, so one
// assistant record holding several blocks yields several stable identities.
func claudeItemID(recordID string, index int) string {
	return recordID + "#" + strconv.Itoa(index)
}

// claudeDetailBeyond keeps a detail only when it carries more than the title
// already says, so a one-line message renders without an empty expandable body.
func claudeDetailBeyond(title, text string) string {
	detail := strings.TrimSpace(text)
	if detail == title {
		return ""
	}
	return detail
}

func claudeRecordTime(value string) time.Time {
	stamp, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return stamp.UTC()
}

// claudeContentBlocks reads a message content that is either an array of typed
// blocks or one plain string. A nil block slice means the content was plain.
func claudeContentBlocks(raw json.RawMessage) ([]claudeContentBlock, string) {
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

// claudeResultText reads a tool result's content, which Claude writes either
// as one string or as an array of text blocks.
func claudeResultText(raw json.RawMessage) string {
	blocks, plain := claudeContentBlocks(raw)
	if blocks == nil {
		return strings.TrimSpace(plain)
	}
	var parts []string
	for _, block := range blocks {
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func claudeToolInput(raw json.RawMessage) map[string]json.RawMessage {
	input := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return input
	}
	_ = json.Unmarshal(raw, &input)
	return input
}

func claudeInputString(input map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		var text string
		if json.Unmarshal(input[key], &text) == nil && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

// claudeToolKindAndTitle is the mapping table from design.md. The title is a
// presentation fact decided here, so no interface has to know a tool name.
func claudeToolKindAndTitle(name string, input map[string]json.RawMessage) (ItemKind, string) {
	switch name {
	case "Bash":
		return ItemKindCommandExecution, claudeTitleLine(
			claudeInputString(input, "command"), "Befehl")
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return ItemKindFileChange, claudeTitleLine(
			claudeInputString(input, "file_path", "notebook_path"), "Datei geändert")
	case "Read", "Glob", "Grep":
		return ItemKindFileRead, claudeTitleLine(
			claudeInputString(input, "file_path", "pattern", "path"), "Gelesen")
	case "WebSearch", "WebFetch":
		return ItemKindWebSearch, claudeTitleLine(
			claudeInputString(input, "query", "url", "prompt"), "Websuche")
	case "Task", "Agent":
		return ItemKindDelegatedTask, claudeTitleLine(
			claudeInputString(input, "description", "prompt"), "Delegierte Aufgabe")
	case "TodoWrite":
		return ItemKindPlan, "Plan aktualisiert"
	}
	if strings.HasPrefix(name, "mcp__") {
		return ItemKindToolCall, claudeMCPTitle(name)
	}
	if strings.TrimSpace(name) == "" {
		return ItemKindToolCall, "Werkzeug"
	}
	return ItemKindToolCall, name
}

// claudeMCPTitle renders an MCP tool name as server and tool, so the row says
// where the capability came from.
func claudeMCPTitle(name string) string {
	parts := strings.Split(strings.TrimPrefix(name, "mcp__"), "__")
	if len(parts) < 2 {
		return name
	}
	return parts[0] + " · " + strings.Join(parts[1:], " · ")
}

// claudeTitleLine reduces text to one line suitable for a collapsed row. An
// empty text falls back to the kind's own wording, so no Item is titleless.
func claudeTitleLine(text, fallback string) string {
	line := strings.TrimSpace(text)
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = strings.TrimSpace(line[:index]) + " …"
	}
	line = strings.Join(strings.Fields(line), " ")
	if line == "" || line == "…" {
		return fallback
	}
	const limit = 120
	if runes := []rune(line); len(runes) > limit {
		line = strings.TrimSpace(string(runes[:limit])) + "…"
	}
	return line
}
