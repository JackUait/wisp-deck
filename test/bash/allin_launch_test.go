package bash_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// A settings file the All-In profile wrote carries wisp/acct.* or wisp/cfg.*
// picker rows (the exact shapes internal/allin/roster.go writes) — that is
// the only signal the launch chain has that a profile needs the router,
// since the display name can be renamed out from under it.
const allInPickerRow = `{"modelPicker":{"options":[{"model":"wisp/acct.default/claude-opus-5"}]}}`

const ordinaryProfile = `{"env":{"ANTHROPIC_MODEL":"claude-sonnet-5"}}`

// bareWispSubstring contains the literal substring "wisp/" but not as a
// wisp/acct.* or wisp/cfg.* picker row — a statusline command living under a
// directory literally named "wisp". An unanchored match would mistake this
// for an All-In profile.
const bareWispSubstring = `{"statusLine":{"command":"/Users/e/wisp/bin/statusline"}}`

// reconstructArgv re-parses a printed argv prefix through a real bash the way
// the launch-chain call site does: the string is spliced into a larger
// command line and re-parsed. Each argument capture() receives is printed on
// its own line, so a value that was not properly %q-quoted (e.g. a path
// containing a space) shows up split across two lines instead of intact on
// one, and every distinct token can be checked for by exact line match.
func reconstructArgv(t *testing.T, printed string) []string {
	t.Helper()
	script := "capture() { printf '%s\\n' \"$@\"; }\ncapture " + strings.TrimSpace(printed)
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func assertArgvHasToken(t *testing.T, argv []string, want string) {
	t.Helper()
	for _, tok := range argv {
		if tok == want {
			return
		}
	}
	t.Errorf("expected argv to contain exact token %q, got: %v", want, argv)
}

func TestClaudeLaunchWrapper_routes_a_wisp_picker_profile_through_the_router(t *testing.T) {
	dir := t.TempDir()
	settingsPath := writeTempFile(t, dir, "overlay.json", allInPickerRow)

	out, code := runBashFunc(t, "lib/tmux-session.sh", "gt_claude_launch_wrapper",
		[]string{settingsPath, ""}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "claude-allin")
	assertNotContains(t, out, "claude-rolefix")
}

func TestClaudeLaunchWrapper_keeps_featherless_on_the_role_repair_proxy(t *testing.T) {
	dir := t.TempDir()
	settingsPath := writeTempFile(t, dir, "overlay.json", ordinaryProfile)

	out, code := runBashFunc(t, "lib/tmux-session.sh", "gt_claude_launch_wrapper",
		[]string{settingsPath, "featherless"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "claude-rolefix")
	assertNotContains(t, out, "claude-allin")
}

func TestClaudeLaunchWrapper_wraps_nothing_for_an_ordinary_profile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := writeTempFile(t, dir, "overlay.json", ordinaryProfile)

	out, code := runBashFunc(t, "lib/tmux-session.sh", "gt_claude_launch_wrapper",
		[]string{settingsPath, ""}, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "claude-allin")
	assertNotContains(t, out, "claude-rolefix")
}

// The match must stay anchored to wisp/acct. / wisp/cfg. — the overlay is a
// whole-object copy of the profile plus the user's own statusline command and
// any Featherless model id, so a bare "wisp/" substring appears there for
// reasons that have nothing to do with the router.
func TestClaudeLaunchWrapper_ignores_a_bare_wisp_substring_that_is_not_a_picker_row(t *testing.T) {
	dir := t.TempDir()
	settingsPath := writeTempFile(t, dir, "overlay.json", bareWispSubstring)

	t.Run("no provider marker wraps nothing", func(t *testing.T) {
		out, code := runBashFunc(t, "lib/tmux-session.sh", "gt_claude_launch_wrapper",
			[]string{settingsPath, ""}, nil)
		assertExitCode(t, code, 0)
		assertNotContains(t, out, "claude-allin")
		assertNotContains(t, out, "claude-rolefix")
	})

	// A false positive on the anchor would silently strip the role-repair
	// proxy here — the only thing that lets a Featherless pane call a tool.
	t.Run("featherless marker still gets the role repair proxy", func(t *testing.T) {
		out, code := runBashFunc(t, "lib/tmux-session.sh", "gt_claude_launch_wrapper",
			[]string{settingsPath, "featherless"}, nil)
		assertExitCode(t, code, 0)
		assertContains(t, out, "claude-rolefix")
		assertNotContains(t, out, "claude-allin")
	})
}

// The flag names and the trailing "--" terminator are the exact contract
// Task 6's cobra command expects (cmd/wisp-deck-tui/claude_allin.go). Tests
// above only assert on the "claude-allin" substring and on path VALUES; a
// typo'd flag name or a dropped terminator passes all of them and fails only
// at real launch time.
func TestClaudeLaunchWrapper_all_in_argv_carries_every_flag_and_terminator(t *testing.T) {
	dir := t.TempDir()
	settingsPath := writeTempFile(t, dir, "overlay.json", allInPickerRow)

	out, code := runBashFunc(t, "lib/tmux-session.sh", "gt_claude_launch_wrapper",
		[]string{settingsPath, ""}, nil)
	assertExitCode(t, code, 0)

	argv := reconstructArgv(t, out)
	for _, want := range []string{
		"wisp-deck-tui", "claude-allin",
		"--settings", "--accounts-list", "--accounts-dir",
		"--configs-list", "--configs-dir", "--",
	} {
		assertArgvHasToken(t, argv, want)
	}
}

// The printed argv embeds the config root's four roster paths and the
// settings path via %q, exactly like every other launch-chain quoting site.
// A directory containing a space must survive as ONE argument, not split.
func TestClaudeLaunchWrapper_quotes_a_settings_path_containing_a_space(t *testing.T) {
	dir := t.TempDir()
	spacedDir := filepath.Join(dir, "has space")
	settingsPath := writeTempFile(t, spacedDir, "overlay.json", allInPickerRow)

	out, code := runBashFunc(t, "lib/tmux-session.sh", "gt_claude_launch_wrapper",
		[]string{settingsPath, ""}, nil)
	assertExitCode(t, code, 0)

	argv := reconstructArgv(t, out)
	assertArgvHasToken(t, argv, settingsPath)
}

// The config root itself can contain a space too. It falls back to
// ${XDG_CONFIG_HOME:-$HOME/.config}/wisp-deck (matching wrapper.sh:450 and
// cmd/wisp-deck-tui/root.go), so this sets XDG_CONFIG_HOME, not an invented
// variable nothing else in the product reads.
func TestClaudeLaunchWrapper_quotes_a_config_root_containing_a_space(t *testing.T) {
	dir := t.TempDir()
	settingsPath := writeTempFile(t, dir, "overlay.json", allInPickerRow)
	xdgHome := filepath.Join(dir, "config root")
	configRoot := filepath.Join(xdgHome, "wisp-deck")

	env := buildEnv(t, nil, "XDG_CONFIG_HOME="+xdgHome)
	out, code := runBashFunc(t, "lib/tmux-session.sh", "gt_claude_launch_wrapper",
		[]string{settingsPath, ""}, env)
	assertExitCode(t, code, 0)

	argv := reconstructArgv(t, out)
	assertArgvHasToken(t, argv, filepath.Join(configRoot, "claude-accounts.list"))
	assertArgvHasToken(t, argv, filepath.Join(configRoot, "claude-accounts"))
	assertArgvHasToken(t, argv, filepath.Join(configRoot, "claude-configs.list"))
	assertArgvHasToken(t, argv, filepath.Join(configRoot, "claude-configs"))
}

// An All-In session serves ChatGPT as one picker row among many, through the
// router's own lazily started bridge. It must never ALSO be wrapped in
// claude-gpt-adapter, which owns a second app-server and points the child's
// env at its own loopback API — the settings overlay beats the environment, so
// the router would win the routing and the adapter's Codex process would be a
// pure leak for the life of the pane.
//
// The two branches are mutually exclusive today because the All-In profile's
// provider marker is "allin", never "openai-chatgpt". This is what goes red if
// something ever reports it as the latter.
func TestClaudeLaunch_never_stacks_the_gpt_adapter_on_the_all_in_router(t *testing.T) {
	dir := t.TempDir()
	settingsPath := writeTempFile(t, dir, "overlay.json", allInPickerRow)
	env := buildEnv(t, nil,
		"HOME=/home/tester",
		"WISP_DECK_CLAUDE_PROVIDER=allin",
		"WISP_DECK_CODEX_CMD=/opt/Codex App/codex",
		"WISP_DECK_CLAUDE_SETTINGS="+settingsPath,
		"WISP_DECK_RESUME=0",
		"WISP_DECK_RESUME_SESSION=",
	)
	out, code := runBashFunc(t, "lib/tmux-session.sh", "build_ai_launch_cmd",
		[]string{"claude", "claude"}, env)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if strings.Count(got, "claude-allin") != 1 {
		t.Fatalf("router count != 1: %q", got)
	}
	assertNotContains(t, got, "claude-gpt-adapter")
}

// The router now rewrites the profile itself when a model list changes, so it
// must read the same default-login tag bin/wisp-deck's ensure-allin does, or
// the rewrite relabels that login's rows "Default".
func TestClaudeLaunchWrapper_passes_the_default_label_file_to_the_router(t *testing.T) {
	dir := t.TempDir()
	settingsPath := writeTempFile(t, dir, "overlay.json", allInPickerRow)
	xdgHome := filepath.Join(dir, "xdg")

	env := buildEnv(t, nil, "XDG_CONFIG_HOME="+xdgHome)
	out, code := runBashFunc(t, "lib/tmux-session.sh", "gt_claude_launch_wrapper",
		[]string{settingsPath, ""}, env)
	assertExitCode(t, code, 0)

	argv := reconstructArgv(t, out)
	assertArgvHasToken(t, argv, "--default-label-file")
	assertArgvHasToken(t, argv, filepath.Join(xdgHome, "wisp-deck", "claude-account-default-label"))
}
