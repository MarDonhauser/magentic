package core

import (
	"encoding/json"
	"errors"
	"fmt"
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

// AgentHostConnectRequest is the first message a connecting daemon sends: the
// token it recorded when it started or last reclaimed this host.
type AgentHostConnectRequest struct {
	Token AgentHostToken `json:"token"`
}

// AgentHostConnectResponse answers the handshake. Confirmed is false, with
// Reason stated, when the presented token does not match. The host neither
// adopts a connection with a mismatched token as its daemon, nor tears
// itself down because of it — an unconfirmed caller may not be the daemon at
// all.
type AgentHostConnectResponse struct {
	Confirmed bool   `json:"confirmed"`
	Reason    string `json:"reason,omitempty"`
}

// ErrAgentHostServedElsewhere reports a live agent-host socket for this
// Session already being served. A second host is never started alongside it.
var ErrAgentHostServedElsewhere = errors.New("für diese Session läuft bereits ein Agent-Host")

// AgentHost owns one managed Session's vendor process and answers the
// identity handshake over a unix socket, so a daemon can confirm it and
// reconnect after its own restart without a second process ever being
// started.
type AgentHost struct {
	sessionID SessionID
	token     AgentHostToken
	path      string
	listener  *net.UnixListener

	mu        sync.Mutex
	process   *agentHostProcess
	closeOnce sync.Once
}

// StartAgentHost claims the socket for sessionID and begins accepting
// handshake connections. The caller supplies the token it already recorded
// durably before calling this — per ADR 0003, intent is written before any
// process exists — so a crash between recording and this call never leaves
// an unconfirmable host.
func StartAgentHost(sessionID SessionID, token AgentHostToken) (*AgentHost, error) {
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
	host := &AgentHost{sessionID: sessionID, token: token, path: path, listener: listener}
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
		go h.serveHandshake(conn)
	}
}

// serveHandshake answers exactly one connect request per connection. Every
// connection is served independently: a connection closing, or the daemon
// disconnecting, never stops this host from accepting the next one — that is
// what lets a daemon restart reconnect instead of losing the process.
func (h *AgentHost) serveHandshake(conn *net.UnixConn) {
	defer conn.Close()
	var request AgentHostConnectRequest
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		writeAgentHostConnectResponse(conn, AgentHostConnectResponse{Reason: "malformed connect request"})
		return
	}
	if request.Token != h.token {
		writeAgentHostConnectResponse(conn, AgentHostConnectResponse{Reason: "token mismatch"})
		return
	}
	writeAgentHostConnectResponse(conn, AgentHostConnectResponse{Confirmed: true})
}

func writeAgentHostConnectResponse(conn *net.UnixConn, response AgentHostConnectResponse) {
	_ = json.NewEncoder(conn).Encode(response)
}

// ConnectAgentHost dials an agent host's socket and performs the identity
// handshake with token. A refusal (mismatched token, or nothing answering)
// is returned as an error; the caller must not adopt or kill anything on
// that path — the process holding the socket may not be this host at all.
func ConnectAgentHost(path string, token AgentHostToken) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(AgentHostConnectRequest{Token: token}); err != nil {
		return err
	}
	var response AgentHostConnectResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return err
	}
	if !response.Confirmed {
		reason := response.Reason
		if reason == "" {
			reason = "Handshake nicht bestätigt"
		}
		return fmt.Errorf("Agent-Host unter %s: %s", path, reason)
	}
	return nil
}

// StartVendorProcess launches binary as this host's owned process, in its
// own working directory. It may be called at most once per host.
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
	return nil
}

// VendorProcessAlive reports whether the owned vendor process has not yet
// exited. It answers false for a host that never started one.
func (h *AgentHost) VendorProcessAlive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.process.alive()
}

// VendorProcessPID is the owned process's identity, or 0 when none is
// running.
func (h *AgentHost) VendorProcessPID() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.process.pid()
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
