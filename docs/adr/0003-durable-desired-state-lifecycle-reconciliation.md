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
