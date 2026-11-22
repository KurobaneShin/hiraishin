# OpenAI Codex CLI Fixtures (`[unverified]`)

All fixtures in this directory are **`[unverified]`** synthetic captures constructed from upstream `openai/codex` source code and official OpenAI Codex CLI documentation because `codex` is not installed locally and requires interactive login. Replace with redacted live captures when available.

| File | Purpose | Source | Status |
|---|---|---|---|
| `exec_session.jsonl` | `codex exec --json` session start (`thread.started` with `thread_id`), turn completion, and token usage | `https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs`, `https://developers.openai.com/codex/cli/` | `[unverified]` |
| `exec_normal.jsonl` | Normal `codex exec --json` output whose assistant message mentions `429` and `usage limit` (false-positive guard) | `https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs` | `[unverified]` |
| `limit_5h.jsonl` | 5-hour usage limit with relative retry duration (`error` + `turn.failed`) | `https://github.com/openai/codex` (`UsageLimitReachedError`, `exec_events.rs`) | `[unverified]` |
| `limit_5h_clock.jsonl` | 5-hour usage limit with clock time (`try again at 8:00 PM (UTC)`) | `https://github.com/openai/codex`, `docs/providers/codex.md` | `[unverified]` |
| `limit_weekly.jsonl` | Weekly usage cap (`turn.failed`) with relative retry duration | `https://github.com/openai/codex` (`UsageLimitReachedError`), `https://help.openai.com/` | `[unverified]` |
| `rate_429.jsonl` | Transient HTTP 429 rate limit without usage-cap phrase (`DEGRADED`) | `https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs` | `[unverified]` |
| `auth_401.txt` | Expired ChatGPT/Codex session auth failure (`AUTH_ERROR`) | `https://developers.openai.com/codex/cli/`, `docs/providers/codex.md` | `[unverified]` |
| `rollout_sample.jsonl` | Local `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` transcript with `session_meta`, `token_count` (`last_token_usage`, `total_token_usage`), and `rate_limits` (`primary` 5h + `secondary` weekly) | `https://github.com/openai/codex` (`codex-rs/protocol` `TokenCountEvent`, `RateLimitSnapshot`, `RolloutItem`) | `[unverified]` |
