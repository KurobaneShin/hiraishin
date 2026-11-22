package forecast

import (
	"fmt"
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)

func linear(start time.Time, used, perHour float64, every, span time.Duration, resets time.Time) []Sample {
	var out []Sample
	for d := time.Duration(0); d <= span; d += every {
		out = append(out, Sample{At: start.Add(d), UsedPct: used + perHour*d.Hours(), ResetsAt: resets})
	}
	return out
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func secs(d time.Duration) int64 { return int64(d.Seconds()) }

func TestBurnRateIsExactForConstantTraces(t *testing.T) {
	for _, rate := range []float64{0.5, 2, 5, 10, 20, 33.3} {
		for _, every := range []time.Duration{2 * time.Minute, 5 * time.Minute, 15 * time.Minute} {
			t.Run(fmt.Sprintf("%v_per_hour_every_%v", rate, every), func(t *testing.T) {
				got, ok := BurnRate(linear(t0, 1, rate, every, 2*time.Hour, t0.Add(5*time.Hour)))
				if !ok || !near(got, rate, 1e-9) {
					t.Fatalf("BurnRate = %v ok=%v, want %v", got, ok, rate)
				}
			})
		}
	}
}

func TestBurnRateFollowsAStepWithTheEWMAWeights(t *testing.T) {
	before := linear(t0, 0, 5, 5*time.Minute, 6*time.Hour, time.Time{})
	last := before[len(before)-1]
	after := linear(last.At, last.UsedPct, 20, 5*time.Minute, 3*time.Hour, time.Time{})[1:]
	got, ok := BurnRate(append(before, after...))
	oldWeight := math.Exp(-3) * (1 - math.Exp(-6))
	newWeight := 1 - math.Exp(-3)
	want := (20*newWeight + 5*oldWeight) / (newWeight + oldWeight)
	if !ok || !near(got, want, 1e-9) {
		t.Fatalf("BurnRate = %v ok=%v, want %v", got, ok, want)
	}
}

func TestBurnRateSkipsResetBoundaries(t *testing.T) {
	first := linear(t0, 40, 10, 10*time.Minute, 4*time.Hour, t0.Add(4*time.Hour+5*time.Minute))
	second := linear(t0.Add(4*time.Hour+10*time.Minute), 0, 10, 10*time.Minute, time.Hour, t0.Add(9*time.Hour+5*time.Minute))
	got, ok := BurnRate(append(first, second...))
	if !ok || !near(got, 10, 1e-9) {
		t.Fatalf("BurnRate across a drop = %v ok=%v, want 10", got, ok)
	}
	moved := linear(t0, 10, 10, 10*time.Minute, time.Hour, t0.Add(5*time.Hour))
	moved = append(moved, linear(t0.Add(70*time.Minute), 80, 10, 10*time.Minute, time.Hour, t0.Add(10*time.Hour))...)
	got, ok = BurnRate(moved)
	if !ok || !near(got, 10, 1e-9) {
		t.Fatalf("BurnRate across a moved resets_at = %v ok=%v, want 10", got, ok)
	}
}

func TestBurnRateToleratesSubSecondResetJitter(t *testing.T) {
	samples := linear(t0, 20, 6, 2*time.Minute, time.Hour, t0.Add(3*time.Hour))
	for i := range samples {
		samples[i].ResetsAt = samples[i].ResetsAt.Add(time.Duration(i%7) * 9 * time.Millisecond)
	}
	got, ok := BurnRate(samples)
	if !ok || !near(got, 6, 1e-9) {
		t.Fatalf("BurnRate with resets_at jittering by milliseconds = %v ok=%v, want 6", got, ok)
	}
}

func TestBurnRateSortsAndIgnoresDuplicateTimestamps(t *testing.T) {
	s := linear(t0, 0, 6, 10*time.Minute, time.Hour, time.Time{})
	s = append(s, s[2], s[4])
	s[0], s[len(s)-3] = s[len(s)-3], s[0]
	got, ok := BurnRate(s)
	if !ok || !near(got, 6, 1e-9) {
		t.Fatalf("BurnRate = %v ok=%v, want 6", got, ok)
	}
}

func TestBurnRateNeedsMinSpan(t *testing.T) {
	for name, s := range map[string][]Sample{
		"empty":         nil,
		"one sample":    linear(t0, 10, 5, time.Minute, 0, time.Time{}),
		"short trace":   linear(t0, 10, 30, 2*time.Minute, 10*time.Minute, time.Time{}),
		"only a reset":  {{At: t0, UsedPct: 90}, {At: t0.Add(time.Hour), UsedPct: 2}},
		"same instants": {{At: t0, UsedPct: 1}, {At: t0, UsedPct: 2}},
	} {
		if got, ok := BurnRate(s); ok {
			t.Errorf("%s: BurnRate = %v, want not ok", name, got)
		}
	}
}

func TestBurnRateOnAQuantizedTrace(t *testing.T) {
	var s []Sample
	for d := time.Duration(0); d <= 4*time.Hour; d += 2 * time.Minute {
		s = append(s, Sample{At: t0.Add(d), UsedPct: math.Floor(3 + 7.3*d.Hours())})
	}
	got, ok := BurnRate(s)
	if !ok || !near(got, 7.3, 0.73) {
		t.Fatalf("BurnRate on integer samples = %v ok=%v, want 7.3 +-10%%", got, ok)
	}
}

func TestBurnRateOfAFlatTraceIsZero(t *testing.T) {
	got, ok := BurnRate(linear(t0, 42, 0, 5*time.Minute, time.Hour, time.Time{}))
	if !ok || got != 0 {
		t.Fatalf("BurnRate = %v ok=%v, want 0", got, ok)
	}
}

func TestProject(t *testing.T) {
	type want struct {
		rate     float64
		empty    *time.Duration
		reset    *time.Duration
		waste    float64
		runsOut  bool
		measured bool
	}
	dur := func(d time.Duration) *time.Duration { return &d }
	cases := []struct {
		name    string
		samples []Sample
		resets  time.Time
		now     time.Time
		want    want
	}{
		{"enough until reset", linear(t0, 30, 10, 10*time.Minute, 2*time.Hour, time.Time{}), t0.Add(5 * time.Hour), t0.Add(2 * time.Hour),
			want{rate: 10, empty: dur(5 * time.Hour), reset: dur(3 * time.Hour), waste: 0.4, measured: true}},
		{"empties before reset", linear(t0, 0, 25, 10*time.Minute, 2*time.Hour, time.Time{}), t0.Add(5 * time.Hour), t0.Add(2 * time.Hour),
			want{rate: 25, empty: dur(2 * time.Hour), reset: dur(3 * time.Hour), runsOut: true, measured: true}},
		{"projects from the last sample", linear(t0, 30, 10, 10*time.Minute, 2*time.Hour, time.Time{}), t0.Add(5 * time.Hour), t0.Add(2*time.Hour + 30*time.Minute),
			want{rate: 10, empty: dur(4*time.Hour + 30*time.Minute), reset: dur(2*time.Hour + 30*time.Minute), waste: 20.0 / 45, measured: true}},
		{"flat wastes everything", linear(t0, 40, 0, 10*time.Minute, time.Hour, time.Time{}), t0.Add(3 * time.Hour), t0.Add(time.Hour),
			want{reset: dur(2 * time.Hour), waste: 1, measured: true}},
		{"full window", linear(t0, 80, 10, 10*time.Minute, 2*time.Hour, time.Time{}), t0.Add(5 * time.Hour), t0.Add(2 * time.Hour),
			want{rate: 10, empty: dur(0), reset: dur(3 * time.Hour), runsOut: true, measured: true}},
		{"unknown reset", linear(t0, 30, 10, 10*time.Minute, 2*time.Hour, time.Time{}), time.Time{}, t0.Add(2 * time.Hour),
			want{rate: 10, empty: dur(5 * time.Hour), measured: true}},
		{"reset already passed", linear(t0, 30, 10, 10*time.Minute, 2*time.Hour, time.Time{}), t0.Add(time.Hour), t0.Add(2 * time.Hour),
			want{}},
		{"too short", linear(t0, 30, 10, 10*time.Minute, 20*time.Minute, time.Time{}), t0.Add(5 * time.Hour), t0.Add(20 * time.Minute),
			want{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, ok := Project(c.samples, c.resets, c.now)
			if ok != c.want.measured {
				t.Fatalf("ok = %v, want %v (%+v)", ok, c.want.measured, f)
			}
			if !ok {
				return
			}
			if !near(f.PctPerHour, c.want.rate, 1e-9) || !near(f.PerishableWasteRisk, c.want.waste, 1e-9) || f.EmptiesBeforeReset != c.want.runsOut {
				t.Errorf("forecast = %+v, want %+v", f, c.want)
			}
			checkSeconds(t, "time_to_empty", f.TimeToEmptySeconds, c.want.empty)
			checkSeconds(t, "time_to_reset", f.TimeToResetSeconds, c.want.reset)
		})
	}
}

func checkSeconds(t *testing.T, name string, got *int64, want *time.Duration) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %d, want unknown", name, *got)
	case want != nil && (got == nil || *got != secs(*want)):
		t.Errorf("%s = %v, want %d", name, got, secs(*want))
	}
}

func TestNextReset(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	pt := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, la) }
	utc := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, time.UTC) }
	cases := []struct {
		name  string
		cron  string
		tz    string
		after time.Time
		want  time.Time
	}{
		{"midnight PST", "0 0 * * *", "America/Los_Angeles", utc(2026, 1, 15, 12, 0), utc(2026, 1, 16, 8, 0)},
		{"midnight PDT", "0 0 * * *", "America/Los_Angeles", utc(2026, 7, 15, 12, 0), utc(2026, 7, 16, 7, 0)},
		{"night before spring forward", "0 0 * * *", "America/Los_Angeles", pt(2026, 3, 7, 23, 30), utc(2026, 3, 8, 8, 0)},
		{"spring forward day is 23h", "0 0 * * *", "America/Los_Angeles", utc(2026, 3, 8, 8, 0), utc(2026, 3, 9, 7, 0)},
		{"inside the spring gap", "0 0 * * *", "America/Los_Angeles", utc(2026, 3, 8, 10, 30), utc(2026, 3, 9, 7, 0)},
		{"night before fall back", "0 0 * * *", "America/Los_Angeles", utc(2026, 10, 31, 12, 0), utc(2026, 11, 1, 7, 0)},
		{"fall back day is 25h", "0 0 * * *", "America/Los_Angeles", utc(2026, 11, 1, 7, 0), utc(2026, 11, 2, 8, 0)},
		{"inside the repeated hour", "0 0 * * *", "America/Los_Angeles", utc(2026, 11, 1, 9, 30), utc(2026, 11, 2, 8, 0)},
		{"weekly monday midnight across fall back", "0 0 * * 1", "America/Los_Angeles", utc(2026, 10, 28, 12, 0), utc(2026, 11, 2, 8, 0)},
		{"weekly sunday same day later", "30 9 * * 0", "America/Los_Angeles", pt(2026, 11, 22, 8, 0), pt(2026, 11, 22, 9, 30)},
		{"time skipped by spring forward", "30 2 * * *", "America/Los_Angeles", pt(2026, 3, 7, 12, 0), utc(2026, 3, 8, 10, 30)},
		{"utc default", "0 0 * * *", "", utc(2026, 11, 21, 15, 0), utc(2026, 11, 22, 0, 0)},
		{"exactly on the reset moves to the next", "15 6 * * *", "UTC", utc(2026, 11, 21, 6, 15), utc(2026, 11, 22, 6, 15)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NextReset(c.cron, c.tz, c.after)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(c.want) {
				t.Fatalf("NextReset(%q, %q, %v) = %v, want %v", c.cron, c.tz, c.after.UTC(), got.UTC(), c.want.UTC())
			}
		})
	}
}

func TestNextResetRejectsUnsupportedSpecs(t *testing.T) {
	for _, c := range [][2]string{
		{"0 0 1 * *", "UTC"},
		{"*/5 * * * *", "UTC"},
		{"0 24 * * *", "UTC"},
		{"60 0 * * *", "UTC"},
		{"0 0 * * 7", "UTC"},
		{"0 0 * *", "UTC"},
		{"0 0 * * *", "Mars/Olympus"},
	} {
		if got, err := NextReset(c[0], c[1], t0); err == nil {
			t.Errorf("NextReset(%q, %q) = %v, want error", c[0], c[1], got)
		}
	}
}
