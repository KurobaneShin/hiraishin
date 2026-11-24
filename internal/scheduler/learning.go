package scheduler

import (
	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

const (
	minDurationRatio = 0.2
	deltaGain        = 2
)

type Outcome struct {
	Success       bool
	ReviewRounds  int
	BuildFailures int
	DurationMin   float64
}

func OutcomeQuality(c config.Learning, o Outcome, roleDurationMin float64) float64 {
	success := 0.0
	if o.Success {
		success = 1
	}
	speed := 1.0
	if o.DurationMin > 0 && roleDurationMin > 0 {
		speed = clamp(roleDurationMin/o.DurationMin, minDurationRatio, 1)
	}
	return c.WeightCompletion*success +
		c.WeightReviewRounds/float64(1+max(o.ReviewRounds, 0)) +
		c.WeightBuildFailures/float64(1+max(o.BuildFailures, 0)) +
		c.WeightDuration*speed
}

func RoleDurationMin(rows []ledger.LearningRow) float64 {
	sum, n := 0.0, 0
	for _, r := range rows {
		if r.AvgDurationMin > 0 {
			sum += r.AvgDurationMin
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func LearnDeltas(c config.Learning, rows []ledger.LearningRow) map[string]float64 {
	out := map[string]float64{}
	if !c.Enabled || c.MaxDelta <= 0 {
		return out
	}
	sum, n := 0.0, 0
	for _, r := range rows {
		if r.Samples > 0 {
			sum += r.EWMAQuality
			n++
		}
	}
	if n == 0 {
		return out
	}
	baseline := sum / float64(n)
	for _, r := range rows {
		if r.Samples >= c.MinSamples {
			out[r.ProviderID] = clamp((r.EWMAQuality-baseline)*c.MaxDelta*deltaGain, -c.MaxDelta, c.MaxDelta)
		}
	}
	return out
}
