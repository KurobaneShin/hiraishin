package cli

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

type backlogItem struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	State    string    `json:"state"`
	Provider string    `json:"provider,omitempty"`
	Worktree string    `json:"worktree"`
	Since    time.Time `json:"since"`
}

type backlogDoc struct {
	SchemaVersion int           `json:"schema_version"`
	Jobs          []backlogItem `json:"jobs"`
}

var activeJobStates = []string{ledger.JobRunningState, ledger.JobDispatchingState, ledger.JobFailingOverState, ledger.JobWaitingQuotaState, ledger.JobQueuedState}

func backlogWaiting(j ledger.JobRow) bool {
	return j.Backlog && (j.State == ledger.JobWaitingQuotaState || j.State == ledger.JobQueuedState)
}

func jobCountParts(all, backlog map[string]int) []string {
	var parts []string
	for _, s := range activeJobStates {
		n := all[s]
		if s == ledger.JobWaitingQuotaState || s == ledger.JobQueuedState {
			n -= backlog[s]
		}
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, jobStateLabel(s)))
		}
	}
	if n := backlog[ledger.JobWaitingQuotaState] + backlog[ledger.JobQueuedState]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d backlog waiting for quota that would go unused", n))
	}
	return parts
}

func backlogCmd(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "List backlog jobs (run --backlog): chores that run only on quota forecast to go unused",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			doc, err := a.backlog(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, doc)
			}
			if len(doc.Jobs) == 0 {
				_, err := fmt.Fprintln(out, "No backlog jobs. Add one: hiraishin run --backlog --brief <file> --worktree <dir>")
				return err
			}
			now := a.env.Now()
			var b strings.Builder
			for _, j := range doc.Jobs {
				state := jobStateLabel(j.State)
				if j.State == ledger.JobWaitingQuotaState || j.State == ledger.JobQueuedState {
					state = "waiting"
				}
				fmt.Fprintf(&b, "%s  %-24s  %-8s  %-13s  %-20s  %s\n", j.ID, truncate(clean(j.Name), 24), truncate(clean(j.Role), 8), state, truncate(clean(cmp.Or(j.Provider, "-")), 20), duration(now.Sub(j.Since)))
			}
			b.WriteString("\nwaiting = no provider is forecast to leave enough quota unused · why: hiraishin why <job_id> · remove: hiraishin backlog rm <job_id>\n")
			_, err = fmt.Fprint(out, b.String())
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.AddCommand(&cobra.Command{
		Use:   "rm <job_id>",
		Short: "Remove a waiting backlog job (marks it CANCELLED; a running one needs hiraishin cancel)",
		Args:  args(1, "<job_id>"),
		RunE: func(cmd *cobra.Command, argv []string) error {
			ctx := cmd.Context()
			e, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			job, err := e.Ledger.Reader().Job(ctx, argv[0])
			if err != nil {
				return err
			}
			switch {
			case !job.Backlog:
				return usageError{msg: fmt.Sprintf("job %s is not a backlog job; stop it with hiraishin cancel %s", job.ID, job.ID)}
			case !backlogWaiting(job):
				return usageError{msg: fmt.Sprintf("backlog job %s is %s, not waiting; stop it with hiraishin cancel %s", job.ID, job.State, job.ID)}
			}
			if err := e.Ledger.Append(ctx, ledger.Event{JobID: job.ID, Source: ledger.SourceCLI, Payload: ledger.JobCancelled{By: "cli"}}); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s (%s) from the backlog\n", job.ID, job.Name)
			return err
		},
	})
	return cmd
}

func (a *app) backlog(ctx context.Context) (backlogDoc, error) {
	e, err := a.open(ctx)
	if err != nil {
		return backlogDoc{}, err
	}
	defer func() { _ = e.Close() }()
	jobs, err := e.Ledger.Reader().Jobs(ctx, activeJobStates...)
	if err != nil {
		return backlogDoc{}, err
	}
	doc := backlogDoc{SchemaVersion: 1, Jobs: []backlogItem{}}
	for _, j := range jobs {
		if j.Backlog {
			doc.Jobs = append(doc.Jobs, backlogItem{ID: j.ID, Name: j.Name, Role: j.Role, State: j.State, Provider: j.CurrentProvider, Worktree: j.WorktreePath, Since: j.CreatedAt})
		}
	}
	return doc, nil
}
