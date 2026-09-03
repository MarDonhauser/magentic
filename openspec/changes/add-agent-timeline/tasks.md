## 1. Item and Conversation vocabulary

- [x] 1.1 Add `ItemKind` with the closed set from the item-model spec (developer prompt, agent message, reasoning, plan, command execution, file change, file read, tool call, web search, delegated task, context compaction, error, unknown) in a new `core/timeline.go`; verify with a table test that every kind has a stable serialized label, that labels round-trip, and that an unrecognized label reads back as the unknown kind.
- [x] 1.2 Add the `Item` type carrying identity, occurrence time, role, kind, title, optional detail, delegated marker, optional parent task identity, and an optional failure marker; verify with a JSON round-trip test that an Item without a detail and without a parent serializes without inventing empty values.
- [x] 1.3 Add `Conversation` (ordered Items plus the ConversationRef they belong to) and `ConversationRef` (vendor plus run reference); verify with a test that appending an Item whose identity is already present leaves the Conversation unchanged.
- [x] 1.4 Add the unavailable readings — not applicable, no normalizer, record not found, record unreadable — as an explicit result type alongside an available Conversation, following ADR 0004; verify with a test that an empty available Conversation and each unavailable reading are all distinguishable and that every unavailable reading carries a reason.

## 2. Normalization contract

- [x] 2.1 Add the normalizer interface (locate a vendor's Conversation record from a `ConversationRef`, normalize a byte range of it into Items) in `core/timeline.go`; verify the package compiles and a test asserts the interface is satisfied by the Claude normalizer once it exists.
- [x] 2.2 Extend `AgentProvider` in `core/provider.go` so every vendor declares either a normalizer or the explicit absence of one; verify with a table test in `core/provider_test.go` that all four builtin vendors declare an answer and that only Claude declares a normalizer.
- [x] 2.3 Resolve a Session's `ConversationRef` from its recorded vendor and `AgentRunRef` only, refusing to fall back to the Session name or runtime name; verify with tests that a renamed Session resolves the same ref, that a Session without a run reference yields not-applicable, and that a terminal Session yields not-applicable.

## 3. Claude normalizer

- [x] 3.1 Add the record-type classification with an explicit activity list and an explicit metadata list (`attachment`, `last-prompt`, `mode`, `permission-mode`, `atis-latch`, `ai-title`, `queue-operation`), mapping anything in neither list to the unknown kind carrying its own type label; verify with a test that a fabricated unknown record type produces one unknown Item rather than being dropped.
- [x] 3.2 Implement the kind mapping table from design.md for `user`, `assistant` and `system` records including every block type and tool-name group; verify with a table test covering one case per table row.
- [x] 3.3 Derive Item identity from the record `uuid` extended by block index, so one assistant record holding several blocks yields several stable identities; verify with a test that normalizing the same bytes twice produces identical identities and that a multi-block record produces distinct ones.
- [x] 3.4 Attach a `tool_result` to the `tool_use` Item carrying its id, supplying that Item's detail and its success or failure, holding open tool-call ids across the scan; verify with tests that a result in the same batch completes its Item, that a result arriving in a later batch supersedes the earlier Item in place, and that a tool call whose result never arrives stays unfinished.
- [x] 3.5 Mark records with `isSidechain` as delegated and carry the parent task identity when the record names one, leaving the parent explicitly unknown otherwise; verify with tests covering both cases.
- [x] 3.6 Fill title and detail per kind (command line, changed path, query, tool name for the unrecognized case); verify with a test asserting the title of one Item per kind and that no title is empty.
- [x] 3.7 Skip a partially written trailing record and report the byte offset up to the last complete record; verify with a test that a truncated final line produces no Item and that normalizing again after the line completes produces it exactly once.
- [ ] 3.8 Add golden tests over real Claude transcript files placed in `core/testdata`, asserting the full normalized Item sequence; verify the golden output covers at least prompts, prose, reasoning, a Bash call with result, a file change, a delegated task with its subagent activity, and one metadata record that yields no Item.

## 4. Incremental reading

- [ ] 4.1 Add the per-Conversation read position (byte offset plus a fixed-size prefix fingerprint) and the decision to continue or to re-read in full; verify with tests that a grown file with a matching prefix continues from the offset, that a shortened file re-reads in full, and that a same-length file with a changed prefix re-reads in full.
- [ ] 4.2 Replace, rather than extend, the held Conversation when a full re-reading occurs; verify with a test that Items from the discarded reading are gone afterwards.
- [ ] 4.3 Read Conversations only for Sessions an interface declares it is watching, and add the watch declaration to the core surface; verify with a test that a pass over several Sessions with one watched reads exactly one Conversation record.
- [ ] 4.4 Drive the incremental read from the existing Observation pass without adding a second loop; verify with a test that no additional goroutine or ticker is started and that a pass with no appended records publishes nothing.
- [ ] 4.5 Confirm reading never writes, moves, truncates, locks, or sends anything; verify with a test that the record file's content, size and modification time are unchanged after a read and that no runtime command is issued.

## 5. Desktop app surface

- [ ] 5.1 Add the Conversation DTO and an incremental fetch to `app/tools.go` beside `SessionPreview`, carrying either the available Items or the unavailable reading with its reason; verify with a Go test that each unavailable reading maps to its own transport value and that none of them serialize as an empty Item list.
- [ ] 5.2 Publish newly normalized Items to the frontend over the existing Wails event channel; verify with a test that a pass producing new Items emits exactly one event carrying only those Items.
- [ ] 5.3 Add `marked` to `app/frontend/package.json` with raw HTML disabled and a render helper; verify with a `node --test` unit test that a message containing raw HTML renders as visible text and not as markup, and that code fences, lists and links render.
- [ ] 5.4 Add the conversation renderer module beside the terminal dock, following the existing vanilla-JS module and test conventions; verify with unit tests over the render model that Items appear in order and that a superseded Item replaces its predecessor in place rather than appending.
- [ ] 5.5 Render tool activity collapsed to its title with an expandable detail, and show a failure in the collapsed line; verify with unit tests over the render model for a successful call, a failed call, and a call without a detail.
- [ ] 5.6 Render agent messages and developer prompts in full, without a toggle; verify with a unit test over the render model.
- [ ] 5.7 Group delegated Items under their parent task, and present parentless delegated Items as delegated work of unknown origin; verify with unit tests for both.
- [ ] 5.8 Add the surface switch between terminal and Conversation for the selected Session, leaving selection and runtime untouched; verify with a test that switching issues no lifecycle or tmux call.
- [ ] 5.9 Keep the scroll position when new Items arrive after the developer has scrolled back; verify with a unit test over the scroll decision, mirroring the existing dock-state test style.
- [ ] 5.10 Render each unavailable reading with its own wording naming the reason and the vendor where applicable, keeping the terminal reachable; verify with unit tests that the empty-but-available wording differs from the record-not-found wording.
- [ ] 5.11 State that the agent is waiting and offer the way to its terminal when the Session is observed waiting, and offer no control that claims to answer a permission prompt; verify with a unit test over the render model and a test asserting no approval action exists in the surface.

## 6. Vocabulary and documentation

- [ ] 6.1 Add Item, ItemKind, Conversation and ConversationRef to `CONTEXT.md` with their `_Avoid_` lines, following the existing entries' shape; verify by reading the section back against the item-model spec for agreement.
- [ ] 6.2 Update the desktop app's description in `README.md` to name the conversation surface beside the embedded terminal; verify the described behavior matches what ships, with no claim about approvals or streaming.
