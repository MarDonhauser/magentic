package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"magentic/core"
)

// ConnectionState ist die eigene Tatsache der Client-Anbindung (D4): strikt
// getrennt vom Zustand jeder Session. Ein Verbindungsproblem wird nie als
// Präsenz-, Aktivitäts- oder Attention-Änderung einer Session ausgedrückt.
type ConnectionState string

const (
	// ConnDetached: nie verbunden oder bewusst getrennt — kein Auto-Reconnect.
	ConnDetached ConnectionState = "detached"
	// ConnAttaching: Anbindung läuft.
	ConnAttaching ConnectionState = "attaching"
	// ConnAttached: letzte Austausche erfolgreich.
	ConnAttached ConnectionState = "attached"
	// ConnDegraded: letzter Aufruf schlug fehl, letzte bekannte Sicht liegt
	// vor, kein Reconnect-Lauf aktiv.
	ConnDegraded ConnectionState = "degraded"
	// ConnReconnecting: Reconnect-Lauf mit begrenztem Backoff aktiv.
	ConnReconnecting ConnectionState = "reconnecting"
	// ConnRefused: Host wies die Anmeldedaten ab — kein Retry mit denselben.
	ConnRefused ConnectionState = "refused"
)

// AuthRefusedError meldet eine abgewiesene Anmeldedaten: kein Transport-
// problem, kein Reconnect mit derselben.
type AuthRefusedError struct {
	Message string
}

func (e *AuthRefusedError) Error() string { return "Anmeldung abgewiesen: " + e.Message }

// TransportFailure meldet einen Drahtfehler unterhalb der Methode.
type TransportFailure struct {
	Message string
}

func (e *TransportFailure) Error() string { return "Transportfehler: " + e.Message }

// Clock ist die monotone Client-Uhr für das Alter der letzten bekannten
// Sicht. Host-Zeit geht nie ein — Clock-Skew kann das Alter nicht
// verfälschen (D-Risiko „last known 4 minutes ago").
type Clock func() time.Time

// ClientConfig beschreibt eine Anbindung. RootCAs trägt Test-Zertifikate;
// nil heißt System-Roots. RoundTripper ersetzt das Netz in Tests.
type ClientConfig struct {
	Link         HostLink
	Credentials  CredentialStore
	Pins         *FingerprintStore
	RootCAs      *x509.CertPool
	RoundTripper http.RoundTripper
	Clock        Clock
	AutoBackoff  Backoff
}

// Backoff begrenzt exponentielles Warten mit Jitter.
type Backoff struct {
	Base time.Duration
	Max  time.Duration
}

// Next nennt die Wartezeit für Versuch attempt (0-basiert): exponentiell
// wachsend, gedeckelt, mit Jitter bis zur Hälfte.
func (b Backoff) Next(attempt int) time.Duration {
	wait := b.Base
	for i := 0; i < attempt && wait < b.Max; i++ {
		wait *= 2
		if wait > b.Max {
			wait = b.Max
		}
	}
	if wait > b.Max {
		wait = b.Max
	}
	if wait <= 0 {
		return 0
	}
	half := int64(wait) / 2
	return wait/2 + time.Duration(rand.Int63n(half+1))
}

// Client ist die remote-Implementierung der Host-API-Nahtstelle: unäre
// Aufrufe als HTTPS-POST mit Bearer-Token und Version, Streams über
// /v1/stream (term.go). Der Token-Wert lebt nur im Speicher, nie in Datei.
type Client struct {
	mu          sync.Mutex
	link        HostLink
	config      ClientConfig
	state       ConnectionState
	token       HostToken
	lastSuccess time.Time
	lastKnown   json.RawMessage
	knownFresh  bool
	policy      map[string]PolicyEntry
	auto        bool
	reconnect   *reconnector
	http        *http.Client
	clock       Clock
}

// NewClient startet detached. Attach wählt den Host bewusst.
func NewClient(config ClientConfig) *Client {
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	backoff := config.AutoBackoff
	if backoff.Base <= 0 {
		backoff.Base = 500 * time.Millisecond
	}
	if backoff.Max <= 0 {
		backoff.Max = 30 * time.Second
	}
	transport := config.RoundTripper
	if transport == nil {
		transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: config.RootCAs}}
	}
	client := &Client{
		link: config.Link, config: config,
		state: ConnDetached, clock: clock,
		policy: map[string]PolicyEntry{},
		http:   &http.Client{Transport: transport, Timeout: 15 * time.Second},
	}
	client.reconnect = newReconnector(backoff, client.dialOnce)
	return client
}

func (c *Client) dialOnce(ctx context.Context) error {
	return c.attach(ctx, true)
}

// State nennt den expliziten Verbindungszustand.
func (c *Client) State() ConnectionState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// AddressedHost nennt den gerade adressierten Host — in Client-Mode immer
// sichtbar, damit keine Aktion je die falsche Maschine trifft.
func (c *Client) AddressedHost() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.link.Address
}

// LastKnownAge nennt das Alter der letzten bekannten Sicht nach der eigenen
// monotonen Uhr.
func (c *Client) LastKnownAge() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastSuccess.IsZero() {
		return -1
	}
	return c.clock().Sub(c.lastSuccess)
}

func (c *Client) setStateLocked(state ConnectionState) {
	c.state = state
}

// Attach verbindet bewusst mit dem HostLink: Anmeldedaten aus dem
// OS-Store, TLS-Pinning, dann Policy als Versions- und Auth-Prüfung.
func (c *Client) Attach(ctx context.Context) error {
	return c.attach(ctx, false)
}

func (c *Client) attach(ctx context.Context, background bool) error {
	c.mu.Lock()
	c.setStateLocked(ConnAttaching)
	c.mu.Unlock()

	token, err := c.config.Credentials.LoadToken(c.link.CredentialRef)
	if err != nil {
		c.mu.Lock()
		c.setStateLocked(ConnDetached)
		c.mu.Unlock()
		return fmt.Errorf("Anmeldedaten unerreichbar — detached: %w", err)
	}
	policy, fingerprint, err := c.handshake(ctx, token)
	if err != nil {
		if refused, ok := err.(*AuthRefusedError); ok {
			c.mu.Lock()
			c.setStateLocked(ConnRefused)
			c.auto = false
			c.mu.Unlock()
			return refused
		}
		c.mu.Lock()
		if c.auto || background {
			c.setStateLocked(ConnReconnecting)
		} else {
			c.setStateLocked(ConnDegraded)
		}
		c.mu.Unlock()
		if !background {
			return &TransportFailure{Message: err.Error()}
		}
		return err
	}
	_ = fingerprint
	c.mu.Lock()
	c.token = token
	c.lastSuccess = c.clock()
	c.knownFresh = false
	for _, doc := range policy {
		c.policy[doc.Method] = PolicyEntry{Class: doc.Class, Reason: doc.Reason}
	}
	c.setStateLocked(ConnAttached)
	c.mu.Unlock()
	return nil
}

// handshake prüft Pinning, Version und Anmeldung in einem Gang: Wer die
// Policy lesen darf, ist authentifiziert und spricht die Version.
func (c *Client) handshake(ctx context.Context, token HostToken) ([]PolicyMethodDoc, string, error) {
	fingerprint, err := c.peerFingerprint(ctx)
	if err != nil {
		return nil, "", err
	}
	if c.config.Pins != nil {
		if _, changed, pinErr := c.config.Pins.Check(c.link.Address, fingerprint); pinErr != nil {
			if changed {
				return nil, "", &AuthRefusedError{Message: pinErr.Error()}
			}
			return nil, "", &TransportFailure{Message: pinErr.Error()}
		}
	}
	policy, err := c.fetchPolicy(ctx, token)
	if err != nil {
		return nil, "", err
	}
	return policy, fingerprint, nil
}

func (c *Client) peerFingerprint(ctx context.Context) (string, error) {
	transport, ok := c.http.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil {
		// Test-Transport ohne TLS: Pinning entfällt, Version+Auth tragen.
		return "test-transport", nil
	}
	config := transport.TLSClientConfig.Clone()
	config.InsecureSkipVerify = true
	prober := &http.Client{Transport: &http.Transport{TLSClientConfig: config}, Timeout: 10 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+c.link.Address+"/v1/policy", nil)
	if err != nil {
		return "", &TransportFailure{Message: err.Error()}
	}
	response, err := prober.Do(request)
	if err != nil {
		return "", &TransportFailure{Message: err.Error()}
	}
	defer response.Body.Close()
	if response.TLS == nil || len(response.TLS.PeerCertificates) == 0 {
		return "", &TransportFailure{Message: "kein TLS-Zertifikat gesehen"}
	}
	return FingerprintOfDER(response.TLS.PeerCertificates[0].Raw), nil
}

func (c *Client) fetchPolicy(ctx context.Context, token HostToken) ([]PolicyMethodDoc, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+c.link.Address+"/v1/policy", nil)
	if err != nil {
		return nil, &TransportFailure{Message: err.Error()}
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	response, err := c.http.Do(request)
	if err != nil {
		return nil, &TransportFailure{Message: err.Error()}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return nil, &AuthRefusedError{Message: "Host wies die Anmeldedaten ab"}
	}
	if response.StatusCode != http.StatusOK {
		return nil, &TransportFailure{Message: "Policy-Endpunkt meldet " + response.Status}
	}
	var policy []PolicyMethodDoc
	if err := json.NewDecoder(response.Body).Decode(&policy); err != nil {
		return nil, &TransportFailure{Message: "Policy unlesbar: " + err.Error()}
	}
	return policy, nil
}

// Call stellt einen unären Aufruf zu: Version, Identität (D7), Bearer-Token.
// Eine Host-Verweigerung ist maßgeblich über dem gecachten Policy-Stand.
func (c *Client) Call(ctx context.Context, method string, params any, identity string) (json.RawMessage, error) {
	c.mu.Lock()
	token := c.token
	state := c.state
	c.mu.Unlock()
	if state == ConnDetached || state == ConnRefused {
		return nil, fmt.Errorf("nicht verbunden (%s) — erst Attach", state)
	}
	encoded, err := json.Marshal(Request{
		Version: ProtocolVersion, ID: core.NewUUID(),
		Method: method, Params: EncodeParams(params), Identity: identity,
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+c.link.Address+"/v1/call", bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+string(token))
	response, err := c.http.Do(request)
	if err != nil {
		c.noteTransportFailure()
		return nil, &TransportFailure{Message: err.Error()}
	}
	defer response.Body.Close()
	var wire Response
	if err := json.NewDecoder(response.Body).Decode(&wire); err != nil {
		c.noteTransportFailure()
		return nil, &TransportFailure{Message: "Antwort unlesbar: " + err.Error()}
	}
	if wire.Error != nil {
		return nil, c.noteWireError(method, wire.Error)
	}
	c.mu.Lock()
	c.lastSuccess = c.clock()
	if c.state == ConnReconnecting || c.state == ConnDegraded {
		c.setStateLocked(ConnAttached)
	}
	c.mu.Unlock()
	return wire.Result, nil
}

func (c *Client) noteTransportFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.auto {
		c.setStateLocked(ConnReconnecting)
	} else {
		c.setStateLocked(ConnDegraded)
	}
}

func (c *Client) noteWireError(method string, wire *WireError) error {
	switch wire.Code {
	case ErrorAuth:
		c.mu.Lock()
		c.setStateLocked(ConnRefused)
		c.auto = false
		c.mu.Unlock()
		return &AuthRefusedError{Message: wire.Message}
	case ErrorRestricted:
		c.mu.Lock()
		c.policy[method] = PolicyEntry{Class: ActionRestricted, Reason: wire.Message}
		c.lastSuccess = c.clock()
		c.mu.Unlock()
		return &RestrictedError{Method: method, Reason: wire.Message}
	default:
		return wire
	}
}

// Policy nennt die gecachte Host-Policy zum Ausgrauen; maßgeblich bleibt der
// Host (noteWireError pflegt sie bei Verweigerung nach).
func (c *Client) Policy() map[string]PolicyEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	copied := make(map[string]PolicyEntry, len(c.policy))
	for method, entry := range c.policy {
		copied[method] = entry
	}
	return copied
}

// Detach trennt bewusst: Streams schließen, kein Auto-Reconnect.
func (c *Client) Detach() {
	c.mu.Lock()
	c.setStateLocked(ConnDetached)
	c.auto = false
	c.token = ""
	c.lastKnown = nil
	c.knownFresh = false
	c.mu.Unlock()
	if c.reconnect != nil {
		c.reconnect.stop()
	}
}

// EnableAutoReconnect schaltet den begrenzten Reconnect-Lauf an und ab.
func (c *Client) EnableAutoReconnect(on bool) {
	c.mu.Lock()
	c.auto = on
	c.mu.Unlock()
	if on && c.reconnect != nil {
		c.reconnect.start()
	} else if c.reconnect != nil {
		c.reconnect.stop()
	}
}

// ReconnectNow löst einen sofortigen manuellen Versuch aus.
func (c *Client) ReconnectNow(ctx context.Context) error {
	return c.attach(ctx, false)
}

// Refresh synchronisiert die Sicht neu: Erst eine frische bekannte Nutzlast
// löscht die Last-known-Markierung (7.5).
func (c *Client) Refresh(ctx context.Context, method string, params any) (availability core.ObservationAvailability, err error) {
	result, err := c.Call(ctx, method, params, "")
	if err != nil {
		return core.ObservationUnavailable, err
	}
	c.mu.Lock()
	c.lastKnown = result
	c.knownFresh = true
	c.mu.Unlock()
	return core.ObservationAvailable, nil
}

// LastKnown nennt die letzte bekannte Sicht mit ihrer Frische.
func (c *Client) LastKnown() (json.RawMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastKnown, c.knownFresh
}

// SwitchHost löst die alte Anbindung vollständig, bevor die neue beginnt:
// Sessions des alten Hosts erscheinen nie neben denen des neuen (7.3).
func (c *Client) SwitchHost(link HostLink) {
	c.Detach()
	c.mu.Lock()
	c.link = link
	c.policy = map[string]PolicyEntry{}
	c.mu.Unlock()
	c.reconnect = newReconnector(c.config.AutoBackoff, c.dialOnce)
}

// reconnector läuft begrenztes Backoff mit Jitter, stoppt bei
// Anmelde-Verweigerung und bei bewusstem Detach.
type reconnector struct {
	backoff Backoff
	dial    func(context.Context) error
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	wakeCh  chan struct{}
}

func newReconnector(backoff Backoff, dial func(context.Context) error) *reconnector {
	return &reconnector{backoff: backoff, dial: dial}
}

func (r *reconnector) start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return
	}
	r.running = true
	r.stopCh = make(chan struct{})
	r.wakeCh = make(chan struct{}, 1)
	go r.loop(r.stopCh, r.wakeCh)
}

func (r *reconnector) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return
	}
	r.running = false
	close(r.stopCh)
}

func (r *reconnector) wake() {
	select {
	case r.wakeCh <- struct{}{}:
	default:
	}
}

func (r *reconnector) loop(stopCh, wakeCh chan struct{}) {
	attempt := 0
	for {
		wait := r.backoff.Next(attempt)
		timer := time.NewTimer(wait)
		select {
		case <-stopCh:
			timer.Stop()
			return
		case <-wakeCh:
			timer.Stop()
		case <-timer.C:
		}
		err := r.dial(context.Background())
		if err == nil {
			return
		}
		if _, refused := err.(*AuthRefusedError); refused {
			return
		}
		attempt++
	}
}
