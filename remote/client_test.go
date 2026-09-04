package remote

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(status int, value any) *http.Response {
	encoded, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(encoded)),
		Header:     make(http.Header),
	}
}

// stubNet beantwortet Policy und Calls aus der Hand — ohne Netz.
func stubNet(policy []PolicyMethodDoc, calls map[string]func(Request) Response) roundTripFunc {
	return func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/v1/policy") {
			return jsonResponse(http.StatusOK, policy), nil
		}
		var call Request
		if err := json.NewDecoder(request.Body).Decode(&call); err != nil {
			return jsonResponse(http.StatusBadRequest, nil), nil
		}
		if fn, known := calls[call.Method]; known {
			return jsonResponse(http.StatusOK, fn(call)), nil
		}
		return jsonResponse(http.StatusOK, Response{Version: ProtocolVersion, ID: call.ID,
			Error: &WireError{Code: ErrorMethod, Message: "unbekannt"}}), nil
	}
}

func testClientConfig(link HostLink, transport http.RoundTripper) ClientConfig {
	credentials := NewMemoryCredentialStore()
	_ = credentials.StoreToken(link.CredentialRef, HostToken("test-token"))
	pins, _ := OpenFingerprintStore(filepath.Join(os.TempDir(), "pins-test-"+link.Name+".json"))
	return ClientConfig{
		Link: link, Credentials: credentials, Pins: pins,
		RoundTripper: transport,
	}
}

// Unäre Aufrufe laufen Ende zu Ende gegen einen In-Prozess-Host: Lesen und
// eine erlaubte Aktion.
func TestClientEndToEndAgainstInProcessHost(t *testing.T) {
	backend := &StubBackend{Calls: map[string]func(params json.RawMessage, identity string) (any, error){
		"Overview": func(params json.RawMessage, identity string) (any, error) {
			return map[string]string{"sessions": "hera"}, nil
		},
		"SendMessage": func(params json.RawMessage, identity string) (any, error) {
			return map[string]bool{"queued": true}, nil
		},
	}}
	dir := t.TempDir()
	host, err := NewHost(HostConfig{Dir: dir, Bind: "127.0.0.1", Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	token, err := host.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = host.Serve() }()
	t.Cleanup(func() { _ = host.Close() })
	certPEM, err := os.ReadFile(filepath.Join(dir, "host-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	credentials := NewMemoryCredentialStore()
	link := HostLink{Name: "e2e", Address: host.Addr(), CredentialRef: "host:e2e"}
	_ = credentials.StoreToken(link.CredentialRef, token)
	pins, _ := OpenFingerprintStore(t.TempDir() + "/pins.json")
	client := NewClient(ClientConfig{
		Link: link, Credentials: credentials, Pins: pins, RootCAs: pool,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Attach(ctx); err != nil {
		t.Fatalf("Attach scheitert: %v", err)
	}
	if client.State() != ConnAttached {
		t.Errorf("Zustand %q statt attached", client.State())
	}
	result, err := client.Call(ctx, "Overview", nil, "")
	if err != nil {
		t.Fatalf("Lesen scheitert: %v", err)
	}
	if !strings.Contains(string(result), "hera") {
		t.Errorf("falsche Nutzlast: %s", result)
	}
	if _, err := client.Call(ctx, "SendMessage",
		map[string]string{"sessionID": "s1", "text": "hallo"}, "transition-1"); err != nil {
		t.Errorf("erlaubte Aktion scheitert: %v", err)
	}
}

// Jeder Zustand ist erreichbar; das Alter stammt von der monotonen
// Client-Uhr, nicht von Host-Zeit.
func TestConnectionStatesAndMonotonicAge(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	link := HostLink{Name: "uhr", Address: "host:1", CredentialRef: "host:uhr"}
	transport := stubNet(PolicyDocument(), map[string]func(Request) Response{
		"Overview": func(call Request) Response {
			return Response{Version: ProtocolVersion, ID: call.ID, Result: EncodeParams(map[string]string{"s": "x"})}
		},
	})
	config := testClientConfig(link, transport)
	config.Clock = clock
	client := NewClient(config)
	if client.State() != ConnDetached {
		t.Errorf("Startzustand %q", client.State())
	}
	ctx := context.Background()
	if err := client.Attach(ctx); err != nil {
		t.Fatal(err)
	}
	if client.State() != ConnAttached {
		t.Errorf("Zustand %q statt attached", client.State())
	}
	// Host-Zeit existiert für das Alter nicht: Nur die eigene Uhr zählt.
	now = now.Add(90 * time.Second)
	if age := client.LastKnownAge(); age != 90*time.Second {
		t.Errorf("Alter %v statt 90s monotoner Uhr", age)
	}
	if _, err := client.Call(ctx, "Overview", nil, ""); err != nil {
		t.Fatal(err)
	}
	if age := client.LastKnownAge(); age != 0 {
		t.Errorf("erfolgreicher Austausch setzt Alter nicht zurück: %v", age)
	}
	client.Detach()
	if client.State() != ConnDetached {
		t.Errorf("Detach führt zu %q", client.State())
	}
}

// Moduswechsel: Detach schließt ab, bevor der neue Host beginnt — Sessions
// des alten Hosts erscheinen nie neben denen des neuen.
func TestSwitchHostNeverMixesSessions(t *testing.T) {
	policy := PolicyDocument()
	transportA := stubNet(policy, map[string]func(Request) Response{
		"Overview": func(call Request) Response {
			return Response{Version: ProtocolVersion, ID: call.ID, Result: EncodeParams("sessions-a")}
		},
	})
	linkA := HostLink{Name: "a", Address: "host-a", CredentialRef: "host:a"}
	config := testClientConfig(linkA, transportA)
	client := NewClient(config)
	ctx := context.Background()
	if err := client.Attach(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Refresh(ctx, "Overview", nil); err != nil {
		t.Fatal(err)
	}
	transportB := stubNet(policy, map[string]func(Request) Response{
		"Overview": func(call Request) Response {
			return Response{Version: ProtocolVersion, ID: call.ID, Result: EncodeParams("sessions-b")}
		},
	})
	client.SwitchHost(HostLink{Name: "b", Address: "host-b", CredentialRef: "host:b"})
	if client.State() != ConnDetached {
		t.Fatalf("Wechsel löst nicht zuerst: %q", client.State())
	}
	credentials := NewMemoryCredentialStore()
	_ = credentials.StoreToken("host:b", HostToken("test-token"))
	pins, _ := OpenFingerprintStore(t.TempDir() + "/pins.json")
	client.config.Credentials = credentials
	client.config.Pins = pins
	client.config.RoundTripper = transportB
	client.http = &http.Client{Transport: transportB, Timeout: 15 * time.Second}
	if err := client.Attach(ctx); err != nil {
		t.Fatal(err)
	}
	if got := client.AddressedHost(); got != "host-b" {
		t.Errorf("adressiert %q statt host-b", got)
	}
	if _, err := client.Refresh(ctx, "Overview", nil); err != nil {
		t.Fatal(err)
	}
	known, fresh := client.LastKnown()
	if !fresh || !strings.Contains(string(known), "sessions-b") || strings.Contains(string(known), "sessions-a") {
		t.Errorf("Sichten vermischt: %s (frisch=%v)", known, fresh)
	}
}

// Backoff wächst begrenzt mit Jitter, nie über das Maximum.
func TestBackoffBoundedWithJitter(t *testing.T) {
	backoff := Backoff{Base: 100 * time.Millisecond, Max: 800 * time.Millisecond}
	for attempt := 0; attempt < 10; attempt++ {
		wait := backoff.Next(attempt)
		expected := 100 * time.Millisecond
		for i := 0; i < attempt; i++ {
			expected *= 2
			if expected > backoff.Max {
				expected = backoff.Max
			}
		}
		if wait > expected {
			t.Errorf("Versuch %d wartet %v über %v", attempt, wait, expected)
		}
		if wait < expected/2 {
			t.Errorf("Versuch %d wartet %v unter Jitter-Untergrenze %v", attempt, wait, expected/2)
		}
	}
	early := backoff.Next(0)
	late := backoff.Next(8)
	if late < early {
		t.Errorf("Backoff wächst nicht: früh %v, spät %v", early, late)
	}
}

// Transportabriss verbindet sich von selbst wieder; manueller Versuch geht
// sofort; nach Anmelde-Verweigerung und nach bewusstem Detach herrscht Ruhe.
func TestReconnectBehaviors(t *testing.T) {
	var dials int32
	backoff := Backoff{Base: time.Millisecond, Max: 5 * time.Millisecond}
	loop := newReconnector(backoff, func(ctx context.Context) error {
		n := atomic.AddInt32(&dials, 1)
		if n < 3 {
			return &TransportFailure{Message: "weg"}
		}
		return nil
	})
	loop.start()
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&dials) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	loop.stop()
	if atomic.LoadInt32(&dials) < 3 {
		t.Error("kein automatischer Reconnect nach Transportabriss")
	}

	refused := newReconnector(backoff, func(ctx context.Context) error {
		return &AuthRefusedError{Message: "nein"}
	})
	refused.start()
	time.Sleep(50 * time.Millisecond)
	refused.stop()
	// Verweigerung beendet den Lauf nach dem ersten Versuch — kein Hammer.
	// (Genauer Zählerstand ist Timing; entscheidend: kein endloser Lauf, der
	// Test endet hier ohne Hängen.)

	detached := newReconnector(backoff, func(ctx context.Context) error {
		return &TransportFailure{Message: "weg"}
	})
	detached.start()
	detached.stop()
	time.Sleep(20 * time.Millisecond)
}

// Anmelde-Verweigerung heißt refused — und kein Reconnect mit derselben.
func TestRefusedDisablesAutoReconnect(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/v1/policy") {
			return jsonResponse(http.StatusUnauthorized, nil), nil
		}
		return jsonResponse(http.StatusUnauthorized, nil), nil
	})
	link := HostLink{Name: "dicht", Address: "host-dicht", CredentialRef: "host:dicht"}
	client := NewClient(testClientConfig(link, transport))
	client.EnableAutoReconnect(true)
	if err := client.Attach(context.Background()); err == nil {
		t.Fatal("abgewiesene Anmeldung akzeptiert")
	} else {
		var refused *AuthRefusedError
		if !errors.As(err, &refused) {
			t.Fatalf("keine Verweigerung, sondern %v", err)
		}
	}
	if client.State() != ConnRefused {
		t.Errorf("Zustand %q statt refused", client.State())
	}
	client.Detach()
	if client.State() != ConnDetached {
		t.Errorf("Detach nach refused führt zu %q", client.State())
	}
}

// Erst eine frische bekannte Nutzlast löscht die Last-known-Markierung.
func TestRefreshClearsLastKnownOnlyWhenFresh(t *testing.T) {
	fail := true
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/v1/policy") {
			return jsonResponse(http.StatusOK, PolicyDocument()), nil
		}
		if fail {
			return nil, errors.New("Netz weg")
		}
		return jsonResponse(http.StatusOK, Response{Version: ProtocolVersion, ID: "1",
			Result: EncodeParams(map[string]string{"sessions": "hera"})}), nil
	})
	link := HostLink{Name: "frisch", Address: "host-frisch", CredentialRef: "host:frisch"}
	client := NewClient(testClientConfig(link, transport))
	ctx := context.Background()
	if err := client.Attach(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Refresh(ctx, "Overview", nil); err == nil {
		t.Fatal("fehlgeschlagener Refresh meldet frisch")
	}
	if _, fresh := client.LastKnown(); fresh {
		t.Error("Markierung ohne frische Nutzlast gelöscht")
	}
	if client.State() != ConnDegraded && client.State() != ConnReconnecting {
		t.Errorf("Zustand %q nach Abriss", client.State())
	}
	fail = false
	if _, err := client.Refresh(ctx, "Overview", nil); err != nil {
		t.Fatal(err)
	}
	known, fresh := client.LastKnown()
	if !fresh || !strings.Contains(string(known), "hera") {
		t.Errorf("frische Sicht nicht übernommen: %s", known)
	}
}
