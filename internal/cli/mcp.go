package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/ledger"
	mcpserver "github.com/KurobaneShin/hiraishin/internal/mcp"
)

type mcpStatusInput struct {
	NoSensors bool `json:"no_sensors"`
}

type mcpWhyInput struct {
	ID  string `json:"id"`
	All bool   `json:"all"`
}

type mcpRunInput struct {
	Role        string `json:"role"`
	Worktree    string `json:"worktree"`
	Brief       string `json:"brief"`
	Name        string `json:"name"`
	Report      string `json:"report"`
	Adapter     string `json:"adapter"`
	Provider    string `json:"provider"`
	NeverSwitch bool   `json:"never_switch"`
	Backlog     bool   `json:"backlog"`
	NoSensors   bool   `json:"no_sensors"`
}

type mcpProviderStateInput struct {
	Provider string `json:"provider"`
	Reason   string `json:"reason"`
}

type mcpPinInput struct {
	JobID    string `json:"job_id"`
	Provider string `json:"provider"`
}

func mcpCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the hiraishin control plane over stdio MCP",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			server := mcpserver.New(mcpserver.Handlers{
				Version: version(),
				Status: func(ctx context.Context, raw json.RawMessage) (any, error) {
					var in mcpStatusInput
					if err := decodeMCPInput(raw, &in); err != nil {
						return nil, err
					}
					return a.status(ctx, in.NoSensors, nil)
				},
				Why: func(ctx context.Context, raw json.RawMessage) (any, error) {
					var in mcpWhyInput
					if err := decodeMCPInput(raw, &in); err != nil {
						return nil, err
					}
					if in.ID == "" {
						return nil, errors.New("id is required")
					}
					return a.why(ctx, in.ID, in.All)
				},
				PickProvider: func(ctx context.Context, raw json.RawMessage) (any, error) {
					in, err := decodeMCPRunInput(raw)
					if err != nil {
						return nil, err
					}
					return a.pickProvider(ctx, mcpRunOpts(in))
				},
				Dispatch: func(ctx context.Context, raw json.RawMessage) (any, error) {
					in, err := decodeMCPRunInput(raw)
					if err != nil {
						return nil, err
					}
					if in.Brief == "" {
						return nil, errors.New("brief is required")
					}
					return a.dispatch(ctx, mcpRunOpts(in), ledger.SourceMCP)
				},
				Pause: mcpProviderStateHandler(a, "pause"),
				Drain: mcpProviderStateHandler(a, "drain"),
				Pin: func(ctx context.Context, raw json.RawMessage) (any, error) {
					var in mcpPinInput
					if err := decodeMCPInput(raw, &in); err != nil {
						return nil, err
					}
					return a.pin(ctx, in.JobID, in.Provider, ledger.SourceMCP)
				},
			})
			return server.Run(cmd.Context(), &sdk.StdioTransport{})
		},
	}
}

func decodeMCPRunInput(raw json.RawMessage) (mcpRunInput, error) {
	in := mcpRunInput{Role: "default", Worktree: "."}
	if err := decodeMCPInput(raw, &in); err != nil {
		return mcpRunInput{}, err
	}
	return in, nil
}

func mcpRunOpts(in mcpRunInput) runOpts {
	return runOpts{role: in.Role, worktree: in.Worktree, brief: in.Brief, name: in.Name, report: in.Report,
		adapter: in.Adapter, provider: in.Provider, neverSwitch: in.NeverSwitch, backlog: in.Backlog, noSensors: in.NoSensors}
}

func mcpProviderStateHandler(a *app, verb string) mcpserver.Handler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in mcpProviderStateInput
		if err := decodeMCPInput(raw, &in); err != nil {
			return nil, err
		}
		return a.providerState(ctx, verb, in.Provider, in.Reason, ledger.SourceMCP)
	}
}

func decodeMCPInput(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid arguments: multiple JSON values")
	}
	return nil
}
