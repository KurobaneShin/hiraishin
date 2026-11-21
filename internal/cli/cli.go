// Package cli implements the hiraishin command-line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
)

// Env carries the process environment so commands stay testable without
// touching global state.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
}

const usage = `hiraishin: coding-agent quota scheduler and failover orchestrator

Usage:
  hiraishin <command> [flags]

Commands:
  version   Print the build version
`

// Run executes one CLI invocation and returns the process exit code.
func Run(_ context.Context, args []string, env Env) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(env.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		_, _ = fmt.Fprintln(env.Stdout, "hiraishin", version())
		return 0
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(env.Stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintf(env.Stderr, "hiraishin: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
