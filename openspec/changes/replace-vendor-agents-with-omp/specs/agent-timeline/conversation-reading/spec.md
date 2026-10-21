## MODIFIED Requirements

### Requirement: A Conversation is read incrementally while the agent works

Magentic SHALL keep a watched Session's Conversation current using the reading mechanism its vendor uses. For a vendor whose Conversation is read from an on-disk record, Magentic SHALL normalize only the vendor records appended since the previous reading. For a vendor whose Conversation is accumulated from a live event stream, Magentic SHALL incorporate only the events observed since the previous reading. In both cases Magentic SHALL publish the resulting new Items to the interfaces.

Incremental reading of an on-disk record SHALL be driven by the existing Observation cadence, and Magentic MUST NOT run a second observation loop for Conversations. Incorporating events from a live stream is driven by the stream's own arrival, not by polling; publishing the resulting Items to interfaces MUST NOT require a second, separate loop beyond what Observation already drives for on-disk vendors.

A Conversation read from an on-disk record SHALL be read incrementally only for Sessions an interface is currently presenting; a Session nobody is watching MUST NOT be read on every pass. A Conversation accumulated from a live event stream SHALL be persisted as its events arrive regardless of whether an interface is presenting the Session, because a stream not captured as it arrives is lost rather than merely deferred; what is gated by presentation for a stream-based Conversation is publication to interfaces, not persistence.

#### Scenario: New activity appears without re-reading everything

- **WHEN** a watched Session's vendor appends records to an on-disk record after a previous reading
- **THEN** only the appended records are normalized
- **AND** the resulting Items are published as new Items of that Conversation

#### Scenario: New activity arrives on a live stream

- **WHEN** a watched Session's vendor delivers new events on a live stream after a previous reading
- **THEN** only the newly observed events are incorporated
- **AND** the resulting Items are published as new Items of that Conversation

#### Scenario: No new activity publishes nothing

- **WHEN** an Observation pass finds no records appended since the previous reading, or no new events have arrived on a live stream
- **THEN** no Items are published for that Conversation

#### Scenario: Unwatched Sessions are not read

- **WHEN** an Observation pass runs and no interface is presenting a given Session whose Conversation is read from an on-disk record
- **THEN** that Session's Conversation is not read

#### Scenario: An unwatched stream-based Conversation is still persisted

- **WHEN** a Session's vendor delivers events on a live stream while no interface is presenting that Session
- **THEN** Magentic persists the resulting Items
- **AND** it does not publish them to an interface that is not presenting the Session

### Requirement: A vendor record rewritten from the start forces a full re-reading

This requirement governs a vendor whose Conversation is read from an on-disk record. When such a record no longer extends what Magentic read before — because it was truncated, replaced, or rewritten — Magentic SHALL discard its incremental position and normalize the record from the beginning, replacing the Conversation it held.

Magentic MUST NOT append normalized Items onto a Conversation whose earlier content it can no longer account for.

A vendor whose Conversation is accumulated from a live event stream has no on-disk record for Magentic to re-read from the beginning. Its Conversation is amended only by incorporating new events as they arrive; Magentic MUST NOT discard and rebuild a stream-based Conversation from the beginning on its own initiative.

#### Scenario: A truncated record is re-read in full

- **WHEN** an on-disk Conversation record is shorter than at the previous reading
- **THEN** it is normalized from the beginning
- **AND** the previously held Items for that Conversation are replaced rather than extended

#### Scenario: A stream-based Conversation is never rebuilt from the beginning

- **WHEN** new events arrive on a live stream for a Conversation Magentic has already partly accumulated
- **THEN** the existing Items are amended rather than discarded and renormalized from the beginning

### Requirement: Reading a Conversation never disturbs the agent

Reading a Conversation SHALL be read-only with respect to everything the vendor owns. Magentic MUST NOT write to, move, truncate, or lock a vendor's on-disk Conversation record, and MUST NOT send anything to the Session's runtime as part of reading, whether the Conversation is read from an on-disk record or accumulated from a live event stream.

An on-disk Conversation record that is being written while Magentic reads it SHALL yield the records completed so far; a partially written trailing record SHALL be skipped and read again on a later pass rather than normalized into a damaged Item. A live event stream observed while an event is still in progress SHALL yield only the events completed so far; an event observed incomplete at reading time MUST NOT be normalized into a damaged Item, and SHALL be incorporated once it completes.

#### Scenario: Reading leaves the vendor's record untouched

- **WHEN** a Conversation is read from an on-disk record
- **THEN** the vendor's record is unchanged in content, position and permissions

#### Scenario: A half-written trailing record is deferred

- **WHEN** the last record in an on-disk Conversation file is incomplete at the time of reading
- **THEN** it produces no Item on that pass
- **AND** it is normalized on a later pass once complete

#### Scenario: An in-progress stream event is deferred

- **WHEN** an event on a live stream is still in progress at the time Magentic incorporates what has arrived
- **THEN** it produces no Item until it completes
- **AND** it is incorporated once complete without being normalized as a damaged Item
