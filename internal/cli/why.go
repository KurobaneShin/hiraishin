package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/engine"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
)

type whyJob struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	State string `json:"state"`
}

type whyDoc struct {
	SchemaVersion int                  `json:"schema_version"`
	Job           whyJob               `json:"job"`
	Decisions     []scheduler.Decision `json:"decisions"`
}

func whyCmd(a *app) *cobra.Command {
	var asJSON, all bool
	cmd := &cobra.Command{
		Use:   "why <job_id|decision_id>",
		Short: "Explain a scheduling decision: stage 1 rejections and stage 2 scores",
		Args:  args(1, "<job_id|decision_id>"),
		RunE: func(cmd *cobra.Command, argv []string) error {
			e, err := a.open(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			doc, err := loadWhy(cmd, e.Ledger.Reader(), argv[0], all)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), doc)
			}
			for i, d := range doc.Decisions {
				if i > 0 {
					_, _ = fmt.Fprintln(cmd.OutOrStdout())
				}
				renderWhy(cmd.OutOrStdout(), doc.Job, d)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON (docs/schemas/why.schema.json)")
	cmd.Flags().BoolVar(&all, "all", false, "show every decision of the job, not only the latest")
	return cmd
}

func loadWhy(cmd *cobra.Command, r *ledger.Tx, id string, all bool) (whyDoc, error) {
	ctx := cmd.Context()
	var rows []ledger.DecisionRow
	jobID := id
	if strings.HasPrefix(id, "dec_") {
		row, err := r.Decision(ctx, id)
		if err != nil {
			return whyDoc{}, err
		}
		rows, jobID = []ledger.DecisionRow{row}, row.JobID
	}
	job, err := r.Job(ctx, jobID)
	if err != nil {
		return whyDoc{}, err
	}
	if rows == nil {
		if rows, err = r.Decisions(ctx, jobID); err != nil {
			return whyDoc{}, err
		}
		if len(rows) == 0 {
			return whyDoc{}, errors.New("job has no scheduling decision yet")
		}
		if !all {
			rows = rows[len(rows)-1:]
		}
	}
	doc := whyDoc{SchemaVersion: scheduler.SchemaVersion, Job: whyJob{ID: job.ID, Name: job.Name, Role: job.Role, State: job.State}}
	for _, row := range rows {
		d, err := engine.DecisionFromRow(row)
		if err != nil {
			return whyDoc{}, err
		}
		doc.Decisions = append(doc.Decisions, d)
	}
	return doc, nil
}

func renderWhy(w io.Writer, job whyJob, d scheduler.Decision) {
	p := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }
	p("Decision: %s (%s)\n", d.DecisionID, d.Trigger)
	p("Job:      %s (%s, role=%s, min_tier=%d)\n", job.ID, job.Name, d.Role, d.MinQualityTier)
	p("Profile:  %s (Q=%.2f, E=%.2f, S=%.2f, L=%.2f)\n", d.Profile, d.Weights.Quality, d.Weights.Expiry, d.Weights.Scarcity, d.Weights.Latency)
	if d.ChosenProvider == "" {
		p("Chosen:   none (WAITING_QUOTA)\n")
	}
	for _, c := range d.Candidates {
		if c.Status == scheduler.StatusSelected {
			p("Chosen:   %s (score %.3f)\n", c.Provider, c.Score)
		}
	}
	p("\nCandidates evaluated:\n")
	for i, c := range d.Candidates {
		if c.Status == scheduler.StatusRejected {
			p("  %d. %-18s REJECTED  %s\n", i+1, c.Provider, strings.Join(c.Rejections, "; "))
			continue
		}
		p("  %d. %-18s %-9s score=%.3f\n", i+1, c.Provider, strings.ToUpper(c.Status), c.Score)
		f, x := c.Features, c.Weighted
		p("     Q=%.2f(%.3f) E=%.2f(%.3f) S=%.2f(%.3f) L=%.2f(%.3f) order=%+.3f prefer=%+.3f degraded=%+.3f learn=%+.3f\n",
			f.Quality, x.Quality, f.Expiry, x.Expiry, f.Scarcity, x.Scarcity, f.Latency, x.Latency, c.OrderBonus, c.PreferBonus, -c.DegradedPenalty, c.LearnDelta)
		p("     %s; slots %d/%d\n", windowNotes(c, d), c.ActiveJobs, c.MaxConcurrent)
	}
	p("\nSummary: %s\n", d.Summary)
}

func windowNotes(c scheduler.Candidate, d scheduler.Decision) string {
	var parts []string
	for _, w := range c.Windows {
		s := fmt.Sprintf("window %s: %s used", w.Name, percent(w.UsedPct))
		if !w.ResetsAt.IsZero() {
			s += ", resets in " + duration(w.ResetsAt.Sub(d.CreatedAt))
		}
		if w.Confidence != "" {
			s += " (" + w.Confidence + ")"
		}
		parts = append(parts, s)
	}
	if c.ExpiryNeutral {
		parts = append(parts, "expiry neutral 0.50 (window usage unknown)")
	}
	if len(parts) == 0 {
		return "no windows configured"
	}
	return strings.Join(parts, "; ")
}
