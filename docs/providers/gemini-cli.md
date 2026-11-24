# Provider: Google Gemini CLI (`gemini`)

Official CLI binary: `gemini` ([google-gemini/gemini-cli on GitHub](https://github.com/google-gemini/gemini-cli), Apache-2.0; docs at [geminicli.com](https://geminicli.com/)).

> **Verification status (2026-11-21):** `gemini` is not installed on this workstation and requires an interactive login. Every CLI flag, `stream-json` schema, local session log path, and error fixture below is sourced from upstream documentation and `google-gemini/gemini-cli` source code and marked **`[unverified]`** until captured against a live binary.

## Launch Commands

### Headless (Non-Interactive)

```bash
gemini -p "<prompt>" \
  --output-format stream-json \
  --yolo \
  [--model <model>]
```

### Same-Provider Resume

```bash
gemini -p "<prompt>" \
  --output-format stream-json \
  --yolo \
  --resume <session_id> \
  [--model <model>]
```

- **No native quota queuing:** Gemini CLI fails immediately on HTTP 429 / `RESOURCE_EXHAUSTED` without queuing until reset — making `hiraishin`'s Failover Watcher and `WAITING_QUOTA` queue directly complementary.
- **Cross-provider failover (`hiraishin`):** Uses the git worktree checkpoint ([ADR 0004](../adr/0004-worktree-as-checkpoint.md)).

### Interactive

```bash
gemini
gemini --resume latest
```

## CLI Flags Reference

| Flag | Purpose | Used by `hiraishin` | Source URL | Status |
|---|---|---|---|---|
| `-p` / `--prompt <text>` | Non-interactive headless mode | Yes | https://geminicli.com/docs/cli/headless/ , https://github.com/google-gemini/gemini-cli/blob/main/packages/cli/src/nonInteractiveCli.ts | `[unverified]` |
| `--output-format <fmt>` | Output mode (`text`, `json`, or `stream-json` newline-delimited JSON events) | Yes (`stream-json`) | https://geminicli.com/docs/cli/headless/ , https://github.com/google-gemini/gemini-cli/blob/main/packages/cli/src/nonInteractiveCli.ts | `[unverified]` |
| `--yolo` | Auto-approve all tool calls for unattended worktree execution (alias/companion to `--approval-mode yolo`) | Yes | https://geminicli.com/docs/cli/headless/ , https://github.com/google-gemini/gemini-cli | `[unverified]` |
| `--resume` / `-r <id\|latest>` | Resume a saved session UUID, index, or `latest` | Yes (same-provider resume) | https://geminicli.com/docs/cli/headless/ , https://github.com/google-gemini/gemini-cli | `[unverified]` |
| `--model` / `-m <model>` | Select the Gemini model (e.g. `gemini-2.5-pro`, `gemini-2.5-flash`) | Yes (when `model` is set) | https://geminicli.com/docs/cli/headless/ , https://github.com/google-gemini/gemini-cli | `[unverified]` |
| `--approval-mode <mode>` | Fine-grained approval mode (`default`, `auto_edit`, `yolo`) | No (`--yolo` used) | https://geminicli.com/docs/cli/headless/ | `[unverified]` |

## Output & Log Formats

### 1. `--output-format stream-json` Events

| Event Type | Format | Purpose in `hiraishin` | Source URL | Status |
|---|---|---|---|---|
| `init` | `{"type":"init","timestamp":"...","session_id":"<uuid>","model":"..."}` | `session_id` extracted by `sensor.SessionID` as `native_session_id` for `--resume` | https://geminicli.com/docs/cli/headless/ , https://github.com/google-gemini/gemini-cli/blob/main/packages/cli/src/nonInteractiveCli.ts | `[unverified]` |
| `message` | `{"type":"message","timestamp":"...","role":"user\|assistant","content":"...","delta":true}` | Ignored by classifier (prevents false positives from model prose mentioning `429`/`RESOURCE_EXHAUSTED`) | https://geminicli.com/docs/cli/headless/ | `[unverified]` |
| `tool_use` / `tool_result` | `{"type":"tool_use","tool_name":"...","tool_id":"...","parameters":{...}}` | Ignored by classifier | https://geminicli.com/docs/cli/headless/ | `[unverified]` |
| `error` | `{"type":"error","timestamp":"...","severity":"error\|warning","message":"..."}` | Classified by `sensor.Classify` (`gemini.*` rules) | https://geminicli.com/docs/cli/headless/ , https://github.com/google-gemini/gemini-cli/blob/main/packages/cli/src/nonInteractiveCli.ts | `[unverified]` |
| `result` (success) | `{"type":"result","timestamp":"...","status":"success","stats":{"total_tokens":...,"input_tokens":...,"output_tokens":...,"duration_ms":...,"tool_calls":...}}` | Ignored by classifier (even if `"error": null`) | https://geminicli.com/docs/cli/headless/ , https://github.com/google-gemini/gemini-cli/blob/main/packages/cli/src/nonInteractiveCli.ts | `[unverified]` |
| `result` (error) | `{"type":"result","timestamp":"...","status":"error","error":{"type":"TerminalQuotaError\|RetryableQuotaError","message":"..."}}` | Classified by `sensor.Classify` (`gemini.*` rules) | https://geminicli.com/docs/cli/headless/ , https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/utils/googleQuotaErrors.ts | `[unverified]` |

### 2. Local Session Storage (`~/.gemini/tmp/<project_hash>/chats/session-*.json{,l}`)

| Field | Format | Purpose in `hiraishin` | Source URL | Status |
|---|---|---|---|---|
| Path layout | `~/.gemini/tmp/<project_hash>/chats/session-*.json` (and `.jsonl`) | Walked by `sensor.GeminiTokens` starting from `00:00 America/Los_Angeles` of the current Pacific day | https://github.com/google-gemini/gemini-cli | `[unverified]` |
| `messages[].tokens` | `{"input":...,"output":...,"cached":...,"thoughts":...,"tool":...}` | Summed (`max(input-cached,0) + output + thoughts + tool`) as `RawTokens` on `daily_rpd` with `ResetsAt = 00:00 America/Los_Angeles` (`confidence = "estimated"`) | https://github.com/google-gemini/gemini-cli | `[unverified]` |

## Quota Windows & Reset Rules

Limits depend on authentication method and tier ([Gemini CLI Docs](https://geminicli.com/), [Gemini API Rate Limits](https://ai.google.dev/gemini-api/docs/rate-limits)):

| Auth Method / Tier | Daily Cap (RPD) | Reset Rule | Notes |
|---|---|---|---|
| **Personal Google Account (Gemini Code Assist Free)** | ~1,000 RPD | Midnight Pacific Time (`America/Los_Angeles`) | Plus per-minute RPM/TPM caps. |
| **Google AI Pro / Code Assist Standard** | ~1,500 RPD | Midnight Pacific Time (`America/Los_Angeles`) | Higher model access & burst limits. |
| **Google AI Ultra / Enterprise** | ~2,000 RPD | Midnight Pacific Time (`America/Los_Angeles`) | Highest daily allowance. |
| **Gemini API Key (Free tier)** | ~250 RPD | Midnight Pacific Time (`America/Los_Angeles`) | Often restricted to Flash models. |
| **Gemini API Key (Paid / Vertex AI)** | Metered | Per-minute RPM/TPM | Off by default in `hiraishin` (`cost_class = "metered"`). |

Reset timestamps are always computed using the IANA `America/Los_Angeles` timezone (`sensor.NextMidnightPacific`) so PST/PDT daylight-saving transitions never drift.

> **Important multiplier:** One agentic CLI prompt frequently triggers 10–50 underlying model API requests (one per tool call turn), so a single heavy coding task can consume a large fraction of the daily RPD pool.

## How to Read Usage (Sensor Chain)

1. **Tier 1 — Official Usage Endpoint:**
   - No documented standalone REST usage-percentage endpoint exists for personal Google Account Gemini CLI logins `[unverified]`.
2. **Tier 2 — Local Session Token Accounting:**
   - `sensor.GeminiTokens` sums non-cached tokens from `~/.gemini/tmp/*/chats/session-*.json{,l}` since `00:00 America/Los_Angeles` (`confidence = "estimated"`, `tier = "local_logs"`), reporting `ResetsAt = NextMidnightPacific(now)`.
3. **Tier 3 — Two-Step 65s RPM-vs-RPD Confirmation & Output Classification:**
   - Step 1: An ambiguous `429` / `RESOURCE_EXHAUSTED` / `RetryableQuotaError` puts Gemini into `DEGRADED` with a **65-second cooldown** (`sensor.GeminiRPMCooldown`). Multiple 429 events emitted within the same `< 65s` burst stay `DEGRADED` and **never** bench Gemini until midnight PT.
   - Step 2: Once 65 seconds have elapsed, `watch` runs `Probe.ProbeGeminiLimit` (`gemini -p ok --output-format stream-json [--model <m>]`).
     - If the probe succeeds, `Tracker.Clear` resets burst history and Gemini returns to `AVAILABLE` (confirming it was a transient RPM burst).
     - If the probe still returns `429` / `RESOURCE_EXHAUSTED`, `Tracker.Observe` escalates it to `EXHAUSTED` on `daily_rpd` with `ResetsAt = 00:00 America/Los_Angeles`.
   - Explicit terminal daily errors (`TerminalQuotaError`, `QUOTA_EXHAUSTED`, `daily quota` / `RequestsPerDay`) mark `daily_rpd` `EXHAUSTED` until `00:00 America/Los_Angeles` immediately.

## Limit & Error Classification (Fixtures)

Recorded `[unverified]` fixtures live in [internal/sensor/testdata/fixtures/gemini/](../../internal/sensor/testdata/fixtures/gemini/README.md) with per-file source URLs.

| Rule ID | Signal | Window | Reset / Cooldown | Pattern Summary | Status |
|---|---|---|---|---|---|
| `gemini.auth` | `AUTH_ERROR` | — | — | `UNAUTHENTICATED`, `401 Unauthorized`, `login required` | `[unverified]` |
| `gemini.quota_daily` | `EXHAUSTED` | `daily_rpd` | `00:00 America/Los_Angeles` | `TerminalQuotaError`, `QUOTA_EXHAUSTED`, `quota ... per day\|daily`, `daily quota\|limit`, `RequestsPerDay` | `[unverified]` |
| `gemini.quota_429` | `DEGRADED` (escalates to `EXHAUSTED` if hit again `>= 65s` and `<= 10m` later) | `daily_rpd` | 65s cooldown; on escalation: `00:00 America/Los_Angeles` | `RESOURCE_EXHAUSTED`, `RetryableQuotaError`, `exceeded your current quota`, `429`, `too many requests` | `[unverified]` |

## Terms of Service (Scripted Use)

- **Gemini CLI Repository (Apache-2.0):** [https://github.com/google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli)
- **Gemini Code Assist Terms of Service (Google Account login):** [https://developers.google.com/code-assist/terms](https://developers.google.com/code-assist/terms)
- **Gemini API Additional Terms of Service (API key auth):** [https://ai.google.dev/gemini-api/terms](https://ai.google.dev/gemini-api/terms)
- **Google Terms of Service:** [https://policies.google.com/terms](https://policies.google.com/terms)

Key policy boundaries:
- The CLI binary is Apache-2.0 and explicitly provides `-p` / `--output-format stream-json` for headless scripting.
- Proxying or extracting personal OAuth credentials from Gemini CLI into third-party multi-tenant wrappers or unauthorized API bridges violates Google's service terms and risks account suspension or token revocation ([Gemini CLI Docs](https://geminicli.com/)).
- `[unverified]`: Confirm exact verbatim quote from `developers.google.com/code-assist/terms` regarding automated local invocations before enabling `gemini-cli` in production.
- **Compliance boundary for `hiraishin`:** `hiraishin` only runs the official `gemini` binary locally in the user's own worktree without touching or relaying its OAuth credentials ([ADR 0001](../adr/0001-no-proxy.md)).

## Open Risks

1. **Unverified live output:** Fixtures and flags are sourced from `google-gemini/gemini-cli` source and docs and must be confirmed with a live redacted capture once `gemini` is installed and authenticated.
2. **RPM vs. RPD ambiguity:** Both per-minute bursts and daily exhaustion surface as `429 RESOURCE_EXHAUSTED`; the 65-second two-step probe prevents transient RPM hiccups from benching Gemini until midnight PT.
3. **Midnight Pacific Time timezone drift:** Reset calculation uses IANA `America/Los_Angeles` (handling PST/PDT DST transitions) rather than a fixed UTC offset.
4. **Silent model downgrade:** Some Gemini CLI configurations auto-fallback from Pro to Flash on quota pressure; `hiraishin` should record the actual served model from `stream-json` `init` metadata when learning is enabled.
