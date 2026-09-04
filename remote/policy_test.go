package remote

import (
	"errors"
	"testing"
)

// Jede Methode der Nahtstelle ist klassifiziert — eine Methode ohne
// Entscheidung lässt diesen Test fehlschlagen.
func TestPolicyCoversEveryMethod(t *testing.T) {
	for _, name := range HostAPIMethods {
		entry, known := Classify(name)
		if !known {
			t.Errorf("%s ist nicht klassifiziert", name)
			continue
		}
		if entry.Class != ActionPermitted && entry.Class != ActionRestricted {
			t.Errorf("%s hat ungültige Klasse %q", name, entry.Class)
		}
		if entry.Reason == "" {
			t.Errorf("%s nennt keinen Grund", name)
		}
	}
	if len(RemoteActionPolicy) != len(HostAPIMethods) {
		t.Errorf("Policy hat %d Einträge für %d Methoden — beide Seiten müssen übereinstimmen",
			len(RemoteActionPolicy), len(HostAPIMethods))
	}
}

// Die Spec-Aufteilung ist der Vertrag: Beobachten und Session-Anlage sind
// erlaubt, Worktree-/Project-Entfernung, Pfad-Registrierung und Kill sind
// beschränkt.
func TestPolicyDefaultSplit(t *testing.T) {
	permitted := []string{
		"Overview", "Board", "GitGraph", "Stats",
		"OpenTerm", "WriteTerm", "ResizeTerm", "CloseTerm",
		"SendMessage", "SendSkill", "NewSession", "MarkSeen",
		"StartBoardItem",
	}
	for _, name := range permitted {
		entry, known := Classify(name)
		if !known || entry.Class != ActionPermitted {
			t.Errorf("%s sollte erlaubt sein", name)
		}
	}
	restricted := []string{
		"RemoveWorktree", "RemoveProject", "AddProject", "KillSession",
	}
	for _, name := range restricted {
		entry, known := Classify(name)
		if !known || entry.Class != ActionRestricted {
			t.Errorf("%s sollte beschränkt sein", name)
		}
	}
}

func TestEnforceRemote(t *testing.T) {
	if err := EnforceRemote("Overview", nil); err != nil {
		t.Errorf("erlaubte Methode verweigert: %v", err)
	}
	if err := EnforceRemote("RemoveWorktree", nil); err == nil {
		t.Error("beschränkte Methode ohne Opt-in erlaubt")
	} else {
		var restricted *RestrictedError
		if !errors.As(err, &restricted) {
			t.Errorf("kein RestrictedError, sondern %T", err)
		}
	}
	if err := EnforceRemote("RemoveWorktree", map[string]bool{"RemoveWorktree": true}); err != nil {
		t.Errorf("beschränkte Methode trotz Host-Opt-in verweigert: %v", err)
	}
	if err := EnforceRemote("GibtEsNicht", nil); err == nil {
		t.Error("unbekannte Methode erlaubt — fail-closed verletzt")
	}
}

func TestRejectClientPath(t *testing.T) {
	rejected := []string{
		"/etc/passwd", "/Users/x/magentic", "~/magentic",
		"C:\\Windows\\System32", `\\host\share`,
		"../../etc", "agent/../../../etc",
		"project/with/slash", `project\with\backslash`,
		"a\x00b",
	}
	for _, value := range rejected {
		if err := RejectClientPath(value); err == nil {
			t.Errorf("Pfad-Eingabe %q wurde akzeptiert", value)
		}
	}
	accepted := []string{
		"", "   ", "wt_a1b2c3d4", "session-name", "name mit leerzeichen",
		"0f3b2a11-22aa-33bb-44cc-55dd66ee77ff",
	}
	for _, value := range accepted {
		if err := RejectClientPath(value); err != nil {
			t.Errorf("Handle %q wurde abgewiesen: %v", value, err)
		}
	}
}
