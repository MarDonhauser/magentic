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
	// ItemKindPermissionRequest und ItemKindPermissionDecision halten eine
	// managed Session offen: Die Frage, die der Agent stellte, und die
	// Antwort, die eine Person darauf gab — in der Reihenfolge, in der sie
	// geschahen. Sie entstehen im Agent-Host, nicht im Transcript-Reader,
	// sind aber dieselben Items, kein zweites Modell.
	ItemKindPermissionRequest  ItemKind = "permission-request"
	ItemKindPermissionDecision ItemKind = "permission-decision"
	ItemKindUnknown            ItemKind = "unknown"
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
		ItemKindPermissionRequest,
		ItemKindPermissionDecision,
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
	ID         string    `json:"id"`
	OccurredAt time.Time `json:"occurredAt,omitzero"`
	Role       ItemRole  `json:"role"`
	Kind       ItemKind  `json:"kind"`
	Title      string    `json:"title"`
	Detail     string    `json:"detail,omitempty"`
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
	// InProgress marks a message the managed runtime published while it is
	// still being produced. The completed message supersedes it in place
	// (see Conversation.Apply), so a streamed message is never presented as
	// finished and never appears twice.
	InProgress bool `json:"inProgress,omitempty"`
	// Label und Collapsible tragen die Darstellung des Items. Sie werden beim
	// Eintritt in eine Conversation aus der Art gefüllt, damit eine Oberfläche
	// Titel, Detail und Einklappbarkeit rendert, ohne die Art zu kennen: eine
	// neue Art ist damit eine Änderung in Go statt in vier Tabellen.
	Label       string `json:"label"`
	Collapsible bool   `json:"collapsible"`
}

// itemLabels ist die einzige Zuordnung von Art zu Beschriftung. Eine Art ohne
// Eintrag fällt auf die Beschriftung der unbekannten Art zurück, damit ein
// vergessener Eintrag eine sichtbare Zeile bleibt und keine Lücke.
var itemLabels = map[ItemKind]string{
	ItemKindDeveloperPrompt:    "Eingabe",
	ItemKindAgentMessage:       "Antwort",
	ItemKindReasoning:          "Überlegung",
	ItemKindPlan:               "Plan",
	ItemKindCommandExecution:   "Befehl",
	ItemKindFileChange:         "Datei geändert",
	ItemKindFileRead:           "Gelesen",
	ItemKindToolCall:           "Werkzeug",
	ItemKindWebSearch:          "Web",
	ItemKindDelegatedTask:      "Delegiert",
	ItemKindContextCompaction:  "Kontext",
	ItemKindPermissionRequest:  "Freigabe erbeten",
	ItemKindPermissionDecision: "Freigabe entschieden",
	ItemKindUnknown:            "Unbekannt",
}

// proseKinds sind die Arten, die dem Entwickler gehören. Sie werden nie hinter
// einen Schalter geklappt; alles andere ist Werkzeugarbeit und einklappbar.
var proseKinds = map[ItemKind]bool{
	ItemKindDeveloperPrompt: true,
	ItemKindAgentMessage:    true,
}

// ItemLabel nennt die Beschriftung einer Art.
func ItemLabel(kind ItemKind) string {
	if label, known := itemLabels[kind]; known {
		return label
	}
	return itemLabels[ItemKindUnknown]
}

// ItemCollapsible sagt, ob eine Art hinter einen Schalter geklappt werden darf.
func ItemCollapsible(kind ItemKind) bool { return !proseKinds[kind] }

// withPresentation füllt die Darstellung eines Items. Es läuft am Eintritt in
// eine Conversation, damit kein Erzeuger sie vergessen kann.
func (i Item) withPresentation() Item {
	i.Label = ItemLabel(i.Kind)
	i.Collapsible = ItemCollapsible(i.Kind)
	return i
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
	// index maps an Item identity to its position. Without it, applying a
	// batch would scan every held Item per new Item, which turns the first
	// reading of a long run into quadratic work. It is rebuilt on demand, so
	// a decoded or zero Conversation stays usable.
	index map[string]int
}

func (c *Conversation) indexOf(id string) int {
	if c.index == nil || len(c.index) != len(c.Items) {
		c.index = make(map[string]int, len(c.Items))
		for i := range c.Items {
			c.index[c.Items[i].ID] = i
		}
	}
	at, known := c.index[id]
	if !known {
		return -1
	}
	return at
}

// Append adds an Item unless its identity is already present. Re-reading the
// same vendor records therefore cannot grow a Conversation.
func (c *Conversation) Append(items ...Item) {
	for _, item := range items {
		if c.indexOf(item.ID) < 0 {
			c.index[item.ID] = len(c.Items)
			c.Items = append(c.Items, item.withPresentation())
		}
	}
}

// Apply adds an Item, or supersedes the Item of the same identity in place.
// Supersession is how a tool result completes a tool call published earlier;
// the Item keeps its position in the Conversation.
func (c *Conversation) Apply(items ...Item) {
	for _, item := range items {
		if at := c.indexOf(item.ID); at >= 0 {
			c.Items[at] = item.withPresentation()
			continue
		}
		c.index[item.ID] = len(c.Items)
		c.Items = append(c.Items, item.withPresentation())
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
	// Locate resolves the record files this Conversation is normalized from,
	// the run's own record first. A vendor that cannot locate the record
	// reports false rather than an empty list.
	//
	// known is what a previous Locate returned for this ref, or nil. Searching
	// a vendor's storage can mean walking thousands of files, and this runs on
	// every Observation pass, so a vendor that is handed what it found last
	// time must confirm it cheaply instead of searching again.
	Locate(ref ConversationRef, known []ConversationSource) ([]ConversationSource, bool)
	// NewScan starts one normalization over one Conversation. The scan carries
	// the state a later byte range depends on — the tool calls still waiting
	// for their result, and the delegated tasks a subagent record can belong
	// to — so appended bytes normalize correctly.
	NewScan() ConversationScan
}

// ConversationSource is one record file a Conversation is normalized from. A
// vendor that records delegated work beside the run's own record contributes
// one source per delegated task.
type ConversationSource struct {
	Path string `json:"path"`
	// DelegatedFrom is the vendor's own identifier of the tool call that
	// spawned the work in this file, and is empty for the run's own record.
	DelegatedFrom string `json:"delegatedFrom,omitempty"`
}

// ConversationScan normalizes a Conversation's record files, one appended byte
// range at a time.
type ConversationScan interface {
	// Normalize turns an appended byte range of one source into Items.
	// consumed reports how many of the given bytes were covered by complete
	// records; a partially written trailing record is left unconsumed and
	// normalized again once the vendor has finished writing it.
	//
	// Returned Items may supersede Items an earlier call already produced,
	// which is how a tool result completes the tool call it names.
	Normalize(source ConversationSource, data []byte) (items []Item, consumed int)
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
