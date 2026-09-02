package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"magentic/core"
)

// controlCLITestSocket serves a socket that answers with a fixed response, so
// the CLI is exercised as what it is: a client with no other path.
func controlCLITestSocket(t *testing.T, answer func(core.ControlRequest) core.ControlResponse) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "mgtcli")
	if err != nil {
		t.Fatalf("Verzeichnis nicht anlegbar: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "c.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Socket nicht bedienbar: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				reader := bufio.NewReader(connection)
				for {
					line, err := reader.ReadBytes('\n')
					if err != nil {
						return
					}
					request, err := core.DecodeControlRequest(bytes.TrimSpace(line))
					if err != nil {
						return
					}
					encoded, err := json.Marshal(answer(request))
					if err != nil {
						return
					}
					if _, err := connection.Write(append(encoded, '\n')); err != nil {
						return
					}
				}
			}()
		}
	}()
	return path
}

func runControlCLI(t *testing.T, socket string, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runControlSession(controlCLI{
		stdout: &stdout, stderr: &stderr,
		client: core.NewControlClient(socket),
	}, args)
	return stdout.String(), stderr.String(), code
}

func TestControlCLIReachesOnlyTheSocket(t *testing.T) {
	var seen core.ControlRequest
	socket := controlCLITestSocket(t, func(request core.ControlRequest) core.ControlResponse {
		seen = request
		return core.ControlResponse{ID: request.ID, Outcome: core.ControlOK,
			Result: &core.ControlResult{SessionID: "session-1"}}
	})
	// No Registry, no tmux and no Git may be reachable on the command's own
	// path: the state file must not appear and the PATH is empty.
	directory := t.TempDir()
	t.Setenv("MAGENTIC_STATE", filepath.Join(directory, "state.json"))
	t.Setenv("MAGENTIC_LIFECYCLE", filepath.Join(directory, "lifecycle.json"))
	t.Setenv("PATH", "")

	stdout, stderr, code := runControlCLI(t, socket, "kill", "--session", "session-1", "--json")
	if code != controlExitOK {
		t.Fatalf("Exit-Code = %d (%s)", code, stderr)
	}
	if seen.Verb != core.ControlSessionKill || seen.Args.Session != "session-1" {
		t.Fatalf("Anfrage = %+v", seen)
	}
	if !strings.Contains(stdout, "session-1") {
		t.Fatalf("Ausgabe = %q", stdout)
	}
	for _, name := range []string{"state.json", "lifecycle.json"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
			t.Fatalf("die CLI hat %s selbst angefasst", name)
		}
	}
}

func TestControlCLIMachineReadableOutput(t *testing.T) {
	socket := controlCLITestSocket(t, func(request core.ControlRequest) core.ControlResponse {
		if request.Args.Session == "fehlt" {
			return core.ControlResponse{ID: request.ID, Outcome: core.ControlNotFound,
				Message: "Session \"fehlt\" ist nicht registriert."}
		}
		return core.ControlResponse{ID: request.ID, Outcome: core.ControlOK,
			Result: &core.ControlResult{SessionID: "session-1", Content: "hallo"}}
	})

	stdout, stderr, code := runControlCLI(t, socket, "output", "--session", "session-1", "--json")
	if code != controlExitOK {
		t.Fatalf("Exit-Code = %d (%s)", code, stderr)
	}
	documents := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(documents) != 1 {
		t.Fatalf("Standardausgabe = %d Zeilen, want genau ein Dokument", len(documents))
	}
	var response core.ControlResponse
	if err := json.Unmarshal([]byte(documents[0]), &response); err != nil {
		t.Fatalf("Dokument nicht parsebar: %v (%s)", err, documents[0])
	}
	if response.Outcome != core.ControlOK || response.Result.SessionID != "session-1" {
		t.Fatalf("Dokument = %+v", response)
	}

	stdout, _, code = runControlCLI(t, socket, "output", "--session", "fehlt", "--json")
	if code == controlExitOK {
		t.Fatal("eine gescheiterte Anfrage endete mit Exit-Code 0")
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &response); err != nil {
		t.Fatalf("Fehlerdokument nicht parsebar: %v (%s)", err, stdout)
	}
	if response.Outcome != core.ControlNotFound || response.Message == "" {
		t.Fatalf("Fehlerdokument = %+v", response)
	}
}

func TestControlCLIKeepsDiagnosticsOffStandardOutput(t *testing.T) {
	socket := controlCLITestSocket(t, func(request core.ControlRequest) core.ControlResponse {
		return core.ControlResponse{ID: request.ID, Outcome: core.ControlOK,
			Message: "Ein Hinweis, der die Standardausgabe nicht verunreinigen darf.",
			Result:  &core.ControlResult{SessionID: "session-1"}}
	})
	stdout, stderr, _ := runControlCLI(t, socket, "list", "--json")
	if strings.Contains(stdout, "Hinweis") {
		t.Fatalf("Diagnose landete auf der Standardausgabe: %q", stdout)
	}
	var response core.ControlResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &response); err != nil {
		t.Fatalf("Dokument nicht parsebar: %v", err)
	}
	if !strings.Contains(stderr, "Hinweis") {
		t.Fatalf("Diagnose fehlt auf der Fehlerausgabe: %q", stderr)
	}
}

func TestControlCLIExitCodes(t *testing.T) {
	tests := []struct {
		name    string
		outcome core.ControlOutcome
		want    int
	}{
		{"Erfolg", core.ControlOK, controlExitOK},
		{"fertig gewartet", core.ControlWaitDone, controlExitOK},
		{"abgelehnt", core.ControlRefused, controlExitRefused},
		{"gescheitert", core.ControlFailed, controlExitRefused},
		{"Zeitgrenze", core.ControlWaitTimeout, controlExitRefused},
		{"nicht gefunden", core.ControlNotFound, controlExitAddressing},
		{"mehrdeutig", core.ControlAmbiguous, controlExitAddressing},
		{"Containment", core.ControlContainment, controlExitAddressing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome := test.outcome
			socket := controlCLITestSocket(t, func(request core.ControlRequest) core.ControlResponse {
				return core.ControlResponse{ID: request.ID, Outcome: outcome}
			})
			if _, _, code := runControlCLI(t, socket, "list"); code != test.want {
				t.Fatalf("Exit-Code = %d, want %d", code, test.want)
			}
		})
	}
	socket := controlCLITestSocket(t, func(request core.ControlRequest) core.ControlResponse {
		return core.ControlResponse{Outcome: core.ControlOK}
	})
	if _, stderr, code := runControlCLI(t, socket, "restart"); code != controlExitAddressing {
		t.Fatalf("unbekanntes Verb = %d (%s)", code, stderr)
	}
}

func TestControlCLIReportsUnavailableSocket(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "mgtcli")
	if err != nil {
		t.Fatalf("Verzeichnis nicht anlegbar: %v", err)
	}
	defer os.RemoveAll(directory)
	socket := filepath.Join(directory, "c.sock")

	stdout, stderr, code := runControlCLI(t, socket, "list", "--json")
	if code != controlExitRefused {
		t.Fatalf("Exit-Code = %d", code)
	}
	var response core.ControlResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &response); err != nil {
		t.Fatalf("Dokument nicht parsebar: %v (%s%s)", err, stdout, stderr)
	}
	if response.Outcome != core.ControlUnavailable {
		t.Fatalf("Ergebnis = %q, want %q", response.Outcome, core.ControlUnavailable)
	}
	if !strings.Contains(response.Message, socket) {
		t.Fatalf("Die Begründung nennt die erwartete Sitzungs-Adresse nicht: %q", response.Message)
	}
	// Nothing was started implicitly: the socket still does not exist.
	if _, err := os.Stat(socket); err == nil {
		t.Fatal("die CLI hat selbst einen Magentic-Prozess gestartet")
	}
	if entries, _ := os.ReadDir(directory); len(entries) != 0 {
		t.Fatalf("die CLI hat Dateien angelegt: %+v", entries)
	}
}

func TestControlSessionHelpListsEveryVerb(t *testing.T) {
	help := controlSessionHelp()
	for _, verb := range core.ControlVerbs() {
		name := strings.TrimPrefix(string(verb), "session.")
		if !strings.Contains(help, "magentic session "+name) {
			t.Fatalf("Die Hilfe nennt %q nicht:\n%s", name, help)
		}
	}
}
