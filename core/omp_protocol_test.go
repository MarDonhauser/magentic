package core

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ompFixtureFrame is one recorded line of core/testdata/omp/*.ndjson: "out" is
// what the host sent, "in" is what omp emitted, in arrival order.
type ompFixtureFrame struct {
	Dir   string          `json:"dir"`
	Frame json.RawMessage `json:"frame"`
}

func readOmpFixture(t *testing.T, name string) []ompFixtureFrame {
	t.Helper()
	path := filepath.Join("testdata", "omp", name)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var frames []ompFixtureFrame
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var frame ompFixtureFrame
		if err := json.Unmarshal(line, &frame); err != nil {
			t.Fatalf("unreadable fixture line: %v", err)
		}
		frames = append(frames, frame)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return frames
}

func frameID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.ID
}

// 2.2: c1 and c2 are sent back to back and answered back to back in
// handshake-and-state.ndjson; each response has to reach the request its own
// id names, never the other one.
func TestOmpCorrelatorMatchesInterleavedRequestsFromFixture(t *testing.T) {
	frames := readOmpFixture(t, "handshake-and-state.ndjson")
	correlator := NewOmpCorrelator()
	c1 := correlator.Register("c1")
	c2 := correlator.Register("c2")
	defer correlator.Release("c1")
	defer correlator.Release("c2")

	for _, fixtureFrame := range frames {
		if fixtureFrame.Dir != "in" {
			continue
		}
		id := frameID(t, fixtureFrame.Frame)
		if id != "c1" && id != "c2" {
			continue
		}
		decoded, err := DecodeOmpFrame(fixtureFrame.Frame)
		if err != nil {
			t.Fatal(err)
		}
		if _, isEvent := correlator.Dispatch(decoded); isEvent {
			t.Fatalf("response frame %q was reported as an event", id)
		}
	}

	select {
	case got := <-c1:
		if got.ID != "c1" || got.Command != "get_state" {
			t.Fatalf("c1 received %+v, want the get_state response", got)
		}
	default:
		t.Fatal("c1 received nothing")
	}
	select {
	case got := <-c2:
		if got.ID != "c2" || got.Command != "get_session_stats" {
			t.Fatalf("c2 received %+v, want the get_session_stats response", got)
		}
	default:
		t.Fatal("c2 received nothing")
	}
}

// 2.2: a synthetic case where the response arriving order is the reverse of
// the request order — the correlator has to route by id regardless.
func TestOmpCorrelatorMatchesOutOfOrderResponses(t *testing.T) {
	correlator := NewOmpCorrelator()
	first := correlator.Register("req-1")
	second := correlator.Register("req-2")
	defer correlator.Release("req-1")
	defer correlator.Release("req-2")

	responseTwo, err := DecodeOmpFrame([]byte(`{"id":"req-2","type":"response","command":"get_state","success":true}`))
	if err != nil {
		t.Fatal(err)
	}
	responseOne, err := DecodeOmpFrame([]byte(`{"id":"req-1","type":"response","command":"abort","success":true}`))
	if err != nil {
		t.Fatal(err)
	}
	correlator.Dispatch(responseTwo)
	correlator.Dispatch(responseOne)

	select {
	case got := <-first:
		if got.ID != "req-1" || got.Command != "abort" {
			t.Fatalf("req-1 received %+v, want the abort response", got)
		}
	default:
		t.Fatal("req-1 received nothing")
	}
	select {
	case got := <-second:
		if got.ID != "req-2" || got.Command != "get_state" {
			t.Fatalf("req-2 received %+v, want the get_state response", got)
		}
	default:
		t.Fatal("req-2 received nothing")
	}
}

// 2.2: prompt-while-streaming-and-abort.ndjson answers "p3" twice on the same
// id — success:true, then success:false — and both have to reach the same
// pending request, in order, with neither dropped or treated as final by the
// correlator itself.
func TestOmpCorrelatorDeliversDoubleResponseToSameRequest(t *testing.T) {
	frames := readOmpFixture(t, "prompt-while-streaming-and-abort.ndjson")
	correlator := NewOmpCorrelator()
	p3 := correlator.Register("p3")
	defer correlator.Release("p3")

	var delivered []OmpFrame
	for _, fixtureFrame := range frames {
		if fixtureFrame.Dir != "in" {
			continue
		}
		if frameID(t, fixtureFrame.Frame) != "p3" {
			continue
		}
		decoded, err := DecodeOmpFrame(fixtureFrame.Frame)
		if err != nil {
			t.Fatal(err)
		}
		correlator.Dispatch(decoded)
	}
	for i := 0; i < 2; i++ {
		select {
		case got := <-p3:
			delivered = append(delivered, got)
		default:
			t.Fatalf("only %d of 2 responses delivered to p3", len(delivered))
		}
	}
	if !delivered[0].Success || !delivered[0].HasSuccess {
		t.Fatalf("first p3 response = %+v, want success:true", delivered[0])
	}
	if delivered[1].Success || !delivered[1].HasSuccess {
		t.Fatalf("second p3 response = %+v, want success:false", delivered[1])
	}
}

// 2.2: a response id that was never registered must surface as unmatched,
// never delivered to some other pending request.
func TestOmpCorrelatorReportsUnmatchedResponse(t *testing.T) {
	correlator := NewOmpCorrelator()
	other := correlator.Register("other-request")
	defer correlator.Release("other-request")

	stray, err := DecodeOmpFrame([]byte(`{"id":"ghost","type":"response","command":"abort","success":true}`))
	if err != nil {
		t.Fatal(err)
	}
	correlator.Dispatch(stray)

	select {
	case got := <-correlator.Unmatched():
		if got.ID != "ghost" {
			t.Fatalf("unmatched frame id = %q, want ghost", got.ID)
		}
	default:
		t.Fatal("stray response was not reported as unmatched")
	}
	select {
	case got := <-other:
		t.Fatalf("stray response was misrouted to an unrelated request: %+v", got)
	default:
	}
}

// 2.2: an event frame — no "response" type, or a response with no id — is
// never treated as a response and passes through to the caller.
func TestOmpCorrelatorPassesNonResponseFramesThroughAsEvents(t *testing.T) {
	correlator := NewOmpCorrelator()
	event, err := DecodeOmpFrame([]byte(`{"type":"agent_start"}`))
	if err != nil {
		t.Fatal(err)
	}
	passedThrough, isEvent := correlator.Dispatch(event)
	if !isEvent {
		t.Fatal("agent_start must be reported as an event")
	}
	if passedThrough.Type != "agent_start" {
		t.Fatalf("event type = %q, want agent_start", passedThrough.Type)
	}
}

// 2.3: the fixture's first frame is a supported ready frame.
func TestParseOmpReadySupportedHandshake(t *testing.T) {
	frames := readOmpFixture(t, "handshake-and-state.ndjson")
	if len(frames) == 0 || frames[0].Dir != "in" {
		t.Fatal("fixture must start with the ready frame")
	}
	handshake, err := ParseOmpReady(frames[0].Frame)
	if err != nil {
		t.Fatalf("supported handshake was refused: %v", err)
	}
	if handshake.ProtocolVersion != 1 {
		t.Fatalf("protocolVersion = %d, want 1", handshake.ProtocolVersion)
	}
	if len(handshake.SupportedProtocolVersions) != 2 || handshake.SupportedProtocolVersions[0] != 1 || handshake.SupportedProtocolVersions[1] != 2 {
		t.Fatalf("supportedProtocolVersions = %v, want [1 2]", handshake.SupportedProtocolVersions)
	}
	if handshake.MaxFrameBytes != 1048576 {
		t.Fatalf("maxFrameBytes = %d, want 1048576", handshake.MaxFrameBytes)
	}
}

// 2.3: a handshake naming only versions this host does not understand is
// refused, and the reason names the versions on both sides.
func TestParseOmpReadyUnsupportedHandshake(t *testing.T) {
	line := []byte(`{"type":"ready","protocolVersion":3,"supportedProtocolVersions":[3],"maxFrameBytes":1048576}`)
	_, err := ParseOmpReady(line)
	if err == nil {
		t.Fatal("a handshake naming no version this host understands must be refused")
	}
	if !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "1") {
		t.Fatalf("refusal %q must name the versions on both sides", err.Error())
	}
}

// 2.3: a supported version that is only on offer is not the one the session
// speaks. Magentic never negotiates, so a ready frame announcing v2 is
// refused even though v1 is in its supported list.
func TestParseOmpReadyRefusesUnsupportedActiveVersionEvenIfOffered(t *testing.T) {
	line := []byte(`{"type":"ready","protocolVersion":2,"supportedProtocolVersions":[1,2],"maxFrameBytes":1048576}`)
	if _, err := ParseOmpReady(line); err == nil {
		t.Fatal("a session speaking an unsupported protocol version must be refused")
	}
}

// 2.3: the real observed malformed first frame — omp's plain-text "model not
// found" line instead of a ready frame — is refused with that text visible.
func TestParseOmpReadyMalformedFirstFrame(t *testing.T) {
	line := []byte(`Model "ollama/qwen3.5:9b" not found. Run "omp models" to see available models.`)
	_, err := ParseOmpReady(line)
	if err == nil {
		t.Fatal("a plain-text first line must be refused")
	}
	if !strings.Contains(err.Error(), `Model "ollama/qwen3.5:9b" not found`) {
		t.Fatalf("refusal %q must carry omp's own text", err.Error())
	}
}

// 2.3: a JSON first frame that parses but is not "ready" is refused too.
func TestParseOmpReadyNonReadyJSONFirstFrame(t *testing.T) {
	line := []byte(`{"id":"m1","type":"response","command":"get_available_models","success":true,"data":{}}`)
	_, err := ParseOmpReady(line)
	if err == nil {
		t.Fatal("a non-ready first frame must be refused")
	}
}

// 2.2: Release racing Dispatch must never send on a closed channel, and a
// full buffer must not stall the reader.
func TestOmpCorrelatorReleaseRacingDispatchDoesNotPanic(t *testing.T) {
	c := NewOmpCorrelator()
	frame := OmpFrame{Type: OmpFrameResponse, ID: "r"}
	for i := 0; i < 200; i++ {
		c.Register("r")
		done := make(chan struct{})
		go func() {
			defer close(done)
			for j := 0; j < ompCorrelatorBuffer*3; j++ {
				c.Dispatch(frame)
			}
		}()
		c.Release("r")
		<-done
	}
}
