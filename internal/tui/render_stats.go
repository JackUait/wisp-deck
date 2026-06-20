package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/usage"
)

// renderStatsRows renders the stats content as box rows using leftBorder/rightBorder,
// reading cached stats fields from MainMenuModel instead of calling NewStatsModel().
func (m *MainMenuModel) renderStatsRows(leftBorder, rightBorder string) []string {
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

	var rows []string
	rows = append(rows, emptyRow)

	// Loading state.
	if m.statsLoading {
		rows = append(rows, textRow(primaryBoldStyle.Render("Crunching token usage…")))
		rows = append(rows, emptyRow)
		rows = append(rows, textRow(faint.Render("Usage data is read from ~/.claude/usage/")))
		rows = append(rows, emptyRow)
		return rows
	}

	// Error state.
	if m.statsErr != nil {
		rows = append(rows, textRow(errStyle.Render("Failed to load usage: "+m.statsErr.Error())))
		rows = append(rows, emptyRow)
		return rows
	}

	// Empty state (loaded but no data).
	if len(m.statsMonths) == 0 {
		rows = append(rows, textRow(muted.Render("No usage data found yet.")))
		rows = append(rows, emptyRow)
		rows = append(rows, textRow(faint.Render("Usage data is read from ~/.claude/usage/")))
		rows = append(rows, emptyRow)
		return rows
	}

	// Column header row.
	header := lipgloss.NewStyle().Foreground(m.theme.Dim).Bold(true)
	hdr := "  " + header.Render(fmt.Sprintf("%-8s %7s %7s %7s %7s       %9s",
		"Month", "Input", "Output", "Cache W", "Cache R", "Total"))
	hdrGap := menuContentWidth - lipgloss.Width(hdr)
	if hdrGap < 0 {
		hdrGap = 0
	}
	rows = append(rows, leftBorder+hdr+strings.Repeat(" ", hdrGap)+rightBorder)

	// Separator
	sepRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder
	rows = append(rows, sepRow)

	// Visible window of months.
	end := m.statsOffset + statsWindow
	if end > len(m.statsMonths) {
		end = len(m.statsMonths)
	}
	visibleMonths := m.statsMonths[m.statsOffset:end]

	grandTotal := statsGrandTotal(m.statsMonths)
	allTotal := grandTotal.Total()
	if allTotal < 1 {
		allTotal = 1
	}

	for i, mu := range visibleMonths {
		// Blank spacer between months so one month's per-model rows don't run
		// straight into the next month's header.
		if i > 0 {
			rows = append(rows, emptyRow)
		}

		frac := float64(mu.Total()) / float64(allTotal)
		pct := int(frac*100 + 0.5)
		monthCost, allPriced := mu.CostUSD()
		costStr := dollarFmt(monthCost)
		if !allPriced {
			costStr = "~" + costStr
		}

		dataLine := "  " + numStyle.Render(fmt.Sprintf("%-8s %7s %7s %7s %7s",
			monthLabel(mu.Month),
			humanizeTokens(mu.Input),
			humanizeTokens(mu.Output),
			humanizeTokens(mu.CacheWrite),
			humanizeTokens(mu.CacheRead))) + "       " +
			primaryBoldStyle.Render(fmt.Sprintf("%9s", humanizeTokens(mu.Total())))
		dataGap := menuContentWidth - lipgloss.Width(dataLine)
		if dataGap < 0 {
			dataGap = 0
		}
		rows = append(rows, leftBorder+dataLine+strings.Repeat(" ", dataGap)+rightBorder)

		// Bar + percent + cost on line below the data.
		gaugeStr := statsGauge(frac, lipgloss.NewStyle().Foreground(m.theme.Primary), dimStyle)
		barLine := "  " + gaugeStr + " " + faint.Render(fmt.Sprintf("%3d%%", pct))
		costPad := menuContentWidth - lipgloss.Width(barLine) - lipgloss.Width(costStr)
		if costPad < 1 {
			costPad = 1
		}
		barRow := leftBorder + barLine + strings.Repeat(" ", costPad) + primaryBoldStyle.Render(costStr) + rightBorder
		rows = append(rows, barRow)

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
			rows = append(rows, leftBorder+modelLine+strings.Repeat(" ", modelPad)+dimStyle.Render(modelCost)+rightBorder)
		}
	}

	// Grand total row.
	rows = append(rows, sepRow)
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

	rows = append(rows, itemRow("Total", humanizeTokens(g.Total()), primaryBoldStyle, primaryBoldStyle))
	rows = append(rows, itemRow("Est. cost", grandCostStr, header, primaryBoldStyle))
	rows = append(rows, emptyRow)

	return rows
}

// renderStatsBox renders the Stats tab: shared chrome (top border + title row +
// tab bar + separator) followed by stats content rows + bottom border + help row.
func (m *MainMenuModel) renderStatsBox() string {
	top, separator, bottom, leftBorder, rightBorder := m.boxBorders()

	var lines []string
	lines = append(lines, top)
	lines = append(lines, m.renderTitleRow(leftBorder, rightBorder))
	lines = append(lines, m.renderTabBar(leftBorder, rightBorder))
	lines = append(lines, separator)
	lines = append(lines, m.renderStatsRows(leftBorder, rightBorder)...)
	lines = append(lines, bottom)
	lines = append(lines, m.renderHelpRow())
	return strings.Join(lines, "\n")
}
