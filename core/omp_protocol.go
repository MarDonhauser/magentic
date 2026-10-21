package core

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// OmpFrameType names the "type" field of one line of omp's rpc-ui NDJSON
// protocol. Most values are omp's own vocabulary (events, extension_ui_*
// requests, ready); "response" is the one type this codec treats specially,
// because it is the one correlated to a request by id.
type OmpFrameType string

// OmpFrameResponse is the frame type omp answers a command with.
const OmpFrameResponse OmpFrameType = "response"

// OmpFrame is one decoded line of omp's rpc-ui NDJSON protocol: enough of the
// response shape to correlate and judge success, plus the raw data and the
// raw frame for callers that need what this codec does not interpret.
type OmpFrame struct {
	Type    OmpFrameType
	ID      string
	Command string
	// Success and HasSuccess separate "the frame said success:false" from
	// "the frame carries no success key at all" — an event frame has neither,
	// and must never read as a failed response because Success is the zero
	// value.
	Success    bool
	HasSuccess bool
	Error      string
	Data       json.RawMessage
	Raw        json.RawMessage
}

// IsResponse reports whether this frame answers a request by id.
func (f OmpFrame) IsResponse() bool { return f.Type == OmpFrameResponse && f.ID != "" }

// DecodeOmpFrame parses one NDJSON line of omp's rpc-ui protocol. line is
// copied into Raw, so callers may reuse the buffer they read it into (a
// bufio.Scanner's, in particular).
func DecodeOmpFrame(line []byte) (OmpFrame, error) {
	var envelope struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Command string          `json:"command"`
		Success *bool           `json:"success"`
		Error   string          `json:"error"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return OmpFrame{}, fmt.Errorf("unlesbarer omp-Frame: %w", err)
	}
	frame := OmpFrame{
		Type:    OmpFrameType(envelope.Type),
		ID:      envelope.ID,
		Command: envelope.Command,
		Error:   envelope.Error,
		Data:    envelope.Data,
		Raw:     append(json.RawMessage(nil), line...),
	}
	if envelope.Success != nil {
		frame.Success = *envelope.Success
		frame.HasSuccess = true
	}
	return frame, nil
}

// EncodeOmpCommand marshals an outgoing rpc-ui command as one NDJSON line
// (without a trailing newline), carrying id so its response can be
// correlated. fields carries the command's own payload, e.g. {"message": …}
// for a prompt; a nil fields is a command with no payload beyond its type.
func EncodeOmpCommand(id, commandType string, fields map[string]any) ([]byte, error) {
	frame := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		frame[k] = v
	}
	frame["id"] = id
	frame["type"] = commandType
	return json.Marshal(frame)
}

// ompCorrelatorBuffer bounds how many responses one pending request's channel
// holds before Dispatch would block on it. omp has been observed answering a
// single request id more than once (a prompt sent while streaming gets
// success:true then success:false on the same id), but never more than a
// handful of times, so a small buffer absorbs that without ever blocking in
// practice while staying finite.
const ompCorrelatorBuffer = 8

// OmpCorrelator routes incoming response frames to the pending request that
// carries their id, by identity and never by arrival order. omp may answer
// one id more than once, so every response for an id is delivered to that
// request's channel, in the order it arrived; it is the caller — not this
// type — that decides which delivery, if any, is final. A response whose id
// matches no pending request is reported as unmatched, never delivered to a
// different request.
type OmpCorrelator struct {
	mu        sync.Mutex
	pending   map[string]chan OmpFrame
	unmatched chan OmpFrame
}

// NewOmpCorrelator creates a correlator with no pending requests. The
// unmatched channel is buffered so a caller that is not actively draining it
// does not stall Dispatch for frames that were never registered.
func NewOmpCorrelator() *OmpCorrelator {
	return &OmpCorrelator{
		pending:   make(map[string]chan OmpFrame),
		unmatched: make(chan OmpFrame, ompCorrelatorBuffer),
	}
}

// Register opens a pending request for id and returns the buffered channel
// its responses are delivered on. The caller must call Release once it is
// done reading, or a request that never gets released keeps routing
// responses nobody reads.
func (c *OmpCorrelator) Register(id string) <-chan OmpFrame {
	ch := make(chan OmpFrame, ompCorrelatorBuffer)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	return ch
}

// Release closes out id's pending request and closes its channel. Responses
// already delivered remain readable before the close; a response arriving
// after Release is reported as unmatched, the same as one for an id that was
// never registered.
func (c *OmpCorrelator) Release(id string) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ok {
		close(ch)
	}
}

// Dispatch routes one decoded frame. A response frame whose id is pending is
// delivered to that request's channel and reported as consumed (ok=false,
// event ignored). A response whose id is not pending, or whose request's
// buffer is full, goes to the unmatched channel instead, and is dropped if
// that is full too. A non-response frame — an event,
// extension_ui_request, ready, or anything else — is not a response at all
// and is returned unchanged for the caller to treat as an event.
func (c *OmpCorrelator) Dispatch(frame OmpFrame) (event OmpFrame, isEvent bool) {
	if !frame.IsResponse() {
		return frame, true
	}
	// The send happens under the lock so Release cannot close the channel
	// between lookup and send. Neither send blocks: the reader loop must never
	// stall on a caller that stopped reading.
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.pending[frame.ID]; ok {
		select {
		case ch <- frame:
			return OmpFrame{}, false
		default:
		}
	}
	select {
	case c.unmatched <- frame:
	default:
	}
	return OmpFrame{}, false
}

// Unmatched reports response frames whose id matched no pending request.
func (c *OmpCorrelator) Unmatched() <-chan OmpFrame { return c.unmatched }

// OmpHandshake records the facts the first frame an omp process sends
// carries: the protocol version it negotiated, the full set it could have
// offered, and the largest single frame it commits to sending.
type OmpHandshake struct {
	ProtocolVersion           int
	SupportedProtocolVersions []int
	MaxFrameBytes             int
}

// OmpSupportedProtocolVersions is the set of omp rpc-ui protocol versions
// this host understands. Only the framing is versioned by the ready frame —
// omp's command and event vocabulary carries no published compatibility
// guarantee beyond it (design.md) — so this set is kept small and explicit
// rather than "the latest omp offers".
var OmpSupportedProtocolVersions = map[int]bool{1: true}

// ParseOmpReady reads the first frame an omp process sends and refuses the
// Session unless it is a "ready" frame naming at least one protocol version
// this host understands. A malformed first frame is refused too, carrying
// its own text: the observed case is omp printing a plain-text line instead
// of a ready frame when --model names a model it cannot find, and exits 0 —
// the refusal has to surface that text for the cause to be visible.
func ParseOmpReady(line []byte) (OmpHandshake, error) {
	var envelope struct {
		Type                      string `json:"type"`
		ProtocolVersion           int    `json:"protocolVersion"`
		SupportedProtocolVersions []int  `json:"supportedProtocolVersions"`
		MaxFrameBytes             int    `json:"maxFrameBytes"`
	}
	trimmed := strings.TrimSpace(string(line))
	if err := json.Unmarshal(line, &envelope); err != nil {
		return OmpHandshake{}, fmt.Errorf("omp meldete statt eines Ready-Frames: %s", trimmed)
	}
	if envelope.Type != "ready" {
		return OmpHandshake{}, fmt.Errorf("erster Frame von omp ist kein Ready-Frame: %s", trimmed)
	}
	handshake := OmpHandshake{
		ProtocolVersion:           envelope.ProtocolVersion,
		SupportedProtocolVersions: envelope.SupportedProtocolVersions,
		MaxFrameBytes:             envelope.MaxFrameBytes,
	}
	// The session speaks protocolVersion until the host negotiates another
	// one, and Magentic never negotiates. A supported version that is only
	// on offer is therefore not enough.
	if OmpSupportedProtocolVersions[envelope.ProtocolVersion] {
		return handshake, nil
	}
	return handshake, fmt.Errorf(
		"omp spricht Protokollversion %d (angeboten: %v), Magentic versteht %v",
		envelope.ProtocolVersion, envelope.SupportedProtocolVersions, ompSupportedProtocolVersionsSorted(),
	)
}

// ompSupportedProtocolVersionsSorted renders OmpSupportedProtocolVersions
// deterministically for an error message.
func ompSupportedProtocolVersionsSorted() []int {
	versions := make([]int, 0, len(OmpSupportedProtocolVersions))
	for version := range OmpSupportedProtocolVersions {
		versions = append(versions, version)
	}
	sort.Ints(versions)
	return versions
}
