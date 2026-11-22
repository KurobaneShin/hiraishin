# Deterministic Simulator Fixture: 2026-11-21 Afternoon

This anonymized, synthetic ledger models the 2026-11-21 afternoon with 12 fronts, four providers, and three recorded stalls. It is not a transcription of provider account data, job names, repository paths, or measured production results.

The fixture covers 12:00–18:00 UTC. The policies have identical provider inventory and hard gates; only the active objective profile changes. Learning is disabled.

Run from the repository root:

```sh
go run ./cmd/hiraishin sim \
  --ledger internal/sim/testdata/2026-11-21/afternoon.db \
  --policies internal/sim/testdata/2026-11-21/quality-first.toml,internal/sim/testdata/2026-11-21/burn-expiring-first.toml,internal/sim/testdata/2026-11-21/save-scarce.toml,internal/sim/testdata/2026-11-21/balanced.toml
```

| Policy | Perishable quota used (%) | Expired wasted quota (%) | Total stall minutes | Failover count | Scarce-pool reserve left (%) |
|---|---:|---:|---:|---:|---:|
| quality-first | 17.31 | 8.31 | 0.00 | 2 | 58.33 |
| burn-expiring-first | 12.88 | 12.75 | 0.00 | 0 | 60.00 |
| save-scarce | 12.50 | 13.12 | 0.00 | 0 | 60.00 |
| balanced | 15.88 | 9.75 | 0.00 | 0 | 58.33 |

Job runtimes come from `job.completed.duration_min`. For each provider and window, positive deltas between consecutive quota samples determine the burn rate per active job. The replay applies that rate to the provider selected by each policy, while preserving arrivals, remaining work after failover, and reset timestamps.

Perishable utilization and expired waste use the combined capacity of rolling and fixed-reset windows shorter than one week, including capacity added by resets inside the trace. Stall minutes sum each job's time waiting for an eligible provider. Failovers count mid-job moves to a different provider. Scarce reserve is the remaining weekly quota of the highest-scarcity pool at the trace horizon.

`TestAnonymizedFixtureComparison` replays these files and fails if the table above becomes stale.
