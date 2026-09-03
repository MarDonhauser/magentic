## Purpose

Brings a coding agent's permission prompts out of its terminal and into Magentic, so a developer can see what an agent is asking for and decide there — while guaranteeing that nobody but the developer ever answers.

## ADDED Requirements

### Requirement: A managed Session's permission prompts reach Magentic

For a managed Session, a vendor permission prompt SHALL be delivered to Magentic as a **PermissionRequest** carrying what is being asked for, the Session it belongs to, and the time it was raised.

A PermissionRequest SHALL be readable by every connected interface, and SHALL survive interfaces connecting and disconnecting while it is open.

Magentic MUST NOT present a managed Session as working while it holds an open PermissionRequest.

#### Scenario: A permission prompt becomes a request

- **WHEN** a managed Session's agent asks for permission to act
- **THEN** Magentic holds an open PermissionRequest for that Session stating what is asked

#### Scenario: A request outlives an interface

- **WHEN** a PermissionRequest is open and the interface that could see it is closed and reopened
- **THEN** the same request is still open and still shown

### Requirement: A Session with an open request waits and is surfaced

A managed Session holding an open PermissionRequest SHALL be observed as waiting for a decision. This SHALL be its own status, distinct from working, from idle, and from waiting for a prompt.

Such a Session SHALL raise the developer's attention through the existing Attention model, following ADR 0007: the attention intent is planned before any notification is emitted.

#### Scenario: Waiting for a decision is its own status

- **WHEN** a managed Session holds an open PermissionRequest
- **THEN** its observed status is waiting for a decision
- **AND** it is distinguishable from a Session that is idle or working

#### Scenario: The developer is told

- **WHEN** a PermissionRequest is opened for a managed Session
- **THEN** an attention intent is planned for that Session before any notification is emitted

### Requirement: Only a person decides

Magentic SHALL answer a PermissionRequest only on an explicit decision made by the developer. Magentic MUST NOT allow, deny, or time out a PermissionRequest on its own, and MUST NOT do so because no interface is connected.

There SHALL be no setting, mode, or configuration in Magentic that answers permission prompts automatically.

A PermissionRequest that is never answered SHALL stay open, and its Session SHALL stay waiting, for as long as the agent process lives.

#### Scenario: An unwatched Session waits indefinitely

- **WHEN** a PermissionRequest is opened and no interface is connected
- **THEN** the request stays open
- **AND** the Session stays waiting for a decision
- **AND** nothing is allowed or denied

#### Scenario: No automatic answer exists

- **WHEN** Magentic's settings are enumerated
- **THEN** none of them causes a PermissionRequest to be answered without a developer decision

#### Scenario: A decision is attributed to the developer

- **WHEN** a PermissionRequest is answered
- **THEN** the answer was made by an explicit developer action

### Requirement: A decision is delivered once and closes the request

Answering a PermissionRequest SHALL deliver the decision to the waiting agent exactly once and SHALL close the request.

Answering a request that is already closed SHALL be refused with that reason, and MUST NOT deliver a second decision. Two interfaces answering the same open request SHALL result in exactly one delivered decision, and the second SHALL be refused.

When the agent process ends while a request is open, the request SHALL be closed as no longer answerable, with that reason, and MUST NOT be presented as allowed or denied.

#### Scenario: A second answer is refused

- **WHEN** two interfaces answer the same open PermissionRequest
- **THEN** exactly one decision is delivered to the agent
- **AND** the other is refused because the request is closed

#### Scenario: A dead agent closes its request honestly

- **WHEN** a managed Session's agent process ends while a PermissionRequest is open
- **THEN** the request closes as no longer answerable
- **AND** it is not recorded as allowed or denied

### Requirement: A permission decision is part of the Session's account

An opened PermissionRequest and its outcome — allowed, denied, or no longer answerable — SHALL appear in the Session's recorded activity, in the order they occurred.

#### Scenario: The decision is visible afterwards

- **WHEN** a PermissionRequest has been answered
- **THEN** both the request and its outcome appear in the Session's activity at the point they occurred
