## Purpose

Client attachment covers how the desktop app attaches to a remote Magentic host instead of the local machine: how a host is configured and selected, how the connection's state is observed and presented, how reconnection and terminal-stream resumption behave, and how a lost connection is rendered as unavailable knowledge rather than as absent or dead Sessions.

## ADDED Requirements

### Requirement: The app runs either locally or against one selected host

The desktop app SHALL operate in exactly one of two modes at a time: **local mode**, running against the in-process core on this machine, or **client mode**, attached to one remote host. Local mode SHALL remain the default and SHALL be unchanged by this capability.

A **HostLink** is durable client-side configuration naming a reachable host: a display name, a network address, and a reference to the credential used for it. The developer SHALL be able to add, edit, and remove HostLinks and to switch the app between local mode and any configured HostLink.

The app SHALL make the currently addressed host visible at all times in client mode, so an action is never issued against a machine the developer did not mean.

#### Scenario: Switching to a configured host

- **WHEN** the developer selects a configured HostLink
- **THEN** the app attaches to that host and presents its Sessions
- **AND** the interface identifies the host it is showing

#### Scenario: Local mode is unchanged

- **WHEN** the app runs in local mode
- **THEN** it behaves exactly as before this change, with no network attachment and no host indicator implying a remote host

#### Scenario: One host at a time

- **WHEN** the developer selects a different HostLink while attached
- **THEN** the app detaches from the current host, closing its streams, before attaching to the new one
- **AND** Sessions from the previous host are not shown alongside the new host's Sessions

### Requirement: HostTokens are stored in the operating system credential store

The client SHALL store each HostToken in the operating system's credential store and SHALL NOT write it into the Registry, into project configuration, or into any file committed to a repository. The token value SHALL NOT be displayed after entry and SHALL NOT appear in logs, diagnostics, or exported state.

#### Scenario: Credential is persisted outside plain configuration

- **WHEN** the developer enters a HostToken for a HostLink
- **THEN** the token is stored in the operating system credential store
- **AND** the HostLink configuration retains only a reference to it

#### Scenario: Credential store is unavailable

- **WHEN** the credential store cannot be read
- **THEN** the app reports that the credential is unavailable and stays detached
- **AND** the app does not fall back to storing or reading the token from plain configuration

### Requirement: Connection state is an explicit, observable HostSession fact

A **HostSession** is the client's live attachment to one host. Its connection state SHALL be explicit and SHALL be one of: attaching, attached, degraded, reconnecting, detached, or refused. The state SHALL be visible in the interface together with the time of the last successful exchange with the host.

The connection state SHALL be distinct from the state of any Session. A connection problem SHALL NOT be expressed as a change to a Session's presence, activity, or attention state.

#### Scenario: Connection state is surfaced

- **WHEN** the HostSession is reconnecting
- **THEN** the interface shows that the connection to the named host is reconnecting and how long ago the last successful exchange was

#### Scenario: Authentication refusal is not shown as unreachable

- **WHEN** the host refuses the credential
- **THEN** the HostSession enters the refused state and the interface says the credential was rejected
- **AND** the app does not retry with the same credential in a reconnect loop

### Requirement: A lost connection is unavailable knowledge, never absent Sessions

When the connection to the host is lost or degraded, the client SHALL set the availability of all host-derived facts to unavailable and SHALL retain and present the last successfully received view, labelled as last known, with the time it was received.

The client SHALL NOT render a lost connection as Sessions that are absent, dead, idle, finished, or clean. It SHALL NOT render an empty list where the last known view had Sessions. It SHALL NOT report zero Git divergence, zero waiting Sessions, or an empty Board because the host is unreachable.

#### Scenario: Connection drops while Sessions are shown

- **WHEN** the connection to the host drops while the client is showing Sessions
- **THEN** the client keeps showing the last known Sessions, marked as last known with their age
- **AND** no Session is shown as ended, dead, or idle as a result of the disconnection

#### Scenario: Board and statistics during a disconnection

- **WHEN** the client cannot reach the host and the developer opens the Board, Git graph, or statistics
- **THEN** those views state that the facts are unavailable
- **AND** they do not present empty columns, an empty graph, or zero totals as findings

#### Scenario: Attention is not raised by a disconnection

- **WHEN** the connection to the host is lost
- **THEN** the client does not emit per-Session notifications, Dock badges, or native attention derived from the stale view
- **AND** the connection state itself is what the developer is told about

### Requirement: Destructive and overwriting actions require fresh known facts

While host-derived facts are unavailable or stale, the client SHALL disable actions whose consequence depends on those facts being current — in particular anything that removes, overwrites, merges, or terminates work — and SHALL state that fresh facts are required. Reads, and actions the host will re-validate against fresh host facts at action time, MAY remain available.

#### Scenario: Removal is blocked on a stale view

- **WHEN** the view is marked last known and the developer invokes a Worktree removal or a Session kill
- **THEN** the client refuses to submit the action and explains that current facts from the host are required

#### Scenario: Action resumes after reconnect

- **WHEN** the connection is restored and a fresh known view arrives
- **THEN** the previously disabled actions become available again without restarting the app

### Requirement: Reconnection is automatic and bounded

The client SHALL attempt to reconnect automatically after a transport failure, with bounded exponential backoff and a jitter, and SHALL allow the developer to trigger an immediate reconnect. The client SHALL NOT reconnect automatically after a refusal caused by an invalid or revoked credential, and SHALL NOT reconnect automatically after the developer has deliberately detached.

On reconnect, the client SHALL re-synchronize the Session, Observation, and lifecycle view from the host's current durable state before clearing the last-known labelling.

#### Scenario: Transport drop reconnects on its own

- **WHEN** the network drops and later recovers
- **THEN** the client reconnects without developer interaction, within the bounded backoff schedule
- **AND** the view is refreshed from the host's current state before it is presented as current

#### Scenario: Backoff does not hammer the host

- **WHEN** the host stays unreachable for an extended period
- **THEN** the interval between reconnect attempts grows to a bounded maximum and stays there

#### Scenario: Deliberate detach stays detached

- **WHEN** the developer detaches from a host
- **THEN** the client does not reconnect automatically

### Requirement: Terminal attachment resumes or re-syncs, never splices

After a reconnect, the client SHALL request resumption of each attached Session's terminal stream from its last received position. When the host cannot serve that position, the client SHALL replace the visible terminal content with the fresh snapshot the host provides and SHALL mark that a gap occurred, rather than appending new output to output that no longer connects to it.

Keystrokes produced while the connection is down SHALL NOT be queued for silent later delivery into a terminal; the client SHALL indicate that input is not being delivered.

#### Scenario: Attached terminal survives a brief drop

- **WHEN** a brief connection drop occurs while a terminal is attached and the host can serve the gap
- **THEN** the missed output is delivered and the terminal continues without a visible break

#### Scenario: Gap that cannot be served

- **WHEN** the host reports it can no longer serve the requested position
- **THEN** the client replaces the terminal content with the host's current pane snapshot and marks that output was missed

#### Scenario: Typing while disconnected

- **WHEN** the developer types into an attached terminal while the connection is down
- **THEN** the input is not delivered and the client makes clear that the terminal is not receiving input

### Requirement: Attention runs on the client's machine

In client mode, notifications, the Dock badge, native attention, and break behavior SHALL be produced on the developer's machine from the status events and Observations the host streams, so the developer is alerted where they are sitting.

Attention SHALL be derived only from facts whose availability is known. Attention intents SHALL NOT be produced from a last-known view while the connection is unavailable.

#### Scenario: Remote Session needs input

- **WHEN** a Session on the host enters a waiting-for-input state and the client is attached
- **THEN** the client raises attention on the developer's machine for that Session

#### Scenario: No attention from stale facts

- **WHEN** the connection is unavailable and the last known view contains a waiting Session
- **THEN** the client does not raise a new per-Session attention intent for it

### Requirement: Restricted actions are presented as restricted

The client SHALL obtain the host's RemoteActionPolicy and SHALL present actions the host restricts as unavailable with the reason, instead of offering them and failing. The client SHALL treat a host refusal as authoritative even if its cached policy said otherwise.

#### Scenario: Restricted action is not offered

- **WHEN** the host restricts Worktree removal for remote clients
- **THEN** the client does not offer that action as available and explains that the host restricts it

#### Scenario: Host refusal overrides a stale policy

- **WHEN** the client's cached policy allows an action that the host now refuses as restricted
- **THEN** the client reports the refusal, updates its policy view, and does not retry
