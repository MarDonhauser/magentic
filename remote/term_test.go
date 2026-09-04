package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeChannel struct {
	mu     sync.Mutex
	frames []Frame
	notify chan struct{}
	closed bool
}

func newFakeChannel(frames ...Frame) *fakeChannel {
	return &fakeChannel{frames: frames, notify: make(chan struct{}, 1)}
}

func (c *fakeChannel) push(frame Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.frames = append(c.frames, frame)
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *fakeChannel) Receive() (Frame, error) {
	for {
		c.mu.Lock()
		if len(c.frames) > 0 {
			frame := c.frames[0]
			c.frames = c.frames[1:]
			c.mu.Unlock()
			return frame, nil
		}
		if c.closed {
			c.mu.Unlock()
			return Frame{}, errors.New("Kanal zu")
		}
		notify := c.notify
		c.mu.Unlock()
		<-notify
	}
}

func (c *fakeChannel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	select {
	case c.notify <- struct{}{}:
	default:
	}
	return nil
}

type fakeDialer struct {
	mu       sync.Mutex
	channels []*fakeChannel
	froms    []uint64
	next     func(sessionID string, fromSeq uint64) *fakeChannel
}

func (d *fakeDialer) DialStream(ctx context.Context, sessionID string, fromSeq uint64) (StreamChannel, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.froms = append(d.froms, fromSeq)
	channel := newFakeChannel()
	if d.next != nil {
		channel = d.next(sessionID, fromSeq)
	}
	d.channels = append(d.channels, channel)
	return channel, nil
}

type termFixture struct {
	client *Client
	dialer *fakeDialer
	writes [][]byte
}

func openTermFixture(t *testing.T) *termFixture {
	t.Helper()
	fixture := &termFixture{dialer: &fakeDialer{}}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/v1/policy") {
			return jsonResponse(http.StatusOK, PolicyDocument()), nil
		}
		var call Request
		if err := json.NewDecoder(request.Body).Decode(&call); err != nil {
			return jsonResponse(http.StatusBadRequest, nil), nil
		}
		switch call.Method {
		case "OpenTerm", "ResizeTerm", "CloseTerm":
			return jsonResponse(http.StatusOK, Response{Version: ProtocolVersion, ID: call.ID,
				Result: EncodeParams(map[string]bool{"ok": true})}), nil
		case "WriteTerm":
			var args struct {
				SessionID string `json:"sessionID"`
				DataB64   string `json:"dataB64"`
			}
			_ = json.Unmarshal(call.Params, &args)
			data, _ := base64.StdEncoding.DecodeString(args.DataB64)
			fixture.writes = append(fixture.writes, data)
			return jsonResponse(http.StatusOK, Response{Version: ProtocolVersion, ID: call.ID,
				Result: EncodeParams(map[string]bool{"ok": true})}), nil
		default:
			return jsonResponse(http.StatusOK, Response{Version: ProtocolVersion, ID: call.ID,
				Error: &WireError{Code: ErrorMethod, Message: "unbekannt"}}), nil
		}
	})
	link := HostLink{Name: "term", Address: "host-term", CredentialRef: "host:term"}
	fixture.client = NewClient(testClientConfig(link, transport))
	if err := fixture.client.Attach(context.Background()); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func waitForContent(t *testing.T, attachment *TermAttachment, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		content, _ := attachment.Content()
		if strings.Contains(string(content), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Inhalt %q fehlt in %q", want, content)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Tippen in die Remote-Session, Ausgabe beobachten: Open/Write/term:data
// laufen über den Kanal.
func TestRemoteTermTypeAndObserve(t *testing.T) {
	fixture := openTermFixture(t)
	ctx := context.Background()
	attachment, err := fixture.client.openTermWith(ctx, "s1", 220, 50, fixture.dialer)
	if err != nil {
		t.Fatal(err)
	}
	defer attachment.Close(ctx)
	channel := fixture.dialer.channels[0]
	channel.push(GapFrame(0, []byte("$ ")))
	channel.push(TermFrame(0, []byte("hallo remote")))
	waitForContent(t, attachment, "hallo remote")
	if err := attachment.Write(ctx, []byte("ls\n")); err != nil {
		t.Fatalf("Schreiben scheitert: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(fixture.writes) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(fixture.writes) != 1 || string(fixture.writes[0]) != "ls\n" {
		t.Errorf("Eingabe kam nicht an: %q", fixture.writes)
	}
}

// Kurzer Abriss im Fenster: Resume liefert Versäumtes, kein sichtbarer
// Bruch, keine Lücken-Markierung.
func TestTermResumeContinuesSeamlessly(t *testing.T) {
	fixture := openTermFixture(t)
	ctx := context.Background()
	attachment, err := fixture.client.openTermWith(ctx, "s1", 220, 50, fixture.dialer)
	if err != nil {
		t.Fatal(err)
	}
	defer attachment.Close(ctx)
	first := fixture.dialer.channels[0]
	first.push(GapFrame(0, []byte("a")))
	waitForContent(t, attachment, "a")
	first.push(TermFrame(1, []byte("b")))
	waitForContent(t, attachment, "ab")
	// Abriss: Kanal stirbt, zwei Bytes gehen „unterwegs" verloren.
	_ = first.Close()
	fixture.dialer.next = func(sessionID string, fromSeq uint64) *fakeChannel {
		if fromSeq != 2 {
			t.Errorf("Resume ab %d statt 2", fromSeq)
		}
		return newFakeChannel(TermFrame(2, []byte("cd")))
	}
	if err := attachment.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	waitForContent(t, attachment, "abcd")
	if content, missed := attachment.Content(); missed || string(content) != "abcd" {
		t.Errorf("Bruch sichtbar: %q missed=%v", content, missed)
	}
}

// Lücke, die der Host nicht bedienen kann: Inhalt wird ersetzt und als
// lückenhaft markiert — nie über die Lücke appendet.
func TestTermGapReplacesAndMarks(t *testing.T) {
	fixture := openTermFixture(t)
	ctx := context.Background()
	attachment, err := fixture.client.openTermWith(ctx, "s1", 220, 50, fixture.dialer)
	if err != nil {
		t.Fatal(err)
	}
	defer attachment.Close(ctx)
	first := fixture.dialer.channels[0]
	first.push(GapFrame(0, []byte("alt")))
	waitForContent(t, attachment, "alt")
	_ = first.Close()
	fixture.dialer.next = func(sessionID string, fromSeq uint64) *fakeChannel {
		return newFakeChannel(GapFrame(99, []byte("neu")))
	}
	if err := attachment.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	waitForContent(t, attachment, "neu")
	content, missed := attachment.Content()
	if string(content) != "neu" {
		t.Errorf("Lücke appendet statt ersetzt: %q", content)
	}
	if !missed {
		t.Error("versäumte Ausgabe nicht markiert")
	}
}

// Tippen im Getrennten: verweigert, nichts zwischengespeichert, bei
// Reconnect kommt nichts still an.
func TestTermInputDroppedWhileDisconnected(t *testing.T) {
	fixture := openTermFixture(t)
	ctx := context.Background()
	attachment, err := fixture.client.openTermWith(ctx, "s1", 220, 50, fixture.dialer)
	if err != nil {
		t.Fatal(err)
	}
	defer attachment.Close(ctx)
	fixture.dialer.channels[0].push(GapFrame(0, []byte("$ ")))
	waitForContent(t, attachment, "$ ")
	fixture.client.Detach()
	if err := attachment.Write(ctx, []byte("rm -rf /\n")); err == nil {
		t.Fatal("Eingabe im Getrennten angenommen")
	} else if _, blocked := err.(*ErrInputNotDelivered); !blocked {
		t.Fatalf("falscher Fehler: %T %v", err, err)
	}
	if len(fixture.writes) != 0 {
		t.Errorf("verweigerte Eingabe zwischengespeichert: %q", fixture.writes)
	}
}
