## Purpose

Lets one agent block until another agent's work in a Session actually finishes,
pinned to the exact occupant it resolved, so a replacement agent in the same
Session can never be mistaken for the one that was awaited.

## ADDED Requirements

### Requirement: Wait resolves and pins an occupant identity

`session wait` SHALL resolve the addressed Session once, at the start of the
wait, to an occupant identity consisting of the SessionID, the RuntimeName
addressed at that moment, and the AgentRunRef of the coding-agent run then
occupying it. That occupant identity SHALL be pinned for the whole wait and
SHALL be reported in the response. Every later evaluation of the wait condition
SHALL be made against the pinned identity, never against whatever currently
carries the same name.

#### Scenario: Occupant is pinned at resolution

- **WHEN** `session wait` starts on a Session hosting a coding-agent run
- **THEN** the wait pins the SessionID, RuntimeName, and AgentRunRef of that run
- **AND** the response reports the pinned occupant identity

#### Scenario: Rename does not break the wait

- **WHEN** the awaited Session's display name changes while the wait runs
- **AND** the pinned SessionID, RuntimeName, and AgentRunRef still describe the
  same live occupant
- **THEN** the wait continues unaffected

#### Scenario: No occupant to pin

- **WHEN** `session wait` addresses a Session with no resolvable coding-agent
  run
- **THEN** the wait fails immediately with a distinct no-occupant outcome and
  does not block

### Requirement: Wait conditions

`session wait` SHALL support waiting until the pinned occupant is `done` — it
has stopped working and is idle, awaiting a new prompt — and until it is
`waiting`, meaning it needs human input. Each condition SHALL be evaluated from
Session Observation, and an Observation that is unavailable or partial SHALL NOT
be treated as either condition being met.

#### Scenario: Wait until done

- **WHEN** the pinned occupant transitions from running to idle
- **THEN** the wait returns with a `done` outcome naming the pinned occupant

#### Scenario: Wait until waiting-for-input

- **WHEN** a wait for `waiting` is running and the pinned occupant starts
  needing human input
- **THEN** the wait returns with a `waiting` outcome

#### Scenario: Unavailable Observation is not a result

- **WHEN** the Session's Observation is unavailable or partial
- **THEN** the wait neither returns `done` nor `waiting`, and keeps waiting
  until the Observation is known again or another terminal outcome applies

#### Scenario: Occupant needing input during a done-wait

- **WHEN** a wait for `done` is running and the pinned occupant starts needing
  human input
- **THEN** the wait reports that the occupant is blocked on input, and returns
  with a distinct blocked outcome rather than reporting the work as done

### Requirement: Occupant replacement fails the wait

If the pinned occupant is replaced during the wait — the Session's runtime is
recreated under a different RuntimeName, a different AgentRunRef occupies it, or
the SessionID is removed and a new Session takes the name — the wait SHALL end
with a distinct `occupant-replaced` outcome. A replacement occupant reaching an
idle state MUST NOT satisfy the wait.

#### Scenario: Runtime recreated under a new RuntimeName

- **WHEN** the awaited Session's runtime is stopped and provisioned again under
  a different RuntimeName
- **THEN** the wait ends with `occupant-replaced` and names the pinned and the
  observed occupant

#### Scenario: A different agent run takes the Session

- **WHEN** a different AgentRunRef comes to occupy the pinned Session
- **THEN** the wait ends with `occupant-replaced`, even if that run is idle

#### Scenario: Session removed and re-created under the same name

- **WHEN** the pinned Session is removed and a new Session is registered with
  the same name in the same Project
- **THEN** the wait ends with `occupant-replaced` and never resolves to the new
  Session

#### Scenario: Replacement idling does not satisfy the wait

- **WHEN** a replacement occupant reaches idle while a `done` wait is pending
- **THEN** the wait does not return `done`

### Requirement: Terminal outcomes of a wait

`session wait` SHALL end with exactly one of a fixed set of outcomes, each
carrying a stable machine-readable code: `done`, `waiting`, `blocked`,
`occupant-replaced`, `session-gone`, `timeout`, or `cancelled`. It SHALL accept
a timeout and SHALL treat a Session whose runtime is confirmed gone without
reaching the condition as `session-gone`, not as `done`.

#### Scenario: Timeout

- **WHEN** the requested timeout elapses before any other outcome applies
- **THEN** the wait ends with `timeout` and reports the last known state of the
  pinned occupant

#### Scenario: Session dies during the wait

- **WHEN** the pinned Session's runtime is confirmed to no longer exist and the
  awaited condition was never met
- **THEN** the wait ends with `session-gone` and not with `done`

#### Scenario: Cancelled

- **WHEN** the waiting client disconnects or cancels
- **THEN** the wait ends with `cancelled` and holds no coordination

#### Scenario: Exactly one outcome

- **WHEN** a wait ends
- **THEN** it reports exactly one outcome code from the defined set

### Requirement: Waiting does not block Magentic

A pending wait SHALL NOT hold a Session transition, a Registry coordination, or
the observation loop. Several waits, from several clients, on the same or
different Sessions SHALL be servable at once.

#### Scenario: Waits hold no coordination

- **WHEN** a wait is pending on a Session
- **THEN** other control requests and interface actions on that Session are
  still served

#### Scenario: Concurrent waits

- **WHEN** several clients wait on the same Session at the same time
- **THEN** each receives its own outcome, each evaluated against its own pinned
  occupant identity
