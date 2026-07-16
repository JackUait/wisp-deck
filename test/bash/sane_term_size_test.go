package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// _sane_term_size guards the tmux launch against the Ghostty pty-size race:
// a tab's wrapper can start before the pty reaches its real dimensions, and
// feeding that transient tiny size to `tmux new-session -x/-y` makes both
// split-window commands fail ("no space for new pane") — the tab then shows
// only the full-width ledger pane forever (the stuck-launch bug).

// A sane size reported by the terminal is passed through unchanged, with no
// retry delay.
func Test_sane_term_size_passes_through_sane_size(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "stty", `echo "51 188"`)
	env := buildEnv(t, []string{bin})
	out, code := runBashFunc(t, "lib/loading.sh", "_sane_term_size", nil, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "51 188" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "51 188")
	}
}

// A size that stays tiny for the whole bounded wait is clamped to the floor
// (24 rows x 80 cols) so the three-pane split can never fail; tmux resizes to
// the client's real size at attach anyway.
func Test_sane_term_size_clamps_persistently_tiny_size(t *testing.T) {
	dir := t.TempDir()
	bin := mockCommand(t, dir, "stty", `echo "2 2"`)
	env := buildEnv(t, []string{bin})
	// floor_rows floor_cols tries interval — tiny interval keeps the test fast.
	out, code := runBashFunc(t, "lib/loading.sh", "_sane_term_size",
		[]string{"24", "80", "3", "0.01"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "24 80" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "24 80")
	}
}

// The transient race: the first reads see the pre-resize tiny pty, then the
// real size lands. The function must keep polling and return the real size.
func Test_sane_term_size_waits_for_late_resize(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	bin := mockCommand(t, dir, "stty", `
count_file="`+counter+`"
n=$(cat "$count_file" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$count_file"
if [ "$n" -le 2 ]; then
  echo "1 1"
else
  echo "51 188"
fi`)
	env := buildEnv(t, []string{bin})
	out, code := runBashFunc(t, "lib/loading.sh", "_sane_term_size",
		[]string{"24", "80", "10", "0.01"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "51 188" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "51 188")
	}
}

// Static guard: the wrapper must size the tmux session via _sane_term_size,
// not the raw single-shot _detect_term_size, or the stuck single-pane launch
// comes back.
func Test_wrapper_sizes_tmux_launch_with_sane_term_size(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatalf("read wrapper.sh: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, `<<< "$(_sane_term_size)"`) {
		t.Error("wrapper.sh must read _tmux_rows/_tmux_cols from _sane_term_size (pty-size race guard)")
	}
	if strings.Contains(src, `<<< "$(_detect_term_size)"`) {
		t.Error("wrapper.sh must not feed raw _detect_term_size output to tmux new-session -x/-y")
	}
}
