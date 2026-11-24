# Provider & Harness Adapters

Each file below specifies how `hiraishin` launches, monitors, senses quota for, and fails over a specific coding-agent provider without proxying API traffic ([ADR 0001](../adr/0001-no-proxy.md)).

| Provider | File | Harnesses Covered | Quota Windows | Primary Sensor Tier |
|---|---|---|---|---|
| **Claude Code** | [claude-code.md](claude-code.md) | Official `claude` CLI (Claude Pro/Max/Ultra/Team) | Rolling 5h + fixed weekly | OAuth usage endpoint / CLI hook context + `~/.claude/` JSONL |
| **Kimi Code** | [kimi.md](kimi.md) | Native Kimi Code CLI (`kimi`) **and** OpenCode (`kimi-worker`) | Rolling 5h + weekly/monthly cap | `api.kimi.com/coding/v1/usages` + output regex |
| **Z.AI GLM Coding Plan** | [glm.md](glm.md) | Z.AI recommended (`claude` via `api.z.ai/api/anthropic`, ZCode) **and** OpenCode (`glm-worker` / `glm-strong`) | Rolling 5h + fixed 7-day weekly | `api.z.ai/api/monitor/usage/quota/limit` + output regex |
| **Google Antigravity** | [antigravity-agy.md](antigravity-agy.md) | Official `agy` CLI | Rolling / RPM+TPM burst (tier-dependent) | Output regex (`429` / `401` / `UNAUTHENTICATED`) + local accounting |
| **OpenAI Codex CLI** | [codex.md](codex.md) | Official `codex` CLI (`openai/codex`) | Rolling 5h + weekly cap | Local JSONL rollout logs (`~/.codex/sessions/`) + output regex |
| **Google Gemini CLI** | [gemini-cli.md](gemini-cli.md) | Official `gemini` CLI (`google-gemini/gemini-cli`) | Per-minute (RPM/TPM) + daily RPD (midnight PT) | Output regex (`RESOURCE_EXHAUSTED` / `429`) + local request counter |

> **Verification note:** CLI flags for `claude`, `opencode`, and `agy` were verified directly against the installed binaries on 2026-11-21 (`--help`). External provider documentation, usage endpoints, and Terms of Service clauses cite their canonical URLs; items that could not be directly verified against live API responses or full legal text are explicitly tagged `[unverified]`.
