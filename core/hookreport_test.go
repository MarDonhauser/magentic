package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testHookStore(now time.Time) *HookReportStore {
	store := NewHookReportStore()
	store.now = func() time.Time { return now }
	return store
}

func hookSession() Session {
	return Session{
		ID: "session-1", Name: "eins", RuntimeName: "mgt-eins",
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-a"}},
	}
}

func claudeHookReport(state HookReportState, at time.Time) HookReport {
	return HookReport{
		State: state, At: at, Vendor: AgentVendorClaude,
		RuntimeName: "mgt-eins", RunRef: "run-a", UID: os.Getuid(),
	}
}

func TestHookReportVocabularyIsValidated(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	sessions := []Session{hookSession()}
	store := testHookStore(now)
	if err := store.Apply(claudeHookReport(HookStateWorking, now), sessions); err != nil {
		t.Fatalf("gültige Meldung abgelehnt: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*HookReport)
		want   string
	}{
		{"unbekannter Zustand", func(r *HookReport) { r.State = "exited" }, "unbekannten Zustand"},
		{"ohne Zeitpunkt", func(r *HookReport) { r.At = time.Time{} }, "keinen Zeitpunkt"},
		{"ohne Vendor", func(r *HookReport) { r.Vendor = "" }, "keinen Vendor"},
		{"ohne Adresse", func(r *HookReport) { r.RuntimeName, r.RunRef = "", "" }, "weder SessionID noch RuntimeName"},
		{"fremder Benutzer", func(r *HookReport) { r.UID = os.Getuid() + 1 }, "angemeldeten Benutzer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := claudeHookReport(HookStateBlocked, now.Add(time.Minute))
			test.mutate(&report)
			err := store.Apply(report, sessions)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Begründung = %v, want etwas mit %q", err, test.want)
			}
			record, fresh := store.fresh("session-1", now)
			if !fresh || record.status != StatusRunning {
				t.Fatalf("die vorherige Meldung wurde angetastet: %#v", record)
			}
		})
	}
}

func TestHookReportCarriesBlockedDetail(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	store := testHookStore(now)
	report := claudeHookReport(HookStateBlocked, now)
	report.Detail = "Claude braucht eine Erlaubnis für Bash"
	if err := store.Apply(report, []Session{hookSession()}); err != nil {
		t.Fatalf("Meldung abgelehnt: %v", err)
	}
	record, fresh := store.fresh("session-1", now)
	if !fresh || record.status != StatusBlocked || record.detail != report.Detail {
		t.Fatalf("Detail ging verloren: %#v", record)
	}
}

func TestHookReportCorrelation(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	sessions := []Session{hookSession()}

	t.Run("unbekannte Session", func(t *testing.T) {
		store := testHookStore(now)
		report := claudeHookReport(HookStateWorking, now)
		report.RuntimeName = "mgt-woanders"
		if err := store.Apply(report, sessions); err == nil {
			t.Fatal("eine Meldung ohne Session wurde angewendet")
		}
		if _, fresh := store.fresh("session-1", now); fresh {
			t.Fatal("eine fremde Meldung hat eine andere Session verändert")
		}
	})

	t.Run("Laufzeit wurde ersetzt", func(t *testing.T) {
		store := testHookStore(now)
		report := claudeHookReport(HookStateWorking, now)
		report.RunRef = "run-b"
		if err := store.Apply(report, sessions); err == nil {
			t.Fatal("die Meldung eines abgelösten Laufs wurde angewendet")
		}
	})

	t.Run("über die SessionID", func(t *testing.T) {
		store := testHookStore(now)
		report := HookReport{
			State: HookStateDone, At: now, Vendor: AgentVendorClaude,
			SessionID: "session-1", UID: os.Getuid(),
		}
		if err := store.Apply(report, sessions); err != nil {
			t.Fatalf("Meldung abgelehnt: %v", err)
		}
		if record, fresh := store.fresh("session-1", now); !fresh || record.status != StatusDone {
			t.Fatalf("Meldung nicht zugeordnet: %#v", record)
		}
	})
}

func TestHookReportFreshnessWindow(t *testing.T) {
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	sessions := []Session{hookSession()}
	store := testHookStore(start)
	if err := store.Apply(claudeHookReport(HookStateWorking, start), sessions); err != nil {
		t.Fatalf("Meldung abgelehnt: %v", err)
	}
	if _, fresh := store.fresh("session-1", start.Add(hookReportFreshness-time.Second)); !fresh {
		t.Fatal("eine Meldung ist zu früh verfallen")
	}
	if _, fresh := store.fresh("session-1", start.Add(hookReportFreshness)); fresh {
		t.Fatal("eine Meldung war nach dem Fenster noch maßgeblich")
	}

	// Eine Auffrischung hält das Fenster offen, ohne den Zustand zu ändern.
	store.now = func() time.Time { return start.Add(30 * time.Second) }
	if err := store.Apply(claudeHookReport(HookStateRefresh, start.Add(30*time.Second)), sessions); err != nil {
		t.Fatalf("Auffrischung abgelehnt: %v", err)
	}
	record, fresh := store.fresh("session-1", start.Add(80*time.Second))
	if !fresh || record.status != StatusRunning {
		t.Fatalf("die Auffrischung hat das Fenster nicht verlängert: %#v", record)
	}

	// Eine ältere Meldung setzt die Session nicht zurück.
	if err := store.Apply(claudeHookReport(HookStateIdle, start.Add(10*time.Second)), sessions); err == nil {
		t.Fatal("eine verspätete ältere Meldung wurde angewendet")
	}
	record, _ = store.fresh("session-1", start.Add(80*time.Second))
	if record.status != StatusRunning {
		t.Fatalf("eine verspätete Meldung hat den Status zurückgedreht: %v", record.status.Label())
	}
}

func TestHookReportEventFile(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "reports", "hook-reports.jsonl")
	sessions := []Session{hookSession()}

	if err := AppendHookReport(path, claudeHookReport(HookStateBlocked, now)); err != nil {
		t.Fatalf("Meldung schreiben: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Meldungsdatei: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("Dateirechte = %v, want 0600", info.Mode().Perm())
	}

	store := testHookStore(now)
	if problems := store.DrainHookReportFile(path, sessions); len(problems) > 0 {
		t.Fatalf("Meldung nicht gefaltet: %v", problems)
	}
	if record, fresh := store.fresh("session-1", now); !fresh || record.status != StatusBlocked {
		t.Fatalf("Meldung nicht angewendet: %#v", record)
	}
	// Nach dem Falten ist die Datei leer, dieselbe Meldung wirkt kein zweites Mal.
	if info, err = os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("Datei nach dem Falten: %v (%v)", info.Size(), err)
	}
	store.now = func() time.Time { return now.Add(time.Minute) }
	if err := store.Apply(claudeHookReport(HookStateBlocked, now), sessions); err == nil {
		t.Fatal("dieselbe Meldung wurde ein zweites Mal angewendet")
	}

	// Eine übergroße Datei wird rotiert statt gelesen.
	oversized := make([]byte, hookReportFileCap+1)
	for i := range oversized {
		oversized[i] = '\n'
	}
	if err := os.WriteFile(path, oversized, 0o600); err != nil {
		t.Fatalf("übergroße Datei: %v", err)
	}
	problems := store.DrainHookReportFile(path, sessions)
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "rotiert") {
		t.Fatalf("übergroße Datei wurde nicht rotiert: %v", problems)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotierte Datei fehlt: %v", err)
	}
}

func TestResolutionPrefersFreshHookReportOverTheSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	session := hookSession()
	resting := "❯ \n  🌿 main"
	store := testHookStore(now)
	if err := store.Apply(claudeHookReport(HookStateWorking, now), []Session{session}); err != nil {
		t.Fatalf("Meldung abgelehnt: %v", err)
	}

	fresh := resolveSessionStatus(statusInput{
		session: session, present: true, paneCommand: "2.1.241",
		content: resting, contentKnown: true, now: now.Add(time.Second), reports: store,
	})
	if fresh.Status != StatusRunning || fresh.Source != StatusSourceHook {
		t.Fatalf("frische Meldung = %v aus %q, want läuft aus dem Hook", fresh.Status.Label(), fresh.Source)
	}

	stale := resolveSessionStatus(statusInput{
		session: session, present: true, paneCommand: "2.1.241",
		content:      " Quick safety check\n ❯ 1. Yes, I trust this folder\n Enter to confirm",
		contentKnown: true, now: now.Add(2 * hookReportFreshness), reports: store,
	})
	if stale.Status != StatusBlocked || stale.Source != StatusSourceSnapshot {
		t.Fatalf("verfallene Meldung = %v aus %q, want wartet aus dem Bildschirm", stale.Status.Label(), stale.Source)
	}

	gone := resolveSessionStatus(statusInput{
		session: session, present: false, paneCommand: "2.1.241",
		content: resting, contentKnown: true, now: now.Add(time.Second), reports: store,
	})
	if gone.Status != StatusDead || gone.Source != StatusSourcePresence {
		t.Fatalf("fehlende Laufzeit = %v aus %q, want tot", gone.Status.Label(), gone.Source)
	}
}

// Eine Hook-Meldung muss ohne den nächsten Beobachtungszyklus sichtbar werden.
func TestHookReportsBecomeVisibleWithinTheSubSecondBudget(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MAGENTIC_HOOK_REPORTS", filepath.Join(root, "hook-reports.jsonl"))
	t.Cleanup(defaultHookReports.forget)
	session := hookSession()
	held := ObservationSnapshot{
		Availability: ObservationAvailable,
		Sessions: []SessionObservation{{
			SessionID: session.ID, Availability: ObservationAvailable,
			Presence: SessionPresencePresent, Status: StatusIdle, StatusSource: StatusSourceSnapshot,
		}},
	}
	if err := AppendHookReport(HookReportPath(), claudeHookReport(HookStateBlocked, time.Now())); err != nil {
		t.Fatalf("Meldung schreiben: %v", err)
	}
	started := time.Now()
	refined, changed := ApplyHookReports(held, []Session{session}, time.Now())
	elapsed := time.Since(started)
	if !changed {
		t.Fatal("die Meldung wurde nicht angewendet")
	}
	if refined.Sessions[0].Status != StatusBlocked ||
		refined.Sessions[0].StatusSource != StatusSourceHook ||
		refined.Sessions[0].Attention != AttentionNeedsInput {
		t.Fatalf("Beobachtung nicht verfeinert: %#v", refined.Sessions[0])
	}
	if held.Sessions[0].Status != StatusIdle {
		t.Fatal("die gehaltene Beobachtung wurde in place verändert")
	}
	if elapsed > time.Second {
		t.Fatalf("Anwendung dauerte %v, Budget ist unter einer Sekunde", elapsed)
	}
}

func TestHookReportsAreNeverAPrerequisite(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	resolved := resolveSessionStatus(statusInput{
		session: Session{ID: "s"}, present: true, paneCommand: "codex",
		content: "• Working (12s • esc to interrupt)", contentKnown: true,
		now: now, reports: testHookStore(now),
	})
	if resolved.Status != StatusRunning || resolved.Source != StatusSourceSnapshot {
		t.Fatalf("ohne Hooks = %v aus %q, want läuft aus dem Bildschirm", resolved.Status.Label(), resolved.Source)
	}
}

func TestHookReportJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	report := claudeHookReport(HookStateDone, now)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Meldung serialisieren: %v", err)
	}
	var decoded HookReport
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Meldung lesen: %v", err)
	}
	if decoded != report {
		t.Fatalf("Meldung verändert sich beim Schreiben und Lesen: %#v", decoded)
	}
}
