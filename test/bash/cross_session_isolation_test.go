package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A tmux command with no -t resolves to tmux's CURRENT session. From inside a
// pane that is the pane's own session, but the wrapper, its watchers and every
// run-shell script run outside any pane, where "current" is whichever session
// was used last — the tab the user is looking at, not the tab doing the work.
// These tests run the real code against a real, private tmux server holding
// two tabs, with the OTHER tab as the most recently used one, and assert that
// work done for one tab never lands on the other.

// isolationServer starts a private tmux server (its own TMUX_TMPDIR, so the
// user's real server is unreachable) and returns an env that talks only to it.
func isolationServer(t *testing.T) []string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	// A socket path must fit sun_path (104 bytes on macOS); t.TempDir() under
	// /var/folders does not reliably fit.
	tmpdir, err := os.MkdirTemp("/tmp", "wdiso")
	if err != nil {
		t.Fatal(err)
	}
	env := []string{}
	for _, e := range os.Environ() {
		// TMUX/TMUX_PANE would point tmux at the live server this test may be
		// running inside of.
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") ||
			strings.HasPrefix(e, "TMUX_TMPDIR=") || strings.HasPrefix(e, "WISP_DECK") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "TMUX_TMPDIR="+tmpdir)
	t.Cleanup(func() {
		sockets, _ := filepath.Glob(filepath.Join(tmpdir, "tmux-*", "*"))
		for _, s := range sockets {
			c := exec.Command("tmux", "-S", s, "kill-server")
			c.Env = env
			_ = c.Run()
		}
		_ = os.RemoveAll(tmpdir)
	})
	return env
}

func isoTmux(t *testing.T, env []string, args ...string) string {
	t.Helper()
	c := exec.Command("tmux", args...)
	c.Env = env
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// isoStubLib is a lib dir whose compact_view just idles, so a respawned ledger
// needs no git renderer. spare-tabs.sh is the real one.
func isoStubLib(t *testing.T) string {
	t.Helper()
	lib := t.TempDir()
	writeTempFile(t, lib, "compact-view.sh", "compact_view() { sleep 600; }\n")
	if err := os.Symlink(filepath.Join(projectRoot(t), "lib", "spare-tabs.sh"),
		filepath.Join(lib, "spare-tabs.sh")); err != nil {
		t.Fatal(err)
	}
	return lib
}

// isoTab builds one wisp tab: ledger (start command runs compact_view), agent
// (@gt_ai=1) and spare, in dir, with the session env the retarget path reads.
func isoTab(t *testing.T, env []string, session, dir, lib string) {
	t.Helper()
	isoTmux(t, env, "new-session", "-d", "-s", session, "-x", "200", "-y", "50",
		"-c", dir, "-e", "WISP_DECK_LIB_DIR="+lib, "-e", "WISP_DECK_PATH="+dir,
		"-e", "WISP_DECK_PROJECT="+session,
		fmt.Sprintf("source %q && compact_view %q", filepath.Join(lib, "compact-view.sh"), dir))
	// tmux stores "." and ":" in a session name as "_".
	session = strings.NewReplacer(".", "_", ":", "_").Replace(session)
	ledger := isoTmux(t, env, "display-message", "-p", "-t", session+":", "#{pane_id}")
	ai := isoTmux(t, env, "split-window", "-h", "-P", "-F", "#{pane_id}", "-t", ledger, "-c", dir, "sleep 600")
	isoTmux(t, env, "set-option", "-p", "-t", ai, "@gt_ai", "1")
	isoTmux(t, env, "split-window", "-v", "-t", ledger, "-c", dir, "sleep 600")
}

// isoPanes snapshots a session's panes: id -> "pid|start command".
func isoPanes(t *testing.T, env []string, session string) map[string]string {
	t.Helper()
	out := isoTmux(t, env, "list-panes", "-s", "-t", "="+session+":", "-F", "#{pane_id}\t#{pane_pid}|#{pane_start_command}")
	panes := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		id, rest, _ := strings.Cut(line, "\t")
		panes[id] = rest
	}
	return panes
}

func isoSessionEnv(t *testing.T, env []string, session, name string) string {
	t.Helper()
	c := exec.Command("tmux", "show-environment", "-t", "="+session, name)
	c.Env = env
	out, _ := c.CombinedOutput()
	return strings.TrimSpace(string(out))
}

// The watcher of one tab follows its own agent into a worktree. The other tab —
// the one the user last touched — must not have its ledger and terminal rebuilt
// inside that worktree, and its crash-restore path must not be rewritten.
func TestFollowAgentCheckout_never_retargets_another_tab(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	elsewhere := resolved(t, t.TempDir())
	relaunch := worktreeSwitchCtx(t, dir, repo)
	env := isolationServer(t)
	lib := isoStubLib(t)

	// The wrapper names a tab after its project, and a project may contain a
	// dot, which tmux stores as "_"; the watcher still passes the name it chose.
	isoTab(t, env, "own.tab", repo, lib)
	// Created last, so it is tmux's current session for a caller outside tmux.
	isoTab(t, env, "other", elsewhere, lib)
	// A window whose name equals the tab's session must not stand in for it.
	isoTmux(t, env, "rename-window", "-t", "other:0", "own_tab")
	if cur := isoTmux(t, env, "display-message", "-p", "#{session_name}"); cur != "other" {
		t.Fatalf("precondition: the other tab must be current, got %q", cur)
	}
	otherBefore := isoPanes(t, env, "other")
	ownBefore := isoPanes(t, env, "own_tab")

	out, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("follow_agent_checkout tmux %q %q own.tab", relaunch, wt)), env)
	assertExitCode(t, code, 0)

	if after := isoPanes(t, env, "other"); fmt.Sprint(after) != fmt.Sprint(otherBefore) {
		t.Fatalf("the other tab's panes were rebuilt:\nbefore %v\nafter  %v\n%s", otherBefore, after, out)
	}
	if got := isoSessionEnv(t, env, "other", "WISP_DECK_PATH"); got != "WISP_DECK_PATH="+elsewhere {
		t.Fatalf("the other tab's restore path was rewritten: %q", got)
	}
	if got := isoSessionEnv(t, env, "own_tab", "WISP_DECK_PATH"); got != "WISP_DECK_PATH="+wt {
		t.Fatalf("the following tab's restore path was not moved: %q", got)
	}
	ownAfter := isoPanes(t, env, "own_tab")
	moved := 0
	for id, before := range ownBefore {
		if ownAfter[id] != before {
			moved++
		}
	}
	if moved != 2 {
		t.Fatalf("want the ledger and spare of the following tab rebuilt, %d changed:\nbefore %v\nafter  %v", moved, ownBefore, ownAfter)
	}
	// Moving the right panes is not enough: the spare must also come back on
	// its OWN inner server and config, never the other tab's.
	all := fmt.Sprint(ownAfter)
	// tmux reports a start command with its quotes backslash-escaped.
	assertContains(t, all, `compact_view \"`+wt+`\"`)
	assertContains(t, all, "gtspare_own_tab ")
	assertContains(t, all, "spare-own_tab.conf")
	assertNotContains(t, all, "gtspare_other")
	share := filepath.Dir(relaunch)
	conf, err := os.ReadFile(filepath.Join(share, "spare-own_tab.conf"))
	if err != nil {
		t.Fatalf("the following tab's spare config was not written: %v", err)
	}
	assertContains(t, string(conf), fmt.Sprintf("@gt_dir %q", wt))
	if _, err := os.Stat(filepath.Join(share, "spare-other.conf")); err == nil {
		t.Fatal("the other tab's spare config was written")
	}
	// Key bindings are server-wide: a follow in one tab must not rebind a key
	// every other tab shares.
	if keys := isoTmux(t, env, "list-keys", "-T", "prefix"); strings.Contains(keys, wt) {
		t.Fatalf("a follow rebound a server-wide key to its checkout:\n%s", keys)
	}
}

// isoSpareTab builds one wisp tab whose spare pane runs the REAL spare launch
// command, so it is a client of that session's own inner spare server. window
// empty opens a new session; otherwise a tab-view window is added to session.
// Returns the outer window id.
func isoSpareTab(t *testing.T, env []string, session, dir, share string, newWindow bool) string {
	t.Helper()
	lib := filepath.Join(projectRoot(t), "lib")
	script := fmt.Sprintf(`source %q
label="$(spare_tabs_socket %q)"
spare_tabs_config %q %q %q "$label" 209 %q > %q
spare_tabs_launch_cmd "$label" %q %q`,
		filepath.Join(lib, "spare-tabs.sh"), session, session, dir,
		filepath.Join(lib, "spare-tabs.sh"), session,
		filepath.Join(share, "spare-"+session+".conf"),
		filepath.Join(share, "spare-"+session+".conf"), dir)
	spare, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	var ledger string
	if newWindow {
		ledger = isoTmux(t, env, "new-window", "-P", "-F", "#{pane_id}", "-t", session+":", "-c", dir, "sleep 600; : compact_view")
	} else {
		isoTmux(t, env, "new-session", "-d", "-s", session, "-x", "200", "-y", "50", "-c", dir, "sleep 600; : compact_view")
		ledger = isoTmux(t, env, "display-message", "-p", "-t", session, "#{pane_id}")
	}
	ai := isoTmux(t, env, "split-window", "-h", "-P", "-F", "#{pane_id}", "-t", ledger, "-c", dir, "sleep 600")
	isoTmux(t, env, "set-option", "-p", "-t", ai, "@gt_ai", "1")
	isoTmux(t, env, "split-window", "-v", "-t", ledger, "-c", dir, strings.TrimSpace(spare))
	return isoTmux(t, env, "display-message", "-p", "-t", ledger, "#{window_id}")
}

// isoInnerWindows counts the windows of every inner session on a spare server.
func isoInnerWindows(t *testing.T, env []string, label string) map[string]int {
	t.Helper()
	c := exec.Command("tmux", "-L", label, "list-windows", "-a", "-F", "#{session_name}")
	c.Env = env
	out, _ := c.CombinedOutput()
	counts := map[string]int{}
	for _, s := range strings.Fields(string(out)) {
		counts[s]++
	}
	return counts
}

// isoWaitInner waits until a spare server has n inner sessions with a client.
func isoWaitInner(t *testing.T, env []string, label string, n int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		c := exec.Command("tmux", "-L", label, "list-clients", "-F", "#{session_name}")
		c.Env = env
		out, _ := c.CombinedOutput()
		if len(strings.Fields(string(out))) >= n {
			return
		}
		runBashSnippet(t, "sleep 0.05", env)
	}
	t.Fatalf("spare server %s never got %d attached inner sessions", label, n)
}

// The outer prefix+t / w / Tab / BTab are bound server-wide, so whatever they
// act on must be resolved from the tab the key was pressed in. Pressed in one
// tab, a new terminal must open in THAT tab's spare — not in the tab launched
// last (a baked socket) nor in the tab used last (an untargeted command), and
// not in a sibling tab-view window sharing the same inner server.
func TestSpareTabsOuterKey_acts_on_the_tab_the_key_was_pressed_in(t *testing.T) {
	env := isolationServer(t)
	share := t.TempDir()
	dir := resolved(t, t.TempDir())

	first := isoSpareTab(t, env, "own", dir, share, false)
	isoSpareTab(t, env, "own", dir, share, true)
	isoSpareTab(t, env, "other", dir, share, false)
	isoWaitInner(t, env, "gtspare_own", 2)
	isoWaitInner(t, env, "gtspare_other", 1)
	own := isoInnerWindows(t, env, "gtspare_own")
	other := isoInnerWindows(t, env, "gtspare_other")

	// Fire wrapper.sh's own bind command the way a key press does: run-shell
	// from a pane of the first tab, so tmux expands #{...} against that tab.
	root := projectRoot(t)
	bind, code := runBashSnippet(t, fmt.Sprintf(
		"_WRAPPER_DIR=%q; eval \"$(grep '^_spare_key_bind=' %q)\"; printf %%s \"$_spare_key_bind\"",
		root, filepath.Join(root, "wrapper.sh")), env)
	assertExitCode(t, code, 0)
	if !strings.Contains(bind, "#{q:session_name}") {
		t.Fatalf("wrapper.sh has no _spare_key_bind resolving the tab at key time: %q", bind)
	}
	agent := isoTmux(t, env, "list-panes", "-t", first, "-F", "#{?@gt_ai,#{pane_id},}")
	isoTmux(t, env, "run-shell", "-t", strings.TrimSpace(agent), bind+" new")

	if got := isoInnerWindows(t, env, "gtspare_other"); fmt.Sprint(got) != fmt.Sprint(other) {
		t.Fatalf("a key pressed in one tab opened a terminal in another tab: %v -> %v", other, got)
	}
	got := isoInnerWindows(t, env, "gtspare_own")
	tty := isoTmux(t, env, "list-panes", "-t", first, "-F", "#{?@gt_ai,,#{pane_tty}}")
	var firstInner string
	c := exec.Command("tmux", "-L", "gtspare_own", "list-clients", "-F", "#{client_tty} #{session_name}")
	c.Env = env
	clients, _ := c.CombinedOutput()
	for _, line := range strings.Split(string(clients), "\n") {
		if f := strings.Fields(line); len(f) == 2 && strings.Contains(tty, f[0]) {
			firstInner = f[1]
		}
	}
	for s, n := range own {
		want := n
		if s == firstInner {
			want = n + 1
		}
		if got[s] != want {
			t.Fatalf("want one new terminal in inner session %q only: %v -> %v", firstInner, own, got)
		}
	}

	// prefix+BTab steps THIS tab's terminals, never the sibling's.
	active := func() string {
		c := exec.Command("tmux", "-L", "gtspare_own", "list-windows", "-a", "-F", "#{session_name}:#{window_index}:#{window_active}")
		c.Env = env
		out, _ := c.CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	beforePrev := active()
	isoTmux(t, env, "run-shell", "-t", strings.TrimSpace(agent), bind+" prev")
	afterPrev := active()
	for _, line := range strings.Split(beforePrev, "\n") {
		moved := !strings.Contains(afterPrev, line)
		if strings.HasPrefix(line, firstInner+":") != moved && strings.HasSuffix(line, ":1") {
			t.Fatalf("prev must move only inner session %q:\nbefore %s\nafter  %s", firstInner, beforePrev, afterPrev)
		}
	}

	// prefix+w from the same tab closes one of that tab's terminals, and
	// nothing anywhere else.
	isoTmux(t, env, "run-shell", "-t", strings.TrimSpace(agent), bind+" close")
	if after := isoInnerWindows(t, env, "gtspare_own"); fmt.Sprint(after) != fmt.Sprint(own) {
		t.Fatalf("close did not undo the new terminal in its own tab: %v -> %v", got, after)
	}
	if after := isoInnerWindows(t, env, "gtspare_other"); fmt.Sprint(after) != fmt.Sprint(other) {
		t.Fatalf("close reached another tab: %v -> %v", other, after)
	}
}

// The static half: no server-wide bind in wrapper.sh may carry a value that
// belongs to the session that happened to install it. Such a bind is rewritten
// by every launch, so it acts on the tab launched last from every other tab.
// Per-tab values must come from #{...} formats, which expand at key time.
func TestWrapperBinds_bake_no_session_value(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(src), "\n")
	defs := map[string]string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if name, value, ok := strings.Cut(trimmed, "="); ok && strings.HasPrefix(name, "_") && !strings.ContainsAny(name, " $[") {
			defs[name] += value + "\n"
		}
	}
	scoped := []string{"SESSION_NAME", "PROJECT_DIR", "PROJECT_NAME", "_spare_label",
		"_spare_conf", "_spare_zdotdir", "_spare_cmd", "SELECTED_AI_TOOL"}
	bakes := func(text string) string {
		for _, v := range scoped {
			if strings.Contains(text, "$"+v) || strings.Contains(text, "${"+v) {
				return v
			}
		}
		return ""
	}
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") || !strings.Contains(line, "bind-key") {
			continue
		}
		if v := bakes(line); v != "" {
			t.Errorf("wrapper.sh:%d binds a server-wide key with $%s baked in:\n%s", i+1, v, line)
		}
		for name, def := range defs {
			if (strings.Contains(line, "$"+name+"\"") || strings.Contains(line, "${"+name+"}")) && bakes(def) != "" {
				t.Errorf("wrapper.sh:%d binds a server-wide key through $%s, which bakes $%s", i+1, name, bakes(def))
			}
		}
	}
}

// The quota auto-switch is triggered from the statusline inside the agent pane,
// but the switch itself runs under `run-shell -b`, whose child tmux gives no
// $TMUX_PANE. Every relaunch helper it calls was written for a pane and asks
// tmux for "this" session untargeted, so without the pane handed down it
// respawns whichever tab the user last typed in with this tab's context.
func TestAutoSwitchMaybeTrigger_runs_the_switch_as_its_own_pane(t *testing.T) {
	env := isolationServer(t)
	dir := t.TempDir()
	stub := filepath.Join(dir, "lib")
	out := filepath.Join(dir, "resolved")
	for _, f := range []string{"statusline.sh", "claude-accounts.sh", "claude-shared-settings.sh", "tmux-session.sh"} {
		writeTempFile(t, stub, f, "")
	}
	writeTempFile(t, stub, "account-switch.sh", fmt.Sprintf(
		"auto_switch_relaunch() { tmux display-message -p '#{session_name}' > %q.tmp && mv %q.tmp %q; }\n", out, out, out))

	isoTmux(t, env, "new-session", "-d", "-s", "own", "sleep 600")
	isoTmux(t, env, "new-session", "-d", "-s", "other", "sleep 600")
	ownPane := isoTmux(t, env, "display-message", "-p", "-t", "own", "#{pane_id}")
	socket := isoTmux(t, env, "display-message", "-p", "#{socket_path}")

	cfg := filepath.Join(dir, "cfg")
	writeTempFile(t, filepath.Join(cfg, "wisp-deck"), "auto-switch-accounts", "on\n")
	writeTempFile(t, filepath.Join(cfg, "wisp-deck"), "claude-accounts.list", "Work:work\nPersonal:personal\n")
	relaunch := writeTempFile(t, dir, "relaunch-own", "tool=claude\n")
	env = append(env, "HOME="+dir, "XDG_CONFIG_HOME="+cfg,
		"TMUX="+socket+",1,0", "TMUX_PANE="+ownPane,
		"WISP_DECK_RELAUNCH_FILE="+relaunch, "WISP_DECK_LIB_DIR="+stub,
		"CLAUDE_CONFIG_DIR="+filepath.Join(cfg, "wisp-deck", "claude-accounts", "work"))
	_, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_maybe_trigger", []string{"98", "40"}, env)
	assertExitCode(t, code, 0)

	var got []byte
	for i := 0; i < 100 && len(got) == 0; i++ {
		got, _ = os.ReadFile(out)
		if len(got) == 0 {
			runBashSnippet(t, "sleep 0.05", env)
		}
	}
	if s := strings.TrimSpace(string(got)); s != "own" {
		t.Fatalf("the switch ran against session %q, not the tab whose quota ran out", s)
	}
}

// The launch's second tmux batch is a fresh client outside tmux: until its
// attach-session runs it has no session of its own, so an untargeted
// set-option lands on the session the user last typed in — another tab's bar
// would take this tab's project label. (The first batch is safe: new-session
// makes the new session current for the rest of its own chain.)
func TestWrapper_second_launch_batch_names_its_session_before_attach(t *testing.T) {
	got := recordWrapperNewSession(t)
	var batch string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "attach-session") && !strings.HasPrefix(line, "new-session") {
			batch = line
		}
	}
	if batch == "" {
		t.Fatalf("no attach batch recorded:\n%s", got)
	}
	before, _, _ := strings.Cut(batch, "attach-session")
	for _, cmd := range strings.Split(before, " ; ") {
		cmd = strings.TrimSpace(cmd)
		if strings.HasPrefix(cmd, "set-option") && !strings.Contains(cmd, " -t ") && !strings.Contains(cmd, " -g ") {
			t.Errorf("untargeted before attach: %.80q", cmd)
		}
	}
}

// A draft is replayed through a named paste buffer, and buffers are
// server-wide. Two tabs relaunching at once — every tab on one login crosses
// the auto-switch threshold together — would each load "their" draft into the
// same buffer and paste whichever landed last. Each pane gets its own buffer,
// dropped once pasted.
func TestDraftPaste_uses_a_buffer_of_its_own_pane(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", fmt.Sprintf(`cat >/dev/null; printf '%%s\n' "$*" >> %q`, rec))
	env := buildEnv(t, []string{bin}, "HOME="+dir)
	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		`_draft_paste tmux %1 "draft one" && _draft_paste tmux %2 "draft two"`), env)
	assertExitCode(t, code, 0)
	logOut, _ := os.ReadFile(rec)
	calls := strings.Split(strings.TrimSpace(string(logOut)), "\n")
	if len(calls) != 4 {
		t.Fatalf("want load+paste per draft, got:\n%s", logOut)
	}
	name := func(call string) string {
		f := strings.Fields(call)
		for i := range f {
			if f[i] == "-b" && i+1 < len(f) {
				return f[i+1]
			}
		}
		return ""
	}
	if name(calls[0]) == name(calls[2]) {
		t.Fatalf("two panes share one paste buffer %q:\n%s", name(calls[0]), logOut)
	}
	for _, i := range []int{0, 2} {
		if name(calls[i]) != name(calls[i+1]) {
			t.Fatalf("pasted a different buffer than it loaded:\n%s", logOut)
		}
		if !strings.Contains(calls[i+1], "paste-buffer -d") {
			t.Fatalf("pasted buffer is left on the server:\n%s", calls[i+1])
		}
	}
}

// prefix+i is bound server-wide; the screenshot has to go to the tab the key
// was pressed in, so the bind hands over #{session_name} instead of letting the
// script ask tmux for the current session.
func TestWrapper_screenshot_bind_names_its_session(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(line, "_screenshot_bind=") {
			assertContains(t, line, "#{q:session_name}")
			return
		}
	}
	t.Fatal("wrapper.sh defines no _screenshot_bind")
}

// When the key's window has no spare pane with a live inner client (a pane
// being healed, a spare that fell back to a plain shell), there is no way to
// name the right inner session, so nothing happens — an untargeted command
// would land in a sibling window's terminals.
func TestSpareTabsOuterKey_does_nothing_without_the_windows_spare(t *testing.T) {
	env := isolationServer(t)
	share := t.TempDir()
	dir := resolved(t, t.TempDir())

	isoSpareTab(t, env, "own", dir, share, false)
	isoWaitInner(t, env, "gtspare_own", 1)
	bare := isoTmux(t, env, "new-window", "-P", "-F", "#{window_id}", "-t", "own:", "sleep 600")
	before := isoInnerWindows(t, env, "gtspare_own")

	lib := filepath.Join(projectRoot(t), "lib")
	_, code := runBashSnippet(t, fmt.Sprintf("source %q && spare_tabs_outer_key own %q new",
		filepath.Join(lib, "spare-tabs.sh"), bare), env)
	assertExitCode(t, code, 0)
	if after := isoInnerWindows(t, env, "gtspare_own"); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("a key in a window with no spare opened a terminal elsewhere: %v -> %v", before, after)
	}
}

// A tab-view tab keeps its inner spare server alive through the respawn (the
// sibling window's inner session holds it), so the server never re-reads its
// config: the follow must move the server's live @gt_dir itself, or prefix+t
// and [+] keep opening terminals in the checkout the tab left.
func TestFollowAgentCheckout_moves_a_live_spare_servers_directory(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	relaunch := worktreeSwitchCtx(t, dir, repo)
	env := isolationServer(t)
	lib := isoStubLib(t)
	isoTab(t, env, "own", repo, lib)

	// The sibling inner session that outlives the respawn.
	sibling := exec.Command("tmux", "-L", "gtspare_own", "new-session", "-d", "sleep 600", ";",
		"set", "-g", "@gt_dir", repo)
	sibling.Env = env
	if out, err := sibling.CombinedOutput(); err != nil {
		t.Fatalf("inner server: %v\n%s", err, out)
	}

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("follow_agent_checkout tmux %q %q own", relaunch, wt)), env)
	assertExitCode(t, code, 0)

	c := exec.Command("tmux", "-L", "gtspare_own", "show", "-gv", "@gt_dir")
	c.Env = env
	got, _ := c.CombinedOutput()
	if strings.TrimSpace(string(got)) != wt {
		t.Fatalf("live spare server still opens terminals in %q, want %q", strings.TrimSpace(string(got)), wt)
	}
}

// A watcher that was already running when the session argument was added keeps
// its old child script, which passes three arguments. The wrapper names the
// relaunch file after its session, so that name still pins the right tab.
func TestFollowAgentCheckout_without_a_session_uses_the_relaunch_files_name(t *testing.T) {
	dir := t.TempDir()
	repo, wt := worktreeSwitchRepo(t, dir)
	ctx := worktreeSwitchCtx(t, dir, repo)
	relaunch := filepath.Join(filepath.Dir(ctx), "relaunch-own.tab")
	if err := os.Rename(ctx, relaunch); err != nil {
		t.Fatal(err)
	}
	env := isolationServer(t)
	lib := isoStubLib(t)
	isoTab(t, env, "own.tab", repo, lib)
	isoTab(t, env, "other", resolved(t, t.TempDir()), lib)
	otherBefore := isoPanes(t, env, "other")

	_, code := runBashSnippet(t, accountSwitchSnippet(t,
		fmt.Sprintf("follow_agent_checkout tmux %q %q", relaunch, wt)), env)
	assertExitCode(t, code, 0)
	if after := isoPanes(t, env, "other"); fmt.Sprint(after) != fmt.Sprint(otherBefore) {
		t.Fatalf("the other tab's panes were rebuilt:\nbefore %v\nafter  %v", otherBefore, after)
	}
	if got := isoSessionEnv(t, env, "own_tab", "WISP_DECK_PATH"); got != "WISP_DECK_PATH="+wt {
		t.Fatalf("the tab named by its relaunch file did not follow: %q", got)
	}
}

// Every run-shell and hook runs outside a pane, where tmux's "current" session
// is the tab the user last typed in. So each one the wrapper or lib installs
// must name its tab: a #{q:session_name} expanded at event time, a script or
// argument carrying this session's name, or the pane handed down as
// TMUX_PANE=. The helpers it reaches were written for a pane and ask tmux for
// "this" session untargeted.
func TestRunShellEntryPoints_name_their_tab(t *testing.T) {
	root := projectRoot(t)
	files, _ := filepath.Glob(filepath.Join(root, "lib", "*.sh"))
	files = append(files, filepath.Join(root, "wrapper.sh"))
	names := []string{"#{q:session_name}", "TMUX_PANE=", "$SESSION_NAME", "${SESSION_NAME}",
		// ledger-hover routes to a pane id stored on its own session.
		"#{@wisp_ledger_hover_pane}",
		// the spare's inner config: its outer tab baked at config time, and a
		// click on the inner server that owns that tab alone.
		"-t $outer", "$click"}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(src), "\n")
		defs := map[string]string{}
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if name, _, ok := strings.Cut(trimmed, "="); ok && strings.HasPrefix(name, "_") && !strings.ContainsAny(name, " $[") {
				defs[name] += line + "\n"
			}
		}
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "#") || !strings.Contains(line, "run-shell") {
				continue
			}
			text := line
			for name, def := range defs {
				if strings.Contains(line, "$"+name) || strings.Contains(line, "${"+name+"}") {
					text += "\n" + def
				}
			}
			named := false
			for _, n := range names {
				if strings.Contains(text, n) {
					named = true
				}
			}
			if !named {
				t.Errorf("%s:%d runs a shell outside any pane without naming its tab:\n%s",
					filepath.Base(file), i+1, strings.TrimSpace(line))
			}
		}
	}
}
