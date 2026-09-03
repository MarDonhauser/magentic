## 1. AgentRuntime as a Session property

- [x] 1.1 Add `AgentRuntime` (`RuntimeTmux`, `RuntimeManaged`) and the runtime field on `core.Session` in `core/state.go`, serialized as a stable string whose absence reads as `RuntimeTmux`; verify with a round-trip test that a `state.json` written before this change loads with every Session on the tmux runtime.
- [x] 1.2 Declare per vendor which runtimes it supports in `core/provider.go`, with only Claude declaring managed support; verify with a table test in `core/provider_test.go` that all four vendors declare a set and that every set contains tmux.
- [x] 1.3 Refuse creating a managed Session for a vendor without managed support, and for terminal Sessions, with the reason stated and no record written; verify with tests covering an unsupported vendor and a terminal Session.
- [x] 1.4 Derive the offered action list from the Session's runtime so attach is absent for managed Sessions and interrupt and permission answering are absent for tmux Sessions; verify with a table test over the action list per runtime rather than by executing the actions.
- [x] 1.5 Add Verify that the runtime cannot change after creation: no lifecycle transition writes it a second time; verify with a test that every transition leaves the recorded runtime untouched.

## 2. Agent host process

- [x] 2.1 Add the `agent-host` mode to the Magentic binary: it takes a Session identity and a working directory, owns one vendor process, and listens on a unix socket under the state directory with owner-only permissions; verify with a test that the socket is created with restrictive permissions and removed on clean exit.
- [x] 2.2 Add the host connect handshake returning the token the daemon recorded at start; verify with tests that a matching token confirms identity and a mismatched token is refused.
- [x] 2.3 Start the Claude process from the host with the verified argument list (`-p`, `--input-format stream-json`, `--output-format stream-json`, `--verbose`, `--include-partial-messages`, `--replay-user-messages`, `--permission-prompts host`, `--permission-prompt-tool`, `--mcp-config`, and `--session-id` or `--resume`); verify with a test asserting the exact argv built for a fresh Session and for a continued one.
- [x] 2.4 Check the Claude Code version at host start and refuse the managed runtime with a stated reason on an unverified version; verify with tests for a verified and an unverified version string.
- [x] 2.5 Keep the host alive across daemon disconnects and accept a later reconnect; verify with a test that disconnecting and reconnecting the daemon leaves the vendor process running and the Session state intact.
- [x] 2.6 Terminate the vendor process and its children on an explicit stop and on host shutdown, and remove the socket; verify with a test that no child survives a stop.

## 3. Daemon ownership and reclaim

- [x] 3.1 Record each managed Session's host durably: socket path, process identity and handshake token, written before the host is started per ADR 0003; verify with a test that the record exists before any process is spawned and that a failed spawn leaves a record marked as not started.
- [x] 3.2 Reclaim recorded hosts at daemon startup by connecting and confirming the token; verify with tests that a live host is reclaimed without a second process being started, that a dead host marks its Session as having no process, and that a socket answering with a wrong token is neither adopted nor killed.
- [x] 3.3 Forbid pattern-based process lookup and termination anywhere in the managed path; verify with a test that stopping a managed Session signals only the recorded identity, and with a source-level check that the managed path issues no process-table search.
- [x] 3.4 Report a recorded host whose Session no longer exists rather than sweeping it; verify with a test that reconciliation lists the orphan and terminates nothing.
- [x] 3.5 Refuse ownership when another daemon already holds the managed processes, reusing the existing single-owner socket handling; verify with a test that a second daemon refuses, states the reason, and starts no agent process.

## 4. Turn control and delivery

- [ ] 4.1 Add the control verbs for the managed runtime — start a turn, interrupt a turn, answer a permission request — to `core/control.go` and its dispatcher, keeping the existing verbs' behavior for tmux Sessions; verify with tests in `core/control_dispatch_test.go` that each new verb is dispatchable and that a managed-only verb against a tmux Session is refused with an addressing outcome.
- [ ] 4.2 Deliver a queued prompt to a managed Session and advance the Outbox only on the protocol's echo of that prompt; verify with tests that the queue advances on the echo, that a delivery failure leaves the prompt queued with its reason, and that a missing echo neither advances the queue nor resends.
- [ ] 4.3 Record turn start and turn end with an explicit end reason (completed, interrupted, failed with the vendor reason) from protocol events; verify with tests that a completed turn, an interrupted turn and a failed turn each record their own reason, and that a long silent stretch keeps the turn running.
- [ ] 4.4 Implement interrupting the running turn so the process stays alive and accepts the next prompt, and refuse an interrupt when no turn is running; verify with tests for both, asserting the process is still alive afterwards in each case.
- [ ] 4.5 Publish streamed output as it arrives, marking the in-progress message, and supersede it with the completed message; verify with a test that a streamed then completed message leaves exactly one final Item in its final form.

## 5. Permission decisions

- [ ] 5.1 Add the `agent-approve` MCP mode to the Magentic binary, configured into the agent through `--mcp-config`, whose tool call blocks in the host until a decision arrives; verify with a test that the tool call returns only after a decision is delivered.
- [ ] 5.2 Represent an open `PermissionRequest` with what is asked, its Session and the time it was raised, readable by every connected interface and surviving disconnects; verify with tests that a request is visible to a second interface and still open after the first one disconnects and reconnects.
- [ ] 5.3 Add "waiting for a decision" as its own observed status in `core/status.go` and `core/observation.go`, distinct from working, idle and waiting for a prompt; verify with tests in `core/status_semantics_test.go`.
- [ ] 5.4 Plan an attention intent for a Session that opens a permission request, before any notification is emitted, per ADR 0007; verify with a test in `core/attention_test.go` asserting the planned intent and the ordering.
- [ ] 5.5 Deliver a decision exactly once and close the request, refusing a second answer; verify with a test that two concurrent answers result in one delivered decision and one refusal.
- [ ] 5.6 Close an open request as no longer answerable when the agent process ends, never as allowed or denied; verify with a test asserting the recorded outcome.
- [ ] 5.7 Confirm no setting, mode or code path answers a permission request without an explicit developer decision; verify with a test that a request with no interface connected stays open indefinitely, and with a test enumerating settings that none of them answers one.
- [ ] 5.8 Record the request and its outcome as Items in the Session's activity in the order they occurred; verify with a test over the resulting Item sequence.

## 6. Lifecycle, observation and status

- [ ] 6.1 Add the managed path to `Start`, `Resume` and `Kill` in `core/lifecycle.go`, dispatching on the Session's runtime and writing durable intent before any process is touched, per ADR 0003; verify with tests that intent precedes the spawn and that an interrupted start is completed by reconciliation without a second process.
- [ ] 6.2 Verify the recorded working directory exists and resolves inside its Project before starting a managed process; verify with tests that a missing directory and a directory outside the Project both fail the start and start nothing.
- [ ] 6.3 Derive a managed Session's observed status from daemon facts and protocol events, issuing no tmux command for it; verify with a test that observing a managed Session runs no tmux call.
- [ ] 6.4 Report an unexpected agent exit as a failed Session with the exit reason and start no replacement; verify with a test.
- [ ] 6.5 Report managed Sessions as unobservable, naming the daemon, when an interface cannot reach it, per ADR 0004; verify with a test that they read as unobservable and not as dead.

## 7. Continuing in a terminal

- [ ] 7.1 Add the action that continues a managed Session's conversation in a new tmux Session using the vendor's fork form, leaving the managed Session and its process untouched; verify with a test asserting the new Session's runtime, the fork command line, and that the managed Session's record and process are unchanged.
- [ ] 7.2 Present both Sessions with their own identities and runtimes in the TUI and desktop projections; verify with a projection test.

## 8. Service installation

- [ ] 8.1 Add the install, status and remove commands for the login service, writing a launchd user agent on macOS and a systemd user unit on Linux, never system-wide and never elevated; verify with tests over the generated unit content and its target path per platform.
- [ ] 8.2 Refuse the managed runtime on unsupported platforms with the reason stated; verify with a test.
- [ ] 8.3 Report installed, running and managed-by-this-service as separate facts, distinguishing a daemon the service did not start; verify with tests for each combination.
- [ ] 8.4 Ensure no interface start, Session creation or update installs the service; verify with tests that each of those paths writes no unit file.
- [ ] 8.5 State the effect on running managed processes before removal proceeds, and leave every Session record and its work untouched; verify with a test that removal changes no Session record, Worktree or conversation record.
- [ ] 8.6 Present managed Sessions with a conditional durability claim while the service is not installed, offering installation; verify with a test over the presented text for both states.

## 9. Surfaces

- [ ] 9.1 Add the permission request surface to the desktop app: what is asked, allow and deny, and the Session it belongs to; verify with unit tests over the render model including a closed request that can no longer be answered.
- [ ] 9.2 Add the interrupt action for managed Sessions to the TUI and the desktop app; verify with tests that it is offered only for managed Sessions with a running turn.
- [ ] 9.3 Show streamed output live in the conversation surface introduced by `add-agent-timeline`; verify with a unit test that an in-progress message renders as in progress and is replaced in place on completion.
- [ ] 9.4 Remove attach from managed Sessions in both interfaces and offer continuing in a terminal in its place; verify with tests over the action lists.

## 10. Vocabulary and documentation

- [ ] 10.1 Add AgentRuntime, agent host, PermissionRequest and turn to `CONTEXT.md` following the existing entries' shape; verify by reading the section back against the specs for agreement.
- [ ] 10.2 Write an ADR in `docs/adr/` for moving coding-agent process ownership from tmux into the daemon, recording the per-Session host decision and the alternatives rejected; verify it follows the existing ADRs' structure.
- [ ] 10.3 Reword the durability claim in `README.md` so it distinguishes tmux Sessions from managed ones and does not promise more than the installed service delivers; verify the claim matches what ships.
