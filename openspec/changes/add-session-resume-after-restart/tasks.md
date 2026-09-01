## 1. Durable resume facts

- [ ] 1.1 Add `LastStatus` and `LastStatusAt` to `core.Session` in `core/state.go`, serialized as a stable status *string* (not the iota ordinal) with an unknown or absent value reading back as `StatusUnknown`; verify with a round-trip test in `core/state_test.go` that a `state.json` written before this change parses with both fields unset and reads as never observed.
- [ ] 1.2 Add the fixed `AgentStatus` ↔ persisted-label mapping, independent of the German `Label()` display strings; verify with a table test that every `AgentStatus` constant has a persisted label, that labels round-trip, and that an unrecognized label maps to `StatusUnknown`.
- [ ] 1.3 Record the observed status and its observation time onto the Session record at the end of each Observation pass, replacing rather than appending; verify with a test in `core/observation_test.go` that two successive observations leave exactly the latest status and time.

## 2. Per-agent-kind resume behavior

- [ ] 2.1 Add `ResumeBehavior` (`ResumeByRunRef`, `ResumeFreshOnly`, `ResumeUnsupported`) and the `ResumeBehavior()` method to the `AgentProvider` interface in `core/provider.go`; verify the package compiles and a test asserts every builtin provider declares a behavior.
- [ ] 2.2 Declare `ResumeByRunRef` for Claude Code, Codex, and Copilot, and `ResumeFreshOnly` for Gemini CLI (matching the existing comment that Gemini has no verified resume form); verify with a test in `core/provider_test.go` pinning each vendor's declared behavior.
- [ ] 2.3 Add a resume-command test that, for each `ResumeByRunRef` vendor and a recorded `AgentRunRef`, `StartCommand(..., "resume")` produces the vendor's real resume line (`claude --resume <id>`, `codex resume <id>`, `copilot --resume=<id>`) with the id shell-quoted, and that Gemini produces a plain fresh start.

## 3. Resumable classification

- [ ] 3.1 Introduce the resumable reading in `core/status.go` / `core/observation.go` so an absent runtime with a usable record no longer collapses to `StatusDead`; verify with tests in `core/status_semantics_test.go` covering runtime-gone-record-intact → resumable and runtime-gone-record-unusable → dead.
- [ ] 3.2 Carry the reason a Session is not resumable (missing directory, unknown vendor, no run reference, terminal Session, vendor cannot resume) alongside the dead reading; verify each reason is produced by its own test case.
- [ ] 3.3 Keep an unobservable runtime explicitly unknown rather than resumable or dead, per ADR 0004; verify with a test that a probe timeout yields unknown availability and offers neither resume nor discard.
- [ ] 3.4 Consult `provider.RunExists` during classification for `ResumeByRunRef` vendors and downgrade to a "start fresh here" offer when the vendor no longer holds the conversation; verify with a test using a stubbed provider that the offered action, not just the command, changes.
- [ ] 3.5 Exclude terminal Sessions from the resumable reading and offer them only a plain shell restart; verify with a test in `core/observation_test.go`.

## 4. Resume transition

- [ ] 4.1 Add a Resume `LifecycleTransition` in `core/lifecycle.go` that writes desired state `running` with mode `resume` for an existing `SessionID` before touching tmux, per ADR 0003; verify with a test that the intent is persisted before any runtime call and that the `SessionID`, name, Project, and `AgentRunRef` are unchanged after it converges.
- [ ] 4.2 Mint and persist a fresh `mgt-` `RuntimeName` as part of the same transition instead of reusing the recorded one; verify with a test that the recorded old runtime name is never addressed and that the new name is persisted atomically with the transition.
- [ ] 4.3 Create the runtime in the Session's recorded working directory and issue the agent kind's resume command there; verify with a fake tmux in `core/lifecycle_test.go` that the created runtime's working directory and command line match the record.
- [ ] 4.4 Verify the recorded working directory still exists and still resolves inside its Project before creating any runtime; verify with tests that a missing directory and a directory outside the Project both fail the resume with the reason stated and create nothing.
- [ ] 4.5 Fail the resume with the vendor's reason, leaving the record intact, when the vendor rejects the recorded conversation between classification and execution; verify with a test using a provider stub whose `RunExists` flips to false.
- [ ] 4.6 Leave the Session's `Outbox` untouched — never flush or replay a queued prompt on resume, per ADR 0003's non-idempotent prompt delivery; verify with a test that a Session with queued messages resumes with those messages still queued and nothing sent.
- [ ] 4.7 Make the transition idempotent under interruption: an interrupted resume that already created a runtime is completed by reconciliation without creating a second one; verify with a test that replays reconciliation against the observed facts.
- [ ] 4.8 Ensure no resume is ever triggered automatically at startup; verify with a test that a startup pass over resumable Sessions creates no runtime.

## 5. Discard

- [ ] 5.1 Add a Discard action in `core/actions.go` that removes the Session record from the Registry and touches neither the working directory, the Worktree, nor vendor conversation storage; verify with a test that all three survive a discard.
- [ ] 5.2 Offer Discard only for a Session whose runtime is observed absent, keeping the existing removal action unchanged for live Sessions; verify with tests that discard is refused for an observed-running Session and for an unknown-availability Session.

## 6. Presentation

- [ ] 6.1 Render the resumable reading with its own icon and label in the TUI list and in `core/overview.go` / `core/sidebar.go` projections, showing the last known status and a relative last-seen time, in the Session's normal Project group; verify with a projection test asserting the rendered fields.
- [ ] 6.2 Offer "Fortsetzen" and "Verwerfen" for resumable Sessions in the TUI and the Wails desktop app, with a fresh-start wording variant for `ResumeFreshOnly` vendors and for the downgraded case from 3.4; verify with tests on the action list produced per reading.
- [ ] 6.3 Assert no user-visible string for a resumable Session claims the process survived: verify with a test scanning the rendered labels, notifications, and action copy for the forbidden claims ("läuft", "wiederhergestellt", "am Leben").

## 7. Documentation and vocabulary

- [ ] 7.1 Add the resumable reading to `CONTEXT.md` under Sessions, in the existing term/`_Avoid_` form; verify the term appears with the words it must not be confused with (dead Session, running Session, Observation).
- [ ] 7.2 Update `README.md` where it lists Session statuses and the `✗ tot` reading, stating plainly that the conversation is resumed and the process is not; verify by reading the section for any surviving claim that work survives a reboot.
