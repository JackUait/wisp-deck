package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/models"
	"github.com/muesli/termenv"
)

// The main-menu redesign trades monochrome-orange chrome for neutral grays so
// the accent only ever marks the selected/focused thing. These tests pin the
// behavior that delivers that hierarchy. Direction mockup: design/main-menu.html.

// Idle box borders should be neutral gray, not the orange theme Dim, so the
// chrome stops competing with the selected row for attention. (Colors are
// asserted via the helper because lipgloss strips ANSI when stdout is not a TTY.)
func TestBoxBorderColor_neutralWhenBodyFocused(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none") // focus defaults to body
	if got := m.boxBorderColor(); got != lipgloss.Color("240") {
		t.Errorf("idle border color = %q, want neutral gray 240", got)
	}
}

// When focus leaves the body, the border still brightens to Primary so you can
// see which box owns the keyboard.
func TestBoxBorderColor_brightensToPrimaryOffBody(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.focus = FocusTabs
	if got := m.boxBorderColor(); got != m.theme.Primary {
		t.Errorf("focused border color = %q, want Primary %q", got, m.theme.Primary)
	}
}

// The active tab should read as a spaced label, not the ▌label▐ glyph artifact
// that looked like a rendering bug. (The underline itself is a visual style;
// here we pin removal of the block-glyph artifacts and presence of all labels.)
func TestRenderTabBar_activeTabNoBlockGlyph(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	_, _, _, lb, rb := m.boxBorders()
	bar := stripAnsi(m.renderTabBar(lb, rb))
	for _, glyph := range []string{"▌", "▐"} {
		if strings.Contains(bar, glyph) {
			t.Errorf("tab bar should not use the %q block-glyph artifact: %q", glyph, bar)
		}
	}
	for _, label := range []string{"Projects", "Settings", "Stats"} {
		if !strings.Contains(bar, label) {
			t.Errorf("tab bar missing %q: %q", label, bar)
		}
	}
}

// When the section navigation holds focus, the active tab should read as
// unmistakably selected — a solid filled pill, not just a faint underline —
// so it's obvious the keyboard is on the nav row, not the project list.
func TestRenderTabBar_navFocusFillsActivePill(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.focus = FocusTabs
	_, _, _, lb, rb := m.boxBorders()
	bar := m.renderTabBar(lb, rb)
	// Background fill on the active tab (claude Primary = 209).
	if !strings.Contains(bar, "48;5;209") {
		t.Errorf("focused-nav active tab should have a filled background pill (48;5;209): %q", bar)
	}
}

// When the body holds focus (just browsing the list), the tab bar must NOT
// fill a pill — the current section shows only the quiet underline, so the
// loud treatment is reserved for when navigation is actually focused.
func TestRenderTabBar_bodyFocusNoFilledPill(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m := NewMainMenu(nil, []string{"claude"}, "claude", "none") // focus defaults to body
	_, _, _, lb, rb := m.boxBorders()
	bar := m.renderTabBar(lb, rb)
	if strings.Contains(bar, "48;5;") {
		t.Errorf("idle (body-focus) tab bar should not fill a background pill: %q", bar)
	}
}

// The contextual action bar should advertise the real key letters (W/D/⏎)
// instead of decorative glyphs, so the labels double as a keymap.
func TestActionBarFor_usesRealKeyLetters(t *testing.T) {
	proj := actionBarFor("project", true)
	for _, k := range []string{"⏎ Open", "W Worktrees", "D Delete"} {
		if !strings.Contains(proj, k) {
			t.Errorf("project action bar missing %q: %q", k, proj)
		}
	}
	wt := actionBarFor("worktree", false)
	if !strings.Contains(wt, "⏎ Open") || !strings.Contains(wt, "D Delete") {
		t.Errorf("worktree action bar wrong: %q", wt)
	}
	for _, glyph := range []string{"◆", "✕", "▸"} {
		if strings.Contains(proj, glyph) {
			t.Errorf("action bar should drop decorative glyph %q: %q", glyph, proj)
		}
	}
}

// The agent picker gets a tiny AGENT label so the right-hand control reads as
// "this switches the agent" rather than a cramped, unlabeled chevron cluster.
func TestRenderTitleRow_hasAgentLabel(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude", "opencode"}, "claude", "none") // 2 tools → chevrons
	_, _, _, lb, rb := m.boxBorders()
	row := stripAnsi(m.renderTitleRow(lb, rb))
	if !strings.Contains(row, "AGENT") {
		t.Errorf("title row should carry an AGENT label: %q", row)
	}
	if !strings.Contains(row, "Claude Code") {
		t.Errorf("title row should still show the agent name: %q", row)
	}
}

// The selected row's worktree badge should join the row's accent (Primary),
// not the muddy orange theme Dim — no stray orange shades on the accent row.
func TestSelectedRow_worktreeBadgeUsesPrimaryNotDim(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{
		{Name: "blok", Path: "/tmp/blok", Worktrees: []models.Worktree{{Branch: "main", Path: "/tmp/wt"}}},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none") // item 0 selected by default
	box := m.renderMenuBox()
	var nameLine string
	for _, l := range strings.Split(box, "\n") {
		if strings.Contains(stripAnsi(l), "1  blok") {
			nameLine = l
			break
		}
	}
	if nameLine == "" {
		t.Fatalf("could not find selected blok name line in:\n%s", box)
	}
	if !strings.Contains(stripAnsi(nameLine), "worktree") {
		t.Fatalf("name line should carry the worktree badge: %q", stripAnsi(nameLine))
	}
	if strings.Contains(nameLine, "\x1b[38;5;166m") {
		t.Errorf("selected worktree badge must not use orange theme Dim (166): %q", nameLine)
	}
}

// When focus is on the section nav (not the body), the selected project row
// must NOT look selected: no selection wash, so the filled nav pill is the only
// thing reading as "selected". A faint neutral cursor marker still remains.
func TestSelectedRow_mutedWhenNavFocused(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{{Name: "blok", Path: "/tmp/blok"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.SetFocus(FocusTabs) // navigation focused
	box := m.renderMenuBox()

	line := findLineContaining(box, "1  blok")
	if line == "" {
		t.Fatalf("could not find blok name line in:\n%s", box)
	}
	if strings.Contains(line, "48;5;236") {
		t.Errorf("selected row must not show the selection wash when nav is focused: %q", line)
	}
}

// Contrast: when the body holds focus, the selected row keeps its wash so the
// cursor is unmistakable. (Locks the focus-follows behavior in both directions.)
func TestSelectedRow_keepsWashWhenBodyFocused(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{{Name: "blok", Path: "/tmp/blok"}}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none") // focus defaults to body
	box := m.renderMenuBox()

	line := findLineContaining(box, "1  blok")
	if line == "" {
		t.Fatalf("could not find blok name line in:\n%s", box)
	}
	if !strings.Contains(line, "48;5;236") {
		t.Errorf("selected row should show the wash when body is focused: %q", line)
	}
}

// The Settings body follows the same focus-mute rule as the Projects list:
// with the nav focused, the selected setting row must not show its wash.
func TestSettingsRow_mutedWhenNavFocused(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	m.SetFocus(FocusTabs) // navigation focused
	box := m.renderSettingsBox()

	line := findLineContaining(box, "Ghost Display")
	if line == "" {
		t.Fatalf("could not find Ghost Display row in:\n%s", box)
	}
	if strings.Contains(line, "48;5;236") {
		t.Errorf("selected settings row must not show the wash when nav is focused: %q", line)
	}
}

// Contrast: with the body focused, the selected setting row keeps its wash.
func TestSettingsRow_keepsWashWhenBodyFocused(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings) // focus stays on the body
	box := m.renderSettingsBox()

	line := findLineContaining(box, "Ghost Display")
	if line == "" {
		t.Fatalf("could not find Ghost Display row in:\n%s", box)
	}
	if !strings.Contains(line, "48;5;236") {
		t.Errorf("selected settings row should show the wash when body is focused: %q", line)
	}
}

// The subscription picker gets a tiny PLAN label mirroring the AGENT label.
func TestRenderSubscriptionRow_hasPlanLabel(t *testing.T) {
	m := subFocusMenu(t, "claude", true)
	row := stripAnsi(m.renderSubscriptionRow("│", "│"))
	if !strings.Contains(row, "PLAN") {
		t.Errorf("subscription row should carry a PLAN label: %q", row)
	}
}

// The footer hint spells out the rare shortcuts instead of cryptic "O once".
func TestFocusHint_projectsBodyExpandsAbbreviations(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none") // focus body, projects tab
	hint := m.focusHint()
	for _, want := range []string{"open once", "plain", "sections"} {
		if !strings.Contains(hint, want) {
			t.Errorf("projects footer hint missing %q: %q", want, hint)
		}
	}
	if strings.Contains(hint, "O once") {
		t.Errorf("footer should expand the cryptic 'O once': %q", hint)
	}
}

// findLineContaining returns the first line of box whose ANSI-stripped form
// contains sub, or "" if none. Used to locate a specific rendered row while
// keeping its raw ANSI codes for color assertions.
func findLineContaining(box, sub string) string {
	for _, l := range strings.Split(box, "\n") {
		if strings.Contains(stripAnsi(l), sub) {
			return l
		}
	}
	return ""
}
