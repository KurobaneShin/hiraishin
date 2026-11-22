package sensor

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

const maxLogLine = 4 << 20

func ClaudeDir(p config.Provider, home string) string {
	if p.ConfigDir == "" {
		return filepath.Join(home, ".claude")
	}
	return config.ExpandHome(p.ConfigDir, home)
}

func ClaudeTokens(configDir string, since time.Time) (int64, error) {
	var total int64
	err := walkRecent(filepath.Join(configDir, "projects"), since, func(path string) bool { return strings.HasSuffix(path, ".jsonl") }, func(line []byte) {
		var rec struct {
			Timestamp time.Time `json:"timestamp"`
			Message   struct {
				Usage struct {
					Input         int64 `json:"input_tokens"`
					Output        int64 `json:"output_tokens"`
					CacheCreation int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Timestamp.Before(since) {
			return
		}
		u := rec.Message.Usage
		total += u.Input + u.Output + u.CacheCreation
	})
	return total, err
}

func AgyTurns(home string, since time.Time) (int64, error) {
	var n int64
	match := func(path string) bool { return filepath.Base(path) == "transcript.jsonl" }
	err := walkRecent(filepath.Join(home, ".gemini", "antigravity-cli", "brain"), since, match, func([]byte) { n++ })
	return n, err
}

func CodexDir(getenv func(string) string, home string) string {
	if getenv != nil {
		if dir := getenv("CODEX_HOME"); dir != "" {
			return config.ExpandHome(dir, home)
		}
	}
	return filepath.Join(home, ".codex")
}

type codexTokenUsage struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Output int64 `json:"output_tokens"`
	Total  int64 `json:"total_tokens"`
}

type codexRateWindow struct {
	UsedPercent   *float64 `json:"used_percent"`
	WindowMinutes int      `json:"window_minutes"`
	ResetsAt      int64    `json:"resets_at"`
}

type codexRateLimits struct {
	Primary   *codexRateWindow `json:"primary"`
	Secondary *codexRateWindow `json:"secondary"`
}

func codexUsageTokens(u codexTokenUsage) int64 {
	if u.Input > 0 || u.Output > 0 {
		return max(u.Input-u.Cached, 0) + u.Output
	}
	return u.Total
}

func CodexRolloutSamples(codexDir string, now time.Time, window config.Window) ([]Sample, error) {
	sinceWindow := now.Add(-time.Duration(window.DurationHours * float64(time.Hour)))
	sinceWeekly := now.Add(-168 * time.Hour)
	var totalTokens int64
	var bestRate *codexRateLimits
	var bestRateAt time.Time
	root := filepath.Join(codexDir, "sessions")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasPrefix(base, "rollout-") || !strings.HasSuffix(base, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(sinceWeekly) {
			return nil
		}
		var sawLast, recentTotal bool
		var fileTotal int64
		scanErr := scanLines(path, func(line []byte) {
			var rec struct {
				Timestamp time.Time `json:"timestamp"`
				Type      string    `json:"type"`
				Payload   struct {
					Type string `json:"type"`
					Info *struct {
						TotalTokenUsage *codexTokenUsage `json:"total_token_usage"`
						LastTokenUsage  *codexTokenUsage `json:"last_token_usage"`
					} `json:"info"`
					RateLimits *codexRateLimits `json:"rate_limits"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &rec) != nil || rec.Type != "event_msg" || rec.Payload.Type != "token_count" {
				return
			}
			ts := rec.Timestamp
			if ts.IsZero() {
				ts = info.ModTime()
			}
			if !ts.Before(sinceWindow) && rec.Payload.Info != nil {
				switch {
				case rec.Payload.Info.LastTokenUsage != nil:
					sawLast = true
					totalTokens += codexUsageTokens(*rec.Payload.Info.LastTokenUsage)
				case rec.Payload.Info.TotalTokenUsage != nil:
					recentTotal = true
					fileTotal = codexUsageTokens(*rec.Payload.Info.TotalTokenUsage)
				}
			}
			if rec.Payload.RateLimits != nil && !ts.Before(bestRateAt) {
				bestRateAt = ts
				bestRate = rec.Payload.RateLimits
			}
		})
		if !sawLast && recentTotal {
			totalTokens += fileTotal
		}
		return scanErr
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var out []Sample
	hasPrimary := false
	if bestRate != nil {
		addWindow := func(w *codexRateWindow, defName, defKind string, withTokens bool) {
			if w == nil || w.UsedPercent == nil {
				return
			}
			var reset time.Time
			if w.ResetsAt > 0 {
				reset = time.Unix(w.ResetsAt, 0).UTC()
				if !reset.After(now) {
					return
				}
			}
			name, kind := defName, defKind
			if w.WindowMinutes >= 10080 {
				name, kind = "weekly", "fixed_reset"
			}
			s := Sample{Window: name, Kind: kind, UsedPct: pct(*w.UsedPercent), ResetsAt: reset, Confidence: ledger.ConfidenceEstimated, Tier: "local_logs"}
			if withTokens {
				s.RawTokens = &totalTokens
				hasPrimary = true
			}
			out = append(out, s)
		}
		addWindow(bestRate.Primary, window.Name, window.Kind, true)
		addWindow(bestRate.Secondary, "weekly", "fixed_reset", false)
	}
	if !hasPrimary {
		out = append(out, Sample{Window: window.Name, Kind: window.Kind, RawTokens: &totalTokens, Confidence: ledger.ConfidenceEstimated, Tier: "local_logs"})
	}
	sortSamples(out)
	return out, nil
}

type geminiTokens struct {
	Input    int64 `json:"input"`
	Output   int64 `json:"output"`
	Cached   int64 `json:"cached"`
	Thoughts int64 `json:"thoughts"`
	Tool     int64 `json:"tool"`
	Total    int64 `json:"total"`
}

type geminiMessage struct {
	Timestamp time.Time     `json:"timestamp"`
	Tokens    *geminiTokens `json:"tokens"`
}

func geminiMsgTokens(t *geminiTokens) int64 {
	if t == nil {
		return 0
	}
	if t.Input > 0 || t.Output > 0 || t.Thoughts > 0 || t.Tool > 0 {
		return max(t.Input-t.Cached, 0) + t.Output + t.Thoughts + t.Tool
	}
	return t.Total
}

func GeminiTokens(home string, since time.Time) (int64, error) {
	var total int64
	root := filepath.Join(home, ".gemini", "tmp")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || filepath.Base(filepath.Dir(path)) != "chats" {
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasPrefix(base, "session-") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(since) {
			return nil
		}
		switch {
		case strings.HasSuffix(base, ".json"):
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var doc struct {
				Messages []geminiMessage `json:"messages"`
			}
			if json.Unmarshal(raw, &doc) != nil {
				return nil
			}
			for _, m := range doc.Messages {
				if !m.Timestamp.IsZero() && m.Timestamp.Before(since) {
					continue
				}
				total += geminiMsgTokens(m.Tokens)
			}
		case strings.HasSuffix(base, ".jsonl"):
			return scanLines(path, func(line []byte) {
				var m geminiMessage
				if json.Unmarshal(line, &m) != nil || (!m.Timestamp.IsZero() && m.Timestamp.Before(since)) {
					return
				}
				total += geminiMsgTokens(m.Tokens)
			})
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return total, err
}

func walkRecent(root string, since time.Time, match func(string) bool, each func([]byte)) error {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !match(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(since) {
			return nil
		}
		return scanLines(path, each)
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func scanLines(path string, each func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLogLine)
	for sc.Scan() {
		each(sc.Bytes())
	}
	return sc.Err()
}
