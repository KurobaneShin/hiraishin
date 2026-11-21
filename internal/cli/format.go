package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

func duration(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	d = d.Round(time.Minute)
	days, hours, mins := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", max(mins, 1))
	}
}

func percent(p *float64) string {
	if p == nil {
		return "?"
	}
	return fmt.Sprintf("%.0f%%", *p)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
