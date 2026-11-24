# 0001. No API or login proxy

Date: 2026-11-21
Status: Accepted

## Context

Subscription-backed coding tools (Claude Code, Kimi Code, Z.AI GLM Coding Plan, Antigravity, Codex, Gemini CLI) bind their quotas to official client authentication and terms of service that prohibit proxying or credential pooling. Proxy routers also break provider-specific tool-calling, prompt caching, and agentic hooks.

## Decision

`hiraishin` never proxies API traffic, terminates TLS, extracts session cookies, or relays tokens across harnesses. Every model runs natively inside its own official CLI (or the user's configured harness such as OpenCode), and `hiraishin` orchestrates processes from the outside.

## Consequences

Mid-turn model switching inside a single HTTP request is impossible; failover happens at the process/worktree boundary instead. In exchange, `hiraishin` stays compliant with subscription terms, preserves native CLI features (skills, hooks, prompt cache), and avoids maintaining brittle wire-format translators.
