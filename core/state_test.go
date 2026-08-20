package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
