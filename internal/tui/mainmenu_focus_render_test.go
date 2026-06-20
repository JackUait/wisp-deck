package tui

import (
	"strings"
	"testing"
)

func TestHelpRow_focusAI(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusAI)
	help := stripAnsi(m.renderHelpRow())
	if !strings.Contains(help, "switch agent") {
		t.Errorf("agent-focus help should say 'switch agent', got %q", help)
	}
	if strings.Contains(help, "switch AI") {
		t.Errorf("agent-focus help should no longer say 'switch AI', got %q", help)
	}
	if !strings.Contains(help, "sections") {
		t.Errorf("agent-focus help should point down to sections, got %q", help)
	}
}

func TestHelpRow_focusTabs(t *testing.T) {
	m := focusTestMenu()
	m.SetFocus(FocusTabs)
	help := stripAnsi(m.renderHelpRow())
	if !strings.Contains(help, "section") {
		t.Errorf("tabs-focus help should mention switching section, got %q", help)
	}
}

func TestHelpRow_focusBodyProjects(t *testing.T) {
	m := focusTestMenu()
	// default focus body, projects tab
	help := stripAnsi(m.renderHelpRow())
	if !strings.Contains(help, "open") {
		t.Errorf("projects-body help should mention open, got %q", help)
	}
	if !strings.Contains(help, "sections") {
		t.Errorf("projects-body help should advertise ↑ to sections, got %q", help)
	}
}

func TestHelpRow_focusBodySettings(t *testing.T) {
	m := focusTestMenu()
	m.EnterSettings()
	help := stripAnsi(m.renderHelpRow())
	if !strings.Contains(help, "change") {
		t.Errorf("settings-body help should mention change, got %q", help)
	}
}

func TestHelpRow_focusBodyStats(t *testing.T) {
	m := focusTestMenu()
	m.SetActiveTab(TabStats)
	m.SetFocus(FocusBody)
	help := stripAnsi(m.renderHelpRow())
	if !strings.Contains(help, "scroll") {
		t.Errorf("stats-body help should mention scroll, got %q", help)
	}
}
