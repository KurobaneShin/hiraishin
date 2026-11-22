# hiraishin — Coding-Agent Quota Scheduler & Failover Orchestrator

> **Status: v0.1.** Ledger, sensors, scheduler with `why` traces, runtime controls, process/tmux/orca dispatch and worktree failover are implemented and covered by an end-to-end test against the [v0.1 acceptance criteria](docs/roadmap.md). Codex, Gemini, forecasting, learning and the MCP server are [v0.2](docs/roadmap.md).

`hiraishin` tracks the rolling usage windows of your flat-rate coding-agent subscriptions (Claude Max via Claude Code, Kimi Code, Z.AI GLM Coding Plan, Google Antigravity via `agy`, OpenAI Codex CLI, and Gemini CLI), forecasts when each window empties or resets, dispatches work across them according to your rules, and fails over mid-task without losing uncommitted progress.

## The Perishable-Quota Idea

Flat-rate coding-agent subscriptions behave like **perishable inventory**: airline seats, hotel rooms, or expiring compute windows. Every 5-hour or weekly quota that goes unused before its reset timestamp vanishes permanently.

Today, running several agents across multiple subscriptions creates two opposite wastes at the same time:

1. **Mid-task stalls:** A worker hits its 5-hour or daily limit halfway through a feature and sits idle until a human notices, creates a new terminal, and manually re-prompts another agent.
2. **Expiring capacity:** Meanwhile, a second subscription resets in 90 minutes with 60% of its window untouched.

`hiraishin` treats quota as perishable inventory bounded by quality rules you define. It routes work to expiring capacity first whenever that provider meets the task's quality bar, holds scarce high-tier quota for the roles that need it, and automatically relaunches a stalled worker in the same git worktree on the next allowed provider.

## Quickstart

```bash
git clone https://github.com/KurobaneShin/hiraishin && cd hiraishin
go install ./cmd/hiraishin            # Go 1.26+, CGO-free static binary

# 1. Detect installed CLIs (claude, kimi, opencode agents, agy) -> ~/.config/hiraishin/preferences.toml
#    API keys are stored only as env var names (api_key_env), never as values.
hiraishin init                        # --yes for non-interactive, --force to overwrite

# 2. Providers, states, window % with confidence, slots and time-to-reset (--json for scripts)
hiraishin status

# 3. Pick a scheduling objective (stored in the ledger, switchable at any time)
hiraishin profile set burn-expiring-first

# 4. Queue a job and launch the chosen provider in a git worktree (process, tmux or orca adapter)
hiraishin run --brief briefs/auth-fix/brief.md --worktree ../wt/auth-fix --role backend --adapter tmux

# 5. Follow running jobs: on a limit hit, stop the agent, keep every dirty file and relaunch
#    the next allowed provider in the same worktree with git status/diff + report.md
hiraishin watch
#    or keep it running as a systemd user service that starts with your session
#    (uses this binary and your current PATH; --uninstall removes it)
hiraishin watch --install

# 6. Ask why the scheduler chose that provider and rejected the others (--json, --all)
hiraishin why job_01J9X4K2

# 7. Override at runtime without editing config
hiraishin pause kimi-native        # also: drain, resume, prefer <p> --ttl 2h, pin <job> <p>, unpin, cancel <job>

# 8. Live view: window bars, usage trend, reset countdowns, jobs per worktree, recent failovers.
#    Keys: ↑↓ select a provider, p pause, d drain, r resume, tab next profile, q quit
hiraishin top
```

Exit codes: `0` success, `1` runtime error, `2` usage error (bad flag, argument or provider name).
Paths: `HIRAISHIN_CONFIG` (default `$XDG_CONFIG_HOME/hiraishin/preferences.toml`) and `HIRAISHIN_DATA_DIR` (default `$XDG_DATA_HOME/hiraishin`, holding `ledger.db` and per-attempt logs).

## Core Principles

- **No proxy.** Every model runs inside its own official CLI (or your configured harness such as OpenCode). `hiraishin` never relays API traffic, scrapes session cookies, or shares subscription credentials across tools ([ADR 0001](docs/adr/0001-no-proxy.md)).
- **Local only.** Prompts, source code, logs, and credentials stay on your machine. The ledger is a local SQLite database; no telemetry or cloud sync exists ([ADR 0006](docs/adr/0006-local-only.md)).
- **The worktree is the checkpoint.** Running work lives in a git worktree plus a progress report file. When a provider runs out mid-task, `hiraishin` stops the stalled session and launches the next allowed provider in the exact same worktree with a structured resume prompt ([ADR 0004](docs/adr/0004-worktree-as-checkpoint.md)).
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
