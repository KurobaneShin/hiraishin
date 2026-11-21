package sensor

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	claudeEpoch  = regexp.MustCompile(`usage limit reached\|(\d{9,11})`)
	claudeResets = regexp.MustCompile(`(?i)resets\s+(?:(mon|tue|wed|thu|fri|sat|sun)\w*\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)(?:\s*\(([^)]+)\))?`)
	weekdays     = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}
)

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
	loc := time.UTC
	if tz := strings.TrimSpace(m[5]); tz != "" && !strings.EqualFold(tz, "UTC") {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return time.Time{}
		}
		loc = l
	}
	hour, _ := strconv.Atoi(m[2])
	minute, _ := strconv.Atoi(m[3])
	hour %= 12
	if strings.EqualFold(m[4], "pm") {
		hour += 12
	}
	local := now.In(loc)
	t := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if wd, ok := weekdays[strings.ToLower(m[1])]; ok {
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
