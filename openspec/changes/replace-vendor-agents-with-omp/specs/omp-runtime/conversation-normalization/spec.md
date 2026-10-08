## Purpose

Normalizes omp's protocol event stream into the Items and Conversations `agent-timeline/item-model` already defines, so an omp-hosted run reads through the same provider-neutral vocabulary as every other vendor.

## ADDED Requirements

### Requirement: omp's events are normalized into the one existing Item and Conversation model

Magentic SHALL normalize omp's lifecycle and message events into Items and Conversations as `agent-timeline/item-model` defines them. This capability MUST NOT define a second Item, ItemKind, or Conversation model: omp is one more source normalized into the model that already exists, satisfying ADR 0005's requirement that local agent history is normalized once.

An Item produced from an omp event SHALL carry the same identity, ordering, title and detail guarantees `agent-timeline/item-model` requires of any vendor's Items.

#### Scenario: omp activity produces Items in the existing model

- **WHEN** an omp Session produces lifecycle and message events
- **THEN** they are normalized into Items conforming to the published Item model
- **AND** no separate normalization model is introduced for omp

### Requirement: omp events map onto the existing closed set of Item kinds

Magentic SHALL map omp's message, tool-execution, and related events onto the closed set of Item kinds `agent-timeline/item-model` defines: developer prompt, agent message, reasoning, plan, command execution, file change, file read, tool call, web search, delegated task, context compaction, and error.

An omp event that matches no kind SHALL be normalized as the explicitly unknown kind, carrying omp's own label for that event. It MUST NOT be dropped and MUST NOT be forced into a neighbouring kind.

#### Scenario: A tool execution maps to an existing kind

- **WHEN** omp reports a `tool_execution_start`/`tool_execution_end` pair for a shell command
- **THEN** the normalized Item has the command-execution kind

#### Scenario: An unrecognized omp event keeps its own label

- **WHEN** omp reports an event Magentic has no matching kind for
- **THEN** the normalized Item has the unknown kind and carries omp's own label
- **AND** it appears in the Conversation rather than being dropped or reclassified

### Requirement: Streaming deltas converge on stable Items

`message_update` events carry incremental deltas rather than complete messages. Magentic SHALL accumulate these deltas into a single Item per message, and that Item's identity SHALL remain stable from the first delta through `message_end`.

Re-reading a Conversation assembled from omp's stream SHALL produce the same Items with the same identities as the first reading, consistent with `agent-timeline/item-model`'s requirement that a Conversation read again does not grow or duplicate. An Item already published while its message was still streaming MUST NOT be duplicated by a later delta for the same message; later deltas update that Item in place until it is finalized at `message_end`.

#### Scenario: Deltas accumulate into one Item

- **WHEN** omp reports `message_start`, one or more `message_update` deltas, and `message_end` for the same message
- **THEN** they normalize into a single Item whose identity is stable across all of those events

#### Scenario: Re-reading a streamed Conversation does not duplicate Items

- **WHEN** a Conversation built from omp's event stream is normalized again after the stream has finished
- **THEN** the resulting Items have identities identical to the first normalization
- **AND** no Item is duplicated

### Requirement: The serving model provider is a fact carried by activity, not a second vendor identity

The model provider that served a turn — for example `anthropic`, `openai`, or `ollama` — SHALL be recorded as a fact carried alongside the Item or turn it served, not as a second agent vendor identity. The agent vendor for every omp-hosted Item SHALL remain omp.

A Conversation MAY contain turns served by different model providers when the model changed mid-session, and the normalized Conversation SHALL represent that difference without splitting the Conversation or changing its vendor.

#### Scenario: A model change mid-session is represented within one Conversation

- **WHEN** the model serving an omp Session changes partway through, and turns before and after the change are recorded
- **THEN** both turns' Items remain in the same Conversation
- **AND** each Item carries the model provider that served it
- **AND** the Conversation's vendor remains omp throughout

#### Scenario: The model provider is not mistaken for the vendor

- **WHEN** an Item's model provider is read
- **THEN** it is read as a served-by fact
- **AND** it is not presented as the agent vendor

### Requirement: omp's own session files are never read

Magentic MUST NOT read the JSONL session records omp writes under its own agent directory. That format is documented as internal to omp, and its bucket-naming scheme has already changed between omp releases without notice; a Conversation built by reading it would be built on an unstable, undocumented contract.

The Conversation for an omp Session SHALL be derived exclusively from the protocol event stream Magentic receives over `omp --mode rpc-ui`.

#### Scenario: Normalization does not touch omp's session files

- **WHEN** a Conversation is normalized for an omp Session
- **THEN** its Items are derived from the protocol event stream
- **AND** omp's own on-disk session records are not read

### Requirement: Subagent activity is attributed to its parent

Activity arriving on omp's subagent event frames (`subagent_lifecycle`, `subagent_progress`, `subagent_event`) SHALL be normalized as delegated Items and attributed to the delegated-task Item that spawned them, consistent with `agent-timeline/item-model`'s delegated-work requirement.

Subagent activity whose parent task cannot be determined from the protocol stream SHALL be marked delegated with its parent explicitly unknown, and MUST NOT be presented as the Session's primary activity.

#### Scenario: Subagent events are attributed to the delegating task

- **WHEN** omp reports subagent event frames naming the delegated task that spawned them
- **THEN** the normalized Items are marked delegated and name that task

#### Scenario: An unattributable subagent event is marked unknown, not primary

- **WHEN** omp reports a subagent event frame without a determinable parent task
- **THEN** the normalized Item is marked delegated with an unknown parent
- **AND** it is not mixed into the Session's primary Items

### Requirement: A Conversation survives interfaces closing and the daemon restarting

An Item already normalized from omp's event stream SHALL remain readable after every interface presenting its Session has closed and reopened, and after the daemon that observed it has restarted.

Magentic MUST NOT rely on omp's live process or an open protocol connection to re-derive an Item it has already normalized: the durability of an already-observed Item SHALL NOT depend on the omp process that produced it still running.

#### Scenario: A Conversation is unchanged after an interface reopens

- **WHEN** an interface presenting a Session closes and later reopens
- **THEN** the Items normalized before it closed are still readable
- **AND** they are unchanged from what was observed before

#### Scenario: A Conversation survives a daemon restart

- **WHEN** the daemon restarts after normalizing part of a Session's Conversation
- **THEN** the Items normalized before the restart remain readable afterwards
- **AND** their readability does not depend on the omp process that produced them still running
