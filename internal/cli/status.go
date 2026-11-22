package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/engine"
	"github.com/KurobaneShin/hiraishin/internal/forecast"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
	"github.com/KurobaneShin/hiraishin/internal/watch"
)

type statusWindow struct {
	Name            string             `json:"name"`
	Kind            string             `json:"kind"`
	UsedPct         *float64           `json:"used_pct"`
	ResetsAt        time.Time          `json:"resets_at,omitzero"`
	ResetsInSeconds *int64             `json:"resets_in_seconds"`
	Confidence      string             `json:"confidence"`
	Forecast        *forecast.Forecast `json:"forecast,omitempty"`
}

type statusProvider struct {
	ID            string                       `json:"id"`
	Harness       string                       `json:"harness"`
	Pool          string                       `json:"pool,omitempty"`
	State         string                       `json:"state"`
	ActiveJobs    int                          `json:"active_jobs"`
	MaxConcurrent int                          `json:"max_concurrent"`
	JobBurn       map[string]scheduler.JobBurn `json:"job_burn,omitempty"`
	CooldownUntil time.Time                    `json:"cooldown_until,omitzero"`
	Windows       []statusWindow               `json:"windows"`
	LastError     string                       `json:"last_error,omitempty"`
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
			out := cmd.OutOrStdout()
			if asJSON {
				out = nil
			}
			doc, err := a.status(cmd.Context(), noSensors, out)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), doc)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON (schema_version 1)")
	cmd.Flags().BoolVar(&noSensors, "no-sensors", false, "show the last recorded usage without polling usage endpoints or local logs")
	return cmd
}

func (a *app) status(ctx context.Context, noSensors bool, out io.Writer) (statusDoc, error) {
	e, err := a.open(ctx)
	if err != nil {
		return statusDoc{}, err
	}
	defer func() { _ = e.Close() }()
	if err := e.Maintain(ctx); err != nil {
		return statusDoc{}, err
	}
	var w *watch.Watcher
	var due []string
	if !noSensors {
		w = watch.New(e, a.probe(), slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
		if due, err = w.Due(ctx); err != nil {
			return statusDoc{}, err
		}
	}
	load := func() (statusDoc, error) {
		doc, _, err := loadStatus(ctx, e, a.env.Now())
		return doc, err
	}
	live := out != nil && len(due) > 0 && isTerminal(out)
	if len(due) > 0 && !live {
		if err := w.Poll(ctx); err != nil {
			return statusDoc{}, err
		}
	}
	doc, err := load()
	if err != nil || out == nil {
		return doc, err
	}
	pal := palette{on: colorEnabled(out, a.env.Getenv)}
	warn, reserve := e.Cfg.General.WarnUsedPct, e.Cfg.General.ReserveUsedPct
	var first strings.Builder
	if err := renderStatus(&first, doc, warn, reserve, pal); err != nil {
		return statusDoc{}, err
	}
	if live {
		first.WriteString(pal.paint(dim, "\n refreshing usage: "+strings.Join(due, ", ")+"…") + "\n")
	}
	if _, err := io.WriteString(out, first.String()); err != nil || !live {
		return doc, err
	}
	pollErr := w.Poll(ctx)
	if doc, err = load(); err != nil {
		return statusDoc{}, err
	}
	var second strings.Builder
	if err := renderStatus(&second, doc, warn, reserve, pal); err != nil {
		return statusDoc{}, err
	}
	if _, err := fmt.Fprintf(out, "\x1b[%dA\x1b[J%s", strings.Count(first.String(), "\n"), second.String()); err != nil {
		return statusDoc{}, err
	}
	return doc, pollErr
}

func loadStatus(ctx context.Context, e *engine.Engine, now time.Time) (statusDoc, []ledger.JobRow, error) {
	r := e.Ledger.Reader()
	snap, err := e.ForecastSnapshot(ctx, r)
	if err != nil {
		return statusDoc{}, nil, err
	}
	burn, err := e.JobBurn(ctx, r)
	if err != nil {
		return statusDoc{}, nil, err
	}
	rows, err := r.Providers(ctx)
	if err != nil {
		return statusDoc{}, nil, err
	}
	profile, err := r.Profile(ctx)
	if err != nil {
		return statusDoc{}, nil, err
	}
	jobs, err := r.Jobs(ctx, ledger.JobQueuedState, ledger.JobDispatchingState, ledger.JobRunningState, ledger.JobFailingOverState, ledger.JobWaitingQuotaState)
	if err != nil {
		return statusDoc{}, nil, err
	}
	return buildStatus(snap, rows, burn, e.Cfg.General.WarnUsedPct, cmp.Or(profile, e.Cfg.General.ActiveProfile), jobs, now), jobs, nil
}

func buildStatus(snap []scheduler.Provider, rows []ledger.ProviderRow, burn map[string]map[string]scheduler.JobBurn, warn float64, profile string, jobs []ledger.JobRow, now time.Time) statusDoc {
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
			ActiveJobs: p.ActiveJobs, MaxConcurrent: p.Config.MaxConcurrent, JobBurn: burn[p.ID], Windows: []statusWindow{}}
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
			sw := statusWindow{Name: w.Name, Kind: w.Kind, UsedPct: w.UsedPct, ResetsAt: w.ResetsAt, Confidence: w.Confidence, Forecast: w.Forecast}
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
	pools, members, nameWidth := groupPools(doc.Providers)
	b.WriteString(statusHeader(doc, pal) + "\n")
	for _, pool := range pools {
		group := members[pool]
		fmt.Fprintf(&b, "\n %s\n", pal.paint(bold, clean(pool)))
		_, windows := poolWindows(group)
		for _, w := range windows {
			fmt.Fprintf(&b, "   %-7s %s %s", clean(w.Name), bar(w.UsedPct, barWidth, warn, reserve, pal), pal.paint(level(w.UsedPct, warn, reserve), fmt.Sprintf("%4s", percent(w.UsedPct))))
			if w.ResetsInSeconds != nil {
				fmt.Fprintf(&b, "  %s", pal.paint(dim, "resets "+duration(time.Duration(*w.ResetsInSeconds)*time.Second)))
			}
			if w.Confidence != "" && w.Confidence != ledger.ConfidenceExact {
				fmt.Fprintf(&b, "  %s", pal.paint(dim, clean(w.Confidence)))
			}
			b.WriteString(windowOutlook(w, windows, pal))
			b.WriteString("\n")
		}
		for _, p := range group {
			dot, color := stateStyle(p.State)
			label := stateLabel(p)
			fmt.Fprintf(&b, "   %s %-*s  %s  %s", pal.paint(color, dot), nameWidth, clean(p.ID), pal.paint(color, fmt.Sprintf("%-13s", label)), pal.paint(dim, jobsLabel(p)))
			if !p.CooldownUntil.IsZero() {
				until := "until " + p.CooldownUntil.Local().Format("15:04")
				if label != "cooldown" {
					until = "cooldown " + until
				}
				fmt.Fprintf(&b, "  %s", pal.paint(yellow, until))
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

func windowOutlook(w statusWindow, all []statusWindow, pal palette) string {
	if atLimit(w) {
		return "  " + pal.paint(red, "limit reached")
	}
	for _, o := range all {
		if o.Name != w.Name && atLimit(o) && !resetsFirst(o, w) {
			return "  " + pal.paint(yellow, "blocked until "+clean(o.Name)+" resets")
		}
	}
	f := w.Forecast
	if f == nil {
		if w.UsedPct == nil {
			return ""
		}
		return "  " + pal.paint(dim, "no burn data yet")
	}
	out := "  " + pal.paint(dim, fmt.Sprintf("burn %.3g%%/h", f.PctPerHour))
	switch {
	case f.EmptiesBeforeReset && f.TimeToEmptySeconds != nil && *f.TimeToEmptySeconds > 0:
		out += "  " + pal.paint(yellow, "runs out in "+duration(time.Duration(*f.TimeToEmptySeconds)*time.Second)+", before reset")
	case f.EmptiesBeforeReset:
		out += "  " + pal.paint(yellow, "runs out now")
	case f.PerishableWasteRisk > 0 && w.UsedPct != nil:
		if unused := (100 - *w.UsedPct) * f.PerishableWasteRisk; unused >= 0.5 {
			out += "  " + pal.paint(dim, fmt.Sprintf("~%.0f%% left unused at reset", unused))
		}
	}
	return out
}

func atLimit(w statusWindow) bool { return w.UsedPct != nil && *w.UsedPct >= 100 }

func resetsFirst(a, b statusWindow) bool {
	return a.ResetsInSeconds != nil && b.ResetsInSeconds != nil && *a.ResetsInSeconds < *b.ResetsInSeconds
}

func stateLabel(p statusProvider) string {
	switch p.State {
	case ledger.StateDegraded:
		if !p.CooldownUntil.IsZero() {
			return "cooldown"
		}
		return "near limit"
	case ledger.StateExhausted:
		return "limit reached"
	}
	return strings.ToLower(strings.ReplaceAll(clean(p.State), "_", " "))
}

func groupPools(providers []statusProvider) ([]string, map[string][]statusProvider, int) {
	var pools []string
	members := map[string][]statusProvider{}
	nameWidth := 0
	for _, p := range providers {
		pool := cmp.Or(p.Pool, p.ID)
		if _, seen := members[pool]; !seen {
			pools = append(pools, pool)
		}
		members[pool] = append(members[pool], p)
		nameWidth = max(nameWidth, len(p.ID))
	}
	return pools, members, nameWidth
}

func poolWindows(group []statusProvider) (string, []statusWindow) {
	for _, p := range group {
		if len(p.Windows) > 0 {
			return p.ID, p.Windows
		}
	}
	return "", []statusWindow{{Name: "usage"}}
}

func statusHeader(doc statusDoc, pal palette) string {
	counts, colors := map[string]int{}, map[string]string{}
	for _, p := range doc.Providers {
		if p.State == ledger.StateAvailable {
			continue
		}
		label := stateLabel(p)
		counts[label]++
		_, colors[label] = stateStyle(p.State)
	}
	labels := slices.SortedFunc(maps.Keys(counts), func(a, b string) int {
		return cmp.Or(cmp.Compare(headerRank(a), headerRank(b)), cmp.Compare(a, b))
	})
	h := fmt.Sprintf("%s  profile %s %s %d providers", pal.paint(bold, "hiraishin"), pal.paint(cyan, clean(doc.Profile)), pal.paint(dim, "·"), len(doc.Providers))
	for _, label := range labels {
		h += fmt.Sprintf(" %s %s", pal.paint(dim, "·"), pal.paint(colors[label], fmt.Sprintf("%d %s", counts[label], label)))
	}
	return h
}

func headerRank(label string) int {
	if i := slices.Index([]string{"limit reached", "auth error", "unavailable", "near limit", "cooldown", "draining", "paused", "disabled"}, label); i >= 0 {
		return i
	}
	return math.MaxInt
}

func bar(used *float64, width int, warn, reserve float64, pal palette) string {
	if used == nil {
		return pal.paint(dim, strings.Repeat("·", width))
	}
	n := min(width, int(math.Round(*used/100*float64(width))))
	return pal.paint(level(used, warn, reserve), strings.Repeat("█", n)) + pal.paint(dim, strings.Repeat("░", width-n))
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

func jobsLabel(p statusProvider) string {
	label := fmt.Sprintf("%d jobs", p.ActiveJobs)
	if p.MaxConcurrent > 0 {
		label = fmt.Sprintf("%d/%d jobs", p.ActiveJobs, p.MaxConcurrent)
	}
	names := slices.Sorted(maps.Keys(p.JobBurn))
	var parts []string
	for _, name := range names {
		b := p.JobBurn[name]
		parts = append(parts, fmt.Sprintf("%s %.2g%%/h", clean(name), b.PctPerHour))
	}
	if len(parts) > 0 {
		label += fmt.Sprintf(" · per job %s (%d measured, ~%s)", strings.Join(parts, " "), p.JobBurn[names[0]].Samples, duration(time.Duration(p.JobBurn[names[0]].DurationMin*float64(time.Minute))))
	}
	return label
}
