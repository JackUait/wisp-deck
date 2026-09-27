package bash_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bashLib runs a snippet with lib/session-restore.sh (and lib/tui.sh) sourced.
// Args reach the snippet as $1.. without Go %q quoting, so control bytes and
// multibyte markers arrive intact.
func bashLib(t *testing.T, env []string, snippet string, args ...string) string {
	t.Helper()
	root := projectRoot(t)
	full := `source "` + filepath.Join(root, "lib/tui.sh") + `" && source "` +
		filepath.Join(root, "lib/session-restore.sh") + `" && ` + snippet
	cmd := exec.Command("bash", append([]string{"-c", full, "snippet"}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("snippet failed: %v\n%s", err, out)
	}
	return string(out)
}

func TestTabMark_round_trips_a_seq(t *testing.T) {
	out := bashLib(t, buildEnv(t, nil),
		`m="$(tab_mark_for_seq 1790509286)"; tab_mark_decode "proj · claude$m"; tab_mark_decode "no marker here"; echo end`)
	if out != "1790509286\nend\n" {
		t.Fatalf("got %q", out)
	}
}

func TestTabMark_is_only_zero_width_characters(t *testing.T) {
	out := bashLib(t, buildEnv(t, nil), `tab_mark_for_seq 5`)
	for _, r := range strings.TrimRight(out, "\n") {
		if r != '​' && r != '‌' && r != '⁠' {
			t.Fatalf("visible rune %U in marker %q", r, out)
		}
	}
}

// Every tab title carries the tab's marker, so the tab strip can be matched
// back to sessions even when several tabs share a project name.
func TestSetTabTitle_appends_the_tab_mark(t *testing.T) {
	out := bashLib(t, buildEnv(t, nil),
		`WISP_DECK_TAB_MARK="$(tab_mark_for_seq 42)"; set_tab_title proj claude | tab_mark_decode "$(cat)"`)
	if strings.TrimSpace(out) != "42" {
		t.Fatalf("title did not carry the mark: %q", out)
	}
}

// observerEnv puts a fake osascript first on PATH. It prints one line per
// window: "<fullscreen>\t<title><US><title>...".
func observerEnv(t *testing.T, dir string, windows []string) []string {
	t.Helper()
	data := filepath.Join(dir, "ax-out")
	if err := os.WriteFile(data, []byte(strings.Join(windows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := mockCommand(t, dir, "osascript", `echo called >> `+quote(filepath.Join(dir, "ax-calls"))+`; cat `+quote(data))
	return buildEnv(t, []string{bin}, "WISP_DECK_TAB_OBSERVE=1")
}

func titled(t *testing.T, env []string, title string, seq int) string {
	return title + strings.TrimRight(bashLib(t, env, `tab_mark_for_seq "$1"`, strconv.Itoa(seq)), "\n")
}

// A windowed Ghostty exposes its tab strip: the observed order replaces the
// recorded one (a drag), fullscreen windows are skipped, and each observed
// window's members are recorded.
func TestTabOrderObserve_follows_the_tab_strip(t *testing.T) {
	dir := t.TempDir()
	env0 := buildEnv(t, nil)
	w1 := "false\t" + titled(t, env0, "a · claude", 100) + us + titled(t, env0, "c · claude", 300) + us + titled(t, env0, "b · claude", 200)
	fs := "true\t" + titled(t, env0, "d · claude", 400)
	writeTempFile(t, dir, "tab-order", "100\n200\n300\n400\n500\n")
	env := observerEnv(t, dir, []string{w1, fs})
	bashLib(t, env, `tab_order_observe "$1"`, dir)
	got := strings.Join(readLines(t, filepath.Join(dir, "tab-order")), ",")
	if got != "100,300,200,400,500" {
		t.Errorf("tab-order = %q", got)
	}
	win := strings.Join(readLines(t, filepath.Join(dir, "tab-windows")), ",")
	if win != "100 300 200" {
		t.Errorf("tab-windows = %q (fullscreen window must not be recorded)", win)
	}
}

func TestTabOrderObserve_runs_at_most_every_ten_seconds(t *testing.T) {
	dir := t.TempDir()
	env0 := buildEnv(t, nil)
	env := observerEnv(t, dir, []string{"false\t" + titled(t, env0, "a", 100)})
	bashLib(t, env, `tab_order_observe "$1"; tab_order_observe "$1"`, dir)
	if n := len(readLines(t, filepath.Join(dir, "ax-calls"))); n != 1 {
		t.Fatalf("osascript ran %d times, want 1", n)
	}
}

// Tests never read the real Ghostty unless they opt in.
func TestTabOrderObserve_is_off_under_test_without_opt_in(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "osascript", `echo called >> `+quote(filepath.Join(dir, "ax-calls")))
	bashLib(t, buildEnv(t, []string{bin}, "WISP_DECK_TESTING=1"), `tab_order_observe "$1"`, dir)
	if _, err := os.Stat(filepath.Join(dir, "ax-calls")); !os.IsNotExist(err) {
		t.Fatal("observer read the tab strip under test without WISP_DECK_TAB_OBSERVE=1")
	}
}

func membershipCase(t *testing.T, windows string, live []fakeTmuxSession, liveRows []string, gone ...string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	old := strconv.FormatInt(time.Now().Unix()-60, 10)
	var snap strings.Builder
	for i, n := range gone {
		snap.WriteString(snapLine("B1", n, "/p/"+n, "claude", "ghostty", "sid-"+n, "L", "", "", n, old, strconv.Itoa(100*(i+1)), "", "0", "0") + "\n")
	}
	for _, s := range live {
		snap.WriteString(snapLine("B1", s.name, "/p/"+s.name, "claude", "ghostty", "", "L", "", "", s.name, "", "900", "", "0", "0") + "\n")
	}
	path := writeTempFile(t, dir, "last-session", snap.String())
	writeTempFile(t, dir, "tab-windows", windows)
	// Both departures inside the timing window: only membership can say no.
	at := time.Now().Unix() - 60
	for _, n := range gone {
		writeDeparted(t, dir, n, at)
	}
	bin := fakeTmux(t, dir, live, liveRows)
	writeSnapshot(t, bin, path)
	got := map[string]string{}
	for _, l := range readLines(t, path) {
		f := strings.Split(l, "|")
		got[f[9]] = "kept"
		if len(f) > 15 && f[15] == "1" {
			got[f[9]] = "batch"
		}
	}
	return got
}

// Two tabs closed within a second while a third tab of the SAME window stays:
// the user closed two tabs, the window did not close.
func TestWriteSessionSnapshot_membership_says_tab_close(t *testing.T) {
	got := membershipCase(t, "100 200 900\n",
		[]fakeTmuxSession{{name: "live", created: 1, attached: 1, env: wispEnv("B1", "live", "/p/live", "900", "")}},
		[]string{"live|0|1||L|1"}, "g1", "g2")
	if got["g1"] != "" || got["g2"] != "" {
		t.Fatalf("closed tabs of a surviving window kept: %v", got)
	}
}

// A window with ONE tab closed while another window survives: a lone
// departure, which timing alone reads as a tab close. Membership says the
// whole window went.
func TestWriteSessionSnapshot_membership_says_single_tab_window_closed(t *testing.T) {
	got := membershipCase(t, "100\n900\n",
		[]fakeTmuxSession{{name: "live", created: 1, attached: 1, env: wispEnv("B1", "live", "/p/live", "900", "")}},
		[]string{"live|0|1||L|1"}, "g1")
	if got["g1"] != "batch" {
		t.Fatalf("single-tab window close not kept as a batch: %v", got)
	}
}

// The mark is set from the launch seq as a plain shell variable: exported, it
// would reach the tmux server's environment and every pane of every tab.
func TestWrapper_sets_the_tab_mark_unexported(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	assertContains(t, s, `WISP_DECK_TAB_MARK="$(tab_mark_for_seq "$_wd_launch_seq")"`)
	assertNotContains(t, s, "export WISP_DECK_TAB_MARK")
}
