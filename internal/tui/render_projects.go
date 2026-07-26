package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Header switcher rows (LOGIN / AGENT / PLAN) are captioned with a single
// nerd-font glyph instead of an all-caps word. The icon reads at a glance and
// keeps the chevron cluster from feeling noisy and cramped. These are Material
// Design Icons (the FA "robot"/"crown" code points are absent from Hack Nerd
// Font and render as tofu). All three are one-cell glyphs at the same byte
// length, so the captions are the same width and the chevrons line up vertically.
const (
	iconLogin = "\U000F0004" // nf-md-account — which Claude login is active
	iconAgent = "\U000F06A9" // nf-md-robot   — the AI agent (Claude Code / OpenCode)
	iconPlan  = "\U000F0148" // nf-md-crown   — the Claude subscription tier
	iconGhost = "\U000F02A0" // nf-md-ghost   — captions the "Wisp Deck" wordmark

	// Switcher prev/next buttons. Material Design chevrons from the same nerd-font
	// family as the caption icons above; both are one-cell glyphs so the
	// caption/value/HitTest column math is unchanged.
	iconChevronLeft  = "\U000F0141" // nf-md-chevron_left
	iconChevronRight = "\U000F0142" // nf-md-chevron_right
)

// menuSurface is the one raised background in the menu box: the selected row's
// stripe, and the idle update chip. Everything else in this interface is text on
// the terminal background, so anything that needs to read as a surface reuses
// this rather than introducing a second fill.
const menuSurface = "236"

// settingsCaption renders a header-row caption glyph. Idle it is neutral gray;
// when the row is targeted (keyboard focus or hover) it brightens to the accent
// so the icon reads as the live ←/→ target, matching the chevrons beside it. The
// two trailing spaces separate the icon from the chevron cluster and, being
// identical across rows, keep the LOGIN/AGENT/PLAN chevrons aligned.
func (m *MainMenuModel) settingsCaption(glyph string, focused bool) string {
	color := lipgloss.Color("245")
	if focused {
		color = m.theme.Accent
	}
	return lipgloss.NewStyle().Foreground(color).Render(glyph + "  ")
}

// zeroWorktreeButtonLabel is the inline button shown in the badge slot of a
// focused/hovered project row that has no worktrees. Activating it (click or W)
// expands the project straight to its add-worktree row.
const zeroWorktreeButtonLabel = "+ Add worktree"

// actionBarFor returns the contextual action label text for a selected row type.
// W is always meaningful on a project row: it toggles existing worktrees, or
// expands a zero-worktree project straight to its add-worktree row.
func actionBarFor(itemType string) string {
	// Labels double as a keymap: the leading glyph/letter is the real keybinding
	// (Enter opens, W toggles worktrees, D deletes — see handleRune).
	switch itemType {
	case "project":
		return "⏎ Open    W Worktrees    D Delete"
	case "worktree":
		return "⏎ Open    D Delete"
	case "add-project":
		return "⏎ Add project"
	default:
		return ""
	}
}

// renderActionBar renders the contextual action line for the current selection.
func (m *MainMenuModel) renderActionBar(leftBorder, rightBorder string) string {
	style := lipgloss.NewStyle().Foreground(m.theme.Accent)
	itemType, _, _ := m.ResolveItem(m.selectedItem)
	text := actionBarFor(itemType)
	rendered := style.Render(text)
	gap := menuContentWidth - lipgloss.Width(rendered) - 2
	if gap < 0 {
		gap = 0
	}
	return leftBorder + "  " + rendered + strings.Repeat(" ", gap) + rightBorder
}

// ghostWordmark renders the right-aligned "Wisp Deck" wordmark with its ghost
// icon. It captions the header's topmost row — the account row when accounts
// exist, then the PLAN row, falling back to the AGENT row.
func (m *MainMenuModel) ghostWordmark() string {
	return lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true).Render(iconGhost + " Wisp Deck")
}

// headerRow assembles a switcher row: left-aligned content, optional
// right-aligned title, padded to fill the box width. A leading space sits
// between the left border and the content.
func (m *MainMenuModel) headerRow(content, title, leftBorder, rightBorder string) string {
	pad := menuContentWidth - lipgloss.Width(content) - lipgloss.Width(title) - 1 // -1 for leading space
	if pad < 1 {
		pad = 1
	}
	return leftBorder + " " + content + strings.Repeat(" ", pad) + title + rightBorder
}

// renderTitleRow renders the left-aligned AGENT tool chooser. It carries the
// right-aligned "Wisp Deck" wordmark only when no account or PLAN row sits above
// it; otherwise the wordmark lives on whichever of those is topmost.
func (m *MainMenuModel) renderTitleRow(leftBorder, rightBorder string) string {
	primaryStyle := lipgloss.NewStyle().Foreground(m.theme.Primary)

	aiDisplay := AIToolDisplayName(m.CurrentAITool())
	// Idle chevrons are neutral gray so they don't read as another accent. When
	// the AI switcher holds focus, the chevrons and name brighten so it reads as
	// the active focus stop driven by ←/→.
	chevronStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	nameStyle := primaryStyle
	if m.focus == FocusAI || m.isHovered(regionAI) {
		chevronStyle = lipgloss.NewStyle().Foreground(m.theme.Accent)
		nameStyle = lipgloss.NewStyle().Foreground(m.theme.Bright).Bold(true)
	}
	// The robot glyph captions the control so it reads as "this switches the agent".
	agentLabel := m.settingsCaption(iconAgent, m.focus == FocusAI || m.isHovered(regionAI))
	var aiPart string
	if len(m.aiTools) > 1 {
		aiPart = agentLabel + chevronStyle.Render(iconChevronLeft+" ") + nameStyle.Render(aiDisplay) + chevronStyle.Render(" "+iconChevronRight)
	} else {
		aiPart = agentLabel + nameStyle.Render(aiDisplay)
	}
	// AGENT switcher on the left; the wordmark right-aligns here only when there
	// is no account or PLAN row above to host it. The update chip rides just left
	// of whichever row carries the wordmark, so the header keeps one line of
	// identity instead of spending the spacer row below on a notice.
	var title string
	if m.accountRowCount() == 0 && m.subscriptionRowCount() == 0 {
		title = m.updateNoticePrefix() + m.ghostWordmark()
	} else {
		title = m.updateNotice()
	}
	return m.headerRow(aiPart, title, leftBorder, rightBorder)
}

// subscriptionRowCount is always 0: the top-of-page PLAN/subscription switcher
// was removed from the header. Choosing a backend now lives in Settings ›
// Subscription and the mid-session ledger pill / account popup — so the header
// no longer carries a PLAN row. Kept as a function (rather than deleting every
// caller) so all the header layout math still reads a single source of truth,
// mirroring accountRowCount.
func (m *MainMenuModel) subscriptionRowCount() int {
	return 0
}

// accountRowCount is always 0: the top-of-page account switcher was removed from
// the header. Account switching now lives only in the unified subscription
// modal, the always-available 'l' key, and the mid-session ledger pill — so the header no
// longer carries a LOGIN row. Kept as a function (rather than deleting every
// caller) so all the header layout math still reads a single source of truth.
func (m *MainMenuModel) accountRowCount() int {
	return 0
}

// renderAccountRow renders the active native Claude login, left-aligned at the
// very top of the header chrome — above the AGENT picker — mirroring the PLAN
// row's chevron/focus styling. The user-glyph caption is the same width as the
// rows below, so the chevrons line up vertically.
func (m *MainMenuModel) renderAccountRow(leftBorder, rightBorder string) string {
	label := m.CurrentClaudeAccountLabel()
	// The active login's name wears its own persistent account color (shared with
	// the statusline and the login panel). Without a color assigned we fall back
	// to the prior green/Default-primary convention. Focus/hover bolds the name
	// but keeps its identity color so you still see which login this is.
	var nameStyle lipgloss.Style
	if c, ok := m.accountColor(m.CurrentClaudeAccountDir()); ok {
		nameStyle = lipgloss.NewStyle().Foreground(c)
	} else if m.CurrentClaudeAccountDir() != "" {
		nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("114")) // green for non-Default
	} else {
		nameStyle = lipgloss.NewStyle().Foreground(m.theme.Primary) // orange for Default
	}
	if m.focus == FocusAccount || m.isHovered(regionAccount) {
		nameStyle = nameStyle.Bold(true)
	}

	acctLabel := m.settingsCaption(iconLogin, m.focus == FocusAccount || m.isHovered(regionAccount))
	// The row only renders when a managed account exists, so there is always a
	// choice to switch between (Default + the account) — the chevrons always show.
	chevronStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	if m.focus == FocusAccount || m.isHovered(regionAccount) {
		chevronStyle = lipgloss.NewStyle().Foreground(m.theme.Accent)
	}
	content := acctLabel + chevronStyle.Render(iconChevronLeft+" ") + nameStyle.Render(label) + chevronStyle.Render(" "+iconChevronRight)

	// This row only renders when accounts exist, so it is always the topmost
	// header row — it hosts the right-aligned "Wisp Deck" wordmark.
	return m.headerRow(content, m.ghostWordmark(), leftBorder, rightBorder)
}

// renderSubscriptionRow renders the current Claude subscription, left-aligned
// directly above the agent picker in the title row.
func (m *MainMenuModel) renderSubscriptionRow(leftBorder, rightBorder string) string {
	name := m.CurrentClaudeConfigName()
	var valColor lipgloss.Color
	if m.CurrentClaudeConfigFile() != "" {
		valColor = lipgloss.Color("114") // green when a custom subscription is active
	} else {
		valColor = m.theme.Primary // orange for Standard, mirroring the AGENT value
	}
	nameStyle := lipgloss.NewStyle().Foreground(valColor)
	// When this row holds focus, brighten the name so it reads as the active
	// ←/→ target, matching the AI switcher's focus treatment.
	if m.focus == FocusSubscription || m.isHovered(regionSubscription) {
		nameStyle = lipgloss.NewStyle().Foreground(m.theme.Bright).Bold(true)
	}

	// The crown glyph captions the subscription, mirroring the AGENT/LOGIN icons
	// around it so the three switcher chevrons line up vertically.
	planLabel := m.settingsCaption(iconPlan, m.focus == FocusSubscription || m.isHovered(regionSubscription))
	// Show cycle chevrons only when there is something to switch to. Idle chevrons
	// are neutral gray; they brighten only when this row holds focus.
	var content string
	if m.subscriptionFocusable() {
		chevronStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		if m.focus == FocusSubscription || m.isHovered(regionSubscription) {
			chevronStyle = lipgloss.NewStyle().Foreground(m.theme.Accent)
		}
		content = planLabel + chevronStyle.Render(iconChevronLeft+" ") + nameStyle.Render(name) + chevronStyle.Render(" "+iconChevronRight)
	} else {
		content = planLabel + nameStyle.Render(name)
	}

	// The PLAN switcher is the topmost header row unless an account row sits above
	// it, so it hosts the right-aligned wordmark in that case.
	var title string
	if m.accountRowCount() == 0 {
		title = m.ghostWordmark()
	}
	return m.headerRow(content, title, leftBorder, rightBorder)
}

// updateChipGap is the blank run between the update chip and the wordmark it
// sits left of. Two cells: enough that the chip's filled edge does not crowd the
// ghost glyph, tight enough that the pair still reads as one header cluster.
const updateChipGap = 2

// updateNoticePrefix is the chip plus that gap, ready to prepend to the
// wordmark. Empty when no update is pending, so the wordmark stays flush right.
// updateNoticeSpan derives the chip's clickable columns from the same two
// pieces, which is what keeps the hit box under the glyphs that were drawn.
func (m *MainMenuModel) updateNoticePrefix() string {
	notice := m.updateNotice()
	if notice == "" {
		return ""
	}
	return notice + strings.Repeat(" ", updateChipGap)
}

// updateNotice renders the right-aligned "new version available" chip with its
// inline Update action. Empty when no update is pending. It always sits on
// the row directly under whichever header row hosts the wordmark, so the eye
// falls from "Wisp Deck" straight onto the notice.
func (m *MainMenuModel) updateNotice() string {
	if m.updateVersion == "" {
		return ""
	}
	hovered := m.isHovered(regionUpdate)

	// One continuous fill, padded a cell on each side. Idle that fill is menuSurface
	// — the same stripe a selected project row sits on — so the chip reads as a
	// raised piece of the menu rather than a button bolted onto it. Nothing else
	// in this box is filled, and a solid block here would also stack a second loud
	// shape directly under the orange wordmark.
	fill := lipgloss.Color(menuSurface)
	// Inside, the parts speak the vocabulary already on screen: the arrow carries
	// the wordmark's Primary, the version reads like a project name, the separator
	// is the footer hint's dim "·", and "U Update" is styled exactly like the
	// action bar's "W Worktrees" — an unfilled key-plus-label in the accent.
	arrowInk := m.theme.Primary
	versionInk := lipgloss.Color("252")
	sepInk := lipgloss.Color("245")
	actionInk := m.theme.Accent
	if hovered {
		// Armed: the surface floods with the theme color and the text inverts to
		// near-black, which is the one ink legible on all three themes' Primary.
		// Flooding is the hover signal rather than a brighter fill because neither
		// Bright nor Accent is reliably lighter than Primary (codex's is darker).
		fill = m.theme.Primary
		ink := lipgloss.Color("234")
		arrowInk, versionInk, sepInk, actionInk = ink, ink, ink, ink
	}

	seg := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Background(fill).Foreground(c)
	}
	version := "v" + strings.TrimPrefix(m.updateVersion, "v")
	return seg(arrowInk).Render(" ⇡ ") +
		seg(versionInk).Render(version) +
		seg(sepInk).Render(" · ") +
		seg(actionInk).Bold(true).Render("U Update ")
}

// renderHeaderGapRow renders the blank spacer between the header switchers and
// the tab bar. The update chip shares the wordmark's row, so this stays empty.
func (m *MainMenuModel) renderHeaderGapRow(leftBorder, rightBorder string) string {
	return m.emptyMenuRow(leftBorder, rightBorder)
}

// renderProjectRows renders the leading blank row, every project (2 rows each)
// with their expanded worktree entries, and the trailing "+ Add project" row.
func (m *MainMenuModel) renderProjectRows(leftBorder, rightBorder string) []string {
	primaryStyle := lipgloss.NewStyle().Foreground(m.theme.Primary)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	neutralTextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	neutralDimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	moveFlashStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	deleteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	staleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	deleteDimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	selectedBgStyle := lipgloss.NewStyle().Background(lipgloss.Color(menuSurface))

	var rows []string

	// Empty line before items
	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder
	rows = append(rows, emptyRow)

	// Empty-state prompt when no projects exist.
	if len(m.projects) == 0 {
		msg := lipgloss.NewStyle().Foreground(m.theme.Dim).
			Render("No projects yet · press A to add")
		pad := (menuContentWidth - lipgloss.Width(msg)) / 2
		if pad < 0 {
			pad = 0
		}
		gap := menuContentWidth - pad - lipgloss.Width(msg)
		if gap < 0 {
			gap = 0
		}
		rows = append(rows, leftBorder+strings.Repeat(" ", pad)+msg+strings.Repeat(" ", gap)+rightBorder)
	}

	// Project items
	for i, proj := range m.projects {
		selected := func() bool {
			if m.deleteMode {
				return m.deleteSelected == m.projectToFlatIndex(i)
			}
			return m.selectedItem == m.projectToFlatIndex(i)
		}()
		flashing := !m.deleteMode && m.moveFlashIdx == i && m.moveFlashTimer > 0
		num := fmt.Sprintf("%d", i+1)

		var nameLine string
		var pathLine string

		shortPath := TruncateMiddle(shortenHomePath(proj.Path), menuContentWidth-7)

		// Worktree count indicator
		var wtIndicator string
		if len(proj.Worktrees) > 0 {
			wtCount := len(proj.Worktrees)
			wtWord := "worktrees"
			if wtCount == 1 {
				wtWord = "worktree"
			}
			wtIndicator = fmt.Sprintf("%d %s", wtCount, wtWord)
		}

		// Zero-worktree projects surface an inline add-worktree button in the
		// badge slot while the row is focused or hovered. Hidden once expanded —
		// the add-worktree row below is the affordance then — and in delete mode.
		showAddButton := len(proj.Worktrees) == 0 && !m.expandedWorktrees[i] && !m.deleteMode

		if selected {
			// Loud selection (Primary + wash) only when the body actually holds
			// focus. When focus is on the nav/AI/subscription, the row drops to a
			// faint neutral cursor marker with no wash, so it never competes with
			// the focused control for "selected" — but it still shows where the
			// cursor will resume.
			bodyFocus := m.focus == FocusBody
			selNameStyle := primaryBoldStyle
			selPathStyle := neutralDimStyle
			markerStyle := primaryBoldStyle
			wtStyle := primaryStyle
			washStyle := selectedBgStyle
			markerChar := "▌"
			if m.deleteMode {
				selNameStyle = deleteStyle
				selPathStyle = deleteDimStyle
				markerStyle = deleteStyle
				wtStyle = deleteStyle
				markerChar = "█" // full block for delete cursor, distinct from normal ▌ and tab bar ▐
			} else if flashing {
				selNameStyle = moveFlashStyle
				selPathStyle = moveFlashStyle
				markerStyle = moveFlashStyle
				wtStyle = moveFlashStyle
			} else if !bodyFocus {
				selNameStyle = neutralTextStyle
				selPathStyle = neutralDimStyle
				markerStyle = neutralDimStyle
				wtStyle = neutralDimStyle
				washStyle = lipgloss.NewStyle() // no background highlight off-focus
			}

			if showAddButton && bodyFocus && !flashing {
				wtIndicator = zeroWorktreeButtonLabel
			}

			marker := markerStyle.Render(markerChar)
			nameBudget := menuContentWidth - 8 - len(num)
			if wtIndicator != "" {
				// Leave room for the right-aligned badge plus its minimum gap.
				nameBudget -= lipgloss.Width(wtIndicator) + 1
			}
			truncName := TruncateMiddle(proj.Name, nameBudget)
			nameText := selNameStyle.Render(num + "  " + truncName)
			// " ▌1  name" -> space + marker + num + 2 spaces + name
			// For stale projects, replace the leading space with "⚠ " marker.
			var namePrefix string
			if proj.Stale && !m.deleteMode {
				namePrefix = staleStyle.Render("⚠")
			} else {
				namePrefix = " "
			}
			nameContent := namePrefix + marker + nameText

			if wtIndicator != "" {
				// On the loud (body-focused) selection the badge joins the row's
				// Primary; off-focus it goes neutral with the rest of the row.
				wtStyled := wtStyle.Render(wtIndicator)
				gap := menuContentWidth - lipgloss.Width(nameContent) - lipgloss.Width(wtStyled)
				if gap < 1 {
					gap = 1
				}
				if m.deleteMode {
					nameLine = leftBorder + nameContent + strings.Repeat(" ", gap) + wtStyled + rightBorder
				} else {
					nameLine = leftBorder + washStyle.Render(nameContent+strings.Repeat(" ", gap)) + wtStyled + rightBorder
				}
			} else {
				namePadding := menuContentWidth - lipgloss.Width(nameContent)
				if namePadding < 0 {
					namePadding = 0
				}
				if m.deleteMode {
					nameLine = leftBorder + nameContent + strings.Repeat(" ", namePadding) + rightBorder
				} else {
					nameLine = leftBorder + washStyle.Render(nameContent+strings.Repeat(" ", namePadding)) + rightBorder
				}
			}

			pathContent := "      " + selPathStyle.Render(shortPath)
			pathPadding := menuContentWidth - lipgloss.Width(pathContent)
			if pathPadding < 0 {
				pathPadding = 0
			}
			if m.deleteMode {
				pathLine = leftBorder + pathContent + strings.Repeat(" ", pathPadding) + rightBorder
			} else {
				pathLine = leftBorder + washStyle.Render(pathContent+strings.Repeat(" ", pathPadding)) + rightBorder
			}
		} else {
			// Choose style: amber flash when recently moved, neutral otherwise.
			rowNameStyle := neutralTextStyle
			rowDimStyle := neutralDimStyle
			if flashing {
				rowNameStyle = moveFlashStyle
				rowDimStyle = moveFlashStyle
			}

			// A hovered-but-unselected project gets a faint wash so the pointer
			// target is visible without competing with the selection cursor.
			rowWash := lipgloss.NewStyle()
			if !flashing && m.isHovered(regionBody) && m.hover.index == m.projectToFlatIndex(i) {
				rowWash = selectedBgStyle
				if showAddButton {
					wtIndicator = zeroWorktreeButtonLabel
				}
			}

			numText := rowDimStyle.Render(num)
			nameBudget := menuContentWidth - 6 - len(num)
			if wtIndicator != "" {
				// Leave room for the right-aligned badge plus its minimum gap.
				nameBudget -= lipgloss.Width(wtIndicator) + 1
			}
			truncName := TruncateMiddle(proj.Name, nameBudget)
			nameText := rowNameStyle.Render(truncName)
			// For stale projects, replace the leading 2 spaces with "⚠ " marker.
			var rowPrefix string
			if proj.Stale {
				rowPrefix = staleStyle.Render("⚠") + " "
			} else {
				rowPrefix = "  "
			}
			nameContent := rowPrefix + "  " + numText + "  " + nameText

			if wtIndicator != "" {
				wtStyled := rowDimStyle.Render(wtIndicator)
				gap := menuContentWidth - lipgloss.Width(nameContent) - lipgloss.Width(wtStyled)
				if gap < 1 {
					gap = 1
				}
				nameLine = leftBorder + rowWash.Render(nameContent+strings.Repeat(" ", gap)) + wtStyled + rightBorder
			} else {
				namePadding := menuContentWidth - lipgloss.Width(nameContent)
				if namePadding < 0 {
					namePadding = 0
				}
				nameLine = leftBorder + rowWash.Render(nameContent+strings.Repeat(" ", namePadding)) + rightBorder
			}

			pathContent := "       " + rowDimStyle.Render(shortPath)
			pathPadding := menuContentWidth - lipgloss.Width(pathContent)
			if pathPadding < 0 {
				pathPadding = 0
			}
			pathLine = leftBorder + rowWash.Render(pathContent+strings.Repeat(" ", pathPadding)) + rightBorder
		}

		rows = append(rows, nameLine)
		rows = append(rows, pathLine)

		// Expanded worktree entries (2 rows each: branch + path) + add-worktree (1 row)
		if m.expandedWorktrees[i] {
			// All worktrees use ├─ connector (add-worktree follows as last item)
			connector := "├─"
			for j, wt := range proj.Worktrees {
				wtFlatIdx := m.projectToFlatIndex(i) + 1 + j
				wtSelected := !m.deleteMode && m.selectedItem == wtFlatIdx
				wtDeleteSelected := m.deleteMode && m.deleteSelected == wtFlatIdx
				var wtBranchLine, wtPathLine string
				branchDisplay := TruncateMiddle(wt.Branch, menuContentWidth-11)
				shortWtPath := TruncateMiddle(shortenHomePath(wt.Path), menuContentWidth-11)

				if wtDeleteSelected {
					marker := deleteStyle.Render("█")
					connStyled := deleteStyle.Render(connector)
					branchText := deleteStyle.Render(branchDisplay)
					content := "     " + marker + " " + connStyled + " " + branchText
					padding := menuContentWidth - lipgloss.Width(content)
					if padding < 0 {
						padding = 0
					}
					wtBranchLine = leftBorder + content + strings.Repeat(" ", padding) + rightBorder

					pathContent := "          " + deleteDimStyle.Render(shortWtPath)
					pathPadding := menuContentWidth - lipgloss.Width(pathContent)
					if pathPadding < 0 {
						pathPadding = 0
					}
					wtPathLine = leftBorder + pathContent + strings.Repeat(" ", pathPadding) + rightBorder
				} else if wtSelected {
					marker := primaryBoldStyle.Render("▌")
					connStyled := primaryBoldStyle.Render(connector)
					branchText := primaryBoldStyle.Render(branchDisplay)
					content := "    " + marker + connStyled + " " + branchText
					padding := menuContentWidth - lipgloss.Width(content)
					if padding < 0 {
						padding = 0
					}
					wtBranchLine = leftBorder + selectedBgStyle.Render(content+strings.Repeat(" ", padding)) + rightBorder

					pathContent := "         " + primaryStyle.Render(shortWtPath)
					pathPadding := menuContentWidth - lipgloss.Width(pathContent)
					if pathPadding < 0 {
						pathPadding = 0
					}
					wtPathLine = leftBorder + selectedBgStyle.Render(pathContent+strings.Repeat(" ", pathPadding)) + rightBorder
				} else {
					wtWash := lipgloss.NewStyle()
					if m.isHovered(regionBody) && m.hover.index == wtFlatIdx {
						wtWash = selectedBgStyle
					}
					connStyled := neutralDimStyle.Render(connector)
					branchText := neutralTextStyle.Render(branchDisplay)
					content := "       " + connStyled + " " + branchText
					padding := menuContentWidth - lipgloss.Width(content)
					if padding < 0 {
						padding = 0
					}
					wtBranchLine = leftBorder + wtWash.Render(content+strings.Repeat(" ", padding)) + rightBorder

					pathContent := "          " + neutralDimStyle.Render(shortWtPath)
					pathPadding := menuContentWidth - lipgloss.Width(pathContent)
					if pathPadding < 0 {
						pathPadding = 0
					}
					wtPathLine = leftBorder + wtWash.Render(pathContent+strings.Repeat(" ", pathPadding)) + rightBorder
				}
				rows = append(rows, wtBranchLine)
				rows = append(rows, wtPathLine)
			}

			// Add-worktree item (1 row, └─ connector)
			addWtFlatIdx := m.projectToFlatIndex(i) + 1 + len(proj.Worktrees)
			addWtSelected := m.selectedItem == addWtFlatIdx
			addConnector := "└─"
			var addWtLine string
			if addWtSelected {
				marker := primaryBoldStyle.Render("▌")
				connStyled := primaryBoldStyle.Render(addConnector)
				addLabel := primaryBoldStyle.Render("+ Add worktree")
				content := "    " + marker + connStyled + " " + addLabel
				padding := menuContentWidth - lipgloss.Width(content)
				if padding < 0 {
					padding = 0
				}
				addWtLine = leftBorder + selectedBgStyle.Render(content+strings.Repeat(" ", padding)) + rightBorder
			} else {
				addWtWash := lipgloss.NewStyle()
				if m.isHovered(regionBody) && m.hover.index == addWtFlatIdx {
					addWtWash = selectedBgStyle
				}
				connStyled := neutralDimStyle.Render(addConnector)
				addLabel := neutralDimStyle.Render("+ Add worktree")
				content := "       " + connStyled + " " + addLabel
				padding := menuContentWidth - lipgloss.Width(content)
				if padding < 0 {
					padding = 0
				}
				addWtLine = leftBorder + addWtWash.Render(content+strings.Repeat(" ", padding)) + rightBorder
			}
			rows = append(rows, addWtLine)
		}
	}

	// Blank spacer row before the add-project row.
	rows = append(rows, emptyRow)

	// "+ Add project" row — the final selectable item.
	addStyle := lipgloss.NewStyle().Foreground(m.theme.Text)
	addSel := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	label := "+  Add project"
	var prefix string
	addSelected := func() bool {
		t, _, _ := m.ResolveItem(m.selectedItem)
		return t == "add-project" && !m.deleteMode
	}()
	// The add-project row is the final flat item, so it can be hover-washed like
	// the project rows above when the pointer is over it but it isn't selected.
	addHovered := !addSelected && !m.deleteMode &&
		m.isHovered(regionBody) && m.hover.index == m.TotalItems()-1
	addWash := lipgloss.NewStyle()
	if addHovered {
		addWash = selectedBgStyle
	}
	if addSelected {
		prefix = " " + addSel.Render("▌") + addSel.Render(label)
	} else {
		prefix = "   " + addStyle.Render(label)
	}
	gap := menuContentWidth - lipgloss.Width(prefix)
	if gap < 0 {
		gap = 0
	}
	if addSelected {
		rows = append(rows, leftBorder+selectedBgStyle.Render(prefix+strings.Repeat(" ", gap))+rightBorder)
	} else {
		rows = append(rows, leftBorder+addWash.Render(prefix+strings.Repeat(" ", gap))+rightBorder)
	}

	// Hint subtitle under the label (mirrors the project path subtitle) so the
	// row explains what it does instead of just repeating "Add project".
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	var hintContent string
	if addSelected {
		hintContent = "      " + hintStyle.Render(addProjectHint)
	} else {
		hintContent = "       " + hintStyle.Render(addProjectHint)
	}
	hintGap := menuContentWidth - lipgloss.Width(hintContent)
	if hintGap < 0 {
		hintGap = 0
	}
	if addSelected {
		rows = append(rows, leftBorder+selectedBgStyle.Render(hintContent+strings.Repeat(" ", hintGap))+rightBorder)
	} else {
		rows = append(rows, leftBorder+addWash.Render(hintContent+strings.Repeat(" ", hintGap))+rightBorder)
	}

	return rows
}

// addProjectHint is the subtitle shown under the "+ Add project" row, mirroring
// the path subtitle on real project rows.
const addProjectHint = "Register a folder to launch dev sessions in"

// renderHelpRow renders the centered footer hint line. It sits below the box
// (after the bottom border) and is centered to the full box width.
func (m *MainMenuModel) renderHelpRow() string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))

	sep := dimStyle.Render(" · ")
	var helpContent string
	if m.deleteMode {
		helpContent = helpStyle.Render("↑↓ navigate") + sep + helpStyle.Render("1-9 jump") + sep + helpStyle.Render("⏎ delete") + sep + helpStyle.Render("Q cancel")
	} else if m.showEscHint {
		helpContent = helpStyle.Render("Press Esc again to quit")
	} else {
		helpContent = helpStyle.Render(m.focusHint())
	}
	// Center within the full box width (inner + 2 border columns), padding the
	// line out to that width on both sides. Right-padding matters: the help row
	// is centered by lipgloss.Place against the widest content line (the box
	// border). A short line gets extra left margin (short/2), shifting the hints
	// right of box-center when no ghost pads the row. A full-width line keeps it
	// centered relative to the box.
	boxWidth := menuInnerWidth + 2
	helpWidth := lipgloss.Width(helpContent)
	helpLeft := (boxWidth - helpWidth) / 2
	if helpLeft < 0 {
		helpLeft = 0
	}
	helpRight := boxWidth - helpLeft - helpWidth
	if helpRight < 0 {
		helpRight = 0
	}
	return strings.Repeat(" ", helpLeft) + helpContent + strings.Repeat(" ", helpRight)
}

// focusHint returns the footer hint line for the current focus region and tab.
// The hints teach the focus ring: ↑/↓ move between AI switcher, tab bar, and
// body; ←/→ act on whatever is focused.
func (m *MainMenuModel) focusHint() string {
	switch m.focus {
	case FocusAccount:
		return "←→ switch login · ↵ manage · ↓ subscription"
	case FocusSubscription:
		return "←→ switch subscription · ↓ agent"
	case FocusAI:
		return "←→ switch agent · ↓ sections"
	case FocusTabs:
		return "←→ switch section · ↑ agent · ↓ enter"
	default: // FocusBody
		switch m.activeTab {
		case TabSettings:
			return "↑↓ move · ←→ change · ↵ edit · ↑ sections"
		case TabStats:
			return "↑↓ scroll · ←→ full/compact · ↑ sections"
		default: // projects
			return "↑↓ move · ↑ sections · N worktree · O open once · P plain · L login"
		}
	}
}

// renderMenuBox renders the full Projects-tab box: chrome borders, title row,
// tab bar, project list, the add-project row, the contextual action bar, and
// the footer help row. Overflow is handled by applyMenuScroll.
func (m *MainMenuModel) renderMenuBox() string {
	staleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	neutralDimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	top, separator, bottom, leftBorder, rightBorder := m.boxBorders()

	var lines []string

	// Top border
	lines = append(lines, top)

	// Native login account switcher, at the very top above the agent picker
	// (shown only once managed accounts exist).
	if m.accountRowCount() > 0 {
		lines = append(lines, m.renderAccountRow(leftBorder, rightBorder))
	}

	// Current subscription, above the agent picker (Claude only)
	if m.subscriptionRowCount() > 0 {
		lines = append(lines, m.renderSubscriptionRow(leftBorder, rightBorder))
	}

	// Title row
	lines = append(lines, m.renderTitleRow(leftBorder, rightBorder))

	// Blank spacer separating the agent/plan switchers from the tab bar. It
	// hosts the update notice when the wordmark sits on the title row above.
	lines = append(lines, m.renderHeaderGapRow(leftBorder, rightBorder))

	// Tab bar
	lines = append(lines, m.renderTabBar(leftBorder, rightBorder))

	// Separator after the tab bar
	lines = append(lines, separator)

	// Project rows (leading blank + projects/worktrees + add-project row)
	lines = append(lines, m.renderProjectRows(leftBorder, rightBorder)...)

	// Feedback message (if any)
	if m.feedbackMsg != "" {
		var feedbackColor lipgloss.Color
		if m.feedbackStyle == "success" {
			feedbackColor = lipgloss.Color("114") // green
		} else {
			feedbackColor = lipgloss.Color("220") // yellow
		}
		fStyle := lipgloss.NewStyle().Foreground(feedbackColor)
		fbContent := "  " + fStyle.Render(m.feedbackMsg)
		fbPadding := menuContentWidth - lipgloss.Width(fbContent)
		if fbPadding < 0 {
			fbPadding = 0
		}
		lines = append(lines, leftBorder+fbContent+strings.Repeat(" ", fbPadding)+rightBorder)
	}

	// Stale confirmation prompt (if active)
	if m.staleConfirmIdx >= 0 && m.staleConfirmIdx < len(m.projects) {
		stalePath := m.projects[m.staleConfirmIdx].Path
		warnLine := staleStyle.Render("⚠") + " " + staleStyle.Render("Path not found: "+stalePath)
		warnContent := "  " + warnLine
		warnPadding := menuContentWidth - lipgloss.Width(warnContent)
		if warnPadding < 0 {
			warnPadding = 0
		}
		lines = append(lines, leftBorder+warnContent+strings.Repeat(" ", warnPadding)+rightBorder)

		promptLine := neutralDimStyle.Render("  Launch anyway? [y/N]")
		promptPadding := menuContentWidth - lipgloss.Width(promptLine)
		if promptPadding < 0 {
			promptPadding = 0
		}
		lines = append(lines, leftBorder+promptLine+strings.Repeat(" ", promptPadding)+rightBorder)
	}

	// Separator before the contextual action bar
	lines = append(lines, separator)

	// Contextual action bar reflecting the selected row
	lines = append(lines, m.renderActionBar(leftBorder, rightBorder))

	// Bottom border
	lines = append(lines, bottom)

	// Help row
	lines = append(lines, m.renderHelpRow())

	// Scroll clipping when menu is taller than the available terminal height.
	// Fixed header = top + title + switcher-gap + tab-bar + sep + leading-blank (6),
	// plus the optional subscription row. The update notice reuses an existing
	// header row, so it never adds a line.
	headerEnd := 6 + m.subscriptionRowCount() + m.accountRowCount()
	// Footer = separator-before-action + action-bar + bottom + help (4 lines).
	// Keeping the separator in the footer ensures the action bar never renders
	// detached when the body is clipped at tiny terminal heights.
	footerStart := len(lines) - 4
	avail := m.availableMenuHeight()
	if avail > 0 && len(lines) > avail && headerEnd < footerStart {
		lines = m.applyMenuScroll(lines, headerEnd, footerStart, avail)
	}

	return strings.Join(lines, "\n")
}
