package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// subscriptionRelaunchCtx writes a relaunch-context file that also carries the
// subscription (claude-config) keys the backend switch needs.
func subscriptionRelaunchCtx(t *testing.T, dir string) string {
	t.Helper()
	return writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude", "tool_cmd=claude",
		"settings=", "settings_source=", "filter=", "project_dir=/proj",
		"accounts_dir=" + filepath.Join(dir, "claude-accounts"),
		"pointer=" + filepath.Join(dir, "claude-account"),
		"list=" + filepath.Join(dir, "claude-accounts.list"),
		"colors=" + filepath.Join(dir, "claude-account-colors"),
		"default_label=" + filepath.Join(dir, "claude-account-default-label"),
		"config_pointer=" + filepath.Join(dir, "claude-config"),
		"configs_dir=" + filepath.Join(dir, "claude-configs"),
		"configs_list=" + filepath.Join(dir, "claude-configs.list"),
		"",
	}, "\n"))
}

// switcherResultMockTmux is switcherMockTmux extended to answer the per-session
// subscription stamp (WISP_DECK_CLAUDE_CONFIG) and to write an arbitrary result
// line (a dir, "config:<file>", "tool:<name>", "" for Default, "CANCEL").
func switcherResultMockTmux(t *testing.T, dir, sessionAcct, sessionConfig, resultLine, rec string) string {
	t.Helper()
	return mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_ACCOUNT" ]; then printf 'WISP_DECK_CLAUDE_ACCOUNT=%s\n'; exit 0; fi
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_CONFIG" ]; then printf 'WISP_DECK_CLAUDE_CONFIG=%s\n'; exit 0; fi
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_SESSION\n'; exit 0; fi
if [ "$1" = "list-panes" ]; then printf '%%s\n' "%%1 1"; exit 0; fi
if [ "$1" = "display-message" ]; then printf '%%s\n' "80 24"; exit 0; fi
if [ "$1" = "display-popup" ]; then
  [ "%s" = "CANCEL" ] && exit 0
  rf=$(printf '%%s' "$*" | sed -n 's/.*--result-file \([^ ]*\).*/\1/p')
  [ -n "$rf" ] && printf '%s\n' > "$rf"
  exit 0
fi
printf '%%s\n' "$*" >> %q`,
		sessionAcct, sessionConfig, resultLine, resultLine, rec))
}

// claude_config_name resolves a config filename to its display name, falling
// back to "Standard Claude" for empty and to the bare filename for a stale one.
func TestClaudeConfigName_resolves_display_name(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\nOpenAI / ChatGPT:gpt.json\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf(`claude_config_name gpt.json %q`, list)), nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "OpenAI / ChatGPT" {
		t.Fatalf("name = %q, want 'OpenAI / ChatGPT'", out)
	}
	out, _ = runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf(`claude_config_name "" %q`, list)), nil)
	if strings.TrimSpace(out) != "Standard Claude" {
		t.Fatalf("empty = %q, want 'Standard Claude'", out)
	}
}

// When a subscription is active for the pane, the ledger pill shows the
// subscription name (in the subscription accent), not the account — the
// account is overridden while a backend runs.
func TestPillCurrent_shows_subscription_when_active(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:1\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	configsList := writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\n")
	configPointer := writeTempFile(t, dir, "claude-config", "glm.json\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`pill_current claude %q %q %q %q "" %q %q`,
		pointer, list, defLabel, colors, configPointer, configsList)), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "GLM\t")
	assertNotContains(t, out, "Work")
}

// With no subscription active (standard Claude), the pill keeps showing the
// account, unchanged.
func TestPillCurrent_shows_account_when_standard(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:1\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	configsList := writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\n")
	configPointer := filepath.Join(dir, "claude-config") // absent = standard
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`pill_current claude %q %q %q %q "" %q %q`,
		pointer, list, defLabel, colors, configPointer, configsList)), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Work\t")
}

// A session launched before the subscription feature carries no config_pointer
// in its relaunch context, so the per-session config machinery is unavailable.
// The pane's launch-frozen WISP_DECK_PLAN — the SAME label the ledger header
// already shows — is the fallback: the pill must show the subscription from it
// instead of lying with the account, so the header and pill agree without a
// full relaunch.
func TestPillCurrent_legacy_context_falls_back_to_plan(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:1\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	// Legacy relaunch context: config_pointer/configs_list are empty.
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`pill_current claude %q %q %q %q "" "" ""`,
		pointer, list, defLabel, colors)),
		buildEnv(t, nil, "WISP_DECK_PLAN=OpenAI / ChatGPT"))
	assertExitCode(t, code, 0)
	assertContains(t, out, "OpenAI / ChatGPT\t")
	assertNotContains(t, out, "Work")
}

// Legacy context with WISP_DECK_PLAN unset or "Standard Claude" (no subscription
// at launch) keeps the account pill — "Standard Claude" is not a subscription.
func TestPillCurrent_legacy_context_standard_plan_shows_account(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:1\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`pill_current claude %q %q %q %q "" "" ""`,
		pointer, list, defLabel, colors)),
		buildEnv(t, nil, "WISP_DECK_PLAN=Standard Claude"))
	assertExitCode(t, code, 0)
	assertContains(t, out, "Work\t")
	assertNotContains(t, out, "Standard Claude")
}

// When the per-session machinery IS present (fresh session), it is authoritative
// and the stale launch-time WISP_DECK_PLAN must NOT leak through: a mid-session
// switch to standard stamps WISP_DECK_CLAUDE_CONFIG empty, and the pill must show
// the account even though WISP_DECK_PLAN still holds the launch subscription.
func TestPillCurrent_session_stamp_wins_over_stale_plan(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:1\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	configsList := writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\n")
	configPointer := writeTempFile(t, dir, "claude-config", "glm.json\n")
	// tmux stamps the per-session config empty (switched to standard mid-session).
	binDir := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_CONFIG" ]; then printf 'WISP_DECK_CLAUDE_CONFIG=\n'; exit 0; fi
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_ACCOUNT" ]; then printf -- '-WISP_DECK_CLAUDE_ACCOUNT\n'; exit 0; fi
exit 0`)
	tmuxPath := filepath.Join(binDir, "tmux")
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`pill_current claude %q %q %q %q %q %q %q`,
		pointer, list, defLabel, colors, tmuxPath, configPointer, configsList)),
		buildEnv(t, []string{binDir}, "WISP_DECK_PLAN=OpenAI / ChatGPT"))
	assertExitCode(t, code, 0)
	assertContains(t, out, "Work\t")
	assertNotContains(t, out, "OpenAI / ChatGPT")
}

// The ledger must feed the pill the config context so the pill can show the
// active subscription.
func TestCompactView_passes_config_context_to_pill_current(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "lib", "compact-view.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "$_rc_config_pointer") || !strings.Contains(string(data), "$_rc_configs_list") {
		t.Fatal("compact-view.sh must pass the config context to pill_current")
	}
}

// A configured subscription makes the switch pill reachable even with a single
// login and no other agent — otherwise the user could never open the popup to
// change backends.
func TestAccountPillEnabled_shows_with_subscription_single_account(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "# only comments\n")
	configsList := writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\n")
	relaunch := writeTempFile(t, dir, "relaunch", "tool=claude\nconfigs_list="+configsList+"\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("account_pill_enabled %q %q", relaunch, list)), nil)
	if code != 0 {
		t.Fatalf("expected pill enabled with a configured subscription: %s", out)
	}
}

// wrapper.sh must stamp the launch subscription into the tmux session env so
// the switcher's active dot marks THIS pane's backend even after another session
// flips the global config pointer (mirrors WISP_DECK_CLAUDE_ACCOUNT).
func TestWrapper_stamps_session_config_on_new_session(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "new-session") && strings.Contains(line, "WISP_DECK_CLAUDE_CONFIG=") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("wrapper.sh must pass WISP_DECK_CLAUDE_CONFIG to tmux new-session via -e")
	}
}

// The per-session subscription stamp (WISP_DECK_CLAUDE_CONFIG) wins over the
// global config pointer, mirroring how WISP_DECK_CLAUDE_ACCOUNT works: another
// session flipping the pointer must not change what THIS pane's dot marks.
func TestCurrentSessionConfig_stamped_env_wins(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_CONFIG" ]; then
  printf 'WISP_DECK_CLAUDE_CONFIG=glm.json\n'; exit 0
fi`)
	env := buildEnv(t, []string{bin})
	pointer := writeTempFile(t, dir, "claude-config", "mimo.json\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("current_session_config tmux %q", pointer)), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "glm.json" {
		t.Fatalf("stamped config must win, got %q", out)
	}
}

// An unstamped session (pre-stamp launch) falls back to the global pointer.
func TestCurrentSessionConfig_unstamped_falls_back_to_pointer(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "tmux", `
if [ "$1" = "show-environment" ]; then printf -- '-WISP_DECK_CLAUDE_CONFIG\n'; exit 0; fi`)
	env := buildEnv(t, []string{bin})
	pointer := writeTempFile(t, dir, "claude-config", "mimo.json\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("current_session_config tmux %q", pointer)), env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "mimo.json" {
		t.Fatalf("unstamped session must fall back to pointer, got %q", out)
	}
}

// The subscription-rows flags (--active-config) are only safe on a binary that
// advertises them; an older binary would reject them and stop the switcher.
func TestSwitcherSupportsSubscriptionRows_ok_when_help_lists_flag(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "wisp-deck-tui",
		`printf 'claude-account-switch\n  --active-config string\n  --result-file string\n'`)
	env := buildEnv(t, []string{bin})
	_, code := runBashSnippet(t, accountSwitchSnippet(t, "switcher_supports_subscription_rows"), env)
	assertExitCode(t, code, 0)
}

func TestSwitcherSupportsSubscriptionRows_legacy_when_flag_absent(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "wisp-deck-tui",
		`printf 'claude-account-switch\n  --result-file string\n  --tools string\n'`)
	env := buildEnv(t, []string{bin})
	_, code := runBashSnippet(t, accountSwitchSnippet(t, "switcher_supports_subscription_rows"), env)
	if code == 0 {
		t.Fatalf("expected legacy (non-zero) when --active-config absent")
	}
}

// write_relaunch_context derives the subscription paths from cfg_root, just as
// it does the account paths — the switcher needs them to list backends.
func TestWriteRelaunchContext_writes_config_keys(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "relaunch")
	cfg := filepath.Join(dir, "cfg")
	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`write_relaunch_context %q claude claude "" "" "/proj" %q`, out, cfg)), nil)
	assertExitCode(t, code, 0)
	body, _ := runBashSnippet(t, fmt.Sprintf("cat %q", out), nil)
	assertContains(t, body, "config_pointer="+filepath.Join(cfg, "claude-config"))
	assertContains(t, body, "configs_dir="+filepath.Join(cfg, "claude-configs"))
	assertContains(t, body, "configs_list="+filepath.Join(cfg, "claude-configs.list"))
}

func TestReadRelaunchCtx_reads_config_keys(t *testing.T) {
	dir := t.TempDir()
	ctx := writeTempFile(t, dir, "relaunch", strings.Join([]string{
		"tool=claude",
		"config_pointer=" + filepath.Join(dir, "claude-config"),
		"configs_dir=" + filepath.Join(dir, "claude-configs"),
		"configs_list=" + filepath.Join(dir, "claude-configs.list"),
		"",
	}, "\n"))
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`_rc_config_pointer="" _rc_configs_dir="" _rc_configs_list=""
_read_relaunch_ctx %q
printf '%%s|%%s|%%s\n' "$_rc_config_pointer" "$_rc_configs_dir" "$_rc_configs_list"`, ctx)), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, filepath.Join(dir, "claude-config")+"|"+filepath.Join(dir, "claude-configs")+"|"+filepath.Join(dir, "claude-configs.list"))
}

// _set_relaunch_kv rewrites an existing key in place and appends a missing one,
// leaving every other line untouched — that is how a subscription switch swaps
// the pane's settings source before the relaunch re-reads it.
func TestSetRelaunchKv_replaces_existing_and_appends_missing(t *testing.T) {
	dir := t.TempDir()
	f := writeTempFile(t, dir, "relaunch", "tool=claude\nsettings_source=/old.json\nfilter=\n")
	_, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`_set_relaunch_kv %q settings_source /new.json; _set_relaunch_kv %q brand new`, f, f)), nil)
	assertExitCode(t, code, 0)
	body, _ := runBashSnippet(t, fmt.Sprintf("cat %q", f), nil)
	assertContains(t, body, "settings_source=/new.json")
	assertNotContains(t, body, "settings_source=/old.json")
	assertContains(t, body, "brand=new")
	assertContains(t, body, "tool=claude")
}

// With a subscription configured and a capable binary, the popup command must
// carry the backend flags so the popup can render and mark them.
func TestOpenAccountSwitcher_passes_subscription_flags(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	writeTempFile(t, filepath.Join(dir, "claude-configs"), "glm.json",
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"zhipu","ANTHROPIC_AUTH_TOKEN":"sk-x"}}`)
	writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\n")
	rec := filepath.Join(dir, "popup.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`
if [ "$1" = "show-environment" ] && [ "$2" = "WISP_DECK_CLAUDE_CONFIG" ]; then printf 'WISP_DECK_CLAUDE_CONFIG=\n'; exit 0; fi
if [ "$1" = "show-environment" ]; then printf 'WISP_DECK_CLAUDE_ACCOUNT=work\n'; exit 0; fi
if [ "$1" = "display-popup" ]; then printf '%%s\n' "$*" >> %q; exit 0; fi
exit 0`, rec))
	// A capable binary: its help advertises --active-config (and the earlier
	// flags), so every switcher probe reports ok.
	mockCommand(t, dir, "wisp-deck-tui",
		`printf 'claude-account-switch\n  --result-file string\n  --tools string\n  --active-config string\n'`)
	relaunch := subscriptionRelaunchCtx(t, dir)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "--configs ")
	assertContains(t, logOut, "--configs-dir ")
	assertContains(t, logOut, "--active-config")
}

// Choosing a subscription relaunches the pane on that backend: the global
// pointer flips, the pane's per-session stamp follows, and claude respawns.
func TestOpenAccountSwitcher_config_result_switches_backend(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "work"), ".keep", "")
	writeTempFile(t, filepath.Join(dir, "claude-configs"), "glm.json",
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"zhipu","ANTHROPIC_AUTH_TOKEN":"sk-x"}}`)
	writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\n")
	configPointer := filepath.Join(dir, "claude-config") // absent = standard
	rec := filepath.Join(dir, "tmux.log")
	bin := switcherResultMockTmux(t, dir, "", "", "config:glm.json", rec)
	relaunch := subscriptionRelaunchCtx(t, dir)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	ptr, _ := runBashSnippet(t, fmt.Sprintf("cat %q", configPointer), nil)
	if strings.TrimSpace(ptr) != "glm.json" {
		t.Fatalf("config pointer = %q, want glm.json", ptr)
	}
	assertContains(t, logOut, "WISP_DECK_CLAUDE_CONFIG glm.json")
}

// Picking an account while a subscription is active returns the pane to the
// standard backend: the config pointer is reset and claude respawns.
func TestOpenAccountSwitcher_account_pick_resets_subscription(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	writeTempFile(t, filepath.Join(dir, "claude-accounts", "work"), ".keep", "")
	writeTempFile(t, filepath.Join(dir, "claude-configs"), "glm.json",
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"zhipu","ANTHROPIC_AUTH_TOKEN":"sk-x"}}`)
	writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\n")
	configPointer := writeTempFile(t, dir, "claude-config", "glm.json\n")
	rec := filepath.Join(dir, "tmux.log")
	// Pane runs account "work" on the glm.json backend; user picks the Default account.
	bin := switcherResultMockTmux(t, dir, "work", "glm.json", "", rec)
	relaunch := subscriptionRelaunchCtx(t, dir)
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("open_account_switcher tmux %q", relaunch)), env)
	assertExitCode(t, code, 0)
	logOut, _ := runBashSnippet(t, fmt.Sprintf("cat %q", rec), nil)
	assertContains(t, logOut, "respawn-pane")
	if _, err := os.Stat(configPointer); !os.IsNotExist(err) {
		t.Fatalf("account pick must reset the subscription to standard (pointer removed)")
	}
}

// The subscription pill wears the subscription's own persistent color from
// claude-config-colors (next to the configs list), matching the switcher rows
// and the statusline usage bars.
func TestPillCurrent_subscription_wears_config_color(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:1\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	configsList := writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\n")
	configPointer := writeTempFile(t, dir, "claude-config", "glm.json\n")
	writeTempFile(t, dir, "claude-config-colors", "glm.json:205\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`pill_current claude %q %q %q %q "" %q %q`,
		pointer, list, defLabel, colors, configPointer, configsList)), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "GLM\t205")
}

// The pill states the identity KIND with its glyph: the subscription spark (✦)
// while a backend runs, the account person (󰀄) for a login — third output
// field, consumed by account_pill via the compact view.
func TestPillCurrent_subscription_emits_spark_glyph(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:1\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	configsList := writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\n")
	configPointer := writeTempFile(t, dir, "claude-config", "glm.json\n")
	writeTempFile(t, dir, "claude-config-colors", "glm.json:205\n")
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`pill_current claude %q %q %q %q "" %q %q`,
		pointer, list, defLabel, colors, configPointer, configsList)), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "GLM\t205\t✦")
}

func TestPillCurrent_account_emits_person_glyph(t *testing.T) {
	dir := t.TempDir()
	list := writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	pointer := writeTempFile(t, dir, "claude-account", "work\n")
	colors := writeTempFile(t, dir, "claude-account-colors", "work:1\n")
	defLabel := filepath.Join(dir, "claude-account-default-label")
	configsList := writeTempFile(t, dir, "claude-configs.list", "GLM:glm.json\n")
	configPointer := filepath.Join(dir, "claude-config") // absent = standard
	out, code := runBashSnippet(t, accountSwitchSnippet(t, fmt.Sprintf(
		`pill_current claude %q %q %q %q "" %q %q`,
		pointer, list, defLabel, colors, configPointer, configsList)), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Work\t1\t\U000f0004")
}

// account_pill renders whatever glyph it is handed (the spark for a
// subscription); no glyph arg keeps the person for older callers.
func TestAccountPill_renders_given_glyph(t *testing.T) {
	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		`account_pill "GLM" 205 0 "✦"`), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "✦ GLM")
	assertNotContains(t, out, "\U000f0004")
	out, code = runBashSnippet(t, accountSwitchSnippet(t,
		`account_pill "Work" 1`), nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\U000f0004 Work")
}
