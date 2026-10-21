## Purpose

Derives a coding-agent Session's status from omp's reported lifecycle events rather than from a captured screen, and extends the existing status vocabulary and unknown contract to a protocol-reported source.

## ADDED Requirements

### Requirement: Status is derived from reported lifecycle events, not a screen

Magentic SHALL derive the status of a Session running under the omp runtime from omp's reported lifecycle events. Magentic MUST NOT capture a pane or infer status from terminal content for such a Session.

The reported events SHALL be treated as facts rather than as hints to be reconciled against a snapshot: where a lifecycle event names a status, that status SHALL NOT be second-guessed by inference from anything else.

#### Scenario: No pane is captured for an omp Session

- **WHEN** the status of a Session running under the omp runtime is observed
- **THEN** the status is derived from omp's reported lifecycle events
- **AND** no pane is captured for that Session

### Requirement: The status source records that a status was protocol-reported

An Observation SHALL record where a status came from, and the set of sources SHALL gain one naming the omp protocol, so a consumer can tell a status omp reported from a status inferred by snapshot matching or from a hook report.

A status derived under this capability SHALL always carry that source. An interface MAY use the source to explain a status, but MUST NOT treat two statuses with different sources as differing in kind.

#### Scenario: A protocol-reported status names its source

- **WHEN** a Session's status is derived from an omp lifecycle event
- **THEN** the Observation records the omp protocol as the status source
- **AND** the source is distinguishable from a hook-reported or snapshot-inferred source

### Requirement: Lifecycle events map onto the existing status vocabulary

Magentic SHALL derive **working** from a running turn: a `turn_start` without a matching `turn_end` SHALL read as working.

Magentic SHALL derive **awaiting a decision** from an outstanding approval request: while a tool approval request is open against the Session, its status SHALL read as awaiting a decision, taking precedence over a running turn.

Magentic SHALL derive **done** from a terminal `agent_end`: an `agent_end` event whose `isTerminal` field is absent or is not `false` SHALL read as done. An `agent_end` event whose `isTerminal` field is `false` SHALL NOT read as done, because maintenance or asynchronously delivered work has already been scheduled to continue; the Session's status SHALL remain working in that case.

Magentic SHALL derive **idle** for a Session that has read as done and been acknowledged, and for a Session that has not yet started a turn.

#### Scenario: A running turn reads as working

- **WHEN** omp reports `turn_start` and no matching `turn_end` has been reported since
- **THEN** the Session's status reads as working

#### Scenario: A terminal agent_end reads as done

- **WHEN** omp reports `agent_end` and the event carries no `isTerminal` field or carries `isTerminal: true`
- **THEN** the Session's status reads as done

#### Scenario: A non-terminal agent_end does not read as done

- **WHEN** omp reports `agent_end` with `isTerminal: false`
- **THEN** the Session's status does not read as done
- **AND** it continues to read as working

#### Scenario: An open approval request takes precedence over working

- **WHEN** a tool approval request is open against a Session whose turn is running
- **THEN** the Session's status reads as awaiting a decision, not as working

### Requirement: A local slash command resolves status without an agent_end

A prompt that omp resolves as a local slash command SHALL still resolve the Session to a definite status, using the `prompt` response carrying `data.agentInvoked: false` rather than waiting for an `agent_end` that will never be reported.

Magentic MUST NOT leave a Session reading as working indefinitely because a prompt it sent never invoked the agent.

#### Scenario: A local slash command completes without an agent_end

- **WHEN** a delivered prompt is answered with `agentInvoked: false` and no `agent_end` is reported for it
- **THEN** the Session's status resolves from that response
- **AND** it does not remain working while waiting for an `agent_end`

### Requirement: A status the protocol did not report stays explicitly unknown

Consistent with ADR 0004, a status omp has not reported SHALL be represented as explicitly unknown. Unknown MUST NOT be rendered, counted, or acted on as idle, done, or dead. Prompt delivery to a Session whose status is unknown SHALL remain fail-closed.

A Session whose process the daemon cannot reach SHALL be reported as unobservable, naming the daemon as the reason it cannot be reached. It MUST NOT be reported as dead: the daemon's inability to reach the process is not evidence that the process has ended.

#### Scenario: An unreported status stays unknown

- **WHEN** no lifecycle event has yet given a Session a status
- **THEN** the Session's status reads as explicitly unknown
- **AND** it is not rendered, counted, or acted on as idle, done, or dead

#### Scenario: Prompt delivery refuses an unknown status

- **WHEN** a prompt is queued for delivery to a Session whose status is unknown
- **THEN** delivery is refused
- **AND** the refusal is fail-closed rather than defaulting to delivery

#### Scenario: An unreachable process is unobservable, not dead

- **WHEN** the daemon cannot reach the process backing a Session
- **THEN** the Session's status reads as unobservable, naming the daemon
- **AND** it is not reported as dead

### Requirement: A turn's boundaries and a prompt's delivery are acknowledged facts

Magentic SHALL treat prompt delivery to an omp Session as acknowledged by the session rather than assumed after a fixed delay: a prompt SHALL NOT be considered delivered until omp confirms receipt of it.

A `success` response to `prompt` is not on its own that confirmation, because omp can answer the same request twice, first with success and then with a refusal when a turn is already running. A prompt SHALL count as delivered only once omp has echoed it as the user message that opens a turn, or has answered it as resolved locally. While a turn is streaming, Magentic SHALL send further input as a steer or follow-up rather than as a new prompt.

A turn's start and end SHALL be recorded from `turn_start` and `turn_end` as reported facts, replacing any assumption about when a turn began or finished from typed input or elapsed time.

#### Scenario: A prompt is not considered delivered until acknowledged

- **WHEN** a prompt is sent to an omp Session
- **THEN** it is not recorded as delivered until omp acknowledges receiving it
- **AND** no fixed delay is used to assume delivery

#### Scenario: A prompt refused after an initial success stays queued

- **WHEN** omp answers a prompt first with success and then, on the same request, with a refusal
- **THEN** the prompt is not recorded as delivered
- **AND** it stays queued

#### Scenario: Turn boundaries come from reported events

- **WHEN** a turn begins and ends during an omp Session
- **THEN** its start is recorded from `turn_start` and its end from `turn_end`
- **AND** neither boundary is guessed from typed input or elapsed time

### Requirement: A protocol-reported transition is visible without a polling cycle

A status transition reported by omp's lifecycle events SHALL become visible to Magentic's interfaces without waiting for a polling or observation cycle. This latency SHALL be a testable contract, not an approximation.

#### Scenario: A reported transition is visible immediately

- **WHEN** omp reports a lifecycle event that changes a Session's status
- **THEN** the new status is visible to interfaces without waiting for the next observation cycle
