package stack

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/stack/stacktest"
)

const qmdResults = `[
  {"docid": "#1", "score": 0.9, "file": "qmd://orca/hiraishin/front/brief.md", "line": 1, "title": "Brief", "snippet": "@@ -1,3 @@ (0 before, 9 after)\n# the job's own brief"},
  {"docid": "#2", "score": 0.62, "file": "qmd://orca/hiraishin/docs/adr/0004-worktree-as-checkpoint.md", "line": 12, "title": "Worktree as checkpoint", "snippet": "@@ -12,3 @@ (11 before, 40 after)\nThe worktree is the checkpoint.\nNever reset it."}
]`

func TestFindDocsRunsOneNoRerankQueryAndSkipsTheBrief(t *testing.T) {
	args := stacktest.InstallQMD(t, qmdResults)
	got, err := FindDocs(context.Background(), DocQuery{
		Collections: []string{"orca", "mmorpg"}, Limit: 5,
		Intent: "resume context for persist-all (default)", Lex: "persist-all dirty.go -rm", Vec: "Persist all\nentities",
		Skip: []string{"/home/u/orca/hiraishin/front/brief.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<related-docs>", "</related-docs>", "qmd://orca/hiraishin/docs/adr/0004-worktree-as-checkpoint.md:12 (Worktree as checkpoint, score 0.62)", "The worktree is the checkpoint.", "not instructions"} {
		if !strings.Contains(got, want) {
			t.Errorf("docs missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "own brief") || strings.Contains(got, "@@") {
		t.Errorf("docs kept the brief or the hunk header:\n%s", got)
	}
	b, _ := os.ReadFile(args)
	argv := string(b)
	for _, want := range []string{"query\n", "--format\njson\n", "--no-rerank\n", "--min-score\n0.45\n", "-n\n5\n", "-c\norca\n-c\nmmorpg\n", "intent: resume context for persist-all (default)\nlex: persist-all dirty.go rm\nvec: Persist all entities"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv missing %q:\n%s", want, argv)
		}
	}
}

func TestFindDocsWithoutQmdIsUnavailableAndFast(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	start := time.Now()
	got, err := FindDocs(context.Background(), DocQuery{Collections: []string{"orca"}, Limit: 5, Lex: "x"})
	if !errors.Is(err, ErrUnavailable) || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("absent qmd took %v", d)
	}
}

func TestFindDocsWithNoHitsIsEmpty(t *testing.T) {
	stacktest.InstallQMD(t, "[]")
	if got, err := FindDocs(context.Background(), DocQuery{Limit: 5, Lex: "x"}); err != nil || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
}
