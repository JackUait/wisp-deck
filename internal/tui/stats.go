package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/usage"
)

const (
	statsInner  = 60 // inner content width between the box borders
	statsGaugeW = 26 // gauge length (columns) for a month that is 100% of all tokens
	statsColEnd = 58 // right edge of the Total column; money/totals align here
	statsWindow = 8  // months visible at once before scrolling
)

// monthLabel turns a "YYYY-MM" bucket into a human label like "Jun 2026". Other
// strings (the "Month" header, the "Total" grand row) are returned unchanged so
// the column formatters can pass everything through it.
func monthLabel(s string) string {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return s
	}
	return t.Format("Jan 2006")
}

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
	months      []usage.MonthlyUsage
	loading     bool
	err         error
	offset      int
	width       int
	height      int
	claudeDir   string
	opencodeDir string
	cachePath   string
}

// SetSize records the terminal dimensions so View can center the box. Callers
// that already know the size (e.g. the main menu pushing this screen) set it up
// front so the box is centered on the first frame, before any resize event.
func (m *StatsModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// NewStatsModelWithData builds a ready-to-render model (no async load). For tests
// and any caller that already has aggregated data.
func NewStatsModelWithData(months []usage.MonthlyUsage) StatsModel {
	return StatsModel{months: months, loading: false}
}

// dollarFmt renders a USD estimate: cents under $1, comma-grouped whole dollars
// under $10K, then $1.2K / $4.6M for larger figures.
func dollarFmt(usd float64) string {
	switch {
	case usd < 1:
		return fmt.Sprintf("$%.2f", usd)
	case usd < 10000:
		s := strconv.FormatInt(int64(usd+0.5), 10)
		var out []byte
		for i := 0; i < len(s); i++ {
			if i > 0 && (len(s)-i)%3 == 0 {
				out = append(out, ',')
			}
			out = append(out, s[i])
		}
		return "$" + string(out)
	case usd < 1_000_000:
		return fmt.Sprintf("$%.1fK", usd/1000)
	default:
		return fmt.Sprintf("$%.1fM", usd/1_000_000)
	}
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

// statsGauge renders a proportional meter: a filled portion (fractional eighths
// for sub-cell resolution) styled with fill, followed by an empty ░ track styled
// with track, always statsGaugeW columns wide so it reads as a gauge.
func statsGauge(frac float64, fill, track lipgloss.Style) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	units := frac * float64(statsGaugeW)
	full := int(units)
	bar := strings.Repeat("█", full)
	if full < statsGaugeW {
		eighths := []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}
		if i := int((units - float64(full)) * 8); i > 0 {
			bar += eighths[i]
		}
	}
	rest := statsGaugeW - lipgloss.Width(bar)
	if rest < 0 {
		rest = 0
	}
	return fill.Render(bar) + track.Render(strings.Repeat("░", rest))
}

// statsCols formats the six aligned columns: month left-justified, the four
// token counts right-justified, total right-justified. Each segment is styled
// independently so the total can stand out.
func statsCols(month, in, out, cw, cr, total string, monthStyle, numStyle, totalStyle lipgloss.Style) string {
	// The four token columns are %7s (their values/headers fit) so the reclaimed
	// columns can set Total apart with a 7-space gap without growing past statsColEnd.
	return "  " + monthStyle.Render(fmt.Sprintf("%-8s", monthLabel(month))) + " " +
		numStyle.Render(fmt.Sprintf("%7s", in)) + " " +
		numStyle.Render(fmt.Sprintf("%7s", out)) + " " +
		numStyle.Render(fmt.Sprintf("%7s", cw)) + " " +
		numStyle.Render(fmt.Sprintf("%7s", cr)) + "       " +
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

// center places content in the middle of the terminal when the size is known;
// otherwise it returns content as-is (origin) for unsized callers and tests.
func (m StatsModel) center(content string) string {
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
	}
	return content
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
		return m.center(message(lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("Failed to load usage: " + m.err.Error())))
	}
	if m.loading {
		return m.center(message(primary.Render("Crunching token usage…")))
	}
	if len(m.months) == 0 {
		return m.center(message(muted.Render("No usage data found yet.")))
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
	var grandCost float64
	grandAllPriced := true
	// rightAlign places a left chunk and a right chunk on one inner row with the
	// right chunk's edge landing on statsColEnd (under the Total column), so every
	// gauge cost, model cost and grand total stacks into one clean money column.
	rightAlign := func(left, right string) string {
		pad := statsColEnd - lipgloss.Width(left) - lipgloss.Width(right)
		if pad < 1 {
			pad = 1
		}
		return left + strings.Repeat(" ", pad) + right
	}
	for idx, mu := range m.months[m.offset:end] {
		if idx > 0 {
			// Blank spacer sets each month block apart so they don't cluster.
			body = append(body, statsBoxLine("", border))
		}
		row := statsCols(mu.Month,
			humanizeTokens(mu.Input), humanizeTokens(mu.Output),
			humanizeTokens(mu.CacheWrite), humanizeTokens(mu.CacheRead),
			humanizeTokens(mu.Total()), num, num, primaryBold)
		frac := float64(mu.Total()) / float64(grandTotal)
		pct := int(frac*100 + 0.5)
		monthCost, allPriced := mu.CostUSD()
		costLabel := dollarFmt(monthCost)
		if !allPriced {
			costLabel = "~" + costLabel
		}
		// Gauge + inline percent on the left, the month's cost aligned right.
		gaugeLeft := "  " + statsGauge(frac, primary, border) + " " +
			faint.Render(fmt.Sprintf("%3d%%", pct))
		barRow := rightAlign(gaugeLeft, primaryBold.Render(costLabel))
		body = append(body, statsBoxLine(row, border))
		body = append(body, statsBoxLine(barRow, border))
		// Per-model breakdown, drawn as a tree. Tokens align under Cache R and cost
		// under Total so the breakdown reads as columns, not a clump.
		for i, md := range mu.Models {
			connector := "├─ "
			if i == len(mu.Models)-1 {
				connector = "└─ "
			}
			label := strings.TrimPrefix(md.Model, "claude-")
			if len(label) > 28 {
				label = label[:27] + "…" // keep the row inside the box
			}
			usd, priced := usage.ModelCostUSD(md)
			cost := "—"
			if priced {
				cost = dollarFmt(usd)
			}
			mrow := "   " + muted.Render(connector) +
				muted.Render(fmt.Sprintf("%-28s", label)) +
				muted.Render(fmt.Sprintf("%8s", humanizeTokens(md.Total()))) + "       " +
				primary.Render(fmt.Sprintf("%9s", cost))
			body = append(body, statsBoxLine(mrow, border))
		}
	}
	// Grand total cost spans every month (not just the visible window).
	for _, mu := range m.months {
		c, ap := mu.CostUSD()
		grandCost += c
		if !ap {
			grandAllPriced = false
		}
	}
	body = append(body, statsSep(border))
	g := statsGrandTotal(m.months)
	body = append(body, statsBoxLine(statsCols(g.Month,
		humanizeTokens(g.Input), humanizeTokens(g.Output),
		humanizeTokens(g.CacheWrite), humanizeTokens(g.CacheRead),
		humanizeTokens(g.Total()), primaryBold, primaryBold, primaryBold), border))
	grandCostStr := dollarFmt(grandCost)
	if !grandAllPriced {
		grandCostStr = "~" + grandCostStr
	}
	costLeft := "  " + header.Render("Total est. cost")
	body = append(body, statsBoxLine(rightAlign(costLeft, primaryBold.Render(grandCostStr)), border))
	body = append(body, statsBoxLine("", border))
	body = append(body, statsBoxLine("  "+hint, border))
	return m.center(statsFrame(body))
}

type statsLoadedMsg struct{ months []usage.MonthlyUsage }
type statsErrMsg struct{ err error }

// NewStatsModel builds a model that loads usage asynchronously on Init.
func NewStatsModel() StatsModel {
	home, _ := os.UserHomeDir()
	claudeDir, opencodeDir, cachePath := usage.DefaultPaths(home)
	return StatsModel{loading: true, claudeDir: claudeDir, opencodeDir: opencodeDir, cachePath: cachePath}
}

func (m StatsModel) Init() tea.Cmd {
	if !m.loading {
		return nil
	}
	claudeDir, opencodeDir, cachePath := m.claudeDir, m.opencodeDir, m.cachePath
	return func() tea.Msg {
		months, err := usage.Aggregate(claudeDir, opencodeDir, cachePath)
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
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
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
