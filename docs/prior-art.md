# Prior Art Survey

Surveyed on 2026-11-21 from public repositories, READMEs, and package metadata (nothing installed or executed).

## Executive Verdict

No single existing open-source project combines all five properties `aq` requires:
1. **Perishable-first scheduling** across rolling 5h / weekly / daily subscription windows,
2. **Mid-task cross-vendor failover** using the git worktree as the recovery checkpoint,
3. **User-chosen, explainable, bounded-adaptive preferences** (`why` traces + hard policy gates),
4. **No API proxy / no OAuth credential relaying** ([ADR 0001](adr/0001-no-proxy.md)), and
5. **First-class coverage** of Claude Code, Kimi Code (native + OpenCode), Z.AI GLM Coding Plan (native/Claude-compatible + OpenCode), Google Antigravity (`agy`), Codex, and Gemini CLI.

Before writing v0.1 from scratch, run a 1-hour hands-on evaluation of [`agent-tools-org/ai-dispatch`](https://github.com/agent-tools-org/ai-dispatch) (closest architectural neighbor) to verify whether its fallback preserves uncommitted worktrees and resumes across providers, and decide build-from-scratch vs. upstream contribution.

---

## Feature Matrix

| Project | Repo / URL | Lang & License | No Proxy | Worktree Checkpoint | Perishable Scheduling | Mid-Task Failover | Kimi / GLM / `agy` | Primary Role |
|---|---|---|---|---|---|---|---|---|
| **`ai-dispatch` (`aid`)** | [agent-tools-org/ai-dispatch](https://github.com/agent-tools-org/ai-dispatch) | Rust, MIT | Yes | Yes | Partial (profiles/budget; no perishable expiry math stated) | Unverified worktree carry-over on 429 | Kimi/GLM via OpenCode only; `agy` listed | Multi-AI CLI dispatcher |
| **`swrm`** | [surlymochan/swrm](https://github.com/surlymochan/swrm) | Python, Apache-2.0 | Yes | Yes | Partial (5h/7d/30d windows, weighted routing) | Lease-based retry; planner fixed to Claude | OpenCode Go / DeepSeek; no native Kimi, GLM, `agy` | Heterogeneous agent dispatcher |
| **`quota-axi`** | [kunchenguid/quota-axi](https://github.com/kunchenguid/quota-axi) | TypeScript, MIT | Yes | N/A | Runway sort (display only) | No (display/data only by design) | **Yes — Claude, Codex, Kimi, Z.AI, `agy`, Copilot, Cursor, etc.** | Local-first quota sensor |
| **`quotamux`** | [yandie-AI/quotamux](https://github.com/yandie-AI/quotamux) | Python, MIT | No (local gateway/router) | No | Picks most-remaining quota | Request-level routing | Kimi, MiniMax, Anthropic | Multi-subscription router/gateway |
| **`hydra`** | [saadnvd1/hydra](https://github.com/saadnvd1/hydra) | Go, MIT | Yes | No | No | Reactive PTY regex handoff (1 session) | No Kimi/GLM/`agy` | Interactive PTY failover wrapper |
| **`tasuki`** | [0xkohe/tasuki](https://github.com/0xkohe/tasuki) | Go, MIT | Yes | No | No | Reactive PTY failover (Claude, Codex, Copilot) | No Kimi/GLM/`agy` | Interactive PTY failover wrapper |
| **`opencode-quota` / failover plugins** | [slkiser/opencode-quota](https://github.com/slkiser/opencode-quota) | TypeScript, MIT | Inside OpenCode | No | No | Retries request on backup model inside OpenCode | OpenCode providers only | OpenCode plugin |
| **`OmniRoute`** | [diegosouzapw/OmniRoute](https://github.com/diegosouzapw/OmniRoute) | JS/TS, MIT (~89k stars) | **No (API proxy)** | No | Quota-aware routing | Proxy request retry | Proxy-level | Subscription API proxy (**out of scope**) |
| **`CLIProxyAPI`** | [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | Go, MIT (~59k stars) | **No (API proxy)** | No | Pool routing | Proxy request retry | Proxy-level | Subscription API proxy (**out of scope**) |
| **`Quotio`** | [nguyenphutrong/quotio](https://github.com/nguyenphutrong/quotio) | Swift/macOS, MIT (~4.9k stars) | **No (wraps CLIProxyAPI)** | No | Quota routing UI | Proxy-level | Proxy-level | macOS quota & proxy manager (**out of scope**) |
| **`CodexBar`** | [steipete/CodexBar](https://github.com/steipete/CodexBar) | Swift/macOS, MIT (~22.7k stars) | Yes | N/A | Display only | No | Claude, Codex, Z.AI, etc. | macOS menu bar quota display |
| **`ccusage`** | [ccusage/ccusage](https://github.com/ccusage/ccusage) | TypeScript, MIT (~19.5k stars) | Yes | N/A | Local JSONL cost/token reports | No | Claude Code JSONL | Local usage analyzer |
| **`ClaudeMeter`** | [eddmann/ClaudeMeter](https://github.com/eddmann/ClaudeMeter) | Swift/macOS, MIT `[unverified exact license]` | Yes | N/A | Display + `~/.claudemeter/usage.json` | No | Claude only | macOS menu bar monitor |
| **`AgentLimits`** | [b01b02014/agent-limit-monitor](https://github.com/b01b02014/agent-limit-monitor) | Desktop widget `[unverified exact license]` | Yes | N/A | Display + reset countdown + safe-resume hook | Restart on reset only | Claude, Codex, Antigravity | Desktop overlay monitor |
| **`ModelDeck`** | [timharris707/modeldeck](https://github.com/timharris707/modeldeck) | SwiftUI + Node, macOS `[unverified exact license]` | Yes | N/A | Display + manual account switch | Manual switch | Claude, Codex | macOS menu bar switcher |

---

## Detailed Notes & Reuse Analysis

### 1. `quota-axi` — Candidate Sensor Layer

- **Repository:** [https://github.com/kunchenguid/quota-axi](https://github.com/kunchenguid/quota-axi)
- **License:** **MIT License** (© 2026 Kun Chen) — fully compatible with both MIT and Apache-2.0.
- **Philosophy alignment:** Explicitly local-first, read-only, no proxy, no credential minting, and delegates expired token refresh to the vendor's official non-interactive CLI. Covers almost every provider on our stack (Claude, Codex, Kimi, Z.AI, Antigravity, Copilot, Cursor, OpenCode Go, DeepSeek, MiniMax).
- **CLI vs. Library surface (verified):**
  - **CLI:** `npx -y quota-axi --json --sort runway` and `npx -y quota-axi auth` (requires Node.js `>= 22.19`).
  - **Library:** `package.json` **does** expose a programmatic TypeScript/ESM entry point:
    ```json
    "exports": {
      ".": {
        "types": "./dist/src/index.d.ts",
        "default": "./dist/src/index.js"
      }
    }
    ```
- **How `aq` reuses it:** Because `aq` is a single static Go binary ([ADR 0002](adr/0002-go-single-binary.md)), it cannot import a TypeScript module in-process without bundling Node. We use a two-step reuse strategy:
  1. **v0.1 fast path (opt-in subprocess adapter):** When `sensor.quota_axi_fallback = true` and `quota-axi` (or `node >= 22.19`) is present locally, `aq` shells out to `quota-axi --json` on the configured poll interval and normalizes its JSON into `sensor.quota_sampled` ledger events.
  2. **v0.2 native Go sensors:** Port the MIT-licensed first-party endpoint probe specs for Claude, Kimi (`api.kimi.com/coding/v1/usages`), Z.AI (`api.z.ai/api/monitor/usage/quota/limit`), and Antigravity into native Go (preserving MIT attribution), removing the Node dependency completely.

### 2. `ai-dispatch` (`aid`) — Closest Orchestrator Neighbor

- **Repository:** [https://github.com/agent-tools-org/ai-dispatch](https://github.com/agent-tools-org/ai-dispatch) (Rust, MIT).
- **Strengths:** Isolated git worktree per task, multi-CLI dispatch (`claude`, `codex`, `gemini`, `opencode`, `agy`), transcripts, verification checks, task profiles.
- **Gaps for our stack:** Does not explicitly model perishable-inventory scheduling (`burn-expiring-first` vs. `save-scarce`), does not support native Kimi CLI or `glm-claude`, lacks our bounded per-`(provider, role)` learning + `why` arithmetic, and lacks `ai-memory`/`qmd`/`graphify` failover enrichment.
- **Action before coding v0.1:** Spend 1 hour testing `aid` locally on a dummy worktree to confirm its exact failover behavior and decide whether any shared concepts or PRs belong upstream.

#### Hands-on result (2026-11-21, `aid` v11.14.1, source `63b6f32`)

Setup: release binary (checksum verified) in a scratch repo under `/tmp`, isolated `AID_HOME`, two mock agents on `PATH`. Agent A (`oz`) edits a tracked file, creates an untracked file, prints `429 You've reached your 5-hour usage limit` and exits 1. Agent B (`codex`) records its cwd, argv, `git status` and `git log`. Command: `aid run oz "Add a feature" -w feat/x --cascade codex`.

| Question | Observed |
|---|---|
| Uncommitted files kept? | Yes, but not as dirty files: `failure_salvage` makes a WIP commit (`wip: partial work salvage (task … failed)`) and writes `partial-work.md` in aid's task dir. |
| Resume on another provider? | Yes, in the same worktree (`inherit_cascade_target`), as a new child task. |
| Resume context for agent B? | No. Agent B receives the original prompt plus aid's boilerplate. No git status/diff, no reference to `partial-work.md`, no progress report. |
| Limit classified as quota? | No for this message: the run failed as a generic `exit code 1`; `aid agent quota` still showed the agent as `UNKNOWN`, no hold recorded. Cascade fires on any failure listed in `--cascade`. |
| Perishable-window scoring, user hard gates with a numeric `why` trace, pause/drain | Not present (aid ranks by advice, price, leaderboard and holds). |
| Native `kimi`, `glm-claude`, tmux and Orca surfaces | Not present (Kimi only as an OpenCode model; no terminal adapters). |

Decision: **build hiraishin**. aid is a large (≈190k lines Rust) general dispatcher whose core model (advice, price, cascade on failure) differs from ours (perishable quota, user gates, limit-specific failover with a resume prompt). Ideas worth borrowing: salvage summary of dirty state and inheriting the worktree on cascade. hiraishin keeps the dirty files uncommitted ([ADR 0004](adr/0004-worktree-as-checkpoint.md)) and puts the state in the resume prompt instead.

### 3. `hydra` and `tasuki` — PTY Output Failover Pattern

- **Repositories:** [saadnvd1/hydra](https://github.com/saadnvd1/hydra) (Go, MIT), [0xkohe/tasuki](https://github.com/0xkohe/tasuki) (Go, MIT).
- **What we reuse conceptually:** Both are written in Go and demonstrate clean PTY wrapping + regex limit matching + automatic session handoff across coding CLIs. `aq` adopts the same Go PTY/output-watcher ergonomics, extending it from one interactive terminal to N parallel worktree fronts with Orca/`tmux` adapters and proactive scheduling.

### 4. `OmniRoute`, `CLIProxyAPI`, `Quotio` — Explicitly Out of Scope

- Despite large star counts, all three operate by proxying API requests through OAuth tokens harvested from consumer coding subscriptions.
- **Why rejected ([ADR 0001](adr/0001-no-proxy.md)):** Direct Terms of Service violation risk (Anthropic Consumer Terms, Google Code Assist Terms, Z.AI Coding Plan Usage Policy), breakage of native CLI tool-calling/MCP/hooks, and prompt-cache invalidation.

### 5. `ccusage`, `CodexBar`, `ClaudeMeter`, `AgentLimits`, `ModelDeck` — Reference Monitors

- Display-only (mostly macOS SwiftUI menu-bar apps).
- **What we reuse conceptually:** `ccusage`'s `~/.claude/projects/**/*.jsonl` token-field parsing conventions and `AgentLimits`'s countdown-to-reset UX pattern for `aq status` and the v0.3 TUI.
