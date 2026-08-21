package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tab bar has two modes. `compact` is the original numbered chip; `large`
// (the default) widens each chip with the tab's own title and the progress of
// the turn running in it. The mode is a settings key, absent meaning large —
// a session that predates the setting must come up in the new mode, not fall
// back to the old one.
func TestTabViewMode_defaults_to_large(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name     string
		settings string
		write    bool
		want     string
	}{
		{name: "no settings file", write: false, want: "large"},
		{name: "key absent", settings: "theme=auto\n", write: true, want: "large"},
		{name: "compact", settings: "tab_bar=compact\n", write: true, want: "compact"},
		{name: "large", settings: "tab_bar=large\n", write: true, want: "large"},
		{name: "unknown value", settings: "tab_bar=zzz\n", write: true, want: "large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "settings-"+strings.ReplaceAll(tc.name, " ", "_"))
			if tc.write {
				writeTempFile(t, dir, filepath.Base(path), tc.settings)
			}
			out, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_mode",
				[]string{path}, nil)
			assertExitCode(t, code, 0)
			if got := strings.TrimSpace(out); got != tc.want {
				t.Fatalf("tab_view_mode = %q, want %q", got, tc.want)
			}
		})
	}
}

// A large chip carries the window's stamped title and progress alongside the
// number, and keeps every click range the compact chip had — the bar is still
// the only way to switch or open a tab.
func TestTabViewStatusLeft_large_chips_carry_title_and_progress(t *testing.T) {
	out, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_status_left",
		[]string{"myproj", "141", "47", "large"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "#{@wd_tab_title}")
	assertContains(t, out, "#{@wd_tab_progress}")
	assertContains(t, out, "range=user|wdtab:#{window_id}")
	assertContains(t, out, "range=user|wdnew")
	assertContains(t, out, "#{e|+:#{window_index},1}")
	// The progress segment only draws when there is progress to draw, or an
	// idle tab shows a dangling separator.
	assertContains(t, out, "#{?#{@wd_tab_progress}")
}

// Omitting the mode means large: the default lives in the renderer, so a
// caller that predates the setting still gets the new bar.
func TestTabViewStatusLeft_defaults_to_large(t *testing.T) {
	out, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_status_left",
		[]string{"myproj", "141", "47"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "#{@wd_tab_title}")
}

// Compact stays exactly what it was: a numbered chip, no stamped state.
func TestTabViewStatusLeft_compact_keeps_numbered_chips(t *testing.T) {
	out, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_status_left",
		[]string{"myproj", "141", "47", "compact"}, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "#{@wd_tab_title}")
	assertNotContains(t, out, "#{@wd_tab_progress}")
	assertContains(t, out, "#{e|+:#{window_index},1}")
	assertContains(t, out, "range=user|wdtab:#{window_id}")
}

// The progress a chip shows is the elapsed time of the turn the agent is
// running right now, read off the agent pane's own live status line. Every
// fixture below is a verbatim capture from a running Claude pane.
func TestTabViewProgressFromPane_reads_the_running_turn(t *testing.T) {
	cases := []struct {
		name string
		pane string
		want string
	}{
		{
			name: "thinking with a token count",
			pane: "  ⎿  Tip: Use /clear to start fresh when switching topics\n" +
				"✶ Doodling… (42m 37s · ↓ 65.8k tokens)\n" +
				"\n" +
				"────────────────\n❯\n────────────────\n",
			want: "42m 37s",
		},
		{
			name: "thinking with an effort suffix",
			pane: "✽ Hyperspacing… (8m 25s · ↓ 16.9k tokens · thinking with xhigh effort)\n",
			want: "8m 25s",
		},
		{
			name: "seconds only",
			pane: "✳ Grooving… (47s · ↓ 8.0k tokens · thought for 2s)\n",
			want: "47s",
		},
		{
			name: "hours",
			pane: "✢ Determining… (1h 16m 52s · ↓ 34.1k tokens)\n",
			want: "1h 16m 52s",
		},
		{
			// A finished turn prints a past-tense summary with no ellipsis. It
			// is history, not progress — the tab is idle.
			name: "finished turn summary is not progress",
			pane: "✻ Cooked for 1h 38m 25s\n\n────────────────\n❯\n",
			want: "",
		},
		{
			name: "idle pane with prose",
			pane: "※ recap: the review is finished and published.\n" +
				"  decide which of the ranked fixes to start. (disable recaps in /config)\n" +
				"────────────────\n❯\n────────────────\n",
			want: "",
		},
		{
			name: "empty capture",
			pane: "",
			want: "",
		},
		{
			// The live line is the LAST one: an older turn's line scrolled up
			// the transcript must not outrank the running one.
			name: "latest line wins",
			pane: "✶ Doodling… (42m 37s · ↓ 65.8k tokens)\n" +
				"✽ Hyperspacing… (2m 3s · ↓ 1.0k tokens)\n",
			want: "2m 3s",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runBashFuncWithStdin(t, "lib/tab-view.sh",
				"tab_view_progress_from_pane", nil, nil, tc.pane)
			assertExitCode(t, code, 0)
			if got := strings.TrimSpace(out); got != tc.want {
				t.Fatalf("tab_view_progress_from_pane = %q, want %q", got, tc.want)
			}
		})
	}
}

// The chip title is the agent pane's own title — the task summary the tool
// stamps there — with the tool's leading glyph dropped, its own styling
// neutered, and a length the bar can actually fit.
func TestTabViewChipTitle(t *testing.T) {
	cases := []struct {
		name  string
		title string
		max   string
		want  string
	}{
		{
			name:  "strips the tool glyph",
			title: "✳ Fix the tab bar",
			max:   "40",
			want:  "Fix the tab bar",
		},
		{
			name:  "plain title is kept",
			title: "Fix the tab bar",
			max:   "40",
			want:  "Fix the tab bar",
		},
		{
			// tmux draws the status line by parsing #[...] out of the EXPANDED
			// format, so a title carrying one repaints the rest of the bar in
			// the model's chosen colour. Verified live on tmux 3.6a.
			name:  "a title cannot inject a style",
			title: "✳ Danger #[fg=red]red#[default] title",
			max:   "60",
			want:  "Danger [fg=red]red[default] title",
		},
		{
			name:  "truncates with an ellipsis",
			title: "✳ Rebuilding the entire attention pipeline again",
			max:   "12",
			want:  "Rebuilding …",
		},
		{
			name:  "exact fit is not truncated",
			title: "✳ Twelve chars",
			max:   "12",
			want:  "Twelve chars",
		},
		{
			// tmux defaults a pane's title to the hostname: the agent has not
			// named the turn yet, so the tab is named after its project.
			name:  "hostname falls back to the project",
			title: "somehost.local",
			max:   "40",
			want:  "myproj",
		},
		{
			name:  "empty falls back to the project",
			title: "",
			max:   "40",
			want:  "myproj",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_chip_title",
				[]string{tc.title, "somehost.local", "myproj", tc.max}, nil)
			assertExitCode(t, code, 0)
			if got := strings.TrimSpace(out); got != tc.want {
				t.Fatalf("tab_view_chip_title(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}

// Chips share one width, so the title each one may spend has to shrink as tabs
// are opened — otherwise the bar runs past the window edge and tmux clips the
// tabs on the right out of existence.
func TestTabViewTitleBudget_shrinks_as_tabs_open(t *testing.T) {
	cases := []struct {
		cols string
		n    string
		want int
	}{
		{cols: "200", n: "1", want: 32},
		{cols: "200", n: "4", want: 29},
		{cols: "200", n: "8", want: 8},
		{cols: "80", n: "4", want: 8},
		{cols: "0", n: "0", want: 8},
	}
	for _, tc := range cases {
		t.Run("cols"+tc.cols+"_n"+tc.n, func(t *testing.T) {
			out, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_title_budget",
				[]string{tc.cols, tc.n}, nil)
			assertExitCode(t, code, 0)
			var got int
			if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &got); err != nil {
				t.Fatalf("unparseable budget %q: %v", out, err)
			}
			if got != tc.want {
				t.Fatalf("tab_view_title_budget(%s,%s) = %d, want %d", tc.cols, tc.n, got, tc.want)
			}
		})
	}
}

// paneField is the inventory delimiter tab_view_stamp_windows asks tmux for:
// a unit separator, because bash collapses runs of tab (an IFS whitespace
// character) and a window with two empty fields in a row would shift every
// later field left.
const paneField = "\x1f"

// stampPaneRow builds one inventory row of the shape the stamping pass parses:
// window, width, agent flag, pane, the title and progress already stamped, and
// the pane's own title.
func stampPaneRow(fields ...string) string {
	return strings.Join(fields, paneField) + "\n"
}

// mockStampTmux is a tmux spy for the per-window stamping pass: it answers the
// one list-panes inventory call from MOCK_PANES and records every set-option.
const mockStampTmux = `#!/bin/bash
printf '%s\n' "$*" >> "$GT_REC"
case "$1" in
  list-panes) printf '%s' "$MOCK_PANES" ;;
  capture-pane)
    for last; do :; done
    case "$*" in
      *"$MOCK_BUSY_PANE"*) printf '%s\n' "$MOCK_BUSY_LINE" ;;
    esac ;;
esac
exit 0
`

// stampRecord runs tab_view_stamp_windows against the spy and returns every
// recorded tmux invocation.
func stampRecord(t *testing.T, panes, busyPane, busyLine string) string {
	t.Helper()
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", mockStampTmux)
	env := buildEnv(t, []string{bin},
		"GT_REC="+rec,
		"MOCK_PANES="+panes,
		"MOCK_BUSY_PANE="+busyPane,
		"MOCK_BUSY_LINE="+busyLine)
	_, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_stamp_windows",
		[]string{"tmux", "mysession", "myproj"}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("read tmux record: %v", err)
	}
	return string(data)
}

// The bar renders window options, so something has to put the title and the
// progress there. One inventory call covers every window in the session; the
// agent pane of each window supplies both halves.
func TestTabViewStampWindows_stamps_each_windows_title_and_progress(t *testing.T) {
	panes := stampPaneRow("@1", "200", "", "%0", "", "", "somehost.local") +
		stampPaneRow("@1", "200", "1", "%1", "", "", "✳ Fix the tab bar") +
		stampPaneRow("@2", "200", "1", "%2", "", "3m 1s", "✳ Review the backend")
	got := stampRecord(t, panes, "%1", "✽ Hyperspacing… (8m 25s · ↓ 16.9k tokens)")

	assertContains(t, got, `set-option -w -t @1 @wd_tab_title Fix the tab bar`)
	assertContains(t, got, `set-option -w -t @1 @wd_tab_progress 8m 25s`)
	assertContains(t, got, `set-option -w -t @2 @wd_tab_title Review the backend`)
	// The second window's agent has gone idle, so the elapsed time of the turn
	// that already ended is cleared rather than left standing on the chip.
	assertContains(t, got, `set-option -w -t @2 @wd_tab_progress `)
	// Only the agent pane is worth capturing — the ledger and the spare shell
	// have no turn to report.
	assertNotContains(t, got, "capture-pane -p -t %0")
}

// Stamping repaints the client, so a value that has not moved must not be
// written again — every session on the machine runs this pass twice a second.
func TestTabViewStampWindows_skips_windows_that_have_not_moved(t *testing.T) {
	panes := stampPaneRow("@1", "200", "1", "%1", "Fix the tab bar", "8m 25s", "✳ Fix the tab bar")
	got := stampRecord(t, panes, "%1", "✽ Hyperspacing… (8m 25s · ↓ 16.9k tokens)")
	assertNotContains(t, got, "set-option")
}

// A window with no agent pane (a layout still building, or one the user broke
// apart) is skipped rather than stamped with a wrong or blank identity.
func TestTabViewStampWindows_skips_a_window_with_no_agent_pane(t *testing.T) {
	panes := stampPaneRow("@1", "200", "", "%0", "", "", "somehost.local")
	got := stampRecord(t, panes, "%9", "")
	assertNotContains(t, got, "set-option")
}

// The whole pass is fail-open: no tmux server, no session, no inventory — it
// prints nothing and exits 0, exactly like the rest of lib/tab-view.sh.
func TestTabViewStampWindows_fails_open(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "tmux.log")
	bin := mockCommand(t, dir, "tmux", "printf '%s\\n' \"$*\" >> \"$GT_REC\"; exit 1\n")
	env := buildEnv(t, []string{bin}, "GT_REC="+rec)
	out, code := runBashFunc(t, "lib/tab-view.sh", "tab_view_stamp_windows",
		[]string{"tmux", "gone", "myproj"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected no output, got %q", out)
	}
}

// Something has to keep the stamped state current, and it is the watcher every
// session already runs: it ticks twice a second, it knows the session and the
// project, and it re-reads the settings file each tick, so a mode change
// reaches an open window without a relaunch.
func TestTabTitleWatcher_stamps_the_tab_bar_each_tick(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		want     bool
	}{
		{name: "large is the default", settings: "theme=auto\n", want: true},
		{name: "large", settings: "tab_bar=large\n", want: true},
		{name: "compact skips the capture", settings: "tab_bar=compact\n", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			generation := "generation.stamp1"
			state := writeAttentionState(t, root, generation, "1", "working", "-")
			descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
			writeTempFile(t, root, "settings", tc.settings)
			logFile := filepath.Join(root, "stamp.log")

			binDir := mockCommand(t, root, "tmux", `
case "$1" in
  list-panes) printf '%%7\t1\n' ;;
  display-message) printf 'task title\n' ;;
esac
`)
			tmuxPath := filepath.Join(binDir, "tmux")
			script := tabTitleSnippet(t, fmt.Sprintf(`
source %q
tab_view_stamp_windows() { printf 'stamp:%%s:%%s\n' "$2" "$3" >> %q; }
apply_tab_title() { :; }
attention_watcher_reset
attention_watcher_tick sess-a myproj full %q %q %q
`, filepath.Join(projectRoot(t), "lib", "tab-view.sh"), logFile,
				tmuxPath, descriptor, root))

			_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
			assertExitCode(t, code, 0)
			data, err := os.ReadFile(logFile)
			if err != nil && tc.want {
				t.Fatalf("expected a stamping pass, got none: %v", err)
			}
			got := strings.Contains(string(data), "stamp:sess-a:myproj")
			if got != tc.want {
				t.Fatalf("stamped = %v, want %v (log %q)", got, tc.want, string(data))
			}
		})
	}
}

// wrapperTabBarRecord launches the wrapper with the given wisp-deck settings
// file and returns every tmux invocation the launch chain made.
func wrapperTabBarRecord(t *testing.T, settings string) string {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	mocks := map[string]string{
		"tmux":          recordingTmuxMock,
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}
	if settings != "" {
		cfg := filepath.Join(home, ".config", "wisp-deck")
		if err := os.MkdirAll(cfg, 0755); err != nil {
			t.Fatalf("mkdir config: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cfg, "settings"), []byte(settings), 0644); err != nil {
			t.Fatalf("write settings: %v", err)
		}
	}
	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	seedRestoreQueue(t, home, projDir, "claude")
	recPath := filepath.Join(home, "rec")
	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("tmux never invoked: %v", err)
	}
	return string(data)
}

// The bar a session comes up with is the saved mode — and with nothing saved,
// large. Both status-left writes in the launch chain (the one that builds the
// session and the realignment once the panes exist) have to agree, or the bar
// changes shape a moment after the tab opens.
func TestWrapper_launches_the_large_tab_bar_by_default(t *testing.T) {
	got := wrapperTabBarRecord(t, "")
	if n := strings.Count(got, "#{@wd_tab_title}"); n < 2 {
		t.Fatalf("large chips in %d status-left writes, want both:\n%s", n, got)
	}
	assertContains(t, got, "#{@wd_tab_progress}")
}

func TestWrapper_launches_the_compact_tab_bar_when_saved(t *testing.T) {
	got := wrapperTabBarRecord(t, "tab_bar=compact\n")
	assertNotContains(t, got, "#{@wd_tab_title}")
	assertContains(t, got, "range=user|wdtab:#{window_id}")
}

// menuTabBarArgs runs the project picker with the given wisp-deck settings and
// returns the argument line it handed the Go menu binary.
func menuTabBarArgs(t *testing.T, settings string) string {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "captured_args")
	binDir := mockCommand(t, dir, "wisp-deck-tui", fmt.Sprintf(`
echo "$*" > %q
echo '{"action":"quit"}'
`, argsFile))
	projectsFile := writeTempFile(t, dir, "projects", "proj1:/tmp/p1\n")
	cfg := filepath.Join(dir, "config", "wisp-deck")
	if settings != "" {
		if err := os.MkdirAll(cfg, 0755); err != nil {
			t.Fatalf("mkdir config: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cfg, "settings"), []byte(settings), 0644); err != nil {
			t.Fatalf("write settings: %v", err)
		}
	}
	root := projectRoot(t)
	env := buildEnv(t, []string{binDir},
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"))
	script := fmt.Sprintf(`
source %q 2>/dev/null || true
source %q
error() { echo "ERROR: $*" >&2; }
AI_TOOLS_AVAILABLE=("claude")
SELECTED_AI_TOOL="claude"
_update_version=""
select_project_interactive %q || true
`, filepath.Join(root, "lib/tui.sh"),
		filepath.Join(root, "lib/menu-tui.sh"),
		projectsFile)
	if _, code := runBashSnippet(t, script, env); code != 0 {
		t.Fatalf("picker exited %d", code)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("args file not found: %v", err)
	}
	return string(data)
}

// The Settings row has to open on the mode that is actually in force, so the
// saved value travels to the menu the same way every other preference does.
func TestMenu_forwards_the_tab_bar_mode(t *testing.T) {
	assertContains(t, menuTabBarArgs(t, ""), "--tab-bar large")
	assertContains(t, menuTabBarArgs(t, "tab_bar=compact\n"), "--tab-bar compact")
}

// Changing the mode in Settings has to reach a window that is already open —
// the bar's format is written once at launch, so the watcher re-sets it when
// it notices the saved mode move. The first tick must NOT refresh: the launch
// chain already drew the right bar, and refreshing on every session's first
// tick is pure churn.
func TestTabTitleWatcher_reruns_the_bar_when_the_mode_changes(t *testing.T) {
	root := t.TempDir()
	generation := "generation.mode1"
	state := writeAttentionState(t, root, generation, "1", "working", "-")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	settings := writeTempFile(t, root, "settings", "tab_bar=large\n")
	logFile := filepath.Join(root, "refresh.log")

	binDir := mockCommand(t, root, "tmux", `
case "$1" in
  list-panes) printf '%%7\t1\n' ;;
  display-message) printf 'task title\n' ;;
esac
`)
	tmuxPath := filepath.Join(binDir, "tmux")
	script := tabTitleSnippet(t, fmt.Sprintf(`
source %q
tab_view_stamp_windows() { :; }
tab_view_refresh_bar() { printf 'refresh\n' >> %q; }
apply_tab_title() { :; }
attention_watcher_reset
attention_watcher_tick sess-a myproj full %q %q %q
attention_watcher_tick sess-a myproj full %q %q %q
printf 'tab_bar=compact\n' > %q
attention_watcher_tick sess-a myproj full %q %q %q
attention_watcher_tick sess-a myproj full %q %q %q
`, filepath.Join(projectRoot(t), "lib", "tab-view.sh"), logFile,
		tmuxPath, descriptor, root,
		tmuxPath, descriptor, root,
		settings,
		tmuxPath, descriptor, root,
		tmuxPath, descriptor, root))

	_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(logFile)
	if n := strings.Count(string(data), "refresh"); n != 1 {
		t.Fatalf("bar refreshed %d times across four ticks and one change, want 1", n)
	}
}
