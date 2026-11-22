package sensor

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	claudeEpoch     = regexp.MustCompile(`usage limit reached\|(\d{9,11})`)
	claudeResets    = regexp.MustCompile(`(?i)resets\s+(?:(mon|tue|wed|thu|fri|sat|sun)\w*\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)(?:\s*\(([^)]+)\))?`)
	codexTryIn      = regexp.MustCompile(`(?i)try again in\s+((?:\d+\s*(?:days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)(?:[\s,]+and\s+|[\s,]+)?)+)`)
	codexDurPart    = regexp.MustCompile(`(?i)(\d+)\s*(days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
	codexTryAtISO   = regexp.MustCompile(`(?i)try again at\s+(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}))`)
	codexTryAtClock = regexp.MustCompile(`(?i)try again at\s+(?:(mon|tue|wed|thu|fri|sat|sun)\w*\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)(?:\s*\(([^)]+)\)|\s+(UTC))?`)
	weekdays        = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}
)

const PacificTZ = "America/Los_Angeles"

func claudeReset(line string, now time.Time) time.Time {
	if m := claudeEpoch.FindStringSubmatch(line); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return time.Unix(sec, 0).UTC()
		}
	}
	m := claudeResets.FindStringSubmatch(line)
	if m == nil {
		return time.Time{}
	}
	return parseClockReset(m[1], m[2], m[3], m[4], m[5], now)
}

func codexReset(line string, now time.Time) time.Time {
	if m := codexTryAtISO.FindStringSubmatch(line); m != nil {
		if at, err := time.Parse(time.RFC3339Nano, m[1]); err == nil {
			return at.UTC()
		}
	}
	if m := codexTryIn.FindStringSubmatch(line); m != nil {
		var d time.Duration
		for _, p := range codexDurPart.FindAllStringSubmatch(m[1], -1) {
			n, _ := strconv.Atoi(p[1])
			switch strings.ToLower(p[2][:1]) {
			case "d":
				d += time.Duration(n) * 24 * time.Hour
			case "h":
				d += time.Duration(n) * time.Hour
			case "m":
				d += time.Duration(n) * time.Minute
			case "s":
				d += time.Duration(n) * time.Second
			}
		}
		if d > 0 {
			return now.Add(d).UTC()
		}
	}
	if m := codexTryAtClock.FindStringSubmatch(line); m != nil {
		tz := m[5]
		if tz == "" {
			tz = m[6]
		}
		return parseClockReset(m[1], m[2], m[3], m[4], tz, now)
	}
	return time.Time{}
}

func parseClockReset(day, hStr, mStr, ampm, tz string, now time.Time) time.Time {
	loc := time.UTC
	if tz = strings.TrimSpace(tz); tz != "" && !strings.EqualFold(tz, "UTC") {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return time.Time{}
		}
		loc = l
	}
	hour, _ := strconv.Atoi(hStr)
	minute, _ := strconv.Atoi(mStr)
	hour %= 12
	if strings.EqualFold(ampm, "pm") {
		hour += 12
	}
	local := now.In(loc)
	t := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if wd, ok := weekdays[strings.ToLower(day)]; ok {
		t = t.AddDate(0, 0, (int(wd)-int(t.Weekday())+7)%7)
		if !t.After(now) {
			t = t.AddDate(0, 0, 7)
		}
		return t.UTC()
	}
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t.UTC()
}

func NextMidnightIn(tz string, now time.Time) (time.Time, bool) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, false
	}
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc).UTC(), true
}

func NextMidnightPacific(now time.Time) time.Time {
	if at, ok := NextMidnightIn(PacificTZ, now); ok {
		return at
	}
	loc := time.FixedZone("PST", -8*3600)
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc).UTC()
}

func PrevMidnightPacific(now time.Time) time.Time {
	loc, err := time.LoadLocation(PacificTZ)
	if err != nil {
		loc = time.FixedZone("PST", -8*3600)
	}
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).UTC()
}

func geminiReset(_ string, now time.Time) time.Time {
	return NextMidnightPacific(now)
}
