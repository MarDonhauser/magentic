## Purpose

Presents a Session's Conversation in the desktop app as a readable account of the agent's work, so a developer can see what was said, run and changed without scrolling a redrawn terminal.

## ADDED Requirements

### Requirement: The desktop app presents the selected Session's Conversation

The desktop app SHALL offer the Conversation of the selected Session as a reading surface beside its terminal, and SHALL let the developer switch between them without ending or disturbing the Session.

Switching surfaces MUST NOT change what the Session is doing, and MUST NOT change which Session is selected.

Items SHALL appear in Conversation order, with the most recent activity reachable without manual scrolling when the developer has not scrolled away from it.

#### Scenario: Switching to the Conversation leaves the agent alone

- **WHEN** the developer switches the selected Session from its terminal to its Conversation
- **THEN** the Conversation is shown
- **AND** the Session's runtime, status and selection are unchanged

#### Scenario: New activity appears while reading

- **WHEN** new Items are published for the Conversation being read
- **THEN** they appear in the surface without the developer reloading it

#### Scenario: Reading back does not fight the developer

- **WHEN** the developer has scrolled back into earlier Items and new Items arrive
- **THEN** the view stays where the developer put it

### Requirement: Tool activity is collapsed by default and expandable

An Item whose kind describes tool activity SHALL be presented as one line carrying its title, and SHALL be expandable to its detail when it has one.

Agent messages and developer prompts SHALL be presented in full, not collapsed.

The failure of a tool activity SHALL be visible in the collapsed line, so a developer does not have to expand Items to find what went wrong.

#### Scenario: A command is one line until expanded

- **WHEN** a command-execution Item is presented
- **THEN** it occupies one line showing its title
- **AND** expanding it reveals its detail

#### Scenario: A failure is visible while collapsed

- **WHEN** a tool activity Item records a failure
- **THEN** its collapsed line shows that it failed

#### Scenario: Prose is not hidden behind a toggle

- **WHEN** an agent-message Item is presented
- **THEN** its text is shown without the developer expanding anything

### Requirement: Delegated work is presented under its task

Items marked as delegated SHALL be presented beneath the delegated-task Item they belong to, and MUST NOT be interleaved with the run's primary Items.

Delegated Items whose parent is unknown SHALL be presented as delegated work of unknown origin rather than as primary activity.

#### Scenario: Subagent activity groups under its task

- **WHEN** a Conversation holds a delegated task and the Items produced inside it
- **THEN** those Items are presented under that task

#### Scenario: Orphaned delegated work is still visible

- **WHEN** a Conversation holds delegated Items whose parent task is unknown
- **THEN** they are presented as delegated work of unknown origin
- **AND** they are not shown as primary activity

### Requirement: An unavailable Conversation says so in the surface

When a Conversation is unavailable, the surface SHALL state which reading applies — not applicable for this Session, no normalizer for this vendor, record not found, or record unreadable — and SHALL name the reason.

The surface MUST NOT render an unavailable Conversation as an empty one, and MUST NOT imply that the agent has done nothing.

When a Conversation is unavailable, the surface SHALL keep the Session's terminal reachable, since the terminal remains the only complete reading in that case.

#### Scenario: An unsupported vendor is named in the surface

- **WHEN** the selected Session hosts a vendor with no normalizer
- **THEN** the surface states that this vendor's Conversations cannot be read yet
- **AND** it names the vendor
- **AND** the terminal stays reachable

#### Scenario: An empty Conversation reads differently from a missing one

- **WHEN** the selected Session's Conversation is available and holds no Items
- **THEN** the surface states that the run has produced nothing yet
- **AND** this differs from the wording shown when the record is missing

### Requirement: The terminal remains the place where a Session is answered

The Conversation surface SHALL be a reading surface. It MUST NOT offer to answer a vendor's permission prompt, because such a prompt is not part of a Conversation and cannot be answered from it.

When the agent is waiting for the developer, the surface SHALL say so and SHALL offer the way to its terminal.

#### Scenario: A waiting agent points at its terminal

- **WHEN** the selected Session is observed to be waiting for the developer
- **THEN** the Conversation surface states that it is waiting
- **AND** it offers the way to that Session's terminal

#### Scenario: No approval controls are offered

- **WHEN** the Conversation surface is presented
- **THEN** it offers no control that claims to grant or deny a vendor permission prompt
