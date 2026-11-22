package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/doctor"
)

func doctorCmd(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Pre-flight check of binaries, auth, sensors, worktrees and integrations",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			e, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			deps := doctor.DefaultDeps()
			deps.Getenv = a.env.Getenv
			deps.LookPath = a.env.LookPath
			deps.ReadFile = a.env.ReadFile
			deps.Stat = a.env.Stat
			report, err := doctor.Check(ctx, e.Cfg, e.Ledger.Reader(), deps)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, report)
			}
			var b strings.Builder
			doctor.RenderText(&b, report)
			if _, err := fmt.Fprint(out, b.String()); err != nil {
				return err
			}
			if !report.Healthy {
				return fmt.Errorf("doctor found problems")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON output")
	return cmd
}
