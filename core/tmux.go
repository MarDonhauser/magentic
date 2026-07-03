package core

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
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

func TmuxHasSession(session string) bool {
	err := exec.Command("tmux", "has-session", "-t", TargetSession(session)).Run()
	return err == nil
}

func TmuxNewClaudeSession(session, dir string, extraArgs string) error {
	if _, err := Tmux("new-session", "-d", "-s", session, "-c", dir, "-x", "220", "-y", "50"); err != nil {
		return fmt.Errorf("tmux new-session: %w", err)
	}
	cmd := "claude"
	if extraArgs != "" {
		cmd += " " + extraArgs
	}
	if _, err := Tmux("send-keys", "-t", TargetPane(session), "-l", cmd); err != nil {
		return err
	}
	_, err := Tmux("send-keys", "-t", TargetPane(session), "Enter")
	return err
}

func TmuxKillSession(session string) error {
	_, err := Tmux("kill-session", "-t", TargetSession(session))
	return err
}

func TmuxCapturePane(session string, scrollback int) string {
	args := []string{"capture-pane", "-p", "-t", TargetPane(session)}
	if scrollback > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", scrollback))
	}
	out, err := Tmux(args...)
	if err != nil {
		return ""
	}
	return out
}

type PaneInfo struct {
	Command  string
	Activity time.Time
}

func TmuxPaneInfos() map[string]PaneInfo {
	out, err := Tmux("list-panes", "-a", "-F", "#{session_name}\t#{pane_current_command}\t#{window_activity}")
	if err != nil {
		return nil
	}
	m := map[string]PaneInfo{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		info := PaneInfo{Command: parts[1]}
		if len(parts) == 3 {
			if ts, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64); err == nil && ts > 0 {
				info.Activity = time.Unix(ts, 0)
			}
		}
		m[parts[0]] = info
	}
	return m
}

func TmuxListSessions() []string {
	out, err := Tmux("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	var sessions []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(line, SessionPrefix) {
			sessions = append(sessions, line)
		}
	}
	return sessions
}
