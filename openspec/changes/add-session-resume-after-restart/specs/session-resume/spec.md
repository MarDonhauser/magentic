## Purpose

Lets a developer pick up coding-agent work after the machine or the tmux server restarted, by keeping each Session's resume facts durably and offering an honest one-click resume that recreates the runtime and hands the vendor its own conversation back.

## ADDED Requirements

### Requirement: Durable resume facts per Session

Magentic SHALL durably record, for every Session it provisions, the facts needed to resume it later: its Project, the working directory it runs in, whether that directory is a Worktree, the agent kind it hosts, the vendor conversation reference (`AgentRunRef`) when one is known, its name, and the last status observed for it together with the time of that observation.

These facts SHALL survive a restart of Magentic, of the tmux server, and of the machine. The last observed status SHALL be recorded as an explicit fact with its observation time; the absence of such a record SHALL be readable as "never observed" and MUST NOT be presented as any particular status.

#### Scenario: Last known status is recorded with its time

- **WHEN** an Observation pass reports a status for a Session
- **THEN** that status and the time of the observation are persisted with the Session record
- **AND** the persisted status is replaced, not appended to, by the next observation

#### Scenario: Resume facts survive a machine restart

- **WHEN** Magentic starts after the machine rebooted and the tmux server is gone
- **THEN** every Session it had recorded is still listed with its Project, working directory, agent kind, name, conversation reference, and last known status and time

#### Scenario: A Session never observed carries no status

- **WHEN** a Session record exists that has never been observed
- **THEN** its last known status reads as unknown
- **AND** it is not presented as idle, running, or dead

### Requirement: Resumability is a distinct reading of a Session

Magentic SHALL classify a Session whose external runtime is absent into exactly one of two readings: **resumable**, when its durable record carries everything a resume needs, or **dead**, when it does not. A resumable Session MUST NOT be presented as running, and MUST NOT be presented as dead.

A Session SHALL be read as resumable only when all of the following hold: its runtime is observed to be absent (not merely unobservable), its recorded working directory still exists, its agent kind is known and its Adapter reports a resume behavior other than "cannot resume", and — for a vendor that resumes by conversation reference — a conversation reference is recorded.

When the runtime cannot be observed at all, the Session's availability SHALL stay explicitly unknown, and Magentic MUST NOT claim it is resumable or dead.

#### Scenario: Runtime gone, record intact

- **WHEN** the tmux runtime for a coding-agent Session no longer exists and its record carries a known agent kind, an existing working directory, and a conversation reference
- **THEN** the Session is presented as resumable
- **AND** its last known status and the time it was last seen are shown alongside it

#### Scenario: Runtime gone, record unusable

- **WHEN** the tmux runtime no longer exists and the recorded working directory is missing, the agent kind is unknown, or the vendor requires a conversation reference and none is recorded
- **THEN** the Session is presented as dead
- **AND** the reason it cannot be resumed is stated

#### Scenario: Terminal Session after a restart

- **WHEN** the runtime of a terminal Session (one hosting no coding agent) is gone
- **THEN** it is not presented as resumable
- **AND** it may be restarted only as a plain shell in its recorded directory

#### Scenario: Runtime state cannot be observed

- **WHEN** the runtime probe fails or times out rather than reporting absence
- **THEN** the Session's availability is presented as unknown
- **AND** neither resume nor discard is offered as if the runtime were known to be gone

### Requirement: Per-agent-kind resume behavior is explicit

Every supported agent kind SHALL declare its resume behavior explicitly as one of: resumes a stored conversation by reference, restarts fresh in the recorded directory without a conversation, or cannot be resumed. Magentic SHALL derive the offered action from that declaration and SHALL NOT infer resumability from whether a command line happens to be constructible.

An agent kind that resumes by reference SHALL be resumed with that vendor's own resume command line applied to the recorded conversation reference. An agent kind that cannot restore a conversation SHALL be offered as "start fresh here" and MUST NOT be labelled as resuming the conversation.

#### Scenario: Vendor that resumes by conversation reference

- **WHEN** a resumable Session hosts an agent kind that resumes by reference and a reference is recorded
- **THEN** the resume issues that vendor's resume command for that reference, for example `claude --resume <id>`

#### Scenario: Vendor that cannot restore the conversation

- **WHEN** a Session's agent kind declares that it cannot restore a stored conversation
- **THEN** the offered action is described as starting the agent fresh in the recorded directory
- **AND** no wording claims the previous conversation is restored

#### Scenario: Recorded conversation no longer exists at the vendor

- **WHEN** a resume is attempted and the vendor reports it no longer holds the recorded conversation
- **THEN** the resume fails with that reason stated
- **AND** the Session remains listed with its record intact
- **AND** starting the agent fresh in the recorded directory is offered as the alternative

### Requirement: Resuming a Session recreates its runtime

Resuming a Session SHALL be a deliberate developer action that creates a new external runtime for that Session in its recorded working directory and issues the agent kind's resume command there. The Session SHALL keep its durable identity, its name, its Project association, and its conversation reference across the resume; only the runtime is new.

The resume SHALL record its durable intent before any external runtime is touched and SHALL be advanced idempotently, so an interrupted resume leaves neither an orphan runtime nor a Session that is silently started twice. Magentic MUST NOT resume any Session automatically at startup.

#### Scenario: Successful resume

- **WHEN** the developer resumes a resumable Session
- **THEN** a new runtime is created in the Session's recorded working directory
- **AND** the agent kind's resume command is issued in it
- **AND** the Session keeps its identity, name, Project, and conversation reference
- **AND** it is presented as running once the runtime is observed

#### Scenario: Resume is never automatic

- **WHEN** Magentic starts and finds resumable Sessions
- **THEN** no runtime is created for them
- **AND** each is offered a resume action for the developer to trigger

#### Scenario: Interrupted resume is reconciled

- **WHEN** a resume is interrupted after the runtime was created but before it was recorded as converged
- **THEN** the next reconciliation completes that same intent against the observed facts
- **AND** no second runtime is created for the Session

#### Scenario: Working directory no longer available

- **WHEN** a resume is attempted and the recorded working directory no longer exists
- **THEN** the resume fails with that reason stated
- **AND** no runtime is created

### Requirement: Discarding a resumable record

Magentic SHALL offer discarding a resumable Session's record as an action distinct from removing a running Session. Discarding SHALL remove the Session's durable record and its resume facts, and SHALL NOT touch the Project, the Worktree, the working directory, or the vendor's own conversation history.

#### Scenario: Discard a resumable Session

- **WHEN** the developer discards a resumable Session
- **THEN** the Session no longer appears in Magentic
- **AND** its working directory, its Worktree, and the vendor's stored conversation are left untouched

#### Scenario: Discard is not offered for a running Session

- **WHEN** a Session's runtime is observed to exist
- **THEN** discard is not offered for it
- **AND** ending it goes through the existing removal action instead

### Requirement: Magentic never claims processes survive a restart

Magentic's presentation of resumable Sessions SHALL state that the agent's conversation is resumed, not that its process survived. No label, notification, or documentation SHALL describe a Session as still running, still working, or preserved across a machine reboot or a tmux server restart.

#### Scenario: Wording after a reboot

- **WHEN** resumable Sessions are presented after a reboot
- **THEN** they are described as resumable work whose runtime is gone
- **AND** none of them is described as running, alive, or preserved
