package scheduler

import "fmt"

func backlogShortfall(req Request, p Provider, byID map[string]Provider) string {
	g := req.Config.General
	best, bestName := 0.0, ""
	for _, w := range p.Windows {
		if w.Forecast == nil || w.UsedPct == nil || !w.ResetsAt.After(req.Now) || w.ResetsAt.Sub(req.Now).Hours() > g.BacklogHorizonHours {
			continue
		}
		unused := (100-*w.UsedPct)*w.Forecast.PerishableWasteRisk - runningBurn(req, p, byID, w)
		if bestName == "" || unused > best {
			best, bestName = unused, w.Name
		}
	}
	switch {
	case bestName == "":
		return fmt.Sprintf("backlog: no window with a burn forecast resets within %gh", g.BacklogHorizonHours)
	case best < g.BacklogMinUnusedPct:
		return fmt.Sprintf("backlog: %s leaves ~%.0f%% unused at reset after running jobs, below backlog_min_unused_pct %g", bestName, max(best, 0), g.BacklogMinUnusedPct)
	}
	return ""
}

func runningBurn(req Request, p Provider, byID map[string]Provider, w Window) float64 {
	left := w.ResetsAt.Sub(req.Now).Hours()
	var pct float64
	for _, m := range req.Config.PoolMembers(p.ID) {
		if b, ok := req.JobBurn[m][w.Name]; ok {
			pct += float64(byID[m].ActiveJobs) * b.PctPerHour * min(left, b.DurationMin/60)
		}
	}
	return pct
}
