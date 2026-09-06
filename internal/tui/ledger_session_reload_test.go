package tui

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/ledger"
)

// scriptedLedgerSessionSource returns one result per Load call, repeating the
// last one, so a test can model a relaunch context that only becomes readable
// after the ledger has already started.
type scriptedLedgerSessionSource struct {
	mu      sync.Mutex
	results []ledgerSessionResult
	calls   int
}

type ledgerSessionResult struct {
	session ledger.SessionContext
	err     error
}

func (s *scriptedLedgerSessionSource) Load(context.Context, string) (ledger.SessionContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.calls
	s.calls++
	if index >= len(s.results) {
		index = len(s.results) - 1
	}
	return s.results[index].session, s.results[index].err
}

func (s *scriptedLedgerSessionSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// runLedgerCmds executes cmd (flattening Tea batches one level, which is all the
// ledger ever returns) and feeds every resulting message back into the model.
// Refresh ticks are dropped so the helper cannot loop forever.
func runLedgerCmds(m *LedgerModel, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	message := cmd()
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		if _, isTick := message.(ledgerRefreshTickMsg); !isTick && message != nil {
			m.Update(message)
		}
		return
	}
	for _, command := range batch {
		if command == nil {
			continue
		}
		result := command()
		if result == nil {
			continue
		}
		if _, isTick := result.(ledgerRefreshTickMsg); isTick {
			continue
		}
		m.Update(result)
	}
}

// wrapper.sh writes the relaunch context AFTER the tmux batch that spawns the
// ledger pane, so the ledger's very first session load can lose the race and
// find no file at all (or a half-written one, which parses into a context with
// no accounts). The pill must recover on a later refresh instead of staying
// hidden for the pane's entire life.
func TestLedgerAccountPillRecoversWhenSessionContextArrivesLate(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	source := &recordingLedgerSource{snapshot: snapshot}
	sessionSource := &scriptedLedgerSessionSource{results: []ledgerSessionResult{
		{err: os.ErrNotExist},
		{session: ledger.SessionContext{
			Tool: "claude",
			Pill: &ledger.SessionPill{Label: "Personal", Color: 39},
		}},
	}}
	m := NewLedgerModel(source, snapshot, LedgerOptions{
		SessionSource:   sessionSource,
		SessionPath:     "/tmp/relaunch",
		RefreshInterval: time.Millisecond,
	})
	sizeLedger(m, 80, 14)
	// A refresh tick only reaches Git once its interval has elapsed, so the
	// clock is driven explicitly rather than raced against.
	clock := freezeClock(m)

	runLedgerCmds(m, m.Init())
	if m.session.Pill != nil {
		t.Fatalf("failed first load produced a pill: %#v", m.session.Pill)
	}

	// One ordinary refresh cycle: tick -> snapshot load -> snapshot applied.
	clock.advance(time.Second)
	_, tickCmd := m.Update(ledgerRefreshTickMsg{})
	if tickCmd == nil {
		t.Fatal("refresh tick returned no load command")
	}
	message := tickCmd()
	_, followUp := m.Update(message)
	runLedgerCmds(m, followUp)

	if sessionSource.count() < 2 {
		t.Fatalf("session context was loaded %d time(s); the ledger never retried",
			sessionSource.count())
	}
	if m.session.Pill == nil {
		t.Fatal("pill still missing after the relaunch context became readable")
	}
	if plain := stripANSI(m.View()); !strings.Contains(plain, "󰀄 Personal") {
		t.Fatalf("footer never rendered the recovered pill:\n%s", plain)
	}
}

// A context that parses but carries no switch options (the half-written-file
// shape: keys present, account list key not yet flushed) must also be retried —
// otherwise a partial read hides the pill permanently.
func TestLedgerAccountPillRecoversFromEmptySessionContext(t *testing.T) {
	snapshot := ledgerTestSnapshot(3)
	source := &recordingLedgerSource{snapshot: snapshot}
	sessionSource := &scriptedLedgerSessionSource{results: []ledgerSessionResult{
		{session: ledger.SessionContext{Tool: "claude"}},
		{session: ledger.SessionContext{
			Tool: "claude",
			Pill: &ledger.SessionPill{Label: "Work", Color: 170},
		}},
	}}
	m := NewLedgerModel(source, snapshot, LedgerOptions{
		SessionSource:   sessionSource,
		SessionPath:     "/tmp/relaunch",
		RefreshInterval: time.Millisecond,
	})
	sizeLedger(m, 80, 14)
	clock := freezeClock(m)

	runLedgerCmds(m, m.Init())
	if m.session.Pill != nil {
		t.Fatalf("empty context produced a pill: %#v", m.session.Pill)
	}

	clock.advance(time.Second)
	_, tickCmd := m.Update(ledgerRefreshTickMsg{})
	message := tickCmd()
	_, followUp := m.Update(message)
	runLedgerCmds(m, followUp)

	if m.session.Pill == nil || m.session.Pill.Label != "Work" {
		t.Fatalf("pill did not recover from an empty context: %#v", m.session.Pill)
	}
}

// A transient session-load failure (a tmux hiccup, a context being rewritten by
// a mid-session switch) must not evict a pill the ledger already resolved. The
// retry repairs a bad load within one tick, but blanking the context first makes
// the pill visibly drop out in the meantime — the same "the toggle vanished"
// symptom, just briefer.
func TestLedgerKeepsPillThroughTransientSessionLoadFailure(t *testing.T) {
	m := NewLedgerModel(nil, ledgerTestSnapshot(3), LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.Update(ledgerSessionMsg{session: ledger.SessionContext{
		Tool: "claude",
		Pill: &ledger.SessionPill{Label: "Personal", Color: 39},
		SwitchOptions: []ledger.SwitchOption{{
			Choice: ledger.SwitchChoice{Kind: ledger.SwitchAccount, Value: "personal"},
			Label:  "Personal", Ready: true, Active: true,
		}},
	}})

	m.Update(ledgerSessionMsg{err: os.ErrDeadlineExceeded})

	if m.session.Pill == nil {
		t.Fatal("a failed reload discarded the pill the ledger already had")
	}
	if len(m.session.SwitchOptions) == 0 {
		t.Fatal("a failed reload discarded the switch rows, disabling the toggle")
	}
	if plain := stripANSI(m.View()); !strings.Contains(plain, "󰀄 Personal") {
		t.Fatalf("footer dropped the pill on a failed reload:\n%s", plain)
	}
}

// An action error (a failed discard, a failed diff popup, a switcher that
// errored) must not take the footer over: the pill is the pane's identity and
// its only switch affordance, and actionError is sticky — it survives until some
// later action succeeds, so evicting the pill hides it indefinitely.
func TestLedgerFooterKeepsPillWhileShowingActionError(t *testing.T) {
	m := NewLedgerModel(nil, ledgerTestSnapshot(3), LedgerOptions{})
	sizeLedger(m, 80, 14)
	m.Update(ledgerSessionMsg{session: ledger.SessionContext{
		Tool: "claude",
		Pill: &ledger.SessionPill{Label: "Personal", Color: 39},
	}})
	m.actionError = os.ErrPermission

	plain := stripANSI(m.View())
	if !strings.Contains(plain, "󰀄 Personal") {
		t.Fatalf("action error evicted the account pill from the footer:\n%s", plain)
	}
	if !strings.Contains(plain, os.ErrPermission.Error()) {
		t.Fatalf("footer stopped reporting the action error:\n%s", plain)
	}
}
