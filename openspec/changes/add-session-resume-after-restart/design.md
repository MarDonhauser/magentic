## Context

See `proposal.md` — Why. Only the current state that shapes the approach is repeated here.

Much of the machinery this change needs already exists:

- `core.Session` durably holds `ID`, `ProjectID`, `Name`, `Dir`, `Worktree`, `SessionKind`, `Vendor`, `AgentRuns`, and `RuntimeName`. Those are exactly the "what is needed to resume it" facts, minus the last known status and its time.
- `AgentProvider.StartCommand(session, run, mode)` already builds a per-vendor start line for `mode == "resume"`: `claude --resume <id>` / `claude --continue`, `codex resume <id>` / `codex resume --last`, `copilot --resume=<id>` / `copilot --continue`, and plain `gemini` because Gemini CLI has no verified resume form. `startCommandForSession` already asks `provider.RunExists` which start form the vendor's own storage accepts.
- `AgentProvider.RunExists(externalID)` already reads the vendor's local conversation storage, so "does the vendor still hold this conversation" is answerable without starting anything.
- Session Lifecycle (ADR 0003) already writes a durable `LifecycleTransition` before touching Git, the filesystem, or tmux, serializes transitions per Session, and reconciles partial execution idempotently.
- Observation collapses an absent tmux runtime to `StatusDead` (`DetectClaudeStatus`, `DetectTermStatus`, `observation.go`), and `StatusDead` is the only reading the UI offers an action for: removal (`x`).

So this change is mostly about *reading* the existing record honestly, not about collecting new state. The genuinely new persistence is two fields, and the genuinely new behavior is one lifecycle transition plus one record deletion.

Constraint from ADR 0001: `SessionID` is the durable identity; `AgentRunRef` is vendor-qualified and is what resume needs. Resume must not mint a new `SessionID` and must not treat the run reference as a Session identity. Constraint from ADR 0004: an unobservable runtime is not an absent runtime.

## Goals / Non-Goals

**Goals:**

- Make "runtime gone, work recoverable" a first-class reading in the domain, distinct from both running and dead, derived rather than stored.
- Reuse the existing per-vendor start-command Adapter as the single resume-command mapping; add only the missing declaration of *whether* a vendor can resume.
- Keep resume a normal `LifecycleTransition` so ADR 0003's reconciliation, locking, and partial-failure handling apply unchanged.
- Persist the minimum needed so that after a cold start, before any Observation pass has run, the developer already sees what each Session was last doing and when.

**Non-Goals (design level, beyond the proposal's):**

- No new storage engine or sidecar file. The resume facts stay in `~/.config/magentic/state.json` next to the Session they describe.
- No adoption of runtimes Magentic did not create. Adoption of hand-made `mgt-*` tmux sessions already exists and is untouched here.
- No change to how `AgentRunRef` is discovered. Vendors that only reveal their run identity after the fact (Codex, Gemini) keep that behavior; resume simply works with whatever reference is on record.
- No batch "resume all". One Session, one deliberate action.

## Decisions

### 1. Resumability is derived, not stored

`Resumable` is computed from the durable record plus the current Observation, exactly like the other status readings. It is not a persisted flag.

*Why:* a stored flag would immediately drift — the working directory can be deleted, the vendor can drop the conversation, the runtime can come back through adoption. ADR 0003's model is "durable intent, observed facts", and a resumability flag is neither. Deriving it also means an existing `state.json` needs no migration to gain the reading.

*Alternative considered:* persist `resumable: true` when a Session is last seen alive. Rejected: it would be a cached answer to a question that is cheap to ask and expensive to get wrong.

### 2. Two new persisted fields, both explicitly optional

`Session` gains `LastStatus AgentStatus` and `LastStatusAt time.Time` (both `omitempty` / `omitzero`, matching the existing `SeenAt`, `DeployAt`, `LaterAt` fields).

*Why:* the record must be able to answer "what was it doing, and when" before the first Observation pass of a fresh process — that is the whole value proposition after a reboot. `SeenAt` already exists but records developer attention, not agent activity, so it cannot serve here.

*Assumption recorded:* `AgentStatus` is currently an `int` iota with `StatusUnknown == 0`, so an absent field deserializes to `StatusUnknown`. That is the correct reading for a pre-existing record and gives a free migration — but it also means the persisted form must be the stable string label, not the ordinal, so that inserting a new status constant later cannot silently rewrite history. Persist as a string, map unknown strings back to `StatusUnknown`.

### 3. The resume-command mapping is the existing `AgentProvider`, extended with one declaration

`AgentProvider` gains `ResumeBehavior() ResumeBehavior` with three values: `ResumeByRunRef`, `ResumeFreshOnly`, `ResumeUnsupported`. Claude Code, Codex and Copilot declare `ResumeByRunRef`; Gemini CLI declares `ResumeFreshOnly` (its comment in `provider.go` already says exactly this: "Gemini CLI has no verified resume form. Starting fresh is the conservative contract"). Nothing today declares `ResumeUnsupported`; the value exists so a future vendor can, and so the UI has an honest thing to render.

*Why:* today "can this vendor resume?" is only implied by whether `StartCommand` produces a resume-shaped line — which is unreadable from outside and produces the wrong UI copy for Gemini (a "Resume" button that silently starts a fresh conversation). Making it explicit is what lets requirement "Per-agent-kind resume behavior is explicit" hold.

*Alternative considered:* a separate `map[AgentVendor]string` resume-command table, as the competitor research describes (cmux stores native resume commands for 14 agents). Rejected: Magentic already has that table, as code, in the Adapters — a second one would be a duplicate that drifts. The declaration is the only missing piece.

### 4. `RunExists` decides the offered wording, before the resume runs

`startCommandForSession` already downgrades `resume` to `new` when `RunExists` is false. That downgrade is correct for starting, but it must not be silent for *resuming*: the developer clicked Resume and would get a fresh conversation without being told.

So resumability classification calls `RunExists` and, when it is false for a `ResumeByRunRef` vendor, the Session is read as resumable-fresh: the action is offered as "start fresh here", not as resume. If `RunExists` flips between classification and execution, the transition fails with the reason stated rather than silently starting fresh (spec scenario "Recorded conversation no longer exists at the vendor").

*Trade-off:* `RunExists` walks vendor storage directories, so classification is no longer purely in-memory. It runs only for Sessions whose runtime is absent, which after a normal working day is a small set, and it is already on the hot path of every session start.

### 5. Resume is a `LifecycleTransition`, and it reuses the provisioning path

A resume records desired state `running` with `mode: resume` for an existing `SessionID`, then follows the existing provisioning steps minus the ones that would be wrong: it does **not** create or resolve a Worktree (the recorded directory is used as-is, and its absence is a failure, not a prompt to re-create), does **not** mint a new `SessionID`, and does **not** replay any queued initial prompt.

The last point matters and follows ADR 0003 directly: prompt delivery is not idempotent, and a Session being resumed after a reboot has, by definition, an unknown delivery state for anything still in its `Outbox`. Resume therefore leaves the Outbox untouched and does not flush it; the developer sees the queued messages and decides.

*Assumption recorded:* the resumed runtime gets a **new** `RuntimeName`, not the recorded one. tmux names are free after a server restart, so reuse would usually work — but ADR 0003 treats `RuntimeName` as the opaque external address, and reusing a name whose runtime Magentic did not create risks addressing someone else's session. Minting a fresh `mgt-` name and persisting it as part of the same transition is the safe form. The Session's display name and identity are unchanged, so the developer sees no difference.

### 6. Discard deletes the record only

Discard removes the Session from the Registry. It is deliberately *not* the existing removal action, which also tears down runtimes and may remove managed Worktrees. Discard touches neither the working directory, nor the Worktree, nor the vendor's conversation storage.

*Why:* after a reboot the working directory usually still holds real, uncommitted work. A discard that deleted the Worktree because the record looked stale would destroy exactly what the feature exists to protect. Discard is offered only when the runtime is observed absent, so it can never race a live runtime.

### 7. Presentation: a fourth thing in the list

Resumable Sessions render with their own icon and label, showing the last known status and a relative last-seen time ("zuletzt: wartete · vor 2 Std."). They keep their place in their Project group rather than moving to a separate "recoverable" area.

*Why:* the developer's mental model is "my four agents", not "my two live agents and my two records". Grouping them out would break the twenty-minutes-of-context argument the feature rests on. Sorting already uses last activity, which stays meaningful.

*Assumption recorded:* copy stays German, consistent with the rest of the UI, and must not use words implying survival of a process ("läuft noch", "wiederhergestellt"). "Fortsetzbar" for the reading, "Fortsetzen" for the action, "Verwerfen" for discard.

## Risks / Trade-offs

- **A resume looks like a rescue and is not.** The developer may expect in-flight work back. → The reading is named for what it is, the last-known status carries an explicit timestamp, and no copy claims process survival. Requirement "Magentic never claims processes survive a restart" is testable against the rendered strings.
- **`RunExists` is a heuristic over vendor storage layouts.** Claude's is verified, Gemini's explicitly is not. A wrong `true` produces a resume command the vendor rejects. → Vendors whose storage is unverified declare `ResumeFreshOnly`, so they never depend on `RunExists` for the offered action; a rejected resume surfaces the vendor's own error rather than being retried.
- **Classification calls the filesystem per absent Session.** On a machine with many stale records, startup does more I/O. → Classification runs inside the existing Observation pass, which is already time-bounded and already distinguishes partial from unavailable readings (ADR 0004); an over-budget classification degrades to unknown, not to a wrong answer.
- **A stale record can point at a directory that has been reused for something else.** Resuming would start an agent in an unrelated checkout. → Resume verifies the recorded directory still resolves inside its Project (the same physical-containment check Specifications already applies) before creating a runtime.
- **`AgentStatus` persisted as a string is a new serialization surface.** A rename of a status label would orphan old records. → The persisted labels are a fixed, explicitly listed mapping independent of the display labels in `Label()`, and unknown strings read back as `StatusUnknown`.

## Migration Plan

1. Ship the two new `Session` fields. Existing `state.json` files parse unchanged; every pre-existing Session reads as "never observed" until the first Observation pass fills the fields in — which happens within one two-second tick of the TUI or the first desktop refresh.
2. Ship the `ResumeBehavior` declaration and the classification. Sessions that were shown as dead after a reboot begin showing as resumable; no state is rewritten.
3. Ship Resume and Discard. The existing removal action stays exactly as it is, so nothing a developer already relies on changes behavior.

Rollback is a plain revert: the two extra fields are ignored by the older binary, and no record is written in a form the older binary cannot read.

## Open Questions

- Should a resumable Session be eligible for Attention (a notification when a record has been resumable for a long time)? It is deferrable: Attention derives its intents from an Observation (ADR 0007), so adding resumable as an input later needs no change to the specs or the record written here.
- How long a resumable record should be kept before Magentic suggests discarding it. No expiry is implemented in this change; adding one later is purely additive.
