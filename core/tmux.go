package core

import (
	"os"
	"os/exec"
)

var SessionPrefix = func() string {
	if p := os.Getenv("MAGENTIC_PREFIX"); p != "" {
		return p
	}
	return "mgt-"
}()

func SessionName(agentName string) string {
	return SessionPrefix + agentName
}

func TargetSession(session string) string {
	return "=" + session
}

func TargetPane(session string) string {
	return "=" + session + ":"
}

func Tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return string(out), err
}

// Ohne Maus-Modus ist die tmux-Historie aus xterm.js unerreichbar, und die
// xterm-Selektion überlebt Claudes ständige Redraws nicht. Mit Maus übernimmt
// tmux beides: Rad scrollt in den Copy-Mode, Auswahl landet beim Loslassen
// über copy-command im macOS-Clipboard.
func TmuxConfigureUX() {
	Tmux("set-option", "-g", "mouse", "on")
	Tmux("set-option", "-s", "copy-command", "pbcopy")
	Tmux("bind-key", "-T", "copy-mode", "MouseDragEnd1Pane", "send-keys", "-X", "copy-pipe-and-cancel")
	Tmux("bind-key", "-T", "copy-mode-vi", "MouseDragEnd1Pane", "send-keys", "-X", "copy-pipe-and-cancel")

	// Was tmux selbst zeichnet, gehört zur Oberfläche und nicht zum Default-Theme.
	// Sichtbar ist davon nur der Copy-Mode: die Auswahl, die Suchtreffer und die
	// Positionsmeldung. Die Werte sind --accent, --grid, --panel und --page aus
	// style.css. Sie stehen fest statt am Theme zu hängen, weil ein deckender
	// Akzentblock auf beiden Hintergründen liest und tmux die Optionen pro Server
	// führt, nicht pro angehängter Ansicht.
	Tmux("set-option", "-g", "mode-style", "bg=#37cfbd,fg=#20242b")
	Tmux("set-option", "-g", "copy-mode-match-style", "bg=#3a4149,fg=#e4e8ee")
	Tmux("set-option", "-g", "copy-mode-current-match-style", "bg=#37cfbd,fg=#20242b")
	Tmux("set-option", "-g", "message-style", "bg=#262b33,fg=#e4e8ee")

	// Ohne das wartet tmux nach jedem Escape auf eine mögliche Tastenfolge. In
	// einer Oberfläche, die die Tasten ohnehin selbst verteilt, ist das nur Trägheit.
	Tmux("set-option", "-g", "escape-time", "0")
}
