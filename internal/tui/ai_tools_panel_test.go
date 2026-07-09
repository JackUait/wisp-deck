package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/models"
)

func panelMenu(t *testing.T, tools ...models.AITool) *MainMenuModel {
	t.Helper()
	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}},
		[]string{"claude"}, "claude", "none")
	m.aiToolRows = tools
	m.aiToolsPanelOpen = true
	m.aiToolsCursor = 0
	return m
}

// --- installCmdFor ---
//
// The install shells into the existing bash installers (ensure_opencode /
// ensure_codex) rather than reimplementing `npm install -g` in Go, so the
// npm/Node fallbacks live in exactly one place.

func TestInstallCmdFor_shells_into_the_bash_installer(t *testing.T) {
	for _, tc := range []struct{ tool, fn string }{
		{"opencode", "ensure_opencode"},
		{"codex", "ensure_codex"},
	} {
		cmd, err := installCmdFor(tc.tool, "/libs")
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.tool, err)
		}
		joined := strings.Join(cmd.Args, " ")
		if !strings.Contains(joined, tc.fn) {
			t.Errorf("%s: command %q must call %s", tc.tool, joined, tc.fn)
		}
		for _, src := range []string{"install.sh", "tui.sh"} {
			if !strings.Contains(joined, src) {
				t.Errorf("%s: command %q must source %s", tc.tool, joined, src)
			}
		}
		// libDir travels in the environment, never interpolated into the script.
		var found bool
		for _, e := range cmd.Env {
			if e == "WISP_DECK_LIB_DIR=/libs" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: WISP_DECK_LIB_DIR=/libs must be in the command env", tc.tool)
		}
	}
}

// A libDir is a path from a flag or the environment, but it must never be able to
// execute code: bash expands $(…) and `…` inside double quotes, so interpolating
// the path into the script text would be a command-injection hole.
func TestInstallCmdFor_does_not_interpolate_libdir_into_the_script(t *testing.T) {
	evil := "/tmp/$(touch /tmp/pwned)/`id`"
	cmd, err := installCmdFor("codex", evil)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	script := cmd.Args[len(cmd.Args)-1]
	if strings.Contains(script, "touch") || strings.Contains(script, "id`") {
		t.Errorf("libDir leaked into the script text: %q", script)
	}
	if !strings.Contains(script, "$WISP_DECK_LIB_DIR") {
		t.Errorf("script %q should reference the env var, not a literal path", script)
	}
}

// Claude's installer is `curl … | bash`; it is never run from the TUI. Enforcing
// that here means the key handler cannot accidentally allow it.
func TestInstallCmdFor_refuses_claude_and_unknown_tools(t *testing.T) {
	for _, tool := range []string{"claude", "bogus", ""} {
		if _, err := installCmdFor(tool, "/libs"); err == nil {
			t.Errorf("installCmdFor(%q) must return an error", tool)
		}
	}
}

func TestInstallCmdFor_requires_a_lib_dir(t *testing.T) {
	if _, err := installCmdFor("codex", ""); err == nil {
		t.Error("installCmdFor with an empty libDir must return an error")
	}
}

// --- Panel navigation ---

func TestAIToolsPanel_opens_and_closes(t *testing.T) {
	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude"}, "claude", "none")
	m.settingsSelected = rowAITools
	m.settingsEnter()
	if !m.aiToolsPanelOpen {
		t.Fatal("↵ on the AI tools row must open the panel")
	}
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyEsc})
	if m.aiToolsPanelOpen {
		t.Error("esc must close the panel")
	}
}

func TestAIToolsPanel_opening_detects_current_tool_state(t *testing.T) {
	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude"}, "claude", "none")
	m.settingsSelected = rowAITools
	m.settingsEnter()
	if len(m.aiToolRows) == 0 {
		t.Fatal("opening the panel must populate the tool rows")
	}
	var names []string
	for _, r := range m.aiToolRows {
		names = append(names, r.Name)
	}
	for _, want := range []string{"claude", "opencode", "codex"} {
		var found bool
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("tool rows %v must include %q", names, want)
		}
	}
}

func TestAIToolsPanel_cursor_stays_in_bounds(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex"},
	)
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyUp})
	if m.aiToolsCursor != 0 {
		t.Errorf("cursor above the top = %d, want 0", m.aiToolsCursor)
	}
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyDown})
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyDown})
	if m.aiToolsCursor != 1 {
		t.Errorf("cursor below the bottom = %d, want 1", m.aiToolsCursor)
	}
}

// --- Setting the default ---

func TestAIToolsPanel_d_sets_default_for_installed_tool(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "opencode", Installed: true},
	)
	m.aiTools = []string{"claude", "opencode"}
	m.aiToolsCursor = 1
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if got := m.CurrentAITool(); got != "opencode" {
		t.Errorf("default = %q, want opencode", got)
	}
}

func TestAIToolsPanel_d_is_a_noop_for_missing_tool(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex"}, // not installed
	)
	m.aiTools = []string{"claude"}
	m.aiToolsCursor = 1
	m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if got := m.CurrentAITool(); got != "claude" {
		t.Errorf("default = %q, want claude (a missing tool cannot become default)", got)
	}
}

// --- Installing ---

func TestAIToolsPanel_enter_installs_only_a_missing_tool(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "opencode", Installed: true},
		models.AITool{Name: "codex"},
	)
	m.libDir = "/libs"

	m.aiToolsCursor = 0 // installed → no command
	if _, cmd := m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("↵ on an installed tool must not issue an install command")
	}

	m.aiToolsCursor = 1 // missing → command
	_, cmd := m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("↵ on a missing tool must issue an install command")
	}
	if m.aiToolInstalling != "codex" {
		t.Errorf("aiToolInstalling = %q, want codex", m.aiToolInstalling)
	}
}

func TestAIToolsPanel_enter_on_claude_never_installs(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "claude"}) // pretend missing
	m.libDir = "/libs"
	if _, cmd := m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("↵ on claude must never issue an install command")
	}
}

func TestAIToolInstallDone_success_marks_installed_and_extends_aiTools(t *testing.T) {
	m := panelMenu(t,
		models.AITool{Name: "claude", Installed: true},
		models.AITool{Name: "codex"},
	)
	m.aiTools = []string{"claude"}
	m.aiToolInstalling = "codex"

	// detect reports codex present now; injected so the test never touches PATH.
	m.detectAITools = func() []models.AITool {
		return []models.AITool{
			{Name: "claude", Installed: true},
			{Name: "codex", Installed: true},
		}
	}
	m.applyAIToolInstallDone(aiToolInstallDoneMsg{tool: "codex"})

	if m.aiToolInstalling != "" {
		t.Error("installing marker must clear")
	}
	var codexInstalled bool
	for _, r := range m.aiToolRows {
		if r.Name == "codex" && r.Installed {
			codexInstalled = true
		}
	}
	if !codexInstalled {
		t.Error("codex must be marked installed after a successful install")
	}
	// The menu runs before tmux launches, so a just-installed tool must be
	// selectable for the session about to start.
	var inAvailable bool
	for _, tool := range m.aiTools {
		if tool == "codex" {
			inAvailable = true
		}
	}
	if !inAvailable {
		t.Errorf("aiTools = %v, want codex appended", m.aiTools)
	}
	if m.feedbackStyle != "success" || !strings.Contains(m.feedbackMsg, "Codex") {
		t.Errorf("feedback = %q/%q, want a success mentioning Codex", m.feedbackStyle, m.feedbackMsg)
	}
}

func TestAIToolInstallDone_does_not_duplicate_an_existing_entry(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex", Installed: true})
	m.aiTools = []string{"claude", "codex"}
	m.aiToolInstalling = "codex"
	m.detectAITools = func() []models.AITool {
		return []models.AITool{{Name: "codex", Installed: true}}
	}
	m.applyAIToolInstallDone(aiToolInstallDoneMsg{tool: "codex"})
	var n int
	for _, tool := range m.aiTools {
		if tool == "codex" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("codex appears %d times in aiTools %v, want 1", n, m.aiTools)
	}
}

func TestAIToolInstallDone_failure_reports_and_leaves_state(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex"})
	m.aiTools = []string{"claude"}
	m.aiToolInstalling = "codex"
	m.detectAITools = func() []models.AITool { return []models.AITool{{Name: "codex"}} }

	m.applyAIToolInstallDone(aiToolInstallDoneMsg{tool: "codex", err: errors.New("npm exploded")})

	if m.aiToolInstalling != "" {
		t.Error("installing marker must clear even on failure")
	}
	if m.feedbackStyle != "error" || !strings.Contains(m.feedbackMsg, "Codex") {
		t.Errorf("feedback = %q/%q, want an error mentioning Codex", m.feedbackStyle, m.feedbackMsg)
	}
	for _, tool := range m.aiTools {
		if tool == "codex" {
			t.Error("a failed install must not add codex to the available tools")
		}
	}
}

// --- libDir plumbing ---

func TestSetLibDir(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetLibDir("/some/lib")
	if m.libDir != "/some/lib" {
		t.Errorf("libDir = %q, want /some/lib", m.libDir)
	}
}

// Without a lib dir the panel cannot install; the error must be surfaced rather
// than silently doing nothing.
func TestAIToolsPanel_enter_without_libdir_surfaces_an_error(t *testing.T) {
	m := panelMenu(t, models.AITool{Name: "codex"})
	m.libDir = ""
	if _, cmd := m.updateAIToolsPanel(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("no install command without a lib dir")
	}
	if m.aiToolsErr == nil {
		t.Error("missing lib dir must surface an error in the panel")
	}
}

// --- Mouse ---

// Clicking the AI tools row opens the panel; it must not "cycle" a value the way
// the plain rows do.
func TestClickSettings_on_ai_tools_row_opens_the_panel(t *testing.T) {
	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude"}, "claude", "none")
	m.clickSettings(rowAITools)
	if !m.aiToolsPanelOpen {
		t.Error("clicking the AI tools row must open the panel")
	}
}

// While the panel is open, clicks must not fall through to the menu behind it.
func TestHandleMouse_is_suppressed_while_the_ai_tools_panel_is_open(t *testing.T) {
	m := NewMainMenu([]models.Project{{Name: "p", Path: "/p"}}, []string{"claude"}, "claude", "none")
	m.SetActiveTab(TabSettings)
	m.aiToolsPanelOpen = true
	before := m.settingsSelected

	m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})

	if m.settingsSelected != before {
		t.Errorf("scroll leaked through the open panel: settingsSelected %d → %d", before, m.settingsSelected)
	}
}
