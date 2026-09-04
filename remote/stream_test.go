package remote

import (
	"context"
	"testing"
	"time"

	"magentic/core"
)

func scriptSource(data []byte) TermSource {
	return &stubTermSource{snapshot: data}
}

type stubTermSource struct {
	snapshot []byte
	written  [][]byte
}

func (s *stubTermSource) Snapshot(sessionID string) ([]byte, error) {
	return s.snapshot, nil
}
func (s *stubTermSource) Write(sessionID string, data []byte) error {
	s.written = append(s.written, append([]byte(nil), data...))
	return nil
}
func (s *stubTermSource) Resize(sessionID string, cols, rows int) error { return nil }

// Was eine angehängte Session schreibt, erreicht den Client mit monotonen
// Sequenzen.
func TestStreamDeliversSessionOutput(t *testing.T) {
	log := newEventLog()
	log.termOf = func(sessionID string) TermSource { return scriptSource([]byte("anfang")) }
	subscription, err := log.subscribe("s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	origin := <-subscription.Frames()
	if origin.Kind != FrameGap {
		t.Fatalf("Neu-Anhang startet nicht mit Ursprung, sondern %q", origin.Kind)
	}
	log.Publish("s1", []byte("hallo "))
	log.Publish("s1", []byte("agent"))
	var seqs []uint64
	for i := 0; i < 2; i++ {
		select {
		case frame := <-subscription.Frames():
			data, err := frame.TermBytes()
			if err != nil {
				t.Fatal(err)
			}
			_ = data
			seqs = append(seqs, frame.Seq)
		case <-time.After(2 * time.Second):
			t.Fatal("Ausgabe erreicht den Client nicht")
		}
	}
	if len(seqs) != 2 || seqs[1] <= seqs[0] {
		t.Errorf("Sequenzen nicht monoton: %v", seqs)
	}
}

// Resume im Fenster: Replay, dann live weiter. Außerhalb: Lücke plus
// frischer Snapshot als neuer Ursprung.
func TestStreamResumeInsideAndOutsideWindow(t *testing.T) {
	log := newEventLog()
	log.termOf = func(sessionID string) TermSource { return scriptSource([]byte("jetzt")) }
	live, err := log.subscribe("s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	<-live.Frames()
	log.Publish("s1", []byte("eins"))
	log.Publish("s1", []byte("zwei"))
	_, next := log.ring("s1").Bounds()

	resumed, err := log.subscribe("s1", next-4)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	first := <-resumed.Frames()
	if first.Kind != FrameTermOutput {
		t.Fatalf("Resume im Fenster liefert %q statt Replay", first.Kind)
	}
	data, _ := first.TermBytes()
	if string(data) != "zwei" {
		t.Errorf("Replay liefert %q statt versäumter Bytes", data)
	}

	log.termOf = func(sessionID string) TermSource { return scriptSource([]byte("frisch")) }
	// Fenster überlaufen lassen.
	for i := 0; i < RingCapSegments+10; i++ {
		log.Publish("s1", []byte("x"))
	}
	gapped, err := log.subscribe("s1", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer gapped.Close()
	gap := <-gapped.Frames()
	if gap.Kind != FrameGap {
		t.Fatalf("Resume außerhalb liefert %q statt Lücke", gap.Kind)
	}
	snapshot, _ := gap.TermBytes()
	if string(snapshot) != "frisch" {
		t.Errorf("Lücke trägt Snapshot %q", snapshot)
	}
}

// Langsamer Client: begrenzt, markiert lückenhaft — der pty-Leser blockiert
// nie.
func TestSlowConsumerStaysBounded(t *testing.T) {
	log := newEventLog()
	log.termOf = func(sessionID string) TermSource { return scriptSource([]byte("s")) }
	subscription, err := log.subscribe("s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	<-subscription.Frames()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5000; i++ {
			log.Publish("s1", []byte("bytes "))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pty-Leser blockiert am langsamen Client")
	}
}

// Eine Observation-Änderung erzeugt genau ein Status-Event je betroffener
// Session — Unveränderte senden nichts.
func TestStatusEventsOncePerAffectedSession(t *testing.T) {
	moment := time.Now().UTC()
	before := core.ObservationSnapshot{
		Availability: core.ObservationAvailable,
		Sessions: []core.SessionObservation{
			{SessionID: "a", Presence: core.SessionPresencePresent, Attention: core.AttentionWorking, Activity: moment},
			{SessionID: "b", Presence: core.SessionPresencePresent, Attention: core.AttentionNone, Activity: moment},
		},
	}
	after := core.ObservationSnapshot{
		Availability: core.ObservationAvailable,
		Sessions: []core.SessionObservation{
			{SessionID: "a", Presence: core.SessionPresencePresent, Attention: core.AttentionNeedsInput, Activity: moment},
			{SessionID: "b", Presence: core.SessionPresencePresent, Attention: core.AttentionNone, Activity: moment},
		},
	}
	events := StatusDiffer(before, after)
	if len(events) != 1 || events[0].SessionID != "a" {
		t.Errorf("falsche Events: %+v", events)
	}
	if len(StatusDiffer(before, before)) != 0 {
		t.Error("unveränderte Sessions erzeugen Events")
	}
}

// Partial und unavailable reisen unverändert und werden nie als abwesend
// serviert.
func TestUnavailableServedUnchanged(t *testing.T) {
	backend := NewCoreBackend()
	backend.observe = func(_ context.Context, sessions []core.Session) core.ObservationSnapshot {
		observations := make([]core.SessionObservation, 0, len(sessions))
		for _, session := range sessions {
			observations = append(observations, core.SessionObservation{
				SessionID:    session.ID,
				Availability: core.ObservationUnavailable,
				Presence:     core.SessionPresenceUnknown,
			})
		}
		return core.ObservationSnapshot{
			Availability: core.ObservationUnavailable,
			Sessions:     observations,
			Problems:     []core.ObservationProblem{{Operation: "list-panes", Message: "timed out"}},
		}
	}
	snapshot := backend.Observe(nil)
	if snapshot.Availability != core.ObservationUnavailable {
		t.Errorf("Verfügbarkeit umgedeutet: %q", snapshot.Availability)
	}
	if snapshot.Transport != core.ObservationTransportRemote {
		t.Errorf("Herkunft fehlt: %q", snapshot.Transport)
	}
	for _, observed := range snapshot.Sessions {
		if observed.Presence == core.SessionPresenceAbsent {
			t.Errorf("Session %s als abwesend serviert", observed.SessionID)
		}
	}
	if len(snapshot.Problems) != 1 {
		t.Errorf("Host-Begründung verloren: %+v", snapshot.Problems)
	}
}
