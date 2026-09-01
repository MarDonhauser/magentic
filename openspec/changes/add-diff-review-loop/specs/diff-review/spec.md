## Purpose

Lets a developer read the changes a Session produced, comment on individual diff
lines, and send those comments back into that same Session as a prompt, so the
coding agent can continue from the review instead of the developer retyping it.

## ADDED Requirements

### Requirement: Diff view for a Session's changes

Magentic SHALL present the changes belonging to a Session or a Worktree as a
structured diff grouped by file, where each changed line carries a stable
addressable reference. The view SHALL offer two comparison modes: the working
tree against the current commit, and the Session's branch against its base
branch. Untracked files SHALL be presented as added files with their content
available for comment. Binary files SHALL be listed as changed without line
content.

#### Scenario: Working-tree changes are shown per file

- **WHEN** a developer opens the diff view for a Session whose Worktree has
  uncommitted modifications
- **THEN** each modified file is listed with its hunks and its added, removed and
  context lines
- **AND** every added or removed line is individually addressable for commenting

#### Scenario: Branch against base branch

- **WHEN** the developer switches the diff view to the branch comparison mode
- **THEN** the diff shows the Session's branch measured against its base branch
- **AND** the comparison mode in effect is stated in the view

#### Scenario: Untracked file

- **WHEN** the Worktree contains a file that Git does not track and that is not
  excluded
- **THEN** the file appears in the diff as an added file
- **AND** its lines can be commented on like any other added line

#### Scenario: No changes to review

- **WHEN** the selected comparison yields no changed file
- **THEN** the view states that there is nothing to review
- **AND** no review action is offered

### Requirement: Unreadable Git knowledge is explicit

The diff view SHALL distinguish "no changes" from "changes could not be read". A
failed or unusable Git observation SHALL be reported as unavailable knowledge
naming the failing operation, and SHALL NOT be rendered as a clean or empty diff.
No review comment SHALL be anchored to a diff that could not be read.

#### Scenario: Git observation fails

- **WHEN** reading the diff for a Worktree fails or returns output that cannot be
  interpreted
- **THEN** the view reports the diff as unavailable together with the failing
  operation
- **AND** commenting and sending are unavailable for that diff

#### Scenario: Worktree no longer exists

- **WHEN** the developer opens the diff view for a Session whose Worktree can no
  longer be resolved
- **THEN** the view reports that the Worktree is unavailable
- **AND** an existing open Review for that Session is preserved unchanged

### Requirement: Line-anchored review comments

A developer SHALL be able to attach a comment to a single diff line or to a
contiguous range of lines within one file. Each comment SHALL retain the file
path, the line reference, the quoted code of the anchored lines, the comment
text, and the comparison mode it was made in. Comments SHALL be editable and
deletable while the Review is open, and their order SHALL follow file order and
then line order.

#### Scenario: Comment on one line

- **WHEN** the developer selects a changed line and writes a comment
- **THEN** the comment is attached to that line with the file path, the line
  reference and the quoted line content
- **AND** the line is marked as commented in the diff view

#### Scenario: Comment on a line range

- **WHEN** the developer selects several contiguous lines in one file and writes
  a comment
- **THEN** one comment is attached to that range with all selected lines quoted

#### Scenario: Editing and deleting

- **WHEN** the developer edits or deletes a comment of an open Review
- **THEN** the change takes effect immediately and the remaining comments keep
  their anchors

#### Scenario: Empty comment is rejected

- **WHEN** the developer tries to save a comment whose text is blank
- **THEN** the comment is rejected with a message and nothing is added to the
  Review

### Requirement: One open Review per Session

Each Session SHALL have at most one open Review. Comments SHALL be collected into
that Review and SHALL survive closing and reopening the desktop application, and
SHALL survive the agent runtime being restarted. A Review SHALL remain attached
to its Session by SessionID, independently of Session name, runtime name, or
branch.

#### Scenario: Comments survive a restart

- **WHEN** the developer comments on two lines and closes the desktop application
  before sending
- **AND** reopens it and returns to that Session
- **THEN** both comments are still present in the Session's open Review

#### Scenario: Session is renamed

- **WHEN** the Session is renamed while a Review is open
- **THEN** the open Review remains attached to that Session with all comments
  intact

#### Scenario: Comments from both comparison modes

- **WHEN** the developer comments in the working-tree mode and then comments in
  the branch mode of the same Session
- **THEN** both comments belong to the same open Review
- **AND** each comment states which comparison it was made in

### Requirement: Sending a Review to the Session

A single action SHALL send the open Review to the Session that produced the
changes. Magentic SHALL render the Review as one prompt that lists every comment
with its file path, line reference, quoted code, and comment text, in the
Review's order. The prompt SHALL be delivered through the Session's durable
message queue, so a busy agent receives it as soon as it is input-ready, and it
SHALL be delivered as one multi-line prompt rather than as several submissions.
Sending SHALL be refused when the Review has no comment.

#### Scenario: Review reaches an idle agent

- **WHEN** the developer sends a Review with three comments to a Session whose
  agent is input-ready
- **THEN** one prompt containing all three comments with their file paths, line
  references and quoted code is delivered to that Session's agent

#### Scenario: Review is queued for a busy agent

- **WHEN** the developer sends a Review while the Session's agent is working
- **THEN** the Review is queued durably for that Session
- **AND** it is delivered as soon as the agent is input-ready

#### Scenario: Empty Review

- **WHEN** the developer triggers the send action while the Review holds no
  comment
- **THEN** sending is refused with a message and no prompt is delivered

#### Scenario: Delivery fails

- **WHEN** the Review cannot be delivered because the Session's runtime is
  unavailable
- **THEN** the failure is reported
- **AND** the Review stays open with all its comments so it can be sent again

### Requirement: Review state after sending

Once a Review has been accepted for delivery it SHALL be marked as sent, carrying
the time it was sent, and the Session SHALL start a new empty open Review. A sent
Review SHALL remain readable as history until the developer discards it, and it
SHALL NOT be editable or sendable again.

#### Scenario: Fresh Review after sending

- **WHEN** a Review has been sent successfully
- **THEN** the Session has an empty open Review
- **AND** new comments go into that new Review

#### Scenario: Sent Review is read-only history

- **WHEN** the developer opens a Review that was already sent
- **THEN** its comments and their send time are shown
- **AND** editing and sending are unavailable for it

#### Scenario: Discarding history

- **WHEN** the developer discards a sent Review
- **THEN** it is removed and the open Review is unaffected
