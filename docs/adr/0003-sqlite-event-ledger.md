# 0003. Append-only SQLite event ledger

Date: 2026-11-21
Status: Accepted

## Context

`hiraishin` needs crash-safe state across daemon restarts, explainable scheduling audits (`hiraishin why`), EWMA burn-rate forecasting, and deterministic replay for the v0.3 policy simulator.

## Decision

Store every sensor sample, limit hit, scheduling decision, job state change, failover, and outcome as an immutable row in a local SQLite database (WAL mode), with materialized projection tables updated transactionally.

## Consequences

Every decision is auditable after the fact and a full day's workload can be replayed offline against alternative objective profiles. Schema changes require lightweight forward migrations and periodic pruning or archiving of high-frequency sensor samples.
