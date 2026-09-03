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

**WorktreeRef**:
An opaque Project-qualified handle that a UI may retain and ask Repositories to resolve freshly without receiving or authorizing a filesystem path.
_Avoid_: Worktree path, branch name, browser-supplied directory

**RepositoryKnowledge**:
The known, partial, unknown, or explicitly not-a-repository result of observing Git facts for a Project or Worktree.
_Avoid_: Empty value, false, clean

**StatsCommitCoverage**:
Whether commit totals cover every registered repository, only a known subset, no readable repository, or no applicable repository.
_Avoid_: Zero commits, Git error, provider coverage

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
The replaceable, opaque and exact scalar by which an external Session runtime is addressed; it is never trimmed or reconstructed from a display name at an external boundary.
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
A deterministic set of notification, Dock badge, native attention, and window intents derived from one Observation plus explicit attention events, break advice, deployment outcomes, active-Session, and quiet signals.
_Avoid_: Notification side effect, status transition, watcher state

**AttentionEvent**:
An explicit local transition, such as a finished or reset break, submitted to Attention so it shares the same priority and deduplication policy as all other attention facts.
_Avoid_: Direct notification, callback side effect, UI-only flag

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

## Agent Timeline

**Item**:
One normalized, provider-neutral unit of agent activity, carrying a stable identity, the time it occurred, the role that produced it, exactly one kind, and the title and detail its vendor's normalizer decided.
_Avoid_: Transcript line, tool payload, rendered row

**ItemKind**:
The closed set an Item's activity is drawn from, with an explicitly unknown kind that keeps the vendor's own label rather than dropping a record.
_Avoid_: Tool name, block type, record type

**Conversation**:
The ordered sequence of Items belonging to one coding-agent run, derived from the vendor's record and never durable state of Magentic's own.
_Avoid_: Transcript file, WorkHistory query, terminal scrollback

**ConversationRef**:
A vendor-qualified handle of one run's Conversation, resolved from a Session's recorded vendor and run reference alone.
_Avoid_: Session name, RuntimeName, record path

## Specifications

**Specification**:
A source-authored unit of planned work discovered within a Project and presented on its Board.
_Avoid_: Change, Board item, card

**SpecificationRef**:
A Project- and source-qualified reference to a Specification using that source's native identity.
_Avoid_: Specification path, title, bare ID

**SpecificationStage**:
The source-derived position of a Specification as backlog, active, review, done, or explicitly unknown when its source facts cannot support a stage.
_Avoid_: Task completion percentage, Board column guess

**SpecificationLifecycle**:
The stage, reason, archive fact, and terminal fact derived together from one Specification source reading.
_Avoid_: Board column, inferred completion, task percentage

**SpecificationStartToken**:
An opaque, short-lived authority that lets Specifications re-resolve a discovered item and verify its Project identity and physical containment immediately before provisioning.
_Avoid_: Specification path, browser-supplied work instructions, SpecificationRef
