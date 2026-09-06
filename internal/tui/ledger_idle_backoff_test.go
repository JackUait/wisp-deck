package tui

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/ledger"

	tea "github.com/charmbracelet/bubbletea"
)

// countingSessionSource records how often the session context was reloaded.
type countingSessionSource struct {
	mu    sync.Mutex
	calls int
}

func (s *countingSessionSource) Load(context.Context, string) (ledger.SessionContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return ledger.SessionContext{Tool: "claude"}, nil
}

func (s *countingSessionSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *countingSessionSource) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = 0
}

// fakeClock is the model's notion of time under test.
type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time          { return c.at }
func (c *fakeClock) advance(d time.Duration) { c.at = c.at.Add(d) }

// freezeClock installs a controllable clock and returns it.
func freezeClock(m *LedgerModel) *fakeClock {
	clock := &fakeClock{at: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	m.clock = clock.now
	return clock
}

// tickUntil drives `ticks` refresh ticks through the model, advancing the clock
// by `step` each time, and returns how many actually loaded from Git.
func tickUntil(m *LedgerModel, clock *fakeClock, source *fakeLedgerSource, ticks int, step time.Duration) int {
	loads := 0
	for i := 0; i < ticks; i++ {
		clock.advance(step)
		// startLoad is the only thing that advances the generation, so it is
		// the signal that this tick actually reached Git.
		before := m.requestedGeneration
		model, _ := m.Update(ledgerRefreshTickMsg{})
		m = model.(*LedgerModel)
		if m.requestedGeneration != before {
			loads++
			// Deliver the load result the way the runtime would.
			model, _ = m.Update(ledgerSnapshotMsg{
				generation: m.requestedGeneration,
				snapshot:   source.snapshot,
			})
			m = model.(*LedgerModel)
		}
	}
	return loads
}

// 99.1% of ledger polls on a real 17-session deck found nothing changed, yet
// each one spawned five git processes. A repository that is not changing must
// be polled progressively less often.
func TestLedgerRefresh_backs_off_while_nothing_changes(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	source := &fakeLedgerSource{snapshot: snapshot}
	m := NewLedgerModel(source, snapshot, LedgerOptions{
		ProjectDir: "/repo", RefreshInterval: 2 * time.Second,
	})
	sizeLedger(m, 120, 40)
	clock := freezeClock(m)

	// 60 seconds of a dormant repository, ticked at the base 2s cadence.
	loads := tickUntil(m, clock, source, 30, 2*time.Second)

	if loads >= 30 {
		t.Fatalf("a dormant repository loaded %d times in 30 ticks; it must back off", loads)
	}
	if loads > 12 {
		t.Errorf("a dormant repository loaded %d times across 60s; the backoff is too weak", loads)
	}
}

// The backoff must never make an ACTIVE repository slower. An agent writing
// files has to keep the ledger at its base cadence.
func TestLedgerRefresh_stays_at_base_cadence_while_the_repo_changes(t *testing.T) {
	source := &fakeLedgerSource{snapshot: ledgerTestSnapshot(1)}
	m := NewLedgerModel(source, source.snapshot, LedgerOptions{
		ProjectDir: "/repo", RefreshInterval: 2 * time.Second,
	})
	sizeLedger(m, 120, 40)
	clock := freezeClock(m)

	loads := 0
	for i := 0; i < 10; i++ {
		// Every tick, the repository looks different from the last one.
		source.snapshot = ledgerTestSnapshot(i + 2)
		clock.advance(2 * time.Second)
		before := m.requestedGeneration
		model, _ := m.Update(ledgerRefreshTickMsg{})
		m = model.(*LedgerModel)
		if m.requestedGeneration != before {
			loads++
			model, _ = m.Update(ledgerSnapshotMsg{
				generation: m.requestedGeneration, snapshot: source.snapshot,
			})
			m = model.(*LedgerModel)
		}
	}
	if loads != 10 {
		t.Errorf("a changing repository loaded %d times in 10 ticks, want 10", loads)
	}
}

// A change after a quiet spell must restore the fast cadence immediately, so
// only the FIRST change of a burst can ever be late.
func TestLedgerRefresh_resets_the_cadence_when_the_repo_changes(t *testing.T) {
	source := &fakeLedgerSource{snapshot: ledgerTestSnapshot(3)}
	m := NewLedgerModel(source, source.snapshot, LedgerOptions{
		ProjectDir: "/repo", RefreshInterval: 2 * time.Second,
	})
	sizeLedger(m, 120, 40)
	clock := freezeClock(m)

	tickUntil(m, clock, source, 20, 2*time.Second) // go dormant, reach the cap
	if m.loadInterval <= 2*time.Second {
		t.Fatalf("interval is %v after 20 idle ticks; it never backed off", m.loadInterval)
	}

	source.snapshot = ledgerTestSnapshot(9)
	clock.advance(ledgerIdleRefreshCap)
	model, _ := m.Update(ledgerRefreshTickMsg{})
	m = model.(*LedgerModel)
	model, _ = m.Update(ledgerSnapshotMsg{
		generation: m.requestedGeneration, snapshot: source.snapshot,
	})
	m = model.(*LedgerModel)

	if m.loadInterval != 2*time.Second {
		t.Errorf("interval is %v after a change, want the base 2s", m.loadInterval)
	}
}

// Someone reading the ledger is asking for it to be current.
func TestLedgerRefresh_resets_the_cadence_when_the_user_interacts(t *testing.T) {
	source := &fakeLedgerSource{snapshot: ledgerTestSnapshot(3)}
	m := NewLedgerModel(source, source.snapshot, LedgerOptions{
		ProjectDir: "/repo", RefreshInterval: 2 * time.Second,
	})
	sizeLedger(m, 120, 40)
	clock := freezeClock(m)

	tickUntil(m, clock, source, 20, 2*time.Second)
	if m.loadInterval <= 2*time.Second {
		t.Fatalf("interval is %v after 20 idle ticks; it never backed off", m.loadInterval)
	}

	model, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionMotion, X: 2, Y: 2})
	m = model.(*LedgerModel)

	if m.loadInterval != 2*time.Second {
		t.Errorf("interval is %v after interaction, want the base 2s", m.loadInterval)
	}
}

// The backoff is bounded, so a dormant pane still notices the world eventually.
func TestLedgerRefresh_backoff_is_capped(t *testing.T) {
	source := &fakeLedgerSource{snapshot: ledgerTestSnapshot(3)}
	m := NewLedgerModel(source, source.snapshot, LedgerOptions{
		ProjectDir: "/repo", RefreshInterval: 2 * time.Second,
	})
	sizeLedger(m, 120, 40)
	clock := freezeClock(m)

	tickUntil(m, clock, source, 200, 2*time.Second)
	if m.loadInterval > ledgerIdleRefreshCap {
		t.Errorf("interval grew to %v, past the %v cap", m.loadInterval, ledgerIdleRefreshCap)
	}
}

// The Git load backs off; the session context must not. It is the account
// pill's only source, it becomes valid AFTER the pane is spawned, and a
// mid-session account switch rewrites it under a live pane -- so a tick that
// skipped it would leave the pill hidden for the pane's whole life. This is the
// regression the backoff introduced on its first draft.
func TestLedgerRefresh_a_backed_off_tick_still_reloads_the_session_context(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	source := &fakeLedgerSource{snapshot: snapshot}
	sessionSource := &countingSessionSource{}
	m := NewLedgerModel(source, snapshot, LedgerOptions{
		ProjectDir:      "/repo",
		RefreshInterval: 2 * time.Second,
		SessionSource:   sessionSource,
		SessionPath:     "/tmp/relaunch",
	})
	sizeLedger(m, 120, 40)
	clock := freezeClock(m)

	// Reach the backed-off cadence, then take a tick that must skip Git.
	tickUntil(m, clock, source, 20, 2*time.Second)
	if m.loadInterval <= 2*time.Second {
		t.Fatalf("interval is %v; the model never backed off", m.loadInterval)
	}

	before := m.requestedGeneration
	sessionSource.reset()
	m.sessionLoading = false
	clock.advance(2 * time.Second) // a tick, but not enough for a Git load
	model, _ := m.Update(ledgerRefreshTickMsg{})
	m = model.(*LedgerModel)
	if m.requestedGeneration != before {
		t.Fatal("the tick reached Git; this test needs a skipped one")
	}

	// loadSession marks the reload in flight synchronously, so this is what the
	// tick decided, without having to run the Tea command.
	if !m.sessionLoading {
		t.Error("a backed-off tick did not reload the session context; the account pill would never appear")
	}
}
