package sim

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func Render(w io.Writer, format string, rows []Metrics) error {
	switch format {
	case "markdown":
		return renderMarkdown(w, rows)
	case "csv":
		return renderCSV(w, rows)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	default:
		return fmt.Errorf("unknown output format %q: want markdown, csv or json", format)
	}
}

func renderMarkdown(w io.Writer, rows []Metrics) error {
	if _, err := fmt.Fprintln(w, "| Policy | Perishable quota used (%) | Expired wasted quota (%) | Total stall minutes | Failover count | Scarce-pool reserve left (%) |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|"); err != nil {
		return err
	}
	for _, m := range rows {
		policy := strings.ReplaceAll(m.Policy, "|", "\\|")
		if _, err := fmt.Fprintf(w, "| %s | %.2f | %.2f | %.2f | %d | %.2f |\n", policy, m.PerishableQuotaUsedPct, m.ExpiredWastedQuotaPct, m.TotalStallMinutes, m.FailoverCount, m.ScarcePoolReserveLeftPct); err != nil {
			return err
		}
	}
	return nil
}

func renderCSV(w io.Writer, rows []Metrics) error {
	out := csv.NewWriter(w)
	if err := out.Write([]string{"policy", "perishable_quota_used_pct", "expired_wasted_quota_pct", "total_stall_minutes", "failover_count", "scarce_pool_reserve_left_pct"}); err != nil {
		return err
	}
	for _, m := range rows {
		record := []string{
			m.Policy,
			strconv.FormatFloat(m.PerishableQuotaUsedPct, 'f', 2, 64),
			strconv.FormatFloat(m.ExpiredWastedQuotaPct, 'f', 2, 64),
			strconv.FormatFloat(m.TotalStallMinutes, 'f', 2, 64),
			strconv.Itoa(m.FailoverCount),
			strconv.FormatFloat(m.ScarcePoolReserveLeftPct, 'f', 2, 64),
		}
		if err := out.Write(record); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}
