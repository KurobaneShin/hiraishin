package cli

import (
	"cmp"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
)

type statusWindow struct {
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	UsedPct         *float64  `json:"used_pct"`
	ResetsAt        time.Time `json:"resets_at,omitzero"`
	ResetsInSeconds *int64    `json:"resets_in_seconds"`
	Confidence      string    `json:"confidence"`
}

type statusProvider struct {
	ID            string         `json:"id"`
	Harness       string         `json:"harness"`
	State         string         `json:"state"`
	ActiveJobs    int            `json:"active_jobs"`
	MaxConcurrent int            `json:"max_concurrent"`
	CooldownUntil time.Time      `json:"cooldown_until,omitzero"`
	Windows       []statusWindow `json:"windows"`
	LastError     string         `json:"last_error,omitempty"`
}

type statusDoc struct {
	SchemaVersion int              `json:"schema_version"`
	GeneratedAt   time.Time        `json:"generated_at"`
	Profile       string           `json:"profile"`
	Providers     []statusProvider `json:"providers"`
	Jobs          map[string]int   `json:"jobs"`
}

func statusCmd(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show providers, quota windows, slots and time to reset",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := a.open(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			if err := e.Maintain(cmd.Context()); err != nil {
				return err
			}
			r := e.Ledger.Reader()
			snap, err := e.Snapshot(cmd.Context(), r)
			if err != nil {
				return err
			}
			rows, err := r.Providers(cmd.Context())
			if err != nil {
				return err
			}
			profile, err := r.Profile(cmd.Context())
			if err != nil {
				return err
			}
			jobs, err := r.Jobs(cmd.Context(), ledger.JobQueuedState, ledger.JobDispatchingState, ledger.JobRunningState, ledger.JobFailingOverState, ledger.JobWaitingQuotaState)
			if err != nil {
				return err
			}
			doc := buildStatus(snap, rows, e.Cfg.General.WarnUsedPct, cmp.Or(profile, e.Cfg.General.ActiveProfile), jobs, a.env.Now())
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), doc)
			}
			return renderStatus(cmd, doc)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON (schema_version 1)")
	return cmd
}

func buildStatus(snap []scheduler.Provider, rows []ledger.ProviderRow, warn float64, profile string, jobs []ledger.JobRow, now time.Time) statusDoc {
	lastErr := map[string]string{}
	harness := map[string]string{}
	for _, r := range rows {
		lastErr[r.ID], harness[r.ID] = r.LastError, r.Harness
	}
	doc := statusDoc{SchemaVersion: 1, GeneratedAt: now.UTC(), Profile: profile, Providers: []statusProvider{}, Jobs: map[string]int{}}
	for _, j := range jobs {
		doc.Jobs[j.State]++
	}
	for _, p := range snap {
		sp := statusProvider{ID: p.ID, Harness: cmp.Or(harness[p.ID], p.Config.Harness), State: scheduler.EffectiveState(p, warn, now),
			ActiveJobs: p.ActiveJobs, MaxConcurrent: p.Config.MaxConcurrent, Windows: []statusWindow{}}
		if p.CooldownUntil.After(now) {
			sp.CooldownUntil = p.CooldownUntil
		}
		if sp.State != ledger.StateAvailable {
			sp.LastError = lastErr[p.ID]
		}
		if !p.Config.IsEnabled() {
			sp.State = "DISABLED"
		}
		for _, w := range p.Windows {
			sw := statusWindow{Name: w.Name, Kind: w.Kind, UsedPct: w.UsedPct, ResetsAt: w.ResetsAt, Confidence: w.Confidence}
			if !w.ResetsAt.IsZero() {
				secs := int64(w.ResetsAt.Sub(now).Seconds())
				sw.ResetsInSeconds = &secs
			}
			sp.Windows = append(sp.Windows, sw)
		}
		doc.Providers = append(doc.Providers, sp)
	}
	return doc
}

func renderStatus(cmd *cobra.Command, doc statusDoc) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Profile: %s\n\nPROVIDER\tSTATE\tSLOTS\tWINDOW\tUSED\tRESETS IN\tCONF\n", doc.Profile)
	for _, p := range doc.Providers {
		lead := fmt.Sprintf("%s\t%s\t%d/%d", p.ID, p.State, p.ActiveJobs, p.MaxConcurrent)
		if len(p.Windows) == 0 {
			_, _ = fmt.Fprintf(w, "%s\t-\t?\t-\t-\n", lead)
		}
		for i, win := range p.Windows {
			if i > 0 {
				lead = "\t\t"
			}
			resets := "-"
			if win.ResetsInSeconds != nil {
				resets = duration(time.Duration(*win.ResetsInSeconds) * time.Second)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", lead, win.Name, percent(win.UsedPct), resets, cmp.Or(win.Confidence, "-"))
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if len(doc.Jobs) > 0 {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), "\nJobs:")
		for _, s := range []string{ledger.JobRunningState, ledger.JobDispatchingState, ledger.JobFailingOverState, ledger.JobWaitingQuotaState, ledger.JobQueuedState} {
			if n := doc.Jobs[s]; n > 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), " %d %s", n, s)
			}
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
	}
	return nil
}
