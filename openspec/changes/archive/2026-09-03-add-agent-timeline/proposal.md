## Why

Reading what a coding agent is doing means reading its terminal. The desktop app embeds xterm.js and `SessionPreview` returns the last sixteen lines of `capture-pane`, so the developer sees a redrawn TUI: a spinner, a truncated tool line, whatever survived the last repaint. Scrolling back through four agents to answer "what did it change, and why" is slow, and on a narrow pane it is not possible at all.

The material for a better reading already exists. Every supported vendor records its conversation structurally on disk, and Magentic already reads those files: `WorkHistory` normalizes four vendors into provider-neutral `HistoryEvent`s. It normalizes only prose and usage, though — the Claude adapter drops every line that carries tool traffic, which is the majority of what an agent actually does. A sample of a real Claude conversation on a developer machine holds 78 tool calls, 78 tool results and 30 thinking blocks against 16 blocks of assistant prose. Magentic keeps the 16 and discards the rest.

This change turns that discarded material into a first-class reading of a Session: a normalized, provider-neutral account of agent activity that the interfaces can render as a conversation instead of as a terminal.

## What Changes

- Introduce **Item** as domain vocabulary: one normalized unit of agent activity, provider-neutral, with a stated kind (developer prompt, agent message, reasoning, command execution, file change, file read, tool call, web search, delegated task, context compaction, error). An Item states what happened; it is not a transcript line and not a rendering.
- Introduce **Conversation** as the ordered, append-only sequence of Items belonging to one coding-agent run, and **ConversationRef** as the vendor-qualified handle that resolves to one.
- Give each vendor Adapter a second reading beside the existing statistics reading: normalization of its native conversation records into Items. Claude Code is implemented; Codex, Gemini CLI and GitHub Copilot declare explicitly that they cannot yet be normalized, rather than returning an empty Conversation that reads like a quiet one.
- Fill each Item's presentation facts — a short title, an optional detail, and a kind-derived icon — during normalization, so an interface renders what the Adapter decided and does not re-derive meaning from a tool name.
- Read the Conversation of a running Session **incrementally**: after the first read, only records appended since the last read are normalized, and new Items are published to the interfaces through the existing observation cadence.
- Present the Conversation of the selected Session in the desktop app as a reading surface beside the existing terminal, with tool activity collapsed to one line by default and expandable to its detail.
- Keep every unavailable fact explicit, per ADR 0004: a Conversation that cannot be located, cannot be read, or belongs to a vendor without a normalizer is reported as such with its reason, and never as an empty Conversation.

### Non-goals

- **How Sessions run does not change.** tmux keeps owning the agent process, the terminal stays, and the start and resume command lines are untouched. Replacing the process owner is a separate change.
- **Approvals are out of scope.** A vendor's permission prompt is answered in its own interface and is not recorded in the conversation file, so the Conversation cannot show one and the terminal remains the only place to grant it.
- **Sending does not change.** The Outbox and its tmux delivery stay exactly as they are; this change adds a reading surface, not a second input path.
- Token-by-token streaming. Vendors record a message once it is complete, so Items appear per message, not per token.
- Reworking `WorkHistory`'s statistics reading, its index, its attribution, or the Timeline and Stats surfaces built on it.
- Search across Conversations, export, and sharing.

## Capabilities

### New Capabilities

- `agent-timeline/item-model`: the provider-neutral Item and Conversation vocabulary, the Item kinds, their presentation facts, and the per-vendor normalization contract including the explicit "not supported" answer.
- `agent-timeline/conversation-reading`: locating the Conversation belonging to a Session, reading it incrementally as the agent works, and reporting an absent or unreadable Conversation with its reason.
- `agent-timeline/conversation-view`: presenting a Session's Conversation in the desktop app beside its terminal, including collapsed tool activity, unavailable readings, and the delegated work of subagents.

### Modified Capabilities

<!-- No capability specs are published under openspec/specs/ yet, so there is no
     existing spec to amend. The WorkHistory statistics reading is deliberately
     left unchanged; the new normalization is a second reading beside it. -->

## Impact

- `core/workhistory_adapters.go`: the Claude adapter currently discards every record carrying tool traffic (`workhistory_adapters.go:433`). That statistics reading stays; a second normalization path is added beside it, sharing the existing JSONL scanning helpers.
- New `core/timeline*.go` (or `core/timeline/`): the Item and Conversation types, the normalizer contract, the Claude normalizer, and the incremental reader.
- `core/provider.go`: each `AgentProvider` states whether it can normalize a Conversation and how its Conversation is located from a Session's `AgentRunRef`. Claude's location is deterministic today because `StartCommand` already passes `--session-id`.
- `core/observation.go`: the existing pass gains the incremental Conversation read for the Sessions an interface is watching. There must not be a second observation loop.
- `app/tools.go` and the Wails bindings: a Conversation DTO and an incremental fetch beside `SessionPreview`, which stays for the terminal preview.
- `app/frontend/src/`: a conversation renderer next to the existing terminal dock, following the existing vanilla-JS module and test conventions.
- `CONTEXT.md`: Item, Conversation and ConversationRef are new domain vocabulary and belong in the model.
- `README.md`: the desktop app's description gains the conversation surface.
