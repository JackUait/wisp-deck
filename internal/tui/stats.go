package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/usage"
)

const (
	statsBarWidth = 24
	statsWindow   = 8 // months visible at once before scrolling
)

// humanizeTokens renders a token count as a compact string (999, 1.5K, 2.0M, 3.1B).
func humanizeTokens(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	default:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	}
}

// StatsModel renders monthly token usage as a scrollable table + bar chart.
type StatsModel struct {
	months    []usage.MonthlyUsage
	loading   bool
	err       error
	offset    int
	claudeDir string
	cachePath string
}

// NewStatsModelWithData builds a ready-to-render model (no async load). For tests
// and any caller that already has aggregated data.
func NewStatsModelWithData(months []usage.MonthlyUsage) StatsModel {
	return StatsModel{months: months, loading: false}
}

// maxTotal returns the largest month Total, used to scale bars. Minimum 1.
func (m StatsModel) maxTotal() int64 {
	var max int64 = 1
	for _, mu := range m.months {
		if t := mu.Total(); t > max {
			max = t
		}
	}
	return max
}

func (m StatsModel) renderRow(mu usage.MonthlyUsage, max int64) string {
	barLen := int(mu.Total() * int64(statsBarWidth) / max)
	if barLen == 0 && mu.Total() > 0 {
		barLen = 1
	}
	bar := strings.Repeat("█", barLen)
	return fmt.Sprintf("%s  in %-7s out %-7s cw %-7s cr %-7s = %-7s %s",
		mu.Month,
		humanizeTokens(mu.Input),
		humanizeTokens(mu.Output),
		humanizeTokens(mu.CacheWrite),
		humanizeTokens(mu.CacheRead),
		humanizeTokens(mu.Total()),
		bar,
	)
}

func (m StatsModel) View() string {
	title := titleStyle.Render("Token Usage by Month")
	hint := lipgloss.NewStyle().Faint(true).Render("↑↓ scroll · esc back")

	if m.err != nil {
		return title + "\n\n" + "Failed to load usage: " + m.err.Error() + "\n\n" + hint
	}
	if m.loading {
		return title + "\n\n" + "Crunching token usage…" + "\n\n" + hint
	}
	if len(m.months) == 0 {
		return title + "\n\n" + "No usage data found yet." + "\n\n" + hint
	}

	max := m.maxTotal()
	end := m.offset + statsWindow
	if end > len(m.months) {
		end = len(m.months)
	}
	var b strings.Builder
	b.WriteString(title + "\n\n")
	for _, mu := range m.months[m.offset:end] {
		b.WriteString(m.renderRow(mu, max) + "\n")
	}
	b.WriteString("\n" + hint)
	return b.String()
}
