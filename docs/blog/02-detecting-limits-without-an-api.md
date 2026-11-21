# Post 02: Detecting Limits From the Outside: Five CLIs Without an API

**Target length:** ~1,800 words  
**Status:** Outline

## Hook

If you refuse to put a reverse proxy in front of your coding agents, you lose the one thing proxies get for free: HTTP response headers. Every provider hides its quota behind a different combination of undocumented OAuth endpoints, multi-gigabyte local SQLite databases, JSONL transcripts, and inconsistent stderr banners. Here is how to sense limits reliably from the outside without violating vendor Terms of Service.

## Sections

1. **The Constraint: No Proxy, No Cookie Scraping**
   - Why `aq` treats official CLIs (`claude`, `kimi`, `opencode`, `agy`, `codex`, `gemini`) as black-box child processes.
2. **The Four-Tier Sensor Hierarchy**
   - Tier 1: Read-only first-party usage endpoints (`api.anthropic.com/api/oauth/usage`, `api.kimi.com/coding/v1/usages`, `api.z.ai/api/monitor/usage/quota/limit`) — exact percentages, but fragile schemas and strict rate limits (e.g. Anthropic `429` with `retry-after: 0`).
   - Tier 2: Local harness storage (`~/.claude/projects/**/*.jsonl`, `~/.codex/sessions/**/rollout-*.jsonl`, `~/.local/share/opencode/opencode.db` — which reached 8.8 GB on a single dev box, requiring indexed queries or `opencode stats`).
   - Tier 3: Recorded output-regex fixtures over PTY / `stream-json` (real patterns: Kimi `429 You've reached your 5-hour usage limit`, `agy` `401 UNAUTHENTICATED` vs. `429 RESOURCE_EXHAUSTED`, Z.AI `1113` vs. `1302`).
   - Tier 4: Own EWMA burn-rate estimator in the SQLite ledger.
3. **The Hard Classification Bugs**
   - `EXHAUSTED` vs. `DEGRADED` (HTTP 529 overloaded or 60-second RPM burst vs. 5-hour/daily cap — why Gemini CLI needs a 65-second confirmation probe).
   - `EXHAUSTED` vs. `AUTH_ERROR` (why `agy` `401 UNAUTHENTICATED` must wake the human instead of waiting 5 hours for a phantom reset).
   - Shared quota pools (`claude-opus` + `claude-sonnet` sharing one Claude Max pool; `glm-strong` burning 3–4x faster than `glm-flash` out of one Z.AI pool).
4. **Learning From Prior Art (`quota-axi` and `quotamux`)**
   - How `quota-axi` solves read-only multi-vendor probing and how `aq` uses recorded test fixtures so regex regressions fail CI immediately.

## Figures & Data Needed

- **Figure 1 (Architecture Diagram):** The 4-tier sensor funnel feeding `sensor.quota_sampled` and `provider.limit_hit` into the SQLite event ledger with `confidence` tags (`exact`, `estimated`, `inferred`).
- **Table 1:** Provider-by-provider sensor matrix (endpoint URL, local log path, exact error fixture, main ambiguity).
- **Real fixture snippets:** Kimi 5h message, `agy` 401/429 guard from `agy-front.sh`, Anthropic `oauth-2025-04-20` rate-limit caveat, OpenCode `8.8 GB` `opencode.db` size observation.
