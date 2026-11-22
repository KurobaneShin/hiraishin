# Google Gemini CLI Fixtures (`[unverified]`)

All fixtures in this directory are **`[unverified]`** synthetic captures constructed from upstream `google-gemini/gemini-cli` source code and official Gemini CLI documentation because `gemini` is not installed locally and requires interactive login. Replace with redacted live captures when available.

| File | Purpose | Source | Status |
|---|---|---|---|
| `stream_session.jsonl` | `gemini -p --output-format stream-json` stream with `init` (`session_id`, `model`), `message`, `tool_use`, `tool_result`, and `result` (`status: "success"`, `stats`) | `https://github.com/google-gemini/gemini-cli/blob/main/packages/cli/src/nonInteractiveCli.ts`, `https://geminicli.com/docs/cli/headless/` | `[unverified]` |
| `stream_normal.jsonl` | Normal `stream-json` output whose assistant message mentions `429 RESOURCE_EXHAUSTED` and `UNAUTHENTICATED` (false-positive guard) | `https://geminicli.com/docs/cli/headless/` | `[unverified]` |
| `quota_429.jsonl` | Ambiguous HTTP 429 / `RESOURCE_EXHAUSTED` error event (`DEGRADED` with 65s cooldown; single burst must not bench until midnight PT) | `https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/utils/googleQuotaErrors.ts`, `docs/providers/gemini-cli.md` | `[unverified]` |
| `quota_429_burst.jsonl` | Two `429 RESOURCE_EXHAUSTED` events (`error` + `result` with `status: "error"`) used to test both within-65s burst suppression (`DEGRADED`) and >=65s RPD escalation (`EXHAUSTED` until `00:00 America/Los_Angeles`) | `https://github.com/google-gemini/gemini-cli/blob/main/packages/cli/src/nonInteractiveCli.ts`, `https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/utils/googleQuotaErrors.ts` | `[unverified]` |
| `quota_daily.jsonl` | Explicit `TerminalQuotaError` / `QUOTA_EXHAUSTED` daily limit error (`EXHAUSTED` on `daily_rpd` until `00:00 America/Los_Angeles`) | `https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/utils/googleQuotaErrors.ts` | `[unverified]` |
| `auth_401.txt` | Unauthenticated CLI error (`AUTH_ERROR`) | `https://geminicli.com/docs/cli/headless/`, `docs/providers/gemini-cli.md` | `[unverified]` |
| `session_chat.json` | Local `~/.gemini/tmp/<project_hash>/chats/session-*.json` chat transcript with per-turn token counts | `https://github.com/google-gemini/gemini-cli` (session storage under `~/.gemini/tmp/<hash>/chats/`) | `[unverified]` |
