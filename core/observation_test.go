package core

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingObservationRunner struct {
	mu    sync.Mutex
	calls [][]string
	run   func(context.Context, ...string) (string, error)
}

func (r *recordingObservationRunner) Run(ctx context.Context, args ...string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string(nil), args...))
	r.mu.Unlock()
	return r.run(ctx, args...)
}

func (r *recordingObservationRunner) Calls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	for i := range r.calls {
		out[i] = append([]string(nil), r.calls[i]...)
	}
	return out
}

func testObservationConfig(now time.Time) observationConfig {
	return observationConfig{
		cycleTimeout: time.Second,
		probeTimeout: time.Second,
		now:          func() time.Time { return now },
	}
}

func TestObserveDistinguishesTmuxUnavailableFromAbsentSession(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	session := Session{ID: "session-1", Name: "one", RuntimeName: "mgt-one", Dir: "/work/one"}

	t.Run("tmux unavailable", func(t *testing.T) {
		runner := &recordingObservationRunner{run: func(_ context.Context, _ ...string) (string, error) {
			return "", errors.New("tmux executable unavailable")
		}}
		got := observeWithRunner(context.Background(), []Session{session}, runner, testObservationConfig(now))

		if got.Availability != ObservationUnavailable || len(got.Sessions) != 1 {
			t.Fatalf("unexpected snapshot: %#v", got)
		}
		observed := got.Sessions[0]
		if observed.SessionID != session.ID || observed.Availability != ObservationUnavailable {
			t.Fatalf("unexpected Session identity or availability: %#v", observed)
		}
		if observed.Presence != SessionPresenceUnknown || observed.Status != StatusUnknown {
			t.Fatalf("unavailable tmux must not look like a dead Session: %#v", observed)
		}
		if observed.Occupancy != OccupancyUnknown || len(got.Problems) != 1 {
			t.Fatalf("unavailable occupancy/problem mismatch: %#v", got)
		}
	})

	t.Run("session absent", func(t *testing.T) {
		runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
			if args[0] != "list-panes" {
				t.Fatalf("unexpected command: %v", args)
			}
			return "another-session\t%8\tzsh\t1787227200\t1\n", nil
		}}
		got := observeWithRunner(context.Background(), []Session{session}, runner, testObservationConfig(now))

		observed := got.Sessions[0]
		if got.Availability != ObservationAvailable || observed.Availability != ObservationAvailable {
			t.Fatalf("successful cycle must be available: %#v", got)
		}
		if observed.Presence != SessionPresenceAbsent || observed.Status != StatusDead {
			t.Fatalf("absent Session mismatch: %#v", observed)
		}
		if observed.Occupancy != OccupancyVacant {
			t.Fatalf("absent Session must not occupy its Worktree: %#v", observed)
		}
	})
}

func TestObserveReturnsPartialResultsWhenOnePaneFails(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	activity := now.Add(-time.Minute).Unix()
	runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
		switch args[0] {
		case "list-panes":
			stamp := strconv.FormatInt(activity, 10)
			return "mgt-one\t%1\tclaude\t" + stamp + "\t1\n" +
				"mgt-two\t%2\tclaude\t" + stamp + "\t1\n", nil
		case "capture-pane":
			if args[3] == "%2" {
				return "", errors.New("pane disappeared")
			}
			return "working… (esc to interrupt)\n", nil
		default:
			return "", errors.New("unexpected command")
		}
	}}
	sessions := []Session{
		{ID: "session-1", Name: "one", RuntimeName: "mgt-one", Dir: "/work/one"},
		{ID: "session-2", Name: "two", RuntimeName: "mgt-two", Dir: "/work/two"},
	}

	got := observeWithRunner(context.Background(), sessions, runner, testObservationConfig(now))
	if got.Availability != ObservationPartial || len(got.Problems) != 1 {
		t.Fatalf("expected one partial failure: %#v", got)
	}
	if got.Sessions[0].Availability != ObservationAvailable || got.Sessions[0].Status != StatusRunning {
		t.Fatalf("healthy pane was lost: %#v", got.Sessions[0])
	}
	failed := got.Sessions[1]
	if failed.Availability != ObservationPartial || failed.Presence != SessionPresencePresent {
		t.Fatalf("failed pane must remain known-present: %#v", failed)
	}
	if failed.Status != StatusUnknown || failed.ActivityKnown != true || failed.Occupancy != OccupancyOccupied {
		t.Fatalf("failed pane facts were over-collapsed: %#v", failed)
	}
}

func TestObserveNormalizesPaneFactsAndDoesNotMutateSessions(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	activity := now.Add(-time.Minute)
	sessions := []Session{{
		ID:          "session-1",
		Name:        "one",
		RuntimeName: "mgt-one",
		Dir:         "/work/project-agents/one",
		Worktree:    true,
		SeenAt:      activity.Add(-time.Minute),
	}}
	before := append([]Session(nil), sessions...)
	runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
		if args[0] == "list-panes" {
			return "mgt-one\t%3\tclaude\t" + strconv.FormatInt(activity.Unix(), 10) + "\t1\n", nil
		}
		return "\r\n\x1b[31mDo you want to run this command?\x1b[0m  \r\n  ❯ 1. Yes   \r\n\r\n", nil
	}}

	got := observeWithRunner(context.Background(), sessions, runner, testObservationConfig(now))
	if !reflect.DeepEqual(sessions, before) {
		t.Fatalf("Observe mutated its Registry input:\nbefore=%#v\nafter=%#v", before, sessions)
	}
	observed := got.Sessions[0]
	if observed.Content != "Do you want to run this command?\n  ❯ 1. Yes" || !observed.ContentKnown {
		t.Fatalf("content was not normalized: %q", observed.Content)
	}
	if observed.Tool != AgentToolClaude || observed.Status != StatusBlocked {
		t.Fatalf("tool/status mismatch: %#v", observed)
	}
	if observed.Attention != AttentionNeedsInput || !observed.Unread {
		t.Fatalf("attention/unread mismatch: %#v", observed)
	}
	if !observed.ActivityKnown || !observed.Activity.Equal(activity) {
		t.Fatalf("activity mismatch: %#v", observed)
	}
	if observed.WorktreePath != sessions[0].Dir || !observed.Worktree || observed.Occupancy != OccupancyOccupied {
		t.Fatalf("occupancy mismatch: %#v", observed)
	}
	if observed.Detail != "Shell-Freigabe" {
		t.Fatalf("blocked detail mismatch: %#v", observed)
	}
}

func TestObservationAttentionAndUnreadPolicy(t *testing.T) {
	seen := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	after := seen.Add(time.Minute)
	before := seen.Add(-time.Minute)
	cases := []struct {
		name      string
		status    AgentStatus
		activity  time.Time
		attention AttentionState
		unread    bool
	}{
		{"running", StatusRunning, after, AttentionWorking, false},
		{"background agents", StatusAgents, after, AttentionWorking, false},
		{"blocked after seen", StatusBlocked, after, AttentionNeedsInput, true},
		{"blocked before seen", StatusBlocked, before, AttentionNeedsInput, false},
		{"idle", StatusIdle, after, AttentionReview, true},
		{"exited", StatusExited, after, AttentionReview, true},
		{"terminal", StatusTerm, after, AttentionNone, false},
		{"dead", StatusDead, after, AttentionNone, false},
		{"unknown", StatusUnknown, after, AttentionUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := observationAttention(tc.status); got != tc.attention {
				t.Fatalf("attention = %q, want %q", got, tc.attention)
			}
			if got := observationUnread(tc.status, seen, tc.activity, true); got != tc.unread {
				t.Fatalf("unread = %v, want %v", got, tc.unread)
			}
		})
	}
}

func TestObserveNeverPassesRuntimeNameToTmuxCommands(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	malicious := "mgt-one; kill-server"
	runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
		if args[0] == "list-panes" {
			return malicious + "\t%9\tclaude\t1787227200\t1\n", nil
		}
		return "❯ \n", nil
	}}

	got := observeWithRunner(context.Background(), []Session{{
		ID: "session-1", Name: "one", RuntimeName: malicious,
	}}, runner, testObservationConfig(now))
	if got.Sessions[0].Presence != SessionPresencePresent {
		t.Fatalf("fixture Session was not observed: %#v", got)
	}
	for _, call := range runner.Calls() {
		for _, arg := range call {
			if strings.Contains(arg, malicious) {
				t.Fatalf("untrusted RuntimeName reached tmux argv: %q in %v", arg, call)
			}
		}
	}
	calls := runner.Calls()
	if len(calls) != 2 || !reflect.DeepEqual(calls[1], []string{"capture-pane", "-p", "-t", "%9", "-S", "-200"}) {
		t.Fatalf("capture must target only tmux's pane ID: %v", calls)
	}
}
