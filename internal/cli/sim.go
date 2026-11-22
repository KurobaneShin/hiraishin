package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/sim"
)

type simOpts struct {
	ledger   string
	policies string
	format   string
}

func simCmd(a *app) *cobra.Command {
	var o simOpts
	cmd := &cobra.Command{
		Use:   "sim --ledger <day.db> --policies <a.toml,b.toml[,...]>",
		Short: "Replay a ledger offline and compare scheduling policies",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.ledger == "" {
				return usageError{msg: "--ledger is required"}
			}
			paths := strings.Split(o.policies, ",")
			if o.policies == "" || len(paths) < 2 {
				return usageError{msg: "--policies requires at least two policies"}
			}
			if !slices.Contains([]string{"markdown", "csv", "json"}, o.format) {
				return usageError{msg: "--format must be markdown, csv or json"}
			}
			policies := make([]sim.Policy, 0, len(paths))
			names := map[string]bool{}
			for _, path := range paths {
				path = strings.TrimSpace(path)
				if path == "" {
					return usageError{msg: "--policies contains an empty path"}
				}
				cfg, err := config.Load(path)
				if err != nil {
					return err
				}
				name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
				if names[name] {
					return usageError{msg: fmt.Sprintf("duplicate policy name %q", name)}
				}
				names[name] = true
				policies = append(policies, sim.Policy{Name: name, Config: cfg})
			}
			rows, err := sim.Replay(cmd.Context(), o.ledger, policies)
			if err != nil {
				return err
			}
			return sim.Render(cmd.OutOrStdout(), o.format, rows)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.ledger, "ledger", "", "exported or live SQLite event ledger (required)")
	f.StringVar(&o.policies, "policies", "", "comma-separated preferences.toml paths (at least two, required)")
	f.StringVar(&o.format, "format", "markdown", "output format: markdown, csv or json")
	return cmd
}
