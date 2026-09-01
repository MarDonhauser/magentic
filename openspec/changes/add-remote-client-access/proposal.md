## Why

The developer's agents and the developer are not always on the same machine. The most common real-world setup in the coding-agent community is a Linux box, a workstation under the desk, or a home server that runs the agents, with the developer attaching from a Mac in another room or from a phone over a Tailscale VPN to start a Session and unblock the two that are waiting. Magentic today assumes the interface and the tmux Sessions live on one host, so leaving that host means losing the control center entirely.

Magentic already has the shape this needs. tmux owns the agent processes, the Registry owns durable state, and both frontends are observers that read Observations and submit LifecycleTransitions. The interface between `core/` and the frontends is already a request/response surface plus a stream of terminal bytes. This change lifts that existing local interface onto a network transport instead of inventing a second architecture, and it deliberately keeps the wire format at the level of terminal output and semantic events. Streaming pixels or screenshots over a home uplink is the known failure mode of comparable tools; terminal bytes stay usable on a phone tethered to mobile data.

## What Changes

- Add a **host service**: a long-running Magentic process on the machine that owns the tmux Sessions, the Registry, and the repositories. It exposes the existing Session, Observation, Specification, and action surface over a network transport, plus a streaming channel that carries terminal output and status events.
- Add a **client mode** to the desktop app. The same Wails app either runs against the local host (today's behavior, unchanged and still the default) or attaches to a remote host by address. Which one it is stays visible in the interface; a client never silently addresses the wrong machine.
- Introduce a **HostSession** — the client's live attachment to one host, with its own connection state — and a **HostLink**, the durable configuration describing a reachable host (address, name, credential reference).
- Represent connection loss explicitly, per ADR 0004. A dropped or degraded link yields `ObservationAvailability` "unavailable", never a list of dead Sessions and never a silently stale board that looks fresh. The client keeps the last successful view, labels it as last known and how old it is, and blocks the actions that would destroy or overwrite work until fresh known facts arrive.
- Reconnect automatically with bounded backoff, and resume the terminal stream at a position the host can still serve. Where the host cannot serve the gap, the client re-syncs the visible pane content rather than presenting a spliced, misleading scrollback.
- Define a **RemoteActionPolicy**: which actions a client may perform over the network. Observing, attaching to a Session's terminal, typing into it, sending messages and skills, and creating Sessions are permitted. Destructive and host-shaped operations (removing a Worktree, removing a Project, registering a Project by filesystem path) are restricted by default and require an explicit host-side opt-in, because a remote client cannot see the host's filesystem to judge the consequence.
- Authenticate every connection with a **HostToken** — a pre-shared, host-generated bearer credential — and carry all traffic over TLS. The trust assumptions are stated explicitly and narrowly: one developer, one credential, a host reachable only inside a trusted overlay network such as Tailscale or a LAN, with the host binding to that interface rather than to a public address. A HostToken authenticates a device, not a person; there is no user model.
- Keep the TUI local-only in this change. It continues to run against the in-process core on the host machine.

## Capabilities

### New Capabilities

- `remote-access/host-service`: The host-side service that owns the Sessions and serves the Session, Observation, and action surface over the network — what it exposes, how requests are authenticated and authorized, how terminal output and status events are streamed, and what it refuses.
- `remote-access/client-attachment`: The client side of a remote attachment — how a HostLink is configured and selected, how a HostSession's connection state is observed and rendered, how reconnect and stream resumption behave, and how unavailable knowledge is presented instead of fabricated absence.

### Modified Capabilities

None. `openspec/specs/` currently holds no published capabilities, so the local Observation, lifecycle, and terminal behavior this change builds on is described as context inside the new capabilities rather than as a delta against an existing spec.

## Impact

- New host-service package (transport, authentication, request handling, event fan-out) wrapping the existing `core/` API surface. `core/` gains no knowledge of the network.
- `core/observation.go`: read-only consumer. `ObservationAvailability` and `ObservationProblem` gain a transport-failure carrier so a client can distinguish "the host could not observe tmux" from "I could not reach the host".
- `core/lifecycle.go`, `core/actions.go`: unchanged semantics. A remote request produces the same LifecycleTransition on the host, with the same interprocess coordination (ADR 0002, ADR 0003). Reconciliation stays a host concern; an interrupted network request never re-sends an initial prompt.
- `app/app.go`: today's bound methods become a two-implementation seam — local (in-process core) and remote (host client). `OpenTerm` / `WriteTerm` / `ResizeTerm` / `CloseTerm` and the `term:data:` events map onto the host's streaming channel.
- `app/watcher.go`: the observation loop either observes locally or subscribes to the host's event stream; the AttentionPlanner keeps running client-side so notifications, Dock badge, and native attention stay on the developer's machine.
- `app/frontend/`: host selector, connection-state presentation, last-known-view labelling, and disabled states for actions the RemoteActionPolicy withholds.
- New durable client-side configuration for HostLinks; HostTokens are stored in the OS credential store, not in the Registry.
- No changes to the TUI's behavior.

## Non-goals

- No multi-user, team, or shared-workspace features. There is one developer and one credential; the host has no user model, no roles, and no per-user Session ownership.
- No hosted or cloud offering, no relay service, no NAT traversal of Magentic's own. Reachability is the overlay network's job.
- No native mobile app. A browser-reachable client is in scope only if it falls out of the existing web frontend without a second UI codebase; if it does not, it is dropped from this change.
- No file synchronization, no remote filesystem browsing, no editing repository files from the client.
- No public-internet exposure story: no OAuth, no certificate authority integration, no rate limiting or abuse defense beyond authentication.
- No remote operation of the TUI.
