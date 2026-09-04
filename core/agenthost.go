package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AgentHostToken is the handshake secret the daemon records when it starts an
// agent host and presents again to reclaim it after a restart. The socket's
// owner-only permissions are the real authorization boundary; the token lets
// a reconnecting daemon tell its own host apart from a stale or foreign
// process answering the same path — reclaiming is identity-confirmed, never
// pattern-matched.
type AgentHostToken string

// NewAgentHostToken creates a fresh, unpredictable token.
func NewAgentHostToken() AgentHostToken { return AgentHostToken(NewUUID()) }

// AgentHostSocketPath is where a managed Session's agent host listens, under
// the state directory and named by the Session so two hosts never collide.
func AgentHostSocketPath(sessionID SessionID) string {
	return filepath.Join(filepath.Dir(StatePath()), "agent-hosts", string(sessionID)+".sock")
}

// agentHostSocketMode is owner-only: the socket is a private control channel
// for this process's own daemon, not a network-reachable interface.
const agentHostSocketMode = 0o600

// AgentHostMethod names what one request on an agent host's socket asks for.
// The empty method is the identity handshake, which is all the wire carries
// today: turn delivery, interrupt and permission answering are the host's own
// methods and are exposed over this socket together with the daemon-side code
// that issues them.
type AgentHostMethod string

// AgentHostConnect is the empty-method handshake.
const AgentHostConnect AgentHostMethod = ""

// AgentHostRequest is one request on an agent host's socket. Token is the
// handshake secret the daemon recorded before the host was started; it is
// checked before anything else is read. Method selects what is asked; unknown
// methods are refused with a stated reason rather than guessed at.
type AgentHostRequest struct {
	Token  AgentHostToken  `json:"token"`
	Method AgentHostMethod `json:"method,omitempty"`
}

// AgentHostResponse answers one request. Confirmed is true only when the
// request was understood and accepted; otherwise Reason states why and the
// caller must treat the outcome as not done.
type AgentHostResponse struct {
	Confirmed bool   `json:"confirmed"`
	Reason    string `json:"reason,omitempty"`
}

// ErrAgentHostServedElsewhere reports a live agent-host socket for this
// Session already being served. A second host is never started alongside it.
var ErrAgentHostServedElsewhere = errors.New("für diese Session läuft bereits ein Agent-Host")

// ErrAgentHostUnreachable reports that nothing answered on the socket path.
// The host is gone; there is nothing there to adopt and nothing to kill.
var ErrAgentHostUnreachable = errors.New("unter diesem Pfad antwortet kein Agent-Host")

// ErrAgentHostForeign reports that something answered on the socket path but
// did not confirm the recorded token. This is the opposite fact from
// unreachable: a process is alive there, and precisely because it could not be
// confirmed it must be neither adopted nor terminated.
var ErrAgentHostForeign = errors.New("der Agent-Host bestätigte das verzeichnete Token nicht")

// AgentHostState is everything a daemon or an interface asks an agent host
// about its Session in one read: whether the owned process is alive and how
// it ended if not, the Session's turn, and the permission requests waiting for
// a person. It is the observation half of this Module's interface — the acting
// half is Deliver, Interrupt and Answer.
type AgentHostState struct {
	SessionID SessionID `json:"sessionId"`
	Alive     bool      `json:"alive"`
	PID       int       `json:"pid,omitempty"`
	// ExitReason states how the owned process ended, and is empty while it
	// runs or when no process was ever started.
	ExitReason string `json:"exitReason,omitempty"`
	// Turn is the running or last turn; TurnKnown is false when no turn was
	// ever recorded for this Session.
	Turn      ManagedTurn `json:"turn,omitzero"`
	TurnKnown bool        `json:"turnKnown,omitempty"`
	// Inflight is a delivered prompt whose echo has not arrived.
	Inflight ManagedInflight `json:"inflight,omitzero"`
	// OpenPermissions are the requests still waiting for an explicit
	// developer decision, oldest first.
	OpenPermissions []PermissionRequest `json:"openPermissions,omitempty"`
	// StreamedItems is the conversation the host produced from the vendor's
	// protocol, completed messages in final form and in-progress ones marked.
	StreamedItems []Item `json:"streamedItems,omitempty"`
}

// AgentHost owns one managed Session's vendor process and speaks that
// vendor's turn protocol on the Session's behalf: it reads the process's
// stream-json output, folds every protocol line into the Session's turn, and
// holds the permission requests the agent is blocked on. Because the host —
// not an interface, and not the daemon — holds all of that, a turn and an open
// permission request survive every interface disconnecting and the daemon
// restarting. Over its unix socket it answers the identity handshake, so a
// restarted daemon confirms and reclaims it instead of starting a second
// process.
type AgentHost struct {
	sessionID SessionID
	token     AgentHostToken
	path      string
	listener  *net.UnixListener

	mu        sync.Mutex
	process   *agentHostProcess
	closeOnce sync.Once
	// turns is this Session's turn, its in-flight prompt and its streamed
	// conversation. One host owns one Session, so this is that Session's
	// state directly and not a map that would have to be addressed.
	turns *ManagedTurns
	// permissions holds this Session's open permission requests. A request
	// with nobody to answer it waits here rather than being decided.
	permissions *PermissionStore
	// stream is the message the running turn is producing, accumulated so a
	// completed message supersedes its own chunks rather than truncating them.
	stream managedStream
}

// StartAgentHost claims the socket for sessionID and begins accepting
// handshake connections. The caller supplies the token it already recorded
// durably before calling this — per ADR 0003, intent is written before any
// process exists — so a crash between recording and this call never leaves
// an unconfirmable host. A platform that cannot own a managed process is
// refused here, before a socket exists.
func StartAgentHost(sessionID SessionID, token AgentHostToken) (*AgentHost, error) {
	if err := managedRuntimeSupported(); err != nil {
		return nil, err
	}
	path := AgentHostSocketPath(sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		probe, dialErr := net.Dial("unix", path)
		if dialErr == nil {
			probe.Close()
			return nil, ErrAgentHostServedElsewhere
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, removeErr
		}
	}
	address, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, agentHostSocketMode); err != nil {
		listener.Close()
		return nil, err
	}
	host := &AgentHost{
		sessionID: sessionID, token: token, path: path, listener: listener,
		turns: NewManagedTurns(sessionID), permissions: NewPermissionStore(),
	}
	go host.accept()
	return host, nil
}

// Path is the socket a daemon is expected to find.
func (h *AgentHost) Path() string { return h.path }

// SessionID is the managed Session this host owns.
func (h *AgentHost) SessionID() SessionID { return h.sessionID }

func (h *AgentHost) accept() {
	for {
		conn, err := h.listener.AcceptUnix()
		if err != nil {
			return
		}
		go h.serve(conn)
	}
}

// serve answers exactly one request per connection. Every connection is
// served independently: a connection closing, or the daemon disconnecting,
// never stops this host from accepting the next one — that is what lets a
// daemon restart reconnect instead of losing the process. The token is
// checked before anything is dispatched, and a method this host does not know
// is refused fail-closed so a newer daemon can never trigger behavior this
// host does not implement.
func (h *AgentHost) serve(conn *net.UnixConn) {
	defer conn.Close()
	encoder := json.NewEncoder(conn)
	var request AgentHostRequest
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		_ = encoder.Encode(AgentHostResponse{Reason: "malformed request"})
		return
	}
	if request.Token != h.token {
		_ = encoder.Encode(AgentHostResponse{Reason: "token mismatch"})
		return
	}
	switch request.Method {
	case AgentHostConnect:
		_ = encoder.Encode(AgentHostResponse{Confirmed: true})
	default:
		_ = encoder.Encode(AgentHostResponse{
			Reason: fmt.Sprintf("unbekannte Agent-Host-Methode %q", strings.TrimSpace(string(request.Method))),
		})
	}
}

// ConnectAgentHost dials an agent host's socket and performs the identity
// handshake with token. Two refusals are told apart, because they call for
// opposite readings: ErrAgentHostUnreachable means nothing is there, while
// ErrAgentHostForeign means something is there that this token does not own.
// In neither case may the caller adopt or kill anything on that path.
func ConnectAgentHost(path string, token AgentHostToken) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrAgentHostUnreachable, path, err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(AgentHostRequest{Token: token, Method: AgentHostConnect}); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrAgentHostUnreachable, path, err)
	}
	var response AgentHostResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrAgentHostUnreachable, path, err)
	}
	if !response.Confirmed {
		reason := response.Reason
		if reason == "" {
			reason = "Handshake nicht bestätigt"
		}
		return fmt.Errorf("%w: %s: %s", ErrAgentHostForeign, path, reason)
	}
	return nil
}

// StartVendorProcess launches binary as this host's owned process, in its
// own working directory, and begins reading its protocol output. It may be
// called at most once per host.
func (h *AgentHost) StartVendorProcess(binary string, argv []string, dir string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.process != nil {
		return errors.New("dieser Agent-Host besitzt bereits einen Prozess")
	}
	process, err := startAgentHostProcess(binary, argv, dir)
	if err != nil {
		return err
	}
	h.process = process
	if events := process.events(); events != nil {
		go h.readVendorEvents(process, events)
	}
	return nil
}

// HostState reads everything this host knows about its Session at once.
func (h *AgentHost) HostState() AgentHostState {
	h.mu.Lock()
	process := h.process
	h.mu.Unlock()
	state := AgentHostState{
		SessionID:       h.sessionID,
		Alive:           process.alive(),
		PID:             process.pid(),
		ExitReason:      process.exitReason(),
		OpenPermissions: h.permissions.OpenRequests(),
		StreamedItems:   h.turns.StreamedConversation(),
	}
	state.Turn, state.TurnKnown = h.turns.TurnState()
	state.Inflight, _ = h.turns.Inflight()
	return state
}

// Deliver writes one queued Outbox prompt to the vendor and marks it as in
// flight. The queue advances only when the vendor echoes the prompt back —
// never on this call returning. A failed write leaves the prompt queued with
// its reason and resends nothing.
func (h *AgentHost) Deliver(messageID, text string) error {
	h.mu.Lock()
	process := h.process
	h.mu.Unlock()
	line, err := json.Marshal(managedPromptLine(text))
	if err != nil {
		return err
	}
	h.turns.MarkInflight(messageID, text)
	if err := process.send(line); err != nil {
		h.turns.FailDelivery(messageID, err.Error())
		return err
	}
	return nil
}

// Interrupt ends the running turn and signals the owned process to abort it
// while staying alive for the next prompt. With no turn running it is refused
// and nothing is signalled.
func (h *AgentHost) Interrupt() (ManagedTurn, error) {
	if !h.turns.TurnRunning() {
		return ManagedTurn{SessionID: h.sessionID},
			fmt.Errorf("für Session %q läuft kein Turn, der unterbrochen werden könnte", h.sessionID)
	}
	h.mu.Lock()
	process := h.process
	h.mu.Unlock()
	if err := process.interrupt(); err != nil {
		return ManagedTurn{SessionID: h.sessionID}, err
	}
	return h.turns.InterruptTurn()
}

// OpenPermission registers a vendor permission prompt and blocks the caller —
// the agent's own tool call — until a person decides it or the process ends.
func (h *AgentHost) OpenPermission(asked string) PermissionRequest {
	return h.permissions.Open(h.sessionID, asked)
}

// Answer delivers a developer's decision to one open permission request,
// exactly once. A second answer is refused and delivers nothing.
func (h *AgentHost) Answer(requestID string, decision PermissionDecision, decidedBy string) error {
	return h.permissions.Answer(requestID, decision, decidedBy)
}

// AwaitPermission blocks in the agent's own tool call until the request is
// decided or closed as no longer answerable. ctx bounds the wait for the
// caller only: giving up on the answer never answers the request.
func (h *AgentHost) AwaitPermission(ctx context.Context, requestID string) (PermissionDecision, PermissionOutcome, error) {
	return h.permissions.Wait(ctx, requestID)
}

// managedPromptLine is the stream-json shape one delivered prompt is written
// as on the vendor's stdin.
func managedPromptLine(text string) any {
	return map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": text,
		},
	}
}

// maxManagedEventLine bounds one protocol line. A single streamed message can
// be long; anything past this is a protocol break, not a message.
const maxManagedEventLine = 4 << 20

// readVendorEvents folds the vendor's protocol stream into this Session's
// turn. It is the only place a managed turn starts, streams and ends: the
// facts come from the protocol, never from watching a pane or from a Session
// going quiet. When the stream ends the process has ended, so every request
// still waiting for a decision is closed as no longer answerable — never as
// allowed or denied.
func (h *AgentHost) readVendorEvents(process *agentHostProcess, events io.Reader) {
	scanner := bufio.NewScanner(events)
	scanner.Buffer(make([]byte, 0, 64<<10), maxManagedEventLine)
	for scanner.Scan() {
		event, ok := ParseManagedEventLine(scanner.Bytes())
		if !ok {
			continue
		}
		h.applyManagedEvent(event)
	}
	h.permissions.CloseUnanswerable(h.sessionID, process.exitReason())
}

// applyManagedEvent folds one parsed protocol line into the Session's turn.
func (h *AgentHost) applyManagedEvent(event ManagedEvent) {
	switch event.Kind {
	case ManagedEventEcho:
		// The echo acknowledges the in-flight prompt and starts its turn.
		// An echo of anything else advances nothing.
		if messageID, ok := h.turns.ConfirmEchoByText(event.EchoText); ok {
			h.mu.Lock()
			h.stream = managedStream{messageID: messageID}
			h.mu.Unlock()
		}
	case ManagedEventChunk:
		h.mu.Lock()
		h.stream.text += event.ChunkText
		item := h.stream.item()
		h.mu.Unlock()
		h.turns.PublishChunk(item)
	case ManagedEventTurnEnd:
		h.mu.Lock()
		item, streaming := h.stream.item(), h.stream.text != ""
		h.stream = managedStream{}
		h.mu.Unlock()
		if streaming {
			h.turns.CompleteMessage(item)
		}
		h.turns.EndTurn(event.EndReason, event.FailReason)
	}
}

// managedStream accumulates the message a turn is producing. The Item's
// identity is the turn's own message, so every chunk and the completed form
// supersede each other in place instead of piling up.
type managedStream struct {
	messageID string
	text      string
}

func (s managedStream) item() Item {
	return Item{
		ID:     "managed-stream-" + s.messageID,
		Role:   ItemRoleAgent,
		Kind:   ItemKindAgentMessage,
		Title:  "Antwort",
		Detail: s.text,
	}
}

// Close stops the vendor process this host owns (if any), stops accepting
// connections, and removes the socket file.
func (h *AgentHost) Close() error {
	var err error
	h.closeOnce.Do(func() {
		h.mu.Lock()
		process := h.process
		h.process = nil
		h.mu.Unlock()
		if process != nil {
			_ = process.stop()
		}
		h.permissions.CloseUnanswerable(h.sessionID, process.exitReason())
		err = h.listener.Close()
		_ = os.Remove(h.path)
	})
	return err
}

// ClaudeApprovalMCPToolName is the fully qualified MCP tool name the managed
// runtime names with --permission-prompt-tool. It is a supported SDK entry
// point that Claude Code's own --help does not advertise (see design.md).
const ClaudeApprovalMCPToolName = "mcp__magentic-approve__approve"

// ClaudeManagedArgv builds the exact argument list a managed Claude Code
// process is launched with. run must carry the Session's own conversation
// identity: mode "new" starts that identity fresh with --session-id, any
// other mode continues it with --resume. mcpConfigPath names the
// --mcp-config file wiring in the agent-approve MCP server for this Session.
func ClaudeManagedArgv(mcpConfigPath string, run *AgentRunRef, mode string) ([]string, error) {
	if run == nil || strings.TrimSpace(run.ExternalID) == "" {
		return nil, errors.New("der managed Runtime braucht eine gespeicherte Run-Referenz")
	}
	if strings.TrimSpace(mcpConfigPath) == "" {
		return nil, errors.New("der managed Runtime braucht einen --mcp-config-Pfad")
	}
	argv := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--replay-user-messages",
		"--permission-prompts", "host",
		"--permission-prompt-tool", ClaudeApprovalMCPToolName,
		"--mcp-config", mcpConfigPath,
	}
	flag := "--resume"
	if mode == "new" {
		flag = "--session-id"
	}
	return append(argv, flag, run.ExternalID), nil
}

// ClaudeManagedRuntimeVerifiedVersions lists the Claude Code CLI versions the
// managed runtime's stream-json protocol was verified against (see
// design.md). The stream-json protocol is an SDK surface, not a stability
// guarantee, so only an exact match is accepted.
var ClaudeManagedRuntimeVerifiedVersions = map[string]bool{
	"2.1.259": true,
}

// VerifyClaudeManagedRuntimeVersion reports whether the installed Claude Code
// CLI's version string is verified for the managed runtime. An unverified
// version fails with a stated reason: a protocol break must degrade to
// "managed runtime unavailable," never to a Session that silently does
// nothing.
func VerifyClaudeManagedRuntimeVersion(version string) (bool, string) {
	trimmed := strings.TrimSpace(version)
	if trimmed != "" && ClaudeManagedRuntimeVerifiedVersions[trimmed] {
		return true, ""
	}
	return false, fmt.Sprintf("Claude Code %q ist für den managed Runtime nicht verifiziert", trimmed)
}
