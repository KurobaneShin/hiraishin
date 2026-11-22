package cli

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/inventory"
)

func initCmd(a *app) *cobra.Command {
	var yes, force bool
	var adapter string
	var claudeDirs []string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Detect installed CLIs and write preferences.toml",
		Args:  args(0, ""),
		RunE: func(_ *cobra.Command, _ []string) error {
			found, err := inventory.Detect(inventory.System{LookPath: a.env.LookPath, ReadFile: a.env.ReadFile, Getenv: a.env.Getenv, Stat: a.env.Stat, Glob: a.env.Glob}, claudeDirs)
			if err != nil {
				return fmt.Errorf("detect CLIs: %w", err)
			}
			if len(found) == 0 {
				return fmt.Errorf("none of claude, kimi, opencode, agy, codex, gemini found on PATH")
			}
			if !yes && a.env.Interactive {
				if found, err = confirm(a, found); err != nil {
					return err
				}
			}
			cfg := inventory.Build(found, adapter)
			if err := config.Write(a.paths.Config, cfg, force); err != nil {
				return err
			}
			return printInit(a, cfg)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "accept detected defaults without prompting")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing preferences.toml")
	cmd.Flags().StringVar(&adapter, "adapter", "process", "default terminal adapter: process, tmux or orca")
	cmd.Flags().StringArrayVar(&claudeDirs, "claude-config-dir", nil, "extra Claude Code config dir (CLAUDE_CONFIG_DIR) to add as its own account; repeatable, globs and ~/ allowed")
	return cmd
}

type prompter struct {
	a  *app
	in *bufio.Scanner
}

func (p prompter) ask(q, def string) (string, error) {
	_, _ = fmt.Fprintf(p.a.env.Stdout, "%s [%s]: ", q, def)
	if !p.in.Scan() {
		return def, p.in.Err()
	}
	if s := strings.TrimSpace(p.in.Text()); s != "" {
		return s, nil
	}
	return def, nil
}

func (p prompter) askInt(q string, def int) (int, error) {
	s, err := p.ask(q, strconv.Itoa(def))
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 99 {
		return 0, usageError{msg: fmt.Sprintf("%s: %q is not a positive number", strings.TrimSpace(q), s)}
	}
	return n, nil
}

func confirm(a *app, found []inventory.Found) ([]inventory.Found, error) {
	p := prompter{a: a, in: bufio.NewScanner(a.env.Stdin)}
	var out []inventory.Found
	for _, f := range found {
		label := f.Provider.Harness
		if f.Provider.HarnessAgent != "" {
			label += " agent " + f.Provider.HarnessAgent
		}
		ok, err := p.ask(fmt.Sprintf("Enable %s (%s)? y/n", f.ID, label), "y")
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(strings.ToLower(ok), "y") {
			continue
		}
		prov := f.Provider
		for _, q := range []struct {
			label string
			dst   *int
		}{{"  quality tier 1-5", &prov.QualityTier}, {"  scarcity tier 1-5", &prov.ScarcityTier}, {"  max concurrent jobs", &prov.MaxConcurrent}} {
			if *q.dst, err = p.askInt(q.label, *q.dst); err != nil {
				return nil, err
			}
		}
		f.Provider = prov
		out = append(out, f)
	}
	return out, nil
}

func printInit(a *app, cfg *config.Config) error {
	w := tabwriter.NewWriter(a.env.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Wrote %s\n\nPROVIDER\tHARNESS\tMODEL\tQUALITY\tSCARCITY\tSLOTS\tPOOL\n", a.paths.Config)
	for _, id := range cfg.ProviderIDs() {
		p := cfg.Providers[id]
		model := p.Model
		if model == "" {
			model = "(CLI default)"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n", id, p.Harness, model, p.QualityTier, p.ScarcityTier, p.MaxConcurrent, p.SharedQuotaPool)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, id := range cfg.ProviderIDs() {
		if env := cfg.Providers[id].APIKeyEnv; env != "" {
			_, _ = fmt.Fprintf(a.env.Stdout, "\n%s reads its key from $%s at launch; the key is never written to disk.\n", id, env)
		}
	}
	_, err := fmt.Fprintln(a.env.Stdout, "\nEdit roles and tiers in the file, then run: hiraishin status")
	return err
}
