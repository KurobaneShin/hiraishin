package cli

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const watchUnitName = "hiraishin-watch.service"

type runner func(ctx context.Context, name string, args ...string) error

func systemctl(ctx context.Context, name string, args ...string) error {
	if out, err := exec.CommandContext(ctx, name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func watchUnitPath(a *app) string {
	base := cmp.Or(a.env.Getenv("XDG_CONFIG_HOME"), filepath.Join(a.env.Getenv("HOME"), ".config"))
	return filepath.Join(base, "systemd", "user", watchUnitName)
}

func installWatchService(ctx context.Context, a *app, exe string, run runner) (string, error) {
	path := watchUnitPath(a)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(watchUnit(exe, a)), 0o644); err != nil {
		return "", err
	}
	for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", watchUnitName}} {
		if err := run(ctx, "systemctl", args...); err != nil {
			return "", err
		}
	}
	return path, nil
}

func uninstallWatchService(ctx context.Context, a *app, run runner) error {
	if err := run(ctx, "systemctl", "--user", "disable", "--now", watchUnitName); err != nil {
		return err
	}
	if err := os.Remove(watchUnitPath(a)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return run(ctx, "systemctl", "--user", "daemon-reload")
}

func watchUnit(exe string, a *app) string {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=hiraishin watch: usage sampling, failover and dispatch\nAfter=network-online.target\n\n[Service]\n")
	fmt.Fprintf(&b, "ExecStart=%s watch\n", unitQuote(exe))
	b.WriteString("Restart=on-failure\nRestartSec=30\n")
	env := [][2]string{{"PATH", dedupePath(a.env.Getenv("PATH"))}, {"HIRAISHIN_CONFIG", a.paths.Config}, {"HIRAISHIN_DATA_DIR", a.paths.DataDir}}
	for _, kv := range env {
		if kv[1] != "" {
			fmt.Fprintf(&b, "Environment=%s\n", unitQuote(kv[0]+"="+kv[1]))
		}
	}
	b.WriteString("\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

func unitQuote(s string) string {
	s = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%").Replace(s)
	return "\"" + s + "\""
}

func dedupePath(path string) string {
	var out []string
	for _, dir := range strings.Split(path, ":") {
		if dir != "" && !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	return strings.Join(out, ":")
}
