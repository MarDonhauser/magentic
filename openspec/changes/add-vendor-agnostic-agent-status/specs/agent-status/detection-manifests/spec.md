## Purpose

Detection manifests describe, as data rather than as compiled code, how one
coding-agent kind's terminal screen is read as a semantic Session status, so a
new agent can be supported by adding a file instead of by changing Magentic.

## ADDED Requirements

### Requirement: Manifest describes one agent kind

A detection manifest SHALL describe exactly one agent kind and SHALL declare
its stable kind identifier, a human-readable label, the pane commands by which
its runtime is recognized, and its status rules. Two manifests SHALL NOT claim
the same kind identifier; when a shipped and a user manifest share an
identifier, the resolution in "User manifests extend and override shipped
manifests" applies.

#### Scenario: Manifest declares its kind

- **WHEN** a manifest is loaded that declares a kind identifier, a label, at
  least one pane-command pattern, and at least one status rule
- **THEN** the agent kind SHALL be available for status detection under that
  identifier

#### Scenario: Two shipped manifests claim the same kind

- **WHEN** two manifests from the same source directory declare the same kind
  identifier
- **THEN** loading SHALL fail for the later one with a stated reason naming the
  conflicting identifier, and the earlier one SHALL remain in effect

### Requirement: Manifest status vocabulary

A manifest SHALL express status rules only in the semantic vocabulary
`working`, `blocked`, `done`, and `idle`. A manifest SHALL NOT be able to
declare a rule that yields `unknown`, `dead`, `exited`, or `terminal`: those
states are derived from runtime presence and pane command, never from screen
content.

#### Scenario: Manifest uses a state outside the vocabulary

- **WHEN** a manifest declares a rule whose state is not one of `working`,
  `blocked`, `done`, or `idle`
- **THEN** the manifest SHALL be rejected with a stated reason naming the
  invalid state

### Requirement: Deterministic matching order

Manifest evaluation SHALL apply rules in a fixed order defined by the format
and not by the order rules appear in the file: `working` first, then `blocked`,
then `done`, then `idle`. Within one state, rules SHALL be evaluated in file
order and the first match SHALL win.

#### Scenario: A snapshot matches both a working and a blocked rule

- **WHEN** a pane snapshot matches one of the kind's `working` patterns and one
  of its `blocked` patterns
- **THEN** the evaluated status SHALL be `working`

#### Scenario: A snapshot matches a done and an idle rule

- **WHEN** a pane snapshot matches a `done` pattern and an `idle` pattern and no
  `working` or `blocked` pattern
- **THEN** the evaluated status SHALL be `done`

### Requirement: Matching operates on a bounded snapshot

Manifest evaluation SHALL be performed against a normalized, bounded tail of
the pane snapshot: control sequences removed, line endings normalized, trailing
whitespace trimmed, and case folded for literal comparison. A manifest MAY
declare how many trailing lines it needs, up to the observation scrollback the
runtime provides; absent a declaration the default tail SHALL be used.

#### Scenario: A marker above the evaluated tail

- **WHEN** the only occurrence of a `blocked` marker lies above the manifest's
  declared tail
- **THEN** that rule SHALL NOT match, and evaluation SHALL continue with the
  remaining rules

### Requirement: Patterns are literal or regular expressions

A rule SHALL declare its patterns as either case-insensitive literal substrings
or anchored regular expressions. A regular expression that fails to compile, or
whose evaluation exceeds the evaluator's per-snapshot time budget, SHALL
invalidate the manifest that contains it rather than silently never matching.

#### Scenario: Uncompilable pattern

- **WHEN** a manifest contains a regular expression that does not compile
- **THEN** the manifest SHALL be rejected with a stated reason naming the rule
  and the pattern

### Requirement: Manifests may extract status detail

A manifest MAY declare detail rules that extract a short human-readable
qualifier for a state, including a labelled reason for `blocked` (for example a
folder, file, or shell approval) and a counted qualifier for `working` (for
example the number of background agents or background shells the agent
reports). Detail extraction SHALL never change the resolved status.

#### Scenario: Blocked with a recognized approval

- **WHEN** a snapshot resolves to `blocked` and matches a declared detail rule
- **THEN** the Observation SHALL carry that rule's label as the status detail

#### Scenario: Blocked without a recognized approval

- **WHEN** a snapshot resolves to `blocked` and matches no detail rule
- **THEN** the Observation SHALL carry an empty detail and the status SHALL
  remain `blocked`

### Requirement: Manifests declare composer readiness

A manifest SHALL be able to declare the patterns by which the agent's own input
line is visible. Prompt delivery SHALL treat a Session as ready for typed input
only when the resolved status permits it and the kind's composer-readiness
patterns match; a kind that declares no composer-readiness patterns SHALL never
be reported as ready for typed input.

#### Scenario: Kind without composer patterns

- **WHEN** a Session's agent kind declares no composer-readiness patterns and
  its status is `idle`
- **THEN** its prompt input state SHALL be unknown rather than ready

### Requirement: Manifest sources and precedence

Magentic SHALL load manifests from a shipped default set that travels with the
binary and from a user manifest directory. Both directories SHALL be readable
and editable as plain files. A user manifest SHALL be able to introduce a new
agent kind or to replace a shipped kind of the same identifier in full;
partial merging of a shipped and a user manifest SHALL NOT occur.

#### Scenario: User adds an unsupported agent kind

- **WHEN** the user places a valid manifest for a kind Magentic does not ship
  into the user manifest directory
- **THEN** Sessions whose pane command matches that manifest SHALL be detected
  under it without rebuilding Magentic

#### Scenario: User overrides a shipped kind

- **WHEN** a valid user manifest declares the same kind identifier as a shipped
  manifest
- **THEN** the user manifest SHALL be used in full and the shipped manifest for
  that identifier SHALL be ignored

### Requirement: An invalid manifest never degrades detection

An invalid or unreadable manifest SHALL be rejected with a stated, surfaceable
reason. Rejecting a user manifest SHALL leave the shipped manifest of the same
identifier in effect. A rejected manifest SHALL NOT cause the affected kind to
be reported as `idle`, `done`, or `dead`.

#### Scenario: Malformed user override

- **WHEN** a user manifest that overrides a shipped kind fails validation
- **THEN** the shipped manifest SHALL remain in effect and the validation
  failure SHALL be reported to the developer

#### Scenario: Malformed manifest for an otherwise unsupported kind

- **WHEN** the only manifest for an agent kind fails validation
- **THEN** Sessions of that kind SHALL resolve to `unknown` status, never to
  `idle` or `done`

### Requirement: Manifests are validated on demand

Magentic SHALL offer a way to validate the manifest set and report, per
manifest, whether it loaded and why it did not. Reloading manifests SHALL NOT
require restarting a running Session or losing Observation history.

#### Scenario: Developer checks the manifest set

- **WHEN** the developer asks Magentic to validate manifests
- **THEN** every shipped and user manifest SHALL be listed with its kind
  identifier, its source, and either an accepted result or a stated reason for
  rejection
