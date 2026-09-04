package core

import "testing"

func resolveTestState() *State {
	return &State{
		Projects: []Project{{ID: "p1", Name: "magentic", Path: "/tmp/magentic"}},
		Agents: []Session{
			{ID: "s1", Name: "hera", Presentation: SessionPresentationDock},
			{ID: "s2", Name: "zeus"},
		},
	}
}

// TestResolveSessionRefusesEmptyAndUnknownIdentity hält fest, dass die
// SessionID die Autorität ist: eine leere ID ist ein Fehler und keine
// Einladung, über den Namen zu suchen.
func TestResolveSessionRefusesEmptyAndUnknownIdentity(t *testing.T) {
	st := resolveTestState()

	if session, err := st.ResolveSession("  s1 "); err != nil || session.Name != "hera" {
		t.Fatalf("ResolveSession(s1) = %+v, %v", session, err)
	}
	if _, err := st.ResolveSession("   "); err == nil {
		t.Error("leere SessionID wurde aufgelöst")
	}
	if _, err := st.ResolveSession("hera"); err == nil {
		t.Error("ein Name wurde als SessionID aufgelöst")
	}
	if _, err := st.ResolveSession("s9"); err == nil {
		t.Error("unbekannte SessionID wurde aufgelöst")
	}
}

// TestResolveSessionTargetNeverFallsThroughFromStaleID hält den Grund fest,
// aus dem der Namensrückfall überhaupt existiert: persistierte Dock-Tabs von
// vor den stabilen IDs. Eine mitgelieferte, aber veraltete ID darf niemals auf
// eine Session durchfallen, die denselben Namen wiederverwendet.
func TestResolveSessionTargetNeverFallsThroughFromStaleID(t *testing.T) {
	st := resolveTestState()

	if _, err := st.ResolveSessionTarget("s9", "hera"); err == nil {
		t.Error("veraltete ID fiel auf den Legacy-Namen durch")
	}
	if session, err := st.ResolveSessionTarget("", "hera"); err != nil || session.ID != "s1" {
		t.Fatalf("Legacy-Dock-Tab = %+v, %v", session, err)
	}
	if _, err := st.ResolveSessionTarget("", "zeus"); err == nil {
		t.Error("eine Session ohne Dock-Präsentation wurde als Legacy-Tab aufgelöst")
	}
	if _, err := st.ResolveSessionTarget("", ""); err == nil {
		t.Error("leeres Ziel wurde aufgelöst")
	}
}

func TestResolveProjectRefusesEmptyAndUnknownIdentity(t *testing.T) {
	st := resolveTestState()

	if project, err := st.ResolveProject(" p1 "); err != nil || project.Name != "magentic" {
		t.Fatalf("ResolveProject(p1) = %+v, %v", project, err)
	}
	if _, err := st.ResolveProject(""); err == nil {
		t.Error("leere ProjectID wurde aufgelöst")
	}
	if _, err := st.ResolveProject("magentic"); err == nil {
		t.Error("ein Name wurde als ProjectID aufgelöst")
	}
}
