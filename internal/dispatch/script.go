package dispatch

import (
	"path/filepath"
	"strings"
)

const (
	PromptFile = "prompt.md"
	ScriptFile = "run.sh"
	OutputFile = "output.log"
	ExitFile   = "exit"
	PgidFile   = "pgid"
)

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func arg(s string) string {
	switch {
	case s == promptArg:
		return `"$prompt"`
	case strings.HasPrefix(s, "~/"):
		return `"$HOME"/` + quote(s[2:])
	default:
		return quote(s)
	}
}

func Script(c Command, worktree, dir string) string {
	in := func(name string) string { return quote(filepath.Join(dir, name)) }
	lines := []string{"#!/usr/bin/env bash", "set -u", "cd " + quote(worktree) + " || exit 97", "ps -o pgid= -p $$ | tr -d ' ' > " + in(PgidFile+".tmp") + " && mv " + in(PgidFile+".tmp") + " " + in(PgidFile)}
	for _, kv := range c.Env {
		lines = append(lines, "export "+kv[0]+"="+quote(kv[1]))
	}
	for _, kv := range c.EnvRef {
		lines = append(lines, "export "+kv[0]+"=\"${"+kv[1]+":-}\"")
	}
	argv := make([]string, len(c.Argv))
	for i, a := range c.Argv {
		argv[i] = arg(a)
	}
	lines = append(lines,
		"prompt=\"$(cat "+in(PromptFile)+")\"",
		strings.Join(argv, " ")+" < /dev/null 2>&1 | tee -a "+in(OutputFile),
		"code=${PIPESTATUS[0]}",
		"printf '%s\\n' \"$code\" > "+in(ExitFile+".tmp")+" && mv "+in(ExitFile+".tmp")+" "+in(ExitFile),
		"exit \"$code\"",
	)
	return strings.Join(lines, "\n") + "\n"
}
