# Post 06: Preferences That Adapt Without Taking Control Away From the User

**Target length:** ~1,600 words  
**Status:** Outline

## Hook

Developers hate "smart" routers that silently downgrade a tough concurrency bug to a cheap model to save 4% on a mysterious internal metric. They also hate babysitting static config files every time a provider ships a better model or has a bad week. There is a clean middle ground: hard user gates, explicit weighted objectives, small clamped learning deltas, and a `why` command that prints the exact arithmetic.

## Sections

1. **Why Ophiraishinue Auto-Routers Lose Trust**
   - The control problem in developer tooling: one surprise bad routing decision destroys confidence in twenty good ones.
   - Contrast black-box bandit routers with glass-box policy engines ([ADR 0005](../adr/0005-user-preferences-over-learned-scores.md)).
2. **Two-Stage Scheduling: Hard Gates First, Scoring Second**
   - Stage 1 (non-negotiable): enabled, state, metered opt-in, pin, `allowed_providers` / `deny_providers`, `min_quality_tier` (optionally escalated by `graphify` god-node centrality), `max_concurrent`, reserve floor.
   - Stage 2 (linear, inspectable): weighted sum of normalized Quality, Expiry, Scarcity, Latency + list-order tie-break + clamped learned delta.
3. **Bounded Learning Without Drift**
   - How per-`(provider, role)` outcomes (completion without rescue, PR review rounds, build/gate failures, duration vs. median) feed an EWMA clamped to `[-0.12, +0.12]`.
   - Why `min_samples = 5`, `hiraishin learn freeze`, and `hiraishin learn reset` matter when a provider changes model quality overnight.
   - Mirroring outcomes into `ai-memory` (`hiraishin/outcomes/`) so both humans and agents can audit empirical performance over weeks.
4. **Explainability as a First-Class CLI Primitive (`hiraishin why`)**
   - Walk through a real `hiraishin why` output line by line, showing why a candidate won and why each rejected candidate failed.
   - Runtime overrides (`prefer`, `pin`, `pause`, `drain`, `profile set`) that change behavior in one command without editing TOML.

## Figures & Data Needed

- **Figure 1 (Diagram):** Two-stage funnel (Stage 1 Hard Gates -> Stage 2 Linear Score + Clamped $\Delta_{\text{learn}} \in [-0.12, +0.12]$ -> `decisions` table -> `hiraishin why`).
- **Figure 2 (Annotated Terminal Output):** Real `hiraishin why` readout from `docs/preferences.md` showing exact feature contributions and rejection reasons.
- **Worked Example Table:** Three real `mmorpg` roles (`engine`, `gameplay`, `mechanical`) showing how `quality-first`, `burn-expiring-first`, and `save-scarce` rank the same 4 providers differently while obeying the same hard gates.
