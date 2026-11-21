package dispatch

import (
	"context"
	"fmt"
	"strings"
)

type Checkpoint struct {
	Root     string
	Branch   string
	Head     string
	Status   string
	DiffStat string
	Dirty    []string
}

func git(ctx context.Context, worktree string, argv ...string) (string, error) {
	return output(ctx, "git", append([]string{"-C", worktree}, argv...)...)
}

func ReadCheckpoint(ctx context.Context, worktree string) (Checkpoint, error) {
	root, err := git(ctx, worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		return Checkpoint{}, fmt.Errorf("worktree %s is not a git work tree: %w", worktree, err)
	}
	c := Checkpoint{Root: root}
	c.Head, _ = git(ctx, worktree, "rev-parse", "--verify", "--quiet", "HEAD")
	c.Branch, _ = git(ctx, worktree, "rev-parse", "--abbrev-ref", "HEAD")
	if c.Status, err = git(ctx, worktree, "status", "--porcelain=v1", "--untracked-files=all"); err != nil {
		return Checkpoint{}, err
	}
	for line := range strings.Lines(c.Status) {
		line = strings.TrimRight(line, "\n")
		if len(line) < 4 {
			continue
		}
		path := line[3:]
		if _, after, ok := strings.Cut(path, " -> "); ok {
			path = after
		}
		c.Dirty = append(c.Dirty, path)
	}
	if c.Head != "" {
		if c.DiffStat, err = git(ctx, worktree, "diff", "--stat", "HEAD"); err != nil {
			return Checkpoint{}, err
		}
	}
	return c, nil
}
