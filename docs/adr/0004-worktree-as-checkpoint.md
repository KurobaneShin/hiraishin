# 0004. Git worktree as the failover checkpoint

Date: 2026-11-21
Status: Accepted

## Context

Because each provider runs in its own CLI with incompatible conversation logs and per-model prompt caches, failing over a stalled agent mid-task cannot rely on replaying raw chat transcripts across vendors.

## Decision

Treat the task's isolated git worktree (committed history, uncommitted working-tree diff, and progress report file) as the canonical checkpoint. On exhaustion, `aq` stops the stalled process without cleaning the worktree and launches the next allowed provider in the same directory with a structured resume prompt.

## Consequences

Failover works uniformly across every harness (Claude, Kimi, GLM, Antigravity, Codex, Gemini) and survives crashes or disk-full restarts without losing edits. Resumed agents spend a short warm-up turn inspecting `git status`/`git diff` (mitigated when optional `ai-memory`, `qmd`, or `graphify` adapters are enabled).
