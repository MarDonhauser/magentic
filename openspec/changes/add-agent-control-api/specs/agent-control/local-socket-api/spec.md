## Purpose

Carries the control vocabulary over a local Unix-domain socket so a script or a
coding agent can drive Magentic and follow Session status transitions live,
reachable only by the local user who owns the running Magentic.

## ADDED Requirements

### Requirement: Local socket endpoint

Magentic SHALL serve the control API on a Unix-domain socket in the user's own
runtime location. It SHALL NOT bind a TCP port or any network-reachable
address. The socket path SHALL be discoverable by clients without a
configuration file, and SHALL be recreated when a stale socket from a dead
Magentic process is found.

#### Scenario: Socket is served while Magentic runs

- **WHEN** Magentic is running with the control API enabled
- **THEN** the socket exists at the discoverable path and accepts connections
  from the owning user

#### Scenario: No network listener

- **WHEN** the control API is serving
- **THEN** no TCP or otherwise network-reachable listener is opened for it

#### Scenario: Stale socket is reclaimed

- **WHEN** a socket file remains from a Magentic process that no longer exists
- **THEN** the starting process replaces it and serves on the same path

#### Scenario: Another live Magentic already serves

- **WHEN** a second Magentic process starts and the socket is served by a live
  process
- **THEN** the second process does not take the socket over, and it reports the
  control API as served elsewhere

### Requirement: Local-user authorization

The socket SHALL be created with owner-only permissions, and every accepted
connection SHALL be authorized against the credentials of the connecting
process: only the user ID that owns the serving Magentic is admitted. A
connection failing that check SHALL be closed with an explicit authorization
outcome. There SHALL be no token, password, or per-client scope.

#### Scenario: Owning user connects

- **WHEN** a process running as the owning user connects
- **THEN** the connection is accepted and may issue requests

#### Scenario: Another user connects

- **WHEN** a process running as a different user connects
- **THEN** the connection is refused with an authorization outcome and no
  request is executed

#### Scenario: Socket permissions

- **WHEN** the socket is created
- **THEN** its permissions admit only the owning user

### Requirement: Request and response protocol

The protocol SHALL be line-delimited JSON: each request is one document naming
a verb, its arguments, and a client-chosen request identifier; each response is
one document carrying that identifier, an outcome, and the verb's result. An
unparseable or unknown request SHALL produce an error response rather than
closing the connection. A connection SHALL support multiple sequential requests.

#### Scenario: Request and matching response

- **WHEN** a client sends a valid request document with a request identifier
- **THEN** the server answers with one response document carrying the same
  identifier

#### Scenario: Unknown verb

- **WHEN** a client sends a request naming a verb the server does not implement
- **THEN** the server answers with an error response carrying a stable outcome
  code, and the connection stays open

#### Scenario: Malformed request

- **WHEN** a client sends a document that cannot be parsed as a request
- **THEN** the server answers with an error response and the connection stays
  open

#### Scenario: Sequential requests

- **WHEN** a client sends a second request on the same connection after a
  response
- **THEN** the second request is served

### Requirement: Verb parity with the command surface

The socket SHALL implement exactly the verbs of the command surface —
`session start`, `list`, `send`, `output`, `wait`, `kill` — with the same
addressing, scoping, and outcome semantics. No verb SHALL exist only on the CLI
and no verb SHALL exist only on the socket.

#### Scenario: Every CLI verb has a socket verb

- **WHEN** the control surface is exercised
- **THEN** each CLI verb maps to one socket verb, and the outcome codes match

#### Scenario: Addressing rules are shared

- **WHEN** a socket request addresses a Session by an ambiguous bare name
- **THEN** it is refused with the same ambiguity outcome the CLI reports

### Requirement: Session event stream

The socket SHALL offer a subscription that streams Session state changes as they
are observed: status transitions between running, waiting, idle, exited, and
dead, and changes in Observation availability. Each event SHALL carry the
SessionID, the ProjectID, the RuntimeName, the previous and new state, and the
observation time. A subscription SHALL accept optional Project and Session
filters, and SHALL end when the client disconnects or unsubscribes.

#### Scenario: Status transition is emitted

- **WHEN** a subscribed client is watching a Session and that Session's observed
  status changes
- **THEN** an event carrying the SessionID, previous state, and new state is
  written to the subscription

#### Scenario: Availability change is emitted

- **WHEN** a watched Session's Observation moves between known, partial, and
  unavailable
- **THEN** an event reporting the new availability is emitted, and an
  unavailable Observation is never emitted as a concrete status

#### Scenario: Filtered subscription

- **WHEN** a client subscribes with a Project filter
- **THEN** only events for Sessions of that Project reach it

#### Scenario: Subscription ends

- **WHEN** a subscribed client disconnects
- **THEN** the subscription is released and no further events are produced for it

#### Scenario: A slow consumer never stalls Magentic

- **WHEN** a subscribed client stops reading its events
- **THEN** Magentic's own observation and interface work continues, and the
  stalled subscription is dropped with an explicit outcome rather than blocking

### Requirement: Serving without an attached interface

The control API SHALL be servable without the TUI or the desktop app being in
the foreground, so a Session started through it survives the interface closing.
When no Magentic process serves the socket, a control client SHALL report the
API as unavailable with a distinct outcome rather than silently starting one.

#### Scenario: Interface closes, socket keeps serving

- **WHEN** the process serving the control API keeps running after its interface
  is detached
- **THEN** the socket continues to accept requests

#### Scenario: Nothing is serving

- **WHEN** a client connects and no Magentic process serves the socket
- **THEN** the client reports a distinct unavailable outcome naming the expected
  socket path, and no Magentic process is started implicitly

### Requirement: Concurrency and coordination

Requests that mutate Sessions SHALL go through the same interprocess-coordinated
Registry changes and durable desired-state transitions as the interfaces, so a
control request and a person acting in the UI cannot produce a lost update or an
orphan runtime.

#### Scenario: Concurrent mutations are serialized

- **WHEN** a control request and an interface action mutate the same Session at
  the same time
- **THEN** both are applied as coordinated semantic changes and neither silently
  discards the other's result

#### Scenario: Read requests do not block on mutations

- **WHEN** a long-running mutation holds a Session transition
- **THEN** `session list` and `session output` still answer
