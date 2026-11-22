package sensor

import (
	"strings"
	"sync"
	"time"
)

type Tracker struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func NewTracker() *Tracker { return &Tracker{seen: map[string][]time.Time{}} }

func (t *Tracker) Clear(providerID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	prefix := providerID + "|"
	for k := range t.seen {
		if strings.HasPrefix(k, prefix) {
			delete(t.seen, k)
		}
	}
}

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
	if sig.Escalate.After > 0 {
		if len(kept) == 0 {
			t.seen[key] = append(kept, now)
			sig.ResetAt = time.Time{}
			return sig
		}
		if now.Sub(kept[0]) < sig.Escalate.After {
			t.seen[key] = kept
			sig.ResetAt = time.Time{}
			return sig
		}
		kept = append(kept, now)
		t.seen[key] = kept
		if len(kept) >= sig.Escalate.Count {
			delete(t.seen, key)
			sig.Kind = Exhausted
			sig.Pattern += ".repeated"
			sig.Cooldown = 0
			return sig
		}
		sig.ResetAt = time.Time{}
		return sig
	}
	kept = append(kept, now)
	t.seen[key] = kept
	if len(kept) >= sig.Escalate.Count {
		delete(t.seen, key)
		sig.Kind = Exhausted
		sig.Pattern += ".repeated"
		sig.Cooldown = 0
	} else {
		sig.ResetAt = time.Time{}
	}
	return sig
}
