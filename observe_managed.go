package main

import (
	"context"

	"magentic/core"
)

// observeSessionsWithManaged reads tmux Sessions from tmux and managed
// Sessions from their agent hosts. No tmux command is issued for a managed
// Session; an unreachable daemon reads as unobservable, never as dead.
func observeSessionsWithManaged(ctx context.Context, sessions []core.Session) core.ObservationSnapshot {
	return core.ObserveWithManaged(ctx, sessions, core.Observe, core.DefaultManagedStateProvider)
}
