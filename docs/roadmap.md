# Roadmap

Each milestone ships as a self-contained static Go binary ([ADR 0002](adr/0002-go-single-binary.md)) with automated tests over recorded provider fixtures (zero live subscription calls required in CI).

## Pre-v0.1 Gate (1-Hour Spike)

Before writing production code:
- [x] Run [`agent-tools-org/ai-dispatch`](https://github.com/agent-tools-org/ai-dispatch) hands-on for 1 hour on a scratch repo to verify whether its fallback preserves uncommitted git worktrees and resumes across providers, and confirm the build-our-own vs. contribute decision.
- [x] Finalize project name ([naming.md](naming.md) recommendation: `hiraishin`) and license (`MIT` vs. `Apache-2.0`).
- [ ] Capture live redacted fixtures for `kimi` (native CLI), `api.z.ai` (`glm-claude` + OpenCode), and `claude -p --output-format stream-json` limit errors.

---

## v0.1 — Core Ledger, Live Sensing, Policy Dispatch, and Worktree Failover

**Goal:** Replace manual scripts (`front.sh`, `agy-front.sh`, manual `prompt-kimi.md` failovers) on a multi-provider workstation with one auditable CLI.

### Scope
- **Inventory & Config:** `hiraishin init` detects installed binaries (`claude`, `kimi`, `opencode`, `agy`), prompts user to confirm plans/windows/tiers, writes `~/.config/hiraishin/preferences.toml`.
- **SQLite Event Ledger:** WAL-mode append-only `events` table plus projections (`providers`, `provider_windows`, `jobs`, `decisions`, `job_attempts`) per [architecture.md](architecture.md).
- **Sensors (v0.1 set):**
  - Claude Code (opt-in OAuth usage probe with `>= 120s` rate-limit guard + `~/.claude/` JSONL + `stream-json` regex).
  - Kimi Code (native `kimi` + OpenCode `kimi-worker`, `api.kimi.com/coding/v1/usages` + `5-hour usage limit` fixture).
  - Z.AI GLM (`glm-claude` via `api.z.ai/api/anthropic` + OpenCode `glm-worker`/`glm-strong`, `api.z.ai/api/monitor/usage/quota/limit` + `1113`/`1302` fixture).
  - Google Antigravity (`agy` output regex `429`/`RESOURCE_EXHAUSTED` vs. `401`/`UNAUTHENTICATED` + local log counter).
  - Optional `quota-axi --json` subprocess fallback behind `sensor.quota_axi_fallback = true`.
- **Scheduler & Preferences:** Stage 1 hard gates + Stage 2 scoring across all 4 built-in profiles (`quality-first`, `burn-expiring-first`, `save-scarce`, `balanced`) + `custom` weights; `hiraishin why <job_id>` full trace; runtime controls (`profile set`, `prefer`, `pin`, `pause`, `drain`, `resume`).
- **Dispatcher & Terminal Adapters:** `process` (PTY/pipes), `tmux`, and `orca` (`orca-ide` CLI) adapters; worktree checkpoint validation; structured failover resume prompt generator.
- **Failover Watcher:** `hiraishin watch` detects mid-job limit hits, stops the stalled CLI without losing uncommitted files, marks the window `EXHAUSTED`, and relaunches on the next allowed provider (respecting `never_switch_mid_task` and `WAITING_QUOTA`).

### v0.1 Acceptance Criteria
1. `hiraishin init` generates a valid `preferences.toml` that passes schema validation with zero secrets printed or written.
2. `hiraishin status` renders all configured providers, their states, window percentages/confidence tags, active slots, and time-to-reset in `< 500 ms` (reading ledger + async sensor refresh).
3. Recorded fixture test suite passes 100% for Claude, Kimi (native + OpenCode), GLM (`glm-claude` + OpenCode), and `agy` — distinguishing `EXHAUSTED`, `DEGRADED`, and `AUTH_ERROR` without false failovers.
4. End-to-end integration test (using mock CLI binaries in a real git worktree): a simulated provider writes uncommitted edits, emits Kimi's exact `429 You've reached your 5-hour usage limit`, and `hiraishin watch` automatically stops it, preserves every dirty file, launches Provider B in the same worktree with git status/diff + `report.md` in the resume prompt, and reaches `COMPLETED`.
5. `hiraishin why <job_id>` prints the exact Stage 1 rejection reasons and Stage 2 numeric score breakdown matching the `decisions` table row; `--json` output validates against a documented schema.
6. A provider marked `PAUSED` or `DRAINING` receives zero new dispatches; `DRAINING` automatically transitions to `PAUSED` when `active_jobs` hits `0`.
7. Running jobs never switch models when a previously exhausted provider resets; only `WAITING_QUOTA` or newly submitted jobs are scheduled onto the reset provider.

---

## v0.2 — MCP Server, Forecasting, Codex/Gemini, Bounded Learning, and Stack Integrations

**Goal:** Turn `hiraishin` into an agent-callable control plane with predictive burn-rate forecasting, full 6-provider coverage, bounded outcome learning, and optional `ai-memory`/`qmd`/`graphify` enrichment.

### Scope
- **MCP Server (`hiraishin mcp`):** Local stdio server exposing `hiraishin_status`, `hiraishin_why`, `hiraishin_pick_provider`, `hiraishin_dispatch`, `hiraishin_pause`, `hiraishin_drain`, `hiraishin_pin`. Forecasts ship inside `hiraishin_status` (each window carries its `forecast`), so there is no separate `hiraishin_forecast` tool.
- **Forecasting Engine:** EWMA burn rate (`pct_per_hour`), `time_to_empty`, `time_to_reset`, and `perishable_waste_risk` computed from `sensor.quota_sampled` history. A window forecast to run out before its reset is shown in `status`/`top` ("runs out in X, before reset"); `DEGRADED` itself stays rule-based (cooldown or `used_pct >= warn_used_pct`) so a forecast never blocks routing.
- **Additional Providers:**
  - OpenAI Codex CLI (`codex exec --json`, `codex exec resume`, `~/.codex/sessions/**/rollout-*.jsonl` sensor).
  - Google Gemini CLI (`gemini -p --output-format stream-json`, two-step 65s RPM vs. daily RPD probe, `America/Los_Angeles` midnight reset).
- **Native Go Sensors:** Port MIT-licensed first-party usage probes into native Go (preserving attribution) so zero Node dependency is needed for exact window sensing.
- **Bounded Learning:** `learning_stats` EWMA updates from `hiraishin outcome record` / `job.completed`, clamped strictly to `[-max_delta, +max_delta]` after `min_samples`, with `hiraishin learn status|freeze|unfreeze|reset`.
- **Optional Stack Integrations ([integrations.md](integrations.md)):**
  - `ai-memory` adapter (cut-off `ended: false` session observation recovery + episodic outcome pages in `hiraishin/outcomes/`).
  - `qmd` adapter (one-query `--no-rerank` context bundle for resumed agents).
  - `graphify` adapter (god-node/community task sizing that can raise `min_quality_tier` + 1,500-token 2-hop resume subgraph).

### v0.2 Acceptance Criteria
1. An MCP client (Claude Code, `agy`, or OpenCode) can call `hiraishin_status`, `hiraishin_pick_provider`, `hiraishin_dispatch`, and `hiraishin_why` over stdio without shelling out, receiving identical decisions to the CLI.
2. Forecast unit tests verify EWMA burn rate, `time_to_empty`, and DST-safe midnight Pacific reset calculations across 20+ synthetic window traces.
3. Codex and Gemini adapters pass recorded fixture suites, including Gemini's 65s RPM-vs-RPD confirmation test (a single 429 burst does not bench Gemini until midnight PT).
4. Property-based tests prove bounded learning can **never** violate any Stage 1 hard gate (deny list, allowed list, `min_quality_tier`, pin, metered opt-in, concurrency cap), never exceeds `±max_delta`, and stops changing scores immediately after `hiraishin learn freeze`.
5. Each stack integration (`ai-memory`, `qmd`, `graphify`) has both an integration test (mock server/index) verifying prompt enrichment and an absent-tool test proving clean fallback with zero errors and `< 50 ms` overhead when missing.

---

## v0.3 — Interactive TUI and Deterministic Policy Simulator

**Goal:** Provide real-time visual observability for multi-front operators and an offline replay simulator to benchmark scheduling policies empirically (powering [Blog Post 05](blog/05-simulator-results.md)).

### Scope
- **Interactive TUI (`hiraishin top` / `byakugan` view):** Real-time Bubble Tea terminal UI showing live provider window bars, burn-rate sparklines, reset countdowns, active jobs per worktree, recent failovers, and single-keystroke `pause`/`drain`/`profile` toggles.
- **Deterministic Replay Simulator (`hiraishin sim`):**
  - Reads an exported or live SQLite `events` ledger (job arrivals, durations, token/quota burn curves, window resets).
  - Replays the trace offline against two or more `preferences.toml` policies (`quality-first`, `burn-expiring-first`, `save-scarce`, `balanced`, static baseline).
  - Outputs side-by-side metrics (CSV/JSON/Markdown table): perishable quota utilized (%), expired wasted quota (%), total stall minutes, failover count, scarce-pool remaining reserve.
- **Hardened Operations:** Ledger compaction/archiving (`hiraishin ledger archive --before 30d`), `hiraishin doctor` comprehensive pre-flight check (binaries, auth freshness, git worktree health, integration reachability).

### v0.3 Acceptance Criteria
1. `hiraishin sim --ledger day.db --policies baseline.toml,burn-expiring.toml,save-scarce.toml` runs deterministically (bit-for-bit identical metrics across repeated runs) and replays a 24-hour 12-front ledger in `< 2 s`.
2. Simulator includes a checked-in anonymized fixture modeled on the 2026-11-21 12-front / 4-provider / 3-stall afternoon and outputs reproducible comparison tables ready for publication.
3. `hiraishin top` renders cleanly in 80×24 and wide terminals, updates within 1s of ledger events, and executes `pause`/`drain`/`profile` commands through the same ledger event path as the CLI.
4. `hiraishin doctor` accurately diagnoses missing binaries, expired auth (`AUTH_ERROR`), stale sensors, and optional integration status without printing any secret tokens.
