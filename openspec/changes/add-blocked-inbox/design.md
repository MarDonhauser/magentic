## Context

See proposal.md — Why for the motivation.

Constraints that shape the approach:

- `core.AttentionPlanner` is already the single stateful attention Module. It takes one `AttentionInput` (an `ObservationSnapshot` plus break, deployment, event, quiet, and active-Session facts) and returns one `AttentionPlan`. ADR 0007 makes it the only place attention policy is decided; notification, Dock, native-attention, and window code only executes its intents. Anything the inbox derives must come out of that same cycle.
- The planner already keeps per-Session memory (`attentionSessionMemory`: last known Attention, a pending-completion flag, and episode counters) and already suppresses Sessions whose facts are insufficient. It does not yet remember *when* a Session entered its current state, which is what ordering by waiting time needs.
- `ObservationSnapshot` and `SessionObservation` carry explicit availability, presence, and content-known facts (ADR 0004). The inbox must preserve that explicitness rather than collapsing unknown into "not waiting".
- Answering already exists end to end: `App.SendMessage` → `core.SendQueuedMessageWithObserver` → the durable per-Session Outbox, dispatched when the Session is input-ready. `OvQueuedMessage` already projects queued messages for the UI, including a `Stuck` marker.
- The desktop watcher polls Observation, builds the Overview, and runs the attention plan; the frontend consumes the resulting structures over the Wails binding. The TUI consumes the same `core` projections from the root package.
- The planner's memory is in-process only; nothing about attention is persisted across restarts.

## Goals / Non-Goals

**Goals:**

- One derivation of "who is waiting", shared by the inbox, notifications, the badge, and native attention.
- Deterministic, testable ordering that survives a restart without inventing wait durations.
- Answering from an entry that reuses the existing Outbox semantics unchanged.
- A projection shaped so both frontends can render it without re-deriving anything.

**Non-Goals:**

- No persistence of waiting-since across restarts in this change.
- No changes to Outbox delivery policy, retry behavior, or prompt-readiness detection.
- No new Observation probes, no new poll cadence, no vendor-specific prompt parsing.
- No inline answering in the TUI.

## Decisions

### The inbox is a field of `AttentionPlan`, not a new module

`AttentionPlan` gains an `Inbox []AttentionInboxEntry` (plus a completeness fact, see below), produced by the same `Plan` call that produces notifications. The planner walks the same observation loop it already walks in `sessionCandidates`; entries are emitted for Sessions whose Attention is `needs-input` or `review`, using the same insufficient-facts gate that already exists.

*Why:* ADR 0007 says attention is planned once, before side effects. A separate "inbox builder" reading the same snapshot would be a second policy site that could disagree with the notifications — exactly the drift the ADR exists to prevent.

*Alternative considered:* deriving the inbox in `BuildOverviewFromObservation`, where the UI already gets Session data. Rejected: Overview has no attention memory, so it could not know waiting-since, and it would put attention policy in a second module.

*Alternative considered:* a standalone `core/inbox.go` module fed by the planner's outputs. Rejected for this change as an extra seam without an extra consumer; the planner already holds every fact needed. If the projection grows (grouping, filters, snoozing), splitting the rendering-side projection out later is a local refactor.

### Waiting-since lives in planner memory and is explicitly a lower bound when unknown

`attentionSessionMemory` gains `waitingSince time.Time` and `waitingSinceKnown bool`. When a Session's observed Attention changes into `needs-input` or `review`, the planner stamps `waitingSince = now` with `waitingSinceKnown = true`. When the planner sees a Session already waiting on the first cycle it knows about that Session (`!memory.known`), it stamps the current time with `waitingSinceKnown = false`, and the entry reports its age as a lower bound (`WaitingAtLeast`).

Ordering: entries with an unknown start sort before all entries with a known start; within each group, older first; ties broken by `SessionID` for determinism.

*Why:* the planner is already the only component with cross-cycle memory, and ADR 0004's rule — do not translate missing knowledge into a definite value — applies directly. Showing a Session that has been blocked for an hour as "waiting 3 seconds" after a restart would be exactly that mistake.

*Alternative considered:* using `SessionObservation.Activity` as the wait start. Rejected: activity is the last pane change, which is not the moment the Session started waiting, and `ActivityKnown` is often false.

*Alternative considered:* persisting waiting-since in the Registry. Rejected for now: it adds a durable write on every attention cycle for a display-only fact, and the lower-bound representation already keeps the ordering honest across restarts.

### A changed waiting kind restarts the wait; a Session appears at most once

`needs-input` → `review` re-stamps `waitingSince`. The inbox is keyed by `SessionID`, so a Session never produces two entries.

*Why:* the two kinds ask for different work from the developer, so the second wait is a new wait. Keying by Session keeps the list countable and lets it agree with the Dock badge.

### Completeness is carried on the plan, not inferred from an empty list

The inbox carries an explicit completeness fact derived from `AttentionObservationState`: complete when the Observation is available, incomplete when partial, and unavailable when the Observation is unavailable (in which case no entries are emitted and the previous list must not be presented as current). Frontends render the unavailable and incomplete cases as text; they never treat an empty list as proof that nothing is waiting.

*Why:* this mirrors the existing `AttentionDockBadge.Update`/`Complete` treatment, so the badge and the inbox tell the same story about how much is known.

### Answering reuses `App.SendMessage` and the existing Outbox

The inbox entry's answer action calls the existing bound method with the entry's `SessionID`. No new transport, no direct `tmux send-keys` from the inbox. Entries surface the Session's queued messages (already available via `OvAgent.Queued`) so a submitted answer that has not been delivered is visible as pending.

*Why:* the Outbox already solves the hard part — delivering only when the Session is input-ready, keeping the message durable otherwise. Sending keys straight from the inbox would bypass that readiness check and could drop text into a busy pane.

### Clearing is observation-driven only

An entry disappears because the next `Plan` cycle no longer produces it. Submitting an answer changes nothing about the entry except that a queued message is now attached to it.

*Why:* the alternative (optimistically removing the entry on submit) hides the case where the agent asks the same question again or where delivery is stuck — the exact failure the developer opened the inbox to catch.

### Frontend plumbing: the watcher already has the plan

The watcher already calls the planner each cycle and applies the plan. It passes the plan's inbox along with the Overview it already emits to the frontend, so the inbox needs no extra poll. A bound accessor returns the last planned inbox for a first paint before the next cycle.

The TUI reads the same `AttentionPlan.Inbox` from its own planner instance and renders a read-only list with jump-to-Session. Cheap because the projection is already computed; inline answering is excluded to avoid building an input mode there in this change.

### Assumptions recorded

- `needs-input` and `review` are the complete set of waiting kinds for this change. `unknown` is not waiting and not not-waiting; it is simply not listed.
- Marking a Session seen (`MarkSeen`) does not by itself clear an entry — only the resulting Observation does. If the developer wants an entry gone without answering, they open the Session.
- The desktop inbox is a view within the existing window (not a separate window or a menu-bar popover), reachable without tearing down an open terminal.
- Copy in both frontends stays German, matching the existing UI.

## Risks / Trade-offs

- **Waiting-since is lost on restart, so ordering right after a restart is coarse** → every affected entry is marked as a lower bound and sorts to the top, which is the honest reading; persistence can be added later without changing the projection's shape.
- **`AttentionPlan` grows a display-shaped field, blurring "policy" and "projection"** → the field is derived from the same facts in the same pass and stays free of formatting (durations as timestamps, no strings the UI must parse); if it grows further, the projection can move behind its own type.
- **Building an entry per waiting Session on every poll adds work to a hot loop** → the loop already iterates every observation; the added work is a struct append, and content excerpts reuse the already-normalized `Content`.
- **Attention misclassification becomes more visible: a Session wrongly read as `needs-input` now sits in a list instead of producing one dismissible toast** → this is largely the point (a wrong entry is now inspectable and reportable), but it raises the value of the content excerpt so the developer can tell at a glance that an entry is bogus.
- **Two surfaces can drift** → both render the same `AttentionPlan.Inbox` with no re-sorting in the frontends; ordering is fixed in core and covered by tests.
- **An entry that never clears because a Session is genuinely stuck could become noise** → out of scope to auto-hide; the lower-bound age plus the stuck-message marker make the condition legible instead of hidden.

## Migration Plan

Additive only. `AttentionPlan` gains fields; existing consumers of notifications, badge, and native attention are untouched, so an incomplete frontend simply does not render the inbox. Rollback is removing the new surface; core can keep the field harmlessly.

## Open Questions

- Whether the desktop inbox should also offer the common one-tap approvals (for example a "yes" reply) as buttons, or only free text. Deferrable: the answer path is the same Outbox call either way, so adding buttons later changes no spec and no core behavior.
- Whether `review` entries should offer a direct route to the Worktree diff rather than only to the Session. Deferrable for the same reason; it is an added action on an existing entry.
