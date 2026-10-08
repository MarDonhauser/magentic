## Purpose

Makes the move to one agent visible in every surface a developer touches, by replacing the vendor as the thing Magentic shows with the model that actually served the work, and by removing or replacing every affordance that assumes a terminal, a vendor choice, or one vendor's prices.

## ADDED Requirements

### Requirement: The model is a Session's visible identity, not the vendor

Every surface that identifies a coding-agent Session SHALL identify it by the model serving it and that model's provider. A surface MUST NOT present the agent vendor as a Session's identity, because under omp every coding-agent Session has the same vendor and the vendor therefore distinguishes nothing.

The visible identity SHALL follow the Session: where the model changes during a Session, the surfaces showing it SHALL show the model in force, not the model chosen at creation.

A Session whose serving model is not yet known SHALL be shown as having an unknown model, and MUST NOT be shown with a default, a guessed, or a previously seen model.

#### Scenario: A Session is identified by its model

- **WHEN** a coding-agent Session appears in a list, a detail surface, or a hover summary
- **THEN** it is identified by the model serving it and that model's provider
- **AND** the agent vendor is not presented as its identity

#### Scenario: A model change is reflected where the Session is shown

- **WHEN** a developer changes a running Session's model
- **THEN** every surface identifying that Session shows the new model
- **AND** no surface keeps showing the model chosen at creation

#### Scenario: An unknown model is shown as unknown

- **WHEN** a Session's serving model has not been reported
- **THEN** the surfaces identifying it show the model as unknown
- **AND** none of them substitutes a default or a previously seen model

### Requirement: No surface offers a choice among agent vendors

Magentic SHALL remove every affordance that asks a developer to pick, switch, or default to an agent vendor. This includes choosing a vendor when creating a Session, a preference for a default vendor, switching a running Session to another vendor, and handing a Session's context to a different vendor.

Where such an affordance chose something a developer still needs to choose, it SHALL be replaced by the equivalent choice over models rather than removed silently.

A surface MUST NOT retain a disabled, hidden, or inert vendor control as a placeholder.

#### Scenario: Session creation asks for a model

- **WHEN** a developer creates a coding-agent Session
- **THEN** the choice offered is among models
- **AND** no agent vendor is offered

#### Scenario: A vendor preference is replaced rather than left behind

- **WHEN** the settings surface is shown after this change
- **THEN** no preference selects a default agent vendor
- **AND** a preference selecting a default model is offered in its place

#### Scenario: A running Session cannot be switched to another vendor

- **WHEN** a developer opens the actions available for a running coding-agent Session
- **THEN** no action offers switching it to another agent vendor
- **AND** no inert or disabled vendor control remains in the surface

### Requirement: Terminal-shaped affordances are replaced, not silently dropped

A coding-agent Session under omp has no terminal pane of its own, so every surface that offered one SHALL state what is offered instead rather than hiding the control.

Where a surface previously showed the Session's terminal content — a pane preview, a terminal tab, a link into the terminal — it SHALL either show the Session's Conversation or offer the route into omp's own interface, and SHALL say which.

Copy that describes an action in terms of a terminal or a terminal multiplexer SHALL be rewritten to describe what actually happens. A confirmation that names a mechanism the Session does not use MUST NOT be shown.

#### Scenario: A surface that offered a terminal offers the replacement

- **WHEN** a surface that previously offered a coding-agent Session's terminal is shown for an omp Session
- **THEN** it offers the Conversation or the route into omp's own interface
- **AND** it states which is being offered
- **AND** the control is not merely absent

#### Scenario: Ending a Session is described by what it does

- **WHEN** a developer is asked to confirm ending a coding-agent Session
- **THEN** the confirmation describes ending the Session
- **AND** it does not name a terminal multiplexer session as the thing being killed

#### Scenario: A pane preview is replaced by Conversation content

- **WHEN** a detail surface that previously showed captured terminal content is shown for an omp Session
- **THEN** it shows that Session's recent Conversation activity instead
- **AND** it does not present an empty or stale terminal preview

### Requirement: An approval request is presented from the request, not from a screen

Every surface that presents a pending approval — a notification, a badge, an inbox entry, an overlay, or an in-Conversation notice — SHALL present what omp actually asked, taken from the approval request.

A surface MUST NOT derive the content of an approval prompt from captured terminal output, and MUST NOT tell the developer that the question is waiting in a terminal.

Where a surface offers to answer an approval, answering it there SHALL be equivalent to answering it anywhere else, and a request already answered or no longer answerable SHALL be presented as such rather than as actionable.

#### Scenario: An approval overlay shows the request's own content

- **WHEN** an approval request is presented to the developer
- **THEN** its content comes from the request omp raised
- **AND** no part of it is taken from captured terminal output

#### Scenario: The developer is not sent to a terminal to answer

- **WHEN** a surface tells the developer that a Session needs a decision
- **THEN** it does not state that the question is open in a terminal
- **AND** it offers answering the request or opening the Session

#### Scenario: An answered request stops being actionable

- **WHEN** a request is answered and another surface still displays it
- **THEN** that surface shows it as answered
- **AND** it offers no control claiming to grant or deny it

### Requirement: Usage and cost surfaces name the provider they describe

A surface reporting quota, limits, token usage or cost SHALL name the provider it is describing, and SHALL NOT present one provider's figures as if they covered the developer's work as a whole.

Where a figure cannot be determined for a provider, the surface SHALL show it as unknown. It MUST NOT show an undeterminable cost as zero, and a total that omits unknown figures SHALL be labelled as incomplete.

A surface that groups work by model SHALL derive the model's name and family from what was reported for that work, not from a naming pattern belonging to one vendor.

#### Scenario: A limits surface names its provider

- **WHEN** quota or limit information is shown
- **THEN** the provider it describes is named in the surface
- **AND** it is not presented as covering every provider the developer uses

#### Scenario: An undeterminable cost is shown as unknown

- **WHEN** work was served by a provider whose cost cannot be determined
- **THEN** the surface shows that cost as unknown
- **AND** it is not shown as zero
- **AND** a total that excludes it is labelled incomplete

#### Scenario: Models are grouped by what was reported

- **WHEN** work is grouped by model in a statistics surface
- **THEN** each group is named from the model reported for that work
- **AND** no grouping depends on a naming pattern specific to one vendor

### Requirement: The state of the approval gate is visible where the Session is

A developer SHALL be able to see whether a Session's tools are gated by Magentic. The surfaces that identify a Session SHALL show when its gate is proven, and SHALL show when the proof has been withdrawn.

Where the proof has been withdrawn because omp's own interface was opened for the Session, the surface SHALL state that as the reason. A surface MUST NOT present a Session as gated by Magentic once the proof no longer holds.

Before a route that would withdraw the proof is opened, the developer SHALL be told that opening it does so.

#### Scenario: A withdrawn proof is visible on the Session

- **WHEN** a Session's gate proof has been withdrawn
- **THEN** the surfaces identifying that Session show it as no longer gated by Magentic
- **AND** the reason names the route that withdrew it

#### Scenario: The developer is warned before withdrawing the proof

- **WHEN** a developer is about to open omp's own interface for a Session
- **THEN** they are told that opening it withdraws Magentic's gating claim
- **AND** they can decline

### Requirement: A refusal is shown with the reason that caused it

Where this change introduces a refusal — omp absent or unusable, a handshake Magentic does not understand, an unverified approval gate, an unprovable gate, a model the session does not offer, or a login that must happen in omp's own interface — the surface SHALL state the specific reason.

A refusal MUST NOT be presented as a generic failure, and a Session that was refused MUST NOT be presented as merely idle, unknown, or dead.

Where the developer can resolve the cause, the surface SHALL say what would resolve it.

#### Scenario: A missing omp is named as the cause

- **WHEN** a Session cannot be created because omp is absent or unusable
- **THEN** the surface names omp as the cause
- **AND** it says what would resolve it
- **AND** the Session is not shown as idle or dead

#### Scenario: An unverified gate is distinguished from a failed one

- **WHEN** Sessions are refused because the approval gate could not be verified
- **THEN** the surface distinguishes a verification that failed from one that could not be attempted
- **AND** a verification blocked by a missing login names the login

### Requirement: Both interfaces reflect the change

Every requirement in this capability SHALL hold for the terminal interface and for the desktop application alike. A surface that exists in only one of them SHALL satisfy the requirements that apply to it.

Where one interface cannot offer an affordance the other offers, it SHALL state that rather than presenting an incomplete or misleading version of it.

Shipped copy, labels, placeholder text, sample content and iconography MUST NOT assert a vendor, a terminal multiplexer, or a product that a Session no longer uses.

#### Scenario: The terminal interface identifies Sessions by model

- **WHEN** a coding-agent Session is shown in the terminal interface
- **THEN** it is identified by its serving model
- **AND** its creation flow offers a model rather than a named vendor

#### Scenario: No shipped copy asserts a retired vendor

- **WHEN** the shipped interface copy, placeholders and sample content are reviewed
- **THEN** none of it states that Magentic reads one named vendor's transcripts
- **AND** none of it presents a retired vendor's interface as Magentic's own
