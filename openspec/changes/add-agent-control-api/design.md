## Context

See proposal.md — Why. What matters for the approach is the shape of what
already exists.

Magentic's shared logic lives in `core/` as a handful of deep modules: Registry
owns Projects and Sessions under stable IDs and applies every mutation as an
interprocess-coordinated semantic change (ADR 0002); Session Lifecycle records a
durable DesiredState before touching Git, the filesystem, or tmux and reconciles
partial execution (ADR 0003); Session Observation reads tmux in one time-bounded
pass and reports known, partial, or unavailable results (ADR 0004);
Repositories owns Git and Worktree meaning and resolves opaque WorktreeRefs
freshly. Two interfaces — the Bubbletea TUI in the root package and the Wails
app in `app/` — are projections over those modules, and `magentic add` is the
only existing CLI verb.

Three constraints shape everything below. Sessions outlive the interfaces, so a
control surface that only exists while a UI is in the foreground is not a
control surface. Every mutation must run through the existing coordination or it
will fight the UI over the same tmux session and the same state file. And the
identities the wait contract needs — SessionID, RuntimeName, AgentRunRef — are
already durable and vendor-qualified per ADR 0001, so the wait does not need a
new identity concept, only the discipline to pin the ones that exist.

## Goals / Non-Goals

**Goals:**

- One request vocabulary, two front doors. The CLI is a socket client with no
  private path to tmux or the Registry, so CLI and socket cannot drift.
- The control API is a client of the existing deep modules and adds no second
  data store, no second observation loop, no second way into tmux.
- A wait that is trustworthy enough to build orchestration on: it either
  observed the exact occupant it resolved finishing, or it says why it could not.
- Failure modes are explicit outcome codes. An agent parsing our output must
  never have to distinguish "idle" from "we could not read it" by guessing.

**Non-Goals:**

- No streaming interactive attach over the socket. Reading a pane's current
  content plus an event stream covers the orchestration case; a full PTY proxy
  does not.
- No scheduling, queueing, or dependency graph across Sessions. The API gives
  primitives; the agent composes them.
- No new observation cadence tuned for API clients. Events derive from the
  existing observation pass.
- No configuration surface for enabling individual verbs. The API is on or off.

## Decisions

### One vocabulary, CLI as a socket client

The control verbs are defined once as a request/response vocabulary in a new
`core/` module. The socket server dispatches them; the CLI marshals arguments
into the same requests and renders the responses. A verb therefore cannot exist
on one surface only, and a bug fixed in dispatch is fixed for both.

*Alternative considered:* a CLI that calls `core/` directly and a socket server
that does the same. Rejected — two callers of the same modules diverge in
addressing, defaults, and error text, and the CLI would then keep working when
the socket is down, hiding exactly the failure an agent needs to see. A shared
in-process library with two thin adapters was the middle option; it still leaves
the CLI able to mutate state with no Magentic running, which we do not want,
because a Session started by a CLI with no serving process would be invisible to
the event stream that another agent is subscribed to.

### Line-delimited JSON over a Unix socket

Requests and responses are one JSON document per line, correlated by a
client-chosen request id; a subscription turns the connection into a stream of
event documents until it ends. This is trivially implementable from a shell with
`nc`/`socat`, from Go, and from any agent's scripting environment, and it needs
no schema toolchain.

*Alternatives considered:* HTTP over the Unix socket — familiar, but the event
stream then needs SSE or chunked framing and the client story gets heavier for
no gain; gRPC — a code-generation dependency and a much worse story for an agent
that wants to poke the socket from a shell one-liner.

### Authorization is the socket itself

The socket is created in the user's runtime directory with owner-only
permissions, and each accepted connection is checked against the connecting
process's user id via the platform's peer-credential mechanism. There is no
token because a token buys nothing here: any process able to reach the socket
runs as the user and could already drive tmux directly. What the API adds over
that baseline is convenience and coordination, not privilege.

The honest part of the blast radius: this is the first surface where a coding
agent can start and kill Sessions unattended. The mitigations are that the
surface is small and enumerable, that `kill` never removes a Worktree, and that
every mutation is a lifecycle transition with a durable record, so what an agent
did is reconstructable rather than a mystery.

*Alternative considered:* a per-client token handed out through the environment
marker. Rejected as security theatre — the token would sit in the same
environment any local process of the same user can read.

### The wait pins (SessionID, RuntimeName, AgentRunRef)

At the start of a wait the addressed Session is resolved once into an occupant
identity: the durable SessionID, the exact RuntimeName currently addressed, and
the vendor-qualified AgentRunRef of the run occupying it. Every subsequent
evaluation compares the observed occupant against that triple. Any drift is
`occupant-replaced` — a terminal outcome, not a retry.

The triple is deliberate. SessionID alone survives a runtime being destroyed and
recreated, so a wait pinned on it could be satisfied by a fresh agent in the same
Session. RuntimeName alone is replaceable by design (ADR 0003 treats rename as a
lifecycle transition with old and target runtime names). AgentRunRef alone is
vendor-scoped and can be absent for a terminal Session. Together they answer the
only question that matters: is the thing I am waiting for still the thing that is
there?

*Alternative considered:* pin the SessionID and accept any occupant, the way a
name-based wait would. Rejected — that is exactly the failure this verb exists to
prevent, and it fails silently, returning `done` for work that was never done.

### Waits are evaluated from the existing Observation, and never hold coordination

A pending wait registers interest with the event fan-out and is evaluated when a
new Observation arrives; it holds no Registry lock and no Session transition.
Unavailable or partial Observations are never treated as a condition being met,
so a tmux read that timed out cannot be reported as an idle agent. A `done` wait
whose occupant starts needing human input returns `blocked` rather than hanging
until the timeout: an agent waiting on a peer that hit a permission dialog needs
to know now, and the human needs to be told which Session to unblock.

*Alternative considered:* letting a `done` wait ignore `waiting` and keep
blocking. Rejected — the common failure of unattended delegation is a permission
prompt, and silently absorbing it makes the whole feature untrustworthy.

### Events derive from the existing observation pass

The server keeps the last observed state per Session and emits an event when the
new pass differs, including availability transitions. It does not start a second
observation loop, and it never polls tmux on behalf of a subscriber. Subscribers
get a bounded per-subscription buffer; a subscriber that stops reading is dropped
with an explicit outcome rather than being allowed to stall the loop that the
interfaces also depend on.

*Alternative considered:* an unbounded queue per subscriber. Rejected — one
abandoned agent process would grow memory without limit and, worse, backpressure
into the observation pass would make the UI stutter.

### The serving process

The control API is served by whichever Magentic process starts it: the TUI, the
desktop app, or a headless serve mode for the case where the developer wants the
API without a UI. Exactly one process serves at a time; a second process finding
a live socket does not take it over. A stale socket left by a dead process is
reclaimed. A client that finds nothing serving reports a distinct unavailable
outcome and does not autostart a Magentic — an implicitly spawned daemon whose
lifetime nobody chose is worse than a clear error.

*Alternative considered:* a separate always-on daemon that owns all state, with
the UIs as clients. That is a cleaner end state, and it is where this would go if
the API becomes central, but it is a rewrite of how both interfaces reach
`core/` and is far beyond this change.

### Environment marker and the shipped skill

Provisioning exports `MAGENTIC_ENV=1` plus the socket path, the occupant's
SessionID, ProjectID, and Worktree fact into the runtime. `MAGENTIC_ENV=1` is a
deliberate echo of herdr's `HERDR_ENV=1`: an agent needs one cheap boolean to
decide whether the rest is worth reading. The skill file is a single document in
the repository that the developer installs into a Project, and it is written to
be loadable by both Claude Code and Codex; it opens by telling the agent to check
the marker and stop if it is absent.

*Alternative considered:* shipping vendor-specific instruction files. Deferred —
one document that both vendors can load keeps the wait contract stated in exactly
one place, and per-vendor packaging can come later without changing the contract.

### Assumptions recorded

These were assumed rather than asked, and each is cheap to revise:

1. Verbs live under `magentic session <verb>`, leaving room for later
   `magentic project` and `magentic worktree` trees without a rename.
2. The socket path follows the user's runtime directory convention, with the
   state directory (`~/.config/magentic/`) as the fallback where no runtime
   directory exists.
3. Machine-readable output is opt-in per invocation; human-readable stays the
   default so the CLI is still pleasant to use by hand.
4. `session output` returns the pane's visible content plus a bounded amount of
   scrollback, not the full transcript. Transcript-level reading is WorkHistory's
   job and is not part of this change.
5. The API is on by default when a Magentic process runs, with a way to disable
   it. A control surface nobody can rely on being there is not one an agent will
   learn to use.

## Risks / Trade-offs

- **An agent kills or restarts a Session a human was working in** → Every
  mutation is a durable lifecycle transition, so the action is attributable;
  `kill` never removes a Worktree, so no work is lost; the event stream and the
  UI both reflect the change immediately rather than leaving a ghost.
- **`wait` becomes the feature people trust and it lies once** → The pinned
  triple plus the rule that unavailable Observations are never a result are the
  two places a lie could enter, and both are stated as spec scenarios with the
  replacement cases enumerated. This is the part of the change that most needs
  tests before it needs polish.
- **Event stream backpressure stalls observation, and the UI stutters** →
  Bounded per-subscription buffers and dropping a stalled subscriber; the
  observation pass never awaits a consumer.
- **Two Magentic processes race for the socket** → Only one serves; a live
  socket is not taken over, and a stale one is reclaimed under the same
  interprocess coordination the Registry already uses.
- **CLI as a socket client means the CLI is dead when nothing serves** →
  Accepted deliberately, and reported as a distinct outcome naming the expected
  path. The alternative hides a worse failure: state mutated behind the back of
  the process other agents are subscribed to.
- **`MAGENTIC_ENV` teaches agents a capability that a stray copied environment
  would falsely advertise** → Self-identification goes through the API, which
  resolves the claimed SessionID against the Registry and answers `not-managed`
  when it does not resolve.
- **Scope creep toward a general-purpose remote API** → Non-goals are stated in
  the proposal; the socket has no network listener at all, which makes the
  boundary structural rather than a matter of discipline.

## Migration Plan

Additive throughout: no existing behavior changes, no persisted format changes.
The environment marker only adds variables to newly provisioned runtimes; already
running Sessions simply do not carry it, which the spec states explicitly rather
than backfilling. Rollback is disabling the server — the CLI then reports the API
as unavailable and everything else works as before.
