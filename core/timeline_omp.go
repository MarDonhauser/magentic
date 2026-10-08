package core

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
)

// ompConversationNormalizer declares omp's normalizer capability through the
// same registry every vendor uses (providerForVendor(...).Normalizer()), so
// core/timeline.go needs no second registry path.
//
// Unlike a vendor read from its OWN on-disk record, Locate here finds a file
// Magentic itself wrote — never omp's own session files, which are
// deliberately never read (agent-timeline/conversation-reading and
// omp-runtime/conversation-normalization both require it). The agent host
// is what writes it: on each protocol frame it normalizes through
// OmpFrameScan below and appends the result durably
// (AppendOmpConversationItems), independent of whether any interface is
// watching. From ConversationReader's side that file behaves exactly like
// any vendor's own growing record — the same incremental, prefix-fingerprinted
// reading the on-disk path already gives every other vendor, restated to
// hold for a record Magentic itself produces.
type ompConversationNormalizer struct{}

func (ompConversationNormalizer) Vendor() AgentVendor { return AgentVendorOmp }

// Locate finds the durable record AppendOmpConversationItems writes for this
// run, if anything has been appended to it yet. A Session whose host has not
// observed any Item yet has no file, which reads as an available empty
// Conversation rather than a missing recording (ConversationReader.Read
// distinguishes the two by whether Locate ever succeeded before).
func (ompConversationNormalizer) Locate(ref ConversationRef, _ []ConversationSource) ([]ConversationSource, bool) {
	if ref.RunID == "" {
		return nil, false
	}
	path := OmpConversationStorePath(ref.RunID)
	if _, err := os.Stat(path); err != nil {
		return nil, false
	}
	return []ConversationSource{{Path: path}}, true
}

// NewScan reads back what the agent host already normalized and persisted;
// see ompDurableScan.
func (ompConversationNormalizer) NewScan() ConversationScan { return ompDurableScan{} }

// OmpFrameScan normalizes omp's rpc-ui event stream into Items, one decoded
// frame at a time. It is not driven by appended bytes of a record: a caller
// feeds it frames as they arrive on the protocol connection, or replays a
// recorded stream, and applies the returned Items to a Conversation with
// Conversation.Apply — the same call every other vendor's Items go through.
//
// Item identities are derived from omp's own stable facts, and never from
// arrival order, so replaying the same frames twice yields identical
// identities:
//   - an assistant message is keyed by its responseId ("asst#<responseId>#<blockIndex>"),
//     falling back to its timestamp when responseId is absent — omp carries
//     no other per-message id, and responseId is the one fact stable across a
//     message's message_start/message_update/message_end.
//   - a user message is keyed by its own timestamp ("user#<timestamp>#<blockIndex>").
//   - a tool call is keyed by its own toolCallId ("tool#<toolCallId>"), the
//     one identifier tool_execution_start and tool_execution_end share.
//   - a subagent is keyed by its own id ("subagent#<id>"), and an event it
//     reports is keyed by that id plus the identity its own frame would carry
//     at the top level.
//
// An in-progress message (message_start/message_update without message_end)
// produces no Item: message_update deltas are deferred until message_end,
// mirroring conversation-reading's guarantee against a half-written trailing
// record.
type OmpFrameScan struct {
	// tools holds the Items still waiting for tool_execution_end, keyed by
	// toolCallId, so a result completes the call it names in place.
	tools map[string]Item
	// tasks maps a tool call id that started a delegated task to that task
	// Item's own identity, so a subagent frame naming the call can find its
	// parent. Populated at tool_execution_start.
	tasks map[string]string
	// subagents holds the delegated Item still open for a subagent id, so a
	// later subagent_progress or a terminal subagent_lifecycle amends or
	// completes it in place instead of starting a second Item.
	subagents map[string]Item
	// subagentParents remembers the parent task identity a subagent's
	// lifecycle frame resolved, so a later subagent_event for the same
	// subagent id can attribute its own Items without re-resolving it.
	subagentParents map[string]string
	// toolModels remembers which model and provider served the assistant
	// message that requested a tool call, keyed by toolCallId. A
	// tool_execution_start/_end frame carries neither fact itself; the
	// assistant message that carried the toolCall content block does.
	toolModels map[string]ompModelRef
}

// ompModelRef is the served-by fact a message or tool call carries: the
// model and the provider that served it.
type ompModelRef struct{ Model, Provider string }

// NewOmpFrameScan starts one normalization over one omp Session's protocol
// event stream.
func NewOmpFrameScan() *OmpFrameScan {
	return &OmpFrameScan{
		tools:           map[string]Item{},
		tasks:           map[string]string{},
		subagents:       map[string]Item{},
		subagentParents: map[string]string{},
		toolModels:      map[string]ompModelRef{},
	}
}

// ompKnownSkipFrameTypes are frame types this normalizer recognizes but
// deliberately produces no Item for: lifecycle bookkeeping, control-plane
// responses, and extension UI traffic outside the approval gate this task
// does not cover. A frame type in neither this set nor one of the handled
// switch cases in Normalize becomes an Item of the unknown kind, so a new
// frame type shows up as a visible row rather than as a silent gap.
var ompKnownSkipFrameTypes = map[OmpFrameType]bool{
	"agent_start":               true,
	"turn_start":                true,
	"turn_end":                  true,
	"agent_end":                 true,
	OmpFrameResponse:            true,
	"extension_ui_request":      true,
	"extension_ui_response":     true,
	"available_commands_update": true,
	"command_output":            true,
	// tool_execution_update streams a running call's partial output; it is
	// unexercised (design.md), and is deferred the same way message_update
	// is, converging on the Item tool_execution_end completes.
	"tool_execution_update": true,
}

// Normalize turns one decoded frame into zero or more Items. The returned
// Items may supersede Items an earlier call already produced (a tool result
// completing the call it names, a subagent update amending its Item), which
// is how Conversation.Apply keeps one Item per identity across the stream.
func (s *OmpFrameScan) Normalize(frame OmpFrame) []Item {
	switch frame.Type {
	case "message_start", "message_update":
		return nil
	case "message_end":
		return s.normalizeMessage(frame.Raw)
	case "tool_execution_start":
		return s.normalizeToolStart(frame.Raw)
	case "tool_execution_end":
		return s.normalizeToolEnd(frame.Raw)
	case "subagent_lifecycle":
		return s.normalizeSubagentLifecycle(frame.Raw)
	case "subagent_progress":
		return s.normalizeSubagentProgress(frame.Raw)
	case "subagent_event":
		return s.normalizeSubagentEvent(frame.Raw)
	}
	if ompKnownSkipFrameTypes[frame.Type] {
		return nil
	}
	return []Item{s.unknownFrameItem(frame)}
}

// ompMessage is the "message" object omp carries on message_start,
// message_update and message_end.
type ompMessage struct {
	Role       string            `json:"role"`
	Content    []ompContentBlock `json:"content"`
	Provider   string            `json:"provider"`
	Model      string            `json:"model"`
	Timestamp  int64             `json:"timestamp"`
	ResponseID string            `json:"responseId"`
	Usage      *ompUsage         `json:"usage"`
}

// ompUsage is the token and cost accounting omp reports on a completed
// assistant message. Cost is a pointer: its absence (the key missing
// entirely, distinct from a present object whose fields are all zero) is
// the signal an undeterminable cost is normalized from.
type ompUsage struct {
	Cost *ompUsageCost `json:"cost"`
}

// ompUsageCost is the message-level cost object, which — unlike a model's
// own static per-token cost table — carries the already-computed Total.
type ompUsageCost struct {
	Total float64 `json:"total"`
}

type ompContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	// ID is the tool call's own id, carried on a "toolCall" content block.
	ID string `json:"id"`
}

type ompMessageEnvelope struct {
	Message ompMessage `json:"message"`
}

// ompMessageIdentity is the stable fact a message is keyed on: its
// responseId when the vendor recorded one (every assistant message observed
// carries it), its own timestamp otherwise.
func ompMessageIdentity(msg ompMessage) string {
	if msg.ResponseID != "" {
		return msg.ResponseID
	}
	return strconv.FormatInt(msg.Timestamp, 10)
}

func ompMessageTime(timestampMillis int64) time.Time {
	if timestampMillis == 0 {
		return time.Time{}
	}
	return time.UnixMilli(timestampMillis).UTC()
}

func (s *OmpFrameScan) normalizeMessage(raw json.RawMessage) []Item {
	var envelope ompMessageEnvelope
	if json.Unmarshal(raw, &envelope) != nil {
		return nil
	}
	msg := envelope.Message
	switch msg.Role {
	case "user":
		return s.normalizeUserMessage(msg)
	case "assistant":
		return s.normalizeAssistantMessage(msg)
	case "toolResult":
		// Already normalized from tool_execution_end, which carries the same
		// result content plus isError under the tool call's own identity.
		// Normalizing this too would duplicate the same activity.
		return nil
	default:
		label := "message:" + msg.Role
		return []Item{{
			ID:          "message#" + ompMessageIdentity(msg),
			OccurredAt:  ompMessageTime(msg.Timestamp),
			Role:        ItemRoleSystem,
			Kind:        ItemKindUnknown,
			VendorLabel: label,
			Title:       "Unbekannter Eintrag: " + label,
		}}
	}
}

func (s *OmpFrameScan) normalizeUserMessage(msg ompMessage) []Item {
	identity := ompMessageIdentity(msg)
	occurred := ompMessageTime(msg.Timestamp)
	var items []Item
	for index, block := range msg.Content {
		if block.Type != "text" {
			continue
		}
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}
		title := claudeTitleLine(text, "Eingabe")
		items = append(items, Item{
			ID:         "user#" + identity + "#" + strconv.Itoa(index),
			OccurredAt: occurred,
			Role:       ItemRoleDeveloper,
			Kind:       ItemKindDeveloperPrompt,
			Title:      title,
			Detail:     claudeDetailBeyond(title, text),
		})
	}
	return items
}

func (s *OmpFrameScan) normalizeAssistantMessage(msg ompMessage) []Item {
	identity := ompMessageIdentity(msg)
	occurred := ompMessageTime(msg.Timestamp)
	var items []Item
	for index, block := range msg.Content {
		switch block.Type {
		case "text":
			text := strings.TrimSpace(block.Text)
			if text == "" {
				continue
			}
			title := claudeTitleLine(text, "Antwort")
			items = append(items, Item{
				ID:            "asst#" + identity + "#" + strconv.Itoa(index),
				OccurredAt:    occurred,
				Role:          ItemRoleAgent,
				Kind:          ItemKindAgentMessage,
				Title:         title,
				Detail:        claudeDetailBeyond(title, text),
				Model:         msg.Model,
				ModelProvider: msg.Provider,
			})
		case "thinking":
			text := strings.TrimSpace(block.Thinking)
			if text == "" {
				continue
			}
			title := claudeTitleLine(text, "Überlegung")
			items = append(items, Item{
				ID:            "asst#" + identity + "#" + strconv.Itoa(index),
				OccurredAt:    occurred,
				Role:          ItemRoleAgent,
				Kind:          ItemKindReasoning,
				Title:         title,
				Detail:        claudeDetailBeyond(title, text),
				Model:         msg.Model,
				ModelProvider: msg.Provider,
			})
		case "toolCall":
			// Normalized from tool_execution_start/_end instead, which
			// report the call's actual execution rather than the model's
			// request for one. Only the served-by fact is worth keeping from
			// this block: tool_execution_start/_end carry neither.
			if block.ID != "" {
				s.toolModels[block.ID] = ompModelRef{Model: msg.Model, Provider: msg.Provider}
			}
			continue
		default:
			label := block.Type
			if label == "" {
				label = "block"
			}
			items = append(items, Item{
				ID:          "asst#" + identity + "#" + strconv.Itoa(index),
				OccurredAt:  occurred,
				Role:        ItemRoleAgent,
				Kind:        ItemKindUnknown,
				VendorLabel: label,
				Title:       "Unbekannter Eintrag: " + label,
			})
		}
	}
	// The message carries one usage/cost figure for the whole turn, not per
	// content block; it is attributed to the last Item this message
	// produced, so a multi-block message (thinking followed by text, say)
	// carries the cost exactly once rather than once per block.
	if msg.Usage != nil && len(items) > 0 {
		last := &items[len(items)-1]
		if msg.Usage.Cost != nil {
			last.Cost, last.CostKnown = msg.Usage.Cost.Total, true
		}
	}
	return items
}

// ompToolExecution is the shape shared by tool_execution_start and
// tool_execution_end.
type ompToolExecution struct {
	ToolCallID string                     `json:"toolCallId"`
	ToolName   string                     `json:"toolName"`
	Args       map[string]json.RawMessage `json:"args"`
	Intent     string                     `json:"intent"`
	Result     *ompToolResult             `json:"result"`
	IsError    bool                       `json:"isError"`
}

type ompToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (s *OmpFrameScan) normalizeToolStart(raw json.RawMessage) []Item {
	var event ompToolExecution
	if json.Unmarshal(raw, &event) != nil || event.ToolCallID == "" {
		return nil
	}
	kind, title := ompToolKindAndTitle(event.ToolName, event.Args)
	item := Item{
		ID:             "tool#" + event.ToolCallID,
		Role:           ItemRoleAgent,
		Kind:           kind,
		Title:          title,
		AwaitingResult: true,
	}
	if event.Intent != "" {
		item.Detail = event.Intent
	}
	if served, known := s.toolModels[event.ToolCallID]; known {
		item.Model, item.ModelProvider = served.Model, served.Provider
	}
	s.tools[event.ToolCallID] = item
	if kind == ItemKindDelegatedTask {
		s.tasks[event.ToolCallID] = item.ID
	}
	return []Item{item}
}

// completeToolCall supersedes the tool-call Item tool_execution_start
// opened, the same shape Claude's completeToolCall follows. A result whose
// call was never seen — the call sits before the range this reading covers —
// completes nothing.
func (s *OmpFrameScan) normalizeToolEnd(raw json.RawMessage) []Item {
	var event ompToolExecution
	if json.Unmarshal(raw, &event) != nil || event.ToolCallID == "" {
		return nil
	}
	item, open := s.tools[event.ToolCallID]
	if !open {
		return nil
	}
	delete(s.tools, event.ToolCallID)
	item.AwaitingResult = false
	item.Failed = event.IsError
	if detail := ompToolResultText(event.Result); detail != "" {
		item.Detail = detail
	}
	return []Item{item}
}

func ompToolResultText(result *ompToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, block := range result.Content {
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return claudeCappedDetail(strings.Join(parts, "\n"))
}

func ompArgString(args map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		var text string
		if json.Unmarshal(args[key], &text) == nil && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

// ompToolKindAndTitle maps omp's tool vocabulary, as named in design.md's
// spike (`read`, `glob`, `write`, `bash`), onto the closed set of Item kinds.
// A tool name outside this table still gets the tool-call kind rather than
// the unknown kind: it is a recognized *category* of activity (a tool ran),
// just not one Magentic has a sharper kind for — the same rule Claude's own
// mapping follows for an MCP tool it does not otherwise know.
func ompToolKindAndTitle(name string, args map[string]json.RawMessage) (ItemKind, string) {
	switch name {
	case "bash", "shell", "execute", "exec":
		return ItemKindCommandExecution, claudeTitleLine(ompArgString(args, "command"), "Befehl")
	case "write", "edit", "patch":
		return ItemKindFileChange, claudeTitleLine(ompArgString(args, "path", "file_path"), "Datei geändert")
	case "read", "glob", "grep":
		return ItemKindFileRead, claudeTitleLine(ompArgString(args, "path", "pattern", "file_path"), "Gelesen")
	case "websearch", "web_search", "fetch", "web_fetch":
		return ItemKindWebSearch, claudeTitleLine(ompArgString(args, "query", "url"), "Websuche")
	case "task", "agent", "delegate":
		return ItemKindDelegatedTask, claudeTitleLine(ompArgString(args, "task", "description"), "Delegierte Aufgabe")
	case "todo", "todowrite", "todo_write":
		return ItemKindPlan, "Plan aktualisiert"
	}
	if strings.TrimSpace(name) == "" {
		return ItemKindToolCall, "Werkzeug"
	}
	return ItemKindToolCall, name
}

// ompSubagentLifecycle is omp's subagent_lifecycle payload
// (pi-tui/overlays/session-observer-registry.ts SubagentLifecyclePayload).
// Unexercised against a live omp process (design.md); mapped from omp's own
// source rather than guessed.
type ompSubagentLifecycle struct {
	ID               string `json:"id"`
	Agent            string `json:"agent"`
	Description      string `json:"description"`
	Status           string `json:"status"`
	ParentToolCallID string `json:"parentToolCallId"`
}

type ompSubagentLifecycleFrame struct {
	Payload ompSubagentLifecycle `json:"payload"`
}

func (s *OmpFrameScan) normalizeSubagentLifecycle(raw json.RawMessage) []Item {
	var envelope ompSubagentLifecycleFrame
	if json.Unmarshal(raw, &envelope) != nil || envelope.Payload.ID == "" {
		return nil
	}
	payload := envelope.Payload
	item := s.subagents[payload.ID]
	item.ID = "subagent#" + payload.ID
	item.Role = ItemRoleAgent
	item.Kind = ItemKindDelegatedTask
	item.Delegated = true
	item.ParentTaskID = s.tasks[payload.ParentToolCallID]
	s.subagentParents[payload.ID] = item.ParentTaskID
	title := payload.Description
	if title == "" {
		title = payload.Agent
	}
	item.Title = claudeTitleLine(title, "Delegierte Aufgabe")
	switch payload.Status {
	case "started":
		item.AwaitingResult = true
		s.subagents[payload.ID] = item
	case "completed":
		item.AwaitingResult = false
		item.Failed = false
		delete(s.subagents, payload.ID)
	case "failed", "aborted":
		item.AwaitingResult = false
		item.Failed = true
		delete(s.subagents, payload.ID)
	default:
		// An unrecognized status still keeps the Item open rather than
		// guessing it finished.
		s.subagents[payload.ID] = item
	}
	return []Item{item}
}

// ompSubagentProgress is omp's subagent_progress payload
// (SubagentProgressPayload), carrying the running subagent's latest
// AgentProgress. Only the fields this normalizer amends an open Item's
// detail with are read.
type ompSubagentProgress struct {
	Progress struct {
		ID          string `json:"id"`
		LastIntent  string `json:"lastIntent"`
		CurrentTool string `json:"currentTool"`
	} `json:"progress"`
}

type ompSubagentProgressFrame struct {
	Payload ompSubagentProgress `json:"payload"`
}

func (s *OmpFrameScan) normalizeSubagentProgress(raw json.RawMessage) []Item {
	var envelope ompSubagentProgressFrame
	if json.Unmarshal(raw, &envelope) != nil || envelope.Payload.Progress.ID == "" {
		return nil
	}
	id := envelope.Payload.Progress.ID
	item, open := s.subagents[id]
	if !open {
		// No lifecycle "started" was seen for this subagent in this
		// reading's window; nothing stable to amend.
		return nil
	}
	switch {
	case envelope.Payload.Progress.LastIntent != "":
		item.Detail = envelope.Payload.Progress.LastIntent
	case envelope.Payload.Progress.CurrentTool != "":
		item.Detail = envelope.Payload.Progress.CurrentTool
	}
	s.subagents[id] = item
	return []Item{item}
}

// normalizeSubagentEvent normalizes subagent_event, which carries one of a
// subagent's own AgentSessionEvent frames verbatim under payload.event
// (SubagentEventPayload). It is unexercised: the "events" subscription level
// that emits it was never turned on during the spike. The inner event is
// normalized the same way any top-level frame would be, then every Item it
// produced is marked delegated, attributed to the parent task the subagent's
// own subagent_lifecycle resolved (explicitly unknown when that link was
// never seen), and given an identity namespaced by the subagent id so it
// cannot collide with the parent run's own Items.
func (s *OmpFrameScan) normalizeSubagentEvent(raw json.RawMessage) []Item {
	var envelope struct {
		Payload struct {
			ID    string          `json:"id"`
			Event json.RawMessage `json:"event"`
		} `json:"payload"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Payload.ID == "" || len(envelope.Payload.Event) == 0 {
		return nil
	}
	inner, err := DecodeOmpFrame(envelope.Payload.Event)
	if err != nil {
		return nil
	}
	items := s.Normalize(inner)
	parent := s.subagentParents[envelope.Payload.ID]
	for index := range items {
		items[index].Delegated = true
		items[index].ParentTaskID = parent
		items[index].ID = "subagent#" + envelope.Payload.ID + "#" + items[index].ID
	}
	return items
}

// unknownFrameItem normalizes a top-level frame type this normalizer does
// not recognize at all — neither a handled case nor a known,
// deliberately-skipped one — so a protocol addition shows up as a visible
// row instead of vanishing. Its identity is the frame's own bytes: a
// top-level frame carries no id of its own to key on, but the same bytes
// arriving twice are the same event.
func (s *OmpFrameScan) unknownFrameItem(frame OmpFrame) Item {
	label := string(frame.Type)
	if label == "" {
		label = "unbekannt"
	}
	return Item{
		ID:          "unknown#" + label + "#" + basicHistoryFingerprint(frame.Raw)[:16],
		Role:        ItemRoleSystem,
		Kind:        ItemKindUnknown,
		VendorLabel: label,
		Title:       "Unbekannter Eintrag: " + label,
	}
}
