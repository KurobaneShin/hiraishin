# Provider: Z.AI GLM Coding Plan (Zhipu AI)

`aq` supports **both** Z.AI's officially recommended harnesses for the GLM Coding Plan (Claude Code routed to Z.AI's Anthropic-compatible endpoint, ZCode, and OpenAI-compatible coding harnesses) **and** running GLM through **OpenCode** (`opencode` with `glm-worker` and `glm-strong`).

Primary references:
- [Z.AI GLM Coding Plan Documentation](https://docs.z.ai/devpack)
- [Z.AI Platform Console](https://z.ai/)

---

## Harness A: Z.AI Recommended Native/Compatible Harnesses

Z.AI does not ship a standalone terminal-only CLI equivalent to `claude` or `codex`; instead, its official Coding Plan docs ([docs.z.ai/devpack](https://docs.z.ai/devpack)) recommend:

1. **Claude Code pointed at Z.AI's Anthropic-compatible endpoint (`glm-claude`):**
   - Official helper: `npx @z_ai/coding-helper` (or environment variables per run).
   - Base URL: `https://api.z.ai/api/anthropic`
   - **Headless launch (isolated env, never mutating Anthropic's `~/.claude` login):**
     ```bash
     ANTHROPIC_BASE_URL="https://api.z.ai/api/anthropic" \
     ANTHROPIC_AUTH_TOKEN="$ZAI_CODING_API_KEY" \
     claude -p "<prompt>" \
       --permission-mode acceptEdits \
       --output-format stream-json \
       --add-dir /path/to/worktree
     ```
   - **Interactive launch:**
     ```bash
     ANTHROPIC_BASE_URL="https://api.z.ai/api/anthropic" \
     ANTHROPIC_AUTH_TOKEN="$ZAI_CODING_API_KEY" \
     claude --add-dir /path/to/worktree "<initial prompt>"
     ```
   - Note: Z.AI maps Claude model slot names to GLM Coding Plan models (e.g. `GLM-5.3` / `GLM-5.3-Flash`) on the server side (`ANTHROPIC_DEFAULT_SONNET_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL` overrides `[unverified exact current env var names in latest @z_ai/coding-helper]`).
   - **Resume semantics:** Inherits `claude --resume <session_id>` and `claude --continue`.
2. **ZCode (Z.AI's native Agentic Development Environment):**
   - GUI/ADE tuned for GLM Coding Plan ([docs.z.ai/devpack](https://docs.z.ai/devpack)). Not headless-scriptable from `aq` `[unverified whether ZCode exposes a CLI entrypoint]`.
3. **OpenAI-compatible Coding Endpoint (for Cline, Roo Code, Kilo Code, OpenCode):**
   - Base URL: `https://api.z.ai/api/coding/paas/v4`

---

## Harness B: OpenCode (`opencode` with `glm-worker` / `glm-strong`)

Verified locally against `opencode run --help` and `~/.config/opencode/opencode.json` (2026-11-21).

### Launch Commands

- **Headless (Flash — low credit burn for bounded/mechanical work):**
  ```bash
  opencode run \
    --agent glm-worker \
    --model zai-coding-plan/glm-5.3-flash \
    --format json \
    --auto \
    "<prompt>"
  ```
- **Headless (Full GLM-5.3 — ~3–4x plan credit burn for multi-file tasks):**
  ```bash
  opencode run \
    --agent glm-strong \
    --model zai-coding-plan/glm-5.3 \
    --format json \
    --auto \
    "<prompt>"
  ```
- **Interactive:**
  ```bash
  opencode --agent glm-strong /path/to/worktree
  ```

### Resume Semantics (OpenCode)

- `opencode run --continue` (`-c`) or `--session <ses_id>` (`-s`) resumes from `~/.local/share/opencode/opencode.db`.
- Cross-provider failover uses the git worktree checkpoint ([ADR 0004](../adr/0004-worktree-as-checkpoint.md)), as validated on 2026-11-21 when `loot-drops` failed over from `glm-strong` to `kimi-worker` (`~/orca/briefs/loot-drops/prompt-kimi.md`).

---

## Quota Windows & Reset Rules

Per Z.AI GLM Coding Plan documentation ([docs.z.ai/devpack](https://docs.z.ai/devpack)):

| Window | Kind | Behavior |
|---|---|---|
| **5-hour rolling window** | Rolling | Credit/prompt pool resets precisely 5 hours after consumption. |
| **Weekly cap** | Fixed 7-day | Secondary ceiling resetting every 7 days from plan purchase/activation timestamp. |
| **Isolation rule** | Hard stop | Exhausting the Coding Plan does **not** spill over into pay-as-you-go Z.AI API balance; requests fail until reset (or until a promotional Z.AI Reset Card is applied). |
| **Model weight** | Tiered | `glm-5.3` (`glm-strong`) burns ~3–4x more plan credits per turn than `glm-5.3-flash` (`glm-worker`); both share `shared_quota_pool = "zai-coding-plan"`. |

## How to Read Usage (Sensor Chain)

1. **Tier 1 — Z.AI Monitor Usage Endpoint:**
   - Endpoint: `GET https://api.z.ai/api/monitor/usage/quota/limit` (and companion `GET https://api.z.ai/api/monitor/usage/model-usage`), authenticated with `Authorization: Bearer <ZAI_API_KEY>`.
   - Used by open-source monitors including [quota-axi](https://github.com/kunchenguid/quota-axi), [CodexBar](https://github.com/steipete/CodexBar), and `melon-hub/zai-usage-tracker` ([GitHub](https://github.com/melon-hub/zai-usage-tracker)).
   - Mainland China accounts use `open.bigmodel.cn` instead of `api.z.ai` `[unverified exact CN path parity]`.
   - Returns real-time 5-hour and weekly quota percentages and reset timestamps (`confidence = "exact"`).
2. **Tier 2 — Local OpenCode / Claude JSONL Logs:**
   - `opencode stats --days 1 --models` or Claude JSONL token sums when using `glm-claude` (`confidence = "estimated"`).
3. **Tier 3 — Terminal Output Patterns:**
   - Detects HTTP 429 and Z.AI Coding Plan quota exhaustion messages.

## Exact Limit & Error Messages (Fixtures)

Real event on 2026-11-21: `loot-drops` on `glm-strong` hit its Coding Plan limit and was handed off to `kimi-worker` (`fronts.tsv` line 47: `loot-drops glm-strong opencode` -> `kimi-worker (took over from glm-strong, limit)`).

```text
# HTTP 429 / Coding Plan quota exhaustion [unverified exact wire JSON from api.z.ai — capture real fixture in v0.1]
Error: 429 Too Many Requests - You have reached your GLM Coding Plan usage limit
{"error":{"code":"1302","message":"Rate limit reached for requests"}}
{"error":{"code":"1113","message":"Insufficient balance or no resource package"}}
```

Regex classification:
- `GLM Coding Plan usage limit|quota limit|resource package|code":"1113"` -> `EXHAUSTED` on `zai-coding-plan` (5h window; check Tier 1 sensor to disambiguate 5h vs. weekly).
- `code":"1302"|rate limit reached` -> short concurrency/RPM burst (`DEGRADED`, 30–60s cooldown) unless repeated 3x within 2 minutes, then `EXHAUSTED`.
- `401|invalid api key|code":"1001"` -> `AUTH_ERROR`.

## Terms of Service (Scripted Use)

- **GLM Coding Plan Documentation & Usage Policy:** [https://docs.z.ai/devpack](https://docs.z.ai/devpack)

Key policy boundaries from Z.AI's Coding Plan Usage Policy:
- Coding Plan quotas are restricted to **officially supported coding and office tools** (ZCode, Claude Code, OpenCode, Cline, Roo Code, Kilo Code, Cursor).
- Using the Coding Plan endpoints (`api.z.ai/api/anthropic`, `api.z.ai/api/coding/paas/v4`) for general-purpose non-coding APIs, standalone SaaS backends, proxying, or reselling to third parties is prohibited and can result in throttling or suspension.
- `[unverified exact verbatim ToS quote]`: Direct text of Z.AI's legal Terms of Service page could not be fetched verbatim; verify exact section numbers and verbatim language on `z.ai` / `docs.z.ai` before v0.1 release.
- **Compliance boundary for `aq`:** `aq` only launches officially supported harnesses (`claude` via `api.z.ai/api/anthropic` or `opencode`) locally for the subscriber's own coding worktrees, never proxies or resells access ([ADR 0001](../adr/0001-no-proxy.md)).

## Open Risks

1. **Rapid credit burn on `glm-strong`:** Full `glm-5.3` burns ~3–4x more credits than `glm-5.3-flash`; long context loops can empty the 5h window in under an hour (as happened on `loot-drops` on 2026-11-21).
2. **Error code ambiguity:** Z.AI reuses HTTP 429 / numeric error codes (`1302`, `1113`) across RPM bursts and 5-hour/weekly plan exhaustion; combining Tier 3 regex with a single follow-up Tier 1 `quota/limit` probe avoids false failovers.
3. **Environment leakage with `glm-claude`:** Launching `claude` with `ANTHROPIC_BASE_URL` must be scoped strictly to the child process environment (never written to `~/.claude/settings.json`), so Anthropic Claude Max sessions running concurrently are unaffected.
