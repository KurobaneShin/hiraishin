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
	path, err := installWatchService(context.Background(), a, "/opt/hi raishin/hiraishin", run)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".config", "systemd", "user", "hiraishin-watch.service"); path != want {
		t.Fatalf("unit path = %s, want %s", path, want)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(raw)
	for _, want := range []string{
		"ExecStart=\"/opt/hi raishin/hiraishin\" watch\n",
		"Restart=on-failure",
		"Environment=\"PATH=/a b/bin:/usr/bin:/x%%y\"",
		"Environment=\"HIRAISHIN_DATA_DIR=" + filepath.Join(dir, "data") + "\"",
		"Environment=\"HIRAISHIN_CONFIG=" + a.paths.Config + "\"",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	if want := []string{"systemctl --user daemon-reload", "systemctl --user enable --now hiraishin-watch.service"}; !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
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
