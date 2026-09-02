package core

import "os"

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
