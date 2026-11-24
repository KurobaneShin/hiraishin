package dispatch

import (
	"errors"
	"fmt"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

var ErrUnsupportedHarness = errors.New("unsupported harness")

const (
	promptArg  = "\x00prompt"
	GLMBaseURL = "https://api.z.ai/api/anthropic"
)

type Command struct {
	Argv   []string
	Env    [][2]string
	EnvRef [][2]string
	Unset  []string
}

func CanResume(harness string) bool {
	switch harness {
	case config.HarnessClaudeCode, config.HarnessGLMClaude, config.HarnessKimiNative, config.HarnessOpenCode, config.HarnessAgy, config.HarnessCodex, config.HarnessGeminiCLI:
		return true
	}
	return false
}

func Build(p config.Provider, worktree, resumeSession string) (Command, error) {
	var c Command
	opt := func(flag, v string) {
		if v != "" {
			c.Argv = append(c.Argv, flag, v)
		}
	}
	switch p.Harness {
	case config.HarnessClaudeCode, config.HarnessGLMClaude:
		c.Argv = []string{"claude", "-p", promptArg, "--output-format", "stream-json", "--verbose", "--permission-mode", "auto", "--add-dir", worktree}
		opt("--resume", resumeSession)
		opt("--model", p.Model)
		opt("--effort", p.Effort)
		if p.ConfigDir != "" {
			c.Env = append(c.Env, [2]string{"CLAUDE_CONFIG_DIR", p.ConfigDir})
		} else {
			c.Unset = append(c.Unset, "CLAUDE_CONFIG_DIR")
		}
		if p.Harness == config.HarnessGLMClaude {
			c.Env = append(c.Env, [2]string{"ANTHROPIC_BASE_URL", GLMBaseURL})
			c.EnvRef = append(c.EnvRef, [2]string{"ANTHROPIC_AUTH_TOKEN", p.APIKeyEnv})
		}
	case config.HarnessKimiNative:
		c.Argv = []string{"kimi", "--prompt", promptArg, "--output-format", "stream-json"}
		opt("--session", resumeSession)
		opt("--model", p.Model)
	case config.HarnessOpenCode:
		c.Argv = []string{"opencode", "run", "--format", "json", "--auto", "--dir", worktree}
		opt("--session", resumeSession)
		opt("--agent", p.HarnessAgent)
		opt("--model", p.Model)
		opt("--variant", p.Effort)
		c.Argv = append(c.Argv, promptArg)
	case config.HarnessAgy:
		c.Argv = []string{"agy", "--mode", "accept-edits", "--dangerously-skip-permissions", "--add-dir", worktree, "--output-format", "stream-json"}
		opt("--conversation", resumeSession)
		opt("--model", p.Model)
		opt("--effort", p.Effort)
		c.Argv = append(c.Argv, "--print", promptArg)
	case config.HarnessCodex:
		c.Argv = []string{"codex", "exec", "--json", "--sandbox", "workspace-write"}
		opt("--model", p.Model)
		if p.Effort != "" {
			c.Argv = append(c.Argv, "-c", "model_reasoning_effort="+p.Effort)
		}
		if resumeSession != "" {
			c.Argv = append(c.Argv, "resume", resumeSession)
		}
		c.Argv = append(c.Argv, promptArg)
	case config.HarnessGeminiCLI:
		c.Argv = []string{"gemini", "-p", promptArg, "--output-format", "stream-json", "--yolo"}
		opt("--resume", resumeSession)
		opt("--model", p.Model)
	default:
		return Command{}, fmt.Errorf("%w: %s", ErrUnsupportedHarness, p.Harness)
	}
	c.Argv = append(append([]string{}, p.LaunchPrefix...), c.Argv...)
	return c, nil
}
