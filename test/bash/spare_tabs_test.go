package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spareTabsMockTmux returns a mock `tmux` body that:
//   - prints GT_WINCOUNT lines for `list-windows` (defaults to 1),
//   - prints a fixed dir for `show` (@gt_dir lookups),
//   - records the full arg string of every other invocation to GT_REC.
//
// It understands both `-L <label> <sub>` and `-S <path> <sub>` addressing.
const spareTabsMockTmux = `#!/bin/bash
sub="$1"
if [ "$1" = "-L" ] || [ "$1" = "-S" ]; then sub="$3"; fi
case "$sub" in
  list-windows) n="${GT_WINCOUNT:-1}"; i=0; while [ "$i" -lt "$n" ]; do echo "@$i"; i=$((i+1)); done ;;
  show) echo "/proj/dir" ;;
  *) printf '%s\n' "$*" >> "$GT_REC" ;;
esac
exit 0
`

// spare_tabs_socket must produce a deterministic, filesystem-safe -L label.
func TestSpareTabs_socket_sanitizes(t *testing.T) {
	out, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_socket",
		[]string{"dev ghost/tab.1"}, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	if got != "gtspare_dev_ghost_tab_1" {
		t.Errorf("socket label = %q, want %q", got, "gtspare_dev_ghost_tab_1")
	}
	// Only [A-Za-z0-9_-] allowed in the result.
	for _, r := range got {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			t.Fatalf("label %q contains unsafe rune %q", got, string(r))
		}
	}
}

// The inner tmux config must enable the features the tab bar relies on, embed
// the project label, and expose the three clickable user-ranges.
func TestSpareTabs_config_core(t *testing.T) {
	out, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_config",
		[]string{"ghost-tab", "/proj/dir", "/abs/lib/spare-tabs.sh", "gtspare_x"}, nil)
	assertExitCode(t, code, 0)

	for _, want := range []string{
		"set -g mouse on",          // clicks reach the inner tmux
		"status-position top",      // tab bar pinned to the top of the pane
		"exit-unattached on",       // inner server dies with its client pane
		"remain-on-exit on",        // pane-died hook can guard the last tab
		"range=user|new",           // [ + ] add button
		"range=user|sel:",          // click a tab to select it
		"range=user|close:",        // per-tab close button
		"@gt_dir",                  // cwd for new/respawned tabs
		"ghost-tab",                // first tab shows the project name
		"/abs/lib/spare-tabs.sh",   // dispatch sources the lib
		"spare_tabs_dispatch",      // mouse handler routes through the helper
		"pane-died",                // typing `exit` is handled
	} {
		assertContains(t, out, want)
	}
}

// The tab bar must sit flush against the pane's left edge — no leading
// status-left padding before the first tab.
func TestSpareTabs_config_flush_left(t *testing.T) {
	out, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_config",
		[]string{"ghost-tab", "/proj/dir", "/abs/lib/spare-tabs.sh", "gtspare_x"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, `set -g status-left ""`)
}

// The launch command must escape the outer $TMUX guard (tmux refuses to nest
// otherwise) and fall back to a plain shell if tmux is unavailable.
func TestSpareTabs_launch_cmd(t *testing.T) {
	out, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_launch_cmd",
		[]string{"gtspare_x", "/run/spare.conf", "/proj/dir"}, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	for _, want := range []string{
		"env -u TMUX",      // shed the parent tmux env so nesting is allowed
		"new-session",
		"gtspare_x",
		"/run/spare.conf",
		"/proj/dir",
		"|| exec bash",     // graceful fallback
	} {
		assertContains(t, got, want)
	}
}

// With a zdotdir argument the launch command must pin ZDOTDIR onto the inner
// tmux env (so the spare shell and every tab it spawns load the minimal prompt).
func TestSpareTabs_launch_cmd_with_zdotdir(t *testing.T) {
	out, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_launch_cmd",
		[]string{"gtspare_x", "/run/spare.conf", "/proj/dir", "/run/zd"}, nil)
	assertExitCode(t, code, 0)
	got := strings.TrimSpace(out)
	for _, want := range []string{
		"env -u TMUX", // still sheds the parent tmux env
		"ZDOTDIR=",    // points zsh at our throwaway config dir
		"/run/zd",
		"new-session",
		"|| exec bash",
	} {
		assertContains(t, got, want)
	}
}

// For a zsh login shell, spare_prompt_zdotdir builds a throwaway ZDOTDIR that
// sources the user's real config then pins a cwd-only prompt; it echoes the path.
func TestSpareTabs_prompt_zdotdir_zsh(t *testing.T) {
	dir := t.TempDir()
	share := filepath.Join(dir, "share")
	real := filepath.Join(dir, "home")
	out, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_prompt_zdotdir",
		[]string{share, "dev-ghost-tab", "/bin/zsh", real}, nil)
	assertExitCode(t, code, 0)
	target := strings.TrimSpace(out)
	if target == "" {
		t.Fatal("expected a ZDOTDIR path for a zsh login shell, got empty")
	}

	zshrc, err := os.ReadFile(filepath.Join(target, ".zshrc"))
	if err != nil {
		t.Fatalf("reading generated .zshrc: %v", err)
	}
	assertContains(t, string(zshrc), real+"/.zshrc") // sources the real config
	assertContains(t, string(zshrc), "PROMPT='%1~ %# '") // then overrides the prompt

	zshenv, err := os.ReadFile(filepath.Join(target, ".zshenv"))
	if err != nil {
		t.Fatalf("reading generated .zshenv: %v", err)
	}
	assertContains(t, string(zshenv), real+"/.zshenv")        // sources the real .zshenv
	assertContains(t, string(zshenv), `ZDOTDIR="`+target+`"`) // re-pins so our .zshrc wins
}

// For any non-zsh login shell, spare_prompt_zdotdir is a no-op: nothing written,
// empty output (bash spare shells keep their default prompt).
func TestSpareTabs_prompt_zdotdir_nonzsh(t *testing.T) {
	dir := t.TempDir()
	share := filepath.Join(dir, "share")
	out, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_prompt_zdotdir",
		[]string{share, "dev-ghost-tab", "/bin/bash", filepath.Join(dir, "home")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("non-zsh shell should yield no ZDOTDIR, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(share, "spare-zdotdir-dev-ghost-tab")); !os.IsNotExist(err) {
		t.Errorf("non-zsh shell should not create a zdotdir, but it exists")
	}
}

// spare_tabs_close never empties the bar: the last remaining tab is respawned
// (fresh shell) rather than killed.
func TestSpareTabs_close_last_window_respawns(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", spareTabsMockTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec, "GT_WINCOUNT=1")

	_, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_close",
		[]string{"gtspare_x", "@7"}, env)
	assertExitCode(t, code, 0)

	data, _ := os.ReadFile(rec)
	got := string(data)
	assertContains(t, got, "respawn-pane")
	assertContains(t, got, "@7")
	assertNotContains(t, got, "kill-window")
}

// With more than one tab, closing one kills just that window.
func TestSpareTabs_close_nonlast_kills(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", spareTabsMockTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec, "GT_WINCOUNT=3")

	_, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_close",
		[]string{"gtspare_x", "@2"}, env)
	assertExitCode(t, code, 0)

	data, _ := os.ReadFile(rec)
	got := string(data)
	assertContains(t, got, "kill-window")
	assertContains(t, got, "@2")
	assertNotContains(t, got, "respawn-pane")
}

// spare_tabs_dispatch routes a click's user-range tag to the right action.
func TestSpareTabs_dispatch_routes(t *testing.T) {
	cases := []struct {
		name string
		rng  string
		want string
	}{
		{"add", "new", "new-window"},
		{"select", "sel:@4", "select-window"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			rec := filepath.Join(dir, "rec")
			binDir := mockCommand(t, dir, "tmux", spareTabsMockTmux)
			env := buildEnv(t, []string{binDir}, "GT_REC="+rec, "GT_WINCOUNT=2")
			_, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_dispatch",
				[]string{"gtspare_x", c.rng}, env)
			assertExitCode(t, code, 0)
			data, _ := os.ReadFile(rec)
			assertContains(t, string(data), c.want)
		})
	}
}

// spare_tabs_cleanup tears down the detached inner tmux server.
func TestSpareTabs_cleanup_kills_server(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", spareTabsMockTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)

	_, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_cleanup",
		[]string{"gtspare_x"}, env)
	assertExitCode(t, code, 0)

	data, _ := os.ReadFile(rec)
	got := string(data)
	assertContains(t, got, "kill-server")
	assertContains(t, got, "gtspare_x")
}

// cleanup_tmux_session must also reap the spare pane's inner tmux server when
// lib/spare-tabs.sh is loaded (the inner server detaches and would otherwise
// leak past window close).
func TestCleanupTmuxSession_reaps_spare_server(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", spareTabsMockTmux)
	env := buildEnv(t, []string{binDir}, "GT_REC="+rec)

	root := projectRoot(t)
	script := "source " + filepath.Join(root, "lib/process.sh") +
		" && source " + filepath.Join(root, "lib/spare-tabs.sh") +
		" && source " + filepath.Join(root, "lib/tmux-session.sh") +
		" && cleanup_tmux_session dev-ghost-tab 99999 tmux"
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	_ = out

	data, _ := os.ReadFile(rec)
	got := string(data)
	assertContains(t, got, "kill-server")
	assertContains(t, got, "gtspare_dev-ghost-tab")
}
