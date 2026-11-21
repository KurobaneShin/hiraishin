# 0002. Go single static binary

Date: 2026-11-21
Status: Accepted

## Context

`hiraishin` must run concurrent terminal watchers, sensor pollers, reset timers, and an MCP stdio server on developer workstations (Linux, WSL2, macOS) without introducing runtime version conflicts or heavy memory overhead alongside 10+ active agent processes.

## Decision

Implement `hiraishin` in Go as a single statically linked binary using pure-Go SQLite (`modernc.org/sqlite`) and no mandatory Node/Python runtime.

## Consequences

Distribution is one binary with fast startup, low RSS, and native goroutine concurrency for PTY/terminal watching. If we invoke an external TypeScript tool like `quota-axi` as an optional sensor fallback, it remains strictly opt-in behind a subprocess adapter rather than a build or runtime dependency.
