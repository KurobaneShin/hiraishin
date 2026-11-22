# Provider: OpenAI Codex CLI (`codex`)

Official CLI binary: `codex` ([openai/codex on GitHub](https://github.com/openai/codex), docs at [OpenAI Codex CLI](https://developers.openai.com/codex/cli/)).

> **Verification status (2026-11-21):** `codex` is not installed on this workstation and requires an interactive login. Every CLI flag, JSONL stream schema, rollout log field, and error fixture below is sourced from upstream documentation and `openai/codex` source code and marked **`[unverified]`** until captured against a live binary.

## Launch Commands

### Headless (Non-Interactive)

```bash
codex exec \
  --json \
  --sandbox workspace-write \
  [--model <model>] \
  [-c model_reasoning_effort=<effort>] \
  "<prompt>"
```

`hiraishin`'s wrapper script (`run.sh`) enters the job's git worktree via `cd <worktree>` before launching `codex`, so `--cd` is not needed on `codex exec` or `codex exec resume`.

### Same-Provider Resume

```bash
codex exec resume <thread_id> \
  --json \
  --sandbox workspace-write \
  [--model <model>] \
  [-c model_reasoning_effort=<effort>] \
  "<prompt>"
```

- **Cross-provider failover (`hiraishin`):** Uses the git worktree checkpoint ([ADR 0004](../adr/0004-worktree-as-checkpoint.md)).

### Interactive

```bash
codex "<initial prompt>"
codex resume
```

Inside the interactive CLI, `/status` displays current session usage and remaining plan limits.

## CLI Flags Reference

| Flag / Subcommand | Purpose | Used by `hiraishin` | Source URL | Status |
|---|---|---|---|---|
| `codex exec` | Non-interactive execution mode reading prompt from argument or stdin | Yes (initial dispatch) | https://developers.openai.com/codex/cli/ , https://github.com/openai/codex/blob/main/codex-rs/exec/src/cli.rs | `[unverified]` |
| `codex exec resume <SESSION_ID>` | Non-interactive resume of an existing session/thread UUID (also supports `--last` interactively) | Yes (same-provider resume) | https://developers.openai.com/codex/cli/ , https://github.com/openai/codex/blob/main/codex-rs/exec/src/cli.rs | `[unverified]` |
| `--json` | Emit newline-delimited JSON (`JSONL`) execution events to stdout | Yes | https://github.com/openai/codex/blob/main/codex-rs/exec/src/cli.rs , https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs | `[unverified]` |
| `--sandbox <mode>` | Sandbox policy (`read-only`, `workspace-write`, `danger-full-access`); replaces deprecated `--full-auto` | Yes (`workspace-write`) | https://developers.openai.com/codex/cli/ , https://github.com/openai/codex/blob/main/codex-rs/exec/src/cli.rs | `[unverified]` |
| `--model` / `-m <model>` | Override the configured model | Yes (when `model` is set) | https://developers.openai.com/codex/cli/ , https://github.com/openai/codex/blob/main/codex-rs/exec/src/cli.rs | `[unverified]` |
| `-c <key>=<value>` | Override Codex config key (e.g. `-c model_reasoning_effort=<effort>`) | Yes (when `effort` is set) | https://developers.openai.com/codex/cli/ , https://github.com/openai/codex/blob/main/codex-rs/exec/src/cli.rs | `[unverified]` |
| `--cd` / `-C <dir>` | Set working directory for `codex exec` | No (`run.sh` already `cd`s into worktree) | https://github.com/openai/codex/blob/main/codex-rs/exec/src/cli.rs | `[unverified]` |
| `--ephemeral` | Skip persisting session rollout files to disk (avoid when resume/sensing is needed) | No | https://github.com/openai/codex/blob/main/codex-rs/exec/src/cli.rs | `[unverified]` |
| `--output-schema <file>` | Enforce a JSON Schema on the final response | No | https://developers.openai.com/codex/cli/ | `[unverified]` |
| `-o <file>` | Write the final response text to a file | No | https://developers.openai.com/codex/cli/ | `[unverified]` |
| `CODEX_HOME` (env) | Override root Codex state directory (default `~/.codex`) | Read by local-log sensor | https://github.com/openai/codex | `[unverified]` |

## Output & Log Formats

### 1. `codex exec --json` Stream Events

| Event / Field | Format | Purpose in `hiraishin` | Source URL | Status |
|---|---|---|---|---|
| `thread.started` (`thread_id`) | `{"type":"thread.started","thread_id":"<uuid>"}` | Extracted by `sensor.SessionID` as `native_session_id` for `codex exec resume <id>` | https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs | `[unverified]` |
| `turn.started` | `{"type":"turn.started"}` | Ignored by classifier | https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs | `[unverified]` |
| `item.completed` | `{"type":"item.completed","item":{"id":"...","type":"agent_message","text":"..."}}` | Ignored by classifier (prevents false positives from model prose mentioning `429`/`401`) | https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs | `[unverified]` |
| `turn.completed` | `{"type":"turn.completed","usage":{"input_tokens":...,"cached_input_tokens":...,"output_tokens":...}}` | Ignored by classifier | https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs | `[unverified]` |
| `error` | `{"type":"error","message":"..."}` | Classified by `sensor.Classify` (`codex.*` rules) | https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs | `[unverified]` |
| `turn.failed` | `{"type":"turn.failed","error":{"message":"..."}}` | Classified by `sensor.Classify` (`codex.*` rules) | https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs | `[unverified]` |

### 2. Local Rollout JSONL Sensor (`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`)

| Record / Field | Format | Purpose in `hiraishin` | Source URL | Status |
|---|---|---|---|---|
| Path layout | `${CODEX_HOME:-~/.codex}/sessions/YYYY/MM/DD/rollout-<UTC-timestamp>-<session-id>.jsonl` | Walked by `sensor.CodexRolloutSamples` | https://github.com/openai/codex | `[unverified]` |
| `session_meta` | `{"timestamp":"...","type":"session_meta","payload":{"id":"<uuid>","cwd":"..."}}` | Session metadata header | https://github.com/openai/codex | `[unverified]` |
| `event_msg` / `token_count` (`info.last_token_usage`, `info.total_token_usage`) | `input_tokens`, `cached_input_tokens`, `output_tokens`, `total_tokens` | Summed over the active 5h window as `RawTokens` (`max(input-cached,0) + output`, `confidence = "estimated"`) | https://github.com/openai/codex (protocol `TokenCountEvent`) | `[unverified]` |
| `event_msg` / `token_count` (`rate_limits.primary`, `rate_limits.secondary`) | `used_percent`, `window_minutes` (`300` = 5h, `10080` = weekly), `resets_at` (Unix seconds) | Latest unexpired snapshot emitted as `5h` (`rolling`) and `weekly` (`fixed_reset`) samples (`confidence = "estimated"`, `tier = "local_logs"`) | https://github.com/openai/codex (protocol `RateLimitSnapshot`) | `[unverified]` |

## Quota Windows & Reset Rules

Subscription-backed Codex CLI usage (ChatGPT Plus, Pro, Business, Enterprise) follows a dual-window structure ([OpenAI Help Center](https://help.openai.com/)):

| Window | Kind | Behavior |
|---|---|---|
| **5-hour rolling window (`5h`)** | `rolling` (`duration_hours = 5`) | Short-term burst limit; resets 5 hours after activity starts. |
| **Weekly cap (`weekly`)** | `fixed_reset` (`duration_hours = 168`) | Broader 7-day ceiling; whichever limit hits first blocks further requests. |
| **Dynamic adjustments** | Server-side | OpenAI adjusts allowances dynamically based on model choice and platform load. |

## How to Read Usage (Sensor Chain)

1. **Tier 1 — Local JSONL Rollout Logs (primary native sensor):**
   - `sensor.CodexRolloutSamples` walks `${CODEX_HOME:-~/.codex}/sessions/**/rollout-*.jsonl` and extracts the newest unexpired `rate_limits.primary` (`5h`) and `rate_limits.secondary` (`weekly`) snapshots (`used_percent`, `resets_at`) plus non-cached token counts (`RawTokens`) with `confidence = "estimated"`.
2. **Tier 2 — Optional `quota-axi --json` Fallback:**
   - Behind `sensor.quota_axi_fallback = true`, maps `provider == "codex"` windows from [quota-axi](https://github.com/kunchenguid/quota-axi).
3. **Tier 3 — `codex exec --json` & Stderr Output Classification:**
   - Parses `error` and `turn.failed` JSONL events plus plain stderr for usage limits, transient 429s, and auth expiration.

## Limit & Error Classification (Fixtures)

Recorded `[unverified]` fixtures live in [internal/sensor/testdata/fixtures/codex/](../../internal/sensor/testdata/fixtures/codex/README.md) with per-file source URLs.

| Rule ID | Signal | Window | Reset Parsing | Pattern Summary | Status |
|---|---|---|---|---|---|
| `codex.auth` | `AUTH_ERROR` | — | — | `session has expired`, `codex login`, `invalid_api_key`, `authentication_error`, `refresh_token_reused`, `401 Unauthorized` | `[unverified]` |
| `codex.limit_weekly` | `EXHAUSTED` | `weekly` | `try again in <dur>` / `try again at <time>` | `weekly (usage )?limit` | `[unverified]` |
| `codex.limit_5h` | `EXHAUSTED` | `5h` | `try again in <dur>` / `try again at <time>` | `hit your usage limit`, `usage_limit_reached`, `UsageLimitReached`, `exceeded your current quota`, `usage limit ... try again` | `[unverified]` |
| `codex.rate_429` | `DEGRADED` | — | 60s cooldown | `429`, `rate limit`, `too many requests` without usage-cap phrase | `[unverified]` |

## Terms of Service (Scripted Use)

- **OpenAI Terms of Use:** [https://openai.com/policies/terms-of-use/](https://openai.com/policies/terms-of-use/)
- **OpenAI Usage Policies:** [https://openai.com/policies/usage-policies/](https://openai.com/policies/usage-policies/)

Relevant clause from OpenAI's Terms of Use (What you cannot do):

> "Interfere with or disrupt our Services, including circumvent any rate limits or restrictions or bypass any protective measures or safety mitigations we put on our Services."
> — [OpenAI Terms of Use](https://openai.com/policies/terms-of-use/)

**Compliance boundary for `hiraishin`:**
- `codex exec` is OpenAI's officially documented headless interface for scripting and automation.
- `hiraishin` strictly **respects** rate limits: when Codex reports exhaustion, `hiraishin` stops dispatching to Codex until its reset window passes rather than retrying aggressively or pooling multiple accounts to circumvent caps.
- `hiraishin` never extracts ChatGPT OAuth tokens to proxy non-Codex clients ([ADR 0001](../adr/0001-no-proxy.md)).

## Open Risks

1. **Unverified live output:** Fixtures and flags are sourced from `openai/codex` source and docs and must be confirmed with a live redacted capture once `codex` is installed and authenticated.
2. **CLI flag churn:** Early `codex` versions changed model and approval flags (`-m` vs. `-c model=...`, `--full-auto` vs. `--sandbox`), and `codex exec resume` historically differed from `codex exec` on `--cd`.
3. **Fast context drain in `codex exec`:** Automatic repo/AGENTS.md ingestion on every fresh `codex exec` call consumes more quota than interactive turns; `hiraishin` prefers single long-lived worktree runs and native `codex exec resume <thread_id>`.
