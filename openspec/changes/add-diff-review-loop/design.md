## Context

See proposal.md — Why. The pieces this design has to fit between already exist:

- `core.Repositories` owns every Git meaning and returns `RepositoryFact[T]`
  values that distinguish known, partial, unknown and not-a-repository knowledge
  (ADR 0004). `WorktreeDiff` already produces a human-readable text blob of
  status, `git diff HEAD` and untracked files; `app.WorktreeDiff` caps it at
  400 000 characters and the desktop shows it in a colorized modal.
- A desktop action never carries a filesystem path. It carries a `ProjectID` plus
  an opaque `WorktreeRef`, and `Repositories.ResolveWorktree` resolves it against
  a fresh topology at action time (ADR 0001, ADR 0004).
- Durable Session state is written only as a semantic Registry change under
  interprocess coordination (ADR 0002). `Outbox` and `Automation` on `Session`
  are the existing precedents.
- Prompt delivery already exists end to end: `SendQueuedMessageWithObserver` puts
  a `QueuedMessage` into the Session's `Outbox`, and `sendPromptLiteralValidated`
  hands it to tmux as a single literal `send-keys` argument, wrapping multi-line
  text in bracketed paste so it stays one prompt until the final Enter.
- The `core/gitgraph.go` graph is a per-Project commit and branch view. It is a
  navigation surface, not a diff, and this change does not alter it.

Assumptions recorded here because the change was scoped without a clarification
round:

1. The review loop is desktop-only in this change. The TUI keeps the existing
   text diff.
2. "The Session's base branch" means the Project's main branch as
   `resolveRepositoryMainBranch` already determines it, compared through the
   merge base. `Session.BaseCommit` is a Session-start baseline for delta
   counting and is not used as the review base.
3. A review comment anchors to a line in a diff, not to a commit-stable blob
   position. If the underlying file changes after a comment was made, the quoted
   code the comment carries stays as it was captured.
4. Interface copy stays German, matching the rest of the desktop app.

## Goals / Non-Goals

**Goals:**

- One structured diff representation that both comparison modes produce, so the
  desktop renders and addresses lines the same way in either mode.
- Review state durable at the same level of care as `Outbox`: survives process
  restart, is attached by `SessionID`, and is written through one semantic
  Registry change.
- Reuse of the existing prompt delivery path unchanged. A Review becomes a
  queued message; nothing new touches tmux.

**Non-Goals:**

- No side-by-side rendering, syntax highlighting, word-level intra-line diffing,
  or diff-based navigation from the Git graph in this change.
- No threading, replies, or resolution state on comments. A comment is written
  once, sent once, and gone.
- No spec-level change to the Git graph, the Board, or Attention.

## Decisions

### A structured diff type in `core`, alongside the existing text diff

`Repositories` gains a structured diff observation — files, hunks, lines, each
line carrying its kind (added, removed, context), its old and new line numbers,
and its text — returned as a `RepositoryFact`, parsed from `git diff` with
explicit rejection of output that cannot be interpreted.

*Why:* the desktop cannot anchor a comment to a line inside an opaque text blob,
and parsing the blob in JavaScript would put a Git meaning outside the module
that owns Git meanings. *Alternative considered:* keep the text diff and anchor
comments to text offsets in it — rejected because the anchor breaks on any
change in the rendering, and because it moves diff parsing into the frontend.
*Alternative considered:* replace `WorktreeDiff` outright — rejected as
unnecessary churn; the text form still serves the existing modal and the TUI, and
the structured form is a second reader over the same commands.

### Both comparison modes produce the same structure

Working tree is `git diff HEAD` plus untracked files rendered as added files.
Branch-vs-base is `git diff <merge-base>...HEAD`. Both are normalized into the
same file/hunk/line shape, and the observation states which comparison produced
it. A file the parser cannot interpret makes the whole observation unavailable
rather than silently dropping the file.

*Why:* one renderer, one comment anchor format, one place where a parse failure
turns into unavailable knowledge (ADR 0004). *Trade-off:* an unparseable file
costs the developer the whole diff view; accepted because a partial diff that
looks complete is the exact failure ADR 0004 forbids.

### Review lives on the Session in the Registry

`Session` gains a `Review` (the open one) and a small list of sent Reviews. Every
mutation — add, edit, delete a comment, send, discard — is a semantic Registry
change applied under the existing coordination, like `SetSessionAutomation`.

*Why:* ADR 0002 admits no other writer, and the durability requirement in the
spec is exactly what the Registry already provides. *Alternative considered:* a
separate review store file — rejected because it would need its own locking and
its own answer to Session removal, both of which the Registry already has.
*Consequence:* removing a Session removes its Reviews with it, which is the
intended behavior.

The comparison mode is stored per comment, so a Review mixing working-tree and
branch comments renders unambiguously in the prompt.

### Sending is a new queued-message kind

A new `QueuedMessageKind` for a review, delivered through
`SendQueuedMessageWithObserver`. The rendered prompt is plain text with one block
per comment: file path, line reference, the quoted code fenced, then the comment.

*Why:* the queue already solves "the agent is busy", already survives restart,
and `promptTerminalInput` already keeps a multi-line prompt together via
bracketed paste for Claude, Codex, Gemini and Copilot. A distinct kind (rather
than reusing `message`) lets the Outbox display name what is waiting and keeps
the door open for a per-kind delivery policy later, as `handoff` already has one.
*Alternative considered:* send each comment as its own prompt — rejected: the
agent would start working after the first one.

### Marking sent is a separate Registry change after acceptance

The Review is marked sent only after `SendQueuedMessage` has accepted it — that
is, after it is durably queued, not after the agent has read it. Failure leaves
the open Review untouched so it can be sent again.

*Why:* the queue is the durability boundary; waiting for the agent to consume the
prompt would need a second observation and could lose the Review to a crash in
between. *Trade-off:* a queued Review that the developer cancels out of the
Outbox is still recorded as sent. Accepted; the sent Review stays visible as
history and its text can be read there.

### Where it lives in the desktop

The review surface sits next to the Git graph in the existing project view and is
opened from a Session and from a Worktree row. The existing `git-state` diff
modal stays for a quick look; the review surface is the reviewing entry point.
The prompt is shown in a preview before sending.

*Why:* the diff-per-Session is the developer's reading position for an agent that
just finished, which is where attention already sends them.

## Risks / Trade-offs

- **Large diffs make the desktop slow or unreadable.** → Cap the parsed diff (a
  file count and a per-file line count), and mark files beyond the cap as
  present-but-not-rendered rather than dropping them silently. A capped file
  cannot be commented on and says so.
- **A comment anchored to a line the agent has since changed misleads the
  agent.** → Every comment carries the quoted code as captured, and the prompt
  states the comparison mode, so the agent can see what the comment referred to
  even if the file has moved on.
- **Comment text is developer input that reaches tmux.** → It goes through the
  existing literal `send-keys` path, which passes the prompt as one argument and
  never interpolates it into a shell command. This change must not introduce a
  second, shell-based path.
- **Registry growth from retained sent Reviews.** → Bound the retained sent
  Reviews per Session and drop the oldest beyond the bound.
- **Untracked binary or very large files.** → Listed as changed without content,
  the same treatment tracked binaries get.

## Migration Plan

Additive only. `Session.Review` is absent in existing state files and reads as
"no open Review"; no migration step and no dual-write. The existing
`WorktreeDiff` binding and its modal keep working unchanged, so the change can be
reverted by removing the new surface without touching stored state. Reviews
written by a newer build are ignored by an older build, which is acceptable
because the Registry preserves unknown fields it does not interpret only if it
already does — this must be verified during implementation, and if it does not,
the rollback story is "sent Reviews are lost, open Reviews are lost", which is
tolerable for planning-stage data.

## Open Questions

- Whether the review surface should also be reachable from the Board when a
  Specification's Session finishes. Deferrable: it is an entry point, not a
  behavior change.
- Whether a per-kind delivery policy for review messages is worth having (as
  `handoff` has one). Deferrable: the default policy is correct until evidence
  says otherwise.
