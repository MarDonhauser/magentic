package remote

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ErrInputNotDelivered meldet Tastatur-Eingabe im Getrennten: Sie wird weder
// übertragen noch für später still zwischengespeichert (9.4).
type ErrInputNotDelivered struct {
	SessionID string
}

func (e *ErrInputNotDelivered) Error() string {
	return fmt.Sprintf("Terminal %s nimmt derzeit keine Eingabe an — Eingabe verworfen, nichts zwischengespeichert", e.SessionID)
}

// streamDialer öffnet einen Terminal-Stream. Die Produktion wählt
// /v1/stream über WebSocket, Tests hängen einen Speicher-Stub ein.
type streamDialer interface {
	DialStream(ctx context.Context, sessionID string, fromSeq uint64) (StreamChannel, error)
}

// StreamChannel ist ein offener WS-Kanal: Rahmen kommen herein,
// Close macht zu.
type StreamChannel interface {
	Receive() (Frame, error)
	Close() error
}

// TermAttachment ist ein angehängtes Remote-Terminal: OpenTerm/WriteTerm/
// ResizeTerm/CloseTerm und der term:data:-Pfad, abgebildet auf den
// Streaming-Kanal. Inhalt ersetzt bei Lücke (nie Append über die Lücke),
// Eingabe im Getrennten wird verworfen statt queued.
type TermAttachment struct {
	client    *Client
	dialer    streamDialer
	sessionID string

	mu       sync.Mutex
	content  []byte
	lastSeq  uint64
	missed   bool
	attached bool
	channel  StreamChannel
	done     chan struct{}
}

// OpenTerm hängt an und liest ab Ursprung (fromSeq 0) oder ab lastSeq.
func (c *Client) OpenTerm(ctx context.Context, sessionID string, cols, rows int) (*TermAttachment, error) {
	return c.openTermWith(ctx, sessionID, cols, rows, &websocketDialer{client: c})
}

// openTermWith ist die testbare Naht mit eingehängtem Dialer.
func (c *Client) openTermWith(ctx context.Context, sessionID string, cols, rows int, dialer streamDialer) (*TermAttachment, error) {
	if _, err := c.Call(ctx, "OpenTerm", map[string]any{
		"sessionID": sessionID, "cols": cols, "rows": rows,
	}, ""); err != nil {
		return nil, err
	}
	attachment := &TermAttachment{client: c, dialer: dialer, sessionID: sessionID, done: make(chan struct{})}
	if err := attachment.subscribe(ctx, 0); err != nil {
		return nil, err
	}
	return attachment, nil
}

func (t *TermAttachment) subscribe(ctx context.Context, fromSeq uint64) error {
	channel, err := t.dialer.DialStream(ctx, t.sessionID, fromSeq)
	if err != nil {
		return err
	}
	t.mu.Lock()
	if t.channel != nil {
		_ = t.channel.Close()
	}
	t.channel = channel
	t.attached = true
	t.mu.Unlock()
	go t.pump()
	return nil
}

func (t *TermAttachment) pump() {
	for {
		t.mu.Lock()
		channel := t.channel
		t.mu.Unlock()
		if channel == nil {
			return
		}
		frame, err := channel.Receive()
		if err != nil {
			t.mu.Lock()
			t.attached = false
			t.mu.Unlock()
			return
		}
		t.apply(frame)
	}
}

// apply faltet einen Rahmen ein: Ausgabe anhängen, Lücke ersetzen und
// markieren — nie über die Lücke appenden.
func (t *TermAttachment) apply(frame Frame) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch frame.Kind {
	case FrameTermOutput:
		data, err := frame.TermBytes()
		if err != nil {
			return
		}
		t.content = append(t.content, data...)
		t.lastSeq = frame.Seq + uint64(len(data))
	case FrameGap:
		snapshot, err := frame.TermBytes()
		if err != nil {
			return
		}
		// Nur wer schon Inhalt hatte, hat etwas versäumt: Der Ursprung eines
		// Neu-Anhangs ersetzt leere Fläche und markiert nichts.
		hadContent := len(t.content) > 0
		t.content = append([]byte(nil), snapshot...)
		t.lastSeq = frame.Seq
		if hadContent {
			t.missed = true
		}
	case FrameControl:
		if frame.Control == "closed" {
			t.attached = false
		}
	}
}

// Content nennt den sichtbaren Inhalt plus Lücken-Markierung.
func (t *TermAttachment) Content() (content []byte, missed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.content...), t.missed
}

// LastSeq nennt die letzte empfangene Position für Resume.
func (t *TermAttachment) LastSeq() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastSeq
}

// Resume hängt nach Abriss an letzter Position wieder an: im Fenster nahtlos
// weiter, außerhalb ehrliche Lücke (9.2/9.3).
func (t *TermAttachment) Resume(ctx context.Context) error {
	t.mu.Lock()
	from := t.lastSeq
	t.mu.Unlock()
	return t.subscribe(ctx, from)
}

// Write schickt Eingabe-Bytes — nur verbunden. Getrennt wird verweigert und
// nichts für später aufgehoben.
func (t *TermAttachment) Write(ctx context.Context, data []byte) error {
	if t.client.State() != ConnAttached {
		return &ErrInputNotDelivered{SessionID: t.sessionID}
	}
	_, err := t.client.Call(ctx, "WriteTerm", map[string]string{
		"sessionID": t.sessionID,
		"dataB64":   base64.StdEncoding.EncodeToString(data),
	}, "")
	return err
}

// Resize meldet die Fenstergröße.
func (t *TermAttachment) Resize(ctx context.Context, cols, rows int) error {
	if t.client.State() != ConnAttached {
		return &ErrInputNotDelivered{SessionID: t.sessionID}
	}
	_, err := t.client.Call(ctx, "ResizeTerm", map[string]any{
		"sessionID": t.sessionID, "cols": cols, "rows": rows,
	}, "")
	return err
}

// Close macht den Anhang zu (Kanal plus serverseitige Abmeldung).
func (t *TermAttachment) Close(ctx context.Context) error {
	t.mu.Lock()
	if t.channel != nil {
		_ = t.channel.Close()
		t.channel = nil
	}
	t.attached = false
	select {
	case <-t.done:
	default:
		close(t.done)
	}
	t.mu.Unlock()
	_, err := t.client.Call(ctx, "CloseTerm", map[string]string{"sessionID": t.sessionID}, "")
	return err
}

// websocketDialer wählt echte Streams über /v1/stream mit Bearer-Handshake.
type websocketDialer struct {
	client *Client
}

func (d *websocketDialer) DialStream(ctx context.Context, sessionID string, fromSeq uint64) (StreamChannel, error) {
	d.client.mu.Lock()
	token := d.client.token
	address := d.client.link.Address
	d.client.mu.Unlock()
	tlsConfig := &tls.Config{}
	if transport, ok := d.client.http.Transport.(*http.Transport); ok && transport.TLSClientConfig != nil {
		tlsConfig = transport.TLSClientConfig.Clone()
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, TLSClientConfig: tlsConfig}
	conn, _, err := dialer.DialContext(ctx, "wss://"+address+"/v1/stream", nil)
	if err != nil {
		return nil, &TransportFailure{Message: err.Error()}
	}
	if state, ok := conn.UnderlyingConn().(*tls.Conn); ok {
		peer := state.ConnectionState()
		if len(peer.PeerCertificates) > 0 && d.client.config.Pins != nil {
			fingerprint := FingerprintOfDER(peer.PeerCertificates[0].Raw)
			if _, changed, pinErr := d.client.config.Pins.Check(address, fingerprint); pinErr != nil {
				_ = conn.Close()
				if changed {
					return nil, &AuthRefusedError{Message: pinErr.Error()}
				}
				return nil, &TransportFailure{Message: pinErr.Error()}
			}
		}
	}
	hello, _ := json.Marshal(StreamHello{Version: ProtocolVersion, SessionID: sessionID, FromSeq: fromSeq, Token: token})
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = conn.Close()
		return nil, &TransportFailure{Message: err.Error()}
	}
	if err := conn.WriteMessage(websocket.TextMessage, hello); err != nil {
		_ = conn.Close()
		return nil, &TransportFailure{Message: err.Error()}
	}
	return &websocketChannel{conn: conn}, nil
}

type websocketChannel struct {
	conn *websocket.Conn
}

func (c *websocketChannel) Receive() (Frame, error) {
	var frame Frame
	if err := c.conn.ReadJSON(&frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func (c *websocketChannel) Close() error {
	return c.conn.Close()
}
