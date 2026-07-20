package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// --- Disable toggle (x), mirroring the AI tools panel ---

func TestSubscriptionModal_x_toggles_disabled_and_persists(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(1) // first managed subscription (Zhipu GLM)

	file := m.subscriptionModalProfile().File
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !m.subscriptionProfiles()[1].Disabled {
		t.Error("x must mark the focused subscription disabled")
	}
	disabledFile := claudeconfig.DisabledFile(m.claudeConfigsList)
	if !claudeconfig.LoadDisabled(disabledFile)[file] {
		t.Error("disabled state must persist to the sidecar file")
	}

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.subscriptionProfiles()[1].Disabled {
		t.Error("second x must re-enable the subscription")
	}
	if claudeconfig.LoadDisabled(disabledFile)[file] {
		t.Error("re-enabling must remove the sidecar entry")
	}
}

func TestSubscriptionModal_x_is_noop_on_standard_row(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(0) // Standard Claude

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	disabled := claudeconfig.LoadDisabled(claudeconfig.DisabledFile(m.claudeConfigsList))
	if len(disabled) != 0 {
		t.Errorf("x on Standard Claude must disable nothing, got %v", disabled)
	}
}

func TestSubscriptionModal_x_is_noop_on_login_rows(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(m.subscriptionLoginRowStart())

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	disabled := claudeconfig.LoadDisabled(claudeconfig.DisabledFile(m.claudeConfigsList))
	if len(disabled) != 0 {
		t.Errorf("x on a login row must disable nothing, got %v", disabled)
	}
}

func TestSubscriptionModal_help_shows_enable_when_focused_profile_disabled(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(1)
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	out := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(out, "x enable") {
		t.Errorf("help must offer 'x enable' on a disabled focused profile, got:\n%s", out)
	}

	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	out = stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(out, "x disable") {
		t.Errorf("help must return to 'x disable' after re-enabling, got:\n%s", out)
	}
}

func TestSubscriptionModal_help_keeps_disable_label_on_standard_row(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(1)
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m.selectSubscriptionProfile(0) // Standard Claude — x is a no-op here

	out := stripAnsi(m.renderSubscriptionModalCard())
	if !strings.Contains(out, "x disable") {
		t.Errorf("help on the Standard row must keep 'x disable', got:\n%s", out)
	}
	if strings.Contains(out, "x enable") {
		t.Errorf("help on the Standard row must not say 'x enable', got:\n%s", out)
	}
}

func TestSubscriptionModal_help_keeps_disable_label_on_login_rows(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(len(m.subscriptionProfiles()) - 1) // last managed profile
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m.selectSubscriptionProfile(m.subscriptionLoginRowStart()) // x is a no-op here

	out := stripAnsi(m.renderSubscriptionModalCard())
	if strings.Contains(out, "x enable") {
		t.Errorf("help on a login row must not say 'x enable' (last profile is disabled but not focused), got:\n%s", out)
	}
}

func TestSubscriptionModal_render_shows_disabled_status(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.selectSubscriptionProfile(1)
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	out := stripAnsi(strings.Join(m.subscriptionProfileLines(40, 20), "\n"))
	if !strings.Contains(out, "Disabled") {
		t.Errorf("profiles pane must tag a disabled subscription, got:\n%s", out)
	}
}
