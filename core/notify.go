package core

import (
	"fmt"
	"os/exec"
	"runtime"
)

func NotifyDesktop(title, message, sound string) {
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
	case StatusIdle:
		return 2
	case StatusExited:
		return 3
	case StatusDead:
		return 4
	}
	return 5
}
