package core

import (
	"errors"
	"io"
	"os"
	"sync"
)

// conversationPrefixBytes caps the prefix whose fingerprint decides whether a
// record still extends what was read before. It is small enough to re-read on
// every pass and large enough to cover several records.
const conversationPrefixBytes = 4096

// conversationPosition is how far one record file was read, together with the
// fingerprint of its prefix. The fingerprint is what tells a grown record from
// a record that was rewritten to the same length.
type conversationPosition struct {
	offset int64
	prefix string
}

// ConversationUpdate is what one pass produced for one watched Session. A pass
// that found nothing appended produces no update at all.
type ConversationUpdate struct {
	SessionID SessionID       `json:"sessionId"`
	Ref       ConversationRef `json:"ref"`
	// Replaced marks a full re-reading: the Items held for this Conversation
	// are discarded, and Items is the Conversation from its beginning.
	Replaced bool   `json:"replaced"`
	Items    []Item `json:"items,omitempty"`
}

type conversationState struct {
	ref          ConversationRef
	conversation Conversation
	scan         ConversationScan
	positions    map[string]conversationPosition
	failure      error
}

// ConversationReader keeps the Conversations of the Sessions an interface is
// presenting current, by normalizing only what a vendor appended since the
// previous pass. It runs no loop of its own: the Observation pass drives it.
//
// Everything it does to a vendor's record is opening it read-only.
type ConversationReader struct {
	mu      sync.Mutex
	watched map[SessionID]bool
	held    map[SessionID]*conversationState
	// readRange is the only way this reader touches a vendor's record. It is
	// a field so a test can count how often a record is read.
	readRange func(path string, offset int64) (conversationRange, error)
}

type conversationRange struct {
	data []byte
	size int64
	// head are the record's first bytes, capped at conversationPrefixBytes.
	// The fingerprint is taken over as much of them as was already read, so a
	// record that merely grew keeps its fingerprint.
	head []byte
}

// conversationPrefix fingerprints the part of a record's head that a previous
// reading already covered. Comparing that fingerprint is what distinguishes a
// record that grew from one that was rewritten to the same length.
func conversationPrefix(head []byte, offset int64) string {
	length := offset
	if length > int64(len(head)) {
		length = int64(len(head))
	}
	if length < 0 {
		length = 0
	}
	return basicHistoryFingerprint(head[:length])
}

func NewConversationReader() *ConversationReader {
	return &ConversationReader{
		watched:   map[SessionID]bool{},
		held:      map[SessionID]*conversationState{},
		readRange: readConversationRange,
	}
}

// Watch declares which Session an interface is presenting. A Session nobody is
// watching is never read, and the Conversation held for a Session that stops
// being watched is released — it can always be derived from the record again.
func (r *ConversationReader) Watch(sessionIDs ...SessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	watched := make(map[SessionID]bool, len(sessionIDs))
	for _, id := range sessionIDs {
		if id != "" {
			watched[id] = true
		}
	}
	r.watched = watched
	for id := range r.held {
		if !watched[id] {
			delete(r.held, id)
		}
	}
}

// Watching reports whether this Session is currently being presented.
func (r *ConversationReader) Watching(sessionID SessionID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.watched[sessionID]
}

// Pass reads the watched Sessions' Conversations once. It returns one update
// per Conversation that gained Items or was re-read from the beginning; a
// Conversation the vendor did not append to produces nothing.
func (r *ConversationReader) Pass(sessions []Session) []ConversationUpdate {
	var updates []ConversationUpdate
	for _, session := range sessions {
		if !r.Watching(session.ID) {
			continue
		}
		if update, ok := r.advance(session); ok {
			updates = append(updates, update)
		}
	}
	return updates
}

// Read answers what this Session's Conversation currently is. An unavailable
// Conversation is reported with its reason and never as an empty one.
func (r *ConversationReader) Read(session Session) ConversationReading {
	ref, unavailable, ok := ConversationRefForSession(session)
	if !ok {
		return unavailable
	}
	if _, unsupported, ok := normalizerForRef(ref); !ok {
		return unsupported
	}
	r.advance(session)

	r.mu.Lock()
	defer r.mu.Unlock()
	state, held := r.held[session.ID]
	switch {
	case !held:
		return UnavailableConversation(ConversationRecordNotFound, ref,
			"Das Aufzeichnungs-File dieses Laufs wurde nicht gefunden.")
	case state.failure != nil:
		return UnavailableConversation(ConversationRecordUnreadable, ref,
			"Das Aufzeichnungs-File konnte nicht gelesen werden: "+state.failure.Error())
	}
	conversation := Conversation{Ref: state.conversation.Ref}
	conversation.Items = append([]Item(nil), state.conversation.Items...)
	return AvailableConversation(conversation)
}

// advance performs one reading of one Session's Conversation.
func (r *ConversationReader) advance(session Session) (ConversationUpdate, bool) {
	ref, _, ok := ConversationRefForSession(session)
	if !ok {
		return ConversationUpdate{}, false
	}
	normalizer, _, ok := normalizerForRef(ref)
	if !ok {
		return ConversationUpdate{}, false
	}
	sources, located := normalizer.Locate(ref)
	if !located {
		r.mu.Lock()
		delete(r.held, session.ID)
		r.mu.Unlock()
		return ConversationUpdate{}, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.held[session.ID]
	if state == nil || state.ref != ref {
		state = r.freshState(ref, normalizer)
		r.held[session.ID] = state
	}

	ranges, replace, err := r.planReading(state, sources)
	if err != nil {
		state.failure = err
		return ConversationUpdate{}, false
	}
	state.failure = nil
	if replace {
		// The record no longer extends what was read before, so the Items
		// derived from the discarded reading cannot be accounted for and are
		// replaced rather than extended.
		state = r.freshState(ref, normalizer)
		r.held[session.ID] = state
		ranges, _, err = r.planReading(state, sources)
		if err != nil {
			state.failure = err
			return ConversationUpdate{}, false
		}
	}

	var produced []Item
	for _, reading := range ranges {
		items, consumed := state.scan.Normalize(reading.source, reading.data)
		position := reading.offset + int64(consumed)
		state.positions[reading.source.Path] = conversationPosition{
			offset: position,
			prefix: conversationPrefix(reading.head, position),
		}
		produced = append(produced, items...)
	}
	state.conversation.Apply(produced...)

	if !replace && len(produced) == 0 {
		return ConversationUpdate{}, false
	}
	update := ConversationUpdate{SessionID: session.ID, Ref: ref, Replaced: replace}
	if replace {
		update.Items = append([]Item(nil), state.conversation.Items...)
	} else {
		update.Items = produced
	}
	return update, true
}

func (r *ConversationReader) freshState(ref ConversationRef, normalizer ConversationNormalizer) *conversationState {
	return &conversationState{
		ref:          ref,
		conversation: Conversation{Ref: ref},
		scan:         normalizer.NewScan(),
		positions:    map[string]conversationPosition{},
	}
}

type conversationReading struct {
	source ConversationSource
	offset int64
	head   []byte
	data   []byte
}

// planReading reads every source from where it was left off. A source that no
// longer extends what was read before makes the whole Conversation a full
// re-reading, because Items already published could otherwise not be accounted
// for.
func (r *ConversationReader) planReading(state *conversationState, sources []ConversationSource) ([]conversationReading, bool, error) {
	var readings []conversationReading
	for _, source := range sources {
		previous, known := state.positions[source.Path]
		offset := previous.offset
		if !known {
			offset = 0
		}
		chunk, err := r.readRange(source.Path, offset)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// A source that vanished between locating and reading is not
				// a failure of the Conversation as a whole.
				continue
			}
			return nil, false, err
		}
		if known && (chunk.size < previous.offset || conversationPrefix(chunk.head, previous.offset) != previous.prefix) {
			return nil, true, nil
		}
		if len(chunk.data) == 0 {
			state.positions[source.Path] = conversationPosition{
				offset: offset, prefix: conversationPrefix(chunk.head, offset),
			}
			continue
		}
		readings = append(readings, conversationReading{
			source: source, offset: offset, head: chunk.head, data: chunk.data,
		})
	}
	return readings, false, nil
}

// readConversationRange opens a vendor's record read-only and reads from the
// given offset. It never writes, moves, truncates or locks the file.
func readConversationRange(path string, offset int64) (conversationRange, error) {
	file, err := os.Open(path)
	if err != nil {
		return conversationRange{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return conversationRange{}, err
	}
	result := conversationRange{size: info.Size()}

	head := make([]byte, conversationPrefixBytes)
	read, err := file.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return conversationRange{}, err
	}
	result.head = head[:read]

	if offset >= result.size {
		return result, nil
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return conversationRange{}, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return conversationRange{}, err
	}
	result.data = data
	return result, nil
}
