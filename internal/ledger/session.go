package ledger

import (
	"bufio"
	"context"
	"fmt"
	"os"
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
	RelaunchFile  string
	Tool          string
	ToolCommand   string
	Settings      string
	Filter        string
	ProjectDir    string
	AccountsDir   string
	Pointer       string
	List          string
	Colors        string
	DefaultLabel  string
	ConfigPointer string
	ConfigsList   string
	Tools         []string
	Pill          *SessionPill
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

// Load parses the context and resolves the pill only when another account or
// agent is available to switch to.
func (s *SessionSource) Load(ctx context.Context, path string) (SessionContext, error) {
	result, err := ParseSessionContext(path)
	if err != nil {
		return SessionContext{}, err
	}
	accounts := claudeaccount.Load(result.List)
	if len(accounts) == 0 && len(result.Tools) < 2 && len(claudeconfig.Load(result.ConfigsList)) == 0 {
		return result, nil
	}
	if result.Tool != "claude" {
		result.Pill = &SessionPill{Label: models.DisplayName(result.Tool), Color: toolAccent(result.Tool)}
		return result, nil
	}

	// A running subscription backend replaces the account on the pill — the
	// account is overridden while the backend runs, so a login label would lie.
	// Legacy contexts (no config_pointer) fall back to the launch-frozen plan.
	if result.ConfigPointer != "" {
		if config := s.activeSubscription(ctx, result.ConfigPointer); config != "" {
			result.Pill = &SessionPill{
				Label: subscriptionDisplayName(config, result.ConfigsList),
				Color: subscriptionColor(config, result.ConfigsList, result.Colors),
				Glyph: subscriptionPillGlyph,
			}
			return result, nil
		}
	} else if s != nil && s.plan != "" && s.plan != "Standard Claude" {
		result.Pill = &SessionPill{Label: s.plan, Color: subscriptionPillColor, Glyph: subscriptionPillGlyph}
		return result, nil
	}

	active := claudeaccount.GetActive(result.Pointer)
	if s != nil && s.runner != nil {
		out, runErr := s.runner.Run(ctx, "tmux", "show-environment", "WISP_DECK_CLAUDE_ACCOUNT")
		if runErr == nil {
			line := strings.TrimSpace(string(out))
			if value, ok := strings.CutPrefix(line, "WISP_DECK_CLAUDE_ACCOUNT="); ok {
				active = value
			}
		}
	}
	label := claudeaccount.GetDefaultLabel(result.DefaultLabel)
	if active != "" {
		label = active
		for _, account := range accounts {
			if account.Dir == active {
				label = account.Label
				break
			}
		}
	}
	colorKey := active
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

// activeSubscription returns the config filename this session's pane runs
// (empty = standard Claude). The active-config pointer is global, so a stamped
// per-session value (WISP_DECK_CLAUDE_CONFIG in the tmux session env) wins —
// including a stamped empty, meaning standard — and only an unstamped session
// falls back to the pointer file.
func (s *SessionSource) activeSubscription(ctx context.Context, pointerFile string) string {
	if s != nil && s.runner != nil {
		out, err := s.runner.Run(ctx, "tmux", "show-environment", "WISP_DECK_CLAUDE_CONFIG")
		if err == nil {
			line := strings.TrimSpace(string(out))
			if value, ok := strings.CutPrefix(line, "WISP_DECK_CLAUDE_CONFIG="); ok {
				return value
			}
		}
	}
	return claudeconfig.GetActive(pointerFile)
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

// subscriptionDisplayName maps a config filename to its display name; a
// filename absent from the list falls back to the bare name (minus .json) so a
// stale pane still shows something meaningful.
func subscriptionDisplayName(file, listFile string) string {
	for _, config := range claudeconfig.Load(listFile) {
		if config.File == file {
			return config.Name
		}
	}
	return strings.TrimSuffix(file, ".json")
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

// AccountSwitcher launches the existing account-switch/relaunch flow.
type AccountSwitcher interface {
	Switch(context.Context, SessionContext) error
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
. "$1/tmux-session.sh"
. "$1/account-switch.sh"
open_account_switcher tmux "$2"
`

// Switch opens the existing Go switcher through the current bash relaunch
// orchestration, preserving draft/session behavior without porting it.
func (s *ExecAccountSwitcher) Switch(ctx context.Context, session SessionContext) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("switch account: no process runner configured")
	}
	if s.libDir == "" || session.RelaunchFile == "" {
		return fmt.Errorf("switch account: missing library or relaunch context")
	}
	if _, err := s.runner.Run(ctx, "bash", "-c", ledgerAccountSwitchScript, "--", s.libDir, session.RelaunchFile); err != nil {
		return fmt.Errorf("switch account: %w", err)
	}
	return nil
}
