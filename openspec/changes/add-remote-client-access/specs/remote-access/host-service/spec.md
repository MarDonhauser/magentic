## Purpose

The host service is the Magentic process on the machine that owns the tmux Sessions, the Registry, and the repositories. It serves the Session, Observation, Specification, and action surface to authenticated clients over a network transport, together with a streaming channel carrying terminal output and status events.

## ADDED Requirements

### Requirement: Host service exposes the Session surface over a network transport

The host service SHALL serve the same Session, Observation, Specification, Board, Git, and statistics reads that a local frontend obtains from the in-process core, and SHALL apply the same domain semantics for every accepted request, so a remote client and a local frontend observe the same Sessions and the same facts.

The host service SHALL NOT define a second Session model. A `SessionID`, `ProjectID`, `WorktreeRef`, `SpecificationRef`, and `AgentRunRef` sent over the network are the same durable identities the host uses locally.

The host service SHALL run as a distinct, long-lived process that may be started without any user interface on the host machine.

#### Scenario: Remote read returns the same Sessions as a local read

- **WHEN** an authenticated client requests the Session overview from the host
- **THEN** the host returns the Sessions, Projects, Worktrees, and Observations that a local frontend on that host would obtain from the same Registry and the same Observation cycle
- **AND** every Session carries the same `SessionID` the host uses locally

#### Scenario: Host serves clients without a local interface running

- **WHEN** the host service is running and no desktop app or TUI is open on the host machine
- **THEN** clients can still connect, observe Sessions, and perform permitted actions

#### Scenario: Opaque runtime identity is never reconstructed

- **WHEN** a client sends an action for a Session
- **THEN** the host resolves the Session's `RuntimeName` from its own Registry
- **AND** the host never accepts a client-supplied `RuntimeName` or display name as the address of an external runtime

### Requirement: Every connection is authenticated with a HostToken

The host service SHALL reject every request and every stream subscription that does not present a valid **HostToken**. A HostToken is a host-generated bearer credential with sufficient entropy to resist guessing, issued by an operator with access to the host machine, revocable individually, and identifying a device rather than a person.

The host service SHALL compare tokens in constant time and SHALL NOT log a token value or emit it in an error message.

The host service SHALL support revoking a HostToken, after which connections presenting it are rejected and any stream it holds open is closed.

#### Scenario: Missing or invalid credential is rejected

- **WHEN** a client connects without a HostToken, or with a token that is unknown, revoked, or expired
- **THEN** the host rejects the connection with an authentication failure
- **AND** the rejection distinguishes an authentication failure from an unreachable host, so the client does not present it as a transport problem

#### Scenario: Revocation ends an established connection

- **WHEN** an operator revokes a HostToken while a client holding it is connected and streaming
- **THEN** the host closes that client's streams and rejects its subsequent requests
- **AND** other clients holding valid tokens are unaffected

#### Scenario: Token value is never disclosed

- **WHEN** an authentication failure is logged or reported
- **THEN** the record identifies the attempt without containing the presented token value

### Requirement: Transport is encrypted and bound to a trusted interface

All traffic between client and host SHALL be carried over an encrypted transport (TLS). The host SHALL bind by default to a loopback or overlay-network interface (for example a Tailscale address or an explicitly configured LAN address) and SHALL NOT bind to a public interface without an explicit, deliberate operator configuration.

The host SHALL state the interface and address it is listening on when it starts, so the operator can see what is exposed.

The trust model is single-user and device-scoped: one developer, one or more device credentials, a network perimeter provided by the overlay network. The host SHALL NOT be presented or documented as safe to expose directly to the public internet.

#### Scenario: Default binding is not public

- **WHEN** the host service starts without an explicit bind address
- **THEN** it listens only on loopback or on a configured overlay-network interface
- **AND** it reports the address it bound to

#### Scenario: Unencrypted transport is refused

- **WHEN** a client attempts to connect without transport encryption
- **THEN** the host refuses the connection

### Requirement: Host streams terminal output and status events

The host service SHALL provide a streaming channel that delivers, to a subscribed client, terminal output for the Sessions the client has attached and status events describing Observation changes, lifecycle progress, and attention-relevant transitions.

The wire format for terminal content SHALL be terminal output bytes, not rendered images or pixel data, so that a client on a constrained or metered link remains usable.

The host SHALL apply per-subscription flow control: when a client cannot keep up, the host SHALL drop or coalesce terminal output for that client and mark the stream as having a gap, rather than growing an unbounded buffer or slowing the tmux Session.

#### Scenario: Terminal output reaches an attached client

- **WHEN** a client is attached to a Session's terminal and the agent writes output
- **THEN** the host delivers that output to the client as terminal bytes over the streaming channel

#### Scenario: Slow client is coalesced, not buffered without limit

- **WHEN** a subscribed client consumes the stream more slowly than the Session produces output
- **THEN** the host drops or coalesces output for that client within a bounded budget
- **AND** the host marks the stream as gapped so the client can re-sync instead of rendering spliced scrollback
- **AND** the Session's own execution is unaffected

#### Scenario: Status events are delivered without polling

- **WHEN** an Observation cycle on the host changes a Session's presence, activity, or attention state
- **THEN** the host emits a status event for that Session to subscribed clients

### Requirement: Host offers a resumable stream position

The host SHALL let an attached client resume its terminal stream after a reconnect from a position the host still holds, within a bounded retained window per Session. When the requested position is no longer retained, the host SHALL refuse resumption explicitly and offer a fresh snapshot of the current pane content instead.

#### Scenario: Resume within the retained window

- **WHEN** a client reconnects and requests resumption at a position still inside the host's retained window
- **THEN** the host delivers the missed output from that position and continues the live stream

#### Scenario: Resume outside the retained window

- **WHEN** a client requests resumption at a position the host no longer retains
- **THEN** the host explicitly reports that the gap cannot be served
- **AND** the host delivers a current snapshot of the pane content as the new stream origin

### Requirement: Remote actions are governed by an explicit RemoteActionPolicy

The host service SHALL classify every action it can perform as remotely permitted or remotely restricted, and SHALL enforce that classification server-side. Client-side presentation of the policy is a convenience and SHALL NOT be the enforcement point.

Permitted by default: observing Sessions and Projects; attaching to a Session terminal; writing input to an attached terminal; resizing a terminal; sending messages, skills, and prompts to a Session; creating a Session in a registered Project (with or without a Worktree); renaming a Session; marking a Session seen; starting a Specification that the host resolves itself.

Restricted by default, requiring an explicit host-side opt-in before a client may invoke them: removing a Worktree; removing a Project; registering a Project by filesystem path; killing a Session; any action that takes a filesystem path from the client.

The host SHALL NOT accept a client-supplied absolute filesystem path as an instruction to act on. Path-shaped inputs SHALL be replaced by opaque, host-resolved handles (`WorktreeRef`, `SpecificationStartToken`, `ProjectID`), resolved against fresh host facts at action time.

#### Scenario: Restricted action is refused without opt-in

- **WHEN** a client invokes a restricted action and the host has not opted in to that action for remote use
- **THEN** the host refuses the action and reports it as restricted rather than as a failure
- **AND** no Git, filesystem, or runtime side effect occurs

#### Scenario: Restricted action after opt-in

- **WHEN** the host operator has opted in to a restricted action and a client invokes it
- **THEN** the host performs it with the same coordination, preconditions, and safety checks as a local invocation

#### Scenario: Client-supplied path is rejected

- **WHEN** a client sends an action carrying a filesystem path instead of a host-resolved handle
- **THEN** the host rejects the request without touching the filesystem

### Requirement: Remote requests produce ordinary host-side LifecycleTransitions

An accepted remote action SHALL create and advance a `LifecycleTransition` on the host exactly as a local action does, under the same interprocess coordination and the same durable desired-state semantics. The network is a transport, not a second lifecycle.

A network failure after the host has accepted an action SHALL NOT cause the host to abandon or roll back the transition; reconciliation remains a host concern and proceeds independently of client connectivity.

Delivery of an initial prompt SHALL NOT be retried automatically because a client lost its connection. A retry SHALL only occur as a new, deliberate intent.

#### Scenario: Client disconnects mid-action

- **WHEN** the host has accepted an action and the client's connection drops before the response is delivered
- **THEN** the host continues to reconcile the transition to its desired state
- **AND** the client, on reconnect, learns the outcome from the host's current durable state rather than by re-sending the action

#### Scenario: Duplicate submission is not duplicated work

- **WHEN** a client re-sends an action carrying the same transition identity after an ambiguous transport failure
- **THEN** the host recognizes the existing transition and advances it idempotently instead of starting a second one

#### Scenario: Initial prompt is not auto-replayed after transport failure

- **WHEN** delivery of a Session's initial prompt is left in an unknown state by a transport failure
- **THEN** the host does not send the prompt again on its own
- **AND** the unknown delivery state is reported to the client as unknown

### Requirement: Host reports its own observation failures distinctly from transport health

The host SHALL continue to report `ObservationAvailability` for the facts it gathers, including partial and unavailable results, and SHALL transmit that availability to clients unchanged. A host that cannot observe tmux SHALL report unavailable observation, and SHALL NOT report absent Sessions.

#### Scenario: Host cannot probe tmux

- **WHEN** the host's Observation cycle fails or returns malformed output
- **THEN** the host serves an unavailable or partial Observation with its problems attached
- **AND** the host does not serve the affected Sessions as absent or idle
