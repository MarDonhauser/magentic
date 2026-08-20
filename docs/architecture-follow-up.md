# Architecture follow-up

Work stopped on 2026-08-20 at commit `a7455c0`. The worktree was clean when
this hand-off was written. All architecture subagents were stopped.

## Current baseline

The main deep-module cutovers are already in place:

- Registry owns stable `ProjectID` and `SessionID` identity and semantic,
  process-coordinated changes.
- Session Lifecycle owns durable desired state, Project → Worktree → Session →
  Runtime transition coordination, fail-closed RuntimeName availability, and
  exact RuntimeName validation.
- Observation owns runtime meaning and represents unavailable or partial facts
  explicitly.
- Repositories owns Git topology, status, history, identity, worktree changes,
  and strict parsing of successful-but-malformed Git output.
- WorkHistory normalizes Claude Code, Codex, Gemini CLI, and GitHub Copilot once
  for timeline, search, links, and statistics.
- Specifications owns Board discovery, lifecycle meaning, physical Project
  containment, stable references, and opaque start tokens.
- Attention owns notification, Dock badge, native attention, break, and
  deployment policy.
- Desktop/TUI actions use stable IDs; Wails bindings were regenerated and the
  old `Projects()` export was removed.
- Callerless mutable-State, legacy Git/status/Overview, raw tmux fact, and raw
  prompt adapter APIs were deleted.

Relevant checkpoint commits, newest first:

- `a7455c0` — RuntimeName availability, exact identity, runtime transition
  coordination, generated Wails contract.
- `33d12a9` — strict runtime identity, prompt-observer injection, Git parser
  hardening, dead prompt adapter deletion.
- `e2ce5a1` — provider-qualified WorkHistory attribution.
- `8002bb2`, `112f4cd`, `9aefc8a` — Registry/Lifecycle, Worktree,
  Specifications, Observation, and stable-ID cutovers.

## Must do before calling the architecture pass complete

### 1. Keep `SessionID` through asynchronous prompt delivery

Severity: **high**.

The desktop and public core APIs resolve a stable `SessionID`, but the prompt
queue currently converts it to a raw RuntimeName before the asynchronous seam.
The affected implementation is in `core/actions.go`; Handoff ends at the same
seam in `core/handoff.go`, and Lifecycle initial-prompt delivery calls it from
`core/lifecycle.go`.

Current unsafe sequence:

1. Session A queues a prompt while its agent is busy.
2. A is renamed or removed before the prompt becomes deliverable.
3. Session B is provisioned with A's former RuntimeName.
4. Queue revalidation observes B by RuntimeName and may send A's prompt to B.

Required design:

- Introduce an internal prompt target whose authority is `SessionID`, not a
  RuntimeName string.
- Key pending deliveries by stable Session identity plus delivery semantics.
- Before every readiness observation, resolve the current Session from a fresh
  Registry snapshot by `SessionID`. Abort if it was removed or its identity is
  otherwise unavailable.
- For the final delivery phase, acquire the process-coordinated RuntimeName
  transition lock, re-resolve the Session again, then keep that lock across the
  final observation, literal send, optional pre-Enter observation, and Enter.
  Do not hold the lock during the long readiness polling loop.
- If A was renamed, either follow A's newly resolved RuntimeName or fail
  explicitly. Never fall back to the old RuntimeName and never deliver to B.
- Migrate `SendSkillByIDWithObserver`, Handoff, and Lifecycle initial-prompt
  delivery to the stable target. Preserve the injected Observation reader used
  by app tests; production deadlines must remain unchanged.
- Remove or privatize the remaining runtime-only prompt path, including
  `observePromptTarget(runtimeName, ...)`, once no legitimate caller remains.

Required regression:

- Use barriers to queue a prompt for A, pause before final delivery, rename or
  remove A, provision B at A's old RuntimeName, then release the queue.
- Assert that no literal or Enter is sent to B. If the chosen contract follows
  a renamed A, assert that both sends target A's new exact RuntimeName.
- Cover SendSkill, Handoff, and Lifecycle initial-prompt call paths.
- Run the focused tests repeatedly and under `-race`.

### 2. Make portable lock cleanup generation-safe

Severity: **high**.

`core/portable_lock.go` still mutates a reusable directory path after a
read/check step:

- Failed owner publication performs unconditional `os.RemoveAll(lockDir)`.
- Release checks the owner nonce, then removes `owner` and `lockDir` by path.
- Stale recovery verifies one generation, then renames a reusable path to a
  randomly named tombstone.

An interleaving can therefore let owner A pause, let B recover and acquire a
new generation at the same path, and then let A delete or rename B's live lock.
Two delayed stale recoverers can produce the same problem.

Required design:

- Redesign cleanup/release/recovery around an exact directory generation or
  unique ownership claim. No operation from generation A may delete, rename,
  overwrite, or heartbeat generation B after the canonical path is reused.
- Do not fix this with sleeps, a wider stale interval, or another
  check-then-delete sequence.
- Preserve atomic owner publication, recovery after a real crash, heartbeat
  protection for a long live owner, cancellation, and portability.
- A platform-specific implementation is acceptable if the common Interface
  preserves the same semantics and both Windows and Unix implementations have
  focused tests.

Required regressions:

- Force A to pause during failed publication; let B recover and acquire; resume
  A's cleanup; assert B remains the only owner.
- Force A to pause between release validation and cleanup; let B acquire; resume
  A; assert B survives.
- Race two stale recoverers against a successor and prove the successor is not
  renamed or deleted.
- Retain the existing long-owner, ownerless-crash, malformed-owner, and atomic
  publication tests.

### 3. Run the final architecture/deletion gate

Only do this after the two high-severity items above are complete.

- Re-run the deep-module deletion scan. At minimum, these must stay absent:

  ```sh
  rg -n 'TmuxHasSession|SendPromptWhenReady|SendPromptToSession|StartBoardSession|CollectStatuses|CollectGitInfo|CollectSessionChanges|LegacyObservationProjection|State\.Save' .
  ```

- Confirm prompt delivery has no RuntimeName-only queue or action entry point.
- Confirm generated Wails files contain no `Projects()` export and include
  `MigrateDockSessions`, `DockSessionRef`, `GraphBranch.mergedKnown`,
  `SessionLinksResult`, `SessionPreviewResult`, and Stats coverage/cost fields.
- Do not hand-add `SpecificationStartIntent` to `models.ts`: it is intentionally
  Go-internal and unreachable from the Wails facade, so the generator correctly
  omits it.
- Re-read `CONTEXT.md` and `docs/adr/` against the final Interfaces and update
  only durable decisions, not implementation chronology.
- Ask for one final read-only architecture gate against the settled tree. Fix
  any remaining correctness or deletion-test blocker before declaring done.

## Verification state at hand-off

These commands passed against `a7455c0` while writing this file:

```text
go test ./...                         PASS
(cd app && go test ./...)             PASS
(cd app/frontend && npm test)         PASS, 16/16
git status                            clean before this document
```

The final implementation still needs the complete release matrix:

```sh
go test ./...
go test -race ./...
go vet ./...

(cd app && go test ./...)
(cd app && go test -race ./...)
(cd app && go vet ./...)

(cd app/frontend && npm test)
(cd app/frontend && npm run build)

git diff --check
test -z "$(gofmt -l $(rg --files -g '*.go'))"
```

Also compile `./core` with CGO disabled for Linux and Windows, writing the test
binaries to explicit temporary files and removing them afterwards. The known
Vite warning for a generated chunk slightly above 500 KiB is not an
architecture failure, but any new warning or generated-contract drift is.

## Optional cleanup after correctness

These are migration/naming debts, not reasons to delay the two safety fixes:

- `type Agent = Session` and helpers such as `AgentByName`, `AgentsFor`, and
  `HasAgent` still preserve historical source vocabulary. Remove them only as a
  deliberate mechanical cutover to `Session`; do not mix that rename into the
  prompt or lock fixes.
- Legacy JSON fields such as `Session.Kind` and the Claude-only
  `Session.SessionID` remain read-side migration inputs. Keep their conversion
  centralized in Registry/AgentRun migration until the supported migration
  window is explicitly closed.

