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
}
