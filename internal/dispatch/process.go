package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var ErrNoProcess = errors.New("attempt process not started")

const DefaultGrace = 10 * time.Second

func pgid(dir string) (int, error) {
	b, err := os.ReadFile(filepath.Join(dir, PgidFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, ErrNoProcess
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n <= 1 {
		return 0, fmt.Errorf("bad pgid file in %s: %q", dir, b)
	}
	return n, nil
}

func Alive(dir string) bool {
	g, err := pgid(dir)
	if err != nil {
		return false
	}
	return syscall.Kill(-g, 0) == nil
}

func ExitCode(dir string) (int, bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, ExitFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false, fmt.Errorf("bad exit file in %s: %w", dir, err)
	}
	return n, true, nil
}

func Stop(ctx context.Context, dir string, grace time.Duration) error {
	g, err := pgid(dir)
	if err != nil {
		return err
	}
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL} {
		if err := syscall.Kill(-g, sig); errors.Is(err, syscall.ESRCH) {
			return nil
		} else if err != nil {
			return fmt.Errorf("signal %v to group %d: %w", sig, g, err)
		}
		if gone(ctx, g, grace) {
			return nil
		}
	}
	return fmt.Errorf("process group %d survived SIGKILL", g)
}

func gone(ctx context.Context, g int, within time.Duration) bool {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	deadline := time.After(within)
	for {
		if syscall.Kill(-g, 0) != nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		case <-t.C:
		}
	}
}
