package main

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/allin"
	"github.com/jackuait/wisp-deck/internal/gptbridge"
)

func init() {
	rootCmd.AddCommand(newClaudeAllInCommandWithExit(runClaudeRolefixChild, os.Exit))
}

// allinBridge is what one claude-allin launch owns on behalf of the OpenAI /
// ChatGPT subscription: the endpoint seam the router resolves against, plus the
// shutdown the launch must run. Keeping the Close half out of
// allin.ChatGPTBridge is deliberate — nothing that routes a request should be
// able to shut a bridge down.
type allinBridge interface {
	allin.ChatGPTBridge
	Close()
}

func newClaudeAllInCommand(run claudeRolefixRunner) *cobra.Command {
	return newClaudeAllInCommandWithExit(run, func(int) {})
}

func newClaudeAllInCommandWithExit(run claudeRolefixRunner, exit func(int)) *cobra.Command {
	return newClaudeAllInCommandWithBridge(run, exit, newChatGPTBridge)
}

// newChatGPTBridge is the production bridge: lazy, so a session that never
// picks a GPT row never starts a Codex app-server.
func newChatGPTBridge(codexPath string) allinBridge {
	return gptbridge.NewChatGPTBridge(gptbridge.ChatGPTBridgeOptions{
		CodexPath: codexPath, ClientVersion: Version,
	})
}

// newClaudeAllInCommandWithBridge wraps one Claude launch in a loopback router
// that sends each turn to the subscription its picker row names. The launch
// and exit-code contract itself is runLoopbackWrappedLaunch, shared with
// claude-rolefix so the two never drift apart.
func newClaudeAllInCommandWithBridge(
	run claudeRolefixRunner, exit func(int), newBridge func(codexPath string) allinBridge,
) *cobra.Command {
	var settingsPath, codexPath string
	var env allin.Env
	command := &cobra.Command{
		Use:          "claude-allin --settings PATH -- COMMAND [ARG...]",
		Short:        "Route one Claude launch across every configured subscription",
		Hidden:       true,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, argv []string) error {
			// Built unconditionally and started by nothing: the bridge only
			// execs Codex when a ChatGPT row actually resolves, so a session
			// that never picks one pays for this exactly nothing.
			bridge := newBridge(sessionCodexPath(codexPath))
			resolver := allin.NewResolver(env)
			resolver.Bridge = bridge
			newHandler := func(upstream string) http.Handler {
				return allin.NewHandler(resolver, upstream)
			}
			return runLoopbackWrappedLaunch(settingsPath, argv, run, exit, newHandler, bridge.Close)
		},
	}
	flags := command.Flags()
	flags.StringVar(&settingsPath, "settings", "", "launch settings overlay to point at the router")
	flags.StringVar(&codexPath, "codex", "", "absolute Codex executable path (defaults to WISP_DECK_CODEX_CMD)")
	flags.StringVar(&env.AccountsList, "accounts-list", "", "name:dir list of Claude logins")
	flags.StringVar(&env.AccountsDir, "accounts-dir", "", "directory holding each login's config dir")
	flags.StringVar(&env.ConfigsList, "configs-list", "", "name:file list of subscription profiles")
	flags.StringVar(&env.ConfigsDir, "configs-dir", "", "directory holding the profile settings files")
	// Resolve never reads this — it only addresses credentials, and the label
	// is display-only. Plumbed anyway so this Env stays shaped the same as
	// every other construction site (ensure-allin, the TUI's own mutations).
	flags.StringVar(&env.DefaultLabelFile, "default-label-file", "",
		"file holding the implicit Default login's custom tag (unused here; kept for parity)")
	return command
}

// sessionCodexPath resolves the Codex executable the ChatGPT bridge will exec.
//
// It comes from the session environment rather than a flag on the launch chain:
// wrapper.sh already stamps WISP_DECK_CODEX_CMD into the tmux session env (and
// lib/tab-view.sh re-exports it for a new tab), so every process in the pane
// inherits it — including this one, however deep the launch chain nests. The
// flag is the override a test uses.
//
// A relative value is dropped rather than resolved. It would otherwise exec
// against whatever directory the pane happens to sit in; reported as absent it
// becomes the bridge's own deterministic 400 naming Codex. resolve_agent_cmd
// only ever writes an absolute path or nothing, so this drops no real setup.
func sessionCodexPath(override string) string {
	path := override
	if path == "" {
		path = os.Getenv("WISP_DECK_CODEX_CMD")
	}
	if !filepath.IsAbs(path) {
		return ""
	}
	return path
}
