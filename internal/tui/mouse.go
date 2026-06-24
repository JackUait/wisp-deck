package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jackuait/ghost-tab/internal/claudeconfig"
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
)

// hitTarget is the element under a given box-relative coordinate.
type hitTarget struct {
	region mouseRegion
	index  int  // tab index, body flat-item index, or settings row index
	prev   bool // switcher rows: the pointer fell on the "previous"/left side
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

// titleRowIndex returns the box-relative row of the AGENT/title row.
func (m *MainMenuModel) titleRowIndex() int {
	return 1 + m.accountRowCount()
}

// subscriptionRowIndex returns the box-relative row of the PLAN switcher, or -1
// when it is not shown.
func (m *MainMenuModel) subscriptionRowIndex() int {
	if m.subscriptionRowCount() > 0 {
		return m.titleRowIndex() + 1
	}
	return -1
}

// tabBarRowIndex returns the box-relative row of the Projects · Settings · Stats
// tab bar. Layout: top(0) → [account] → title → [subscription] → spacer → tabs.
func (m *MainMenuModel) tabBarRowIndex() int {
	return 3 + m.accountRowCount() + m.subscriptionRowCount()
}

// firstSettingsItemRow returns the box-relative row of settings item 0. After
// the tab bar comes the separator, a blank row, then the items.
func (m *MainMenuModel) firstSettingsItemRow() int {
	return m.tabBarRowIndex() + 3
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

// switcherName returns the value label rendered in a switcher row, used to find
// the midpoint that separates the ◀ (prev) side from the ▶ (next) side.
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
// "previous" (left/◀) side. Every switcher caption is padded to width 6
// ("AGENT ", "LOGIN ", "PLAN  "), so the value name starts at column 10
// (col 0 border, col 1 space, cols 2..7 caption, col 8 ◀, col 9 space). The
// value's own midpoint cleanly divides the ◀ side from the ▶ side.
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
// Layout after the caption: ◀(col 8) space(9) value(10..10+w) then " ▶"(2 cols),
// so the control occupies [switcherControlStart, 12+w). Hovering past it — the
// gap and the right-aligned "Ghost Tab" title — is not the switcher.
func (m *MainMenuModel) switcherSpanEnd(region mouseRegion) int {
	return 12 + lipgloss.Width(m.switcherName(region))
}

// onSwitcherControl reports whether a box column falls on a switcher's actual
// control span (caption + ◀ value ▶), so the empty remainder of the row never
// registers as the switcher.
func (m *MainMenuModel) onSwitcherControl(boxX int, region mouseRegion) bool {
	return boxX >= switcherControlStart && boxX < m.switcherSpanEnd(region)
}

// HitTest maps a box-relative coordinate to the interactive element under it.
func (m *MainMenuModel) HitTest(boxX, boxY int) hitTarget {
	// Switcher rows (only clickable when there is actually something to switch).
	// The hit is bounded to the control span so the empty remainder of the row —
	// and, on the title row, the right-aligned "Ghost Tab" — never registers.
	if boxY == m.accountRowIndex() && m.onSwitcherControl(boxX, regionAccount) {
		return hitTarget{region: regionAccount, prev: m.switcherPrev(boxX, regionAccount)}
	}
	if boxY == m.titleRowIndex() && len(m.aiTools) > 1 && m.onSwitcherControl(boxX, regionAI) {
		return hitTarget{region: regionAI, prev: m.switcherPrev(boxX, regionAI)}
	}
	if boxY == m.subscriptionRowIndex() && m.subscriptionFocusable() && m.onSwitcherControl(boxX, regionSubscription) {
		return hitTarget{region: regionSubscription, prev: m.switcherPrev(boxX, regionSubscription)}
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

	// Tab body.
	switch m.activeTab {
	case TabProjects:
		if item := m.MapRowToItem(boxY); item >= 0 {
			return hitTarget{region: regionBody, index: item}
		}
	case TabSettings:
		if !m.settingsInputMode {
			if idx := m.mapRowToSettingsItem(boxY); idx >= 0 {
				return hitTarget{region: regionSettings, index: idx}
			}
		}
	}

	return hitTarget{region: regionNone}
}

// mapRowToSettingsItem maps a box-relative row to a settings item index, or -1.
func (m *MainMenuModel) mapRowToSettingsItem(boxY int) int {
	first := m.firstSettingsItemRow()
	idx := boxY - first
	if idx >= 0 && idx < m.settingsItemCount() {
		return idx
	}
	return -1
}

// handleMouse routes a mouse event to hover (motion), click (left press), or
// wheel scrolling. Overlay/input modes own all input, so the menu's hit-testing
// is suppressed while one is open.
func (m *MainMenuModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// The login-management modal is clickable (its cursor doubles as the hover
	// highlight); its text-entry and remove-confirm sub-modes own input.
	if m.accountMenuOpen && !m.accountMenuInputMode && !m.accountMenuConfirm {
		return m.handleAccountMenuMouse(msg)
	}
	// The model-map modal is clickable: model slots cycle, the API-key row opens
	// the key field, and the Save/Cancel buttons finalize. Its key-entry sub-mode
	// owns input.
	if m.modelMapOpen && !m.modelMapKeyMode {
		return m.handleModelMapMouse(msg)
	}
	if m.modelMapOpen || m.accountMenuOpen || m.settingsInputMode ||
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
	}
	return m, nil
}

// clickSettings activates a settings row: the edit/manage rows open their flow,
// every other row cycles its value (same as the → key).
func (m *MainMenuModel) clickSettings(idx int) (tea.Model, tea.Cmd) {
	m.settingsSelected = idx
	m.focus = FocusBody
	loginIdx := m.settingsItemCount() - 1
	switch {
	case idx == 5: // Default projects dir → inline edit
		return m.settingsEnter()
	case idx == loginIdx: // Login → account management
		return m.settingsEnter()
	case idx == 6 && m.ClaudeConfigVisible() && m.selectedConfig > 0:
		// Plan row on a custom config → open the model map (its ⏎ action). Cycling
		// the plan stays available via the top PLAN switcher row.
		return m.settingsEnter()
	default:
		m.settingsValueRight()
		return m, nil
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
			if m.settingsSelected < m.settingsItemCount()-1 {
				m.settingsSelected++
			}
		} else if m.settingsSelected > 0 {
			m.settingsSelected--
		}
	case TabStats:
		if delta > 0 {
			m.statsScrollDown()
		} else if m.statsOffset > 0 {
			m.statsOffset--
		}
	}
}

// accountMenuRowToCursor maps a panel-relative row to a login cursor index, or
// -1. Panel layout: top(0) title(1) sep(2) blank(3) Default(4) managed(5..4+len)
// blank then the add row (6+len) when not typing a label.
func (m *MainMenuModel) accountMenuRowToCursor(panelY int) int {
	n := len(m.claudeAccounts)
	switch {
	case panelY == 4:
		return 0
	case panelY >= 5 && panelY <= 4+n:
		return panelY - 4
	case !m.accountMenuInputMode && panelY == 6+n:
		return m.accountMenuAddRow()
	}
	return -1
}

// handleAccountMenuMouse gives the login-management modal pointer parity with the
// keyboard: hover moves the cursor (its own highlight), left-click activates the
// row under it (switch login, or open the add-login field), and the wheel moves
// the cursor.
func (m *MainMenuModel) handleAccountMenuMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	addRow := m.accountMenuAddRow()
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		if m.accountMenuCursor < addRow {
			m.accountMenuCursor++
		}
		return m, nil
	case tea.MouseButtonWheelUp:
		if m.accountMenuCursor > 0 {
			m.accountMenuCursor--
		}
		return m, nil
	}

	boxX := msg.X - m.menuOriginX
	cursor := -1
	// Require an actual glyph under the pointer so the row's trailing padding and
	// the gap before the right-aligned status don't register as the login.
	if boxX >= 0 && boxX < menuBoxWidth && m.boxCellHasGlyph(boxX, msg.Y-m.menuOriginY) {
		cursor = m.accountMenuRowToCursor(msg.Y - m.modalOriginY)
	}

	switch msg.Action {
	case tea.MouseActionMotion:
		// Transient highlight only: cursor is -1 when the pointer is off every login
		// row, clearing the hover; the keyboard cursor stays where it was.
		m.accountMenuHover = cursor
		return m, nil
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft && cursor >= 0 {
			m.accountMenuCursor = cursor
			if cursor == addRow {
				return m, m.enterAccountAddInput()
			}
			// Switch the active login to the clicked row and close the modal,
			// mirroring the Enter action.
			m.selectedAccount = cursor
			m.persistClaudeAccount()
			m.accountMenuOpen = false
			return m, nil
		}
	}
	return m, nil
}

// model-map hit-test target kinds.
const (
	mmNone = iota
	mmModel
	mmKey
	mmSave
	mmCancel
)

// modelMapButtonRanges returns the [start,end) box-relative column spans of the
// Save and Cancel buttons, mirroring renderModelMapPanel's "   [ Save ]   [ Cancel ]"
// row (content begins at box column 2 after the border + leading space).
func modelMapButtonRanges() (saveStart, saveEnd, cancelStart, cancelEnd int) {
	col := 2 + 3 // leading space + the row's three-space indent
	saveStart = col
	saveEnd = col + lipgloss.Width(modelMapSaveLabel)
	col = saveEnd + 3 // three-space gap between buttons
	cancelStart = col
	cancelEnd = col + lipgloss.Width(modelMapCancelLabel)
	return
}

// modelMapTarget maps a box-relative coordinate to a model-map element. Panel
// layout: top(0) title(1) sep(2) blank(3) slots(4..7) blank(8) apikey(9)
// buttons(10).
func (m *MainMenuModel) modelMapTarget(boxX, panelY int) (kind, index int) {
	if boxX < 0 || boxX >= menuBoxWidth {
		return mmNone, 0
	}
	switch {
	case panelY >= 4 && panelY <= 7:
		return mmModel, panelY - 4
	case panelY == 9:
		return mmKey, 0
	case panelY == 10:
		saveStart, saveEnd, cancelStart, cancelEnd := modelMapButtonRanges()
		if boxX >= saveStart && boxX < saveEnd {
			return mmSave, 0
		}
		if boxX >= cancelStart && boxX < cancelEnd {
			return mmCancel, 0
		}
	}
	return mmNone, 0
}

// handleModelMapMouse gives the model-map modal full pointer parity: hover
// highlights slots/buttons, clicking a slot cycles its model forward, the
// API-key row opens the key field, and Save/Cancel finalize or discard.
func (m *MainMenuModel) handleModelMapMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		m.modelMapCursor = (m.modelMapCursor + 1) % 4
		return m, nil
	case tea.MouseButtonWheelUp:
		m.modelMapCursor = (m.modelMapCursor - 1 + 4) % 4
		return m, nil
	}

	kind, index := m.modelMapTarget(msg.X-m.menuOriginX, msg.Y-m.modalOriginY)
	// Slots and the API-key row span the full box width; require a glyph under the
	// pointer so their trailing padding doesn't register. The Save/Cancel buttons
	// are already column-bounded by modelMapButtonRanges.
	if (kind == mmModel || kind == mmKey) && !m.boxCellHasGlyph(msg.X-m.menuOriginX, msg.Y-m.menuOriginY) {
		kind = mmNone
	}

	switch msg.Action {
	case tea.MouseActionMotion:
		m.applyModelMapHover(kind, index)
		return m, nil
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft {
			return m.clickModelMap(kind, index)
		}
	}
	return m, nil
}

// applyModelMapHover highlights the hovered element with a transient layer that
// never moves the keyboard cursor and clears the moment the pointer leaves it:
// slots set modelMapSlotHover, the API-key row and buttons set modelMapHover.
func (m *MainMenuModel) applyModelMapHover(kind, index int) {
	m.modelMapSlotHover = -1
	switch kind {
	case mmModel:
		m.modelMapSlotHover = index
		m.modelMapHover = -1
	case mmKey:
		m.modelMapHover = 4
	case mmSave:
		m.modelMapHover = 5
	case mmCancel:
		m.modelMapHover = 6
	default:
		m.modelMapHover = -1
	}
}

// clickModelMap activates a model-map element under a left-click.
func (m *MainMenuModel) clickModelMap(kind, index int) (tea.Model, tea.Cmd) {
	switch kind {
	case mmModel:
		m.modelMapCursor = index
		// Cycle the slot's model forward, wrapping through "(none)" (mirrors →).
		n := len(m.modelMapModels)
		cur := m.modelMap[index]
		if cur >= n-1 {
			m.modelMap[index] = -1
		} else {
			m.modelMap[index] = cur + 1
		}
		return m, nil
	case mmKey:
		return m, m.enterModelMapKeyInput()
	case mmSave:
		return m.saveModelMap()
	case mmCancel:
		m.modelMapOpen = false
		return m, nil
	}
	return m, nil
}

// saveModelMap persists the current mappings and closes the panel — the click
// equivalent of pressing Enter in the model map.
func (m *MainMenuModel) saveModelMap() (tea.Model, tea.Cmd) {
	file := m.CurrentClaudeConfigFile()
	if file == "" {
		m.modelMapOpen = false
		return m, nil
	}
	if err := claudeconfig.WriteModelMappings(m.claudeConfigsDir, file, m.modelMap, m.modelMapModels); err != nil {
		m.modelMapErr = err
		return m, nil
	}
	m.syncOpenCode()
	m.modelMapOpen = false
	return m, nil
}

// directionFor maps the prev/next flag to the "prev"/"next" strings the Cycle*
// helpers expect.
func directionFor(prev bool) string {
	if prev {
		return "prev"
	}
	return "next"
}
