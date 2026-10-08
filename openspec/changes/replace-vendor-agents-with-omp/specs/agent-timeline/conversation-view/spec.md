## MODIFIED Requirements

### Requirement: An unavailable Conversation says so in the surface

When a Conversation is unavailable, the surface SHALL state which reading applies — not applicable for this Session, no normalizer for this vendor, record not found, or record unreadable — and SHALL name the reason.

The surface MUST NOT render an unavailable Conversation as an empty one, and MUST NOT imply that the agent has done nothing.

When a Conversation is unavailable for a Session that has a terminal, the surface SHALL keep that terminal reachable, since the terminal remains the only complete reading available for that Session in that case.

When a Conversation is unavailable for a Session that has no terminal of its own, the surface SHALL keep the way to that Session's own agent interface reachable instead, and SHALL state that opening it withdraws Magentic's claim that the Session's tools are gated.

#### Scenario: An unsupported vendor is named in the surface

- **WHEN** the selected Session has a terminal and hosts a vendor with no normalizer
- **THEN** the surface states that this vendor's Conversations cannot be read yet
- **AND** it names the vendor
- **AND** the terminal stays reachable

#### Scenario: An unsupported vendor is named in the surface (Session without a terminal)

- **WHEN** the selected Session has no terminal of its own and hosts a vendor with no normalizer
- **THEN** the surface states that this vendor's Conversations cannot be read yet
- **AND** it names the vendor
- **AND** the way to that Session's own agent interface stays reachable
- **AND** the surface states that opening it withdraws Magentic's claim that the Session's tools are gated

#### Scenario: An empty Conversation reads differently from a missing one

- **WHEN** the selected Session's Conversation is available and holds no Items
- **THEN** the surface states that the run has produced nothing yet
- **AND** this differs from the wording shown when the record is missing

### Requirement: The terminal remains the place where a Session is answered

The Conversation surface SHALL be a reading surface. It MUST NOT offer to answer a vendor's permission prompt, because such a prompt is not part of a Conversation and cannot be answered from it.

When the agent is waiting for the developer, the surface SHALL say so. For a Session that has a terminal, the surface SHALL offer the way to that terminal. For a Session that has no terminal of its own, the surface SHALL offer the way to that Session's own agent interface instead, and SHALL state that opening it withdraws Magentic's claim that the Session's tools are gated.

#### Scenario: A waiting agent points at its terminal

- **WHEN** the selected Session has a terminal and is observed to be waiting for the developer
- **THEN** the Conversation surface states that it is waiting
- **AND** it offers the way to that Session's terminal

#### Scenario: A waiting agent without a terminal points at its own interface

- **WHEN** the selected Session has no terminal of its own and is observed to be waiting for the developer
- **THEN** the Conversation surface states that it is waiting
- **AND** it offers the way to that Session's own agent interface
- **AND** it states that opening it withdraws Magentic's claim that the Session's tools are gated

#### Scenario: No approval controls are offered

- **WHEN** the Conversation surface is presented
- **THEN** it offers no control that claims to grant or deny a vendor permission prompt
