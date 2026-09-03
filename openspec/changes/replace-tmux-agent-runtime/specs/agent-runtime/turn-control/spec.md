## Purpose

Gives managed Sessions the turn semantics a keystroke channel cannot express: a prompt whose delivery is acknowledged, a turn whose start and end are facts rather than guesses, and a turn that can be interrupted.

## ADDED Requirements

### Requirement: A prompt to a managed Session is acknowledged

Delivering a prompt to a managed Session SHALL yield an acknowledgement from the agent that the prompt was received, or a failure. The Outbox SHALL treat a prompt as delivered only on that acknowledgement.

The Outbox MUST NOT infer delivery from elapsed time, from observed status, or from content appearing afterwards.

An unacknowledged prompt SHALL remain queued and SHALL stay visible to the developer as queued. Magentic MUST NOT deliver the same prompt twice on the strength of a missing acknowledgement.

#### Scenario: Delivery is confirmed before the queue advances

- **WHEN** a queued prompt is delivered to a managed Session
- **THEN** it leaves the queue only once the agent acknowledges it

#### Scenario: A failed delivery keeps the prompt queued

- **WHEN** delivering a prompt to a managed Session fails
- **THEN** the prompt is still queued
- **AND** the failure reason is available to the developer

#### Scenario: A missing acknowledgement does not resend

- **WHEN** a prompt has been sent to a managed Session and no acknowledgement has arrived
- **THEN** the prompt is not sent again
- **AND** it is presented as in flight rather than as delivered

### Requirement: A turn's start and end are observable facts

For a managed Session, Magentic SHALL observe the start and the end of each turn from the vendor protocol, and SHALL record the end reason: completed, interrupted, or failed with the vendor's own reason.

A turn's end MUST NOT be inferred from a Session going quiet.

#### Scenario: A completed turn is recorded as completed

- **WHEN** a managed Session's agent finishes a turn on its own
- **THEN** the turn is recorded as ended with the completed reason

#### Scenario: Silence is not an ending

- **WHEN** a managed Session produces no output for an extended period during a turn
- **THEN** the turn is still recorded as running

### Requirement: A managed turn can be interrupted

Magentic SHALL offer interrupting the running turn of a managed Session. Interrupting SHALL stop the current turn and leave the Session and its agent process alive and able to accept the next prompt.

Interrupting a Session with no running turn SHALL be refused with that reason, and MUST NOT stop the process.

An interrupted turn SHALL be recorded as ended with the interrupted reason, and the work the agent already did SHALL remain in its conversation.

#### Scenario: Interrupting ends the turn, not the Session

- **WHEN** the developer interrupts a managed Session's running turn
- **THEN** the turn ends with the interrupted reason
- **AND** the Session's process is still running and accepts the next prompt

#### Scenario: Nothing to interrupt is refused

- **WHEN** the developer interrupts a managed Session that has no running turn
- **THEN** the request is refused with that reason
- **AND** the process is unaffected

#### Scenario: Interrupted work is not erased

- **WHEN** a turn is interrupted after the agent has already run tools
- **THEN** that activity remains in the Session's conversation

### Requirement: Managed output streams as it is produced

A managed Session's agent output SHALL be published as it is produced, rather than only once a message is complete.

Streaming SHALL produce the same final activity as a completed message would. A partially received message MUST NOT be presented as a finished one.

#### Scenario: Output appears before the message ends

- **WHEN** a managed Session's agent is producing a long message
- **THEN** its text is published while the message is still being produced
- **AND** it is marked as still being produced

#### Scenario: The finished message matches what streamed

- **WHEN** a streamed message completes
- **THEN** the recorded activity holds that message once, in its final form
