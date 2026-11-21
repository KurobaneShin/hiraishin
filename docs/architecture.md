# Architecture

`hiraishin` is a local-first Go daemon and CLI ([ADR 0002](adr/0002-go-single-binary.md)) backed by an append-only SQLite event ledger ([ADR 0003](adr/0003-sqlite-event-ledger.md)). It never proxies model traffic ([ADR 0001](adr/0001-no-proxy.md)): every coding agent runs natively in its own official CLI (or user-configured harness such as OpenCode), inside a git worktree that serves as the recovery checkpoint ([ADR 0004](adr/0004-worktree-as-checkpoint.md)).

## Pipeline Overview

```mermaid
flowchart LR
    S[Sensors] -->|QuotaSample / LimitHit| L[(SQLite Event Ledger)]
    L --> PS[Provider State & Forecast]
    C[preferences.toml] --> SC[Scheduler]
    PS --> SC
    INT["Optional Integrations\n(ai-memory / qmd / graphify)"] --> SC
    INT --> D[Dispatcher]
    SC -->|Decision + WhyTrace| L
    SC -->|DispatchPlan| D
    D --> HA["Harness Adapters\n(claude, kimi, glm, agy, codex, gemini, opencode)"]
    HA --> TA["Terminal Adapters\n(process, tmux, orca)"]
    TA --> FW[Failover Watcher]
    FW -->|LimitHit / Exit| L
    FW -->|Failover Request| SC
    MCP[MCP Server stdio] <--> SC
    MCP <--> L
```

## Components

| Component | Package | Responsibility |
|---|---|---|
| **Sensors** | `internal/sensor` | Collect quota utilization, reset timestamps, and rate-limit signals from a provider using a tiered reliability chain (first-party usage endpoint -> local CLI logs/DB -> terminal output regex with recorded fixtures -> local token estimator). Never scrapes web cookies or proxies inference. |
| **Event Ledger** | `internal/ledger` | Append-only SQLite store (WAL mode, pure-Go `modernc.org/sqlite`). Persists every sample, decision, `why` trace, job transition, failover, and outcome. Projections (`provider_windows`, `jobs`, `learning_stats`) are rebuilt or updated transactionally from events. |
| **Forecast** (v0.2) | `internal/forecast` | Pure functions over recent `QuotaSample` events and active job counts. Computes exponentially weighted burn rate (`pct_per_hour`), `time_to_empty`, `time_to_reset`, and `perishable_waste_risk` (fraction of remaining window projected to expire unused before reset). |
| **Scheduler** | `internal/scheduler` | Evaluates hard policy gates (inventory enabled, concurrency cap, deny list, min quality tier, pin, cooldown) and scores eligible providers with the active objective profile plus bounded learned adjustments ([preferences.md](preferences.md)). Always emits a structured `Decision` with a human-readable `WhyTrace`. |
| **Dispatcher** | `internal/dispatch` | Prepares the job's git worktree, builds either the initial launch prompt or a failover resume prompt (pulling git status/diff, report file, and optional `ai-memory`/`qmd`/`graphify` context), selects the harness command line, and hands execution to a terminal adapter. |
| **Harness Adapters** | `internal/dispatch` (`harness.go`) | Provider-specific translators (`claude-code`, `kimi-native`, `glm-claude`, `opencode`, `antigravity-agy`, `codex`, `gemini-cli`). Encapsulate CLI flags for headless vs. interactive mode, session ID extraction, native resume flags, environment variables, and limit-error regex patterns. |
| **Terminal Adapters** | `internal/dispatch` (`adapter.go`) | Execution surfaces (`process` via `os/exec` in its own session, `tmux` window in session `hiraishin`, `orca` via `orca-ide terminal create`). Each runs the same per-attempt `run.sh` wrapper, which records the process group, tees `output.log` and writes the exit code, so `Stop` (SIGINT, SIGTERM, SIGKILL on the group) and output tailing are uniform across adapters. |
| **Failover Watcher** | `internal/watch` | Subscribes to terminal output streams and process exit codes for every running job. Matches harness-specific limit/auth/quota error patterns, records a `provider.limit_hit` event, marks the window `EXHAUSTED`, stops the stalled process cleanly (preserving uncommitted files), and triggers re-scheduling on the next allowed provider unless `never_switch_mid_task` is set. |
| **Engine** | `internal/engine` | Glue used by the CLI and the watcher: opens config + ledger, syncs configured providers, emits maintenance events (cooldown recovery, window resets), turns scheduler decisions into ledger events and launches attempts. |
| **MCP Server** (v0.2) | `internal/mcp` | Local stdio MCP server exposing read-only status (`hiraishin_status`, `hiraishin_why`, `hiraishin_forecast`) and orchestration actions (`hiraishin_pick_provider`, `hiraishin_dispatch`, `hiraishin_pause`, `hiraishin_drain`, `hiraishin_pin`) so a supervisor agent can coordinate fleets without shelling out. |

### Key Scheduling Invariants

1. **No mid-task switching unless exhausted.** Prompt caches are per-model and switching models mid-stream wastes context and causes drift. A running job stays on its assigned provider until completion, cancellation, or provider exhaustion.
2. **Reset returns new work only.** When an exhausted provider's window resets, it transitions back to `AVAILABLE` and becomes eligible for newly dispatched jobs (and future failovers); already-running jobs on a fallback provider are not disrupted.
3. **Bounded concurrency per provider.** Each provider has an explicit `max_concurrent` slot cap enforced atomically in the ledger transaction before launch.

## Data Model (SQLite)

Database path: `${XDG_DATA_HOME:-~/.local/share}/hiraishin/ledger.db` (WAL mode, `busy_timeout = 5000`, `foreign_keys = ON`).

### `events` (append-only source of truth)

| Column | Type | Constraints | Description |
|---|---|---|---|
| `seq` | `INTEGER` | `PRIMARY KEY AUTOINCREMENT` | Strictly monotonic event sequence number |
| `event_id` | `TEXT` | `NOT NULL UNIQUE` | ULID for idempotency and external references |
| `ts` | `TEXT` | `NOT NULL` | RFC3339Nano UTC timestamp |
| `kind` | `TEXT` | `NOT NULL` | Event type (see Event Types below) |
| `provider_id` | `TEXT` | `NULL` | Provider identifier (e.g. `claude-max`, `kimi-native`) |
| `job_id` | `TEXT` | `NULL` | Job ULID when event relates to a job |
| `source` | `TEXT` | `NOT NULL` | Origin (`sensor:endpoint`, `sensor:log`, `watcher:regex`, `scheduler`, `cli`, `mcp`) |
| `payload_json` | `TEXT` | `NOT NULL` | Canonical JSON payload for `kind` |

Indexes: `idx_events_ts(ts)`, `idx_events_provider_ts(provider_id, ts)`, `idx_events_job_ts(job_id, ts)`, `idx_events_kind_ts(kind, ts)`.

### `providers` (materialized provider state)

| Column | Type | Constraints | Description |
|---|---|---|---|
| `provider_id` | `TEXT` | `PRIMARY KEY` | Configured provider ID from `preferences.toml` |
| `harness` | `TEXT` | `NOT NULL` | Adapter name (`claude-code`, `kimi-native`, `opencode`, `antigravity-agy`, etc.) |
| `state` | `TEXT` | `NOT NULL` | `AVAILABLE`, `DEGRADED`, `DRAINING`, `PAUSED`, `EXHAUSTED`, `AUTH_ERROR`, `UNAVAILABLE` |
| `active_jobs` | `INTEGER` | `NOT NULL DEFAULT 0` | Count of currently running jobs on this provider |
| `max_concurrent` | `INTEGER` | `NOT NULL` | Configured concurrency limit |
| `cooldown_until` | `TEXT` | `NULL` | RFC3339 UTC timestamp when transient backoff ends |
| `last_error` | `TEXT` | `NULL` | Last matched limit/auth error snippet |
| `updated_at` | `TEXT` | `NOT NULL` | Last projection update timestamp |

### `provider_windows` (current rolling/fixed quota windows)

| Column | Type | Constraints | Description |
|---|---|---|---|
| `provider_id` | `TEXT` | `NOT NULL REFERENCES providers(provider_id)` | Owner provider |
| `window_name` | `TEXT` | `NOT NULL` | e.g. `5h`, `weekly`, `daily_rpd`, `rpm` |
| `window_kind` | `TEXT` | `NOT NULL` | `rolling`, `fixed_reset`, `credit_pool`, `metered` |
| `used_pct` | `REAL` | `NULL` | Observed utilization `0.0..100.0` (`NULL` if unknown) |
| `remaining_pct` | `REAL` | `NULL` | `100.0 - used_pct` when known |
| `resets_at` | `TEXT` | `NULL` | RFC3339 UTC expected reset timestamp |
| `burn_rate_pct_hr` | `REAL` | `NULL` | EWMA burn rate (`% / hour`) |
| `confidence` | `TEXT` | `NOT NULL` | `exact` (official endpoint), `estimated` (logs/tokens), `inferred` (limit hit only) |
| `sampled_at` | `TEXT` | `NOT NULL` | Timestamp of last sensor observation |

Primary key: `(provider_id, window_name)`.

### `jobs` (materialized job state)

| Column | Type | Constraints | Description |
|---|---|---|---|
| `job_id` | `TEXT` | `PRIMARY KEY` | ULID (`job_...`) |
| `name` | `TEXT` | `NOT NULL` | Human slug (e.g. `loot-drops`, `persist-all`) |
| `role` | `TEXT` | `NOT NULL` | Task role matching policy rules (e.g. `engine`, `ui`, `mechanical`, `docs`) |
| `state` | `TEXT` | `NOT NULL` | `QUEUED`, `DISPATCHING`, `RUNNING`, `FAILING_OVER`, `WAITING_QUOTA`, `COMPLETED`, `FAILED`, `CANCELLED` |
| `worktree_path` | `TEXT` | `NOT NULL` | Absolute path to git worktree checkpoint |
| `branch` | `TEXT` | `NOT NULL` | Git branch checked out in `worktree_path` |
| `brief_path` | `TEXT` | `NOT NULL` | Path to task brief |
| `report_path` | `TEXT` | `NULL` | Path to progress report file (checkpoint complement) |
| `current_provider` | `TEXT` | `NULL REFERENCES providers(provider_id)` | Provider currently executing the job |
| `terminal_adapter` | `TEXT` | `NOT NULL` | `process`, `tmux`, or `orca` |
| `terminal_handle` | `TEXT` | `NULL` | PID, tmux pane ID, or Orca terminal handle |
| `native_session_id` | `TEXT` | `NULL` | Harness session/conversation ID for resume or `ai-memory` lookup |
| `pinned_provider` | `TEXT` | `NULL` | Runtime pin override if set |
| `never_switch` | `INTEGER` | `NOT NULL DEFAULT 0` | Boolean: block automatic mid-task failover |
| `failover_count` | `INTEGER` | `NOT NULL DEFAULT 0` | Number of failovers completed |
| `created_at` | `TEXT` | `NOT NULL` | Creation timestamp |
| `updated_at` | `TEXT` | `NOT NULL` | Last state change timestamp |

### `decisions` (explainable scheduling audit trail)

| Column | Type | Constraints | Description |
|---|---|---|---|
| `decision_id` | `TEXT` | `PRIMARY KEY` | ULID (`dec_...`) |
| `job_id` | `TEXT` | `NOT NULL REFERENCES jobs(job_id)` | Target job |
| `trigger` | `TEXT` | `NOT NULL` | `initial_dispatch`, `failover`, `retry_after_reset`, `dry_run` |
| `profile` | `TEXT` | `NOT NULL` | Active objective profile name |
| `chosen_provider` | `TEXT` | `NULL` | Selected provider (`NULL` if no eligible provider -> `WAITING_QUOTA`) |
| `candidates_json` | `TEXT` | `NOT NULL` | Full JSON array of every candidate's score breakdown and hard-gate pass/reject reasons |
| `summary` | `TEXT` | `NOT NULL` | Single-paragraph human explanation rendered by `hiraishin why` |
| `created_at` | `TEXT` | `NOT NULL` | Decision timestamp |

### `job_attempts` (per-provider execution segments)

| Column | Type | Constraints | Description |
|---|---|---|---|
| `attempt_id` | `TEXT` | `PRIMARY KEY` | ULID |
| `job_id` | `TEXT` | `NOT NULL REFERENCES jobs(job_id)` | Parent job |
| `attempt_num` | `INTEGER` | `NOT NULL` | 1-based attempt counter |
| `provider_id` | `TEXT` | `NOT NULL` | Provider used for this segment |
| `decision_id` | `TEXT` | `NOT NULL REFERENCES decisions(decision_id)` | Decision that selected this provider |
| `start_commit` | `TEXT` | `NULL` | `HEAD` SHA when attempt started |
| `end_commit` | `TEXT` | `NULL` | `HEAD` SHA when attempt stopped |
| `exit_reason` | `TEXT` | `NULL` | `completed`, `quota_exhausted`, `auth_error`, `process_error`, `timeout`, `user_stop` |
| `started_at` | `TEXT` | `NOT NULL` | Start timestamp |
| `ended_at` | `TEXT` | `NULL` | End timestamp |

### `learning_stats` (bounded per-(provider, role) outcome aggregates)

| Column | Type | Constraints | Description |
|---|---|---|---|
| `provider_id` | `TEXT` | `NOT NULL` | Provider ID |
| `role` | `TEXT` | `NOT NULL` | Task role |
| `samples` | `INTEGER` | `NOT NULL DEFAULT 0` | Completed outcome count |
| `completed_ok` | `INTEGER` | `NOT NULL DEFAULT 0` | Finished without human takeover |
| `avg_review_rounds` | `REAL` | `NOT NULL DEFAULT 0.0` | EWMA of review/fix rounds before merge |
| `avg_build_failures` | `REAL` | `NOT NULL DEFAULT 0.0` | EWMA of build/gate failures |
| `avg_duration_min` | `REAL` | `NOT NULL DEFAULT 0.0` | EWMA wall minutes to completion |
| `bounded_delta` | `REAL` | `NOT NULL DEFAULT 0.0` | Clamped adjustment in `[-max_delta, +max_delta]` |
| `frozen` | `INTEGER` | `NOT NULL DEFAULT 0` | `1` if frozen globally or for this pair |
| `updated_at` | `TEXT` | `NOT NULL` | Last update timestamp |

Primary key: `(provider_id, role)`.

## Event Types

Every mutation enters `events` first.

| Category | Event Kind | Producer | Key Payload Fields |
|---|---|---|---|
| Sensor | `sensor.quota_sampled` | Sensors | `window_name`, `used_pct`, `resets_at`, `confidence`, `raw_tokens` |
| Sensor | `sensor.probe_failed` | Sensors | `sensor_tier`, `error`, `http_status` |
| Provider | `provider.configured` | Engine (config sync) | `harness`, `max_concurrent` |
| Provider | `provider.degraded` | Watcher | `matched_pattern`, `excerpt`, `cooldown_until` |
| Provider | `provider.recovered` | Engine (cooldown expiry) | `previous_state` |
| Provider | `provider.limit_hit` | Watcher / Sensor | `window_name`, `matched_pattern`, `excerpt`, `estimated_reset_at` |
| Provider | `provider.auth_failed` | Watcher / Sensor | `http_status`, `excerpt` |
| Provider | `provider.window_reset` | Forecast / Sensor | `window_name`, `previous_state`, `resets_at_next` |
| Provider | `provider.paused` | CLI / MCP | `reason`, `by` |
| Provider | `provider.resumed` | CLI / MCP | `by` |
| Provider | `provider.draining` | CLI / MCP | `reason`, `by` |
| Runtime | `runtime.profile_set` | CLI | `profile`, `by` |
| Runtime | `runtime.prefer_set` / `runtime.prefer_cleared` | CLI | `role`, `expires_at`, `by` |
| Scheduler | `scheduler.decision_made` | Scheduler | `decision_id`, `trigger`, `profile`, `role`, `min_quality_tier`, `weights`, `chosen_provider`, `candidates`, `summary`, `earliest_reset_at`, `blocked_providers` |
| Job | `job.queued` | CLI / MCP | `name`, `role`, `worktree_path`, `branch`, `brief_path`, `size_tier` |
| Job | `job.dispatched` | Dispatcher | `attempt_id`, `provider_id`, `terminal_adapter`, `terminal_handle`, `start_commit` |
| Job | `job.spawn_failed` | Dispatcher | `decision_id`, `error`, `retryable` |
| Job | `job.session_detected` | Watcher | `native_session_id` |
| Job | `job.failover_started` | Watcher | `from_provider`, `reason`, `dirty_files`, `head_commit` |
| Job | `job.waiting_quota` | Scheduler | `earliest_reset_at`, `blocked_providers` |
| Job | `job.completed` | Watcher / CLI | `end_commit`, `duration_min`, `pr_url` |
| Job | `job.failed` | Watcher / CLI | `exit_code`, `reason` |
| Job | `job.cancelled` | CLI / MCP | `by`, `reason` |
| Job | `job.pinned` / `job.unpinned` | CLI | `provider` / `never_switch` |
| Learning | `learning.outcome_recorded` | CLI / MCP | `provider_id`, `role`, `success`, `review_rounds`, `build_failures`, `duration_min` |
| Learning | `learning.frozen` / `learning.unfrozen` | CLI / MCP | `scope` (`global` or `provider:role`) |

## State Machines

### Provider State Machine

```mermaid
stateDiagram-v2
    [*] --> AVAILABLE: init / sensor ok

    AVAILABLE --> DEGRADED: used_pct >= warn_threshold\nor sensor probe flaky
    DEGRADED --> AVAILABLE: window reset / probe recovers

    AVAILABLE --> EXHAUSTED: limit_hit OR used_pct >= 100
    DEGRADED --> EXHAUSTED: limit_hit OR used_pct >= 100
    EXHAUSTED --> AVAILABLE: resets_at reached AND probe ok

    AVAILABLE --> AUTH_ERROR: 401 / UNAUTHENTICATED / expired login
    DEGRADED --> AUTH_ERROR: 401 / UNAUTHENTICATED
    AUTH_ERROR --> AVAILABLE: user re-auths AND probe ok

    AVAILABLE --> DRAINING: hiraishin drain <provider>
    DEGRADED --> DRAINING: hiraishin drain <provider>
    DRAINING --> PAUSED: active_jobs == 0
    DRAINING --> AVAILABLE: hiraishin resume <provider>

    AVAILABLE --> PAUSED: hiraishin pause <provider>
    DEGRADED --> PAUSED: hiraishin pause <provider>
    EXHAUSTED --> PAUSED: hiraishin pause <provider>
    PAUSED --> AVAILABLE: hiraishin resume <provider> (if quota > 0)
    PAUSED --> EXHAUSTED: hiraishin resume <provider> (if window still empty)

    AVAILABLE --> UNAVAILABLE: binary missing / harness broken
    UNAVAILABLE --> AVAILABLE: doctor check passes
```

State semantics:
- `AVAILABLE`: eligible for new dispatches and failovers.
- `DEGRADED`: still eligible (with optional penalty), approaching exhaustion or falling back to lower-confidence sensor.
- `DRAINING`: running jobs finish normally; no new jobs or failovers are assigned. Automatically settles into `PAUSED` once `active_jobs == 0`.
- `PAUSED`: operator-suspended; ignored by scheduler regardless of quota.
- `EXHAUSTED`: hard-blocked until `resets_at` passes and a lightweight check confirms recovery (or operator overrides).
- `AUTH_ERROR`: hard-blocked; requires human login refresh (`claude auth login`, `agy`, etc.). Never auto-retried in a tight loop.

### Job State Machine

```mermaid
stateDiagram-v2
    [*] --> QUEUED: hiraishin run / MCP dispatch

    QUEUED --> DISPATCHING: scheduler picks provider
    QUEUED --> WAITING_QUOTA: no eligible provider available

    WAITING_QUOTA --> DISPATCHING: provider window resets\nor operator unpauses/changes rule
    WAITING_QUOTA --> CANCELLED: hiraishin cancel

    DISPATCHING --> RUNNING: terminal spawned & slot locked
    DISPATCHING --> QUEUED: transient spawn error (retryable)
    DISPATCHING --> FAILED: invalid worktree or fatal launch error

    RUNNING --> COMPLETED: exit 0 & completion marker (e.g. report/PR)
    RUNNING --> FAILING_OVER: watcher detects quota exhaustion\n(and never_switch == false)
    RUNNING --> WAITING_QUOTA: watcher detects quota exhaustion\n(and never_switch == true)
    RUNNING --> FAILED: non-quota crash exceeding retry cap
    RUNNING --> CANCELLED: hiraishin cancel

    FAILING_OVER --> DISPATCHING: next allowed provider picked\n& resume prompt built
    FAILING_OVER --> WAITING_QUOTA: all allowed providers exhausted/paused
    FAILING_OVER --> CANCELLED: hiraishin cancel

    COMPLETED --> [*]
    FAILED --> [*]
    CANCELLED --> [*]
```

## Sequence Diagrams

### 1. Normal Dispatch

```mermaid
sequenceDiagram
    actor User as User / Orchestrator
    participant CLI as hiraishin CLI / MCP
    participant Int as Integrations (graphify / qmd)
    participant Sched as Scheduler
    participant Ledger as SQLite Ledger
    participant Disp as Dispatcher
    participant Term as Terminal Adapter (Orca/tmux/proc)
    participant Agent as Harness CLI

    User->>CLI: hiraishin run --role backend --worktree ../wt/feat --brief brief.md
    CLI->>Ledger: append job.queued
    opt graphify enabled
        CLI->>Int: size_task(worktree, brief)
        Int-->>CLI: touched_files, centrality -> suggested quality_tier
    end
    CLI->>Sched: SelectProvider(job, active_profile)
    Sched->>Ledger: read provider_windows, providers, learning_stats
    Sched->>Sched: filter hard gates -> compute weighted score -> build WhyTrace
    Sched->>Ledger: append scheduler.decision_made + lock concurrency slot
    Sched-->>CLI: Decision(provider=kimi-native, decision_id)
    CLI->>Disp: Launch(job, Decision)
    Disp->>Disp: inspect git HEAD + render harness args
    Disp->>Term: Spawn(worktree, cmd, prompt)
    Term->>Agent: exec kimi --prompt ...
    Term-->>Disp: terminal_handle
    Disp->>Ledger: append job.dispatched (state=RUNNING)
    CLI-->>User: Dispatched job_01J9X4K2 on kimi-native (see: hiraishin why job_01J9X4K2)
```

### 2. Exhaustion Mid-Job & Worktree Failover

```mermaid
sequenceDiagram
    participant AgentA as Provider A (kimi-native)
    participant Watch as Failover Watcher
    participant Ledger as SQLite Ledger
    participant Sched as Scheduler
    participant Int as Integrations (ai-memory / qmd / graphify)
    participant Disp as Dispatcher
    participant Term as Terminal Adapter
    participant AgentB as Provider B (agy / gemini-4-neon-medium)

    AgentA-->>Watch: stderr/PTY: "429 You've reached your 5-hour usage limit"
    Watch->>Watch: match harness fixture regex (kimi.limit_5h)
    Watch->>Ledger: append provider.limit_hit (kimi-native -> EXHAUSTED)
    Watch->>Ledger: append job.failover_started (state=FAILING_OVER)
    Watch->>Term: Stop(handle_A) [SIGINT -> grace -> SIGTERM, keep worktree intact]
    Watch->>Sched: SelectFallbackProvider(job, exclude=[kimi-native])
    Sched->>Ledger: read remaining providers & windows
    Sched->>Ledger: append scheduler.decision_made (chosen=antigravity-agy)
    Sched-->>Watch: Decision(provider=antigravity-agy)
    Watch->>Disp: ResumeInWorktree(job, Decision)
    Disp->>Disp: git status --porcelain, git diff --stat, read report.md
    opt ai-memory enabled
        Disp->>Int: memory_read_session_observations(session_A, order="desc")
        Int-->>Disp: last tool calls & stops from dead session
    end
    opt qmd / graphify enabled
        Disp->>Int: fetch relevant brief snippets & 2-hop graph around modified files
        Int-->>Disp: compact resume context
    end
    Disp->>Disp: write resume prompt referencing worktree state + brief + report
    Disp->>Term: Spawn(same worktree, agy --model gemini-4-neon-medium --print=...)
    Term->>AgentB: start in same git worktree
    Disp->>Ledger: append job.dispatched (attempt=2, state=RUNNING)
```

### 3. Window Reset & Queued Job Wakeup

```mermaid
sequenceDiagram
    participant Timer as Reset Timer / Sensor Loop
    participant Sensor as Provider Sensor
    participant Ledger as SQLite Ledger
    participant Sched as Scheduler
    participant Disp as Dispatcher

    Timer->>Timer: now >= kimi-native.windows[5h].resets_at
    Timer->>Sensor: Probe(kimi-native)
    Sensor-->>Timer: QuotaSample(window=5h, used_pct=0.0, resets_at=+5h, confidence=exact)
    Timer->>Ledger: append sensor.quota_sampled + provider.window_reset (state=AVAILABLE)
    Note over Ledger: Running jobs on fallback providers are NOT interrupted
    Timer->>Ledger: query jobs WHERE state = 'WAITING_QUOTA' ORDER BY created_at
    alt waiting jobs exist
        Timer->>Sched: SelectProvider(waiting_job)
        Sched->>Ledger: append scheduler.decision_made (trigger=retry_after_reset)
        Sched-->>Timer: Decision(provider=kimi-native)
        Timer->>Disp: ResumeInWorktree(waiting_job, Decision)
        Disp->>Ledger: append job.dispatched (state=RUNNING)
    else no waiting jobs
        Note over Timer: kimi-native ready for next hiraishin run
    end
```
