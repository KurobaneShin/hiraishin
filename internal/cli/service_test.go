package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

func TestWatchServiceInstallWritesAUnitAndStartsIt(t *testing.T) {
	dir := t.TempDir()
	vars := map[string]string{"HOME": dir, "PATH": "/a b/bin:/usr/bin:/a b/bin:/x%y", "HIRAISHIN_DATA_DIR": filepath.Join(dir, "data")}
	a := &app{env: Env{Getenv: func(k string) string { return vars[k] }}}
	a.paths = config.ResolvePaths(a.env.Getenv)
	var calls []string
	run := func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	built := filepath.Join(dir, "go bin", "hiraishin")
	if err := os.MkdirAll(filepath.Dir(built), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(built, []byte("new build"), 0o700); err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(dir, ".local", "bin", "hiraishin")
	if err := os.MkdirAll(filepath.Dir(stable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stable, []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, bin, err := installWatchService(context.Background(), a, built, run)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".config", "systemd", "user", "hiraishin-watch.service"); path != want {
		t.Fatalf("unit path = %s, want %s", path, want)
	}
	if bin != stable {
		t.Fatalf("binary path = %s, want %s", bin, stable)
	}
	if got, err := os.ReadFile(stable); err != nil || string(got) != "new build" {
		t.Fatalf("stable binary = %q, %v; want the new build", got, err)
	}
	if fi, err := os.Stat(stable); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("stable binary mode = %v, %v; want 0755", fi.Mode().Perm(), err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(stable)); len(entries) != 1 {
		t.Fatalf("temp files left in %s: %v", filepath.Dir(stable), entries)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(raw)
	for _, want := range []string{
		"ExecStart=\"" + stable + "\" watch\n",
		"Restart=on-failure",
		"KillMode=process\n",
		"Environment=\"PATH=/a b/bin:/usr/bin:/x%%y\"",
		"Environment=\"HIRAISHIN_DATA_DIR=" + filepath.Join(dir, "data") + "\"",
		"Environment=\"HIRAISHIN_CONFIG=" + a.paths.Config + "\"",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	want := []string{"systemctl --user daemon-reload", "systemctl --user enable hiraishin-watch.service", "systemctl --user restart hiraishin-watch.service"}
	if !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
	calls = nil
	if _, _, err := installWatchService(context.Background(), a, stable, run); err != nil {
		t.Fatalf("reinstall from the stable binary: %v", err)
	}
	if got, err := os.ReadFile(stable); err != nil || string(got) != "new build" {
		t.Fatalf("stable binary after reinstall from itself = %q, %v", got, err)
	}
	if !slices.Equal(calls, want) {
		t.Errorf("reinstall calls = %q, want %q", calls, want)
	}
	calls = nil
	if err := uninstallWatchService(context.Background(), a, run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("unit still present after uninstall: %v", err)
	}
	if want := []string{"systemctl --user disable --now hiraishin-watch.service", "systemctl --user daemon-reload"}; !slices.Equal(calls, want) {
		t.Errorf("uninstall calls = %q, want %q", calls, want)
	}
}
