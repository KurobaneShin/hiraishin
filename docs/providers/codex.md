# Provider: OpenAI Codex CLI (`codex`)

Official CLI binary: `codex` ([openai/codex on GitHub](https://github.com/openai/codex)).

> **Local status (2026-11-21):** `codex` is not installed on this workstation; CLI flags and log paths below are sourced from upstream documentation and marked `[unverified locally]` where not tested against a live binary.

## Launch Commands

Sourced from [openai/codex](https://github.com/openai/codex) and [OpenAI Codex CLI docs](https://developers.openai.com/codex/cli/):

### Headless (Non-Interactive)

```bash
codex exec \
  --json \
  --sandbox workspace-write \
  "<prompt>"
```

- `codex exec`: non-interactive execution mode (reads prompt from arg or stdin).
- `--json`: emits newline-delimited JSON (`JSONL`) events to stdout (items, tool calls, token telemetry, errors).
- `--sandbox <mode>`: `read-only`, `workspace-write`, or `danger-full-access`.
- `--output-schema <file>`: enforces structured JSON output on the final message.
- `-o <file>`: writes the final response text to a file.
- `--ephemeral`: skips writing session files to disk (avoid when `hiraishin` needs session IDs for resume).
- `-m <model>` (or `-c model="<model>"` on older builds): selects the model.

### Interactive

```bash
codex "<initial prompt>"
```

Inside the interactive CLI, `/status` displays current session usage and remaining plan limits.

## Resume Semantics

- **Same-provider resume:**
  - `codex exec resume --last "<prompt>"`: resumes the most recent session non-interactively.
  - `codex exec resume <session_id> "<prompt>"`: resumes a specific session by UUID.
  - `codex resume`: interactive session resume.
- **Cross-provider failover (`hiraishin`):** Uses the git worktree checkpoint ([ADR 0004](../adr/0004-worktree-as-checkpoint.md)).

## Quota Windows & Reset Rules

Subscription-backed Codex CLI usage (ChatGPT Plus, Pro, Business, Enterprise) follows a dual-window structure ([OpenAI Codex Usage Limits](https://help.openai.com/)):

| Window | Kind | Behavior |
|---|---|---|
| **5-hour rolling window** | Rolling | Short-term burst limit; resets 5 hours after activity starts. |
| **Weekly cap** | Rolling / fixed 7-day | Broader weekly ceiling; whichever limit hits first blocks further requests. |
| **Dynamic adjustments** | Server-side | OpenAI adjusts allowances dynamically based on model choice and platform load. |

## How to Read Usage (Sensor Chain)

1. **Tier 1 — First-Party Usage Endpoint (opt-in / `quota-axi`):**
   - Tools such as [quota-axi](https://github.com/kunchenguid/quota-axi), [CodexBar](https://github.com/steipete/CodexBar), and [ModelDeck](https://github.com/timharris707/modeldeck) read Codex account quota windows via ChatGPT/Codex auth tokens `[unverified exact endpoint path & stability]`.
2. **Tier 2 — Local JSONL Rollout Logs (strong local signal):**
   - Codex writes structured session transcripts to `~/.codex/sessions/YYYY/MM/DD/rollout-<UTC-timestamp>-<session-id>.jsonl` (`%USERPROFILE%\.codex\sessions\...` on Windows).
   - Each JSONL file includes token usage telemetry events (`input_tokens`, `output_tokens`, cached tokens) and rate-limit metadata when emitted by the CLI.
   - `hiraishin` tails the active rollout file for token burn rate (`confidence = "estimated"`).
3. **Tier 3 — Terminal / `--json` Output Patterns:**
   - Parses `codex exec --json` error items and stderr for rate-limit / usage-cap messages.

## Exact Limit & Error Messages (Fixtures)

`[unverified verbatim fixtures — capture from live codex exec --json before enabling in v0.2]`

```text
# Usage limit / 5-hour or weekly cap
You've hit your usage limit. Upgrade to Pro or try again at ...

# HTTP 429 JSON error event
{"type":"error","message":"Rate limit reached (429): You have exceeded your current quota"}

# Auth failure
401 Unauthorized: Your ChatGPT session has expired. Run `codex login`.
```

Regex classification:
- `hit your usage limit|usage limit.*try again|exceeded your current quota` -> `EXHAUSTED` (parse `try again at <time>` when present).
- `429` without cap phrase -> transient backoff (`DEGRADED`).
- `401|session has expired|codex login` -> `AUTH_ERROR`.

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

1. **Dynamic server-side caps:** OpenAI periodically changes 5-hour and weekly allowances without fixed token numbers, making pure local token forecasting approximate unless anchored by Tier 1 probes or `/status`.
2. **CLI flag churn:** Early `codex` versions changed model and approval flags (`-m` vs. `-c model=...`, `--full-auto` vs. `--sandbox`), so the `codex` adapter must detect `codex --version` during `hiraishin init`.
3. **Fast context drain in `codex exec`:** Automatic repo/AGENTS.md ingestion on every fresh `codex exec` call consumes more quota than interactive turns; `hiraishin` should prefer single long-lived worktree runs over tight polling loops.
