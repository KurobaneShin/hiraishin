package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/dispatch"
	"github.com/KurobaneShin/hiraishin/internal/engine"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
)

type runOpts struct {
	role, worktree, brief, name, report, adapter, provider string
	neverSwitch                                            bool
}

func runCmd(a *app) *cobra.Command {
	var o runOpts
	cmd := &cobra.Command{
		Use:   "run --brief <file>",
		Short: "Queue a job, pick a provider and launch it in the worktree",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.brief == "" {
				return usageError{msg: "--brief is required"}
			}
			return a.run(cmd, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.brief, "brief", "", "brief file the agent must follow (required)")
	f.StringVar(&o.worktree, "worktree", ".", "git worktree the agent works in")
	f.StringVar(&o.role, "role", "default", "role from preferences.toml")
	f.StringVar(&o.name, "name", "", "job name (default: the brief's directory name)")
	f.StringVar(&o.report, "report", "", "progress report path (default: report.md next to the brief)")
	f.StringVar(&o.adapter, "adapter", "", "terminal adapter: process, tmux or orca (default: general.default_terminal_adapter)")
	f.StringVar(&o.provider, "provider", "", "pin the job to one provider (implies --never-switch)")
	f.BoolVar(&o.neverSwitch, "never-switch", false, "wait for quota instead of switching providers mid-job")
	return cmd
}

func (a *app) run(cmd *cobra.Command, o runOpts) error {
	ctx := cmd.Context()
	wt, err := filepath.Abs(o.worktree)
	if err != nil {
		return err
	}
	brief, err := filepath.Abs(o.brief)
	if err != nil {
		return err
	}
	if _, err := os.Stat(brief); err != nil {
		return usageError{msg: fmt.Sprintf("brief: %v", err)}
	}
	report, err := filepath.Abs(cmp.Or(o.report, filepath.Join(filepath.Dir(brief), "report.md")))
	if err != nil {
		return err
	}
	cp, err := dispatch.ReadCheckpoint(ctx, wt)
	if err != nil {
		return usageError{msg: err.Error()}
	}
	e, err := a.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = e.Close() }()
	if o.provider != "" {
		if err := requireProvider(e, o.provider); err != nil {
			return err
		}
	}
	adapter := cmp.Or(o.adapter, e.Cfg.General.DefaultTerminalAdapter)
	if _, err := dispatch.NewAdapter(adapter); err != nil {
		return usageError{msg: err.Error()}
	}
	role, _, err := e.Cfg.Role(o.role)
	if err != nil {
		return usageError{msg: err.Error()}
	}
	job := ledger.JobRow{
		ID: ledger.NewID("job"), Name: cmp.Or(o.name, filepath.Base(filepath.Dir(brief))), Role: o.role,
		WorktreePath: wt, Branch: cp.Branch, BriefPath: brief, ReportPath: report, TerminalAdapter: adapter,
		PinnedProvider: o.provider, NeverSwitch: o.neverSwitch || o.provider != "" || role.NeverSwitchMidTask,
	}
	d, err := a.queue(ctx, e, job)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if d.ChosenProvider == "" {
		_, err = fmt.Fprintf(out, "Job %s is WAITING_QUOTA. %s\nhiraishin watch dispatches it when a provider resets.\n", job.ID, d.Summary)
		return err
	}
	prompt, err := dispatch.InitialPrompt(dispatch.PromptData{JobID: job.ID, JobName: job.Name, Worktree: wt, BriefPath: brief, ReportPath: report})
	if err != nil {
		return err
	}
	ev := e.Launch(ctx, job, d.DecisionID, d.ChosenProvider, prompt)
	if err := e.Ledger.Append(ctx, ev); err != nil {
		return err
	}
	if failed, ok := ev.Payload.(ledger.JobSpawnFailed); ok {
		return fmt.Errorf("launch %s on %s: %s", job.ID, d.ChosenProvider, failed.Error)
	}
	disp := ev.Payload.(ledger.JobDispatched)
	_, err = fmt.Fprintf(out, "Dispatched %s (%s) on %s via %s [%s]\n%s\nExplain: hiraishin why %s\n", job.ID, job.Name, d.ChosenProvider, adapter, disp.TerminalHandle, d.Summary, job.ID)
	return err
}

func (a *app) queue(ctx context.Context, e *engine.Engine, job ledger.JobRow) (scheduler.Decision, error) {
	var d scheduler.Decision
	err := e.Ledger.Update(ctx, func(ctx context.Context, tx *ledger.Tx) ([]ledger.Event, error) {
		queued := ledger.Event{JobID: job.ID, Source: ledger.SourceCLI, Payload: ledger.JobQueued{
			Name: job.Name, Role: job.Role, WorktreePath: job.WorktreePath, Branch: job.Branch, BriefPath: job.BriefPath,
			ReportPath: job.ReportPath, TerminalAdapter: job.TerminalAdapter, PinnedProvider: job.PinnedProvider, NeverSwitch: job.NeverSwitch,
		}}
		var dec ledger.Event
		var err error
		d, dec, err = e.Decide(ctx, tx, job, scheduler.TriggerInitial, nil)
		if err != nil {
			return nil, err
		}
		evs := []ledger.Event{queued, dec}
		if d.ChosenProvider == "" {
			evs = append(evs, ledger.Event{JobID: job.ID, Source: ledger.SourceScheduler, Payload: ledger.WaitingQuota{EarliestResetAt: d.EarliestResetAt, BlockedProviders: d.BlockedProviders}})
		}
		return evs, nil
	})
	return d, err
}

func cancelCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <job_id>",
		Short: "Stop a job's agent and mark the job CANCELLED (the worktree is left as is)",
		Args:  args(1, "<job_id>"),
		RunE: func(cmd *cobra.Command, argv []string) error {
			ctx := cmd.Context()
			e, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			r := e.Ledger.Reader()
			job, err := r.Job(ctx, argv[0])
			if err != nil {
				return err
			}
			switch job.State {
			case ledger.JobCompletedState, ledger.JobFailedState, ledger.JobCancelledState:
				return fmt.Errorf("job %s is already %s", job.ID, job.State)
			}
			attempts, err := r.Attempts(ctx, job.ID)
			if err != nil {
				return err
			}
			var attemptID string
			if n := len(attempts); n > 0 && attempts[n-1].EndedAt.IsZero() {
				last := attempts[n-1]
				attemptID = last.ID
				if err := dispatch.Stop(ctx, last.Dir, dispatch.DefaultGrace); err != nil && !errors.Is(err, dispatch.ErrNoProcess) {
					return err
				}
			}
			if err := e.Ledger.Append(ctx, ledger.Event{JobID: job.ID, Source: ledger.SourceCLI, Payload: ledger.JobCancelled{AttemptID: attemptID, By: "cli"}}); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Cancelled %s; worktree %s left as is\n", job.ID, job.WorktreePath)
			return err
		},
	}
}
