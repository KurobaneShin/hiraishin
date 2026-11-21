# Post 01: Subscription Quota Is Perishable Inventory

**Target length:** ~1,600 words  
**Status:** Outline

## Hook

At 15:10 on a Saturday afternoon, three of my coding-agent fronts stopped dead on a 5-hour usage cap while two other subscriptions I was already paying for sat idle — one of them scheduled to reset in 90 minutes with most of its window untouched. We manage flat-rate AI coding subscriptions as if they were metered APIs, when economically they behave like airline seats: any capacity left at reset vanishes forever.

## Sections

1. **The Multi-Subscription Reality**
   - Why solo builders and small teams stack flat-rate coding plans (Claude Max, Kimi Code, Z.AI GLM Coding Plan, Google Antigravity, Codex) instead of paying raw per-token API rates for multi-hour agent loops.
   - The two opposite failure modes: mid-task stalls on an exhausted plan vs. silent expiration of unused rolling 5-hour/daily windows.
2. **Perishable Inventory Economics**
   - Contrast metered APIs (cost = tokens consumed) with flat-rate rolling windows (sunk cost; marginal cost of unused capacity before reset = 0; cost of waste = lost throughput).
   - Introduce the two-axis trade-off: **expiry urgency** vs. **quality floor** and **weekly scarcity**.
3. **Why Proxies Are the Wrong Answer**
   - Why API proxies over subscription OAuth logins break prompt caches, tool-calling formats, and vendor Terms of Service ([ADR 0001](../adr/0001-no-proxy.md)).
   - Scheduling at the **task and process boundary** instead of the HTTP request boundary.
4. **A 20-Line Mental Model for `aq`**
   - Show the intended CLI flow (`aq init`, `aq status`, `aq profile set burn-expiring-first`, `aq run`, `aq why`).
   - Tease the rest of the series (sensing without APIs, worktree checkpoints, the 12-front incident, simulator benchmarks, bounded preferences).

## Figures & Data Needed

- **Figure 1 (Timeline Gantt / Burn Chart):** Four parallel 5-hour rolling windows over an 8-hour workday comparing "always pick default model" (red stall gaps + gray expired quota) vs. `burn-expiring-first` (higher utilization, zero idle stalls).
- **Table 1:** Window structure comparison across the 6 providers (rolling 5h, fixed weekly, daily midnight PT RPD) pulled from `docs/providers/README.md`.
- **Real anchor data (2026-11-21):** 12 parallel fronts on 4 providers (Claude, Kimi, GLM, Antigravity), 3 quota stalls and manual failovers (`loot-drops`, `persist-all`, `char-flow`) in a single afternoon.
