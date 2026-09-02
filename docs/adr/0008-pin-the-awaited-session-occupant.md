# Pin the awaited Session occupant

`session wait` resolves the addressed Session once, at the start of the wait,
into an occupant identity: the durable SessionID, the exact RuntimeName then
addressed, and the vendor-qualified AgentRunRef then occupying it. That triple
is pinned for the whole wait and every later evaluation is made against it,
never against whatever currently carries the same name.

Each part is needed. A SessionID survives a runtime being destroyed and
recreated, so a wait pinned on it alone could be satisfied by a fresh agent in
the same Session. A RuntimeName is replaceable by design, since rename is a
lifecycle transition with old and target runtime names. An AgentRunRef is
vendor-scoped and absent for a terminal Session. Together they answer the only
question a waiting agent has: is the thing I am waiting for still the thing that
is there?

Any drift ends the wait with `occupant-replaced`, a terminal outcome and not a
retry, and a replacement reaching idle never satisfies the wait. An Observation
that is unavailable or partial satisfies no condition, so a tmux read that timed
out is never reported as an idle agent. A `done` wait whose occupant starts
needing human input ends with `blocked` rather than hanging until its timeout:
the common failure of unattended delegation is a permission prompt, and silently
absorbing it would make the verb untrustworthy.

A pending wait holds no Registry coordination and no Session transition. It is
evaluated from the observation pass the serving process already runs, so waiting
costs no second observation loop and never blocks the interfaces.
