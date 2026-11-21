# Post 04: Twelve Agents on One Workstation: Disk, GPU, and Quota

**Target length:** ~2,200 words  
**Status:** Outline

## Hook

At 14:10 my workstation had 62 GB free on `C:`. Sixteen minutes later, at 14:26, it had 10 GB free and every terminal died. That afternoon — running 12 parallel coding fronts across Claude, Kimi, GLM, and Antigravity on a single 40 GB WSL2 machine — taught us that multi-agent throughput is not bottlenecked by prompts; it is bottlenecked by shared physical resources (disk, RAM, GPU) and asynchronous quota ceilings.

## Sections

1. **The Setup: 12 Parallel Fronts on 4 Providers**
   - Architecture of the `mmorpg` orchestration (`orca-ide` worktrees, `front.sh`, `merge-loop.sh`, `agy-front.sh`, `fronts.tsv`).
   - Provider split across the cycle: Claude Opus/Sonnet for engine/art/web fronts, Kimi K3.1 Pro (`kimi-worker`) for `char-flow`/`persist-all`/`loot-drops`, GLM-5.3 (`glm-strong`) for `loot-drops`, and Antigravity (`gemini-4-neon-high` / `medium`) for `neon-perf` and failover rescue.
2. **Failure Mode 1: The 16-Minute Disk Cliff**
   - Minute-by-minute reconstruction from `merge-loop.log`:
     - `14:10`: 62 GB free (`web-seo` merged)
     - `14:16`: 53 GB free -> `14:19`: 46 GB -> `14:20`: 39 GB -> `14:22`: 31 GB -> `14:24`: 21 GB -> `14:26`: **10 GB free** (52 GB burned in 16 minutes; ~3.25 GB/min from concurrent C++ debug builds and Hugging Face model pulls).
   - Every terminal died; 22-minute recovery window (`14:26` to `14:48`, back to 49 GB, then 81 GB at `14:51`), followed by a second WSL freeze restart (`resume-wsl.md`).
   - Operational fixes we invented that day: centralized single-worktree builds in `merge-loop.sh` (`~/orca/verify`), `mmo-slot` (max 3 heavy jobs, 20 GB RAM cgroup cap, exit 75 below 40 GB free C:), and `flock ~/gen3d/gpu.lock` for GPU jobs.
3. **Failure Mode 2: Cascading Quota Stalls**
   - `loot-drops` hit GLM Coding Plan's limit -> manually reassigned to `kimi-worker`.
   - With 3 fronts now hammering Kimi simultaneously (`char-flow`, `persist-all`, `loot-drops`), Kimi hit its 5-hour limit (`429 You've reached your 5-hour usage limit`) across all three.
   - Manual failover of all three worktrees onto `agy` (`gemini-4-neon-medium`): `persist-all` merged PR #248 at 15:35; `loot-drops` opened PR #249 at 15:39.
4. **What Belongs in `aq` vs. OS Resource Guards**
   - Why `aq` focuses on quota scheduling, concurrency caps (`max_concurrent`), and automated worktree failover, while exposing `launch_prefix` hooks (`flock`, `mmo-slot`) so OS-level RAM/disk/GPU guards compose cleanly instead of being reimplemented poorly.

## Figures & Data Needed

- **Figure 1 (Line Chart from `merge-loop.log`):** `C:` free GB over time from `13:50` to `15:40` showing the `62G -> 10G` 16-minute cliff (`14:10–14:26`), recovery to `81G` (`14:51`), and steady `62–68G` plateau after centralized builds + `mmo-slot`.
- **Figure 2 (Swimlane Timeline):** The 12 active fronts across the 4 providers showing the 3 quota stalls, manual failover latencies, and final PR merges (#245, #246, #248, #249).
- **Table 1:** Hard workstation constraints (40 GB WSL RAM, max 3 heavy slots × 20 GB, 40 GB disk floor, 1 GPU lock) vs. provider concurrency caps.
