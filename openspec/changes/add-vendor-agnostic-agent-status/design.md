## Context

See proposal.md — Why for the motivation. The design-relevant state today:

- `Observe` (`core/observation.go`) asks tmux once for Session presence and once
  per present pane for content, normalizes that content, and calls
  `statusFromObservation` → `statusForAgentRuntime`, which resolves the vendor
  from the pane command and calls `AgentProvider.Status(LastLines(content, 25))`.
- `AgentProvider` (`core/provider.go`) bundles four unrelated concerns behind one
  interface: launch (`StartCommand`, `Binary`, `NewRunID`), run identity
  (`RunExists`), recognition (`Matches`), and screen semantics (`Status`,
  `ComposerReady`, `ScreensRecorded`). Only the last group is vendor screen
  knowledge, and only that group changes here.
- Claude Code's screen semantics live outside the provider entirely, in
  `DetectClaudeStatus` and the regular expressions around it in `core/status.go`
  (`spinnerRe`, `runningPatterns`, `blockedPatterns`, `bgAgentsRe`,
  `agentTreeRe`, `bgShellRe`, `permissionDetails`).
- The TUI polls Observation every two seconds (`observationInterval` in
  `model.go`); the desktop app drives the same `core` Observation.
- `AgentStatus` is an unexported-value `int` enum with German `Label()` and glyph
  `Icon()` methods, serialized into the desktop frontend as a number.
- `promptInputStateFromObservation` already implements the fail-closed rule this
  change generalizes: an unrecognized vendor is `promptInputUnknown`, never
  ready.

Constraints that shape the approach: Observation must not mutate the Registry or
touch Git; a full observation cycle has a six-second budget with a two-second
per-probe timeout; unknown facts must stay unknown (ADR 0004); vendor run
identities are vendor-qualified (ADR 0001); and status is read by the TUI list,
the desktop sidebar and overview, Attention, prompt delivery, and handoff.

## Goals / Non-Goals

**Goals:**

- Move screen semantics out of Go and into data that ships with the binary and
  can be extended by the user, with one deterministic evaluation order.
- Give Observation a second, faster and more certain status source, and a
  precedence rule between the two that is stated once and tested.
- Keep the safety property that a status Magentic cannot prove stays `unknown`
  and blocks automated input.
- Reach Codex parity with Claude Code without regressing Claude Code's detail.

**Non-Goals:**

- Turning `AgentProvider` into data. Launch, resume, and run-identity remain Go
  per vendor; only status semantics move.
- A general rules engine. The manifest format is the smallest thing that
  expresses the four states plus detail, not a scripting surface.
- Changing when Observation runs, how tmux is probed, or the poll intervals.
- Any UI consumption of the new `done` state.

## Decisions

### Manifest format: embedded YAML files, one per kind

Manifests are YAML, one file per agent kind, embedded into the binary with
`go:embed` for the shipped set and read from `~/.config/magentic/agents/*.yaml`
for user manifests. YAML because the repository already parses YAML for
OpenSpec-adjacent tooling and because a user editing detection markers needs
comments; embedding because a shipped manifest must not be a runtime dependency
the user can lose, and because the shipped set must still be a plain file in the
repository that a contributor can diff.

*Alternatives:* JSON (no comments, hostile to hand-editing); keeping Go structs
and only adding a user override layer (leaves two sources of truth for the same
rules, which is the problem); a plugin interface (process boundary and version
skew for what is a list of strings).

*Assumption recorded:* the user manifest directory follows the platform
convention already used for Magentic's own state; if state lives elsewhere on
this machine, the manifest directory sits next to it rather than inventing a
second location.

### Fixed evaluation order in the evaluator, not in the file

The evaluator applies `working` → `blocked` → `done` → `idle` regardless of how
the file is ordered. This is exactly the order `vendorStatus` already encodes
and the order `DetectClaudeStatus` follows, and it is the safe one: an active
agent that also shows an old question on screen must read as working, not as
blocked, or Magentic would notify for input nobody is waiting for. Letting the
manifest choose its own order would make one kind's mistake unreviewable.

### `done` versus `idle` is decided by the evaluator, not by two rule sets

A manifest declares `idle` markers (the resting composer) and optionally `done`
markers. Where a kind has no distinguishable done screen — most of them —
`done` is derived: a Session whose resting screen appeared after the developer
last saw it is `done`, otherwise `idle`. That is the same input
`observationUnread` uses today (`session.SeenAt` versus observed activity), so
the change is a promotion of an existing signal into the status vocabulary
rather than new detection. `Unread` stays for now to avoid breaking the desktop
frontend in this change; it becomes derivable from status and can be removed
later.

*Alternative considered:* requiring every manifest to declare a done screen.
Rejected: most agents do not have one, and the requirement would push kinds
toward invented markers.

### Hook transport: a line-appended local event file, not a socket

Claude Code hooks are shell commands with a JSON payload on stdin. The cheapest
thing they can do reliably, without Magentic running, is append one JSON line to
a per-user event file. Magentic reads new lines at observation time and, in the
desktop app, watches the file so a hook-reported transition is applied without
waiting for the next cycle. A file also survives Magentic not running, which a
socket does not: the hook must never fail because the UI is closed.

This is deliberately *not* the local socket proposed in `add-agent-control-api`.
That surface is bidirectional and command-oriented; this one is append-only,
one-directional telemetry. If the control API lands, the socket becomes a second
accepted ingress into the same report store, which is why the spec fixes the
report vocabulary rather than the transport.

*Assumption recorded:* correlation uses the environment already available inside
the runtime. Magentic starts Claude Code with `--name <TmuxName>` and knows the
`RuntimeName`; the hook reports that plus, where Claude Code exposes it, its
session id as the `AgentRunRef`. A report that resolves to no registered Session
is dropped.

*Hook mapping:* `UserPromptSubmit` and `PreToolUse` → `working`; `Notification`
→ `blocked` with the notification text as detail; `Stop` → `done`;
`SessionEnd` → `idle`. `PostToolUse` refreshes the freshness window without
changing state, which is what keeps a long tool call from decaying to stale.

### Freshness window: 60 seconds, refreshed by any report

A hook report is authoritative for 60 seconds. Long enough that a quiet
`working` stretch between tool calls does not fall back to the snapshot;
short enough that a crashed agent that never emits `Stop` decays into snapshot
inference within one screenful of the developer's attention rather than lying
indefinitely. The window is a constant with a test, not a setting: a
user-tunable window would produce bug reports nobody can reproduce.

### Evaluation stays inside the existing cycle

Manifest evaluation is pure string and regular-expression work over a bounded
tail, executed where `provider.Status` is called today. No new probe, no new
timeout, no goroutine. The per-snapshot budget (a low single-digit millisecond
ceiling, enforced by the evaluator abandoning a snapshot that exceeds it) exists
because user manifests can contain regular expressions Magentic never reviewed;
a catastrophic pattern must cost one Session's status for one cycle, not the
cycle.

### Where the code lands

- `core/agentkind.go` — manifest types, loading, validation, source precedence.
- `core/agentkind_eval.go` — evaluation against a normalized snapshot.
- `core/agents/*.yaml` — shipped manifests, embedded.
- `core/hookreport.go` — the report store, freshness, correlation.
- `core/observation.go` — resolution precedence and the new status source field.
- `core/provider.go` — `Status`, `ComposerReady`, `ScreensRecorded` removed from
  `AgentProvider`; `Matches` delegates to the manifest's pane-command patterns
  so recognition has one source too.
- `core/status.go` — `AgentStatus` gains `StatusDone`; the Claude-specific
  regular expressions and marker lists are deleted once their content lives in
  `core/agents/claude.yaml`.

## Risks / Trade-offs

- **A user manifest silently makes detection worse than the shipped one.** →
  Override is all-or-nothing per kind and the validation command reports which
  source each kind resolved from, so "why does my Claude status look wrong" has
  a one-command answer.
- **Regular expressions from a user file run inside the observation cycle.** →
  Bounded tail, per-snapshot time budget, abandonment yields `unknown` for that
  Session only. No backreference-capable engine is involved (Go's RE2 has no
  catastrophic backtracking), so the budget is a belt over an existing brace.
- **The event file grows without bound.** → It is truncated on read once its
  reports are folded into the in-memory store, and it is capped; a file larger
  than the cap is rotated rather than parsed.
- **Hook reports could be forged by any local process.** → Everything the hook
  channel can do, a local process running as the user can already do by typing
  into the tmux pane. Owner-only permissions on the file are the boundary; there
  is no privilege here to escalate.
- **`done` changes what existing consumers see.** → Every consumer that
  currently switches on `StatusIdle` must be visited; the migration below makes
  that a compile error rather than a silent behavior change.
- **Detection lag is not removed for hookless agents.** → It is bounded and
  stated (one cycle plus evaluation) rather than eliminated. Codex gaining hooks
  later needs configuration, not architecture.
- **Two ingress paths if the control API lands.** → The report vocabulary is
  specified independently of transport for exactly this reason.

## Migration Plan

1. Introduce manifests and the evaluator behind the existing
   `AgentProvider.Status` signature; port Claude Code's rules into
   `core/agents/claude.yaml` and prove equivalence against the existing status
   tests before deleting any Go marker list.
2. Port Codex, Copilot, and the unrecorded Gemini declaration; delete
   `vendorStatus` and the per-vendor marker slices.
3. Switch `statusForAgentRuntime` to the evaluator, remove `Status` and
   `ComposerReady` from `AgentProvider`, delete `DetectClaudeStatus`.
4. Add `StatusDone` and the status-source field; visit every consumer switch.
   `AgentStatus` is serialized as an integer to the desktop frontend, so
   `StatusDone` is appended after `StatusTerm` rather than inserted, and the
   frontend's mapping is updated in the same step.
5. Add the hook report store and resolution precedence with hooks absent
   (everything falls through to snapshot inference — this step must be a no-op
   for existing behavior).
6. Add the Claude Code hook installation command and the user manifest
   directory with its validation command.

Rollback: each step is independently revertable, and steps 1–4 leave no new
runtime surface. Reverting step 5 or 6 leaves Sessions on snapshot inference,
which is today's behavior.

## Open Questions

- Whether the shipped manifests should also declare the Session-start command,
  eventually collapsing `AgentProvider` into data. Deferred: it does not change
  this change's specs, approach, or tasks.
- Whether `Unread` is removed once `done` exists. Deferred to the UI change that
  consumes the roll-up.
