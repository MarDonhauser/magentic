## Purpose

Lets a coding agent running inside a Magentic-managed Session discover that it
can drive Magentic, and gives it a shipped instruction file that teaches the
verbs and the delegation pattern without the developer writing that prompt.

## ADDED Requirements

### Requirement: Environment marker in managed Session runtimes

Every Session runtime Magentic provisions SHALL export an environment marker
identifying it as Magentic-managed, together with the facts an occupant needs to
address the control API: the marker flag, the control socket path, the
occupant's own SessionID, its ProjectID, and whether it runs in a Worktree. The
marker SHALL be present in coding-agent Sessions and terminal Sessions alike, and
SHALL be absent from any process Magentic did not provision.

#### Scenario: Marker present in a provisioned Session

- **WHEN** a Session is provisioned by Magentic
- **THEN** its runtime environment carries the marker flag, the control socket
  path, its own SessionID, and its ProjectID

#### Scenario: Worktree fact is exported

- **WHEN** a Session is provisioned in a Worktree
- **THEN** its environment reports that it runs in a Worktree and identifies it

#### Scenario: Unmanaged shell has no marker

- **WHEN** a shell is started outside Magentic
- **THEN** none of the marker variables are set, so an agent there does not
  assume it may drive Magentic

#### Scenario: Adopted runtime

- **WHEN** an externally created runtime is adopted as a Session
- **THEN** its already-running processes are not retroactively given the marker,
  and the adopted Session is not reported as carrying it

### Requirement: Self-identification through the control API

The control API SHALL answer a request that identifies the caller's own Session
from the marker facts, so an agent can learn its Project, Worktree, and
SessionID without parsing state files. A caller whose marker facts do not resolve
to a registered Session SHALL receive a distinct not-managed outcome.

#### Scenario: Occupant identifies itself

- **WHEN** an agent inside a managed Session asks the control API who it is
- **THEN** the response names its SessionID, ProjectID, Worktree, and vendor

#### Scenario: Caller is not a managed Session

- **WHEN** the caller presents no marker facts, or facts that resolve to no
  registered Session
- **THEN** the response is a distinct not-managed outcome, and the request is
  not answered with another Session's identity

### Requirement: Shipped agent instruction file

Magentic SHALL ship an agent instruction file that documents the control verbs,
the addressing rules, the pinned-occupant wait contract with its outcome codes,
and the delegation pattern of spawning a Session in a Worktree, waiting for it,
and reading its output. The file SHALL be written for a coding agent to follow
directly, SHALL be usable by both Claude Code and Codex, and SHALL state that it
applies only when the marker is present.

#### Scenario: Instruction file ships with the repository

- **WHEN** Magentic is installed from the repository
- **THEN** the agent instruction file is present and names every control verb
  and every wait outcome code

#### Scenario: Delegation pattern is documented

- **WHEN** an agent follows the instruction file
- **THEN** it can start a Session in a fresh Worktree, wait for its pinned
  occupant, and read its output using only what the file states

#### Scenario: Guarded by the marker

- **WHEN** the instruction file is read in a context where the marker is absent
- **THEN** the file directs the agent not to attempt control API calls

#### Scenario: Installing it into a Project

- **WHEN** the developer installs the instruction file into a Project
- **THEN** it is placed where that Project's agents load instructions from, and
  installing it again does not duplicate its content

### Requirement: Control API misuse is reported, not guessed

An agent-issued request that would act on a Session outside the caller's
Project, or that addresses a Session the caller cannot resolve, SHALL be
answered with an explicit outcome. The control API MUST NOT widen a request by
guessing which Session or Project the caller meant.

#### Scenario: Request outside the caller's Project

- **WHEN** an agent addresses a Session in another Project without naming that
  Project explicitly
- **THEN** the request is refused with an addressing outcome rather than being
  resolved to the caller's own Project

#### Scenario: No implicit target

- **WHEN** a verb requiring a Session is issued with no Session addressed
- **THEN** the request is refused, and the caller's own Session is not
  substituted as the target
