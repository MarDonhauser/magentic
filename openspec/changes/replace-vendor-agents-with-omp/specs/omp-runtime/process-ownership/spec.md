## Purpose

Defines how the daemon starts, supervises, reclaims and stops the omp process behind every coding-agent Session, so a Session's process outlives any interface and is never confused with, or driven by, a process the daemon cannot confirm is its own.

## ADDED Requirements

### Requirement: The daemon owns an omp Session's process, not its creator

A coding-agent Session's omp process SHALL be started and owned by the daemon, never by whichever interface happened to create the Session. Closing every interface MUST NOT end the process, and the process SHALL continue running until the daemon itself stops it or the Session ends.

The daemon SHALL be the only actor that stops an omp process on purpose. An interface MAY ask the daemon to end a Session, but MUST NOT hold a process handle of its own to kill.

#### Scenario: Closing every interface leaves the Session running

- **WHEN** every Magentic interface connected to a running coding-agent Session is closed
- **THEN** the Session's omp process keeps running
- **AND** it remains reachable when an interface reconnects

#### Scenario: A Session's process is not a child of its creating interface

- **WHEN** an interface that created a coding-agent Session exits
- **THEN** the omp process it caused to start is not sent along with it
- **AND** the daemon still reports the process as running

### Requirement: A session is usable only after its handshake is confirmed

Magentic SHALL read the `ready` frame an omp process sends before treating its Session as usable, and SHALL record the protocol version the Session negotiated. A Session whose handshake Magentic does not understand SHALL be refused with a stated reason, never guessed at or assumed compatible.

Because omp's command and event vocabulary carries no published compatibility guarantee beyond the framing, Magentic SHALL treat a handshake it cannot interpret as a reason to refuse the Session rather than as license to keep driving it and hope.

#### Scenario: A recognized handshake makes the Session usable

- **WHEN** an omp process sends its `ready` frame with a protocol version Magentic supports
- **THEN** the negotiated protocol version is recorded against the Session
- **AND** the Session becomes usable

#### Scenario: An unrecognized handshake refuses the Session

- **WHEN** an omp process sends a `ready` frame naming no protocol version Magentic supports
- **THEN** the Session is refused with a reason naming the unsupported handshake
- **AND** Magentic does not send further commands to that process

### Requirement: An unusable or absent omp is refused, not worked around

When omp cannot be started, cannot complete its handshake, or exits before it does, Magentic SHALL refuse the Session with a stated reason. Magentic MUST NOT silently degrade the Session to a reduced mode and MUST NOT fall back to hosting the coding agent through any other runtime.

#### Scenario: omp fails to start

- **WHEN** starting the omp process for a new coding-agent Session fails
- **THEN** the Session is refused with a reason naming the failure
- **AND** no other runtime is substituted in its place

### Requirement: Reclaiming a process after a daemon restart is identity-confirmed

After the daemon restarts, it SHALL reclaim an omp process belonging to an existing Session only by confirming that process's own recorded identity, never by matching a process name, command line, or working directory against the process table.

A process the daemon cannot confirm as belonging to a specific Session SHALL be left alone: Magentic MUST NOT kill a process it cannot confirm is its own, even when it looks like a plausible match.

#### Scenario: A restarted daemon reclaims its own process

- **WHEN** the daemon restarts and finds a recorded Session whose process identity it can confirm
- **THEN** the Session is reclaimed and reported as running
- **AND** the process is not restarted

#### Scenario: An unconfirmable process is left running

- **WHEN** the daemon restarts and a recorded Session's process identity cannot be confirmed
- **THEN** the Session is reported as unreclaimed with a stated reason
- **AND** the daemon does not kill the process it failed to confirm

### Requirement: Requests and responses correlate on identity, not order

A host driving an omp Session SHALL correlate each response to the request that produced it by request identity, and MUST NOT assume responses arrive in the order their requests were sent.

#### Scenario: Responses arrive out of order

- **WHEN** two commands are sent to an omp Session and their responses arrive in the reverse order
- **THEN** each response is matched to the request that carries its identity
- **AND** neither result is attributed to the wrong request

### Requirement: Credentials are the developer's own, never stored or proxied by Magentic

The daemon SHALL run omp as the same user, under a dedicated omp profile reserved for Magentic, so that the developer's own omp configuration — in particular per-tool approval policies — does not apply to Sessions Magentic starts. Credentials for that profile SHALL be established by the developer in omp itself, under that profile. Magentic MUST NOT store, copy, or proxy a provider credential, and MUST NOT copy the developer's other profiles into Magentic's.

The profile name SHALL be fixed and stated to the developer, so that they can log in under it in omp's own interface.

When omp asks the host for secret input during a login — an `input` request for a key or token — Magentic SHALL cancel that request without collecting anything, and SHALL report to the developer by name which provider needs a login and that it must happen in omp's own interface under Magentic's profile.

#### Scenario: A Session uses Magentic's profile

- **WHEN** a coding-agent Session is started
- **THEN** its omp process is launched under Magentic's dedicated profile
- **AND** a per-tool approval policy in the developer's own omp profile does not apply to it

#### Scenario: A login asking for secret input is cancelled and named

- **WHEN** omp asks the host for a key or token while completing a login
- **THEN** Magentic cancels the request without collecting any input
- **AND** it reports which provider needs a login, that it must happen in omp's own interface, and under which profile
- **AND** no credential passes through Magentic

### Requirement: A Session's working directory is the process's working directory

The directory a coding-agent Session is bound to — including a worktree — SHALL be the working directory the omp process is launched with. Magentic MUST NOT launch a Session's process in a different directory than the one recorded against the Session.

#### Scenario: A worktree Session launches in its worktree

- **WHEN** a coding-agent Session bound to a worktree is started
- **THEN** the omp process is launched with that worktree as its working directory
- **AND** no other directory is substituted
