package core

import (
	"fmt"
	"os/exec"
	"runtime"
)

var Notifier func(title, message, sound string)

func NotifyDesktop(title, message, sound string) {
	if Notifier != nil {
		Notifier(title, message, sound)
		return
	}
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf("display notification %q with title %q sound name %q", message, title, sound)
		exec.Command("osascript", "-e", script).Start()
	case "linux":
		exec.Command("notify-send", title, message).Start()
	}
}

func StatusRank(s AgentStatus) int {
	switch s {
	case StatusBlocked:
		return 0
	case StatusRunning:
		return 1
	case StatusAgents:
		return 2
	case StatusIdle:
		return 3
	case StatusExited:
		return 4
	case StatusDead:
		return 5
	}
	return 6
}
