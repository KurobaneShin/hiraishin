# Provider: Kimi Code (Moonshot AI)

`aq` supports **both** Moonshot AI's native **Kimi Code CLI** (`kimi`) and running Kimi Code through **OpenCode** (`opencode`, e.g., the `kimi-worker` and `kimi-fast` agents). Users must not be forced onto OpenCode to schedule Kimi quotas.

Primary references:
- [Kimi Code Documentation](https://www.kimi.com/code/docs/)
- [MoonshotAI/kimi-code (GitHub)](https://github.com/MoonshotAI/kimi-code) (replaces the archived Python `kimi-cli`)
- [Moonshot AI Open Platform](https://platform.kimi.ai/)

---

## Harness A: Native Kimi Code CLI (`kimi-native`)

### Launch Commands

Documented for Moonshot's native Kimi Code CLI (`kimi`):

- **Headless (single prompt):**
  ```bash
  kimi --prompt "<prompt>" \
    --output-format stream-json \
    --auto
  ```
  - `--prompt`: runs a single task non-interactively instead of entering the TUI.
  - `--output-format`: `text` or `stream-json` (JSONL events for `aq`'s Failover Watcher).
  - Permission modes: `--yolo` (ask when needed) or `--auto` (never ask, for autonomous worktrees). `[unverified against a local kimi binary — binary not installed on this workstation on 2026-11-21; verify exact flag spelling when installing MoonshotAI/kimi-code]`.
- **Interactive:**
  ```bash
  kimi
  ```
  Inside the TUI: `/login` authenticates with a Kimi Code subscription (OAuth) or Moonshot Open Platform API key; `/usage` displays current window utilization.

### Resume Semantics (Native)

- `kimi --continue`: resumes the most recent session in the current working directory.
- `kimi --session <session-id>`: resumes a specific saved session ID (or opens an interactive picker when `<session-id>` is omitted).

---

## Harness B: OpenCode (`opencode` with `kimi-worker` / `kimi-fast`)

Verified locally against `opencode --help`, `opencode run --help`, and `~/.config/opencode/opencode.json` (2026-11-21).

### Launch Commands

- **Headless:**
  ```bash
  opencode run \
    --agent kimi-worker \
    --model kimi-code-plan-cn/k3.1-pro \
    --format json \
    --auto \
    "<prompt>"
  ```
  - `--agent kimi-worker` (uses `kimi-code-plan-cn/k3.1-pro`, `reasoningEffort: medium`, lean toolset) or `--agent kimi-fast` (`kimi-code-plan-cn/kimi-for-coding-highspeed`).
  - `--format json` emits raw JSON events on stdout.
  - `--dir <worktree>` or run with `cwd = <worktree>`.
- **Interactive:**
  ```bash
  opencode --agent kimi-worker --model kimi-code-plan-cn/k3.1-pro /path/to/worktree
  ```

### Resume Semantics (OpenCode)

- `opencode run --continue` (`-c`) or `--session <ses_id>` (`-s`): resumes a session stored in `~/.local/share/opencode/opencode.db`.
- `--fork`: forks before continuing.
- OpenCode session IDs (`ses_...`) map directly to `ai-memory`'s `memory_read_session_observations(session_id=...)`.

---

## Quota Windows & Reset Rules

Kimi Code subscriptions (Plus, Pro, Max, Ultra) use a dedicated quota pool separate from pay-as-you-go Moonshot Open Platform keys (`api.moonshot.ai/v1` / `api.moonshot.cn/v1`) ([Kimi Code Docs](https://www.kimi.com/code/docs/)):

| Window | Kind | Behavior |
|---|---|---|
| **5-hour rolling window** | Rolling | Burst cap for coding-agent requests; resets 5 hours after consumption begins. |
| **Weekly / Monthly pool** | Fixed / rolling period | Longer-horizon cap (`limit_month_total` or weekly ceiling depending on membership tier) `[unverified exact reset alignment per region CN vs. Global]`. |

Subscription endpoints (used by Kimi Code CLI and OpenCode's `kimi-code-plan-cn` provider):
- OpenAI-compatible: `https://api.kimi.com/coding/v1`
- Anthropic-compatible: `https://api.kimi.com/coding/`

## How to Read Usage (Sensor Chain)

1. **Tier 1 — First-Party Coding Usage Endpoint:**
   - Endpoint: `GET https://api.kimi.com/coding/v1/usages` (used by Kimi Code CLI `/usage`, [quota-axi](https://github.com/kunchenguid/quota-axi), and [quotamux](https://github.com/yandie-AI/quotamux)).
   - Returns rolling 5-hour utilization/reset data and aggregate plan quota fields (`limit_month_total` etc.).
   - **Caution:** `quotamux` notes that Kimi's usage endpoint is not formally versioned as a public third-party SLA API `[unverified schema stability across CN and global accounts]`.
2. **Tier 2 — Local Harness Storage:**
   - When running via OpenCode: `opencode stats --days 1 --models` or read-only SQLite query against `~/.local/share/opencode/opencode.db` (note: this DB can grow multi-GB — 8.8 GB on this workstation — so `aq` must use indexed timestamp queries or CLI stats, never full-table scans).
   - Native Kimi CLI local session store path `[unverified — confirm once kimi binary is installed]`.
3. **Tier 3 — Terminal Output Patterns:**
   - Watches stderr/PTY and `stream-json` / `opencode run --format json` events for HTTP 429 and 5-hour limit banners.

## Exact Limit & Error Messages (Fixtures)

Real error observed on 2026-11-21 during the `mmorpg` fronts (`persist-all`, `loot-drops`, `char-flow` failing over from `kimi-worker` to `agy`), recorded in [plan.md](../plan.md):

```text
# Observed 5-hour limit message (real fixture from 2026-11-21)
429 You've reached your 5-hour usage limit

# Variant seen in OpenCode / API error wrapper [approximate wording]
Error: 429 Too Many Requests - You've reached your 5-hour usage limit. Please try again later.
```

Regex classification:
- `reached your 5-hour usage limit|5-hour usage limit` -> `EXHAUSTED` on window `5h` (estimated reset = `now + 5h` if sensor lacks exact `resets_at`).
- `429` without 5h marker -> inspect body for weekly/monthly cap vs. short RPM burst (`DEGRADED` with 60s cooldown if RPM).
- `401|invalid_api_key|unauthorized` -> `AUTH_ERROR`.

## Terms of Service (Scripted Use)

- **Kimi Code Documentation & Terms:** [https://www.kimi.com/code/docs/](https://www.kimi.com/code/docs/)
- **Moonshot Open Platform:** [https://platform.kimi.ai/](https://platform.kimi.ai/)

Key policy boundaries from Moonshot's platform documentation:
- Kimi Code subscription endpoints (`https://api.kimi.com/coding/`) are intended for coding-agent tools (Kimi Code CLI, IDE extensions, supported coding harnesses like OpenCode) and are distinct from general pay-as-you-go Open Platform API keys (`api.moonshot.ai/v1`).
- `[unverified exact verbatim ToS clause]`: Moonshot's full legal ToS page behind regional login (`kimi.com` / `platform.kimi.ai`) could not be fetched verbatim in this documentation pass; before v0.1 release, retrieve and quote the exact automation and fair-use clauses for both global (`kimi.com`) and China (`kimi-code-plan-cn`) accounts.

## Open Risks

1. **Undocumented usage endpoint drift:** `https://api.kimi.com/coding/v1/usages` may change JSON fields or require specific client headers (`User-Agent` / `Client-Version`) without notice.
2. **CN vs. Global account differences:** The user's `opencode.json` uses `kimi-code-plan-cn/k3.1-pro`; `aq` must allow configuring the base region (`cn` vs. `global`) for the sensor URL.
3. **Rapid 5h exhaustion under parallel fronts:** On 2026-11-21, running 3 parallel Kimi fronts (`char-flow`, `persist-all`, `loot-drops`) exhausted the 5h window rapidly, forcing manual failover of all three to `agy`. Setting `max_concurrent = 2..3` and pairing with `burn-expiring-first` avoids simultaneous stalls.
4. **Native CLI installation gap:** `kimi` is not yet installed on this workstation, so native `--output-format stream-json` field names must be captured as recorded fixtures before enabling the native adapter in v0.1.
