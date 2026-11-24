# MCP server

`hiraishin mcp` runs a local Model Context Protocol server over stdio. It reads the same `preferences.toml` and SQLite ledger as the CLI and calls the same command operations directly in Go. Tool results are the CLI JSON documents, encoded as MCP text content.

The server inherits `HIRAISHIN_CONFIG`, `HIRAISHIN_DATA_DIR`, `HOME`, and the other environment settings from the client process.

## Tools

| Tool | CLI equivalent | Required input |
|---|---|---|
| `hiraishin_status` | `status --json` | none |
| `hiraishin_why` | `why --json` | `id` |
| `hiraishin_pick_provider` | `run --dry-run --json` | none |
| `hiraishin_dispatch` | `run --json` | `brief` |
| `hiraishin_pause` | `pause --json` | `provider` |
| `hiraishin_drain` | `drain --json` | `provider` |
| `hiraishin_pin` | `pin --json` | `job_id`, `provider` |

Status and provider selection accept `no_sensors`. Selection and dispatch accept `role`, `name`, `provider`, `never_switch`, and `backlog`; dispatch also accepts `worktree`, `report`, and `adapter`. Pause and drain accept `reason`. Why accepts `all`.

`hiraishin_dispatch`, `hiraishin_pause`, `hiraishin_drain`, and `hiraishin_pin` mutate the ledger. Dispatch can also start an agent process. There is no `hiraishin_forecast` tool: every window in `hiraishin_status` carries its `forecast` (burn rate, time to empty, waste risk).

## Claude Code

Add this server to `.mcp.json`:

```json
{
  "mcpServers": {
    "hiraishin": {
      "command": "hiraishin",
      "args": ["mcp"]
    }
  }
}
```

## OpenCode

Add this server to `opencode.json`:

```json
{
  "mcp": {
    "hiraishin": {
      "type": "local",
      "command": ["hiraishin", "mcp"],
      "enabled": true
    }
  }
}
```

## Result schemas

- Status: [schemas/status.schema.json](schemas/status.schema.json)
- Why and provider selection: [schemas/why.schema.json](schemas/why.schema.json)
- Dispatch: [schemas/run.schema.json](schemas/run.schema.json)
- Pause, drain, and pin: [schemas/control.schema.json](schemas/control.schema.json)
