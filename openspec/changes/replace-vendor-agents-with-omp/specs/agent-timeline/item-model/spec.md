## MODIFIED Requirements

### Requirement: Every vendor states whether it can be normalized

A **vendor** is the agent that recorded the activity — the thing that owns the record and the run identity — and not the model provider that served the turn. One vendor MAY route to many model providers; that routing is a fact about a turn, not a second vendor.

Each supported agent vendor SHALL declare explicitly whether Magentic can normalize its Conversations. A vendor without a normalizer SHALL declare that fact, and asking it for a Conversation SHALL yield an explicit "not supported" answer naming the vendor.

A vendor without a normalizer MUST NOT return an empty Conversation, because an empty Conversation is indistinguishable from a run in which nothing has happened yet.

Retiring a vendor from the launch path MUST NOT change its declaration in either direction. A retired vendor that declared a normalizer SHALL keep declaring it, because the records it already wrote remain addressable; a retired vendor that declared no normalizer SHALL keep declaring that absence explicitly, rather than falling out of the enumerated set and leaving its records unaddressable.

#### Scenario: An unsupported vendor answers explicitly

- **WHEN** a Conversation is requested for a Session hosting a vendor that has no normalizer
- **THEN** the answer states that this vendor cannot be normalized
- **AND** it names the vendor
- **AND** it is distinguishable from an empty Conversation

#### Scenario: Every supported vendor has a declared answer

- **WHEN** the set of supported vendors is enumerated
- **THEN** each one declares either a normalizer or the explicit absence of one

#### Scenario: A retired vendor keeps whatever it declared

- **WHEN** the set of vendors is enumerated after a vendor has been retired from the launch path
- **THEN** that vendor still appears in the set
- **AND** a vendor that declared a normalizer still declares it, and a Conversation from a record it wrote earlier is still readable
- **AND** a vendor that declared no normalizer still declares that absence explicitly

#### Scenario: Routing to many providers does not create many vendors

- **WHEN** one vendor serves turns through several different model providers
- **THEN** the Conversation has exactly one vendor
- **AND** the model providers are not enumerated as vendors

## ADDED Requirements

### Requirement: The serving model provider is a recorded fact

An Item MAY carry the model provider and model that served the activity it describes. Where the vendor recorded which model produced a turn, normalization SHALL preserve it.

This fact SHALL be carried as an attribute of the activity. It MUST NOT be used as an identity: it MUST NOT qualify a run reference, address a Conversation, or select a normalizer.

A Conversation whose model changed partway through SHALL represent that faithfully, carrying the model that actually served each Item rather than one model for the whole Conversation.

#### Scenario: A turn records the model that served it

- **WHEN** a vendor recorded which model and provider produced an agent message
- **THEN** the normalized Item carries that model and provider

#### Scenario: A Conversation spans more than one model

- **WHEN** a developer changes the model partway through a run and work continues
- **THEN** Items recorded before the change carry the earlier model
- **AND** Items recorded after it carry the later model
- **AND** the Conversation is still addressed by a single reference

#### Scenario: The provider does not become an identity

- **WHEN** a Conversation is addressed and a normalizer is selected for it
- **THEN** neither depends on which model provider served any of its Items
