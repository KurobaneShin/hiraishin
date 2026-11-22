package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
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
		Short: "Explain where a job was routed: who was skipped and how the rest scored",
		Args:  args(1, "<job_id|decision_id>"),
		RunE: func(cmd *cobra.Command, argv []string) error {
			doc, err := a.why(cmd.Context(), argv[0], all)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), doc)
			}
			pal := palette{on: colorEnabled(cmd.OutOrStdout(), a.env.Getenv)}
			for i, d := range doc.Decisions {
				if i > 0 {
					_, _ = fmt.Fprintln(cmd.OutOrStdout())
				}
				renderWhy(cmd.OutOrStdout(), doc.Job, d, pal)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON (docs/schemas/why.schema.json)")
	cmd.Flags().BoolVar(&all, "all", false, "show every decision of the job, not only the latest")
	return cmd
}

func (a *app) why(ctx context.Context, id string, all bool) (whyDoc, error) {
	e, err := a.open(ctx)
	if err != nil {
		return whyDoc{}, err
	}
	defer func() { _ = e.Close() }()
	return loadWhy(ctx, e.Ledger.Reader(), id, all)
}

func loadWhy(ctx context.Context, r *ledger.Tx, id string, all bool) (whyDoc, error) {
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

var triggerWords = map[string]string{
	scheduler.TriggerInitial:         "first dispatch",
	scheduler.TriggerFailover:        "failover",
	scheduler.TriggerRetryAfterReset: "retry after reset",
	scheduler.TriggerDryRun:          "dry run",
}

func renderWhy(w io.Writer, job whyJob, d scheduler.Decision, pal palette) {
	var b strings.Builder
	sep := pal.paint(dim, " · ")
	fmt.Fprintf(&b, "%s  %s%s%s%srole %s (min quality %d)\n", pal.paint(bold, "why"), clean(job.ID), sep, clean(job.Name), sep, clean(d.Role), d.MinQualityTier)
	fmt.Fprintf(&b, "decision %s%s%s\n", clean(d.DecisionID), sep, cmp.Or(triggerWords[d.Trigger], clean(d.Trigger)))
	fmt.Fprintf(&b, "profile %s %s\n", pal.paint(cyan, clean(d.Profile)),
		pal.paint(dim, fmt.Sprintf("quality %.2f · expiring %.2f · scarcity %.2f · speed %.2f", d.Weights.Quality, d.Weights.Expiry, d.Weights.Scarcity, d.Weights.Latency)))
	width := 0
	for _, c := range d.Candidates {
		width = max(width, len(c.Provider))
	}
	b.WriteString("\n")
	for _, c := range d.Candidates {
		name := fmt.Sprintf("%-*s", width, clean(c.Provider))
		switch c.Status {
		case scheduler.StatusRejected:
			fmt.Fprintf(&b, " %s %s  %s  %s\n", pal.paint(red, "✗"), pal.paint(dim, name), pal.paint(dim, "skipped "), pal.paint(dim, clean(strings.Join(c.Rejections, "; "))))
			continue
		case scheduler.StatusSelected:
			fmt.Fprintf(&b, " %s %s  %s  score %.3f\n", pal.paint(green, "✓"), pal.paint(bold, name), pal.paint(green, "picked  "), c.Score)
		default:
			fmt.Fprintf(&b, " %s %s  %s  score %.3f\n", pal.paint(yellow, "•"), name, pal.paint(yellow, "eligible"), c.Score)
		}
		f, x := c.Features, c.Weighted
		for _, row := range []struct {
			name          string
			value, scored float64
			weight        float64
			note          string
		}{
			{"quality", f.Quality, x.Quality, d.Weights.Quality, ""},
			{"expiring", f.Expiry, x.Expiry, d.Weights.Expiry, expiryNote(c)},
			{"scarcity", f.Scarcity, x.Scarcity, d.Weights.Scarcity, ""},
			{"speed", f.Latency, x.Latency, d.Weights.Latency, ""},
		} {
			n := min(factorWidth, int(math.Round(row.value*factorWidth)))
			fmt.Fprintf(&b, "     %-9s %s%s %.2f × %.2f = %.3f", row.name, pal.paint(cyan, strings.Repeat("█", n)), pal.paint(dim, strings.Repeat("░", factorWidth-n)), row.value, row.weight, row.scored)
			if row.note != "" {
				fmt.Fprintf(&b, "  %s", pal.paint(dim, row.note))
			}
			b.WriteString("\n")
		}
		var extras []string
		for _, e := range []struct {
			name string
			v    float64
		}{{"order", c.OrderBonus}, {"prefer", c.PreferBonus}, {"near limit", -c.DegradedPenalty}, {"learned", c.LearnDelta}} {
			if e.v != 0 {
				extras = append(extras, fmt.Sprintf("%s %+.3f", e.name, e.v))
			}
		}
		if len(extras) > 0 {
			fmt.Fprintf(&b, "     %-9s %s\n", "bonus", strings.Join(extras, ", "))
		}
		fmt.Fprintf(&b, "     %-9s %s\n", "usage", pal.paint(dim, windowNotes(c, d)+fmt.Sprintf(" · %d/%d jobs", c.ActiveJobs, c.MaxConcurrent)))
	}
	fmt.Fprintf(&b, "\n %s %s\n", pal.paint(bold, "→"), clean(d.Summary))
	fmt.Fprintf(&b, " %s\n", pal.paint(dim, "how routing works: hiraishin help routing"))
	_, _ = io.WriteString(w, b.String())
}

const factorWidth = 16

func expiryNote(c scheduler.Candidate) string {
	switch {
	case c.ExpiryNeutral:
		return "usage unknown, neutral"
	case c.ExpiryLimitedBy != "" && c.Features.Expiry == 0:
		return clean(c.ExpiryLimitedBy) + " runs out first, nothing here expires unused"
	case c.ExpiryLimitedBy != "":
		return "capped by the room " + clean(c.ExpiryLimitedBy) + " leaves"
	}
	return ""
}

func windowNotes(c scheduler.Candidate, d scheduler.Decision) string {
	var parts []string
	for _, w := range c.Windows {
		s := fmt.Sprintf("%s %s", clean(w.Name), percent(w.UsedPct))
		if !w.ResetsAt.IsZero() {
			s += " resets " + duration(w.ResetsAt.Sub(d.CreatedAt))
		}
		if w.Confidence != "" && w.Confidence != ledger.ConfidenceExact {
			s += " (" + clean(w.Confidence) + ")"
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "no windows configured"
	}
	return strings.Join(parts, ", ")
}
