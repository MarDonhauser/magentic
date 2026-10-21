## Purpose

Gives Magentic's own interfaces direct control over a running omp Session's model, thinking level, fast mode, compaction and turn behaviour, and the live state to show for them, replacing the need to reach into the agent's own terminal.

## ADDED Requirements

### Requirement: Live session state is read from the session, not from rendered output

Magentic SHALL surface the active model and its provider, the thinking level, fast-mode status, context usage, whether a turn is currently streaming, and how many messages are queued, as facts read from the Session's own reported state. These facts MUST NOT be derived by parsing terminal output.

The surfaced state SHALL be refreshed when the Session reports a change, so an interface does not show a stale value while the Session has since moved on.

#### Scenario: Session state is shown from reported facts

- **WHEN** an interface displays a running coding-agent Session
- **THEN** the model, provider, thinking level, fast-mode status, context usage, streaming status and queued-message count shown are the values the Session last reported
- **AND** none of them is derived from rendered terminal text

#### Scenario: State updates when the session changes it

- **WHEN** a running Session's reported state changes without any action from Magentic's interface
- **THEN** the surfaced state is updated to match
- **AND** the previous value is not shown after the update is known

### Requirement: A developer changes the model and thinking level of a running Session

A developer SHALL be able to change the model and the thinking level of a running coding-agent Session from Magentic's own interfaces, without opening the agent's own terminal. A change applies to the Session it was made on and to no other Session.

The interface SHALL reflect what the Session reports back after the change, not what was requested: where the Session rejects or clamps a requested value, the surfaced state SHALL show the value the Session actually holds.

#### Scenario: A developer switches the model

- **WHEN** a developer selects a different model for a running Session from Magentic's interface
- **THEN** the change is sent to that Session only
- **AND** the interface then shows the model the Session reports holding

#### Scenario: A requested change is rejected or clamped

- **WHEN** a developer requests a thinking level the Session does not accept as given
- **THEN** the interface shows the thinking level the Session actually reports afterward
- **AND** it does not show the requested value as if it had taken effect

### Requirement: The offered model list matches what the session can route to

The set of models Magentic offers for a running Session SHALL be the set the Session itself reports as available. Magentic MUST NOT offer a model the Session does not report, and MUST NOT hard-code a list that can drift from what the Session can actually route a turn to.

#### Scenario: The model list reflects the session's own report

- **WHEN** a developer opens the model picker for a running Session
- **THEN** the offered models are exactly the ones that Session reported as available
- **AND** a model absent from that report is not offered

### Requirement: Compaction is controllable and its outcome is defined during a turn

A developer SHALL be able to trigger compaction of a running Session's context from Magentic's interface. Whether automatic compaction is enabled SHALL be a readable and settable state, not an assumed default.

Triggering compaction while a turn is streaming SHALL be refused with a stated reason. omp itself neither queues nor refuses it: it compacts mid-turn, stalls the stream, and can leave the turn without a reported end. Magentic MUST NOT pass such a request through.

#### Scenario: A developer compacts an idle session

- **WHEN** a developer triggers compaction on a Session that is not streaming
- **THEN** compaction is requested
- **AND** the interface reflects compaction as in progress until the Session reports it complete

#### Scenario: Compaction is requested during a streaming turn

- **WHEN** a developer triggers compaction while the Session is streaming a turn
- **THEN** the request is refused with a reason saying a turn is running
- **AND** no compaction is sent to the session

### Requirement: The current turn can be interrupted and turn modes are readable and settable

A developer SHALL be able to interrupt a Session's current turn from Magentic's interface. The steering mode, follow-up mode, and interrupt mode SHALL each be readable as the Session's own reported state and settable from Magentic's interface.

#### Scenario: A developer interrupts a running turn

- **WHEN** a developer interrupts a Session that is streaming a turn
- **THEN** an interrupt is sent to that Session
- **AND** the interface reflects the interruption once the Session reports the turn ended

#### Scenario: A turn mode is changed and read back

- **WHEN** a developer changes the steering, follow-up, or interrupt mode of a running Session
- **THEN** the change is sent to that Session
- **AND** the interface then shows the mode the Session reports holding

### Requirement: An unconfirmed control outcome does not overwrite known state

When Magentic sends a control command and the Session's response does not confirm it took effect, the control SHALL be reported as not applied, and the previously known value SHALL NOT be overwritten by the value that was merely requested.

A fact this capability cannot currently determine SHALL be represented explicitly as unknown and MUST NOT be rendered as if it were a real, current value.

#### Scenario: A control command receives no confirming response

- **WHEN** a control command is sent and the Session's process ends or times out before confirming the change
- **THEN** the interface reports the control as not applied
- **AND** the previously known state is retained rather than replaced by the requested value

#### Scenario: An undetermined fact is shown as unknown

- **WHEN** a piece of session state has not yet been reported by the Session
- **THEN** the interface shows it as unknown
- **AND** it is not rendered as any specific model, level, or mode

### Requirement: This capability grants no tool approval

This capability MUST NOT expose any control that grants or denies a tool's permission to run. Approving or denying a tool call belongs exclusively to the approval-gate capability.

#### Scenario: No session-control action approves a tool

- **WHEN** the set of controls this capability exposes is enumerated
- **THEN** none of them grants or denies a pending tool approval
- **AND** an open approval request is only ever resolved through the approval gate
