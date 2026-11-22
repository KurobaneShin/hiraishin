package cli

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/engine"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

func (a *app) appendEvents(ctx context.Context, build func(ctx context.Context, e *engine.Engine, tx *ledger.Tx) ([]ledger.Event, error)) (*engine.Engine, error) {
	e, err := a.open(ctx)
	if err != nil {
		return nil, err
	}
	err = e.Ledger.Update(ctx, func(ctx context.Context, tx *ledger.Tx) ([]ledger.Event, error) { return build(ctx, e, tx) })
	if err != nil {
		_ = e.Close()
		return nil, err
	}
	return e, nil
}

func requireProvider(e *engine.Engine, id string) error {
	if _, ok := e.Cfg.Providers[id]; !ok {
		return usageError{msg: fmt.Sprintf("provider %q is not in preferences.toml", id)}
	}
	return nil
}

func profileCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Show or set the active objective profile",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := a.open(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			p, err := e.Ledger.Reader().Profile(cmd.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), cmp.Or(p, e.Cfg.General.ActiveProfile))
			return err
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "set <quality-first|burn-expiring-first|save-scarce|balanced|custom>",
		Short: "Switch the active profile at runtime (stored in the ledger)",
		Args:  args(1, "<profile>"),
		RunE: func(cmd *cobra.Command, argv []string) error {
			e, err := a.open(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			if err := setProfile(cmd.Context(), e, argv[0]); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Active profile: %s\n", argv[0])
			return err
		},
	})
	return cmd
}

func preferCmd(a *app) *cobra.Command {
	var role string
	var ttl time.Duration
	var clear bool
	cmd := &cobra.Command{
		Use:   "prefer <provider>",
		Short: "Boost a provider (+0.15, still behind every hard gate)",
		Args:  args(1, "<provider>"),
		RunE: func(cmd *cobra.Command, argv []string) error {
			e, err := a.appendEvents(cmd.Context(), func(_ context.Context, e *engine.Engine, _ *ledger.Tx) ([]ledger.Event, error) {
				if err := requireProvider(e, argv[0]); err != nil {
					return nil, err
				}
				if clear {
					return []ledger.Event{{ProviderID: argv[0], Source: ledger.SourceCLI, Payload: ledger.PreferCleared{Role: role, By: "cli"}}}, nil
				}
				var exp time.Time
				if ttl > 0 {
					exp = a.env.Now().Add(ttl)
				}
				return []ledger.Event{{ProviderID: argv[0], Source: ledger.SourceCLI, Payload: ledger.PreferSet{Role: role, ExpiresAt: exp, By: "cli"}}}, nil
			})
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			verb := "Preferring"
			if clear {
				verb = "Cleared preference for"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s (role %s)\n", verb, argv[0], cmp.Or(role, "any"))
			return err
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "limit the boost to one role")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "expire the boost after this duration (e.g. 2h)")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the boost")
	return cmd
}

func pinCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "pin <job_id> <provider>",
		Short: "Pin a job to one provider and block automatic switching",
		Args:  args(2, "<job_id> <provider>"),
		RunE: func(cmd *cobra.Command, argv []string) error {
			e, err := a.appendEvents(cmd.Context(), func(ctx context.Context, e *engine.Engine, tx *ledger.Tx) ([]ledger.Event, error) {
				if err := requireProvider(e, argv[1]); err != nil {
					return nil, err
				}
				if _, err := tx.Job(ctx, argv[0]); err != nil {
					return nil, err
				}
				return []ledger.Event{{JobID: argv[0], Source: ledger.SourceCLI, Payload: ledger.JobPinned{Provider: argv[1]}}}, nil
			})
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Pinned %s to %s (never_switch = true)\n", argv[0], argv[1])
			return err
		},
	}
}

func unpinCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "unpin <job_id>",
		Short: "Remove a job pin and restore its role's switch policy",
		Args:  args(1, "<job_id>"),
		RunE: func(cmd *cobra.Command, argv []string) error {
			e, err := a.appendEvents(cmd.Context(), func(ctx context.Context, e *engine.Engine, tx *ledger.Tx) ([]ledger.Event, error) {
				job, err := tx.Job(ctx, argv[0])
				if err != nil {
					return nil, err
				}
				role, _, err := e.Cfg.Role(job.Role)
				if err != nil {
					return nil, err
				}
				return []ledger.Event{{JobID: job.ID, Source: ledger.SourceCLI, Payload: ledger.JobUnpinned{NeverSwitch: role.NeverSwitchMidTask}}}, nil
			})
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Unpinned %s\n", argv[0])
			return err
		},
	}
}

func providerStateCmd(a *app, verb string) *cobra.Command {
	var reason string
	short := map[string]string{
		"pause":  "Stop all new dispatches and failovers to a provider",
		"drain":  "Let running jobs finish, assign no new work, then pause",
		"resume": "Return a paused or draining provider to service",
	}[verb]
	cmd := &cobra.Command{
		Use:   verb + " <provider>",
		Short: short,
		Args:  args(1, "<provider>"),
		RunE: func(cmd *cobra.Command, argv []string) error {
			e, err := a.open(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			state, err := setProviderState(cmd.Context(), e, verb, argv[0], reason)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", argv[0], state)
			return err
		},
	}
	if verb != "resume" {
		cmd.Flags().StringVar(&reason, "reason", "", "note stored in the ledger")
	}
	return cmd
}

func setProfile(ctx context.Context, e *engine.Engine, profile string) error {
	if _, err := e.Cfg.ProfileWeights(profile); err != nil {
		return usageError{msg: err.Error()}
	}
	return e.Ledger.Append(ctx, ledger.Event{Source: ledger.SourceCLI, Payload: ledger.ProfileSet{Profile: profile, By: "cli"}})
}

func setProviderState(ctx context.Context, e *engine.Engine, verb, id, reason string) (string, error) {
	if err := requireProvider(e, id); err != nil {
		return "", err
	}
	var p ledger.Payload
	switch verb {
	case "pause":
		p = ledger.Paused{Reason: reason, By: "cli"}
	case "drain":
		p = ledger.Draining{Reason: reason, By: "cli"}
	default:
		p = ledger.Resumed{By: "cli"}
	}
	if err := e.Ledger.Append(ctx, ledger.Event{ProviderID: id, Source: ledger.SourceCLI, Payload: p}); err != nil {
		return "", err
	}
	row, err := e.Ledger.Reader().Provider(ctx, id)
	return row.State, err
}
