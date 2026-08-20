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

Changing a Session's display name is also a LifecycleTransition. The old and
target RuntimeNames are persisted before tmux is touched, and reconciliation
uses the observed old/target postcondition to finish the Registry change without
blindly replaying an ambiguous rename. An offline Session keeps its opaque
RuntimeName; display identity never reconstructs runtime identity.

Initial prompt delivery is recorded as an explicit applied fact. If delivery is
unknown after a process or transport failure, reconciliation never sends the
prompt automatically again: prompt delivery is not idempotent, so replay could
duplicate work or instructions. A deliberate user retry creates a new intent.
