## Why

tmux does three jobs for Magentic at once: it owns the agent process and makes it outlive the interface, it renders the agent's terminal, and it is the delivery channel the Outbox sends prompts through. Only the second job is really tmux's. The other two are paid for with the limits that follow from driving an interactive program by keystrokes.

Those limits are concrete. A prompt is delivered with `send-keys` and nothing acknowledges it, so the Outbox learns whether a prompt arrived only by watching the pane afterwards. A permission prompt is answered in the agent's own terminal, so a developer who wants to grant one must attach — and `add-agent-timeline` reaches exactly this wall: the conversation surface can show everything an agent did, but not the question it is blocked on. There is no supported way to interrupt a turn, to change permission mode, or to know that a turn ended other than by reading a redrawn pane.

Claude Code offers the alternative directly. Verified against the installed CLI (2.1.259): `-p --input-format stream-json --output-format stream-json` gives a bidirectional protocol, `--include-partial-messages` gives token-level output, `--permission-prompts host` together with `--permission-prompt-tool` routes permission decisions to the host instead of a terminal, and `--replay-user-messages` acknowledges each delivered prompt. What is missing on Magentic's side is an owner for that process. `magentic serve` looks like one but is not: it serves the control socket and runs an Observation pass, while tmux still owns everything (`control_serve.go`).

This change makes the daemon the owner.

## What Changes

- Introduce **AgentRuntime** as domain vocabulary: the thing that owns a Session's coding-agent process, speaks that vendor's protocol, and outlives every interface. tmux becomes one AgentRuntime among two rather than the only way a Session can run.
- Make `magentic serve` a supervisor. It starts, owns and stops managed agent processes, keeps them running when every interface closes, and reclaims them on its own restart. The TUI and the desktop app become clients of it for those Sessions.
- Add the **managed** AgentRuntime for Claude Code: the agent runs headless under the daemon, its events are normalized into the Items introduced by `add-agent-timeline`, and its output streams rather than appearing per message.
- Route permission prompts to Magentic. A managed Session that needs a decision SHALL block, be observed as waiting for a decision, and raise the developer's attention through the existing Attention model. **No permission decision is ever made on the developer's behalf**, whether or not an interface is open.
- Give the Outbox real delivery acknowledgement for managed Sessions, replacing the unacknowledged `send-keys` path.
- Add turn control a keystroke cannot express: interrupt the current turn, and read a turn's start and end as facts rather than as a guess from pane content.
- Keep tmux for everything that has no protocol: terminal Sessions, and coding-agent Sessions whose vendor Magentic cannot yet drive headless. The runtime is a property of the Session, chosen when it is created — not a global switch. **BREAKING** for a managed Session: `attach` no longer opens it, because there is no tmux pane to attach to.
- Add a way back to a terminal for a managed Session, so the vendor's own interactive features stay reachable.
- Install `magentic serve` as a user service that starts at login, since durability now depends on the daemon running rather than on the tmux server.

### Non-goals

- **Sessions still do not survive a reboot.** The daemon reclaims its processes when it restarts; it does not rescue processes an OS restart killed. `add-session-resume-after-restart` covers what is genuinely rescuable.
- Driving Codex, Gemini CLI or GitHub Copilot headless. Their Sessions keep the tmux runtime, declared explicitly rather than by omission.
- Removing tmux. Terminal Sessions and the terminal dock are untouched.
- Remote or multi-machine access to the daemon.
- A second normalization model. Managed runtime events map onto the Items `add-agent-timeline` defines.
- Deciding permissions automatically, in any mode. A managed Session waits for a person.

## Capabilities

### New Capabilities

- `agent-runtime/runtime-selection`: AgentRuntime as an explicit property of a Session, which runtime a Session gets and why, and the actions each runtime does and does not offer.
- `agent-runtime/managed-process`: the daemon owning, supervising, reclaiming and stopping managed agent processes, and what happens to them when interfaces come and go.
- `agent-runtime/turn-control`: starting a turn with acknowledged delivery, observing its progress and end, and interrupting it.
- `agent-runtime/permission-decisions`: routing a vendor permission prompt to Magentic, blocking the Session until a person decides, and surfacing that wait.
- `agent-runtime/service-installation`: installing, inspecting and removing the login service that keeps the daemon running.

### Modified Capabilities

<!-- No capability specs are published under openspec/specs/ yet. The Item model
     this change produces events for is specified by add-agent-timeline, which
     must be archived first. -->

## Impact

- **Depends on `add-agent-timeline`.** Managed runtime events are normalized into the Items and Conversations that change defines; this one must not introduce a parallel model.
- `control_serve.go`, `core/control.go`, `core/control_socket.go`, `core/control_dispatch.go`: the daemon gains process ownership and the control vocabulary gains the verbs for it. Today's verbs address tmux Sessions only.
- `core/lifecycle.go`: creating, resuming and killing a Session becomes runtime-dependent. ADR 0003 still governs — durable intent before any runtime is touched.
- `core/provider.go`: each vendor declares which AgentRuntimes it supports, and the managed runtime needs an argument list per vendor rather than a shell command string.
- `core/outbox.go`: managed delivery is acknowledged; the tmux path stays for tmux Sessions.
- `core/observation.go` and `core/status.go`: a managed Session is observed from the daemon's own process facts, not from `tmux list-panes`, and "waiting for a permission decision" becomes an observable status.
- `core/attention.go`: a Session blocked on a permission decision is a new attention fact.
- `app/`, TUI and desktop: the approval surface, the interrupt action, and the absence of `attach` for managed Sessions.
- `core/state.go`: the Session record gains its runtime; absence must read as tmux so existing Sessions keep working.
- `CONTEXT.md`, `README.md`, `docs/adr/`: AgentRuntime is new vocabulary, the README's tmux promise needs rewording, and moving process ownership into the daemon deserves its own ADR.
