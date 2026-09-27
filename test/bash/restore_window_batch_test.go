package bash_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeDeparted(t *testing.T, dir, name string, at int64) {
	t.Helper()
	d := filepath.Join(dir, "departed")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, d, name, strconv.FormatInt(at, 10)+"\n")
}

// Closing one Ghostty window while another holds Wisp tabs: the closed
// window's tabs leave together (their wrappers stamp their departure within a
// second of each other). That cluster is a window close, not a user closing a
// tab, so it outlives the grace a survivor would otherwise apply.
func TestWriteSessionSnapshot_keeps_a_closed_window_while_another_survives(t *testing.T) {
	dir := t.TempDir()
	old := strconv.FormatInt(time.Now().Unix()-60, 10)
	snap := writeTempFile(t, dir, "last-session",
		snapLine("B1", "a", "/p/a", "claude", "ghostty", "sid-a", "L", "", "", "dev-a-1", old, "10", "", "0", "0")+"\n"+
			snapLine("B1", "b", "/p/b", "claude", "ghostty", "sid-b", "L", "", "", "dev-b-2", old, "20", "", "0", "0")+"\n"+
			snapLine("B1", "c", "/p/c", "claude", "ghostty", "sid-c", "L", "", "", "dev-c-3", "", "30", "", "0", "0")+"\n")
	at := time.Now().Unix() - 60
	writeDeparted(t, dir, "dev-a-1", at)
	writeDeparted(t, dir, "dev-b-2", at+1)
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-c-3", created: 1, attached: 1, env: wispEnv("B1", "c", "/p/c", "30", "")},
	}, []string{"dev-c-3|0|1||L|1"})
	writeSnapshot(t, bin, snap)
	got := map[string]string{}
	for _, l := range readLines(t, snap) {
		f := strings.Split(l, "|")
		if len(f) < 15 {
			t.Fatalf("short line: %q", l)
		}
		got[f[9]] = ""
		if len(f) > 15 {
			got[f[9]] = f[15]
		}
	}
	for _, n := range []string{"dev-a-1", "dev-b-2"} {
		if got[n] != "1" {
			t.Errorf("%s: batch flag = %q, want kept with 1 (lines %v)", n, got[n], got)
		}
	}
	if got["dev-c-3"] != "" {
		t.Errorf("live tab flagged as a batch: %q", got["dev-c-3"])
	}
}

// One tab leaving alone while others stay is the user closing that tab.
func TestWriteSessionSnapshot_lone_departure_still_expires(t *testing.T) {
	dir := t.TempDir()
	old := strconv.FormatInt(time.Now().Unix()-60, 10)
	snap := writeTempFile(t, dir, "last-session",
		snapLine("B1", "a", "/p/a", "claude", "ghostty", "sid-a", "L", "", "", "dev-a-1", old, "10", "", "0", "0")+"\n")
	writeDeparted(t, dir, "dev-a-1", time.Now().Unix()-60)
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-c-3", created: 1, attached: 1, env: wispEnv("B1", "c", "/p/c", "30", "")},
	}, []string{"dev-c-3|0|1||L|1"})
	writeSnapshot(t, bin, snap)
	for _, l := range readLines(t, snap) {
		if strings.Contains(l, "dev-a-1") {
			t.Fatalf("lone closed tab kept: %q", l)
		}
	}
}

// With a survivor attached, the next launch restores the closed window's
// tabs (batch lines) and nothing else.
func TestMaybeRestore_restores_a_closed_window_while_another_survives(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(name, gone, seq, batch string) string {
		f := strings.Split(snapLine("B1", "p", proj, "claude", "ghostty", "", "L", "", "", name, gone, seq, "", "0", "0"), "|")
		return strings.Join(append(f, batch), "|")
	}
	writeTempFile(t, dir, "last-session",
		line("dev-a-1", "100", "10", "1")+"\n"+
			line("dev-x-9", "100", "15", "")+"\n"+
			line("dev-b-2", "100", "20", "1")+"\n"+
			line("dev-c-3", "", "30", "")+"\n")
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-c-3", created: 1, attached: 1, env: wispEnv("B1", "p", proj, "30", "")},
	}, nil)
	_, code := runBashFunc(t, "lib/session-restore.sh", "maybe_restore_session",
		[]string{dir, "B1", filepath.Join(bin, "tmux")}, buildEnv(t, nil, "HOME="+dir))
	assertExitCode(t, code, 0)
	q := readLines(t, filepath.Join(dir, "restore-queue"))
	if len(q) != 2 {
		t.Fatalf("want the two batch tabs queued, got %q", q)
	}
	got := strings.Join(readLines(t, filepath.Join(dir, "restored-sessions")), ",")
	if got != "dev-a-1,dev-b-2" {
		t.Errorf("restored-sessions = %q", got)
	}
}

// A survivor and no closed window: nothing to restore (the user closed tabs).
func TestMaybeRestore_no_batch_no_restore_while_a_survivor_lives(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, dir, "last-session",
		snapLine("B1", "p", proj, "claude", "ghostty", "", "L", "", "", "dev-x-9", "100", "15", "", "0", "0")+"\n"+
			snapLine("B1", "p", proj, "claude", "ghostty", "", "L", "", "", "dev-c-3", "", "30", "", "0", "0")+"\n")
	bin := fakeTmux(t, dir, []fakeTmuxSession{
		{name: "dev-c-3", created: 1, attached: 1, env: wispEnv("B1", "p", proj, "30", "")},
	}, nil)
	_, code := runBashFunc(t, "lib/session-restore.sh", "maybe_restore_session",
		[]string{dir, "B1", filepath.Join(bin, "tmux")}, buildEnv(t, nil, "HOME="+dir))
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); !os.IsNotExist(err) {
		t.Fatalf("queue built with no closed window: %v", err)
	}
}

// cleanup stamps the departure before anything else, so the stamps of one
// window close land together however slow the rest of the teardown is.
func TestWrapperCleanup_stamps_departure_first(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	i := strings.Index(s, "cleanup() {")
	if i < 0 {
		t.Fatal("no cleanup()")
	}
	body := s[i:]
	stamp := strings.Index(body, "departed/")
	watcher := strings.Index(body, "stop_tab_title_watcher")
	if stamp < 0 || stamp > watcher {
		t.Fatalf("cleanup must stamp departed/ before its first teardown step")
	}
}

// A wrapper that exits from the picker never reaches the read that deletes
// its focus-pred file, so cleanup removes it.
func TestWrapperCleanup_removes_the_focus_pred_file(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	i := strings.Index(s, "cleanup() {")
	j := strings.Index(s[i:], "\n}\n")
	assertContains(t, s[i:i+j], `rm -f "${_wd_focus_file:-}"`)
}
