## 1. Attention planner: inbox projection

- [x] 1.1 Add the inbox types to `core/attention.go` — an entry carrying SessionID, waiting kind, waiting-since with its known/lower-bound fact, the content excerpt with its known fact, and a completeness fact for the list — and verify `go build ./...` passes with no other package changed
- [x] 1.2 Extend `attentionSessionMemory` with `waitingSince` / `waitingSinceKnown` and stamp it when a Session enters `needs-input` or `review`; verify with a planner test that two cycles across a state change produce a known start at the moment of the change
- [x] 1.3 Stamp an unknown (lower-bound) start when a Session is already waiting on the first cycle the planner knows about it; verify with a test that a first-cycle waiting Session is reported as a lower bound, not as a fresh wait
- [x] 1.4 Re-stamp waiting-since when the waiting kind changes between `needs-input` and `review`; verify with a test that the entry keeps one row per Session and its wait restarts
- [x] 1.5 Emit `AttentionPlan.Inbox` from `Plan`, reusing the existing insufficient-facts gate so Sessions with unavailable Observation, unknown presence, or unknown Attention are not listed; verify with a test that such Sessions appear in neither the inbox nor as "not waiting"
- [x] 1.6 Implement the ordering — unknown-start entries first, then oldest known wait first, ties broken by SessionID — and verify with a test that two renderings of unchanged facts produce the identical order
- [x] 1.7 Derive the list-completeness fact from `AttentionObservationState` (available → complete, partial → incomplete, unavailable → no entries and unavailable) and verify with tests for all three states, including that an unavailable Observation does not produce an empty-but-complete inbox
- [x] 1.8 Verify with a test that one `Plan` call produces an inbox describing the same waiting Sessions as the notification and badge output of that same call, so no second attention derivation is introduced

## 2. Desktop app wiring

- [ ] 2.1 Carry the planned inbox out of the watcher cycle alongside the plan it already applies, and add a bound accessor returning the last planned inbox for a first paint; verify `go build ./...` in `app/` and that the accessor returns the unavailable state before the first cycle
- [ ] 2.2 Enrich each entry for the UI with its Project name, Session name, and the Session's queued messages (including the stuck marker) taken from the existing Overview projection; verify with a test that a Session with a pending queued message is marked as awaiting delivery
- [ ] 2.3 Regenerate the Wails bindings and verify the frontend can call the inbox accessor and `SendMessage` with the types it receives

## 3. Desktop inbox view

- [ ] 3.1 Build the inbox view in `app/frontend/` as a surface reachable from anywhere without tearing down an open terminal; verify by opening the inbox with a Session terminal open and returning to that Session unchanged
- [ ] 3.2 Render each entry with Project, Session, waiting kind, waiting time (lower-bound waits marked as such), and the content excerpt, with an explicit marker when content is not known; verify against a Session whose Observation reports content as unknown
- [ ] 3.3 Render the unavailable and incomplete list states as text instead of an empty inbox; verify by forcing an unavailable and a partial Observation
- [ ] 3.4 Add the answer action on an entry, routed through the existing `SendMessage` binding, plus an action to open the Session; verify that answering an input-ready Session delivers the text and that answering a busy Session leaves it queued and visible as pending
- [ ] 3.5 Verify that an entry stays until the next Observation reports the Session as no longer waiting — submitting an answer alone must not remove it

## 4. TUI inbox

- [ ] 4.1 Render a read-only inbox list in the TUI from the same `AttentionPlan.Inbox`, with no re-sorting in the view layer; verify with a view test that the rendered order matches the core order
- [ ] 4.2 Add jump-to-Session from a selected entry and verify with a model test that selecting an entry moves to that Session
- [ ] 4.3 Verify with a test that the TUI and desktop projections come from the same planner output, so both surfaces list the same entries in the same order

## 5. Verification

- [ ] 5.1 Write the end-to-end core test covering the spec's clearing scenarios — Session resumes work, runtime becomes absent, waiting kind changes — and verify each removes or replaces the entry as specified
- [ ] 5.2 Run `go build ./...` and `go vet ./...` in the repository root and in `app/`, and report the full test suite as ready to run rather than running it
