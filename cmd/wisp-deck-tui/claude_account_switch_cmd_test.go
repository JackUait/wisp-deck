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
	for _, corner := range []string{"╭", "╮", "╰", "╯"} {
		if !strings.Contains(view, corner) {
			t.Errorf("view missing rounded corner %q", corner)
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
	// The rounded card is still drawn on top.
	if !strings.Contains(view, "╭") {
		t.Errorf("view missing the rounded card over the backdrop")
	}
}

func TestAccountSwitchModel_closeAreaDimmedByFaint(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	rows := []switchRow{{Label: "Default"}, {Label: "Work", Dir: "work"}}
	m := newAccountSwitchModel(rows, 0, "").withBackdrop([]string{"session behind"})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 48, Height: 18})
	m = sized.(accountSwitchModel)

	view := m.View()
	// Matching the file-list modal, the close area is dimmed via FAINTNESS, not a
	// dark background tint. Keeping the surround the same gray as the card is what
	// lets the gray-filled corners read as cleanly rounded (a dark scrim would make
	// them square). The faint escape (SGR 2) is what the diff modal's dim uses.
	faint := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("240")).Render("x")
	fi := strings.IndexByte(faint, 'm')
	faintSeq := faint[:fi+1]
	if !strings.Contains(view, faintSeq) {
		t.Errorf("close-area margin is not dimmed by faintness %q:\n%s", faintSeq, view)
	}
	// No dark scrim background — the old scrim tint must be gone so the surround
	// matches the card gray.
	if strings.Contains(view, "48;2;20;20;27") {
		t.Errorf("close area still paints a dark scrim; surround must match the card gray:\n%s", view)
	}
}

func TestAccountSwitchModel_roundedCornersNoOwnBackground(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	// Matching the file-list modal, the card sets no background of its own: the gray
	// is the terminal default shown by both the card and the (faint) surround, so
	// they are guaranteed identical and the thin rounded border rounds the corners
	// with the gray filling through on both sides. Setting a card background would
	// risk a lighter rectangle whose corners read as square against the surround.
	card := accountSwitchCardStyle().Render("content")

	top := strings.SplitN(card, "\n", 2)[0]
	// Thin rounded corners.
	if !strings.Contains(top, "╭") || !strings.Contains(card, "╯") {
		t.Errorf("card does not use a thin rounded border:\n%q", card)
	}
	// No background fill on the card — no 48;2 (truecolor bg) or 48;5 (256 bg) escape
	// anywhere, so the terminal default gray fills it and the surround alike.
	if strings.Contains(card, "48;2;") || strings.Contains(card, "48;5;") {
		t.Errorf("card paints its own background; it must inherit the terminal default so it matches the surround:\n%q", card)
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

// The switcher is no longer claude-only: bash passes the other available AI
// tools via --tools and the popup appends one agent row per tool AFTER the
// account rows. "claude" never gets its own agent row — the account rows ARE
// claude.
func TestAccountSwitch_switchRowsForSession_appendsToolRows(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(list, []byte("Work:work\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rows, cursor := switchRowsForSession(list, defLabel, "work", "claude", []string{"claude", "opencode", "codex"})
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (Default, Work, OpenCode, Codex)", len(rows))
	}
	if rows[2].Tool != "opencode" || rows[2].Label != "OpenCode" {
		t.Fatalf("row2 = %+v, want the OpenCode agent row", rows[2])
	}
	if rows[3].Tool != "codex" || rows[3].Label != "Codex" {
		t.Fatalf("row3 = %+v, want the Codex agent row", rows[3])
	}
	if rows[0].Tool != "" || rows[1].Tool != "" {
		t.Fatalf("account rows must carry no Tool, got %+v / %+v", rows[0], rows[1])
	}
	if cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (the running claude account)", cursor)
	}
}

// When the pane runs a non-claude agent, the cursor (and active dot) must land
// on that agent's row, not on any claude account.
func TestAccountSwitch_switchRowsForSession_cursorOnActiveTool(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(list, []byte("Work:work\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rows, cursor := switchRowsForSession(list, defLabel, "", "codex", []string{"opencode", "codex"})
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(rows))
	}
	if rows[cursor].Tool != "codex" {
		t.Fatalf("cursor row = %+v, want the codex agent row", rows[cursor])
	}
}

// Agent rows report through the result file as "tool:<name>" so the bash side
// can tell an agent switch apart from a claude account dir.
func TestAccountSwitch_switchResultValue(t *testing.T) {
	if got := switchResultValue(switchRow{Label: "Work", Dir: "work"}); got != "work" {
		t.Fatalf("account row result = %q, want work", got)
	}
	if got := switchResultValue(switchRow{Label: "OpenCode", Tool: "opencode"}); got != "tool:opencode" {
		t.Fatalf("agent row result = %q, want tool:opencode", got)
	}
}

// Choosing an agent row emits tool JSON and must NOT touch the claude account
// pointer — the account stays whatever it was for the next claude launch.
func TestAccountSwitch_selectToolResultJSON(t *testing.T) {
	got, err := selectToolResultJSON("opencode", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"selected":true,"tool":"opencode","changed":true}` {
		t.Fatalf("json = %s", got)
	}
	got, err = selectToolResultJSON("codex", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"selected":true,"tool":"codex","changed":false}` {
		t.Fatalf("json = %s", got)
	}
}

// With agent rows present the popup is an agent switcher, so the title says so;
// without them it keeps the claude-only wording.
func TestAccountSwitch_titleSwitchAgentWithToolRows(t *testing.T) {
	rows := []switchRow{{Label: "Default"}, {Label: "OpenCode", Tool: "opencode"}}
	m := newAccountSwitchModel(rows, 0, "")
	joined := strings.Join(m.innerLines(), "\n")
	if !strings.Contains(joined, "Switch agent") {
		t.Fatalf("title must read Switch agent, got:\n%s", joined)
	}
	m2 := newAccountSwitchModel([]switchRow{{Label: "Default"}}, 0, "")
	joined2 := strings.Join(m2.innerLines(), "\n")
	if !strings.Contains(joined2, "Switch Claude login") {
		t.Fatalf("account-only title must stay Switch Claude login, got:\n%s", joined2)
	}
}

func TestAccountSwitch_toolFlagsRegistered(t *testing.T) {
	if claudeAccountSwitchCmd.Flags().Lookup("tools") == nil {
		t.Fatal("claude-account-switch must register --tools")
	}
	if claudeAccountSwitchCmd.Flags().Lookup("active-tool") == nil {
		t.Fatal("claude-account-switch must register --active-tool")
	}
}
