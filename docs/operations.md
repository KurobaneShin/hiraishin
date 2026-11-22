# Operations

## `hiraishin doctor`

Run a pre-flight health check without changing state:

```bash
hiraishin doctor
hiraishin doctor --json
```

It checks:

1. **Harness binaries** — every enabled provider's harness binary is on `PATH`.
2. **Auth freshness** — providers in `AUTH_ERROR`, Claude credentials expiry, and required API-key environment variables.
3. **Sensor freshness** — the newest window sample for each enabled provider is within twice its configured `poll_interval_sec`.
4. **Worktree health** — every `RUNNING`, `DISPATCHING` and `FAILING_OVER` job is in a valid git worktree on the expected branch.
5. **Integration reachability** — `ai-memory`, `qmd` and `graphify` are reachable when enabled in `preferences.toml`.

`doctor` reports failures but never prints a secret token value.

## `hiraishin ledger archive`

Move old events out of the live ledger without breaking live projections, `status`, or `why`:

```bash
hiraishin ledger archive --before 30d
hiraishin ledger archive --before 30d --to /backups/hiraishin-2026-11-01.db
```

Events older than the cutoff are moved into the archive file if they belong to a terminal job (`COMPLETED`, `FAILED`, `CANCELLED`) whose last update is also older than the cutoff, or if they have no `job_id` at all (provider-only events). Events for live jobs, recent decisions and current projections stay in the live ledger.

The command runs in a single transaction and is safe to run while `hiraishin watch` is active.
