package dispatch

import (
	"errors"
	"fmt"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

var ErrUnsupportedHarness = errors.New("harness not supported in v0.1")

const (
	promptArg  = "\x00prompt"
	GLMBaseURL = "https://api.z.ai/api/anthropic"
)

type Command struct {
	Argv   []string
	Env    [][2]string
	EnvRef [][2]string
}

func Build(p config.Provider, worktree string) (Command, error) {
	var c Command
	opt := func(flag, v string) {
		if v != "" {
			c.Argv = append(c.Argv, flag, v)
		}
	}
	switch p.Harness {
	case config.HarnessClaudeCode, config.HarnessGLMClaude:
		c.Argv = []string{"claude", "-p", promptArg, "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--add-dir", worktree}
		opt("--model", p.Model)
		opt("--effort", p.Effort)
		if p.Harness == config.HarnessGLMClaude {
			c.Env = append(c.Env, [2]string{"ANTHROPIC_BASE_URL", GLMBaseURL})
			c.EnvRef = append(c.EnvRef, [2]string{"ANTHROPIC_AUTH_TOKEN", p.APIKeyEnv})
		}
	case config.HarnessKimiNative:
		c.Argv = []string{"kimi", "--prompt", promptArg, "--output-format", "stream-json", "--auto"}
		opt("--model", p.Model)
	case config.HarnessOpenCode:
		c.Argv = []string{"opencode", "run", "--format", "json", "--auto", "--dir", worktree}
		opt("--agent", p.HarnessAgent)
		opt("--model", p.Model)
		opt("--variant", p.Effort)
		c.Argv = append(c.Argv, promptArg)
	case config.HarnessAgy:
		c.Argv = []string{"agy", "--mode", "accept-edits", "--dangerously-skip-permissions", "--add-dir", worktree, "--output-format", "stream-json"}
		opt("--model", p.Model)
		opt("--effort", p.Effort)
		c.Argv = append(c.Argv, "--print", promptArg)
	default:
		return Command{}, fmt.Errorf("%w: %s", ErrUnsupportedHarness, p.Harness)
	}
	c.Argv = append(append([]string{}, p.LaunchPrefix...), c.Argv...)
	return c, nil
}
