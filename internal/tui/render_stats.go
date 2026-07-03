package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/usage"
)

// renderStatsRows renders the stats content as a flat slice of box rows. It is the
// concatenation of the three groups returned by statsRowGroups; retained so callers
// (and tests) that want the whole content block keep working.
func (m *MainMenuModel) renderStatsRows(leftBorder, rightBorder string) []string {
	header, body, footer := m.statsRowGroups(leftBorder, rightBorder)
	rows := make([]string, 0, len(header)+len(body)+len(footer))
	rows = append(rows, header...)
	rows = append(rows, body...)
	rows = append(rows, footer...)
	return rows
}

// statsRowGroups splits the stats content into a fixed header (column labels), a
// scrollable body (one group of rows per month), and a fixed footer (grand totals).
// Only the body is scrolled when the tab is taller than the terminal, so the column
// headers and the grand-total line stay pinned. Non-data states (loading, error,
// empty) put everything in the header group with no body to scroll.
func (m *MainMenuModel) statsRowGroups(leftBorder, rightBorder string) (headerRows, bodyRows, footerRows []string) {
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	faint := lipgloss.NewStyle().Faint(true)
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203"))

	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder

	// Helper: render a single padded text row inside the box.
	textRow := func(s string) string {
		gap := menuContentWidth - lipgloss.Width(s) - 2
		if gap < 0 {
			gap = 0
		}
		return leftBorder + "  " + s + strings.Repeat(" ", gap) + rightBorder
	}

	// Helper: render a label-left / value-right row inside the box. The value's
	// right edge lands on menuContentWidth so it shares the Total-tokens column.
	itemRow := func(label, value string, labelStyle, valStyle lipgloss.Style) string {
		labelRendered := labelStyle.Render(label)
		valRendered := valStyle.Render(value)
		prefix := "  " + labelRendered
		gap := menuContentWidth - lipgloss.Width(prefix) - lipgloss.Width(valRendered)
		if gap < 1 {
			gap = 1
		}
		return leftBorder + prefix + strings.Repeat(" ", gap) + valRendered + rightBorder
	}

	headerRows = append(headerRows, emptyRow)

	// Loading state.
	if m.statsLoading {
		headerRows = append(headerRows, textRow(primaryBoldStyle.Render("Crunching token usage…")))
		headerRows = append(headerRows, emptyRow)
		headerRows = append(headerRows, textRow(faint.Render("Usage data is read from ~/.claude/usage/")))
		headerRows = append(headerRows, emptyRow)
		return headerRows, nil, nil
	}

	// Error state.
	if m.statsErr != nil {
		headerRows = append(headerRows, textRow(errStyle.Render("Failed to load usage: "+m.statsErr.Error())))
		headerRows = append(headerRows, emptyRow)
		return headerRows, nil, nil
	}

	// Empty state (loaded but no data).
	if len(m.statsMonths) == 0 {
		headerRows = append(headerRows, textRow(muted.Render("No usage data found yet.")))
		headerRows = append(headerRows, emptyRow)
		headerRows = append(headerRows, textRow(faint.Render("Usage data is read from ~/.claude/usage/")))
		headerRows = append(headerRows, emptyRow)
		return headerRows, nil, nil
	}

	// Column header row.
	header := lipgloss.NewStyle().Foreground(m.theme.Dim).Bold(true)
	hdr := "  " + header.Render(fmt.Sprintf("%-8s %7s   %7s   %7s   %7s         %9s",
		"Month", "Input", "Output", "Cache W", "Cache R", "Total"))
	hdrGap := menuContentWidth - lipgloss.Width(hdr)
	if hdrGap < 0 {
		hdrGap = 0
	}
	headerRows = append(headerRows, leftBorder+hdr+strings.Repeat(" ", hdrGap)+rightBorder)

	// Separator
	sepRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder
	headerRows = append(headerRows, sepRow)

	// Every month is rendered into the scrollable body; the visible slice is
	// chosen later by applyStatsScroll based on the terminal height budget.
	grandTotal := statsGrandTotal(m.statsMonths)
	allTotal := grandTotal.Total()
	if allTotal < 1 {
		allTotal = 1
	}

	for i, mu := range m.statsMonths {
		// Blank spacer between months so one month's per-model rows don't run
		// straight into the next month's header.
		if i > 0 {
			bodyRows = append(bodyRows, emptyRow)
		}

		frac := float64(mu.Total()) / float64(allTotal)
		pct := int(frac*100 + 0.5)
		monthCost, allPriced := mu.CostUSD()
		costStr := dollarFmt(monthCost)
		if !allPriced {
			costStr = "~" + costStr
		}

		dataLine := "  " + numStyle.Render(fmt.Sprintf("%-8s %7s   %7s   %7s   %7s",
			monthLabel(mu.Month),
			humanizeTokens(mu.Input),
			humanizeTokens(mu.Output),
			humanizeTokens(mu.CacheWrite),
			humanizeTokens(mu.CacheRead))) + "         " +
			primaryBoldStyle.Render(fmt.Sprintf("%9s", humanizeTokens(mu.Total())))
		dataGap := menuContentWidth - lipgloss.Width(dataLine)
		if dataGap < 0 {
			dataGap = 0
		}
		bodyRows = append(bodyRows, leftBorder+dataLine+strings.Repeat(" ", dataGap)+rightBorder)

		// Bar + percent + cost on line below the data.
		gaugeStr := statsGauge(frac, lipgloss.NewStyle().Foreground(m.theme.Primary), dimStyle)
		barLine := "  " + gaugeStr + " " + faint.Render(fmt.Sprintf("%3d%%", pct))
		costPad := menuContentWidth - lipgloss.Width(barLine) - lipgloss.Width(costStr)
		if costPad < 1 {
			costPad = 1
		}
		barRow := leftBorder + barLine + strings.Repeat(" ", costPad) + primaryBoldStyle.Render(costStr) + rightBorder
		bodyRows = append(bodyRows, barRow)

		// Per-model breakdown: which models drove the month's spend. Drawn as a tree
		// hanging off the month (├─ for each model, └─ for the last) and muted so the
		// rows read as children of the month, not new months.
		branchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		for j, md := range mu.Models {
			connector := "├─"
			if j == len(mu.Models)-1 {
				connector = "└─"
			}
			label := strings.TrimPrefix(md.Model, "claude-")
			if len(label) > 16 {
				label = label[:15] + "…"
			}
			usd, priced := usage.ModelCostUSD(md)
			modelCost := "—"
			if priced {
				modelCost = dollarFmt(usd)
			}
			modelLine := "  " + branchStyle.Render(connector) + " " + muted.Render(fmt.Sprintf("%-16s %8s", label, humanizeTokens(md.Total())))
			modelPad := menuContentWidth - lipgloss.Width(modelLine) - lipgloss.Width(modelCost)
			if modelPad < 1 {
				modelPad = 1
			}
			bodyRows = append(bodyRows, leftBorder+modelLine+strings.Repeat(" ", modelPad)+dimStyle.Render(modelCost)+rightBorder)
		}
	}

	// Grand total row.
	footerRows = append(footerRows, sepRow)
	g := grandTotal
	grandCost := 0.0
	grandAllPriced := true
	for _, mu := range m.statsMonths {
		c, ap := mu.CostUSD()
		grandCost += c
		if !ap {
			grandAllPriced = false
		}
	}
	grandCostStr := dollarFmt(grandCost)
	if !grandAllPriced {
		grandCostStr = "~" + grandCostStr
	}

	footerRows = append(footerRows, itemRow("Total", humanizeTokens(g.Total()), primaryBoldStyle, primaryBoldStyle))
	footerRows = append(footerRows, itemRow("Est. cost", grandCostStr, header, primaryBoldStyle))
	footerRows = append(footerRows, emptyRow)

	return headerRows, bodyRows, footerRows
}

// renderStatsBox renders the Stats tab: shared chrome (top border + title row +
// tab bar + separator) followed by stats content rows + bottom border + help row.
func (m *MainMenuModel) renderStatsBox() string {
	top, separator, bottom, leftBorder, rightBorder := m.boxBorders()

	var lines []string
	lines = append(lines, top)
	if m.accountRowCount() > 0 {
		lines = append(lines, m.renderAccountRow(leftBorder, rightBorder))
	}
	lines = append(lines, m.renderTitleRow(leftBorder, rightBorder))
	// PLAN switcher mirrors the Projects header so the chrome lines up on every tab.
	if m.subscriptionRowCount() > 0 {
		lines = append(lines, m.renderSubscriptionRow(leftBorder, rightBorder))
	}
	lines = append(lines, m.emptyMenuRow(leftBorder, rightBorder))
	lines = append(lines, m.renderTabBar(leftBorder, rightBorder))
	lines = append(lines, separator)

	statsHeader, statsBody, statsFooter := m.statsRowGroups(leftBorder, rightBorder)
	// Index of the first scrollable body line and the first line past it, within
	// the full box. Only the month body between the column headers and the grand
	// total is scrolled.
	bodyStart := len(lines) + len(statsHeader)
	bodyEnd := bodyStart + len(statsBody)

	lines = append(lines, statsHeader...)
	lines = append(lines, statsBody...)
	lines = append(lines, statsFooter...)
	lines = append(lines, bottom)
	lines = append(lines, m.renderHelpRow())

	lines = m.applyStatsScroll(lines, bodyStart, bodyEnd)
	return strings.Join(lines, "\n")
}

// applyStatsScroll clips the scrollable month body (lines[bodyStart:bodyEnd]) to the
// terminal's height budget, driven by m.statsOffset (a line offset). The column
// headers above and the grand-total footer below stay pinned; ▲/▼ indicators mark
// hidden content. It also records the largest valid offset in m.statsMaxOffset so
// the scroll handlers can bound themselves, and clamps a stale offset into range.
func (m *MainMenuModel) applyStatsScroll(lines []string, bodyStart, bodyEnd int) []string {
	avail := m.availableMenuHeight()
	body := bodyEnd - bodyStart
	// Rows outside the body (chrome, column headers, footer, borders, help) are
	// always shown; the body gets whatever height remains.
	fixed := len(lines) - body
	availBody := avail - fixed

	if avail <= 0 || body == 0 || availBody >= body {
		// Everything fits (or no height info yet): show it all, no scrolling.
		m.statsMaxOffset = 0
		m.statsOffset = 0
		return lines
	}
	if availBody < 1 {
		availBody = 1
	}

	maxOffset := body - availBody
	m.statsMaxOffset = maxOffset
	offset := m.statsOffset
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	m.statsOffset = offset

	window := make([]string, availBody)
	copy(window, lines[bodyStart+offset:bodyStart+offset+availBody])
	if offset > 0 {
		window[0] = m.scrollIndicatorRow("▲")
	}
	if offset+availBody < body {
		window[len(window)-1] = m.scrollIndicatorRow("▼")
	}

	out := make([]string, 0, len(lines)-body+availBody)
	out = append(out, lines[:bodyStart]...)
	out = append(out, window...)
	out = append(out, lines[bodyEnd:]...)
	return out
}
