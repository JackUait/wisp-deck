package bash_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func recordTabOrder(t *testing.T, dir string, args ...string) {
	t.Helper()
	_, code := runBashFunc(t, "lib/session-restore.sh", "tab_order_record",
		append([]string{dir}, args...), nil)
	assertExitCode(t, code, 0)
}

func tabOrder(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "tab-order"))
	if err != nil {
		t.Fatalf("read tab-order: %v", err)
	}
	return strings.Join(strings.Fields(string(data)), ",")
}

func TestTabOrderRecord_appends_inserts_after_and_dedupes(t *testing.T) {
	dir := t.TempDir()
	recordTabOrder(t, dir, "10")
	recordTabOrder(t, dir, "20", "")
	recordTabOrder(t, dir, "30", "10")
	if got := tabOrder(t, dir); got != "10,30,20" {
		t.Fatalf("after insert-after: %s, want 10,30,20", got)
	}
	// An unknown predecessor appends.
	recordTabOrder(t, dir, "40", "99")
	// Re-recording a seq moves it; it never appears twice.
	recordTabOrder(t, dir, "10", "20")
	if got := tabOrder(t, dir); got != "30,20,10,40" {
		t.Errorf("after move: %s, want 30,20,10,40", got)
	}
}

func TestTabOrderRecord_trims_to_last_500(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 600; i++ {
		b.WriteString(strconv.Itoa(i) + "\n")
	}
	writeTempFile(t, dir, "tab-order", b.String())
	recordTabOrder(t, dir, "1000")
	got := strings.Split(tabOrder(t, dir), ",")
	if len(got) != 500 || got[0] != "102" || got[499] != "1000" {
		t.Errorf("trimmed file has %d lines from %s to %s, want 500 from 102 to 1000",
			len(got), got[0], got[len(got)-1])
	}
}

func TestTabOrderRecord_never_fails_the_caller(t *testing.T) {
	_, code := runBashFunc(t, "lib/session-restore.sh", "tab_order_record",
		[]string{"/nonexistent/dir", "10"}, nil)
	assertExitCode(t, code, 0)
	dir := t.TempDir()
	recordTabOrder(t, dir, "not-a-seq")
	if _, err := os.Stat(filepath.Join(dir, "tab-order")); err == nil {
		t.Error("a non-numeric seq must not be recorded")
	}
}

// Extra tab-view windows get their own layout replay.
func TestRestoreLayoutWatch_targets_the_given_window(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	bin := mockCommand(t, dir, "tmux", `
echo "$*" >> `+quote(log)+`
case "$1" in
  display-message) echo "100x50 L" ;;
esac
exit 0
`)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_layout_watch",
		[]string{"tmux", "dev-a-1", "L", "0.01", "1", "3", "2"}, buildEnv(t, []string{bin}))
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(log)
	assertContains(t, string(data), "select-layout -t =dev-a-1:2 L")
	assertNotContains(t, string(data), "=dev-a-1:0")
}

// Cmd+9 first, so the next restored tab is appended at the end even if the
// user clicked another tab mid-chain.
func TestRestoreTriggerTab_goes_to_the_last_tab_before_opening_one(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	bin := mockCommand(t, dir, "osascript", `for a in "$@"; do echo "$a"; done >> `+quote(rec)+`; exit 0`)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_trigger_tab", nil, buildEnv(t, []string{bin}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("osascript not invoked: %v", err)
	}
	assertSubstringsInOrder(t, string(data),
		`keystroke "9" using command down`,
		`keystroke "t" using command down`)
}
