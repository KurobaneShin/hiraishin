# Provider: Google Antigravity (`agy`)

Official CLI binary: `agy` ([Google Antigravity](https://antigravity.google)).

## Launch Commands

Verified directly against local `agy --help` (2026-11-21) and production orchestration wrappers (`~/orca/briefs/agy-front.sh`, `~/orca/briefs/neon-perf/run.sh`).

### Headless (Non-Interactive)

```bash
agy \
  --model gemini-4-neon-medium \
  --mode accept-edits \
  --dangerously-skip-permissions \
  --add-dir /path/to/worktree \
  --output-format stream-json \
  --print "<prompt>"
```

- `-p` / `--print` / `--prompt`: runs a single prompt non-interactively and exits.
- `--output-format`: `text` (default), `json`, or `stream-json` (NDJSON events).
- `--input-format`: `text` or `stream-json` (multi-turn NDJSON over stdin; requires `--output-format stream-json`).
- `--mode`: `accept-edits`, `auto`, or `plan`.
- `--effort`: `low`, `medium`, `high`, `xhigh`, `max`.
- `--print-timeout`: optional duration limit (default `0s` = wait until turn finishes; production scripts wrap with `timeout 100m`).
- `--log-file <path>`: overrides the CLI log file path for structured tailing.
- **Single-flight lock (operational gotcha):** On multi-front runs, `neon-perf/run.sh` serializes high-tier runs with `flock ~/.agy.lock` when sharing local auth/cache state (`launch_prefix = ["flock", "~/.agy.lock"]` in `preferences.toml`).

### Interactive

```bash
agy --model gemini-4-neon-high --mode accept-edits --add-dir /path/to/worktree -i "<initial prompt>"
```

- `-i` / `--prompt-interactive`: runs an initial prompt interactively and keeps the session open.
- `agy models`: lists available models.
- `agy acp`: starts an Agent Client Protocol (ACP) stdio server.

## Resume Semantics

- **Same-provider resume:**
  - `agy --continue` (`-c`): continues the most recent conversation.
  - `agy --conversation <id>`: resumes a specific conversation by ID.
- **Multi-phase headless loop:** Because `agy --print` exits after completing a turn (or hitting `--print-timeout`), existing wrappers (`agy-front.sh`) run iterative phases in the same worktree, checking `report.md` and `inbox.md` between phases.
- **Cross-provider failover (`aq`):** Uses the git worktree checkpoint ([ADR 0004](../adr/0004-worktree-as-checkpoint.md)). On 2026-11-21, `agy` (`gemini-4-neon-medium`) successfully resumed all three fronts that failed over from Kimi (`persist-all`, `loot-drops`, `char-flow`).

## Quota Windows & Reset Rules

| Window | Kind | Behavior |
|---|---|---|
| **Short-term / rolling model quota** | Rolling (plan-dependent) | Tied to Google AI Pro / Ultra or linked Google Cloud / AI Studio tier; heavy models (`high`) deplete faster than `medium`/`flash` variants `[unverified exact public rolling window duration — community monitors report 5h or daily cycles depending on account tier]`. |
| **Burst RPM / TPM** | Per-minute | Large directory context ingestion or recursive subagents can trigger `429 RESOURCE_EXHAUSTED` even when aggregate quota remains. |

## How to Read Usage (Sensor Chain)

1. **Tier 1 — Local / First-Party Probe (opt-in via `quota-axi`):**
   - [quota-axi](https://github.com/kunchenguid/quota-axi) and [AgentLimits](https://github.com/b01b02014/agent-limit-monitor) support reading Antigravity (`agy`) quota metadata from local credentials/loopback state `[unverified exact endpoint path — inspect quota-axi source before implementing native Go probe]`.
2. **Tier 2 — Local Transcripts & Logs:**
   - `agy` writes conversation transcripts to `~/.gemini/antigravity-cli/brain/<conversation-id>/.system_generated/logs/transcript.jsonl` (verified locally on 2026-11-21) and `--log-file <path>` when passed by `aq`.
   - `aq` counts turns and elapsed minutes per window (`confidence = "estimated"`).
3. **Tier 3 — Terminal Output Patterns:**
   - Primary detector in v0.1. Watches stdout/stderr and `--log-file` for `429`, `RESOURCE_EXHAUSTED`, `401`, and `UNAUTHENTICATED`.

## Exact Limit & Error Messages (Fixtures)

Real patterns handled in production (`~/orca/briefs/agy-front.sh` line 15 and [plan.md](../plan.md)):

```text
# Auth expiration / token refresh failure (real pattern guarded in agy-front.sh)
Error: 401 UNAUTHENTICATED: Request had invalid authentication credentials.

# Quota / rate limit exhaustion (HTTP 429)
Error: 429 RESOURCE_EXHAUSTED: Quota exceeded for quota metric ...
```

Regex classification:
- `429|RESOURCE_EXHAUSTED|Quota exceeded` -> first occurrence triggers short backoff (`DEGRADED`, 60s retry); repeated hit within 5 minutes (or explicit daily/window quota message) marks model/provider `EXHAUSTED`.
- `UNAUTHENTICATED|401` -> transitions provider to `AUTH_ERROR` (per `agy-front.sh`, do not retry more than 2–3 times; requires interactive `/logout` + re-login or `AGY_ADC_AUTH=true`).

## Terms of Service (Scripted Use)

- **Google Antigravity Product & Terms:** [https://antigravity.google](https://antigravity.google)
- **Gemini Code Assist Terms of Service:** [https://developers.google.com/code-assist/terms](https://developers.google.com/code-assist/terms)
- **Google Terms of Service:** [https://policies.google.com/terms](https://policies.google.com/terms)

Key policy points:
- `agy` explicitly ships first-party non-interactive scripting flags (`--print`, `--output-format stream-json`, `--input-format stream-json`, `acp`).
- Google's Universal Terms of Service and Gemini Code Assist Terms prohibit unauthorized scraping, proxying, or sharing OAuth credentials through third-party API relays.
- `[unverified exact verbatim Antigravity Additional Terms quote]`: The Antigravity-specific terms page behind `antigravity.google` could not be fetched verbatim in this documentation pass; verify exact clauses on automated multi-session execution before v0.1 release.

## Open Risks

1. **Silent `401 UNAUTHENTICATED` expiry:** Cached keyring/OAuth tokens can expire mid-loop during long overnight runs; `aq` must distinguish `AUTH_ERROR` from `EXHAUSTED` so it alerts the user instead of waiting 5 hours for a phantom reset.
2. **Account-wide vs. per-model lockout:** Hitting `429` on a heavy model (`gemini-4-neon-high`) can sometimes lock or throttle lighter models on the same account; `shared_quota_pool = "google-antigravity"` should be configured cautiously with separate cooldown tracking per model tier.
3. **No documented public usage API:** Until a first-party `agy usage --json` command exists or `quota-axi`'s probe is verified stable, `agy` relies primarily on Tier 3 reactive limit detection and local burn-rate estimation.
