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

// With agent rows present, the claude logins render as a subgroup: a
// non-selectable "󰚩 Claude" header line above them, with the login rows
// indented beneath it, so the logins visibly belong to the Claude agent while
// OpenCode/Codex stay top-level rows.
func TestAccountSwitch_innerLines_groupsClaudeLoginsUnderHeader(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	rows := []switchRow{
		{Label: "Work", Dir: "work"},
		{Label: "Personal", Dir: "personal"},
		{Label: "OpenCode", Tool: "opencode"},
		{Label: "Codex", Tool: "codex"},
	}
	m := newAccountSwitchModel(rows, 1, "")
	lines := m.innerLines()
	// title, blank, header, 2 logins, 2 agents, blank, help = 9 lines
	if len(lines) != 9 {
		t.Fatalf("expected 9 lines (with Claude header), got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	header := lines[2]
	if !strings.Contains(header, "Claude") || !strings.Contains(header, toolRowGlyph("claude")) {
		t.Fatalf("line 2 must be the Claude group header with its icon, got %q", header)
	}
	// Login rows sit under the header, indented past the agent rows.
	if !strings.HasPrefix(lines[3], "    ") {
		t.Errorf("login row must be indented under the Claude header, got %q", lines[3])
	}
	if !strings.Contains(lines[3], "Work") || !strings.Contains(lines[4], "Personal") {
		t.Errorf("login rows must follow the Claude header, got %q / %q", lines[3], lines[4])
	}
	// Agent rows stay top-level (marker column + glyph, no extra indent).
	if strings.HasPrefix(lines[5], "    ") {
		t.Errorf("agent row must not be indented, got %q", lines[5])
	}
	if !strings.Contains(lines[5], "OpenCode") || !strings.Contains(lines[6], "Codex") {
		t.Errorf("agent rows must follow the login subgroup, got %q / %q", lines[5], lines[6])
	}
}

// Without agent rows (legacy claude-only popup) there is no subgroup header.
func TestAccountSwitch_innerLines_noHeaderWithoutToolRows(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	rows := []switchRow{{Label: "Default"}, {Label: "Work", Dir: "work"}}
	m := newAccountSwitchModel(rows, 0, "")
	lines := m.innerLines()
	if len(lines) != 6 {
		t.Fatalf("expected 6 lines (no header), got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if strings.Contains(l, "󰚩") {
			t.Errorf("claude-only popup must not render a group header, got %q", l)
		}
	}
}

// The header line shifts every selectable row down by one on screen: clicks
// must account for it, both for login rows and agent rows.
func TestAccountSwitchModel_clickWithGroupHeaderMapsRows(t *testing.T) {
	rows := []switchRow{
		{Label: "Work", Dir: "work"},
		{Label: "Personal", Dir: "personal"},
		{Label: "OpenCode", Tool: "opencode"},
	}
	m := newAccountSwitchModel(rows, 0, "")
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m = sized.(accountSwitchModel)

	firstRowY, cardLeft, _ := accountSwitchLayout(m.width, m.height, len(rows)+1, m.contentWidth())
	// firstRowY is the header line; the agent row is 3 lines below it.
	click := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: cardLeft + 2, Y: firstRowY + 3}
	out, cmd := m.Update(click)
	mm := out.(accountSwitchModel)
	if !mm.chosen || mm.cursor != 2 {
		t.Fatalf("click on agent row: chosen=%v cursor=%d, want chosen row 2", mm.chosen, mm.cursor)
	}
	if !isQuitCmd(t, cmd) {
		t.Errorf("expected Quit cmd after selecting a row")
	}

	// Clicking the header itself selects nothing — it dismisses like any other
	// non-row click on the card.
	headerClick := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: cardLeft + 2, Y: firstRowY}
	out2, _ := m.Update(headerClick)
	if out2.(accountSwitchModel).chosen {
		t.Fatalf("clicking the Claude group header must not choose a row")
	}
}

// Each tool renders its own icon instead of a generic robot: the Claude
// starburst spark, the six-spoked OpenAI mark for Codex, and OpenCode's boxed
// square. Unknown tools keep the robot fallback.
func TestAccountSwitch_toolRowGlyph_perTool(t *testing.T) {
	tests := []struct{ tool, want string }{
		{"claude", "󰵲"},
		{"codex", "󰛄"},
		{"opencode", "▣"},
		{"mystery", "󰚩"},
	}
	for _, tt := range tests {
		if got := toolRowGlyph(tt.tool); got != tt.want {
			t.Errorf("toolRowGlyph(%q) = %q, want %q", tt.tool, got, tt.want)
		}
	}
}

// The rendered rows carry the per-tool icons: the Claude subgroup header shows
// the starburst, each agent row its own mark — no generic robot anywhere.
func TestAccountSwitch_innerLines_perToolIcons(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	rows := []switchRow{
		{Label: "Work", Dir: "work"},
		{Label: "OpenCode", Tool: "opencode"},
		{Label: "Codex", Tool: "codex"},
	}
	m := newAccountSwitchModel(rows, 0, "")
	lines := m.innerLines()
	if header := lines[2]; !strings.Contains(header, "󰵲 Claude") {
		t.Errorf("Claude header must show the starburst icon, got %q", header)
	}
	if !strings.Contains(lines[4], "▣ OpenCode") {
		t.Errorf("OpenCode row must show its boxed-square icon, got %q", lines[4])
	}
	if !strings.Contains(lines[5], "󰛄 Codex") {
		t.Errorf("Codex row must show the OpenAI mark, got %q", lines[5])
	}
	for _, l := range lines {
		if strings.Contains(l, "󰚩") {
			t.Errorf("generic robot glyph must be gone, got %q", l)
		}
	}
}

// On an indented login row the cursor bar moves in with the row instead of
// hugging the card's left edge — the indent comes before the marker, so the
// bar sits right next to the nested label.
func TestAccountSwitch_innerLines_cursorBarFollowsNestedIndent(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	rows := []switchRow{
		{Label: "Work", Dir: "work"},
		{Label: "Personal", Dir: "personal"},
		{Label: "OpenCode", Tool: "opencode"},
	}
	m := newAccountSwitchModel(rows, 1, "")
	lines := m.innerLines()
	if !strings.HasPrefix(lines[4], "  ▌ ") {
		t.Errorf("cursor bar must be indented with the nested login row, got %q", lines[4])
	}
	// Agent rows stay flush: cursor there renders the bar at the left edge.
	m2 := newAccountSwitchModel(rows, 2, "")
	if l := m2.innerLines()[5]; !strings.HasPrefix(l, "▌ ") {
		t.Errorf("cursor bar on an agent row must stay at the left edge, got %q", l)
	}
}

// Rows that are neither active nor under the cursor render gray, so the brand
// colors highlight only where you are and what is running. The Claude group
// header keeps its orange only while the active or cursored row is one of its
// nested logins.
func TestAccountSwitch_innerLines_inactiveRowsGrayed(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	rows := []switchRow{
		{Label: "Work", Dir: "work"},
		{Label: "Personal", Dir: "personal"},
		{Label: "OpenCode", Tool: "opencode"},
		{Label: "Codex", Tool: "codex"},
	}
	// Active = Personal (row 1); cursor moved down to Codex (row 3).
	m := newAccountSwitchModel(rows, 1, "")
	for i := 0; i < 2; i++ {
		out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = out.(accountSwitchModel)
	}
	lines := m.innerLines()
	const gray = "38;5;244m"
	if !strings.Contains(lines[3], gray) {
		t.Errorf("inactive login row must be grayed, got %q", lines[3])
	}
	if !strings.Contains(lines[5], gray) {
		t.Errorf("inactive agent row must be grayed, got %q", lines[5])
	}
	if strings.Contains(lines[4], gray) {
		t.Errorf("active row must keep its color, got %q", lines[4])
	}
	if strings.Contains(lines[6], gray) {
		t.Errorf("cursor row must keep its color, got %q", lines[6])
	}
	// Active login is nested under Claude, so the header keeps its orange.
	if strings.Contains(lines[2], gray) {
		t.Errorf("header must stay colored while a nested login is active, got %q", lines[2])
	}

	// With the pane running codex (active row 3, cursor there too), the whole
	// claude subgroup — header included — is inactive and grays out.
	m2 := newAccountSwitchModel(rows, 3, "")
	lines2 := m2.innerLines()
	if !strings.Contains(lines2[2], gray) {
		t.Errorf("header must gray out when no nested login is active or cursored, got %q", lines2[2])
	}
}
