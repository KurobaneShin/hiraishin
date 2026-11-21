package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const (
	AdapterProcess = "process"
	AdapterTmux    = "tmux"
	AdapterOrca    = "orca"
	tmuxSession    = "hiraishin"
	orcaBinary     = "orca-ide"
)

type Launch struct {
	Title    string
	Worktree string
	Script   string
}

type Adapter interface {
	Spawn(ctx context.Context, l Launch) (string, error)
}

func NewAdapter(name string) (Adapter, error) {
	switch name {
	case AdapterProcess, "":
		return process{}, nil
	case AdapterTmux:
		return tmux{}, nil
	case AdapterOrca:
		return orca{}, nil
	default:
		return nil, fmt.Errorf("unknown terminal adapter %q", name)
	}
}

type process struct{}

func (process) Spawn(_ context.Context, l Launch) (string, error) {
	cmd := exec.Command("bash", l.Script) //nolint:noctx
	cmd.Dir = l.Worktree
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start %s: %w", l.Script, err)
	}
	go func() { _ = cmd.Wait() }()
	return "pid:" + strconv.Itoa(cmd.Process.Pid), nil
}

func output(ctx context.Context, name string, argv ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(argv, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), " \t\r\n"), nil
}

type tmux struct{}

func (tmux) Spawn(ctx context.Context, l Launch) (string, error) {
	if _, err := output(ctx, "tmux", "has-session", "-t", tmuxSession); err != nil {
		if _, err := output(ctx, "tmux", "new-session", "-d", "-s", tmuxSession); err != nil {
			return "", err
		}
	}
	pane, err := output(ctx, "tmux", "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", tmuxSession+":", "-n", l.Title, "-c", l.Worktree, "bash "+quote(l.Script))
	if err != nil {
		return "", err
	}
	return "tmux:" + pane, nil
}

type orca struct{}

func (orca) Spawn(ctx context.Context, l Launch) (string, error) {
	out, err := output(ctx, orcaBinary, "terminal", "create", "--worktree", "path:"+l.Worktree, "--title", l.Title, "--command", "bash "+quote(l.Script), "--json")
	if err != nil {
		return "", err
	}
	var doc any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return "", fmt.Errorf("orca terminal create: %w", err)
	}
	if h := findHandle(doc); h != "" {
		return "orca:" + h, nil
	}
	return "", errors.New("orca terminal create: no terminal handle in reply")
}

func findHandle(v any) string {
	switch t := v.(type) {
	case map[string]any:
		if h, ok := t["handle"].(string); ok && h != "" {
			return h
		}
		for _, x := range t {
			if h := findHandle(x); h != "" {
				return h
			}
		}
	case []any:
		for _, x := range t {
			if h := findHandle(x); h != "" {
				return h
			}
		}
	}
	return ""
}
