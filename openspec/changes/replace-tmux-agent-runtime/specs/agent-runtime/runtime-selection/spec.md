## Purpose

Makes the thing that owns a Session's coding-agent process an explicit, durable property of that Session, so a developer knows which actions a Session offers and Magentic never guesses whether a Session lives in tmux or under the daemon.

## ADDED Requirements

### Requirement: Every Session records its AgentRuntime

Magentic SHALL record, durably and per Session, which **AgentRuntime** owns its process: the **tmux** runtime or the **managed** runtime. The runtime SHALL be chosen when the Session is created and MUST NOT change for the life of that Session.

A Session record written before this change carries no runtime; its absence SHALL read as the tmux runtime, never as unknown and never as managed.

The runtime MUST NOT be inferred from observed facts, from the vendor, or from a global setting at read time.

#### Scenario: An existing Session record reads as tmux

- **WHEN** a Session record written before this change is loaded
- **THEN** its AgentRuntime reads as tmux
- **AND** every action it offered before is still offered

#### Scenario: The runtime is fixed at creation

- **WHEN** a Session has been created with the managed runtime
- **THEN** it reports the managed runtime for its whole life
- **AND** no action changes it to tmux

### Requirement: A vendor states which runtimes it supports

Each supported agent vendor SHALL declare which AgentRuntimes it can be run under. A vendor Magentic cannot drive headless SHALL declare that explicitly, and creating a managed Session for it SHALL be refused with that reason stated.

Terminal Sessions SHALL always use the tmux runtime.

#### Scenario: An unsupported vendor refuses the managed runtime

- **WHEN** a managed Session is requested for a vendor that declares no managed support
- **THEN** the request is refused
- **AND** the refusal names the vendor and states that it cannot be run headless
- **AND** no Session record is created

#### Scenario: Every vendor declares its runtimes

- **WHEN** the supported vendors are enumerated
- **THEN** each one declares the set of AgentRuntimes it supports
- **AND** every vendor supports the tmux runtime

#### Scenario: A terminal Session is always tmux

- **WHEN** a terminal Session is created
- **THEN** its AgentRuntime is tmux

### Requirement: Offered actions follow the runtime

The actions Magentic offers for a Session SHALL be derived from its AgentRuntime. An action a runtime cannot perform MUST NOT be offered for a Session using that runtime, and MUST NOT fail at execution time as the way of communicating that.

A managed Session SHALL NOT offer attaching to a terminal, because it has none. A tmux Session SHALL NOT offer interrupting a turn or answering a permission decision through Magentic, because those are answered in its pane.

#### Scenario: Attach is absent for a managed Session

- **WHEN** the actions for a managed Session are listed
- **THEN** attaching is not among them

#### Scenario: Interrupt is absent for a tmux Session

- **WHEN** the actions for a tmux Session are listed
- **THEN** interrupting a turn is not among them
- **AND** answering a permission decision is not among them

### Requirement: A managed Session can be continued in a terminal

Magentic SHALL offer, for a managed Session, a way to continue its conversation in an ordinary terminal, so the vendor's own interactive features stay reachable.

Continuing in a terminal SHALL create a separate Session with the tmux runtime, and SHALL leave the managed Session and its conversation intact. Magentic MUST NOT present this as moving a Session between runtimes.

#### Scenario: Continuing in a terminal leaves the managed Session alone

- **WHEN** the developer continues a managed Session in a terminal
- **THEN** a new tmux Session is created carrying the vendor's own continuation of that conversation
- **AND** the managed Session still exists with its runtime and its process unchanged

#### Scenario: The two Sessions are distinguishable

- **WHEN** a managed Session has been continued in a terminal
- **THEN** both Sessions are listed with their own identities and runtimes
