# aq — Coding-Agent Quota Scheduler & Failover Orchestrator

> **Status: Design phase.** This repository currently contains architecture specifications, provider research, preference schemas, ADRs, and a roadmap. No runnable binary exists yet. The working name `aq` is a placeholder until a final name is chosen ([naming shortlist](docs/naming.md)).

`aq` tracks the rolling usage windows of your flat-rate coding-agent subscriptions (Claude Max via Claude Code, Kimi Code, Z.AI GLM Coding Plan, Google Antigravity via `agy`, OpenAI Codex CLI, and Gemini CLI), forecasts when each window empties or resets, dispatches work across them according to your rules, and fails over mid-task without losing uncommitted progress.

## The Perishable-Quota Idea

Flat-rate coding-agent subscriptions behave like **perishable inventory**: airline seats, hotel rooms, or expiring compute windows. Every 5-hour or weekly quota that goes unused before its reset timestamp vanishes permanently.

Today, running several agents across multiple subscriptions creates two opposite wastes at the same time:

1. **Mid-task stalls:** A worker hits its 5-hour or daily limit halfway through a feature and sits idle until a human notices, creates a new terminal, and manually re-prompts another agent.
2. **Expiring capacity:** Meanwhile, a second subscription resets in 90 minutes with 60% of its window untouched.

`aq` treats quota as perishable inventory bounded by quality rules you define. It routes work to expiring capacity first whenever that provider meets the task's quality bar, holds scarce high-tier quota for the roles that need it, and automatically relaunches a stalled worker in the same git worktree on the next allowed provider.

## Intended CLI Quickstart

```bash
# 1. Detect installed CLIs and logins, confirm plans and windows -> ~/.config/aq/config.toml
aq init

# 2. Inspect live quota windows, burn rates, and time-to-reset across all providers
aq status

# 3. Pick a scheduling objective (switchable at any time)
aq profile set burn-expiring-first

# 4. Dispatch a task into a git worktree with automatic mid-task failover
aq run --role backend --worktree ../wt/auth-fix --brief briefs/auth-fix.md

# 5. Ask why the scheduler chose that provider (or rejected others)
aq why job_01J9X4K2

# 6. Watch running jobs, detect limit errors, and fail over in-place
aq watch --adapter orca

# 7. Override at runtime without editing config
aq pause kimi-native && aq pin job_01J9X4K2 claude-max
```

## Core Principles

- **No proxy.** Every model runs inside its own official CLI (or your configured harness such as OpenCode). `aq` never relays API traffic, scrapes session cookies, or shares subscription credentials across tools ([ADR 0001](docs/adr/0001-no-proxy.md)).
- **Local only.** Prompts, source code, logs, and credentials stay on your machine. The ledger is a local SQLite database; no telemetry or cloud sync exists ([ADR 0006](docs/adr/0006-local-only.md)).
- **The worktree is the checkpoint.** Running work lives in a git worktree plus a progress report file. When a provider runs out mid-task, `aq` stops the stalled session and launches the next allowed provider in the exact same worktree with a structured resume prompt ([ADR 0004](docs/adr/0004-worktree-as-checkpoint.md)).
- **The user decides.** Your inventory, quality floors, allow/deny lists, pins, and objective profile always win. Optional bounded learning can only reorder providers inside the list you explicitly allowed, can be frozen with one command, and every decision emits a readable `why` trace ([ADR 0005](docs/adr/0005-user-preferences-over-learned-scores.md)).
- **Orchestrator-agnostic.** Works over plain OS processes, `tmux`, and Orca terminals, and exposes a local MCP server over stdio so higher-level orchestrating agents can query quota state and dispatch work.

## Documentation Map

Start with the [design plan index](docs/plan.md) or jump straight into a spec:

- **Core design:** [Architecture](docs/architecture.md) · [User Preferences & Scoring](docs/preferences.md) · [Roadmap](docs/roadmap.md)
- **Providers & harnesses:** [Providers Index](docs/providers/README.md) (`claude-code`, `kimi`, `glm`, `antigravity-agy`, `codex`, `gemini-cli`)
- **Stack integrations:** [Integrations (`ai-memory`, `qmd`, `graphify`)](docs/integrations.md)
- **Context & decisions:** [Prior Art Survey](docs/prior-art.md) · [Architecture Decision Records](docs/adr/README.md) · [Naming Shortlist](docs/naming.md)
- **Writing:** [Blog Series Outlines](docs/blog/README.md)

## Target Stack

Single static Go binary ([ADR 0002](docs/adr/0002-go-single-binary.md)), pure-Go SQLite append-only event ledger ([ADR 0003](docs/adr/0003-sqlite-event-ledger.md)), TOML configuration, and MCP over stdio.
