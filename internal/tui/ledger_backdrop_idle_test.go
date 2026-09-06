package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jackuait/wisp-deck/internal/ledger"
)

// The backdrop is the dimmed screen painted behind the diff popup, so it is
// only ever needed once the user opens one. It used to be rebuilt on every
// 2s data refresh, which spawned `tmux display-message`, `tmux list-panes` and
// one `tmux capture-pane` PER PANE, then wrote and renamed a temp file --
// measured at 220-247ms per rebuild, in every open session, forever, whether or
// not a popup was ever opened.
func TestLedgerBackdrop_is_not_rebuilt_by_an_idle_refresh(t *testing.T) {
	cache := &fakeBackdropCache{}
	snapshot := ledgerSnapshotWithRows(2)
	m := NewLedgerModel(fakeLedgerSource{snapshot: snapshot}, snapshot,
		LedgerOptions{ProjectDir: "/repo", BackdropCache: cache})
	sizeLedger(m, 120, 40)

	for i := 0; i < 5; i++ {
		model, _ := m.Update(ledgerSnapshotMsg{generation: m.requestedGeneration, snapshot: snapshot})
		m = model.(*LedgerModel)
	}

	if got := cache.refreshCount(); got != 0 {
		t.Errorf("an unattended pane rebuilt the popup backdrop %d times; it must rebuild only for a user who might open one", got)
	}
}

// The user cannot open a popup without first moving onto a row, so interaction
// is what arms the backdrop -- and it makes it fresher than a 2s timer did.
func TestLedgerBackdrop_is_rebuilt_when_the_user_interacts(t *testing.T) {
	cache := &fakeBackdropCache{}
	snapshot := ledgerSnapshotWithRows(2)
	m := NewLedgerModel(fakeLedgerSource{snapshot: snapshot}, snapshot,
		LedgerOptions{ProjectDir: "/repo", BackdropCache: cache})
	sizeLedger(m, 120, 40)

	model, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionMotion, X: 2, Y: 2})
	m = model.(*LedgerModel)
	drainCmd(cmd)
	if got := cache.refreshCount(); got != 1 {
		t.Fatalf("interaction rebuilt the backdrop %d times, want 1", got)
	}
}

// A mouse crossing the pane emits a motion event per cell. Rebuilding on each
// would be far worse than the timer this replaced.
func TestLedgerBackdrop_rebuild_is_throttled_across_a_burst(t *testing.T) {
	cache := &fakeBackdropCache{}
	snapshot := ledgerSnapshotWithRows(2)
	m := NewLedgerModel(fakeLedgerSource{snapshot: snapshot}, snapshot,
		LedgerOptions{ProjectDir: "/repo", BackdropCache: cache})
	sizeLedger(m, 120, 40)

	for i := 0; i < 40; i++ {
		model, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionMotion, X: 2, Y: 2 + i%5})
		m = model.(*LedgerModel)
		drainCmd(cmd)
	}
	if got := cache.refreshCount(); got != 1 {
		t.Errorf("a 40-event mouse burst rebuilt the backdrop %d times, want 1", got)
	}
}

// Throttling must not freeze it: a later interaction gets a current backdrop.
func TestLedgerBackdrop_rebuilds_again_after_the_throttle_window(t *testing.T) {
	cache := &fakeBackdropCache{}
	snapshot := ledgerSnapshotWithRows(2)
	m := NewLedgerModel(fakeLedgerSource{snapshot: snapshot}, snapshot,
		LedgerOptions{ProjectDir: "/repo", BackdropCache: cache})
	sizeLedger(m, 120, 40)

	model, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionMotion, X: 2, Y: 2})
	m = model.(*LedgerModel)
	drainCmd(cmd)
	// The runtime delivers the completion, which clears the in-flight guard.
	model, _ = m.Update(ledgerBackdropReadyMsg{})
	m = model.(*LedgerModel)

	m.backdropRefreshedAt = time.Now().Add(-2 * backdropRefreshThrottle)

	model, cmd = m.Update(tea.MouseMsg{Action: tea.MouseActionMotion, X: 3, Y: 3})
	m = model.(*LedgerModel)
	drainCmd(cmd)

	if got := cache.refreshCount(); got != 2 {
		t.Errorf("backdrop rebuilt %d times across the throttle window, want 2", got)
	}
}

// drainCmd runs a returned tea.Cmd (and any batch it wraps) so the work a real
// Bubbletea runtime would perform actually happens.
func drainCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.([]tea.Cmd); ok {
		for _, inner := range batch {
			drainCmd(inner)
		}
	}
}

func ledgerSnapshotWithRows(rows int) ledger.Snapshot {
	return ledgerTestSnapshot(rows)
}
