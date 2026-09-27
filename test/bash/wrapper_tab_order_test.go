package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// wrapperStubRun launches the real wrapper.sh against a recording tmux mock.
// The functions owned by lib/session-restore.sh and lib/tab-view.sh that the
// wrapper calls are replaced by readonly stubs from BASH_ENV: the wrapper's
// later `source` cannot redefine a readonly function, so every call lands in
// the same log as the tmux calls, in the order it was made.
type wrapperStubRun struct {
	home    string
	confDir string
	project string
	log     string
}

type wrapperStubOptions struct {
	queue       string // restore-queue contents; empty = no restore
	ghosttyConf string // ~/.config/ghostty/config contents; empty = no file
	focusSeq    string // the global @wd_focus_seq the batch-1 read reports
	withProject bool   // launch with the project dir as $1
}

const wrapperStubBashEnv = `_wd_stub_log() {
  local name="$1" a line
  shift
  line="$name"
  for a in "$@"; do line="$line [$a]"; done
  printf '%s\n' "$line" >> "$GT_CALLS"
}
tab_view_new_window() { _wd_stub_log tab_view_new_window "$@"; echo @9; }
restore_layout_watch() { _wd_stub_log restore_layout_watch "$@"; }
tab_order_record() { _wd_stub_log tab_order_record "$@"; }
maybe_restore_session() { _wd_stub_log maybe_restore_session "$@"; }
readonly -f _wd_stub_log tab_view_new_window restore_layout_watch tab_order_record maybe_restore_session
`

// The tmux mock logs every call; the new-session batch prints the ledger and
// AI pane ids, and the global focus read answers the focused tab's seq.
const wrapperStubTmux = `#!/bin/bash
printf 'tmux %s\n' "$*" >> "$GT_CALLS"
case "$1" in
  new-session)
    printf '%%1\n%%2\n'
    ;;
  show-option)
    [ "$*" = "show-option -gqv @wd_focus_seq" ] && printf '%s\n' "$GT_FOCUS_SEQ"
    ;;
esac
exit 0
`

func runWrapperWithStubs(t *testing.T, opts wrapperStubOptions) wrapperStubRun {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	must(t, os.MkdirAll(binDir, 0o755))
	mocks := map[string]string{
		"tmux":          wrapperStubTmux,
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
		"osascript":     "#!/bin/bash\nexit 1\n",
		"open":          "#!/bin/bash\nexit 0\n",
	}
	for name, body := range mocks {
		must(t, os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755))
	}
	run := wrapperStubRun{
		home:    home,
		confDir: filepath.Join(home, ".config", "wisp-deck"),
		project: filepath.Join(home, "proj"),
		log:     filepath.Join(home, "calls"),
	}
	must(t, os.MkdirAll(run.project, 0o755))
	must(t, os.MkdirAll(run.confDir, 0o755))
	if opts.ghosttyConf != "" {
		dir := filepath.Join(home, ".config", "ghostty")
		must(t, os.MkdirAll(dir, 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "config"), []byte(opts.ghosttyConf), 0o644))
	}
	if opts.queue != "" {
		queue := strings.ReplaceAll(opts.queue, "PROJ", run.project)
		must(t, os.WriteFile(filepath.Join(run.confDir, "restore-queue"), []byte(queue), 0o644))
		must(t, os.WriteFile(filepath.Join(run.confDir, "last-restore-boot"), []byte("12345\n"), 0o644))
		seedChainTicket(t, run.confDir)
	}
	bashEnv := filepath.Join(home, "bash-env")
	must(t, os.WriteFile(bashEnv, []byte(wrapperStubBashEnv), 0o600))

	var args []string
	if opts.withProject {
		args = []string{run.project}
	}
	out, code := runBashScript(t, "wrapper.sh", args, buildEnv(t, nil,
		"HOME="+home,
		"GT_CALLS="+run.log,
		"GT_FOCUS_SEQ="+opts.focusSeq,
		"BASH_ENV="+bashEnv,
		"PROJECT_NAME=",
	))
	if code != 0 {
		t.Fatalf("wrapper exited %d:\n%s", code, out)
	}
	return run
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// calls returns the log once it holds a line matching done (background jobs
// outlive the wrapper, whose attach returns at once under the mock).
func (r wrapperStubRun) calls(t *testing.T, done string) []string {
	t.Helper()
	re := regexp.MustCompile(done)
	deadline := time.Now().Add(5 * time.Second)
	var lines []string
	for {
		data, _ := os.ReadFile(r.log)
		lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		for _, l := range lines {
			if re.MatchString(l) {
				return lines
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no call matched %q within 5s; calls:\n%s", done, strings.Join(lines, "\n"))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func matching(lines []string, prefix string) []string {
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func launchSeq(t *testing.T, lines []string) string {
	t.Helper()
	m := regexp.MustCompile(`WISP_DECK_SEQ=(\d+)`).FindStringSubmatch(strings.Join(lines, "\n"))
	if m == nil {
		t.Fatalf("no WISP_DECK_SEQ in the new-session batch:\n%s", strings.Join(lines, "\n"))
	}
	return m[1]
}

func TestWrapperTabOrder_new_session_stamps_seq_and_installs_focus_hook(t *testing.T) {
	run := runWrapperWithStubs(t, wrapperStubOptions{withProject: true})
	lines := run.calls(t, `^tab_order_record `)
	batch := matching(lines, "tmux new-session ")
	if len(batch) != 1 {
		t.Fatalf("want one new-session batch, got %d:\n%s", len(batch), strings.Join(lines, "\n"))
	}
	seq := launchSeq(t, batch)
	assertContains(t, batch[0], "; set-option @wd_seq "+seq+" ;")
	assertContains(t, batch[0], `; set-hook -g client-focus-in[77] set-option -gF @wd_focus_seq "#{@wd_seq}" ;`)
	// The focused tab is read at wrapper start, before the new session exists
	// (and before any picker), so it still names the tab where Cmd+T was pressed.
	read, created := -1, -1
	for i, l := range lines {
		if l == "tmux show-option -gqv @wd_focus_seq" && read < 0 {
			read = i
		}
		if strings.HasPrefix(l, "tmux new-session ") {
			created = i
		}
	}
	if read < 0 || read > created {
		t.Fatalf("focus seq must be read before new-session (read %d, new-session %d):\n%s", read, created, strings.Join(lines, "\n"))
	}
}

func TestWrapperTabOrder_current_position_inserts_after_the_focused_tab(t *testing.T) {
	for _, conf := range []string{"", "window-new-tab-position = current\n"} {
		run := runWrapperWithStubs(t, wrapperStubOptions{withProject: true, focusSeq: "41", ghosttyConf: conf})
		lines := run.calls(t, `^tab_order_record `)
		seq := launchSeq(t, matching(lines, "tmux new-session "))
		got := matching(lines, "tab_order_record ")
		want := "tab_order_record [" + run.confDir + "] [" + seq + "] [41]"
		if len(got) != 1 || got[0] != want {
			t.Fatalf("config %q: tab_order_record calls = %q, want [%q]", conf, got, want)
		}
	}
}

func TestWrapperTabOrder_end_position_appends(t *testing.T) {
	run := runWrapperWithStubs(t, wrapperStubOptions{withProject: true, focusSeq: "41",
		ghosttyConf: "window-new-tab-position = end\n"})
	lines := run.calls(t, `^tab_order_record `)
	seq := launchSeq(t, matching(lines, "tmux new-session "))
	got := matching(lines, "tab_order_record ")
	want := "tab_order_record [" + run.confDir + "] [" + seq + "]"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("tab_order_record calls = %q, want [%q]", got, want)
	}
}

func TestWrapperTabOrder_restore_launch_appends_before_advancing_the_chain(t *testing.T) {
	run := runWrapperWithStubs(t, wrapperStubOptions{focusSeq: "41",
		queue: "12345|PROJ|claude|sid-main\n"})
	lines := run.calls(t, `^tab_order_record `)
	seq := launchSeq(t, matching(lines, "tmux new-session "))
	got := matching(lines, "tab_order_record ")
	want := "tab_order_record [" + run.confDir + "] [" + seq + "]"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("tab_order_record calls = %q, want [%q]", got, want)
	}
	// Recorded before the next chain tab can launch and record its own seq.
	for i, l := range lines {
		if strings.HasPrefix(l, "tab_order_record ") {
			for _, earlier := range lines[:i] {
				if strings.HasPrefix(earlier, "tmux new-session ") {
					t.Fatalf("restore must record its tab position before building the session:\n%s",
						strings.Join(lines, "\n"))
				}
			}
		}
	}
}

func TestWrapperRestoreGate_passes_the_tmux_command(t *testing.T) {
	run := runWrapperWithStubs(t, wrapperStubOptions{queue: "12345|PROJ|claude|sid-main\n"})
	lines := run.calls(t, `^tab_order_record `)
	got := matching(lines, "maybe_restore_session ")
	if len(got) != 1 {
		t.Fatalf("maybe_restore_session calls = %q", got)
	}
	m := regexp.MustCompile(`^maybe_restore_session \[[^]]+\] \[[^]]+\] \[([^]]+)\]$`).FindStringSubmatch(got[0])
	if m == nil || filepath.Base(m[1]) != "tmux" {
		t.Fatalf("maybe_restore_session must get the tmux command as its 3rd arg: %q", got[0])
	}
}
