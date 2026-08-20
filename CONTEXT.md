# Magentic

Magentic coordinates persistent coding work across local Projects, Sessions,
Worktrees, coding-agent runs, and Specifications.

## Projects and Worktrees

**Project**:
A registered software repository and the durable context that groups its Sessions, Worktrees, and Specifications.
_Avoid_: Repository, repo, project folder

**ProjectID**:
The durable identity of a Project, unchanged when its name or location changes.
_Avoid_: Project name, Project path

**Worktree**:
A Git working directory associated with a Project and available as a Session's place of work.
_Avoid_: Branch, checkout, folder

**RepositoryKnowledge**:
The known, unknown, or inapplicable result of observing Git facts for a Project or Worktree.
_Avoid_: Empty value, false, clean

## Sessions

**Session**:
A durable coding or terminal work context tracked by Magentic, whether or not its external runtime currently exists.
_Avoid_: Agent, tmux session, process

**SessionID**:
The durable identity of a Session, unchanged when its name, presentation, or runtime changes.
_Avoid_: Session name, AgentRunRef, RuntimeName

**AgentRunRef**:
A vendor-qualified reference to a coding-agent run or conversation associated with a Session.
_Avoid_: SessionID, provider session ID, transcript ID

**RuntimeName**:
The replaceable name by which an external Session runtime is addressed.
_Avoid_: SessionID, Session name

**SessionKind**:
What kind of working environment a Session hosts, independently of how it is presented or why it was created.
_Avoid_: Kind, SessionPresentation, SessionPurpose

**SessionPresentation**:
Where a Session belongs in Magentic's user experience, independently of what it hosts or why it was created.
_Avoid_: SessionKind, SessionPurpose, visibility

**SessionPurpose**:
The reason a Session exists, such as ordinary work, cleanup, merge, or deployment.
_Avoid_: SessionKind, SessionPresentation, mode

**Observation**:
A time-bounded account of an external Session runtime's availability, activity, content, and attention needs.
_Avoid_: Status, Registry state

**LifecycleTransition**:
A uniquely identified durable intent to move a Session toward a desired state, together with the applied progress needed to reconcile it.
_Avoid_: Event, log entry, command

**DesiredState**:
The durable outcome sought for a Session independently of the currently observed runtime state.
_Avoid_: Observation, phase, action

## Attention

**AttentionPlan**:
A deterministic set of notification, Dock badge, native attention, and window intents derived from one Observation plus break, deployment, and quiet signals.
_Avoid_: Notification side effect, status transition, watcher state

**AttentionSuppression**:
An explicit reason why a potential attention intent was deferred or consumed without notifying the developer.
_Avoid_: Missing notification, silent return, debounce flag

## Work History

**WorkHistory**:
The normalized local history of developer and coding-agent activity across supported agent vendors.
_Avoid_: Transcript, statistics cache, quota

**HistoryEvent**:
A provider-neutral prompt, output, or usage fact attributed to a Project and Session when that association is known.
_Avoid_: Raw envelope, transcript line, message blob

## Specifications

**Specification**:
A source-authored unit of planned work discovered within a Project and presented on its Board.
_Avoid_: Change, Board item, card

**SpecificationRef**:
A Project- and source-qualified reference to a Specification using that source's native identity.
_Avoid_: Specification path, title, bare ID

**SpecificationState**:
The source-derived position of a Specification as backlog, active, review, or terminally done.
_Avoid_: Task completion percentage, Board column guess
