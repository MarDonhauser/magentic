package core

import "time"

// ItemKind is the closed set of activity kinds Magentic normalizes agent work
// into. A vendor record that matches no kind becomes ItemKindUnknown carrying
// the vendor's own label, so a format change shows up as a visible row rather
// than as a gap.
type ItemKind string

const (
	ItemKindDeveloperPrompt   ItemKind = "developer-prompt"
	ItemKindAgentMessage      ItemKind = "agent-message"
	ItemKindReasoning         ItemKind = "reasoning"
	ItemKindPlan              ItemKind = "plan"
	ItemKindCommandExecution  ItemKind = "command-execution"
	ItemKindFileChange        ItemKind = "file-change"
	ItemKindFileRead          ItemKind = "file-read"
	ItemKindToolCall          ItemKind = "tool-call"
	ItemKindWebSearch         ItemKind = "web-search"
	ItemKindDelegatedTask     ItemKind = "delegated-task"
	ItemKindContextCompaction ItemKind = "context-compaction"
	ItemKindError             ItemKind = "error"
	ItemKindUnknown           ItemKind = "unknown"
)

// ItemKinds enumerates the closed set in a stable order.
func ItemKinds() []ItemKind {
	return []ItemKind{
		ItemKindDeveloperPrompt,
		ItemKindAgentMessage,
		ItemKindReasoning,
		ItemKindPlan,
		ItemKindCommandExecution,
		ItemKindFileChange,
		ItemKindFileRead,
		ItemKindToolCall,
		ItemKindWebSearch,
		ItemKindDelegatedTask,
		ItemKindContextCompaction,
		ItemKindError,
		ItemKindUnknown,
	}
}

// ParseItemKind reads a serialized label back. A label outside the closed set
// is the unknown kind, never an error and never a neighbouring kind.
func ParseItemKind(label string) ItemKind {
	for _, kind := range ItemKinds() {
		if string(kind) == label {
			return kind
		}
	}
	return ItemKindUnknown
}

// ItemRole names who produced an Item.
type ItemRole string

const (
	ItemRoleDeveloper ItemRole = "developer"
	ItemRoleAgent     ItemRole = "agent"
	ItemRoleSystem    ItemRole = "system"
)

// Item is one normalized, provider-neutral unit of agent activity. Title and
// Detail are presentation facts decided by the vendor's normalizer; an
// interface renders them and never re-derives meaning from a tool name.
type Item struct {
	// ID is stable within its Conversation and derived from the vendor's own
	// record identity, so re-reading the same records is idempotent.
	ID          string    `json:"id"`
	OccurredAt  time.Time `json:"occurredAt,omitzero"`
	Role        ItemRole  `json:"role"`
	Kind        ItemKind  `json:"kind"`
	Title       string    `json:"title"`
	Detail      string    `json:"detail,omitempty"`
	// VendorLabel carries the vendor's own name for an activity Magentic has
	// no kind for. It is set only for ItemKindUnknown.
	VendorLabel string `json:"vendorLabel,omitempty"`
	// Delegated marks work a subagent produced. ParentTaskID names the
	// delegated-task Item it belongs to, and stays empty when the vendor did
	// not record the link.
	Delegated    bool   `json:"delegated,omitempty"`
	ParentTaskID string `json:"parentTaskId,omitempty"`
	// Failed marks a tool activity the vendor recorded as failed.
	Failed bool `json:"failed,omitempty"`
	// AwaitingResult marks a tool activity whose result has not arrived. It
	// stays set when the agent was killed mid-call, which renders as
	// unfinished rather than as a success.
	AwaitingResult bool `json:"awaitingResult,omitempty"`
}

// ConversationRef is the vendor-qualified handle of one coding-agent run's
// Conversation. RunID is the vendor's own run identity, taken from the
// Session's AgentRunRef.
type ConversationRef struct {
	Vendor AgentVendor `json:"vendor"`
	RunID  string      `json:"runId"`
}

// Conversation is the ordered sequence of Items belonging to one run.
type Conversation struct {
	Ref   ConversationRef `json:"ref"`
	Items []Item          `json:"items,omitempty"`
}

func (c *Conversation) indexOf(id string) int {
	for i := range c.Items {
		if c.Items[i].ID == id {
			return i
		}
	}
	return -1
}

// Append adds an Item unless its identity is already present. Re-reading the
// same vendor records therefore cannot grow a Conversation.
func (c *Conversation) Append(items ...Item) {
	for _, item := range items {
		if c.indexOf(item.ID) < 0 {
			c.Items = append(c.Items, item)
		}
	}
}

// Apply adds an Item, or supersedes the Item of the same identity in place.
// Supersession is how a tool result completes a tool call published earlier;
// the Item keeps its position in the Conversation.
func (c *Conversation) Apply(items ...Item) {
	for _, item := range items {
		if at := c.indexOf(item.ID); at >= 0 {
			c.Items[at] = item
			continue
		}
		c.Items = append(c.Items, item)
	}
}

// ConversationAvailability distinguishes a delivered Conversation from each
// way one can be undeliverable. Per ADR 0004 an unavailable reading is never
// flattened into an empty Conversation.
type ConversationAvailability string

const (
	ConversationAvailable        ConversationAvailability = "available"
	ConversationNotApplicable    ConversationAvailability = "not-applicable"
	ConversationNoNormalizer     ConversationAvailability = "no-normalizer"
	ConversationRecordNotFound   ConversationAvailability = "record-not-found"
	ConversationRecordUnreadable ConversationAvailability = "record-unreadable"
)

// ConversationReading is one answer to "what is this Session's Conversation".
// Exactly one of Conversation and Reason is meaningful: an available reading
// carries the Conversation, every other reading carries its reason.
type ConversationReading struct {
	Availability ConversationAvailability `json:"availability"`
	Ref          ConversationRef          `json:"ref,omitzero"`
	Conversation *Conversation            `json:"conversation,omitempty"`
	Reason       string                   `json:"reason,omitempty"`
}

// AvailableConversation reports a Conversation that was located and read. An
// available Conversation holding no Items is empty, which is distinct from
// every unavailable reading.
func AvailableConversation(conversation Conversation) ConversationReading {
	return ConversationReading{
		Availability: ConversationAvailable,
		Ref:          conversation.Ref,
		Conversation: &conversation,
	}
}

// UnavailableConversation reports why a Conversation cannot be delivered. A
// reason is always stated; an availability that is not one of the unavailable
// readings is a caller error and is reported as unreadable.
func UnavailableConversation(availability ConversationAvailability, ref ConversationRef, reason string) ConversationReading {
	switch availability {
	case ConversationNotApplicable, ConversationNoNormalizer,
		ConversationRecordNotFound, ConversationRecordUnreadable:
	default:
		availability = ConversationRecordUnreadable
	}
	if reason == "" {
		reason = "Grund nicht angegeben"
	}
	return ConversationReading{Availability: availability, Ref: ref, Reason: reason}
}

// ConversationNormalizer is the Adapter by which one vendor's own conversation
// records become Items. It owns locating the record and reading it; it never
// starts, drives or writes to the agent.
type ConversationNormalizer interface {
	Vendor() AgentVendor
	// Locate resolves the file holding this Conversation's records. A vendor
	// that cannot locate the record reports false rather than an empty path.
	Locate(ref ConversationRef) (string, bool)
	// NewScan starts one normalization over one Conversation record. The scan
	// carries the state a later byte range depends on — the tool calls still
	// waiting for their result — so appended bytes normalize correctly.
	NewScan() ConversationScan
}

// ConversationScan normalizes one Conversation record, one appended byte range
// at a time.
type ConversationScan interface {
	// Normalize turns an appended byte range into Items. consumed reports how
	// many of the given bytes were covered by complete records; a partially
	// written trailing record is left unconsumed and normalized again once the
	// vendor has finished writing it.
	//
	// Returned Items may supersede Items an earlier call already produced,
	// which is how a tool result completes the tool call it names.
	Normalize(data []byte) (items []Item, consumed int)
}

// ConversationRefForSession resolves a Session's ConversationRef from its
// recorded vendor and run reference alone. The Session's name and its runtime
// name are deliberately not consulted: a rename must not move a Conversation.
//
// When no ref can be resolved, the returned reading states why and ok is false.
func ConversationRefForSession(session Session) (ConversationRef, ConversationReading, bool) {
	vendor := session.SessionVendor()
	if vendor == "" {
		return ConversationRef{}, UnavailableConversation(ConversationNotApplicable, ConversationRef{},
			"Diese Session hostet keinen Coding-Agenten."), false
	}
	run, ok := session.AgentRun(vendor)
	if !ok || run.ExternalID == "" {
		return ConversationRef{}, UnavailableConversation(ConversationNotApplicable, ConversationRef{Vendor: vendor},
			"Für diese Session ist keine Run-Referenz hinterlegt."), false
	}
	return ConversationRef{Vendor: vendor, RunID: run.ExternalID}, ConversationReading{}, true
}

// normalizerForRef resolves the vendor's normalizer, or the unavailable
// reading that names why this Conversation cannot be normalized.
func normalizerForRef(ref ConversationRef) (ConversationNormalizer, ConversationReading, bool) {
	provider, known := providerForVendor(ref.Vendor)
	if !known {
		return nil, UnavailableConversation(ConversationNoNormalizer, ref,
			"Unbekannter Agent-Vendor "+string(ref.Vendor)+"."), false
	}
	normalizer, supported := provider.Normalizer()
	if !supported {
		return nil, UnavailableConversation(ConversationNoNormalizer, ref,
			"Conversations von "+string(ref.Vendor)+" können noch nicht gelesen werden."), false
	}
	return normalizer, ConversationReading{}, true
}
