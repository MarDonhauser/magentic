package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type focusTickMsg time.Time
type focusPreviewMsg string

var tmuxKeyNames = map[string]string{
	"enter": "Enter", "tab": "Tab", "shift+tab": "BTab", "backspace": "BSpace",
	"delete": "DC", "esc": "Escape", "up": "Up", "down": "Down",
	"left": "Left", "right": "Right", "home": "Home", "end": "End",
	"pgup": "PageUp", "pgdown": "PageDown", "insert": "IC",
	"shift+up": "S-Up", "shift+down": "S-Down", "shift+left": "S-Left", "shift+right": "S-Right",
	"f1": "F1", "f2": "F2", "f3": "F3", "f4": "F4", "f5": "F5", "f6": "F6",
	"f7": "F7", "f8": "F8", "f9": "F9", "f10": "F10", "f11": "F11", "f12": "F12",
}

func forwardKeyToSession(session string, msg tea.KeyMsg) {
	target := targetPane(session)
	s := msg.String()
	if name, ok := tmuxKeyNames[s]; ok {
		tmux("send-keys", "-t", target, name)
		return
	}
	if strings.HasPrefix(s, "ctrl+") && len(s) == len("ctrl+")+1 {
		tmux("send-keys", "-t", target, "C-"+s[len("ctrl+"):])
		return
	}
	if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
		text := string(msg.Runes)
		if msg.Type == tea.KeySpace {
			text = " "
		}
		if len(msg.Runes) > 8 {
			tmux("set-buffer", "-b", "magentic-paste", "--", text)
			tmux("paste-buffer", "-p", "-d", "-b", "magentic-paste", "-t", target)
			return
		}
		args := []string{"send-keys", "-t", target, "-H"}
		if msg.Alt {
			args = append(args, "1b")
		}
		for _, b := range []byte(text) {
			args = append(args, fmt.Sprintf("%02x", b))
		}
		tmux(args...)
	}
}

func TmuxCapturePaneANSI(session string) string {
	out, err := tmux("capture-pane", "-e", "-p", "-t", targetPane(session))
	if err != nil {
		return ""
	}
	return out
}

func focusTick() tea.Cmd {
	return tea.Tick(400*time.Millisecond, func(t time.Time) tea.Msg { return focusTickMsg(t) })
}

func focusPollCmd(name string) tea.Cmd {
	return func() tea.Msg {
		return focusPreviewMsg(TmuxCapturePaneANSI(tmuxSessionName(name)))
	}
}

func (m model) focusPaneSize() (w, h int) {
	_, detailW, innerH := m.layout()
	return detailW - 4, innerH - 2
}

func (m *model) resizeFocusedWindow() {
	if m.focusAgent == "" || m.width == 0 {
		return
	}
	w, h := m.focusPaneSize()
	if w < 20 || h < 5 {
		return
	}
	tmux("resize-window", "-t", targetPane(tmuxSessionName(m.focusAgent)), "-x", fmt.Sprintf("%d", w), "-y", fmt.Sprintf("%d", h))
}

func (m model) enterFocus() (tea.Model, tea.Cmd) {
	a := m.selectedAgent()
	if a == nil {
		return m, nil
	}
	sn := tmuxSessionName(a.Name)
	if !TmuxHasSession(sn) {
		m.setFlash("Session existiert nicht mehr — mit x entfernen oder n neu starten", true)
		return m, nil
	}
	m.focusAgent = a.Name
	m.focusPreview = ""
	m.resizeFocusedWindow()
	return m, tea.Batch(focusPollCmd(a.Name), focusTick())
}

func (m model) exitFocus() (tea.Model, tea.Cmd) {
	if m.focusAgent != "" {
		sn := tmuxSessionName(m.focusAgent)
		if TmuxHasSession(sn) {
			tmux("set-option", "-w", "-t", targetPane(sn), "window-size", "latest")
		}
	}
	m.focusAgent = ""
	m.focusPreview = ""
	return m, m.pollNow()
}

func (m model) updateFocusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+q" {
		return m.exitFocus()
	}
	sn := tmuxSessionName(m.focusAgent)
	if !TmuxHasSession(sn) {
		return m.exitFocus()
	}
	forwardKeyToSession(sn, msg)
	return m, focusPollCmd(m.focusAgent)
}
