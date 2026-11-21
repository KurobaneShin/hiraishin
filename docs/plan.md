# agent-quota (working name): plan

Status: design, nothing built. Started 2026-11-21 from the mmorpg orchestration (12 fronts on Claude, Kimi, GLM and Gemini Neon; three quota stalls and a full disk in one afternoon).

## Problem

People run several coding agents on flat-rate subscriptions (Claude Max, Kimi Code, Z.AI GLM Coding Plan, Antigravity/Gemini, Codex). Each has rolling windows (5 h, weekly, daily). When one runs out mid-task the work stalls until a human moves it; meanwhile other subscriptions expire unused at their reset.

## Core idea

Subscription quota is perishable inventory: what is not used before the reset is lost. Schedule work so expiring capacity is used first, within the quality bar the user sets for each kind of task, and move a running task to another agent without losing work when its provider runs out.

## Prior art (surveyed 2026-11-21, READMEs and GitHub metadata, nothing run)

| Project | Covers | Missing for us |
|---|---|---|
| ai-dispatch / aid (Rust, MIT, 12 stars) https://github.com/agent-tools-org/ai-dispatch | Dispatches tasks to agent CLIs (agy, codex, opencode, claude, ...), worktree per task, profiles (difficulty, budget, urgency), quota probes and 429 markers | Kimi/GLM only through opencode; worktree carry-over on fallback not verified; no perishable-quota scheduling stated. Closest overall: test it first |
| swrm / openswarm (Python, Apache-2.0, 6 stars) https://github.com/surlymochan/swrm | Routes tasks to the cheapest capable agent, worktree per run, 5h/7d/30d windows, ordered and weighted routing | No agy, Kimi, GLM; planner fixed to Claude |
| quota-axi (TypeScript, MIT, 179 stars) https://github.com/kunchenguid/quota-axi | Best sensor coverage: Claude, Codex, Cursor, Copilot, Grok, Kimi, Z.AI, agy, MiniMax, DeepSeek; first-party usage endpoints; runway sort | Display only, by design. Candidate sensor dependency |
| quotamux (Python, MIT, 0 stars) https://github.com/yandie-AI/quotamux | Picks the subscription with most quota left (Kimi, MiniMax, Anthropic usage endpoints) | Does not launch or move work; no worktrees |
| hydra (Go, MIT, 21 stars) https://github.com/saadnvd1/hydra and Tasuki (Go, MIT) https://github.com/0xkohe/tasuki | PTY wrapper, regex on output, hands context to the next CLI | Reactive only, one interactive session, no dispatch, no Kimi/GLM/agy |
| opencode-fallback, opencode-quota-failover | Retry the request on a backup model inside OpenCode | OpenCode only |
| OmniRoute (MIT, 89k stars), CLIProxyAPI (MIT, 59k stars), Quotio | Quota-aware routing | API proxies over subscription logins: terms-of-service risk and tool-call breakage. Out of scope by principle |
| CodexBar, ClaudeMeter, AgentLimits, modeldeck, ccusage | Show limits and reset times | Display only, mostly macOS or Windows tray |

Verdict: no project covers the whole case (perishable-first scheduling + mid-task failover with worktree resume + user-chosen, adaptive preferences + orchestrator-agnostic + Kimi/GLM/agy/Claude). Before building: run ai-dispatch hands-on for one hour (does fallback keep the worktree and resume?) and decide build-our-own vs contribute. If we build, reuse quota-axi as the sensor layer where its licence and API allow.

## Principles

- No proxy: each model runs in its own official CLI; the tool never relays API traffic or shares logins.
- Local only: no prompt, code or credential leaves the machine.
- The worktree is the checkpoint: git state plus a report file; failover relaunches in the same worktree with a resume prompt.
- Orchestrator-agnostic: adapters for plain process and tmux first, Orca next; MCP server so any orchestrating agent can ask and dispatch.
- Explainable: every decision has a "why" the user can read.

## User preferences: clear, chosen by the user, adaptive

The user decides; the tool adapts only inside what the user allowed, and always says why.

1. Inventory (`init` detects installed CLIs and logins; the user confirms): which subscriptions exist, their windows (auto-detected when a sensor can read them, else typed), max concurrent jobs, cost class (subscription, metered, free). Metered APIs are off unless the user turns them on.
2. Objective profile, one line, switchable any time: `quality-first`, `burn-expiring-first`, `save-scarce` (keep the scarcest quota for the tasks that need it), `balanced`, or custom weights (quality, expiry, scarcity, latency).
3. Task rules: per role or label (e.g. engine, ui, mechanical, review): allowed providers in the user's order, minimum quality tier, pins ("this task always on X"), deny lists, "never switch mid-task".
4. Learning, bounded: per (provider, role) outcome history (finished, review rounds, build failures, time to done) updates a score that may reorder providers inside the user's allowed list; never breaks a rule; can be frozen.
5. Runtime controls: `prefer`, `pin`, `pause <provider>`, `drain <provider>`, `why <job>`.
6. Presets shipped as examples (not defaults): "one subscription only", "Claude + Codex", "Claude + Kimi + GLM + Antigravity" (ours).

## Architecture

sensors -> event ledger (SQLite, append-only) -> provider state + forecast (burn rate, time to empty, time to reset) -> scheduler (policy + objective + learned scores) -> dispatcher -> harness adapters (claude, opencode, agy, codex, gemini) -> terminal adapters (process, tmux, Orca) -> failover watcher (limit detected -> relaunch in the same worktree with the next allowed provider and a resume prompt).

Sensors, most to least reliable: official usage endpoints; local logs (Claude JSONL, opencode store); terminal output patterns with recorded fixtures (real messages: Kimi "You've reached your 5-hour usage limit", agy 401/429, GLM limit); own token accounting.

Running jobs never change model on their own (prompt cache is per model) except on exhaustion; after a reset only new jobs go back to the preferred provider.

## Stack

Go single static binary (concurrent watchers, user's stack), pure-Go SQLite, TOML config, MCP over stdio. Licence MIT or Apache-2.0 (open).

## Versions

- v0.1: inventory + sensors (claude, opencode Kimi/GLM, agy), ledger, `status`, `run`, `watch` with failover, process/tmux/Orca adapters, policy TOML with profiles and task rules, `why`.
- v0.2: MCP server, forecasting, codex and gemini adapters, bounded learning.
- v0.3: TUI, simulator that replays a day of events and compares policies.

## Blog series

1. Subscription quota is perishable inventory.
2. Detecting limits from the outside: five CLIs without an API.
3. Failover without losing work: git worktrees as checkpoints.
4. Twelve agents on one workstation: disk, GPU and quota (real data from the mmorpg fronts).
5. Simulator results: quota used, stall minutes avoided, failovers.
6. Preferences that adapt without taking control away from the user.

## Harnesses to support (not only through OpenCode)

- Kimi: Moonshot's native Kimi CLI / Kimi Code, besides opencode with kimi-worker.
- GLM: the harnesses Z.AI ships or recommends for the GLM Coding Plan (its Claude-Code-compatible endpoint, its own CLI if any), besides opencode with glm-worker/glm-strong.
- The user uses OpenCode for convenience; the tool must not depend on it.

## Integrations with the user's stack (optional adapters)

- ai-memory: build the failover resume prompt from the dead session's observations and handoff; write job outcomes and provider decisions so learning and the user can query them.
- qmd: collections for briefs, reports and plans; a resumed agent gets its context in one query.
- graphify: size a task (files touched, centrality) to choose the quality tier; give a resumed agent a small graph context.

## Documentation Index

- [README](../README.md): overview, perishable-quota concept, 20-line intended CLI quickstart, principles, design-phase status.
- [Architecture](architecture.md): components, SQLite event-ledger schema, event types, provider & job state machines, and Mermaid sequence diagrams (dispatch, mid-job exhaustion, reset).
- [User Preferences & Scoring](preferences.md): full commented TOML schema, 3 complete example configs, scoring formula, bounded learning & freeze, `why` output format, and runtime commands.
- [Providers & Harnesses](providers/README.md): per-provider specifications (`claude-code`, `kimi` native + OpenCode, `glm` native/Claude-compatible + OpenCode, `antigravity-agy`, `codex`, `gemini-cli`) with launch/resume flags, windows, usage sensing, exact error fixtures, ToS quotes/URLs, and open risks.
- [Optional Stack Integrations](integrations.md): concrete adapter interfaces and absent-tool fallbacks for `ai-memory`, `qmd`, and `graphify`.
- [Prior Art Survey](prior-art.md): expanded feature matrix and reuse analysis (including `quota-axi` MIT license and CLI/library surface).
- [Architecture Decision Records](adr/README.md): Nygard-style ADRs (`0001` no proxy, `0002` Go single binary, `0003` SQLite event ledger, `0004` worktree as checkpoint, `0005` user preferences over learned scores, `0006` local only).
- [Blog Series Outlines](blog/README.md): outlines `01`–`06` with hooks, sections, figures/data, and real 2026-11-21 `mmorpg` case numbers.
- [Naming Shortlist](naming.md): anime/manga candidates, metaphors, GitHub/npm/crates/domain availability checks, and recommendation (`hiraishin`).
- [Roadmap](roadmap.md): v0.1 / v0.2 / v0.3 milestones and acceptance criteria.

## Naming

The user likes anime/manga references. Shortlist and availability checks in [docs/naming.md](naming.md) (recommended: `hiraishin`, with `byakugan` as an optional sensor-dashboard name).

## Open questions

- Final project name confirmation (`hiraishin` recommended in [docs/naming.md](naming.md); verify registrar availability before claiming).
- Licence: MIT or Apache-2.0.
- Public from the first commit or private until v0.1.
- Pre-v0.1 verification of verbatim Terms of Service quotes for Moonshot/Kimi, Z.AI Coding Plan, Google Antigravity, and Gemini Code Assist (marked `[unverified]` in [docs/providers/README.md](providers/README.md)).
- Live fixture capture for native `kimi`, `api.z.ai` error JSON, `codex exec --json`, and `gemini --output-format stream-json`.
- 1-hour hands-on test of `agent-tools-org/ai-dispatch` before starting v0.1 implementation ([docs/prior-art.md](prior-art.md)).

## Log

- 2026-11-21: pre-v0.1 gate done. Hands-on with `aid` v11.14.1: fallback keeps dirty files only as a WIP commit and relaunches in the same worktree, but with no resume context and no limit classification for the Kimi 429; no perishable scoring, native Kimi, `glm-claude`, tmux or Orca. Decision: build, not contribute (details in [prior-art.md](prior-art.md)).
- 2026-11-21: completed full design documentation pack (README, architecture, preferences, 6 provider specs, integrations, prior art, ADRs 0001–0006, 6 blog outlines, naming shortlist, roadmap) and linked every document from `plan.md`.
- 2026-11-21: added native Kimi/GLM harnesses, ai-memory/qmd/graphify integrations and anime naming; docs front aq-docs started (documentation only).
- 2026-11-21: survey of prior art; design and preference model drafted.
