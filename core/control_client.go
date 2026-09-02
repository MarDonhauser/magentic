package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// ControlClient talks to the local socket. It is the only path the CLI has: a
// verb that cannot reach the serving process reports that, rather than mutating
// state behind the back of the process other agents are subscribed to.
type ControlClient struct {
	path    string
	timeout time.Duration
}

func NewControlClient(path string) *ControlClient {
	if path == "" {
		path = ControlSocketPath()
	}
	return &ControlClient{path: path, timeout: controlDialTimeout}
}

// Path names the socket the client expects to find.
func (c *ControlClient) Path() string { return c.path }

// controlUnavailable is the distinct outcome for a socket nobody serves. No
// Magentic process is started implicitly: a daemon whose lifetime nobody chose
// is worse than a clear error.
func (c *ControlClient) unavailable(id string, err error) ControlResponse {
	return controlFailure(id, ControlUnavailable, fmt.Sprintf(
		"Unter %s bedient kein Magentic die Steuer-API (%v). Starte die TUI, die Desktop-App oder »magentic serve«.",
		c.path, err))
}

// Call sends one request and reads its response.
func (c *ControlClient) Call(ctx context.Context, request ControlRequest) ControlResponse {
	connection, err := c.dial(ctx)
	if err != nil {
		return c.unavailable(request.ID, err)
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	if err := writeControlRequest(connection, request); err != nil {
		return c.unavailable(request.ID, err)
	}
	response, err := readControlResponse(reader)
	if err != nil {
		return c.unavailable(request.ID, err)
	}
	return response
}

// Watch subscribes to the event stream and calls consume for every event until
// it returns false, the context ends, or the stream stops.
func (c *ControlClient) Watch(ctx context.Context, request ControlRequest, consume func(ControlEvent) bool) ControlResponse {
	connection, err := c.dial(ctx)
	if err != nil {
		return c.unavailable(request.ID, err)
	}
	defer connection.Close()
	go func() {
		<-ctx.Done()
		connection.Close()
	}()
	request.Verb = ControlSessionWatch
	if err := writeControlRequest(connection, request); err != nil {
		return c.unavailable(request.ID, err)
	}
	reader := bufio.NewReader(connection)
	acknowledged, err := readControlResponse(reader)
	if err != nil {
		return c.unavailable(request.ID, err)
	}
	if acknowledged.Outcome != ControlOK {
		return acknowledged
	}
	for {
		line, err := readControlLine(reader)
		if err != nil {
			return ControlResponse{ID: request.ID, Outcome: ControlOK}
		}
		if len(line) == 0 {
			continue
		}
		var message ControlEventMessage
		if err := json.Unmarshal(line, &message); err == nil && message.Event.SessionID != "" {
			if !consume(message.Event) {
				return ControlResponse{ID: request.ID, Outcome: ControlOK}
			}
			continue
		}
		// Anything that is not an event ends the subscription, such as the
		// explicit outcome a dropped subscriber receives.
		var response ControlResponse
		if err := json.Unmarshal(line, &response); err != nil {
			return controlFailure(request.ID, ControlInvalidRequest, "Der Ereignisstrom lieferte ein unlesbares Dokument.")
		}
		return response
	}
}

func (c *ControlClient) dial(ctx context.Context) (net.Conn, error) {
	dialer := net.Dialer{Timeout: c.timeout}
	return dialer.DialContext(ctx, "unix", c.path)
}

func writeControlRequest(writer net.Conn, request ControlRequest) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(encoded, '\n'))
	return err
}

func readControlResponse(reader *bufio.Reader) (ControlResponse, error) {
	line, err := readControlLine(reader)
	if err != nil {
		return ControlResponse{}, err
	}
	var response ControlResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return ControlResponse{}, err
	}
	return response, nil
}
