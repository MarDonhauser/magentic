package core

import (
	"context"
	"errors"
	"os/exec"
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

func malformedListPanesObservation(t testing.TB, sessions []Session) ObservationSnapshot {
	t.Helper()
	runtimeName := "mgt-malformed"
	if len(sessions) > 0 {
		runtimeName = sessions[0].TmuxName()
	}
	runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list-panes" {
			return runtimeName + "\tnot-a-pane-id\tclaude\t1787227200\t1\t1\n", nil
		}
		return "", errors.New("capture-pane must not run for an unparsed pane")
	}}
	return observeWithRunner(
		context.Background(), sessions, runner,
		testObservationConfig(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)),
	)
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
			return "another-session\t%8\tzsh\t1787227200\t1\t1\n", nil
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

func TestObserveMalformedListDoesNotProveMissingSessionAbsent(t *testing.T) {
	session := Session{ID: "session-1", Name: "one", RuntimeName: "mgt-one", Dir: "/work/one"}
	got := malformedListPanesObservation(t, []Session{session})

	if got.Availability != ObservationPartial || len(got.Problems) != 1 {
		t.Fatalf("malformed list-panes must make the snapshot partial: %#v", got)
	}
	observed := got.Sessions[0]
	if observed.Availability != ObservationPartial || observed.Presence != SessionPresenceUnknown {
		t.Fatalf("unparsed row fabricated Session absence: %#v", observed)
	}
	if observed.Status != StatusUnknown || observed.Attention != AttentionUnknown || observed.Occupancy != OccupancyUnknown {
		t.Fatalf("unknown presence leaked derived negative facts: %#v", observed)
	}
	if got.Problems[0].RuntimeName != session.TmuxName() || got.Problems[0].Operation != "parse-list-panes" {
		t.Fatalf("malformed row diagnostic lost RuntimeName: %#v", got.Problems)
	}
}

func TestObserveDoesNotReconstructMissingRuntimeName(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
		t.Fatalf("missing RuntimeName crossed Observation Adapter: %v", args)
		return "", nil
	}}
	got := observeWithRunner(context.Background(), []Session{{
		ID: "session-1", Name: "display-only", RuntimeName: "",
	}}, runner, testObservationConfig(now))
	if len(runner.Calls()) != 0 || got.Availability != ObservationUnavailable || len(got.Problems) != 1 {
		t.Fatalf("missing RuntimeName was not rejected before probing: %#v calls=%v", got, runner.Calls())
	}
	observed := got.Sessions[0]
	if observed.Presence != SessionPresenceUnknown || observed.Status != StatusUnknown || observed.Attention != AttentionUnknown {
		t.Fatalf("missing RuntimeName fabricated runtime facts: %#v", observed)
	}
}

func TestObserveReturnsPartialResultsWhenOnePaneFails(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	activity := now.Add(-time.Minute).Unix()
	runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
		switch args[0] {
		case "list-panes":
			stamp := strconv.FormatInt(activity, 10)
			return "mgt-one\t%1\tclaude\t" + stamp + "\t1\t1\n" +
				"mgt-two\t%2\tclaude\t" + stamp + "\t1\t1\n", nil
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

func TestObserveSelectsPaneFromActiveWindow(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
		switch args[0] {
		case "list-panes":
			// pane_active is true once per window. Only the pane that is also in
			// window_active may represent the Session's current state.
			return "mgt-one\t%1\tclaude\t1787227100\t0\t1\n" +
				"mgt-one\t%2\tclaude\t1787227200\t1\t1\n", nil
		case "capture-pane":
			if args[3] == "%1" {
				return "Ready\nshift+tab to cycle\n", nil
			}
			if args[3] == "%2" {
				return "working… (esc to interrupt)\n", nil
			}
			return "", errors.New("unexpected pane")
		default:
			return "", errors.New("unexpected command")
		}
	}}
	session := Session{ID: "session-1", Name: "one", RuntimeName: "mgt-one"}

	got := observeWithRunner(context.Background(), []Session{session}, runner, testObservationConfig(now))
	observed := got.Sessions[0]
	if observed.Availability != ObservationAvailable || observed.Status != StatusRunning ||
		!strings.Contains(observed.Content, "working") {
		t.Fatalf("inactive window supplied Session status: %#v", observed)
	}
	for _, call := range runner.Calls() {
		if len(call) > 3 && call[0] == "capture-pane" && call[3] == "%1" {
			t.Fatalf("Observe captured inactive-window pane: %#v", call)
		}
	}
}

func TestObserveTreatsMissingTmuxServerAsAbsence(t *testing.T) {
	now := time.Date(2026, 9, 3, 8, 5, 0, 0, time.UTC)
	session := Session{ID: "session-1", Name: "one", RuntimeName: "mgt-one", Dir: "/work/one"}
	for _, stderr := range []string{
		"error connecting to /private/tmp/tmux-503/default (No such file or directory)\n",
		"no server running on /private/tmp/tmux-503/default\n",
	} {
		runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
			if args[0] == "list-panes" {
				return "", &exec.ExitError{Stderr: []byte(stderr)}
			}
			return "", errors.New("capture-pane must not run without a server")
		}}
		got := observeWithRunner(context.Background(), []Session{session}, runner, testObservationConfig(now))
		observed := got.Sessions[0]
		if got.Availability != ObservationAvailable || len(got.Problems) != 0 ||
			observed.Availability != ObservationAvailable || observed.Presence != SessionPresenceAbsent ||
			observed.Status != StatusDead || observed.Occupancy != OccupancyVacant {
			t.Fatalf("missing tmux server (%q) was not reported as absence: %#v", stderr, got)
		}
	}

	runner := &recordingObservationRunner{run: func(_ context.Context, _ ...string) (string, error) {
		return "", &exec.ExitError{Stderr: []byte("error connecting to /private/tmp/tmux-503/default (Permission denied)\n")}
	}}
	got := observeWithRunner(context.Background(), []Session{session}, runner, testObservationConfig(now))
	if got.Availability != ObservationUnavailable || got.Sessions[0].Presence != SessionPresenceUnknown {
		t.Fatalf("unreachable tmux socket fabricated absence: %#v", got)
	}
	if len(got.Problems) != 1 || !strings.Contains(got.Problems[0].Message, "Permission denied") {
		t.Fatalf("list-panes problem lost the tmux diagnostic: %#v", got.Problems)
	}
}

func TestObserveEmptyOrUnterminatedPaneListKeepsPresenceUnknown(t *testing.T) {
	session := Session{ID: "session-1", Name: "one", RuntimeName: "mgt-one"}
	for _, output := range []string{"", "another-session\t%8\tzsh\t1787227200\t1\t1"} {
		runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
			if args[0] == "list-panes" {
				return output, nil
			}
			return "", errors.New("capture-pane must not run")
		}}
		got := observeWithRunner(context.Background(), []Session{session}, runner, testObservationConfig(time.Now()))
		observed := got.Sessions[0]
		if got.Availability != ObservationPartial || observed.Availability != ObservationPartial ||
			observed.Presence != SessionPresenceUnknown || observed.Status != StatusUnknown {
			t.Fatalf("malformed successful listing fabricated absence: %#v", got)
		}
	}
}

func TestPromptTargetActionsRejectUnknownOrUnavailableCapture(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	target := Session{ID: "target", Name: "target", RuntimeName: "mgt-target"}
	tests := []struct {
		name             string
		run              func(context.Context, ...string) (string, error)
		wantAvailability ObservationAvailability
	}{
		{
			name: "list unavailable",
			run: func(context.Context, ...string) (string, error) {
				return "", errors.New("tmux unavailable")
			},
			wantAvailability: ObservationUnavailable,
		},
		{
			name: "capture unavailable",
			run: func(_ context.Context, args ...string) (string, error) {
				if args[0] == "list-panes" {
					return "mgt-target\t%7\tclaude\t1787227200\t1\t1\n", nil
				}
				return "", errors.New("capture failed")
			},
			wantAvailability: ObservationPartial,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingObservationRunner{run: test.run}
			snapshot := observeWithRunner(
				context.Background(), []Session{target}, runner, testObservationConfig(now),
			)
			observed := promptTargetObservationFromSnapshot(target, snapshot)
			if observed.Availability != test.wantAvailability || observed.ContentKnown || observed.Input != promptInputUnknown {
				t.Fatalf("failed capture became a known prompt target: %#v", observed)
			}
			if err := validatePromptTargetObservation(target.Name, observed); err == nil {
				t.Fatal("prompt action accepted unavailable capture facts")
			}
		})
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
			return "mgt-one\t%3\tclaude\t" + strconv.FormatInt(activity.Unix(), 10) + "\t1\t1\n", nil
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

// Recorded vendor markers are the only licence to interpret a screen. A pane
// that matches nothing a vendor was observed to show must stay unknown, and
// Gemini stays unknown throughout because it was never observed at all.
func TestObserveDoesNotFabricateStatusForUnfamiliarScreens(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		tool    string
		content string
	}{
		{tool: AgentToolCodex, content: "irgendein fremder Bildschirm\nohne bekannte Merkmale\n"},
		{tool: AgentToolCopilot, content: "irgendein fremder Bildschirm\nohne bekannte Merkmale\n"},
		// Gemini wurde nie beobachtet; auch ein Dialog, den Claude erkennen
		// würde, bleibt für Gemini unbekannt.
		{tool: AgentToolGemini, content: "Do you want to run this command?\n❯ 1. Yes\n"},
	}
	for _, tt := range cases {
		t.Run(tt.tool, func(t *testing.T) {
			runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
				if args[0] == "list-panes" {
					return "mgt-one\t%3\t" + tt.tool + "\t1787227200\t1\t1\n", nil
				}
				return tt.content, nil
			}}
			got := observeWithRunner(context.Background(), []Session{{
				ID: "session-1", Name: "one", RuntimeName: "mgt-one", Kind: KindTerm,
			}}, runner, testObservationConfig(now))
			observed := got.Sessions[0]
			if observed.Tool != tt.tool || observed.Status != StatusUnknown || observed.Attention != AttentionUnknown || observed.Unread {
				t.Fatalf("unsupported %s status semantics were fabricated: %#v", tt.tool, observed)
			}
			promptTarget := promptTargetObservationFromSession(observed)
			if promptTarget.Input != promptInputUnknown {
				t.Fatalf("unsupported %s input readiness was fabricated: %#v", tt.tool, promptTarget)
			}
			if err := validatePromptTargetObservation("one", promptTarget); err == nil {
				t.Fatalf("ein fremder %s-Bildschirm darf keinen Prompt bekommen", tt.tool)
			}
		})
	}
}

// A recorded marker, by contrast, must be honoured: Codex and Copilot both
// present a numbered question when they need an answer.
func TestObserveHonoursRecordedVendorMarkers(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, tool := range []string{AgentToolCodex, AgentToolCopilot} {
		t.Run(tool, func(t *testing.T) {
			runner := &recordingObservationRunner{run: func(_ context.Context, args ...string) (string, error) {
				if args[0] == "list-panes" {
					return "mgt-one\t%3\t" + tool + "\t1787227200\t1\t1\n", nil
				}
				return "Do you want to run this command?\n❯ 1. Yes\n", nil
			}}
			got := observeWithRunner(context.Background(), []Session{{
				ID: "session-1", Name: "one", RuntimeName: "mgt-one", Kind: KindTerm,
			}}, runner, testObservationConfig(now))
			if observed := got.Sessions[0]; observed.Status != StatusBlocked {
				t.Fatalf("%s: Status = %v, want wartet", tool, observed.Status.Label())
			}
		})
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
			return malicious + "\t%9\tclaude\t1787227200\t1\t1\n", nil
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

func TestObserveBoundsTmuxProbes(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	runner := &recordingObservationRunner{run: func(ctx context.Context, _ ...string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}
	config := testObservationConfig(now)
	config.cycleTimeout = 40 * time.Millisecond
	config.probeTimeout = 20 * time.Millisecond
	started := time.Now()

	got := observeWithRunner(context.Background(), []Session{{
		ID: "session-1", Name: "one", RuntimeName: "mgt-one",
	}}, runner, config)

	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("tmux probe exceeded its bounded timeout: %s", elapsed)
	}
	if got.Availability != ObservationUnavailable || len(got.Problems) != 1 || !got.Problems[0].TimedOut {
		t.Fatalf("timeout was not represented explicitly: %#v", got)
	}
}
