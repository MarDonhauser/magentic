package core

import (
	"os/exec"
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
