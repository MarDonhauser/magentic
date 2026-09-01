## Purpose

Status resolution decides one semantic Session status from runtime presence,
hook reports, and manifest inference, and fixes what happens when none of them
can answer, so every consumer of Observation sees the same rules and the same
honest unknown.

## ADDED Requirements

### Requirement: Semantic status vocabulary

An Observation SHALL report exactly one status per Session from: `working`,
`blocked` (waiting for developer input), `done` (the agent finished work the
developer has not acknowledged), `idle` (at rest with nothing pending),
`exited` (the agent process left its pane), `dead` (the runtime is gone),
`terminal` (a Session that hosts a shell rather than a coding agent), and
`unknown`. `done` and `idle` SHALL be distinguishable from status alone,
without consulting a separate unread flag.

#### Scenario: Finished turn versus resting Session

- **WHEN** an agent has completed work the developer has not yet seen
- **THEN** its status SHALL be `done`, and a Session at rest whose last result
  the developer already saw SHALL be `idle`

### Requirement: Resolution precedence

Status SHALL be resolved in this order and no other: runtime presence first
(an absent runtime is `dead`; a pane running a login shell where an agent is
expected is `exited`; a Session that hosts a shell is `terminal`); then a fresh
hook report; then manifest inference over the pane snapshot; then `unknown`.

#### Scenario: Hook report disagrees with the snapshot

- **WHEN** a fresh hook report says `working` while the manifest infers `idle`
  from the current snapshot
- **THEN** the resolved status SHALL be `working` and the Observation SHALL name
  the hook as its status source

#### Scenario: Stale hook report with a matching snapshot

- **WHEN** the latest hook report is stale and the manifest infers `blocked`
- **THEN** the resolved status SHALL be `blocked` and the Observation SHALL name
  the snapshot as its status source

#### Scenario: Runtime is gone despite a fresh report

- **WHEN** the Session's runtime no longer exists and a fresh hook report exists
- **THEN** the resolved status SHALL be `dead`

### Requirement: Observation states its status source

Every Observation SHALL carry which source produced its status: the hook
channel, the pane snapshot, runtime presence, or none. A consumer SHALL be able
to distinguish a confirmed status from an inferred one without re-deriving it.

#### Scenario: Consumer reads a confirmed status

- **WHEN** a status came from a fresh hook report
- **THEN** the Observation SHALL name the hook channel as the source

### Requirement: Unknown is explicit and never softened

When no source can determine a Session's status, the Observation SHALL report
`unknown`. Per ADR 0004, `unknown` SHALL NOT be reported, counted, sorted, or
acted on as `idle`, `done`, `dead`, or `exited`, and SHALL NOT be silently
replaced by the Session's previous status. A consumer MAY display the last
known status, but SHALL label it as a preserved earlier reading rather than a
current one.

#### Scenario: Unfamiliar screen for a supported kind

- **WHEN** a Session's agent kind is known but its snapshot matches none of the
  kind's rules and no fresh hook report exists
- **THEN** the status SHALL be `unknown`

#### Scenario: Pane content could not be read

- **WHEN** the runtime is present but its pane content could not be read
- **THEN** the Observation SHALL be partial and the status SHALL be `unknown`
  rather than `idle` or `dead`

#### Scenario: Unknown in a status roll-up

- **WHEN** a count is produced over Sessions by status
- **THEN** Sessions with `unknown` status SHALL NOT be counted as `idle`, `done`,
  or `dead`

### Requirement: Unknown is fail-closed for automated input

Magentic SHALL NOT deliver a queued prompt, an automated command, or any typed
input into a Session whose status is `unknown`.

#### Scenario: Queued prompt meets an unknown status

- **WHEN** a prompt is queued for a Session whose status resolves to `unknown`
- **THEN** the prompt SHALL remain queued and nothing SHALL be typed into the
  Session

### Requirement: Detection latency budget

A snapshot-inferred status transition SHALL become visible within one
observation cycle plus its evaluation, and manifest evaluation for one Session
SHALL be bounded so that a full observation cycle over the registered Sessions
stays inside the cycle interval. A hook-reported transition SHALL become
visible without waiting for the next observation cycle, within a stated
sub-second budget from acceptance of the report.

#### Scenario: Snapshot-only transition

- **WHEN** an agent kind without hook reporting moves from working to blocked
- **THEN** the change SHALL be visible in the next Observation, no later than
  one observation cycle plus one evaluation after the screen changed

#### Scenario: Hook-reported transition

- **WHEN** an agent with hook reporting reports `blocked`
- **THEN** the change SHALL be visible within the stated sub-second budget,
  without waiting for the next observation cycle

#### Scenario: Evaluation cost stays bounded

- **WHEN** manifest evaluation for one Session exceeds its per-snapshot time
  budget
- **THEN** that evaluation SHALL be abandoned, the Session's status SHALL be
  `unknown` for that cycle, and the remaining Sessions SHALL still be observed
  within the cycle

### Requirement: Attention derives from resolved status

The attention mapping SHALL be derived from the resolved semantic status:
`working` needs no developer action, `blocked` needs input, `done` invites
review, `terminal` and `dead` need no attention, and `unknown` maps to unknown
attention rather than to none.

#### Scenario: Unknown status and attention

- **WHEN** a Session's status is `unknown`
- **THEN** its attention state SHALL be unknown, and no notification asserting
  that the Session is finished or idle SHALL be produced
