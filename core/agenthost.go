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
	"time"
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
// The empty method is the identity handshake; the others are the managed
// control surface the daemon and the interfaces use instead of keystrokes.
type AgentHostMethod string

const (
	// AgentHostConnect is the empty-method handshake.
	AgentHostConnect AgentHostMethod = ""
	// AgentHostStateMethod reads everything the host knows in one read.
	AgentHostStateMethod AgentHostMethod = "state"
	// AgentHostInterruptMethod ends the running turn, leaving the process alive.
	AgentHostInterruptMethod AgentHostMethod = "interrupt"
	// AgentHostAnswerMethod delivers a developer's decision to one request.
	AgentHostAnswerMethod AgentHostMethod = "answer"
	// AgentHostDeliverMethod hands one queued Outbox prompt to the process.
	// The answer confirms only that it was sent; delivery is confirmed later
	// by the protocol's echo, visible in the host's state.
	AgentHostDeliverMethod AgentHostMethod = "deliver"
	// AgentHostShutdownMethod asks the host to stop its process and exit.
	// Only the daemon holding the recorded token can ask for it.
	AgentHostShutdownMethod AgentHostMethod = "shutdown"
	// AgentHostWatchMethod blocks the connection until the host's state has
	// changed past the caller's last-known revision, or a bounded timeout
	// elapses, then returns the current state and revision. It is how a
	// status transition reaches an interface without waiting for the next
	// periodic observation cycle.
	AgentHostWatchMethod AgentHostMethod = "watch"
)

// AgentHostRequest is one request on an agent host's socket. Token is the
// handshake secret the daemon recorded before the host was started; it is
// checked before anything else is read. Method selects what is asked; unknown
// methods are refused with a stated reason rather than guessed at.
type AgentHostRequest struct {
	Token  AgentHostToken  `json:"token"`
	Method AgentHostMethod `json:"method,omitempty"`
	// RequestID and Decision answer one PermissionRequest; DecidedBy names the
	// explicit developer action for the record and carries no authority.
	RequestID string             `json:"requestId,omitempty"`
	Decision  PermissionDecision `json:"decision,omitempty"`
	DecidedBy string             `json:"decidedBy,omitempty"`
	// MessageID and Text carry one Outbox prompt for the deliver method.
	MessageID string `json:"messageId,omitempty"`
	Text      string `json:"text,omitempty"`
	// Since and TimeoutMS parametrize the watch method: the caller's
	// last-known revision, and how long to block for a newer one.
	Since     uint64 `json:"since,omitempty"`
	TimeoutMS int    `json:"timeoutMs,omitempty"`
}

// AgentHostResponse answers one request. Confirmed is true only when the
// request was understood and accepted; otherwise Reason states why and the
// caller must treat the outcome as not done.
type AgentHostResponse struct {
	Confirmed  bool               `json:"confirmed"`
	Reason     string             `json:"reason,omitempty"`
	State      *AgentHostState    `json:"state,omitempty"`
	Turn       *ManagedTurn       `json:"turn,omitempty"`
	Permission *PermissionRequest `json:"permission,omitempty"`
	// Revision and Changed answer the watch method: the state's revision at
	// return time, and whether it actually advanced past the request's
	// Since (false means the bounded wait simply timed out).
	Revision uint64 `json:"revision,omitempty"`
	Changed  bool   `json:"changed,omitempty"`
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
	// LaunchArgv is the argument list this host started its omp process
	// with. A reclaimed host reports its own, never one inherited from a
	// record; it is empty for a host that did not start omp.
	LaunchArgv []string `json:"launchArgv,omitempty"`
	// ProtocolVersion is the omp protocol version the handshake confirmed.
	ProtocolVersion int `json:"protocolVersion,omitempty"`
	// ProtocolTurnRunning is omp's own turn_start/turn_end boundary: true
	// while a turn_start has been reported with no turn_end since. It is
	// finer-grained than Turn.Running, which spans the whole agent run
	// across tool round trips.
	ProtocolTurnRunning bool `json:"protocolTurnRunning,omitempty"`
	// FailedMessageID and DeliveryFailure name the last prompt the process
	// refused and its own reason, so the Outbox can keep it queued with why.
	FailedMessageID string `json:"failedMessageId,omitempty"`
	DeliveryFailure string `json:"deliveryFailure,omitempty"`
	// LoginRequired names a login omp asked the host to complete with a
	// secret it cancelled rather than collected, naming what the developer
	// must supply in omp's own interface. Empty once nothing is pending.
	LoginRequired   string    `json:"loginRequired,omitempty"`
	LoginRequiredAt time.Time `json:"loginRequiredAt,omitzero"`
	// OmpSessionID is omp's own sessionId, once learned from get_state.
	OmpSessionID string `json:"ompSessionId,omitempty"`
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
	// done is closed once the host has closed, so its owner can exit.
	done chan struct{}
	// revMu, revision and changed back the watch method: touch() bumps
	// revision and closes+replaces changed under revMu, so a waiter blocked
	// on the old channel wakes, rereads revision under the lock, and either
	// returns or waits again on the fresh channel.
	revMu    sync.Mutex
	revision uint64
	changed  chan struct{}
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
	// omp is set when the owned process speaks omp's rpc-ui protocol rather
	// than Claude Code's stream-json; it selects how prompts, interrupts and
	// permission answers reach the process.
	omp *ompHostState
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
		done: make(chan struct{}), changed: make(chan struct{}),
	}
	go host.accept()
	return host, nil
}

// touch bumps the state revision and wakes every goroutine blocked in
// waitForChange. Call it after any mutation HostState() would surface
// differently — a turn boundary, a permission opening or closing, a login
// requirement, or a delivery failure.
func (h *AgentHost) touch() {
	h.revMu.Lock()
	h.revision++
	ch := h.changed
	h.changed = make(chan struct{})
	h.revMu.Unlock()
	close(ch)
}

// waitForChange blocks until the revision differs from since, or ctx ends.
// changed reports which: true means the returned revision is newer, false
// means ctx ended (typically the bounded watch timeout) with nothing new.
func (h *AgentHost) waitForChange(ctx context.Context, since uint64) (revision uint64, changed bool) {
	for {
		h.revMu.Lock()
		revision, ch := h.revision, h.changed
		h.revMu.Unlock()
		if revision != since {
			return revision, true
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return revision, false
		}
	}
}

// Done is closed once the host has closed, whether asked to by its owner or
// by the daemon's shutdown request.
func (h *AgentHost) Done() <-chan struct{} { return h.done }

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
	case AgentHostStateMethod:
		_ = encoder.Encode(AgentHostResponse{Confirmed: true, State: func() *AgentHostState {
			state := h.HostState()
			return &state
		}()})
	case AgentHostInterruptMethod:
		ended, err := h.Interrupt()
		if err != nil {
			_ = encoder.Encode(AgentHostResponse{Reason: err.Error()})
			return
		}
		_ = encoder.Encode(AgentHostResponse{Confirmed: true, Turn: &ended})
	case AgentHostDeliverMethod:
		if inflight, pending := h.turns.Inflight(); pending {
			_ = encoder.Encode(AgentHostResponse{Reason: fmt.Sprintf("Prompt %q ist noch unterwegs", inflight.MessageID)})
			return
		}
		if err := h.Deliver(request.MessageID, request.Text); err != nil {
			_ = encoder.Encode(AgentHostResponse{Reason: err.Error()})
			return
		}
		_ = encoder.Encode(AgentHostResponse{Confirmed: true})
	case AgentHostWatchMethod:
		timeout := time.Duration(request.TimeoutMS) * time.Millisecond
		if timeout <= 0 || timeout > agentHostWatchMaxTimeout {
			timeout = agentHostWatchMaxTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		revision, changed := h.waitForChange(ctx, request.Since)
		cancel()
		state := h.HostState()
		_ = encoder.Encode(AgentHostResponse{Confirmed: true, State: &state, Revision: revision, Changed: changed})
	case AgentHostShutdownMethod:
		_ = encoder.Encode(AgentHostResponse{Confirmed: true})
		go h.Close()
	case AgentHostAnswerMethod:
		permission, err := h.answerWithOutcome(request.RequestID, request.Decision, request.DecidedBy)
		if err != nil {
			_ = encoder.Encode(AgentHostResponse{Reason: err.Error()})
			return
		}
		_ = encoder.Encode(AgentHostResponse{Confirmed: true, Permission: &permission})
	default:
		_ = encoder.Encode(AgentHostResponse{
			Reason: fmt.Sprintf("unbekannte Agent-Host-Methode %q", strings.TrimSpace(string(request.Method))),
		})
	}
}

// agentHostWatchMaxTimeout bounds how long a watch call blocks the
// connection when the caller asks for longer, or asks for nothing. It keeps
// a misbehaving or absent-minded caller from pinning a goroutine forever.
const agentHostWatchMaxTimeout = 30 * time.Second

// ConnectAgentHost dials an agent host's socket and performs the identity
// handshake with token. Two refusals are told apart, because they call for
// opposite readings: ErrAgentHostUnreachable means nothing is there, while
// ErrAgentHostForeign means something is there that this token does not own.
// In neither case may the caller adopt or kill anything on that path.
func ConnectAgentHost(path string, token AgentHostToken) error {
	response, err := callAgentHost(path, AgentHostRequest{Token: token, Method: AgentHostConnect})
	if err != nil {
		return err
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

// callAgentHost issues one method call on an agent host's socket. The token
// is checked before anything is dispatched; a mismatch reads as foreign,
// anything else unreadable as unreachable.
func callAgentHost(path string, request AgentHostRequest) (AgentHostResponse, error) {
	return callAgentHostTimeout(path, request, 0)
}

// callAgentHostTimeout is callAgentHost with an explicit deadline on the
// whole round trip; zero means none, for the ordinary quick calls.
func callAgentHostTimeout(path string, request AgentHostRequest, timeout time.Duration) (AgentHostResponse, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return AgentHostResponse{}, fmt.Errorf("%w: %s: %v", ErrAgentHostUnreachable, path, err)
	}
	defer conn.Close()
	if timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return AgentHostResponse{}, fmt.Errorf("%w: %s: %v", ErrAgentHostUnreachable, path, err)
	}
	var response AgentHostResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return AgentHostResponse{}, fmt.Errorf("%w: %s: %v", ErrAgentHostUnreachable, path, err)
	}
	if !response.Confirmed && isAgentHostTokenMismatch(response.Reason) {
		return response, fmt.Errorf("%w: %s: %s", ErrAgentHostForeign, path, response.Reason)
	}
	return response, nil
}

func isAgentHostTokenMismatch(reason string) bool {
	return reason == "token mismatch"
}

// QueryAgentHostState reads everything one managed Session's host knows, in
// one call. Unreachable and foreign stay distinguishable: only the handshake
// decides ownership, never a guessed process.
func QueryAgentHostState(path string, token AgentHostToken) (AgentHostState, error) {
	response, err := callAgentHost(path, AgentHostRequest{Token: token, Method: AgentHostStateMethod})
	if err != nil {
		return AgentHostState{}, err
	}
	if !response.Confirmed || response.State == nil {
		reason := response.Reason
		if reason == "" {
			reason = "Agent-Host gab keinen Zustand zurück"
		}
		return AgentHostState{}, errors.New(reason)
	}
	return *response.State, nil
}

// DeliverAgentHostPrompt hands one queued prompt to a host. A confirmed call
// means only that the prompt was sent; it counts as delivered once the host's
// state shows the turn it opened.
func DeliverAgentHostPrompt(path string, token AgentHostToken, messageID, text string) error {
	response, err := callAgentHost(path, AgentHostRequest{Token: token, Method: AgentHostDeliverMethod, MessageID: messageID, Text: text})
	if err != nil {
		return err
	}
	if !response.Confirmed {
		return errors.New(response.Reason)
	}
	return nil
}

// ShutdownAgentHost asks a host to stop its process and exit.
func ShutdownAgentHost(path string, token AgentHostToken) error {
	response, err := callAgentHost(path, AgentHostRequest{Token: token, Method: AgentHostShutdownMethod})
	if err != nil {
		return err
	}
	if !response.Confirmed {
		return errors.New(response.Reason)
	}
	return nil
}

// WatchAgentHostState blocks until the host reports a revision newer than
// since, or timeout elapses, then returns the state it read at that point.
// changed reports which happened: false means the timeout, not a real
// change — the caller should not treat the returned state as new in that
// case, only as current.
func WatchAgentHostState(path string, token AgentHostToken, since uint64, timeout time.Duration) (state AgentHostState, revision uint64, changed bool, err error) {
	response, err := callAgentHostTimeout(path, AgentHostRequest{
		Token: token, Method: AgentHostWatchMethod, Since: since, TimeoutMS: int(timeout / time.Millisecond),
	}, timeout+agentHostWatchClientMargin)
	if err != nil {
		return AgentHostState{}, since, false, err
	}
	if !response.Confirmed || response.State == nil {
		reason := response.Reason
		if reason == "" {
			reason = "Agent-Host beantwortete watch nicht"
		}
		return AgentHostState{}, since, false, errors.New(reason)
	}
	return *response.State, response.Revision, response.Changed, nil
}

// agentHostWatchClientMargin is added to a watch call's own deadline before
// the client gives up waiting for the connection, so the server's own
// timeout is always the one that fires first.
const agentHostWatchClientMargin = 5 * time.Second

// InterruptAgentHostTurn ends the running turn of one managed Session through
// its host. With no turn running the host refuses and nothing is signalled.
func InterruptAgentHostTurn(path string, token AgentHostToken) (ManagedTurn, error) {
	response, err := callAgentHost(path, AgentHostRequest{Token: token, Method: AgentHostInterruptMethod})
	if err != nil {
		return ManagedTurn{}, err
	}
	if !response.Confirmed || response.Turn == nil {
		reason := response.Reason
		if reason == "" {
			reason = "Agent-Host unterbrach den Turn nicht"
		}
		return ManagedTurn{}, mapAgentHostMethodError(reason)
	}
	return *response.Turn, nil
}

// AnswerAgentHostPermission delivers a developer's explicit decision to one
// open PermissionRequest through its host, exactly once.
func AnswerAgentHostPermission(path string, token AgentHostToken, requestID string, decision PermissionDecision, decidedBy string) (PermissionRequest, error) {
	response, err := callAgentHost(path, AgentHostRequest{
		Token: token, Method: AgentHostAnswerMethod,
		RequestID: requestID, Decision: decision, DecidedBy: decidedBy,
	})
	if err != nil {
		return PermissionRequest{}, err
	}
	if !response.Confirmed || response.Permission == nil {
		reason := response.Reason
		if reason == "" {
			reason = "Agent-Host nahm die Entscheidung nicht entgegen"
		}
		return PermissionRequest{}, mapAgentHostMethodError(reason)
	}
	return *response.Permission, nil
}

// mapAgentHostMethodError restores the typed refusal a host method answered
// with, so a second answer stays a refusal and an unknown request stays
// unknown instead of flattening into one generic failure.
func mapAgentHostMethodError(reason string) error {
	if strings.Contains(reason, ErrPermissionClosed.Error()) {
		return fmt.Errorf("%w: %s", ErrPermissionClosed, reason)
	}
	if strings.Contains(reason, ErrPermissionUnknown.Error()) {
		return fmt.Errorf("%w: %s", ErrPermissionUnknown, reason)
	}
	if strings.Contains(reason, ErrManagedNoTurn.Error()) {
		return fmt.Errorf("%w: %s", ErrManagedNoTurn, reason)
	}
	return errors.New(reason)
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
	process, omp := h.process, h.omp
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
	state.FailedMessageID, state.DeliveryFailure = h.turns.LastDeliveryFailure()
	if omp != nil {
		h.mu.Lock()
		state.LoginRequired, state.LoginRequiredAt = omp.loginRequired, omp.loginRequiredAt
		state.LaunchArgv = append([]string(nil), omp.launchArgv...)
		state.ProtocolVersion = omp.handshake.ProtocolVersion
		state.ProtocolTurnRunning = omp.protocolTurnRunning
		state.OmpSessionID = omp.ompSessionID
		h.mu.Unlock()
	}
	return state
}

// Deliver writes one queued Outbox prompt to the vendor and marks it as in
// flight. The queue advances only when the vendor echoes the prompt back —
// never on this call returning. A failed write leaves the prompt queued with
// its reason and resends nothing.
func (h *AgentHost) Deliver(messageID, text string) error {
	h.mu.Lock()
	process, omp := h.process, h.omp
	h.mu.Unlock()
	if omp != nil {
		return h.deliverOmp(process, messageID, text)
	}
	line, err := json.Marshal(managedPromptLine(text))
	if err != nil {
		return err
	}
	h.turns.MarkInflight(messageID, text)
	h.touch()
	if err := process.send(line); err != nil {
		h.turns.FailDelivery(messageID, err.Error())
		h.touch()
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
			fmt.Errorf("%w: Session %q", ErrManagedNoTurn, h.sessionID)
	}
	h.mu.Lock()
	process, omp := h.process, h.omp
	h.mu.Unlock()
	if omp != nil {
		return h.interruptOmp(process)
	}
	if err := process.interrupt(); err != nil {
		return ManagedTurn{SessionID: h.sessionID}, err
	}
	return h.turns.InterruptTurn()
}

// OpenPermission registers a vendor permission prompt and blocks the caller —
// the agent's own tool call — until a person decides it or the process ends.
// The opened request is part of the Session's activity, in the order it
// occurred.
func (h *AgentHost) OpenPermission(asked string) PermissionRequest {
	request := h.permissions.Open(h.sessionID, asked)
	item := PermissionRequestItem(request)
	h.turns.CompleteMessage(item)
	h.persistOmpItems(item)
	h.touch()
	return request
}

// Answer delivers a developer's decision to one open permission request,
// exactly once. A second answer is refused and delivers nothing.
func (h *AgentHost) Answer(requestID string, decision PermissionDecision, decidedBy string) error {
	_, err := h.answerWithOutcome(requestID, decision, decidedBy)
	return err
}

// answerWithOutcome delivers the decision and records its outcome as the Item
// following the request, so the Session's account holds question and answer
// in order.
func (h *AgentHost) answerWithOutcome(requestID string, decision PermissionDecision, decidedBy string) (PermissionRequest, error) {
	if err := h.permissions.Answer(requestID, decision, decidedBy); err != nil {
		return PermissionRequest{}, err
	}
	closed := h.permissions.closedRequest(requestID)
	item := PermissionOutcomeItem(closed)
	h.turns.CompleteMessage(item)
	h.persistOmpItems(item)
	h.touch()
	return closed, nil
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
	closed := h.permissions.CloseUnanswerable(h.sessionID, process.exitReason())
	for _, request := range closed {
		item := PermissionOutcomeItem(request)
		h.turns.CompleteMessage(item)
		h.persistOmpItems(item)
	}
	h.touch()
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
			h.touch()
		}
	case ManagedEventChunk:
		h.mu.Lock()
		h.stream.text += event.ChunkText
		item := h.stream.item()
		h.mu.Unlock()
		h.turns.PublishChunk(item)
		h.touch()
	case ManagedEventTurnEnd:
		h.mu.Lock()
		item, streaming := h.stream.item(), h.stream.text != ""
		h.stream = managedStream{}
		h.mu.Unlock()
		if streaming {
			h.turns.CompleteMessage(item)
		}
		h.turns.EndTurn(event.EndReason, event.FailReason)
		h.touch()
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
		closed := h.permissions.CloseUnanswerable(h.sessionID, process.exitReason())
		for _, request := range closed {
			h.turns.CompleteMessage(PermissionOutcomeItem(request))
		}
		err = h.listener.Close()
		_ = os.Remove(h.path)
		close(h.done)
	})
	return err
}

// OmpMagenticProfile is the omp profile every Session Magentic starts runs
// under. It is dedicated rather than shared so a developer's own per-tool
// approval policy — which omp honors in every approval mode, `always-ask`
// included — never narrows the gate Magentic depends on (see design.md).
const OmpMagenticProfile = "magentic"

// ompApprovalGateArgv is the argument prefix every omp launch shares to keep
// the approval gate on: --mode rpc-ui, --approval-mode always-ask, --profile
// magentic. OmpArgv and the behavioral verifier (core/omp_verify.go) both
// build their argv on this one place, so the verifier cannot drift from what
// a real Session is actually launched with.
func ompApprovalGateArgv() []string {
	return []string{
		"--mode", "rpc-ui",
		"--approval-mode", "always-ask",
		"--profile", OmpMagenticProfile,
	}
}

// OmpArgv builds the exact argument list an omp process is launched with for
// one Session. The approval gate rides on process provenance, so
// --approval-mode always-ask and --profile magentic are always present and
// never anything this function did not put there: no --auto-approve, no
// --approval-mode yolo, no --no-session. mode "new" starts session.Dir fresh
// with no --resume flag; any other mode resumes run.ExternalID and refuses
// when no run ref is given. The model is passed only when SessionModel
// reports one known; an unknown model is omitted, never defaulted.
func OmpArgv(session Session, run *AgentRunRef, mode string) ([]string, error) {
	argv := append(ompApprovalGateArgv(), "--cwd", session.Dir)
	if model, ok := session.SessionModel(); ok {
		argv = append(argv, "--model", model)
	}
	if mode != "new" {
		if run == nil || strings.TrimSpace(run.ExternalID) == "" {
			return nil, errors.New("der omp Runtime braucht eine gespeicherte Run-Referenz, um fortzusetzen")
		}
		argv = append(argv, "--resume", run.ExternalID)
	}
	return argv, nil
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
