package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ControlSocketPath is where the control API is served. It follows the user's
// runtime directory and falls back to the state directory, so a client finds it
// without a configuration file.
func ControlSocketPath() string {
	if path := os.Getenv("MAGENTIC_SOCKET"); path != "" {
		return path
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "magentic", "control.sock")
	}
	return filepath.Join(filepath.Dir(StatePath()), "control.sock")
}

// ErrControlServedElsewhere reports a live socket owned by another Magentic
// process. A second process never takes it over.
var ErrControlServedElsewhere = errors.New("die Steuer-API wird bereits von einem anderen Magentic-Prozess bedient")

// controlSocketMode is owner-only by construction: the socket is the
// authorization boundary, so nobody but the owning user may even connect.
const controlSocketMode = 0o600

// controlDialTimeout bounds the probe that tells a live socket from a stale one.
const controlDialTimeout = 250 * time.Millisecond

// ControlServer serves the control vocabulary on a Unix-domain socket. It opens
// no TCP and no otherwise network-reachable listener.
type ControlServer struct {
	service  *ControlService
	listener *net.UnixListener
	path     string
	uid      int
	// peerUID reads the connecting process's user id. It is a Seam only so the
	// refusal path is testable; production always reads real credentials.
	peerUID     func(*net.UnixConn) (int, error)
	connections sync.WaitGroup
	closeOnce   sync.Once
}

// ServeControl claims the socket and starts accepting. A stale socket left by a
// dead process is reclaimed; a live one is left alone.
func ServeControl(service *ControlService, path string) (*ControlServer, error) {
	if service == nil {
		return nil, errors.New("die Steuer-API braucht einen Dienst")
	}
	if path == "" {
		path = ControlSocketPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		probe, dialErr := net.DialTimeout("unix", path, controlDialTimeout)
		if dialErr == nil {
			probe.Close()
			return nil, ErrControlServedElsewhere
		}
		if err := os.Remove(path); err != nil {
			return nil, err
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
	if err := os.Chmod(path, controlSocketMode); err != nil {
		listener.Close()
		return nil, err
	}
	server := &ControlServer{
		service: service, listener: listener, path: path,
		uid: os.Getuid(), peerUID: controlPeerUID,
	}
	go server.accept()
	return server, nil
}

// Path is the socket a client is expected to find.
func (s *ControlServer) Path() string { return s.path }

// Network names the transport, so a caller can assert the API is local-only.
func (s *ControlServer) Network() string { return s.listener.Addr().Network() }

// Close stops accepting and waits for the connections in flight.
func (s *ControlServer) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.listener.Close()
		s.connections.Wait()
	})
	return err
}

func (s *ControlServer) accept() {
	for {
		connection, err := s.listener.AcceptUnix()
		if err != nil {
			return
		}
		s.connections.Add(1)
		go func() {
			defer s.connections.Done()
			defer connection.Close()
			s.serve(connection)
		}()
	}
}

// serve authorizes the peer and then answers one request per line. A malformed
// or unknown request is answered rather than closing the connection.
func (s *ControlServer) serve(connection *net.UnixConn) {
	writer := bufio.NewWriter(connection)
	uid, err := s.peerUID(connection)
	if err != nil || uid != s.uid {
		message := fmt.Sprintf("Die Steuer-API bedient nur den Benutzer %d.", s.uid)
		if err != nil {
			message = fmt.Sprintf("Die Anmeldedaten der Gegenstelle sind nicht prüfbar: %v", err)
		}
		writeControlResponse(writer, controlFailure("", ControlUnauthorized, message))
		return
	}
	reader := bufio.NewReader(connection)
	for {
		line, err := readControlLine(reader)
		if err != nil {
			return
		}
		if len(line) == 0 {
			continue
		}
		request, decodeErr := DecodeControlRequest(line)
		if decodeErr != nil {
			if !writeControlResponse(writer, controlFailure("", ControlInvalidRequest, decodeErr.Error())) {
				return
			}
			continue
		}
		if request.Verb == ControlSessionWatch {
			s.watch(reader, writer, request)
			return
		}
		response := s.service.Dispatch(context.Background(), request)
		if !writeControlResponse(writer, response) {
			return
		}
	}
}

// watch turns the connection into an event stream. It ends when the client
// disconnects, sends any further line as an unsubscribe, or is dropped for not
// reading its events.
func (s *ControlServer) watch(reader *bufio.Reader, writer *bufio.Writer, request ControlRequest) {
	filter := ControlEventFilter{
		ProjectID: ProjectID(request.Args.Project),
		SessionID: SessionID(request.Args.Session),
	}
	events := s.service.Events()
	subscription := events.Subscribe(filter)
	defer events.Release(subscription)

	if !writeControlResponse(writer, ControlResponse{ID: request.ID, Outcome: ControlOK}) {
		return
	}
	unsubscribed := make(chan struct{})
	go func() {
		defer close(unsubscribed)
		// Any further line, and a closed connection, ends the subscription.
		reader.ReadBytes('\n')
	}()
	for {
		select {
		case <-unsubscribed:
			return
		case event, open := <-subscription.Events():
			if !open {
				if subscription.Stalled() {
					writeControlResponse(writer, controlFailure(request.ID, ControlStalled,
						"Der Ereignisstrom wurde nicht gelesen und die Anmeldung deshalb beendet."))
				}
				return
			}
			encoded, err := json.Marshal(ControlEventMessage{ID: request.ID, Event: event})
			if err != nil {
				return
			}
			if _, err := writer.Write(append(encoded, '\n')); err != nil || writer.Flush() != nil {
				return
			}
		}
	}
}

// controlLineLimit bounds one request document. A client that sends more than
// this is not speaking the protocol.
const controlLineLimit = 1 << 20

func readControlLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if len(line) > controlLineLimit {
		return nil, errors.New("Steuer-Anfrage ist zu lang")
	}
	if err != nil && len(line) == 0 {
		return nil, err
	}
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line, nil
}

func writeControlResponse(writer *bufio.Writer, response ControlResponse) bool {
	encoded, err := json.Marshal(response)
	if err != nil {
		return false
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		return false
	}
	return writer.Flush() == nil
}
