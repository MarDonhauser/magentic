package main

import (
	"sync"

	"magentic/core"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// conversationEvent is the Wails event new Items are published on. The
// frontend subscribes once and applies whatever a pass produced.
const conversationEvent = "conversation:items"

// ConversationItemsResult is how a Conversation reaches the frontend. Either
// Items are available — possibly empty, which means the run has produced
// nothing yet — or the reading is unavailable and names its reason.
type ConversationItemsResult struct {
	SessionID    string      `json:"sessionId"`
	Availability string      `json:"availability"`
	Vendor       string      `json:"vendor"`
	Reason       string      `json:"reason,omitempty"`
	Items        []core.Item `json:"items"`
	// ItemsKnown separates an available Conversation without Items from every
	// unavailable reading, which carries no Items at all.
	ItemsKnown bool `json:"itemsKnown"`
}

// ConversationUpdateEvent carries the Items one Observation pass produced.
type ConversationUpdateEvent struct {
	SessionID string      `json:"sessionId"`
	Replaced  bool        `json:"replaced"`
	Items     []core.Item `json:"items"`
}

var conversationReaderOnce sync.Once

func (a *App) conversations() *core.ConversationReader {
	conversationReaderOnce.Do(func() {
		if a.conversationReader == nil {
			a.conversationReader = core.NewConversationReader()
		}
	})
	return a.conversationReader
}

// WatchConversation declares which Session's Conversation the frontend is
// presenting. Only that Session's record is read on an Observation pass.
func (a *App) WatchConversation(sessionID string) {
	if sessionID == "" {
		a.conversations().Watch()
		return
	}
	a.conversations().Watch(core.SessionID(sessionID))
}

// SessionConversation answers what a Session's Conversation currently is. It
// reads only; the Session's runtime is not touched.
func (a *App) SessionConversation(sessionID string) ConversationItemsResult {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return ConversationItemsResult{
			SessionID:    sessionID,
			Availability: string(core.ConversationNotApplicable),
			Reason:       err.Error(),
		}
	}
	return conversationResult(sessionID, a.conversations().Read(session))
}

func conversationResult(sessionID string, reading core.ConversationReading) ConversationItemsResult {
	result := ConversationItemsResult{
		SessionID:    sessionID,
		Availability: string(reading.Availability),
		Vendor:       string(reading.Ref.Vendor),
		Reason:       reading.Reason,
	}
	if reading.Availability == core.ConversationAvailable && reading.Conversation != nil {
		result.ItemsKnown = true
		result.Items = reading.Conversation.Items
		if result.Items == nil {
			result.Items = []core.Item{}
		}
	}
	return result
}

// publishConversationUpdates emits the Items an Observation pass produced. It
// is called from the existing pass; it starts no loop of its own.
func (a *App) publishConversationUpdates(sessions []core.Session) []ConversationUpdateEvent {
	var events []ConversationUpdateEvent
	for _, update := range a.conversations().Pass(sessions) {
		events = append(events, ConversationUpdateEvent{
			SessionID: string(update.SessionID),
			Replaced:  update.Replaced,
			Items:     update.Items,
		})
	}
	if a.ctx == nil {
		return events
	}
	for _, event := range events {
		runtime.EventsEmit(a.ctx, conversationEvent, event)
	}
	return events
}
