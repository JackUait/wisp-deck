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
)

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

// actionBarFor returns the contextual action label text for a selected row type.
// hasWorktrees is consulted for project rows: the "W Worktrees" action only does
// something when the project actually has worktrees, so it is hidden otherwise.
func actionBarFor(itemType string, hasWorktrees bool) string {
	// Labels double as a keymap: the leading glyph/letter is the real keybinding
	// (Enter opens, W toggles worktrees, D deletes — see handleRune).
	switch itemType {
	case "project":
		if hasWorktrees {
			return "⏎ Open    W Worktrees    D Delete"
		}
		return "⏎ Open    D Delete"
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
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	hasWorktrees := itemType == "project" && projectIdx >= 0 && projectIdx < len(m.projects) && len(m.projects[projectIdx].Worktrees) > 0
	text := actionBarFor(itemType, hasWorktrees)
	rendered := style.Render(text)
	gap := menuContentWidth - lipgloss.Width(rendered) - 2
	if gap < 0 {
		gap = 0
	}
	return leftBorder + "  " + rendered + strings.Repeat(" ", gap) + rightBorder
}

// renderTitleRow renders the left-aligned AGENT tool chooser + right-aligned "Ghost Tab" row.
func (m *MainMenuModel) renderTitleRow(leftBorder, rightBorder string) string {
	primaryStyle := lipgloss.NewStyle().Foreground(m.theme.Primary)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)

	title := primaryBoldStyle.Render("Ghost Tab")
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
		aiPart = agentLabel + chevronStyle.Render("◄ ") + nameStyle.Render(aiDisplay) + chevronStyle.Render(" ►")
	} else {
		aiPart = agentLabel + nameStyle.Render(aiDisplay)
	}
	// Switcher on the left, "Ghost Tab" right-aligned: "AGENT ◄ Claude Code ►" left, "Ghost Tab" right
	aiPadding := menuContentWidth - lipgloss.Width(title) - lipgloss.Width(aiPart) - 1 // -1 for leading space
	if aiPadding < 1 {
		aiPadding = 1
	}
	return leftBorder + " " + aiPart + strings.Repeat(" ", aiPadding) + title + rightBorder
}

// subscriptionRowCount returns 1 when the PLAN/subscription line is shown, else
// 0. The row renders in the header chrome of every tab; layout math (height,
// scroll header, click mapping) all add this so the body rows stay aligned.
func (m *MainMenuModel) subscriptionRowCount() int {
	if m.ClaudeConfigVisible() {
		return 1
	}
	return 0
}

// accountRowCount returns 1 when the LOGIN/account line is shown, else 0. The
// row appears only once the user has a real choice — i.e. at least one managed
// account beyond the implicit Default login — so a single-account user sees no
// extra row. When shown it renders in every tab's header chrome, so all layout
// math (height, scroll header, click mapping) adds it to keep body rows aligned.
func (m *MainMenuModel) accountRowCount() int {
	if len(m.claudeAccounts) > 0 {
		return 1
	}
	return 0
}

// renderAccountRow renders the active native Claude login, left-aligned at the
// very top of the header chrome — above the AGENT picker — mirroring the PLAN
// row's chevron/focus styling. The user-glyph caption is the same width as the
// rows below, so the chevrons line up vertically.
func (m *MainMenuModel) renderAccountRow(leftBorder, rightBorder string) string {
	label := m.CurrentClaudeAccountLabel()
	var valColor lipgloss.Color
	if m.CurrentClaudeAccountDir() != "" {
		valColor = lipgloss.Color("114") // green when a non-Default account is active
	} else {
		valColor = m.theme.Primary // orange for Default, mirroring the AGENT value
	}
	nameStyle := lipgloss.NewStyle().Foreground(valColor)
	if m.focus == FocusAccount || m.isHovered(regionAccount) {
		nameStyle = lipgloss.NewStyle().Foreground(m.theme.Bright).Bold(true)
	}

	acctLabel := m.settingsCaption(iconLogin, m.focus == FocusAccount || m.isHovered(regionAccount))
	// The row only renders when a managed account exists, so there is always a
	// choice to switch between (Default + the account) — the chevrons always show.
	chevronStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	if m.focus == FocusAccount || m.isHovered(regionAccount) {
		chevronStyle = lipgloss.NewStyle().Foreground(m.theme.Accent)
	}
	content := acctLabel + chevronStyle.Render("◄ ") + nameStyle.Render(label) + chevronStyle.Render(" ►")

	pad := menuContentWidth - lipgloss.Width(content) - 1 // -1 for leading space
	if pad < 1 {
		pad = 1
	}
	return leftBorder + " " + content + strings.Repeat(" ", pad) + rightBorder
}

// renderSubscriptionRow renders the current Claude subscription, left-aligned
// directly beneath the agent picker in the title row.
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
	// above it so the three switcher chevrons line up vertically.
	planLabel := m.settingsCaption(iconPlan, m.focus == FocusSubscription || m.isHovered(regionSubscription))
	// Show cycle chevrons only when there is something to switch to. Idle chevrons
	// are neutral gray; they brighten only when this row holds focus.
	var content string
	if m.subscriptionFocusable() {
		chevronStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		if m.focus == FocusSubscription || m.isHovered(regionSubscription) {
			chevronStyle = lipgloss.NewStyle().Foreground(m.theme.Accent)
		}
		content = planLabel + chevronStyle.Render("◄ ") + nameStyle.Render(name) + chevronStyle.Render(" ►")
	} else {
		content = planLabel + nameStyle.Render(name)
	}

	pad := menuContentWidth - lipgloss.Width(content) - 1 // -1 for leading space
	if pad < 1 {
		pad = 1
	}
	// Left-aligned so the PLAN switcher sits directly under the AGENT switcher.
	return leftBorder + " " + content + strings.Repeat(" ", pad) + rightBorder
}

// renderUpdateRow renders the "Update available" notification row.
func (m *MainMenuModel) renderUpdateRow(leftBorder, rightBorder string) string {
	updateStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	updateMsg := fmt.Sprintf("Update available: %s (brew upgrade ghost-tab)", m.updateVersion)
	updateContent := updateStyle.Render(updateMsg)
	updatePadding := menuContentWidth - lipgloss.Width(updateContent) - 2 // leading 2 spaces
	if updatePadding < 0 {
		updatePadding = 0
	}
	return leftBorder + "  " + updateContent + strings.Repeat(" ", updatePadding) + rightBorder
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
	selectedBgStyle := lipgloss.NewStyle().Background(lipgloss.Color("236"))

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

			marker := markerStyle.Render(markerChar)
			truncName := TruncateMiddle(proj.Name, menuContentWidth-8-len(num))
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
			}

			numText := rowDimStyle.Render(num)
			truncName := TruncateMiddle(proj.Name, menuContentWidth-6-len(num))
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
	// Center within the full box width (inner + 2 border columns).
	boxWidth := menuInnerWidth + 2
	helpWidth := lipgloss.Width(helpContent)
	helpLeft := (boxWidth - helpWidth) / 2
	if helpLeft < 0 {
		helpLeft = 0
	}
	return strings.Repeat(" ", helpLeft) + helpContent
}

// focusHint returns the footer hint line for the current focus region and tab.
// The hints teach the focus ring: ↑/↓ move between AI switcher, tab bar, and
// body; ←/→ act on whatever is focused.
func (m *MainMenuModel) focusHint() string {
	switch m.focus {
	case FocusAccount:
		return "←→ switch login · ↵ manage · ↓ agent"
	case FocusAI:
		return "←→ switch agent · ↓ sections"
	case FocusSubscription:
		return "←→ switch subscription · ↑ agent · ↓ sections"
	case FocusTabs:
		return "←→ switch section · ↑ agent · ↓ enter"
	default: // FocusBody
		switch m.activeTab {
		case TabSettings:
			return "↑↓ move · ←→ change · ↵ edit · ↑ sections"
		case TabStats:
			return "↑↓ scroll · ↑ sections"
		default: // projects
			return "↑↓ move · ↵ open · ↑ sections · O open once · P plain · L login"
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

	// Title row
	lines = append(lines, m.renderTitleRow(leftBorder, rightBorder))

	// Current subscription, under the agent picker (Claude only)
	if m.subscriptionRowCount() > 0 {
		lines = append(lines, m.renderSubscriptionRow(leftBorder, rightBorder))
	}

	// Blank spacer separating the agent/plan switchers from the tab bar.
	lines = append(lines, m.emptyMenuRow(leftBorder, rightBorder))

	// Tab bar
	lines = append(lines, m.renderTabBar(leftBorder, rightBorder))

	// Separator after the tab bar
	lines = append(lines, separator)

	// Update notification (if set)
	if m.updateVersion != "" {
		lines = append(lines, m.renderUpdateRow(leftBorder, rightBorder))
	}

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
	// plus the optional subscription and update rows.
	headerEnd := 6 + m.subscriptionRowCount() + m.accountRowCount()
	if m.updateVersion != "" {
		headerEnd++
	}
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
