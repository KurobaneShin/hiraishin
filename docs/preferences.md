# User Preferences, Scoring, and Bounded Learning

`hiraishin` follows one governing rule: **the user decides** ([ADR 0005](adr/0005-user-preferences-over-learned-scores.md)). Every routing decision starts from explicit user configuration, evaluates hard policy gates before any score is computed, and writes a human-readable `why` trace into the SQLite ledger. Optional learning is strictly bounded inside the provider list the user already approved, and can be frozen with a single command.

## Configuration File Location

- Default path: `${XDG_CONFIG_HOME:-~/.config}/hiraishin/preferences.toml`
- Project-local override (optional, checked into or beside a repo): `.hiraishin.toml` (only task rules, profile override, and pins; never inventory or credentials)
- Precedence: CLI/runtime flags (`--provider`, `hiraishin pin`, `hiraishin pause`) > `.hiraishin.toml` > `~/.config/hiraishin/preferences.toml`.

## Full TOML Schema (Commented)

```toml
# Schema version for forward-compatible migrations.
schema_version = 1

# ---------------------------------------------------------------------------
# Global defaults & active objective profile
# ---------------------------------------------------------------------------
[general]
# Default terminal adapter when not passed to `hiraishin run`: "process" | "tmux" | "orca"
default_terminal_adapter = "tmux"

# Active objective profile:
# "quality-first" | "burn-expiring-first" | "save-scarce" | "balanced" | "custom"
active_profile = "burn-expiring-first"

# Minimum fraction of remaining quota required before a provider is considered
# DEGRADED (triggers a soft penalty in balanced/save-scarce profiles).
warn_used_pct = 85.0

# Hard reserve floor: stop assigning NEW jobs (allow failovers only if
# `reserve_allow_failover = true`) once used_pct reaches this threshold.
# Set to 100.0 to allow full burn.
reserve_used_pct = 98.0
reserve_allow_failover = true

# On a limit hit, wait for the same provider instead of failing over when its
# reset falls within this many minutes of the agent stopping, then resume the
# agent's own session (claude --resume, kimi --session, opencode --session,
# agy --conversation). The default stays inside Claude Code's 1h prompt-cache
# TTL (measured on 2026-11-21: every cache write in local transcripts, `claude -p`
# included, was ephemeral_1h). Kimi Code and Z.AI publish no TTL. 0 turns it off.
failover_wait_max_min = 50

# Never automatically turn on pay-as-you-go / metered providers unless
# explicitly enabled in both [general] and the provider's own entry.
allow_metered_fallback = false

# ---------------------------------------------------------------------------
# Objective profile weights (used when active_profile = "custom" or to inspect
# built-in presets; weights are normalized to sum to 1.0 before scoring).
# ---------------------------------------------------------------------------
[profiles.custom]
quality  = 0.35   # Preference for higher quality_tier providers
expiry   = 0.35   # Preference for quota expiring soonest (perishable inventory)
scarcity = 0.20   # Penalty against burning scarce high-tier or tight-weekly pools
latency  = 0.10   # Preference for faster / lower-overhead harnesses

# ---------------------------------------------------------------------------
# Bounded learning configuration
# ---------------------------------------------------------------------------
[learning]
# Master switch. When false (the default when omitted), learned deltas are
# ignored (0.0 everywhere); outcomes are still recorded.
enabled = true

# Same as `hiraishin learn freeze`: outcomes are still logged to the ledger,
# but learning_stats and every learned delta stay as they are.
frozen = false

# Outcomes a (provider, role) pair needs before its delta applies. Default 5.
min_samples = 5

# Largest score adjustment (normalized score space) learning may add or
# subtract, 0 < max_delta <= 0.5. Default 0.12. Learning can NEVER violate
# hard gates (deny list, allowed list, min_quality_tier, pin, concurrency cap,
# metered opt-in).
max_delta = 0.12

# EWMA smoothing factor for new outcomes, 0 < ewma_alpha <= 1. Default 0.25.
ewma_alpha = 0.25

# Relative weights inside the raw outcome quality signal (sum to 1.0).
# Defaults below apply only when all four are omitted.
weight_completion     = 0.45   # Completed without human takeover
weight_review_rounds  = 0.25   # Fewer review/fix rounds before merge
weight_build_failures = 0.20   # Fewer build/gate failures
weight_duration       = 0.10   # Faster completion relative to the role average

# ---------------------------------------------------------------------------
# Provider inventory (`hiraishin init` detects installed CLIs/logins; user confirms)
# ---------------------------------------------------------------------------
[providers.<provider_id>]
enabled = true

# Harness adapter:
# "claude-code" | "kimi-native" | "glm-claude" | "opencode" |
# "antigravity-agy" | "codex" | "gemini-cli"
harness = "claude-code"

# Model or agent name passed to the harness (harness-specific).
model = "claude-opus-5-5"
# Optional harness-specific extra args or opencode agent name.
harness_agent = ""
effort = "high"

# Billing / cost class: "subscription" | "free" | "metered"
# Metered providers are ignored unless `enabled = true` AND
# `general.allow_metered_fallback = true` (or explicitly pinned).
cost_class = "subscription"

# User-assigned capability tier (1..5):
# 5 = frontier architecture/hard debugging, 4 = strong general coding,
# 3 = solid implementation/multi-file, 2 = fast/mechanical edits, 1 = basic.
quality_tier = 5

# User-assigned scarcity rating (1..5):
# 5 = very scarce (protect for hard tasks), 1 = abundant / fast-resetting.
scarcity_tier = 5

# User-assigned speed/responsiveness rating (1..5, higher = faster).
speed_tier = 3

# Optional hard cap on concurrent jobs launched by `hiraishin` on this provider.
# 0 or unset: no fixed cap; the quota brake (Stage 1, rule 8) limits parallel jobs instead.
max_concurrent = 4

# Optional command wrapper (e.g. "flock ~/.agy.lock" for single-flight CLIs).
launch_prefix = []

# Name of the environment variable that holds an API key (glm-claude needs it;
# endpoint sensors use it). Only the variable name is stored, never the key.
api_key_env = ""

# claude-code / glm-claude only: the Claude Code config dir of this account
# (exported as CLAUDE_CONFIG_DIR; absolute or ~/). Empty means the default
# ~/.claude, and an inherited CLAUDE_CONFIG_DIR is unset. Give each account
# its own provider and config_dir to fail over between accounts; two providers
# on the same config_dir must share a shared_quota_pool.
# `hiraishin init --claude-config-dir '<path or glob>'` adds one provider per
# account; dirs whose .claude.json names an account already found are skipped.
# The local-log sensor skips a config_dir whose projects/ is a symlink, since
# those logs may belong to another account.
config_dir = ""

# Quota windows. Auto-detected when a sensor supports it; typed values act as
# fallback when a sensor is offline or unavailable.
[[providers.<provider_id>.windows]]
name = "5h"                    # Identifier in ledger & status table
kind = "rolling"               # "rolling" | "fixed_reset" | "credit_pool" | "metered"
duration_hours = 5             # Used for rolling window reset estimates
# fixed_reset_cron = "0 0 * * *"   # Optional for daily/weekly fixed resets: "M H * * DOW", DOW 0-6 or *
# fixed_reset_tz   = "America/Los_Angeles"

# Sensor chain order for this provider (first working tier wins).
[providers.<provider_id>.sensor]
mode = "auto"                  # "auto" | "endpoint" | "local_logs" | "output_regex" | "none"
poll_interval_sec = 120        # Minimum poll interval (respect vendor rate limits)
quota_axi_fallback = false     # Allow calling `quota-axi --json` as a sensor probe

# ---------------------------------------------------------------------------
# Task rules: keyed by role name (`hiraishin run --role <role>`)
# ---------------------------------------------------------------------------
[roles.<role_name>]
# Ordered list of allowed providers. Hard gate: providers not listed here are
# NEVER selected for this role. Earlier position gives a small tie-break bonus.
allowed_providers = ["claude-max", "antigravity-high"]

# Hard gate: reject any provider whose quality_tier < min_quality_tier.
min_quality_tier = 4

# Optional hard deny list (takes precedence over allowed_providers / wildcards).
deny_providers = []

# Optional static pin: if set, only this provider may run this role.
# If exhausted, job enters WAITING_QUOTA instead of failing over.
pin_provider = ""

# If true, hitting a limit pauses the job in WAITING_QUOTA instead of failing
# over to another provider mid-task.
never_switch_mid_task = false

# Optional profile override for this role alone (e.g. keep engine quality-first
# even when global active_profile is burn-expiring-first).
profile_override = ""

# Optional failover prompt template path.
resume_prompt_template = ""

# ---------------------------------------------------------------------------
# Optional stack integrations (see docs/integrations.md)
# ---------------------------------------------------------------------------
[integrations.ai_memory]
enabled = false
transport = "http"             # only "http" is supported
url = "http://127.0.0.1:49374/mcp"
record_outcomes = true         # Write job outcome summaries as episodic pages

[integrations.qmd]
enabled = false
collections = ["orca", "mmorpg"]
max_snippets = 5

[integrations.graphify]
enabled = false
auto_size_tier = true          # Raise min_quality_tier on high-centrality tasks
max_context_tokens = 1500
```

## Complete Example Configurations

These presets ship in documentation as starting examples (never silently applied as defaults).

### Example 1: One Subscription Only (Claude Max)

Useful even with a single subscription: tracks 5-hour and weekly burn rates, prevents runaway parallel sessions from hitting the wall unexpectedly, and queues jobs cleanly until reset.

```toml
schema_version = 1

[general]
default_terminal_adapter = "tmux"
active_profile = "balanced"
warn_used_pct = 80.0
reserve_used_pct = 98.0
allow_metered_fallback = false

[learning]
enabled = false
frozen = true

[providers.claude-opus]
enabled = true
harness = "claude-code"
model = "opus"
effort = "high"
cost_class = "subscription"
quality_tier = 5
scarcity_tier = 4
speed_tier = 3
max_concurrent = 2
shared_quota_pool = "claude-max-account"

[[providers.claude-opus.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5

[[providers.claude-opus.windows]]
name = "weekly"
kind = "fixed_reset"
duration_hours = 168

[providers.claude-sonnet]
enabled = true
harness = "claude-code"
model = "sonnet"
effort = "medium"
cost_class = "subscription"
quality_tier = 4
scarcity_tier = 2
speed_tier = 4
max_concurrent = 4
# Shares the same underlying subscription pool as claude-opus:
shared_quota_pool = "claude-max-account"

[roles.architecture]
allowed_providers = ["claude-opus"]
min_quality_tier = 5
never_switch_mid_task = true

[roles.default]
allowed_providers = ["claude-sonnet", "claude-opus"]
min_quality_tier = 3
never_switch_mid_task = false
```

### Example 2: Two Subscriptions (Claude Max + OpenAI Codex)

Pairs Anthropic and OpenAI subscriptions so 5-hour windows complement each other and failover crosses vendors cleanly.

```toml
schema_version = 1

[general]
default_terminal_adapter = "tmux"
active_profile = "burn-expiring-first"
warn_used_pct = 85.0
reserve_used_pct = 97.0
allow_metered_fallback = false

[learning]
enabled = true
frozen = false
min_samples = 5
max_delta = 0.10
ewma_alpha = 0.25

[providers.claude-max]
enabled = true
harness = "claude-code"
model = "opus"
effort = "high"
cost_class = "subscription"
quality_tier = 5
scarcity_tier = 4
speed_tier = 3
max_concurrent = 3

[[providers.claude-max.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5

[[providers.claude-max.windows]]
name = "weekly"
kind = "fixed_reset"
duration_hours = 168

[providers.codex-pro]
enabled = true
harness = "codex"
model = "default"
cost_class = "subscription"
quality_tier = 4
scarcity_tier = 3
speed_tier = 4
max_concurrent = 3

[[providers.codex-pro.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5

[[providers.codex-pro.windows]]
name = "weekly"
kind = "fixed_reset"
duration_hours = 168

[roles.engine]
allowed_providers = ["claude-max", "codex-pro"]
min_quality_tier = 4
profile_override = "quality-first"

[roles.backend]
allowed_providers = ["codex-pro", "claude-max"]
min_quality_tier = 4

[roles.review]
allowed_providers = ["codex-pro", "claude-max"]
min_quality_tier = 4

[roles.mechanical]
allowed_providers = ["codex-pro", "claude-max"]
min_quality_tier = 3
```

### Example 3: Four Subscriptions (Claude + Kimi + GLM + Antigravity)

Matches the multi-front workstation stack (12 parallel fronts across Orca worktrees, supporting both native CLIs and OpenCode agents).

```toml
schema_version = 1

[general]
default_terminal_adapter = "orca"
active_profile = "burn-expiring-first"
warn_used_pct = 85.0
reserve_used_pct = 98.0
allow_metered_fallback = false

[learning]
enabled = true
frozen = false
min_samples = 5
max_delta = 0.12
ewma_alpha = 0.25

[providers.claude-opus]
enabled = true
harness = "claude-code"
model = "claude-opus-5-5"
effort = "high"
cost_class = "subscription"
quality_tier = 5
scarcity_tier = 5
speed_tier = 3
max_concurrent = 4
shared_quota_pool = "claude-max"

[[providers.claude-opus.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5

[[providers.claude-opus.windows]]
name = "weekly"
kind = "fixed_reset"
duration_hours = 168

[providers.claude-sonnet]
enabled = true
harness = "claude-code"
model = "claude-sonnet-5-5"
effort = "medium"
cost_class = "subscription"
quality_tier = 4
scarcity_tier = 4
speed_tier = 4
max_concurrent = 4
shared_quota_pool = "claude-max"

[providers.agy-neon-high]
enabled = true
harness = "antigravity-agy"
model = "gemini-4-neon-high"
cost_class = "subscription"
quality_tier = 5
scarcity_tier = 4
speed_tier = 3
max_concurrent = 2
launch_prefix = ["flock", "~/.agy.lock"]
shared_quota_pool = "google-antigravity"

[[providers.agy-neon-high.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5

[providers.agy-neon-medium]
enabled = true
harness = "antigravity-agy"
model = "gemini-4-neon-medium"
cost_class = "subscription"
quality_tier = 4
scarcity_tier = 3
speed_tier = 4
max_concurrent = 3
shared_quota_pool = "google-antigravity"

[providers.kimi-native]
enabled = true
harness = "kimi-native"
model = "k3.1-pro"
effort = "medium"
cost_class = "subscription"
quality_tier = 4
scarcity_tier = 2
speed_tier = 4
max_concurrent = 3
shared_quota_pool = "kimi-code"

[[providers.kimi-native.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5

[[providers.kimi-native.windows]]
name = "weekly"
kind = "fixed_reset"
duration_hours = 168

[providers.kimi-opencode]
enabled = true
harness = "opencode"
harness_agent = "kimi-worker"
model = "kimi-code-plan-cn/k3.1-pro"
cost_class = "subscription"
quality_tier = 4
scarcity_tier = 2
speed_tier = 4
max_concurrent = 3
shared_quota_pool = "kimi-code"

[providers.glm-strong]
enabled = true
harness = "opencode"
harness_agent = "glm-strong"
model = "zai-coding-plan/glm-5.3"
cost_class = "subscription"
quality_tier = 3
scarcity_tier = 2
speed_tier = 4
max_concurrent = 2
shared_quota_pool = "zai-coding-plan"

[[providers.glm-strong.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5

[[providers.glm-strong.windows]]
name = "weekly"
kind = "fixed_reset"
duration_hours = 168

[providers.glm-claude]
enabled = true
harness = "glm-claude"
model = "glm-5.3"
api_key_env = "ZAI_API_KEY"
cost_class = "subscription"
quality_tier = 3
scarcity_tier = 2
speed_tier = 4
max_concurrent = 2
shared_quota_pool = "zai-coding-plan"

[providers.glm-flash]
enabled = true
harness = "opencode"
harness_agent = "glm-worker"
model = "zai-coding-plan/glm-5.3-flash"
cost_class = "subscription"
quality_tier = 2
scarcity_tier = 1
speed_tier = 5
max_concurrent = 4
shared_quota_pool = "zai-coding-plan"

[roles.engine]
allowed_providers = ["claude-opus", "agy-neon-high"]
min_quality_tier = 5
profile_override = "quality-first"
never_switch_mid_task = false

[roles.gameplay]
allowed_providers = ["kimi-native", "kimi-opencode", "agy-neon-medium", "claude-sonnet", "glm-strong"]
min_quality_tier = 3

[roles.ui]
allowed_providers = ["claude-sonnet", "agy-neon-medium", "kimi-native", "glm-strong"]
min_quality_tier = 3

[roles.docs]
allowed_providers = ["agy-neon-high", "agy-neon-medium", "claude-sonnet", "kimi-native"]
min_quality_tier = 3

[roles.mechanical]
allowed_providers = ["glm-flash", "glm-strong", "kimi-opencode", "agy-neon-medium"]
min_quality_tier = 2

[integrations.ai_memory]
enabled = true
transport = "http"
url = "http://127.0.0.1:49374/mcp"
record_outcomes = true

[integrations.qmd]
enabled = true
collections = ["orca", "mmorpg"]
max_snippets = 5

[integrations.graphify]
enabled = true
auto_size_tier = true
max_context_tokens = 1500
```

## Scoring Formula

Scheduling runs in two strictly separated stages: **hard policy gates** followed by **weighted scoring**.

### Stage 1: Hard Policy Gates

A candidate provider $p$ is immediately rejected (and its rejection reason logged in `candidates_json`) if any of the following fail:

1. `p.enabled == true`
2. `p.state` is `AVAILABLE` or `DEGRADED` (rejects `EXHAUSTED`, `PAUSED`, `DRAINING`, `AUTH_ERROR`, `UNAVAILABLE`)
3. If `p.cost_class == "metered"`, `general.allow_metered_fallback == true` (unless explicitly pinned)
4. If the job or role has a `pin_provider`, `p.id == pin_provider`
5. `p.id` is in `role.allowed_providers` and NOT in `role.deny_providers`
6. `p.quality_tier >= effective_min_quality_tier` (where `effective_min_quality_tier` may be raised by `graphify` sizing if enabled)
7. `p.active_jobs < p.max_concurrent` when `max_concurrent > 0`
8. Quota brake, for every window of `p` with a measured burn per job: `used_pct + rate × horizon < reserve_used_pct`, where `rate` is the burn per job of `p` plus the burn per job of every job already running on the pool (each at its own provider's estimate, or at `p`'s when that provider has none), and `horizon` is `min(time to reset, typical job duration of p)`. The burn per job is the duration-weighted mean of the newest 20 `quota.job_burn` measurements for that provider and window (a median would read 0 on windows reported in whole percent, where most short jobs see no step), and the typical duration is their median; the watcher records one per window when an attempt ends, splitting each sample interval equally between the jobs running on the pool at that time. Usage outside hiraishin on the same account counts as job usage, so the estimate errs high. Without a measurement there is no brake.
9. Every window of `p` (and its `shared_quota_pool`, if set) has `used_pct < reserve_used_pct` (or `< 100.0` during failover when `reserve_allow_failover = true`; the quota brake uses the same limit)

If zero candidates pass Stage 1, the job transitions to `WAITING_QUOTA` and records the earliest `resets_at` among blocked allowed providers.

### Stage 2: Normalized Feature Scores

For each surviving candidate $p$, four features are normalized to $[0.0, 1.0]$:

1. **Quality ($Q_p$):**
   $$Q_p = \frac{\text{quality\_tier}_p - 1}{4}$$
2. **Expiry / Perishable Urgency ($E_p$):**
   Evaluated across the provider's shortest active rolling/fixed window $w$ (typically `5h` or `daily_rpd`):
   $$\text{rem}_w = \frac{100 - \text{used\_pct}_w}{100}, \qquad \text{time\_frac}_w = \text{clamp}\left(\frac{\text{time\_to\_reset}_w}{\text{duration}_w}, 0.05, 1.0\right)$$
   $$E_p = \text{clamp}\left(\text{rem}_w \cdot (1 - \text{time\_frac}_w) \cdot 1.6,\; 0.0,\; 1.0\right)$$
   Interpretation: a window with 70% remaining and only 15% of its time left before reset scores near $1.0$ (high waste risk if unused); a nearly empty window or a freshly reset window scores low. If `used_pct` or `resets_at` is unknown, $E_p = 0.50$ (neutral) and `confidence` is flagged in `why`.

   A longer window that outlasts $w$ caps $\text{rem}_w$ at the room it leaves beyond its own pace, converted with the two burn rates:
   $$\text{room}_w = \text{spare}_o \cdot \frac{\text{burn}_w}{\text{burn}_o}, \qquad \text{rem}_w = \frac{\min(100 - \text{used\_pct}_w,\ \text{room}_w)}{100}$$
   $\text{spare}_o$ is what $o$ leaves unused at its reset at the current burn. $\text{room}_w = 0$ when $o$ is at 100% or runs out before its reset: the short window cannot be spent, so it is not urgent. Without a burn forecast for both windows the cap does not apply. `why` shows "<window> runs out first" or "capped by the room <window> leaves" and the decision row records `expiry_limited_by`. The forecast is read outside the ledger write lock.
3. **Scarcity Preservation ($S_p$):**
   Rewards using less scarce pools and penalizes burning pools whose weekly cap is already tight:
   $$S_{\text{base}} = 1 - \frac{\text{scarcity\_tier}_p - 1}{4}, \qquad S_{\text{weekly}} = \frac{100 - \text{used\_pct}_{\text{weekly}}}{100}$$
   $$S_p = 0.6 \cdot S_{\text{base}} + 0.4 \cdot S_{\text{weekly}}$$
   (If no weekly window exists, $S_p = S_{\text{base}}$.)
4. **Latency / Speed ($L_p$):**
   $$L_p = \frac{\text{speed\_tier}_p - 1}{4}$$

### Built-In Objective Profiles

| Profile | $w_Q$ (quality) | $w_E$ (expiry) | $w_S$ (scarcity) | $w_L$ (latency) | Intent |
|---|---|---|---|---|---|
| `quality-first` | `0.60` | `0.15` | `0.15` | `0.10` | Always pick the strongest capable model first; expiry only breaks near-ties. |
| `burn-expiring-first` | `0.20` | `0.50` | `0.20` | `0.10` | Use soon-to-reset subscription quota before it vanishes, above the role's quality floor. |
| `save-scarce` | `0.20` | `0.25` | `0.45` | `0.10` | Preserve scarce pools (e.g. Claude Opus weekly) for high-tier roles. |
| `balanced` | `0.35` | `0.30` | `0.25` | `0.10` | Default middle ground across quality, expiry, and scarcity. |

### Total Score

$$\text{Score}(p, r) = w_Q Q_p + w_E E_p + w_S S_p + w_L L_p + B_{\text{order}}(p, r) - P_{\text{degraded}}(p) + \Delta_{\text{learn}}(p, r)$$

Where:
- $B_{\text{order}}(p, r) = 0.03 \cdot \left(1 - \frac{\text{index}(p, r.\text{allowed\_providers})}{\max(1, |r.\text{allowed\_providers}| - 1)}\right)$ is a gentle tie-breaker honoring the user's written order in `allowed_providers`.
- $P_{\text{degraded}}(p) = 0.08$ if `p.state == DEGRADED`, else `0.0`.
- $\Delta_{\text{learn}}(p, r) \in [-\text{max\_delta}, +\text{max\_delta}]$ is the bounded learning adjustment (`0.0` if learning is disabled or `samples < min_samples`).

## Bounded Learning and Freezing

Learning keeps one row per `(provider, role)` in the `learning_stats` table. The role is the job's role name. Two sources append a `learning.outcome_recorded` event:

- every `job.completed` seen by `hiraishin watch` (success, wall minutes of the last attempt);
- `hiraishin outcome record <provider> <role> [--failed] [--review-rounds N] [--build-failures N] [--duration-min M]`, for what only you know (review rounds, build failures, a human takeover).

Each outcome carries:

- `success` $\in \{0, 1\}$ (completed without manual takeover; `--failed` sets 0)
- `review_rounds` $\ge 0$ (PR review or fix cycles)
- `build_failures` $\ge 0$ (build or gate failures)
- `duration_min` $\ge 0$ (wall time; `0` means unknown and scores neutral)

For each outcome, a raw quality score $y$ is computed with the `weight_*` keys (defaults shown):

$$y = 0.45 \cdot \text{success} + 0.25 \cdot \frac{1}{1 + \text{review\_rounds}} + 0.20 \cdot \frac{1}{1 + \text{build\_failures}} + 0.10 \cdot \text{clamp}\left(\frac{M_r}{\text{duration\_min}}, 0.2, 1.0\right)$$

$M_r$ is the mean of the EWMA durations of every provider tried for role $r$. The duration term is `1.0` when the duration or $M_r$ is unknown. The event stores $y$ and `ewma_alpha`, so replaying the ledger gives the same stats.

The per-`(provider, role)` EWMA $\hat{y}_{p,r}$ starts at the first $y$ and then updates as:

$$\hat{y}_{p,r} \leftarrow (1 - \alpha)\,\hat{y}_{p,r} + \alpha\,y$$

At each decision, the delta is computed against the role's baseline $\bar{y}_r$, the mean $\hat{y}$ of every provider tried for role $r$:

$$\Delta_{\text{learn}}(p, r) = \text{clamp}\left((\hat{y}_{p,r} - \bar{y}_r) \cdot \text{max\_delta} \cdot 2,\; -\text{max\_delta},\; +\text{max\_delta}\right)$$

The scheduler clamps the delta to `±max_delta` a second time when it adds it to the score.

### Guardrails

- **Cannot break a rule:** Learning runs only in Stage 2 after Stage 1 hard gates. It can never enable a denied provider, bypass `min_quality_tier`, override a pin, fill a full slot, or turn on a metered API. Property-based tests over thousands of random configs check this.
- **Cold-start protection:** $\Delta_{\text{learn}} = 0$ until `samples >= min_samples` (default `5`). A provider alone in a role is its own baseline, so its delta is `0`.
- **One-command freeze:** `hiraishin learn freeze` (or `[learning] frozen = true`) stops outcomes from changing `learning_stats`, so every delta and score stays fixed from the next decision on. Outcomes are still appended to the ledger with `frozen: true`. Freeze is global. `hiraishin learn unfreeze` lets new outcomes count again; outcomes logged while frozen are not replayed.
- **Reset:** `hiraishin learn reset` deletes every learned stat. All deltas return to `0.0` until each pair has `min_samples` new outcomes.
- **Inspect:** `hiraishin learn status` prints the switches and, per role and provider, samples, completed count, $\hat{y}$ and the delta the scheduler adds now. `hiraishin why` shows the same delta as `learned` in each candidate's bonus line.

## The `why` Output Format

Every decision is stored in the `decisions` table and inspectable with `hiraishin why <job_id>` (or `hiraishin why <decision_id>`, `--json` for scripts/MCP):

```text
Decision: dec_01J9X4K2M8 (initial_dispatch)
Job:      job_01J9X4K2 (persist-all, role=gameplay, min_tier=3)
Profile:  burn-expiring-first (Q=0.20, E=0.50, S=0.20, L=0.10)
Chosen:   kimi-native (score 0.742)

Candidates evaluated:
  1. kimi-native       SELECTED  score=0.742
     Q=0.75(0.150) E=0.82(0.410) S=0.65(0.130) L=0.75(0.075) order=+0.030 learn=-0.053
     window 5h: 24% used, resets in 1h12m (exact); weekly: 38% used; slots 1/3
  2. agy-neon-medium   eligible  score=0.631
     Q=0.75(0.150) E=0.48(0.240) S=0.60(0.120) L=0.75(0.075) order=+0.015 learn=+0.031
     window 5h: 18% used, resets in 3h40m (estimated); slots 1/3
  3. claude-sonnet     eligible  score=0.518
     Q=0.75(0.150) E=0.30(0.150) S=0.38(0.076) L=0.75(0.075) order=+0.008 learn=+0.059
     window 5h: 40% used, resets in 3h55m (exact); weekly: 62% used; slots 2/4
  4. glm-strong        REJECTED  state=EXHAUSTED (5h limit hit at 15:09, resets ~20:09)
  5. claude-opus       REJECTED  not in role.gameplay.allowed_providers

Summary: Picked kimi-native because 76% of its 5h window expires in 1h12m under
burn-expiring-first, outweighing claude-sonnet's higher learned score (+0.059).
```

## Runtime Commands

All runtime overrides write an event to the ledger and take effect immediately without restarting `hiraishin watch`:

| Command | Effect | Scope / Persistence |
|---|---|---|
| `hiraishin profile set <name>` | Switches `active_profile` (`quality-first`, `burn-expiring-first`, `save-scarce`, `balanced`, `custom`) | Persists in runtime state until changed |
| `hiraishin prefer <provider> [--role <role>] [--ttl 2h]` | Temporarily boosts `<provider>` to first position in `allowed_providers` (adds `+0.15` preference bonus while still obeying hard gates) | Optional TTL; default until cleared |
| `hiraishin pin <job_id> <provider>` | Pins a job to `<provider>` and sets `never_switch = true` | Job lifetime (or `hiraishin unpin <job_id>`) |
| `hiraishin pause <provider> [--reason "..."]` | Transitions provider to `PAUSED`; no new dispatches or failovers | Until `hiraishin resume <provider>` |
| `hiraishin drain <provider> [--reason "..."]` | Transitions provider to `DRAINING`; running jobs finish, then transitions to `PAUSED` | Until `hiraishin resume <provider>` |
| `hiraishin resume <provider>` | Returns a `PAUSED` or `DRAINING` provider to `AVAILABLE` (or `EXHAUSTED` if window is still empty) | Immediate |
| `hiraishin why <job_id \| decision_id> [--all] [--json]` | Prints the full Stage 1 & Stage 2 trace for the latest (or all) scheduling decisions | Read-only |
| `hiraishin outcome record <provider> <role> [--failed] [--review-rounds N] [--build-failures N] [--duration-min M]` | Appends one outcome for learning (under `hiraishin watch`, `job.completed` records a success and `job.failed` a failure automatically) | Ignored by stats while frozen |
| `hiraishin learn status \| freeze \| unfreeze \| reset` | Inspects per-(provider, role) EWMA stats and current deltas, freezes or unfreezes updates, or deletes all stats (deltas back to `0.0`) | Global |
