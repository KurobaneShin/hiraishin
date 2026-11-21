// Command hiraishin schedules coding-agent CLI jobs across subscription
// quota windows and fails over stalled jobs inside their git worktree.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/KurobaneShin/hiraishin/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], cli.Env{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getenv: os.Getenv,
	})
	stop()
	os.Exit(code)
}
