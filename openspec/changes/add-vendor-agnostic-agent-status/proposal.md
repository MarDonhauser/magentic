## Why

Semantic Session status — working, blocked on the developer, done, idle — is the
one thing that makes Magentic more than a tmux session list, and today it is
built around Claude Code. `DetectClaudeStatus` in `core/status.go` owns the
regular expressions, the running and blocked marker lists, the background-agent
and background-shell counting, and the German labels; every other vendor gets a
thinner copy of the same idea inside `core/provider.go` (`vendorStatus` plus
three hard-coded marker slices per vendor). Adding a vendor means editing Go,
recompiling, and hoping the markers were transcribed correctly, and a vendor
nobody transcribed — Gemini CLI today — returns `StatusUnknown` forever.

Competitor research puts this at the center. herdr's sidebar rolls blocked ·
working · done · idle up across every agent it manages, and that roll-up is the
capability its users praise most. herdr gets there by evaluating a declarative
per-agent *manifest* against a terminal snapshot, which is portable across
vendors but pays for it with a documented two-to-three second lag whenever the
agent offers no lifecycle hooks. Magentic can do both: keep snapshot matching as
the portable baseline for any agent, and take the faster, more certain answer
from a vendor that reports its own lifecycle. Claude Code ships exactly such a
mechanism in its hooks, so the vendor that dominates the current detection code
becomes the first vendor that no longer needs to be guessed at.

PRODUCT.md already commits to agent-agnostic orchestration, and the status path
is the last place where the product boundary is still one vendor's terminal UI.

## What Changes

- **Declarative detection manifests as the portable baseline.** A manifest
  describes one *agent kind*: how its pane command is recognized, and which
  patterns on the pane snapshot mean working, blocked, done, or idle, together
  with optional detail extraction (the permission label, the background-agent
  and background-shell counts Claude Code reports today). Matching order inside
  a manifest is fixed by the format, not by each vendor's Go code, so two agent
  kinds cannot disagree about precedence.
- **Shipped defaults plus user extension.** Magentic ships manifests for the
  agent kinds it supports. A user may add a manifest for an unsupported agent
  or override a shipped one from a user configuration directory, without
  building Magentic. A manifest that fails validation is rejected with a stated
  reason and the shipped default keeps working; a malformed user file never
  degrades an agent kind into a wrong answer.
- **A hook-reported status channel.** Where a vendor emits lifecycle events,
  Magentic accepts them on a local channel and treats the reported state as the
  authoritative status for that Session until it goes stale. Claude Code is the
  first implementation, driven by its hooks; the channel itself is vendor-neutral
  so a second vendor needs configuration, not new transport.
- **Explicit precedence.** A fresh hook report wins over the snapshot inference.
  A stale hook report does not: it falls back to the snapshot. Where neither a
  hook report nor a manifest match is available, status is **unknown** — and per
  ADR 0004, unknown is never rendered, counted, or acted on as idle, done, or
  dead. Prompt delivery keeps its current fail-closed behavior on unknown.
- **Stated latency expectations.** Snapshot-inferred transitions are visible
  within one observation cycle plus one evaluation, and the current cycle
  (two seconds in the TUI) is the contract; hook-reported transitions are
  visible in well under a second and do not wait for the next cycle. Both are
  measurable, so a regression is a test failure rather than a feeling.
- **Codex as the second first-class agent kind.** Codex gets a full manifest at
  the same fidelity as Claude Code — working, blocked with a detail, done, idle,
  composer readiness — recorded from an observed Codex build rather than
  guessed. Gemini CLI and GitHub Copilot keep their current status: whatever
  their manifests can prove, and unknown otherwise.
- **A `done` status distinct from `idle`.** Today a Session that has just
  finished a turn and one that has been sitting untouched for an hour both read
  `StatusIdle`, and the difference is smuggled into the separate `Unread` flag.
  The status vocabulary gains *done* (the agent finished work the developer has
  not looked at) alongside *idle* (at rest, nothing pending), so the roll-up the
  UI will eventually show can be derived from status alone.

Non-goals: the UI surfaces that consume the status (sidebar roll-up, badges,
banners, notification copy) — Attention already turns status into intents and
that mapping is where UI work belongs; remote or networked status transport; and
any new agent-launch flow. Adding a vendor to the *launch* path
(`AgentProvider.StartCommand`, run identity, resume) stays out of scope: this
change moves only the status half of the Adapter into data.

## Capabilities

### New Capabilities
- `agent-status/detection-manifests`: the manifest format, its status
  vocabulary and matching semantics, where shipped and user manifests live,
  their precedence, and how an invalid manifest is handled.
- `agent-status/hook-reported-status`: the local channel by which a vendor
  reports its own lifecycle, the event vocabulary, Session correlation under
  stable identities, freshness, and Claude Code's hook installation.
- `agent-status/status-resolution`: how one Session status is resolved from
  hook reports, manifest inference, and runtime presence; the explicit unknown
  contract; and the detection latency budget.
- `agent-status/agent-kinds`: which agent kinds Magentic ships, what
  first-class support means for Claude Code and Codex, and how a kind whose
  screens were never recorded behaves.

### Modified Capabilities
<!-- None. openspec/specs/ currently holds no published capabilities, so every
     spec in this change is new. Session Observation, Attention, and Registry
     behavior is reused rather than respecified; where this change alters what
     Observation reports, that is stated inside the new capabilities above. -->

## Impact

- **Replaced logic**: `DetectClaudeStatus` and the per-vendor marker slices in
  `core/provider.go` (`claudeProvider.Status`, `codexProvider.Status`,
  `copilotProvider.Status`, `geminiProvider.Status`, `vendorStatus`) stop being
  the source of truth. `AgentProvider` keeps launch, run identity, and binary
  concerns; status moves behind a manifest evaluator.
- **Extended domain vocabulary**: `AgentStatus` gains a *done* member and
  `Observation` carries the source of its status (hook, snapshot, or none) so a
  consumer can tell a confirmed state from an inferred one. `AttentionState`
  mapping in `core/observation.go` follows.
- **New surface**: a manifest loader and evaluator in `core/`, a manifest
  directory shipped with the binary plus a user-level override directory, and a
  local receiver for hook reports that Observation reads at snapshot time.
- **Touched behavior**: `statusForAgentRuntime`, `statusWithoutPaneContent`,
  `observationDetail`, `observationAttention`, and `observationUnread` all
  change shape; `promptInputStateFromObservation` gains a *done* case and keeps
  its fail-closed unknown handling. `ScreensRecorded` becomes a manifest fact.
- **Blast radius**: status is read by the TUI list, the desktop sidebar and
  overview, Attention (notifications, Dock badge), prompt delivery, and the
  handoff flows. A wrong *idle* would let Magentic type into a dialog, so the
  unknown contract is a safety property, not cosmetics. Existing status tests
  (`core/status_semantics_test.go`, `core/provider_status_test.go`,
  `core/status_observation_test.go`, `status_test.go`) become the regression
  net for the manifest evaluator.
