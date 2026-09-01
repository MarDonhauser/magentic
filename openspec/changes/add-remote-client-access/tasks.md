## 1. Extract the host API seam (no network yet)

- [ ] 1.1 Define the host API Go interface covering every method the frontend is bound to today, using only durable identities and opaque handles (`SessionID`, `ProjectID`, `WorktreeRef`, `SpecificationStartToken`); verify the file compiles and lists every currently bound method.
- [ ] 1.2 Make the existing `*App` satisfy the interface as the local implementation without changing behavior; verify by a compile-time assertion and by the existing `app/` and `core/` tests passing unchanged.
- [ ] 1.3 Add a test asserting that every method of the host API interface is classified in the RemoteActionPolicy table (permitted or restricted), so a new method cannot be added without a decision; verify the test fails when a method is added without a classification.
- [ ] 1.4 Audit the interface for any parameter that is a filesystem path and replace it with a host-resolved handle; verify with a test that rejects path-shaped inputs at the boundary.

## 2. Wire protocol and shared message types

- [ ] 2.1 Define the request/response envelope, the error shape (distinguishing authentication failure, restricted action, host-side observation failure, and transport failure), and the protocol version handshake; verify with round-trip marshalling tests.
- [ ] 2.2 Define stream frame types: terminal output (sequence number + base64 payload), status event, gap marker, and control; verify with round-trip marshalling tests including a gap marker.
- [ ] 2.3 Extend the observation availability carrier so a client can distinguish "the host could not observe tmux" from "I could not reach the host"; verify with a `core/observation` test that the host-side unavailability reason survives serialization.

## 3. Host service: transport, authentication, and lifecycle

- [ ] 3.1 Implement the host service process with an explicit opt-in start (subcommand or flag), binding to loopback or a configured overlay address by default and printing the bound address; verify by starting it and confirming it is not reachable on a public interface.
- [ ] 3.2 Generate and persist a self-signed TLS certificate on first run and serve only over TLS; verify that a plaintext connection attempt is refused.
- [ ] 3.3 Implement HostToken generation, storage, constant-time comparison, and revocation; verify with tests that an unknown token, a revoked token, and a missing token are each rejected as authentication failures and that no token value appears in logs.
- [ ] 3.4 Close open streams and reject subsequent requests when a token is revoked mid-connection; verify with a test that revokes a token while a stream is subscribed.
- [ ] 3.5 Verify the host serves clients with no local interface running, by starting only the service and exercising a read from a test client.

## 4. Host service: reads, actions, and policy

- [ ] 4.1 Route every unary read (overview, board, git graph, statistics, specifications, automation, work history) through the local implementation and serve it over the transport; verify a remote read returns the same payload as a direct local call in a test that compares both.
- [ ] 4.2 Implement the RemoteActionPolicy with the default permitted/restricted split from the spec, enforced before any side effect, plus host-side opt-in configuration for restricted actions; verify with tests that a restricted action produces no Git, filesystem, or runtime side effect and is reported as restricted rather than as a failure.
- [ ] 4.3 Serve the policy to clients as a readable document; verify a client receives the same classification the server enforces.
- [ ] 4.4 Carry the client-supplied transition identity into the host's `LifecycleTransition` and advance an existing transition idempotently on re-submission; verify with a test that submits the same identity twice and asserts one transition and one side effect.
- [ ] 4.5 Verify that a client disconnect after action acceptance leaves the host reconciling to the desired state, and that an unknown initial-prompt delivery is reported as unknown and never auto-replayed.

## 5. Host service: terminal and event streaming

- [ ] 5.1 Implement the streaming endpoint with per-Session subscription, delivering terminal output as bytes with monotonic sequence numbers; verify a client receives output written by an attached Session.
- [ ] 5.2 Implement the bounded per-Session ring buffer and resume-at-sequence; verify with tests for resume inside the retained window (replay then live) and outside it (gap marker plus fresh pane snapshot).
- [ ] 5.3 Implement per-subscription flow control that drops or coalesces within a bounded budget and marks the stream gapped; verify with a test using a deliberately slow consumer that the buffer stays bounded and the pty reader is never blocked.
- [ ] 5.4 Emit status events on Observation changes (presence, activity, attention) so clients need not poll; verify with a test that an observation change produces exactly one status event per affected Session.
- [ ] 5.5 Verify the host serves partial and unavailable Observations unchanged over the stream and never serves affected Sessions as absent when its own probe fails.

## 6. Client: HostLink configuration and credentials

- [ ] 6.1 Implement durable client-side HostLink configuration (name, address, credential reference) with add, edit, remove; verify with tests covering persistence across app restart.
- [ ] 6.2 Store and read HostTokens through the OS credential store, never in the Registry or plain configuration; verify with a test that the written configuration contains no token value and that an unavailable credential store leaves the app detached with an explicit report.
- [ ] 6.3 Implement TLS fingerprint pinning on first attach and refusal on a changed fingerprint; verify with a test that a changed certificate is refused rather than silently accepted.

## 7. Client: attachment, connection state, and reconnect

- [ ] 7.1 Implement the remote host API implementation issuing unary requests; verify with an integration test running a host service in-process and exercising reads and a permitted action end to end.
- [ ] 7.2 Implement `HostSession` with the explicit connection states and the last-successful-exchange timestamp derived from the client's monotonic clock; verify with tests that each state is reachable and that the age does not depend on host time.
- [ ] 7.3 Implement mode switching between local and one selected host, detaching and closing streams before attaching elsewhere; verify with a test that Sessions from a previous host are never shown alongside the new host's.
- [ ] 7.4 Implement bounded exponential backoff with jitter, a manual immediate reconnect, no auto-reconnect after a credential refusal, and no auto-reconnect after a deliberate detach; verify with tests for each of the four behaviors.
- [ ] 7.5 Re-synchronize the Session, Observation, and lifecycle view from the host before clearing the last-known labelling on reconnect; verify with a test that the view is not marked current until a fresh known payload has arrived.

## 8. Client: unavailable knowledge and gated actions

- [ ] 8.1 Shape the client-side payload so host-derived facts are only reachable through their availability, making an availability-blind read a compile error rather than a bug; verify by attempting a direct read in a test that must not compile.
- [ ] 8.2 Render the last-known view with its age while unavailable, and never render a disconnection as absent, dead, idle, finished, or clean Sessions; verify with view-level tests covering the sidebar, overview, board, git graph, and statistics during a disconnection.
- [ ] 8.3 Gate destructive and overwriting actions on fresh known facts, reusing the existing ADR 0004 gate rather than adding a parallel rule, and re-enable them after a fresh known view arrives; verify with tests for both directions.
- [ ] 8.4 Present restricted actions as unavailable with the host's reason, and treat a host refusal as authoritative over a cached policy; verify with tests for a restricted action and for a stale-policy refusal.

## 9. Client: terminal attachment over the network

- [ ] 9.1 Map `OpenTerm` / `WriteTerm` / `ResizeTerm` / `CloseTerm` and the `term:data:` event path onto the streaming channel; verify with an end-to-end test that types into a remote Session and observes its output.
- [ ] 9.2 Resume each attached terminal at its last received sequence on reconnect; verify a brief drop within the retained window continues without a visible break.
- [ ] 9.3 Replace terminal content with the host's fresh snapshot and mark that output was missed when the gap cannot be served, never appending across a gap; verify with a test that forces a gap.
- [ ] 9.4 Indicate that input is not being delivered while disconnected and never queue keystrokes for silent later delivery; verify with a test that types during a disconnection and asserts nothing is delivered on reconnect.

## 10. Attention in client mode

- [ ] 10.1 Feed the AttentionPlanner from the host's streamed Observations and status events so notifications, Dock badge, native attention, and breaks run on the developer's machine; verify with a test that a remote Session entering a waiting state raises client-side attention.
- [ ] 10.2 Suppress per-Session attention intents derived from a last-known view while the connection is unavailable, surfacing the connection state instead; verify with a test that a disconnection with a waiting Session in the last-known view raises no new per-Session intent.

## 11. Integration, safety, and documentation

- [ ] 11.1 Verify the host service and a local desktop app running concurrently on the host coordinate correctly on the Registry under ADR 0002, with a test that mutates from both.
- [ ] 11.2 Run an end-to-end scenario across two processes: attach, observe, create a Session, attach its terminal, drop the network, confirm the last-known presentation and the disabled destructive actions, restore, confirm resume or an honest gap.
- [ ] 11.3 Document the host service's trust assumptions, default binding, token handling, revocation, and the RemoteActionPolicy defaults in `README.md`; verify the documented defaults match the enforced ones by cross-checking against the policy table test.
- [ ] 11.4 Decide the browser-client question from design.md Open Questions by attempting to serve the existing web frontend from the host service; verify either a working browser client or a recorded decision that it is out of scope for this change.
