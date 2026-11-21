package sensor

import (
	"sync"
	"time"
)

type Tracker struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func NewTracker() *Tracker { return &Tracker{seen: map[string][]time.Time{}} }

func (t *Tracker) Observe(providerID string, sig Signal, now time.Time) Signal {
	if sig.Kind != Degraded || sig.Escalate == nil {
		return sig
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := providerID + "|" + sig.Pattern
	cutoff := now.Add(-sig.Escalate.Within)
	kept := t.seen[key][:0]
	for _, ts := range t.seen[key] {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	t.seen[key] = kept
	if len(kept) >= sig.Escalate.Count {
		delete(t.seen, key)
		sig.Kind = Exhausted
		sig.Pattern += ".repeated"
		sig.Cooldown = 0
	}
	return sig
}
