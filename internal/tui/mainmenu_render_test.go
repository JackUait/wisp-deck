package tui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/models"
	"github.com/muesli/termenv"
)

func newTestMenu() *MainMenuModel {
	projects := []models.Project{
		{Name: "test-proj", Path: "/tmp/test-proj"},
	}
	m := NewMainMenu(projects, []string{"claude", "codex"}, "claude", "animated")
	m.width = 100
	m.height = 40
	return m
}

func TestMenuBox_AIToolRightAligned(t *testing.T) {
	m := newTestMenu()
	box := m.renderMenuBox()
	lines := strings.Split(box, "\n")

	// Title row is the second line (index 1), after top border
	if len(lines) < 2 {
		t.Fatal("renderMenuBox produced fewer than 2 lines")
	}
	titleRow := lines[1]

	// The AGENT switcher sits on the left and "Ghost Tab" is right-aligned, with
	// padding between the switcher cluster and the title.
	if !strings.Contains(titleRow, "Ghost Tab") {
		t.Error("title row missing 'Ghost Tab'")
	}
	if !strings.Contains(titleRow, "Claude Code") {
		t.Error("title row missing 'Claude Code'")
	}

	// Strip ANSI codes to check raw layout
	raw := stripAnsi(titleRow)
	agentIdx := strings.Index(raw, "AGENT")
	ghostIdx := strings.Index(raw, "Ghost Tab")
	if agentIdx < 0 || ghostIdx < 0 {
		t.Fatal("could not find AGENT or Ghost Tab in stripped title row")
	}
	// AGENT switcher comes first (left), Ghost Tab after it (right).
	if agentIdx > ghostIdx {
		t.Errorf("expected AGENT switcher left of Ghost Tab, got agent=%d ghost=%d in %q", agentIdx, ghostIdx, raw)
	}
	// There should be significant whitespace padding between the switcher's last
	// chevron and the right-aligned title.
	lastArrow := strings.LastIndex(raw, "▸")
	if lastArrow < 0 || lastArrow > ghostIdx {
		t.Fatalf("could not find switcher chevron left of Ghost Tab in %q", raw)
	}
	gap := raw[lastArrow+len("▸") : ghostIdx]
	if len(strings.TrimSpace(gap)) != 0 {
		t.Errorf("expected only whitespace between the switcher and Ghost Tab, got %q", gap)
	}
	if len(gap) < 5 {
		t.Errorf("expected at least 5 chars padding for right-alignment, got %d: %q", len(gap), gap)
	}
}

func TestMenuBox_TitleRightAligned(t *testing.T) {
	m := newTestMenu()
	box := m.renderMenuBox()
	lines := strings.Split(box, "\n")
	if len(lines) < 2 {
		t.Fatal("renderMenuBox produced fewer than 2 lines")
	}
	raw := stripAnsi(lines[1]) // title row
	// "Ghost Tab" is right-aligned: only whitespace (the box's right padding)
	// sits between the end of the title and the trailing border.
	ghostIdx := strings.LastIndex(raw, "Ghost Tab")
	borderChar := strings.LastIndex(raw, "│")
	if ghostIdx < 0 || borderChar < 0 || borderChar <= ghostIdx {
		t.Errorf("expected right-aligned 'Ghost Tab' before the trailing border, got: %q", raw)
	}
	trailing := raw[ghostIdx+len("Ghost Tab") : borderChar]
	if len(strings.TrimSpace(trailing)) != 0 || len(trailing) < 1 {
		t.Errorf("expected only whitespace between Ghost Tab and border, got: %q", trailing)
	}
}

func TestMenuBox_HelpTextCentered(t *testing.T) {
	// The footer hint now renders below the box (no border framing) and is
	// centered to the full box width. leftPad should roughly equal rightPad
	// when measured against the box width.
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	box := m.renderMenuBox()
	raw := stripAnsi(box)
	lines := strings.Split(raw, "\n")

	var helpLine string
	for _, l := range lines {
		if strings.Contains(l, "move") && strings.Contains(l, "open") {
			helpLine = l
			break
		}
	}
	if helpLine == "" {
		t.Fatal("could not find help row in:\n" + raw)
	}

	// The hint is the last line, below the rounded box. It carries no border
	// columns, so its width may differ slightly from the box. Verify it is
	// approximately centered against the box width.
	boxWidth := len([]rune(lines[0])) // top border defines box width
	content := strings.TrimSpace(helpLine)
	leftPad := len([]rune(helpLine)) - len([]rune(strings.TrimLeft(helpLine, " ")))
	rightPad := boxWidth - leftPad - len([]rune(content))
	diff := leftPad - rightPad
	if diff < 0 {
		diff = -diff
	}
	if diff > 1 {
		t.Errorf("help row not centered: leftPad=%d rightPad=%d (diff %d > 1); line=%q",
			leftPad, rightPad, diff, helpLine)
	}
}

func TestMenuBox_HelpTextPresent(t *testing.T) {
	m := newTestMenu()
	box := m.renderMenuBox()

	raw := stripAnsi(box)
	// The footer hint must show the redesigned key list and not the old
	// delete-mode "navigate" hint.
	if strings.Contains(raw, "navigate") {
		t.Error("help text should NOT contain 'navigate'")
	}
	if !strings.Contains(raw, "move") {
		t.Error("help text missing 'move' hint")
	}
	// AI switching moved to its own focus stop; the default body footer now
	// advertises ↑ to reach the tab bar instead.
	if !strings.Contains(raw, "sections") {
		t.Error("help text missing 'sections' hint")
	}
	if !strings.Contains(raw, "open") {
		t.Error("help text missing 'open' hint")
	}
	if !strings.Contains(raw, "P plain") {
		t.Error("help text missing 'P plain' hint")
	}
}

func TestSettingsBox_StateRightAligned(t *testing.T) {
	m := newTestMenu()
	m.SetActiveTab(TabSettings)
	m.tabTitle = "full"
	box := m.renderSettingsBox()
	raw := stripAnsi(box)
	lines := strings.Split(raw, "\n")

	// Find lines containing "Ghost Display" and "Tab Title"
	for _, line := range lines {
		if strings.Contains(line, "Ghost Display") && strings.Contains(line, "[Animated]") {
			// State text should be right-aligned: ends near the right border
			// The line should end with the state text followed by the border character
			trimmed := strings.TrimRight(line, " ")
			idx := strings.Index(trimmed, "[Animated]")
			if idx < 0 {
				t.Fatal("could not find [Animated] in Ghost Display line")
			}
			afterState := trimmed[idx+len("[Animated]"):]
			// After state text, only a small gap + border char should remain
			cleaned := strings.TrimSpace(afterState)
			if cleaned != "│" {
				t.Errorf("expected only border after [Animated], got %q", afterState)
			}
			// Between label and state there should be significant padding
			labelEnd := strings.Index(line, "Ghost Display") + len("Ghost Display")
			gap := line[labelEnd:idx]
			if len(strings.TrimSpace(gap)) != 0 {
				t.Errorf("expected only whitespace between label and state, got %q", gap)
			}
			if len(gap) < 5 {
				t.Errorf("expected at least 5 chars gap for right-alignment, got %d", len(gap))
			}
		}
	}
}

func TestGhostDisplayLabel_AllModes(t *testing.T) {
	tests := []struct {
		mode     string
		expected string
	}{
		{"animated", "Animated"},
		{"static", "Static"},
		{"none", "None"},
		{"custom", "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			result := ghostDisplayLabel(tt.mode)
			if result != tt.expected {
				t.Errorf("ghostDisplayLabel(%q) = %q, want %q", tt.mode, result, tt.expected)
			}
		})
	}
}

func TestTabTitleLabel_AllModes(t *testing.T) {
	tests := []struct {
		mode     string
		expected string
	}{
		{"full", "Project \u00b7 Tool"},
		{"project", "Project Only"},
		{"other", "other"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			result := tabTitleLabel(tt.mode)
			if result != tt.expected {
				t.Errorf("tabTitleLabel(%q) = %q, want %q", tt.mode, result, tt.expected)
			}
		})
	}
}

func TestShortenHomePath(t *testing.T) {
	home := os.Getenv("HOME")
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"home prefix", home + "/projects/foo", "~/projects/foo"},
		{"no home prefix", "/usr/local/bin", "/usr/local/bin"},
		{"exact home", home, "~"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shortenHomePath(tt.input)
			if result != tt.expected {
				t.Errorf("shortenHomePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSettingsBox_SoundDisabled(t *testing.T) {
	m := newTestMenu()
	m.SetSoundName("")
	m.EnterSettings()
	box := m.renderSettingsBox()
	if !strings.Contains(box, "Sound") {
		t.Error("settings box missing 'Sound' label")
	}
	if !strings.Contains(box, "Off") {
		t.Error("settings box should show 'Off' when sound disabled")
	}
}

func TestSettingsBox_SoundName(t *testing.T) {
	m := newTestMenu()
	m.SetSoundName("Glass")
	m.EnterSettings()
	box := m.renderSettingsBox()
	if !strings.Contains(box, "Sound") {
		t.Error("settings box missing 'Sound' label")
	}
	if !strings.Contains(box, "Glass") {
		t.Error("settings box should show 'Glass' when sound set to Glass")
	}
}

func TestCycleSoundName(t *testing.T) {
	m := newTestMenu()
	m.SetSoundName("")
	m.CycleSoundName()
	if m.SoundName() != "Basso" {
		t.Errorf("expected 'Basso' after cycling from Off, got %q", m.SoundName())
	}
}

func TestCycleSoundNameReverse(t *testing.T) {
	m := newTestMenu()
	m.SetSoundName("")
	m.CycleSoundNameReverse()
	if m.SoundName() != "Tink" {
		t.Errorf("expected 'Tink' after reverse cycling from Off, got %q", m.SoundName())
	}
}

func TestSoundNameForResult_UnchangedReturnsNil(t *testing.T) {
	m := newTestMenu()
	m.SetSoundName("Bottle")
	result := m.soundNameForResult()
	if result != nil {
		t.Error("expected nil when sound not changed")
	}
}

func TestSoundNameForResult_ChangedReturnsValue(t *testing.T) {
	m := newTestMenu()
	m.SetSoundName("Bottle")
	m.CycleSoundName()
	result := m.soundNameForResult()
	if result == nil {
		t.Fatal("expected non-nil when sound changed")
	}
	if *result != "Frog" {
		t.Errorf("expected 'Frog' after cycling from Bottle, got %q", *result)
	}
}

func TestMenuBox_WorktreeCountIndicator(t *testing.T) {
	projects := []models.Project{
		{
			Name: "ghost-tab",
			Path: "/tmp/ghost-tab",
			Worktrees: []models.Worktree{
				{Path: "/tmp/wt1", Branch: "feature/auth"},
				{Path: "/tmp/wt2", Branch: "fix/bug"},
			},
		},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	box := m.renderMenuBox()
	raw := stripAnsi(box)

	if !strings.Contains(raw, "2 worktrees") {
		t.Errorf("expected '2 worktrees' indicator in menu, got:\n%s", raw)
	}
}

func TestMenuBox_ExpandedWorktreeEntries(t *testing.T) {
	projects := []models.Project{
		{
			Name: "ghost-tab",
			Path: "/tmp/ghost-tab",
			Worktrees: []models.Worktree{
				{Path: "/tmp/wt1", Branch: "feature/auth"},
				{Path: "/tmp/wt2", Branch: "fix/bug"},
			},
		},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	m.expandedWorktrees = map[int]bool{0: true}
	box := m.renderMenuBox()
	raw := stripAnsi(box)

	if !strings.Contains(raw, "feature/auth") {
		t.Errorf("expected 'feature/auth' in expanded menu, got:\n%s", raw)
	}
	if !strings.Contains(raw, "fix/bug") {
		t.Errorf("expected 'fix/bug' in expanded menu, got:\n%s", raw)
	}
}

func TestMenuBox_WorktreeTreeConnectors(t *testing.T) {
	projects := []models.Project{
		{
			Name: "proj",
			Path: "/tmp/proj",
			Worktrees: []models.Worktree{
				{Path: "/tmp/wt1", Branch: "feature/auth"},
				{Path: "/tmp/wt2", Branch: "fix/bug"},
			},
		},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	m.expandedWorktrees = map[int]bool{0: true}
	box := m.renderMenuBox()
	raw := stripAnsi(box)

	// All worktrees use ├─ connector (add-worktree follows)
	if !strings.Contains(raw, "├─ feature/auth") {
		t.Errorf("expected '├─ feature/auth' for worktree, got:\n%s", raw)
	}
	if !strings.Contains(raw, "├─ fix/bug") {
		t.Errorf("expected '├─ fix/bug' for worktree, got:\n%s", raw)
	}
	// Add-worktree item uses └─ connector as last item
	if !strings.Contains(raw, "└─ + Add worktree") {
		t.Errorf("expected '└─ + Add worktree' as last item, got:\n%s", raw)
	}
}

func TestMenuBox_SingleWorktreeUsesEndConnector(t *testing.T) {
	projects := []models.Project{
		{
			Name: "proj",
			Path: "/tmp/proj",
			Worktrees: []models.Worktree{
				{Path: "/tmp/wt1", Branch: "only-branch"},
			},
		},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	m.expandedWorktrees = map[int]bool{0: true}
	box := m.renderMenuBox()
	raw := stripAnsi(box)

	// Single worktree uses ├─ (add-worktree follows as └─)
	if !strings.Contains(raw, "├─ only-branch") {
		t.Errorf("expected '├─ only-branch' for single worktree, got:\n%s", raw)
	}
	// Add-worktree item uses └─ connector as last item
	if !strings.Contains(raw, "└─ + Add worktree") {
		t.Errorf("expected '└─ + Add worktree' as last item, got:\n%s", raw)
	}
}

func TestMenuBox_WorktreeShowsPath(t *testing.T) {
	projects := []models.Project{
		{
			Name: "proj",
			Path: "/tmp/proj",
			Worktrees: []models.Worktree{
				{Path: "/home/jack/wt/feature-auth", Branch: "feature/auth"},
			},
		},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	m.expandedWorktrees = map[int]bool{0: true}
	box := m.renderMenuBox()
	raw := stripAnsi(box)

	// Worktree entry should show the shortened path on a second line
	if !strings.Contains(raw, "wt/feature-auth") {
		t.Errorf("expected worktree path in expanded menu, got:\n%s", raw)
	}
}

func TestMenuBox_NoIndicatorWithoutWorktrees(t *testing.T) {
	projects := []models.Project{
		{Name: "simple", Path: "/tmp/simple"},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	box := m.renderMenuBox()
	raw := stripAnsi(box)

	// Check for the worktree count badge (e.g. "2 worktrees" or "1 worktree"),
	// not the delete action label which legitimately contains "or a worktree".
	if regexp.MustCompile(`\d+ worktrees?`).MatchString(raw) {
		t.Errorf("expected no worktree indicator for project without worktrees, got:\n%s", raw)
	}
}

func TestMenuBox_ActionBarShowsWorktreesForProject(t *testing.T) {
	// Worktree access moved from the footer hint into the contextual action
	// bar: a selected project row offers a "Worktrees" action.
	projects := []models.Project{
		{
			Name: "proj",
			Path: "/tmp/proj",
			Worktrees: []models.Worktree{
				{Path: "/tmp/wt", Branch: "feature"},
			},
		},
	}
	m := NewMainMenu(projects, []string{"claude", "codex"}, "claude", "animated")
	m.width = 100
	m.height = 40
	m.selectedItem = 0 // project row
	box := m.renderMenuBox()
	raw := stripAnsi(box)

	if !strings.Contains(raw, "Worktrees") {
		t.Errorf("expected action bar to offer 'Worktrees' for a project, got:\n%s", raw)
	}
}

func TestMenuBox_HelpTextFitsWithinBorders(t *testing.T) {
	// Every framed box row (top border .. bottom border) must share the same
	// width. The footer hint now renders below the box (after the bottom
	// border) without border columns, so it is excluded from this check.
	projects := []models.Project{
		{
			Name: "proj",
			Path: "/tmp/proj",
			Worktrees: []models.Worktree{
				{Path: "/tmp/wt", Branch: "feature"},
			},
		},
	}
	m := NewMainMenu(projects, []string{"claude", "codex"}, "claude", "animated")
	m.width = 100
	m.height = 40
	box := m.renderMenuBox()
	raw := stripAnsi(box)
	lines := strings.Split(raw, "\n")

	if len(lines) < 3 {
		t.Fatal("renderMenuBox produced fewer than 3 lines")
	}

	// The top border defines the expected width of every framed row. The last
	// line is the borderless footer hint, so stop at the bottom border.
	borderWidth := len([]rune(lines[0]))
	for i := 0; i < len(lines)-1; i++ {
		line := lines[i]
		lineWidth := len([]rune(line))
		if lineWidth != borderWidth {
			t.Errorf("line %d width %d != border width %d: %q", i, lineWidth, borderWidth, line)
		}
	}
}

func TestMenuBox_UnselectedProjectUsesNeutralColors(t *testing.T) {
	// Force color output so lipgloss emits ANSI codes in tests.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{
		{Name: "selected-proj", Path: "/tmp/selected"},
		{Name: "unselected-proj", Path: "/tmp/unselected"},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	// Item 0 is selected by default, so item 1 (unselected-proj) should use neutral colors
	box := m.renderMenuBox()

	// The unselected project name should use neutral text color (252), not theme.Text (223)
	// ANSI 256-color format: \033[38;5;COLORm
	if !strings.Contains(box, "\x1b[38;5;252m") {
		t.Error("expected neutral text color (252) for unselected project name")
	}
	// The unselected project path should use neutral dim color (245), not theme.Dim (166)
	if !strings.Contains(box, "\x1b[38;5;245m") {
		t.Error("expected neutral dim color (245) for unselected project path/number")
	}
}

func TestMenuBox_SelectedProjectUsesThemeColor(t *testing.T) {
	// Force color output so lipgloss emits ANSI codes in tests.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{
		{Name: "selected-proj", Path: "/tmp/selected"},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	box := m.renderMenuBox()

	// Selected project should use theme.Primary (209) not neutral colors
	if !strings.Contains(box, "\x1b[38;5;209m") {
		t.Error("expected theme primary color (209) for selected project")
	}
}

func TestMenuBox_SelectedProjectPathNotHighlighted(t *testing.T) {
	// Force color output so lipgloss emits ANSI codes in tests.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{
		{Name: "myproj", Path: "/tmp/projpath"},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	box := m.renderMenuBox()

	// Find the path line (contains "projpath", not the name "myproj").
	var pathLine string
	for _, line := range strings.Split(box, "\n") {
		if strings.Contains(stripAnsi(line), "projpath") {
			pathLine = line
			break
		}
	}
	if pathLine == "" {
		t.Fatal("could not find selected project path line")
	}

	// The path must NOT use the theme primary highlight color (209).
	if strings.Contains(pathLine, "\x1b[38;5;209m") {
		t.Errorf("selected project path should not be highlighted with primary color (209): %q", pathLine)
	}
	// It should use the neutral dim color (245) instead.
	if !strings.Contains(pathLine, "\x1b[38;5;245m") {
		t.Errorf("expected neutral dim color (245) for selected project path: %q", pathLine)
	}
}

func TestMenuBox_AddProjectRowHasHint(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{
		{Name: "proj", Path: "/tmp/proj"},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	box := m.renderMenuBox()

	// The add-project row carries a meaningful hint subtitle, styled with the
	// neutral dim color (245), instead of just repeating the label.
	var hintLine string
	for _, line := range strings.Split(box, "\n") {
		if strings.Contains(stripAnsi(line), "Register a folder") {
			hintLine = line
			break
		}
	}
	if hintLine == "" {
		t.Fatal("expected an Add project hint subtitle line")
	}
	if !strings.Contains(hintLine, "\x1b[38;5;245m") {
		t.Errorf("add-project hint should use neutral dim color (245): %q", hintLine)
	}
}

func TestMenuBox_UnselectedAddProjectUsesThemeText(t *testing.T) {
	// Force color output so lipgloss emits ANSI codes in tests.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	projects := []models.Project{
		{Name: "proj", Path: "/tmp/proj"},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 40
	// Select first project (item 0), so the add-project row is unselected.
	m.selectedItem = 0
	box := m.renderMenuBox()

	// The unselected "+ Add project" row uses theme.Text (223 for claude).
	lines := strings.Split(box, "\n")
	found := false
	for _, line := range lines {
		raw := stripAnsi(line)
		if strings.Contains(raw, "Add project") && strings.Contains(line, "\x1b[38;5;223m") {
			found = true
		}
	}
	if !found {
		t.Error("expected theme text color (223) for unselected '+ Add project' row")
	}
}

func TestMenuBox_IdleBordersUseNeutralGray(t *testing.T) {
	// Force color output so lipgloss emits ANSI codes in tests.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	m := newTestMenu() // focus defaults to the body
	box := m.renderMenuBox()
	lines := strings.Split(box, "\n")

	// Top border (first line) should be neutral gray (240) so the chrome recedes
	// and the accent is reserved for the selected row — NOT the old orange Dim.
	if len(lines) < 1 {
		t.Fatal("no lines in rendered box")
	}
	if !strings.Contains(lines[0], "\x1b[38;5;240m") {
		t.Errorf("expected neutral gray (240) for idle box border, got: %q", lines[0])
	}
	if strings.Contains(lines[0], "\x1b[38;5;166m") {
		t.Errorf("idle box border must not use orange theme Dim (166): %q", lines[0])
	}
}

func TestMenuBox_ContentRowsHavePadding(t *testing.T) {
	m := newTestMenu()
	box := m.renderMenuBox()
	raw := stripAnsi(box)
	lines := strings.Split(raw, "\n")

	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		// Skip border-only rows (top/bottom border, separators)
		if !strings.HasPrefix(line, "│") || !strings.HasSuffix(line, "│") {
			continue
		}
		// Extract content between the border characters
		inner := line[len("│") : len(line)-len("│")]
		if len(inner) < 4 {
			continue
		}
		// Content rows must have at least 2 spaces of right padding
		rightPad := inner[len(inner)-2:]
		if rightPad != "  " {
			t.Errorf("line %d missing 2-char right padding: %q", i, line)
		}
	}
}

func TestMenuBox_UsesRoundedCorners(t *testing.T) {
	m := newTestMenu()
	box := m.renderMenuBox()
	raw := stripAnsi(box)
	lines := strings.Split(raw, "\n")

	if len(lines) < 2 {
		t.Fatal("renderMenuBox produced fewer than 2 lines")
	}
	topLine := lines[0]
	// The footer hint renders below the box, so the bottom border is the
	// second-to-last line.
	bottomLine := lines[len(lines)-2]

	if !strings.HasPrefix(topLine, "╭") || !strings.HasSuffix(topLine, "╮") {
		t.Errorf("top border should use rounded corners ╭╮, got: %q", topLine)
	}
	if !strings.HasPrefix(bottomLine, "╰") || !strings.HasSuffix(bottomLine, "╯") {
		t.Errorf("bottom border should use rounded corners ╰╯, got: %q", bottomLine)
	}
}

func TestSettingsBox_UsesRoundedCorners(t *testing.T) {
	m := newTestMenu()
	m.SetActiveTab(TabSettings)
	box := m.renderSettingsBox()
	raw := stripAnsi(box)
	lines := strings.Split(raw, "\n")

	if len(lines) < 2 {
		t.Fatal("renderSettingsBox produced fewer than 2 lines")
	}
	topLine := lines[0]
	bottomLine := lines[len(lines)-1]

	if !strings.HasPrefix(topLine, "╭") || !strings.HasSuffix(topLine, "╮") {
		t.Errorf("top border should use rounded corners ╭╮, got: %q", topLine)
	}
	if !strings.HasPrefix(bottomLine, "╰") || !strings.HasSuffix(bottomLine, "╯") {
		t.Errorf("bottom border should use rounded corners ╰╯, got: %q", bottomLine)
	}
}

func TestInputBox_UsesRoundedCorners(t *testing.T) {
	m := newTestMenu()
	m.inputMode = "add-project"
	box := m.renderInputBox()
	raw := stripAnsi(box)
	lines := strings.Split(raw, "\n")

	if len(lines) < 2 {
		t.Fatal("renderInputBox produced fewer than 2 lines")
	}
	topLine := lines[0]
	bottomLine := lines[len(lines)-1]

	if !strings.HasPrefix(topLine, "╭") || !strings.HasSuffix(topLine, "╮") {
		t.Errorf("top border should use rounded corners ╭╮, got: %q", topLine)
	}
	if !strings.HasPrefix(bottomLine, "╰") || !strings.HasSuffix(bottomLine, "╯") {
		t.Errorf("bottom border should use rounded corners ╰╯, got: %q", bottomLine)
	}
}

func TestDeleteBox_UsesRoundedCorners(t *testing.T) {
	m := newTestMenu()
	m.deleteMode = true
	box := m.renderMenuBox()
	raw := stripAnsi(box)
	lines := strings.Split(raw, "\n")

	if len(lines) < 2 {
		t.Fatal("renderMenuBox (delete mode) produced fewer than 2 lines")
	}
	topLine := lines[0]
	// The footer hint renders below the box, so the bottom border is the
	// second-to-last line.
	bottomLine := lines[len(lines)-2]

	if !strings.HasPrefix(topLine, "╭") || !strings.HasSuffix(topLine, "╮") {
		t.Errorf("top border should use rounded corners ╭╮, got: %q", topLine)
	}
	if !strings.HasPrefix(bottomLine, "╰") || !strings.HasSuffix(bottomLine, "╯") {
		t.Errorf("bottom border should use rounded corners ╰╯, got: %q", bottomLine)
	}
}

// scrollTestProjects builds n projects for scroll-behaviour tests.
func scrollTestProjects(n int) []models.Project {
	projects := make([]models.Project, n)
	for i := 0; i < n; i++ {
		projects[i] = models.Project{
			Name: "proj-" + string(rune('a'+(i%26))) + string(rune('0'+(i/26))),
			Path: "/tmp/proj-" + string(rune('a'+(i%26))) + string(rune('0'+(i/26))),
		}
	}
	return projects
}

func TestMenuBox_FitsWithinTerminalHeight(t *testing.T) {
	// 30 projects = 60 item rows + ~8 chrome rows = ~68 lines.
	// Terminal height is 24, menu box must not exceed it.
	m := NewMainMenu(scrollTestProjects(30), []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 24
	box := m.renderMenuBox()
	lines := strings.Split(box, "\n")
	if len(lines) > m.height {
		t.Errorf("menu box exceeds terminal height: %d lines > height %d", len(lines), m.height)
	}
}

func TestMenuBox_CursorVisibleAfterScrollDown(t *testing.T) {
	// Cursor on project 25 (flat idx 24) must appear in the rendered output
	// even though it sits far below the viewport.
	m := NewMainMenu(scrollTestProjects(30), []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 24
	m.selectedItem = 24 // 25th project
	box := m.renderMenuBox()
	raw := stripAnsi(box)
	// The selected project's name should appear with the cursor marker ▌.
	if !strings.Contains(raw, m.projects[24].Name) {
		t.Errorf("selected project %q not visible in scrolled view:\n%s", m.projects[24].Name, raw)
	}
	if !strings.Contains(raw, "▌25  "+m.projects[24].Name) {
		t.Errorf("cursor marker ▌ missing on selected project in scrolled view:\n%s", raw)
	}
}

func TestMenuBox_ShowsScrollIndicatorWhenOverflowing(t *testing.T) {
	// With cursor near the bottom, the view should indicate that content exists above.
	m := NewMainMenu(scrollTestProjects(30), []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 24
	m.selectedItem = 29 // last project
	box := m.renderMenuBox()
	raw := stripAnsi(box)
	// Expect an overflow indicator showing content extends beyond viewport.
	// We use ▲ (U+25B2) and ▼ (U+25BC) so they don't collide with the Shift+↑↓
	// text in the help row.
	if !strings.ContainsAny(raw, "▲▼") {
		t.Errorf("expected overflow indicator (▲ or ▼) when content is clipped, got:\n%s", raw)
	}
}

func TestMenuBox_NoScrollClipWhenFits(t *testing.T) {
	// When everything fits, menu must render all projects & the add-project row.
	m := NewMainMenu(scrollTestProjects(3), []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 60
	box := m.renderMenuBox()
	raw := stripAnsi(box)
	for _, p := range m.projects {
		if !strings.Contains(raw, p.Name) {
			t.Errorf("project %q missing when content fits:\n%s", p.Name, raw)
		}
	}
	if !strings.Contains(raw, "Add project") {
		t.Errorf("add-project row missing when content fits:\n%s", raw)
	}
	// No scroll indicators when everything fits.
	if strings.ContainsAny(raw, "▲▼") {
		t.Errorf("unexpected scroll indicator when content fits:\n%s", raw)
	}
}

func TestAvailableMenuHeight_AboveGhostAnimationReserve(t *testing.T) {
	// 30 projects ⇒ natural menuHeight = 73. Above layout is chosen when
	// height ≥ menuHeight+17 and width < 92.
	m := NewMainMenu(scrollTestProjects(30), []string{"claude"}, "claude", "animated")
	m.width = 70
	m.height = 90

	if got := m.availableMenuHeight(); got != 73 {
		t.Errorf("animated above should reserve 17 rows (ghost 15 + gap 1 + bob 1), got avail=%d (want 73)", got)
	}

	m.ghostDisplay = "static"
	if got := m.availableMenuHeight(); got != 74 {
		t.Errorf("static above should reserve 16 rows (ghost 15 + gap 1), got avail=%d (want 74)", got)
	}

	m.ghostDisplay = "none"
	if got := m.availableMenuHeight(); got != m.height {
		t.Errorf("ghost=none should not reserve rows, got avail=%d (want %d)", got, m.height)
	}
}

func TestMenuBox_CursorVisibleAtVerySmallHeight(t *testing.T) {
	// Very short terminal: availBody is 1 or 2. Indicators must not overwrite
	// the cursor row.
	m := NewMainMenu(scrollTestProjects(10), []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 9       // 4 header + 3 footer + 2 body
	m.selectedItem = 4 // 5th project, middle of list
	box := m.renderMenuBox()
	raw := stripAnsi(box)
	if !strings.Contains(raw, m.projects[4].Name) {
		t.Errorf("selected project %q hidden under scroll indicator in small viewport:\n%s",
			m.projects[4].Name, raw)
	}
}

func TestMenuBox_CursorVisibleAtMinimalHeight(t *testing.T) {
	// Height below header+footer+1: availBody clamped to 1. Must still show cursor.
	m := NewMainMenu(scrollTestProjects(10), []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 8 // 4 header + 3 footer + 1 body
	m.selectedItem = 4
	box := m.renderMenuBox()
	raw := stripAnsi(box)
	if !strings.Contains(raw, m.projects[4].Name) {
		t.Errorf("selected project %q hidden at minimal height:\n%s",
			m.projects[4].Name, raw)
	}
}

func TestMenuBox_NoPanicWhenCursorPastBody(t *testing.T) {
	// Defensive: if cursorBodyRow ever yields a value beyond len(body)
	// (stale state after a reorder, off-by-one, etc.), applyMenuScroll must
	// not slice out of bounds.
	m := NewMainMenu(scrollTestProjects(10), []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 12
	// Force selectedItem far past any valid flat index so ResolveItem's
	// action branch computes a cursor row well beyond len(body).
	m.selectedItem = 9999
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("applyMenuScroll panicked with out-of-range cursor: %v", r)
		}
	}()
	_ = m.renderMenuBox()
}

func TestMenuBox_FitsWhenGhostAboveAndHeightTight(t *testing.T) {
	// With ghost-above active, the menu must not draw past m.height even
	// when the reserved budget would force availBody below the header+footer
	// sum. The floor in availableMenuHeight must keep the full box ≤ m.height.
	m := NewMainMenu(scrollTestProjects(20), []string{"claude"}, "claude", "animated")
	m.width = 70 // < 82, forces non-side layout
	m.height = 20
	box := m.renderMenuBox()
	lines := strings.Split(box, "\n")
	if len(lines) > m.height {
		t.Errorf("rendered menu exceeds terminal height: %d > %d", len(lines), m.height)
	}
}

func TestAvailableMenuHeight_SleepingReservesZzzRows(t *testing.T) {
	// Sleeping ghost stacks Zzz frames above the ghost in "above" layout.
	// The reserve must account for them so the menu doesn't overflow.
	m := NewMainMenu(scrollTestProjects(30), []string{"claude"}, "claude", "animated")
	m.width = 70
	m.height = 100
	m.ghostSleeping = true
	if m.zzz == nil {
		t.Fatal("zzz animation unexpectedly nil")
	}
	got := m.availableMenuHeight()
	// Animated above = 17. Zzz adds at least 3 extra rows; require strictly
	// less avail than the awake case.
	awake := m.height - 17
	if got >= awake {
		t.Errorf("sleeping avail should be less than awake (%d); got %d", awake, got)
	}
}

func TestMenuBox_ScrollsWithExpandedWorktrees(t *testing.T) {
	// Recreate the screenshot scenario: one project expanded with 12 worktrees
	// plus other projects. Must fit in a shortish terminal.
	wts := make([]models.Worktree, 12)
	for i := range wts {
		wts[i] = models.Worktree{
			Branch: "feat/branch-" + string(rune('a'+i)),
			Path:   "/tmp/kb/wt-" + string(rune('a'+i)),
		}
	}
	projects := []models.Project{
		{Name: "blok", Path: "/tmp/blok"},
		{Name: "elen", Path: "/tmp/elen"},
		{Name: "knowledgebase", Path: "/tmp/knowledgebase", Worktrees: wts},
		{Name: "ghost-tab", Path: "/tmp/ghost-tab"},
		{Name: "agent-desk", Path: "/tmp/agent-desk"},
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "animated")
	m.width = 100
	m.height = 30
	m.expandedWorktrees[2] = true
	m.selectedItem = 2 // knowledgebase project row
	box := m.renderMenuBox()
	lines := strings.Split(box, "\n")
	if len(lines) > m.height {
		t.Errorf("expected menu to fit in %d rows, got %d", m.height, len(lines))
	}
	raw := stripAnsi(box)
	if !strings.Contains(raw, "knowledgebase") {
		t.Errorf("selected project missing:\n%s", raw)
	}
	if !strings.Contains(raw, "▼") {
		t.Errorf("expected ▼ indicator for content below:\n%s", raw)
	}
}

func TestMenuBox_AddProjectRowVisibleWhenOverflow(t *testing.T) {
	// Regression: cursorBodyRow had stale "action" case that returned 0 when
	// cursor is on add-project row, pinning viewport to top and hiding the row.
	// Build a list large enough to overflow a short terminal.
	projects := make([]models.Project, 30)
	for i := range projects {
		projects[i] = models.Project{
			Name: fmt.Sprintf("project-%02d", i),
			Path: fmt.Sprintf("/tmp/p%02d", i),
		}
	}
	m := NewMainMenu(projects, []string{"claude"}, "claude", "none")
	m.width = 100
	m.height = 24 // short enough to trigger scroll with 30 projects
	// Move cursor to add-project (last selectable item).
	m.selectedItem = m.TotalItems() - 1

	box := m.renderMenuBox()
	raw := stripAnsi(box)

	// (a) add-project row must be visible with its selection marker ▌.
	if !strings.Contains(raw, "▌+  Add project") {
		t.Errorf("add-project row with cursor marker ▌ missing in scrolled view:\n%s", raw)
	}
	if !strings.Contains(raw, "Add project") {
		t.Errorf("add-project row text missing in scrolled view:\n%s", raw)
	}

	// (b) scroll indicator ▲ must appear (there is content above the viewport).
	if !strings.Contains(raw, "▲") {
		t.Errorf("expected ▲ scroll indicator (content above cursor), got:\n%s", raw)
	}
}

// stripAnsi removes ANSI escape sequences from a string.
func stripAnsi(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			// Skip until 'm' (end of ANSI sequence)
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++ // skip the 'm'
			}
			continue
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}
