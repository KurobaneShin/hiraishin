package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/engine"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
)

func outcomeCmd(a *app) *cobra.Command {
	var o scheduler.Outcome
	var failed bool
	record := &cobra.Command{
		Use:   "record <provider> <role>",
		Short: "Record how one job went so bounded learning can adjust the provider's score for the role",
		Args:  args(2, "<provider> <role>"),
		RunE: func(cmd *cobra.Command, argv []string) error {
			if o.ReviewRounds < 0 || o.BuildFailures < 0 || o.DurationMin < 0 {
				return usageError{msg: "--review-rounds, --build-failures and --duration-min must be >= 0"}
			}
			o.Success = !failed
			var rec ledger.OutcomeRecorded
			e, err := a.appendEvents(cmd.Context(), func(ctx context.Context, e *engine.Engine, tx *ledger.Tx) ([]ledger.Event, error) {
				if err := requireProvider(e, argv[0]); err != nil {
					return nil, err
				}
				if _, _, err := e.Cfg.Role(argv[1]); err != nil {
					return nil, usageError{msg: err.Error()}
				}
				ev, err := e.OutcomeEvent(ctx, tx, argv[0], argv[1], "", ledger.SourceCLI, o)
				if err != nil {
					return nil, err
				}
				rec = ev.Payload.(ledger.OutcomeRecorded)
				return []ledger.Event{ev}, nil
			})
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			msg := fmt.Sprintf("Recorded outcome for %s on %s: quality %.3f", argv[0], argv[1], rec.Quality)
			if rec.Frozen {
				msg += " (learning frozen: stats and scores unchanged)"
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), msg)
			return err
		},
	}
	f := record.Flags()
	f.BoolVar(&failed, "failed", false, "the job did not finish without a human taking over")
	f.IntVar(&o.ReviewRounds, "review-rounds", 0, "review or fix rounds before merge")
	f.IntVar(&o.BuildFailures, "build-failures", 0, "build or gate failures")
	f.Float64Var(&o.DurationMin, "duration-min", 0, "wall minutes to done (0 = unknown, scored neutral)")
	cmd := &cobra.Command{Use: "outcome", Short: "Feed job outcomes to bounded learning"}
	cmd.AddCommand(record)
	return cmd
}

func learnCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "learn", Short: "Inspect, freeze, unfreeze or reset bounded learning"}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show per-(provider, role) outcome stats and the delta each adds to the score",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := a.open(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			tx := e.Ledger.Reader()
			frozen, err := tx.LearningFrozen(cmd.Context())
			if err != nil {
				return err
			}
			rows, err := tx.LearningStats(cmd.Context(), "")
			if err != nil {
				return err
			}
			return printLearnStatus(cmd.OutOrStdout(), e, frozen, rows)
		},
	})
	for _, s := range []struct {
		use, short, done string
		payload          ledger.Payload
	}{
		{"freeze", "Stop outcomes from changing learned deltas; scores stop moving at once", "Learning frozen: outcomes are still logged, learned deltas stay fixed.", ledger.LearnFrozen{By: "cli"}},
		{"unfreeze", "Let outcomes change learned deltas again", "Learning unfrozen.", ledger.LearnUnfrozen{By: "cli"}},
		{"reset", "Forget every learned stat; deltas return to 0 until min_samples new outcomes", "Learning reset: every learned delta is 0.", ledger.LearnReset{By: "cli"}},
	} {
		cmd.AddCommand(&cobra.Command{
			Use:   s.use,
			Short: s.short,
			Args:  args(0, ""),
			RunE: func(cmd *cobra.Command, _ []string) error {
				e, err := a.appendEvents(cmd.Context(), func(context.Context, *engine.Engine, *ledger.Tx) ([]ledger.Event, error) {
					return []ledger.Event{{Source: ledger.SourceCLI, Payload: s.payload}}, nil
				})
				if err != nil {
					return err
				}
				defer func() { _ = e.Close() }()
				_, err = fmt.Fprintln(cmd.OutOrStdout(), s.done)
				return err
			},
		})
	}
	return cmd
}

func printLearnStatus(out io.Writer, e *engine.Engine, frozen bool, rows []ledger.LearningRow) error {
	c := e.Cfg.Learning
	state := "enabled"
	if !c.Enabled {
		state = "disabled (learned deltas ignored; set [learning] enabled = true)"
	}
	switch {
	case c.Frozen:
		state += ", frozen by preferences.toml"
	case frozen:
		state += ", frozen (hiraishin learn unfreeze)"
	}
	if _, err := fmt.Fprintf(out, "Learning: %s · min_samples %d · max_delta ±%.3f · ewma_alpha %.2f\n", state, c.MinSamples, c.MaxDelta, c.EWMAAlpha); err != nil {
		return err
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "No outcomes recorded yet (hiraishin outcome record, or a job completing under hiraishin watch).")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ROLE\tPROVIDER\tSAMPLES\tOK\tQUALITY\tDELTA")
	for start := 0; start < len(rows); {
		end := start
		for end < len(rows) && rows[end].Role == rows[start].Role {
			end++
		}
		deltas := scheduler.LearnDeltas(c, rows[start:end])
		for _, r := range rows[start:end] {
			delta := fmt.Sprintf("%+.3f", deltas[r.ProviderID])
			if r.Samples < c.MinSamples {
				delta += fmt.Sprintf(" (needs %d samples)", c.MinSamples)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%.3f\t%s\n", r.Role, r.ProviderID, r.Samples, r.CompletedOK, r.EWMAQuality, delta)
		}
		start = end
	}
	return w.Flush()
}
