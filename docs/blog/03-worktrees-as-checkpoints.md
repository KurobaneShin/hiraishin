# Post 03: Failover Without Losing Work: Git Worktrees as Checkpoints

**Target length:** ~1,700 words  
**Status:** Outline

## Hook

On 2026-11-21, a single feature branch (`fix/loot-drops`) passed through **three different AI vendors** in under an hour: it started on Z.AI GLM-5.3, hit GLM's limit, moved to Moonshot Kimi K3.1 Pro, hit Kimi's 5-hour limit, and finished on Google Antigravity (`gemini-4-neon-medium`) to open PR #249 with passing unit tests. Not a single line of chat history was translated between vendors — because the git worktree was the checkpoint.

## Sections

1. **Why Transcript Translation Fails**
   - Incompatible tool schemas, hidden thinking blocks, per-model prompt caches, and bloated context windows make cross-vendor chat replay slow, brittle, and expensive.
2. **The Worktree Checkpoint Contract**
   - Every task gets an isolated git worktree + branch + brief + `report.md` progress checklist ([ADR 0004](../adr/0004-worktree-as-checkpoint.md)).
   - On exhaustion: stop the stalled CLI cleanly (`SIGINT` -> grace -> `SIGTERM`) without touching uncommitted edits, inspect `git status --porcelain`, `git diff --stat`, `git log origin/main..HEAD`, and `report.md`.
   - Show the real manual handoff prompt from `loot-drops/prompt-kimi.md` that inspired `hiraishin`'s automated resume prompt generator.
3. **Shrinking Cold-Start Warm-Up With Local Tools**
   - How optional adapters cut the resuming agent's orientation phase from ~8 exploratory turns to 1:
     - `ai-memory`: reading `memory_read_session_observations(order="desc")` for the cut-off `ended: false` session to see the last edited files and failing test right before the 429.
     - `qmd`: one hybrid query across briefs/docs/gotchas (`--no-rerank`).
     - `graphify`: injecting a 1,500-token 2-hop subgraph around modified files.
4. **Why Running Jobs Never Switch Back on Reset**
   - Prompt-cache affinity: once Provider B warms up on the worktree, let it finish; when Provider A's window resets, only newly queued or newly failing jobs return to Provider A.

## Figures & Data Needed

- **Figure 1 (Mermaid Sequence Diagram):** Mid-job exhaustion & worktree failover flow (from `docs/architecture.md`).
- **Figure 2 (Real Relay Diagram):** The `loot-drops` 3-hop relay on 2026-11-21 (`glm-strong` -> `kimi-worker` -> `agy gemini-4-neon-medium` -> commits `199a5614` & `de05738b` -> PR #249) and `persist-all` (`kimi-worker` -> `agy` -> PR #248 merged in 2 minutes after opening).
- **Code/Prompt Box:** Sanitized diff between the manual `prompt-kimi.md` and `hiraishin`'s structured resume prompt template.
