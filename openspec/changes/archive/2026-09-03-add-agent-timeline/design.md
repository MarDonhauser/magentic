## Context

See proposal.md — Why. The constraints that shape the approach:

- Claude Code records every conversation as JSON Lines under `~/.claude/projects/<encoded-cwd>/<run-id>.jsonl`, one record per line, appended as the run proceeds. A record carries `uuid`, `timestamp`, `sessionId`, `cwd`, `isSidechain`, and a `message` whose `content` is an array of typed blocks.
- The path is deterministic for Sessions Magentic provisions, because `claudeProvider.StartCommand` already passes `--session-id` (`core/provider.go:167`) and `claudeProvider.RunExists` already resolves `<run-id>.jsonl` under that root (`core/provider.go:157`).
- `WorkHistory` already walks these files for four vendors behind a `historyProviderAdapter` interface (`core/workhistory_adapters.go:22`). It is a batch index: it fingerprints whole files, caches parses, deduplicates across sources, and recomputes Project and Session attribution per query. Its Claude adapter deliberately drops every record carrying tool traffic (`core/workhistory_adapters.go:433`).
- The Observation Module already runs one pass on a fixed cadence for both interfaces, and ADR 0004 requires an unavailable observation to stay explicitly unavailable.
- The desktop frontend is vanilla ES modules with `node --test` unit tests beside each module. There is no UI framework and no markdown renderer today.

## Goals / Non-Goals

**Goals:**

- One normalization contract that a second vendor can be added to without touching the interfaces.
- Incremental reading cheap enough to run inside the existing Observation pass.
- Item identity stable enough that re-reading is idempotent without a diff.

**Non-Goals:**

- Persisting Items. A Conversation is derived from the vendor's record and can always be derived again; nothing here is durable state.
- Changing `WorkHistory`, its index, its cache, or the Timeline and Stats surfaces.
- A second reading path for the TUI in this change. The TUI keeps the terminal; the Item model is placed in `core/` so it can follow later.

## Decisions

### Read the vendor's record rather than drive the agent

Items are normalized from the file the vendor already writes. Magentic does not own the agent process and does not change how it is started.

*Alternative considered:* run Claude headless now (`-p --output-format stream-json`) and normalize its event stream. That yields streaming, subagent links and permission prompts, but it moves process ownership out of tmux and breaks the durability promise in README line 3. It is the subject of the follow-on change; doing it here would mean the Item model and the process rewrite land together with no reviewable step in between.

*Consequence:* no token-by-token output, and permission prompts are structurally invisible — they are never written to the record. The specs state both.

### A second reading beside WorkHistory, not a widened HistoryEvent

The normalizer is new code in `core/` that reuses `historyJSONLines` and the existing record structs, but produces Items rather than `HistoryEvent`s and does not enter the WorkHistory index.

*Alternative considered:* extend `HistoryEvent` with tool facts and let the Conversation be a WorkHistory query. Rejected: WorkHistory's value is a deduplicated, attributed, cached index over *all* history for statistics. Tool payloads are large, per-run, and only interesting live; putting them in that index inflates the cache for every Stats query and couples a live surface to a batch one. The two readings share parsing helpers, not a data model.

### Item identity comes from the vendor's own record identity

A Claude record carries a `uuid`. An Item's identity is that uuid, extended by the block index when one record yields several Items (an assistant record holding a thinking block and two tool calls yields three Items). Idempotent re-reading then falls out of identity rather than out of a comparison pass, which is what the item-model spec requires.

*Alternative considered:* hash the normalized Item. Rejected: a normalizer change would silently change every identity, and a vendor that legitimately repeats content would collide.

### Incremental position is a byte offset plus a prefix fingerprint

Each watched Conversation keeps the byte offset it read to and a fingerprint of a fixed-size prefix of the file. On the next pass, a file that is at least as long and whose prefix still matches is read from the offset; anything else is read from the beginning and replaces the held Conversation. This is the mechanism behind the conversation-reading spec's full-re-reading requirement.

*Alternative considered:* filesystem watching. Rejected: another dependency, different semantics on macOS and Linux, and the Observation pass already ticks at a cadence both interfaces are built around. Polling a file size is cheap; the pass already runs `tmux` commands per Session.

### Only watched Sessions are read

The interface tells the core which Session it is presenting; the incremental read runs for that Session only. A machine with twenty registered Sessions does not normalize twenty conversations per pass.

*Consequence:* opening a Session's Conversation costs one full read of its record. For a long-running Session that file can be tens of megabytes, so the first read is the expensive one and the steady state is cheap.

### Record types are classified, never silently skipped

A real Claude record file holds more than activity. A sample from a developer machine contains `attachment`, `last-prompt`, `mode`, `permission-mode`, `atis-latch`, `ai-title` and `queue-operation` records alongside `user`, `assistant` and `system`. The normalizer holds two explicit lists: record types that carry activity, and record types that are known session metadata. A record in neither list becomes an Item of unknown kind carrying its own type label.

This is what keeps the item-model spec's promise honest as Claude Code changes: a new activity record shows up in the surface as an unknown row rather than disappearing.

### The kind mapping is a table, not a chain of conditionals

| Claude record | Item kind |
| --- | --- |
| `user` record, no `toolUseResult`, not `isMeta` | developer prompt |
| `assistant` block `text` | agent message |
| `assistant` block `thinking` | reasoning |
| `tool_use` `Bash` | command execution |
| `tool_use` `Edit`, `MultiEdit`, `Write`, `NotebookEdit` | file change |
| `tool_use` `Read`, `Glob`, `Grep` | file read |
| `tool_use` `WebSearch`, `WebFetch` | web search |
| `tool_use` `Task`, `Agent` | delegated task |
| `tool_use` `TodoWrite` | plan |
| `tool_use` `mcp__*` | tool call, MCP-qualified |
| `tool_use`, any other name | tool call |
| `user` block `tool_result` | attaches to the `tool_use` Item it names |
| `system` compaction boundary | context compaction |
| known metadata record types | not an Item |
| anything else | unknown |

A `tool_result` is not its own Item: it completes the `tool_use` Item that carries its id, supplying that Item's detail and its success or failure. The normalizer therefore holds open tool-call ids while scanning, and a result arriving in a later incremental read completes an Item published earlier. That is the one case where a published Item changes; the item-model spec permits it as supersession, and the surface re-renders the row in place.

### Delegated work is detected from `isSidechain` and the parent tool-use id

`isSidechain` marks a record as delegated; the parent link, when Claude records one, names the `Task` tool call it belongs to. Both facts already appear in the struct `WorkHistory` parses (`core/workhistory_adapters.go:393`), which today maps only `isSidechain` onto its lineage field.

### Presentation facts are filled by the normalizer

Title and detail are produced per kind: the command line for a command execution, the changed path for a file change, the query for a web search, the tool name for anything unrecognized. The interfaces render kinds, never tool names. This is the one idea taken from T3 Code's architecture, whose contracts decide a tool row's title, detail and icon server-side rather than in the renderer.

### Markdown is rendered with raw HTML disabled

Agent messages are markdown and contain code, lists and links; hand-rolling that is a known trap. The frontend gains one small dependency (`marked`) configured with raw HTML disabled, so file content quoted by an agent cannot inject markup. No sanitizer is added, because no HTML is ever produced from Item text.

*Alternative considered:* render as preformatted text. Rejected: code fences, lists and links are most of what makes the surface more readable than the terminal.

## Risks / Trade-offs

- **Claude changes its record format.** → Golden tests run against real transcript files checked into `core/testdata`, and the unknown kind absorbs new record types as visible rows rather than as gaps. A format change degrades the surface; it does not break the Session.
- **The first read of a long Conversation is expensive.** → Only watched Sessions are read at all, and the cost is paid once per Session on opening. If it proves too slow, the fix is to normalize from the end backwards for the initial window, which does not change any spec.
- **Memory grows with a long watched Conversation.** → Bounded by watching one Session at a time; the record is on disk and can be re-read. Measure before adding a cap, because a cap would collide with the item-model spec's promise that Items do not disappear.
- **A tool result never arrives** (the agent was killed mid-call). → The Item stays in its started state and renders as unfinished. This is accurate and needs no special case.
- **The surface can look complete while missing a permission prompt.** → The conversation-view spec requires the waiting state to be shown with the way to the terminal, and forbids any control that claims to answer a prompt. This is the honest limit of reading a record instead of owning the process, and it is the reason the follow-on change exists.
- **A Session Magentic did not provision** may carry no run reference, so its Conversation cannot be located. → Reported as unavailable with that reason; the terminal stays.

## Migration Plan

Nothing is persisted and no existing behavior changes, so there is no data migration and no compatibility window. `WorkHistory`, the Outbox, the lifecycle and the terminal are untouched. Rollback is removing the surface; Sessions are unaffected either way.

## Open Questions

- Whether the Conversation surface eventually replaces the terminal as the default view for coding-agent Sessions. It stays secondary here; making it the default is a presentation decision that can be taken once the surface has been used against real work.
- The initial-read window for very long Conversations. Deferred until measured, because it changes performance and not behavior.
