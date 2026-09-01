## Purpose

Agent kinds are the coding agents Magentic can recognize in a pane; this
capability fixes which kinds ship, what first-class support obliges Magentic to
prove, and how a kind whose screens were never observed must behave.

## ADDED Requirements

### Requirement: An agent kind is recognized from the pane command

Magentic SHALL identify a Session's agent kind from the command its runtime
reports, matched against the pane-command patterns the kinds declare. A pane
command that matches no kind SHALL leave the agent kind undetermined, and the
Session's status SHALL be `unknown` rather than borrowing another kind's rules.

#### Scenario: Unrecognized pane command

- **WHEN** a Session's pane command matches no agent kind
- **THEN** the agent kind SHALL be undetermined and the status SHALL be
  `unknown`

#### Scenario: Vendor changes its process title

- **WHEN** a vendor is known to report a process title that differs from its
  binary name
- **THEN** its manifest SHALL be able to declare that form as a pane-command
  pattern, so recognition survives the change without a code change

### Requirement: Claude Code and Codex are first-class kinds

Magentic SHALL ship first-class manifests for Claude Code and for Codex. A
first-class kind SHALL cover `working`, `blocked`, `done`, `idle`, composer
readiness, and at least the blocked detail labels its screens support, and each
of its rules SHALL be backed by a recorded screen from an observed build of that
agent rather than by an assumed marker. The shipped manifest SHALL record which
agent version its rules were observed from.

#### Scenario: Codex reaches the same fidelity as Claude Code

- **WHEN** a Codex Session is working, blocked, done, or idle
- **THEN** its resolved status SHALL be the corresponding semantic status, with
  no state that only Claude Code can produce

#### Scenario: A shipped rule without a recorded screen

- **WHEN** a first-class manifest declares a rule for which no recorded screen
  exists
- **THEN** that manifest SHALL be treated as incomplete and the rule SHALL NOT
  be shipped

### Requirement: Claude Code's richer working detail is preserved

Claude Code's manifest SHALL preserve the working-state detail Magentic reports
today: the count of background agents it is waiting on and the count of
background shells it is running, expressed as detail on the `working` status.
No other kind SHALL be required to produce these details.

#### Scenario: Claude Code waits on background agents

- **WHEN** a Claude Code Session's screen shows it waiting on background agents
- **THEN** the status SHALL be `working` and the detail SHALL state how many
  agents it is waiting on

#### Scenario: A kind that reports no background work

- **WHEN** an agent kind's manifest declares no background-work detail rules
- **THEN** its `working` status SHALL carry no such detail, and this SHALL NOT
  be treated as a detection failure

### Requirement: Unobserved kinds stay honest

A shipped kind whose screens were never recorded — Gemini CLI today — SHALL
declare that its screens are unrecorded. Such a kind SHALL still be startable
and manageable, its status SHALL be `unknown` until rules are recorded for it,
and it SHALL never be reported as `idle` or `done`.

#### Scenario: Session of an unobserved kind

- **WHEN** a Session runs an agent kind marked as having unrecorded screens
- **THEN** its status SHALL be `unknown` and no automated input SHALL be
  delivered to it

#### Scenario: Someone records the missing screens

- **WHEN** rules are added for a previously unrecorded kind through a shipped or
  a user manifest
- **THEN** that kind SHALL be detected under those rules with no further change
  to Magentic

### Requirement: Status support is separable from launch support

An agent kind's status rules SHALL be usable for any Session whose pane command
matches it, including a Session Magentic did not start. Adding status rules for
a kind SHALL NOT imply that Magentic can start, resume, or address runs of that
kind.

#### Scenario: Externally started agent in a managed runtime

- **WHEN** an agent Magentic cannot launch is running in a runtime Magentic can
  observe, and its pane command matches a manifest
- **THEN** its semantic status SHALL be resolved from that manifest

#### Scenario: Manifest for a kind without launch support

- **WHEN** a user manifest introduces a kind for which Magentic has no launch
  support
- **THEN** status detection SHALL work for it and the kind SHALL NOT be offered
  as a startable agent
