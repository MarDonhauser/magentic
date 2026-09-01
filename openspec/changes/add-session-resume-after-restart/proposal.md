## Why

Sessions run their coding agents in tmux, so they survive the UI closing — but not a machine reboot or a tmux server restart. After a reboot the developer is left with a list of Sessions marked dead and no honest way back: the tmux runtime is gone, and with it the visible thread of what four agents were doing. Nothing rescues a live process across an OS reboot, and tools that claim otherwise are overselling. What is genuinely rescuable is the Session's *metadata*: which Project and Worktree it worked in, which agent vendor it hosted, and which vendor conversation it belonged to. Reopening four terminals costs thirty seconds; rebuilding what four agents were doing costs twenty minutes. This change buys back the twenty minutes.

Magentic already stores most of what a resume needs — `Session` carries `ProjectID`, `Dir`, `Vendor`, and `AgentRuns`, and `AgentProvider.StartCommand` already builds a vendor-specific `resume` command line. What is missing is the honest reading of the situation after a restart: a Session whose runtime is gone but whose record is intact is presented today as `StatusDead`, indistinguishable from a Session the developer deliberately let go, and the only offered action is removal.

## What Changes

- Introduce **resumability** as an explicit reading of a Session: its durable record is complete enough to restart the agent where it left off, and its runtime is absent. This sits beside the existing Observation statuses rather than replacing them.
- Durably record, per Session, the facts a resume needs: Project, Worktree directory, agent kind (vendor), the vendor conversation/run reference, the Session name, and the **last known status together with the time it was observed**. The first four already exist in the `Session` record; the last-known status and its timestamp are new persisted facts.
- On startup, and on every Observation pass, classify a Session whose tmux runtime is absent but whose record is resumable as **resumable** rather than as dead. A record that cannot be resumed (no run reference, unknown vendor, missing working directory, terminal Session) stays dead, with the reason stated.
- Add a **Resume** action: a LifecycleTransition that recreates the tmux runtime in the recorded working directory and issues the agent-kind-specific resume command (for example `claude --resume <id>`), reusing the existing `AgentProvider.StartCommand(session, run, "resume")` mapping rather than introducing a second, parallel command table.
- Extend the per-agent-kind resume mapping so every supported vendor (Claude Code, Codex, Gemini CLI, GitHub Copilot) states explicitly whether it can resume a stored conversation, can only be restarted fresh in the right directory, or cannot be resumed at all. A vendor that cannot resume must offer "start fresh here", never a resume that silently loses the conversation.
- Add **Discard**: drop a resumable record deliberately, distinct from removing a running Session, so a stale record after a reboot can be cleared without ambiguity.
- Present resumable Sessions distinctly in the TUI and the desktop app: neither running (nothing is running) nor dead (nothing is lost), with the last known status and the time it was last seen shown so the developer can judge what to pick back up.

### Non-goals

- **Running processes do not survive a reboot.** Magentic resumes the conversation, not the process. No claim to the contrary may appear in UI copy, docs, or notifications.
- Rescuing unsaved in-flight agent work — an agent interrupted mid-turn by a reboot loses that turn; only what the vendor itself persisted comes back.
- Remote access, cloud state, or syncing Sessions across machines.
- Automatic resume on startup. Resume is always a deliberate, one-click developer action.
- Restoring window, pane, or tab layout inside a resumed tmux runtime.

## Capabilities

### New Capabilities
- `session-resume`: recording the durable resume facts of a Session, classifying a Session with an absent runtime as resumable or dead, resuming it into a fresh runtime with the vendor's own resume command, and discarding a resumable record.

### Modified Capabilities
<!-- No existing capability specs are published under openspec/specs/ yet; all
     behavior introduced here is captured in the new capability above. -->

## Impact

- `core/state.go`: `Session` gains persisted last-known-status and last-seen-status-time facts; state migration must treat their absence as unknown, never as a status.
- `core/status.go` / `core/observation.go`: an absent runtime is no longer flattened to `StatusDead`; the resumable reading and its reason are derived here.
- `core/provider.go` and the per-vendor Adapters: the resume capability of each vendor becomes explicit and inspectable, not implied by whether `StartCommand` happens to succeed.
- `core/lifecycle.go`: a Resume transition and a Discard transition, both following ADR 0003 — durable intent written before tmux is touched, advanced idempotently.
- `core/actions.go`, `core/overview.go`, `core/sidebar.go` and the TUI/desktop presentations: a new session state to render, and two new actions to offer.
- `README.md` and `CONTEXT.md`: the resumable reading is new domain vocabulary and belongs in both.
