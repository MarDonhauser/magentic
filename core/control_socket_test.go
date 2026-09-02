package core

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// controlSocketDir keeps the socket path inside the platform's address length
// limit, which the deeply nested per-test temporary directory exceeds.
func controlSocketDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "mgt")
	if err != nil {
		t.Fatalf("Verzeichnis nicht anlegbar: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	return directory
}

func controlSocketServer(t *testing.T) *ControlServer {
	t.Helper()
	service, _, _ := controlTestService(controlDispatchState())
	path := filepath.Join(controlSocketDir(t), "c.sock")
	server, err := ServeControl(service, path)
	if err != nil {
		t.Fatalf("Socket nicht bedienbar: %v", err)
	}
	t.Cleanup(func() { server.Close() })
	return server
}

func controlDial(t *testing.T, server *ControlServer) (*bufio.Reader, net.Conn) {
	t.Helper()
	connection, err := net.DialTimeout("unix", server.Path(), time.Second)
	if err != nil {
		t.Fatalf("Verbindung fehlgeschlagen: %v", err)
	}
	t.Cleanup(func() { connection.Close() })
	return bufio.NewReader(connection), connection
}

func controlAsk(t *testing.T, reader *bufio.Reader, connection net.Conn, line string) ControlResponse {
	t.Helper()
	if _, err := connection.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("Anfrage nicht sendbar: %v", err)
	}
	connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	raw, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("Antwort nicht lesbar: %v", err)
	}
	var response ControlResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("Antwort ist kein JSON-Dokument: %v (%s)", err, raw)
	}
	return response
}

func TestServeControlCreatesOwnerOnlySocket(t *testing.T) {
	server := controlSocketServer(t)
	info, err := os.Stat(server.Path())
	if err != nil {
		t.Fatalf("Socket fehlt: %v", err)
	}
	if info.Mode().Perm() != controlSocketMode {
		t.Fatalf("Rechte = %04o, want %04o", info.Mode().Perm(), controlSocketMode)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("Der Pfad ist kein Socket: %v", info.Mode())
	}
}

func TestServeControlReclaimsStaleSocketAndKeepsLiveOne(t *testing.T) {
	path := filepath.Join(controlSocketDir(t), "c.sock")
	// A socket file left by a process that no longer exists is reclaimed.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("veralteten Socket nicht anlegbar: %v", err)
	}
	service, _, _ := controlTestService(controlDispatchState())
	server, err := ServeControl(service, path)
	if err != nil {
		t.Fatalf("veralteter Socket wurde nicht übernommen: %v", err)
	}
	defer server.Close()

	second, err := ServeControl(service, path)
	if err == nil {
		second.Close()
		t.Fatal("ein zweiter Prozess hat den bedienten Socket übernommen")
	}
	if err != ErrControlServedElsewhere {
		t.Fatalf("Begründung = %v, want %v", err, ErrControlServedElsewhere)
	}
}

func TestServeControlOpensNoNetworkListener(t *testing.T) {
	server := controlSocketServer(t)
	if server.Network() != "unix" {
		t.Fatalf("Transport = %q, want %q", server.Network(), "unix")
	}
	address := server.listener.Addr()
	if _, ok := address.(*net.UnixAddr); !ok {
		t.Fatalf("Adresse = %T, want *net.UnixAddr", address)
	}
	// The address is a filesystem path, not a host and port.
	if _, err := os.Stat(address.String()); err != nil {
		t.Fatalf("Die Adresse ist kein Dateipfad: %v", err)
	}
	if strings.Contains(address.String(), ":") {
		t.Fatalf("Die Adresse sieht nach einem Port aus: %q", address)
	}
}

func TestControlSocketRefusesForeignCredentials(t *testing.T) {
	service, _, _ := controlTestService(controlDispatchState())
	path := filepath.Join(controlSocketDir(t), "c.sock")
	server, err := ServeControl(service, path)
	if err != nil {
		t.Fatalf("Socket nicht bedienbar: %v", err)
	}
	defer server.Close()
	server.peerUID = func(*net.UnixConn) (int, error) { return os.Getuid() + 1, nil }

	reader, connection := controlDial(t, server)
	connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	raw, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("Ablehnung nicht lesbar: %v", err)
	}
	var response ControlResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("Ablehnung ist kein JSON-Dokument: %v", err)
	}
	if response.Outcome != ControlUnauthorized {
		t.Fatalf("Ergebnis = %q, want %q", response.Outcome, ControlUnauthorized)
	}
	// The connection is closed without executing anything.
	if _, err := reader.ReadBytes('\n'); err == nil {
		t.Fatal("die Verbindung blieb nach der Ablehnung offen")
	}
}

func TestControlSocketRequestLoop(t *testing.T) {
	server := controlSocketServer(t)
	reader, connection := controlDial(t, server)

	response := controlAsk(t, reader, connection, "kein json")
	if response.Outcome != ControlInvalidRequest {
		t.Fatalf("fehlerhaftes Dokument = %q", response.Outcome)
	}
	response = controlAsk(t, reader, connection, `{"id":"a","verb":"session.restart"}`)
	if response.Outcome != ControlUnknownVerb || response.ID != "a" {
		t.Fatalf("unbekanntes Verb = %+v", response)
	}
	response = controlAsk(t, reader, connection, `{"id":"b","verb":"session.list"}`)
	if response.Outcome != ControlOK || response.ID != "b" {
		t.Fatalf("erste Anfrage = %+v", response)
	}
	response = controlAsk(t, reader, connection, `{"id":"c","verb":"session.list"}`)
	if response.Outcome != ControlOK || response.ID != "c" {
		t.Fatalf("zweite Anfrage auf derselben Verbindung = %+v", response)
	}
}
