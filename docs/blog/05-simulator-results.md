# Post 05: Simulator Results: Quota Used, Stall Minutes Avoided, Failovers

**Target length:** ~1,600 words  
**Status:** Outline (data to be populated once v0.3 simulator runs against recorded SQLite ledgers)

## Hook

Scheduling heuristics sound convincing on a whiteboard until they meet real bursty workloads. Because `hiraishin` stores every sensor sample, job arrival, burn rate, and reset timestamp in an append-only SQLite event ledger, we can replay an entire 12-hour workday offline in under a second and measure what each policy actually saves.

## Sections

1. **Why Build a Deterministic Event-Replay Simulator**
   - Live A/B testing across paid subscriptions takes weeks and confounds workload difficulty with time of day.
   - Replaying the append-only `events` table ([ADR 0003](../adr/0003-sqlite-event-ledger.md)) isolates the scheduler: same job arrivals, same role requirements, same window resets, different policy configs.
2. **The Four Policies Under Test**
   - Baseline A: Static manual assignment (reproducing the 2026-11-21 `fronts.tsv` assignments).
   - Policy B: `quality-first`
   - Policy C: `burn-expiring-first`
   - Policy D: `save-scarce`
   - Policy E: `balanced` (with and without bounded learning enabled)
3. **Metrics That Matter**
   - **Perishable quota utilization (%):** fraction of expiring 5h/daily capacity converted into completed work before reset.
   - **Total stall minutes:** wall time jobs spent in `WAITING_QUOTA` or waiting on human failover.
   - **Mid-task failover count & warm-up overhead:** proving that smarter initial dispatch reduces failovers rather than just automating them.
   - **Scarce-tier weekly reserve remaining:** how much frontier quota (`claude-opus` weekly) remained available for late-day `engine` tasks.
4. **Failure Cases & Surprises**
   - Where `burn-expiring-first` can over-commit a nearly-empty window if `reserve_used_pct` is too high (triggering an avoidable failover 10 minutes before reset).
   - Sensitivity to sensor confidence (`exact` endpoint vs. `estimated` local logs).

## Figures & Data Needed

- **Figure 1 (Grouped Bar Chart — v0.3 output):** Comparison of the 5 policies on (a) stall minutes, (b) expired unused 5h quota %, (c) failover count, and (d) remaining weekly frontier quota.
- **Figure 2 (Sensitivity Curve — v0.3 output):** Failover count vs. `reserve_used_pct` (`85%`, `90%`, `95%`, `98%`, `100%`).
- **Dataset:** Recorded 1-day and 5-day SQLite ledgers from real multi-front `mmorpg` sessions (starting with a reconstructed trace of the 2026-11-21 12-front / 4-provider / 3-stall afternoon).
- **Reproducible fixture:** [Anonymized afternoon comparison](../simulator-2026-11-21.md) contains the checked-in ledger, policy paths, command, and expected table.
