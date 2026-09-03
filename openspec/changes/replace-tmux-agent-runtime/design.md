## Context

See proposal.md — Why. The constraints that shape the approach:

- Verified against the installed Claude Code CLI (2.1.259): `-p --input-format stream-json --output-format stream-json --verbose` is a bidirectional JSON-lines protocol over stdin and stdout. `--include-partial-messages` adds token-level chunks, `--replay-user-messages` echoes each delivered prompt back as acknowledgement, `--permission-prompts host` together with `--permission-prompt-tool` routes permission decisions to the host, `--forward-subagent-text` surfaces subagent output, and `--session-id`, `--resume` and `--fork-session` control conversation identity. `--permission-prompt-tool` is referenced in the CLI's own help text for `--permission-prompts` but is not itself listed: it is a supported SDK entry point that is not advertised.
- The protocol is stdio-only. A process speaking it is reachable exactly as long as the process holding the other end of its pipes lives.
- `magentic serve` today owns nothing. It claims the control socket and runs an Observation pass (`control_serve.go`); tmux owns every agent process.
- The control surface already exists and is shaped for this: `ControlVerb` with `session.start`, `session.send`, `session.output`, `session.wait`, `session.kill` and a streaming `session.watch` (`core/control.go:14`), served over a unix socket (`core/control_socket.go`).
- ADR 0003 requires durable intent before any runtime is touched. ADR 0004 requires an unobservable fact to stay explicitly unobservable. ADR 0007 requires attention to be planned before side effects.
- `add-agent-timeline` defines Item, Conversation and the normalization contract. This change produces Items from a different source; it must not define a second model.

## Goals / Non-Goals

**Goals:**

- Preserve today's guarantee that closing the interface does not end an agent's work — including across a daemon restart, which is when a naive "the daemon spawns the child" design is strictly worse than tmux.
- One process-ownership story that a second vendor can be added to.
- Permission decisions that a person makes, in the interface they are already looking at.

**Non-Goals:**

- Surviving a machine reboot. No design here rescues a killed process.
- Windows. The service installation targets launchd and systemd user units; Windows managed Sessions are refused with that reason until someone needs them.
- Replacing the control socket with a new transport.

## Decisions

### A per-Session agent host owns the process; the daemon coordinates

The daemon does not hold the agent's pipes. It starts a small long-lived host process per managed Session — the Magentic binary in an `agent-host` mode — which owns the vendor process, speaks its stdio protocol, and exposes a unix socket under the state directory. The daemon connects to that socket. A daemon restart reconnects; the agent never notices.

This is precisely the job tmux does today, done by something that understands the protocol instead of a terminal. It is the reason the managed-process spec can require reclaiming at all.

*Alternative considered:* the daemon spawns the vendor process directly. Simpler by one process, but the pipes die with the daemon, so every daemon restart — including a Magentic update — kills every in-flight turn. That is a regression against tmux, and the durability promise is the reason this project exists.

*Alternative considered:* `claude --bg` with `claude attach`, `logs`, `stop` and `rm`, which is a real background-session manager built into the CLI. Rejected on two counts: `logs` returns terminal output rather than the stream protocol, so permissions and turn boundaries stay invisible; and no equivalent exists for the other three vendors, so it cannot carry the runtime abstraction.

*Consequence:* one extra process per managed Session. That is the same order as today, where each Session carries a tmux server pane and a shell.

### Reclaiming is identity-confirmed, never pattern-matched

The daemon records, per managed Session, the host's socket path, its process identity and a token the host returns on connect. Reclaiming means connecting to the socket and receiving the recorded token back. A socket that does not answer, or answers with a different token, is not adopted — and is not killed either, because the process holding it may belong to something else entirely.

Nothing in this change may find a process by matching a command line, a path or a Session name against the process table. The managed-process spec states this as a requirement because it is the failure mode most likely to be reintroduced by a later convenience.

### Permission decisions travel over the host's own socket

The agent is started with `--permission-prompts host` and `--permission-prompt-tool` naming an MCP tool. The MCP server backing that tool is the Magentic binary again, in an `agent-approve` mode, configured through `--mcp-config` and connected to its own Session's host socket. When the agent asks, the tool call blocks in the host until a decision arrives from an interface, then returns it.

Blocking is the correct behavior, not a limitation: the permission-decisions spec forbids Magentic from answering on its own, so a request with nobody to answer it must wait. The Session is observable as waiting for a decision the whole time, and the Attention model raises it.

*Alternative considered:* run the agent in a permissive mode and mediate afterwards. Rejected outright — that is answering on the developer's behalf, which the spec forbids.

*Alternative considered:* an HTTP MCP server on a local port. Rejected: a port is reachable by anything on the machine, a unix socket with file permissions is not, and the daemon already uses that pattern.

### The managed runtime produces the same Items as the transcript reader

The host normalizes the vendor's event stream into the Items defined by `add-agent-timeline`. Streaming chunks update an Item that is marked as still being produced; the completed message supersedes it, which is the supersession path that change already specifies for tool results. A PermissionRequest and its outcome are Items too, so a Session's account holds the question and the answer in order.

*Alternative considered:* keep reading the JSONL transcript for the durable account and use the stream only for liveness. Rejected: the same activity would arrive twice from two sources with different identities, and reconciling them is more code than one normalizer. The transcript reader stays for tmux Sessions, which is where it is the only option.

### Delivery acknowledgement comes from the protocol, not from a timer

`--replay-user-messages` echoes each delivered prompt. The Outbox advances on that echo. This replaces the current `send-keys` path for managed Sessions only; `core/outbox.go` keeps its tmux delivery for tmux Sessions, selected by the Session's runtime.

### Runtime is a Session property, chosen at creation

`core.Session` gains a runtime field. Absent reads as tmux, so every existing record keeps working without a migration pass. Lifecycle transitions dispatch on it: `Start`, `Resume` and `Kill` gain a managed path beside the tmux path, both still writing durable intent first per ADR 0003.

*Alternative considered:* a global setting that switches all Sessions. Rejected: a machine will run both for a long time — terminal Sessions are always tmux, and three of four vendors have no managed path — so the choice belongs to the Session, where it can be read back honestly.

### Continuing in a terminal forks rather than moves

"Open this in a terminal" creates a new tmux Session started with the vendor's fork of the conversation (`--fork-session` for Claude), leaving the managed Session untouched. Moving a live conversation between runtimes would mean two processes believing they own one conversation.

### Service installation is a user agent, per platform

macOS gets a launchd user agent under `~/Library/LaunchAgents`; Linux gets a systemd user unit. Both run `magentic serve`. Installation is an explicit command, never a side effect, and never asks for elevated privileges. `core/control_socket.go`'s existing single-owner handling covers the "one daemon owns the processes" requirement; the service must not weaken it.

## Risks / Trade-offs

- **The stream-json protocol is an SDK surface, not a stability guarantee.** A CLI update can change event shapes, and `--permission-prompt-tool` is not even advertised. → Record real event streams as fixtures and test the normalizer against them; check the CLI version at startup and refuse the managed runtime with a stated reason on a version whose protocol was not verified. A protocol break must degrade to "managed runtime unavailable", never to a Session that silently does nothing.
- **One more process per Session.** → It replaces a tmux pane rather than adding to one, and the host is small. Measure resident memory per host before enabling this for many Sessions.
- **A blocked permission request can look like a hung agent.** → It has its own observed status and its own attention intent; the specs require both. The failure to avoid is a Session that reads as working while it waits.
- **A host that outlives its daemon and is never reclaimed** leaves an orphaned agent process. → The daemon records hosts durably and reconciles at startup; a host whose Session no longer exists is reported so a developer can end it. Magentic does not sweep processes it cannot confirm.
- **Attach disappears for managed Sessions**, and muscle memory will reach for it. → It must be absent from the action list rather than failing when used, and continuing in a terminal must be offered in its place.
- **Durability now depends on the daemon.** Before the service is installed, a managed Session is *less* durable than a tmux one. → The service-installation spec forbids the unconditional durability claim while the service is missing.
- **Two runtimes mean two paths through lifecycle, outbox, observation and status** for a long time. → The runtime is read from the Session record at one place per path, and every vendor declares its supported runtimes, so an unsupported combination is refused at creation rather than discovered at runtime.

## Migration Plan

1. `add-agent-timeline` must be archived first; this change produces Items and must not define them.
2. The Session record gains a runtime whose absence reads as tmux, so no state migration runs and no existing Session changes behavior.
3. The managed runtime ships behind an explicit choice at Session creation, for Claude only. Nothing existing switches.
4. The login service ships as an explicit command. Until it is installed, managed Sessions are honest about ending with the daemon.

Rollback is refusing new managed Sessions: existing tmux Sessions were never touched, and a managed Session can be continued in a terminal, which is a tmux Session again.

## Open Questions

- Whether the agent host should also serve tmux Sessions' observation, which would let `tmux list-panes` polling shrink over time. Deferred: it changes no behavior specified here.
- Which Claude Code versions the managed runtime declares as verified. Answerable when the fixtures exist, and it changes a refusal message rather than the approach.
