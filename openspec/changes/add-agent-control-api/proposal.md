## Why

Magentic can observe and manage Sessions, but only a human can drive it. Every
Session is started, prompted, watched, and ended through the TUI or the desktop
app, so a coding agent that would like to delegate work — spawn a reviewer in a
Worktree, wait for it, read what it produced — has no way to ask Magentic for
that. The single CLI verb (`magentic add`) registers a Project and stops there.

Competitor research points at the same gap. herdr's scriptable control surface
is its strongest differentiator: an agent inside a managed pane can start
another agent, read its output, subscribe to state changes, and — the verb cmux
is criticised for missing — block until a named occupant is done. That turns a
session viewer into an orchestration multiplier. Magentic already owns the parts
that make this safe to build (stable SessionIDs per ADR 0001, coordinated
Registry changes per ADR 0002, durable desired-state transitions per ADR 0003);
what is missing is a surface that exposes them to a process instead of a person.

## What Changes

- A scriptable **Agent Control** surface with two front doors over one shared
  request vocabulary: a `magentic` CLI subcommand tree and a local Unix-domain
  socket. The CLI is a thin client of the socket, so both speak the same verbs
  and neither can drift into private behavior.
- **Command set**, all Project- and Worktree-scoped: `session start`,
  `session list`, `session send`, `session output`, `session wait`,
  `session kill`. Each command addresses a Session by SessionID or by a
  Project-qualified name, and reports Observation availability explicitly rather
  than flattening an unreadable runtime into a plausible-looking state.
- **A local socket protocol** with request/response calls plus a subscribable
  **event stream** that emits Session status transitions (running, waiting,
  idle, exited, dead) and Observation availability changes, so a client can
  react without polling.
- **Pinned wait semantics.** `session wait --until done` resolves the addressed
  Session to a concrete SessionID plus the RuntimeName and AgentRunRef occupying
  it at the moment of resolution, and pins that identity for the lifetime of the
  wait. If the occupant is replaced — the Session restarted, renamed, its runtime
  recreated, or its agent run swapped — the wait fails with a distinct
  *occupant-replaced* outcome instead of being satisfied by a stranger.
- **An environment marker** (`MAGENTIC_ENV=1` plus the addressing facts an agent
  needs: socket path, its own SessionID, ProjectID, and Worktree) exported into
  every Magentic-managed Session runtime, so an agent can detect that it is
  running inside Magentic and may talk to the local socket.
- **A shipped agent instruction file** — one skill document, installable into a
  Project — that teaches Claude Code and Codex the verbs, the wait contract, and
  the delegation pattern (spawn a reviewer in a Worktree, wait, read the result).
- **Authorization** is local-user only: the socket lives in the user's runtime
  directory with owner-only permissions, and every accepted connection is
  checked against the owning UID. No token, no network listener.

Non-goals: remote or network access, multi-user authentication or per-client
scopes, and any plugin or extension marketplace.

## Capabilities

### New Capabilities
- `agent-control/command-surface`: the CLI verb set, its addressing rules,
  scoping to Project and Worktree, and its machine-readable output contract.
- `agent-control/local-socket-api`: the local socket, its transport and request
  vocabulary, the status-transition event stream, and the local-user
  authorization boundary.
- `agent-control/session-wait`: the pinned-occupant contract for waiting on a
  Session, including its terminal outcomes and replacement detection.
- `agent-control/agent-integration`: the environment marker exported into managed
  Session runtimes and the shipped agent instruction file.

### Modified Capabilities
<!-- None. openspec/specs/ currently holds no capabilities, so every spec in this
     change is new. Existing behavior of Registry, Session Lifecycle, and Session
     Observation is reused, not respecified. -->

## Impact

- **New surface**: a control-API module under `core/` that owns the request
  vocabulary, the socket server, and the event stream; CLI wiring in the root
  package next to the existing `magentic add`; a shipped skill/instruction file
  in the repository.
- **Reused modules**: Registry (Project and Session resolution under stable IDs),
  Session Lifecycle (provisioning, prompt delivery, stop), Session Observation
  (status and availability), Repositories (Worktree resolution). The control API
  is a client of these modules and introduces no second data store and no second
  path to tmux.
- **Touched behavior**: managed Session runtimes gain environment variables at
  provisioning time; the TUI and desktop app gain a background socket server
  while running, plus a way to run the server without a UI attached.
- **Blast radius**: anything the API can do, a local process running as the user
  can already do by driving tmux directly; the API adds convenience and
  coordination, not privilege. It is still the first surface where a coding agent
  can start and kill Sessions unattended, so its failure modes must be explicit.
