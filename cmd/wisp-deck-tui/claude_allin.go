package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/allin"
	"github.com/jackuait/wisp-deck/internal/claudeaccount"
	"github.com/jackuait/wisp-deck/internal/gptbridge"
	"github.com/jackuait/wisp-deck/internal/usage"
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
func newChatGPTBridge(codexPath string, lookup func() string) allinBridge {
	return gptbridge.NewChatGPTBridge(gptbridge.ChatGPTBridgeOptions{
		CodexPath: codexPath, ResolveCodexPath: lookup, ClientVersion: Version,
		ColdStarts: newGPTBridgeColdStartFuse(gptBridgeColdLog()),
	})
}

// newClaudeAllInCommandWithBridge wraps one Claude launch in a loopback router
// that sends each turn to the subscription its picker row names. The launch
// and exit-code contract itself is runLoopbackWrappedLaunch, shared with
// claude-rolefix so the two never drift apart.
func newClaudeAllInCommandWithBridge(
	run claudeRolefixRunner, exit func(int), newBridge func(codexPath string, lookup func() string) allinBridge,
) *cobra.Command {
	var settingsPath, codexPath, resumeSession string
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
			lookup := lookupCodexPath(filepath.Dir(env.AccountsList))
			codex := sessionCodexPath(codexPath)
			if codex == "" {
				codex = lookup()
			}
			bridge := newBridge(codex, lookup)
			resolver := allin.NewResolver(env)
			resolver.Bridge = bridge
			observe := func(http.Header) {}
			if currentHostEffectsDecision().Allowed {
				refresher := &allin.ModelRefresher{
					Env:        env,
					Client:     &http.Client{Timeout: 15 * time.Second},
					CodexCache: codexModelsCache(),
					CodexList:  codexModelLister(codex),
					Now:        time.Now,
					Ensure:     allin.EnsureProfileIfEligible,
				}
				observe = refresher.Observe
				home, _ := os.UserHomeDir()
				resolver.Reconcile = claudeaccount.GatedReconcile(claudeaccount.ReconcileOptions{
					ListFile:    env.AccountsList,
					AccountsDir: env.AccountsDir,
					EmailsFile:  filepath.Join(filepath.Dir(env.AccountsList), "claude-account-emails"),
					UsageDir:    filepath.Join(filepath.Dir(env.AccountsList), "account-usage"),
					HomeDir:     home,
					Store:       allin.KeychainLogins{},
				})
			}
			rows := allin.SessionRows{Dir: allin.SessionRowsDir(env.AccountsList)}
			// Claude Code resumes on the shared settings.json model, which
			// any pane's /model pick overwrites. ANTHROPIC_MODEL outranks it
			// and an in-session /model still outranks ANTHROPIC_MODEL.
			if row := rows.Lookup(resumeSession); row != "" {
				_ = os.Setenv("ANTHROPIC_MODEL", row)
			}
			fast := allin.FastRoute{
				Start:      allin.StartingRowFor(rows, resumeSession, allin.UserSettingsPath()),
				ConfigFast: env.FastModelFor,
			}
			if rows.Dir != "" {
				fast.Remember = func(session, model string) { _ = rows.Record(session, model) }
			}
			newHandler := func(upstream string) http.Handler {
				return allin.NewRoutingHandler(resolver, upstream, observe, fast)
			}
			return runLoopbackWrappedLaunch(settingsPath, argv, run, exit, newHandler, bridge.Close)
		},
	}
	flags := command.Flags()
	flags.StringVar(&settingsPath, "settings", "", "launch settings overlay to point at the router")
	flags.StringVar(&resumeSession, "resume-session", "",
		"Claude session id this launch resumes; puts it back on its own row")
	flags.StringVar(&codexPath, "codex", "", "absolute Codex executable path (defaults to WISP_DECK_CODEX_CMD)")
	flags.StringVar(&env.AccountsList, "accounts-list", "", "name:dir list of Claude logins")
	flags.StringVar(&env.AccountsDir, "accounts-dir", "", "directory holding each login's config dir")
	flags.StringVar(&env.ConfigsList, "configs-list", "", "name:file list of subscription profiles")
	flags.StringVar(&env.ConfigsDir, "configs-dir", "", "directory holding the profile settings files")
	// Resolve never reads this, but the model refresher's profile rewrite
	// does: without it the default login's rows lose the user's tag.
	flags.StringVar(&env.DefaultLabelFile, "default-label-file", "",
		"file holding the implicit Default login's custom tag")
	return command
}

// sessionCodexPath resolves the Codex executable the ChatGPT bridge will exec.
//
// It comes from the session environment rather than a flag on the launch chain:
// wrapper.sh already stamps WISP_DECK_CODEX_CMD into the tmux session env (and
// lib/tab-view.sh re-exports it for a new tab), so every process in the pane
// inherits it — including this one, however deep the launch chain nests. The
// flag is the override a test uses. An empty result is not final: see
// lookupCodexPath.
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

// lookupCodexPath finds Codex the way resolve_agent_cmd does when the session
// env names none: PATH, then the path setup cached in <configRoot>/codex-cmd.
// The env is empty when the tab opened while Codex was being reinstalled.
func lookupCodexPath(configRoot string) func() string {
	return func() string {
		if path, err := exec.LookPath("codex"); err == nil && filepath.IsAbs(path) {
			return path
		}
		raw, err := os.ReadFile(filepath.Join(configRoot, "codex-cmd"))
		if err != nil {
			return ""
		}
		path := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
		if !filepath.IsAbs(path) {
			return ""
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			return ""
		}
		return path
	}
}

// codexModelListTimeout bounds `codex debug models`, which fetches the live
// list (~0.7s warm). It runs in the background refresh, never on a turn.
const codexModelListTimeout = 30 * time.Second

// codexModelLister asks the bridge's own Codex which models it serves. The
// shared models_cache.json cannot answer that: the ChatGPT desktop app's
// bundled Codex and the app-servers of panes opened before an upgrade write
// it too, each with the shorter list their older client is offered.
func codexModelLister(codexPath string) func() ([]allin.Listed, error) {
	if codexPath == "" {
		return nil
	}
	return func() ([]allin.Listed, error) {
		ctx, cancel := context.WithTimeout(context.Background(), codexModelListTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, codexPath, "debug", "models").Output()
		if err != nil {
			return nil, err
		}
		models, _, err := allin.ParseCodexModels(out)
		return models, err
	}
}

// codexModelsCache is the list Codex fetches and caches for itself. Reading it
// starts no app-server, which would cost seconds cold.
func codexModelsCache() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(filepath.Dir(usage.CodexSessionsDir(home)), "models_cache.json")
}
