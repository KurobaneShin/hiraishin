package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/KurobaneShin/hiraishin/internal/dispatch"
	"github.com/KurobaneShin/hiraishin/internal/sensor"
	"github.com/KurobaneShin/hiraishin/internal/watch"
)

func lockWatch(dataDir string) (func(), error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, "watch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("another hiraishin watch is already running")
		}
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

func (a *app) probe() *sensor.Probe {
	return &sensor.Probe{
		Client: &http.Client{}, Getenv: a.env.Getenv, Home: a.env.Getenv("HOME"), Now: a.env.Now,
		Run: func(ctx context.Context, name string, argv ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, argv...).Output()
		},
	}
}

func watchCmd(a *app) *cobra.Command {
	var interval, grace time.Duration
	var exitWhenIdle, noSensors bool
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Follow running jobs, fail over on limit hits and dispatch waiting jobs",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			unlock, err := lockWatch(a.paths.DataDir)
			if err != nil {
				return err
			}
			defer unlock()
			e, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			var probe *sensor.Probe
			if !noSensors {
				probe = a.probe()
			}
			log.Info("watching", "ledger", e.Paths.Ledger(), "interval", interval, "sensors", !noSensors)
			if err := watch.New(e, probe, log, grace).Run(ctx, interval, exitWhenIdle); err != nil {
				return fmt.Errorf("watch: %w", err)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.DurationVar(&interval, "interval", 2*time.Second, "time between checks")
	f.DurationVar(&grace, "grace", dispatch.DefaultGrace, "wait between SIGINT, SIGTERM and SIGKILL when stopping an agent")
	f.BoolVar(&exitWhenIdle, "exit-when-idle", false, "exit once no job is RUNNING or FAILING_OVER")
	f.BoolVar(&noSensors, "no-sensors", false, "do not poll usage endpoints or local logs")
	return cmd
}
