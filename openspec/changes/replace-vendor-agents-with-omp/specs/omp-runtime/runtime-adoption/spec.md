## Purpose

Makes omp the runtime every coding-agent Session runs under, states what tmux keeps, and defines which Session actions exist once a coding agent no longer has a terminal pane of its own.

## ADDED Requirements

### Requirement: Coding-agent Sessions run under the omp runtime

Magentic SHALL offer exactly one runtime for a coding-agent Session: **omp**. A newly created coding-agent Session SHALL be given the omp runtime, and the developer SHALL NOT be asked to choose a runtime.

The runtime SHALL remain a recorded property of the Session rather than a global setting, so that a Session recorded before this change keeps the runtime it was created with. A Session record that names no runtime SHALL continue to read as tmux.

Creating a coding-agent Session SHALL ask for a model rather than for an agent vendor. Where a developer previously chose between Claude Code, Codex, Copilot and Antigravity, they SHALL choose among the models omp reports as available.

#### Scenario: A new coding-agent Session is created

- **WHEN** a developer creates a coding-agent Session
- **THEN** the Session records the omp runtime
- **AND** no runtime choice is presented
- **AND** the creation flow offers a model rather than an agent vendor

#### Scenario: A Session recorded before this change keeps its runtime

- **WHEN** a Session record created under an earlier version is read
- **THEN** a record naming tmux reads as tmux
- **AND** a record naming no runtime reads as tmux
- **AND** neither is silently rewritten to omp

### Requirement: tmux keeps terminal Sessions and loses coding agents

tmux SHALL remain the runtime for terminal Sessions, and the terminal dock, terminal creation and attaching to a terminal Session SHALL be unchanged by this capability.

tmux SHALL NOT host a newly created coding-agent Session. Magentic MUST NOT fall back to a tmux-hosted coding agent when omp is unavailable; an unavailable omp is refused with a stated reason rather than worked around.

#### Scenario: Terminal Sessions are untouched

- **WHEN** a developer creates a terminal Session
- **THEN** it runs under tmux exactly as before
- **AND** it can be attached to

#### Scenario: An unavailable omp does not fall back to tmux

- **WHEN** a coding-agent Session is created and omp cannot be started
- **THEN** the Session is refused with a reason naming omp
- **AND** no tmux-hosted coding agent is started in its place

### Requirement: Session actions reflect the absence of a pane

A coding-agent Session under the omp runtime SHALL declare the actions it supports, and `attach` SHALL NOT be among them, because there is no terminal pane belonging to the Session.

Magentic SHALL offer a way to reach omp's own interactive interface for such a Session, so that capabilities Magentic does not surface stay reachable. Opening that interface MUST NOT silently take the Session away from the daemon: it either joins the same session or states plainly that it is a separate view.

An interface MUST NOT present an action a Session's runtime does not support. Where an action is unavailable, the reason SHALL be stated rather than the control being silently absent from an otherwise identical Session.

#### Scenario: Attach is not offered for a coding-agent Session

- **WHEN** an interface lists the actions of a coding-agent Session under omp
- **THEN** attach is not among them
- **AND** the list states that this Session has no pane to attach to

#### Scenario: The developer reaches omp's own interface

- **WHEN** a developer asks to open omp's interface for a running coding-agent Session
- **THEN** an interactive omp interface for that session opens
- **AND** the relationship to the Magentic-owned session is stated
- **AND** the daemon does not lose ownership of the process without saying so

### Requirement: Existing tmux and managed Sessions are carried across explicitly

A Session created under the tmux or managed runtime SHALL keep running under that runtime until it ends. Magentic MUST NOT migrate a live Session to omp underneath a running turn.

When such a Session is resumed, Magentic SHALL offer to resume it as an omp Session, and SHALL state what carries across and what does not. Where the Session's recorded vendor has an importer, the offer SHALL include importing the existing conversation; where it has none, the offer SHALL say so explicitly rather than presenting an import that would silently start empty.

#### Scenario: A live tmux coding-agent Session is not migrated

- **WHEN** a coding-agent Session recorded with the tmux runtime is observed while running
- **THEN** it continues under tmux
- **AND** it is not restarted under omp

#### Scenario: Resuming an older Session offers the omp path

- **WHEN** a developer resumes a coding-agent Session recorded with the tmux or managed runtime
- **THEN** resuming it as an omp Session is offered
- **AND** the offer states whether the existing conversation can be imported
- **AND** a vendor without an importer is named as such rather than offering an empty import
