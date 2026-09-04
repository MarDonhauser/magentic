package remote

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"magentic/core"
)

// Host-Dienst und lokale App nebeneinander teilen sich die Registry über die
// bestehende ADR-0002-Koordination: Beide schreiben, beide lesen danach
// beide Fakten — kein Schreiber verliert.
func TestHostAndLocalAppCoordinateOnRegistry(t *testing.T) {
	tempState(t)
	backend := NewCoreBackend()
	ctx := context.Background()
	if _, err := backend.HandleCall(ctx, "AddDivider", EncodeParams(map[string]string{"name": "host-seite"}), "id-host-1"); err != nil {
		t.Fatal(err)
	}
	// Die „lokale App" schreibt direkt über denselben Registry-Pfad.
	if _, err := core.OpenRegistry(core.StatePath()).Change(ctx, core.AddDivider("lokal-1", "lokal-seite")); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.HandleCall(ctx, "AddDivider", EncodeParams(map[string]string{"name": "host-seite-2"}), "id-host-2"); err != nil {
		t.Fatal(err)
	}
	st, err := core.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	// Beide Schreiber sind sichtbar: je zwei Divider-Namen im State.
	encoded, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"host-seite", "host-seite-2", "lokal-seite"} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("Schreiber verlor %q", want)
		}
	}
}

// End-to-end über zwei Endpunkte: anbinden, beobachten, Terminal anhängen,
// Netz kappen (letzte bekannte Sicht + blockierte Destruktion), heilen
// (nahtloses Resume).
func TestTwoEndpointScenario(t *testing.T) {
	tempState(t)
	projectID := seedProject(t, "demo", t.TempDir())
	ctx := context.Background()

	backend := NewCoreBackend()
	stub := &stubTermSource{snapshot: []byte("pane-anfang")}
	backend.termSrc = stub
	backend.events.termOf = func(sessionID string) TermSource { return stub }

	// Host-seitige Geburt einer Session direkt in der Registry.
	session := core.Session{
		ID: core.SessionID("sess-e2e-1"), Name: "hera", ProjectID: projectID,
		RuntimeName: "mgt-hera", SessionKind: core.SessionKindCodingAgent,
	}
	if _, err := core.OpenRegistry(core.StatePath()).Change(ctx, core.RegisterSession(session)); err != nil {
		t.Fatal(err)
	}

	host, err := NewHost(HostConfig{Dir: t.TempDir(), Bind: "127.0.0.1", Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	token, err := host.IssueToken()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = host.Serve() }()
	t.Cleanup(func() { _ = host.Close() })
	certPEM, err := os.ReadFile(filepath.Join(host.config.Dir, "host-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	credentials := NewMemoryCredentialStore()
	link := HostLink{Name: "szenario", Address: host.Addr(), CredentialRef: "host:szenario"}
	_ = credentials.StoreToken(link.CredentialRef, token)
	pins, _ := OpenFingerprintStore(t.TempDir() + "/pins.json")
	client := NewClient(ClientConfig{Link: link, Credentials: credentials, Pins: pins, RootCAs: pool})
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Anbinden und beobachten.
	if err := client.Attach(callCtx); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	overview, err := client.Call(callCtx, "Overview", map[string]bool{"fresh": true}, "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if !strings.Contains(string(overview), "hera") {
		t.Errorf("Session nicht beobachtet: %s", overview)
	}

	// Terminal anhängen, Ausgabe fließt.
	attachment, err := client.openTermWith(callCtx, "sess-e2e-1", 220, 50, &websocketDialer{client: client})
	if err != nil {
		t.Fatalf("OpenTerm: %v", err)
	}
	defer attachment.Close(callCtx)
	// Erst publizieren, wenn der Host das Abo eingehängt hat.
	deadline := time.Now().Add(5 * time.Second)
	for backend.events.subscriberCount("sess-e2e-1") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Host hängt kein Terminal-Abo ein")
		}
		time.Sleep(10 * time.Millisecond)
	}
	backend.events.Publish("sess-e2e-1", []byte("agent schreibt"))
	deadline = time.Now().Add(5 * time.Second)
	for {
		content, _ := attachment.Content()
		if strings.Contains(string(content), "agent schreibt") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("keine Terminal-Ausgabe: %q", content)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Netz kappen: Transport versagt, letzte bekannte Sicht bleibt stehen.
	working := client.http.Transport
	client.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("Netz gekappt")
	}), Timeout: 5 * time.Second}
	if _, err := client.Refresh(callCtx, "Overview", map[string]bool{"fresh": true}); err == nil {
		t.Fatal("Kappung meldet frisch")
	}
	if client.State() != ConnDegraded && client.State() != ConnReconnecting {
		t.Errorf("Zustand nach Kappung: %q", client.State())
	}
	if err := RequireFreshFacts(core.ObservationSnapshot{Availability: core.ObservationUnavailable}); err == nil {
		t.Error("Destruktion trotz Kappung freigegeben")
	}
	if available, _ := ActionAvailable(client.Policy(), "KillSession", false); available {
		t.Error("Kill trotz Kappung angeboten")
	}

	// Heilen: frische Sicht, Aktionen wieder frei, Stream resumes nahtlos.
	client.http = &http.Client{Transport: working, Timeout: 15 * time.Second}
	if _, err := client.Refresh(callCtx, "Overview", map[string]bool{"fresh": true}); err != nil {
		t.Fatalf("Heilung: %v", err)
	}
	if _, fresh := client.LastKnown(); !fresh {
		t.Error("Sicht nach Heilung nicht frisch")
	}
	_ = attachment.channel.Close()
	backend.events.Publish("sess-e2e-1", []byte(" weiter"))
	if err := attachment.Resume(callCtx); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		content, missed := attachment.Content()
		if strings.Contains(string(content), "weiter") {
			if missed {
				t.Errorf("nahtloses Resume markiert Lücke: %q", content)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Resume liefert nicht: %q", content)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
