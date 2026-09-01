## Why

Once more than two or three coding agents run at the same time, the developer has no single place that answers "who is waiting for me right now?" Attention today is expressed as notifications, a Dock badge, and per-Session markers scattered across the Project-grouped sidebar, so finding the blocked Sessions means scanning panes. Competitor research points the same way: herdr turns six running agents into six toasts instead of one list, while the feature cmux is most praised for is a consolidated approval feed where every blocked run is visible in one place and can be answered inline without switching to its pane.

Magentic already derives the facts this needs. Observation reports each Session's `AttentionState`, and the AttentionPlanner already owns priority, deduplication, and suppression for exactly those facts. What is missing is a surface that presents the same planned attention as a standing, ordered list instead of a stream of one-shot intents.

## What Changes

- Add a **BlockedInbox**: one cross-Project, ordered list of every Session that is currently waiting on the developer — a permission prompt or question (`needs-input`) and finished work awaiting review (`review`).
- Produce the inbox inside the existing attention planner cycle, as an additional field of `AttentionPlan`. No second notification path, no second observation loop, no separate watcher.
- Order entries by waiting time, longest wait first. The planner remembers when each Session entered its current waiting state; a wait whose start is not known from planner memory is reported as a lower bound rather than as a fresh wait, following ADR 0004.
- Answer or unblock a Session directly from its entry. The inbox reuses the existing queued-message transport (`SendMessage` / Outbox), so a reply is delivered when the Session is input-ready and stays durably queued when it is not.
- Clear an entry when the Session leaves its waiting state — it resumed work, its answer was delivered, its runtime went absent, or the developer marked it seen. Clearing is derived from the next Observation, never from the act of typing a reply.
- Surface the inbox as a first-class view in the desktop app, reachable without leaving the current Session, showing per entry: Project, Session, waiting kind, waiting time, and the tail of pane content that explains what is being asked.
- Surface the same list in the TUI where it fits cheaply: a read-only, ordered overview plus jump-to-Session, built on the same core projection. Inline answering in the TUI is out of scope for this change.
- Represent unavailable knowledge explicitly: when Observation is unavailable or partial, the inbox says its list is incomplete instead of rendering an empty or stale-looking inbox.

## Capabilities

### New Capabilities

- `blocked-inbox`: The cross-Project list of Sessions waiting on the developer — what qualifies as waiting, how entries are ordered and aged, how a developer answers from an entry, and when an entry clears.

### Modified Capabilities

None. `openspec/specs/` currently holds no published capabilities, so the attention behavior this change builds on is described as context inside the new capability rather than as a delta against an existing spec.

## Impact

- `core/attention.go`: `AttentionPlan` gains the inbox projection; per-Session planner memory gains the waiting-since fact. Existing notification, badge, native-attention, and suppression behavior is unchanged.
- `core/observation.go`: read-only consumer. No new probes; the inbox is derived from the `ObservationSnapshot` the planner already receives.
- `app/`: a new bound method exposing the inbox and its actions to the frontend; the watcher passes the planner's inbox along with the plan it already applies.
- `app/frontend/`: a new inbox view and its entry actions.
- Root TUI package (`model.go`, `view.go`): an inbox overview fed by the same core projection.
- Reuses `core/outbox.go` for answering. No new transport, no changes to delivery semantics.

## Non-goals

- No new notification transports (no email, push, webhook, or chat delivery).
- No remote or mobile access to the inbox.
- No multi-user or shared-inbox behavior; the inbox belongs to the one developer running Magentic locally.
- No new agent-vendor integrations and no vendor-specific parsing of prompts beyond the attention facts Observation already derives.
