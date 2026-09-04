package remote

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"magentic/core"
)

// RingCapSegments und RingCapBytes begrenzen den behaltenen
// Terminal-Verlauf je Session (D5). Was herausfällt, ist ehrlich weg: Der
// Client bekommt eine Lücke plus frischen Snapshot statt Flickwerk.
const (
	RingCapSegments = 1024
	RingCapBytes    = 512 << 10
)

// ringSegment ist ein behaltenes Stück Terminal-Ausgabe mit seiner Position
// ab Stream-Ursprung.
type ringSegment struct {
	seq  uint64
	data []byte
}

// RingBuffer hält bounded Verlauf mit monotonen Sequenznummern. next ist die
// Sequenz des nächsten Bytes, oldest die des ältesten behaltenen.
type RingBuffer struct {
	mu       sync.Mutex
	segments []ringSegment
	bytes    int
	next     uint64
}

// Append hängt Bytes an und vergibt ihre Sequenz. Gibt die erste Sequenz des
// Stücks zurück.
func (r *RingBuffer) Append(data []byte) uint64 {
	if len(data) == 0 {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.next
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	first := r.next
	r.segments = append(r.segments, ringSegment{seq: first, data: append([]byte(nil), data...)})
	r.bytes += len(data)
	r.next += uint64(len(data))
	for len(r.segments) > RingCapSegments || r.bytes > RingCapBytes {
		r.bytes -= len(r.segments[0].data)
		r.segments = r.segments[1:]
	}
	return first
}

// Bounds nennt älteste und nächste Sequenz.
func (r *RingBuffer) Bounds() (oldest, next uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.segments) == 0 {
		return r.next, r.next
	}
	return r.segments[0].seq, r.next
}

// Replay liefert alles ab from (inklusive) im Fenster. ok=false heißt: Die
// Position ist herausgefallen oder liegt in der Zukunft — der Aufrufer
// schuldet eine Lücke plus Snapshot.
func (r *RingBuffer) Replay(from uint64) (frames []Frame, next uint64, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.segments) == 0 {
		return nil, r.next, from == r.next
	}
	oldest := r.segments[0].seq
	if from < oldest || from > r.next {
		return nil, r.next, false
	}
	for _, segment := range r.segments {
		end := segment.seq + uint64(len(segment.data))
		if end <= from {
			continue
		}
		start := from
		if start < segment.seq {
			start = segment.seq
		}
		frames = append(frames, TermFrame(start, segment.data[start-segment.seq:]))
	}
	return frames, r.next, true
}

// subscriberBudget deckelt, was ein langsamer Client dem Host schuldet.
const subscriberBudget = 64

type termSubscriber struct {
	mu      sync.Mutex
	frames  chan Frame
	gapped  bool
	gapSeq  uint64
	backend *CoreBackend
	session string
}

// eventLog ist Fan-out plus Verlauf je Session: publish blockiert nie den
// pty-Leser; wer nicht mitkommt, bekommt eine Lücke markiert statt den
// Strom zu stauen (D5).
type eventLog struct {
	mu     sync.Mutex
	rings  map[string]*RingBuffer
	subs   map[string]map[*termSubscriber]bool
	feeds  map[string]*termFeed
	termOf func(sessionID string) TermSource
}

func newEventLog() *eventLog {
	return &eventLog{
		rings: map[string]*RingBuffer{},
		subs:  map[string]map[*termSubscriber]bool{},
		feeds: map[string]*termFeed{},
	}
}

func (l *eventLog) ring(sessionID string) *RingBuffer {
	l.mu.Lock()
	defer l.mu.Unlock()
	ring, known := l.rings[sessionID]
	if !known {
		ring = &RingBuffer{}
		l.rings[sessionID] = ring
	}
	return ring
}

// Publish hängt Host-seitig gelesene Terminal-Bytes an und verteilt sie.
func (l *eventLog) Publish(sessionID string, data []byte) {
	if len(data) == 0 {
		return
	}
	ring := l.ring(sessionID)
	ring.Append(data)
	l.mu.Lock()
	subs := make([]*termSubscriber, 0, len(l.subs[sessionID]))
	for sub := range l.subs[sessionID] {
		subs = append(subs, sub)
	}
	l.mu.Unlock()
	for _, sub := range subs {
		sub.deliver(TermFrame(ring.next-uint64(len(data)), data), l)
	}
}

func (s *termSubscriber) deliver(frame Frame, log *eventLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gapped {
		return
	}
	select {
	case s.frames <- frame:
	default:
		// Langsamer Client: Altes verwerfen, Lücke markieren, Leserseite
		// läuft weiter. Der nächste Pump versendet die Lücke zuerst.
		s.gapped = true
		s.gapSeq = frame.Seq
	}
}

// subscribe öffnet einen Stream ab fromSeq: im Fenster Replay plus live,
// außerhalb Lücke plus frischer Snapshot. fromSeq 0 heißt Neu-Anhang und
// bekommt den aktuellen Stand als Ursprung. Das Abo hängt VOR dem Snapshot
// ein, damit kein Byte zwischen Snapshot und Anhang verloren geht: Was
// dazwischen live eintrifft, deckt der Snapshot ab (die Lücke ersetzt), was
// danach kommt, trägt das Replay nach.
func (l *eventLog) subscribe(sessionID string, fromSeq uint64) (StreamSubscription, error) {
	if l.termOf == nil {
		return nil, &WireError{Code: ErrorInternal, Message: "keine Terminal-Quelle eingehängt"}
	}
	source := l.termOf(sessionID)
	if source == nil {
		return nil, &WireError{Code: ErrorMethod, Message: "Session " + sessionID + " hat keine Terminal-Quelle"}
	}
	sub := &termSubscriber{frames: make(chan Frame, subscriberBudget)}
	l.attach(sessionID, sub)
	subscription := &logSubscription{log: l, session: sessionID, sub: sub}
	ring := l.ring(sessionID)
	if fromSeq > 0 {
		if frames, _, ok := ring.Replay(fromSeq); ok {
			for _, frame := range frames {
				sub.frames <- frame
			}
			return subscription, nil
		}
	}
	snapshot, err := source.Snapshot(sessionID)
	if err != nil {
		l.detach(sessionID, sub)
		return nil, &WireError{Code: ErrorObservation, Message: "Pane-Snapshot derzeit nicht lesbar: " + err.Error()}
	}
	_, origin := ring.Bounds()
	sub.frames <- GapFrame(origin, snapshot)
	if frames, _, ok := ring.Replay(origin); ok {
		for _, frame := range frames {
			sub.frames <- frame
		}
	}
	return subscription, nil
}

func (l *eventLog) attach(sessionID string, sub *termSubscriber) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.subs[sessionID] == nil {
		l.subs[sessionID] = map[*termSubscriber]bool{}
	}
	l.subs[sessionID][sub] = true
}

func (l *eventLog) detach(sessionID string, sub *termSubscriber) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.subs[sessionID], sub)
}

// subscriberCount nennt die Zahl offener Abos einer Session (für Tests, die
// erst nach vollständigem Anhang publizieren).
func (l *eventLog) subscriberCount(sessionID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.subs[sessionID])
}

type logSubscription struct {
	log     *eventLog
	session string
	sub     *termSubscriber
	once    sync.Once
	closed  chan struct{}
}

func (s *logSubscription) Frames() <-chan Frame { return s.sub.frames }
func (s *logSubscription) Close() {
	s.once.Do(func() {
		s.log.detach(s.session, s.sub)
	})
}

// StatusDiffer faltet Snapshots zu genau einem Status-Event je betroffener
// Session: neu, verschwunden oder in Präsenz/Aktivität/Attention verändert.
// Unveränderte Sessions senden nichts; unavailable Snapshots reisen als
// Ganzes (5.5), nicht als Event-Strom erfundener Abwesenheit.
func StatusDiffer(previous, next core.ObservationSnapshot) []StatusEvent {
	before := map[core.SessionID]core.SessionObservation{}
	for _, observed := range previous.Sessions {
		before[observed.SessionID] = observed
	}
	var events []StatusEvent
	for _, observed := range next.Sessions {
		old, known := before[observed.SessionID]
		if !known || old.Presence != observed.Presence ||
			old.Attention != observed.Attention ||
			!old.Activity.Equal(observed.Activity) {
			events = append(events, StatusEvent{
				SessionID: string(observed.SessionID),
				Presence:  string(observed.Presence),
				Activity:  observed.Activity.Format(time.RFC3339),
				Attention: string(observed.Attention),
			})
		}
	}
	return events
}

// termFeed pollt ein Pane und hängt Neues an den Ring. Genau ein Feed je
// Session; er startet beim ersten Abo und endet ohne Abonnenten.
type termFeed struct {
	stop chan struct{}
}

func (l *eventLog) ensureFeed(sessionID string, source TermSource, snapshot func() ([]byte, error)) {
	l.mu.Lock()
	if _, running := l.feeds[sessionID]; running {
		l.mu.Unlock()
		return
	}
	feed := &termFeed{stop: make(chan struct{})}
	l.feeds[sessionID] = feed
	l.mu.Unlock()
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		var last []byte
		if initial, err := snapshot(); err == nil {
			last = initial
		}
		for {
			select {
			case <-feed.stop:
				return
			case <-ticker.C:
				current, err := snapshot()
				if err != nil {
					continue
				}
				if suffix := newTail(current, last); len(suffix) > 0 {
					l.Publish(sessionID, suffix)
				}
				last = current
				l.mu.Lock()
				idle := len(l.subs[sessionID]) == 0
				if idle {
					delete(l.feeds, sessionID)
				}
				l.mu.Unlock()
				if idle {
					return
				}
			}
		}
	}()
}

// newTail gibt den angehängten Rest von current gegenüber last zurück. Der
// Pane-Inhalt ist kein Append-Log (tmux malt um), deshalb gilt pragmatisch:
// Ist last ein Präfix, ist der Rest neu; sonst gilt der ganze aktuelle Stand
// als neu, sobald er sich geändert hat. Lücken daraus trägt der Ring ehrlich.
func newTail(current, last []byte) []byte {
	if len(last) == 0 {
		return nil
	}
	if len(current) >= len(last) && string(current[:len(last)]) == string(last) {
		return current[len(last):]
	}
	if string(current) == string(last) {
		return nil
	}
	return current
}

// tmuxTermSource liest und beschreibt Panes über tmux — dieselbe Quelle, die
// lokale Leser sehen. Schreiben geht über send-keys wörtlich (-l), damit
// kein Byte als Tastennamen missverstanden wird.
type tmuxTermSource struct {
	backend *CoreBackend
}

func (s *tmuxTermSource) runtimeTarget(sessionID string) (string, error) {
	st, err := s.backend.loadState()
	if err != nil {
		return "", err
	}
	session := st.SessionByID(core.SessionID(sessionID))
	if session == nil {
		return "", fmt.Errorf("unbekannte SessionID: %s", sessionID)
	}
	return core.TargetSession(session.TmuxName()), nil
}

// Snapshot liest den aktuellen Pane-Inhalt.
func (s *tmuxTermSource) Snapshot(sessionID string) ([]byte, error) {
	target, err := s.runtimeTarget(sessionID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-t", target, "-S", "-200").Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Write schreibt Eingabe-Bytes wörtlich in das Pane.
func (s *tmuxTermSource) Write(sessionID string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	target, err := s.runtimeTarget(sessionID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", "send-keys", "-t", target, "-l", "--", string(data)).Run()
}

// Resize meldet die Fenstergröße; tmux folgt dem jüngsten Leser
// (window-size latest setzt der Host beim Abo).
func (s *tmuxTermSource) Resize(sessionID string, cols, rows int) error {
	target, err := s.runtimeTarget(sessionID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", "set-option", "-w", "-t", target+":", "window-size", "latest").Run()
}
