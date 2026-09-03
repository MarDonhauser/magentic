## Purpose

Locates the Conversation belonging to a Session, keeps it current while the agent works by reading only what the vendor appended, and states plainly when a Conversation is missing or unreadable instead of presenting silence as an empty run.

## ADDED Requirements

### Requirement: A Session's Conversation is located from its own record

Magentic SHALL resolve a Session's ConversationRef from the Session's recorded run reference and agent vendor. Resolution MUST NOT depend on the Session's name, its runtime name, or on matching text scraped from a terminal.

When a Session carries no run reference, or hosts no coding agent, the Conversation SHALL be reported as not applicable, distinct from missing.

#### Scenario: A coding-agent Session resolves its Conversation

- **WHEN** a Session records an agent vendor and a run reference
- **THEN** its ConversationRef resolves from those two facts alone

#### Scenario: A terminal Session has no Conversation

- **WHEN** a Conversation is requested for a Session that hosts no coding agent
- **THEN** the answer states that a Conversation is not applicable
- **AND** it is distinguishable from a Conversation that is missing or unreadable

#### Scenario: A renamed Session keeps its Conversation

- **WHEN** a Session is renamed
- **THEN** the same Conversation resolves for it afterwards

### Requirement: A Conversation is read incrementally while the agent works

Magentic SHALL keep a watched Session's Conversation current by normalizing only the vendor records appended since its previous reading, and SHALL publish the resulting new Items to the interfaces.

Incremental reading SHALL be driven by the existing Observation cadence. Magentic MUST NOT run a second observation loop for Conversations.

A Conversation SHALL be read incrementally only for Sessions an interface is currently presenting. A Session nobody is watching MUST NOT be read on every pass.

#### Scenario: New activity appears without re-reading everything

- **WHEN** a watched Session's vendor appends records after a previous reading
- **THEN** only the appended records are normalized
- **AND** the resulting Items are published as new Items of that Conversation

#### Scenario: No new activity publishes nothing

- **WHEN** an Observation pass finds no records appended since the previous reading
- **THEN** no Items are published for that Conversation

#### Scenario: Unwatched Sessions are not read

- **WHEN** an Observation pass runs and no interface is presenting a given Session
- **THEN** that Session's Conversation is not read

### Requirement: A vendor record rewritten from the start forces a full re-reading

When a vendor's Conversation record no longer extends what Magentic read before — because it was truncated, replaced, or rewritten — Magentic SHALL discard its incremental position and normalize the record from the beginning, replacing the Conversation it held.

Magentic MUST NOT append normalized Items onto a Conversation whose earlier content it can no longer account for.

#### Scenario: A truncated record is re-read in full

- **WHEN** a Conversation record is shorter than at the previous reading
- **THEN** it is normalized from the beginning
- **AND** the previously held Items for that Conversation are replaced rather than extended

### Requirement: An unavailable Conversation is reported with its reason

A Conversation that cannot be delivered SHALL be reported as unavailable together with the reason: not applicable for this Session, no normalizer for this vendor, the vendor's record could not be located, or the record could not be read. A reason SHALL be stated in every case.

An unavailable Conversation MUST NOT be presented as an empty Conversation, and MUST NOT be presented as a Session with no activity.

A Conversation that is located and readable but holds no Items yet SHALL be reported as empty, which is distinct from every unavailable reading.

#### Scenario: A missing record is not an empty Conversation

- **WHEN** a Session's Conversation record cannot be located
- **THEN** the Conversation reads as unavailable with the reason that its record was not found
- **AND** it does not read as empty

#### Scenario: An unreadable record states why

- **WHEN** a Conversation record exists but cannot be read
- **THEN** the Conversation reads as unavailable with the read failure as its reason

#### Scenario: A run that has not started yet reads as empty

- **WHEN** a Session's Conversation record exists and holds no records Magentic can normalize
- **THEN** the Conversation reads as empty and available

### Requirement: Reading a Conversation never disturbs the agent

Reading a Conversation SHALL be read-only with respect to everything the vendor owns. Magentic MUST NOT write to, move, truncate, or lock a vendor's Conversation record, and MUST NOT send anything to the Session's runtime as part of reading.

A Conversation record that is being written while Magentic reads it SHALL yield the records completed so far; a partially written trailing record SHALL be skipped and read again on a later pass rather than normalized into a damaged Item.

#### Scenario: Reading leaves the vendor's record untouched

- **WHEN** a Conversation is read
- **THEN** the vendor's record is unchanged in content, position and permissions

#### Scenario: A half-written trailing record is deferred

- **WHEN** the last record in a Conversation file is incomplete at the time of reading
- **THEN** it produces no Item on that pass
- **AND** it is normalized on a later pass once complete
