package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/engine"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

const (
	topPollInterval = 250 * time.Millisecond
	trendCell       = 15 * time.Minute
	wideTrendCells  = 24
	recentFailovers = 5
	wideLayoutMin   = 120
)

var profileCycle = []string{"quality-first", "burn-expiring-first", "save-scarce", "balanced", config.CustomProfile}

type topData struct {
	seq       int64
	status    statusDoc
	jobs      []ledger.JobRow
	failovers []ledger.FailoverRow
	trend     map[string][]ledger.SampleRow
}

type topModel struct {
	ctx           context.Context
	e             *engine.Engine
	now           func() time.Time
	pal           palette
	warn, reserve float64
	width, height int
	clock         time.Time
	data          topData
	order         []string
	cursor        int
	note          string
}

type topRefresh struct {
	now  time.Time
	data *topData
	err  error
}

type topNote string

func topCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "top",
		Short: "Live view of quota windows, jobs and failovers, with pause, drain and profile keys",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTerminal(cmd.OutOrStdout()) {
				return errors.New("top needs a terminal; use hiraishin status for plain output")
			}
			e, err := a.open(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			m := newTopModel(cmd.Context(), e, a.env.Now, palette{on: a.env.Getenv("NO_COLOR") == ""})
			_, err = tea.NewProgram(m, tea.WithContext(cmd.Context()), tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout())).Run()
			return err
		},
	}
}

func newTopModel(ctx context.Context, e *engine.Engine, now func() time.Time, pal palette) topModel {
	return topModel{ctx: ctx, e: e, now: now, pal: pal, warn: e.Cfg.General.WarnUsedPct, reserve: e.Cfg.General.ReserveUsedPct,
		width: 80, height: 24, clock: now(), data: topData{seq: -1}}
}

func (m topModel) Init() tea.Cmd { return m.poll }

func (m topModel) poll() tea.Msg {
	now := m.now()
	seq, err := m.e.Ledger.Reader().LastSeq(m.ctx)
	if err != nil || seq == m.data.seq {
		return topRefresh{now: now, err: err}
	}
	d, err := loadTop(m.ctx, m.e, now)
	if err != nil {
		return topRefresh{now: now, err: err}
	}
	d.seq = seq
	return topRefresh{now: now, data: &d}
}

func loadTop(ctx context.Context, e *engine.Engine, now time.Time) (topData, error) {
	doc, jobs, err := loadStatus(ctx, e, now)
	if err != nil {
		return topData{}, err
	}
	r := e.Ledger.Reader()
	failovers, err := r.RecentFailovers(ctx, recentFailovers)
	if err != nil {
		return topData{}, err
	}
	samples, err := r.QuotaSamples(ctx, now.Add(-wideTrendCells*trendCell))
	if err != nil {
		return topData{}, err
	}
	return topData{status: doc, jobs: jobs, failovers: failovers, trend: trendByWindow(samples)}, nil
}

func trendByWindow(samples []ledger.SampleRow) map[string][]ledger.SampleRow {
	out := map[string][]ledger.SampleRow{}
	for _, s := range samples {
		k := s.ProviderID + "/" + s.WindowName
		out[k] = append(out[k], s)
	}
	return out
}

func providerOrder(doc statusDoc) []string {
	pools, members, _ := groupPools(doc.Providers)
	var ids []string
	for _, pool := range pools {
		for _, p := range members[pool] {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

func (m topModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case topRefresh:
		m.clock = msg.now
		if msg.err != nil {
			m.note = "error: " + msg.err.Error()
		}
		if msg.data != nil {
			m.data = *msg.data
			m.order = providerOrder(m.data.status)
			m.cursor = max(min(m.cursor, len(m.order)-1), 0)
		}
		return m, tea.Tick(topPollInterval, func(time.Time) tea.Msg { return m.poll() })
	case topNote:
		m.note = string(msg)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.cursor = max(m.cursor-1, 0)
		case "down", "j":
			m.cursor = max(min(m.cursor+1, len(m.order)-1), 0)
		case "p":
			return m, m.setState("pause")
		case "d":
			return m, m.setState("drain")
		case "r":
			return m, m.setState("resume")
		case "tab":
			return m, m.nextProfile()
		}
	}
	return m, nil
}

func (m topModel) setState(verb string) tea.Cmd {
	if len(m.order) == 0 {
		return nil
	}
	id := m.order[m.cursor]
	return func() tea.Msg {
		state, err := setProviderState(m.ctx, m.e, verb, id, "")
		if err != nil {
			return topNote(id + ": " + err.Error())
		}
		return topNote(id + ": " + state)
	}
}

func (m topModel) nextProfile() tea.Cmd {
	current := slices.Index(profileCycle, m.data.status.Profile)
	return func() tea.Msg {
		for step := 1; step < len(profileCycle); step++ {
			next := profileCycle[(current+step+len(profileCycle))%len(profileCycle)]
			if _, err := m.e.Cfg.ProfileWeights(next); err != nil {
				continue
			}
			if err := setProfile(m.ctx, m.e, next); err != nil {
				return topNote("profile: " + err.Error())
			}
			return topNote("profile: " + next)
		}
		return topNote("profile: no other profile is configured")
	}
}

func (m topModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m topModel) render() string {
	pal, doc := m.pal, m.data.status
	bars, cells, wtWidth, nameWidth := barWidth, wideTrendCells/2, 18, 22
	if m.width >= wideLayoutMin {
		bars, cells, wtWidth, nameWidth = 40, wideTrendCells, 30, 40
	}
	body := []string{statusHeader(doc, pal)}
	pools, members, idWidth := groupPools(doc.Providers)
	for _, pool := range pools {
		group := members[pool]
		body = append(body, " "+pal.paint(bold, clean(pool)))
		owner, windows := poolWindows(group)
		for _, w := range windows {
			line := fmt.Sprintf("   %-7s %s %s  %s", clean(w.Name), bar(w.UsedPct, bars, m.warn, m.reserve, pal),
				pal.paint(level(w.UsedPct, m.warn, m.reserve), fmt.Sprintf("%4s", percent(w.UsedPct))),
				pal.paint(dim, sparkline(m.data.trend[owner+"/"+w.Name], m.clock, cells)))
			if !w.ResetsAt.IsZero() {
				line += "  " + pal.paint(dim, "resets "+duration(w.ResetsAt.Sub(m.clock)))
			}
			if w.Confidence != "" && w.Confidence != ledger.ConfidenceExact {
				line += "  " + pal.paint(dim, clean(w.Confidence))
			}
			body = append(body, line)
		}
		for _, p := range group {
			mark := "  "
			if len(m.order) > 0 && m.order[m.cursor] == p.ID {
				mark = pal.paint(cyan, "›") + " "
			}
			dot, color := stateStyle(p.State)
			label := stateLabel(p)
			line := fmt.Sprintf(" %s%s %-*s  %s  %s", mark, pal.paint(color, dot), idWidth, clean(p.ID),
				pal.paint(color, fmt.Sprintf("%-13s", label)),
				pal.paint(dim, fmt.Sprintf("%d/%d jobs", p.ActiveJobs, p.MaxConcurrent)))
			if p.CooldownUntil.After(m.clock) {
				left := "for " + duration(p.CooldownUntil.Sub(m.clock))
				if label != "cooldown" {
					left = "cooldown " + left
				}
				line += "  " + pal.paint(yellow, left)
			}
			body = append(body, line)
			if p.LastError != "" {
				body = append(body, "     "+pal.paint(yellow, "↳ "+clean(p.LastError)))
			}
		}
	}
	body = append(body, "")
	body = append(body, m.renderJobs(wtWidth, nameWidth)...)
	body = append(body, "")
	body = append(body, m.renderFailovers(nameWidth)...)

	footer := []string{" " + pal.paint(dim, "↑↓ select · p pause · d drain · r resume · tab profile · q quit")}
	if m.note != "" {
		footer = append([]string{" " + pal.paint(cyan, clean(m.note))}, footer...)
	}
	if room := max(m.height-len(footer), 1); len(body) > room {
		hidden := len(body) - room + 1
		body = append(body[:room-1], pal.paint(dim, fmt.Sprintf("   … %d more lines, enlarge the terminal", hidden)))
	}
	body = append(body, footer...)
	for i, l := range body {
		body[i] = fit(l, m.width)
	}
	return strings.Join(body, "\n")
}

func (m topModel) renderJobs(wtWidth, nameWidth int) []string {
	pal := m.pal
	if len(m.data.jobs) == 0 {
		return []string{" " + pal.paint(bold, "jobs") + "  " + pal.paint(dim, "none active")}
	}
	jobs := slices.Clone(m.data.jobs)
	slices.SortStableFunc(jobs, func(a, b ledger.JobRow) int { return strings.Compare(a.WorktreePath, b.WorktreePath) })
	wtWidth, nameWidth = min(wtWidth, maxLen(jobs, func(j ledger.JobRow) string { return filepath.Base(j.WorktreePath) })), min(nameWidth, maxLen(jobs, func(j ledger.JobRow) string { return j.Name }))
	perState, backlog := map[string]int{}, map[string]int{}
	for _, j := range jobs {
		perState[j.State]++
		if j.Backlog {
			backlog[j.State]++
		}
	}
	out := []string{" " + pal.paint(bold, "jobs") + "  " + strings.Join(jobCountParts(perState, backlog), pal.paint(dim, " · "))}
	for i, j := range jobs {
		wt := ""
		if i == 0 || jobs[i-1].WorktreePath != j.WorktreePath {
			wt = filepath.Base(j.WorktreePath)
		}
		color, label, tag := yellow, jobStateLabel(j.State), ""
		switch {
		case backlogWaiting(j):
			color, label = dim, "backlog wait"
		case j.State == ledger.JobRunningState:
			color = green
		case j.State == ledger.JobQueuedState:
			color = dim
		}
		if j.Backlog && !backlogWaiting(j) {
			tag = "  " + pal.paint(dim, "backlog")
		}
		out = append(out, fmt.Sprintf("   %-*s  %-*s  %s  %-8s  %s%s", wtWidth, truncate(clean(wt), wtWidth), nameWidth, truncate(clean(j.Name), nameWidth),
			pal.paint(color, fmt.Sprintf("%-13s", label)), truncate(clean(cmp.Or(j.CurrentProvider, "-")), 8), pal.paint(dim, duration(m.clock.Sub(j.UpdatedAt))), tag))
	}
	return out
}

func (m topModel) renderFailovers(nameWidth int) []string {
	pal := m.pal
	if len(m.data.failovers) == 0 {
		return []string{" " + pal.paint(bold, "failovers") + "  " + pal.paint(dim, "none recent")}
	}
	nameWidth = min(nameWidth, maxLen(m.data.failovers, func(f ledger.FailoverRow) string { return f.JobName }))
	out := []string{" " + pal.paint(bold, "failovers")}
	for _, f := range m.data.failovers {
		out = append(out, fmt.Sprintf("   %s %-*s  from %-8s  %s", pal.paint(dim, fmt.Sprintf("%-9s", duration(m.clock.Sub(f.TS))+" ago")), nameWidth, truncate(clean(f.JobName), nameWidth),
			truncate(clean(f.FromProvider), 8), pal.paint(yellow, clean(f.Reason))))
	}
	return out
}

func jobStateLabel(state string) string { return strings.ToLower(strings.ReplaceAll(state, "_", " ")) }

func maxLen[T any](items []T, field func(T) string) int {
	n := 1
	for _, it := range items {
		n = max(n, utf8.RuneCountInString(field(it)))
	}
	return n
}

var sparkLevels = []rune("▁▂▃▄▅▆▇█")

func sparkline(samples []ledger.SampleRow, now time.Time, cells int) string {
	out := []rune(strings.Repeat(" ", cells))
	start := now.Add(-time.Duration(cells) * trendCell)
	last, i := -1.0, 0
	for c := range cells {
		end := start.Add(time.Duration(c+1) * trendCell)
		for i < len(samples) && (c == cells-1 || samples[i].At.Before(end)) {
			last = samples[i].UsedPct
			i++
		}
		if last >= 0 {
			out[c] = sparkLevels[min(max(int(last/100*float64(len(sparkLevels))), 0), len(sparkLevels)-1)]
		}
	}
	return string(out)
}

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return sgr.ReplaceAllString(s, "") }

func fit(s string, width int) string {
	if utf8.RuneCountInString(stripANSI(s)) <= width {
		return s
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(s) && n < width-1; {
		if loc := sgr.FindStringIndex(s[i:]); s[i] == 0x1b && loc != nil && loc[0] == 0 {
			b.WriteString(s[i : i+loc[1]])
			i += loc[1]
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		n++
		i += size
	}
	b.WriteString("…")
	if strings.Contains(s, "\x1b[") {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}
