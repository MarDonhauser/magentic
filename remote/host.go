package remote

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"magentic/core"
)

// DefaultBind ist die einzige Bindung ohne ausdrückliche Betreiber-
// Entscheidung: Loopback. Ein Overlay (Tailscale-/LAN-Adresse) braucht
// --bind und damit eine bewusste Konfiguration; eine öffentliche Adresse
// ohne gesetztes Flag verweigert der Host fail-closed.
const DefaultBind = "127.0.0.1"

// HostConfig beschreibt einen Host-Dienst. Backend bedient die Aufrufe,
// OptIn öffnet einzelne beschränkte Methoden, Log bekommt Status ohne je
// Token-Werte zu sehen.
type HostConfig struct {
	Dir          string
	Bind         string
	Port         int
	BindExplicit bool
	Backend      HostBackend
	OptIn        map[string]bool
	Log          func(format string, args ...any)
}

// Host ist der langlebige Magentic-Prozess auf der Maschine, der die tmux-
// Sessions, die Registry und die Repositories besitzt. Er spricht
// ausschließlich TLS (HTTP/1.1 — HTTP/2 versteht kein WebSocket-Upgrade,
// deshalb ist TLSNextProto leer) und Bearer-HostTokens.
type Host struct {
	config   HostConfig
	tokens   *TokenStore
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	streams  map[*streamConn]string
	closed   chan struct{}
	upgrader websocket.Upgrader
}

type streamConn struct {
	conn  *websocket.Conn
	close chan struct{}
	once  sync.Once
}

func (s *streamConn) shutdown() {
	s.once.Do(func() { close(s.close) })
}

// NewHost richtet Zertifikat, Token-Speicher und Listener ein, startet aber
// noch nichts — Serve blockiert danach bis Close.
func NewHost(config HostConfig) (*Host, error) {
	if config.Dir == "" {
		return nil, fmt.Errorf("Host-Verzeichnis fehlt")
	}
	if config.Backend == nil {
		return nil, fmt.Errorf("Host-Backend fehlt")
	}
	bind := config.Bind
	if bind == "" {
		bind = DefaultBind
	}
	if !config.BindExplicit && !isLoopbackBind(bind) {
		return nil, fmt.Errorf("Bindung an %q braucht eine ausdrückliche Konfiguration (--bind)", bind)
	}
	cert, err := LoadOrCreateCertificate(config.Dir)
	if err != nil {
		return nil, err
	}
	tokens, err := OpenTokenStore(config.Dir + "/host-tokens.json")
	if err != nil {
		return nil, err
	}
	host := &Host{
		config:   config,
		tokens:   tokens,
		closed:   make(chan struct{}),
		streams:  map[*streamConn]string{},
		upgrader: websocket.Upgrader{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/call", host.serveCall)
	mux.HandleFunc("/v1/policy", host.servePolicy)
	mux.HandleFunc("/v1/stream", host.serveStream)
	host.server = &http.Server{
		Handler: mux,
		// HTTP/2 und WebSocket-Upgrade schließen sich auf einem Go-Server
		// aus: Hijack braucht HTTP/1.1. Deshalb bleibt TLSNextProto leer
		// (kein H2) — bewusste Abweichung von „HTTP/2 + TLS", der Stream
		// braucht das Upgrade.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	address := fmt.Sprintf("%s:%d", bind, config.Port)
	listener, err := tls.Listen("tcp", address, host.server.TLSConfig)
	if err != nil {
		return nil, err
	}
	host.listener = listener
	return host, nil
}

// Addr nennt die gebundene Adresse zum Anzeigen beim Start.
func (h *Host) Addr() string {
	if h.listener == nil {
		return ""
	}
	return h.listener.Addr().String()
}

// Serve blockiert, bis Close gerufen wird.
func (h *Host) Serve() error {
	err := h.server.Serve(h.listener)
	select {
	case <-h.closed:
		return nil
	default:
		return err
	}
}

// Close beendet Listener und alle offenen Streams.
func (h *Host) Close() error {
	h.mu.Lock()
	select {
	case <-h.closed:
	default:
		close(h.closed)
	}
	for stream := range h.streams {
		stream.shutdown()
	}
	h.mu.Unlock()
	return h.server.Close()
}

// IssueToken gibt eine frische Geräte-Anmeldedaten aus.
func (h *Host) IssueToken() (HostToken, error) {
	return h.tokens.Issue()
}

// Revoke entzieht eine Anmeldedaten, schließt ihre offenen Streams und
// verweigert danach jede ihrer Anfragen. Andere Geräte bleiben unberührt.
func (h *Host) Revoke(token HostToken) error {
	hash := tokenHash(token)
	if err := h.tokens.Revoke(token); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for stream, owner := range h.streams {
		if owner == hash {
			stream.shutdown()
		}
	}
	return nil
}

func (h *Host) log(format string, args ...any) {
	if h.config.Log != nil {
		h.config.Log(format, args...)
	}
}

func bearerToken(request *http.Request) HostToken {
	header := request.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return HostToken(strings.TrimSpace(parts[1]))
}

func (h *Host) authenticated(request *http.Request) bool {
	return h.tokens.Valid(bearerToken(request))
}

func writeResponse(writer http.ResponseWriter, response Response) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(response)
}

func (h *Host) serveCall(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "nur POST", http.StatusMethodNotAllowed)
		return
	}
	if !h.authenticated(request) {
		h.log("Aufruf ohne gültige Anmeldung von %s verweigert", request.RemoteAddr)
		writer.WriteHeader(http.StatusUnauthorized)
		writeResponse(writer, Response{Version: ProtocolVersion, Error: RedactedAuthError("unbekannte oder fehlende Anmeldedaten")})
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 4<<20))
	if err != nil {
		writeResponse(writer, Response{Version: ProtocolVersion, Error: &WireError{Code: ErrorTransport, Message: "Anfrage unlesbar"}})
		return
	}
	var call Request
	if err := json.Unmarshal(body, &call); err != nil {
		writeResponse(writer, Response{Version: ProtocolVersion, Error: &WireError{Code: ErrorTransport, Message: "Anfrage unlesbar"}})
		return
	}
	if call.Version != ProtocolVersion {
		writeResponse(writer, ErrorResult(call, ErrorVersion, "Protokollversion passt nicht"))
		return
	}
	if err := EnforceRemote(call.Method, h.config.OptIn); err != nil {
		h.log("Beschränkte Methode %s von %s verweigert", call.Method, request.RemoteAddr)
		writeResponse(writer, ErrorResult(call, ErrorRestricted, err.Error()))
		return
	}
	if err := rejectPathShapedReference(call.Method, call.Params); err != nil {
		h.log("Pfad-Eingabe in %s von %s verweigert", call.Method, request.RemoteAddr)
		writeResponse(writer, ErrorResult(call, ErrorRestricted, err.Error()))
		return
	}
	result, err := h.config.Backend.HandleCall(request.Context(), call.Method, call.Params, call.Identity)
	if err != nil {
		if wire, ok := err.(*WireError); ok {
			writeResponse(writer, ErrorResult(call, wire.Code, wire.Message))
			return
		}
		writeResponse(writer, ErrorResult(call, ErrorInternal, err.Error()))
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		writeResponse(writer, ErrorResult(call, ErrorInternal, "Ergebnis nicht codierbar"))
		return
	}
	writeResponse(writer, Response{Version: ProtocolVersion, ID: call.ID, Result: encoded})
}

// rejectPathShapedReference ist das Grenznetz für Handle-Parameter: Der
// reference-Schlüssel trägt einen WorktreeRef, nie einen Pfad. Freitext
// (Nachrichten, Prompts) wird bewusst nicht gescannt.
func rejectPathShapedReference(method string, params json.RawMessage) error {
	if len(params) == 0 {
		return nil
	}
	var shaped map[string]any
	if err := json.Unmarshal(params, &shaped); err != nil {
		return nil
	}
	reference, ok := shaped["reference"].(string)
	if !ok {
		return nil
	}
	if err := RejectClientPath(reference); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	return nil
}

func (h *Host) servePolicy(writer http.ResponseWriter, request *http.Request) {
	if !h.authenticated(request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(writer).Encode(RedactedAuthError("unbekannte oder fehlende Anmeldedaten"))
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(PolicyDocument())
}

// StreamHello eröffnet einen Stream: Handshake plus Anmeldung in der ersten
// Nachricht mit Frist, vor jedem Abo.
type StreamHello struct {
	Version   int       `json:"version"`
	SessionID string    `json:"sessionId"`
	FromSeq   uint64    `json:"fromSeq"`
	Token     HostToken `json:"token"`
}

func (h *Host) serveStream(writer http.ResponseWriter, request *http.Request) {
	conn, err := h.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	stream := &streamConn{conn: conn, close: make(chan struct{})}
	defer func() {
		h.mu.Lock()
		delete(h.streams, stream)
		h.mu.Unlock()
		_ = conn.Close()
	}()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello StreamHello
	if err := conn.ReadJSON(&hello); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if hello.Version != ProtocolVersion {
		_ = conn.WriteJSON(Frame{Kind: FrameControl, Control: "version-mismatch"})
		return
	}
	if !h.tokens.Valid(hello.Token) {
		h.log("Stream ohne gültige Anmeldung von %s verweigert", request.RemoteAddr)
		_ = conn.WriteJSON(Frame{Kind: FrameControl, Control: "auth-refused"})
		return
	}
	subscription, err := h.config.Backend.Subscribe(hello.SessionID, hello.FromSeq)
	if err != nil {
		if wire, ok := err.(*WireError); ok {
			_ = conn.WriteJSON(Frame{Kind: FrameControl, Control: string(wire.Code) + ":" + wire.Message})
			return
		}
		_ = conn.WriteJSON(Frame{Kind: FrameControl, Control: "error"})
		return
	}
	defer subscription.Close()
	h.mu.Lock()
	h.streams[stream] = tokenHash(hello.Token)
	h.mu.Unlock()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	go func() {
		defer stream.shutdown()
		for {
			var discard any
			if err := conn.ReadJSON(&discard); err != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		}
	}()
	for {
		select {
		case <-stream.close:
			_ = conn.WriteJSON(Frame{Kind: FrameControl, Control: "closed"})
			return
		case frame, open := <-subscription.Frames():
			if !open {
				_ = conn.WriteJSON(Frame{Kind: FrameControl, Control: "closed"})
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(frame); err != nil {
				return
			}
		}
	}
}

// isLoopbackBind akzeptiert Loopback-Bindungen und „alle Interfaces" nur als
// explizite Entscheidung — der Aufrufer prüft BindExplicit davor.
func isLoopbackBind(bind string) bool {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		host = bind
	}
	trimmed := strings.Trim(host, "[]")
	if trimmed == "" || trimmed == "localhost" {
		return true
	}
	parsed := net.ParseIP(trimmed)
	return parsed != nil && parsed.IsLoopback()
}

// HostDir nennt das Standardverzeichnis für Zertifikat und Token-Hashes:
// neben dem State, damit MAGENTIC_STATE-Umleitungen (Tests) folgen.
func HostDir() string {
	return filepath.Join(filepath.Dir(core.StatePath()), "remote-host")
}
