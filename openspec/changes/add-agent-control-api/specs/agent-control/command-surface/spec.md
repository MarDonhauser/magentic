## Purpose

Gives a process — a script or a coding agent — the same control over Magentic's
Projects and Sessions that the TUI and the desktop app give a person, through a
stable command vocabulary whose output is safe to parse.

## ADDED Requirements

### Requirement: Control command vocabulary

Magentic SHALL expose a control command surface under `magentic session` with
the verbs `start`, `list`, `send`, `output`, `wait`, and `kill`. Each verb SHALL
be a client of the local control API and MUST NOT reach an external runtime,
the Registry, or the filesystem on its own path.

#### Scenario: Verb reaches the control API

- **WHEN** any `magentic session` verb runs
- **THEN** it issues the corresponding control API request and reports the
  response
- **AND** it performs no runtime, Registry, or Git mutation outside that request

#### Scenario: Unknown verb

- **WHEN** a caller invokes `magentic session` with a verb outside the defined
  set
- **THEN** the command exits non-zero, names the unknown verb, and lists the
  supported verbs
- **AND** no Session is created, prompted, or ended

### Requirement: Session addressing by durable identity

Control commands SHALL address a Session by its SessionID, or by a
Project-qualified name that is resolved to exactly one SessionID before any
action is taken. A bare name that is ambiguous across Projects SHALL be
rejected rather than resolved by guessing. Resolution SHALL follow the durable
identities of ADR 0001: a name is a label, never lookup authority beyond this
resolution step.

#### Scenario: Address by SessionID

- **WHEN** a command is given a SessionID that is registered
- **THEN** the command acts on that Session

#### Scenario: Project-qualified name

- **WHEN** a command is given a name together with a Project
- **AND** exactly one Session in that Project carries the name
- **THEN** the command resolves it to that Session's SessionID and reports the
  resolved SessionID in its output

#### Scenario: Ambiguous bare name

- **WHEN** a command is given a bare name carried by Sessions in more than one
  Project
- **THEN** the command exits non-zero with an ambiguity outcome that lists the
  candidate SessionIDs and Projects
- **AND** no Session is acted on

#### Scenario: Unknown Session

- **WHEN** a command addresses a SessionID or name that is not registered
- **THEN** the command exits non-zero with a not-found outcome naming what was
  addressed

### Requirement: Project and Worktree scoping

`session start` SHALL require a Project and SHALL accept an optional Worktree
scope: an existing Worktree of that Project, or a request to provision a fresh
managed Worktree. `session list` SHALL accept optional Project and Worktree
filters. A Worktree SHALL be addressed by a Project-qualified handle resolved by
Repositories immediately before use, never by a caller-supplied filesystem path
taken on trust.

#### Scenario: Start in the Project directory

- **WHEN** `session start` is given a Project and no Worktree scope
- **THEN** the Session is provisioned in that Project's directory

#### Scenario: Start in a fresh managed Worktree

- **WHEN** `session start` is given a Project and requests a fresh Worktree
- **THEN** a managed Worktree is provisioned for that Project and the Session is
  provisioned inside it
- **AND** the response reports the resolved Worktree

#### Scenario: Path outside the Project is refused

- **WHEN** `session start` is given a directory that Repositories does not
  resolve to a Worktree physically contained in the addressed Project
- **THEN** the command exits non-zero with a containment outcome and provisions
  nothing

#### Scenario: List filtered by Project

- **WHEN** `session list` is given a Project filter
- **THEN** only Sessions of that Project are listed

### Requirement: Session start selects agent kind

`session start` SHALL accept the kind of agent to run — the coding-agent vendor,
or a plain terminal Session — and SHALL accept an optional initial prompt. The
initial prompt SHALL be delivered as an explicit applied fact; if delivery
outcome is unknown after a transport failure, the command SHALL report delivery
as unknown and MUST NOT resend it automatically.

#### Scenario: Start a vendor agent Session

- **WHEN** `session start` names a supported coding-agent vendor
- **THEN** the Session is provisioned with that vendor and the response reports
  the SessionID, the vendor, and the resolved working directory

#### Scenario: Start a terminal Session

- **WHEN** `session start` requests a terminal Session
- **THEN** a Session hosting only a shell is provisioned, and it accepts no
  agent-directed prompt

#### Scenario: Unsupported vendor

- **WHEN** `session start` names a vendor Magentic does not support
- **THEN** the command exits non-zero naming the supported vendors and
  provisions nothing

#### Scenario: Initial prompt delivery is unknown

- **WHEN** an initial prompt was submitted and its delivery outcome cannot be
  established
- **THEN** the response reports the Session as started with prompt delivery
  unknown
- **AND** the prompt is not delivered again without a new explicit request

### Requirement: Sending input to a Session

`session send` SHALL submit text to a Session that hosts a coding agent and
SHALL report whether the text was delivered, was queued for delivery, or was
refused. It SHALL refuse to send agent-directed input to a terminal Session.

#### Scenario: Deliverable Session

- **WHEN** `session send` targets a Session whose Observation shows it ready for
  input
- **THEN** the text is delivered and the response reports delivery

#### Scenario: Session not ready

- **WHEN** `session send` targets a Session that is not currently ready for
  input
- **THEN** the text is queued and the response reports it as queued, naming the
  queued message

#### Scenario: Terminal Session refuses agent input

- **WHEN** `session send` targets a terminal Session
- **THEN** the command exits non-zero with a refusal outcome and sends nothing

### Requirement: Reading Session output

`session output` SHALL return the current visible content of a Session's runtime
with the terminal control sequences removed, together with the Observation
availability that produced it. It SHALL accept a limit on how many trailing
lines are returned.

#### Scenario: Readable Session

- **WHEN** `session output` targets a Session whose runtime is readable
- **THEN** the response carries the normalized content and reports the
  Observation as known

#### Scenario: Unreadable runtime

- **WHEN** the Session's runtime cannot be read within the observation budget
- **THEN** the response reports the Observation as unavailable or partial and
  MUST NOT present empty content as an empty Session

#### Scenario: Trailing line limit

- **WHEN** `session output` is given a line limit
- **THEN** at most that many trailing lines are returned

### Requirement: Ending a Session

`session kill` SHALL end a Session's runtime through the durable desired-state
lifecycle. It SHALL leave any Worktree in place and SHALL be idempotent for a
Session whose runtime is already gone.

#### Scenario: Running Session is ended

- **WHEN** `session kill` targets a Session with a live runtime
- **THEN** the runtime is stopped and the response reports the Session as
  stopped

#### Scenario: Already stopped

- **WHEN** `session kill` targets a Session whose runtime no longer exists
- **THEN** the command succeeds and reports the Session as already stopped

#### Scenario: Worktree survives

- **WHEN** `session kill` ends a Session running in a managed Worktree
- **THEN** the Worktree and its checkout remain in place

### Requirement: Machine-readable output contract

Every control command SHALL support a machine-readable output mode that emits
one JSON document carrying the outcome, the resolved SessionID where one was
resolved, and a stable machine-readable outcome code on failure. Human-readable
output SHALL carry the same facts but is not a parsing contract. Exit codes
SHALL distinguish success, a refused or failed request, and an addressing error.

#### Scenario: Success in machine-readable mode

- **WHEN** a command runs in machine-readable mode and succeeds
- **THEN** exactly one JSON document is written to standard output, it carries
  the resolved SessionID, and the exit code is zero

#### Scenario: Failure in machine-readable mode

- **WHEN** a command runs in machine-readable mode and fails
- **THEN** exactly one JSON document is written carrying a stable outcome code
  and a human-readable message
- **AND** the exit code is non-zero

#### Scenario: Diagnostics never pollute the document

- **WHEN** a command in machine-readable mode emits progress or diagnostic text
- **THEN** that text goes to standard error and the JSON document on standard
  output stays parseable on its own
