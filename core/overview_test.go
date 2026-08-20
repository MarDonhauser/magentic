package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestUnread(t *testing.T) {
	seen := time.Now().Add(-1 * time.Hour)
	after := seen.Add(10 * time.Minute)
	before := seen.Add(-10 * time.Minute)

	cases := []struct {
		name   string
		status AgentStatus
		active time.Time
		want   bool
	}{
		{"idle mit Aktivität nach dem Blick", StatusIdle, after, true},
		{"idle ohne neue Aktivität", StatusIdle, before, false},
		{"wartet und war aktiv", StatusBlocked, after, true},
		{"beendet nach dem Blick", StatusExited, after, true},
		{"läuft gerade", StatusRunning, after, false},
		{"Background-Agents laufen", StatusAgents, after, false},
		{"Terminal", StatusTerm, after, false},
		{"tot", StatusDead, after, false},
	}
	for _, c := range cases {
		if got := unread(c.status, seen, c.active); got != c.want {
			t.Errorf("%s: %v, erwartet %v", c.name, got, c.want)
		}
	}
}

func TestUnreadNieGesehen(t *testing.T) {
	if !unread(StatusIdle, time.Time{}, time.Now()) {
		t.Fatal("nie gesehene Session mit Aktivität muss ungelesen sein")
	}
}

func gitInit(t *testing.T, dir string, args ...[]string) {
	t.Helper()
	for _, a := range args {
		cmd := exec.Command("git", append([]string{"-C", dir}, a...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v — %s", a, err, out)
		}
	}
}

func TestSessionNameHint(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir,
		[]string{"init", "-q", "-b", "main"},
		[]string{"config", "user.email", "t@example.com"},
		[]string{"config", "user.name", "Test"},
		[]string{"commit", "-q", "--allow-empty", "-m", "init"},
	)
	FlushGitMemo()

	if got := SessionNameHint(dir, "projekt"); got != "projekt" {
		t.Fatalf("auf main erwartet Fallback %q, bekam %q", "projekt", got)
	}

	gitInit(t, dir, []string{"checkout", "-q", "-b", "agent/neue-suche"})
	FlushGitMemo()
	if got := SessionNameHint(dir, "projekt"); got != "neue-suche" {
		t.Fatalf("erwartet %q, bekam %q", "neue-suche", got)
	}

	gitInit(t, dir, []string{"checkout", "-q", "-b", "dev"})
	FlushGitMemo()
	if got := SessionNameHint(dir, "projekt"); got != "projekt" {
		t.Fatalf("dev ist ein Integrationsbranch, erwartet Fallback, bekam %q", got)
	}

	if got := SessionNameHint("", "projekt"); got != "projekt" {
		t.Fatalf("ohne Verzeichnis erwartet Fallback, bekam %q", got)
	}
	FlushGitMemo()
	if got := SessionNameHint(t.TempDir(), "projekt"); got != "projekt" {
		t.Fatalf("ohne Repo erwartet Fallback, bekam %q", got)
	}
}

func TestMarkSeen(t *testing.T) {
	s := &State{Agents: []Agent{{Name: "hera"}}}
	if !s.MarkSeen("hera") {
		t.Fatal("MarkSeen muss true liefern")
	}
	if s.Agents[0].SeenAt.IsZero() {
		t.Fatal("SeenAt wurde nicht gesetzt")
	}
	if s.MarkSeen("gibtsnicht") {
		t.Fatal("MarkSeen für unbekannte Session muss false liefern")
	}
}

func TestOverviewProjectsCoherentObservationFactsAndStableIDs(t *testing.T) {
	dir := t.TempDir()
	activeAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	state := &State{
		Projects: []Project{{ID: "project-1", Name: "NAVI", Path: dir, MainBranch: "main"}},
		Agents: []Session{{
			ID: "session-1", Name: "one", ProjectID: "project-1", Project: "NAVI", Dir: dir,
			Purpose: SessionPurposeCleanup, CreatedAt: activeAt.Add(-time.Hour),
		}},
	}
	snapshot := ObservationSnapshot{
		ObservedAt:   activeAt,
		Availability: ObservationAvailable,
		Sessions: []SessionObservation{{
			SessionID: "session-1", Availability: ObservationAvailable,
			Presence: SessionPresencePresent, Status: StatusBlocked,
			Content: "content intentionally does not encode the detail", ContentKnown: true,
			Activity: activeAt, ActivityKnown: true, Tool: AgentToolCodex,
			Detail: "coherent detail", Attention: AttentionNeedsInput, Unread: true,
			Occupancy: OccupancyOccupied,
		}},
	}

	got := BuildOverviewFromObservation(state, snapshot)
	if len(got.Projects) != 1 || got.Projects[0].ID != "project-1" {
		t.Fatalf("stable Project identity missing: %#v", got.Projects)
	}
	agent := got.Projects[0].Worktrees[0].Agents[0]
	if agent.ID != "session-1" || agent.Name != "one" {
		t.Fatalf("stable Session identity missing: %#v", agent)
	}
	if agent.Status != "blocked" || agent.Tool != AgentToolCodex || agent.Detail != "coherent detail" || !agent.Unread {
		t.Fatalf("Overview recomputed or lost Observation facts: %#v", agent)
	}
	if agent.Phase != "cleanup" {
		t.Fatalf("Session purpose was lost during projection: %#v", agent)
	}
	if got.Counts["blocked"] != 1 || got.Counts["unread"] != 1 {
		t.Fatalf("Overview counts do not match Observation: %#v", got.Counts)
	}
}

func TestOverviewUnavailableObservationIsReadOnlyAndDoesNotWarnAsDead(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir,
		[]string{"init", "-q", "-b", "main"},
		[]string{"config", "user.email", "t@example.com"},
		[]string{"config", "user.name", "Test"},
		[]string{"commit", "-q", "--allow-empty", "-m", "init"},
	)
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	FlushGitMemo()
	state := &State{
		Projects: []Project{{ID: "project-1", Name: "NAVI", Path: dir, MainBranch: "main"}},
		Agents:   []Session{{ID: "session-1", Name: "one", Project: "NAVI", Dir: dir}},
	}
	snapshot := ObservationSnapshot{
		Availability: ObservationUnavailable,
		Sessions: []SessionObservation{{
			SessionID: "session-1", Availability: ObservationUnavailable,
			Presence: SessionPresenceUnknown, Status: StatusUnknown,
			Attention: AttentionUnknown, Occupancy: OccupancyUnknown,
		}},
	}

	got := BuildOverviewFromObservation(state, snapshot)
	if len(state.Agents) != 1 || state.Agents[0].ID != "session-1" {
		t.Fatalf("Overview mutated the Registry-shaped input: %#v", state.Agents)
	}
	if got.Counts["unknown"] != 1 {
		t.Fatalf("unavailable tmux was not preserved as unknown: %#v", got.Counts)
	}
	wt := got.Projects[0].Worktrees[0]
	if wt.Clean {
		t.Fatal("dirty Worktree fixture was not observed")
	}
	if len(wt.Warnings) != 0 {
		t.Fatalf("unknown Session was treated as dead: %#v", wt.Warnings)
	}
}
