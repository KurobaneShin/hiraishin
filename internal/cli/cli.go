package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/engine"
)

type Env struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Getenv      func(string) string
	LookPath    func(string) (string, error)
	ReadFile    func(string) ([]byte, error)
	Stat        func(string) (fs.FileInfo, error)
	Glob        func(string) ([]string, error)
	Now         func() time.Time
	Interactive bool
}

type app struct {
	env   Env
	paths config.Paths
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

func Run(ctx context.Context, args []string, env Env) int {
	if env.Now == nil {
		env.Now = time.Now
	}
	if env.Stat == nil {
		env.Stat = os.Stat
	}
	if env.Glob == nil {
		env.Glob = filepath.Glob
	}
	a := &app{env: env, paths: config.ResolvePaths(env.Getenv)}
	root := newRoot(a)
	root.SetArgs(args)
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	_, _ = fmt.Fprintf(env.Stderr, "hiraishin: %v\n", err)
	if _, ok := errors.AsType[usageError](err); ok || strings.HasPrefix(err.Error(), "unknown command") {
		return ExitUsage
	}
	return ExitError
}

func newRoot(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "hiraishin",
		Short:         "Coding-agent quota scheduler and failover orchestrator",
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{msg: err.Error()} })
	root.AddCommand(
		initCmd(a), runCmd(a), watchCmd(a), cancelCmd(a), statusCmd(a), whyCmd(a), mcpCmd(a), simCmd(a),
		profileCmd(a), preferCmd(a), pinCmd(a), unpinCmd(a), routingTopic(),
		outcomeCmd(a), learnCmd(a),
		providerStateCmd(a, "pause"), providerStateCmd(a, "drain"), providerStateCmd(a, "resume"),
		doctorCmd(a), ledgerCmd(a),
		versionCmd(),
	)
	return root
}

func args(n int, names string) cobra.PositionalArgs {
	return func(_ *cobra.Command, got []string) error {
		if len(got) != n {
			return usageError{msg: fmt.Sprintf("want %d argument(s): %s", n, names)}
		}
		return nil
	}
}

func (a *app) open(ctx context.Context) (*engine.Engine, error) {
	return engine.Open(ctx, a.paths, a.env.Now)
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "hiraishin", version())
			return err
		},
	}
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
