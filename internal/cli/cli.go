package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

type Env struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Getenv      func(string) string
	LookPath    func(string) (string, error)
	ReadFile    func(string) ([]byte, error)
	Now         func() time.Time
	Interactive bool
}

type app struct {
	env   Env
	paths config.Paths
}

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, a *app, args []string) error
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func commands() []command {
	return []command{
		{"init", "Detect installed CLIs and write preferences.toml", runInit},
		{"version", "Print the build version", runVersion},
	}
}

func usage() string {
	var b strings.Builder
	b.WriteString("hiraishin: coding-agent quota scheduler and failover orchestrator\n\nUsage:\n  hiraishin <command> [flags]\n\nCommands:\n")
	for _, c := range commands() {
		fmt.Fprintf(&b, "  %-9s %s\n", c.name, c.summary)
	}
	return b.String()
}

func find(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func Run(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(env.Stderr, usage())
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(env.Stdout, usage())
		return 0
	case "--version":
		args[0] = "version"
	}
	cmd, ok := find(args[0])
	if !ok {
		_, _ = fmt.Fprintf(env.Stderr, "hiraishin: unknown command %q\n\n%s", args[0], usage())
		return 2
	}
	a := &app{env: env, paths: config.ResolvePaths(env.Getenv)}
	if err := cmd.run(ctx, a, args[1:]); err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "hiraishin %s: %v\n", args[0], err)
		if _, ok := errors.AsType[usageError](err); ok {
			return 2
		}
		return 1
	}
	return 0
}

func runVersion(_ context.Context, a *app, _ []string) error {
	v := "(devel)"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		v = info.Main.Version
	}
	_, err := fmt.Fprintln(a.env.Stdout, "hiraishin", v)
	return err
}
