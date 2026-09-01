package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeRollout(t *testing.T, dir, name string, lines []string, modAt time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modAt, modAt); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodexUsageReadsNewestRateLimits(t *testing.T) {
	home := t.TempDir()
	sessions := filepath.Join(home, "sessions", "2026", "09", "01")
	older := `{"payload":{"rate_limits":{"primary":{"used_percent":11.0,"window_minutes":10080,"resets_at":1788764029}}}}`
	newer := `{"payload":{"rate_limits":{"primary":{"used_percent":60.5,"window_minutes":10080,"resets_at":1788764029},"secondary":{"used_percent":4.0,"window_minutes":300,"resets_at":1788764029}}}}`
	writeRollout(t, sessions, "rollout-a.jsonl", []string{older}, time.Now().Add(-2*time.Hour))
	writeRollout(t, sessions, "rollout-b.jsonl", []string{`{"payload":{}}`, older, newer}, time.Now())

	usage := readCodexUsage(home)
	if usage.Err != "" {
		t.Fatalf("Err = %q", usage.Err)
	}
	if len(usage.Windows) != 2 {
		t.Fatalf("Fenster = %d, want 2", len(usage.Windows))
	}
	if usage.Windows[0].Label != "7d" || usage.Windows[0].Percent != 60.5 {
		t.Fatalf("erstes Fenster = %+v", usage.Windows[0])
	}
	if usage.Windows[1].Label != "5h" || usage.Windows[1].Percent != 4.0 {
		t.Fatalf("zweites Fenster = %+v", usage.Windows[1])
	}
	if usage.Windows[0].Reset.IsZero() {
		t.Fatal("Reset-Zeitpunkt fehlt")
	}
}

func TestCodexUsageWithoutSourceStaysEmpty(t *testing.T) {
	usage := readCodexUsage(t.TempDir())
	if usage.Err == "" {
		t.Fatal("ohne Rollout-Datei muss ein Grund genannt sein")
	}
	if len(usage.Windows) != 0 {
		t.Fatalf("Fenster = %d, want 0", len(usage.Windows))
	}
}

func TestUsageWindowLabel(t *testing.T) {
	tests := map[int]string{300: "5h", 10080: "7d", 60: "1h", 45: "45m", 1440: "1d"}
	for minutes, want := range tests {
		if got := usageWindowLabel(minutes); got != want {
			t.Fatalf("usageWindowLabel(%d) = %q, want %q", minutes, got, want)
		}
	}
}
