package core

import "os/exec"

// Der Transport einer Session-Runtime: anhängen, hineinwechseln, Tasten
// senden, Darstellung vorbereiten. Er liegt hier, damit keine Oberfläche
// tmux-argv hält — sonst lässt sich die Runtime nicht durch Austausch eines
// Adapters ersetzen, sondern nur durch Nacharbeit in Desktop und TUI.

// SessionAttachment ist der fertige Befehl, mit dem eine Oberfläche eine
// Session-Runtime anhängt. Die Oberfläche startet ihn, sie baut ihn nicht.
type SessionAttachment struct {
	Program string
	Args    []string
	Env     []string
}

// Command baut den Befehl. Env bleibt leer, wenn der Aufrufer die Umgebung
// erbt.
func (a SessionAttachment) Command() *exec.Cmd {
	cmd := exec.Command(a.Program, a.Args...)
	if len(a.Env) > 0 {
		cmd.Env = append([]string(nil), a.Env...)
	}
	return cmd
}

// PrepareSessionPresentation richtet die Runtime für die Anzeige in einer
// eingebetteten Oberfläche her: keine eigene Statuszeile, Größe folgt dem
// zuletzt angehängten Client.
func PrepareSessionPresentation(runtimeName string) {
	Tmux("set-option", "-t", runtimeName, "status", "off")
	Tmux("set-option", "-w", "-t", runtimeName+":", "window-size", "latest")
}

// AttachSessionCommand hängt eine Session in einem eigenen Terminal an. Die
// Umgebung lässt TMUX bewusst weg: ein verschachteltes Anhängen wäre kein
// Anhängen, sondern ein Fehler. Ein leerer term erbt das Terminal des
// Aufrufers, statt ihm eines aufzuzwingen.
func AttachSessionCommand(runtimeName, term string) SessionAttachment {
	env := EnvWithout("TMUX")
	if term != "" {
		env = append(env, "TERM="+term)
	}
	return SessionAttachment{
		Program: "tmux",
		Args:    []string{"attach-session", "-t", TargetSession(runtimeName)},
		Env:     env,
	}
}

// SwitchToSessionCommand wechselt innerhalb eines bereits angehängten Clients
// zu einer Session, statt eine zweite Anhängung aufzumachen.
func SwitchToSessionCommand(runtimeName string) SessionAttachment {
	return SessionAttachment{
		Program: "tmux",
		Args:    []string{"switch-client", "-t", TargetSession(runtimeName)},
	}
}

// SendSessionKeys schickt Tasten an die Eingabe einer Session-Runtime.
func SendSessionKeys(runtimeName string, keys ...string) error {
	args := append([]string{"send-keys", "-t", TargetPane(runtimeName)}, keys...)
	_, err := Tmux(args...)
	return err
}
