# 0006. Local-only execution and storage

Date: 2026-11-21
Status: Accepted

## Context

Coding-agent orchestration touches proprietary repositories, briefs, credentials, and usage patterns; developers will not adopt a quota tool that phones home or requires a hosted account.

## Decision

`hiraishin` runs 100% locally: configuration, SQLite ledger, logs, and the stdio MCP server stay on the local machine, and the only outbound network calls are optional read-only first-party usage probes explicitly enabled by the user.

## Consequences

No account signup, cloud sync, or cross-machine fleet coordination is included in v0.1–v0.3. Users get zero-trust privacy guarantees and full offline resilience for local scheduling and simulation.
