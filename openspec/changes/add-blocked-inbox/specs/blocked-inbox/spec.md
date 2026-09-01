## Purpose

The blocked inbox is the single cross-Project list of Sessions that are currently waiting on the developer, so that supervising many concurrent coding agents means reading one ordered list instead of scanning panes or chasing individual notifications.

## ADDED Requirements

### Requirement: Single cross-Project list of waiting Sessions

Magentic SHALL present one list, spanning every registered Project, of the Sessions that are currently waiting on the developer. A Session SHALL be listed when its Observation reports an Attention of `needs-input` (a permission prompt or a question) or `review` (finished work awaiting the developer). Sessions that are working, idle, or absent SHALL NOT be listed.

The list SHALL be derived from the same attention facts that already drive notifications, the Dock badge, and native attention. Magentic SHALL NOT introduce a second attention derivation or a second notification path for the inbox.

#### Scenario: Waiting Sessions from several Projects

- **WHEN** three Sessions in two different Projects report `needs-input` and one Session in a third Project reports `review`
- **THEN** the inbox lists exactly those four Sessions, each with its Project, its Session name, and its waiting kind

#### Scenario: Working Sessions are not listed

- **WHEN** a Session reports Attention `working` or `none`
- **THEN** that Session does not appear in the inbox

#### Scenario: One attention derivation

- **WHEN** one attention planning cycle runs for an Observation
- **THEN** the inbox and the notification, badge, and native-attention intents for that cycle are produced from that same cycle and describe the same set of waiting Sessions

### Requirement: Entries carry enough context to act without opening the pane

Each entry SHALL identify its Project, its Session, the waiting kind (`needs-input` or `review`), the waiting time, and the observed content excerpt that explains what the Session is asking. When Observation reports that a Session's content is not known, the entry SHALL say so instead of showing an empty or stale excerpt.

#### Scenario: Entry shows the question

- **WHEN** a Session waits at a permission prompt and its pane content is known
- **THEN** its entry shows the tail of that content as the reason it is waiting

#### Scenario: Content not known

- **WHEN** a listed Session's Observation reports content as not known
- **THEN** its entry marks the reason as unknown rather than showing empty or previously captured content

### Requirement: Entries are ordered by waiting time

The inbox SHALL order entries by how long each Session has been waiting, longest wait first. Waiting time SHALL be measured from the moment the Session entered its current waiting state as recorded by attention planning, not from the moment the inbox was rendered.

When the moment a Session entered its waiting state is not known — for example because Magentic started while the Session was already waiting — the entry SHALL report its waiting time as a lower bound and SHALL be marked as such, never as a wait that just began. Entries with a lower-bound wait SHALL sort as at least as old as the oldest known wait.

Ties SHALL be broken deterministically so that two consecutive renderings of an unchanged inbox produce the same order.

#### Scenario: Longest wait first

- **WHEN** Session A has been waiting for 12 minutes and Session B for 2 minutes
- **THEN** Session A is listed above Session B

#### Scenario: Wait already in progress at startup

- **WHEN** Magentic starts and observes a Session that is already waiting
- **THEN** that entry's waiting time is presented as a lower bound and the entry is not treated as a wait that just started

#### Scenario: Stable order

- **WHEN** the inbox is rendered twice from the same unchanged facts
- **THEN** both renderings list the entries in the same order

### Requirement: Answering a Session from its entry

The developer SHALL be able to send free text to a listed Session directly from its entry, without switching to that Session's pane or terminal tab. The text SHALL be delivered through the existing queued-message transport, so it reaches the Session when the Session is input-ready and remains durably queued when it is not.

Submitting an answer SHALL NOT by itself remove the entry from the inbox, and SHALL NOT report the Session as unblocked before delivery is observed. An entry whose answer is queued but not yet delivered SHALL be marked as awaiting delivery.

An entry SHALL also offer to open its Session, so the developer can move to the pane when the answer needs more than text.

#### Scenario: Reply reaches an input-ready Session

- **WHEN** the developer submits an answer for an entry whose Session is input-ready
- **THEN** the text is delivered to that Session and the entry is marked as awaiting the next Observation

#### Scenario: Reply to a Session that is not input-ready

- **WHEN** the developer submits an answer for an entry whose Session cannot accept input right now
- **THEN** the text stays durably queued for that Session and the entry shows that delivery is still pending

#### Scenario: Answering never falsely clears

- **WHEN** an answer has been submitted but the next Observation still reports the Session as `needs-input`
- **THEN** the entry remains in the inbox

### Requirement: Entries clear when the Session moves on

An entry SHALL be removed when the next Observation reports that its Session is no longer waiting — it resumed work, it became idle, its runtime is absent, or the developer marked it seen. Removal SHALL be derived from observed facts, never from the act of submitting an answer or from the entry being read.

#### Scenario: Session resumes work

- **WHEN** a listed Session's next Observation reports Attention `working`
- **THEN** its entry is removed from the inbox

#### Scenario: Session runtime disappears

- **WHEN** a listed Session's next Observation reports its runtime as absent
- **THEN** its entry is removed from the inbox

#### Scenario: Waiting kind changes

- **WHEN** a Session listed as `needs-input` is next observed as `review`
- **THEN** the inbox shows one entry for that Session with the new waiting kind, and its waiting time restarts from the moment of that change

### Requirement: Unavailable and partial knowledge are stated explicitly

The inbox SHALL declare whether its list is complete. When Observation is unavailable, the inbox SHALL NOT be rendered as empty and SHALL NOT drop the last known entries silently; it SHALL state that current waiting facts could not be read. When Observation is partial, the inbox SHALL state that the list is incomplete and SHALL list the Sessions whose facts are known.

A Session whose own Observation is unavailable or whose Attention is unknown SHALL NOT be listed as waiting and SHALL NOT be reported as not waiting.

#### Scenario: Observation unavailable

- **WHEN** an attention cycle runs with an unavailable Observation
- **THEN** the inbox reports that waiting facts are unavailable rather than showing an empty inbox

#### Scenario: Observation partial

- **WHEN** an attention cycle runs with a partial Observation
- **THEN** the inbox lists the known waiting Sessions and marks the list as incomplete

### Requirement: Inbox surfaces in the desktop app and the TUI

The desktop app SHALL offer the inbox as a first-class surface, reachable from anywhere in the app without losing the currently open Session, and SHALL support answering and opening a Session from an entry.

The TUI SHALL offer the same ordered list, built from the same derived inbox, with at least reading an entry and jumping to its Session. Inline answering in the TUI is not required by this capability.

Both surfaces SHALL show the same entries and the same order for the same facts.

#### Scenario: Desktop inbox while a Session is open

- **WHEN** the developer opens the inbox while a Session's terminal is open
- **THEN** the inbox is shown and the open Session remains available to return to

#### Scenario: TUI list matches the desktop list

- **WHEN** the same attention cycle feeds both surfaces
- **THEN** the TUI and the desktop app show the same entries in the same order

#### Scenario: Jump to Session from the TUI

- **WHEN** the developer selects an entry in the TUI inbox and chooses to open it
- **THEN** the TUI moves to that Session
