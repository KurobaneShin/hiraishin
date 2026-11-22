package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/KurobaneShin/hiraishin/internal/scheduler"
)

const learningTOML = `
[learning]
enabled = true
min_samples = 2
max_delta = 0.1
ewma_alpha = 0.5
`

func newLearningHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	if err := os.WriteFile(h.env.Getenv("HIRAISHIN_CONFIG"), []byte(testConfig+learningTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	return h
}

func candidateOf(t *testing.T, d scheduler.Decision, id string) scheduler.Candidate {
	t.Helper()
	for _, c := range d.Candidates {
		if c.Provider == id {
			return c
		}
	}
	t.Fatalf("no candidate %s in %+v", id, d.Candidates)
	return scheduler.Candidate{}
}

func (h *harness) teachKimiOverAgy() {
	for range 2 {
		h.run(0, "outcome", "record", "kimi", "gameplay", "--duration-min", "30")
		h.run(0, "outcome", "record", "agy", "gameplay", "--failed", "--review-rounds", "3", "--build-failures", "2", "--duration-min", "90")
	}
}

func TestOutcomesShiftScoresWithinMaxDelta(t *testing.T) {
	h := newLearningHarness(t)
	h.run(0, "outcome", "record", "kimi", "gameplay", "--duration-min", "30")
	if c := candidateOf(t, h.decide("job_cold"), "kimi"); c.LearnDelta != 0 {
		t.Fatalf("learned %v before min_samples", c.LearnDelta)
	}
	h.teachKimiOverAgy()
	d := h.decide("job_a")
	k, a := candidateOf(t, d, "kimi"), candidateOf(t, d, "agy")
	if k.LearnDelta <= 0 || a.LearnDelta >= 0 || k.LearnDelta > 0.1 || a.LearnDelta < -0.1 {
		t.Fatalf("learn deltas kimi=%v agy=%v, want kimi > 0 > agy within ±0.1", k.LearnDelta, a.LearnDelta)
	}
	if out := h.run(0, "why", "job_a"); !strings.Contains(out, "learned +0.") {
		t.Fatalf("why misses the learned bonus:\n%s", out)
	}
}

func TestLearnFreezeStopsScoresChangingImmediately(t *testing.T) {
	h := newLearningHarness(t)
	h.teachKimiOverAgy()
	before := h.decide("job_a")
	if out := h.run(0, "learn", "freeze"); !strings.Contains(out, "frozen") {
		t.Fatalf("freeze output = %q", out)
	}
	for range 4 {
		h.run(0, "outcome", "record", "agy", "gameplay", "--duration-min", "5")
		h.run(0, "outcome", "record", "kimi", "gameplay", "--failed", "--review-rounds", "9")
	}
	after := h.decide("job_b")
	for i, c := range after.Candidates {
		b := before.Candidates[i]
		if c.Provider != b.Provider || c.Score != b.Score || c.LearnDelta != b.LearnDelta {
			t.Fatalf("score moved while frozen: %s %v/%v -> %s %v/%v", b.Provider, b.Score, b.LearnDelta, c.Provider, c.Score, c.LearnDelta)
		}
	}
	h.run(0, "learn", "unfreeze")
	h.run(0, "outcome", "record", "agy", "gameplay", "--duration-min", "5")
	if thawed := h.decide("job_c"); candidateOf(t, thawed, "agy").LearnDelta == candidateOf(t, before, "agy").LearnDelta {
		t.Fatal("outcome after unfreeze did not move agy's delta")
	}
}

func TestConfigFreezeAlsoStopsLearning(t *testing.T) {
	h := newLearningHarness(t)
	h.teachKimiOverAgy()
	before := candidateOf(t, h.decide("job_a"), "kimi").LearnDelta
	if err := os.WriteFile(h.env.Getenv("HIRAISHIN_CONFIG"), []byte(testConfig+learningTOML+"frozen = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.run(0, "outcome", "record", "kimi", "gameplay", "--failed", "--review-rounds", "9")
	if after := candidateOf(t, h.decide("job_b"), "kimi").LearnDelta; after != before {
		t.Fatalf("delta moved under [learning] frozen = true: %v -> %v", before, after)
	}
}

func TestLearnStatusAndReset(t *testing.T) {
	h := newLearningHarness(t)
	h.teachKimiOverAgy()
	out := h.run(0, "learn", "status")
	for _, want := range []string{"enabled", "min_samples 2", "max_delta ±0.100", "gameplay", "kimi", "agy", "+0.0", "-0.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
	h.run(0, "learn", "freeze")
	if out := h.run(0, "learn", "status"); !strings.Contains(out, "frozen") {
		t.Fatalf("status hides the freeze:\n%s", out)
	}
	h.run(0, "learn", "reset")
	if c := candidateOf(t, h.decide("job_a"), "kimi"); c.LearnDelta != 0 {
		t.Fatalf("delta %v after reset", c.LearnDelta)
	}
	if out := h.run(0, "learn", "status"); !strings.Contains(out, "No outcomes recorded") {
		t.Fatalf("status after reset:\n%s", out)
	}
}

func TestOutcomeRecordRejectsBadInput(t *testing.T) {
	h := newLearningHarness(t)
	h.run(ExitUsage, "outcome", "record", "nope", "gameplay")
	h.run(ExitUsage, "outcome", "record", "kimi", "gameplay", "--review-rounds", "-1")
	h.run(ExitUsage, "outcome", "record", "kimi")
}
