## Purpose

Draws the line between the per-vendor machinery that is removed because omp replaces it and the per-vendor machinery that must be kept because records already written to disk still have to be readable.

## ADDED Requirements

### Requirement: The per-vendor launch and drive path is removed

Magentic SHALL NOT **start** a coding agent other than omp. The per-vendor knowledge that existed to launch a vendor binary, recognize its terminal, infer its status from rendered output, install its hooks, or type into its prompt SHALL be removed rather than left dormant.

A Session already running under an earlier runtime is the stated exception, because `omp-runtime/runtime-adoption` requires such a Session to keep running until it ends. Magentic SHALL retain exactly enough of the earlier runtime to observe, dispatch to and end a Session that is already running under it, and no more: nothing retained for that purpose SHALL be reachable from the path that creates a Session.

Removing this machinery SHALL remove its configuration surface with it. A setting, flag or picker whose only purpose was to choose among agent vendors SHALL NOT remain as an option that no longer does anything.

Recognition of terminal Sessions SHALL survive this removal. The pane-recognition rules are shared between coding agents and terminals, so retiring coding-agent status detection MUST NOT take terminal recognition with it.

#### Scenario: No vendor binary other than omp is started

- **WHEN** Magentic starts a coding-agent Session
- **THEN** omp is the only coding agent started
- **AND** no other vendor binary is looked up, launched or required to be installed

#### Scenario: Dead settings are removed, not left inert

- **WHEN** the settings Magentic exposes are enumerated after this change
- **THEN** no setting offers a choice among agent vendors
- **AND** no setting is present that silently has no effect

#### Scenario: Terminal recognition still works

- **WHEN** a terminal Session is observed
- **THEN** it is still recognized as a terminal
- **AND** its status is unaffected by the removal of coding-agent status detection

### Requirement: Records already written stay readable

Magentic SHALL continue to read agent records that were written before this change. Retiring the ability to **launch** a vendor MUST NOT retire the ability to **read** what that vendor already wrote.

Statistics, usage history and past Conversations that were derived from those records SHALL continue to be derivable from them. A developer MUST NOT lose their history as a consequence of the runtime changing.

These readers SHALL be declared as legacy readers for records at rest. They SHALL NOT be used to observe, drive or status a live Session, and no new record SHALL be written in a legacy vendor's format.

#### Scenario: A past Conversation is still readable

- **WHEN** a developer opens the Conversation of a Session that ran before this change under a retired vendor that has a normalizer
- **THEN** its Items are read from the records that vendor wrote
- **AND** the Conversation is presented the same way as any other

#### Scenario: A retired vendor that never had a normalizer still answers explicitly

- **WHEN** a developer opens the Conversation of a Session that ran under a retired vendor with no normalizer
- **THEN** the answer states that this vendor cannot be normalized, naming it
- **AND** it is distinguishable from an empty Conversation
- **AND** retirement did not turn a previously explicit answer into an empty one

#### Scenario: History and statistics survive the change

- **WHEN** usage statistics are computed after this change
- **THEN** activity recorded before the change is still included
- **AND** the totals do not silently reset

#### Scenario: A legacy reader is never used on a live Session

- **WHEN** a live omp Session is observed or normalized
- **THEN** no legacy vendor reader is consulted
- **AND** its status and Conversation come from the protocol

### Requirement: A pre-omp Session is imported or declared un-importable

When a developer resumes a Session that ran under a retired vendor, Magentic SHALL offer to continue it as an omp Session and SHALL state what happens to the existing conversation.

Where omp can import that vendor's session, importing it SHALL be offered, and the resumed Session SHALL carry the imported conversation. Where omp cannot import it, Magentic SHALL say so by name and offer to continue without the prior context, rather than presenting an import that would start empty.

An import SHALL NOT destroy the original record. The source remains readable afterwards, so a failed or unsatisfactory import costs nothing.

#### Scenario: An importable vendor session is carried over

- **WHEN** a developer resumes a Session whose vendor omp can import
- **THEN** importing the existing conversation is offered
- **AND** accepting it produces an omp Session carrying that conversation
- **AND** the original record still exists afterwards

#### Scenario: A vendor without an importer is named

- **WHEN** a developer resumes a Session whose vendor omp cannot import
- **THEN** the absence of an import path is stated, naming the vendor
- **AND** continuing without the prior context is offered
- **AND** no empty import is presented as a successful one

### Requirement: Pricing and usage cover the providers actually used

Usage and cost reporting SHALL be derived from what the session reports for the model that served each turn, and SHALL NOT assume a single vendor's price list.

Where the cost of a turn cannot be determined — because the provider is unpriced, locally hosted, or covered by a subscription rather than metered — Magentic SHALL represent that cost as explicitly unknown. An unknown cost MUST NOT be reported as zero, and MUST NOT be silently omitted from a total that is presented as complete.

#### Scenario: Turns served by different providers are priced separately

- **WHEN** a Session's turns were served by more than one model provider
- **THEN** each turn's usage is attributed to the provider that served it
- **AND** no single provider's prices are applied to all of them

#### Scenario: An unpriced provider is not counted as free

- **WHEN** a turn is served by a provider whose cost Magentic cannot determine
- **THEN** that turn's cost is reported as unknown
- **AND** it is not counted as zero
- **AND** a total that excludes it says that it is incomplete
