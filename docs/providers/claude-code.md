# Provider: Claude Code (Anthropic)

Official CLI binary: `claude` ([Claude Code Documentation](https://claude-code.mintlify.app/)).

## Launch Commands

Verified against local `claude --help` (2026-11-21) and [Run Claude Code programmatically](https://claude-code.mintlify.app/docs/automation/run-programmatically).

### Headless (Non-Interactive)

```bash
claude -p "<prompt>" \
  --model claude-opus-5-5 \
  --effort high \
  --permission-mode acceptEdits \
  --output-format stream-json \
  --add-dir /path/to/worktree
```

- `-p` / `--print`: non-interactive mode; skips workspace trust dialog (only use in trusted worktrees).
- `--output-format stream-json`: emits NDJSON events (including session ID, token usage, and structured errors) ideal for `aq`'s Failover Watcher.
- `--permission-mode`: `acceptEdits`, `auto`, `bypassPermissions` (or `--dangerously-skip-permissions` in isolated sandboxes), `dontAsk`, `plan`.
- `--fallback-model <model>`: built-in intra-Anthropic fallback when a model is busy; `aq` leaves this unset or controlled by user config so cross-provider scheduling remains explicit.
- `--bg` / `--background`: starts a background session managed via `claude attach|logs|stop|rm`.

### Interactive

```bash
claude --model claude-sonnet-5-5 --effort medium --add-dir /path/to/worktree "<initial prompt>"
```

Inside Orca (`tools/orch/front.sh`), Claude Code is launched via:

```bash
orca-ide agent launch --agent claude --worktree "id:<wt_id>" --surface terminal --prompt "<prompt>" --json
```

## Resume Semantics

- **Same-provider resume:**
  - `claude --resume <session_id>` (or `-r <session_id>`) resumes a specific conversation by UUID or name.
  - `claude --continue` (or `-c`) continues the most recent conversation in the current working directory.
  - `--fork-session` creates a new session ID on resume to preserve the original transcript.
  - Note: `--system-prompt-snapshot on` (default) reuses the first turn's system prompt verbatim across resumes until compaction.
- **Cross-provider failover (`aq`):** When Claude hits its 5h or weekly limit, `aq` does not convert Claude's JSONL transcript; it stops the process and launches the next allowed provider in the same git worktree ([ADR 0004](../adr/0004-worktree-as-checkpoint.md)).

## Quota Windows & Reset Rules

Claude Code shares subscription capacity with `claude.ai` and Claude Desktop ([Anthropic Claude Usage Limits](https://support.claude.com/) / [Claude Code Docs](https://claude-code.mintlify.app/)):

| Window | Kind | Behavior |
|---|---|---|
| **5-hour session window** | Rolling | Starts on the first prompt of a fresh cycle; resets 5 hours later. |
| **Weekly cap** | Fixed weekly | Account-assigned weekly reset timestamp across all models (with separate model-tier weighting, e.g. Opus vs. Sonnet). |
| **Ultra / Enterprise / API** | Plan-dependent | Ultra tiers may waive or expand the 5h window `[unverified exact current threshold]`; `ANTHROPIC_API_KEY` is token-metered (off by default in `aq`). |

## How to Read Usage (Sensor Chain)

1. **Tier 1 — OAuth Usage Endpoint (caution: rate-limited):**
   - Endpoint: `GET https://api.anthropic.com/api/oauth/usage` with headers `Authorization: Bearer <oauth_token>` and `anthropic-beta: oauth-2025-04-20` (as documented in community monitors such as [quota-axi](https://github.com/kunchenguid/quota-axi) and [CodexBar](https://github.com/steipete/CodexBar)).
   - Returns rolling 5-hour utilization percentage, weekly utilization percentage, and reset timestamps (mirroring `/usage` inside the CLI).
   - **Important:** Aggressive polling triggers `429 Too Many Requests` (often with `retry-after: 0`). `aq` enforces `poll_interval_sec >= 120`, backs off exponentially on 429, and prefers passive rate-limit metadata passed via Claude Code's native status/hook environment when available.
   - Credential lookup (read-only, never exported): `~/.claude/.credentials.json` or OS keychain.
2. **Tier 2 — Local JSONL Session Logs:**
   - Claude Code writes per-session JSONL transcripts under `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl` and `~/.claude/history.jsonl` (verified locally on 2026-11-21).
   - Records exact `input_tokens`, `output_tokens`, `cache_read_input_tokens`, and `cache_creation_input_tokens` per turn (parsed by tools like [ccusage](https://github.com/ccusage/ccusage)).
   - Limitation: Anthropic does not publish exact token-to-percentage formulas for subscription windows, so JSONL alone yields `confidence = "estimated"` unless calibrated against Tier 1 or Tier 3.
3. **Tier 3 — Terminal / Stream-JSON Output Patterns:**
   - Watches `stream-json` result errors and PTY output for exhaustion messages.

## Exact Limit & Error Messages (Fixtures)

Examples observed from Claude Code CLI and `stream-json` error payloads `[exact wording varies slightly across CLI releases; marked where approximate]`:

```text
# 5-hour session limit reached (CLI interactive / print stderr)
Claude AI usage limit reached|1763769600
You've hit your limit · resets 8pm (UTC)

# Weekly cap reached [unverified exact punctuation across versions]
You've reached your weekly usage limit · resets Mon 12am (UTC)

# OAuth / API rate-limit JSON event (HTTP 429)
{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit."}}

# Overloaded transient (HTTP 529 — transient DEGRADED, not EXHAUSTED)
{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}

# Expired auth (HTTP 401 -> AUTH_ERROR)
{"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired"}}
```

Regex classification:
- `usage limit reached|hit your limit|weekly usage limit` -> `EXHAUSTED` (extract reset timestamp/time if present).
- `overloaded_error|529` -> transient backoff (`DEGRADED`), do not mark window exhausted.
- `authentication_error|OAuth token has expired|401` -> `AUTH_ERROR`.

## Terms of Service (Scripted Use of Official CLI)

- **Consumer Terms of Service:** [https://www.anthropic.com/legal/consumer-terms](https://www.anthropic.com/legal/consumer-terms)
- **Commercial Terms of Service:** [https://www.anthropic.com/legal/commercial-terms](https://www.anthropic.com/legal/commercial-terms)
- **Claude Code Legal & Automation Docs:** [https://claude-code.mintlify.app/docs/resources/legal](https://claude-code.mintlify.app/docs/resources/legal), [https://claude-code.mintlify.app/docs/automation/run-programmatically](https://claude-code.mintlify.app/docs/automation/run-programmatically)

Key clause in Anthropic's Consumer Terms of Service (Use Restrictions):

> "Except when you are accessing our Services via an Anthropic API Key or where we otherwise explicitly permit it, to access the Services through automated or non-human means, whether through a bot, script, or otherwise."
> — [Anthropic Consumer Terms of Service](https://www.anthropic.com/legal/consumer-terms)

**Compliance boundary for `aq`:**
- Running the official `claude` CLI (including `claude -p`, which Anthropic documents in [Run Claude Code programmatically](https://claude-code.mintlify.app/docs/automation/run-programmatically)) on the user's own workstation under the user's own login is the officially supported surface.
- Extracting Claude Code's OAuth token to feed a third-party API client, reverse proxy, or multi-tenant pool is prohibited — and explicitly forbidden by [ADR 0001](../adr/0001-no-proxy.md).
- Polling `/api/oauth/usage` directly from `aq` sits in a gray area because it reads the OAuth token from `~/.claude/.credentials.json` for a metadata GET request; therefore `aq` makes endpoint polling **opt-in** (`sensor.mode = "endpoint"` or `"auto"` confirmed during `aq init`) and supports zero-endpoint local-log + output-regex operation.

## Open Risks

1. **Undocumented `/api/oauth/usage` stability:** Endpoint path, `anthropic-beta` header (`oauth-2025-04-20`), and tight rate limits have changed before and can break without notice.
2. **Account-level shared pool:** Interactive web/Desktop usage and multiple parallel `claude` worktrees drain the same 5h and weekly pool simultaneously; `aq` must model `claude-opus` and `claude-sonnet` under one `shared_quota_pool`.
3. **Consumer vs. Commercial automation boundary:** Heavy unattended multi-agent loops on a consumer Pro/Max plan may face stricter enforcement than interactive-assisted development; users running CI/production automation should opt into an API key (`cost_class = "metered"`).
