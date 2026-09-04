package core

import (
	"slices"
	"strings"
	"testing"
)

// TestAttachSessionCommandNeverNestsAnAttachment hält fest, warum das Anhängen
// hier liegt und nicht in der Oberfläche: TMUX muss aus der Umgebung fallen,
// sonst hängt sich ein Client in sich selbst.
func TestAttachSessionCommandNeverNestsAnAttachment(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")

	attachment := AttachSessionCommand("magentic-hera", "xterm-256color")
	if attachment.Program != "tmux" {
		t.Errorf("Programm = %q", attachment.Program)
	}
	want := []string{"attach-session", "-t", TargetSession("magentic-hera")}
	if !slices.Equal(attachment.Args, want) {
		t.Errorf("Args = %v, erwartet %v", attachment.Args, want)
	}
	for _, entry := range attachment.Env {
		if strings.HasPrefix(entry, "TMUX=") {
			t.Errorf("TMUX blieb in der Umgebung: %q", entry)
		}
	}
	if !slices.Contains(attachment.Env, "TERM=xterm-256color") {
		t.Error("TERM wurde nicht gesetzt")
	}
}

// TestAttachSessionCommandInheritsTerminalWhenUnspecified hält fest, dass ein
// leeres term das Terminal des Aufrufers erbt, statt ihm ein leeres TERM
// aufzuzwingen.
func TestAttachSessionCommandInheritsTerminalWhenUnspecified(t *testing.T) {
	for _, entry := range AttachSessionCommand("magentic-hera", "").Env {
		if strings.HasPrefix(entry, "TERM=") && entry == "TERM=" {
			t.Errorf("leeres TERM wurde gesetzt: %q", entry)
		}
	}
}

// TestSwitchToSessionCommandAddressesTheSessionNotThePane hält den Unterschied
// fest, der beim Zusammenbauen von argv in der Oberfläche leicht verrutscht.
func TestSwitchToSessionCommandAddressesTheSessionNotThePane(t *testing.T) {
	args := SwitchToSessionCommand("magentic-hera").Args
	want := []string{"switch-client", "-t", TargetSession("magentic-hera")}
	if !slices.Equal(args, want) {
		t.Errorf("Args = %v, erwartet %v", args, want)
	}
	if args[2] == TargetPane("magentic-hera") {
		t.Error("switch-client adressiert das Pane statt der Session")
	}
}
