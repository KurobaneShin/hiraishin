# Provider: Google Gemini CLI (`gemini`)

Official CLI binary: `gemini` ([google-gemini/gemini-cli on GitHub](https://github.com/google-gemini/gemini-cli), Apache-2.0; docs at [geminicli.com](https://geminicli.com/)).

> **Local status (2026-11-21):** `gemini` is not installed on this workstation; CLI flags and quota tables below are sourced from upstream documentation and marked `[unverified locally]` where not tested against a live binary.

## Launch Commands

Sourced from [google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli) and [geminicli.com](https://geminicli.com/):

### Headless (Non-Interactive)

```bash
gemini -p "<prompt>" \
  --output-format stream-json \
  --yolo
```

- `-p` / `--prompt`: non-interactive mode for scripts, git hooks, and pipelines.
- `--output-format`: `text`, `json`, or `stream-json` (real-time JSON events).
- `--yolo` (or approval-mode flag depending on release `[unverified latest flag name]`): auto-approves tool actions for unattended worktree execution.
- `-m` / `--model`: selects the Gemini model.

### Interactive

```bash
gemini
```

## Resume Semantics

- **Same-provider resume:**
  - `gemini --resume latest` (or `-r latest`): resumes the latest saved conversation checkpoint.
  - `gemini --resume <session-id>` (or `-r <session-id>`): resumes a specific saved session.
- **No native quota queuing:** Gemini CLI fails immediately on HTTP 429 / `RESOURCE_EXHAUSTED` without queuing until reset — making `aq`'s Failover Watcher and `WAITING_QUOTA` queue directly complementary.
- **Cross-provider failover (`aq`):** Uses the git worktree checkpoint ([ADR 0004](../adr/0004-worktree-as-checkpoint.md)).

## Quota Windows & Reset Rules

Limits depend on authentication method and tier ([Gemini CLI Quotas](https://geminicli.com/), [Gemini API Rate Limits](https://ai.google.dev/gemini-api/docs/rate-limits)):

| Auth Method / Tier | Daily Cap (RPD) | Reset Rule | Notes |
|---|---|---|---|
| **Personal Google Account (Gemini Code Assist Free)** | ~1,000 RPD | Midnight Pacific Time (`America/Los_Angeles`) | Plus per-minute RPM/TPM caps. |
| **Google AI Pro / Code Assist Standard** | ~1,500 RPD | Midnight Pacific Time (`America/Los_Angeles`) | Higher model access & burst limits. |
| **Google AI Ultra / Enterprise** | ~2,000 RPD | Midnight Pacific Time (`America/Los_Angeles`) | Highest daily allowance. |
| **Gemini API Key (Free tier)** | ~250 RPD | Midnight Pacific Time (`America/Los_Angeles`) | Often restricted to Flash models. |
| **Gemini API Key (Paid / Vertex AI)** | Metered | Per-minute RPM/TPM | Off by default in `aq` (`cost_class = "metered"`). |

> **Important multiplier:** One agentic CLI prompt frequently triggers 10–50 underlying model API requests (one per tool call turn), so a single heavy coding task can consume a large fraction of the daily RPD pool.

## How to Read Usage (Sensor Chain)

1. **Tier 1 — Official Usage Endpoint:**
   - No documented standalone REST usage-percentage endpoint exists for personal Google Account Gemini CLI logins `[unverified whether any internal stats endpoint is exposed]`.
2. **Tier 2 — Local Session & Request Accounting:**
   - Parse `stream-json` / `json` output stats (per-turn API request count and token counts) and accumulate against the configured daily RPD ceiling resetting at `00:00 America/Los_Angeles` (`confidence = "estimated"`).
3. **Tier 3 — Terminal / `stream-json` Output Patterns:**
   - Authoritative exhaustion trigger via `429` / `RESOURCE_EXHAUSTED` detection.

## Exact Limit & Error Messages (Fixtures)

`[unverified exact CLI wrapper formatting — capture from live gemini --output-format stream-json in v0.2]`

```text
# Daily or per-minute quota hit (HTTP 429 / gRPC RESOURCE_EXHAUSTED)
Error: [API Error: 429 Too Many Requests] {"error":{"code":429,"message":"You exceeded your current quota, please check your plan and billing details.","status":"RESOURCE_EXHAUSTED"}}

# Auth failure (HTTP 401)
Error: 401 UNAUTHENTICATED: Login required.
```

Regex classification:
- `RESOURCE_EXHAUSTED|exceeded your current quota|quota.*daily` -> distinguish RPM burst from RPD exhaustion: retry once after 65s; if the second attempt immediately returns `RESOURCE_EXHAUSTED`, mark `daily_rpd` `EXHAUSTED` until `00:00 America/Los_Angeles`.
- `401|UNAUTHENTICATED` -> `AUTH_ERROR`.

## Terms of Service (Scripted Use)

- **Gemini CLI Repository (Apache-2.0):** [https://github.com/google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli)
- **Gemini Code Assist Terms of Service (Google Account login):** [https://developers.google.com/code-assist/terms](https://developers.google.com/code-assist/terms)
- **Gemini API Additional Terms of Service (API key auth):** [https://ai.google.dev/gemini-api/terms](https://ai.google.dev/gemini-api/terms)
- **Google Terms of Service:** [https://policies.google.com/terms](https://policies.google.com/terms)

Key policy boundaries:
- The CLI binary is Apache-2.0 and explicitly provides `-p` / `--output-format json` for headless scripting.
- Proxying or extracting personal OAuth credentials from Gemini CLI into third-party multi-tenant wrappers or unauthorized API bridges violates Google's service terms and risks account suspension or token revocation ([Gemini CLI Docs](https://geminicli.com/)).
- `[unverified exact verbatim ToS clause]`: Confirm exact quote from `developers.google.com/code-assist/terms` regarding automated local invocations before enabling `gemini-cli` in v0.2.
- **Compliance boundary for `aq`:** `aq` only runs the official `gemini` binary locally in the user's own worktree without touching or relaying its OAuth credentials ([ADR 0001](../adr/0001-no-proxy.md)).

## Open Risks

1. **RPM vs. RPD ambiguity:** Both per-minute bursts and daily exhaustion surface as `429 RESOURCE_EXHAUSTED`; misclassifying a 60-second RPM hiccup as a 24-hour daily exhaustion would unnecessarily bench the provider until midnight PT. The two-step probe (65s cooldown re-check) is mandatory.
2. **Midnight Pacific Time timezone drift:** Reset calculation must use `America/Los_Angeles` (handling PST/PDT DST transitions) rather than a fixed UTC offset.
3. **Silent model downgrade:** Some Gemini CLI configurations auto-fallback from Pro to Flash on quota pressure; `aq` should log the actual served model from `stream-json` metadata so `learning_stats` attributes outcomes to the model that actually ran.
