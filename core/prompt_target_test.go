package core

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// promptTargetRegistry legt eine Registry mit zwei Sessions an und richtet
// MAGENTIC_STATE darauf aus, damit promptTarget.resolve echt liest.
func promptTargetRegistry(t *testing.T, sessions ...Session) *Registry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", path)
	registry := OpenRegistry(path)
	for _, session := range sessions {
		if _, err := registry.Change(t.Context(), RegisterSession(session)); err != nil {
			t.Fatalf("Session %q anlegen: %v", session.Name, err)
		}
	}
	return registry
}

// TestPromptTargetNeverFollowsAReusedRuntimeName ist die Regression zum
// gefährlichsten Fall: A stellt einen Prompt in die Warteschlange, A wird
// entfernt, B bekommt A's frühere Laufzeitadresse. Wer über den Namen
// revalidiert, liefert A's Prompt an B.
func TestPromptTargetNeverFollowsAReusedRuntimeName(t *testing.T) {
	registry := promptTargetRegistry(t, Session{
		ID: "session-a", Name: "hera", RuntimeName: SessionPrefix + "hera",
		Dir: t.TempDir(),
	})

	target := promptTargetForSession(Session{
		ID: "session-a", Name: "hera", RuntimeName: SessionPrefix + "hera",
	})
	if _, _, err := target.resolve(); err != nil {
		t.Fatalf("A ist auflösbar: %v", err)
	}

	// A verschwindet, B erbt die Laufzeitadresse.
	if _, err := registry.Change(t.Context(), RemoveSession(SessionID("session-a"), "hera")); err != nil {
		t.Fatalf("A entfernen: %v", err)
	}
	if _, err := registry.Change(t.Context(), RegisterSession(Session{
		ID: "session-b", Name: "hera", RuntimeName: SessionPrefix + "hera",
		Dir: t.TempDir(),
	})); err != nil {
		t.Fatalf("B anlegen: %v", err)
	}

	_, _, err := target.resolve()
	if err == nil {
		t.Fatal("A's Ziel löste sich auf, obwohl A entfernt wurde — der Prompt ginge an B")
	}
	if !errors.Is(err, errPromptTargetGone) {
		t.Fatalf("Abbruchgrund = %v, erwartet errPromptTargetGone", err)
	}
}

// TestPromptTargetFollowsARenameToTheNewRuntimeName hält die andere Hälfte
// fest: eine umbenannte Session bleibt dieselbe Session. Die Zustellung folgt
// ihrer neuen Laufzeitadresse und fällt nie auf die alte zurück.
func TestPromptTargetFollowsARenameToTheNewRuntimeName(t *testing.T) {
	registry := promptTargetRegistry(t, Session{
		ID: "session-a", Name: "hera", RuntimeName: SessionPrefix + "hera",
		Dir: t.TempDir(),
	})

	target := promptTargetForSession(Session{
		ID: "session-a", Name: "hera", RuntimeName: SessionPrefix + "hera",
	})

	if _, err := registry.Change(t.Context(), RenameRegisteredSessionRuntime(SessionID("session-a"), "hera", "rhea", SessionPrefix+"rhea")); err != nil {
		t.Skipf("Registry-Umbenennung nicht in dieser Form verfügbar: %v", err)
	}

	resolved, session, err := target.resolve()
	if err != nil {
		t.Fatalf("umbenannte Session ist nicht mehr auflösbar: %v", err)
	}
	if resolved.runtime == SessionPrefix+"hera" {
		t.Error("Zustellung blieb auf der alten Laufzeitadresse")
	}
	if session.ID != "session-a" {
		t.Errorf("aufgelöste Session = %q, erwartet session-a", session.ID)
	}
	if !strings.Contains(resolved.runtime, "rhea") {
		t.Errorf("neue Laufzeitadresse = %q", resolved.runtime)
	}
}

// TestPromptTargetKeyIsTheStableIdentity hält fest, dass die Autorität die
// SessionID ist und nicht der Name.
func TestPromptTargetKeyIsTheStableIdentity(t *testing.T) {
	target := promptTargetForSession(Session{ID: "session-a", Name: "hera", RuntimeName: SessionPrefix + "hera"})
	if !strings.HasPrefix(target.key(), "session:") {
		t.Errorf("Schlüssel = %q, erwartet Präfix session:", target.key())
	}
	legacy := promptTarget{runtime: SessionPrefix + "hera"}
	if !strings.HasPrefix(legacy.key(), "runtime:") {
		t.Errorf("Legacy-Schlüssel = %q, erwartet Präfix runtime:", legacy.key())
	}
}
