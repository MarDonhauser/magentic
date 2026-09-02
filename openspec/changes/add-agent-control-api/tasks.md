## 1. Request vocabulary

- [x] 1.1 Define the control request and response types in a new `core/` control
      module — verb, arguments, request id, outcome code, result — and verify a
      table test round-trips every verb through JSON encode/decode unchanged
- [x] 1.2 Define the stable outcome-code set (success, not-found, ambiguous,
      containment, refused, unavailable, not-managed, plus the wait outcomes) and
      verify a test asserts every code string is unique and stable
- [x] 1.3 Implement Session addressing — SessionID, Project-qualified name, bare
      name — over the Registry, and verify tests cover resolution, the ambiguous
      bare name across two Projects, and the unknown Session case
- [x] 1.4 Implement Worktree scoping through Repositories with fresh resolution of
      the Project-qualified handle, and verify a test rejects a directory not
      physically contained in the addressed Project

## 2. Verb dispatch over the existing modules

- [x] 2.1 Implement `session list` against Registry plus Session Observation and
      verify a test shows an unavailable Observation is reported as unavailable,
      never as a concrete status
- [x] 2.2 Implement `session start` over Session Lifecycle (Project scope,
      existing Worktree, fresh managed Worktree, vendor selection, terminal
      Session) and verify tests cover each scope and an unsupported vendor
- [x] 2.3 Wire the optional initial prompt to Lifecycle's explicit applied-fact
      delivery and verify a test asserts an unknown delivery outcome is reported
      as unknown and never re-sent automatically
- [x] 2.4 Implement `session send` over the outbox and verify tests cover
      delivered, queued, and the refusal for a terminal Session
- [x] 2.5 Implement `session output` returning normalized content with the
      Observation availability, honoring a trailing-line limit, and verify tests
      cover ANSI stripping, the limit, and the unreadable-runtime case
- [x] 2.6 Implement `session kill` over Lifecycle and verify tests cover the
      running Session, the idempotent already-stopped case, and that a managed
      Worktree survives

## 3. Pinned wait

- [x] 3.1 Implement occupant resolution into the pinned triple (SessionID,
      RuntimeName, AgentRunRef) and verify tests cover a pinned coding-agent
      Session and the no-occupant refusal
- [x] 3.2 Implement the `done` and `waiting` conditions evaluated from
      Observation, and verify tests assert an unavailable or partial Observation
      satisfies neither condition
- [x] 3.3 Implement replacement detection and verify tests cover a recreated
      runtime under a new RuntimeName, a different AgentRunRef, a removed and
      re-created Session under the same name, and a replacement idling without
      satisfying a pending `done` wait
- [x] 3.4 Implement the terminal outcomes `blocked`, `session-gone`, `timeout`,
      and `cancelled`, and verify tests assert exactly one outcome per wait and
      that a confirmed-gone runtime never returns `done`
- [x] 3.5 Verify with a test that a pending wait holds no Registry coordination
      and no Session transition, and that several concurrent waits on one Session
      each resolve independently

## 4. Local socket server

- [x] 4.1 Implement socket creation in the user's runtime directory with
      owner-only permissions, stale-socket reclamation, and refusal to take over
      a live socket; verify tests cover the permission bits, the stale case, and
      the live case
- [x] 4.2 Implement peer-credential authorization against the owning user id and
      verify a test refuses a connection whose credentials differ
- [x] 4.3 Implement the line-delimited JSON request loop with request-id
      correlation and verify tests cover a malformed document, an unknown verb,
      and two sequential requests keeping the connection open
- [x] 4.4 Verify with a test that no TCP or otherwise network-reachable listener
      is opened by the control API

## 5. Event stream

- [x] 5.1 Emit status-transition and availability-change events by diffing each
      observation pass against the last observed state per Session, and verify a
      test asserts one event per real change and none for an unchanged pass
- [x] 5.2 Implement subscriptions with optional Project and Session filters and
      clean release on disconnect or unsubscribe, and verify tests cover
      filtering and release
- [x] 5.3 Implement bounded per-subscription buffering that drops a stalled
      subscriber with an explicit outcome, and verify a test shows the
      observation pass is never blocked by a non-reading subscriber
- [x] 5.4 Feed the pending waits from the same event fan-out and verify an
      integration test drives a wait to `done` purely through observation events

## 6. CLI front door

- [x] 6.1 Add the `magentic session` verb tree to the root package as a socket
      client, and verify a test asserts it performs no direct Registry, tmux, or
      Git access on its own path
- [x] 6.2 Implement the machine-readable output mode (exactly one JSON document
      on stdout, diagnostics on stderr) and verify tests parse the document for a
      success and a failure invocation
- [x] 6.3 Implement exit codes distinguishing success, refused/failed request,
      and addressing error, and verify a test covers each
- [x] 6.4 Report a distinct unavailable outcome naming the expected socket path
      when nothing serves, and verify a test asserts no Magentic process is
      started implicitly
- [x] 6.5 Extend `magentic --help` with the new verbs and verify the output lists
      every verb in the spec

## 7. Serving process and interfaces

- [x] 7.1 Start the control server from the TUI and the desktop app, with a way
      to disable it, and verify a manual run shows the socket answering while
      each interface runs
- [x] 7.2 Add a headless serve mode that serves the API without an interface, and
      verify a manual run shows a Session started through it surviving the client
      exiting
- [x] 7.3 Verify with a test that a control mutation and an interface action on
      the same Session are both applied as coordinated semantic changes with no
      lost update
- [x] 7.4 Verify with a test that `session list` and `session output` still answer
      while a long-running mutation holds a Session transition

## 8. Agent integration

- [x] 8.1 Export `MAGENTIC_ENV=1`, the socket path, SessionID, ProjectID, and the
      Worktree fact into every provisioned Session runtime, and verify tests cover
      an agent Session, a terminal Session, and an adopted runtime carrying no
      marker
- [x] 8.2 Implement the self-identification verb resolving the caller's claimed
      marker facts against the Registry, and verify tests cover a managed
      occupant and the `not-managed` outcome for unresolvable facts
- [x] 8.3 Verify with tests that a request addressing a Session in another Project
      without naming it is refused, and that a verb requiring a Session never
      substitutes the caller's own Session
- [x] 8.4 Write the shipped agent instruction file covering every verb, the
      addressing rules, all wait outcome codes, the marker guard, and the
      spawn-in-Worktree/wait/read delegation pattern; verify a test asserts the
      file names every verb and every outcome code defined in code
- [x] 8.5 Implement installing the instruction file into a Project idempotently
      and verify a test shows a second install does not duplicate its content

## 9. Documentation and end-to-end verification

- [x] 9.1 Add the control surface to `README.md` (verbs, socket, marker, skill
      installation) and verify the documented verbs match the implemented set
- [x] 9.2 Add the new Agent Control module to the architecture section of
      `README.md` and record the pinned-occupant wait decision as an ADR under
      `docs/adr/`, verified by the files existing and being referenced
- [x] 9.3 Run an end-to-end scenario — an agent inside a managed Session starts a
      reviewer Session in a fresh Worktree, waits for it, reads its output, kills
      it — and verify the Worktree survives and the wait reported `done`
- [x] 9.4 Run an end-to-end replacement scenario — kill and re-create the awaited
      Session while a wait is pending — and verify the wait ends
      `occupant-replaced`
