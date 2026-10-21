## Context

See proposal.md — Why for the motivation. The design-relevant state today:

- `AgentProvider` (`core/provider.go:12-49`) bundles launch, run identity, recognition and runtimes behind one interface, with four implementations. Only `claudeProvider` returns a normalizer (`:246-248`); `codexProvider:280`, `copilotProvider:330` and `antigravityProvider:382` return `(nil, false)`. Only `claudeProvider.Runtimes()` declares `RuntimeManaged` (`:219`).
- The managed (headless) runtime from `replace-tmux-agent-runtime` is largely built: `core/agenthost.go` (646 lines, with `ClaudeManagedArgv` at `:602-625` and a verified-version gate at `:630-646`), `core/agenthost_process.go`, `core/managed_protocol.go`, `core/managed_turn.go`, `core/observation_managed.go`, `core/control_managed.go`. 32 of its 49 tasks are done. This change substitutes a protocol into that scaffolding rather than building a second one.
- Status detection is already data-driven: `core/agentkind.go` (526 lines) loads embedded manifests from `core/agents/*.yaml` plus user overrides, and `core/agentkind_eval.go` evaluates them against a pane snapshot. `resolveSessionStatus` (`core/observation.go:647-683`) orders presence → term → shell → fresh hook report → manifest → unknown. Recognition and status share the manifests: `AgentProvider.Matches` delegates to `paneCommandMatchesKind` (`core/provider.go:206-210`).
- Prompt delivery is unacknowledged: `sendPromptLiteralValidated` (`core/actions.go:654-701`) sends `tmux send-keys -l` plus a separate `Enter`, and the only delivery marker is `AttemptedAt` on the queued message (`core/state.go:110-123`).
- `AgentRunRef` is `{Vendor, ExternalID}` (`core/state.go:67-70`); `Session.SessionVendor()` defaults to Claude (`:357-365`) and `SessionRuntime()` defaults to tmux (`:370-375`).
- Five history adapters read five on-disk formats (`core/workhistory_adapters.go`, 1056 lines); the pricing table in `core/stats.go:136-146` is Claude-only.

**Exercised against the omp build installed on this machine (`omp/18.2.8`)**, by driving `omp --mode rpc-ui --no-session` directly. These facts were observed, not read:

- The first frame out is `{"type":"ready","protocolVersion":1,"supportedProtocolVersions":[1,2],"maxFrameBytes":1048576,"maxReassembledFrameBytes":67108864}`.
- `get_state` answers with `sessionId`, `model` (id, name, api, provider, baseUrl), `thinkingLevel`, `fastModeActive`, `fastModeEnabled`, `contextUsage` (tokens, contextWindow, percent), `isStreaming`, `isCompacting`, `messageCount`, `queuedMessageCount`, `steeringMode`, `followUpMode`, `interruptMode`, `autoCompactionEnabled`, `todoPhases`, `tokensPerSecond`, `systemPrompt`, `dumpTools`.
- **`get_state` does not report the approval mode.** Checked explicitly, with and without `--approval-mode always-ask`; the key set is identical in both cases. It is the single most consequential fact in this design.
- `extension_ui_request` frames arrive unsolicited, and an `available_commands_update` frame enumerates the session's slash commands.
- `--approval-mode always-ask|write|yolo` exists as a per-session flag whose help text describes it as overriding `tools.approvalMode` for that run. `--profile`, `--from-claude`, `--from-codex`, `--thinking`, `--resume`, `--session-dir`, `--add-dir`, `--auto-approve` and `--max-time` all exist as flags.

**Exercised by the task 2.11 spike (`omp/18.2.8`, `--approval-mode always-ask --no-session` in a temporary directory, local `ollama` model).** Recorded frames are in `core/testdata/omp/`. Confirmed as documented:

- The lifecycle events `agent_start`, `turn_start`, `turn_end` (carrying `message` and `toolResults`), `message_start`/`message_update`/`message_end` and `tool_execution_start`/`_end`. Every assistant message carries `provider`, `model`, `api` and `usage` including `cost`, so the serving model and its cost are per-message facts.
- `agent_end` carries `isTerminal`; it was `true` in every observed case.
- An abort during a streaming turn is reported as `message_end` and `turn_end` with `stopReason: "aborted"`, then `agent_end` with `isTerminal: true`, before the `abort` response arrives.
- The commands `get_available_models`, `set_model`, `cycle_model`, `set_thinking_level`, `set_fast_mode`, `compact`, `set_steering_mode`, `set_interrupt_mode`, `steer`, `abort`, `get_session_stats` and `login` answer with a `response` frame carrying the request `id`; interleaved requests were answered on their own `id`. `set_model` answers `success: false` with a stated error for an unknown model, and `set_fast_mode` does so for a model without fast mode.
- `get_available_models` returns each model with its `provider` and per-token `cost`, which gives the pricing open question below a source.

**Contradicted or changed by the spike.** The specs and tasks resting on these must be amended before their sections are built:

- **Approval requests arrive as `extension_ui_request` with `method: "select"`, not `confirm`.** The frame carries `title` (`"Allow tool: write\nPath: …"`) and `options: ["Approve","Deny"]`, and is answered with `extension_ui_response` carrying `value: "Deny"` or `"Approve"`. A denial surfaces as `tool_execution_end` with `isError: true`.
- **`always-ask` does not ask for every tool.** omp's approval resolution (`src/tools/approval.ts`) lets `always-ask` auto-allow the read tier, which the spike confirmed: `read` and `glob` ran without a request, `write` and `bash` asked. Worse, a per-tool `tools.approval.<tool>: allow` in the developer's shared omp configuration, or a tool that declares its own `allow` policy, is honored in every mode, `always-ask` included. The flag therefore proves "writes and execution ask unless configuration or the tool says otherwise", not "every tool asks". The approval-gate spec has to state that scope, and the behavioral verification only proves the tools it exercises.
- **There is no `prompt_result` frame.** A prompt resolved locally (`/context`) answers its `prompt` response with `data: {"agentInvoked": false}` and emits `command_output`; no agent event follows. A prompt that reaches the agent answers with `success: true` and no `data`.
- **A `prompt` response is not a delivery acknowledgement on its own.** A `prompt` sent while a turn is streaming is answered twice on the same `id`: first `success: true`, then `success: false` with "Agent is already processing". Correlation has to accept more than one response per `id`, and delivery has to be judged by the final one or by the user `message_start` echo. A host should use `steer` or `follow_up` while `isStreaming` is true.
- **`set_thinking_level` clamps silently.** `xhigh` was answered `success: true` and `get_state` then reported `high`. The value has to be read back rather than taken from the response, which task 5.3 already requires.
- **`model_changed` carries no payload.** The new model has to be read from the command response or from `get_state`.
- **`compact` during a streaming turn is neither queued nor refused.** It ran for about 80 seconds, stalled the stream, and returned a summary. The turn that was streaming then emitted no `turn_end` or `agent_end` after a subsequent `abort`. Magentic should refuse compaction while `isStreaming` is true rather than rely on omp's behavior (task 5.6).
- **`login` for an API-key provider does not reject secret input.** It asks the host for the key through `extension_ui_request` `open_url` followed by `input` ("Paste your Z.AI API key"). Magentic must cancel that request and never collect or relay the key; cancelling yields `success: false`, `"API key is required"`. Task 2.10 has to detect the `input` request rather than wait for a rejection.

**Exercised end to end after sections 1–3 were built** (isolated `MAGENTIC_STATE`, `magentic serve`, `session start --vendor omp --model ollama/qwen3.5:9b`):

- The behavioral gate verification passed against `omp/18.2.8` under the `magentic` profile: write and bash each raised an approval request and were denied. The first start of a build takes about 80 seconds for it; later starts reuse the cached result and take about 8 seconds.
- `set_model` persists the chosen model as the profile's default, so a model switch in one Session changes what every later Session under `magentic` starts with. Section 5 has to decide whether a Session's model change may do that, or whether Magentic passes `--model` for every start and treats the profile default as irrelevant.
- The agent host outlives `magentic serve` being terminated, and a restarted daemon reclaims it by token and delivers to it.
- A prompt counted as delivered only on omp's echo; a write request put the Session into `waiting-decision` from the omp protocol; a denial through `session answer-permission` left no file behind and the turn ended as done.
- The CLI offers no way to learn an open request's identity, so `answer-permission` could only be used by reading the host socket directly. That gap predates this change and belongs to task 5.7.

**Still not exercised:** the `subagent_*` frames, `tool_execution_update`, `agent_end` with `isTerminal: false`, `set_follow_up_mode`, `set_auto_compaction` and `switch_session`.

Constraints that shape the approach: unknown facts stay unknown and never render as idle, done or dead (ADR 0004); run identities stay vendor-qualified (ADR 0001); durable intent is recorded before any runtime is touched (ADR 0003); local agent history is normalized once (ADR 0005); attention is planned before side effects (ADR 0007).

## Goals / Non-Goals

**Goals:**

- Substitute one protocol for four vendor integrations without building a second host, a second normalization model, or a second permission store.
- Make the approval gate provable under a protocol that does not report it, and make the proof fail closed.
- Keep every record already on disk readable after the vendor that wrote it stops being launchable.
- Give Magentic's own interfaces the session controls the terminal used to own, without reimplementing omp's interface.

**Non-Goals:**

- A vendor abstraction that survives this change. The point is to stop having one; `AgentProvider`'s per-vendor shape is removed rather than generalized.
- Wrapping omp's full command vocabulary. Only the commands behind a specified requirement are driven.
- Pinning omp's version. Pinning a daily-shipping dependency trades one failure mode for another; the design detects incompatibility instead.

## Decisions

### The transport is `omp --mode rpc-ui`, not ACP

ACP is the more conservative target: it is an external versioned protocol (`PROTOCOL_VERSION = 1`) rather than omp's own, it carries a typed `session/request_permission` gate, and it has session identity, resume and fork in-protocol. On stability alone it wins.

It loses on the requirement that decided this change's shape. The session controls — thinking level, fast mode, model cycling, steering, follow-up and interrupt modes, compaction — are omp's own commands and have no ACP equivalent. Building on ACP would put exactly the surface Magentic wants to own back inside omp's terminal, which is the situation this change exists to end.

`rpc-ui` rather than plain `rpc`: the UI-extension frames are how a host learns that omp is asking something, and the approval gate depends on receiving them.

*Alternative considered:* ACP for lifecycle and permissions, RPC for controls, over two connections. Rejected — two protocols against one process means two identity stories, two failure modes, and a reconciliation problem for turn boundaries, to avoid a risk that pinning and detection already address.

*Consequence recorded:* Magentic takes a dependency on a vocabulary with no published compatibility policy. The `ready` frame versions the framing, not the schema. This is paid for in the handshake and verification decisions below, and named in Risks.

### The approval gate is proven by launch provenance, and the mechanism is verified behaviorally

`get_state` does not report the approval mode, so the obvious design — start the session, ask it whether the gate is on, refuse if not — is not available. Two weaker proofs are.

The first is provenance: Magentic controls the argument list, so a process Magentic started with `--approval-mode always-ask` has the gate on, and `--approval-mode` is documented to override configuration rather than be overridden by it. Provenance is only as good as process identity, which `replace-tmux-agent-runtime` already solved with a recorded socket path plus a handshake token; the recorded launch rides along with that record, so a reclaimed process carries its own provenance and an unconfirmed process carries none.

The second guards against the first silently rotting. If a future omp renamed or dropped the flag, Magentic would keep passing it, keep believing the gate was on, and simply never see an approval request — the failure is invisible in exactly the direction that matters. So the daemon verifies behaviorally, once per omp build: start a throwaway session in a scratch directory with the real launch arguments, ask for an operation that requires approval, and confirm an approval request arrives. No verification, no omp Sessions.

*Alternative considered:* pass the mode through a `--config` overlay instead of the flag. Rejected — an overlay is a file, a file is a surface, and the failure mode of a config that stops being read is the same invisible one, without the flag's documented precedence.

*Alternative considered:* infer the gate from traffic, by noticing that tool executions are completing without approval requests. Rejected as a primary proof: it can only detect the failure after unapproved tools have already run, which is the thing being prevented. It is worth keeping as a secondary alarm, which is why the design records it as an open question rather than a requirement.

*Assumption recorded:* the scratch directory for verification is a Magentic-owned temporary directory, never a Project or worktree, and the throwaway session runs ephemeral.

### omp is the vendor; the model provider is an attribute of a turn

One agent fronting sixty providers makes *vendor* ambiguous, and ADR 0001 requires run identities to be vendor-qualified. Resolving it the other way — treating `anthropic` or `openai` as the vendor — would break that ADR, because the run identity, the session record and the conversation all belong to omp, not to whoever served a token.

So `AgentVendor` becomes `omp`, qualified by omp's own `sessionId` from `get_state`, and the serving model and provider become facts carried by the Item. A Conversation whose model changed partway through carries both, which is the case that proves the model cannot be a session-level property.

*Consequence:* the published `agent-timeline/item-model` spec needs its vendor definition sharpened, which is why this change carries a delta against it rather than quietly reinterpreting it.

### The vendor badge becomes a model badge, and the identity axis moves with it

The interfaces key on a tool identity derived from the tmux pane command (`DetectAgentTool`, `core/status.go:160-170`), and it surfaces everywhere: a brand badge on every session row and in the session bar, a segmented vendor switch, a "Harness wählen" creation menu, a default-vendor preference, a "Zu X wechseln" action, a per-vendor label table in the Verlauf sidebar. None of that survives contact with one agent fronting sixty providers — not because the machinery breaks, but because it would render the same badge on every Session and convey nothing.

The replacement is not to delete the axis but to move it. What a developer actually wants to see at a glance is which model is doing the work, and the brand iconography already in `avatar.js` keeps meaning something under that reading: the sunburst stops meaning "this Session runs Claude Code" and starts meaning "this turn is served by Anthropic". The visual language survives; what it denotes changes.

Two consequences fall out and both are specified rather than left to the implementer. The identity becomes **mutable within a Session**, because a developer can switch models mid-run, so every surface showing it has to follow rather than cache what was chosen at creation. And it becomes **absent before it is known**, because a Session that has not reported its model yet has no identity to draw — which under ADR 0004 must render as unknown, not as a default badge.

*Alternative considered:* show `omp` as the identity, with its own icon. Rejected — it is the same badge on every row, which is the definition of a useless distinction, and it would hide the one fact that varies.

*Alternative considered:* keep a vendor picker at creation that selects among providers rather than agents. Rejected as a separate axis: the provider is implied by the model, and offering both invites picking a model its provider cannot serve.

### The existing agent host is reused; the vendor protocol layer is swapped

`core/agenthost.go` already owns a process, survives interfaces closing, reclaims by identity and stores permission requests. What is Claude-specific in it is the argument list (`ClaudeManagedArgv`), the frame vocabulary in `core/managed_protocol.go`, and the version gate. Those are replaced; the ownership, reclaim, socket and permission-store machinery is not.

This is also why the change depends on `replace-tmux-agent-runtime` rather than competing with it: its remaining 17 tasks are the foundation, and duplicating them under a different name would leave two hosts to keep correct.

*Alternative considered:* keep the managed Claude runtime alongside omp, so Claude Code stays reachable natively. Rejected for this change's scope — the user asked for one runtime, and two headless runtimes means two permission paths, two normalizers and two status sources for the same vendor. The Claude-specific host code is removed, not left dormant.

### Retirement splits launch from read

`hookinstall.go`, the Claude status reporter, `ClaudeManagedArgv`, `SwitchVendor`, the per-vendor handoff storage hints and the coding-agent half of the pane manifests exist to *drive* a vendor, and go.

The five history adapters and Claude's conversation normalizer exist to *read* records already written, and stay. Deleting them would delete a developer's statistics and past Conversations as a side effect of changing how new Sessions start, which is not a trade anyone agreed to. They are re-declared as legacy readers: consulted for records at rest, never for a live Session, never written to.

The pane manifests are the awkward case, because recognition and status share them (`core/provider.go:206-210`). They keep their `pane_commands` role for terminal Sessions and lose their status rules for coding agents, rather than being deleted.

*Alternative considered:* delete the history adapters and backfill everything through omp's `--from-claude` import. Rejected — import is per-session and developer-initiated, so a bulk history backfill would be a migration with no rollback over data Magentic did not write.

### The protocol connection is the only way in; omp's files are never read

omp's session records are JSONL under its agent directory, documented as internal, and their bucket-naming scheme already changed between releases. Reading them would re-create exactly the per-vendor on-disk coupling this change removes, against a faster-moving format than the ones being retired.

Conversations are therefore derived from the event stream, which means Magentic must persist what it observed rather than re-reading it.

This breaks a definition, and the change says so rather than sliding past it. CONTEXT.md defines a Conversation as "derived from the vendor's record and **never durable state of Magentic's own**", and `agent-timeline/conversation-reading` is written around a file that grows on disk and can be re-read from any point. Neither survives a source that exists only while it is being received: if Magentic does not keep what it observed, the activity is gone. The definition is therefore amended — a Conversation is derived from the vendor's record where one exists, and is Magentic's own durable record of what the vendor reported where the vendor keeps no record Magentic may read — and the reading spec is restated to hold for both. Identity stability across deltas is specified rather than left to the reader, because a stream is the hard case for the published guarantee that re-reading a Conversation is not a Conversation that grew.

*Consequence:* this is the one place where the change makes Magentic the system of record for something it previously only read. It is also the one place where losing Magentic's own state loses history that is nowhere else, which is a durability obligation the retired vendors never imposed.

*Alternative considered:* read omp's JSONL for history and use the protocol only for live status. Rejected — it is two normalization paths for one vendor, which ADR 0005 forbids, against an explicitly unstable format.

### Sessions run under a dedicated Magentic profile; credentials stay the developer's

omp keeps credentials, settings and sessions per profile under its agent directory. The daemon runs as the developer and launches every Session with `--profile magentic`, and Magentic never stores, copies or proxies a provider credential.

The profile is dedicated rather than shared because of what the spike found in omp's approval resolution: a per-tool `tools.approval.<tool>: allow` in configuration wins over `--approval-mode always-ask`. Under the shared profile, any such entry the developer set for their own use would silently narrow the gate on every Session Magentic starts. A dedicated profile keeps the developer's own policies out of Magentic's Sessions.

The cost is paid once per provider and is larger than a second login. A fresh profile has no provider configuration either: probed on this machine, it fell back to a Bedrock model instead of the developer's local Ollama model. The developer sets up models and logs in under `magentic` in omp's own interface. The RPC `login` command does not refuse secret input: for an API-key provider it asks the host for the key through an `input` request. Magentic cancels that request, never collects the key, and reports the provider as needing a login in omp under the `magentic` profile.

The dedicated profile does not isolate Project configuration. omp also merges `.omp/config.yml` and `.claude/settings.json` from the working directory, and its source applies no filter to `tools.approval` there. The attempt to exercise this stalled (the local model produced no turn), so it rests on the source rather than on a run. The approval-gate spec states this limit rather than claiming the profile closes it.

*Alternative considered:* the shared default profile, reusing the developer's logins. Rejected after the spike, because shared configuration can relax the gate without Magentic seeing it.

## Risks / Trade-offs

**omp's RPC vocabulary has no compatibility policy and ships roughly daily.** A command or event rename breaks Magentic silently, at the moment a developer needs it. → The handshake records the negotiated protocol version and refuses a handshake it does not understand; the approval gate is verified behaviorally per build rather than assumed; the driven surface is kept to the commands behind a requirement, so the exposure is as small as the feature set allows. This risk is not eliminated, and it is the price of the transport decision.

**The approval gate rests on a flag rather than a reported state.** Any failure of the flag is invisible in the unsafe direction: no approval requests simply look like an agent that never needed approval. → Behavioral verification per omp build, fail-closed refusal when it cannot be carried out, and the traffic-based secondary alarm recorded as an open question.

**Magentic gains a hard dependency on a third-party binary for its core function.** If omp is absent, broken or incompatible, no coding-agent Session can start at all, where today four vendors could each fail independently. → Absence and incompatibility are refused with a stated reason naming omp and the version rather than degrading; terminal Sessions are unaffected, so the product does not become unusable.

**The escape hatch undermines the gate it sits next to.** The route into omp's own interface is required, and a developer who takes it can change that session's approval mode — which `get_state` cannot report, so Magentic would keep asserting a gate that is no longer there. → Opening the route withdraws the proof: the Session stops being presented as gated by Magentic, and the claim is not re-asserted by asking the session. This is honest rather than safe; the alternative, refusing the route, would take away the only way to reach what Magentic does not model.

**Magentic becomes the system of record for omp Conversations.** Losing its own state now loses history that exists nowhere else, which was never true while vendors kept their own transcripts. → The retired vendors' records stay readable and untouched, so only omp-era activity carries this exposure, and it begins the day omp Sessions do.

**Retiring four vendors is irreversible for a developer's habits even though the code is revertable.** A developer who relies on a Claude Code feature Magentic does not surface loses it the day this ships. → The route into omp's own interface is a requirement rather than a convenience, and it is the escape hatch for everything Magentic does not model.

**Pricing across sixty providers cannot be as accurate as one vendor's price list was.** → Unknown cost is represented explicitly and never as zero, and a total that excludes unknowns says so. Accuracy is traded for honesty, which matches ADR 0004.

**One big change is hard to review and harder to roll back.** This is a consequence of the chosen scope, not an oversight. → The migration plan below is ordered so each step is independently revertable, and omp Sessions do not become the default until the steps before them are in place.

## Migration Plan

1. Add the omp runtime alongside tmux and managed, behind explicit opt-in per Session. Nothing changes for an existing Session. Revert by removing the runtime member.
2. Land process ownership, handshake and version negotiation on the existing agent host, and the behavioral verification of the approval gate. Revert by refusing the new runtime.
3. Land status, acknowledged delivery and turn control for omp Sessions, with the pane path untouched for everything else. Revert as step 2.
4. Land normalization of omp's event stream into Items, alongside the existing normalizers. Revert by declaring omp un-normalizable, which the item-model spec already defines a behavior for.
5. Land the session controls and the approval surface in the TUI and the desktop app. Revert by hiding the controls; the runtime keeps working without them.
6. Move the interfaces from the vendor axis to the model axis, and rewrite every surface that assumes a terminal, a vendor choice or one vendor's prices. This is the largest step by file count and the smallest by risk, because it touches presentation rather than behavior, and it is revertable per surface. It lands before step 7 so that the first Session created under omp by default is already described correctly everywhere.
7. Make omp the runtime new coding-agent Sessions are created with, and offer the resume-and-import path for older ones. **This is the point of no easy return for developer habits**, and it should follow a period of running step 1's opt-in in real use.
8. Remove the per-vendor launch and drive machinery, keeping the legacy readers. Revert by restoring the removed code; this step is deliberately last because it is the only one that is not a straightforward revert.

Rollback: before step 7, refusing new omp Sessions restores the previous behavior entirely. After step 8, rollback is a code restore rather than a configuration change, which is why steps 7 and 8 are separated by real use rather than by a release.

## Open Questions

- **Should Magentic raise a secondary alarm when tool executions complete without approval requests?** It would catch a silently failed gate, but it needs a definition of which tool executions must have required approval, and getting that wrong produces false alarms about a safety property. Deferred because the primary proof does not depend on it and adding it later changes no spec.
- **How is Project configuration kept from relaxing the gate?** A `--config` overlay is loaded after Project settings and could reset `tools.approval` for known tool names, but it cannot cover tools it does not name, and the design rejected overlays for their invisible failure mode. Refusing Sessions in Projects that carry approval configuration would need Magentic to read omp's configuration files, which this change forbids. Until this is settled, the limit is stated in the interfaces.
- **Where do prices for sixty providers come from?** The spec requires unknown costs to be explicit, which is answerable without deciding the source. Whether omp's reported usage is sufficient, or a price table is needed, can be settled when the reporting surface is built.
- **Should protocol v2 be negotiated?** v1 is sufficient for every specified requirement; v2 adds chunked frame reassembly, which matters only if frames exceed the advertised limit. Deferred until a real payload approaches it.
