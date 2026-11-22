package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/stack"
)

const (
	sizingTimeout       = 2 * time.Second
	contextTimeout      = 5 * time.Second
	defaultMemoryURL    = "http://127.0.0.1:49375/mcp"
	defaultQMDSnippets  = 5
	defaultGraphTokens  = 1500
	briefGoalChars      = 300
	maxLexFiles         = 10
	outcomeTier         = "episodic"
	outcomeKind         = "hiraishin-outcome"
	integrationAIMemory = "ai-memory"
	integrationQMD      = "qmd"
	integrationGraphify = "graphify"
)

var slugUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)

func skipped(integration string, err error) {
	slog.Debug("stack integration skipped", "integration", integration, "err", err)
}

func (e *Engine) memory() (stack.Memory, bool) {
	m := e.Cfg.Integrations.AIMemory
	if m == nil || !m.Enabled {
		return stack.Memory{}, false
	}
	if m.Transport != "" && m.Transport != "http" {
		skipped(integrationAIMemory, fmt.Errorf("transport %q is not supported; use http", m.Transport))
		return stack.Memory{}, false
	}
	return stack.Memory{URL: cmp.Or(m.URL, defaultMemoryURL)}, true
}

func (e *Engine) Enrich(ctx context.Context, job ledger.JobRow, resume bool) string {
	in := e.Cfg.Integrations
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	changed := sync.OnceValue(func() []string {
		if !resume {
			return nil
		}
		return e.changedFiles(ctx, job, e.Ledger.Reader())
	})
	var parts [3]string
	var wg sync.WaitGroup
	if mem, ok := e.memory(); ok && resume && job.NativeSessionID != "" {
		wg.Go(func() {
			var err error
			if parts[0], err = mem.Recovery(ctx, stack.ResolveScope(ctx, job.WorktreePath), job.NativeSessionID); err != nil {
				skipped(integrationAIMemory, err)
			}
		})
	}
	if q := in.QMD; q != nil && q.Enabled {
		wg.Go(func() {
			var err error
			if parts[1], err = stack.FindDocs(ctx, e.docQuery(job, resume, changed)); err != nil {
				skipped(integrationQMD, err)
			}
		})
	}
	if g := in.Graphify; g != nil && g.Enabled && resume {
		wg.Go(func() {
			graph, err := loadGraph(ctx, job.WorktreePath)
			if err != nil {
				skipped(integrationGraphify, err)
				return
			}
			parts[2] = graph.Subgraph(changed(), cmp.Or(g.MaxContextTokens, defaultGraphTokens))
		})
	}
	wg.Wait()
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

func (e *Engine) docQuery(job ledger.JobRow, resume bool, changed func() []string) stack.DocQuery {
	q := e.Cfg.Integrations.QMD
	intent := fmt.Sprintf("context for %s (%s)", job.Name, job.Role)
	if resume {
		intent = "resume " + intent
	}
	lex := []string{job.Name, job.Role}
	for _, f := range changed()[:min(maxLexFiles, len(changed()))] {
		lex = append(lex, filepath.Base(f))
	}
	return stack.DocQuery{
		Collections: q.Collections, Limit: cmp.Or(q.MaxSnippets, defaultQMDSnippets),
		Intent: intent, Lex: strings.Join(lex, " "), Vec: briefGoal(job.BriefPath),
		Skip: []string{job.BriefPath, job.ReportPath},
	}
}

func briefGoal(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var words []string
	n := 0
	for _, f := range strings.Fields(string(b)) {
		if strings.Trim(f, "#") == "" {
			continue
		}
		if n+len(f) > briefGoalChars {
			break
		}
		words = append(words, f)
		n += len(f) + 1
	}
	return strings.Join(words, " ")
}

func (e *Engine) tierFloor(ctx context.Context, tx *ledger.Tx, job ledger.JobRow) (int, string) {
	g := e.Cfg.Integrations.Graphify
	if g == nil || !g.Enabled || !g.AutoSizeTier || job.WorktreePath == "" {
		return 0, ""
	}
	ctx, cancel := context.WithTimeout(ctx, sizingTimeout)
	defer cancel()
	graph, err := loadGraph(ctx, job.WorktreePath)
	if err != nil {
		skipped(integrationGraphify, err)
		return 0, ""
	}
	files := e.changedFiles(ctx, job, tx)
	if brief, err := os.ReadFile(job.BriefPath); err == nil {
		files = append(files, graph.Mentioned(string(brief))...)
	}
	size := graph.Size(files)
	return size.TierFloor(), "graphify (" + size.Reason() + ")"
}

func loadGraph(ctx context.Context, worktree string) (*stack.Graph, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", worktree, "rev-parse", "--show-toplevel", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not a git work tree", stack.ErrUnavailable, worktree)
	}
	lines := strings.Fields(string(out))
	roots := []string{worktree}
	if len(lines) == 2 {
		roots = []string{lines[0], filepath.Dir(lines[1])}
	}
	var errs []error
	for _, root := range slices.Compact(roots) {
		g, err := stack.LoadGraph(root)
		if err == nil {
			return g, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

func (e *Engine) changedFiles(ctx context.Context, job ledger.JobRow, tx *ledger.Tx) []string {
	base := "HEAD"
	if attempts, err := tx.Attempts(ctx, job.ID); err == nil && len(attempts) > 0 && attempts[0].StartCommit != "" {
		base = attempts[0].StartCommit
	}
	var files []string
	for _, argv := range [][]string{{"diff", "--name-only", base}, {"ls-files", "--others", "--exclude-standard", "--full-name"}} {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", job.WorktreePath}, argv...)...).Output()
		if err != nil {
			continue
		}
		files = append(files, strings.Fields(string(out))...)
	}
	return files
}

func (e *Engine) RecordOutcome(ctx context.Context, jobID string) error {
	mem, ok := e.memory()
	if !ok || !e.Cfg.Integrations.AIMemory.RecordOutcomes {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	r := e.Ledger.Reader()
	job, err := r.Job(ctx, jobID)
	if err != nil {
		return err
	}
	attempts, err := r.Attempts(ctx, jobID)
	if err != nil {
		return err
	}
	decisions, err := r.Decisions(ctx, jobID)
	if err != nil {
		return err
	}
	tags := []string{"hiraishin", "role:" + job.Role}
	var b strings.Builder
	fmt.Fprintf(&b, "# Outcome: %s\n\n- Job: %s (role %s)\n- Result: %s\n- Worktree: %s (branch %s)\n- Failovers: %d\n", job.Name, job.ID, job.Role, job.State, job.WorktreePath, cmp.Or(job.Branch, "unknown"), job.FailoverCount)
	if n := len(attempts); n > 0 {
		first, last := attempts[0], attempts[n-1]
		fmt.Fprintf(&b, "- Final commit: %s\n", cmp.Or(last.EndCommit, "none"))
		if !last.EndedAt.IsZero() {
			fmt.Fprintf(&b, "- Wall time: %s\n", last.EndedAt.Sub(first.StartedAt).Round(time.Second))
		}
	}
	b.WriteString("\n## Provider chain\n")
	for _, a := range attempts {
		fmt.Fprintf(&b, "%d. %s: %s\n", a.Num, a.ProviderID, cmp.Or(a.ExitReason, "running"))
		if tag := "provider:" + a.ProviderID; !slices.Contains(tags, tag) {
			tags = append(tags, tag)
		}
	}
	b.WriteString("\n## Routing decisions\n")
	for _, d := range decisions {
		fmt.Fprintf(&b, "- %s: %s\n", d.Trigger, d.Summary)
	}
	date := e.Now().UTC().Format(time.DateOnly)
	path := fmt.Sprintf("hiraishin/outcomes/%s-%s-%s.md", date, strings.Trim(slugUnsafe.ReplaceAllString(strings.ToLower(job.Name), "-"), "-"), job.ID)
	if err := mem.WritePage(ctx, stack.ResolveScope(ctx, job.WorktreePath), stack.Page{Path: path, Body: b.String(), Tier: outcomeTier, Kind: outcomeKind, Tags: tags}); err != nil {
		skipped(integrationAIMemory, err)
		return err
	}
	return nil
}
