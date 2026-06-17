package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackuait/ghost-tab/internal/claudeconfig"
	"github.com/jackuait/ghost-tab/internal/models"
	"github.com/jackuait/ghost-tab/internal/util"
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
	"codex":    "Codex CLI",
	"copilot":  "Copilot CLI",
	"opencode": "OpenCode",
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
	settingsMode        bool
	settingsSelected    int
	initialGhostDisplay string
	ghostDisplayChanged bool
	tabTitle            string
	initialTabTitle     string
	tabTitleChanged     bool
	soundName           string // "" means Off
	initialSoundName    string
	soundNameChanged    bool
	zzz                 *ZzzAnimation
	centerOffsetY       int

	// Inline input mode (add-project or open-once)
	inputMode    string // "", "add-project", "open-once"
	pathInput    textinput.Model
	autocomplete AutocompleteModel
	inputErr     error

	// Two-field add-project form state
	nameInput      textinput.Model
	nameTouched    bool // user manually edited name; disable auto-derive
	nameErr        error
	nameWarnShown  bool // true after first Enter on duplicate name; second Enter confirms
	inputFocusPath bool // true = path field focused, false = name field focused

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

	// File path for projects root directory preference
	projectsRootFile string
	// projectsRoot is the current value loaded from projectsRootFile; "" = not set.
	projectsRoot string

	// Settings panel inline text input for "Default projects dir" item.
	settingsInputMode bool
	settingsInput     textinput.Model
	settingsInputErr  error

	// showEscHint is set by AppModel to display "Press Esc again to quit" in
	// the help row instead of the normal key hints — no extra line is added.
	showEscHint bool

	// File path for AI tool preference persistence
	aiToolFile string

	// File path for settings persistence (ghost display, tab title)
	settingsFile string

	// File path for sound features persistence ({tool}-features.json)
	soundFile string

	// Claude config selection state
	claudeConfigs    []ClaudeConfig // Standard is implicit index 0, not stored here
	selectedConfig   int            // 0 = Standard, 1.. = claudeConfigs[i-1]
	claudeConfigFile string         // pointer file path for persistence
	claudeConfigsList string        // name:file list file path (for mutations)
	claudeConfigsDir  string        // directory holding the settings JSON files

	// Inline Claude config management panel (opened with Enter on the row)
	configPanelOpen   bool
	configPanelCursor int                 // 0 = Standard, 1.. = claudeConfigs[i-1]
	configPanelMode   string              // "" list, "add", "rename", "delete"
	configPanelInput  textinput.Model     // name entry for add/rename
	configPanelErr    error               // last mutation error, shown in the panel

	// Worktree expand/collapse state (project index -> expanded)
	expandedWorktrees map[int]bool

	// worktreePendingProjectIdx tracks which project index is waiting for a
	// branch picker result. -1 means no pending worktree.
	worktreePendingProjectIdx int

	// staleConfirmIdx holds the index of the stale project awaiting launch
	// confirmation. -1 means no confirmation is active.
	staleConfirmIdx int
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
		theme:                     ThemeForTool(currentAI),
		zzz:                       NewZzzAnimation(),
		expandedWorktrees:         make(map[int]bool),
		worktreePendingProjectIdx: -1,
		moveFlashIdx:              -1,
		staleConfirmIdx:           -1,
	}
}

// SetUpdateVersion sets the available update version string.
func (m *MainMenuModel) SetUpdateVersion(version string) {
	m.updateVersion = version
}

// SelectedItem returns the currently selected item index.
func (m *MainMenuModel) SelectedItem() int {
	return m.selectedItem
}

// TotalItems returns the total number of selectable items (projects + expanded worktrees + 4 actions).
func (m *MainMenuModel) TotalItems() int {
	return len(m.projects) + m.expandedWorktreeCount() + len(actionNames)
}

// ToggleWorktrees toggles expand/collapse for the given project index.
// No-op if the project has no worktrees.
// The cursor is anchored to the same logical item before and after the toggle.
func (m *MainMenuModel) ToggleWorktrees(projectIdx int) {
	if projectIdx < 0 || projectIdx >= len(m.projects) {
		return
	}
	if len(m.projects[projectIdx].Worktrees) == 0 {
		return
	}

	// Snapshot the logical item under the cursor before mutating.
	anchorType, anchorProjectIdx, anchorWorktreeIdx := m.ResolveItem(m.selectedItem)

	if m.expandedWorktrees[projectIdx] {
		delete(m.expandedWorktrees, projectIdx)
	} else {
		m.expandedWorktrees[projectIdx] = true
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
	case "action":
		// projectIdx holds the action offset for "action" items.
		actionStart := len(m.projects) + m.expandedWorktreeCount()
		return actionStart + projectIdx
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
	// "action" → no-op
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
// Returns: itemType ("project", "worktree", "add-worktree", or "action"), projectIdx, worktreeIdx.
// For "action", projectIdx is the action offset (0=add, 1=delete, etc).
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
	// Must be an action
	actionIdx := flatIdx - pos
	return "action", actionIdx, -1
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

// CycleAITool cycles the AI tool selection forward ("next") or backward ("prev").
// If aiToolFile is set, the new tool is persisted to disk immediately.
func (m *MainMenuModel) CycleAITool(direction string) {
	n := len(m.aiTools)
	if n <= 1 {
		return
	}
	if direction == "next" {
		m.selectedAI = (m.selectedAI + 1) % n
	} else {
		m.selectedAI = (m.selectedAI - 1 + n) % n
	}
	m.theme = ThemeForTool(m.aiTools[m.selectedAI])
	m.persistAITool()
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

// CycleTabTitle cycles between "full" and "project".
func (m *MainMenuModel) CycleTabTitle() {
	if m.tabTitle == "full" {
		m.tabTitle = "project"
	} else {
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

// CycleSoundName cycles forward through system sounds + Off.
func (m *MainMenuModel) CycleSoundName() {
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
	m.previewSound()
	m.persistSound()
}

// CycleSoundNameReverse cycles backward through Off + system sounds.
func (m *MainMenuModel) CycleSoundNameReverse() {
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
	m.previewSound()
	m.persistSound()
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
func (m *MainMenuModel) persistSound() {
	if m.soundFile == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.soundFile), 0755)

	// Read existing data to preserve other keys
	existing := map[string]interface{}{}
	if data, err := os.ReadFile(m.soundFile); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	if m.soundName == "" {
		existing["sound"] = false
		delete(existing, "sound_name")
	} else {
		existing["sound"] = true
		existing["sound_name"] = m.soundName
	}

	data, _ := json.Marshal(existing)
	_ = os.WriteFile(m.soundFile, append(data, '\n'), 0644)
}

// SetClaudeConfigFile sets the pointer file path used to persist the active config.
func (m *MainMenuModel) SetClaudeConfigFile(path string) { m.claudeConfigFile = path }

// SetClaudeConfigPaths records the list-file and configs-directory paths the
// inline management panel needs to create, rename, and delete config files.
func (m *MainMenuModel) SetClaudeConfigPaths(listFile, dir string) {
	m.claudeConfigsList = listFile
	m.claudeConfigsDir = dir
}

// ConfigPanelOpen reports whether the inline Claude config panel is showing.
func (m *MainMenuModel) ConfigPanelOpen() bool { return m.configPanelOpen }

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

// ClaudeConfigVisible reports whether the config control should be shown.
func (m *MainMenuModel) ClaudeConfigVisible() bool { return m.CurrentAITool() == "claude" }

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
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.claudeConfigFile), 0755)
	_ = os.WriteFile(m.claudeConfigFile, []byte(file+"\n"), 0644)
}

// soundNameForResult returns a pointer to the sound name if changed, nil if unchanged.
func (m *MainMenuModel) soundNameForResult() *string {
	if m.soundNameChanged {
		return &m.soundName
	}
	return nil
}

// InSettingsMode returns true if the menu is currently showing the settings panel.
func (m *MainMenuModel) InSettingsMode() bool {
	return m.settingsMode
}

// EnterSettings switches the menu to settings mode.
func (m *MainMenuModel) EnterSettings() {
	m.settingsMode = true
	m.settingsSelected = 0
}

// ExitSettings returns from settings mode to the main menu.
func (m *MainMenuModel) ExitSettings() {
	m.settingsMode = false
}

// settingsItemCount returns the number of settings rows (5 when the Claude
// config row is visible, otherwise 4).
func (m *MainMenuModel) settingsItemCount() int {
	if m.ClaudeConfigVisible() {
		return 5
	}
	return 4
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
func (m *MainMenuModel) persistSetting(key, value string) {
	if m.settingsFile == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.settingsFile), 0755)
	entry := key + "=" + value
	data, err := os.ReadFile(m.settingsFile)
	if err != nil {
		_ = os.WriteFile(m.settingsFile, []byte(entry+"\n"), 0644)
		return
	}
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
	_ = os.WriteFile(m.settingsFile, []byte(strings.Join(lines, "\n")), 0644)
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

// InputMode returns the current inline input mode ("", "add-project", "open-once").
func (m *MainMenuModel) InputMode() string { return m.inputMode }

// InInputMode returns true if the menu is currently in an inline input mode.
func (m *MainMenuModel) InInputMode() bool { return m.inputMode != "" }

// InDeleteMode returns true if the menu is currently in delete mode.
func (m *MainMenuModel) InDeleteMode() bool { return m.deleteMode }

// WantsEsc implements EscInterceptor. It returns true when the menu is in a
// sub-mode (settings, input, or delete) where Esc should navigate back within
// the menu rather than triggering the AppModel double-Esc quit flow.
func (m *MainMenuModel) WantsEsc() bool {
	return m.settingsMode || m.inputMode != "" || m.deleteMode || m.settingsInputMode
}

// SetSettingsMode directly sets settings mode — intended for tests only.
func (m *MainMenuModel) SetSettingsMode(v bool) { m.settingsMode = v }

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

// SetProjectsRootFile sets the file path for the default projects root directory.
func (m *MainMenuModel) SetProjectsRootFile(path string) { m.projectsRootFile = path }

// LoadProjectsRoot reads the projects root value from projectsRootFile and
// stores it in projectsRoot so the settings panel can display it.
func (m *MainMenuModel) LoadProjectsRoot() {
	m.projectsRoot = readProjectsRoot(m.projectsRootFile)
}

// SetAIToolFile sets the file path for AI tool preference persistence.
// When set, CycleAITool writes the new tool to this file immediately.
func (m *MainMenuModel) SetAIToolFile(path string) { m.aiToolFile = path }

// AIToolFile returns the file path for AI tool preference persistence.
func (m *MainMenuModel) AIToolFile() string { return m.aiToolFile }

// SetSettingsFile sets the file path for settings persistence.
func (m *MainMenuModel) SetSettingsFile(path string) { m.settingsFile = path }

// SettingsFile returns the file path for settings persistence.
func (m *MainMenuModel) SettingsFile() string { return m.settingsFile }

// SetSoundFile sets the file path for sound features persistence.
func (m *MainMenuModel) SetSoundFile(path string) { m.soundFile = path }

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

// SetPathInputValue sets the path field value — intended for tests only.
func (m *MainMenuModel) SetPathInputValue(v string) { m.pathInput.SetValue(v) }

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
	numSeparators := 0
	if numProjects > 0 {
		numSeparators = 1
	}
	// Projects = 2 rows each, worktrees = 2 rows each (branch + path),
	// add-worktree = 1 row each, actions = 1 row each
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
	actionRows := len(actionNames)
	menuHeight := 7 + projectRows + worktreeRows + addWorktreeRows + actionRows + numSeparators
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
	// Row 1: title row
	// Row 2: separator
	// (optional) update notification row
	// Row 3/4: empty row
	// Then items start
	startRow := 4
	if m.updateVersion != "" {
		startRow++ // update notification takes a row
	}

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

	// Separator
	if len(m.projects) > 0 {
		currentRow++
	}

	// Action items (1 row each)
	for range actionNames {
		if clickY == currentRow {
			return flatIdx
		}
		currentRow++
		flatIdx++
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
	case "action":
		if projectIdx < len(actionNames) {
			m.result = &MainMenuResult{
				Action:       actionNames[projectIdx],
				AITool:       m.CurrentAITool(),
				GhostDisplay: m.ghostDisplayForResult(),
				TabTitle:     m.tabTitleForResult(),
				SoundName:    m.soundNameForResult(),
			}
		}
	}

	if m.result != nil {
		m.quitting = true
	}
	return nil
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
	var cmds []tea.Cmd
	if m.ghostDisplay == "animated" {
		cmds = append(cmds, m.bobTickCmd())
		cmds = append(cmds, m.sleepTickCmd())
	}
	return tea.Batch(cmds...)
}

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

	case tea.MouseMsg:
		// Reset sleep state on any mouse activity
		m.Wake()

		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			item := m.MapRowToItem(msg.Y - m.centerOffsetY)
			if item >= 0 {
				if m.selectedItem == item {
					// Already selected, activate (double-click-like behavior)
					if cmd := m.selectCurrent(); cmd != nil {
						return m, cmd
					}
					return m, tea.Quit
				}
				m.selectedItem = item
			}
		}
		return m, nil

	case tea.KeyMsg:
		// Reset sleep state on any keypress
		m.Wake()

		// Settings mode intercepts all key handling
		if m.settingsMode {
			return m.updateSettings(msg)
		}

		// Input mode intercepts all key handling
		if m.inputMode != "" {
			return m.updateInputMode(msg)
		}

		// Delete mode intercepts all key handling
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

		switch msg.Type {
		case tea.KeyUp:
			m.MoveUp()
			return m, nil
		case tea.KeyDown:
			m.MoveDown()
			return m, nil
		case tea.KeyShiftUp:
			m.MoveProjectUp()
			return m, nil
		case tea.KeyShiftDown:
			m.MoveProjectDown()
			return m, nil
		case tea.KeyLeft:
			m.CycleAITool("prev")
			return m, nil
		case tea.KeyRight:
			m.CycleAITool("next")
			return m, nil
		case tea.KeyEnter:
			itemType, projectIdx, _ := m.ResolveItem(m.selectedItem)
			if itemType == "action" {
				if projectIdx < len(actionNames) {
					switch actionNames[projectIdx] {
					case "add-project":
						return m.enterInputMode("add-project")
					case "delete-project":
						return m.enterDeleteMode()
					case "open-once":
						return m.enterInputMode("open-once")
					}
				}
			}
			if cmd := m.selectCurrent(); cmd != nil {
				return m, cmd
			}
			return m, tea.Quit
		case tea.KeyEsc:
			// Emit PopScreenMsg — AppModel handles double-Esc timeout to quit.
			return m, func() tea.Msg { return PopScreenMsg{} }
		case tea.KeyCtrlC:
			m.setActionResult("quit")
			return m, tea.Quit
		case tea.KeyRunes:
			if len(msg.Runes) == 1 {
				return m.handleRune(msg.Runes[0])
			}
		}
	}

	return m, nil
}

// handleRune processes a single rune keypress.
func (m *MainMenuModel) handleRune(r rune) (tea.Model, tea.Cmd) {
	r = TranslateRune(r)
	switch r {
	case 'j':
		m.MoveDown()
		return m, nil
	case 'k':
		m.MoveUp()
		return m, nil
	case 'J':
		m.MoveProjectDown()
		return m, nil
	case 'K':
		m.MoveProjectUp()
		return m, nil
	case 'a', 'A':
		return m.enterInputMode("add-project")
	case 'd', 'D':
		return m.enterDeleteMode()
	case 'o', 'O':
		return m.enterInputMode("open-once")
	case 'p', 'P':
		m.setActionResult("plain-terminal")
		return m, tea.Quit
	case 'w', 'W':
		m.ToggleWorktreesAtCursor()
		return m, nil
	case 's', 'S':
		m.settingsMode = true
		m.settingsSelected = 0
		return m, nil
	case 't', 'T':
		stats := NewStatsModel()
		stats.SetSize(m.width, m.height)
		return m, func() tea.Msg { return PushScreenMsg{Model: stats} }
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

// updateSettings handles key events while in settings mode.
func (m *MainMenuModel) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.configPanelOpen {
		return m.updateConfigPanel(msg)
	}
	if m.settingsInputMode {
		return m.updateSettingsInput(msg)
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.settingsMode = false
		return m, nil
	case tea.KeyCtrlC:
		m.settingsMode = false
		m.setActionResult("quit")
		return m, tea.Quit
	case tea.KeyEnter:
		// Activate current settings item
		switch m.settingsSelected {
		case 0:
			m.CycleGhostDisplay()
		case 1:
			m.CycleTabTitle()
		case 2:
			m.CycleSoundName()
		case 3:
			// Open text input for projects root
			m.settingsInputMode = true
			si := textinput.New()
			si.Placeholder = "e.g., ~/Projects"
			si.Width = menuContentWidth - 11
			si.SetValue(m.projectsRoot)
			si.Focus()
			m.settingsInput = si
			m.settingsInputErr = nil
			return m, textinput.Blink
		case 4:
			m.openConfigPanel()
		}
		return m, nil
	case tea.KeyUp:
		n := m.settingsItemCount()
		m.settingsSelected = (m.settingsSelected - 1 + n) % n
		return m, nil
	case tea.KeyDown:
		n := m.settingsItemCount()
		m.settingsSelected = (m.settingsSelected + 1) % n
		return m, nil
	case tea.KeyRight:
		switch m.settingsSelected {
		case 0:
			m.CycleGhostDisplay()
		case 1:
			m.CycleTabTitle()
		case 2:
			m.CycleSoundName()
		case 4:
			m.CycleClaudeConfig("next")
		}
		return m, nil
	case tea.KeyLeft:
		switch m.settingsSelected {
		case 0:
			m.CycleGhostDisplayReverse()
		case 1:
			m.CycleTabTitle()
		case 2:
			m.CycleSoundNameReverse()
		case 4:
			m.CycleClaudeConfig("prev")
		}
		return m, nil
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			r := TranslateRune(msg.Runes[0])
			switch r {
			case 'j':
				m.settingsSelected = (m.settingsSelected + 1) % m.settingsItemCount()
				return m, nil
			case 'k':
				n := m.settingsItemCount()
				m.settingsSelected = (m.settingsSelected - 1 + n) % n
				return m, nil
			}
		}
	}
	return m, nil
}

// updateSettingsInput handles key events while in settings input mode (editing projects root).
func (m *MainMenuModel) updateSettingsInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.settingsInputMode = false
		m.settingsInput.Blur()
		return m, nil
	case tea.KeyEnter:
		val := strings.TrimSpace(m.settingsInput.Value())
		if val != "" {
			expanded := util.ExpandPath(val)
			if _, err := os.Stat(expanded); err != nil {
				m.settingsInputErr = fmt.Errorf("directory not found")
				return m, nil
			}
			if err := os.WriteFile(m.projectsRootFile, []byte(expanded+"\n"), 0644); err != nil {
				m.settingsInputErr = fmt.Errorf("failed to save: %v", err)
				return m, nil
			}
			m.projectsRoot = expanded
		} else {
			os.Remove(m.projectsRootFile) //nolint:errcheck
			m.projectsRoot = ""
		}
		m.settingsInputMode = false
		m.settingsInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.settingsInput, cmd = m.settingsInput.Update(msg)
	m.settingsInputErr = nil
	return m, cmd
}

// readProjectsRoot reads the default projects root from the given file.
// Returns empty string if the file is missing or unreadable.
func readProjectsRoot(file string) string {
	if file == "" {
		return ""
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (m *MainMenuModel) enterInputMode(mode string) (tea.Model, tea.Cmd) {
	m.inputMode = mode
	m.inputErr = nil
	m.nameErr = nil
	m.nameTouched = false
	m.nameWarnShown = false
	m.inputFocusPath = true

	ti := textinput.New()
	ti.Placeholder = "Project path (e.g., ~/code/project)"
	ti.Focus()
	// Bubbletea textinput Width is inconsistent: placeholder mode uses it as
	// total width, but text mode renders prompt + Width + 1 (cursor). Account
	// for both: 8 (label "  Path: ") + 2 (prompt "> ") + 1 (cursor) = 11.
	ti.Width = menuContentWidth - 11
	if root := readProjectsRoot(m.projectsRootFile); root != "" {
		prefill := root
		if !strings.HasSuffix(prefill, "/") {
			prefill += "/"
		}
		ti.SetValue(prefill)
		ti.CursorEnd()
	}
	m.pathInput = ti

	ni := textinput.New()
	ni.Placeholder = "Project name"
	// Reserve 7 chars for the " (auto)" hint shown when the user has not
	// manually edited the name.  The width is widened to menuContentWidth-11
	// once the user touches the field (see updateInputModeName).
	ni.Width = menuContentWidth - 18
	m.nameInput = ni

	m.autocomplete = NewAutocomplete(PathSuggestionProvider(8), 8)
	return m, textinput.Blink
}

func (m *MainMenuModel) exitInputMode() {
	m.inputMode = ""
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
	if err := util.ValidatePath(path); err != nil {
		m.inputErr = err
		return m, nil
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
	expanded := filepath.Clean(util.ExpandPath(path))
	base := filepath.Base(expanded)
	if base != "" && base != "." && base != "/" {
		m.nameInput.SetValue(base)
	}
}

func (m *MainMenuModel) updateInputModeName(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyShiftTab:
		// Return to path field and clear soft-warn.
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

// confirmDeleteWorktree attempts to remove the worktree at (projectIdx, wtIdx) without force.
// On dirty error, sets pendingForceDeleteWT and shows a prompt. On other errors, shows error feedback.
func (m *MainMenuModel) confirmDeleteWorktree(projectIdx, wtIdx int) (tea.Model, tea.Cmd) {
	if projectIdx >= len(m.projects) || wtIdx >= len(m.projects[projectIdx].Worktrees) {
		m.exitDeleteMode()
		return m, nil
	}
	wt := m.projects[projectIdx].Worktrees[wtIdx]
	projectPath := m.projects[projectIdx].Path

	if err := models.RemoveWorktree(projectPath, wt.Path, false); err != nil {
		if models.IsWorktreeDirtyError(err) {
			m.pendingForceDeleteWT = &pendingWorktreeDelete{projectIdx: projectIdx, wtIdx: wtIdx}
			m.setFeedback("Worktree has changes — press Y to force remove", "error")
			return m, nil
		}
		if models.IsWorktreeLockedError(err) {
			m.pendingForceDeleteWT = &pendingWorktreeDelete{projectIdx: projectIdx, wtIdx: wtIdx}
			m.setFeedback("Worktree is locked — press Y to force remove", "error")
			return m, nil
		}
		m.setFeedback("Failed to remove worktree", "error")
		m.exitDeleteMode()
		return m, nil
	}

	return m.reloadAfterWorktreeRemoval(wt.Branch)
}

// forceDeleteWorktree runs git worktree remove --force for the pending worktree.
func (m *MainMenuModel) forceDeleteWorktree() (tea.Model, tea.Cmd) {
	pending := m.pendingForceDeleteWT
	m.pendingForceDeleteWT = nil
	if pending == nil || pending.projectIdx >= len(m.projects) ||
		pending.wtIdx >= len(m.projects[pending.projectIdx].Worktrees) {
		m.exitDeleteMode()
		return m, nil
	}
	wt := m.projects[pending.projectIdx].Worktrees[pending.wtIdx]
	projectPath := m.projects[pending.projectIdx].Path

	if err := models.RemoveWorktree(projectPath, wt.Path, true); err != nil {
		m.setFeedback("Failed to force remove worktree", "error")
		m.exitDeleteMode()
		return m, nil
	}

	return m.reloadAfterWorktreeRemoval(wt.Branch)
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

// renderSettingsItem renders a single settings item row with state right-aligned.
func (m *MainMenuModel) renderSettingsItem(index int, label, stateText string, stateStyle, brightBoldStyle lipgloss.Style, leftBorder, rightBorder string) string {
	stateRendered := stateStyle.Render(stateText)
	if m.settingsSelected == index {
		marker := brightBoldStyle.Render("\u258e")
		labelText := brightBoldStyle.Render(label)
		prefix := "  " + marker + " " + labelText
		gap := menuContentWidth - lipgloss.Width(prefix) - lipgloss.Width(stateRendered) - 1
		if gap < 1 {
			gap = 1
		}
		return leftBorder + prefix + strings.Repeat(" ", gap) + stateRendered + " " + rightBorder
	}
	prefix := "    " + label
	gap := menuContentWidth - lipgloss.Width(prefix) - lipgloss.Width(stateRendered) - 1
	if gap < 1 {
		gap = 1
	}
	return leftBorder + prefix + strings.Repeat(" ", gap) + stateRendered + " " + rightBorder
}

// tabTitleLabel returns a display label for the tab title mode.
func tabTitleLabel(mode string) string {
	switch mode {
	case "full":
		return "Project \u00b7 Tool"
	case "project":
		return "Project Only"
	default:
		return mode
	}
}

// renderSettingsBox builds the settings panel box string.
func (m *MainMenuModel) renderSettingsBox() string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))

	// State color depends on ghost display mode
	var stateColor lipgloss.Color
	switch m.ghostDisplay {
	case "animated":
		stateColor = lipgloss.Color("114") // green
	case "static":
		stateColor = lipgloss.Color("220") // yellow
	default:
		stateColor = lipgloss.Color("241") // gray
	}
	stateStyle := lipgloss.NewStyle().Foreground(stateColor)

	hLine := strings.Repeat("\u2500", menuInnerWidth)
	topBorder := dimStyle.Render("\u256d" + hLine + "\u256e")
	separator := dimStyle.Render("\u251c" + hLine + "\u2524")
	bottomBorder := dimStyle.Render("\u2570" + hLine + "\u256f")
	leftBorder := dimStyle.Render("\u2502")
	rightBorder := strings.Repeat(" ", menuPadding) + dimStyle.Render("\u2502")

	var lines []string

	// Top border
	lines = append(lines, topBorder)

	// Title row
	title := primaryBoldStyle.Render("Settings")
	titlePadding := menuContentWidth - lipgloss.Width(title) - 1
	if titlePadding < 0 {
		titlePadding = 0
	}
	titleRow := leftBorder + " " + title + strings.Repeat(" ", titlePadding) + rightBorder
	lines = append(lines, titleRow)

	// Separator after title
	lines = append(lines, separator)

	// Empty row
	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder
	lines = append(lines, emptyRow)

	// Ghost Display item
	ghostLabel := "Ghost Display"
	ghostState := "[" + ghostDisplayLabel(m.ghostDisplay) + "]"
	lines = append(lines, m.renderSettingsItem(0, ghostLabel, ghostState, stateStyle, primaryBoldStyle, leftBorder, rightBorder))

	// Tab Title item
	var tabTitleColor lipgloss.Color
	if m.tabTitle == "full" {
		tabTitleColor = lipgloss.Color("114") // green
	} else {
		tabTitleColor = lipgloss.Color("220") // yellow
	}
	tabTitleStyle := lipgloss.NewStyle().Foreground(tabTitleColor)
	tabLabel := "Tab Title"
	tabState := "[" + tabTitleLabel(m.tabTitle) + "]"
	lines = append(lines, m.renderSettingsItem(1, tabLabel, tabState, tabTitleStyle, primaryBoldStyle, leftBorder, rightBorder))

	// Sound Notifications item
	var soundColor lipgloss.Color
	if m.soundName != "" {
		soundColor = lipgloss.Color("114") // green
	} else {
		soundColor = lipgloss.Color("241") // gray
	}
	soundStyle := lipgloss.NewStyle().Foreground(soundColor)
	soundLabel := "Sound"
	soundState := "[Off]"
	if m.soundName != "" {
		soundState = "[" + m.soundName + "]"
	}
	lines = append(lines, m.renderSettingsItem(2, soundLabel, soundState, soundStyle, primaryBoldStyle, leftBorder, rightBorder))

	// Default projects dir item
	var rootState string
	if m.projectsRoot == "" {
		rootState = "(not set)"
	} else {
		rootState = shortenHomePath(m.projectsRoot)
	}
	rootColor := lipgloss.Color("241") // gray when not set
	if m.projectsRoot != "" {
		rootColor = lipgloss.Color("114") // green when set
	}
	rootStyle := lipgloss.NewStyle().Foreground(rootColor)
	if m.settingsInputMode && m.settingsSelected == 3 {
		// Render inline text input
		inputView := m.settingsInput.View()
		inputWidth := lipgloss.Width(inputView)
		inputPadding := menuContentWidth - inputWidth - 1
		if inputPadding < 0 {
			inputPadding = 0
		}
		inputRow := leftBorder + " " + inputView + strings.Repeat(" ", inputPadding) + rightBorder
		lines = append(lines, inputRow)
		if m.settingsInputErr != nil {
			errText := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(m.settingsInputErr.Error())
			errPadding := menuContentWidth - lipgloss.Width(errText) - 1
			if errPadding < 0 {
				errPadding = 0
			}
			errRow := leftBorder + " " + errText + strings.Repeat(" ", errPadding) + rightBorder
			lines = append(lines, errRow)
		}
	} else {
		lines = append(lines, m.renderSettingsItem(3, "Default projects dir", rootState, rootStyle, primaryBoldStyle, leftBorder, rightBorder))
	}

	// Claude Config item (only for the claude tool)
	if m.ClaudeConfigVisible() {
		cfgName := m.CurrentClaudeConfigName()
		var cfgColor lipgloss.Color
		if m.CurrentClaudeConfigFile() != "" {
			cfgColor = lipgloss.Color("114") // green when a config is active
		} else {
			cfgColor = lipgloss.Color("241") // gray for Standard
		}
		cfgStyle := lipgloss.NewStyle().Foreground(cfgColor)
		lines = append(lines, m.renderSettingsItem(4, "Claude Config", "["+cfgName+"]", cfgStyle, primaryBoldStyle, leftBorder, rightBorder))
	}

	// Empty row
	lines = append(lines, emptyRow)

	// Separator before help
	lines = append(lines, separator)

	// Help row — show ⏎ edit hint for item 3 (projects dir), ← → cycle for others
	sep := dimStyle.Render(" · ")
	var cycleOrEdit string
	switch m.settingsSelected {
	case 3:
		cycleOrEdit = helpStyle.Render("\u23ce edit")
	case 4:
		cycleOrEdit = helpStyle.Render("\u2190\u2192 cycle") + sep + helpStyle.Render("\u23ce manage")
	default:
		cycleOrEdit = helpStyle.Render("\u2190\u2192 cycle")
	}
	helpContent := helpStyle.Render("\u2191\u2193 navigate") + sep + cycleOrEdit + sep + helpStyle.Render("Esc close")
	helpContentWidth := lipgloss.Width(helpContent)
	helpPadding := menuContentWidth - helpContentWidth - 1
	if helpPadding < 0 {
		helpPadding = 0
	}
	helpRow := leftBorder + " " + helpContent + strings.Repeat(" ", helpPadding) + rightBorder
	lines = append(lines, helpRow)

	// Bottom border
	lines = append(lines, bottomBorder)

	return strings.Join(lines, "\n")
}

const menuInnerWidth = 56
const menuPadding = 2
const menuContentWidth = menuInnerWidth - menuPadding // 54 (right-side padding only)

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

// renderMenuBox builds the complete menu box string.
func (m *MainMenuModel) renderMenuBox() string {
	dimStyle := lipgloss.NewStyle().Foreground(m.theme.Dim)
	primaryStyle := lipgloss.NewStyle().Foreground(m.theme.Primary)
	primaryBoldStyle := lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	updateStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	neutralTextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	neutralDimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	moveFlashStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	deleteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	staleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	deleteDimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	hLine := strings.Repeat("\u2500", menuInnerWidth)
	topBorder := dimStyle.Render("\u256d" + hLine + "\u256e")
	separator := dimStyle.Render("\u251c" + hLine + "\u2524")
	bottomBorder := dimStyle.Render("\u2570" + hLine + "\u256f")
	leftBorder := dimStyle.Render("\u2502")
	rightBorder := strings.Repeat(" ", menuPadding) + dimStyle.Render("\u2502")

	var lines []string

	// Top border
	lines = append(lines, topBorder)

	// Title row
	title := primaryBoldStyle.Render("Ghost Tab")
	aiDisplay := AIToolDisplayName(m.CurrentAITool())
	var aiPart string
	if len(m.aiTools) > 1 {
		aiPart = dimStyle.Render(" \u25c2 ") + primaryStyle.Render(aiDisplay) + dimStyle.Render(" \u25b8")
	} else {
		aiPart = " " + primaryStyle.Render(aiDisplay)
	}
	// Right-align AI tool chooser: "⬡ Ghost Tab" left, "◂ Claude Code ▸" right
	aiPadding := menuContentWidth - lipgloss.Width(title) - lipgloss.Width(aiPart) - 1 // -1 for leading space
	if aiPadding < 1 {
		aiPadding = 1
	}
	titleRow := leftBorder + " " + title + strings.Repeat(" ", aiPadding) + aiPart + rightBorder
	lines = append(lines, titleRow)

	// Separator after title
	lines = append(lines, separator)

	// Update notification (if set)
	if m.updateVersion != "" {
		updateMsg := fmt.Sprintf("Update available: %s (brew upgrade ghost-tab)", m.updateVersion)
		updateContent := updateStyle.Render(updateMsg)
		updatePadding := menuContentWidth - lipgloss.Width(updateContent) - 2 // leading 2 spaces
		if updatePadding < 0 {
			updatePadding = 0
		}
		updateRow := leftBorder + "  " + updateContent + strings.Repeat(" ", updatePadding) + rightBorder
		lines = append(lines, updateRow)
	}

	// Empty line before items
	emptyRow := leftBorder + strings.Repeat(" ", menuContentWidth) + rightBorder
	lines = append(lines, emptyRow)

	// Project items
	numProjects := len(m.projects)
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
			// Delete mode: red styles. Normal mode: primary or flash.
			selNameStyle := primaryBoldStyle
			selPathStyle := primaryStyle
			if m.deleteMode {
				selNameStyle = deleteStyle
				selPathStyle = deleteDimStyle
			} else if flashing {
				selNameStyle = moveFlashStyle
				selPathStyle = moveFlashStyle
			}

			marker := selNameStyle.Render("\u258e")
			truncName := TruncateMiddle(proj.Name, menuContentWidth-7-len(num))
			nameText := selNameStyle.Render(num + "  " + truncName)
			// "  ▎ 1  name" -> 2 spaces + marker + space + num + 2 spaces + name
			// For stale projects, replace the first 2 spaces with "⚠ " marker.
			var namePrefix string
			if proj.Stale && !m.deleteMode {
				namePrefix = staleStyle.Render("⚠") + " "
			} else {
				namePrefix = "  "
			}
			nameContent := namePrefix + marker + " " + nameText

			if wtIndicator != "" {
				wtStyled := dimStyle.Render(wtIndicator)
				gap := menuContentWidth - lipgloss.Width(nameContent) - lipgloss.Width(wtStyled)
				if gap < 1 {
					gap = 1
				}
				nameLine = leftBorder + nameContent + strings.Repeat(" ", gap) + wtStyled + rightBorder
			} else {
				namePadding := menuContentWidth - lipgloss.Width(nameContent)
				if namePadding < 0 {
					namePadding = 0
				}
				nameLine = leftBorder + nameContent + strings.Repeat(" ", namePadding) + rightBorder
			}

			pathContent := "       " + selPathStyle.Render(shortPath)
			pathPadding := menuContentWidth - lipgloss.Width(pathContent)
			if pathPadding < 0 {
				pathPadding = 0
			}
			pathLine = leftBorder + pathContent + strings.Repeat(" ", pathPadding) + rightBorder
		} else {
			// Choose style: amber flash when recently moved, neutral otherwise.
			rowNameStyle := neutralTextStyle
			rowDimStyle := neutralDimStyle
			if flashing {
				rowNameStyle = moveFlashStyle
				rowDimStyle = moveFlashStyle
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
				nameLine = leftBorder + nameContent + strings.Repeat(" ", gap) + wtStyled + rightBorder
			} else {
				namePadding := menuContentWidth - lipgloss.Width(nameContent)
				if namePadding < 0 {
					namePadding = 0
				}
				nameLine = leftBorder + nameContent + strings.Repeat(" ", namePadding) + rightBorder
			}

			pathContent := "       " + rowDimStyle.Render(shortPath)
			pathPadding := menuContentWidth - lipgloss.Width(pathContent)
			if pathPadding < 0 {
				pathPadding = 0
			}
			pathLine = leftBorder + pathContent + strings.Repeat(" ", pathPadding) + rightBorder
		}

		lines = append(lines, nameLine)
		lines = append(lines, pathLine)

		// Expanded worktree entries (2 rows each: branch + path) + add-worktree (1 row)
		if m.expandedWorktrees[i] {
			// All worktrees use ├─ connector (add-worktree follows as last item)
			connector := "\u251c\u2500"
			for j, wt := range proj.Worktrees {
				wtFlatIdx := m.projectToFlatIndex(i) + 1 + j
				wtSelected := !m.deleteMode && m.selectedItem == wtFlatIdx
				wtDeleteSelected := m.deleteMode && m.deleteSelected == wtFlatIdx
				var wtBranchLine, wtPathLine string
				branchDisplay := TruncateMiddle(wt.Branch, menuContentWidth-11)
				shortWtPath := TruncateMiddle(shortenHomePath(wt.Path), menuContentWidth-11)

				if wtDeleteSelected {
					marker := deleteStyle.Render("\u258e")
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
					marker := primaryBoldStyle.Render("\u258e")
					connStyled := primaryBoldStyle.Render(connector)
					branchText := primaryBoldStyle.Render(branchDisplay)
					content := "     " + marker + " " + connStyled + " " + branchText
					padding := menuContentWidth - lipgloss.Width(content)
					if padding < 0 {
						padding = 0
					}
					wtBranchLine = leftBorder + content + strings.Repeat(" ", padding) + rightBorder

					pathContent := "          " + primaryStyle.Render(shortWtPath)
					pathPadding := menuContentWidth - lipgloss.Width(pathContent)
					if pathPadding < 0 {
						pathPadding = 0
					}
					wtPathLine = leftBorder + pathContent + strings.Repeat(" ", pathPadding) + rightBorder
				} else {
					connStyled := neutralDimStyle.Render(connector)
					branchText := neutralTextStyle.Render(branchDisplay)
					content := "       " + connStyled + " " + branchText
					padding := menuContentWidth - lipgloss.Width(content)
					if padding < 0 {
						padding = 0
					}
					wtBranchLine = leftBorder + content + strings.Repeat(" ", padding) + rightBorder

					pathContent := "          " + neutralDimStyle.Render(shortWtPath)
					pathPadding := menuContentWidth - lipgloss.Width(pathContent)
					if pathPadding < 0 {
						pathPadding = 0
					}
					wtPathLine = leftBorder + pathContent + strings.Repeat(" ", pathPadding) + rightBorder
				}
				lines = append(lines, wtBranchLine)
				lines = append(lines, wtPathLine)
			}

			// Add-worktree item (1 row, └─ connector)
			addWtFlatIdx := m.projectToFlatIndex(i) + 1 + len(proj.Worktrees)
			addWtSelected := m.selectedItem == addWtFlatIdx
			addConnector := "\u2514\u2500"
			var addWtLine string
			if addWtSelected {
				marker := primaryBoldStyle.Render("\u258e")
				connStyled := primaryBoldStyle.Render(addConnector)
				addLabel := primaryBoldStyle.Render("+ Add worktree")
				content := "     " + marker + " " + connStyled + " " + addLabel
				padding := menuContentWidth - lipgloss.Width(content)
				if padding < 0 {
					padding = 0
				}
				addWtLine = leftBorder + content + strings.Repeat(" ", padding) + rightBorder
			} else {
				connStyled := neutralDimStyle.Render(addConnector)
				addLabel := neutralDimStyle.Render("+ Add worktree")
				content := "       " + connStyled + " " + addLabel
				padding := menuContentWidth - lipgloss.Width(content)
				if padding < 0 {
					padding = 0
				}
				addWtLine = leftBorder + content + strings.Repeat(" ", padding) + rightBorder
			}
			lines = append(lines, addWtLine)
		}
	}

	// Separator between projects and actions (only if there are projects)
	if numProjects > 0 {
		lines = append(lines, separator)
	}

	// Action items
	for i, action := range actionLabels {
		actionIdx := numProjects + m.expandedWorktreeCount() + i
		selected := !m.deleteMode && m.selectedItem == actionIdx

		var actionLine string
		if selected {
			marker := primaryBoldStyle.Render("\u258e")
			shortcutText := primaryBoldStyle.Render(action.shortcut + "  " + action.label)
			content := "  " + marker + " " + shortcutText
			padding := menuContentWidth - lipgloss.Width(content)
			if padding < 0 {
				padding = 0
			}
			actionLine = leftBorder + content + strings.Repeat(" ", padding) + rightBorder
		} else {
			shortcutText := neutralDimStyle.Render(action.shortcut)
			labelText := neutralTextStyle.Render(action.label)
			content := "    " + shortcutText + "  " + labelText
			padding := menuContentWidth - lipgloss.Width(content)
			if padding < 0 {
				padding = 0
			}
			actionLine = leftBorder + content + strings.Repeat(" ", padding) + rightBorder
		}

		lines = append(lines, actionLine)
	}

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

	// Separator before help
	lines = append(lines, separator)

	// Help row
	hasWorktrees := false
	for _, p := range m.projects {
		if len(p.Worktrees) > 0 {
			hasWorktrees = true
			break
		}
	}

	sep := dimStyle.Render(" · ")
	var helpContent string
	if m.deleteMode {
		helpContent = helpStyle.Render("\u2191\u2193 navigate") + sep + helpStyle.Render("1-9 jump") + sep + helpStyle.Render("\u23ce delete") + sep + helpStyle.Render("Q cancel")
	} else if m.showEscHint {
		helpContent = helpStyle.Render("Press Esc again to quit")
	} else {
		var parts []string
		if len(m.projects) > 1 {
			parts = append(parts, helpStyle.Render("Shift+\u2191\u2193 move"))
		}
		if len(m.aiTools) > 1 {
			parts = append(parts, helpStyle.Render("\u2190\u2192 AI"))
		}
		if hasWorktrees {
			parts = append(parts, helpStyle.Render("w trees"))
		}
		parts = append(parts, helpStyle.Render("d delete"))
		parts = append(parts, helpStyle.Render("\u23ce select"))
		helpContent = strings.Join(parts, sep)
	}
	helpWidth := lipgloss.Width(helpContent)
	helpLeft := (menuInnerWidth - helpWidth) / 2
	if helpLeft < 0 {
		helpLeft = 0
	}
	helpRight := menuInnerWidth - helpWidth - helpLeft - menuPadding
	if helpRight < 0 {
		helpRight = 0
	}
	helpRow := leftBorder + strings.Repeat(" ", helpLeft) + helpContent + strings.Repeat(" ", helpRight) + rightBorder
	lines = append(lines, helpRow)

	// Bottom border
	lines = append(lines, bottomBorder)

	// Scroll clipping when menu is taller than the available terminal height.
	headerEnd := 4
	if m.updateVersion != "" {
		headerEnd = 5
	}
	footerStart := len(lines) - 3
	avail := m.availableMenuHeight()
	if avail > 0 && len(lines) > avail && headerEnd < footerStart {
		lines = m.applyMenuScroll(lines, headerEnd, footerStart, avail)
	}

	return strings.Join(lines, "\n")
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
		// Ghost (15) + gap (1) = 16. Animated ghost adds a bob row.
		// Sleeping stacks a 3-row Zzz frame above the ghost.
		reserve := 16
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
	case "action":
		row := baseProjectRow(len(m.projects))
		if len(m.projects) > 0 {
			row++ // separator between projects and actions
		}
		return row + projectIdx
	}
	return 0
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

	title := primaryBoldStyle.Render("Ghost Tab")
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

	pathLabel := "  Path: "
	inputView := m.pathInput.View()
	inputContent := pathLabel + inputView
	inputPadding := menuContentWidth - lipgloss.Width(inputContent)
	if inputPadding < 0 {
		inputPadding = 0
	}
	lines = append(lines, leftBorder+inputContent+strings.Repeat(" ", inputPadding)+rightBorder)

	if m.inputErr != nil {
		errMsg := errorStyle.Render(m.inputErr.Error())
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
		nameLabel := "  Name: "
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
			errMsg := errorStyle.Render(m.nameErr.Error())
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
	if m.autocomplete.ShowSuggestions() {
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
	if m.settingsMode {
		if m.configPanelOpen {
			menuBox = m.renderConfigPanel()
		} else {
			menuBox = m.renderSettingsBox()
		}
	} else if m.inputMode != "" {
		menuBox = m.renderInputBox()
	} else {
		menuBox = m.renderMenuBox()
	}

	layout := m.CalculateLayout(m.width, m.height)

	// Determine ghost display
	ghostPosition := layout.GhostPosition
	if m.ghostDisplay == "none" {
		ghostPosition = "hidden"
	}

	var content string

	switch ghostPosition {
	case "side":
		ghostLines := GhostForTool(m.CurrentAITool(), m.ghostSleeping)
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
		ghostLines := GhostForTool(m.CurrentAITool(), m.ghostSleeping)
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

	if m.width > 0 && m.height > 0 {
		contentHeight := strings.Count(content, "\n") + 1
		m.centerOffsetY = (m.height - contentHeight) / 2
		if m.centerOffsetY < 0 {
			m.centerOffsetY = 0
		}
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
	}
	m.centerOffsetY = 0
	return content
}
