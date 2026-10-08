## Purpose

Guarantees that a tool omp wants to run reaches a person before it runs, by forcing an asking approval mode, proving that mode is active before the Session may work, and failing closed whenever that proof is missing.

## ADDED Requirements

### Requirement: Every omp Session is launched with an asking approval mode

Magentic SHALL start every omp Session with an approval mode that asks before running a tool, and SHALL NOT rely on omp's own configured default. omp's shipped default auto-approves reads, writes and execution, so a Session started without an explicit override would run tools unattended.

Magentic MUST NOT expose an auto-approving mode as a setting, a flag, a preference or a per-Session option. There SHALL be no configuration of Magentic under which an omp Session it starts approves a tool on the developer's behalf.

Where a developer wants unattended execution, they SHALL run omp themselves outside Magentic; Magentic states this rather than offering it.

#### Scenario: A Session is started with the gate forced on

- **WHEN** Magentic starts an omp Session
- **THEN** the session is launched with an approval mode that asks before running a tool
- **AND** the mode is set explicitly rather than inherited from omp's configuration

#### Scenario: No setting turns the gate off

- **WHEN** every setting, flag and preference Magentic exposes is enumerated
- **THEN** none of them causes an omp Session to approve a tool without a person
- **AND** an auto-approving mode is not offered

### Requirement: The gate's scope is stated, not overstated

omp's asking mode does not ask for every tool. It lets read-tier tools run without a request, and it honours a per-tool `allow` policy from configuration and a tool's own declared `allow` policy in every mode. Magentic SHALL therefore state the gate's scope as what it actually is: **tools that write or execute ask before they run**, and read-only tools do not.

Magentic SHALL launch every omp Session under its dedicated profile, so that per-tool policies from the developer's own omp configuration do not narrow that scope.

omp also merges configuration found in the Session's working directory. A Project can therefore carry configuration that allows a write or execution tool without asking. Magentic SHALL NOT claim that its profile excludes Project configuration, and SHALL present the gate's scope with that limit named until a later change closes it.

An interface MUST NOT describe a Session's tools as all gated, and MUST NOT present the read-tier exemption or the Project-configuration limit as absent.

#### Scenario: The stated scope matches omp's behavior

- **WHEN** an interface states what the approval gate covers for an omp Session
- **THEN** it names write and execution tools as gated
- **AND** it names read-only tools as not gated
- **AND** it names Project configuration as able to relax the gate

#### Scenario: The developer's own omp policies do not apply

- **WHEN** the developer's own omp profile allows a write or execution tool without asking
- **THEN** a Session Magentic starts still asks before running that tool

### Requirement: The gate is proven by launch provenance under confirmed identity

omp does not report its own approval mode, so the gate cannot be confirmed by asking a running session what mode it is in. Magentic SHALL therefore prove the gate from **launch provenance**: a Session may accept work only while Magentic can establish that its process is one Magentic itself started with an asking approval mode.

A Session SHALL be refused with a stated reason, and MUST NOT be presented as ready, started, or available to the Outbox, when Magentic cannot establish that provenance — including when the process was not started by Magentic, when the recorded launch is missing, or when identity confirmation fails.

Provenance SHALL survive a daemon restart only together with identity confirmation. A reclaimed process SHALL carry the recorded launch of the process that was actually reclaimed; a process whose identity cannot be confirmed SHALL NOT inherit another process's provenance.

#### Scenario: A Session without established provenance accepts no work

- **WHEN** an omp Session's process cannot be established as one Magentic started with an asking approval mode
- **THEN** the Session is refused with a reason naming the unproven approval gate
- **AND** it is not offered as ready
- **AND** no queued prompt is delivered to it

#### Scenario: A reclaimed process carries its own recorded launch

- **WHEN** the daemon reclaims an omp process after a restart and confirms its identity
- **THEN** the provenance used is the one recorded for that process
- **AND** a process whose identity cannot be confirmed inherits no provenance

### Requirement: The gate mechanism is verified against the installed omp before it is relied upon

Because the gate rests on a flag rather than on a reported state, Magentic SHALL verify that the installed omp still honours that flag, and SHALL do so before relying on it rather than assuming it from a previous release.

Verification SHALL be behavioral: an isolated, throwaway omp session carrying the same approval-relevant arguments a real Session carries, including Magentic's profile, SHALL be shown to raise an approval request both for a write tool and for an execution tool, and each request SHALL be answered with a denial so that nothing runs. The verification session MUST NOT run in a developer's Project or worktree, MUST NOT resume an existing session, and MUST NOT modify anything a developer owns.

Verification that **fails** — a write or execution tool ran without an approval request having been raised for it — SHALL cause Magentic to refuse to start omp Sessions, stating that the approval gate could not be verified and naming the installed omp version.

Verification that is **inconclusive** — the session ran but the model called no write or execution tool, so no request could be observed either way — SHALL NOT count as verified and SHALL NOT be reported as a failed gate. It SHALL be stated as inconclusive and attempted again.

Verification that **could not be attempted** SHALL be distinguished from verification that failed, and the refusal SHALL name the actual obstacle. In particular, a verification that cannot run because no provider is usable is a login problem, not a gate failure, and SHALL be reported as one so that the developer is directed at the thing that actually blocks them.

Magentic MAY reuse a previous verification for an omp build it has already verified, and SHALL repeat it when the installed omp changes. Magentic MUST NOT treat an unattempted verification as a successful one.

#### Scenario: The installed omp is verified before Sessions are allowed

- **WHEN** Magentic is asked to start an omp Session and the installed omp has not been verified
- **THEN** an isolated throwaway session is used to confirm that an operation requiring approval raises an approval request
- **AND** no Project or worktree is touched by the verification

#### Scenario: An omp that does not raise approval requests is refused

- **WHEN** during verification a write or execution tool runs without an approval request being raised for it
- **THEN** Magentic refuses to start omp Sessions
- **AND** it states that the approval gate could not be verified, naming the installed omp version

#### Scenario: A model that calls no tool leaves the gate unverified

- **WHEN** verification runs to completion without the model calling a write or execution tool
- **THEN** the result is stated as inconclusive
- **AND** it is neither recorded as verified nor reported as a failed gate
- **AND** no omp Session is started on the strength of it

#### Scenario: Verification blocked by a missing login names the login

- **WHEN** verification cannot be attempted because no provider is usable
- **THEN** the refusal names the missing login rather than the approval gate
- **AND** the verification is not recorded as successful
- **AND** it is attempted again once a provider is usable

#### Scenario: A changed omp is verified again

- **WHEN** the installed omp differs from the build a previous verification covered
- **THEN** verification is repeated before an omp Session is started
- **AND** the earlier result is not reused

### Requirement: An approval request blocks the Session and reaches a person

When omp asks for a decision about a tool, Magentic SHALL record an open approval request against the Session, SHALL observe the Session as awaiting a decision, and SHALL raise the developer's attention through the existing Attention model.

The Session SHALL remain blocked until a person answers. Blocking is the correct behavior rather than a limitation: a turn that waits indefinitely for a developer who is not at the machine is the intended outcome.

The request SHALL carry what the developer needs to decide — what omp asked, and for which Session — and an interface SHALL be able to present it without knowing omp's frame format.

**No permission decision is ever made on the developer's behalf**, whether or not an interface is open, whether or not the developer is present, and regardless of how long the request has been waiting. Magentic MUST NOT answer, time out into an answer, or default a request.

#### Scenario: A tool approval waits for a person

- **WHEN** omp asks for a decision about running a tool
- **THEN** an open approval request is recorded against the Session
- **AND** the Session is observed as awaiting a decision
- **AND** the developer's attention is raised
- **AND** the turn does not proceed until a person answers

#### Scenario: No interface is open when the request arrives

- **WHEN** an approval request arrives while every Magentic interface is closed
- **THEN** the request stays open
- **AND** it is presented when an interface opens
- **AND** it is not answered by the passage of time

#### Scenario: A decision is attributed to the person who made it

- **WHEN** a developer answers an open approval request
- **THEN** the answer is delivered to the session that asked
- **AND** the request is recorded as answered by a person

### Requirement: Reaching omp's own interface does not silently outlive the proof

Provenance proves how a process was started, not what has happened to it since. Because a developer can reach omp's own interface for a Session, and because omp does not report its approval mode, Magentic cannot prove that a gate it established at launch is still in force after someone has driven that session by another route.

Magentic SHALL therefore treat the Session's approval gate as no longer proven once a route into omp's own interface has been opened for it, and SHALL say so rather than continuing to present the Session as gated.

An interface MUST NOT claim that a Session's tools are gated by Magentic when the proof no longer holds. Magentic MUST NOT silently continue as though it did, and MUST NOT re-establish the proof by asking the session, which cannot answer.

#### Scenario: Opening omp's interface withdraws the claim

- **WHEN** a developer opens omp's own interface for a Session Magentic started
- **THEN** the Session's approval gate is recorded as no longer proven
- **AND** the Session is no longer presented as gated by Magentic
- **AND** the reason names the route that was opened

#### Scenario: The claim is not restored by asking the session

- **WHEN** Magentic needs to know whether a Session whose proof was withdrawn is still gated
- **THEN** it does not assert that it is
- **AND** it does not claim to have re-confirmed the mode from the session

### Requirement: An unanswerable request closes as unanswerable

An open approval request whose session is gone — the process ended, the daemon lost it, or the Session was killed — SHALL be closed as **no longer answerable**, naming why.

Such a request MUST NOT be closed as allowed or as denied. An interface MUST NOT present a stale request as if answering it would still have an effect.

#### Scenario: The process ends while a request is open

- **WHEN** an omp process with an open approval request ends
- **THEN** the request is closed as no longer answerable
- **AND** the reason names the ended session
- **AND** it is recorded neither as allowed nor as denied

#### Scenario: A stale request is not presented as actionable

- **WHEN** an interface displays approval requests and one of them is no longer answerable
- **THEN** it is shown as no longer answerable
- **AND** no control claims that answering it would grant or deny anything
