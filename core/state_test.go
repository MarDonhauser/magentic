package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func useTempState(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", p)
	return p
}

func TestLoadStateRescuesOverlappedWrite(t *testing.T) {
	p := useTempState(t)
	good := &State{
		Projects: []Project{{Name: "req.pilot", Path: "/tmp/req.pilot"}},
		Agents:   []Agent{{Name: "hera", Project: "req.pilot", Dir: "/tmp/req.pilot"}},
	}
	data, _ := json.MarshalIndent(good, "", "  ")
	// Genau das Schadensbild aus der Praxis: gültiges Objekt, danach der
	// Schwanz eines längeren Schreibvorgangs.
	broken := append(data, []byte(`n_at": "2026-08-03T10:42:33+02:00"`+"\n    }\n  ]\n}")...)
	if err := os.WriteFile(p, broken, 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := LoadState()
	if err != nil {
		t.Fatalf("beschädigte Datei nicht gerettet: %v", err)
	}
	if len(st.Agents) != 1 || st.Agents[0].Name != "hera" {
		t.Fatalf("geretteter Stand falsch: %+v", st.Agents)
	}
	if len(st.Projects) != 1 {
		t.Fatalf("Projekte verloren: %+v", st.Projects)
	}

	// Die Rettung muss die Datei auch gleich wieder heilen.
	again, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(again, &State{}); err != nil {
		t.Fatalf("state.json wurde nicht repariert: %v", err)
	}
}

func TestLoadStateRejectsGarbage(t *testing.T) {
	p := useTempState(t)
	if err := os.WriteFile(p, []byte("das ist kein json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(); err == nil {
		t.Fatal("völlig kaputte Datei muss einen Fehler liefern, nicht stillschweigend leeren State")
	}
}

func TestLoadStateMissingFile(t *testing.T) {
	useTempState(t)
	st, err := LoadState()
	if err != nil {
		t.Fatalf("fehlende Datei ist kein Fehler: %v", err)
	}
	if len(st.Agents) != 0 || len(st.Projects) != 0 {
		t.Fatal("erwartet leerer State")
	}
}

func TestDockIstTerminalAberKeineSitzung(t *testing.T) {
	dock := Agent{Name: "term-magentic", Kind: KindDock}
	term := Agent{Name: "term-x", Kind: KindTerm}
	claude := Agent{Name: "hera"}

	if !dock.IsTerm() {
		t.Error("Dock-Terminal muss als Terminal gelten — sonst wird es beim Wiederherstellen als Claude gestartet")
	}
	if !dock.IsDock() {
		t.Error("IsDock muss für Dock-Terminals true sein")
	}
	if term.IsDock() {
		t.Error("gewöhnliches Terminal darf nicht als Dock gelten — es gehört in die Sitzungsliste")
	}
	if claude.IsTerm() || claude.IsDock() {
		t.Error("Claude-Session ist weder Terminal noch Dock")
	}
}

func TestSessionRuntimeNameIsNeverReconstructedFromDisplayName(t *testing.T) {
	session := Session{Name: "display-only"}
	if got := session.TmuxName(); got != "" {
		t.Fatalf("TmuxName() reconstructed mutable display identity: %q", got)
	}
}

func TestAgentStatusPersistedLabelsRoundTrip(t *testing.T) {
	statuses := []AgentStatus{
		StatusUnknown, StatusRunning, StatusAgents, StatusShell, StatusBlocked,
		StatusDone, StatusIdle, StatusExited, StatusDead, StatusTerm,
	}
	seen := map[string]AgentStatus{}
	for _, status := range statuses {
		label := status.PersistedLabel()
		if other, dup := seen[label]; dup && status != StatusUnknown {
			t.Fatalf("PersistedLabel %q is shared by %v and %v", label, other, status)
		}
		seen[label] = status
		if back := AgentStatusFromPersistedLabel(label); back != status {
			t.Fatalf("PersistedLabel(%v) = %q round-trips to %v", status, label, back)
		}
	}
	if StatusUnknown.PersistedLabel() != "" {
		t.Fatalf("unknown status must persist as absent, got %q", StatusUnknown.PersistedLabel())
	}
	for _, label := range []string{"", "gelaufen", "RUNNING", "dead ", "  "} {
		if got := AgentStatusFromPersistedLabel(label); got != StatusUnknown {
			t.Fatalf("AgentStatusFromPersistedLabel(%q) = %v, want unknown", label, got)
		}
	}
}

func TestSessionLastStatusPersistsAsStableString(t *testing.T) {
	at := time.Date(2026, 9, 2, 20, 14, 0, 0, time.UTC)
	session := Session{
		ID: "session-1", Name: "hera", RuntimeName: "mgt-hera",
		Dir: "/work/hera", CreatedAt: at,
		LastStatus: StatusBlocked, LastStatusAt: at,
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["last_status"] != "blocked" {
		t.Fatalf("last_status = %v, want stable string \"blocked\": %s", raw["last_status"], data)
	}
	var back Session
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.LastStatus != StatusBlocked || !back.LastStatusAt.Equal(at) {
		t.Fatalf("round-trip lost last status: %+v", back)
	}
}

func TestSessionRecordWithoutRuntimeReadsAsTmux(t *testing.T) {
	// Genau das Format, das vor diesem Change geschrieben wurde: kein
	// runtime-Feld.
	data := []byte(`{"id":"session-1","name":"hera","runtime_name":"mgt-hera",` +
		`"dir":"/work/hera","created_at":"2026-09-02T20:14:00Z"}`)
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	if session.SessionRuntime() != RuntimeTmux {
		t.Fatalf("SessionRuntime() = %v, want tmux for a record with no runtime field", session.SessionRuntime())
	}
}

func TestSessionRuntimeRoundTrips(t *testing.T) {
	session := Session{ID: "session-1", Name: "hera", RuntimeName: "mgt-hera", Runtime: RuntimeManaged}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["runtime"] != "managed" {
		t.Fatalf("runtime = %v, want \"managed\": %s", raw["runtime"], data)
	}
	var back Session
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.SessionRuntime() != RuntimeManaged {
		t.Fatalf("round-trip lost runtime: %+v", back)
	}
}

func TestLoadStateWrittenBeforeRuntimeExistedReadsEveryAgentAsTmux(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	pre := []byte(`{"projects":[],"agents":[` +
		`{"id":"session-1","name":"hera","runtime_name":"mgt-hera","dir":"/work/hera","created_at":"2026-09-02T20:14:00Z"},` +
		`{"id":"session-2","name":"zeta","runtime_name":"mgt-zeta","dir":"/work/zeta","created_at":"2026-09-02T20:15:00Z"}` +
		`]}`)
	if err := os.WriteFile(statePath, pre, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGENTIC_STATE", statePath)
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(state.Agents))
	}
	for _, agent := range state.Agents {
		if agent.SessionRuntime() != RuntimeTmux {
			t.Fatalf("agent %q runtime = %v, want tmux", agent.Name, agent.SessionRuntime())
		}
	}
}

func TestRuntimeActionsPerRuntime(t *testing.T) {
	tests := []struct {
		runtime AgentRuntime
		want    []string
	}{
		{runtime: RuntimeTmux, want: []string{RuntimeActionAttach}},
		{runtime: RuntimeManaged, want: []string{RuntimeActionInterrupt, RuntimeActionAnswerPermission}},
		// An absent runtime field reads as tmux everywhere else; the action
		// list must agree.
		{runtime: "", want: []string{RuntimeActionAttach}},
	}
	for _, tt := range tests {
		t.Run(string(tt.runtime)+"-empty-means-tmux", func(t *testing.T) {
			got := RuntimeActionsFor(tt.runtime)
			if len(got) != len(tt.want) {
				t.Fatalf("RuntimeActionsFor(%q) = %v, want %v", tt.runtime, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("RuntimeActionsFor(%q) = %v, want %v", tt.runtime, got, tt.want)
				}
			}
		})
	}
	if RuntimeOffersAction(RuntimeManaged, RuntimeActionAttach) {
		t.Fatal("a managed Session must not offer attach")
	}
	if RuntimeOffersAction(RuntimeTmux, RuntimeActionInterrupt) {
		t.Fatal("a tmux Session must not offer interrupt")
	}
	if RuntimeOffersAction(RuntimeTmux, RuntimeActionAnswerPermission) {
		t.Fatal("a tmux Session must not offer answering a permission decision")
	}
	if !RuntimeOffersAction(RuntimeManaged, RuntimeActionInterrupt) {
		t.Fatal("a managed Session must offer interrupt")
	}
}

func TestSessionWithoutLastStatusReadsAsNeverObserved(t *testing.T) {
	// Genau das Format, das vor diesem Change geschrieben wurde: kein
	// last_status, kein last_status_at.
	data := []byte(`{"id":"session-1","name":"hera","runtime_name":"mgt-hera",` +
		`"dir":"/work/hera","created_at":"2026-09-02T20:14:00Z"}`)
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	if session.LastStatus != StatusUnknown {
		t.Fatalf("LastStatus = %v, want unknown", session.LastStatus)
	}
	if !session.LastStatusAt.IsZero() {
		t.Fatalf("LastStatusAt = %v, want zero", session.LastStatusAt)
	}
	if session.LastStatus == StatusIdle || session.LastStatus == StatusDead || session.LastStatus == StatusRunning {
		t.Fatal("a never-observed Session must not read as idle, running, or dead")
	}
}
