package ledger

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackuait/wisp-deck/internal/claudeaccount"
	"github.com/jackuait/wisp-deck/internal/models"
)

// SessionPill is the current agent/account identity shown in the ledger footer.
type SessionPill struct {
	Label string
	Color int
}

// SessionContext is the existing key=value relaunch context plus its resolved
// display identity. Unknown keys remain forward-compatible and are ignored.
type SessionContext struct {
	RelaunchFile string
	Tool         string
	ToolCommand  string
	Settings     string
	Filter       string
	ProjectDir   string
	AccountsDir  string
	Pointer      string
	List         string
	Colors       string
	DefaultLabel string
	Tools        []string
	Pill         *SessionPill
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
}

// NewSessionSource creates a session-context loader.
func NewSessionSource(runner ProcessRunner) *SessionSource {
	return &SessionSource{runner: runner}
}

// Load parses the context and resolves the pill only when another account or
// agent is available to switch to.
func (s *SessionSource) Load(ctx context.Context, path string) (SessionContext, error) {
	result, err := ParseSessionContext(path)
	if err != nil {
		return SessionContext{}, err
	}
	accounts := claudeaccount.Load(result.List)
	if len(accounts) == 0 && len(result.Tools) < 2 {
		return result, nil
	}
	if result.Tool != "claude" {
		result.Pill = &SessionPill{Label: models.DisplayName(result.Tool), Color: toolAccent(result.Tool)}
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
