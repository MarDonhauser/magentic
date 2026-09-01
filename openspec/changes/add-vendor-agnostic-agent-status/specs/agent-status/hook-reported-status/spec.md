## Purpose

Where a coding-agent vendor can report its own lifecycle, Magentic accepts those
reports on one local channel instead of guessing from the screen, so status is
confirmed rather than inferred and appears without waiting for the next
observation cycle.

## ADDED Requirements

### Requirement: One vendor-neutral local report channel

Magentic SHALL accept status reports from agent processes through a single
local channel owned by the current user. The channel SHALL be local-only: no
network listener, owner-only access, and every accepted report SHALL be
attributable to the owning user. The channel SHALL NOT be specific to one
vendor; supporting a second vendor SHALL require configuration and a shipped
hook definition, not a new transport.

#### Scenario: Report from the owning user

- **WHEN** a process running as the owning user submits a well-formed status
  report for a known Session
- **THEN** the report SHALL be accepted and recorded as that Session's latest
  hook-reported status

#### Scenario: Report from another user

- **WHEN** a report arrives that cannot be attributed to the owning user
- **THEN** it SHALL be rejected and SHALL NOT affect any Session's status

### Requirement: Report vocabulary

A status report SHALL carry a state from the same semantic vocabulary the
manifests use — `working`, `blocked`, `done`, `idle` — plus the reporting
instant, the reporting vendor, an addressing identity, and an optional short
detail. A report carrying any other state, or missing a required field, SHALL
be rejected with a stated reason and SHALL leave the Session's previous status
untouched.

#### Scenario: Report with an unsupported state

- **WHEN** a report declares a state outside the semantic vocabulary
- **THEN** the report SHALL be rejected and the Session SHALL keep the status it
  had before the report arrived

#### Scenario: Report carrying a blocked detail

- **WHEN** a report declares `blocked` together with a detail describing what is
  being approved
- **THEN** the Session's status detail SHALL be that reported detail

### Requirement: Reports are correlated under stable identities

A report SHALL address a Session through identities Magentic already owns —
`SessionID`, or `RuntimeName` plus the vendor's `AgentRunRef` — per ADR 0001.
A report whose addressing identity resolves to no registered Session SHALL be
discarded. A report SHALL NOT be matched to a Session by display name.

#### Scenario: Report for an unknown Session

- **WHEN** a report addresses an identity that resolves to no registered Session
- **THEN** the report SHALL be discarded without affecting any other Session

#### Scenario: Session runtime was replaced

- **WHEN** a report addresses a `RuntimeName` and `AgentRunRef` that no longer
  occupy the Session they once did
- **THEN** the report SHALL NOT be applied to that Session

### Requirement: Reports have a freshness window

Every recorded report SHALL carry the instant it was received. A report SHALL
be treated as authoritative only inside a bounded freshness window; outside it,
the report SHALL be treated as stale and SHALL no longer determine the
Session's status. The freshness window SHALL be long enough to cover an agent
that is working quietly without emitting further events, and MAY be extended by
a vendor's periodic keep-alive report.

#### Scenario: Fresh report during a long turn

- **WHEN** an agent reported `working` and is still inside the freshness window
- **THEN** the Session SHALL be reported as `working` even if the pane snapshot
  currently shows no recognized activity marker

#### Scenario: Report outgrows its window

- **WHEN** the freshness window for a Session's latest report has elapsed
- **THEN** that report SHALL be marked stale and SHALL no longer be used as the
  Session's status source

### Requirement: Later reports supersede earlier ones

For one Session, the report with the latest reporting instant SHALL be the
current one. A report that arrives out of order and is older than the recorded
one SHALL be discarded rather than reverting the Session.

#### Scenario: Out-of-order delivery

- **WHEN** a report whose reporting instant precedes the recorded report arrives
- **THEN** it SHALL be discarded and the recorded report SHALL remain current

### Requirement: Claude Code hook installation is explicit and reversible

Magentic SHALL be able to install the hook definitions that make Claude Code
report its lifecycle, SHALL state what it writes and where before writing it,
and SHALL be able to remove them again. Installation SHALL be idempotent and
SHALL NOT discard or reorder hook definitions that Magentic did not write.

#### Scenario: Installing alongside existing hooks

- **WHEN** hook installation runs in a configuration that already contains the
  user's own hooks
- **THEN** Magentic's hook definitions SHALL be added and the user's existing
  hook definitions SHALL be preserved unchanged

#### Scenario: Installing twice

- **WHEN** hook installation runs again on a configuration Magentic already
  installed into
- **THEN** the result SHALL be unchanged and no duplicate definitions SHALL be
  written

#### Scenario: Uninstalling

- **WHEN** the developer removes Magentic's hook integration
- **THEN** only Magentic's own hook definitions SHALL be removed, and affected
  Sessions SHALL fall back to snapshot-inferred status

### Requirement: Hooks are never a prerequisite

A Session whose vendor reports nothing, or whose hooks are not installed, SHALL
remain fully supported through snapshot inference. Absence of hook reports
SHALL NOT be represented as an error state, as `idle`, or as `dead`.

#### Scenario: Vendor without hooks

- **WHEN** a Session runs an agent kind that has no hook integration
- **THEN** its status SHALL be resolved from its manifest, and no failure SHALL
  be reported for the missing hook channel
