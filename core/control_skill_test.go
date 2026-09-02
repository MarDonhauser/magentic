package core

import (
	"os"
	"strings"
	"testing"
)

func TestControlSkillNamesEveryVerbAndOutcome(t *testing.T) {
	document := ControlSkillDocument()
	if strings.TrimSpace(document) == "" {
		t.Fatal("Die Anleitung für Agents fehlt")
	}
	for _, verb := range ControlVerbs() {
		if !strings.Contains(document, string(verb)) {
			t.Fatalf("Die Anleitung nennt das Verb %q nicht", verb)
		}
	}
	for _, outcome := range ControlOutcomes() {
		if !strings.Contains(document, string(outcome)) {
			t.Fatalf("Die Anleitung nennt den Ergebnis-Code %q nicht", outcome)
		}
	}
	// The marker guard opens the document, and the delegation pattern is in it.
	if !strings.Contains(document, ControlEnvMarker) {
		t.Fatalf("Die Anleitung nennt den Marker %q nicht", ControlEnvMarker)
	}
	for _, fact := range []string{ControlEnvSocket, ControlEnvSessionID, ControlEnvProjectID, ControlEnvWorktree} {
		if !strings.Contains(document, fact) {
			t.Fatalf("Die Anleitung nennt %q nicht", fact)
		}
	}
	for _, needle := range []string{"--new-worktree", "--until done", "--lines"} {
		if !strings.Contains(document, needle) {
			t.Fatalf("Das Delegationsmuster nennt %q nicht", needle)
		}
	}
}

func TestInstallControlSkillIsIdempotent(t *testing.T) {
	project := Project{ID: "projekt-a", Name: "alpha", Path: t.TempDir()}
	changed, err := InstallControlSkill(project)
	if err != nil || !changed {
		t.Fatalf("Erstinstallation = %v (%v)", changed, err)
	}
	first, err := os.ReadFile(ControlSkillPath(project))
	if err != nil {
		t.Fatalf("Die Anleitung wurde nicht abgelegt: %v", err)
	}
	if !strings.Contains(string(first), "Magentic-Steuer-API") {
		t.Fatalf("Die abgelegte Datei trägt die Anleitung nicht: %q", first)
	}

	changed, err = InstallControlSkill(project)
	if err != nil {
		t.Fatalf("Zweitinstallation: %v", err)
	}
	if changed {
		t.Fatal("Die zweite Installation hat die Datei verändert")
	}
	second, _ := os.ReadFile(ControlSkillPath(project))
	if string(second) != string(first) {
		t.Fatal("Die zweite Installation hat den Inhalt verdoppelt")
	}
	if count := strings.Count(string(second), controlSkillStart); count != 1 {
		t.Fatalf("Die Anleitung steht %d mal in der Datei", count)
	}
}

func TestInstallControlSkillKeepsTheDevelopersText(t *testing.T) {
	project := Project{ID: "projekt-a", Name: "alpha", Path: t.TempDir()}
	own := "# Alpha\n\nBitte immer die Tests laufen lassen.\n"
	if err := os.WriteFile(ControlSkillPath(project), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallControlSkill(project); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(ControlSkillPath(project))
	if !strings.Contains(string(written), "Bitte immer die Tests laufen lassen.") {
		t.Fatalf("Der eigene Text ging verloren: %q", written)
	}
	if _, err := InstallControlSkill(project); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(ControlSkillPath(project))
	if string(again) != string(written) {
		t.Fatal("Die zweite Installation hat die Datei verändert")
	}
	if count := strings.Count(string(again), controlSkillEnd); count != 1 {
		t.Fatalf("Die Anleitung steht %d mal in der Datei", count)
	}
}
