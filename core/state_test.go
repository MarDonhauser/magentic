package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func useTempState(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", p)
	return p
}

func TestSaveIsAtomicUnderConcurrency(t *testing.T) {
	p := useTempState(t)
	base := &State{
		Projects: []Project{{Name: "req.pilot", Path: "/tmp/req.pilot"}},
		Agents: []Agent{
			{Name: "hera", Project: "req.pilot", Dir: "/tmp/req.pilot"},
			{Name: "atlas", Project: "req.pilot", Dir: "/tmp/req.pilot"},
		},
	}
	if err := base.Save(); err != nil {
		t.Fatal(err)
	}

	// Zwei Zustände unterschiedlicher Länge parallel schreiben — genau die
	// Konstellation, die state.json früher zu ungültigem JSON verschränkt hat.
	long := &State{Projects: base.Projects}
	for i := 0; i < 60; i++ {
		long.Agents = append(long.Agents, Agent{
			Name:      fmt.Sprintf("agent-mit-langem-namen-%02d", i),
			Project:   "req.pilot",
			Dir:       "/tmp/req.pilot",
			CreatedAt: time.Now(),
			SeenAt:    time.Now(),
		})
	}
	short := &State{Projects: base.Projects, Agents: base.Agents[:1]}

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				long.Save()
			} else {
				short.Save()
			}
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var out State
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("state.json ist nach parallelem Speichern ungültig: %v\n%s", err, data)
	}
	if len(out.Agents) != 1 && len(out.Agents) != 60 {
		t.Fatalf("%d Agents — weder der kurze noch der lange Stand, also vermischt", len(out.Agents))
	}
}

func TestSaveLeavesNoTempFile(t *testing.T) {
	p := useTempState(t)
	if err := (&State{}).Save(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "state.json" && e.Name() != "state.json.lock" {
			t.Fatalf("unerwartete Datei übrig: %s", e.Name())
		}
	}
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

func TestMarkSeenThrottles(t *testing.T) {
	s := &State{Agents: []Agent{{Name: "hera"}}}
	if !s.MarkSeen("hera") {
		t.Fatal("erster Aufruf muss speichern wollen")
	}
	if s.MarkSeen("hera") {
		t.Fatal("direkt folgender Aufruf darf keinen Schreibvorgang auslösen")
	}
	s.Agents[0].SeenAt = time.Now().Add(-10 * time.Second)
	if !s.MarkSeen("hera") {
		t.Fatal("nach Ablauf der Sperre muss wieder gespeichert werden")
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
