package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func isQuitCmd(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestClaudeAccountSwitchCmd_Registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"claude-account-switch"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if cmd.Name() != "claude-account-switch" {
		t.Errorf("resolved to %q", cmd.Name())
	}
}

func TestAccountSwitch_selectResultJSON_differentDirWritesPointer(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-account")

	got, err := selectResultJSON(ptr, "work-max", "")
	if err != nil {
		t.Fatalf("selectResultJSON: %v", err)
	}
	if got != `{"selected":true,"dir":"work-max","changed":true}` {
		t.Fatalf("json = %s", got)
	}
	data, err := os.ReadFile(ptr)
	if err != nil {
		t.Fatalf("pointer not written: %v", err)
	}
	if strings.TrimSpace(string(data)) != "work-max" {
		t.Fatalf("pointer = %q", data)
	}
}

func TestAccountSwitch_selectResultJSON_sameDirUnchanged(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-account")

	got, err := selectResultJSON(ptr, "work-max", "work-max")
	if err != nil {
		t.Fatalf("selectResultJSON: %v", err)
	}
	if got != `{"selected":true,"dir":"work-max","changed":false}` {
		t.Fatalf("json = %s", got)
	}
}

func TestAccountSwitch_selectResultJSON_defaultRemovesPointer(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-account")
	if err := os.WriteFile(ptr, []byte("work-max\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := selectResultJSON(ptr, "", "work-max")
	if err != nil {
		t.Fatalf("selectResultJSON: %v", err)
	}
	if got != `{"selected":true,"dir":"","changed":true}` {
		t.Fatalf("json = %s", got)
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatalf("pointer not removed when selecting Default")
	}
}

func TestAccountSwitch_cancelResultJSON(t *testing.T) {
	if got := cancelResultJSON(); got != `{"selected":false}` {
		t.Fatalf("json = %s", got)
	}
}

func TestAccountSwitch_loadSwitchRows_orderAndCursor(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	ptr := filepath.Join(dir, "claude-account")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(list, []byte("Work Max:work-max\nPersonal:personal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ptr, []byte("personal\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rows, cursor := loadSwitchRows(list, defLabel, ptr)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].Label != "Default" || rows[0].Dir != "" {
		t.Fatalf("row0 = %+v, want Default/\"\"", rows[0])
	}
	if rows[1].Label != "Work Max" || rows[1].Dir != "work-max" {
		t.Fatalf("row1 = %+v", rows[1])
	}
	if rows[2].Label != "Personal" || rows[2].Dir != "personal" {
		t.Fatalf("row2 = %+v", rows[2])
	}
	if cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (active personal)", cursor)
	}
}

func TestAccountSwitch_loadSwitchRows_defaultActiveCursorZero(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	ptr := filepath.Join(dir, "claude-account")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(list, []byte("Work Max:work-max\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// No pointer file => Default active.

	rows, cursor := loadSwitchRows(list, defLabel, ptr)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (Default active)", cursor)
	}
}

func TestAccountSwitchLayout_centersAndLocatesFirstRow(t *testing.T) {
	// contentW=20, 3 rows, in a 48x18 terminal:
	//   cardWidth  = 20 + 2*3(pad) + 2*1(border) = 28
	//   cardHeight = (2 header + 3 rows + 2 footer) + 2*1(pad) + 2*1(border) = 11
	//   cardLeft   = (48-28)/2 = 10 ; cardTop = (18-11)/2 = 3
	//   firstRowY  = cardTop + border + padY + header = 3 + 1 + 1 + 2 = 7
	firstRowY, cardLeft, cardWidth := accountSwitchLayout(48, 18, 3, 20)
	if cardWidth != 28 {
		t.Errorf("cardWidth = %d, want 28", cardWidth)
	}
	if cardLeft != 10 {
		t.Errorf("cardLeft = %d, want 10", cardLeft)
	}
	if firstRowY != 7 {
		t.Errorf("firstRowY = %d, want 7", firstRowY)
	}
}

func TestAccountSwitchModel_clickOnRowSelectsAndQuits(t *testing.T) {
	rows := []switchRow{{Label: "Default"}, {Label: "Work", Dir: "work"}, {Label: "Personal", Dir: "personal"}}
	m := newAccountSwitchModel(rows, 0, "")
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 48, Height: 18})
	m = sized.(accountSwitchModel)

	firstRowY, cardLeft, _ := accountSwitchLayout(m.width, m.height, len(rows), m.contentWidth())
	click := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: cardLeft + 2, Y: firstRowY + 2}
	out, cmd := m.Update(click)
	mm := out.(accountSwitchModel)
	if !mm.chosen {
		t.Fatalf("expected click on a row to choose it")
	}
	if mm.cursor != 2 {
		t.Errorf("cursor = %d, want 2", mm.cursor)
	}
	if !isQuitCmd(t, cmd) {
		t.Errorf("expected Quit cmd after selecting a row")
	}
}

func TestAccountSwitchModel_clickOutsideCardCancels(t *testing.T) {
	rows := []switchRow{{Label: "Default"}, {Label: "Work", Dir: "work"}}
	m := newAccountSwitchModel(rows, 0, "")
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 48, Height: 18})
	m = sized.(accountSwitchModel)

	// Top-left corner is inside the popup but outside the centered card.
	click := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 0, Y: 0}
	out, cmd := m.Update(click)
	mm := out.(accountSwitchModel)
	if mm.chosen {
		t.Fatalf("clicking outside the card must cancel, not choose")
	}
	if !isQuitCmd(t, cmd) {
		t.Errorf("expected Quit cmd when clicking outside the card")
	}
}

func TestAccountSwitchModel_viewEmptyBeforeSize(t *testing.T) {
	// Before the first WindowSizeMsg the switcher must paint nothing (like the diff
	// modal's !ready guard), so bubbletea's first real frame is already full-screen
	// rather than a small partial card that leaves edge rows stale in the popup.
	rows := []switchRow{{Label: "Default"}, {Label: "Work", Dir: "work"}}
	m := newAccountSwitchModel(rows, 0, "")
	if v := m.View(); v != "" {
		t.Errorf("expected empty view before size, got %q", v)
	}
}

func TestAccountSwitchModel_viewHasRoundedCorners(t *testing.T) {
	rows := []switchRow{{Label: "Default"}, {Label: "Work", Dir: "work"}}
	m := newAccountSwitchModel(rows, 0, "")
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 48, Height: 18})
	m = sized.(accountSwitchModel)
	view := m.View()
	for _, corner := range []string{"▘", "▝", "▖", "▗"} {
		if !strings.Contains(view, corner) {
			t.Errorf("view missing beveled (rounded) corner %q", corner)
		}
	}
}

func TestAccountSwitchModel_viewCompositesDimmedBackdrop(t *testing.T) {
	rows := []switchRow{{Label: "Default"}, {Label: "Work", Dir: "work"}}
	m := newAccountSwitchModel(rows, 0, "").withBackdrop([]string{"HELLO-BACKDROP-ROW"})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 48, Height: 18})
	m = sized.(accountSwitchModel)

	view := m.View()
	// The captured screen behind the popup shows through (dimmed) in the margin,
	// so a full-screen popup isn't a blank void. Row 0 isn't overlapped by the
	// centered card, so its text survives intact.
	if !strings.Contains(view, "HELLO-BACKDROP-ROW") {
		t.Errorf("view does not composite the backdrop behind the card:\n%s", view)
	}
	// The beveled card is still drawn on top.
	if !strings.Contains(view, "▘") {
		t.Errorf("view missing the beveled card over the backdrop")
	}
}

func TestAccountSwitchModel_closeAreaIsDimScrim(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	rows := []switchRow{{Label: "Default"}, {Label: "Work", Dir: "work"}}
	m := newAccountSwitchModel(rows, 0, "").withBackdrop([]string{"session behind"})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 48, Height: 18})
	m = sized.(accountSwitchModel)

	view := m.View()
	// The close area (everything outside the card) is darkened into a scrim — a dim
	// background tint — so it reads as a half-transparent backdrop over the session
	// rather than a solid opaque block.
	if !strings.Contains(view, "48;2;20;20;27") {
		t.Errorf("close-area margin is not rendered as a dim scrim background:\n%s", view)
	}
}

func TestAccountSwitchModel_roundedGrayCorners(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	// The container is filled gray edge to edge, but the corners must still read as
	// rounded. A terminal cell is one solid color, so a gray-filled corner cell
	// can't also carve a rounded notch against the dark scrim — instead each corner
	// uses a quadrant block whose outer quarter is the scrim (a sub-cell bevel), so
	// the gray fills the whole card while the corner is softened/rounded.
	card := accountSwitchCardStyle().Render("content")

	var topBorder string
	lines := strings.Split(card, "\n")
	if len(lines) > 0 {
		topBorder = lines[0]
	}
	if topBorder == "" {
		t.Fatal("card rendered no top border line")
	}
	// Derive the exact escapes lipgloss emits for each color so the test tracks the
	// constants, not a hand-computed RGB triple.
	seq := func(style lipgloss.Style, sgr string) string {
		rendered := style.Render(" ")
		i := strings.Index(rendered, sgr)
		if i < 0 {
			t.Fatalf("no %s in %q", sgr, rendered)
		}
		return rendered[i : i+strings.IndexByte(rendered[i:], 'm')]
	}
	cardBg := seq(lipgloss.NewStyle().Background(accountSwitchCardBg), "48;2;")
	scrimFg := seq(lipgloss.NewStyle().Foreground(accountSwitchScrim), "38;2;")
	// Gray fills the top border row (the whole container is one gray block).
	if !strings.Contains(topBorder, cardBg) {
		t.Errorf("top border does not carry the card gray fill %q:\n%q", cardBg, topBorder)
	}
	// Corners are beveled with quadrant blocks, softening the gray corner.
	for _, corner := range []string{"▘", "▝", "▖", "▗"} {
		if !strings.Contains(card, corner) {
			t.Errorf("card missing beveled corner glyph %q:\n%q", corner, card)
		}
	}
	// The bevel's outer quarter is the scrim (drawn as the quadrant's foreground).
	if !strings.Contains(topBorder, scrimFg) {
		t.Errorf("corner bevel does not use the scrim notch %q:\n%q", scrimFg, topBorder)
	}
}

func TestAccountSwitch_loadSwitchRows_customDefaultLabel(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	ptr := filepath.Join(dir, "claude-account")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(defLabel, []byte("My Keychain\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rows, _ := loadSwitchRows(list, defLabel, ptr)
	if rows[0].Label != "My Keychain" {
		t.Fatalf("row0 label = %q, want My Keychain", rows[0].Label)
	}
}

// The switcher marks the account THIS pane runs (passed by bash via --active),
// not the global pointer: after another session flips the pointer, the popup
// would otherwise show the wrong login as active — the visual "switch back".
func TestAccountSwitch_switchRowsForActive_marksSessionAccount(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(list, []byte("Work Max:work-max\nPersonal:personal\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rows, cursor := switchRowsForActive(list, defLabel, "personal")
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (the session's running account)", cursor)
	}
	if rows2, cursor2 := switchRowsForActive(list, defLabel, ""); len(rows2) != 3 || cursor2 != 0 {
		t.Fatalf("active=\"\" must select the Default row, got cursor %d", cursor2)
	}
}

// The user's choice is reported through a result file (display-popup swallows
// stdout): the chosen dir on select, and NO file at all on cancel — so the bash
// side can tell "picked the same account" apart from "cancelled".
func TestAccountSwitch_writeSwitchResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result")
	if err := writeSwitchResult(path, "personal"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "personal\n" {
		t.Fatalf("result = %q, want personal\\n", data)
	}
	if err := writeSwitchResult(path, ""); err != nil { // Default: empty but present
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "\n" {
		t.Fatalf("Default result = %q, want a bare newline", data)
	}
	if err := writeSwitchResult("", "personal"); err != nil { // no file requested: no-op
		t.Fatal(err)
	}
}

// The bash switcher passes --active (the session's running account) and
// --result-file (where to report the choice); both must be registered flags.
func TestAccountSwitch_activeAndResultFileFlagsRegistered(t *testing.T) {
	if claudeAccountSwitchCmd.Flags().Lookup("active") == nil {
		t.Fatal("claude-account-switch must register --active")
	}
	if claudeAccountSwitchCmd.Flags().Lookup("result-file") == nil {
		t.Fatal("claude-account-switch must register --result-file")
	}
}
