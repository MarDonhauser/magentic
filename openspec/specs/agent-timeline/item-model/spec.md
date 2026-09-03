## Purpose

Gives Magentic one provider-neutral vocabulary for what a coding agent did — prompts, messages, reasoning, commands, file changes, tool calls and errors — so every interface reads agent activity the same way regardless of which vendor produced it.

## Requirements

### Requirement: Item is the unit of agent activity

Magentic SHALL represent agent activity as **Items**: normalized, provider-neutral records of one thing that happened during a coding-agent run. An Item SHALL carry a stable identity within its Conversation, the time it occurred, the role that produced it, and exactly one kind drawn from a closed set.

The closed set of Item kinds SHALL be: developer prompt, agent message, reasoning, plan, command execution, file change, file read, tool call, web search, delegated task, context compaction, and error. A vendor record that matches no kind SHALL be normalized as an explicitly unknown kind carrying the vendor's own label, and MUST NOT be silently dropped or forced into a neighbouring kind.

An Item SHALL be derived from vendor records only. Magentic MUST NOT invent an Item for activity a vendor did not record.

#### Scenario: A tool call becomes an Item of the matching kind

- **WHEN** a vendor record describes the agent running a shell command
- **THEN** the normalized Item has the command-execution kind
- **AND** it carries the command and, when the vendor recorded one, its result

#### Scenario: An unrecognized vendor record keeps its label

- **WHEN** a vendor record describes an activity Magentic has no kind for
- **THEN** the normalized Item has the unknown kind
- **AND** it carries the vendor's own label for that activity
- **AND** it appears in the Conversation rather than being discarded

#### Scenario: Reasoning is distinguishable from prose

- **WHEN** a vendor records the agent's internal reasoning separately from its reply to the developer
- **THEN** the two are normalized as Items of different kinds

### Requirement: Presentation facts are decided during normalization

Every Item SHALL carry a short title and, where the vendor recorded enough, a detail. These facts SHALL be produced by the vendor's normalizer, not derived by an interface from a tool name or a raw payload.

A title SHALL be a single line suitable for a collapsed row. A detail SHALL carry the material an interface expands to, and MAY be absent when the vendor recorded nothing beyond the title.

An interface MAY choose how to render an Item, but MUST NOT reinterpret its kind or reconstruct its title from vendor-specific fields.

#### Scenario: Two vendors produce comparable titles for the same activity

- **WHEN** two different vendors each record the agent editing a file
- **THEN** both normalize to the file-change kind
- **AND** each Item carries a title naming the changed file, without the interface knowing which vendor produced it

#### Scenario: An Item without a detail is still renderable

- **WHEN** a vendor recorded only enough for a title
- **THEN** the Item carries that title and no detail
- **AND** an interface renders the row without an expandable body

### Requirement: Conversations are ordered and append-only

Magentic SHALL represent a coding-agent run's normalized activity as a **Conversation**: the ordered sequence of its Items. A Conversation SHALL be addressed by a **ConversationRef**, a vendor-qualified handle resolved from a Session's recorded run reference.

Normalizing the same vendor records twice SHALL produce the same Items with the same identities, so that a Conversation read again is not a Conversation that grew.

Items already present in a Conversation MUST NOT change or disappear as the run continues. A vendor that revises a record it already wrote SHALL be normalized as a new Item that supersedes the earlier one, leaving the earlier Item in place.

#### Scenario: Re-reading a Conversation does not duplicate Items

- **WHEN** the same vendor records are normalized twice
- **THEN** both readings produce Items with identical identities
- **AND** a Conversation assembled from both holds each Item once

#### Scenario: Order follows the vendor's record order

- **WHEN** a Conversation is normalized
- **THEN** its Items appear in the order the vendor recorded them
- **AND** a record without a usable timestamp keeps its position rather than being sorted to the front or the end

### Requirement: Delegated work is attributed to its parent

An Item produced by a subagent SHALL be marked as delegated and SHALL carry the identity of the delegated-task Item that spawned it, when the vendor recorded that link.

A delegated Item whose parent cannot be determined SHALL be marked delegated with its parent explicitly unknown, and MUST NOT be presented as primary activity.

#### Scenario: Subagent activity carries its parent task

- **WHEN** a vendor records a tool call made inside a delegated task and names the task it belongs to
- **THEN** the normalized Item is marked delegated and names that task

#### Scenario: Delegated work without a recorded parent

- **WHEN** a vendor records delegated activity without naming its parent task
- **THEN** the Item is marked delegated with an unknown parent
- **AND** it is not mixed into the run's primary Items

### Requirement: Every vendor states whether it can be normalized

Each supported agent vendor SHALL declare explicitly whether Magentic can normalize its Conversations. A vendor without a normalizer SHALL declare that fact, and asking it for a Conversation SHALL yield an explicit "not supported" answer naming the vendor.

A vendor without a normalizer MUST NOT return an empty Conversation, because an empty Conversation is indistinguishable from a run in which nothing has happened yet.

#### Scenario: An unsupported vendor answers explicitly

- **WHEN** a Conversation is requested for a Session hosting a vendor that has no normalizer
- **THEN** the answer states that this vendor cannot be normalized
- **AND** it names the vendor
- **AND** it is distinguishable from an empty Conversation

#### Scenario: Every supported vendor has a declared answer

- **WHEN** the set of supported vendors is enumerated
- **THEN** each one declares either a normalizer or the explicit absence of one
