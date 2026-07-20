package ledger

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
	"github.com/jackuait/wisp-deck/internal/claudeconfig"
	"github.com/jackuait/wisp-deck/internal/models"
)

// subscriptionPillColor matches the switcher popup's subscription rows.
const subscriptionPillColor = 111

// subscriptionPillGlyph is the subscription spark, matching configRowGlyph in
// the switcher popup.
const subscriptionPillGlyph = "✦"

// SessionPill is the current agent/account identity shown in the ledger footer.
// Glyph is the identity-kind mark: empty means the account person (󰀄, the
// renderer's default); a subscription pill carries the spark (✦), matching the
// switcher's subscription rows.
type SessionPill struct {
	Label string
	Color int
	Glyph string
}

// SessionContext is the existing key=value relaunch context plus its resolved
// display identity. Unknown keys remain forward-compatible and are ignored.
type SessionContext struct {
	RelaunchFile    string
	Tool            string
	ToolCommand     string
	ClaudeCommand   string
	OpenCodeCommand string
	CodexCommand    string
	Settings        string
	Filter          string
	ProjectDir      string
	AccountsDir     string
	Pointer         string
	List            string
	Colors          string
	DefaultLabel    string
	ConfigPointer   string
	ConfigsDir      string
	ConfigsList     string
	Tools           []string
	SwitchOptions   []SwitchOption
	Pill            *SessionPill
}

// ParseSessionContext reads the established relaunch-context format without
// changing whitespace or splitting values that contain '='.
func ParseSessionContext(path string) (SessionContext, error) {
	file, err := os.Open(path)
	if err != nil {
		return SessionContext{}, err
	}
	defer file.Close()
	result := SessionContext{RelaunchFile: path}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "tool":
			result.Tool = value
		case "tool_cmd":
			result.ToolCommand = value
		case "claude_cmd":
			result.ClaudeCommand = value
		case "opencode_cmd":
			result.OpenCodeCommand = value
		case "codex_cmd":
			result.CodexCommand = value
		case "settings":
			result.Settings = value
		case "filter":
			result.Filter = value
		case "project_dir":
			result.ProjectDir = value
		case "accounts_dir":
			result.AccountsDir = value
		case "pointer":
			result.Pointer = value
		case "list":
			result.List = value
		case "colors":
			result.Colors = value
		case "default_label":
			result.DefaultLabel = value
		case "config_pointer":
			result.ConfigPointer = value
		case "configs_dir":
			result.ConfigsDir = value
		case "configs_list":
			result.ConfigsList = value
		case "tools":
			result.Tools = strings.Fields(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return SessionContext{}, err
	}
	if result.Tool == "" {
		result.Tool = "claude"
	}
	return result, nil
}

// SessionSource resolves the live session identity from a relaunch context.
type SessionSource struct {
	runner ProcessRunner
	plan   string
}

// SessionSourceOption configures a SessionSource.
type SessionSourceOption func(*SessionSource)

// WithSessionPlan supplies the pane's launch-frozen subscription label
// (WISP_DECK_PLAN) used as the pill fallback for legacy relaunch contexts
// that carry no config_pointer.
func WithSessionPlan(plan string) SessionSourceOption {
	return func(source *SessionSource) {
		source.plan = plan
	}
}

// NewSessionSource creates a session-context loader.
func NewSessionSource(runner ProcessRunner, options ...SessionSourceOption) *SessionSource {
	source := &SessionSource{runner: runner}
	for _, option := range options {
		option(source)
	}
	return source
}

// Load parses the context and precomputes every switcher row off the Bubble Tea
// loop, so clicking the pill can paint the chooser without starting a process.
func (s *SessionSource) Load(ctx context.Context, path string) (SessionContext, error) {
	result, err := ParseSessionContext(path)
	if err != nil {
		return SessionContext{}, err
	}
	accounts := claudeaccount.Load(result.List)
	configs := claudeconfig.Load(result.ConfigsList)
	if len(accounts) == 0 && len(result.Tools) < 2 && len(configs) == 0 {
		return result, nil
	}
	activeAccount, activeConfig := s.activeSessionIdentities(ctx, result.Pointer, result.ConfigPointer)
	result.SwitchOptions = sessionSwitchOptions(result, accounts, configs, activeAccount, activeConfig)
	if result.Tool != "claude" {
		result.Pill = &SessionPill{Label: models.DisplayName(result.Tool), Color: toolAccent(result.Tool)}
		return result, nil
	}

	// A running subscription backend replaces the account on the pill — the
	// account is overridden while the backend runs, so a login label would lie.
	// Legacy contexts (no config_pointer) fall back to the launch-frozen plan.
	if activeConfig != "" {
		for _, option := range result.SwitchOptions {
			if option.Choice.Kind == SwitchSubscription && option.Choice.Value == activeConfig {
				result.Pill = &SessionPill{Label: option.Label, Color: option.Color, Glyph: option.Glyph}
				return result, nil
			}
		}
		result.Pill = &SessionPill{
			Label: strings.TrimSuffix(activeConfig, ".json"),
			Color: subscriptionColor(activeConfig, result.ConfigsList, result.Colors),
			Glyph: subscriptionPillGlyph,
		}
		return result, nil
	}
	if result.ConfigPointer == "" && s != nil && s.plan != "" && s.plan != "Standard Claude" {
		result.Pill = &SessionPill{Label: s.plan, Color: subscriptionPillColor, Glyph: subscriptionPillGlyph}
		return result, nil
	}

	for _, option := range result.SwitchOptions {
		if option.Active && option.Choice.Kind == SwitchAccount {
			result.Pill = &SessionPill{Label: option.Label, Color: option.Color}
			return result, nil
		}
	}

	label := claudeaccount.GetDefaultLabel(result.DefaultLabel)
	if activeAccount != "" {
		label = activeAccount
		for _, account := range accounts {
			if account.Dir == activeAccount {
				label = account.Label
				break
			}
		}
	}
	colorKey := activeAccount
	if colorKey == "" {
		colorKey = "default"
	}
	color := claudeaccount.LoadColors(result.Colors)[colorKey]
	if color == 0 {
		color = 209
	}
	result.Pill = &SessionPill{Label: label, Color: color}
	return result, nil
}

func (s *SessionSource) activeSessionIdentities(
	ctx context.Context,
	accountPointer string,
	configPointer string,
) (string, string) {
	var account, config string
	var accountStamped, configStamped bool
	if s != nil && s.runner != nil {
		out, err := s.runner.Run(ctx, "tmux", "show-environment")
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if value, ok := strings.CutPrefix(line, "WISP_DECK_CLAUDE_ACCOUNT="); ok {
					account, accountStamped = value, true
				}
				if value, ok := strings.CutPrefix(line, "WISP_DECK_CLAUDE_CONFIG="); ok {
					config, configStamped = value, true
				}
			}
		}
	}
	if !accountStamped {
		account = claudeaccount.GetActive(accountPointer)
	}
	if configPointer != "" && !configStamped {
		config = claudeconfig.GetActive(configPointer)
	}
	return account, config
}

func sessionToolReady(session SessionContext, tool string) bool {
	available := len(session.Tools) == 0 && session.Tool == tool
	for _, candidate := range session.Tools {
		if candidate == tool {
			available = true
			break
		}
	}
	return available && sessionToolExecutableReady(session, tool)
}

func sessionToolExecutableReady(session SessionContext, tool string) bool {
	command := ""
	switch tool {
	case "claude":
		command = session.ClaudeCommand
	case "opencode":
		command = session.OpenCodeCommand
	case "codex":
		command = session.CodexCommand
	}
	if command == "" && session.Tool == tool {
		command = session.ToolCommand
	}
	// Relaunch contexts predating per-tool commands can only describe their
	// currently-running tool. Preserve that compatibility; Bash resolves PATH
	// again before any actual switch.
	if command == "" {
		return session.Tool == tool
	}
	if info, err := os.Stat(command); err == nil {
		return !info.IsDir() && info.Mode().Perm()&0o111 != 0
	}
	if tool == "opencode" &&
		(command == "npx --no-install opencode-ai" ||
			command == "npx --prefer-offline opencode-ai@latest") {
		_, err := exec.LookPath("npx")
		return err == nil
	}
	if strings.ContainsAny(command, " \t\r\n") {
		return false
	}
	_, err := exec.LookPath(command)
	return err == nil
}

func sessionSwitchOptions(
	session SessionContext,
	accounts []claudeaccount.Account,
	configs []claudeconfig.Config,
	activeAccount string,
	activeConfig string,
) []SwitchOption {
	configColors := ""
	if session.ConfigsList != "" {
		configColors = filepath.Join(filepath.Dir(session.ConfigsList), "claude-config-colors")
	}
	accountColor := func(dir string) int {
		if configColors != "" {
			return claudeaccount.ColorFor(session.Colors, dir, configColors)
		}
		return claudeaccount.ColorFor(session.Colors, dir)
	}
	claudeActive := session.Tool == "" || session.Tool == "claude"
	claudeReady := sessionToolReady(session, "claude")
	options := []SwitchOption{{
		Choice: SwitchChoice{Kind: SwitchAccount},
		Label:  claudeaccount.GetDefaultLabel(session.DefaultLabel),
		Color:  accountColor(""),
		Ready:  claudeReady,
		Active: claudeActive && activeConfig == "" && activeAccount == "",
	}}
	for _, account := range accounts {
		options = append(options, SwitchOption{
			Choice: SwitchChoice{Kind: SwitchAccount, Value: account.Dir},
			Label:  account.Label,
			Color:  accountColor(account.Dir),
			Ready:  claudeReady,
			Active: claudeActive && activeConfig == "" && activeAccount == account.Dir,
		})
	}

	disabled := claudeconfig.LoadDisabled(claudeconfig.DisabledFile(session.ConfigsList))
	for _, config := range configs {
		if disabled[config.File] && config.File != activeConfig {
			continue
		}
		ready := claudeReady && claudeconfig.ConfigReady(session.ConfigsDir, config)
		if claudeconfig.ProviderForConfig(session.ConfigsDir, config).Auth == claudeconfig.AuthCodexChatGPT {
			ready = ready && sessionToolExecutableReady(session, "codex")
		}
		options = append(options, SwitchOption{
			Choice: SwitchChoice{Kind: SwitchSubscription, Value: config.File},
			Label:  config.Name,
			Color:  subscriptionColor(config.File, session.ConfigsList, session.Colors),
			Glyph:  subscriptionPillGlyph,
			Ready:  ready,
			Active: claudeActive && activeConfig == config.File,
		})
	}
	for _, tool := range session.Tools {
		if tool == "" || tool == "claude" {
			continue
		}
		options = append(options, SwitchOption{
			Choice: SwitchChoice{Kind: SwitchTool, Value: tool},
			Label:  models.DisplayName(tool),
			Color:  toolAccent(tool),
			Glyph:  switchToolGlyph(tool),
			Ready:  sessionToolReady(session, tool),
			Active: session.Tool == tool,
		})
	}
	return options
}

func switchToolGlyph(tool string) string {
	switch tool {
	case "codex":
		return "󰛄"
	case "opencode":
		return "▣"
	default:
		return "󰚩"
	}
}

// subscriptionColor resolves the subscription's persistent identity color from
// the claude-config-colors file next to the configs list, assigning one on
// first use and steering clear of the account colors so a subscription never
// mimics a login. Without a configs list there is nowhere to persist, so the
// shared switcher blue stands in.
func subscriptionColor(file, configsList, accountColors string) int {
	if configsList == "" {
		return subscriptionPillColor
	}
	colorsFile := filepath.Join(filepath.Dir(configsList), "claude-config-colors")
	return claudeconfig.ColorFor(colorsFile, file, accountColors)
}

func toolAccent(tool string) int {
	switch tool {
	case "opencode":
		return 141
	case "codex":
		return 36
	default:
		return 209
	}
}

// SwitchKind identifies which session identity a chooser selection targets.
type SwitchKind string

const (
	SwitchAccount      SwitchKind = "account"
	SwitchSubscription SwitchKind = "subscription"
	SwitchTool         SwitchKind = "tool"
)

// SwitchChoice is the exact identity selected by the in-process ledger chooser.
type SwitchChoice struct {
	Kind  SwitchKind
	Value string
}

// SwitchOption is one fully-resolved chooser row. Session loading performs all
// file reads, readiness checks, and color assignment before the row reaches the
// Bubble Tea input/render loop.
type SwitchOption struct {
	Choice SwitchChoice
	Label  string
	Color  int
	Glyph  string
	Ready  bool
	Active bool
}

// AccountSwitcher applies a chooser selection through the existing relaunch flow.
type AccountSwitcher interface {
	Switch(context.Context, SessionContext, SwitchChoice) error
}

// ExecAccountSwitcher delegates to account-switch.sh with a fixed shell
// program and argv-safe library/context paths.
type ExecAccountSwitcher struct {
	runner ProcessRunner
	libDir string
}

// NewExecAccountSwitcher creates an adapter for the established switch flow.
func NewExecAccountSwitcher(runner ProcessRunner, libDir string) *ExecAccountSwitcher {
	return &ExecAccountSwitcher{runner: runner, libDir: libDir}
}

const ledgerAccountSwitchScript = `
. "$1/statusline.sh"
. "$1/claude-accounts.sh"
. "$1/claude-configs.sh"
. "$1/claude-shared-settings.sh"
. "$1/tmux-session.sh"
. "$1/session-pool.sh"
. "$1/account-switch.sh"
apply_account_switch_choice tmux "$2" "$3" "$4"
`

// Switch applies an already-selected identity through the established relaunch
// orchestration. The chooser runs in-process, so this shell starts only after
// confirmation and never opens or capability-probes the standalone popup.
func (s *ExecAccountSwitcher) Switch(ctx context.Context, session SessionContext, choice SwitchChoice) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("switch account: no process runner configured")
	}
	if s.libDir == "" || session.RelaunchFile == "" {
		return fmt.Errorf("switch account: missing library or relaunch context")
	}
	switch choice.Kind {
	case SwitchAccount:
		if choice.Value != "" {
			info, err := os.Stat(filepath.Join(session.AccountsDir, choice.Value))
			if err != nil || !info.IsDir() {
				return fmt.Errorf("switch account: account %q is no longer available", choice.Value)
			}
		}
	case SwitchSubscription:
		var selected *claudeconfig.Config
		for _, config := range claudeconfig.Load(session.ConfigsList) {
			if config.File == choice.Value {
				selected = &config
				break
			}
		}
		disabled := claudeconfig.LoadDisabled(claudeconfig.DisabledFile(session.ConfigsList))
		if selected == nil || disabled[choice.Value] ||
			!claudeconfig.ConfigReady(session.ConfigsDir, *selected) {
			return fmt.Errorf("switch account: subscription %q is no longer ready", choice.Value)
		}
	case SwitchTool:
		if choice.Value == "" {
			return fmt.Errorf("switch account: missing tool choice")
		}
	default:
		return fmt.Errorf("switch account: unknown choice kind %q", choice.Kind)
	}
	if _, err := s.runner.Run(ctx, "bash", "-c", ledgerAccountSwitchScript, "--",
		s.libDir, session.RelaunchFile, string(choice.Kind), choice.Value); err != nil {
		return fmt.Errorf("switch account: %w", err)
	}
	return nil
}
