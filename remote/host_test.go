package remote

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// testHost startet einen Host auf Loopback mit Stub-Backend. Der Client
// vertraut dem Host-Zertifikat aus Datei — kein SkipVerify, damit der Test
// denselben Pfad geht wie der echte Client.
func testHost(t *testing.T, backend HostBackend) (*Host, HostToken, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	host, err := NewHost(HostConfig{
		Dir:     dir,
		Bind:    "127.0.0.1",
		Backend: backend,
		Log:     func(format string, args ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := host.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = host.Serve() }()
	t.Cleanup(func() { _ = host.Close() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := tls.Dial("tcp", host.Addr(), &tls.Config{InsecureSkipVerify: true})
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Host auf %s antwortet nicht", host.Addr())
		}
		time.Sleep(10 * time.Millisecond)
	}
	certPEM, err := os.ReadFile(filepath.Join(dir, "host-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("Host-Zertifikat unlesbar")
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}
	return host, token, client
}

func callHost(t *testing.T, client *http.Client, addr string, token HostToken, request Request) (int, Response) {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest, err := http.NewRequest(http.MethodPost, "https://"+addr+"/v1/call", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+string(token))
	}
	httpResponse, err := client.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer httpResponse.Body.Close()
	var response Response
	if err := json.NewDecoder(httpResponse.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return httpResponse.StatusCode, response
}

// Ohne Opt-in hört niemand auf einer öffentlichen Schnittstelle, und der
// Host meldet, woran er hängt.
func TestHostBindsLoopbackByDefault(t *testing.T) {
	host, _, _ := testHost(t, &StubBackend{})
	if !strings.HasPrefix(host.Addr(), "127.0.0.1:") {
		t.Errorf("Host hängt an %q statt an Loopback", host.Addr())
	}
	if _, err := NewHost(HostConfig{
		Dir: t.TempDir(), Bind: "0.0.0.0", Port: 0, Backend: &StubBackend{},
	}); err == nil {
		t.Error("öffentliche Bindung ohne ausdrückliche Konfiguration akzeptiert")
	}
}

// Nur TLS: Ein Klartext-Versuch wird abgewiesen, nicht bedient. Go beantwortet
// ihn mit 400 statt TLS — entscheidend ist, dass keine API-Antwort kommt.
func TestHostRefusesPlaintext(t *testing.T) {
	host, _, _ := testHost(t, &StubBackend{})
	plain := &http.Client{Timeout: 5 * time.Second}
	response, err := plain.Post("http://"+host.Addr()+"/v1/call", "application/json", strings.NewReader(`{}`))
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("Klartext-Verbindung bedient")
	}
	var decoded Response
	if err := json.NewDecoder(response.Body).Decode(&decoded); err == nil && (decoded.Result != nil || decoded.Error != nil) {
		t.Fatalf("Klartext bekam API-Antwort: %+v", decoded)
	}
}

// Fehlende, unbekannte und widerrufene Anmeldedaten werden je als
// Auth-Fehlschlag abgewiesen — und kein Token-Wert landet im Log.
func TestHostRejectsBadTokens(t *testing.T) {
	var logs []string
	backend := &StubBackend{Calls: map[string]func(params json.RawMessage, identity string) (any, error){
		"Overview": func(params json.RawMessage, identity string) (any, error) {
			return map[string]string{"ok": "ja"}, nil
		},
	}}
	dir := t.TempDir()
	host, err := NewHost(HostConfig{
		Dir: dir, Bind: "127.0.0.1", Backend: backend,
		Log: func(format string, args ...any) { logs = append(logs, format) },
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := host.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = host.Serve() }()
	t.Cleanup(func() { _ = host.Close() })
	_, _, client := testHost(t, backend)
	_ = client

	certPEM, err := os.ReadFile(filepath.Join(dir, "host-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	tlsClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}

	call := Request{Version: ProtocolVersion, ID: "1", Method: "Overview"}
	if status, response := callHost(t, tlsClient, host.Addr(), "", call); status != http.StatusUnauthorized || response.Error == nil || response.Error.Code != ErrorAuth {
		t.Errorf("fehlender Token: Status %d, Antwort %+v", status, response)
	}
	unknown, _ := NewHostToken()
	if status, response := callHost(t, tlsClient, host.Addr(), unknown, call); status != http.StatusUnauthorized || response.Error.Code != ErrorAuth {
		t.Errorf("unbekannter Token: Status %d, Antwort %+v", status, response)
	}
	if err := host.Revoke(token); err != nil {
		t.Fatal(err)
	}
	if status, response := callHost(t, tlsClient, host.Addr(), token, call); status != http.StatusUnauthorized || response.Error.Code != ErrorAuth {
		t.Errorf("widerrufener Token: Status %d, Antwort %+v", status, response)
	}
	for _, line := range logs {
		if strings.Contains(line, string(token)) || strings.Contains(line, string(unknown)) {
			t.Errorf("Token-Wert im Log: %q", line)
		}
	}
}

// Der Host bedient Clients ohne jede lokale Oberfläche: Ein Leseaufruf über
// das Netz liefert, was das Backend hergibt.
func TestHostServesReadsWithoutLocalUI(t *testing.T) {
	backend := &StubBackend{Calls: map[string]func(params json.RawMessage, identity string) (any, error){
		"Overview": func(params json.RawMessage, identity string) (any, error) {
			return map[string]string{"sessions": "hera,atlas"}, nil
		},
	}}
	host, token, client := testHost(t, backend)
	status, response := callHost(t, client, host.Addr(), token,
		Request{Version: ProtocolVersion, ID: "1", Method: "Overview"})
	if status != http.StatusOK || response.Error != nil {
		t.Fatalf("Lesen scheitert: Status %d, Antwort %+v", status, response)
	}
	var result map[string]string
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result["sessions"] != "hera,atlas" {
		t.Errorf("falsche Nutzlast: %+v", result)
	}
}

// Widerruf mitten im Stream schließt den Stream; fremde Streams mit
// gültigem Token bleiben offen.
func TestRevokeClosesOpenStreams(t *testing.T) {
	frames := make(chan Frame, 4)
	backend := &StubBackend{}
	backend.Subscript = func(sessionID string, fromSeq uint64) (StreamSubscription, error) {
		return &openStubSubscription{frames: frames}, nil
	}
	host, token, client := testHost(t, backend)
	_ = client

	certPEM, err := os.ReadFile(filepath.Join(host.config.Dir, "host-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: pool}}
	conn, _, err := dialer.Dial("wss://"+host.Addr()+"/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(StreamHello{Version: ProtocolVersion, SessionID: "s1", Token: token}); err != nil {
		t.Fatal(err)
	}
	frames <- TermFrame(1, []byte("erste bytes"))
	var first Frame
	if err := conn.ReadJSON(&first); err != nil {
		t.Fatalf("Stream liefert nicht: %v", err)
	}
	if err := host.Revoke(token); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	closed := false
	for i := 0; i < 5; i++ {
		var frame Frame
		if err := conn.ReadJSON(&frame); err != nil {
			closed = true
			break
		}
		if frame.Kind == FrameControl && frame.Control == "closed" {
			closed = true
			break
		}
	}
	if !closed {
		t.Error("widerrufener Stream bleibt offen")
	}
	if status, response := callHost(t, client, host.Addr(), token,
		Request{Version: ProtocolVersion, ID: "2", Method: "Overview"}); status != http.StatusUnauthorized || response.Error.Code != ErrorAuth {
		t.Error("Folgeanfragen nach Widerruf nicht abgewiesen")
	}
}

type openStubSubscription struct {
	frames chan Frame
}

func (s *openStubSubscription) Frames() <-chan Frame { return s.frames }
func (s *openStubSubscription) Close()               {}
