package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/usage"
)

const (
	statsInner  = 60 // inner content width between the box borders
	statsBarMax = 40 // bar length (columns) for a month that is 100% of all tokens
	statsWindow = 8  // months visible at once before scrolling
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

// statsGrandTotal sums every month into one MonthlyUsage labelled "Total".
func statsGrandTotal(months []usage.MonthlyUsage) usage.MonthlyUsage {
	g := usage.MonthlyUsage{Month: "Total"}
	for _, mu := range months {
		g.Input += mu.Input
		g.Output += mu.Output
		g.CacheWrite += mu.CacheWrite
		g.CacheRead += mu.CacheRead
	}
	return g
}

// statsBar renders a proportional bar of fractional block glyphs (eighths for
// sub-cell resolution), padded to statsBarMax columns so trailing labels align.
func statsBar(frac float64) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	units := frac * float64(statsBarMax)
	full := int(units)
	bar := strings.Repeat("█", full)
	if full < statsBarMax {
		eighths := []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}
		if i := int((units - float64(full)) * 8); i > 0 {
			bar += eighths[i]
		}
	}
	return bar + strings.Repeat(" ", statsBarMax-lipgloss.Width(bar))
}

// statsCols formats the six aligned columns: month left-justified, the four
// token counts right-justified, total right-justified. Each segment is styled
// independently so the total can stand out.
func statsCols(month, in, out, cw, cr, total string, monthStyle, numStyle, totalStyle lipgloss.Style) string {
	return "  " + monthStyle.Render(fmt.Sprintf("%-7s", month)) + " " +
		numStyle.Render(fmt.Sprintf("%8s", in)) + " " +
		numStyle.Render(fmt.Sprintf("%8s", out)) + " " +
		numStyle.Render(fmt.Sprintf("%8s", cw)) + " " +
		numStyle.Render(fmt.Sprintf("%8s", cr)) + " " +
		totalStyle.Render(fmt.Sprintf("%9s", total))
}

// statsBoxLine wraps inner content with vertical borders, padding to statsInner.
func statsBoxLine(content string, border lipgloss.Style) string {
	pad := statsInner - lipgloss.Width(content)
	if pad < 0 {
		pad = 0
	}
	return border.Render("│") + content + strings.Repeat(" ", pad) + border.Render("│")
}

// statsSep renders an inner horizontal rule row (├───┤).
func statsSep(border lipgloss.Style) string {
	return border.Render("├" + strings.Repeat("─", statsInner) + "┤")
}

// statsFrame wraps already-box-wrapped body lines with a titled rounded top
// border and a bottom border, matching the ghost-tab look.
func statsFrame(body []string) string {
	border := lipgloss.NewStyle().Foreground(currentTheme.Dim)
	title := lipgloss.NewStyle().Foreground(currentTheme.Primary).Bold(true).Render("⬡ Token Usage by Month")
	n := statsInner - 3 - lipgloss.Width(title)
	if n < 0 {
		n = 0
	}
	top := border.Render("╭─ ") + title + border.Render(" "+strings.Repeat("─", n)+"╮")
	bottom := border.Render("╰" + strings.Repeat("─", statsInner) + "╯")
	return strings.Join(append(append([]string{top}, body...), bottom), "\n")
}

func (m StatsModel) View() string {
	border := lipgloss.NewStyle().Foreground(currentTheme.Dim)
	primary := lipgloss.NewStyle().Foreground(currentTheme.Primary)
	primaryBold := lipgloss.NewStyle().Foreground(currentTheme.Primary).Bold(true)
	header := lipgloss.NewStyle().Foreground(currentTheme.Dim).Bold(true)
	num := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	faint := lipgloss.NewStyle().Faint(true)
	hint := faint.Render("↑↓ scroll · esc back")

	// One-line message states (error / loading / empty) share the same frame.
	message := func(body string) string {
		return statsFrame([]string{
			statsBoxLine("", border),
			statsBoxLine("  "+body, border),
			statsBoxLine("", border),
			statsBoxLine("  "+hint, border),
		})
	}
	if m.err != nil {
		return message(lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("Failed to load usage: " + m.err.Error()))
	}
	if m.loading {
		return message(primary.Render("Crunching token usage…"))
	}
	if len(m.months) == 0 {
		return message(muted.Render("No usage data found yet."))
	}

	grandTotal := statsGrandTotal(m.months).Total()
	if grandTotal < 1 {
		grandTotal = 1
	}
	end := m.offset + statsWindow
	if end > len(m.months) {
		end = len(m.months)
	}

	var body []string
	body = append(body, statsBoxLine("", border))
	body = append(body, statsBoxLine(
		statsCols("Month", "Input", "Output", "Cache W", "Cache R", "Total", header, header, header), border))
	body = append(body, statsSep(border))
	for _, mu := range m.months[m.offset:end] {
		row := statsCols(mu.Month,
			humanizeTokens(mu.Input), humanizeTokens(mu.Output),
			humanizeTokens(mu.CacheWrite), humanizeTokens(mu.CacheRead),
			humanizeTokens(mu.Total()), num, num, primaryBold)
		frac := float64(mu.Total()) / float64(grandTotal)
		pct := int(frac*100 + 0.5)
		barRow := "  " + primary.Render(statsBar(frac)) + " " +
			faint.Render(fmt.Sprintf("%d%% of all", pct))
		body = append(body, statsBoxLine(row, border))
		body = append(body, statsBoxLine(barRow, border))
	}
	body = append(body, statsSep(border))
	g := statsGrandTotal(m.months)
	body = append(body, statsBoxLine(statsCols(g.Month,
		humanizeTokens(g.Input), humanizeTokens(g.Output),
		humanizeTokens(g.CacheWrite), humanizeTokens(g.CacheRead),
		humanizeTokens(g.Total()), primaryBold, primaryBold, primaryBold), border))
	body = append(body, statsBoxLine("", border))
	body = append(body, statsBoxLine("  "+hint, border))
	return statsFrame(body)
}

type statsLoadedMsg struct{ months []usage.MonthlyUsage }
type statsErrMsg struct{ err error }

// NewStatsModel builds a model that loads usage asynchronously on Init.
func NewStatsModel() StatsModel {
	home, _ := os.UserHomeDir()
	claudeDir, cachePath := usage.DefaultPaths(home)
	return StatsModel{loading: true, claudeDir: claudeDir, cachePath: cachePath}
}

func (m StatsModel) Init() tea.Cmd {
	if !m.loading {
		return nil
	}
	claudeDir, cachePath := m.claudeDir, m.cachePath
	return func() tea.Msg {
		months, err := usage.Aggregate(claudeDir, cachePath)
		if err != nil {
			return statsErrMsg{err: err}
		}
		return statsLoadedMsg{months: months}
	}
}

func (m StatsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statsLoadedMsg:
		m.months = msg.months
		m.loading = false
		return m, nil
	case statsErrMsg:
		m.err = msg.err
		m.loading = false
		return m, nil
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			return m, func() tea.Msg { return PopScreenMsg{} }
		case tea.KeyUp:
			m.scrollUp()
			return m, nil
		case tea.KeyDown:
			m.scrollDown()
			return m, nil
		case tea.KeyRunes:
			if len(msg.Runes) == 1 {
				switch msg.Runes[0] {
				case 'k':
					m.scrollUp()
				case 'j':
					m.scrollDown()
				}
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *StatsModel) scrollUp() {
	if m.offset > 0 {
		m.offset--
	}
}

func (m *StatsModel) scrollDown() {
	if m.offset < len(m.months)-statsWindow {
		m.offset++
	}
}
