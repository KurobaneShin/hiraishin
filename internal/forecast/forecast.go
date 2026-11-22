package forecast

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	Tau         = time.Hour
	MinSpan     = 30 * time.Minute
	ResetJitter = time.Minute
	Lookback    = 6 * Tau
)

type Sample struct {
	At       time.Time
	UsedPct  float64
	ResetsAt time.Time
}

type Forecast struct {
	PctPerHour          float64 `json:"pct_per_hour"`
	TimeToEmptySeconds  *int64  `json:"time_to_empty_seconds"`
	TimeToResetSeconds  *int64  `json:"time_to_reset_seconds"`
	PerishableWasteRisk float64 `json:"perishable_waste_risk"`
	EmptiesBeforeReset  bool    `json:"empties_before_reset"`
}

func BurnRate(samples []Sample) (float64, bool) {
	sorted := slices.SortedStableFunc(slices.Values(samples), func(a, b Sample) int { return a.At.Compare(b.At) })
	var ewma, weight float64
	var span time.Duration
	for i := 1; i < len(sorted); i++ {
		prev, cur := sorted[i-1], sorted[i]
		dt := cur.At.Sub(prev.At)
		if dt <= 0 || cur.UsedPct < prev.UsedPct || !sameReset(cur.ResetsAt, prev.ResetsAt) {
			continue
		}
		alpha := 1 - math.Exp(-dt.Hours()/Tau.Hours())
		ewma = alpha*(cur.UsedPct-prev.UsedPct)/dt.Hours() + (1-alpha)*ewma
		weight = alpha + (1-alpha)*weight
		span += dt
	}
	if span < MinSpan {
		return 0, false
	}
	return ewma / weight, true
}

func sameReset(a, b time.Time) bool {
	d := a.Sub(b)
	return d > -ResetJitter && d < ResetJitter
}

func Project(samples []Sample, resetsAt, now time.Time) (Forecast, bool) {
	if !resetsAt.IsZero() && !resetsAt.After(now) {
		return Forecast{}, false
	}
	rate, ok := BurnRate(samples)
	if !ok {
		return Forecast{}, false
	}
	last := slices.MaxFunc(samples, func(a, b Sample) int { return a.At.Compare(b.At) })
	f := Forecast{PctPerHour: rate}
	remaining := max(0, 100-last.UsedPct-rate*now.Sub(last.At).Hours())
	var toEmpty time.Duration
	if rate > 0 {
		toEmpty = hours(remaining / rate)
		f.TimeToEmptySeconds = seconds(toEmpty)
	}
	if resetsAt.IsZero() {
		return f, true
	}
	toReset := resetsAt.Sub(now)
	f.TimeToResetSeconds = seconds(toReset)
	f.EmptiesBeforeReset = rate > 0 && toEmpty < toReset
	if remaining > 0 {
		f.PerishableWasteRisk = max(0, remaining-rate*toReset.Hours()) / remaining
	}
	return f, true
}

func hours(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }

func seconds(d time.Duration) *int64 {
	s := int64(math.Round(d.Seconds()))
	return &s
}

func NextReset(cron, tz string, after time.Time) (time.Time, error) {
	fields := strings.Fields(cron)
	if len(fields) != 5 || fields[2] != "*" || fields[3] != "*" {
		return time.Time{}, fmt.Errorf("fixed_reset_cron %q: want \"M H * * DOW\" with DOW a weekday 0-6 or *", cron)
	}
	minute, errM := field(fields[0], 59)
	hour, errH := field(fields[1], 23)
	weekday := -1
	var errW error
	if fields[4] != "*" {
		weekday, errW = field(fields[4], 6)
	}
	if err := cmp.Or(errM, errH, errW); err != nil {
		return time.Time{}, fmt.Errorf("fixed_reset_cron %q: %w", cron, err)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("fixed_reset_tz %q: %w", tz, err)
	}
	local := after.In(loc)
	for day := 0; day <= 7; day++ {
		at := wallClock(local.Year(), local.Month(), local.Day()+day, hour, minute, loc)
		if at.After(after) && (weekday < 0 || int(time.Date(local.Year(), local.Month(), local.Day()+day, 12, 0, 0, 0, loc).Weekday()) == weekday) {
			return at, nil
		}
	}
	return time.Time{}, fmt.Errorf("fixed_reset_cron %q: no reset within a week of %s", cron, after)
}

func field(s string, maxValue int) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 || v > maxValue {
		return 0, fmt.Errorf("field %q: want an integer 0-%d", s, maxValue)
	}
	return v, nil
}

func wallClock(year int, month time.Month, day, hour, minute int, loc *time.Location) time.Time {
	at := time.Date(year, month, day, hour, minute, 0, 0, loc)
	skipped := time.Duration(hour-at.Hour())*time.Hour + time.Duration(minute-at.Minute())*time.Minute
	return at.Add(skipped)
}
