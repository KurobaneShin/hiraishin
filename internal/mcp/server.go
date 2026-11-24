package mcp

import (
	"context"
	"encoding/json"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Handler func(context.Context, json.RawMessage) (any, error)

type Handlers struct {
	Version      string
	Status       Handler
	Why          Handler
	PickProvider Handler
	Dispatch     Handler
	Pause        Handler
	Drain        Handler
	Pin          Handler
}

func New(handlers Handlers) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "hiraishin", Version: handlers.Version}, nil)
	add(server, "hiraishin_status", "Show providers, quota windows, slots and active jobs", handlers.Status, []string{}, map[string]any{
		"no_sensors": map[string]any{"type": "boolean", "description": "Use recorded usage without polling sensors"},
	})
	add(server, "hiraishin_why", "Explain provider selection for a job or decision", handlers.Why, []string{"id"}, map[string]any{
		"id":  map[string]any{"type": "string", "description": "Job ID or decision ID"},
		"all": map[string]any{"type": "boolean", "description": "Return every decision for the job"},
	})
	add(server, "hiraishin_pick_provider", "Choose a provider without queuing or launching a job", handlers.PickProvider, []string{}, runProperties(false))
	add(server, "hiraishin_dispatch", "Queue a job, choose a provider and launch it", handlers.Dispatch, []string{"brief"}, runProperties(true))
	add(server, "hiraishin_pause", "Stop new dispatches and failovers to a provider", handlers.Pause, []string{"provider"}, providerStateProperties())
	add(server, "hiraishin_drain", "Stop new work and pause a provider when its jobs finish", handlers.Drain, []string{"provider"}, providerStateProperties())
	add(server, "hiraishin_pin", "Pin a job to a provider and disable automatic switching", handlers.Pin, []string{"job_id", "provider"}, map[string]any{
		"job_id":   map[string]any{"type": "string"},
		"provider": map[string]any{"type": "string"},
	})
	return server
}

func add(server *sdk.Server, name, description string, handler Handler, required []string, properties map[string]any) {
	server.AddTool(&sdk.Tool{
		Name:        name,
		Description: description,
		InputSchema: map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false},
	}, func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		doc, err := handler(ctx, request.Params.Arguments)
		if err != nil {
			return errorResult(err), nil
		}
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return errorResult(err), nil
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(raw) + "\n"}}}, nil
	})
}

func errorResult(err error) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}}, IsError: true}
}

func runProperties(dispatch bool) map[string]any {
	properties := map[string]any{
		"role":         map[string]any{"type": "string", "default": "default"},
		"name":         map[string]any{"type": "string"},
		"provider":     map[string]any{"type": "string"},
		"never_switch": map[string]any{"type": "boolean"},
		"backlog":      map[string]any{"type": "boolean", "description": "Run only on quota forecast to go unused before its reset"},
	}
	if dispatch {
		properties["brief"] = map[string]any{"type": "string", "description": "Absolute or relative path to the task brief"}
		properties["worktree"] = map[string]any{"type": "string", "default": "."}
		properties["report"] = map[string]any{"type": "string"}
		properties["adapter"] = map[string]any{"type": "string", "enum": []string{"", "process", "tmux", "orca"}}
	} else {
		properties["no_sensors"] = map[string]any{"type": "boolean"}
	}
	return properties
}

func providerStateProperties() map[string]any {
	return map[string]any{
		"provider": map[string]any{"type": "string"},
		"reason":   map[string]any{"type": "string"},
	}
}
