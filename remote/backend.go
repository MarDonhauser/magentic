package remote

import (
	"context"
	"encoding/json"

	"magentic/core"
)

// HostBackend ist, was der Host hinter dem Transport bedient. Die
// Produktion hängt hier die lokale Implementierung ein (core-Funktionen
// über denselben Registry- und Lifecycle-Pfaden wie die Desktop-App);
// Tests hängen einen Stub ein. Der Host selbst kennt kein core-Verhalten
// jenseits dieser schmalen Naht.
type HostBackend interface {
	// HandleCall führt einen policy-erlaubten Aufruf aus und liefert das
	// Ergebnis als JSON-fähigen Wert. Ein *WireError steuert die
	// Fehlergestalt; jeder andere Fehler wird ErrorInternal.
	HandleCall(ctx context.Context, method string, params json.RawMessage, identity string) (any, error)
	// Observe liefert den aktuellen Snapshot für Status-Events.
	Observe(ctx context.Context) core.ObservationSnapshot
	// Subscribe öffnet einen Terminal-Stream für eine Session ab fromSeq.
	Subscribe(sessionID string, fromSeq uint64) (StreamSubscription, error)
}

// StreamSubscription ist ein offener Terminal-Stream: Rahmen kommen über
// Frames, Close beendet ihn.
type StreamSubscription interface {
	Frames() <-chan Frame
	Close()
}

// StubBackend ist der Test-Stub für HostBackend: Methoden werden aus
// Calls beantwortet, Observe liefert Fixed, Subscribe liefert Scripted.
type StubBackend struct {
	Calls     map[string]func(params json.RawMessage, identity string) (any, error)
	Fixed     core.ObservationSnapshot
	Subscript func(sessionID string, fromSeq uint64) (StreamSubscription, error)
}

// HandleCall beantwortet aus Calls; Unbekanntes meldet fail-closed.
func (s *StubBackend) HandleCall(_ context.Context, method string, params json.RawMessage, identity string) (any, error) {
	if fn, known := s.Calls[method]; known {
		return fn(params, identity)
	}
	return nil, &WireError{Code: ErrorMethod, Message: "Methode " + method + " wird von diesem Host nicht bedient"}
}

// Observe liefert den eingehängten Snapshot.
func (s *StubBackend) Observe(_ context.Context) core.ObservationSnapshot {
	return s.Fixed
}

// Subscribe liefert den eingehängten Stream oder einen leeren, sofort
// geschlossenen.
func (s *StubBackend) Subscribe(sessionID string, fromSeq uint64) (StreamSubscription, error) {
	if s.Subscript != nil {
		return s.Subscript(sessionID, fromSeq)
	}
	sub := &stubSubscription{frames: make(chan Frame)}
	close(sub.frames)
	return sub, nil
}

type stubSubscription struct {
	frames chan Frame
}

func (s *stubSubscription) Frames() <-chan Frame { return s.frames }

// Close ist idempotent.
func (s *stubSubscription) Close() {}
