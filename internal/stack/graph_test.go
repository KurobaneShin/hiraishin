package stack

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/KurobaneShin/hiraishin/internal/stack/stacktest"
)

var graphFixture = func() string {
	nodes := []string{
		`{"id": "engine_decide", "label": "Decide", "community": 0, "community_name": "Engine", "source_file": "internal/engine/engine.go"}`,
		`{"id": "engine_launch", "label": "Launch", "community": 0, "community_name": "Engine", "source_file": "internal/engine/launch.go"}`,
		`{"id": "sched_select", "label": "Select", "community": 1, "community_name": "Scheduler", "source_file": "internal/scheduler/scheduler.go"}`,
		`{"id": "sched_evaluate", "label": "evaluate", "community": 1, "community_name": "Scheduler", "source_file": "internal/scheduler/scheduler.go"}`,
		`{"id": "ledger_append", "label": "Append", "community": 2, "community_name": "Ledger", "source_file": "internal/ledger/ledger.go"}`,
		`{"id": "cli_run", "label": "runCmd", "community": 3, "community_name": "CLI", "source_file": "internal/cli/run.go"}`,
		`{"id": "docs_readme", "label": "README", "community": null, "source_file": "README.md"}`,
	}
	links := []string{
		`{"source": "engine_decide", "target": "sched_select", "relation": "calls"}`,
		`{"source": "sched_select", "target": "sched_evaluate", "relation": "calls"}`,
		`{"source": "engine_launch", "target": "ledger_append", "relation": "calls"}`,
		`{"source": "cli_run", "target": "engine_decide", "relation": "calls"}`,
		`{"source": "cli_run", "target": "engine_launch", "relation": "calls"}`,
	}
	for h := range 10 {
		nodes = append(nodes, fmt.Sprintf(`{"id": "hub%d", "label": "Hub%d", "community": 4, "community_name": "Hubs", "source_file": "internal/hub/hub.go"}`, h, h))
		for l := range 3 {
			nodes = append(nodes, fmt.Sprintf(`{"id": "leaf%d_%d", "label": "Leaf%d_%d", "community": 4, "community_name": "Hubs", "source_file": "internal/hub/leaf.go"}`, h, l, h, l))
			links = append(links, fmt.Sprintf(`{"source": "hub%d", "target": "leaf%d_%d", "relation": "calls"}`, h, h, l))
		}
	}
	for l := range 4 {
		links = append(links, fmt.Sprintf(`{"source": "leaf%d_0", "target": "engine_decide", "relation": "calls"}`, l))
	}
	return `{"directed": false, "multigraph": false, "graph": {}, "nodes": [` + strings.Join(nodes, ",") + `], "links": [` + strings.Join(links, ",") + `]}`
}()

func writeGraph(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	stacktest.WriteGraph(t, root, body)
	return root
}

func TestLoadGraphAbsentIsUnavailable(t *testing.T) {
	if _, err := LoadGraph(t.TempDir()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestGraphSizeRaisesTierOnlyForWideOrCoreChanges(t *testing.T) {
	g, err := LoadGraph(writeGraph(t, graphFixture))
	if err != nil {
		t.Fatal(err)
	}
	local := g.Size([]string{"internal/ledger/ledger.go", "missing.go"})
	if local.TouchedFiles != 1 || local.Communities != 1 || local.TierFloor() != 0 {
		t.Errorf("local change = %+v floor %d", local, local.TierFloor())
	}
	wide := g.Size([]string{"internal/engine/launch.go", "internal/ledger/ledger.go", "internal/scheduler/scheduler.go"})
	if wide.Communities != 3 || wide.TierFloor() != 4 {
		t.Errorf("three communities = %+v floor %d", wide, wide.TierFloor())
	}
	if !strings.Contains(wide.Reason(), "3 communities") {
		t.Errorf("reason = %q", wide.Reason())
	}
	core := g.Size([]string{"internal/engine/engine.go"})
	if !slices.Contains(core.GodNodes, "Decide") || core.TierFloor() != 4 {
		t.Errorf("god node change = %+v floor %d", core, core.TierFloor())
	}
	if blast := (Size{GodNodes: []string{"X"}, BlastEdges: 40}); blast.TierFloor() != 5 {
		t.Errorf("40 blast edges floor = %d, want 5", blast.TierFloor())
	}
}

func TestGraphMentionedFindsSourceFilesNamedInABrief(t *testing.T) {
	g, err := LoadGraph(writeGraph(t, graphFixture))
	if err != nil {
		t.Fatal(err)
	}
	got := g.Mentioned("Change internal/cli/run.go and README.md, not internal/foo.go")
	if !slices.Equal(got, []string{"README.md", "internal/cli/run.go"}) {
		t.Errorf("mentioned = %q", got)
	}
}

func TestSubgraphWalksTwoHopsWithinTheTokenBudget(t *testing.T) {
	g, err := LoadGraph(writeGraph(t, graphFixture))
	if err != nil {
		t.Fatal(err)
	}
	got := g.Subgraph([]string{"internal/ledger/ledger.go"}, 1500)
	for _, want := range []string{"<codebase-subgraph>", "</codebase-subgraph>", "Launch (internal/engine/launch.go) calls Append (internal/ledger/ledger.go)", "runCmd (internal/cli/run.go) calls Launch (internal/engine/launch.go)", "Ledger"} {
		if !strings.Contains(got, want) {
			t.Errorf("subgraph missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Select") {
		t.Errorf("subgraph went past 2 hops:\n%s", got)
	}
	if g.Subgraph([]string{"nothing.go"}, 1500) != "" {
		t.Error("subgraph for unknown files is not empty")
	}

	var nodes, links []string
	for i := range 400 {
		nodes = append(nodes, fmt.Sprintf(`{"id": "n%d", "label": "Node%d", "community": 0, "source_file": "f%d.go"}`, i, i, i))
		links = append(links, fmt.Sprintf(`{"source": "n0", "target": "n%d", "relation": "calls"}`, i+1))
	}
	big, err := LoadGraph(writeGraph(t, `{"nodes": [`+strings.Join(nodes, ",")+`], "links": [`+strings.Join(links[:399], ",")+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	capped := big.Subgraph([]string{"f0.go"}, 1500)
	if len(capped) > 1500*charsPerToken || !strings.HasSuffix(capped, "</codebase-subgraph>\n") {
		t.Errorf("capped subgraph is %d chars, budget %d", len(capped), 1500*charsPerToken)
	}
}
