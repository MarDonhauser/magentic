## Context

See proposal.md — Why. The design-relevant state of the code today:

- `core/` is a pure Go package with no interface dependency. It owns the Registry, Observation, Lifecycle, Attention, Specifications, Repositories, and WorkHistory. It already coordinates across processes (ADR 0002) and reconciles durable desired state (ADR 0003), so a second Magentic process on the same host is an existing, handled condition rather than a new one.
- `app/app.go` is the Wails binding layer: roughly sixty exported methods, all synchronous request/response over `SessionID`, `ProjectID`, and opaque refs, plus four terminal methods (`OpenTerm`, `WriteTerm`, `ResizeTerm`, `CloseTerm`) and a `runtime.EventsEmit` stream keyed `term:data:<connectionKey>` / `term:closed:<connectionKey>`.
- `app/watcher.go` runs the observation loop and hands each `ObservationSnapshot` to the AttentionPlanner, which produces the notification, badge, and native-attention intents.

That is already a client/server split with an in-process transport. The interesting design work is choosing the seam, the wire, and the failure semantics — not inventing an architecture.

Assumptions recorded here because the request said not to ask: the host runs Linux or macOS with tmux; the developer owns both machines; reachability is provided by Tailscale, a LAN, or an SSH tunnel; there is exactly one developer; and clock skew between host and client is small but not assumed to be zero.

## Goals / Non-Goals

**Goals:**

- One seam, two implementations. The frontend calls the same interface whether the core is in-process or across a network.
- The wire carries terminal bytes and semantic events. Never pixels, never screenshots, never a remote-framebuffer protocol.
- Connection state is a first-class, separately typed fact that can never be confused with Session state.
- The host remains fully functional and safe when no client is connected; reconciliation does not depend on a client.
- Adding the network must not add a second lifecycle, a second Registry, or a second notion of Session identity.

**Non-Goals (design level, beyond the proposal's non-goals):**

- No offline queue of actions to replay when the host returns. An action either reaches the host and becomes a durable transition there, or it did not happen.
- No client-side cache that survives app restart. Last-known state lives only for the lifetime of an attachment.
- No protocol versioning scheme beyond a single version handshake that refuses mismatches. Host and client are expected to be upgraded together by the same person.
- No change to the TUI.

## Decisions

### D1: The seam is the existing `app` binding surface, extracted to an interface

The Wails-bound methods on `*App` become an interface (call it the host API) with two implementations: a local one that calls `core/` directly — today's code, moved, not rewritten — and a remote one that issues network requests. The frontend keeps calling the same generated bindings.

*Why:* the surface already exists, is already coarse-grained (one call per user-visible action), and is already free of pointers, callbacks, and filesystem paths for anything a browser could supply — ADR 0004's `WorktreeRef` and the `SpecificationStartToken` were introduced precisely so an untrusted frontend could not name a path. Those same properties are what makes a surface safe to expose over a network. Choosing a lower seam (exposing `core/` types directly) would export the Registry's internals; choosing a higher one (screen state) is the pixel trap the proposal rules out.

*Alternatives considered:* a fresh, purpose-built remote API — rejected, it would drift from the local surface and double the maintenance; exposing `core/` over the network directly — rejected, `core/` must stay unaware of the network so its coordination and reconciliation invariants stay locally reasoned.

### D2: Transport is HTTP/2 + TLS with WebSocket streams; requests are JSON

Unary requests are HTTP POSTs with JSON bodies (the same shapes Wails already marshals for the frontend). The streaming channel is a WebSocket carrying framed messages: terminal output frames (base64 payload, matching the existing `term:data:` encoding), status-event frames, and control frames.

*Why:* the payloads are already JSON because the frontend consumes them. WebSocket over TLS traverses Tailscale, LAN, and an SSH tunnel without extra machinery, and is the one streaming transport the existing web frontend can consume unchanged — which is the only path by which the proposal's conditional browser client can fall out for free.

*Alternatives considered:* gRPC — better typed and better flow-controlled, but it makes the browser-client path require grpc-web and a proxy, and forces a schema layer for shapes that are already JSON; SSH transport with Magentic speaking over a channel — attractive because it reuses existing key management, but it pushes multiplexing and reconnect logic into a place that is harder to observe, and rules out the browser client.

*Trade-off accepted:* JSON over WebSocket is less efficient than a binary frame format for terminal output. Terminal output is small; the coalescing budget in D5 caps the worst case.

### D3: Authentication is a pre-shared HostToken; TLS is self-signed and pinned

The host generates a HostToken at first run and prints it. The client stores it in the OS credential store and sends it as a bearer credential on every request and on the WebSocket handshake. The host generates a self-signed certificate; the client pins its fingerprint on first attach and refuses a changed fingerprint afterwards.

*Why:* the trust boundary is a single developer on a trusted overlay network. A token plus pinned TLS gives confidentiality and device authentication with zero infrastructure. Anything more — OIDC, a CA, per-user identity — presumes a user model the product explicitly does not have.

*Explicit trust assumptions, stated so they can be challenged later:* the overlay network is the perimeter; anyone holding a HostToken has the full permitted action surface, which includes typing arbitrary input into a shell; a compromised client device is a compromised host; there is no audit trail attributing actions to a person.

*Alternatives considered:* mutual TLS with client certificates — stronger, but certificate handling on a phone browser is hostile; SSH-only exposure with no Magentic-level auth — rejected because it makes the host's safety depend entirely on a deployment detail the host cannot verify, and because it forecloses the browser client.

### D4: Connection state is its own type, and availability is the existing ADR 0004 vocabulary

The client models a `HostSession` with an explicit connection state (attaching, attached, degraded, reconnecting, detached, refused). Host-derived facts reuse `ObservationAvailability`: when the connection is not attached, every host-derived fact is presented as unavailable with a last-known payload and its age.

*Why:* ADR 0004 already forbids translating probe failure into absence. A network failure is the same category of failure one layer out; giving it the same vocabulary means the frontend's existing handling of unavailable observations is what renders it, rather than a parallel code path that would be the natural place for the "all sessions dead" bug to appear.

*Consequence:* the frontend's existing rule — destroying or overwriting work needs fresh known facts — extends to network unavailability without a new rule. The gate is the same gate.

### D5: Terminal streaming keeps a bounded per-Session ring buffer and an explicit gap marker

The host keeps a bounded ring buffer of recent output per attached Session with a monotonically increasing sequence number. A reconnecting client asks to resume at its last sequence. If the sequence is still in the buffer, the host replays; if not, the host answers with a gap marker plus a fresh `capture-pane` snapshot, and the client replaces its terminal content rather than appending.

*Why:* the alternative — appending live output after a gap — produces scrollback that looks continuous but is not, which is worse than an honest reset when the reader is deciding whether an agent finished cleanly. The same principle as ADR 0004: say "unknown" rather than manufacture a plausible whole.

*Flow control:* if a client's send queue exceeds the budget, the host drops the oldest frames for that client and marks the stream gapped. A slow client must never block the pty reader; the pty reader must never block tmux.

### D6: Attention stays on the client; the host emits facts, not notifications

The host streams Observations and status events. The AttentionPlanner runs in the client process and produces notifications, Dock badge, and native attention there. While the connection is unavailable, the planner receives unavailable observations and therefore emits no per-Session intents; the connection state is surfaced instead.

*Why:* the developer sits at the client. A host-side notifier would fire into an empty room. Keeping the planner client-side also means its existing deduplication and suppression policy applies unchanged, and it is the natural place to add "the host is unreachable" as its own attention concern later.

### D7: Idempotency by transition identity, not by retry

Every action-carrying request carries the client-generated identity of the intended `LifecycleTransition`. The host, on seeing an identity it already holds, advances that transition instead of creating a second one. The client never auto-retries an action after an ambiguous failure; it reconnects, re-reads state, and shows the developer what actually happened.

*Why:* ADR 0003 already forbids replaying non-idempotent effects — initial prompt delivery in particular. A transport that retries on the developer's behalf is exactly the mechanism that would duplicate an instruction to an agent. The identity makes a deliberate re-submission safe; the absence of auto-retry makes an accidental one impossible.

### D8: RemoteActionPolicy is enforced on the host, advertised to the client

The host classifies each action as remotely permitted or restricted (see the host-service spec for the default split) and enforces it before any side effect. The client fetches the policy to gray out what it cannot do, and treats a host refusal as authoritative over its cached copy.

*Why:* the restricted set is exactly the set whose consequence a remote operator cannot see — a Worktree with uncommitted work, a Project directory whose contents they cannot inspect. Defaulting those off keeps the phone-in-the-pocket case from being able to lose work. Advertising the policy is a usability affordance only; enforcing it client-side would be no enforcement at all.

## Risks / Trade-offs

- **A HostToken is a remote shell.** Permitted actions include writing arbitrary bytes into an attached terminal. → Restrict binding to the overlay interface by default, pin TLS, support revocation, and state the assumption plainly in the host's own documentation rather than implying a security boundary that is not there.
- **Two `app` implementations drift.** A method added to the local one and forgotten in the remote one is a runtime hole. → Make the seam a Go interface so the compiler catches an unimplemented method, and add a test that asserts every bound method is classified in the RemoteActionPolicy.
- **Availability plumbing is easy to skip in the frontend.** A view that reads a last-known payload without reading its availability re-creates the "all sessions dead" bug. → Shape the client payload so the facts are unreachable without passing through the availability, and cover the disconnection cases with tests at the view level.
- **Terminal latency over a home uplink.** Round-trip echo on a mobile link is noticeably worse than local. → Accepted; terminal bytes are the cheapest honest format available, and the coalescing budget bounds the worst case. No local echo or prediction in this change — a predicted keystroke that the host never received would be a lie about delivery.
- **Clock skew makes "last known 4 minutes ago" wrong.** → Derive the age from the client's own monotonic clock since its last successful exchange, not from a host timestamp.
- **The host process is a new thing to keep running.** A host that dies leaves the agents alive in tmux but unreachable. → Acceptable and correct: tmux, not Magentic, owns the processes. The host must be restartable at any time without touching Sessions, which the durable Registry already permits.
- **A second Magentic process on the host (service plus a local desktop app) races on the Registry.** → Already handled by ADR 0002's interprocess coordination; this change adds no new writer, but the task list includes verifying it under a service plus a local app.

## Migration Plan

1. Extract the seam and ship the local implementation only. No behavior change, no network. Verifiable by the existing test suite plus the app running as before.
2. Add the host service behind an explicit opt-in flag or subcommand. Default off; a developer who never enables it sees no change and opens no port.
3. Add client mode. Local mode stays the default on every start; a HostLink must be selected deliberately.
4. Roll back by not starting the host service and staying in local mode. There is no durable state migration, no schema change to the Registry, and nothing to undo — HostLinks and HostTokens live entirely in client-side configuration and the OS credential store.

## Open Questions

- Whether the existing web frontend can be served directly by the host service as a browser client, or whether Wails-specific runtime bindings make that a separate build target. Answerable during step 3 without changing the specs: the browser client is conditional in the proposal, and the transport chosen in D2 is the one that keeps it possible either way.
- The retained window size for the per-Session ring buffer (D5). A number to tune against real Session output; the gap-marker behavior is specified and does not depend on the value.
