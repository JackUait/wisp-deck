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

const (
	rs = "\x1e"
	us = "\x1f"
)

type fakeTmuxSession struct {
	name     string
	created  int
	attached int
	env      string
}

// fakeTmux answers list-sessions, list-windows and show-environment from
// files, so rows can carry the RS/US bytes the snapshot format uses. With
// sessions == nil the server is "gone": list-sessions fails.
// windowRows are "session|index|active|sid|layout".
func fakeTmux(t *testing.T, dir string, sessions []fakeTmuxSession, windowRows []string) string {
	t.Helper()
	state := filepath.Join(dir, "fake-tmux")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if sessions != nil {
		var b strings.Builder
		for _, s := range sessions {
			b.WriteString(strconv.Itoa(s.created) + " " + strconv.Itoa(s.attached) + " " + s.name + "\n")
			writeTempFile(t, state, "env."+s.name, s.env)
		}
		writeTempFile(t, state, "sessions", b.String())
	}
	var w strings.Builder
	for _, row := range windowRows {
		w.WriteString(strings.ReplaceAll(row, "|", us) + "\n")
	}
	writeTempFile(t, state, "windows", w.String())
	return mockCommand(t, dir, "tmux", `
d=`+quote(state)+`
echo "$*" >> "$d/calls"
case "$1" in
  list-sessions) [ -f "$d/sessions" ] || exit 1; cat "$d/sessions" ;;
  list-windows) cat "$d/windows" ;;
  show-environment) n="$3"; n="${n#=}"; n="${n%:}"; cat "$d/env.$n" 2>/dev/null || exit 1 ;;
esac
exit 0
`)
}

func wispEnv(boot, proj, path, seq, extra string) string {
	return "WISP_DECK=1\nWISP_DECK_BOOT=" + boot + "\nWISP_DECK_PROJECT=" + proj +
		"\nWISP_DECK_PATH=" + path + "\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\nWISP_DECK_SEQ=" +
		seq + "\n" + extra
}

// snapLine builds a 15-field snapshot line.
func snapLine(f ...string) string {
	for len(f) < 15 {
		f = append(f, "")
	}
	return strings.Join(f, "|")
}

func writeSnapshot(t *testing.T, binDir, snap string) {
	t.Helper()
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	s := strings.TrimRight(string(data), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// A window close SIGHUPs every wrapper, and each kills its own session. The
// heartbeats that outlive the first kills must not shrink the snapshot: with no
// attached Wisp session left, the file stays exactly as it was.
func TestWriteSessionSnapshot_mass_close_leaves_snapshot_byte_identical(t *testing.T) {
	prev := snapLine("B1", "a", "/p/a", "claude", "ghostty", "sid-a", "L", "", "", "dev-a-1", "", "10", "", "0", "0") + "\n" +
		snapLine("B1", "b", "/p/b", "claude", "ghostty", "sid-b", "L", "", "", "dev-b-1", "", "20", "", "0", "0") + "\n" +
		snapLine("B1", "c", "/p/c", "claude", "ghostty", "sid-c", "L", "", "", "dev-c-1", "", "30", "", "0", "0") + "\n"
	cases := map[string][]fakeTmuxSession{
		"survivors unattached": {
			{name: "dev-b-1", created: 1, attached: 0, env: wispEnv("B1", "b", "/p/b", "20", "")},
		},
		"no session left": {},
		"server gone":     nil,
	}
	for name, sessions := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			snap := writeTempFile(t, dir, "last-session", prev)
			bin := fakeTmux(t, dir, sessions, []string{"dev-b-1|0|1||L"})
			writeSnapshot(t, bin, snap)
			data, _ := os.ReadFile(snap)
			if string(data) != prev {
				t.Errorf("snapshot changed during a mass close:\n got %q\nwant %q", data, prev)
			}
		})
	}
}

// A tab the user closes on purpose while other tabs stay attached is kept for
// one tick with gone_at stamped, then dropped once the grace has run out.
func TestWriteSessionSnapshot_deliberate_close_is_dropped_after_grace(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "last-session")
	gone := snapLine("B1", "b", "/p/b", "claude", "ghostty", "sid-b", "LB", "", "", "dev-b-1", "", "20", "", "0", "0")
	writeTempFile(t, dir, "last-session", gone+"\n")
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-a-1", created: 1, attached: 1, env: wispEnv("B1", "a", "/p/a", "10", "")},
	}, []string{"dev-a-1|0|1||LA"})

	before := time.Now().Unix()
	writeSnapshot(t, bin, snap)
	after := time.Now().Unix()
	lines := readLines(t, snap)
	if len(lines) != 2 {
		t.Fatalf("want the live session plus the just-gone one, got:\n%s", strings.Join(lines, "\n"))
	}
	var goneLine string
	for _, l := range lines {
		if strings.Contains(l, "|dev-b-1|") {
			goneLine = l
		}
	}
	f := strings.Split(goneLine, "|")
	if len(f) != 15 {
		t.Fatalf("gone line has %d fields: %q", len(f), goneLine)
	}
	stamp, err := strconv.ParseInt(f[10], 10, 64)
	if err != nil || stamp < before || stamp > after {
		t.Fatalf("gone_at = %q, want an epoch in [%d,%d]", f[10], before, after)
	}
	f[10] = ""
	if strings.Join(f, "|") != gone {
		t.Errorf("gone line changed beyond gone_at:\n got %q\nwant %q", strings.Join(f, "|"), gone)
	}

	// Still inside the grace: kept, gone_at untouched.
	recent := strconv.FormatInt(time.Now().Unix()-1, 10)
	f[10] = recent
	writeTempFile(t, dir, "last-session", strings.Join(f, "|")+"\n")
	writeSnapshot(t, bin, snap)
	assertContains(t, strings.Join(readLines(t, snap), "\n"), "|dev-b-1|"+recent+"|")

	// Past the grace: a survivor outlived it, so the close was deliberate.
	f[10] = strconv.FormatInt(time.Now().Unix()-5, 10)
	writeTempFile(t, dir, "last-session", strings.Join(f, "|")+"\n")
	writeSnapshot(t, bin, snap)
	lines = readLines(t, snap)
	if len(lines) != 1 || !strings.Contains(lines[0], "|dev-a-1|") {
		t.Errorf("a tab gone past the grace must be dropped, got:\n%s", strings.Join(lines, "\n"))
	}
}

// Old 9-field lines name no session. Once a survivor exists they describe
// sessions tmux re-reports anyway, so they are dropped.
func TestWriteSessionSnapshot_drops_old_nine_field_lines_once_a_survivor_exists(t *testing.T) {
	dir := t.TempDir()
	snap := writeTempFile(t, dir, "last-session", "B0|old|/p/old|claude|ghostty|sid-o|L||\n")
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-a-1", created: 1, attached: 1, env: wispEnv("B1", "a", "/p/a", "10", "")},
	}, []string{"dev-a-1|0|1||LA"})
	writeSnapshot(t, bin, snap)
	lines := readLines(t, snap)
	if len(lines) != 1 || strings.Contains(lines[0], "/p/old") {
		t.Errorf("old 9-field line must be dropped, got:\n%s", strings.Join(lines, "\n"))
	}
}

// Tab-view windows each run their own conversation: every window after the
// first is recorded as index US sid US layout, joined by RS, with the active
// and first indexes. Empty sids must survive the US split.
func TestWriteSessionSnapshot_records_every_window(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "last-session")
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-a-1", created: 1, attached: 1, env: wispEnv("B1", "a", "/p/a", "10",
			"WISP_DECK_CLAUDE_SESSION=env-sid-a\nWISP_DECK_CLAUDE_ACCOUNT=work\n")},
		{name: "dev b 1", created: 2, attached: 1, env: wispEnv("B1", "b", "/p/b", "20",
			"WISP_DECK_CLAUDE_SESSION=env-sid-b\n")},
	}, []string{
		"dev-a-1|0|0|win-sid-0|L0",
		"dev-a-1|1|1|win-sid-1|L1",
		"dev-a-1|2|0||L2",
		"dev b 1|3|1||LB3",
		"dev b 1|4|0|win-sid-4|LB4",
	})
	writeSnapshot(t, bin, snap)
	lines := readLines(t, snap)
	want := []string{
		snapLine("B1", "a", "/p/a", "claude", "ghostty", "win-sid-0", "L0", "work", "", "dev-a-1", "", "10",
			"1"+us+"win-sid-1"+us+"L1"+rs+"2"+us+us+"L2", "1", "0"),
		// No window option on the first window: the session env names it.
		snapLine("B1", "b", "/p/b", "claude", "ghostty", "env-sid-b", "LB3", "", "", "dev b 1", "", "20",
			"4"+us+"win-sid-4"+us+"LB4", "3", "3"),
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("snapshot:\n got %q\nwant %q", lines, want)
	}
}

// Order follows the tab-order file (Ghostty's tab order, not launch order).
// Seqs it does not list come first, by seq.
func TestWriteSessionSnapshot_orders_by_tab_order(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "last-session")
	now := strconv.FormatInt(time.Now().Unix(), 10)
	writeTempFile(t, dir, "last-session",
		snapLine("B1", "gone", "/p/gone", "claude", "ghostty", "", "", "", "", "dev-gone-1", now, "5")+"\n")
	writeTempFile(t, dir, "tab-order", "30\n10\n20\n")
	var sessions []fakeTmuxSession
	var windows []string
	for _, s := range []string{"10", "20", "30", "40"} {
		name := "dev-s" + s
		sessions = append(sessions, fakeTmuxSession{name: name, created: 1, attached: 1,
			env: wispEnv("B1", "s"+s, "/p/"+s, s, "")})
		windows = append(windows, name+"|0|1||L")
	}
	bin := fakeTmux(t, dir, sessions, windows)
	writeSnapshot(t, bin, snap)
	var order []string
	for _, l := range readLines(t, snap) {
		order = append(order, strings.Split(l, "|")[11])
	}
	if got := strings.Join(order, ","); got != "5,40,30,10,20" {
		t.Errorf("seq order = %s, want 5,40,30,10,20", got)
	}
}

// runRestoreGate runs maybe_restore_session against a fake tmux and returns
// its output (the builder flag is echoed).
func runRestoreGate(t *testing.T, dir, boot, home, binDir string) string {
	t.Helper()
	root := projectRoot(t)
	script := `
source ` + quote(filepath.Join(root, "lib", "session-restore.sh")) + `
maybe_restore_session ` + quote(dir) + ` ` + quote(boot) + ` tmux
echo "builder=${WISP_DECK_RESTORE_BUILDER:-0}"
`
	out, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}, "HOME="+home))
	assertExitCode(t, code, 0)
	return out
}

// A window close happens within one boot. With no Wisp session attached, the
// snapshot of the CURRENT boot is restored, in order, with its extra windows.
// Sessions still alive are skipped; unresumable window sids are blanked.
func TestMaybeRestore_restores_a_same_boot_window_close(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/a", "sid-a", time.Hour)
	writeTranscript(t, home, "/p/a", "sid-a1", time.Hour)
	writeTranscript(t, home, "/p/b", "sid-b", time.Hour)
	extrasA := "1" + us + "sid-a1" + us + "LA1" + rs + "2" + us + "dead-sid" + us + "LA2"
	writeTempFile(t, dir, "last-session",
		snapLine("BOOT-X", "b", "/p/b", "claude", "ghostty", "sid-b", "LB", "work", "", "dev-b-1", "", "20", "", "0", "0")+"\n"+
			snapLine("BOOT-X", "a", "/p/a", "claude", "ghostty", "sid-a", "LA", "", "", "dev-a-1", "", "10", extrasA, "2", "0")+"\n"+
			snapLine("BOOT-X", "c", "/p/c", "claude", "ghostty", "", "LC", "", "", "dev-c-1", "", "30", "", "0", "0")+"\n")
	// dev-c-1 is still alive (unattached): it must not be restored twice.
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-c-1", created: 1, attached: 0, env: wispEnv("BOOT-X", "c", "/p/c", "30", "")},
	}, nil)

	// Build and first pop run in one process, as in the wrapper: the builder
	// holds the pop lock for its own pop.
	script := `
source ` + quote(filepath.Join(projectRoot(t), "lib", "session-restore.sh")) + `
maybe_restore_session ` + quote(dir) + ` BOOT-X tmux
echo "builder=${WISP_DECK_RESTORE_BUILDER:-0}"
cp ` + quote(filepath.Join(dir, "restore-queue")) + ` ` + quote(filepath.Join(dir, "queue.copy")) + `
echo "POP=$(restore_queue_pop ` + quote(dir) + ` BOOT-X)"
`
	out, code := runBashSnippet(t, script, buildEnv(t, []string{bin}, "HOME="+home))
	assertExitCode(t, code, 0)
	assertContains(t, out, "builder=1")
	got := readLines(t, filepath.Join(dir, "queue.copy"))
	// Fields: boot path tool sid layout account identity_key extras active first.
	want := []string{
		strings.Join([]string{"BOOT-X", "/p/b", "claude", "sid-b", "LB", "work", "", "", "0", "0"}, "|"),
		strings.Join([]string{"BOOT-X", "/p/a", "claude", "sid-a", "LA", "", "",
			"1" + us + "sid-a1" + us + "LA1" + rs + "2" + us + us + "LA2", "2", "0"}, "|"),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}

	// The pop hands the wrapper 9 fields.
	popped := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "POP=") {
			popped = strings.TrimPrefix(l, "POP=")
		}
	}
	if f := strings.Split(strings.TrimRight(popped, "\n"), "|"); len(f) != 9 || f[0] != "/p/b" || f[8] != "0" {
		t.Errorf("pop output = %q, want 9 fields starting with /p/b", popped)
	}
}

// While any Wisp session is alive and attached, the user is working: a new
// launch is an ordinary new tab, never a restore.
func TestMaybeRestore_no_queue_while_an_attached_wisp_session_lives(t *testing.T) {
	line := snapLine("B1", "a", "/p/a", "opencode", "ghostty", "", "L", "", "", "dev-a-1", "", "10") + "\n"

	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", line)
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-z-9", created: 1, attached: 1, env: wispEnv("B1", "z", "/p/z", "90", "")},
	}, nil)
	out := runRestoreGate(t, dir, "B1", t.TempDir(), bin)
	assertContains(t, out, "builder=0")
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("queue built while an attached Wisp session is alive")
	}

	// An attached session that is not Wisp's does not hold the gate.
	dir2 := t.TempDir()
	writeTempFile(t, dir2, "last-session", line)
	bin2 := fakeTmux(t, dir2, []fakeTmuxSession{
		{name: "other", created: 1, attached: 1, env: "FOO=1\n"},
	}, nil)
	out = runRestoreGate(t, dir2, "B1", t.TempDir(), bin2)
	assertContains(t, out, "builder=1")
}

// Every tab of a restore storm runs the gate at once. Only one may build: a
// second build would resurrect entries the first already handed out.
func TestMaybeRestore_concurrent_launches_build_once(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	var snap strings.Builder
	for i := 0; i < 5; i++ {
		n := strconv.Itoa(i)
		snap.WriteString(snapLine("B1", "p"+n, "/p/"+n, "opencode", "ghostty", "", "L", "", "", "dev-p-"+n, "", n) + "\n")
	}
	writeTempFile(t, dir, "last-session", snap.String())
	bin := fakeTmux(t, dir, nil, nil)
	root := projectRoot(t)
	script := `source ` + quote(filepath.Join(root, "lib", "session-restore.sh")) +
		` && maybe_restore_session ` + quote(dir) + ` B1 tmux`
	env := buildEnv(t, []string{bin}, "HOME="+home)
	var cmds []*exec.Cmd
	for i := 0; i < 2; i++ {
		c := exec.Command("bash", "-c", script)
		c.Env = env
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, c)
	}
	for _, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatalf("maybe_restore_session: %v", err)
		}
	}
	if got := len(readLines(t, filepath.Join(dir, "restore-queue"))); got != 5 {
		t.Errorf("queue has %d entries, want 5", got)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "restore.log"))
	if n := strings.Count(string(log), "queue-built"); n != 1 {
		t.Errorf("queue built %d times, want once:\n%s", n, log)
	}
}

// The claim is keyed by the snapshot's content and expires after 60s, so a
// later window close of the same deck restores again.
func TestMaybeRestore_claim_expires(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session",
		snapLine("B1", "a", "/p/a", "opencode", "ghostty", "", "L", "", "", "dev-a-1", "", "10")+"\n")
	bin := fakeTmux(t, dir, nil, nil)
	ck, code := runBashSnippet(t, `c="$(cksum < `+quote(filepath.Join(dir, "last-session"))+`)"; echo "${c%% *}"`, nil)
	assertExitCode(t, code, 0)
	claim := writeTempFile(t, dir, "restore-claim."+strings.TrimSpace(ck), "")

	out := runRestoreGate(t, dir, "B1", t.TempDir(), bin)
	assertContains(t, out, "builder=0")

	old := time.Now().Add(-61 * time.Second)
	if err := os.Chtimes(claim, old, old); err != nil {
		t.Fatal(err)
	}
	out = runRestoreGate(t, dir, "B1", t.TempDir(), bin)
	assertContains(t, out, "builder=1")
}

// An identity key is matched as a whole field, now that fields follow it.
func TestCodexIdentityReferenced_matches_whole_field(t *testing.T) {
	dir := t.TempDir()
	bin := fakeTmux(t, dir, []fakeTmuxSession{}, nil)
	env := buildEnv(t, []string{bin})
	writeTempFile(t, dir, "last-session",
		snapLine("B", "a", "/p/a", "codex", "ghostty", "", "", "", "a.codex", "dev-a-1", "", "10")+"\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "codex_identity_referenced",
		[]string{"tmux", dir, "a.codex"}, env)
	assertExitCode(t, code, 0)
	_, code = runBashFunc(t, "lib/session-restore.sh", "codex_identity_referenced",
		[]string{"tmux", dir, "b.codex"}, env)
	if code == 0 {
		t.Error("an unreferenced key was reported referenced")
	}
	writeTempFile(t, dir, "last-session", "B|a|/p/a|codex|ghostty||||a.codexx|dev-a-1\n")
	_, code = runBashFunc(t, "lib/session-restore.sh", "codex_identity_referenced",
		[]string{"tmux", dir, "a.codex"}, env)
	if code == 0 {
		t.Error("a key that is only a prefix of another key matched")
	}
}
