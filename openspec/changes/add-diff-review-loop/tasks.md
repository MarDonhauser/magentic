## 1. Structured diff in the Repositories module

- [ ] 1.1 Add the structured diff types (diff observation, file, hunk, line with kind and old/new line numbers, comparison mode) to `core/repositories.go` and verify `go build ./...` succeeds with the types exported alongside the existing `WorktreeDiff`
- [ ] 1.2 Implement parsing of unified `git diff` output into those types behind the existing private command runner, returning `RepositoryFact` knowledge, and verify with table tests in `core/repositories_test.go` covering added, removed, context, renamed and mode-change entries
- [ ] 1.3 Reject malformed or truncated diff output as unavailable knowledge naming the failing operation, and verify a test asserts an unknown fact rather than an empty diff for garbled runner output
- [ ] 1.4 Implement the working-tree comparison (`git diff HEAD` plus untracked files rendered as added files, binaries listed without content) and verify a fake-runner test produces one file entry per change with untracked content commentable
- [ ] 1.5 Implement the branch-vs-base comparison against the merge base with the Project main branch resolved as today, and verify a test asserts the comparison mode is reported on the observation
- [ ] 1.6 Apply the file-count and per-file line caps from design.md, marking capped files as present-but-not-rendered, and verify a test asserts a capped file is listed and flagged rather than dropped

## 2. Durable Review on the Session

- [ ] 2.1 Add the Review and review-comment types to `core/state.go` (file path, line reference, quoted code, comment text, comparison mode, created time; Review with open/sent state and sent time) and verify JSON round-trip tests keep absent Reviews absent
- [ ] 2.2 Add semantic Registry changes for adding, editing and deleting a comment on a Session's open Review, and verify tests assert one open Review per Session and stable file-then-line ordering
- [ ] 2.3 Reject a blank comment text at the Registry change boundary and verify a test asserts the Review is unchanged
- [ ] 2.4 Add the Registry change that marks the open Review sent, starts a fresh empty open Review and bounds retained sent Reviews, and verify a test asserts the bound drops the oldest sent Review
- [ ] 2.5 Add the Registry change that discards a sent Review and verify a test asserts the open Review is untouched
- [ ] 2.6 Verify with a test that a Session rename leaves the open Review and its comments attached to the same `SessionID`

## 3. Rendering and delivering a Review

- [ ] 3.1 Implement rendering of a Review into one plain-text prompt (per comment: file path, line reference, fenced quoted code, comment text, comparison mode) and verify a golden test in `core` pins the exact format for a multi-comment Review
- [ ] 3.2 Add the review `QueuedMessageKind` and route sending through `SendQueuedMessageWithObserver` so the existing literal bracketed-paste delivery is reused unchanged, and verify a test asserts the prompt reaches the Outbox as a single message of the new kind
- [ ] 3.3 Refuse sending an empty Review with a message and verify a test asserts nothing is queued
- [ ] 3.4 Mark the Review sent only after the queue accepts it, leaving the open Review intact on failure, and verify a test with a failing observer asserts the comments survive

## 4. Desktop bindings

- [ ] 4.1 Add the Wails binding that returns the structured diff for a `ProjectID` plus opaque `WorktreeRef` and comparison mode, resolving the Worktree freshly at action time, and verify a test asserts an unresolvable Worktree yields an error rather than an empty diff
- [ ] 4.2 Add the bindings for adding, editing and deleting a review comment and for discarding a sent Review, keyed by `SessionID`, and verify `app` tests cover the blank-comment and unknown-Session errors
- [ ] 4.3 Add the binding that returns the rendered prompt preview for the open Review and the binding that sends it, and verify a test asserts sending an empty Review is refused
- [ ] 4.4 Regenerate the Wails bindings and verify `app/frontend/src/wails-bindings.test.js` covers the new calls forwarding `ProjectID`, `WorktreeRef` and `SessionID` unchanged

## 5. Desktop review surface

- [ ] 5.1 Render the structured diff per file with hunks and per-line addressing in a review surface next to the Git graph, opened from a Session and from a Worktree row, and verify by opening a Session with uncommitted changes in the running desktop app
- [ ] 5.2 Add the comparison-mode switch (working tree / branch vs base) stating the active mode, and verify both modes render for a Session on a feature branch
- [ ] 5.3 Implement selecting a line or contiguous line range and writing, editing and deleting a comment, marking commented lines in the diff, and verify the interactions in the running app plus unit tests for the review state module in `app/frontend/src`
- [ ] 5.4 Render unavailable diff knowledge as an explicit unavailable state naming the failing operation, with commenting and sending disabled, and verify by pointing the view at a removed Worktree
- [ ] 5.5 Render the empty-diff case as "nothing to review" with no review action offered, and verify against a clean Worktree
- [ ] 5.6 Add the send action with the prompt preview, and render sent Reviews as read-only history with their send time and a discard action, and verify the full loop in the running app: comment, send, agent receives the prompt, open Review is empty again
- [ ] 5.7 Verify the German interface copy of the new surface reads as whole sentences and carries no decorative badges, per the project's UI and copy rules

## 6. Integration verification

- [ ] 6.1 Verify end to end against a busy agent: send a Review while the agent is working and confirm it is queued and delivered as one multi-line prompt once the agent is input-ready
- [ ] 6.2 Verify a Review with comments survives quitting and restarting the desktop app before sending
- [ ] 6.3 Run `go test ./...` and the frontend test suite and verify both pass with the new tests included
