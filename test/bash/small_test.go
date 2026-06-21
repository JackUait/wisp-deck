package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// Makefile tests (migrated from test/makefile.bats)
// ============================================================

func TestMakefile_build_creates_binary(t *testing.T) {
	root := projectRoot(t)
	binPath := filepath.Join(root, "bin", "ghost-tab-tui")

	// Clean before test
	runBashSnippet(t, "cd "+root+" && make clean 2>/dev/null || true", nil)

	t.Cleanup(func() {
		runBashSnippet(t, "cd "+root+" && make clean 2>/dev/null || true", nil)
	})

	out, code := runBashSnippet(t, "cd "+root+" && make build", nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Building ghost-tab-tui")
	assertContains(t, out, "Built bin/ghost-tab-tui")

	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("expected bin/ghost-tab-tui to exist, got error: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("expected bin/ghost-tab-tui to be executable, mode=%v", info.Mode())
	}
}

func TestMakefile_clean_removes_binary(t *testing.T) {
	root := projectRoot(t)
	binPath := filepath.Join(root, "bin", "ghost-tab-tui")

	// Build first
	_, code := runBashSnippet(t, "cd "+root+" && make build", nil)
	assertExitCode(t, code, 0)

	t.Cleanup(func() {
		runBashSnippet(t, "cd "+root+" && make clean 2>/dev/null || true", nil)
	})

	// Now clean
	out, code := runBashSnippet(t, "cd "+root+" && make clean", nil)
	assertExitCode(t, code, 0)
	_ = out

	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Errorf("expected bin/ghost-tab-tui to be removed after make clean")
	}
}

func TestMakefile_help_shows_targets(t *testing.T) {
	root := projectRoot(t)

	out, code := runBashSnippet(t, "cd "+root+" && make help", nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "make build")
	assertContains(t, out, "make install")
	assertContains(t, out, "make test")
}

// ============================================================
// Setup tests (migrated from test/setup.bats)
// ============================================================

func TestSetup_resolve_share_dir_returns_brew_share_when_in_brew_prefix(t *testing.T) {
	out, code := runBashFunc(t, "lib/setup.sh", "resolve_share_dir",
		[]string{"/opt/homebrew/bin", "/opt/homebrew"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/opt/homebrew/share/ghost-tab" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "/opt/homebrew/share/ghost-tab")
	}
}

func TestSetup_resolve_share_dir_returns_parent_dir_when_not_in_brew_prefix(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "ghost-tab", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	out, code := runBashFunc(t, "lib/setup.sh", "resolve_share_dir",
		[]string{binDir, ""}, nil)
	assertExitCode(t, code, 0)
	expected := filepath.Join(dir, "ghost-tab")
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestSetup_resolve_share_dir_returns_parent_dir_when_brew_prefix_is_empty(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "ghost-tab", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	out, code := runBashFunc(t, "lib/setup.sh", "resolve_share_dir",
		[]string{binDir, ""}, nil)
	assertExitCode(t, code, 0)
	expected := filepath.Join(dir, "ghost-tab")
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

// ============================================================
// Integration sleep test (migrated from test/integration-sleep.bats)
// ============================================================

func TestIntegration_sleep_feature_manual(t *testing.T) {
	t.Skip("manual test - requires visual inspection")
	// This test documents the expected behavior.
	// Actual integration testing requires running ghost-tab.
}
