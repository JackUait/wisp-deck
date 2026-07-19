package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// This file holds the mouse layer for the main menu: hover (motion) and click
// (press) support for every interactive element so the interface is fully
// usable with the pointer as well as the keyboard.
//
// Coordinates: HitTest works in *box-relative* cells — (0,0) is the menu box's
// top-left "╭". The Update handler converts absolute pointer coordinates using
// menuOriginX/menuOriginY (recomputed every View). Keeping HitTest pure makes
// the row/column math directly unit-testable without rendering.
//
// Hover follows the focus ring: moving the pointer over a region moves keyboard
// focus (and the body/settings cursor) there, so every region reuses its
// existing focused appearance — no parallel highlight styling required. The one
// exception is the tab bar, whose highlight tracks the *active* tab, so a
// hovered-but-inactive tab is marked via hoverTab. The motion handler does only
// pure Go work (no subprocess), keeping the all-motion event stream cheap.

// mouseRegion identifies which interactive element a pointer coordinate hit.
type mouseRegion int

const (
	regionNone mouseRegion = iota
	regionAccount
	regionAI
	regionSubscription
	regionTab
	regionBody
	regionSettings
	regionStatsMode // the Full/Compact toggle in the Stats view
	regionUpdate    // the right-aligned update notice / Update button
)

// hitTarget is the element under a given box-relative coordinate.
type hitTarget struct {
	region   mouseRegion
	index    int  // tab index, body flat-item index, or settings row index
	prev     bool // switcher rows: the pointer fell on the "previous"/left side
	wtButton bool // body project rows: the pointer fell on the inline add-worktree button
}

// menuBoxWidth is the fixed rendered width of the menu box (border + interior +
// padding + border): 1 + menuInnerWidth + 1.
const menuBoxWidth = menuInnerWidth + 2

// accountRowIndex returns the box-relative row of the LOGIN switcher, or -1 when
// it is not shown.
func (m *MainMenuModel) accountRowIndex() int {
	if m.accountRowCount() > 0 {
		return 1 // directly under the top border
	}
	return -1
}

// subscriptionRowIndex returns the box-relative row of the PLAN switcher, or -1
// when it is not shown.
func (m *MainMenuModel) subscriptionRowIndex() int {
	if m.subscriptionRowCount() > 0 {
		return 1 + m.accountRowCount()
	}
	return -1
}

// titleRowIndex returns the box-relative row of the AGENT/title row.
func (m *MainMenuModel) titleRowIndex() int {
	return 1 + m.accountRowCount() + m.subscriptionRowCount()
}

// updateNoticeRowIndex returns the box-relative row of the update notice, or -1
// when no update is pending. Mirrors the render placement: the notice shares
// the title row when a header row above hosts the wordmark, else it drops to
// the spacer row under the wordmark-bearing title row.
func (m *MainMenuModel) updateNoticeRowIndex() int {
	if m.updateVersion == "" {
		return -1
	}
	if m.accountRowCount() > 0 || m.subscriptionRowCount() > 0 {
		return m.titleRowIndex()
	}
	return m.titleRowIndex() + 1
}

// updateNoticeSpan returns the [start, end) box-relative column span of the
// right-aligned update notice, mirroring headerRow's layout: the title's last
// column is the last content column (menuContentWidth) after the left border
// and leading space.
func (m *MainMenuModel) updateNoticeSpan() (int, int) {
	end := 1 + menuContentWidth
	return end - lipgloss.Width(m.updateNotice()), end
}

// tabBarRowIndex returns the box-relative row of the Projects · Settings · Stats
// tab bar. Layout: top(0) → [account] → [subscription] → title → spacer → tabs.
func (m *MainMenuModel) tabBarRowIndex() int {
	return 3 + m.accountRowCount() + m.subscriptionRowCount()
}

// firstSettingsItemRow returns the box-relative row where the settings body
// begins (the first section header). After the tab bar comes the separator, a
// blank row, then the sectioned item rows.
func (m *MainMenuModel) firstSettingsItemRow() int {
	return m.tabBarRowIndex() + 3
}

// settingsBodyRowIndices maps each settings body row (starting at
// firstSettingsItemRow) to the item index rendered there, or -1 for the section
// header and blank-separator rows. It mirrors renderSettingsBox's emission order
// so mouse hit-testing lands on the right row.
func (m *MainMenuModel) settingsBodyRowIndices() []int {
	var rows []int
	for si, section := range m.settingsSections() {
		if si > 0 {
			rows = append(rows, -1) // blank separator before the header
		}
		rows = append(rows, -1) // section header
		rows = append(rows, section.indices...)
	}
	return rows
}

// tabHitRanges returns the [start, end) box-relative column span of each tab
// label, mirroring renderTabBar's layout: a leading "│ " (cols 0,1) then each
// padded label (width = label+2) joined by a two-space separator.
func tabHitRanges() [][2]int {
	ranges := make([][2]int, len(menuTabLabels))
	col := 2 // skip the left border + the leading space
	for i, label := range menuTabLabels {
		w := lipgloss.Width(label) + 2
		ranges[i] = [2]int{col, col + w}
		col += w + 2 // two-space separator between tabs
	}
	return ranges
}

// statsModeRowIndex returns the box-relative row of the Full/Compact toggle in the
// Stats view: it is the first stats content row, right below the separator that
// follows the tab bar.
func (m *MainMenuModel) statsModeRowIndex() int {
	return m.tabBarRowIndex() + 2
}

// statsModeButtonVisible reports whether the Full/Compact toggle is shown — only
// once usage data has loaded (the loading/error/empty states have no toggle row).
func (m *MainMenuModel) statsModeButtonVisible() bool {
	return m.activeTab == TabStats && !m.statsLoading && m.statsErr == nil && len(m.statsMonths) > 0
}

// statsModeHitRanges returns the [start, end) box-relative column span of each
// Full/Compact label, mirroring renderStatsModeRow's layout: a leading "│ " (cols
// 0,1), the caption, then each label cell (width = label+2, whether padded pill,
// "[label]" accent, or plain) joined by two spaces.
func statsModeHitRanges() [][2]int {
	ranges := make([][2]int, len(statsModeLabels))
	col := 2 + lipgloss.Width(statsModeCaption)
	for i, label := range statsModeLabels {
		w := lipgloss.Width(label) + 2
		ranges[i] = [2]int{col, col + w}
		col += w + 2
	}
	return ranges
}

// switcherName returns the value label rendered in a switcher row, used to find
// the midpoint that separates the ‹ (prev) side from the › (next) side.
func (m *MainMenuModel) switcherName(region mouseRegion) string {
	switch region {
	case regionAI:
		return AIToolDisplayName(m.CurrentAITool())
	case regionAccount:
		return m.CurrentClaudeAccountLabel()
	case regionSubscription:
		return m.CurrentClaudeConfigName()
	}
	return ""
}

// switcherPrev reports whether a box-relative X on a switcher row falls on the
// "previous" (left/‹) side. Every switcher caption is padded to width 6
// ("AGENT ", "LOGIN ", "PLAN  "), so the value name starts at column 10
// (col 0 border, col 1 space, cols 2..7 caption, col 8 ‹, col 9 space). The
// value's own midpoint cleanly divides the ‹ side from the › side.
func (m *MainMenuModel) switcherPrev(boxX int, region mouseRegion) bool {
	const nameStartCol = 10
	mid := nameStartCol + lipgloss.Width(m.switcherName(region))/2
	return boxX < mid
}

// switcherControlStart is the box column where a switcher's control begins — the
// caption ("AGENT "/"LOGIN "/"PLAN  ") that starts after the border + leading
// space.
const switcherControlStart = 2

// switcherSpanEnd is the exclusive box column where a switcher's control ends.
// Layout after the caption: ‹(col 8) space(9) value(10..10+w) then " ›"(2 cols),
// so the control occupies [switcherControlStart, 12+w). Hovering past it — the
// gap and the right-aligned "Wisp Deck" title — is not the switcher.
func (m *MainMenuModel) switcherSpanEnd(region mouseRegion) int {
	return 12 + lipgloss.Width(m.switcherName(region))
}

// onSwitcherControl reports whether a box column falls on a switcher's actual
// control span (caption + ‹ value ›), so the empty remainder of the row never
// registers as the switcher.
func (m *MainMenuModel) onSwitcherControl(boxX int, region mouseRegion) bool {
	return boxX >= switcherControlStart && boxX < m.switcherSpanEnd(region)
}

// HitTest maps a box-relative coordinate to the interactive element under it.
func (m *MainMenuModel) HitTest(boxX, boxY int) hitTarget {
	// Switcher rows (only clickable when there is actually something to switch).
	// The hit is bounded to the control span so the empty remainder of the row —
	// and, on the title row, the right-aligned "Wisp Deck" — never registers.
	if boxY == m.accountRowIndex() && m.onSwitcherControl(boxX, regionAccount) {
		return hitTarget{region: regionAccount, prev: m.switcherPrev(boxX, regionAccount)}
	}
	if boxY == m.titleRowIndex() && len(m.aiTools) > 1 && m.onSwitcherControl(boxX, regionAI) {
		return hitTarget{region: regionAI, prev: m.switcherPrev(boxX, regionAI)}
	}
	if boxY == m.subscriptionRowIndex() && m.subscriptionFocusable() && m.onSwitcherControl(boxX, regionSubscription) {
		return hitTarget{region: regionSubscription, prev: m.switcherPrev(boxX, regionSubscription)}
	}
	if boxY == m.updateNoticeRowIndex() && m.updateVersion != "" {
		if start, end := m.updateNoticeSpan(); boxX >= start && boxX < end {
			return hitTarget{region: regionUpdate}
		}
	}

	// Tab bar.
	if boxY == m.tabBarRowIndex() {
		for i, r := range tabHitRanges() {
			if boxX >= r[0] && boxX < r[1] {
				return hitTarget{region: regionTab, index: i}
			}
		}
		return hitTarget{region: regionNone}
	}

	// Stats Full/Compact toggle (a fixed header row, not scrolled with the body).
	if m.statsModeButtonVisible() && boxY == m.statsModeRowIndex() {
		for i, r := range statsModeHitRanges() {
			if boxX >= r[0] && boxX < r[1] {
				return hitTarget{region: regionStatsMode, index: i}
			}
		}
		return hitTarget{region: regionNone}
	}

	// Tab body.
	switch m.activeTab {
	case TabProjects:
		if item := m.MapRowToItem(boxY); item >= 0 {
			return hitTarget{region: regionBody, index: item, wtButton: m.onZeroWorktreeButton(boxX, boxY, item)}
		}
	case TabSettings:
		if idx := m.mapRowToSettingsItem(boxY); idx >= 0 {
			return hitTarget{region: regionSettings, index: idx}
		}
	}

	return hitTarget{region: regionNone}
}

// onZeroWorktreeButton reports whether the coordinate falls on the inline
// "+ Add worktree" button span of a zero-worktree project's name row. This is
// geometry only: when the button isn't rendered (row neither focused nor
// hovered) those cells are blank, so handleMouse's glyph filter already
// discards the hit.
func (m *MainMenuModel) onZeroWorktreeButton(boxX, boxY, item int) bool {
	itemType, projectIdx, _ := m.ResolveItem(item)
	if itemType != "project" || len(m.projects[projectIdx].Worktrees) > 0 || m.expandedWorktrees[projectIdx] {
		return false
	}
	if m.MapRowToItem(boxY-1) == item {
		return false // the second (path) row of the project, not the name row
	}
	// The button is right-aligned in the badge slot: the content spans columns
	// [1, 1+menuContentWidth) inside the borders, with the label at its end.
	w := len(zeroWorktreeButtonLabel)
	return boxX >= 1+menuContentWidth-w && boxX < 1+menuContentWidth
}

// mapRowToSettingsItem maps a box-relative row to a settings item index, or -1
// for header/blank rows and rows outside the body.
func (m *MainMenuModel) mapRowToSettingsItem(boxY int) int {
	rows := m.settingsBodyRowIndices()
	i := boxY - m.firstSettingsItemRow()
	if i < 0 || i >= len(rows) {
		return -1
	}
	return rows[i]
}

// handleMouse routes a mouse event to hover (motion), click (left press), or
// wheel scrolling. Overlay/input modes own all input, so the menu's hit-testing
// is suppressed while one is open.
func (m *MainMenuModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// The subscription overlay owns the full pointer stream; nothing may fall
	// through to the dimmed Settings screen.
	if m.subscriptionModal.open {
		return m.handleSubscriptionModalMouse(msg)
	}
	// The About overlay owns all mouse input: click outside the card closes it.
	if m.aboutOpen {
		return m.handleAboutMouse(msg)
	}
	// The folder browser owns all mouse input: click outside the card closes it.
	if m.browser != nil {
		return m.handleBrowserMouse(msg)
	}
	if m.aiToolsPanelOpen ||
		m.inputMode != "" || m.deleteMode || m.staleConfirmIdx >= 0 {
		return m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelDown:
		m.scrollBody(1)
		return m, nil
	case tea.MouseButtonWheelUp:
		m.scrollBody(-1)
		return m, nil
	}

	boxX, boxY := msg.X-m.menuOriginX, msg.Y-m.menuOriginY
	target := m.HitTest(boxX, boxY)
	// Body and settings rows span the full box width, so require the pointer to be
	// on an actual glyph — not the trailing padding or the mid-row gap before a
	// right-aligned worktree badge. (Switchers and tabs are already column-bounded.)
	if (target.region == regionBody || target.region == regionSettings) && !m.boxCellHasGlyph(boxX, boxY) {
		target = hitTarget{region: regionNone}
	}

	switch msg.Action {
	case tea.MouseActionMotion:
		m.applyHover(target)
		return m, nil
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft {
			m.applyHover(target) // sync focus/cursor even without a prior motion
			return m.clickTarget(target)
		}
	}
	return m, nil
}

// applyHover records what the pointer is over so the renderers can highlight it.
// Hover is a *separate* visual layer: it never moves keyboard focus or the
// selection cursor (that would hijack keyboard state and risk accidental
// activation). hoverTab is mirrored for the tab bar, whose own highlight tracks
// the active tab and so needs a distinct marker for a hovered-but-inactive tab.
func (m *MainMenuModel) applyHover(t hitTarget) {
	m.hover = t
	if t.region == regionTab {
		m.hoverTab = t.index
	} else {
		m.hoverTab = -1
	}
	if t.region == regionStatsMode {
		m.hoverStatsMode = t.index
	} else {
		m.hoverStatsMode = -1
	}
}

// isHovered reports whether the pointer is currently over the given region.
func (m *MainMenuModel) isHovered(r mouseRegion) bool {
	return m.hover.region == r
}

// frameCellHasGlyph reports whether box/screen-relative cell (x, y) holds a
// visible (non-space) rune in the given rendered frame. This is how hit-testing
// stays glyph-precise: trailing padding and inter-element gaps render as spaces,
// so the pointer must actually be on an element's glyph to count as a hit.
// ANSI styling (color/background washes) is stripped first; a background-washed
// space is still a space, so a hovered row's empty remainder never holds it.
func frameCellHasGlyph(frame []string, x, y int) bool {
	if y < 0 || y >= len(frame) || x < 0 {
		return false
	}
	plain := diffAnsiSeq.ReplaceAllString(frame[y], "")
	rs := []rune(plain)
	if x >= len(rs) {
		return false
	}
	return rs[x] != ' '
}

// boxCellHasGlyph reports whether box-relative cell (boxX, boxY) holds a glyph in
// the last rendered menu frame (menu box + any modal panel).
func (m *MainMenuModel) boxCellHasGlyph(boxX, boxY int) bool {
	return frameCellHasGlyph(m.menuLines, boxX, boxY)
}

// clickTarget activates the element under a left-click, mirroring its keyboard
// action. Switchers cycle (and take focus so the highlight persists); the
// project body keeps its select-then-activate behavior so a stray click never
// launches a project; settings rows act immediately (toggles are reversible).
func (m *MainMenuModel) clickTarget(t hitTarget) (tea.Model, tea.Cmd) {
	switch t.region {
	case regionTab:
		m.activeTab = MenuTab(t.index)
		m.focus = FocusTabs
		m.hoverTab = -1
		if m.activeTab == TabStats {
			return m, m.ensureStatsLoad()
		}
		return m, nil
	case regionAI:
		m.focus = FocusAI
		m.CycleAITool(directionFor(t.prev))
		return m, nil
	case regionAccount:
		m.focus = FocusAccount
		m.CycleAccount(directionFor(t.prev))
		return m, nil
	case regionSubscription:
		m.focus = FocusSubscription
		m.CycleMainSubscription(directionFor(t.prev))
		return m, nil
	case regionBody:
		m.focus = FocusBody
		if t.wtButton {
			// The inline add-worktree button acts immediately (expanding is
			// harmless and reversible, unlike launching a project): expand the
			// project and land the cursor on its add-worktree row.
			if _, projectIdx, _ := m.ResolveItem(t.index); projectIdx >= 0 {
				m.selectedItem = t.index
				m.ToggleWorktrees(projectIdx)
			}
			return m, nil
		}
		if m.selectedItem == t.index {
			// Clicking the already-selected row activates it (double-click-like).
			if cmd := m.selectCurrent(); cmd != nil {
				return m, cmd
			}
			return m, tea.Quit
		}
		m.selectedItem = t.index
		return m, nil
	case regionSettings:
		return m.clickSettings(t.index)
	case regionStatsMode:
		// index 0 = Full, 1 = Compact.
		m.focus = FocusBody
		m.setStatsCompact(t.index == 1)
		return m, nil
	case regionUpdate:
		// The notice is the button: clicking it mirrors the U key.
		m.setActionResult("update")
		return m, tea.Quit
	}
	return m, nil
}

// clickSettings activates a settings row: the edit/manage rows open their flow,
// every other row cycles its value (same as the → key).
func (m *MainMenuModel) clickSettings(idx int) (tea.Model, tea.Cmd) {
	m.settingsSelected = idx
	m.focus = FocusBody
	switch {
	case idx == rowAccount: // Login → account management
		return m.settingsEnter()
	case idx == rowAITools: // AI tools → install / set default
		return m.settingsEnter()
	case idx == rowSubscription && m.ClaudeConfigVisible():
		return m.settingsEnter()
	default:
		return m, m.settingsValueRight()
	}
}

// scrollBody moves the active tab's body cursor by delta (+1 down, -1 up), the
// mouse-wheel equivalent of ↑/↓ within the body.
func (m *MainMenuModel) scrollBody(delta int) {
	m.focus = FocusBody
	switch m.activeTab {
	case TabProjects:
		if delta > 0 {
			if m.selectedItem < m.TotalItems()-1 {
				m.MoveDown()
			}
		} else if m.selectedItem > 0 {
			m.MoveUp()
		}
	case TabSettings:
		if delta > 0 {
			m.settingsStep(1, false)
		} else {
			m.settingsStep(-1, false)
		}
	case TabStats:
		if delta > 0 {
			m.statsScrollDown()
		} else if m.statsOffset > 0 {
			m.statsOffset--
		}
	}
}


// directionFor maps the prev/next flag to the "prev"/"next" strings the Cycle*
// helpers expect.
func directionFor(prev bool) string {
	if prev {
		return "prev"
	}
	return "next"
}
