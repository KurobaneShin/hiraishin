package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

func newMCPClient(t *testing.T, h *harness) *sdk.ClientSession {
	t.Helper()
	root := filepath.Join("..", "..")
	bin := filepath.Join(t.TempDir(), "hiraishin")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "./cmd/hiraishin")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hiraishin: %v\n%s", err, out)
	}
	cmd := exec.CommandContext(context.Background(), bin, "mcp")
	cmd.Env = append(os.Environ(),
		"HOME="+h.env.Getenv("HOME"),
		"HIRAISHIN_CONFIG="+h.env.Getenv("HIRAISHIN_CONFIG"),
		"HIRAISHIN_DATA_DIR="+h.env.Getenv("HIRAISHIN_DATA_DIR"),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := sdk.NewClient(&sdk.Implementation{Name: "hiraishin-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect MCP: %v\n%s", err, stderr.String())
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil && !strings.Contains(err.Error(), "closed") {
			t.Errorf("close MCP: %v\n%s", err, stderr.String())
		}
	})
	return session
}

func callTool(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) string {
	t.Helper()
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("%s result = %+v", name, result)
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("%s content type = %T", name, result.Content[0])
	}
	return text.Text
}

func assertSameJSON(t *testing.T, cli, mcp string) {
	t.Helper()
	var cliDoc, mcpDoc any
	if err := json.Unmarshal([]byte(cli), &cliDoc); err != nil {
		t.Fatalf("CLI JSON: %v\n%s", err, cli)
	}
	if err := json.Unmarshal([]byte(mcp), &mcpDoc); err != nil {
		t.Fatalf("MCP JSON: %v\n%s", err, mcp)
	}
	if !reflect.DeepEqual(withoutTimestamps(cliDoc), withoutTimestamps(mcpDoc)) {
		t.Fatalf("CLI and MCP differ\nCLI: %s\nMCP: %s", cli, mcp)
	}
}

func withoutTimestamps(value any) any {
	switch value := value.(type) {
	case map[string]any:
		delete(value, "generated_at")
		delete(value, "created_at")
		for key, child := range value {
			value[key] = withoutTimestamps(child)
		}
	case []any:
		for i, child := range value {
			value[i] = withoutTimestamps(child)
		}
	}
	return value
}

func TestMCPMatchesCLIOverStdio(t *testing.T) {
	h := newHarness(t)
	session := newMCPClient(t, h)

	assertSameJSON(t, h.run(0, "status", "--json", "--no-sensors"), callTool(t, session, "hiraishin_status", map[string]any{"no_sensors": true}))
	cliPick := h.run(0, "run", "--dry-run", "--json", "--no-sensors", "--role", "default")
	validate(t, "why.schema.json", cliPick)
	assertSameJSON(t, cliPick, callTool(t, session, "hiraishin_pick_provider", map[string]any{"role": "default", "no_sensors": true}))

	h.decide("job_mcp_why")
	assertSameJSON(t, h.run(0, "why", "job_mcp_why", "--json"), callTool(t, session, "hiraishin_why", map[string]any{"id": "job_mcp_why"}))
}

func TestMCPDispatchAndControlsUseLedger(t *testing.T) {
	agentBin(t, "kimi", "exit 0")
	h := newHarness(t)
	h.now = h.now.UTC()
	session := newMCPClient(t, h)
	wt, brief := worktree(t)

	raw := callTool(t, session, "hiraishin_dispatch", map[string]any{
		"brief": brief, "worktree": wt, "adapter": "process", "role": "default",
	})
	validate(t, "run.schema.json", raw)
	var dispatched runDoc
	if err := json.Unmarshal([]byte(raw), &dispatched); err != nil {
		t.Fatal(err)
	}
	if dispatched.Job.ID == "" || dispatched.Decision.ChosenProvider != "kimi" || dispatched.Dispatch == nil {
		t.Fatalf("dispatch = %+v", dispatched)
	}
	whyRaw := h.run(0, "why", dispatched.Job.ID, "--json")
	var why whyDoc
	if err := json.Unmarshal([]byte(whyRaw), &why); err != nil {
		t.Fatal(err)
	}
	mcpDecision, err := json.Marshal(dispatched.Decision)
	if err != nil {
		t.Fatal(err)
	}
	cliDecision, err := json.Marshal(why.Decisions[0])
	if err != nil {
		t.Fatal(err)
	}
	assertSameJSON(t, string(cliDecision), string(mcpDecision))

	paused := callTool(t, session, "hiraishin_pause", map[string]any{"provider": "kimi", "reason": "maintenance"})
	validate(t, "control.schema.json", paused)
	assertSameJSON(t, h.run(0, "pause", "kimi", "--reason", "maintenance", "--json"), paused)
	drained := callTool(t, session, "hiraishin_drain", map[string]any{"provider": "agy"})
	validate(t, "control.schema.json", drained)
	assertSameJSON(t, h.run(0, "drain", "agy", "--json"), drained)
	db, err := sql.Open("sqlite", config.ResolvePaths(h.env.Getenv).Ledger())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for provider, kind := range map[string]ledger.Kind{"kimi": ledger.KindPaused, "agy": ledger.KindDraining} {
		var gotKind ledger.Kind
		var source, payload string
		if err := db.QueryRowContext(context.Background(), "SELECT kind, source, payload_json FROM events WHERE provider_id = ? AND kind = ? AND source = 'mcp' ORDER BY seq DESC LIMIT 1", provider, kind).Scan(&gotKind, &source, &payload); err != nil {
			t.Fatal(err)
		}
		if gotKind != kind || source != ledger.SourceMCP || !strings.Contains(payload, `"by":"mcp"`) {
			t.Fatalf("%s event = kind %q source %q payload %s", provider, gotKind, source, payload)
		}
	}

	pinned := callTool(t, session, "hiraishin_pin", map[string]any{"job_id": dispatched.Job.ID, "provider": "kimi"})
	validate(t, "control.schema.json", pinned)
	job, _ := h.job(dispatched.Job.ID)
	if job.PinnedProvider != "kimi" || !job.NeverSwitch {
		t.Fatalf("pin projection = %+v", job)
	}
	e := h.engine()
	events, err := e.Ledger.Reader().JobEvents(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Kind != ledger.KindJobPinned || last.Source != ledger.SourceMCP {
		t.Fatalf("pin event = %+v", last)
	}

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if tool.Name == "hiraishin_forecast" {
			t.Fatal("forecast tool belongs to another front")
		}
	}
	if len(names) != 7 {
		t.Fatalf("tools = %v", names)
	}
}
