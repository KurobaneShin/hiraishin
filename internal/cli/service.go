package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
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

func watchBinPath(a *app) string {
	base := cmp.Or(a.env.Getenv("XDG_BIN_HOME"), filepath.Join(a.env.Getenv("HOME"), ".local", "bin"))
	return filepath.Join(base, "hiraishin")
}

func installWatchService(ctx context.Context, a *app, exe string, run runner) (unit, bin string, err error) {
	unit, bin = watchUnitPath(a), watchBinPath(a)
	if err := installBinary(exe, bin); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(unit, []byte(watchUnit(bin, a)), 0o644); err != nil {
		return "", "", err
	}
	for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", watchUnitName}, {"--user", "restart", watchUnitName}} {
		if err := run(ctx, "systemctl", args...); err != nil {
			return "", "", err
		}
	}
	return unit, bin, nil
}

func installBinary(src, dst string) error {
	from, err := os.Stat(src)
	if err != nil {
		return err
	}
	if to, err := os.Stat(dst); err == nil && os.SameFile(from, to) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".hiraishin-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
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
	b.WriteString("Restart=on-failure\nRestartSec=30\nKillMode=process\n")
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
	for dir := range strings.SplitSeq(path, ":") {
		if dir != "" && !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	return strings.Join(out, ":")
}
