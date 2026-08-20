# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

The primary user is an individual developer coordinating multiple coding agents across multiple repositories on macOS. Magentic must support Claude Code, Codex, and other coding-agent tools rather than treating any single agent vendor as the product boundary.

## Product Purpose

Magentic gives one developer a persistent control center for starting, observing, and managing concurrent coding-agent work. Success means the developer can understand where attention is needed, move between repositories and sessions, and act without losing agent state or momentum.

## Positioning

Magentic unifies persistent terminal sessions, repository and worktree state, Git and specification visibility, embedded terminals, usage insights, and break management around the real workflow of supervising multiple coding agents.

## Operating Context

- Developers work across several local repositories and concurrent agent sessions.
- Agent processes run in tmux sessions so they can survive closing the interface.
- The product has a terminal UI and a Wails desktop app with a web-based interface.
- The desktop workflow includes project-grouped sessions, attention states, worktrees, Git history, specification boards, statistics, terminal tabs, and break reminders.
- The current desktop implementation targets macOS and integrates with native notifications and attention behavior.

## Capabilities and Constraints

- Interaction must feel fast and direct.
- The product must not depend on keyboard-heavy operation; pointer-driven use must remain efficient even where shortcuts are available.
- Agent integration must remain extensible beyond Claude Code to Codex and other tools.
- Existing session persistence and repository-aware workflows are core product behavior.
- The current interface copy is German. Whether German remains the sole or default language is undecided.
- The current implementation operates on local tmux sessions, Git repositories, transcripts, and configuration. Whether local-only operation is a permanent constraint is undecided.

## Brand Commitments

- The product name is **magentic**.

## Evidence on Hand

- The root `README.md` documents the implemented TUI and desktop workflows in detail.
- The Wails application is implemented under `app/`, with its frontend in `app/frontend/`.
- The shared Go domain and workflow logic lives under `core/`.
- Automated tests cover status, overview, board, Git graph, statistics, usage, and related core behavior.
- No external testimonials, customer evidence, pricing claims, or public benchmarks are present and future design work must not fabricate them.

## Product Principles

1. **Attention before administration.** Make it immediately clear which agent needs the developer and what action unblocks it.
2. **Persistent work, continuous context.** Sessions and their repository context should survive interface changes and interruptions.
3. **Fast by default.** Frequent monitoring and management actions should require minimal waiting and minimal interaction cost.
4. **Agent-agnostic orchestration.** Product concepts should describe coding-agent work without coupling the experience to one vendor.
5. **Multiple paths, equal competence.** Pointer interactions and keyboard shortcuts should both support efficient use; shortcuts must not be a prerequisite.
