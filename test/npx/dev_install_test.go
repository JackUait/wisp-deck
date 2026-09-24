package npx_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// seedDevInstall fakes an install that scripts/sync-dev-install.sh wrote from
// a commit newer than the published package.
func seedDevInstall(t *testing.T) (home, installDir string) {
	t.Helper()
	home = t.TempDir()
	installDir = filepath.Join(home, ".local", "share", "wisp-deck")
	for rel, body := range map[string]string{
		".dev-install":         "0123456789abcdef\n",
		".version":             "9.9.9\n",
		"VERSION":              "9.9.9\n",
		"wrapper.sh":           "# dev wrapper\n",
		"lib/tui.sh":           "# dev lib newer than npm\n",
		"bin/wisp-deck":        "# dev installer\n",
		"bin/wisp-deck-config": "# dev config\n",
	} {
		path := filepath.Join(installDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, installDir
}

func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestLauncher_keeps_a_dev_install(t *testing.T) {
	home, installDir := seedDevInstall(t)
	env := append(os.Environ(),
		"HOME="+home,
		"WISP_DECK_INSTALL_DIR="+installDir,
		"WISP_DECK_SKIP_EXEC=1",
		"WISP_DECK_FORCE_INSTALL=",
		// No SKIP_TUI_DOWNLOAD: a dev install must not reach ensureTuiBinary,
		// which would replace a HEAD-built binary with the published one.
		"WISP_DECK_SKIP_TUI_DOWNLOAD=",
		"WISP_DECK_MOCK_ARCH=unsupported-arch",
	)

	stdout, stderr, code := runLauncher(t, env)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. stdout: %s stderr: %s", code, stdout, stderr)
	}
	if got := readTrimmed(t, filepath.Join(installDir, "lib", "tui.sh")); got != "# dev lib newer than npm" {
		t.Errorf("dev lib was overwritten: %q", got)
	}
	if got := readTrimmed(t, filepath.Join(installDir, ".version")); got != "9.9.9" {
		t.Errorf(".version was rewritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(installDir, ".dev-install")); err != nil {
		t.Errorf(".dev-install marker was removed: %v", err)
	}
	if strings.Contains(stdout, "wisp-deck-tui") {
		t.Errorf("dev install must skip the TUI download, got: %s", stdout)
	}
	if !strings.Contains(stdout, ".dev-install") || !strings.Contains(stdout, "WISP_DECK_FORCE_INSTALL=1") {
		t.Errorf("expected a notice naming the marker and the override, got: %s", stdout)
	}
}

func TestLauncher_force_install_replaces_a_dev_install(t *testing.T) {
	home, installDir := seedDevInstall(t)
	env := append(os.Environ(),
		"HOME="+home,
		"WISP_DECK_INSTALL_DIR="+installDir,
		"WISP_DECK_SKIP_TUI_DOWNLOAD=1",
		"WISP_DECK_SKIP_EXEC=1",
		"WISP_DECK_FORCE_INSTALL=1",
	)

	stdout, stderr, code := runLauncher(t, env)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. stdout: %s stderr: %s", code, stdout, stderr)
	}
	root := projectRoot(t)
	if got, want := readTrimmed(t, filepath.Join(installDir, "lib", "tui.sh")),
		readTrimmed(t, filepath.Join(root, "lib", "tui.sh")); got != want {
		t.Error("forced install did not replace the dev lib with the package copy")
	}
	if got, want := readTrimmed(t, filepath.Join(installDir, ".version")),
		readTrimmed(t, filepath.Join(root, "VERSION")); got != want {
		t.Errorf(".version = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(installDir, ".dev-install")); !os.IsNotExist(err) {
		t.Errorf("forced install must drop the .dev-install marker, stat err: %v", err)
	}
}

// The sync script and the launcher must ship the same entries, or a dev
// install drifts from what an npm user gets.
func TestSyncDevInstall_entries_match_copyDistribution(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(projectRoot(t), "scripts", "sync-dev-install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)DIST_ENTRIES=\((.*?)\)`).FindSubmatch(src)
	if block == nil {
		t.Fatal("could not find DIST_ENTRIES in scripts/sync-dev-install.sh")
	}
	got := strings.Fields(string(block[1]))
	if want := copyDistributionEntries(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("sync-dev-install.sh DIST_ENTRIES = %v, copyDistribution = %v", got, want)
	}
}
