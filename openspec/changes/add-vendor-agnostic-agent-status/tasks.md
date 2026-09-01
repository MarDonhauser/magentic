## 1. Manifest format and loader

- [ ] 1.1 Define the manifest types (kind identifier, label, pane-command patterns, per-state rules, detail rules, composer-readiness patterns, declared tail length, observed-version note, unrecorded-screens flag) in `core/agentkind.go`; verify with a unit test that decodes one hand-written YAML manifest into the expected structure
- [ ] 1.2 Implement manifest validation (known states only, patterns compile, kind identifier present and unique per source, tail within the observation scrollback); verify with table-driven tests covering each rejection reason
- [ ] 1.3 Implement loading from the embedded shipped set plus the user manifest directory, with whole-kind override and no partial merging; verify with tests that a user manifest replaces a shipped kind of the same identifier and that an invalid user manifest leaves the shipped one in effect
- [ ] 1.4 Implement the manifest report used by the validation surface (per manifest: kind, source, accepted or stated rejection reason); verify with a test asserting a rejected user manifest and its surviving shipped kind both appear

## 2. Evaluator

- [ ] 2.1 Implement evaluation against the normalized bounded snapshot with the fixed order working → blocked → done → idle and first-match-wins within a state; verify with tests including a snapshot matching both a working and a blocked rule and a snapshot matching both done and idle
- [ ] 2.2 Implement detail extraction for blocked labels and counted working qualifiers, asserting that detail never changes the resolved status; verify with tests covering a recognized label, an unrecognized one, and a background-agent count
- [ ] 2.3 Implement composer-readiness evaluation and the rule that a kind without such patterns is never reported ready; verify with a test on the prompt input state for such a kind
- [ ] 2.4 Enforce the per-snapshot time budget, abandoning an over-budget evaluation into `unknown` for that Session only; verify with a test using a deliberately expensive manifest that the remaining Sessions still resolve
- [ ] 2.5 Add a benchmark for evaluating a full manifest set over a realistic snapshot and assert the per-Session budget holds

## 3. Shipped manifests

- [ ] 3.1 Port Claude Code's rules from `DetectClaudeStatus` and the marker lists in `core/status.go` into `core/agents/claude.yaml`, including background-agent and background-shell working detail and the permission labels; verify by running the existing Claude status tests against the evaluator with the Go path still present
- [ ] 3.2 Record Codex screens from an installed Codex build and write `core/agents/codex.yaml` at first-class fidelity (working, blocked with detail, done, idle, composer readiness, observed version); verify with fixture-based tests, one per state, using the recorded screens
- [ ] 3.3 Port GitHub Copilot's markers into `core/agents/copilot.yaml` at the fidelity its recorded screens support; verify with fixture tests for the states it can prove
- [ ] 3.4 Write `core/agents/gemini.yaml` declaring unrecorded screens and no status rules; verify with a test that a Gemini Session resolves to `unknown` and is refused automated input
- [ ] 3.5 Embed the shipped manifests with `go:embed` and verify with a test that every shipped manifest loads and validates at startup

## 4. Status vocabulary and resolution

- [ ] 4.1 Add `StatusDone` to `AgentStatus` after `StatusTerm` (preserving existing serialized values), with its label and icon; verify with a test asserting the numeric values of the pre-existing members are unchanged
- [ ] 4.2 Derive `done` from the resting screen plus the developer's last-seen instant where a kind declares no done markers; verify with tests for a resting Session seen and not seen since its last activity
- [ ] 4.3 Add the status-source field (hook, snapshot, presence, none) to `SessionObservation`; verify with tests asserting the source for a presence-derived, a snapshot-derived, and an absent-source Session
- [ ] 4.4 Replace `statusForAgentRuntime` and `statusWithoutPaneContent` with the presence-first resolution over the evaluator; verify with the existing `core/status_observation_test.go` and `core/status_semantics_test.go` suites plus new cases for unreadable pane content resolving to `unknown`
- [ ] 4.5 Remove `Status`, `ComposerReady`, and `ScreensRecorded` from `AgentProvider`, delegate `Matches` to the manifests' pane-command patterns, and delete `vendorStatus`, the per-vendor marker slices, and `DetectClaudeStatus`; verify the package builds and the provider tests pass against the manifest-backed behavior
- [ ] 4.6 Update `observationDetail`, `observationAttention`, `observationUnread`, and `promptInputStateFromObservation` for `done` and for the unknown contract; verify with tests that `unknown` maps to unknown attention and that a queued prompt is not delivered into an unknown Session
- [ ] 4.7 Update the desktop frontend's status mapping for `StatusDone` and verify the sidebar and overview render it without falling back to the idle presentation

## 5. Hook-reported status

- [ ] 5.1 Implement the report record and its vocabulary validation (state, instant, vendor, addressing identity, optional detail); verify with tests rejecting an unsupported state and a report missing a required field, each leaving the prior status untouched
- [ ] 5.2 Implement the append-only local event file with owner-only permissions, its size cap and rotation, and truncation after folding into the store; verify with tests for permissions, an oversized file, and no double-application of an already folded report
- [ ] 5.3 Implement correlation to a Session by `SessionID` or `RuntimeName` plus `AgentRunRef` per ADR 0001, discarding reports that resolve to nothing and reports for a replaced occupant; verify with tests for both discard cases and one successful correlation
- [ ] 5.4 Implement the 60-second freshness window, refresh on any report, supersede-by-latest-instant, and discard of out-of-order older reports; verify with tests for a fresh report outliving a quiet stretch, a decayed report falling back to the snapshot, and an out-of-order arrival
- [ ] 5.5 Wire the report store into resolution so a fresh report wins over the evaluator and a stale one does not, and presence still wins over both; verify with tests for hook-versus-snapshot disagreement, staleness fallback, and an absent runtime with a fresh report resolving to `dead`
- [ ] 5.6 Apply hook-reported transitions in the desktop app without waiting for the next observation cycle and assert the sub-second visibility budget in a test

## 6. Claude Code hook integration

- [ ] 6.1 Define the shipped hook definitions mapping `UserPromptSubmit` and `PreToolUse` to working, `Notification` to blocked with its text as detail, `Stop` to done, `SessionEnd` to idle, and `PostToolUse` to a freshness refresh; verify with tests translating each recorded hook payload into the expected report
- [ ] 6.2 Implement idempotent install and uninstall of those definitions that states what it writes before writing and preserves hook definitions Magentic did not write; verify with tests for installing alongside existing hooks, installing twice, and uninstalling back to the original configuration
- [ ] 6.3 Verify end to end in a real Claude Code Session that a blocked transition appears within the sub-second budget and that uninstalling returns the Session to snapshot-inferred status

## 7. Surfaces and documentation

- [ ] 7.1 Add the manifest validation surface (listing each kind, its resolved source, and any rejection reason) to the CLI; verify by running it against a deliberately broken user manifest
- [ ] 7.2 Document the manifest format, the user manifest directory, the shipped kinds and their observed versions, and the hook installation and removal steps in `README.md`; verify by following the written steps to add a manifest for an agent Magentic does not ship
- [ ] 7.3 Run the full test suite plus the observation benchmark and confirm the stated latency budgets hold for a registry with the developer's usual number of Sessions
