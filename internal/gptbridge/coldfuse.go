package gptbridge

import (
	"sync"
	"time"
)

const (
	coldStartWindow    = 10 * time.Minute
	coldStartThreshold = 2_000_000
	coldStartQuiet     = time.Hour
)

// ColdStartWarning reports uncached replays inside one window.
type ColdStartWarning struct {
	Tokens int64
	Starts int
	Window time.Duration
}

// ColdStartFuse counts fresh threads that replay history: each one is sent
// uncached, because Codex scopes the prompt cache to the thread. Legitimate
// traffic stays well under the threshold; a background fork that replays a
// conversation on a timer does not. It only warns, never blocks.
type ColdStartFuse struct {
	mu       sync.Mutex
	warn     func(ColdStartWarning)
	events   []coldStart
	lastWarn time.Time

	now    func() time.Time
	async  bool
	record func(int64) // test hook
}

type coldStart struct {
	at     time.Time
	tokens int64
}

func NewColdStartFuse(warn func(ColdStartWarning)) *ColdStartFuse {
	return &ColdStartFuse{warn: warn, now: time.Now, async: true}
}

// Record is safe on a nil fuse.
func (f *ColdStartFuse) Record(tokens int64) {
	if f == nil {
		return
	}
	if f.record != nil {
		f.record(tokens)
	}
	f.mu.Lock()
	now := f.now()
	f.events = append(f.events, coldStart{at: now, tokens: tokens})
	cut := 0
	for cut < len(f.events) && now.Sub(f.events[cut].at) > coldStartWindow {
		cut++
	}
	f.events = f.events[cut:]
	var total int64
	for _, event := range f.events {
		total += event.tokens
	}
	fire := total > coldStartThreshold && f.warn != nil &&
		(f.lastWarn.IsZero() || now.Sub(f.lastWarn) >= coldStartQuiet)
	warning := ColdStartWarning{Tokens: total, Starts: len(f.events), Window: coldStartWindow}
	if fire {
		f.lastWarn = now
	}
	f.mu.Unlock()
	if !fire {
		return
	}
	if f.async {
		go f.warn(warning)
		return
	}
	f.warn(warning)
}
