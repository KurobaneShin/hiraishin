package dispatch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"text/template"
)

const reportTailBytes = 16 << 10

type PromptData struct {
	JobID        string
	JobName      string
	Worktree     string
	BriefPath    string
	ReportPath   string
	FromProvider string
	SessionID    string
	Reason       string
	Checkpoint   Checkpoint
	Report       string
	Enrichment   string
}

var initialPrompt = template.Must(template.New("initial").Parse(`You are working on job {{.JobName}} ({{.JobID}}) in the git worktree {{.Worktree}}.
Read the brief at {{.BriefPath}} and follow it.
Keep {{.ReportPath}} current with progress, decisions and open questions.
{{- with .Enrichment}}

{{.}}{{end}}
`))

var resumePrompt = template.Must(template.New("resume").Parse(`You are resuming job {{.JobName}} ({{.JobID}}) in the git worktree {{.Worktree}}.
The previous agent ({{.FromProvider}}) stopped: {{.Reason}}.
Its work is preserved in this worktree. Do not reset, stash or discard any change.
{{- if .SessionID}}
Its session id was {{.SessionID}}. If the ai-memory MCP is available, read what it did with memory_read_session_observations (session_id {{.SessionID}}, order desc) before exploring the code again.
{{- end}}

Brief: {{.BriefPath}}
Report: {{.ReportPath}}
HEAD: {{or .Checkpoint.Head "(no commits)"}}

## git status --porcelain
{{or .Checkpoint.Status "(clean)"}}

## git diff --stat HEAD
{{or .Checkpoint.DiffStat "(no diff)"}}

## Report so far
{{or .Report "(no report yet)"}}
{{with .Enrichment}}
{{.}}{{end}}
Inspect the diff, continue the brief from this state, and keep the report current.
`))

var continuePrompt = template.Must(template.New("continue").Parse(`Your quota has reset; you were stopped mid-task on job {{.JobName}} ({{.JobID}}): {{.Reason}}.
Continue the brief at {{.BriefPath}} from where you stopped. The worktree {{.Worktree}} is as you left it.
Keep {{.ReportPath}} current.
`))

func ContinuePrompt(d PromptData) (string, error) { return render(continuePrompt, d) }

func render(t *template.Template, d PromptData) (string, error) {
	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		return "", fmt.Errorf("render %s prompt: %w", t.Name(), err)
	}
	return b.String(), nil
}

func InitialPrompt(d PromptData) (string, error) { return render(initialPrompt, d) }

func ResumePrompt(d PromptData, templatePath string) (string, error) {
	report, err := ReadReport(d.ReportPath)
	if err != nil {
		return "", err
	}
	d.Report = report
	t := resumePrompt
	if templatePath != "" {
		src, err := os.ReadFile(templatePath)
		if err != nil {
			return "", fmt.Errorf("resume_prompt_template: %w", err)
		}
		if t, err = template.New("custom resume").Parse(string(src)); err != nil {
			return "", fmt.Errorf("resume_prompt_template: %w", err)
		}
	}
	return render(t, d)
}

func ReadReport(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read report: %w", err)
	}
	if len(b) > reportTailBytes {
		b = append([]byte("[...]\n"), b[len(b)-reportTailBytes:]...)
	}
	return string(b), nil
}
