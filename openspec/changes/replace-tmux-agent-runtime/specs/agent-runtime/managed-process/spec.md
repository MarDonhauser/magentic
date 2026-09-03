## Purpose

Makes the Magentic daemon the owner of managed coding-agent processes, so those Sessions keep working when every interface is closed and are reclaimed rather than orphaned when the daemon itself restarts.

## ADDED Requirements

### Requirement: The daemon owns managed agent processes

A managed Session's agent process SHALL be started by, owned by, and stopped by the Magentic daemon. No interface SHALL start or own a managed agent process.

A managed agent process SHALL keep running when every interface disconnects, and its activity SHALL continue to be recorded while nobody is watching.

An interface that cannot reach the daemon SHALL report managed Sessions as unobservable with that reason, per ADR 0004, and MUST NOT report them as dead or as idle.

#### Scenario: Closing every interface leaves the agent running

- **WHEN** a managed Session is working and every interface is closed
- **THEN** its process keeps running
- **AND** reopening an interface shows the work that happened in between

#### Scenario: An unreachable daemon is not a dead Session

- **WHEN** an interface cannot reach the daemon
- **THEN** managed Sessions read as unobservable with the daemon named as the reason
- **AND** no managed Session reads as dead

#### Scenario: An interface never owns the process

- **WHEN** an interface requests a managed Session to start
- **THEN** the daemon starts and owns the process
- **AND** the process is not a child of the interface

### Requirement: The daemon reclaims its processes when it restarts

The daemon SHALL record, durably, enough about each managed process to recognize it again after the daemon restarts: the Session it belongs to, the process identity, and the facts needed to confirm that the running process is the one recorded.

On startup the daemon SHALL, for each recorded managed process, either reclaim it after confirming its identity, or record that it is gone and mark the Session accordingly. A recorded process whose identity cannot be confirmed MUST NOT be adopted, and MUST NOT be killed.

The daemon MUST NOT identify a process by matching a command line, a path, or a Session name against the process table.

#### Scenario: A surviving process is reclaimed

- **WHEN** the daemon restarts while a managed agent process is still running
- **THEN** that process is reclaimed for its Session
- **AND** it is not started a second time

#### Scenario: A process that exited is reported, not restarted

- **WHEN** the daemon restarts and a recorded managed process is gone
- **THEN** its Session is marked as having no running process
- **AND** no replacement process is started automatically

#### Scenario: An unconfirmable process is left alone

- **WHEN** the daemon restarts and a recorded process identity now belongs to a process whose identity cannot be confirmed
- **THEN** that process is neither adopted nor killed
- **AND** the Session is marked as having no running process

### Requirement: Only recorded processes are stopped

Magentic SHALL stop a managed agent process only through the process identity it recorded when it started or reclaimed that process. Magentic MUST NOT stop a process found by matching a name, a path, a working directory, or a Session name.

Stopping a managed Session SHALL end its agent process and its children, and SHALL leave its Worktree, its working directory and its vendor conversation record untouched.

#### Scenario: Stopping uses the recorded identity

- **WHEN** a managed Session is stopped
- **THEN** only the recorded process identity is signalled

#### Scenario: A stop leaves the work on disk

- **WHEN** a managed Session is stopped
- **THEN** its Worktree, working directory and vendor conversation record are unchanged

#### Scenario: No pattern-based termination

- **WHEN** a managed process must be stopped and its recorded identity no longer exists
- **THEN** Magentic reports that the process is gone
- **AND** it terminates nothing else

### Requirement: A managed Session's status comes from its own process

The observed status of a managed Session SHALL be derived from the daemon's own facts about that process and the vendor protocol it speaks. It MUST NOT be derived from tmux, and MUST NOT be derived from scraped terminal content.

A managed process that exits without being asked to SHALL leave its Session observed as having failed, with the exit reason stated. Magentic MUST NOT restart it automatically.

#### Scenario: Status is not read from a pane

- **WHEN** a managed Session's status is observed
- **THEN** no tmux command is issued for it

#### Scenario: An unexpected exit is stated, not repaired

- **WHEN** a managed agent process exits without having been asked to stop
- **THEN** the Session is observed as failed with the exit reason
- **AND** no replacement process is started

### Requirement: A managed process starts in the Session's own place

A managed agent process SHALL be started in the Session's recorded working directory, with the vendor's own conversation continuation when the Session already has one.

Magentic SHALL verify that the recorded working directory still exists and still resolves inside its Project before starting a process. A failing verification SHALL fail the start with the reason stated and SHALL start nothing.

#### Scenario: A missing working directory fails the start

- **WHEN** a managed Session is started and its recorded working directory no longer exists
- **THEN** the start fails with that reason
- **AND** no process is started

#### Scenario: A continued Session keeps its conversation

- **WHEN** a managed Session that already holds a vendor conversation reference is started
- **THEN** the process is started continuing that conversation
- **AND** the Session's conversation reference is unchanged
