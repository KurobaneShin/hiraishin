package cli

import (
	"cmp"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
	"github.com/KurobaneShin/hiraishin/internal/watch"
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
	Pool          string         `json:"pool,omitempty"`
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
	var asJSON, noSensors bool
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
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			var w *watch.Watcher
			var due []string
			if !noSensors {
				w = watch.New(e, a.probe(), slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
				if due, err = w.Due(ctx); err != nil {
					return err
				}
			}
			load := func() (statusDoc, error) {
				r := e.Ledger.Reader()
				snap, err := e.Snapshot(ctx, r)
				if err != nil {
					return statusDoc{}, err
				}
				rows, err := r.Providers(ctx)
				if err != nil {
					return statusDoc{}, err
				}
				profile, err := r.Profile(ctx)
				if err != nil {
					return statusDoc{}, err
				}
				jobs, err := r.Jobs(ctx, ledger.JobQueuedState, ledger.JobDispatchingState, ledger.JobRunningState, ledger.JobFailingOverState, ledger.JobWaitingQuotaState)
				if err != nil {
					return statusDoc{}, err
				}
				return buildStatus(snap, rows, e.Cfg.General.WarnUsedPct, cmp.Or(profile, e.Cfg.General.ActiveProfile), jobs, a.env.Now()), nil
			}
			live := !asJSON && len(due) > 0 && isTerminal(out)
			if len(due) > 0 && !live {
				if err := w.Poll(ctx); err != nil {
					return err
				}
			}
			doc, err := load()
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(out, doc)
			}
			pal := palette{on: colorEnabled(out, a.env.Getenv)}
			warn, reserve := e.Cfg.General.WarnUsedPct, e.Cfg.General.ReserveUsedPct
			var first strings.Builder
			if err := renderStatus(&first, doc, warn, reserve, pal); err != nil {
				return err
			}
			if live {
				first.WriteString(pal.paint(dim, "\n refreshing usage: "+strings.Join(due, ", ")+"…") + "\n")
			}
			if _, err := io.WriteString(out, first.String()); err != nil || !live {
				return err
			}
			pollErr := w.Poll(ctx)
			if doc, err = load(); err != nil {
				return err
			}
			var second strings.Builder
			if err := renderStatus(&second, doc, warn, reserve, pal); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "\x1b[%dA\x1b[J%s", strings.Count(first.String(), "\n"), second.String()); err != nil {
				return err
			}
			return pollErr
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON (schema_version 1)")
	cmd.Flags().BoolVar(&noSensors, "no-sensors", false, "show the last recorded usage without polling usage endpoints or local logs")
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
		sp := statusProvider{ID: p.ID, Harness: cmp.Or(harness[p.ID], p.Config.Harness), Pool: p.Config.SharedQuotaPool, State: scheduler.EffectiveState(p, warn, now),
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

const barWidth = 24

func renderStatus(out io.Writer, doc statusDoc, warn, reserve float64, pal palette) error {
	var b strings.Builder
	var pools []string
	members := map[string][]statusProvider{}
	nameWidth := 0
	nearLimit, unavailable := 0, 0
	for _, p := range doc.Providers {
		pool := cmp.Or(p.Pool, p.ID)
		if _, seen := members[pool]; !seen {
			pools = append(pools, pool)
		}
		members[pool] = append(members[pool], p)
		nameWidth = max(nameWidth, len(p.ID))
		switch p.State {
		case ledger.StateAvailable:
		case ledger.StateDegraded:
			nearLimit++
		default:
			unavailable++
		}
	}
	fmt.Fprintf(&b, "%s  profile %s %s %d providers", pal.paint(bold, "hiraishin"), pal.paint(cyan, clean(doc.Profile)), pal.paint(dim, "·"), len(doc.Providers))
	if nearLimit > 0 {
		fmt.Fprintf(&b, " %s %s", pal.paint(dim, "·"), pal.paint(yellow, fmt.Sprintf("%d near limit", nearLimit)))
	}
	if unavailable > 0 {
		fmt.Fprintf(&b, " %s %s", pal.paint(dim, "·"), pal.paint(red, fmt.Sprintf("%d unavailable", unavailable)))
	}
	b.WriteString("\n")
	for _, pool := range pools {
		group := members[pool]
		fmt.Fprintf(&b, "\n %s\n", pal.paint(bold, clean(pool)))
		var windows []statusWindow
		for _, p := range group {
			if len(p.Windows) > 0 {
				windows = p.Windows
				break
			}
		}
		if len(windows) == 0 {
			windows = []statusWindow{{Name: "usage"}}
		}
		for _, w := range windows {
			fmt.Fprintf(&b, "   %-7s %s %s", clean(w.Name), bar(w.UsedPct, warn, reserve, pal), pal.paint(level(w.UsedPct, warn, reserve), fmt.Sprintf("%4s", percent(w.UsedPct))))
			if w.ResetsInSeconds != nil {
				fmt.Fprintf(&b, "  %s", pal.paint(dim, "resets "+duration(time.Duration(*w.ResetsInSeconds)*time.Second)))
			}
			if w.Confidence != "" && w.Confidence != ledger.ConfidenceExact {
				fmt.Fprintf(&b, "  %s", pal.paint(dim, clean(w.Confidence)))
			}
			b.WriteString("\n")
		}
		for _, p := range group {
			dot, color := stateStyle(p.State)
			fmt.Fprintf(&b, "   %s %-*s  %s  %s", pal.paint(color, dot), nameWidth, clean(p.ID), pal.paint(color, fmt.Sprintf("%-11s", strings.ToLower(strings.ReplaceAll(clean(p.State), "_", " ")))), pal.paint(dim, fmt.Sprintf("%d/%d jobs", p.ActiveJobs, p.MaxConcurrent)))
			if !p.CooldownUntil.IsZero() {
				fmt.Fprintf(&b, "  %s", pal.paint(yellow, "cooldown until "+p.CooldownUntil.Local().Format("15:04")))
			}
			b.WriteString("\n")
			if p.LastError != "" {
				fmt.Fprintf(&b, "     %s\n", pal.paint(yellow, "↳ "+truncate(clean(p.LastError), 100)))
			}
		}
	}
	if len(doc.Jobs) > 0 {
		var parts []string
		for _, s := range []string{ledger.JobRunningState, ledger.JobDispatchingState, ledger.JobFailingOverState, ledger.JobWaitingQuotaState, ledger.JobQueuedState} {
			if n := doc.Jobs[s]; n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, strings.ToLower(strings.ReplaceAll(s, "_", " "))))
			}
		}
		fmt.Fprintf(&b, "\n %s %s\n", pal.paint(bold, "jobs"), strings.Join(parts, pal.paint(dim, " · ")))
	}
	fmt.Fprintf(&b, "\n %s\n", pal.paint(dim, "jobs = running/max at once per provider · how routing works: hiraishin help routing"))
	_, err := io.WriteString(out, b.String())
	return err
}

func bar(used *float64, warn, reserve float64, pal palette) string {
	if used == nil {
		return pal.paint(dim, strings.Repeat("·", barWidth))
	}
	n := min(barWidth, int(math.Round(*used/100*barWidth)))
	return pal.paint(level(used, warn, reserve), strings.Repeat("█", n)) + pal.paint(dim, strings.Repeat("░", barWidth-n))
}

func level(used *float64, warn, reserve float64) string {
	switch {
	case used == nil:
		return dim
	case *used >= reserve:
		return red
	case *used >= warn:
		return yellow
	}
	return green
}

func stateStyle(state string) (string, string) {
	switch state {
	case ledger.StateAvailable:
		return "●", green
	case ledger.StateDegraded, ledger.StateDraining:
		return "●", yellow
	case ledger.StatePaused, "DISABLED":
		return "○", dim
	}
	return "●", red
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
