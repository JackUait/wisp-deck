package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/wisp-deck/internal/claudeaccount"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
	"github.com/jackuait/wisp-deck/internal/models"
	"github.com/jackuait/wisp-deck/internal/opencodeconfig"
	"github.com/jackuait/wisp-deck/internal/soundpref"
	"github.com/jackuait/wisp-deck/internal/usage"
	"github.com/jackuait/wisp-deck/internal/util"
)

// bobTickMsg is sent on each bob animation tick.
type bobTickMsg struct{}

// sleepTickMsg is sent on each sleep timer tick.
type sleepTickMsg struct{}

// worktreeDoneMsg is sent after a worktree creation attempt completes.
type worktreeDoneMsg struct {
	path string
	err  error
}

// githubCloneDoneMsg is sent after a background `git clone` of a GitHub URL
// pasted into the add-project path field completes. The clone is dispatched
// as a tea.Cmd (never run inline in Update) because cloning can take many
// seconds and must not freeze the UI event loop.
type githubCloneDoneMsg struct {
	name string
	dest string
	err  error
}

// worktreeRemoveDoneMsg is sent after a background `git worktree remove` attempt
// completes. Removal is dispatched as a tea.Cmd (never run inline in Update) because
// git must rm -rf the entire worktree tree, which can take many seconds on a real
// project and would otherwise freeze the whole UI event loop.
type worktreeRemoveDoneMsg struct {
	branch     string
	projectIdx int
	wtIdx      int
	force      bool // whether this was the --force attempt (no dirty/locked re-prompt)
	err        error
}

// pendingWorktreeDelete holds a worktree awaiting force-removal confirmation.
type pendingWorktreeDelete struct {
	projectIdx int
	wtIdx      int
}

// computeWorktreePath returns the destination path for a new worktree.
// It mirrors the bash compute_worktree_path function in lib/menu-tui.sh.
// branch is sanitised: "origin/" prefix stripped, "/" replaced with "-".
func computeWorktreePath(projectPath, projectName, branch, settingsFile string) string {
	// Strip origin/ prefix
	if strings.HasPrefix(branch, "origin/") {
		branch = branch[len("origin/"):]
	}
	// Replace / with -
	sanitized := strings.ReplaceAll(branch, "/", "-")

	worktreeBase := ""
	if settingsFile != "" {
		if data, err := os.ReadFile(settingsFile); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "worktree_base=") {
					worktreeBase = strings.TrimPrefix(line, "worktree_base=")
					break
				}
			}
		}
	}

	if worktreeBase != "" {
		return filepath.Join(worktreeBase, projectName+"--"+sanitized)
	}
	parentDir := filepath.Dir(projectPath)
	return filepath.Join(parentDir, projectName+"--"+sanitized)
}

const (
	// bobTickInterval is the animation tick rate (~60fps).
	bobTickInterval = 16 * time.Millisecond
	// bobCyclePeriod is the duration of one full bob cycle in milliseconds.
	bobCyclePeriod = 2500.0
	// bobPhaseStep is the phase increment per tick (2*pi / ticks-per-cycle).
	bobPhaseStep = 2 * math.Pi / (bobCyclePeriod / 16.0)
	// ZzzTickEvery controls how many bob ticks between Zzz frame advances (~192ms).
	ZzzTickEvery = 12
	// FeedbackDismissTicks is the number of bob ticks before feedback auto-dismisses (~0.8s).
	FeedbackDismissTicks = 50
)

// MainMenuResult represents the JSON output when the main menu exits.
type MainMenuResult struct {
	Action       string  `json:"action"`
	Name         string  `json:"name,omitempty"`
	Path         string  `json:"path,omitempty"`
	AITool       string  `json:"ai_tool"`
	GhostDisplay string  `json:"ghost_display,omitempty"`
	TabTitle     string  `json:"tab_title,omitempty"`
	SoundName    *string `json:"sound_name,omitempty"`
}

// MenuTab identifies which top-level tab is active.
type MenuTab int

const (
	TabProjects MenuTab = iota
	TabSettings
	TabStats
)

const menuTabCount = 3

// focusRegion is the vertically-stacked region currently receiving arrow input.
// The focus ring is AI switcher (top) ↔ tab bar ↔ body (bottom). ↑/↓ move
// between regions (and within the body); ←/→ act on the focused region.
type focusRegion int

const (
	// FocusBody is the zero value so a freshly-built model starts with the
	// project list focused (the primary action is selecting a project).
	FocusBody focusRegion = iota
	FocusTabs
	FocusAI
	// FocusSubscription is the optional stop directly above the AI switcher,
	// reachable only when the Claude subscription line is changeable.
	FocusSubscription
	// FocusAccount is the optional top stop, above the subscription row, reachable
	// only when the user has at least one managed native Claude login account.
	FocusAccount
)

// MenuLayout describes how the ghost and menu are arranged at a given terminal size.
type MenuLayout struct {
	GhostPosition string // "side", "above", "hidden"
	MenuWidth     int    // Always 48
	MenuHeight    int    // Calculated from items
	FirstItemRow  int    // Row offset of first item within menu box
}

// ClaudeConfig is one selectable Claude settings file (display name + filename).
type ClaudeConfig struct {
	Name string
	File string
}

// ClaudeAccount is one selectable native Claude login (display label + the
// config dir name that isolates its credentials via CLAUDE_CONFIG_DIR).
type ClaudeAccount struct {
	Label string
	Dir   string
}

// actionNames maps action item offsets to their action strings.
var actionNames = []string{"add-project", "delete-project", "open-once", "plain-terminal"}

// actionLabels maps action items to their display labels.
var actionLabels = []struct {
	shortcut string
	label    string
}{
	{"A", "Add new project"},
	{"D", "Delete a project or a worktree"},
	{"O", "Open once"},
	{"P", "Plain terminal"},
	{"S", "Settings"},
	{"T", "Stats"},
}

// aiToolDisplayNames maps tool names to their display names.
var aiToolDisplayNames = map[string]string{
	"claude":   "Claude Code",
	"opencode": "OpenCode",
	"codex":    "Codex",
}

// SystemSounds is the ordered list of macOS system sounds available for notification.
var SystemSounds = []string{
	"Basso", "Blow", "Bottle", "Frog", "Funk", "Glass", "Hero",
	"Morse", "Ping", "Pop", "Purr", "Sosumi", "Submarine", "Tink",
}

// AIToolDisplayName returns the display name for the given AI tool.
// Unknown tools return the tool name as-is.
func AIToolDisplayName(tool string) string {
	if name, ok := aiToolDisplayNames[tool]; ok {
		return name
	}
	return tool
}

// MainMenuModel is the Bubbletea model for the unified main menu.
type MainMenuModel struct {
	projects            []models.Project
	aiTools             []string
	selectedAI          int
	selectedItem        int
	ghostDisplay        string
	ghostSleeping       bool
	bobPhase            float64
	zzzCounter          int
	sleepTimer          int
	width               int
	height              int
	theme               AIToolTheme
	quitting            bool
	result              *MainMenuResult
	updateVersion       string
	appVersion          string
	activeTab           MenuTab
	focus               focusRegion
	settingsSelected    int
	initialGhostDisplay string
	ghostDisplayChanged bool
	tabTitle            string
	initialTabTitle     string
	tabTitleChanged     bool
	soundName           string // "" means Off
	initialSoundName    string
	soundNameChanged    bool
	usageBars           string // "7d", "5h", "both", or "none" — which statusline usage pills show
	initialUsageBars    string
	usageBarsChanged    bool
	themePref           string // "auto" (follow tool) or a preset name
	initialThemePref    string
	themePrefChanged    bool
	zzz                 *ZzzAnimation
	centerOffsetY       int

	// Mouse support: absolute screen coordinates of the menu box's top-left
	// corner (the "╭"), recomputed every View so click/hover hit-testing can map
	// pointer coordinates back to box-relative cells. hoverTab is the tab index
	// currently under the pointer (-1 = none) so the tab bar can highlight a
	// hovered-but-inactive tab.
	menuOriginX int
	menuOriginY int
	hoverTab    int
	hover       hitTarget
	// menuLines is the box-relative rendered frame (the menu box plus any appended
	// modal panel), captured each View so hit-testing can require the pointer cell
	// to actually hold a glyph — trailing padding and inter-element gaps are spaces
	// and so never register as a hover/click.
	menuLines []string
	// modalOriginY is the absolute screen row of the first line of the modal
	// panel (account menu / model map) appended below the menu box, or -1 when no
	// modal is open. Lets the modal's rows be hit-tested like the menu's.
	modalOriginY int
	// modelMapHover marks which non-cursor element of the model-map panel the
	// pointer is over so it can highlight: -1 none, 4 = API key row, 5 = Save
	// button, 6 = Cancel button.
	modelMapHover int
	// modelMapSlotHover is the model slot (0..3) under the pointer, or -1. It is a
	// transient highlight separate from the keyboard cursor (modelMapCursor): it
	// clears the moment the pointer leaves the slots and never moves the cursor.
	modelMapSlotHover int
	// accountMenuHover is the login row under the pointer in the account modal, or
	// -1. Transient highlight, separate from the keyboard cursor (accountMenuCursor).
	accountMenuHover int

	// Inline input mode (add-project or open-once)
	inputMode    string // "", "add-project", "open-once"
	pathInput    textinput.Model
	autocomplete AutocompleteModel
	inputErr     error

	// Folder browser overlay for add-project: non-nil while the user is
	// navigating the filesystem to pick the project folder.
	browser *DirBrowserModel

	// Two-field add-project form state
	nameInput      textinput.Model
	nameTouched    bool // user manually edited name; disable auto-derive
	nameErr        error
	nameWarnShown  bool // true after first Enter on duplicate name; second Enter confirms
	inputFocusPath bool // true = path field focused, false = name field focused

	// GitHub-URL add-project state: a clone is in flight. Keys are ignored
	// (except Ctrl+C) until githubCloneDoneMsg arrives.
	cloning bool
	// cloneSlug/cloneDest label the in-flight clone in the status slot;
	// cloneFrame drives its spinner (advanced by cloneTickMsg).
	cloneSlug  string
	cloneDest  string
	cloneFrame int
	// gitClone runs the actual clone; nil means real `git clone`. Injectable
	// so tests never touch the network.
	gitClone func(url, dest string) error

	// Delete mode
	deleteMode           bool
	deleteSelected       int
	pendingForceDeleteWT *pendingWorktreeDelete

	// Feedback message
	feedbackMsg   string
	feedbackStyle string // "success" or "error"
	feedbackTimer int    // bob ticks remaining

	// Move-flash: briefly highlights the row that was just moved.
	// moveFlashIdx is the project index to flash; -1 means inactive.
	moveFlashIdx   int
	moveFlashTimer int // bob ticks remaining

	// File path for project file operations
	projectsFile string

	// showEscHint is set by AppModel to display "Press Esc again to quit" in
	// the help row instead of the normal key hints — no extra line is added.
	showEscHint bool

	// File path for AI tool preference persistence
	aiToolFile string

	// File path for settings persistence (ghost display, tab title)
	settingsFile string

	// File path for sound features persistence ({tool}-features.json)
	soundFile      string
	soundConfigDir string

	// Claude config selection state
	claudeConfigs     []ClaudeConfig // Standard is implicit index 0, not stored here
	selectedConfig    int            // 0 = Standard, 1.. = claudeConfigs[i-1]
	claudeConfigFile  string         // pointer file path for persistence
	claudeConfigsList string         // name:file list file path (for mutations)
	claudeConfigsDir  string         // directory holding the settings JSON files

	// Claude account (native login) selection state
	claudeAccounts         []ClaudeAccount // Default is implicit index 0, not stored here
	selectedAccount        int             // 0 = Default, 1.. = claudeAccounts[i-1]
	claudeAccountFile      string          // pointer file path for persistence
	claudeAccountsList     string          // label:dir list file path (for mutations)
	claudeAccountsDir      string          // directory holding the per-account config dirs
	defaultAccountLabel    string          // display label for the implicit Default login
	claudeDefaultLabelFile string          // file persisting the Default login's label
	accountColors          map[string]int  // dir → 256-color index (Default under "default")

	// Automatic account switching (rotation proxy) toggle. Stored in its own
	// on/off flag file, shared with wrapper.sh and lib/auto-switch.sh.
	autoSwitch     string // "on" or "off"
	autoSwitchFile string // flag file path for persistence
	keepAwake      string // "on" or "off" — hold the kernel sleep veto while an agent works

	// Login-management panel, opened from the LOGIN row (mirrors the model-map
	// panel that Plan opens). Lists Default + managed logins + an add row.
	accountMenuOpen bool

	// About panel: a small credit card opened with 'a' on the Settings tab and
	// closed with Esc. Purely informational — no selectable rows.
	aboutOpen bool

	// AI-tools panel (Settings → Tools → AI tools): install a missing tool and
	// choose the default. detectAITools is an injectable seam so tests never
	// depend on the machine's PATH; nil means models.DetectAITools.
	aiToolsPanelOpen     bool
	aiToolsCursor        int
	aiToolRows           []models.AITool
	aiToolInstalling     string
	aiToolInstallPct     float64
	aiToolsErr           error
	libDir               string
	disabledToolsFile    string
	aiToolRemovePending  string
	aiToolRemoving       string
	detectAITools        func() []models.AITool
	accountMenuCursor    int  // 0=Default, 1..len=managed logins, len+1=add row
	accountMenuConfirm   bool // delete confirmation showing for the cursor login
	accountMenuInputMode bool // inline label entry (add or rename) is showing
	accountMenuInput     textinput.Model
	accountMenuRenameRow int // -1 = adding a login; 0 = renaming Default; 1..len = renaming a managed login (cursor row)
	accountMenuErr       error

	// Model mapping panel for non-Standard configs
	modelMapOpen     bool
	modelMapCursor   int      // 0-3: which Anthropic slot (opus, sonnet, haiku, fable)
	modelMap         [4]int   // index into modelMapModels for each slot
	modelMapModels   []string // provider-specific model list
	modelMapErr      error
	modelMapKeyMode  bool // true when entering API key
	modelMapKeyInput textinput.Model

	// Worktree expand/collapse state (project index -> expanded)
	expandedWorktrees map[int]bool

	// worktreePendingProjectIdx tracks which project index is waiting for a
	// branch picker result. -1 means no pending worktree.
	worktreePendingProjectIdx int

	// staleConfirmIdx holds the index of the stale project awaiting launch
	// confirmation. -1 means no confirmation is active.
	staleConfirmIdx int

	// Cached stats data for the embedded Stats tab.
	statsLoaded  bool
	statsLoading bool
	statsMonths  []usage.MonthlyUsage
	statsErr     error
	// statsOffset is the top line index of the visible slice of the scrollable
	// month body (line-based, not month-based). statsMaxOffset is the largest
	// valid offset for the current terminal height, recomputed on each render.
	statsOffset    int
	statsMaxOffset int
	// statsCompact hides the per-model breakdown, showing only each month's
	// headline row and progress bar. Toggled by the Full/Compact button or 'c'.
	statsCompact   bool
	hoverStatsMode int // hovered Full/Compact button index, or -1
}

// NewMainMenu creates a new main menu model.
func NewMainMenu(projects []models.Project, aiTools []string, currentAI string, ghostDisplay string) *MainMenuModel {
	selectedAI := 0
	for i, tool := range aiTools {
		if tool == currentAI {
			selectedAI = i
			break
		}
	}

	return &MainMenuModel{
		projects:                  projects,
		aiTools:                   aiTools,
		selectedAI:                selectedAI,
		selectedItem:              0,
		ghostDisplay:              ghostDisplay,
		initialGhostDisplay:       ghostDisplay,
		usageBars:                 "7d",
		initialUsageBars:          "7d",
		themePref:                 "auto",
		initialThemePref:          "auto",
		theme:                     ThemeForTool(currentAI),
		zzz:                       NewZzzAnimation(),
		expandedWorktrees:         make(map[int]bool),
		worktreePendingProjectIdx: -1,
		moveFlashIdx:              -1,
		staleConfirmIdx:           -1,
		defaultAccountLabel:       "Default",
		hoverTab:                  -1,
		hoverStatsMode:            -1,
		modelMapHover:             -1,
		modelMapSlotHover:         -1,
		accountMenuHover:          -1,
	}
}

// SetUpdateVersion sets the available update version string.
func (m *MainMenuModel) SetUpdateVersion(version string) {
	m.updateVersion = version
}

// UpdateVersion returns the pending update version, empty when none.
func (m *MainMenuModel) UpdateVersion() string {
	return m.updateVersion
}

// SetAppVersion sets the running binary's own version (shown in About).
func (m *MainMenuModel) SetAppVersion(version string) {
	m.appVersion = version
}

// AppVersion returns the running binary's own version, empty when unknown.
func (m *MainMenuModel) AppVersion() string {
	return m.appVersion
}

// SelectedItem returns the currently selected item index.
func (m *MainMenuModel) SelectedItem() int {
	return m.selectedItem
}

// TotalItems returns the total number of selectable items
// (projects + expanded worktrees + the add-project row).
func (m *MainMenuModel) TotalItems() int {
	return len(m.projects) + m.expandedWorktreeCount() + 1 // +1 for the add-project row
}

// ToggleWorktrees toggles expand/collapse for the given project index.
// The cursor is anchored to the same logical item before and after the toggle.
// A project with no worktrees expands straight to its only child — the
// add-worktree row — and the cursor lands there, so Enter immediately opens
// the branch picker.
func (m *MainMenuModel) ToggleWorktrees(projectIdx int) {
	if projectIdx < 0 || projectIdx >= len(m.projects) {
		return
	}

	// Snapshot the logical item under the cursor before mutating.
	anchorType, anchorProjectIdx, anchorWorktreeIdx := m.ResolveItem(m.selectedItem)

	if m.expandedWorktrees[projectIdx] {
		delete(m.expandedWorktrees, projectIdx)
	} else {
		m.expandedWorktrees[projectIdx] = true
		if len(m.projects[projectIdx].Worktrees) == 0 {
			m.selectedItem = m.projectToFlatIndex(projectIdx) + 1
			return
		}
	}

	// Restore cursor to the same logical item.
	m.selectedItem = m.resolveToFlatIndex(anchorType, anchorProjectIdx, anchorWorktreeIdx)
}

// resolveToFlatIndex converts a logical item (as returned by ResolveItem) back
// to its flat index in the current list layout. If the item no longer exists
// (e.g. a worktree row after collapse), it falls back to the parent project row.
func (m *MainMenuModel) resolveToFlatIndex(itemType string, projectIdx int, worktreeIdx int) int {
	switch itemType {
	case "project":
		return m.projectToFlatIndex(projectIdx)
	case "worktree":
		// If the project is still expanded, restore to the worktree row.
		if m.expandedWorktrees[projectIdx] && worktreeIdx < len(m.projects[projectIdx].Worktrees) {
			return m.projectToFlatIndex(projectIdx) + 1 + worktreeIdx
		}
		// Project collapsed — fall back to the project row.
		return m.projectToFlatIndex(projectIdx)
	case "add-worktree":
		// If the project is still expanded, restore to the add-worktree row.
		if m.expandedWorktrees[projectIdx] {
			return m.projectToFlatIndex(projectIdx) + 1 + len(m.projects[projectIdx].Worktrees)
		}
		// Project collapsed — fall back to the project row.
		return m.projectToFlatIndex(projectIdx)
	case "add-project":
		return len(m.projects) + m.expandedWorktreeCount()
	}
	return 0
}

// ToggleWorktreesAtCursor toggles expand/collapse for the project that the
// cursor is currently on. If the cursor is on a worktree or add-worktree row,
// it toggles that row's parent project. No-op on action rows.
func (m *MainMenuModel) ToggleWorktreesAtCursor() {
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	switch itemType {
	case "project", "worktree", "add-worktree":
		m.ToggleWorktrees(projectIdx)
	}
	// "add-project" → no-op
}

// expandedWorktreeCount returns the total number of visible worktree entries
// (worktrees + one add-worktree item per expanded project).
func (m *MainMenuModel) expandedWorktreeCount() int {
	count := 0
	for idx := range m.expandedWorktrees {
		if idx < len(m.projects) {
			count += len(m.projects[idx].Worktrees) + 1 // +1 for add-worktree item
		}
	}
	return count
}

// projectToFlatIndex converts a project index to its flat item index,
// accounting for expanded worktrees and add-worktree items above it.
func (m *MainMenuModel) projectToFlatIndex(projectIdx int) int {
	flat := 0
	for i := 0; i < projectIdx; i++ {
		flat++ // the project itself
		if m.expandedWorktrees[i] {
			flat += len(m.projects[i].Worktrees) + 1 // +1 for add-worktree item
		}
	}
	return flat
}

// DeletableItems returns the sorted list of flat indices that are valid delete
// targets: project rows and visible worktree rows. Add-worktree rows and action
// rows are excluded.
func (m *MainMenuModel) DeletableItems() []int {
	var items []int
	for i, proj := range m.projects {
		items = append(items, m.projectToFlatIndex(i))
		if m.expandedWorktrees[i] {
			for j := range proj.Worktrees {
				items = append(items, m.projectToFlatIndex(i)+1+j)
			}
			// skip the add-worktree item at projectToFlatIndex(i)+1+len(proj.Worktrees)
		}
	}
	return items
}

// ResolveItem maps a flat selectedItem index to what it represents.
// Returns: itemType ("project", "worktree", "add-worktree", or "add-project"), projectIdx, worktreeIdx.
// The final selectable index is the "add-project" row.
func (m *MainMenuModel) ResolveItem(flatIdx int) (itemType string, projectIdx int, worktreeIdx int) {
	pos := 0
	for i, proj := range m.projects {
		if flatIdx == pos {
			return "project", i, -1
		}
		pos++
		if m.expandedWorktrees[i] {
			for j := range proj.Worktrees {
				if flatIdx == pos {
					return "worktree", i, j
				}
				pos++
			}
			// Add-worktree item comes after all worktrees
			if flatIdx == pos {
				return "add-worktree", i, -1
			}
			pos++
		}
	}
	// Final selectable item is the add-project row.
	return "add-project", -1, -1
}

// ToggleAllWorktrees expands all projects with worktrees if any are collapsed,
// or collapses all if every one is already expanded.
// The cursor is anchored to the same logical item before and after the toggle.
func (m *MainMenuModel) ToggleAllWorktrees() {
	// Check if all projects with worktrees are already expanded
	allExpanded := true
	for i, proj := range m.projects {
		if len(proj.Worktrees) > 0 && !m.expandedWorktrees[i] {
			allExpanded = false
			break
		}
	}

	// Snapshot the logical item under the cursor before mutating the list.
	anchorType, anchorProjectIdx, anchorWorktreeIdx := m.ResolveItem(m.selectedItem)

	if allExpanded {
		// Collapse all
		m.expandedWorktrees = make(map[int]bool)
	} else {
		// Expand all projects that have worktrees
		for i, proj := range m.projects {
			if len(proj.Worktrees) > 0 {
				m.expandedWorktrees[i] = true
			}
		}
	}

	// Restore cursor to the same logical item.
	m.selectedItem = m.resolveToFlatIndex(anchorType, anchorProjectIdx, anchorWorktreeIdx)
}

// IsExpanded returns whether the given project index is expanded.
func (m *MainMenuModel) IsExpanded(projectIdx int) bool {
	return m.expandedWorktrees[projectIdx]
}

// CurrentAITool returns the name of the currently selected AI tool.
func (m *MainMenuModel) CurrentAITool() string {
	if len(m.aiTools) == 0 {
		return ""
	}
	return m.aiTools[m.selectedAI]
}

// setSelectedAI is the only production mutation point for the active tool.
// Tool selection and its per-tool sound preference must change together.
func (m *MainMenuModel) setSelectedAI(index int) {
	m.selectedAI = index
	m.loadCurrentToolSound()
}

// CycleAITool cycles the AI tool selection forward ("next") or backward ("prev").
// If aiToolFile is set, the new tool is persisted to disk immediately.
func (m *MainMenuModel) CycleAITool(direction string) {
	n := len(m.aiTools)
	if n <= 1 {
		return
	}
	if direction == "next" {
		m.setSelectedAI((m.selectedAI + 1) % n)
	} else {
		m.setSelectedAI((m.selectedAI - 1 + n) % n)
	}
	m.theme = ResolveTheme(m.aiTools[m.selectedAI], m.themePref)
	// The PLAN row only renders for Claude, so cycling to another agent can pull
	// the row out from under the focus ring. Drop focus to the AGENT row below it.
	if m.focus == FocusSubscription && !m.subscriptionFocusable() {
		m.focus = FocusAI
	}
	m.persistAITool()
}

// SetThemePref sets the user's theme preference ("auto" or a preset name) and
// resolves the live palette. Called from the cmd layer with the persisted value.
func (m *MainMenuModel) SetThemePref(pref string) {
	if pref == "" {
		pref = "auto"
	}
	m.themePref = pref
	m.initialThemePref = pref
	m.theme = ResolveTheme(m.CurrentAITool(), pref)
}

// CycleTheme advances the theme preference through ThemePresets (auto → orange →
// … → cyan → auto), recolors the live menu, and persists the choice.
func (m *MainMenuModel) CycleTheme() {
	m.stepTheme(1)
}

// CycleThemeReverse steps the theme preference backwards.
func (m *MainMenuModel) CycleThemeReverse() {
	m.stepTheme(-1)
}

func (m *MainMenuModel) stepTheme(delta int) {
	n := len(ThemePresets)
	idx := 0
	for i, p := range ThemePresets {
		if p == m.themePref {
			idx = i
			break
		}
	}
	idx = (idx + delta + n) % n
	m.themePref = ThemePresets[idx]
	m.theme = ResolveTheme(m.CurrentAITool(), m.themePref)
	m.themePrefChanged = m.themePref != m.initialThemePref
	m.persistSetting("theme", m.themePref)
}

// themeLabel returns the display label for a theme preset (e.g. "auto" → "Auto").
func themeLabel(pref string) string {
	if pref == "" {
		pref = "auto"
	}
	return strings.ToUpper(pref[:1]) + pref[1:]
}

// persistAITool writes the current AI tool to the preference file if set.
func (m *MainMenuModel) persistAITool() {
	if m.aiToolFile == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.aiToolFile), 0755)
	_ = os.WriteFile(m.aiToolFile, []byte(m.CurrentAITool()+"\n"), 0644)
}

// MoveUp moves the selection up by one, wrapping around.
func (m *MainMenuModel) MoveUp() {
	total := m.TotalItems()
	m.selectedItem = (m.selectedItem - 1 + total) % total
}

// MoveDown moves the selection down by one, wrapping around.
func (m *MainMenuModel) MoveDown() {
	total := m.TotalItems()
	m.selectedItem = (m.selectedItem + 1) % total
}

// MoveProjectUp moves the project at the current cursor position one slot up
// in the list, persists the new order, and follows the cursor to the project.
// No-op if the cursor is not on a project or the project is already first.
func (m *MainMenuModel) MoveProjectUp() {
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		return
	}
	if projectIdx == 0 {
		return // already first
	}

	// Build the new order without mutating m.projects yet.
	newProjects := make([]models.Project, len(m.projects))
	copy(newProjects, m.projects)
	newProjects[projectIdx], newProjects[projectIdx-1] = newProjects[projectIdx-1], newProjects[projectIdx]

	// Persist first — only mutate memory on success.
	if err := RewriteProjectsFile(newProjects, m.projectsFile); err != nil {
		m.setFeedback("Failed to save order", "error")
		return
	}

	// Apply to memory.
	m.projects = newProjects

	// Keep expand states consistent with their projects.
	expandedA := m.expandedWorktrees[projectIdx]
	expandedB := m.expandedWorktrees[projectIdx-1]
	if expandedA {
		m.expandedWorktrees[projectIdx-1] = true
	} else {
		delete(m.expandedWorktrees, projectIdx-1)
	}
	if expandedB {
		m.expandedWorktrees[projectIdx] = true
	} else {
		delete(m.expandedWorktrees, projectIdx)
	}

	// Move cursor to follow the project.
	m.selectedItem = m.projectToFlatIndex(projectIdx - 1)
	m.setMoveFlash(projectIdx - 1)
}

// MoveProjectDown moves the project at the current cursor position one slot
// down in the list, persists the new order, and follows the cursor.
// No-op if the cursor is not on a project or the project is already last.
func (m *MainMenuModel) MoveProjectDown() {
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	if itemType != "project" {
		return
	}
	if projectIdx == len(m.projects)-1 {
		return // already last
	}

	// Build the new order without mutating m.projects yet.
	newProjects := make([]models.Project, len(m.projects))
	copy(newProjects, m.projects)
	newProjects[projectIdx], newProjects[projectIdx+1] = newProjects[projectIdx+1], newProjects[projectIdx]

	// Persist first — only mutate memory on success.
	if err := RewriteProjectsFile(newProjects, m.projectsFile); err != nil {
		m.setFeedback("Failed to save order", "error")
		return
	}

	// Apply to memory.
	m.projects = newProjects

	// Keep expand states consistent with their projects.
	expandedA := m.expandedWorktrees[projectIdx]
	expandedB := m.expandedWorktrees[projectIdx+1]
	if expandedA {
		m.expandedWorktrees[projectIdx+1] = true
	} else {
		delete(m.expandedWorktrees, projectIdx+1)
	}
	if expandedB {
		m.expandedWorktrees[projectIdx] = true
	} else {
		delete(m.expandedWorktrees, projectIdx)
	}

	// Move cursor to follow the project.
	m.selectedItem = m.projectToFlatIndex(projectIdx + 1)
	m.setMoveFlash(projectIdx + 1)
}

// JumpTo jumps to the given 1-indexed project number.
// Does nothing if n is out of range or beyond the number of projects.
func (m *MainMenuModel) JumpTo(n int) {
	if n < 1 || n > len(m.projects) {
		return
	}
	m.selectedItem = m.projectToFlatIndex(n - 1)
}

// SetSize updates the stored terminal dimensions.
func (m *MainMenuModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// GhostDisplay returns the ghost display mode.
func (m *MainMenuModel) GhostDisplay() string {
	return m.ghostDisplay
}

// TabTitle returns the tab title mode.
func (m *MainMenuModel) TabTitle() string {
	return m.tabTitle
}

// SetTabTitle sets the tab title mode and records the initial value.
func (m *MainMenuModel) SetTabTitle(mode string) {
	m.tabTitle = mode
	m.initialTabTitle = mode
}

// usageBarsOrder is the forward cycle order for the Usage bars setting; the
// statusline reads the persisted value (usage_bars=) to decide which usage pills
// to draw.
var usageBarsOrder = []string{"7d", "5h", "both", "none"}

// UsageBars returns the usage-bars mode ("7d", "5h", "both", or "none").
func (m *MainMenuModel) UsageBars() string { return m.usageBars }

// SetUsageBars sets the usage-bars mode and records it as the initial value.
func (m *MainMenuModel) SetUsageBars(mode string) {
	if !isValidUsageBars(mode) {
		mode = "7d"
	}
	m.usageBars = mode
	m.initialUsageBars = mode
}

func isValidUsageBars(mode string) bool {
	for _, v := range usageBarsOrder {
		if v == mode {
			return true
		}
	}
	return false
}

// stepUsageBars advances the usage-bars mode by delta through usageBarsOrder
// (wrapping) and persists the new value.
func (m *MainMenuModel) stepUsageBars(delta int) {
	n := len(usageBarsOrder)
	idx := 0
	for i, v := range usageBarsOrder {
		if v == m.usageBars {
			idx = i
			break
		}
	}
	idx = (idx + delta%n + n) % n
	m.usageBars = usageBarsOrder[idx]
	m.usageBarsChanged = m.usageBars != m.initialUsageBars
	m.persistSetting("usage_bars", m.usageBars)
}

// CycleUsageBars advances the usage-bars mode: 7d -> 5h -> both -> none -> 7d.
func (m *MainMenuModel) CycleUsageBars() { m.stepUsageBars(1) }

// CycleUsageBarsReverse steps the usage-bars mode backwards.
func (m *MainMenuModel) CycleUsageBarsReverse() { m.stepUsageBars(-1) }

// CycleTabTitle cycles through tab title modes: full -> project -> model -> full.
// "model" leaves the AI tool's own title (the one the model set) showing.
func (m *MainMenuModel) CycleTabTitle() {
	switch m.tabTitle {
	case "full":
		m.tabTitle = "project"
	case "project":
		m.tabTitle = "model"
	default:
		m.tabTitle = "full"
	}
	m.tabTitleChanged = m.tabTitle != m.initialTabTitle
	m.persistSetting("tab_title", m.tabTitle)
}

// CycleTabTitleReverse cycles tab title modes in reverse: full -> model -> project -> full.
func (m *MainMenuModel) CycleTabTitleReverse() {
	switch m.tabTitle {
	case "full":
		m.tabTitle = "model"
	case "model":
		m.tabTitle = "project"
	default:
		m.tabTitle = "full"
	}
	m.tabTitleChanged = m.tabTitle != m.initialTabTitle
	m.persistSetting("tab_title", m.tabTitle)
}

// SetSoundName sets the sound name and records the initial value.
// Empty string means sound is off.
func (m *MainMenuModel) SetSoundName(name string) {
	m.soundName = name
	m.initialSoundName = name
}

// SoundName returns the current sound name ("" means off).
func (m *MainMenuModel) SoundName() string {
	return m.soundName
}

func (m *MainMenuModel) loadCurrentToolSound() {
	tool := m.CurrentAITool()
	if m.soundConfigDir == "" || tool == "" {
		return
	}
	m.soundFile = filepath.Join(m.soundConfigDir, tool+"-features.json")
	m.soundName = soundpref.Read(m.soundFile)
	m.initialSoundName = m.soundName
	m.soundNameChanged = false
}

// CycleSoundName cycles forward through system sounds + Off.
func (m *MainMenuModel) CycleSoundName() {
	previous := m.soundName
	if m.soundName == "" {
		m.soundName = SystemSounds[0]
	} else {
		idx := -1
		for i, s := range SystemSounds {
			if s == m.soundName {
				idx = i
				break
			}
		}
		if idx >= 0 && idx < len(SystemSounds)-1 {
			m.soundName = SystemSounds[idx+1]
		} else {
			m.soundName = ""
		}
	}
	m.soundNameChanged = m.soundName != m.initialSoundName
	if !m.persistSound() {
		m.soundName = previous
		m.soundNameChanged = m.soundName != m.initialSoundName
		m.feedbackMsg = "Failed to save Idle Sound"
		return
	}
	m.feedbackMsg = ""
	m.previewSound()
}

// CycleSoundNameReverse cycles backward through Off + system sounds.
func (m *MainMenuModel) CycleSoundNameReverse() {
	previous := m.soundName
	if m.soundName == "" {
		m.soundName = SystemSounds[len(SystemSounds)-1]
	} else {
		idx := -1
		for i, s := range SystemSounds {
			if s == m.soundName {
				idx = i
				break
			}
		}
		if idx > 0 {
			m.soundName = SystemSounds[idx-1]
		} else {
			m.soundName = ""
		}
	}
	m.soundNameChanged = m.soundName != m.initialSoundName
	if !m.persistSound() {
		m.soundName = previous
		m.soundNameChanged = m.soundName != m.initialSoundName
		m.feedbackMsg = "Failed to save Idle Sound"
		return
	}
	m.feedbackMsg = ""
	m.previewSound()
}

// previewSound plays the current sound in the background using afplay.
func (m *MainMenuModel) previewSound() {
	if m.soundName == "" {
		return
	}
	path := "/System/Library/Sounds/" + m.soundName + ".aiff"
	go func() {
		cmd := exec.Command("afplay", path)
		_ = cmd.Start()
	}()
}

// persistSound writes the current sound state to the features JSON file.
func (m *MainMenuModel) persistSound() bool {
	if m.soundFile == "" {
		return true
	}
	if err := os.MkdirAll(filepath.Dir(m.soundFile), 0755); err != nil {
		return false
	}

	return soundpref.WithExclusiveLock(m.soundFile, func() error {
		// Preserve other keys only from a bounded, unambiguous regular file.
		// Invalid or special-file input becomes a fresh document so a writer
		// cannot hang or normalize a stale opt-in into enabled audio.
		existing, err := soundpref.LoadDocument(m.soundFile)
		if err != nil {
			existing = map[string]any{}
		}

		if m.soundName == "" {
			existing["sound"] = false
			delete(existing, "sound_name")
		} else {
			existing["sound"] = true
			existing["sound_name"] = m.soundName
		}

		data, err := json.Marshal(existing)
		if err != nil {
			return err
		}
		return writeFileAtomic(m.soundFile, append(data, '\n'), 0644)
	}) == nil
}

// SetClaudeConfigFile sets the pointer file path used to persist the active config.
func (m *MainMenuModel) SetClaudeConfigFile(path string) { m.claudeConfigFile = path }

// SetClaudeConfigPaths records the list-file and configs-directory paths the
// inline management panel needs to create, rename, and delete config files.
func (m *MainMenuModel) SetClaudeConfigPaths(listFile, dir string) {
	m.claudeConfigsList = listFile
	m.claudeConfigsDir = dir
}

// APIKeyInputOpen reports whether the API key input is showing.
func (m *MainMenuModel) APIKeyInputOpen() bool { return m.modelMapOpen }

// SetClaudeConfigs stores the managed config list (excluding the implicit Standard).
func (m *MainMenuModel) SetClaudeConfigs(configs []ClaudeConfig) { m.claudeConfigs = configs }

// SetActiveClaudeConfig selects the entry matching filename ("" or no match = Standard).
func (m *MainMenuModel) SetActiveClaudeConfig(file string) {
	m.selectedConfig = 0
	if file == "" {
		return
	}
	for i, c := range m.claudeConfigs {
		if c.File == file {
			m.selectedConfig = i + 1
			return
		}
	}
}

// CurrentClaudeConfigName returns the active config's display name.
func (m *MainMenuModel) CurrentClaudeConfigName() string {
	if m.selectedConfig <= 0 || m.selectedConfig > len(m.claudeConfigs) {
		return "Standard Claude"
	}
	return m.claudeConfigs[m.selectedConfig-1].Name
}

// CurrentClaudeConfigFile returns the active config's filename ("" for Standard).
func (m *MainMenuModel) CurrentClaudeConfigFile() string {
	if m.selectedConfig <= 0 || m.selectedConfig > len(m.claudeConfigs) {
		return ""
	}
	return m.claudeConfigs[m.selectedConfig-1].File
}

// ClaudeConfigVisible reports whether the Settings › Subscription row should be
// shown. Subscriptions are shared across every agent — the active plan selects
// Claude's --settings file AND drives OpenCode's default model via the sync — so
// the setting stays editable regardless of the selected agent. The header PLAN
// line is narrower: it names a Claude subscription, so it only renders under
// Claude (see subscriptionRowCount).
func (m *MainMenuModel) ClaudeConfigVisible() bool { return true }

// subscriptionFocusable reports whether the PLAN/subscription row is a reachable
// focus stop: its header row must actually render, and it must offer something to
// switch to beyond Standard — i.e. at least one custom config with an API key.
func (m *MainMenuModel) subscriptionFocusable() bool {
	return m.subscriptionRowCount() > 0 && len(m.mainSubscriptionRing()) > 1
}

// configHasKey reports whether the custom config file carries an API key.
func (m *MainMenuModel) configHasKey(file string) bool {
	return claudeconfig.ReadAPIKey(m.claudeConfigsDir, file) != ""
}

// mainSubscriptionRing returns the selectedConfig values the main page lets the
// user switch between, in order: Standard (0) plus each custom config that has
// an API key. Keyless configs are hidden from the main page — they are finished
// in Settings first.
func (m *MainMenuModel) mainSubscriptionRing() []int {
	ring := []int{0} // Standard is always available
	for i, c := range m.claudeConfigs {
		if m.configHasKey(c.File) {
			ring = append(ring, i+1)
		}
	}
	return ring
}

// CycleMainSubscription moves the active subscription through the main-page ring
// (Standard + keyed configs only) and persists the choice. Keyless configs are
// skipped. If the active config is not on the ring (e.g. a keyless config picked
// in Settings), cycling resumes from Standard.
func (m *MainMenuModel) CycleMainSubscription(direction string) {
	ring := m.mainSubscriptionRing()
	if len(ring) <= 1 {
		return
	}
	pos := 0
	for i, v := range ring {
		if v == m.selectedConfig {
			pos = i
			break
		}
	}
	n := len(ring)
	if direction == "prev" {
		pos = (pos - 1 + n) % n
	} else {
		pos = (pos + 1) % n
	}
	m.selectedConfig = ring[pos]
	m.persistClaudeConfig()
}

// CycleClaudeConfig moves through [Standard, configs...] and persists the choice.
func (m *MainMenuModel) CycleClaudeConfig(direction string) {
	n := len(m.claudeConfigs) + 1 // +1 for Standard
	if direction == "prev" {
		m.selectedConfig = (m.selectedConfig - 1 + n) % n
	} else {
		m.selectedConfig = (m.selectedConfig + 1) % n
	}
	m.persistClaudeConfig()
}

// persistClaudeConfig writes the active filename ("" clears) to the pointer file.
func (m *MainMenuModel) persistClaudeConfig() {
	if m.claudeConfigFile == "" {
		return
	}
	file := m.CurrentClaudeConfigFile()
	if file == "" {
		_ = os.Remove(m.claudeConfigFile)
		m.syncOpenCode()
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.claudeConfigFile), 0755)
	_ = os.WriteFile(m.claudeConfigFile, []byte(file+"\n"), 0644)
	m.syncOpenCode()
}

// SetClaudeAccountFile sets the pointer file path for account persistence.
func (m *MainMenuModel) SetClaudeAccountFile(path string) { m.claudeAccountFile = path }

// SetClaudeAccounts stores the managed account list (excluding the implicit Default).
func (m *MainMenuModel) SetClaudeAccounts(accounts []ClaudeAccount) {
	m.claudeAccounts = accounts
	m.ensureAccountColors()
}

// SetClaudeAccountPaths records the list file and config-dir root for mutations.
func (m *MainMenuModel) SetClaudeAccountPaths(listFile, dir string) {
	m.claudeAccountsList = listFile
	m.claudeAccountsDir = dir
	m.ensureAccountColors()
}

// accountColorsFile is the sibling of the accounts list that persists each
// login's color, keyed by dir. Empty when no list path is configured (so tests
// and headless runs without account files simply skip coloring).
func (m *MainMenuModel) accountColorsFile() string {
	if m.claudeAccountsList == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(m.claudeAccountsList), "claude-account-colors")
}

// ensureAccountColors assigns (and persists) a distinct color for the implicit
// Default login and every managed account that lacks one, then caches the whole
// map. It is idempotent and safe to call whenever the account set or paths
// change. The statusline assigns colors the same way into the same file, so an
// account keeps one identity color across both surfaces.
func (m *MainMenuModel) ensureAccountColors() {
	file := m.accountColorsFile()
	if file == "" {
		return
	}
	if m.accountColors == nil {
		m.accountColors = map[string]int{}
	}
	keys := []string{"default"}
	for _, a := range m.claudeAccounts {
		keys = append(keys, a.Dir)
	}
	for _, k := range keys {
		if _, ok := m.accountColors[k]; ok {
			continue
		}
		m.accountColors[k] = claudeaccount.ColorFor(file, k)
	}
}

// accountColor returns the lipgloss color for an account dir (Default keys under
// "default"), and whether one is assigned. Callers fall back to the prior
// green/gray convention when false, so surfaces stay styled even without a
// colors file.
func (m *MainMenuModel) accountColor(dir string) (lipgloss.Color, bool) {
	if dir == "" {
		dir = "default"
	}
	if c, ok := m.accountColors[dir]; ok {
		return lipgloss.Color(strconv.Itoa(c)), true
	}
	return "", false
}

// SetActiveClaudeAccount selects the entry matching dir ("" or no match = Default).
func (m *MainMenuModel) SetActiveClaudeAccount(dir string) {
	m.selectedAccount = 0
	if dir == "" {
		return
	}
	for i, a := range m.claudeAccounts {
		if a.Dir == dir {
			m.selectedAccount = i + 1
			return
		}
	}
}

// CurrentClaudeAccountLabel returns the active account's display label.
func (m *MainMenuModel) CurrentClaudeAccountLabel() string {
	if m.selectedAccount <= 0 || m.selectedAccount > len(m.claudeAccounts) {
		return m.DefaultAccountLabel()
	}
	return m.claudeAccounts[m.selectedAccount-1].Label
}

// DefaultAccountLabel returns the display label for the implicit Default login.
func (m *MainMenuModel) DefaultAccountLabel() string {
	if m.defaultAccountLabel == "" {
		return "Default"
	}
	return m.defaultAccountLabel
}

// SetClaudeDefaultLabel sets the implicit Default login's display label.
func (m *MainMenuModel) SetClaudeDefaultLabel(label string) {
	if label == "" {
		label = "Default"
	}
	m.defaultAccountLabel = label
}

// SetClaudeDefaultLabelFile records where the Default login's label is persisted.
func (m *MainMenuModel) SetClaudeDefaultLabelFile(path string) { m.claudeDefaultLabelFile = path }

// CurrentClaudeAccountDir returns the active account's dir name ("" for Default).
func (m *MainMenuModel) CurrentClaudeAccountDir() string {
	if m.selectedAccount <= 0 || m.selectedAccount > len(m.claudeAccounts) {
		return ""
	}
	return m.claudeAccounts[m.selectedAccount-1].Dir
}

// accountFocusable is always false: the top-of-page account switcher was removed
// from the header, so its LOGIN row is never a focus stop. Account switching
// lives in Settings › Account, the 'L' key, and the ledger pill instead.
func (m *MainMenuModel) accountFocusable() bool {
	return false
}

// CycleAccount moves through [Default, accounts...] and persists the choice.
func (m *MainMenuModel) CycleAccount(direction string) {
	n := len(m.claudeAccounts) + 1 // +1 for Default
	if n <= 1 {
		return
	}
	if direction == "prev" {
		m.selectedAccount = (m.selectedAccount - 1 + n) % n
	} else {
		m.selectedAccount = (m.selectedAccount + 1) % n
	}
	m.persistClaudeAccount()
}

// persistClaudeAccount writes the active dir name to the pointer file. Default
// (empty) removes the file so Claude falls back to the standard Keychain login.
func (m *MainMenuModel) persistClaudeAccount() {
	if m.claudeAccountFile == "" {
		return
	}
	dir := m.CurrentClaudeAccountDir()
	if dir == "" {
		_ = os.Remove(m.claudeAccountFile)
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.claudeAccountFile), 0755)
	_ = os.WriteFile(m.claudeAccountFile, []byte(dir+"\n"), 0644)
}

// syncOpenCode mirrors the current subscriptions into OpenCode's global config.
// Best-effort: errors are swallowed inside opencodeconfig.Sync.
func (m *MainMenuModel) syncOpenCode() {
	if m.claudeConfigsList == "" || m.claudeConfigsDir == "" {
		return
	}
	home, _ := os.UserHomeDir()
	_ = opencodeconfig.Sync(opencodeconfig.Inputs{
		ListFile:    m.claudeConfigsList,
		ConfigsDir:  m.claudeConfigsDir,
		PointerFile: m.claudeConfigFile,
		Home:        home,
	})
}

// soundNameForResult returns a pointer to the sound name if changed, nil if unchanged.
func (m *MainMenuModel) soundNameForResult() *string {
	if m.soundNameChanged {
		return &m.soundName
	}
	return nil
}

// InSettingsMode returns true if the menu is currently showing the settings panel.
func (m *MainMenuModel) InSettingsMode() bool { return m.activeTab == TabSettings }

// EnterSettings switches the menu to settings mode with the body focused.
func (m *MainMenuModel) EnterSettings() {
	m.activeTab = TabSettings
	m.settingsSelected = 0
	m.focus = FocusBody
}

// ExitSettings returns from settings mode to the main menu.
func (m *MainMenuModel) ExitSettings() { m.activeTab = TabProjects }

// Focus returns the region currently receiving arrow-key input.
func (m *MainMenuModel) Focus() focusRegion { return m.focus }

// SetFocus moves the focus ring to the given region.
func (m *MainMenuModel) SetFocus(f focusRegion) { m.focus = f }

// ActiveTab returns the currently selected top-level tab.
func (m *MainMenuModel) ActiveTab() MenuTab { return m.activeTab }

// SetActiveTab switches to the given tab.
func (m *MainMenuModel) SetActiveTab(t MenuTab) { m.activeTab = t }

// CycleTab moves to the next/prev tab, wrapping across all tabs.
// Any direction other than "prev" is treated as "next".
func (m *MainMenuModel) CycleTab(direction string) {
	if direction == "prev" {
		m.activeTab = (m.activeTab - 1 + menuTabCount) % menuTabCount
	} else {
		m.activeTab = (m.activeTab + 1) % menuTabCount
	}
}

// Settings rows are keyed by a stable handler index shared by the renderer
// (render_settings.go), the key handlers (settingsEnter / settingsValueLeft /
// settingsValueRight) and the section grouping (settingsSections). Naming them
// keeps those three in step: the handlers previously hardcoded 6 and 7 while the
// tail rows were derived arithmetically from the row count, so appending a row
// silently aimed ↵-on-Account at the Auto-switch toggle.
const (
	rowMascot       = 0
	rowTabTitle     = 1
	rowIdleSound    = 2
	rowTheme        = 3
	rowUsageBars    = 4
	rowSubscription = 5
	rowAccount      = 6
	rowAutoSwitch   = 7
	rowAITools      = 8
	rowKeepAwake    = 9
)

// settingsItemCount returns the number of settings rows.
func (m *MainMenuModel) settingsItemCount() int { return rowKeepAwake + 1 }

// loginRowIndex is the index of the Login row.
func (m *MainMenuModel) loginRowIndex() int { return rowAccount }

// autoSwitchRowIndex is the index of the Auto-switch toggle.
func (m *MainMenuModel) autoSwitchRowIndex() int { return rowAutoSwitch }

// settingsSectionTitle labels the section headers the settings items are grouped
// under, in visual (top-to-bottom) order.
type settingsSection struct {
	title   string
	indices []int
}

// settingsSections returns the settings items grouped under section headers, in
// visual order. Item indices are the stable handler indices (see settingsEnter),
// which do NOT match visual order: Idle sound (2) moves out of the Appearance
// block into its own section.
func (m *MainMenuModel) settingsSections() []settingsSection {
	appearance := []int{rowMascot, rowTabTitle, rowTheme, rowUsageBars}
	account := []int{}
	if m.ClaudeConfigVisible() {
		account = append(account, rowSubscription)
	}
	account = append(account, rowAccount, rowAutoSwitch)
	return []settingsSection{
		{title: "Appearance", indices: appearance},
		{title: "Tools", indices: []int{rowAITools}},
		{title: "Notifications", indices: []int{rowIdleSound}},
		{title: "Power", indices: []int{rowKeepAwake}},
		{title: "Account", indices: account},
	}
}

// settingsItemOrder returns the settings item indices in visual (top-to-bottom)
// order — the flattened section grouping used for keyboard/mouse navigation.
func (m *MainMenuModel) settingsItemOrder() []int {
	var order []int
	for _, s := range m.settingsSections() {
		order = append(order, s.indices...)
	}
	return order
}

// settingsVisualPos returns the position of a settings item index within the
// visual order, or -1 when not present.
func (m *MainMenuModel) settingsVisualPos(idx int) int {
	for i, v := range m.settingsItemOrder() {
		if v == idx {
			return i
		}
	}
	return -1
}

// settingsStep moves the settings selection by delta visual rows. wrap enables
// wrap-around (vim j/k); without it the selection clamps at the ends.
func (m *MainMenuModel) settingsStep(delta int, wrap bool) {
	order := m.settingsItemOrder()
	if len(order) == 0 {
		return
	}
	pos := m.settingsVisualPos(m.settingsSelected)
	if pos < 0 {
		m.settingsSelected = order[0]
		return
	}
	next := pos + delta
	if wrap {
		next = ((next % len(order)) + len(order)) % len(order)
	} else if next < 0 || next >= len(order) {
		return
	}
	m.settingsSelected = order[next]
}

// SetAutoSwitchFile records the on/off flag file and loads its current value.
func (m *MainMenuModel) SetAutoSwitchFile(path string) {
	m.autoSwitchFile = path
	m.autoSwitch = readAutoSwitch(path)
}

// AutoSwitchEnabled reports whether automatic account switching is on.
func (m *MainMenuModel) AutoSwitchEnabled() bool { return m.autoSwitch == "on" }

// CycleAutoSwitch toggles automatic account switching and persists it.
func (m *MainMenuModel) CycleAutoSwitch() {
	if m.autoSwitch == "on" {
		m.autoSwitch = "off"
	} else {
		m.autoSwitch = "on"
	}
	if m.autoSwitchFile != "" {
		_ = os.MkdirAll(filepath.Dir(m.autoSwitchFile), 0o755)
		_ = os.WriteFile(m.autoSwitchFile, []byte(m.autoSwitch+"\n"), 0o644)
	}
}

// keepAwakeRowIndex is the index of the Keep-awake toggle.
func (m *MainMenuModel) keepAwakeRowIndex() int { return rowKeepAwake }

// KeepAwakeEnabled reports whether a working agent should hold the machine awake.
func (m *MainMenuModel) KeepAwakeEnabled() bool { return m.keepAwake == "on" }

// SetKeepAwake records the launch-time value; anything but "on" reads as off.
func (m *MainMenuModel) SetKeepAwake(v string) {
	if v == "on" {
		m.keepAwake = "on"
		return
	}
	m.keepAwake = "off"
}

// CycleKeepAwake toggles keep-awake and persists it. The bash side re-reads the
// setting each watcher tick, so turning it off releases a mid-turn session at
// once; turning it on prompts for the sudo rule when the menu closes.
func (m *MainMenuModel) CycleKeepAwake() {
	if m.keepAwake == "on" {
		m.keepAwake = "off"
	} else {
		m.keepAwake = "on"
	}
	m.persistSetting("keep_awake", m.keepAwake)
}

// readAutoSwitch reads the on/off flag file; anything other than "on" is off.
func readAutoSwitch(path string) string {
	if path == "" {
		return "off"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "off"
	}
	if strings.TrimSpace(string(data)) == "on" {
		return "on"
	}
	return "off"
}

// LoadClaudeConfigsList parses a name:file list file into ClaudeConfig entries.
// It delegates to internal/claudeconfig (the single source of truth) and maps
// to the local ClaudeConfig type.
func LoadClaudeConfigsList(path string) []ClaudeConfig {
	loaded := claudeconfig.Load(path)
	if loaded == nil {
		return nil
	}
	out := make([]ClaudeConfig, len(loaded))
	for i, c := range loaded {
		out[i] = ClaudeConfig{Name: c.Name, File: c.File}
	}
	return out
}

// ReadActiveClaudeConfig returns the active filename from the pointer file ("" if none/standard).
func ReadActiveClaudeConfig(path string) string {
	return claudeconfig.GetActive(path)
}

// LoadClaudeAccountsList parses a label:dir list file into ClaudeAccount entries.
// It delegates to internal/claudeaccount (the single source of truth) and maps
// to the local ClaudeAccount type.
func LoadClaudeAccountsList(path string) []ClaudeAccount {
	loaded := claudeaccount.Load(path)
	if loaded == nil {
		return nil
	}
	out := make([]ClaudeAccount, len(loaded))
	for i, a := range loaded {
		out[i] = ClaudeAccount{Label: a.Label, Dir: a.Dir}
	}
	return out
}

// ReadActiveClaudeAccount returns the active account dir from the pointer file
// ("" if none/default).
func ReadActiveClaudeAccount(path string) string {
	return claudeaccount.GetActive(path)
}

// ReadDefaultAccountLabel returns the implicit Default login's custom label, or
// "Default" if none is set.
func ReadDefaultAccountLabel(path string) string {
	return claudeaccount.GetDefaultLabel(path)
}

// CycleGhostDisplay cycles through ghost display modes: animated -> static -> none -> animated.
func (m *MainMenuModel) CycleGhostDisplay() {
	switch m.ghostDisplay {
	case "animated":
		m.ghostDisplay = "static"
	case "static":
		m.ghostDisplay = "none"
	default:
		m.ghostDisplay = "animated"
	}
	m.ghostDisplayChanged = m.ghostDisplay != m.initialGhostDisplay
	m.persistSetting("ghost_display", m.ghostDisplay)
}

// CycleGhostDisplayReverse cycles ghost display in reverse order: animated → none → static → animated.
func (m *MainMenuModel) CycleGhostDisplayReverse() {
	switch m.ghostDisplay {
	case "animated":
		m.ghostDisplay = "none"
	case "none":
		m.ghostDisplay = "static"
	default:
		m.ghostDisplay = "animated"
	}
	m.ghostDisplayChanged = m.ghostDisplay != m.initialGhostDisplay
	m.persistSetting("ghost_display", m.ghostDisplay)
}

// persistSetting writes a key=value pair to the settings file.
// Creates the file if it doesn't exist, updates the key if it does.
//
// The write is atomic (temp file + rename). The wrapper's loading splash reads
// this same file from a SEPARATE process the instant a new tab opens; a plain
// truncate-then-write leaves a window where that reader sees an empty/partial
// file and the loader falls back to the tool's default palette — which looks
// like the theme "not changing" on the first tab after a change. An atomic
// rename guarantees every reader sees either the complete old or complete new
// file, never a torn one.
func (m *MainMenuModel) persistSetting(key, value string) {
	if m.settingsFile == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.settingsFile), 0755)
	entry := key + "=" + value
	var out string
	data, err := os.ReadFile(m.settingsFile)
	if err != nil {
		out = entry + "\n"
	} else {
		lines := strings.Split(string(data), "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, key+"=") {
				lines[i] = entry
				found = true
				break
			}
		}
		if !found {
			// Insert before any trailing empty line
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				lines = append(lines[:len(lines)-1], entry, "")
			} else {
				lines = append(lines, entry)
			}
		}
		out = strings.Join(lines, "\n")
	}
	writeFileAtomic(m.settingsFile, []byte(out), 0644)
}

// writeFileAtomic writes data to path via a temp file in the same directory then
// renames it into place, so a concurrent reader never observes a partial write.
// Failures preserve the previous complete file; a truncate fallback would
// recreate the torn-read window this helper exists to prevent.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// SetSleepTimer sets the sleep inactivity timer to the given number of seconds.
func (m *MainMenuModel) SetSleepTimer(seconds int) { m.sleepTimer = seconds }

// ShouldSleep returns true if the sleep timer has reached the threshold (120s).
func (m *MainMenuModel) ShouldSleep() bool { return m.sleepTimer >= 120 }

// IsSleeping returns true if the ghost is currently in sleeping state.
func (m *MainMenuModel) IsSleeping() bool { return m.ghostSleeping }

// Wake resets the ghost to awake state and clears the sleep timer.
func (m *MainMenuModel) Wake() {
	m.ghostSleeping = false
	m.sleepTimer = 0
	if m.zzz != nil {
		m.zzz.Reset()
	}
}

// CenterOffsetY returns the vertical centering offset calculated in View().
func (m *MainMenuModel) CenterOffsetY() int { return m.centerOffsetY }

// MenuOriginX returns the absolute screen column of the menu box's left edge,
// calculated in View(). Exposed so tests can target glyph columns precisely now
// that hit-testing requires the pointer to be on an element, not its padding.
func (m *MainMenuModel) MenuOriginX() int { return m.menuOriginX }

// InputMode returns the current inline input mode ("", "add-project", "open-once").
func (m *MainMenuModel) InputMode() string { return m.inputMode }

// InInputMode returns true if the menu is currently in an inline input mode.
func (m *MainMenuModel) InInputMode() bool { return m.inputMode != "" }

// InDeleteMode returns true if the menu is currently in delete mode.
func (m *MainMenuModel) InDeleteMode() bool { return m.deleteMode }

// WantsEsc implements EscInterceptor. It returns true when the menu is in a
// sub-mode (settings, stats, input, or delete) where Esc should navigate back within
// the menu rather than triggering the AppModel double-Esc quit flow.
func (m *MainMenuModel) WantsEsc() bool {
	return m.activeTab != TabProjects || m.inputMode != "" || m.deleteMode
}

// SetSettingsMode directly sets settings mode — intended for tests only.
func (m *MainMenuModel) SetSettingsMode(v bool) {
	if v {
		m.activeTab = TabSettings
	} else {
		m.activeTab = TabProjects
	}
}

// SetShowEscHint tells the model to display the "Press Esc again to quit"
// hint inside the help row instead of the normal key hints.
func (m *MainMenuModel) SetShowEscHint(v bool) { m.showEscHint = v }

// EnterInputModeForTest calls the real enterInputMode — intended for tests only.
func (m *MainMenuModel) EnterInputModeForTest(mode string) { m.enterInputMode(mode) } //nolint:unparam

// EnterDeleteModeForTest directly sets delete mode — intended for tests only.
func (m *MainMenuModel) EnterDeleteModeForTest() { m.deleteMode = true }

// DeleteSelected returns the index of the selected item in delete mode.
func (m *MainMenuModel) DeleteSelected() int { return m.deleteSelected }

// SettingsSelected returns the currently highlighted settings item index.
func (m *MainMenuModel) SettingsSelected() int { return m.settingsSelected }

// FeedbackMsg returns the current feedback message.
func (m *MainMenuModel) FeedbackMsg() string { return m.feedbackMsg }

// FeedbackStyle returns the current feedback style ("success" or "error").
func (m *MainMenuModel) FeedbackStyle() string { return m.feedbackStyle }

// SetProjectsFile sets the file path for project file operations.
func (m *MainMenuModel) SetProjectsFile(path string) { m.projectsFile = path }

// ProjectsFile returns the file path for project file operations.
func (m *MainMenuModel) ProjectsFile() string { return m.projectsFile }

// SetAIToolFile sets the file path for AI tool preference persistence.
// When set, CycleAITool writes the new tool to this file immediately.
func (m *MainMenuModel) SetAIToolFile(path string) { m.aiToolFile = path }

// SetLibDir points the AI-tools panel at wisp-deck's lib/ directory, which it
// sources to run the existing bash installers.
func (m *MainMenuModel) SetLibDir(path string) { m.libDir = path }

// SetDisabledToolsFile points the AI-tools panel at the disabled-tools file
// (one tool name per line) shared with wrapper.sh.
func (m *MainMenuModel) SetDisabledToolsFile(path string) { m.disabledToolsFile = path }

// AIToolFile returns the file path for AI tool preference persistence.
func (m *MainMenuModel) AIToolFile() string { return m.aiToolFile }

// SetSettingsFile sets the file path for settings persistence.
func (m *MainMenuModel) SetSettingsFile(path string) { m.settingsFile = path }

// SettingsFile returns the file path for settings persistence.
func (m *MainMenuModel) SettingsFile() string { return m.settingsFile }

// SetSoundFile sets the file path for sound features persistence.
func (m *MainMenuModel) SetSoundFile(path string) {
	m.soundFile = path
	if path == "" {
		m.soundConfigDir = ""
		return
	}
	m.soundConfigDir = filepath.Dir(path)
	m.soundName = soundpref.Read(path)
	m.initialSoundName = m.soundName
	m.soundNameChanged = false
}

// SetWorktreeProject sets the pending worktree project index (for testing).
func (m *MainMenuModel) SetWorktreeProject(projectIdx int, _ string) {
	m.worktreePendingProjectIdx = projectIdx
}

// SoundFile returns the file path for sound features persistence.
func (m *MainMenuModel) SoundFile() string { return m.soundFile }

// InputFocusPath returns true when the path field is focused in the two-field form.
func (m *MainMenuModel) InputFocusPath() bool { return m.inputFocusPath }

// NameInputValue returns the current name field value.
func (m *MainMenuModel) NameInputValue() string { return m.nameInput.Value() }

// PathInputValue returns the current path field value.
func (m *MainMenuModel) PathInputValue() string { return m.pathInput.Value() }

// SetPathInputValue sets the path field value — intended for tests only. In
// add-project mode it also closes the folder browser, simulating the browser
// having chosen this path, so keys reach the classic path/name flow.
func (m *MainMenuModel) SetPathInputValue(v string) {
	m.browser = nil
	m.pathInput.SetValue(v)
}

// BrowserOpenForTest reports whether the folder browser overlay is open.
func (m *MainMenuModel) BrowserOpenForTest() bool { return m.browser != nil }

// BrowserCwdForTest returns the open browser's current directory.
func (m *MainMenuModel) BrowserCwdForTest() string {
	if m.browser == nil {
		return ""
	}
	return m.browser.Cwd()
}

// SetNameInputValue sets the name field value — intended for tests only.
func (m *MainMenuModel) SetNameInputValue(v string) { m.nameInput.SetValue(v) }

// SetNameTouched sets the nameTouched flag — intended for tests only.
// When setting to true, the name input is widened to remove the (auto) hint reservation.
func (m *MainMenuModel) SetNameTouched(v bool) {
	m.nameTouched = v
	if v {
		m.nameInput.Width = menuContentWidth - 11
	} else {
		m.nameInput.Width = menuContentWidth - 18
	}
}

// SetNameWarnShown sets the nameWarnShown flag — intended for tests only.
func (m *MainMenuModel) SetNameWarnShown(v bool) { m.nameWarnShown = v }

// NameErr returns the current name field error.
func (m *MainMenuModel) NameErr() error { return m.nameErr }

// Cloning returns true while a GitHub clone dispatched from the add-project
// form is in flight.
func (m *MainMenuModel) Cloning() bool { return m.cloning }

// SetGitCloneForTest injects the clone function so tests never hit the network.
func (m *MainMenuModel) SetGitCloneForTest(fn func(url, dest string) error) { m.gitClone = fn }

// NameWarnShown returns true when the duplicate-name soft-warn is active.
func (m *MainMenuModel) NameWarnShown() bool { return m.nameWarnShown }

// TriggerAutoDeriveName calls maybeAutoDeriveName — intended for tests only.
func (m *MainMenuModel) TriggerAutoDeriveName() { m.maybeAutoDeriveName() }

// BobPhase returns the current bob animation phase (0 to 2*pi).
func (m *MainMenuModel) BobPhase() float64 { return m.bobPhase }

// BobOffset returns the current vertical offset (0 or 1) computed from the sine wave phase.
func (m *MainMenuModel) BobOffset() int {
	if math.Sin(m.bobPhase) < 0 {
		return 1
	}
	return 0
}

// ZzzFrame returns the current Zzz animation frame index.
func (m *MainMenuModel) ZzzFrame() int {
	if m.zzz == nil {
		return 0
	}
	return m.zzz.Frame()
}

// NewBobTickMsg creates a bobTickMsg for testing.
func NewBobTickMsg() tea.Msg { return bobTickMsg{} }

// NewSleepTickMsg creates a sleepTickMsg for testing.
func NewSleepTickMsg() tea.Msg { return sleepTickMsg{} }

// Result returns the menu result, or nil if the menu has not exited.
func (m *MainMenuModel) Result() *MainMenuResult {
	return m.result
}

// StaleConfirmIdx returns the index of the stale project awaiting launch
// confirmation, or -1 if no confirmation is active.
func (m *MainMenuModel) StaleConfirmIdx() int { return m.staleConfirmIdx }

// CalculateLayout determines how the ghost and menu should be arranged.
func (m *MainMenuModel) CalculateLayout(width, height int) MenuLayout {
	numProjects := len(m.projects)
	// Projects = 2 rows each, worktrees = 2 rows each (branch + path),
	// add-worktree = 1 row each.
	projectRows := numProjects * 2
	expandedProjectCount := 0
	for idx := range m.expandedWorktrees {
		if idx < len(m.projects) {
			expandedProjectCount++
		}
	}
	wtEntryCount := m.expandedWorktreeCount() - expandedProjectCount // actual worktrees only
	worktreeRows := wtEntryCount * 2                                 // worktrees are 2 rows each
	addWorktreeRows := expandedProjectCount                          // add-worktree is 1 row each
	// emptyStateRow: renderProjectRows emits one extra centered-prompt row when
	// there are no projects.
	emptyStateRow := 0
	if numProjects == 0 {
		emptyStateRow = 1
	}
	// 12 fixed chrome lines: top + title + tab-bar + sep + leading-blank +
	// spacer-before-add + add-project + add-project-hint + sep-before-action +
	// action-bar + bottom + help. Plus the optional subscription row (Claude only).
	menuHeight := 12 + m.subscriptionRowCount() + m.accountRowCount() + projectRows + worktreeRows + addWorktreeRows + emptyStateRow
	menuWidth := 58

	ghostPosition := "hidden"
	// Side layout: width >= 48 + 3 + 28 + 3 = 82
	if width >= menuWidth+3+28+3 {
		ghostPosition = "side"
	} else if height >= menuHeight+15+2 {
		// Above layout: enough vertical space for ghost (15 lines) + gap (2)
		ghostPosition = "above"
	}

	return MenuLayout{
		GhostPosition: ghostPosition,
		MenuWidth:     menuWidth,
		MenuHeight:    menuHeight,
		FirstItemRow:  0,
	}
}

// MapRowToItem maps a terminal Y coordinate (0-indexed click row within the
// menu box) to a selectable item index. Returns -1 if the click is not on a
// valid item row.
func (m *MainMenuModel) MapRowToItem(clickY int) int {
	// Menu box row layout:
	// Row 0: top border
	// (optional) account/LOGIN row (very top, above the title)
	// Row 1: title row
	// (optional) subscription row (Claude only)
	// switcher gap (blank row separating switchers from the tab bar)
	// tab bar
	// separator
	// leading empty row
	// Then project items start
	// (the update notice reuses a header row above, so it never shifts this)
	startRow := 6 + m.subscriptionRowCount() + m.accountRowCount()

	currentRow := startRow
	flatIdx := 0

	for i, proj := range m.projects {
		// Project takes 2 rows (name + path)
		if clickY == currentRow || clickY == currentRow+1 {
			return flatIdx
		}
		currentRow += 2
		flatIdx++

		// Expanded worktrees (2 rows each: branch + path) + add-worktree (1 row)
		if m.expandedWorktrees[i] {
			for range proj.Worktrees {
				if clickY == currentRow || clickY == currentRow+1 {
					return flatIdx
				}
				currentRow += 2
				flatIdx++
			}
			// Add-worktree item (1 row)
			if clickY == currentRow {
				return flatIdx
			}
			currentRow++
			flatIdx++
		}
	}

	// Blank spacer row before the add-project row.
	currentRow++

	// Add-project row (label + hint subtitle, 2 rows; the final selectable item).
	if clickY == currentRow || clickY == currentRow+1 {
		return flatIdx
	}

	return -1
}

// ghostDisplayForResult returns the ghost display value to include in the result,
// or empty string if unchanged.
func (m *MainMenuModel) ghostDisplayForResult() string {
	if m.ghostDisplayChanged {
		return m.ghostDisplay
	}
	return ""
}

// tabTitleForResult returns the tab title value to include in the result,
// or empty string if unchanged.
func (m *MainMenuModel) tabTitleForResult() string {
	if m.tabTitleChanged {
		return m.tabTitle
	}
	return ""
}

// selectCurrent produces a result for the currently selected item.
// Returns a tea.Cmd if the item requires a navigation action (e.g. push a screen).
func (m *MainMenuModel) selectCurrent() tea.Cmd {
	itemType, projectIdx, worktreeIdx := m.ResolveItem(m.selectedItem)

	switch itemType {
	case "project":
		if m.projects[projectIdx].Stale {
			m.staleConfirmIdx = projectIdx
			return nil
		}
		m.result = &MainMenuResult{
			Action:       "select-project",
			Name:         m.projects[projectIdx].Name,
			Path:         m.projects[projectIdx].Path,
			AITool:       m.CurrentAITool(),
			GhostDisplay: m.ghostDisplayForResult(),
			TabTitle:     m.tabTitleForResult(),
			SoundName:    m.soundNameForResult(),
		}
	case "worktree":
		m.result = &MainMenuResult{
			Action:       "select-project",
			Name:         m.projects[projectIdx].Name,
			Path:         m.projects[projectIdx].Worktrees[worktreeIdx].Path,
			AITool:       m.CurrentAITool(),
			GhostDisplay: m.ghostDisplayForResult(),
			TabTitle:     m.tabTitleForResult(),
			SoundName:    m.soundNameForResult(),
		}
	case "add-worktree":
		// Store the project index so BranchPickerDoneMsg can reference it.
		m.worktreePendingProjectIdx = projectIdx
		// Load branches and push the branch picker onto the navigation stack.
		wtCmd := exec.Command("git", "-C", m.projects[projectIdx].Path, "worktree", "list", "--porcelain")
		wtOut, _ := wtCmd.Output()
		mainBranch := models.ParseMainBranch(string(wtOut))
		branches := models.ListBranches(m.projects[projectIdx].Path)
		worktrees := models.DetectWorktrees(m.projects[projectIdx].Path)
		available := models.FilterAvailableBranches(branches, worktrees, mainBranch)
		picker := NewBranchPicker(available, m.theme, m.projects[projectIdx].Path)
		return func() tea.Msg { return PushScreenMsg{Model: picker} }
	case "add-project":
		// handled by Enter routing; selectCurrent only sets results.
	}

	if m.result != nil {
		m.quitting = true
	}
	return nil
}

// createRandomWorktreeAtCursor creates a worktree with a random four-word
// name (new branch off HEAD) for the project under the cursor. On a worktree
// or add-worktree row the parent project is used. The git call runs inside a
// tea.Cmd — `git worktree add` checks out a full tree, which can take seconds
// on a real project and must never block the Update loop.
func (m *MainMenuModel) createRandomWorktreeAtCursor() tea.Cmd {
	if m.activeTab != TabProjects {
		return nil
	}
	itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
	switch itemType {
	case "project", "worktree", "add-worktree":
	default:
		return nil
	}
	name := models.RandomWorktreeName()
	projectPath := m.projects[projectIdx].Path
	worktreePath := computeWorktreePath(projectPath, m.projects[projectIdx].Name, name, m.settingsFile)
	return func() tea.Msg {
		if err := models.AddWorktree(projectPath, worktreePath, name); err != nil {
			return worktreeDoneMsg{err: err, path: worktreePath}
		}
		return worktreeDoneMsg{path: worktreePath}
	}
}

// setActionResult produces a result for the given action name.
func (m *MainMenuModel) setActionResult(action string) {
	m.result = &MainMenuResult{
		Action:       action,
		AITool:       m.CurrentAITool(),
		GhostDisplay: m.ghostDisplayForResult(),
		TabTitle:     m.tabTitleForResult(),
		SoundName:    m.soundNameForResult(),
	}
	m.quitting = true
}

// ensureStatsLoad kicks off the async usage aggregation the first time the
// Stats tab is shown. Returns nil if already loaded or loading.
func (m *MainMenuModel) ensureStatsLoad() tea.Cmd {
	if m.statsLoaded || m.statsLoading {
		return nil
	}
	m.statsLoading = true
	home, _ := os.UserHomeDir()
	_, opencodeDir, codexDir, cachePath := usage.DefaultPaths(home)
	claudeDirs := usage.ClaudeAccountProjectDirs(home)
	return func() tea.Msg {
		months, err := usage.AggregateAll(claudeDirs, opencodeDir, codexDir, cachePath)
		if err != nil {
			return statsErrMsg{err: err}
		}
		return statsLoadedMsg{months: months}
	}
}

// bobTickCmd returns a command that sends a bobTickMsg at ~60fps.
func (m *MainMenuModel) bobTickCmd() tea.Cmd {
	return tea.Tick(bobTickInterval, func(t time.Time) tea.Msg {
		return bobTickMsg{}
	})
}

// sleepTickCmd returns a command that sends a sleepTickMsg after 1 second.
func (m *MainMenuModel) sleepTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return sleepTickMsg{}
	})
}

// Init implements tea.Model. Starts animation ticks when in animated mode.
func (m *MainMenuModel) Init() tea.Cmd {
	return tea.Batch(m.initCmds()...)
}

// initCmds collects the startup commands: the usage-stats ingest always, plus
// the animation tickers in animated mode.
func (m *MainMenuModel) initCmds() []tea.Cmd {
	var cmds []tea.Cmd
	if cmd := m.ensureStatsLoad(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if m.ghostDisplay == "animated" {
		cmds = append(cmds, m.bobTickCmd())
		cmds = append(cmds, m.sleepTickCmd())
	}
	return cmds
}

// InitCmdCountForTest returns how many startup commands Init batches —
// intended for tests distinguishing static (stats only) from animated.
func (m *MainMenuModel) InitCmdCountForTest() int { return len(m.initCmds()) }

// Update implements tea.Model. Handles key bindings, window resize, and animation ticks.
func (m *MainMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case bobTickMsg:
		if m.ghostDisplay == "animated" {
			m.bobPhase += bobPhaseStep
			if m.bobPhase >= 2*math.Pi {
				m.bobPhase -= 2 * math.Pi
			}
			if m.ghostSleeping && m.zzz != nil {
				m.zzzCounter++
				if m.zzzCounter >= ZzzTickEvery {
					m.zzzCounter = 0
					m.zzz.Tick()
				}
			}
			if m.feedbackTimer > 0 {
				m.feedbackTimer--
				if m.feedbackTimer == 0 {
					m.feedbackMsg = ""
					m.feedbackStyle = ""
				}
			}
			if m.moveFlashTimer > 0 {
				m.moveFlashTimer--
				if m.moveFlashTimer == 0 {
					m.moveFlashIdx = -1
				}
			}
			return m, m.bobTickCmd()
		}
		return m, nil

	case sleepTickMsg:
		if m.ghostDisplay == "animated" && !m.ghostSleeping {
			m.sleepTimer++
			if m.sleepTimer >= 120 {
				m.ghostSleeping = true
			}
			return m, m.sleepTickCmd()
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil

	case BranchPickerDoneMsg:
		if !msg.Selected || m.worktreePendingProjectIdx < 0 {
			m.worktreePendingProjectIdx = -1
			return m, nil
		}
		projectIdx := m.worktreePendingProjectIdx
		m.worktreePendingProjectIdx = -1
		projectPath := m.projects[projectIdx].Path
		projectName := m.projects[projectIdx].Name
		branch := msg.Branch
		settingsFile := m.settingsFile
		return m, func() tea.Msg {
			worktreePath := computeWorktreePath(projectPath, projectName, branch, settingsFile)
			cmd := exec.Command("git", "-C", projectPath, "worktree", "add", worktreePath, branch)
			if err := cmd.Run(); err != nil {
				return worktreeDoneMsg{err: err, path: worktreePath}
			}
			return worktreeDoneMsg{path: worktreePath}
		}

	case aiToolInstallDoneMsg:
		m.applyAIToolInstallDone(msg)
		return m, nil

	case aiToolRemoveDoneMsg:
		m.applyAIToolRemoveDone(msg)
		return m, nil

	case installTickMsg:
		if m.applyInstallTick() {
			return m, installTickCmd()
		}
		return m, nil

	case worktreeDoneMsg:
		if msg.err != nil {
			m.feedbackMsg = "Failed to create worktree: " + msg.err.Error()
			m.feedbackStyle = "error"
		} else {
			m.feedbackMsg = "Created worktree at " + msg.path
			m.feedbackStyle = "success"
		}
		m.feedbackTimer = FeedbackDismissTicks
		// Reload worktrees so the new entry appears.
		models.PopulateWorktrees(m.projects)
		return m, nil

	case githubCloneDoneMsg:
		return m.applyGitHubCloneDone(msg)

	case cloneTickMsg:
		if m.cloning {
			m.cloneFrame++
			return m, cloneTickCmd()
		}
		return m, nil

	case worktreeRemoveDoneMsg:
		return m.applyWorktreeRemoveDone(msg)

	case statsLoadedMsg:
		m.statsMonths = msg.months
		m.statsLoading = false
		m.statsLoaded = true
		return m, nil

	case statsErrMsg:
		m.statsErr = msg.err
		m.statsLoading = false
		m.statsLoaded = true
		return m, nil

	case tea.MouseMsg:
		// Reset sleep state on any mouse activity
		m.Wake()
		return m.handleMouse(msg)

	case tea.KeyMsg:
		// Reset sleep state on any keypress
		m.Wake()

		// Modal sub-screens intercept all key handling regardless of focus.
		if m.aboutOpen {
			return m.updateAbout(msg)
		}
		if m.modelMapOpen {
			return m.updateModelMap(msg)
		}
		if m.accountMenuOpen {
			return m.updateAccountMenu(msg)
		}
		if m.aiToolsPanelOpen {
			return m.updateAIToolsPanel(msg)
		}
		if m.browser != nil {
			return m.updateBrowser(msg)
		}
		if m.inputMode != "" {
			return m.updateInputMode(msg)
		}
		if m.deleteMode {
			return m.updateDeleteMode(msg)
		}

		// Stale confirmation mode intercepts all key handling
		if m.staleConfirmIdx >= 0 {
			switch msg.String() {
			case "y", "Y":
				savedIdx := m.staleConfirmIdx
				m.staleConfirmIdx = -1
				m.result = &MainMenuResult{
					Action:       "select-project",
					Name:         m.projects[savedIdx].Name,
					Path:         m.projects[savedIdx].Path,
					AITool:       m.CurrentAITool(),
					GhostDisplay: m.ghostDisplayForResult(),
					TabTitle:     m.tabTitleForResult(),
					SoundName:    m.soundNameForResult(),
				}
				m.quitting = true
				return m, tea.Quit
			default:
				// n, N, Enter, Esc, anything else → cancel
				m.staleConfirmIdx = -1
				return m, nil
			}
		}

		return m.routeFocusKey(msg)
	}

	return m, nil
}

// routeFocusKey dispatches a keypress through the focus ring. Arrow keys act on
// the focused region; Tab/Shift+Tab and the rune accelerators jump directly.
func (m *MainMenuModel) routeFocusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.setActionResult("quit")
		return m, tea.Quit
	case tea.KeyTab:
		m.CycleTab("next")
		m.focus = FocusTabs
		if m.activeTab == TabStats {
			return m, m.ensureStatsLoad()
		}
		return m, nil
	case tea.KeyShiftTab:
		m.CycleTab("prev")
		m.focus = FocusTabs
		if m.activeTab == TabStats {
			return m, m.ensureStatsLoad()
		}
		return m, nil
	case tea.KeyShiftUp:
		if m.focus == FocusBody && m.activeTab == TabProjects {
			m.MoveProjectUp()
		}
		return m, nil
	case tea.KeyShiftDown:
		if m.focus == FocusBody && m.activeTab == TabProjects {
			m.MoveProjectDown()
		}
		return m, nil
	case tea.KeyUp:
		return m, m.focusUp()
	case tea.KeyDown:
		return m, m.focusDown()
	case tea.KeyLeft:
		return m, m.focusLeft()
	case tea.KeyRight:
		return m, m.focusRight()
	case tea.KeyEnter:
		return m.focusEnter()
	case tea.KeyEsc:
		return m.focusEsc()
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			return m.handleRune(msg.Runes[0])
		}
	}
	return m, nil
}

// focusUp moves up the focus ring or up within the focused body region. When
// the body cursor is already at its first item, focus escapes to the tab bar.
func (m *MainMenuModel) focusUp() tea.Cmd {
	switch m.focus {
	case FocusAccount:
		// already the top stop
	case FocusSubscription:
		if m.accountFocusable() {
			m.focus = FocusAccount
		}
	case FocusAI:
		if m.subscriptionFocusable() {
			m.focus = FocusSubscription
		} else if m.accountFocusable() {
			m.focus = FocusAccount
		}
	case FocusTabs:
		m.focus = FocusAI
	case FocusBody:
		switch m.activeTab {
		case TabSettings:
			if m.settingsVisualPos(m.settingsSelected) <= 0 {
				m.focus = FocusTabs
			} else {
				m.settingsStep(-1, false)
			}
		case TabStats:
			if m.statsOffset <= 0 {
				m.focus = FocusTabs
			} else {
				m.statsOffset--
			}
		default: // projects
			if m.selectedItem <= 0 {
				m.focus = FocusTabs
			} else {
				m.MoveUp()
			}
		}
	}
	return nil
}

// focusDown moves down the focus ring or down within the focused body region.
func (m *MainMenuModel) focusDown() tea.Cmd {
	switch m.focus {
	case FocusAccount:
		if m.subscriptionFocusable() {
			m.focus = FocusSubscription
		} else {
			m.focus = FocusAI
		}
	case FocusSubscription:
		m.focus = FocusAI
	case FocusAI:
		m.focus = FocusTabs
	case FocusTabs:
		m.focus = FocusBody
		if m.activeTab == TabStats {
			return m.ensureStatsLoad()
		}
	case FocusBody:
		switch m.activeTab {
		case TabSettings:
			m.settingsStep(1, false)
		case TabStats:
			m.statsScrollDown()
		default: // projects
			if m.selectedItem < m.TotalItems()-1 {
				m.MoveDown()
			}
		}
	}
	return nil
}

// focusLeft acts on the focused region: cycle AI tool, switch tab, or decrement
// the focused settings value.
func (m *MainMenuModel) focusLeft() tea.Cmd {
	switch m.focus {
	case FocusAccount:
		m.CycleAccount("prev")
	case FocusAI:
		m.CycleAITool("prev")
	case FocusSubscription:
		m.CycleMainSubscription("prev")
	case FocusTabs:
		m.CycleTab("prev")
		if m.activeTab == TabStats {
			return m.ensureStatsLoad()
		}
	case FocusBody:
		if m.activeTab == TabSettings {
			m.settingsValueLeft()
		}
		// On the Stats tab, ← targets the Full label of the view-mode toggle
		// (keyboard equivalent of clicking it), instead of blindly flipping.
		if m.activeTab == TabStats {
			m.setStatsCompact(false)
		}
	}
	return nil
}

// focusRight mirrors focusLeft in the opposite direction.
func (m *MainMenuModel) focusRight() tea.Cmd {
	switch m.focus {
	case FocusAccount:
		m.CycleAccount("next")
	case FocusAI:
		m.CycleAITool("next")
	case FocusSubscription:
		m.CycleMainSubscription("next")
	case FocusTabs:
		m.CycleTab("next")
		if m.activeTab == TabStats {
			return m.ensureStatsLoad()
		}
	case FocusBody:
		if m.activeTab == TabSettings {
			m.settingsValueRight()
		}
		// On the Stats tab, → targets the Compact label of the view-mode toggle
		// (keyboard equivalent of clicking it), instead of blindly flipping.
		if m.activeTab == TabStats {
			m.setStatsCompact(true)
		}
	}
	return nil
}

// focusEnter activates the focused region: drill from the tab bar into the body,
// or trigger the body item's primary action.
func (m *MainMenuModel) focusEnter() (tea.Model, tea.Cmd) {
	switch m.focus {
	case FocusAccount:
		// Enter on the LOGIN row opens the login-management panel (switch / add /
		// remove), mirroring how the PLAN row opens its model-map panel.
		m.openAccountMenu()
		return m, nil
	case FocusAI:
		return m, nil
	case FocusSubscription:
		// Enter opens the model-map panel for a custom subscription (Standard
		// has nothing to configure).
		if m.selectedConfig > 0 {
			m.openModelMap()
		}
		return m, nil
	case FocusTabs:
		m.focus = FocusBody
		if m.activeTab == TabStats {
			return m, m.ensureStatsLoad()
		}
		return m, nil
	default: // FocusBody
		switch m.activeTab {
		case TabSettings:
			return m.settingsEnter()
		case TabStats:
			return m, nil
		default: // projects
			return m.projectsEnter()
		}
	}
}

// focusEsc backs out: from any non-Projects tab return to Projects; on Projects
// it bubbles up to AppModel for the double-Esc quit flow.
func (m *MainMenuModel) focusEsc() (tea.Model, tea.Cmd) {
	if m.activeTab != TabProjects {
		m.SetActiveTab(TabProjects)
		m.focus = FocusBody
		return m, nil
	}
	return m, func() tea.Msg { return PopScreenMsg{} }
}

// projectsEnter triggers the primary action for the selected Projects-tab row.
func (m *MainMenuModel) projectsEnter() (tea.Model, tea.Cmd) {
	itemType, _, _ := m.ResolveItem(m.selectedItem)
	switch itemType {
	case "add-project":
		return m.enterInputMode("add-project")
	case "add-worktree":
		if cmd := m.selectCurrent(); cmd != nil {
			return m, cmd
		}
		return m, nil
	}
	if cmd := m.selectCurrent(); cmd != nil {
		return m, cmd
	}
	return m, tea.Quit
}

// setStatsCompact switches the Stats view between full (per-model rows) and compact
// (progress bar only). Toggling changes the body height, so the scroll is
// re-anchored to the top to avoid landing past the shorter content.
func (m *MainMenuModel) setStatsCompact(compact bool) {
	if m.statsCompact == compact {
		return
	}
	m.statsCompact = compact
	m.statsOffset = 0
	m.persistSetting("stats_mode", statsModeString(compact))
}

// toggleStatsCompact flips the Full/Compact view mode.
func (m *MainMenuModel) toggleStatsCompact() {
	m.setStatsCompact(!m.statsCompact)
}

// statsModeString maps the compact flag to its persisted setting value.
func statsModeString(compact bool) string {
	if compact {
		return "compact"
	}
	return "full"
}

// SetStatsMode seeds the Stats view mode from a saved preference at launch:
// "compact" starts on the bar-only view, "full" (or anything else) on the full
// per-model breakdown. It sets the flag directly so restoring never re-persists.
func (m *MainMenuModel) SetStatsMode(mode string) {
	switch mode {
	case "compact":
		m.statsCompact = true
	case "full":
		m.statsCompact = false
	}
}

// statsScrollDown scrolls the stats body down one line, bounded by statsMaxOffset
// (recomputed on each render from the terminal height). The render also re-clamps,
// so a stale bound here can never scroll past the content.
func (m *MainMenuModel) statsScrollDown() {
	if m.statsOffset < m.statsMaxOffset {
		m.statsOffset++
	}
}

// handleRune processes a single rune keypress.
func (m *MainMenuModel) handleRune(r rune) (tea.Model, tea.Cmd) {
	r = TranslateRune(r)
	switch r {
	case 'j':
		m.runeMoveDown()
		return m, nil
	case 'k':
		m.runeMoveUp()
		return m, nil
	case 'c':
		if m.activeTab == TabStats {
			m.toggleStatsCompact()
		}
		return m, nil
	case 'J':
		if m.activeTab == TabProjects {
			m.MoveProjectDown()
		}
		return m, nil
	case 'K':
		if m.activeTab == TabProjects {
			m.MoveProjectUp()
		}
		return m, nil
	case 'a', 'A':
		// On the Settings tab 'a' opens the About card; everywhere else it keeps
		// its long-standing add-project meaning.
		if m.activeTab == TabSettings {
			m.aboutOpen = true
			return m, nil
		}
		return m.enterInputMode("add-project")
	case 'd', 'D':
		return m.enterDeleteMode()
	case 'o', 'O':
		return m.enterInputMode("open-once")
	case 'p', 'P':
		m.setActionResult("plain-terminal")
		return m, tea.Quit
	case 'l', 'L':
		// Add a native Claude login. Always available (the LOGIN switcher row is
		// hidden until at least one managed account exists), so this is the entry
		// point for the first account: open the login panel straight into the
		// inline label input.
		m.openAccountMenu()
		return m, m.enterAccountAddInput()
	case 'w', 'W':
		m.ToggleWorktreesAtCursor()
		return m, nil
	case 'n', 'N':
		return m, m.createRandomWorktreeAtCursor()
	case 'u', 'U':
		// Run the pending update (the header notice's button). Inert until an
		// update is actually available.
		if m.updateVersion != "" {
			m.setActionResult("update")
			return m, tea.Quit
		}
		return m, nil
	case 's', 'S':
		m.SetActiveTab(TabSettings)
		m.settingsSelected = 0
		m.focus = FocusBody
		return m, nil
	case 't', 'T':
		m.SetActiveTab(TabStats)
		m.focus = FocusBody
		return m, m.ensureStatsLoad()
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		n := int(r - '0')
		if n > len(m.projects) {
			return m, nil
		}
		m.JumpTo(n)
		if cmd := m.selectCurrent(); cmd != nil {
			return m, cmd
		}
		return m, tea.Quit
	}
	return m, nil
}

// runeMoveDown handles the vim 'j' accelerator, scoped to the active tab's body
// (wraps within the region rather than escaping focus like the arrow keys).
func (m *MainMenuModel) runeMoveDown() {
	switch m.activeTab {
	case TabSettings:
		m.settingsStep(1, true)
	case TabStats:
		m.statsScrollDown()
	default:
		m.MoveDown()
	}
}

// runeMoveUp handles the vim 'k' accelerator, scoped to the active tab's body.
func (m *MainMenuModel) runeMoveUp() {
	switch m.activeTab {
	case TabSettings:
		m.settingsStep(-1, true)
	case TabStats:
		if m.statsOffset > 0 {
			m.statsOffset--
		}
	default:
		m.MoveUp()
	}
}

// settingsEnter activates the selected settings row: cycle a value or open the
// model-map panel.
func (m *MainMenuModel) settingsEnter() (tea.Model, tea.Cmd) {
	switch m.settingsSelected {
	case rowMascot:
		m.CycleGhostDisplay()
	case rowTabTitle:
		m.CycleTabTitle()
	case rowIdleSound:
		m.CycleSoundName()
	case rowTheme:
		m.CycleTheme()
	case rowUsageBars:
		m.CycleUsageBars()
	case rowSubscription:
		if m.selectedConfig > 0 {
			m.openModelMap()
		}
	case rowAccount:
		// Open the login-management panel (switch / add / remove logins).
		m.openAccountMenu()
		return m, nil
	case rowAITools:
		m.openAIToolsPanel()
		return m, nil
	case m.autoSwitchRowIndex():
		m.CycleAutoSwitch()
	case m.keepAwakeRowIndex():
		m.CycleKeepAwake()
	}
	return m, nil
}

// settingsValueRight increments the focused settings row's value.
func (m *MainMenuModel) settingsValueRight() {
	switch m.settingsSelected {
	case rowMascot:
		m.CycleGhostDisplay()
	case rowTabTitle:
		m.CycleTabTitle()
	case rowIdleSound:
		m.CycleSoundName()
	case rowTheme:
		m.CycleTheme()
	case rowUsageBars:
		m.CycleUsageBars()
	case rowSubscription:
		m.CycleClaudeConfig("next")
	case rowAccount:
		m.CycleAccount("next")
	case m.autoSwitchRowIndex():
		m.CycleAutoSwitch()
	case m.keepAwakeRowIndex():
		m.CycleKeepAwake()
	}
}

// settingsValueLeft decrements the focused settings row's value.
func (m *MainMenuModel) settingsValueLeft() {
	switch m.settingsSelected {
	case rowMascot:
		m.CycleGhostDisplayReverse()
	case rowTabTitle:
		m.CycleTabTitleReverse()
	case rowIdleSound:
		m.CycleSoundNameReverse()
	case rowTheme:
		m.CycleThemeReverse()
	case rowUsageBars:
		m.CycleUsageBarsReverse()
	case rowSubscription:
		m.CycleClaudeConfig("prev")
	case rowAccount:
		m.CycleAccount("prev")
	case m.autoSwitchRowIndex():
		m.CycleAutoSwitch()
	case m.keepAwakeRowIndex():
		m.CycleKeepAwake()
	}
}

func (m *MainMenuModel) enterInputMode(mode string) (tea.Model, tea.Cmd) {
	m.inputMode = mode
	m.inputErr = nil
	m.nameErr = nil
	m.nameTouched = false
	m.nameWarnShown = false
	m.inputFocusPath = true

	ti := textinput.New()
	ti.Placeholder = "Project path or GitHub URL"
	ti.Focus()
	// Bubbletea textinput Width is inconsistent: placeholder mode uses it as
	// total width, but text mode renders prompt + Width + 1 (cursor). Account
	// for both: 8 (label "  Path: ") + 2 (prompt "> ") + 1 (cursor) = 11.
	ti.Width = menuContentWidth - 11
	m.pathInput = ti

	ni := textinput.New()
	ni.Placeholder = "Project name"
	// Reserve 7 chars for the " (auto)" hint shown when the user has not
	// manually edited the name.  The width is widened to menuContentWidth-11
	// once the user touches the field (see updateInputModeName).
	ni.Width = menuContentWidth - 18
	m.nameInput = ni

	m.autocomplete = NewAutocomplete(PathSuggestionProvider(8), 8)

	// add-project picks its folder by navigating, not typing: open the
	// browser overlay instead of focusing the path field.
	if mode == "add-project" {
		b := NewDirBrowser(m.browserStartDir())
		m.browser = &b
		m.pathInput.SetValue("")
		m.pathInput.Blur()
	}
	return m, textinput.Blink
}

// browserStartDir is where the folder browser opens: the home directory.
func (m *MainMenuModel) browserStartDir() string {
	return "~"
}

func (m *MainMenuModel) exitInputMode() {
	m.inputMode = ""
	m.browser = nil
	m.inputErr = nil
	m.nameErr = nil
	m.nameTouched = false
	m.nameWarnShown = false
	m.inputFocusPath = true
	m.pathInput.Blur()
	m.nameInput.Blur()
	m.autocomplete.Dismiss()
}

func (m *MainMenuModel) setFeedback(msg, style string) {
	m.feedbackMsg = msg
	m.feedbackStyle = style
	m.feedbackTimer = FeedbackDismissTicks
}

func (m *MainMenuModel) setMoveFlash(projectIdx int) {
	m.moveFlashIdx = projectIdx
	m.moveFlashTimer = FeedbackDismissTicks
}

func (m *MainMenuModel) updateInputMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While a clone is in flight, the form is frozen: only Ctrl+C gets through.
	if m.cloning {
		if msg.Type == tea.KeyCtrlC {
			m.setActionResult("quit")
			return m, tea.Quit
		}
		return m, nil
	}
	// For open-once mode, always use path-only flow.
	if m.inputMode == "open-once" || m.inputFocusPath {
		return m.updateInputModePath(msg)
	}
	return m.updateInputModeName(msg)
}

func (m *MainMenuModel) updateInputModePath(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		if m.autocomplete.ShowSuggestions() {
			m.autocomplete.Dismiss()
			return m, nil
		}
		m.exitInputMode()
		return m, nil
	case tea.KeyCtrlC:
		m.exitInputMode()
		m.setActionResult("quit")
		return m, tea.Quit
	case tea.KeyUp:
		if m.autocomplete.ShowSuggestions() {
			m.autocomplete.MoveUp()
			return m, nil
		}
	case tea.KeyDown:
		if m.autocomplete.ShowSuggestions() {
			m.autocomplete.MoveDown()
			return m, nil
		}
	case tea.KeyTab:
		if m.autocomplete.ShowSuggestions() && len(m.autocomplete.Suggestions()) > 0 {
			accepted := m.autocomplete.AcceptSelected()
			m.pathInput.SetValue(accepted)
			m.autocomplete.SetInput(accepted)
			m.autocomplete.RefreshSuggestions()
			m.maybeAutoDeriveName()
			return m, nil
		}
		if m.inputMode == "add-project" {
			return m.advanceToNameField()
		}
	case tea.KeyEnter:
		if m.autocomplete.ShowSuggestions() && len(m.autocomplete.Suggestions()) > 0 {
			accepted := m.autocomplete.AcceptSelected()
			m.pathInput.SetValue(accepted)
			m.autocomplete.SetInput(accepted)
			m.autocomplete.RefreshSuggestions()
			m.maybeAutoDeriveName()
			return m, nil
		}
		if m.inputMode == "add-project" {
			return m.advanceToNameField()
		}
		return m.submitInputMode()
	}

	var cmd tea.Cmd
	m.pathInput, cmd = m.pathInput.Update(msg)
	current := m.pathInput.Value()
	if current != "" {
		m.autocomplete.SetInput(current)
		m.autocomplete.RefreshSuggestions()
	} else {
		m.autocomplete.Dismiss()
	}
	if m.inputMode == "add-project" {
		m.maybeAutoDeriveName()
	}
	return m, cmd
}

// advanceToNameField validates the path and moves focus to the name field.
func (m *MainMenuModel) advanceToNameField() (tea.Model, tea.Cmd) {
	path := strings.TrimSpace(m.pathInput.Value())
	if path == "" {
		m.inputErr = fmt.Errorf("project path cannot be empty")
		return m, nil
	}
	// A GitHub URL is cloned on submit, so it is not validated as a directory.
	if _, _, isGitHub := util.ParseGitHubRepo(path); !isGitHub {
		if err := util.ValidatePath(path); err != nil {
			m.inputErr = err
			return m, nil
		}
	} else {
		m.autocomplete.Dismiss()
	}
	m.inputErr = nil
	m.inputFocusPath = false
	m.pathInput.Blur()
	m.nameInput.Focus()
	m.maybeAutoDeriveName()
	return m, textinput.Blink
}

// maybeAutoDeriveName sets the name field from the path basename if the user has not edited it.
func (m *MainMenuModel) maybeAutoDeriveName() {
	if m.nameTouched {
		return
	}
	path := strings.TrimSpace(m.pathInput.Value())
	if path == "" {
		return
	}
	if _, repo, isGitHub := util.ParseGitHubRepo(path); isGitHub {
		m.nameInput.SetValue(repo)
		return
	}
	expanded := filepath.Clean(util.ExpandPath(path))
	base := filepath.Base(expanded)
	if base != "" && base != "." && base != "/" {
		m.nameInput.SetValue(base)
	}
}

func (m *MainMenuModel) updateInputModeName(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyShiftTab:
		// Go back and clear soft-warn: focus returns to the path field.
		m.nameWarnShown = false
		m.nameErr = nil
		m.inputFocusPath = true
		m.nameInput.Blur()
		m.pathInput.Focus()
		return m, textinput.Blink
	case tea.KeyCtrlC:
		m.exitInputMode()
		m.setActionResult("quit")
		return m, tea.Quit
	case tea.KeyEnter:
		return m.submitInputMode()
	}

	var cmd tea.Cmd
	prev := m.nameInput.Value()
	m.nameInput, cmd = m.nameInput.Update(msg)
	if m.nameInput.Value() != prev {
		if !m.nameTouched {
			// First edit: widen the name field now that (auto) hint is gone.
			m.nameInput.Width = menuContentWidth - 11
		}
		m.nameTouched = true
		m.nameWarnShown = false
		m.nameErr = nil
	}
	return m, cmd
}

func (m *MainMenuModel) submitInputMode() (tea.Model, tea.Cmd) {
	// open-once: validate path and return result.
	if m.inputMode == "open-once" {
		path := strings.TrimSpace(m.pathInput.Value())
		if path == "" {
			m.exitInputMode()
			return m, nil
		}
		if err := util.ValidatePath(path); err != nil {
			m.inputErr = err
			return m, nil
		}
		expanded := filepath.Clean(util.ExpandPath(path))
		name := filepath.Base(expanded)
		m.exitInputMode()
		m.result = &MainMenuResult{
			Action:       "open-once",
			Name:         name,
			Path:         expanded,
			AITool:       m.CurrentAITool(),
			GhostDisplay: m.ghostDisplayForResult(),
			TabTitle:     m.tabTitleForResult(),
			SoundName:    m.soundNameForResult(),
		}
		m.quitting = true
		return m, tea.Quit
	}

	// add-project: both fields already validated; submit.
	name := strings.TrimSpace(m.nameInput.Value())
	if name == "" {
		m.nameErr = fmt.Errorf("project name cannot be empty")
		return m, nil
	}

	path := strings.TrimSpace(m.pathInput.Value())
	if cloneURL, _, isGitHub := util.ParseGitHubRepo(path); isGitHub {
		return m.startGitHubClone(cloneURL, name)
	}
	expanded := filepath.Clean(util.ExpandPath(path))

	if IsDuplicateProject(expanded, m.projects) {
		m.nameErr = fmt.Errorf("project already exists")
		return m, nil
	}

	if IsDuplicateName(name, m.projects) {
		if !m.nameWarnShown {
			m.nameWarnShown = true
			m.nameErr = fmt.Errorf("a project named '%s' already exists — press Enter again to add anyway", name)
			return m, nil
		}
		// Second Enter: user confirmed, proceed.
	}
	m.nameWarnShown = false

	if err := AppendProject(name, expanded, m.projectsFile); err != nil {
		m.nameErr = fmt.Errorf("failed to save: %v", err)
		return m, nil
	}

	projects, _ := models.LoadProjects(m.projectsFile)
	models.PopulateWorktrees(projects)
	m.projects = projects
	m.expandedWorktrees = make(map[int]bool)

	m.exitInputMode()
	m.setFeedback("Added "+name, "success")
	return m, nil
}

// startGitHubClone validates the clone destination and dispatches the clone
// as a tea.Cmd — cloning can take many seconds and must never block Update.
func (m *MainMenuModel) startGitHubClone(cloneURL, name string) (tea.Model, tea.Cmd) {
	dest, err := m.cloneDestDir(name)
	if err != nil {
		m.nameErr = fmt.Errorf("cannot determine clone destination: %v", err)
		return m, nil
	}

	if _, err := os.Stat(dest); err == nil {
		m.nameErr = fmt.Errorf("directory already exists: %s", dest)
		return m, nil
	}
	if IsDuplicateProject(dest, m.projects) {
		m.nameErr = fmt.Errorf("project already exists")
		return m, nil
	}

	clone := m.gitClone
	if clone == nil {
		clone = defaultGitClone
	}
	m.nameErr = nil
	m.cloning = true
	m.cloneSlug = repoSlug(cloneURL)
	m.cloneDest = dest
	m.cloneFrame = 0
	return m, tea.Batch(func() tea.Msg {
		return githubCloneDoneMsg{name: name, dest: dest, err: clone(cloneURL, dest)}
	}, cloneTickCmd())
}

// cloneTickMsg animates the clone spinner. It needs its own ticker: bobTickCmd
// runs only when the mascot is animated, so a spinner hung off it would sit
// frozen for anyone running Mascot [None].
type cloneTickMsg struct{}

// cloneSpinnerFrames are the braille spinner frames, indexed by cloneFrame.
var cloneSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// cloneTickCmd schedules the next spinner frame.
func cloneTickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return cloneTickMsg{} })
}

// cloneDestDir computes where a GitHub clone for the given project name lands:
// under the user's home directory.
func (m *MainMenuModel) cloneDestDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, name), nil
}

// abbreviateHome shortens a home-prefixed path to ~ form for display.
func abbreviateHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

// repoSlug reduces a GitHub clone URL to its owner/repo form for display.
func repoSlug(cloneURL string) string {
	s := strings.TrimSuffix(cloneURL, ".git")
	if i := strings.Index(s, "github.com"); i >= 0 {
		s = strings.TrimLeft(s[i+len("github.com"):], ":/")
	}
	return strings.TrimSuffix(s, "/")
}

// defaultGitClone runs a real `git clone url dest`.
func defaultGitClone(url, dest string) error {
	out, err := exec.Command("git", "clone", "--", url, dest).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// setAddProjectErr shows an add-project error where the user is looking: the
// browser's status slot when it is open, otherwise the form's name row.
func (m *MainMenuModel) setAddProjectErr(err error) {
	if m.browser != nil {
		m.browser.errMsg = err.Error()
		return
	}
	m.nameErr = err
}

// applyGitHubCloneDone finishes the GitHub-URL add-project flow.
func (m *MainMenuModel) applyGitHubCloneDone(msg githubCloneDoneMsg) (tea.Model, tea.Cmd) {
	m.cloning = false
	m.cloneSlug = ""
	m.cloneDest = ""
	if msg.err != nil {
		m.setAddProjectErr(fmt.Errorf("clone failed: %v", msg.err))
		return m, nil
	}
	if err := AppendProject(msg.name, msg.dest, m.projectsFile); err != nil {
		m.setAddProjectErr(fmt.Errorf("failed to save: %v", err))
		return m, nil
	}
	projects, _ := models.LoadProjects(m.projectsFile)
	models.PopulateWorktrees(projects)
	m.projects = projects
	m.expandedWorktrees = make(map[int]bool)

	m.exitInputMode()
	m.setFeedback("Added "+msg.name, "success")
	return m, nil
}

// enterDeleteMode switches to delete mode (stub - Task 4 will implement fully).
func (m *MainMenuModel) enterDeleteMode() (tea.Model, tea.Cmd) {
	if len(m.projects) == 0 {
		m.setFeedback("No projects to delete", "error")
		return m, nil
	}
	m.deleteMode = true
	// Preserve the user's current cursor position if it is a valid delete target;
	// otherwise fall back to the first deletable item.
	items := m.DeletableItems()
	m.deleteSelected = items[0]
	for _, idx := range items {
		if idx == m.selectedItem {
			m.deleteSelected = m.selectedItem
			break
		}
	}
	return m, nil
}

func (m *MainMenuModel) exitDeleteMode() {
	m.deleteMode = false
	m.deleteSelected = 0
}

// updateDeleteMode handles key events while in delete mode.
func (m *MainMenuModel) updateDeleteMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.exitDeleteMode()
		return m, nil
	case tea.KeyCtrlC:
		m.exitDeleteMode()
		m.setActionResult("quit")
		return m, tea.Quit
	case tea.KeyUp:
		m.deleteMoveUp()
		return m, nil
	case tea.KeyDown:
		m.deleteMoveDown()
		return m, nil
	case tea.KeyEnter:
		return m.confirmDeleteFlat()
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			r := TranslateRune(msg.Runes[0])
			switch {
			case r == 'q' || r == 'Q':
				m.exitDeleteMode()
				return m, nil
			case r == 'd' || r == 'D':
				m.exitDeleteMode()
				return m, nil
			case r == 'y' || r == 'Y':
				if m.pendingForceDeleteWT != nil {
					return m.forceDeleteWorktree()
				}
				return m, nil
			case r == 'j':
				m.deleteMoveDown()
				return m, nil
			case r == 'k':
				m.deleteMoveUp()
				return m, nil
			case r >= '1' && r <= '9':
				n := int(r-'0') - 1 // 0-based index into DeletableItems
				items := m.DeletableItems()
				if n >= 0 && n < len(items) {
					m.deleteSelected = items[n]
				}
				return m, nil
			}
		}
	}
	return m, nil
}

// deleteMoveUp moves the delete cursor to the previous deletable item (wrapping).
func (m *MainMenuModel) deleteMoveUp() {
	items := m.DeletableItems()
	if len(items) == 0 {
		return
	}
	for i, v := range items {
		if v == m.deleteSelected {
			if i == 0 {
				m.deleteSelected = items[len(items)-1]
			} else {
				m.deleteSelected = items[i-1]
			}
			return
		}
	}
	m.deleteSelected = items[0]
}

// deleteMoveDown moves the delete cursor to the next deletable item (wrapping).
func (m *MainMenuModel) deleteMoveDown() {
	items := m.DeletableItems()
	if len(items) == 0 {
		return
	}
	for i, v := range items {
		if v == m.deleteSelected {
			if i == len(items)-1 {
				m.deleteSelected = items[0]
			} else {
				m.deleteSelected = items[i+1]
			}
			return
		}
	}
	m.deleteSelected = items[0]
}

// confirmDeleteFlat is a temporary stub — Task 9 will replace this with real dispatch.
// confirmDeleteFlat dispatches to the appropriate deletion handler based on
// what the current deleteSelected flat index points to.
func (m *MainMenuModel) confirmDeleteFlat() (tea.Model, tea.Cmd) {
	itemType, projectIdx, wtIdx := m.ResolveItem(m.deleteSelected)
	switch itemType {
	case "project":
		return m.confirmDeleteProject(projectIdx)
	case "worktree":
		return m.confirmDeleteWorktree(projectIdx, wtIdx)
	default:
		m.exitDeleteMode()
		return m, nil
	}
}

// confirmDeleteWorktree dispatches a background removal of the worktree at
// (projectIdx, wtIdx) without force. The actual `git worktree remove` runs in a
// tea.Cmd — never inline — so the UI stays responsive while git deletes the tree.
// The result is reported back as a worktreeRemoveDoneMsg.
func (m *MainMenuModel) confirmDeleteWorktree(projectIdx, wtIdx int) (tea.Model, tea.Cmd) {
	if projectIdx >= len(m.projects) || wtIdx >= len(m.projects[projectIdx].Worktrees) {
		m.exitDeleteMode()
		return m, nil
	}
	return m, m.removeWorktreeCmd(projectIdx, wtIdx, false)
}

// forceDeleteWorktree dispatches a background `git worktree remove --force` for the
// pending worktree. Like confirmDeleteWorktree, the removal runs in a tea.Cmd so the
// UI never freezes on a slow delete.
func (m *MainMenuModel) forceDeleteWorktree() (tea.Model, tea.Cmd) {
	pending := m.pendingForceDeleteWT
	m.pendingForceDeleteWT = nil
	if pending == nil || pending.projectIdx >= len(m.projects) ||
		pending.wtIdx >= len(m.projects[pending.projectIdx].Worktrees) {
		m.exitDeleteMode()
		return m, nil
	}
	return m, m.removeWorktreeCmd(pending.projectIdx, pending.wtIdx, true)
}

// removeWorktreeCmd builds the tea.Cmd that removes a worktree off the UI event loop
// and shows immediate "Removing…" feedback so the interface stays live during the
// potentially multi-second `git worktree remove`.
func (m *MainMenuModel) removeWorktreeCmd(projectIdx, wtIdx int, force bool) tea.Cmd {
	wt := m.projects[projectIdx].Worktrees[wtIdx]
	projectPath := m.projects[projectIdx].Path
	m.setFeedback("Removing worktree "+wt.Branch+"…", "progress")
	branch := wt.Branch
	wtPath := wt.Path
	return func() tea.Msg {
		err := models.RemoveWorktree(projectPath, wtPath, force)
		return worktreeRemoveDoneMsg{
			branch:     branch,
			projectIdx: projectIdx,
			wtIdx:      wtIdx,
			force:      force,
			err:        err,
		}
	}
}

// applyWorktreeRemoveDone handles the result of a background worktree removal:
// re-prompts for force on dirty/locked, reports other failures, and reloads on success.
func (m *MainMenuModel) applyWorktreeRemoveDone(msg worktreeRemoveDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if !msg.force && models.IsWorktreeDirtyError(msg.err) {
			m.pendingForceDeleteWT = &pendingWorktreeDelete{projectIdx: msg.projectIdx, wtIdx: msg.wtIdx}
			m.setFeedback("Worktree has changes — press Y to force remove", "error")
			return m, nil
		}
		if !msg.force && models.IsWorktreeLockedError(msg.err) {
			m.pendingForceDeleteWT = &pendingWorktreeDelete{projectIdx: msg.projectIdx, wtIdx: msg.wtIdx}
			m.setFeedback("Worktree is locked — press Y to force remove", "error")
			return m, nil
		}
		if msg.force {
			m.setFeedback("Failed to force remove worktree", "error")
		} else {
			m.setFeedback("Failed to remove worktree", "error")
		}
		m.exitDeleteMode()
		return m, nil
	}

	return m.reloadAfterWorktreeRemoval(msg.branch)
}

// reloadAfterWorktreeRemoval reloads projects+worktrees, resets state, and stays in delete mode.
func (m *MainMenuModel) reloadAfterWorktreeRemoval(branch string) (tea.Model, tea.Cmd) {
	projects, _ := models.LoadProjects(m.projectsFile)
	models.PopulateWorktrees(projects)
	m.projects = projects

	// Preserve expanded state for projects that still exist and still have worktrees.
	newExpanded := make(map[int]bool)
	for i, proj := range m.projects {
		if m.expandedWorktrees[i] && len(proj.Worktrees) > 0 {
			newExpanded[i] = true
		}
	}
	m.expandedWorktrees = newExpanded

	items := m.DeletableItems()
	found := false
	for _, v := range items {
		if v == m.deleteSelected {
			found = true
			break
		}
	}
	if !found && len(items) > 0 {
		m.deleteSelected = items[0]
	}

	if m.selectedItem >= m.TotalItems() {
		m.selectedItem = m.TotalItems() - 1
		if m.selectedItem < 0 {
			m.selectedItem = 0
		}
	}

	m.setFeedback("Removed worktree "+branch, "success")
	return m, nil
}

// confirmDeleteProject removes the project at projectIdx from the projects file and updates the list.
func (m *MainMenuModel) confirmDeleteProject(projectIdx int) (tea.Model, tea.Cmd) {
	if projectIdx >= len(m.projects) {
		m.exitDeleteMode()
		return m, nil
	}

	proj := m.projects[projectIdx]
	line := proj.Name + ":" + proj.Path

	if err := RemoveProject(line, m.projectsFile); err != nil {
		m.setFeedback("Failed to delete", "error")
		m.exitDeleteMode()
		return m, nil
	}

	projects, _ := models.LoadProjects(m.projectsFile)
	models.PopulateWorktrees(projects)
	m.projects = projects
	m.expandedWorktrees = make(map[int]bool)

	if m.selectedItem >= m.TotalItems() {
		m.selectedItem = m.TotalItems() - 1
		if m.selectedItem < 0 {
			m.selectedItem = 0
		}
	}

	// Clamp deleteSelected to a valid deletable item; exit if none remain.
	items := m.DeletableItems()
	if len(items) == 0 {
		m.exitDeleteMode()
	} else {
		found := false
		for _, v := range items {
			if v == m.deleteSelected {
				found = true
				break
			}
		}
		if !found {
			m.deleteSelected = items[0]
		}
	}

	m.setFeedback("Deleted "+proj.Name, "success")
	return m, nil
}

// ghostDisplayLabel returns a capitalized display label for the ghost display mode.
func ghostDisplayLabel(mode string) string {
	switch mode {
	case "animated":
		return "Animated"
	case "static":
		return "Static"
	case "none":
		return "None"
	default:
		return mode
	}
}

// tabTitleLabel returns a display label for the tab title mode.
func tabTitleLabel(mode string) string {
	switch mode {
	case "full":
		return "Project \u00b7 Tool"
	case "project":
		return "Project Only"
	case "model":
		return "Model Set"
	default:
		return mode
	}
}

// usageBarsLabel returns the display label for a usage-bars mode: "7d", "5h" stay
// as-is; "both"/"none" are capitalized to match the other settings values.
func usageBarsLabel(mode string) string {
	switch mode {
	case "5h":
		return "5h"
	case "both":
		return "Both"
	case "none":
		return "None"
	default:
		return "7d"
	}
}

// menuInnerWidth is sized so the widest view — the Stats table
// (Month/Input/Output/Cache W/Cache R/Total, indented 2) — fits with 3-space gaps
// between the numeric columns and a roomy gap pushing the right-aligned Total
// column clear of Cache R. All views share this width so the box is a consistent
// size across Projects, Settings, and Stats.
const menuInnerWidth = 68
const menuPadding = 2
const menuContentWidth = menuInnerWidth - menuPadding // 66 (right-side padding only)

// TruncateMiddle truncates s in the middle with "…" if it exceeds maxWidth.
func TruncateMiddle(s string, maxWidth int) string {
	if len(s) <= maxWidth {
		return s
	}
	if maxWidth <= 1 {
		return "\u2026"
	}
	left := (maxWidth - 1 + 1) / 2
	right := maxWidth - 1 - left
	return s[:left] + "\u2026" + s[len(s)-right:]
}

// shortenHomePath replaces $HOME prefix with ~ for display.
func shortenHomePath(path string) string {
	home := os.Getenv("HOME")
	if home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// availableMenuHeight returns the vertical budget the menu box has inside the
// terminal, accounting for any room the ghost illustration consumes above the
// menu (in the "above" layout the ghost + a 1-row gap eat 16 rows).
func (m *MainMenuModel) availableMenuHeight() int {
	if m.height <= 0 {
		return 0
	}
	layout := m.CalculateLayout(m.width, m.height)
	if layout.GhostPosition == "above" && m.ghostDisplay != "none" {
		// Ghost (15 awake, 16 sleeping) + gap (1). Animated ghost adds a bob row.
		// Sleeping stacks a 3-row Zzz frame above the ghost.
		reserve := 16
		if m.ghostSleeping {
			reserve = 17 // 16 ghost lines + 1 gap
		}
		if m.ghostDisplay == "animated" {
			reserve = 17
		}
		if m.ghostSleeping && m.zzz != nil {
			reserve += 3
		}
		h := m.height - reserve
		if h < 5 {
			h = 5
		}
		return h
	}
	return m.height
}

// applyMenuScroll clips the body region of the menu box to a visible window so
// that the total row count fits within the given height budget. Overflow is
// indicated with ▲/▼ markers at the edges of the window.
func (m *MainMenuModel) applyMenuScroll(lines []string, headerEnd, footerStart, avail int) []string {
	header := lines[:headerEnd]
	body := lines[headerEnd:footerStart]
	footer := lines[footerStart:]

	availBody := avail - len(header) - len(footer)
	if availBody < 1 {
		availBody = 1
	}
	if availBody >= len(body) {
		return lines
	}

	cursorRow := m.cursorBodyRow()
	// Clamp cursor to body bounds in case upstream state is stale.
	if cursorRow < 0 {
		cursorRow = 0
	}
	if cursorRow >= len(body) {
		cursorRow = len(body) - 1
	}

	offset := cursorRow - availBody/2
	if offset < 0 {
		offset = 0
	}
	if offset+availBody > len(body) {
		offset = len(body) - availBody
	}
	if offset < 0 {
		offset = 0
	}

	// Guarantee cursor lies within the window, then re-clamp to body bounds.
	if cursorRow < offset {
		offset = cursorRow
	}
	if cursorRow >= offset+availBody {
		offset = cursorRow - availBody + 1
	}
	if offset+availBody > len(body) {
		offset = len(body) - availBody
	}
	if offset < 0 {
		offset = 0
	}

	topIndicator := offset > 0
	bottomIndicator := offset+availBody < len(body)

	window := make([]string, availBody)
	for i := 0; i < availBody; i++ {
		window[i] = body[offset+i]
	}
	// Drop indicators that would land on the cursor's row (happens when
	// availBody is too small to both show an indicator and the cursor).
	cursorWinIdx := cursorRow - offset
	if topIndicator && cursorWinIdx != 0 {
		window[0] = m.scrollIndicatorRow("▲")
	}
	if bottomIndicator && cursorWinIdx != len(window)-1 {
		window[len(window)-1] = m.scrollIndicatorRow("▼")
	}

	out := make([]string, 0, len(header)+availBody+len(footer))
	out = append(out, header...)
	out = append(out, window...)
	out = append(out, footer...)
	return out
}

// scrollIndicatorRow renders a menu-box row containing a centered overflow
// indicator (▲ or ▼).
func (m *MainMenuModel) scrollIndicatorRow(symbol string) string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	leftBorder := dimStyle.Render("│")
	rightBorder := strings.Repeat(" ", menuPadding) + dimStyle.Render("│")
	ind := dimStyle.Render(symbol + " " + symbol + " " + symbol)
	indWidth := lipgloss.Width(ind)
	padLeft := (menuInnerWidth - indWidth) / 2
	if padLeft < 0 {
		padLeft = 0
	}
	padRight := menuInnerWidth - indWidth - padLeft - menuPadding
	if padRight < 0 {
		padRight = 0
	}
	return leftBorder + strings.Repeat(" ", padLeft) + ind + strings.Repeat(" ", padRight) + rightBorder
}

// cursorBodyRow returns the 0-indexed row of the selected item within the
// scrollable body region of the menu.
func (m *MainMenuModel) cursorBodyRow() int {
	cursor := m.selectedItem
	if m.deleteMode {
		cursor = m.deleteSelected
	}
	itemType, projectIdx, worktreeIdx := m.ResolveItem(cursor)
	baseProjectRow := func(upto int) int {
		row := 0
		for i := 0; i < upto && i < len(m.projects); i++ {
			row += 2
			if m.expandedWorktrees[i] {
				row += 2*len(m.projects[i].Worktrees) + 1
			}
		}
		return row
	}
	switch itemType {
	case "project":
		return baseProjectRow(projectIdx)
	case "worktree":
		return baseProjectRow(projectIdx) + 2 + 2*worktreeIdx
	case "add-worktree":
		return baseProjectRow(projectIdx) + 2 + 2*len(m.projects[projectIdx].Worktrees)
	case "add-project":
		// add-project row = all project rows + blank spacer row.
		return baseProjectRow(len(m.projects)) + 1
	}
	return 0
}

// addProjectStatusLine picks the one styled message for the status slot under
// the path field: an error, a GitHub clone-destination preview (with a live
// collision check), the discoverability tip, or nothing.
func (m *MainMenuModel) addProjectStatusLine(dimStyle, errorStyle lipgloss.Style) string {
	if m.cloning {
		frame := cloneSpinnerFrames[m.cloneFrame%len(cloneSpinnerFrames)]
		accentStyle := lipgloss.NewStyle().Foreground(m.theme.Accent)
		return accentStyle.Render(TruncateMiddle(frame+" Cloning "+m.cloneSlug+" → "+abbreviateHome(m.cloneDest), menuContentWidth-2))
	}
	if m.inputErr != nil {
		return errorStyle.Render(TruncateMiddle("✗ "+m.inputErr.Error(), menuContentWidth-2))
	}
	if cloneURL, repo, ok := util.ParseGitHubRepo(strings.TrimSpace(m.pathInput.Value())); ok {
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			name = repo
		}
		dest, err := m.cloneDestDir(name)
		if err != nil {
			return ""
		}
		if _, statErr := os.Stat(dest); statErr == nil || IsDuplicateProject(dest, m.projects) {
			return errorStyle.Render(TruncateMiddle("✗ already exists: "+abbreviateHome(dest), menuContentWidth-2))
		}
		accentStyle := lipgloss.NewStyle().Foreground(m.theme.Accent)
		return accentStyle.Render(TruncateMiddle("⬡ "+repoSlug(cloneURL)+" → "+abbreviateHome(dest), menuContentWidth-2))
	}
	if m.inputFocusPath && !m.autocomplete.ShowSuggestions() && !m.cloning {
		return dimStyle.Render("Tip: paste a GitHub repo URL to clone it")
	}
	return ""
}

// renderInputBox builds the input mode box string (add-project or open-once).
func (m *MainMenuModel) renderInputBox() string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	hLine := strings.Repeat("\u2500", menuInnerWidth)
	topBorder := dimStyle.Render("\u256d" + hLine + "\u256e")
	separator := dimStyle.Render("\u251c" + hLine + "\u2524")
	bottomBorder := dimStyle.Render("\u2570" + hLine + "\u256f")
	leftBorder := dimStyle.Render("\u2502")
	rightBorder := strings.Repeat(" ", menuPadding) + dimStyle.Render("\u2502")
	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder

	var lines []string

	lines = append(lines, topBorder)

	title := primaryBoldStyle.Render("Wisp Deck")
	var label string
	if m.inputMode == "add-project" {
		label = "Add Project"
	} else {
		label = "Open Once"
	}
	titleContent := title + " " + dimStyle.Render("\u00b7 "+label)
	titlePadding := menuContentWidth - lipgloss.Width(titleContent) - 1
	if titlePadding < 0 {
		titlePadding = 0
	}
	lines = append(lines, leftBorder+" "+titleContent+strings.Repeat(" ", titlePadding)+rightBorder)
	lines = append(lines, separator)
	lines = append(lines, emptyRow)

	// Field labels carry a ▸ marker on the focused field; both forms are 8 cells
	// so focus changes never shift the layout.
	pathFocused := (m.inputFocusPath || m.inputMode == "open-once") && m.inputMode != "add-project"
	pathLabel := dimStyle.Render("  Path: ")
	if pathFocused {
		pathLabel = primaryBoldStyle.Render("▸ Path: ")
	}
	inputView := m.pathInput.View()
	if m.inputMode == "add-project" {
		// The path is picked in the folder browser, never typed: render it as
		// static text (no prompt, no cursor).
		inputView = TruncateMiddle(abbreviateHome(strings.TrimSpace(m.pathInput.Value())), menuContentWidth-10)
	}
	inputContent := pathLabel + inputView
	inputPadding := menuContentWidth - lipgloss.Width(inputContent)
	if inputPadding < 0 {
		inputPadding = 0
	}
	lines = append(lines, leftBorder+inputContent+strings.Repeat(" ", inputPadding)+rightBorder)

	if m.inputMode == "add-project" {
		// A single always-rendered status slot under the path row: error,
		// GitHub destination preview, tip, or blank — so the box height never
		// jumps while typing.
		status := m.addProjectStatusLine(dimStyle, errorStyle)
		statusContent := "  " + status
		statusPadding := menuContentWidth - lipgloss.Width(statusContent)
		if statusPadding < 0 {
			statusPadding = 0
		}
		lines = append(lines, leftBorder+statusContent+strings.Repeat(" ", statusPadding)+rightBorder)
	} else if m.inputErr != nil {
		errMsg := errorStyle.Render("✗ " + m.inputErr.Error())
		errContent := "  " + errMsg
		errPadding := menuContentWidth - lipgloss.Width(errContent)
		if errPadding < 0 {
			errPadding = 0
		}
		lines = append(lines, leftBorder+errContent+strings.Repeat(" ", errPadding)+rightBorder)
	}

	// Autocomplete suggestions rendered inside the box (path field only)
	if m.autocomplete.ShowSuggestions() {
		selectedStyle := lipgloss.NewStyle().Reverse(true)
		lines = append(lines, emptyRow)
		lines = append(lines, separator)
		for i, s := range m.autocomplete.Suggestions() {
			truncated := TruncateMiddle(s, menuContentWidth-4) // 2 padding + 2 border spacing
			var row string
			if i == m.autocomplete.Selected() {
				highlighted := selectedStyle.Render(" " + truncated + strings.Repeat(" ", menuContentWidth-4-lipgloss.Width(truncated)) + " ")
				row = leftBorder + " " + highlighted + " " + rightBorder
			} else {
				padding := menuContentWidth - lipgloss.Width(truncated) - 2
				if padding < 0 {
					padding = 0
				}
				row = leftBorder + " " + truncated + strings.Repeat(" ", padding) + " " + rightBorder
			}
			lines = append(lines, row)
		}
	}

	// Name field (add-project mode only)
	if m.inputMode == "add-project" {
		nameLabel := dimStyle.Render("  Name: ")
		if !m.inputFocusPath {
			nameLabel = primaryBoldStyle.Render("▸ Name: ")
		}
		nameView := m.nameInput.View()
		nameBase := nameLabel + nameView
		namePadding := menuContentWidth - lipgloss.Width(nameBase)
		if namePadding < 0 {
			namePadding = 0
		}
		var nameRow string
		if !m.nameTouched {
			// Append " (auto)" hint; name field width is pre-reduced by 7 to
			// guarantee this always fits within the box.
			autoHint := helpStyle.Render(" (auto)")
			autoHintWidth := 7
			trailing := namePadding - autoHintWidth
			if trailing < 0 {
				trailing = 0
			}
			nameRow = leftBorder + nameBase + autoHint + strings.Repeat(" ", trailing) + rightBorder
		} else {
			nameRow = leftBorder + nameBase + strings.Repeat(" ", namePadding) + rightBorder
		}
		lines = append(lines, nameRow)

		if m.nameErr != nil {
			errMsg := errorStyle.Render("✗ " + m.nameErr.Error())
			nameErrContent := "  " + errMsg
			nameErrPadding := menuContentWidth - lipgloss.Width(nameErrContent)
			if nameErrPadding < 0 {
				nameErrPadding = 0
			}
			lines = append(lines, leftBorder+nameErrContent+strings.Repeat(" ", nameErrPadding)+rightBorder)
		}

	}

	lines = append(lines, emptyRow)
	lines = append(lines, separator)

	sep := dimStyle.Render(" · ")
	var helpContent string
	if m.cloning {
		// Esc and Enter are ignored while a clone is in flight; advertising
		// them here would lie.
		helpContent = helpStyle.Render("Ctrl+C quit")
	} else if m.autocomplete.ShowSuggestions() {
		helpContent = helpStyle.Render("\u2191\u2193 navigate") + sep + helpStyle.Render("\u23ce complete") + sep + helpStyle.Render("Esc cancel")
	} else if m.inputMode == "add-project" && !m.inputFocusPath {
		helpContent = helpStyle.Render("\u21e7Tab back") + sep + helpStyle.Render("\u23ce confirm") + sep + helpStyle.Render("Esc back")
	} else {
		helpContent = helpStyle.Render("Tab complete") + sep + helpStyle.Render("\u23ce confirm") + sep + helpStyle.Render("Esc cancel")
	}
	helpPadding := menuContentWidth - lipgloss.Width(helpContent) - 1
	if helpPadding < 0 {
		helpPadding = 0
	}
	lines = append(lines, leftBorder+" "+helpContent+strings.Repeat(" ", helpPadding)+rightBorder)
	lines = append(lines, bottomBorder)

	return strings.Join(lines, "\n")
}

// View implements tea.Model. Renders the full box-drawing menu with optional ghost.
func (m *MainMenuModel) View() string {
	if m.quitting {
		return ""
	}

	var menuBox string
	// modalBaseLines records how many lines precede an appended modal panel so its
	// absolute origin can be computed once the box is placed; -1 means no modal.
	modalBaseLines := -1
	appendModal := func(panel string) {
		modalBaseLines = strings.Count(menuBox, "\n") + 1
		menuBox += "\n" + panel
	}
	switch {
	case m.inputMode != "":
		menuBox = m.renderInputBox()
	case m.activeTab == TabSettings:
		menuBox = m.renderSettingsBox()
		if m.modelMapOpen {
			appendModal(m.renderModelMapPanel())
		}
		if m.accountMenuOpen {
			appendModal(m.renderAccountMenuPanel())
		}
		if m.aiToolsPanelOpen {
			appendModal(m.renderAIToolsPanel())
		}
	case m.activeTab == TabStats:
		menuBox = m.renderStatsBox()
	default:
		menuBox = m.renderMenuBox()
		if m.accountMenuOpen {
			appendModal(m.renderAccountMenuPanel())
		}
	}

	// Capture the box-relative frame (menu + any appended modal) for glyph-precise
	// hit-testing. Box-relative (boxX, boxY) indexes these lines directly, so the
	// ghost padding / centering applied below doesn't matter here.
	m.menuLines = strings.Split(menuBox, "\n")

	layout := m.CalculateLayout(m.width, m.height)

	// Determine ghost display
	ghostPosition := layout.GhostPosition
	if m.ghostDisplay == "none" {
		ghostPosition = "hidden"
	}

	var content string

	switch ghostPosition {
	case "side":
		ghostLines := GhostForTheme(m.CurrentAITool(), m.ghostSleeping, m.theme)
		ghostStr := RenderGhost(ghostLines)
		if m.ghostSleeping && m.zzz != nil {
			zzzColor := AnsiFromThemeColor(m.theme.SleepAccent)
			zzzStr := m.zzz.ViewColored(zzzColor)
			zzzLines := strings.Split(zzzStr, "\n")
			maxW := 0
			for _, line := range zzzLines {
				if w := lipgloss.Width(line); w > maxW {
					maxW = w
				}
			}
			pad := 28 - maxW
			if pad < 0 {
				pad = 0
			}
			prefix := strings.Repeat(" ", pad)
			var paddedZzz []string
			for _, line := range zzzLines {
				paddedZzz = append(paddedZzz, prefix+line)
			}
			ghostStr = strings.Join(paddedZzz, "\n") + "\n" + ghostStr
		}
		if m.ghostDisplay == "animated" {
			if m.BobOffset() == 1 {
				ghostStr = "\n" + ghostStr
			} else {
				ghostStr = ghostStr + "\n"
			}
		}
		spacer := strings.Repeat(" ", 3)
		content = lipgloss.JoinHorizontal(lipgloss.Center, menuBox, spacer, ghostStr)

	case "above":
		ghostLines := GhostForTheme(m.CurrentAITool(), m.ghostSleeping, m.theme)
		ghostStr := RenderGhost(ghostLines)
		if m.ghostSleeping && m.zzz != nil {
			zzzColor := AnsiFromThemeColor(m.theme.SleepAccent)
			zzzStr := m.zzz.ViewColored(zzzColor)
			zzzLines := strings.Split(zzzStr, "\n")
			maxW := 0
			for _, line := range zzzLines {
				if w := lipgloss.Width(line); w > maxW {
					maxW = w
				}
			}
			pad := 28 - maxW
			if pad < 0 {
				pad = 0
			}
			prefix := strings.Repeat(" ", pad)
			var paddedZzz []string
			for _, line := range zzzLines {
				paddedZzz = append(paddedZzz, prefix+line)
			}
			ghostStr = strings.Join(paddedZzz, "\n") + "\n" + ghostStr
		}
		if m.ghostDisplay == "animated" {
			if m.BobOffset() == 1 {
				ghostStr = "\n" + ghostStr
			} else {
				ghostStr = ghostStr + "\n"
			}
		}
		content = lipgloss.JoinVertical(lipgloss.Center, ghostStr, "", menuBox)

	default:
		content = menuBox
	}

	// Locate the menu box's top-left corner within the (possibly ghost-padded)
	// content so mouse hit-testing can map absolute pointer coordinates to box
	// cells. The box is the left column in the "side" layout and centered under
	// the ghost in the "above" layout.
	var boxRelX, boxRelY int
	switch ghostPosition {
	case "above":
		boxRelY = strings.Count(content, "\n") + 1 - (strings.Count(menuBox, "\n") + 1)
		if cw := lipgloss.Width(content); cw > menuBoxWidth {
			boxRelX = (cw - menuBoxWidth) / 2
		}
	}

	if m.width > 0 && m.height > 0 {
		contentWidth := lipgloss.Width(content)
		contentHeight := strings.Count(content, "\n") + 1
		m.centerOffsetY = (m.height - contentHeight) / 2
		if m.centerOffsetY < 0 {
			m.centerOffsetY = 0
		}
		placedLeft := (m.width - contentWidth) / 2
		if placedLeft < 0 {
			placedLeft = 0
		}
		m.menuOriginX = placedLeft + boxRelX
		m.menuOriginY = m.centerOffsetY + boxRelY
		m.setModalOrigin(modalBaseLines)
		placed := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
		// The folder browser and the About card float as modals over the
		// faint-dimmed screen (like the account-switch popup) instead of
		// appending below the box.
		if m.browser != nil {
			return m.overlayBrowser(placed)
		}
		if m.aboutOpen {
			return m.overlayAbout(placed)
		}
		return placed
	}
	m.centerOffsetY = 0
	m.menuOriginX = boxRelX
	m.menuOriginY = boxRelY
	m.setModalOrigin(modalBaseLines)
	// No terminal size yet: there is nothing to composite the overlay onto, so
	// the card renders alone.
	if m.browser != nil {
		return m.renderBrowserCard()
	}
	if m.aboutOpen {
		return m.renderAboutCard()
	}
	return content
}

// setModalOrigin records the absolute row of an appended modal panel's first
// line (it shares the menu box's left edge), or -1 when no modal is open.
func (m *MainMenuModel) setModalOrigin(baseLines int) {
	if baseLines < 0 {
		m.modalOriginY = -1
		return
	}
	m.modalOriginY = m.menuOriginY + baseLines
}
