package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// writeConfigFixtures creates a subscriptions dir + list with three profiles:
// a ready GLM (api key present), a not-ready MiMo (no stored key), and a
// ChatGPT (Codex-authed, so ConfigReady is always true). Returns the list file
// and the configs dir.
func writeConfigFixtures(t *testing.T) (listFile, configsDir string) {
	t.Helper()
	dir := t.TempDir()
	configsDir = filepath.Join(dir, "claude-configs")
	if err := os.MkdirAll(configsDir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(configsDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("glm.json", `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"zhipu","ANTHROPIC_AUTH_TOKEN":"sk-x"}}`)
	write("mimo.json", `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"mimo"}}`)
	write("gpt.json", `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"openai-chatgpt"}}`)
	listFile = filepath.Join(dir, "claude-configs.list")
	if err := os.WriteFile(listFile, []byte("GLM:glm.json\nMiMo:mimo.json\nGPT:gpt.json\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return listFile, configsDir
}

// Subscriptions render as rows nested under the Claude group, AFTER the account
// rows and BEFORE the other-agent rows. Each carries its settings filename in
// Config and a readiness flag (a keyless API provider is not ready).
func TestAccountSwitch_buildSwitchRows_appendsSubscriptions(t *testing.T) {
	dir := t.TempDir()
	accList := filepath.Join(dir, "claude-accounts.list")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(accList, []byte("Work:work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfgList, cfgDir := writeConfigFixtures(t)

	rows, cursor := buildSwitchRows(switchRowsInput{
		listFile:         accList,
		defaultLabelFile: defLabel,
		active:           "work",
		activeTool:       "claude",
		tools:            []string{"claude", "codex"},
		configsList:      cfgList,
		configsDir:       cfgDir,
	})
	// Default, Work, GLM, MiMo, GPT, Codex
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6: %+v", len(rows), rows)
	}
	if rows[2].Config != "glm.json" || rows[2].Label != "GLM" || !rows[2].Ready {
		t.Errorf("row2 = %+v, want ready GLM subscription", rows[2])
	}
	if rows[3].Config != "mimo.json" || rows[3].Ready {
		t.Errorf("row3 = %+v, want NOT-ready MiMo subscription", rows[3])
	}
	if rows[4].Config != "gpt.json" || !rows[4].Ready {
		t.Errorf("row4 = %+v, want ready GPT subscription (chatgpt always ready)", rows[4])
	}
	if rows[5].Tool != "codex" {
		t.Errorf("row5 = %+v, want the Codex agent row after subscriptions", rows[5])
	}
	if rows[1].Config != "" {
		t.Errorf("account row must carry no Config, got %+v", rows[1])
	}
	if cursor != 1 {
		t.Errorf("cursor = %d, want 1 (active account, no active subscription)", cursor)
	}
}

// When a subscription is the pane's active backend, the cursor (and active dot)
// lands on that subscription row, not on an account.
func TestAccountSwitch_buildSwitchRows_cursorOnActiveConfig(t *testing.T) {
	dir := t.TempDir()
	accList := filepath.Join(dir, "claude-accounts.list")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	if err := os.WriteFile(accList, []byte("Work:work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfgList, cfgDir := writeConfigFixtures(t)

	rows, cursor := buildSwitchRows(switchRowsInput{
		listFile:         accList,
		defaultLabelFile: defLabel,
		active:           "work",
		activeTool:       "claude",
		tools:            []string{"claude", "codex"},
		configsList:      cfgList,
		configsDir:       cfgDir,
		activeConfig:     "glm.json",
	})
	if rows[cursor].Config != "glm.json" {
		t.Fatalf("cursor row = %+v, want the active GLM subscription", rows[cursor])
	}
}

// A subscription row reports through the result file as "config:<file>" so the
// bash side can tell it apart from an account dir ("<dir>") and an agent
// ("tool:<name>").
func TestAccountSwitch_switchResultValue_config(t *testing.T) {
	if got := switchResultValue(switchRow{Label: "GLM", Config: "glm.json"}); got != "config:glm.json" {
		t.Fatalf("subscription row result = %q, want config:glm.json", got)
	}
}

// Choosing a subscription emits config JSON; changed reflects whether it
// differs from the subscription the pane was already running.
func TestAccountSwitch_configResultJSON(t *testing.T) {
	got, err := configResultJSON("glm.json", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"selected":true,"config":"glm.json","changed":true}` {
		t.Fatalf("json = %s", got)
	}
	got, err = configResultJSON("glm.json", "glm.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"selected":true,"config":"glm.json","changed":false}` {
		t.Fatalf("json = %s", got)
	}
}

func TestAccountSwitch_subscriptionFlagsRegistered(t *testing.T) {
	for _, f := range []string{"configs", "configs-dir", "active-config"} {
		if claudeAccountSwitchCmd.Flags().Lookup(f) == nil {
			t.Errorf("claude-account-switch must register --%s", f)
		}
	}
}

// Subscription rows nest under the Claude group header, indented like account
// rows, each with the subscription glyph.
func TestAccountSwitch_innerLines_subscriptionRowsNestedUnderClaude(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	rows := []switchRow{
		{Label: "Work", Dir: "work"},
		{Label: "GLM", Config: "glm.json", Ready: true},
		{Label: "Codex", Tool: "codex"},
	}
	m := newAccountSwitchModel(rows, 0, "")
	lines := m.innerLines()
	var glmLine string
	for _, l := range lines {
		if strings.Contains(l, "GLM") {
			glmLine = l
		}
	}
	if glmLine == "" {
		t.Fatalf("no GLM subscription row rendered:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(glmLine, "    ") {
		t.Errorf("subscription row must indent under the Claude header, got %q", glmLine)
	}
	if !strings.Contains(glmLine, configRowGlyph()+" GLM") {
		t.Errorf("subscription row must show the subscription glyph, got %q", glmLine)
	}
}

// A not-ready subscription renders gray (dimmed), signaling it can't be picked
// until it's set up in the Subscription modal.
func TestAccountSwitch_innerLines_notReadySubscriptionGrayed(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	rows := []switchRow{
		{Label: "Work", Dir: "work"},
		{Label: "MiMo", Config: "mimo.json", Ready: false},
		{Label: "Codex", Tool: "codex"},
	}
	m := newAccountSwitchModel(rows, 0, "")
	lines := m.innerLines()
	var mimoLine string
	for _, l := range lines {
		if strings.Contains(l, "MiMo") {
			mimoLine = l
		}
	}
	const gray = "38;5;244m"
	if !strings.Contains(mimoLine, gray) {
		t.Errorf("not-ready subscription must render gray, got %q", mimoLine)
	}
}

// Keyboard navigation skips over a not-ready subscription — it can't be the
// cursor target in either direction.
func TestAccountSwitchModel_navSkipsNotReadySubscription(t *testing.T) {
	rows := []switchRow{
		{Label: "Work", Dir: "work"},
		{Label: "MiMo", Config: "mimo.json", Ready: false},
		{Label: "GLM", Config: "glm.json", Ready: true},
	}
	m := newAccountSwitchModel(rows, 0, "")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = out.(accountSwitchModel)
	if m.cursor != 2 {
		t.Fatalf("down from row0 must skip the not-ready row to row2, got cursor %d", m.cursor)
	}
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = out.(accountSwitchModel)
	if m.cursor != 0 {
		t.Fatalf("up from row2 must skip the not-ready row back to row0, got cursor %d", m.cursor)
	}
}

// Clicking a not-ready subscription neither selects it nor dismisses the popup.
func TestAccountSwitchModel_clickNotReadySubscriptionIgnored(t *testing.T) {
	rows := []switchRow{
		{Label: "Work", Dir: "work"},
		{Label: "MiMo", Config: "mimo.json", Ready: false},
	}
	m := newAccountSwitchModel(rows, 0, "")
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 48, Height: 18})
	m = sized.(accountSwitchModel)
	firstRowY, cardLeft, _ := accountSwitchLayout(m.width, m.height, len(rows), m.contentWidth())
	click := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: cardLeft + 2, Y: firstRowY + 1}
	out, cmd := m.Update(click)
	mm := out.(accountSwitchModel)
	if mm.chosen {
		t.Fatalf("clicking a not-ready subscription must not choose it")
	}
	if isQuitCmd(t, cmd) {
		t.Errorf("clicking a not-ready row should keep the popup open, not quit")
	}
}

// Each subscription wears its own persistent color (claude-config-colors),
// mirroring account colors, so the switcher row matches the ledger pill and
// the statusline usage bars.
func TestAccountSwitch_innerLines_subscriptionRowWearsOwnColor(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	dir := t.TempDir()
	configColors := filepath.Join(dir, "claude-config-colors")
	if err := os.WriteFile(configColors, []byte("glm.json:205\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := []switchRow{
		{Label: "Work", Dir: "work"},
		{Label: "GLM", Config: "glm.json", Ready: true},
	}
	m := newAccountSwitchModel(rows, 1, "", configColors)
	lines := m.innerLines()
	var glmLine string
	for _, l := range lines {
		if strings.Contains(l, "GLM") {
			glmLine = l
		}
	}
	if !strings.Contains(glmLine, "38;5;205m") {
		t.Errorf("subscription row must wear its persisted color 205, got %q", glmLine)
	}
}
