package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/models"
)

// The install percentage is time-driven, not real progress: `npm install -g`
// reports nothing usable when run detached. It eases toward a ceiling below 100%
// and holds there, so a wedged install parks rather than claiming completion.

func TestAdvanceInstallPct_is_strictly_increasing_below_the_ceiling(t *testing.T) {
	pct := 0.0
	for i := 0; i < 200; i++ {
		next := advanceInstallPct(pct)
		if next <= pct {
			t.Fatalf("step %d: %v did not advance past %v", i, next, pct)
		}
		pct = next
	}
}

func TestAdvanceInstallPct_never_reaches_the_ceiling(t *testing.T) {
	pct := 0.0
	for i := 0; i < 10000; i++ {
		pct = advanceInstallPct(pct)
		if pct >= installPctCeiling {
			t.Fatalf("iteration %d: pct %v reached the ceiling %v", i, pct, installPctCeiling)
		}
	}
	// It should get close, or the bar looks stuck near zero.
	if pct < installPctCeiling*0.99 {
		t.Errorf("after 10000 ticks pct = %v, expected it to approach %v", pct, installPctCeiling)
	}
}

// A hung install must never render as complete.
func TestAdvanceInstallPct_ceiling_is_below_one(t *testing.T) {
	if installPctCeiling >= 1.0 {
		t.Fatalf("installPctCeiling = %v, must be < 1.0 so a hung install never shows 100%%", installPctCeiling)
	}
}

func TestAdvanceInstallPct_clamps_a_pct_already_at_or_past_the_ceiling(t *testing.T) {
	for _, in := range []float64{installPctCeiling, 0.95, 1.5} {
		if got := advanceInstallPct(in); got > installPctCeiling {
			t.Errorf("advanceInstallPct(%v) = %v, want <= %v", in, got, installPctCeiling)
		}
	}
}

// --- renderProgressBar ---

// visibleLen strips ANSI so the bar's rendered width can be asserted.
func visibleLen(s string) int { return len([]rune(stripAnsiTui(s))) }

func stripAnsiTui(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TestRenderProgressBar_has_the_requested_cell_count(t *testing.T) {
	for _, pct := range []float64{0, 0.47, installPctCeiling} {
		out := stripAnsiTui(renderProgressBar(pct, 24))
		var cells int
		for _, r := range out {
			if r == '█' || r == '░' {
				cells++
			}
		}
		if cells != 24 {
			t.Errorf("pct %v: %d bar cells, want 24 (%q)", pct, cells, out)
		}
	}
}

func TestRenderProgressBar_fills_proportionally(t *testing.T) {
	empty := strings.Count(stripAnsiTui(renderProgressBar(0, 20)), "█")
	if empty != 0 {
		t.Errorf("0%% bar has %d filled cells, want 0", empty)
	}
	half := strings.Count(stripAnsiTui(renderProgressBar(0.5, 20)), "█")
	if half != 10 {
		t.Errorf("50%% bar has %d filled cells, want 10", half)
	}
	near := strings.Count(stripAnsiTui(renderProgressBar(installPctCeiling, 20)), "█")
	if near != 18 {
		t.Errorf("90%% bar has %d filled cells, want 18", near)
	}
}

func TestRenderProgressBar_shows_the_percentage(t *testing.T) {
	out := stripAnsiTui(renderProgressBar(0.47, 20))
	if !strings.Contains(out, "47%") {
		t.Errorf("bar %q should show 47%%", out)
	}
	if !strings.Contains(stripAnsiTui(renderProgressBar(0, 20)), "0%") {
		t.Error("0% bar should show 0%")
	}
}

// The bar plus its percentage must fit the panel's right column.
func TestRenderProgressBar_fits_the_panel_width(t *testing.T) {
	const longestLeft = len("  ● Claude Code") + 2 // marker + bullet + longest name
	bar := renderProgressBar(0.47, installBarWidth)
	if got := visibleLen(bar) + longestLeft; got > menuContentWidth-1 {
		t.Errorf("row of width %d exceeds the content width %d", got, menuContentWidth-1)
	}
}

// --- Ticker ---

// The install ticker must be self-arming: bobTickCmd only runs when the mascot is
// animated, so a bar hung off it would freeze for users with Mascot [None].
func TestInstallTick_advances_while_installing_and_rearms(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex"})
	m.aiToolInstalling = "codex"
	m.aiToolInstallPct = 0

	_, cmd := m.Update(installTickMsg{})
	if cmd == nil {
		t.Fatal("tick must re-arm while an install is running")
	}
	if m.aiToolInstallPct <= 0 {
		t.Errorf("tick must advance the percentage, got %v", m.aiToolInstallPct)
	}
}

// The ticker must stop itself once the install finishes, or it runs forever.
func TestInstallTick_stops_when_no_install_is_running(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	m.aiToolInstalling = ""

	_, cmd := m.Update(installTickMsg{})
	if cmd != nil {
		t.Error("tick must not re-arm when no install is running")
	}
}

func TestInstallFocusedTool_starts_the_ticker_and_zeroes_the_pct(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex"})
	m.libDir = "/libs"
	m.aiToolInstallPct = 0.5 // residue from a previous install

	cmd := m.installFocusedTool()
	if cmd == nil {
		t.Fatal("starting an install must return a command batch")
	}
	if m.aiToolInstallPct != 0 {
		t.Errorf("pct = %v, want 0 at the start of an install", m.aiToolInstallPct)
	}
}

// --- Row rendering ---

func TestRenderAIToolsPanel_shows_a_bar_only_for_the_installing_tool(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex"},
	)
	m.aiToolInstalling = "codex"
	m.aiToolInstallPct = 0.47

	out := stripAnsiTui(m.renderAIToolsPanel())
	if !strings.Contains(out, "47%") {
		t.Errorf("installing row should show a percentage:\n%s", out)
	}
	if !strings.Contains(out, "█") {
		t.Errorf("installing row should show a bar:\n%s", out)
	}
	// The claude row must be untouched.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Claude Code") && strings.Contains(line, "%") {
			t.Errorf("non-installing row must not show a bar: %q", line)
		}
	}
}

func TestRenderAIToolsPanel_no_bar_when_nothing_is_installing(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex"})
	out := stripAnsiTui(m.renderAIToolsPanel())
	if strings.Contains(out, "█") || strings.Contains(out, "%") {
		t.Errorf("no bar should render when idle:\n%s", out)
	}
}

// After the install finishes the row flips to its normal state — the bar is gone.
func TestRenderAIToolsPanel_bar_disappears_after_install_completes(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex"})
	m.aiToolInstalling = "codex"
	m.aiToolInstallPct = 0.8
	m.detectAITools = func() []models.AITool {
		return []models.AITool{{Name: "codex", Installed: true}}
	}
	m.applyAIToolInstallDone(aiToolInstallDoneMsg{tool: "codex"})

	out := stripAnsiTui(m.renderAIToolsPanel())
	if strings.Contains(out, "█") {
		t.Errorf("bar must disappear once the install completes:\n%s", out)
	}
	if !strings.Contains(out, "installed") {
		t.Errorf("row should read installed:\n%s", out)
	}
}

// A failed install must clear the bar too, not leave it frozen mid-way.
func TestRenderAIToolsPanel_bar_disappears_after_install_fails(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex"})
	m.aiToolInstalling = "codex"
	m.aiToolInstallPct = 0.8
	m.detectAITools = func() []models.AITool { return []models.AITool{{Name: "codex"}} }
	m.applyAIToolInstallDone(aiToolInstallDoneMsg{tool: "codex", err: errFake})

	out := stripAnsiTui(m.renderAIToolsPanel())
	if strings.Contains(out, "█") {
		t.Errorf("bar must disappear when the install fails:\n%s", out)
	}
	if !strings.Contains(out, "install") {
		t.Errorf("row should offer to install again:\n%s", out)
	}
}

var errFake = fakeErr{}

type fakeErr struct{}

func (fakeErr) Error() string { return "boom" }

// tea.Cmd type assertion guard: Update must keep returning a tea.Model.
var _ tea.Model = (*MainMenuModel)(nil)

// Pacing. A real `npm install -g` takes tens of seconds. The first ramp reached
// the ceiling in under 6s, which pins the bar for the rest of a real install and
// makes it useless. These bounds keep the bar moving across a plausible install.
func pctAfter(seconds float64) float64 {
	ticks := int(seconds / installTickInterval.Seconds())
	pct := 0.0
	for i := 0; i < ticks; i++ {
		pct = advanceInstallPct(pct)
	}
	return pct
}

func TestAdvanceInstallPct_is_paced_for_a_real_npm_install(t *testing.T) {
	// Early on it should have moved perceptibly, but nowhere near the ceiling.
	if got := pctAfter(5); got > 0.5 {
		t.Errorf("after 5s pct = %.2f, want <= 0.50 (ramp saturates too fast)", got)
	}
	if got := pctAfter(5); got < 0.15 {
		t.Errorf("after 5s pct = %.2f, want >= 0.15 (bar looks stuck)", got)
	}
	// By the time a typical install finishes, the bar should be most of the way.
	if got := pctAfter(30); got < 0.8 {
		t.Errorf("after 30s pct = %.2f, want >= 0.80", got)
	}
	// And it still must not reach the ceiling, however long it runs.
	if got := pctAfter(600); got >= installPctCeiling {
		t.Errorf("after 10min pct = %.2f, must stay below %v", got, installPctCeiling)
	}
}

// feedbackMsg is only rendered by the Projects tab (render_projects.go), so an
// install failure raised from the Settings panel would otherwise be invisible.
// The panel must show the error itself.
func TestAIToolInstallDone_failure_is_visible_in_the_panel(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex"})
	m.detectAITools = func() []models.AITool { return []models.AITool{{Name: "codex"}} }
	m.aiToolInstalling = "codex"

	m.applyAIToolInstallDone(aiToolInstallDoneMsg{tool: "codex", err: errFake})

	if m.aiToolsErr == nil {
		t.Fatal("a failed install must set aiToolsErr so the panel can show it")
	}
	out := stripAnsiTui(m.renderAIToolsPanel())
	if !strings.Contains(out, "Codex") || !strings.Contains(strings.ToLower(out), "failed") {
		t.Errorf("panel should show the failure:\n%s", out)
	}
}

// A subsequent successful install must clear the stale error.
func TestAIToolInstallDone_success_clears_a_previous_error(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex"})
	m.aiToolsErr = errFake
	m.aiToolInstalling = "codex"
	m.detectAITools = func() []models.AITool {
		return []models.AITool{{Name: "codex", Installed: true}}
	}
	m.applyAIToolInstallDone(aiToolInstallDoneMsg{tool: "codex"})
	if m.aiToolsErr != nil {
		t.Errorf("a successful install must clear the previous error, got %v", m.aiToolsErr)
	}
}
