package sim

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

func TestAnonymizedFixtureComparison(t *testing.T) {
	dir := filepath.Join("testdata", "2026-11-21")
	trace, err := loadTrace(context.Background(), filepath.Join(dir, "afternoon.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.jobs) != 12 {
		t.Fatalf("jobs = %d, want 12", len(trace.jobs))
	}
	providers := map[string]bool{}
	stalls := 0
	for _, e := range trace.events {
		if e.kind == ledger.KindProviderConfigured {
			providers[e.providerID] = true
		}
		if e.kind == ledger.KindFailoverStarted {
			stalls++
		}
	}
	if len(providers) != 4 || stalls != 3 {
		t.Fatalf("providers = %d, stalls = %d, want 4 and 3", len(providers), stalls)
	}
	policyNames := []string{"quality-first", "burn-expiring-first", "save-scarce", "balanced"}
	policies := make([]Policy, 0, len(policyNames))
	for _, name := range policyNames {
		cfg, err := config.Load(filepath.Join(dir, name+".toml"))
		if err != nil {
			t.Fatal(err)
		}
		policies = append(policies, Policy{Name: name, Config: cfg})
	}
	rows, err := Replay(context.Background(), filepath.Join(dir, "afternoon.db"), policies)
	if err != nil {
		t.Fatal(err)
	}
	var table bytes.Buffer
	if err := Render(&table, "markdown", rows); err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "simulator-2026-11-21.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), table.String()) {
		t.Fatalf("published table is stale:\n%s", table.String())
	}
}
