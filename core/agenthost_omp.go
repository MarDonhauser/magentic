package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ompBinary is the executable an omp host starts. It is a variable only so
// tests can point it at a scripted stand-in; production always runs omp from
// PATH.
var ompBinary = "omp"

// ompReadyTimeout bounds how long a host waits for omp's ready frame before
// it refuses the Session.
var ompReadyTimeout = 30 * time.Second

// ompDeliverIDPrefix marks the request id a delivered Outbox prompt is sent
// with, so its responses can be traced back to the message.
const ompDeliverIDPrefix = "deliver-"

// ompHostState is what an omp host keeps beyond the turn state it shares with
// every managed host: the handshake it confirmed, the message it is streaming,
// and how the last assistant message ended, which decides how the turn ends.
type ompHostState struct {
	handshake  OmpHandshake
	launchArgv []string
	stream     managedStream
	lastStop   string
	lastError  string
	requestSeq int
	// loginRequired names the last login-secret request omp raised and was
	// cancelled, and when. It is cleared once a Deliver call is made, so a
	// stale notice does not linger after the developer has acted in omp's
	// own interface and sent a new prompt.
	loginRequired   string
	loginRequiredAt time.Time
	// protocolTurnRunning tracks omp's own turn_start/turn_end boundaries,
	// finer-grained than ManagedTurns' notion of a turn (which spans the
	// whole agent_start..agent_end run, including tool round trips). It is
	// what distinguishes "working" from a momentary between-rounds gap
	// within a running agent run — see the status-and-lifecycle spec.
	protocolTurnRunning bool
	// ompSessionID is omp's own sessionId, learned from get_state right
	// after the handshake. It qualifies the Session's run reference (ADR
	// 0001) and is what a persisted Conversation is located by.
	ompSessionID string
	// scan accumulates the durable Conversation this host observes from the
	// protocol, independent of whether any interface is watching.
	scan *OmpFrameScan
}

// OmpLaunchArgvHasGate reports whether argv carries the approval gate's
// arguments, each exactly once and never an auto-approving flag. It is how a
// recorded launch is judged before it is used and after a host is reclaimed.
func OmpLaunchArgvHasGate(argv []string) bool {
	gate := ompApprovalGateArgv()
	for i := 0; i+1 < len(gate); i += 2 {
		found := 0
		for j := 0; j+1 < len(argv); j++ {
			if argv[j] == gate[i] {
				if argv[j+1] != gate[i+1] {
					return false
				}
				found++
			}
		}
		if found != 1 {
			return false
		}
	}
	for _, arg := range argv {
		if arg == "--auto-approve" || arg == "--yolo" {
			return false
		}
	}
	return true
}

// OmpLaunchCwd is the working directory a recorded launch names.
func OmpLaunchCwd(argv []string) string {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--cwd" {
			return argv[i+1]
		}
	}
	return ""
}

// ErrOmpUnavailable refuses the omp runtime when omp cannot be started at all.
// No other agent is started in its place.
var ErrOmpUnavailable = errors.New("omp ist nicht verfügbar")

// StartOmpProcess launches omp with argv in dir and waits for its ready frame
// before the host accepts any work. A missing binary, a process that exits
// first, and a handshake Magentic does not understand all refuse the start
// with a stated reason and leave no process behind.
func (h *AgentHost) StartOmpProcess(argv []string, dir string) (OmpHandshake, error) {
	h.mu.Lock()
	if h.process != nil {
		h.mu.Unlock()
		return OmpHandshake{}, errors.New("dieser Agent-Host besitzt bereits einen Prozess")
	}
	h.mu.Unlock()

	process, err := startAgentHostProcess(ompBinary, argv, dir)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return OmpHandshake{}, fmt.Errorf("%w: %q ist nicht installiert oder nicht im PATH", ErrOmpUnavailable, ompBinary)
		}
		return OmpHandshake{}, fmt.Errorf("%w: omp konnte nicht gestartet werden: %v", ErrOmpUnavailable, err)
	}
	reader := bufio.NewReaderSize(process.events(), 64<<10)
	type firstLine struct {
		line []byte
		err  error
	}
	first := make(chan firstLine, 1)
	go func() {
		line, err := readOmpLine(reader)
		first <- firstLine{line, err}
	}()
	var handshake OmpHandshake
	select {
	case got := <-first:
		if len(got.line) == 0 {
			_ = process.stop()
			return OmpHandshake{}, fmt.Errorf("%w: omp endete vor dem Handshake (%s)", ErrOmpUnavailable, process.exitReason())
		}
		handshake, err = ParseOmpReady(got.line)
		if err != nil {
			_ = process.stop()
			return OmpHandshake{}, fmt.Errorf("%w: %v", ErrOmpUnavailable, err)
		}
	case <-time.After(ompReadyTimeout):
		_ = process.stop()
		return OmpHandshake{}, fmt.Errorf("%w: omp sendete binnen %s keinen Ready-Frame", ErrOmpUnavailable, ompReadyTimeout)
	}

	h.mu.Lock()
	h.process = process
	h.omp = &ompHostState{handshake: handshake, launchArgv: append([]string(nil), argv...), scan: NewOmpFrameScan()}
	h.mu.Unlock()
	// A confirmed handshake is itself a state change: a watcher starting
	// from since=0 gets an immediate baseline rather than blocking for the
	// full timeout with nothing to report.
	h.touch()
	go h.readOmpEvents(process, reader)
	// A state query never requires the agent to run, so this resolves near-
	// instantly in practice. It is fire-and-forget: the response, whenever
	// it arrives, is handled like any other frame by the loop just started —
	// no separate blocking read that could consume a line meant for it.
	if ompSendInitialGetState {
		_ = h.ompSend(process, ompInitGetStateRequestID, "get_state", nil)
	}
	return handshake, nil
}

// ompSendInitialGetState gates the get_state query StartOmpProcess sends to
// learn omp's own session identity. Tests disable it so a scripted omp's
// terse stdin script is not consumed by a request it never anticipated.
var ompSendInitialGetState = true

// ompInitGetStateRequestID marks the get_state query StartOmpProcess sends
// for its own bookkeeping, distinct from ompDeliverIDPrefix's requests.
const ompInitGetStateRequestID = "init-get-state"

// readOmpLine reads one NDJSON line. A final line without a newline is still
// returned, so a process that prints one plain-text reason and exits has that
// reason visible.
func readOmpLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := reader.ReadLine()
		line = append(line, chunk...)
		if len(line) > maxManagedEventLine {
			return nil, fmt.Errorf("omp-Frame überschreitet %d Bytes", maxManagedEventLine)
		}
		if err != nil {
			return line, err
		}
		if !isPrefix {
			return line, nil
		}
	}
}

// ompSend writes one command to the omp process.
func (h *AgentHost) ompSend(process *agentHostProcess, id, commandType string, fields map[string]any) error {
	line, err := EncodeOmpCommand(id, commandType, fields)
	if err != nil {
		return err
	}
	return process.send(line)
}

// ompSendRaw writes one frame that is not a command, such as an
// extension_ui_response.
func ompSendRaw(process *agentHostProcess, frame map[string]any) error {
	line, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return process.send(line)
}

// deliverOmp sends a queued prompt. While a turn runs it goes as a follow-up,
// because omp answers a prompt sent mid-turn first with success and then with
// a refusal on the same id. Either way the prompt stays in flight until omp
// echoes it as the user message opening a turn, or resolves it locally.
func (h *AgentHost) deliverOmp(process *agentHostProcess, messageID, text string) error {
	command := "prompt"
	if h.turns.TurnRunning() {
		command = "follow_up"
	}
	h.turns.MarkInflight(messageID, text)
	h.touch()
	if err := h.ompSend(process, ompDeliverIDPrefix+messageID, command, map[string]any{"message": text}); err != nil {
		h.turns.FailDelivery(messageID, err.Error())
		h.touch()
		return err
	}
	return nil
}

// interruptOmp asks omp to abort the running turn. The turn is not ended
// here: omp reports the abort as a turn end, and the status follows that
// report rather than the request.
func (h *AgentHost) interruptOmp(process *agentHostProcess) (ManagedTurn, error) {
	h.mu.Lock()
	h.omp.requestSeq++
	id := fmt.Sprintf("abort-%d", h.omp.requestSeq)
	h.mu.Unlock()
	if err := h.ompSend(process, id, "abort", nil); err != nil {
		return ManagedTurn{SessionID: h.sessionID}, err
	}
	turn, _ := h.turns.TurnState()
	return turn, nil
}

// ompEvent is the part of omp's event vocabulary the host folds into turn,
// delivery and permission state.
type ompEvent struct {
	Type       string   `json:"type"`
	ID         string   `json:"id"`
	Method     string   `json:"method"`
	Title      string   `json:"title"`
	Options    []string `json:"options"`
	IsTerminal *bool    `json:"isTerminal"`
	Message    *struct {
		Role         string          `json:"role"`
		Content      json.RawMessage `json:"content"`
		StopReason   string          `json:"stopReason"`
		ErrorMessage string          `json:"errorMessage"`
	} `json:"message"`
	AssistantMessageEvent *struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
	} `json:"assistantMessageEvent"`
}

// readOmpEvents folds omp's event stream into this Session's turn. When the
// stream ends the process has ended, so every request still waiting for a
// decision is closed as no longer answerable — never as allowed or denied.
func (h *AgentHost) readOmpEvents(process *agentHostProcess, reader *bufio.Reader) {
	for {
		line, err := readOmpLine(reader)
		if len(line) > 0 {
			h.applyOmpLine(process, line)
		}
		if err != nil {
			break
		}
	}
	<-process.exited
	closed := h.permissions.CloseUnanswerable(h.sessionID, process.exitReason())
	for _, request := range closed {
		item := PermissionOutcomeItem(request)
		h.turns.CompleteMessage(item)
		h.persistOmpItems(item)
	}
	h.touch()
}

// applyOmpLine folds one protocol line. A line that is not JSON is skipped:
// the handshake already proved the stream speaks the protocol.
func (h *AgentHost) applyOmpLine(process *agentHostProcess, line []byte) {
	frame, err := DecodeOmpFrame(line)
	if err != nil {
		return
	}
	h.persistOmpFrame(frame)
	if frame.IsResponse() {
		if frame.ID == ompInitGetStateRequestID {
			h.applyOmpSessionIDDiscovery(frame)
			return
		}
		h.applyOmpResponse(frame)
		return
	}
	var event ompEvent
	if json.Unmarshal(line, &event) != nil {
		return
	}
	switch event.Type {
	case "turn_start":
		h.mu.Lock()
		h.omp.protocolTurnRunning = true
		h.mu.Unlock()
		h.touch()
	case "turn_end":
		h.mu.Lock()
		h.omp.protocolTurnRunning = false
		h.mu.Unlock()
		h.touch()
	case "message_start":
		if event.Message == nil || event.Message.Role != "user" {
			return
		}
		// The echo acknowledges the in-flight prompt and starts its turn.
		if messageID, ok := h.turns.ConfirmEchoByText(ompMessageText(event.Message.Content)); ok {
			h.mu.Lock()
			h.omp.stream = managedStream{messageID: messageID}
			h.mu.Unlock()
			h.touch()
		}
	case "message_update":
		if event.AssistantMessageEvent == nil || event.AssistantMessageEvent.Type != "text_delta" {
			return
		}
		h.mu.Lock()
		h.omp.stream.text += event.AssistantMessageEvent.Delta
		item := h.omp.stream.item()
		h.mu.Unlock()
		h.turns.PublishChunk(item)
		h.touch()
	case "message_end":
		if event.Message == nil || event.Message.Role != "assistant" {
			return
		}
		h.mu.Lock()
		item, streamed := h.omp.stream.item(), h.omp.stream.text != ""
		h.omp.stream.text = ""
		h.omp.lastStop, h.omp.lastError = event.Message.StopReason, event.Message.ErrorMessage
		h.mu.Unlock()
		if streamed {
			h.turns.CompleteMessage(item)
		}
		h.touch()
	case "agent_end":
		// isTerminal:false announces more work in the same run; only an
		// absent or true value ends the turn.
		if event.IsTerminal != nil && !*event.IsTerminal {
			return
		}
		h.mu.Lock()
		stop, failure := h.omp.lastStop, h.omp.lastError
		h.omp.stream = managedStream{}
		h.omp.lastStop, h.omp.lastError = "", ""
		h.mu.Unlock()
		switch stop {
		case "aborted":
			h.turns.EndTurn(TurnEndInterrupted, "")
		case "error":
			h.turns.EndTurn(TurnEndFailed, failure)
		default:
			h.turns.EndTurn(TurnEndCompleted, "")
		}
		h.touch()
	case "extension_ui_request":
		h.applyOmpUIRequest(process, event)
	}
}

// applyOmpSessionIDDiscovery captures omp's own sessionId from the
// get_state response StartOmpProcess sent. A response without one, or one
// that never arrives, simply leaves OmpSessionID empty — a run reference
// Magentic itself assigned already makes the Session locatable either way.
func (h *AgentHost) applyOmpSessionIDDiscovery(frame OmpFrame) {
	var data struct {
		SessionID string `json:"sessionId"`
	}
	if len(frame.Data) == 0 || json.Unmarshal(frame.Data, &data) != nil || data.SessionID == "" {
		return
	}
	h.mu.Lock()
	h.omp.ompSessionID = data.SessionID
	h.mu.Unlock()
	h.touch()
}

// persistOmpFrame normalizes one frame through this host's own OmpFrameScan
// and durably appends whatever Items it produced — independent of whether
// any interface is presenting the Session, per the conversation-normalization
// spec's durability requirement. It runs before the frame's other effects,
// so persistence never depends on some later branch returning early.
func (h *AgentHost) persistOmpFrame(frame OmpFrame) {
	h.mu.Lock()
	scan := h.omp.scan
	h.mu.Unlock()
	if scan == nil {
		return
	}
	h.persistOmpItems(scan.Normalize(frame)...)
}

// persistOmpItems durably appends items to this Session's own Conversation
// record. It is a no-op for a host that does not speak omp (Claude-managed
// carries no such record).
func (h *AgentHost) persistOmpItems(items ...Item) {
	h.mu.Lock()
	isOmp := h.omp != nil
	h.mu.Unlock()
	if !isOmp || len(items) == 0 {
		return
	}
	if err := AppendOmpConversationItems(string(h.sessionID), items); err != nil {
		Logf("omp-Konversation für Session %q nicht gespeichert: %v", h.sessionID, err)
	}
}

// applyOmpResponse settles a delivered prompt from omp's answers to it. A
// refusal leaves the prompt queued with omp's reason; a prompt omp resolved
// locally, without invoking the agent, is delivered and has no turn to wait
// for. A plain success proves nothing on its own.
func (h *AgentHost) applyOmpResponse(frame OmpFrame) {
	messageID, ok := strings.CutPrefix(frame.ID, ompDeliverIDPrefix)
	if !ok {
		return
	}
	inflight, pending := h.turns.Inflight()
	if !pending || inflight.MessageID != messageID {
		return
	}
	if frame.HasSuccess && !frame.Success {
		h.turns.FailDelivery(messageID, frame.Error)
		h.touch()
		return
	}
	var data struct {
		AgentInvoked *bool `json:"agentInvoked"`
	}
	if len(frame.Data) > 0 && json.Unmarshal(frame.Data, &data) == nil && data.AgentInvoked != nil && !*data.AgentInvoked {
		if h.turns.ConfirmEcho(messageID) {
			h.turns.EndTurn(TurnEndCompleted, "")
			h.touch()
		}
	}
}

// applyOmpUIRequest handles omp asking the host something. A tool approval
// becomes a PermissionRequest that only a person answers; a request for text
// input — a login asking for a key — is cancelled without collecting
// anything, because no credential ever passes through Magentic.
func (h *AgentHost) applyOmpUIRequest(process *agentHostProcess, event ompEvent) {
	switch event.Method {
	case "select":
		if !ompIsApproval(event.Options) {
			_ = ompSendRaw(process, map[string]any{"type": "extension_ui_response", "id": event.ID, "cancelled": true})
			return
		}
		request := h.OpenPermission(event.Title)
		go h.relayOmpDecision(process, event.ID, request.ID)
	case "input", "editor":
		_ = ompSendRaw(process, map[string]any{"type": "extension_ui_response", "id": event.ID, "cancelled": true})
		reason := strings.TrimSpace(event.Title)
		if reason == "" {
			reason = "omp fragt nach einer Eingabe, die einen Login abschließt"
		}
		h.mu.Lock()
		h.omp.loginRequired, h.omp.loginRequiredAt = reason, time.Now()
		h.mu.Unlock()
		h.touch()
	case "confirm":
		_ = ompSendRaw(process, map[string]any{"type": "extension_ui_response", "id": event.ID, "cancelled": true})
	}
}

// relayOmpDecision waits for a person's decision and hands it to omp. A
// request closed as no longer answerable sends nothing: its process is gone.
func (h *AgentHost) relayOmpDecision(process *agentHostProcess, uiRequestID, permissionID string) {
	decision, _, err := h.AwaitPermission(context.Background(), permissionID)
	if err != nil {
		return
	}
	value := "Deny"
	if decision == PermissionAllow {
		value = "Approve"
	}
	_ = ompSendRaw(process, map[string]any{"type": "extension_ui_response", "id": uiRequestID, "value": value})
}

// ompIsApproval reports whether a select request is omp's tool approval,
// which offers exactly Approve and Deny.
func ompIsApproval(options []string) bool {
	var approve, deny bool
	for _, option := range options {
		approve = approve || option == "Approve"
		deny = deny || option == "Deny"
	}
	return approve && deny && len(options) == 2
}

// ompMessageText joins the text parts of a message's content.
func ompMessageText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var joined []string
	for _, part := range parts {
		if part.Type == "text" {
			joined = append(joined, part.Text)
		}
	}
	return strings.Join(joined, "\n")
}
