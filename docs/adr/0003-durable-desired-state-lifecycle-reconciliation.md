# Reconcile Session Lifecycle from durable desired state

Session Lifecycle records a uniquely identified desired-state transition before
changing Git, the filesystem, or an external runtime, then advances it
idempotently and retains explicit partial-failure state for reconciliation.
These resources cannot participate in one transaction, so recovery rolls the
durable intent forward from observed facts instead of relying on rollback or
holding Registry coordination across external work.

Transitions for the same Session are serialized across processes and reread the
latest desired state while holding that coordination. A superseded transition
must not apply an older runtime side effect after a newer transition has already
converged.

Project removal and Session provisioning share a Project transition lock;
managed Worktree provisioning and removal additionally share a canonical,
symlink-aware Worktree lock before acquiring the Session lock. The fixed
Project → Worktree → Session order keeps fresh Registry resolution and the
external mutation in one scoped transition, so a concurrent provision cannot
create a runtime in a Worktree being removed and a stale ProjectID cannot leave
an orphan runtime or checkout.

Changing a Session's display name is also a LifecycleTransition. The old and
target RuntimeNames are persisted before tmux is touched, and reconciliation
uses the observed old/target postcondition to finish the Registry change without
blindly replaying an ambiguous rename. An offline Session keeps its opaque
RuntimeName; display identity never reconstructs runtime identity. RuntimeName
is validated as an exact scalar before any process probe or mutation, because
normalizing it could address a different external Session.

Initial prompt delivery is recorded as an explicit applied fact. If delivery is
unknown after a process or transport failure, reconciliation never sends the
prompt automatically again: prompt delivery is not idempotent, so replay could
duplicate work or instructions. A deliberate user retry creates a new intent.
