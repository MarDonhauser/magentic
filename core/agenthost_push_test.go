package core

import (
	"context"
	"sync"
	"testing"
	"time"
)

// 3.9: a status transition reported over the protocol reaches
// ControlEvents.Publish immediately — driven by a real agent host — well
// before any periodic observation cycle (the TUI and headless "serve" loop
// both poll every couple of seconds; this asserts well under that).
func TestHostPushSupervisorPublishesBeforeAnObservationCycle(t *testing.T) {
	host, _, err := startScriptedOmpHost(t, `echo '`+ompTestReady+`'
IFS= read -r line
echo '{"type":"turn_start"}'
echo '{"type":"message_start","message":{"role":"user","content":"hallo"}}'
sleep 30`)
	if err != nil {
		t.Fatal(err)
	}
	session := Session{ID: SessionID("session-1"), Name: "orbit", Runtime: RuntimeOmp, Vendor: AgentVendorOmp, SessionKind: SessionKindCodingAgent}

	var mu sync.Mutex
	var published []ObservationSnapshot
	supervisor := NewHostPushSupervisor(func(_ []Session, snapshot ObservationSnapshot) {
		mu.Lock()
		published = append(published, snapshot)
		mu.Unlock()
	})
	supervisor.records = func(SessionID) (ManagedHostRecord, bool, error) {
		return ManagedHostRecord{SessionID: session.ID, SocketPath: host.Path(), Token: host.token}, true, nil
	}
	supervisor.watch = func(ctx context.Context, record ManagedHostRecord, since uint64) (AgentHostState, uint64, bool, error) {
		return WatchAgentHostState(record.SocketPath, record.Token, since, time.Second)
	}
	supervisor.Reconcile([]Session{session})
	defer supervisor.Stop()

	// The handshake itself is the first publishable state; wait for it so
	// the timed delivery below measures the delivery transition alone.
	waitFor(t, "die erste Veröffentlichung nach dem Handshake", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(published) >= 1
	})
	mu.Lock()
	published = nil
	mu.Unlock()

	start := time.Now()
	if err := host.Deliver("msg-1", "hallo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "die Veröffentlichung nach der Zustellung", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, snapshot := range published {
			if len(snapshot.Sessions) == 1 && snapshot.Sessions[0].Status == StatusRunning {
				return true
			}
		}
		return false
	})
	elapsed := time.Since(start)
	// observationInterval in the TUI is 2s, in headless serve mode longer
	// still; well under half of that proves this did not wait for a cycle.
	if elapsed > time.Second {
		t.Fatalf("transition visible after %s, want well under one periodic cycle", elapsed)
	}

	mu.Lock()
	snapshot := published[len(published)-1]
	mu.Unlock()
	if snapshot.Sessions[0].StatusSource != StatusSourceOmpProtocol {
		t.Fatalf("source = %q, want the omp protocol", snapshot.Sessions[0].StatusSource)
	}
}

// Reconcile stops a push loop once a Session leaves the set (ended, or no
// longer host-owned), rather than leaking it.
func TestHostPushSupervisorStopsALoopWhenTheSessionIsGone(t *testing.T) {
	calls := 0
	var mu sync.Mutex
	supervisor := NewHostPushSupervisor(func([]Session, ObservationSnapshot) {})
	supervisor.records = func(SessionID) (ManagedHostRecord, bool, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return ManagedHostRecord{}, false, nil
	}
	session := Session{ID: "session-x", Runtime: RuntimeOmp, Vendor: AgentVendorOmp, SessionKind: SessionKindCodingAgent}
	supervisor.Reconcile([]Session{session})
	waitFor(t, "mindestens einen records()-Aufruf", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls > 0
	})
	supervisor.Reconcile(nil)
	mu.Lock()
	seenAfterStop := calls
	mu.Unlock()
	time.Sleep(hostPushRetryBackoff + 200*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls > seenAfterStop+1 {
		t.Fatalf("records() kept being called after the Session left the set: %d calls", calls)
	}
}
