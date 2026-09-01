## Why

Magentic can already show what a coding agent did — a Git graph per Project and a
plain-text Worktree diff in a modal — but there is no way back: the developer who
spots a problem in that diff has to retype the finding into the Session by hand.
Competing tools have made this return channel their most-cited feature (Emdash's
diff review, cmux's review sidebar where comments on a diff go back to the agent),
and it is the half of the review loop magentic is missing. Closing it turns
reading a diff from a dead end into the normal way to hand corrections back to the
agent that produced them.

## What Changes

- A structured diff view per Session and per Worktree, replacing the single
  read-only text modal for review purposes. Two comparison modes: the working tree
  against `HEAD`, and the Session's branch against its base branch. The diff is
  presented per file with hunks and per-line addressing.
- Line-anchored review comments. The developer selects a line (or a line range)
  in the diff and writes a comment. Comments accumulate into exactly one open
  Review per Session, held durably so the desktop app can be closed and reopened
  without losing them.
- A single "send review" action that renders the open Review into one prompt
  containing, per comment, the file path, the line reference, the quoted code and
  the comment text, and delivers that prompt to the Session's agent through the
  existing queued-message path (Outbox), so a busy agent receives it as soon as it
  is input-ready.
- After successful delivery the Review is marked sent and the Session starts with
  a fresh, empty Review; a sent Review remains visible as history until it is
  discarded.
- Unavailable Git knowledge stays explicit. A diff that cannot be read is
  reported as unavailable, never as "no changes", and review comments are never
  anchored to a diff that could not be read.

## Capabilities

### New Capabilities
- `diff-review`: Reviewing a Session's changes inside magentic — the diff view
  with its two comparison modes, line-anchored comments collected into one Review
  per Session, and delivery of that Review as a prompt into the Session that
  produced the changes.

### Modified Capabilities
<!-- None. openspec/specs/ currently holds no capabilities, so no existing
     requirements change. -->

## Impact

- `core/repositories.go`: a structured diff alongside the existing text-producing
  `WorktreeDiff`, plus branch-vs-base comparison; both keep returning
  `RepositoryFact` knowledge rather than collapsing failures.
- `core/state.go` and the Registry change path: a durable Review attached to a
  Session, written through a semantic Registry change like `Outbox` and
  `Automation` are today.
- `core/outbox.go` / `core/actions.go`: a new queued-message kind for a delivered
  Review; the existing bracketed-paste prompt delivery is reused unchanged.
- `app/tools.go` and `app/app.go`: Wails bindings for reading a diff, editing
  Review comments and sending the Review; `WorktreeRef` and `SessionID` stay the
  only transported handles.
- `app/frontend/src/`: a diff review surface next to the Git graph, replacing the
  diff modal as the review entry point.
- The TUI (root package) is unaffected in this change; the loop is desktop-only
  for now.
- Non-goals: GitHub PR creation, CI-check monitoring, remote or multi-user review.
