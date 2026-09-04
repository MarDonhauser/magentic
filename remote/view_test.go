package remote

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"magentic/core"
)

// Ein Griff ohne Verfügbarkeit kompiliert nicht: testdata/blindread baut
// nicht, weil payload unexportiert ist.
func TestBlindReadDoesNotCompile(t *testing.T) {
	command := exec.Command("go", "build", "./remote/testdata/blindread")
	command.Dir = ".."
	out, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("Verfügbarkeits-blinder Griff kompiliert — Kapselung gebrochen")
	}
	if !strings.Contains(string(out), "payload") {
		t.Errorf("falscher Baufehler: %s", out)
	}
}

// Getrennt bleibt die letzte bekannte Sicht stehen — mit Alter etikettiert,
// nie als beendet, tot oder idle umgedeutet, nie leer.
func TestDisconnectedRendersLastKnown(t *testing.T) {
	known := []SessionDigest{
		{ID: "a", Name: "hera", Status: "läuft"},
		{ID: "b", Name: "atlas", Status: "wartet"},
	}
	// Aufsicht: Auch 90s später steht noch dieselbe Liste.
	view := LastKnownView(known, 90*time.Second, "Host nicht erreichbar")
	lines, notice := SidebarSessions(view, "atelier")
	if len(lines) != 2 {
		t.Fatalf("letzte bekannte Sessions verloren: %v", lines)
	}
	if !strings.Contains(lines[0], "hera") || !strings.Contains(lines[1], "atlas") {
		t.Errorf("Sessions umgedeutet: %v", lines)
	}
	for _, line := range lines {
		for _, forbidden := range []string{"beendet", "tot", "idle", "dead", "finished"} {
			if strings.Contains(strings.ToLower(line), forbidden) {
				t.Errorf("Abriss als %q gerendert: %q", forbidden, line)
			}
		}
	}
	if !strings.Contains(notice, "Letztbekannt") || !strings.Contains(notice, "90s") && !strings.Contains(notice, "1m") {
		t.Errorf("Etikett fehlt Alter/Stand: %q", notice)
	}

	board, boardNotice := BoardColumns(
		LastKnownView(map[string][]string{"Inbox": {"Spec-1"}}, time.Minute, "Host nicht erreichbar"), "atelier")
	if len(board["Inbox"]) != 1 {
		t.Error("Board-Spalte als leer gerendert")
	}
	if !strings.Contains(boardNotice, "unbekannt") {
		t.Errorf("Board verschweigt Unwissen: %q", boardNotice)
	}

	totals, statsNotice := StatsTotals(
		LastKnownView(map[string]int{"sessions": 2}, time.Minute, "Host nicht erreichbar"), "atelier")
	if totals["sessions"] != 2 {
		t.Error("Summe als Null gerendert")
	}
	if !strings.Contains(statsNotice, "unbekannt") {
		t.Errorf("Statistik verschweigt Unwissen: %q", statsNotice)
	}
}

// Destruktives braucht frische Fakten — dieselbe Regel wie lokal, keine
// zweite. Nach frischer Sicht geht es ohne App-Neustart wieder.
func TestDestructiveGateBothDirections(t *testing.T) {
	stale := core.ObservationSnapshot{Availability: core.ObservationUnavailable}
	if err := RequireFreshFacts(stale); err == nil {
		t.Error("stale Sicht lässt Zerstörung zu")
	}
	available, fresh := ActionAvailable(
		map[string]PolicyEntry{"RemoveWorktree": {Class: ActionPermitted}}, "RemoveWorktree", false)
	if available || !strings.Contains(fresh, "frische Fakten") {
		t.Errorf("Gate offen ohne Frische: %v %q", available, fresh)
	}
	freshSnapshot := core.ObservationSnapshot{Availability: core.ObservationAvailable}
	if err := RequireFreshFacts(freshSnapshot); err != nil {
		t.Fatalf("frische Sicht blockiert: %v", err)
	}
	available, _ = ActionAvailable(
		map[string]PolicyEntry{"RemoveWorktree": {Class: ActionPermitted}}, "RemoveWorktree", true)
	if !available {
		t.Error("frische Sicht gibt nicht frei")
	}
	// Lesen braucht keine Frische.
	if available, _ := ActionAvailable(
		map[string]PolicyEntry{}, "Overview", false); !available {
		t.Error("Lesen trotz stale blockiert")
	}
}

// Beschränktes bleibt beschränkt; eine Host-Verweigerung schlägt den
// gecachten Policy-Stand.
func TestRestrictedPresentationAndAuthoritativeRefusal(t *testing.T) {
	available, reason := ActionAvailable(
		map[string]PolicyEntry{"RemoveWorktree": {Class: ActionRestricted, Reason: "Host-Opt-in fehlt"}},
		"RemoveWorktree", true)
	if available || !strings.Contains(reason, "beschränkt") {
		t.Errorf("beschränkte Aktion angeboten: %v %q", available, reason)
	}
	// Veralteter Cache erlaubt, Host verweigert: Host gewinnt und pflegt nach.
	policy := map[string]PolicyEntry{"KillSession": {Class: ActionPermitted}}
	client := NewClient(ClientConfig{Link: HostLink{Name: "x", Address: "h", CredentialRef: "c"}})
	client.policy = policy
	refusal := client.noteWireError("KillSession", &WireError{Code: ErrorRestricted, Message: "braucht Host-Opt-in"})
	if _, ok := refusal.(*RestrictedError); !ok {
		t.Fatalf("kein RestrictedError: %T", refusal)
	}
	if entry := client.Policy()["KillSession"]; entry.Class != ActionRestricted {
		t.Errorf("Cache nicht nachgepflegt: %+v", entry)
	}
	available, _ = ActionAvailable(client.Policy(), "KillSession", true)
	if available {
		t.Error("Verweigerung nicht übernommen")
	}
}
