package tui

import (
	"strings"
	"testing"
)

// The Account row was removed from Settings: logins are managed only through
// the Subscription row's unified modal (and the 'l' shortcut). The section
// header still reads ACCOUNT (uppercased at render), so a case-sensitive
// "Account" match catches only a leftover row label.
func TestSettings_account_row_is_gone(t *testing.T) {
	m := rowTestMenu()
	m.SetActiveTab(TabSettings)
	out := m.renderSettingsBox()
	if strings.Contains(out, "Account") {
		t.Errorf("settings box must not render an Account row:\n%s", out)
	}
}

// With the Account row gone, every remaining row must still be reachable and
// the Account section must hold exactly Subscription and Auto-switch.
func TestSettings_account_section_holds_subscription_and_autoswitch(t *testing.T) {
	m := rowTestMenu()
	var section *settingsSection
	for _, s := range m.settingsSections() {
		if s.title == "Account" {
			sc := s
			section = &sc
		}
	}
	if section == nil {
		t.Fatalf("no Account section in %v", m.settingsSections())
	}
	want := []int{rowSubscription, rowAutoSwitch}
	if len(section.indices) != len(want) {
		t.Fatalf("Account section indices = %v, want %v", section.indices, want)
	}
	for i, idx := range want {
		if section.indices[i] != idx {
			t.Fatalf("Account section indices = %v, want %v", section.indices, want)
		}
	}
}
