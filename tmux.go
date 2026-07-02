package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const sessionPrefix = "mgt-"

func tmuxSessionName(agentName string) string {
	return sessionPrefix + agentName
}

func targetSession(session string) string {
	return "=" + session
}

func targetPane(session string) string {
	return "=" + session + ":"
}

func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return string(out), err
}

func TmuxHasSession(session string) bool {
	err := exec.Command("tmux", "has-session", "-t", targetSession(session)).Run()
	return err == nil
}

func TmuxNewClaudeSession(session, dir string, extraArgs string) error {
	if _, err := tmux("new-session", "-d", "-s", session, "-c", dir, "-x", "220", "-y", "50"); err != nil {
		return fmt.Errorf("tmux new-session: %w", err)
	}
	cmd := "claude"
	if extraArgs != "" {
		cmd += " " + extraArgs
	}
	if _, err := tmux("send-keys", "-t", targetPane(session), "-l", cmd); err != nil {
		return err
	}
	_, err := tmux("send-keys", "-t", targetPane(session), "Enter")
	return err
}

func TmuxKillSession(session string) error {
	_, err := tmux("kill-session", "-t", targetSession(session))
	return err
}

func TmuxCapturePane(session string, scrollback int) string {
	args := []string{"capture-pane", "-p", "-t", targetPane(session)}
	if scrollback > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", scrollback))
	}
	out, err := tmux(args...)
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
	out, err := tmux("list-panes", "-a", "-F", "#{session_name}\t#{pane_current_command}\t#{window_activity}")
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
	out, err := tmux("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	var sessions []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(line, sessionPrefix) {
			sessions = append(sessions, line)
		}
	}
	return sessions
}
