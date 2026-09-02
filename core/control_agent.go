package core

import (
	"fmt"
	"os"
)

// The environment marker Magentic exports into every Session runtime it
// provisions. MAGENTIC_ENV is the one cheap boolean an agent checks before
// deciding whether the rest is worth reading.
const (
	ControlEnvMarker      = "MAGENTIC_ENV"
	ControlEnvSocket      = "MAGENTIC_SOCKET"
	ControlEnvSessionID   = "MAGENTIC_SESSION_ID"
	ControlEnvProjectID   = "MAGENTIC_PROJECT_ID"
	ControlEnvWorktree    = "MAGENTIC_WORKTREE"
	ControlEnvWorktreeDir = "MAGENTIC_WORKTREE_DIR"
)

// ControlMarkerFromEnvironment reads the facts the caller's own runtime carries.
// They are a claim: the control API resolves them against the Registry.
func ControlMarkerFromEnvironment() ControlMarker {
	if os.Getenv(ControlEnvMarker) != "1" {
		return ControlMarker{}
	}
	return ControlMarker{
		SessionID: SessionID(os.Getenv(ControlEnvSessionID)),
		ProjectID: ProjectID(os.Getenv(ControlEnvProjectID)),
	}
}

// ControlRuntimeEnvironment is the marker Magentic exports into a Session
// runtime it provisions. An agent checks MAGENTIC_ENV first and stops there if
// it is absent; the remaining facts are what it needs to address the control
// API. A runtime Magentic did not provision — an adopted one included — never
// receives these variables.
func ControlRuntimeEnvironment(session Session) []string {
	worktree := "0"
	if session.Worktree {
		worktree = "1"
	}
	environment := []string{
		ControlEnvMarker + "=1",
		ControlEnvSocket + "=" + ControlSocketPath(),
		ControlEnvSessionID + "=" + string(session.ID),
		ControlEnvProjectID + "=" + string(session.ProjectID),
		ControlEnvWorktree + "=" + worktree,
	}
	if session.Worktree && session.Dir != "" {
		environment = append(environment, ControlEnvWorktreeDir+"="+session.Dir)
	}
	return environment
}

// controlEnvironmentArgs turns the marker into the tmux flags that carry it
// into the new runtime.
func controlEnvironmentArgs(session Session) []string {
	var args []string
	for _, variable := range ControlRuntimeEnvironment(session) {
		args = append(args, "-e", variable)
	}
	return args
}

// controlMarkerDescription is what an unresolvable marker is reported as.
func controlMarkerDescription(marker ControlMarker) string {
	switch {
	case marker.SessionID != "":
		return fmt.Sprintf("SessionID %s", marker.SessionID)
	case marker.ProjectID != "":
		return fmt.Sprintf("ProjectID %s", marker.ProjectID)
	}
	return "keine Marker-Angaben"
}
