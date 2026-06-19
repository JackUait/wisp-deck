package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// _gt_pick_marked_pane reads "<index> <flag>" lines and prints the marked index.
func TestPickMarkedPane_returns_marked_index(t *testing.T) {
	out, code := runBashFuncWithStdin(t, "lib/screenshot.sh", "_gt_pick_marked_pane",
		nil, nil, "0 \n2 1\n1 \n")
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "2" {
		t.Errorf("got %q, want 2", strings.TrimSpace(out))
	}
}

func TestPickMarkedPane_none_marked_returns_error(t *testing.T) {
	out, code := runBashFuncWithStdin(t, "lib/screenshot.sh", "_gt_pick_marked_pane",
		nil, nil, "0 \n1 \n2 \n")
	if code == 0 {
		t.Errorf("expected non-zero when nothing marked, got 0; out=%q", out)
	}
}

// tmuxAiPaneMock returns a tmux mock whose `list-panes` answers two formats:
// the @gt_ai marker query and the geometry query. binDir/tmux is the mock.
func tmuxAiPaneMock(t *testing.T, marker, geometry string) []string {
	t.Helper()
	dir := t.TempDir()
	body := `if [ "$1" = "list-panes" ]; then
  case "$*" in
    *pane_at_right*) printf '%s\n' "$GT_GEOM" ;;
    *@gt_ai*)        printf '%s\n' "$GT_MARK" ;;
  esac
fi
exit 0`
	binDir := mockCommand(t, dir, "tmux", body)
	return []string{filepath.Join(binDir, "tmux"),
		"GT_MARK=" + marker, "GT_GEOM=" + geometry}
}

// gt_ai_pane prefers the @gt_ai-marked pane.
func TestAiPane_prefers_marked_pane(t *testing.T) {
	m := tmuxAiPaneMock(t,
		"0 \n1 1\n2 ",                  // marker: pane 1 is the AI pane
		"0 0 1 0\n1 0 0 1\n2 1 1 1\n")  // geometry would say pane 2
	tmuxPath, env := m[0], m[1:]
	out, code := runBashFunc(t, "lib/screenshot.sh", "gt_ai_pane",
		[]string{tmuxPath, "sess"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "1" {
		t.Errorf("marked pane should win; got %q want 1", strings.TrimSpace(out))
	}
}

// When no pane is marked (e.g. a session from an older ghost-tab), gt_ai_pane
// must fall back to the full-height pane on the right edge -- where the AI tool
// lives -- NOT a fixed index. Regression: the old fallback returned index 1
// (the spare shell), stealing focus from / mis-targeting Claude at index 2.
func TestAiPane_falls_back_to_full_height_right_pane(t *testing.T) {
	m := tmuxAiPaneMock(t,
		"0 \n1 \n2 \n",                 // no marker
		"0 0 1 0\n1 0 0 1\n2 1 1 1\n")  // pane 2 is full-height on the right
	tmuxPath, env := m[0], m[1:]
	out, code := runBashFunc(t, "lib/screenshot.sh", "gt_ai_pane",
		[]string{tmuxPath, "sess"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "2" {
		t.Errorf("unmarked fallback should pick the full-height right pane; got %q want 2", strings.TrimSpace(out))
	}
}

// gt_latest_screenshot <dir> prints the newest image file in dir.
func TestLatestScreenshot_returns_newest_image(t *testing.T) {
	dir := t.TempDir()
	older := writeTempFile(t, dir, "Screen Shot old.png", "old")
	newer := writeTempFile(t, dir, "Screen Shot new.png", "new")
	// Force mtimes: older is older, newer is newer.
	now := time.Now()
	os.Chtimes(older, now.Add(-2*time.Minute), now.Add(-2*time.Minute))
	os.Chtimes(newer, now, now)

	out, code := runBashFunc(t, "lib/screenshot.sh", "gt_latest_screenshot",
		[]string{dir}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Screen Shot new.png")
	assertNotContains(t, out, "Screen Shot old.png")
}

// Non-image files must be ignored.
func TestLatestScreenshot_ignores_non_images(t *testing.T) {
	dir := t.TempDir()
	img := writeTempFile(t, dir, "shot.png", "img")
	txt := writeTempFile(t, dir, "notes.txt", "text")
	now := time.Now()
	os.Chtimes(img, now.Add(-1*time.Minute), now.Add(-1*time.Minute))
	os.Chtimes(txt, now, now) // newer, but not an image

	out, code := runBashFunc(t, "lib/screenshot.sh", "gt_latest_screenshot",
		[]string{dir}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "shot.png")
	assertNotContains(t, out, "notes.txt")
}

// Empty / no-image dir returns non-zero so the binding can no-op.
func TestLatestScreenshot_no_images_returns_error(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "notes.txt", "text")
	out, code := runBashFunc(t, "lib/screenshot.sh", "gt_latest_screenshot",
		[]string{dir}, nil)
	if code == 0 {
		t.Errorf("expected non-zero exit when no images present, got 0; out=%q", out)
	}
}

// gt_screenshot_dir uses the macOS screencapture location when set.
func TestScreenshotDir_uses_configured_location(t *testing.T) {
	dir := t.TempDir()
	shotDir := filepath.Join(dir, "Shots")
	if err := os.MkdirAll(shotDir, 0755); err != nil {
		t.Fatal(err)
	}
	bin := mockCommand(t, dir, "defaults", `echo "`+shotDir+`"`)
	env := buildEnv(t, []string{bin})
	out, code := runBashFunc(t, "lib/screenshot.sh", "gt_screenshot_dir", nil, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, shotDir)
}

// gt_screenshot_dir falls back to ~/Desktop when location is unset.
func TestScreenshotDir_defaults_to_desktop(t *testing.T) {
	home := t.TempDir()
	desktop := filepath.Join(home, "Desktop")
	if err := os.MkdirAll(desktop, 0755); err != nil {
		t.Fatal(err)
	}
	// defaults read prints nothing and exits non-zero (key absent).
	bin := mockCommand(t, home, "defaults", `exit 1`)
	env := buildEnv(t, []string{bin}, "HOME="+home)
	out, code := runBashFunc(t, "lib/screenshot.sh", "gt_screenshot_dir", nil, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, desktop)
}
