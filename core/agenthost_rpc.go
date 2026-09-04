package core

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// AgentHostRPCMethod names what one request on an agent host's socket asks
// for. The empty method is the identity handshake: the wire shape of the
// handshake is the host RPC with no method, so a daemon reclaiming a host
// after its own restart speaks the same envelope as every later call.
type AgentHostRPCMethod string

const (
	// AgentHostRPCConnect is the empty-method handshake. serveHandshake uses
	// this name for what the wire leaves unnamed.
	AgentHostRPCConnect AgentHostRPCMethod = ""
)

// AgentHostRPCRequest is one request on an agent host's socket. Token is the
// handshake secret the daemon recorded before the host was started; it is
// checked before anything else is read. Method selects what is asked; unknown
// methods are refused with a stated reason rather than guessed at.
type AgentHostRPCRequest struct {
	Token  AgentHostToken     `json:"token"`
	Method AgentHostRPCMethod `json:"method,omitempty"`
}

// AgentHostRPCResponse answers one host-RPC request. Confirmed is true only
// when the request was understood and accepted; otherwise Reason states why,
// and the caller must treat the outcome as not done.
type AgentHostRPCResponse struct {
	Confirmed bool   `json:"confirmed"`
	Reason    string `json:"reason,omitempty"`
}

// writeAgentHostRPCResponse encodes one response onto the connection. A
// failed encode is reported to the caller; there is nothing to retry — the
// daemon reconnects and asks again.
func writeAgentHostRPCResponse(conn net.Conn, response AgentHostRPCResponse) error {
	return json.NewEncoder(conn).Encode(response)
}

// dispatchRPC answers one token-confirmed request. The token check already
// happened in serveRPC: by the time this runs, the caller proved it holds the
// recorded secret. Verbs beyond the handshake are added here together with
// the client code that issues them; a method this host does not know is
// refused fail-closed so a newer daemon can never trigger behavior this host
// does not implement.
func (h *AgentHost) dispatchRPC(conn net.Conn, request AgentHostRPCRequest) {
	switch request.Method {
	case AgentHostRPCConnect:
		_ = writeAgentHostRPCResponse(conn, AgentHostRPCResponse{Confirmed: true})
	default:
		_ = writeAgentHostRPCResponse(conn, AgentHostRPCResponse{
			Reason: fmt.Sprintf("unbekannte Agent-Host-Methode %q", strings.TrimSpace(string(request.Method))),
		})
	}
}
