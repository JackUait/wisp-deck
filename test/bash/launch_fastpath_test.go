package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// screenshotLibSnippet sources screenshot.sh then runs body.
func screenshotLibSnippet(t *testing.T, body string) string {
	t.Helper()
	lib := filepath.Join(projectRoot(t), "lib", "screenshot.sh")
	return fmt.Sprintf("source %q && %s", lib, body)
}

func TestFastPath_claude_filter_prefix_probes_once_then_caches(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	counter := filepath.Join(tmpDir, "probe-count")
	// Mock wisp-deck-tui: records each invocation and succeeds (supports filter).
	binDir := mockCommand(t, tmpDir, "wisp-deck-tui",
		fmt.Sprintf("echo x >> %q\nexit 0", counter))
	env := buildEnv(t, []string{binDir})

	snippet := screenshotLibSnippet(t, fmt.Sprintf(`gt_claude_filter_prefix %q`, cacheDir))

	out1, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out1, "wisp-deck-tui screenshot-filter -- ")

	out2, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	assertContains(t, out2, "wisp-deck-tui screenshot-filter -- ")

	data, _ := os.ReadFile(counter)
	n := strings.Count(string(data), "x")
	if n != 1 {
		t.Errorf("probe ran %d times, want 1 (result should be cached after the first launch)", n)
	}
}

func TestFastPath_claude_filter_prefix_reprobes_when_binary_changes(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	counter := filepath.Join(tmpDir, "probe-count")
	binDir := mockCommand(t, tmpDir, "wisp-deck-tui",
		fmt.Sprintf("echo x >> %q\nexit 0", counter))
	env := buildEnv(t, []string{binDir})
	snippet := screenshotLibSnippet(t, fmt.Sprintf(`gt_claude_filter_prefix %q`, cacheDir))

	runBashSnippet(t, snippet, env)
	// Simulate an install/update: change the binary's mtime so its signature differs.
	binPath := filepath.Join(binDir, "wisp-deck-tui")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(binPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	runBashSnippet(t, snippet, env)

	data, _ := os.ReadFile(counter)
	if n := strings.Count(string(data), "x"); n != 2 {
		t.Errorf("probe ran %d times, want 2 (a changed binary must be re-probed)", n)
	}
}

func TestFastPath_claude_filter_prefix_caches_negative_result(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	counter := filepath.Join(tmpDir, "probe-count")
	// Mock that does NOT support the subcommand (exits non-zero).
	binDir := mockCommand(t, tmpDir, "wisp-deck-tui",
		fmt.Sprintf("echo x >> %q\nexit 1", counter))
	env := buildEnv(t, []string{binDir})
	snippet := screenshotLibSnippet(t, fmt.Sprintf(`gt_claude_filter_prefix %q`, cacheDir))

	out1, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0) // helper must not fail the launch
	if strings.TrimSpace(out1) != "" {
		t.Errorf("unsupported binary should yield no prefix, got %q", out1)
	}
	out2, _ := runBashSnippet(t, snippet, env)
	if strings.TrimSpace(out2) != "" {
		t.Errorf("cached negative result should still yield no prefix, got %q", out2)
	}
	data, _ := os.ReadFile(counter)
	if n := strings.Count(string(data), "x"); n != 1 {
		t.Errorf("probe ran %d times, want 1 (negative result should be cached)", n)
	}
}
