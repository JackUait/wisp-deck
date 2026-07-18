package bash_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// Makefile tests (migrated from test/makefile.bats)
// ============================================================

func TestMakefile_build_creates_binary(t *testing.T) {
	root := projectRoot(t)
	binPath := filepath.Join(root, "bin", "wisp-deck-tui")

	// Clean before test
	runBashSnippet(t, "cd "+root+" && make clean 2>/dev/null || true", nil)

	t.Cleanup(func() {
		runBashSnippet(t, "cd "+root+" && make clean 2>/dev/null || true", nil)
	})

	out, code := runBashSnippet(
		t,
		"cd "+root+" && make build",
		environmentWithoutTestMarker(os.Environ()),
	)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Building wisp-deck-tui")
	assertContains(t, out, "Built bin/wisp-deck-tui")

	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("expected bin/wisp-deck-tui to exist, got error: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("expected bin/wisp-deck-tui to be executable, mode=%v", info.Mode())
	}

	capabilities := readBinaryCapabilities(t, binPath)
	if !capabilities.HostEffectsCompiled || !capabilities.SoundPreviewCompiled {
		t.Fatalf("normal Make build capabilities = %#v, want both compiled", capabilities)
	}
	if capabilities.HostEffectsBoundary != 1 {
		t.Fatalf("normal Make boundary = %d, want 1", capabilities.HostEffectsBoundary)
	}
	requireProductionCapabilities(t, binPath, true)
}

func TestMakefile_buildTestSelectorsCannotBeOverridden(t *testing.T) {
	root := projectRoot(t)
	binPath := filepath.Join(root, "bin", "wisp-deck-tui")
	t.Cleanup(func() {
		runBashSnippet(t, "cd "+root+" && make clean 2>/dev/null || true", nil)
	})

	for _, selector := range []string{"1", "0", ""} {
		t.Run(fmt.Sprintf("selector_%q", selector), func(t *testing.T) {
			command := fmt.Sprintf(
				"cd %q && make clean >/dev/null && make WISP_DECK_TESTING=%q HOST_EFFECTS_CAPABILITY=enabled SOUND_PREVIEW_CAPABILITY=enabled build",
				root,
				selector,
			)
			out, code := runBashSnippet(t, command, nil)
			if code != 0 {
				t.Fatalf("marked Make build failed (%d): %s", code, out)
			}
			capabilities := readBinaryCapabilities(t, binPath)
			if capabilities.HostEffectsCompiled || capabilities.SoundPreviewCompiled {
				t.Fatalf(
					"marked Make selector %q capabilities = %#v, want both disabled",
					selector,
					capabilities,
				)
			}
			if capabilities.HostEffectsBoundary != 1 {
				t.Fatalf("marked Make boundary = %d, want 1", capabilities.HostEffectsBoundary)
			}
			requireProductionCapabilities(t, binPath, false)
		})
	}
}

func TestMakefile_ordinaryGoBuildStaysFailClosed(t *testing.T) {
	root := projectRoot(t)
	binary := filepath.Join(t.TempDir(), "ordinary-go-build")
	command := exec.Command(
		"go",
		"build",
		"-o",
		binary,
		"./cmd/wisp-deck-tui",
	)
	command.Dir = root
	command.Env = environmentWithoutTestMarker(os.Environ())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ordinary go build: %v\n%s", err, output)
	}
	capabilities := readBinaryCapabilities(t, binary)
	if capabilities.HostEffectsCompiled || capabilities.SoundPreviewCompiled {
		t.Fatalf("ordinary go build capabilities = %#v, want both disabled", capabilities)
	}
	if capabilities.HostEffectsBoundary != 1 {
		t.Fatalf("ordinary go build boundary = %d, want 1", capabilities.HostEffectsBoundary)
	}
	requireProductionCapabilities(t, binary, false)
}

func TestEnabledChildDetectsExactTestAncestorMarker(t *testing.T) {
	root := projectRoot(t)
	binary := filepath.Join(t.TempDir(), "enabled-child")
	build := exec.Command(
		"go",
		"build",
		"-ldflags",
		"-X main.HostEffectsCapability=enabled -X main.SoundPreviewCapability=enabled",
		"-o",
		binary,
		"./cmd/wisp-deck-tui",
	)
	build.Dir = root
	build.Env = environmentWithoutTestMarker(os.Environ())
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build enabled child: %v\n%s", err, output)
	}

	// This non-.test shell is started with the repository test marker, remains
	// the child's direct parent while waiting, and strips the marker only from
	// the child. The exact denial reason must therefore come from structural
	// ancestor environment inspection rather than the child's environment.
	helper := exec.Command(
		"/bin/bash",
		"-c",
		`env -u WISP_DECK_TESTING "$1" capabilities`,
		"host-boundary-helper",
		binary,
	)
	helper.Env = repositoryTestEnvironment(os.Environ())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	helper.Stdout = &stdout
	helper.Stderr = &stderr
	if err := helper.Run(); err != nil {
		t.Fatalf("run enabled child through marked helper: %v\n%s", err, stderr.String())
	}

	var capabilities testBinaryCapabilities
	if err := json.Unmarshal(stdout.Bytes(), &capabilities); err != nil {
		t.Fatalf("decode capabilities: %v\n%s", err, stdout.String())
	}
	want := testBinaryCapabilities{
		HostEffectsCompiled:     true,
		SoundPreviewCompiled:    true,
		HostEffectsBoundary:     1,
		HostEffectsAllowed:      false,
		HostEffectsDenialReason: "test_ancestor_marker",
	}
	if capabilities != want {
		t.Fatalf("enabled child capabilities = %#v, want %#v", capabilities, want)
	}
}

type testBinaryCapabilities struct {
	HostEffectsCompiled     bool   `json:"host_effects_compiled"`
	SoundPreviewCompiled    bool   `json:"sound_preview_compiled"`
	HostEffectsBoundary     int    `json:"host_effects_boundary"`
	HostEffectsAllowed      bool   `json:"host_effects_allowed"`
	HostEffectsDenialReason string `json:"host_effects_denial_reason"`
}

func readBinaryCapabilities(t *testing.T, binary string) testBinaryCapabilities {
	t.Helper()
	command := exec.Command(binary, "capabilities")
	command.Env = environmentWithoutTestMarker(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s capabilities: %v\n%s", binary, err, output)
	}
	var capabilities testBinaryCapabilities
	if err := json.Unmarshal(output, &capabilities); err != nil {
		t.Fatalf("decode %s capabilities: %v\n%s", binary, err, output)
	}
	return capabilities
}

func requireProductionCapabilities(t *testing.T, binary string, wantSuccess bool) {
	t.Helper()
	command := exec.Command(binary, "capabilities", "--require-production")
	command.Env = environmentWithoutTestMarker(os.Environ())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if wantSuccess && err != nil {
		t.Fatalf(
			"%s --require-production rejected valid artifact: %v\n%s",
			binary,
			err,
			stderr.String(),
		)
	}
	if !wantSuccess && err == nil {
		t.Fatalf("%s --require-production accepted invalid artifact", binary)
	}
	var capabilities testBinaryCapabilities
	if decodeErr := json.Unmarshal(stdout.Bytes(), &capabilities); decodeErr != nil {
		t.Fatalf(
			"%s --require-production did not emit JSON before status: %v\nstdout=%s\nstderr=%s",
			binary,
			decodeErr,
			stdout.String(),
			stderr.String(),
		)
	}
}

func environmentWithoutTestMarker(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "WISP_DECK_TESTING=") {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func TestMakefile_clean_removes_binary(t *testing.T) {
	root := projectRoot(t)
	binPath := filepath.Join(root, "bin", "wisp-deck-tui")

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
		t.Errorf("expected bin/wisp-deck-tui to be removed after make clean")
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
	if strings.TrimSpace(out) != "/opt/homebrew/share/wisp-deck" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "/opt/homebrew/share/wisp-deck")
	}
}

func TestSetup_resolve_share_dir_returns_parent_dir_when_not_in_brew_prefix(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "wisp-deck", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	out, code := runBashFunc(t, "lib/setup.sh", "resolve_share_dir",
		[]string{binDir, ""}, nil)
	assertExitCode(t, code, 0)
	expected := filepath.Join(dir, "wisp-deck")
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestSetup_resolve_share_dir_returns_parent_dir_when_brew_prefix_is_empty(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "wisp-deck", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	out, code := runBashFunc(t, "lib/setup.sh", "resolve_share_dir",
		[]string{binDir, ""}, nil)
	assertExitCode(t, code, 0)
	expected := filepath.Join(dir, "wisp-deck")
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
	// Actual integration testing requires running wisp-deck.
}
