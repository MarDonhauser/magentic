package core

import (
	"encoding/json"
	"strings"
)

// ManagedEventKind names what one line of the vendor's stream-json protocol
// told the host: the echo of a delivered prompt, a piece of a message still
// being produced, the end of a turn, or a transport-level error.
type ManagedEventKind string

const (
	ManagedEventEcho    ManagedEventKind = "echo"
	ManagedEventChunk   ManagedEventKind = "chunk"
	ManagedEventTurnEnd ManagedEventKind = "turn-end"
	ManagedEventError   ManagedEventKind = "error"
)

// ManagedEvent is one parsed protocol line. EchoText carries the replayed
// user message the Outbox matches against its in-flight prompt; ChunkText
// carries streamed output; EndReason carries the turn's end with FailReason
// holding the vendor's own reason for a failed turn.
type ManagedEvent struct {
	Kind       ManagedEventKind
	EchoText   string
	MessageID  string
	ChunkText  string
	EndReason  TurnEndReason
	FailReason string
	Error      string
}

// ParseManagedEventLine reads one line of Claude Code's stream-json protocol
// (`-p --input-format stream-json --output-format stream-json`). The protocol
// is an SDK surface, not a stability guarantee, so anything unrecognized
// reports false and is ignored — a protocol break must degrade to "managed
// runtime unavailable", never to a Session that silently does nothing.
//
// Shapes handled, verified against CLI 2.1.259:
//   - {"type":"user","message":{"role":"user","content":[...]}} is the
//     --replay-user-messages echo acknowledging a delivered prompt.
//   - {"type":"stream_event","event":{"type":"content_block_delta",...}} and
//     {"type":"assistant","message":{"content":[...]}} carry output while it
//     is being produced.
//   - {"type":"result","subtype":"success"|"error_...",...} ends the turn.
func ParseManagedEventLine(line []byte) (ManagedEvent, bool) {
	var envelope struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message *struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"message"`
		Event *struct {
			Type  string `json:"type"`
			Delta *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			Text string `json:"text"`
		} `json:"event"`
		Result  string `json:"result"`
		Errors  []string `json:"errors"`
		IsError bool   `json:"is_error"`
		Error   string `json:"error"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	if err := decoder.Decode(&envelope); err != nil || envelope.Type == "" {
		return ManagedEvent{}, false
	}
	switch envelope.Type {
	case "user":
		if envelope.Message == nil || envelope.Message.Role != "user" {
			return ManagedEvent{}, false
		}
		text := managedContentText(envelope.Message.Content)
		if text == "" {
			return ManagedEvent{}, false
		}
		return ManagedEvent{Kind: ManagedEventEcho, EchoText: text}, true
	case "stream_event":
		if envelope.Event == nil {
			return ManagedEvent{}, false
		}
		text := ""
		if envelope.Event.Delta != nil {
			text = envelope.Event.Delta.Text
		} else {
			text = envelope.Event.Text
		}
		if text == "" {
			return ManagedEvent{}, false
		}
		return ManagedEvent{Kind: ManagedEventChunk, ChunkText: text}, true
	case "assistant":
		if envelope.Message == nil {
			return ManagedEvent{}, false
		}
		text := managedContentText(envelope.Message.Content)
		if text == "" {
			return ManagedEvent{}, false
		}
		return ManagedEvent{Kind: ManagedEventChunk, ChunkText: text}, true
	case "result":
		event := ManagedEvent{Kind: ManagedEventTurnEnd, EndReason: TurnEndCompleted}
		if envelope.IsError || (envelope.Subtype != "" && envelope.Subtype != "success") {
			event.EndReason = TurnEndFailed
			event.FailReason = strings.TrimSpace(envelope.Subtype)
			if detail := strings.TrimSpace(envelope.Error); detail != "" {
				event.FailReason = detail
			} else if len(envelope.Errors) > 0 {
				event.FailReason = strings.Join(envelope.Errors, "; ")
			} else if event.FailReason == "" {
				event.FailReason = "der Agent meldete einen Fehler ohne Grund"
			}
		}
		return event, true
	default:
		// System init lines and anything a newer CLI adds carry no turn or
		// delivery fact Magentic acts on.
		return ManagedEvent{}, false
	}
}

// managedContentText reads the text out of a message content that may be a
// plain string or a list of content blocks.
func managedContentText(content any) string {
	switch shaped := content.(type) {
	case string:
		return strings.TrimSpace(shaped)
	case []any:
		var parts []string
		for _, block := range shaped {
			mapped, ok := block.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := mapped["text"].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, ""))
	default:
		return ""
	}
}
