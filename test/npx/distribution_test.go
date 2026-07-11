package npx_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// copyDistributionEntries extracts the entry list that npx-wisp-deck.js copies
// into the install dir, so the tests below stay in sync when it grows.
func copyDistributionEntries(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(projectRoot(t), "bin", "npx-wisp-deck.js"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)const entries = \[(.*?)\]`).FindSubmatch(src)
	if block == nil {
		t.Fatal("could not find copyDistribution entries in npx-wisp-deck.js")
	}
	var entries []string
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(block[1], -1) {
		entries = append(entries, string(m[1]))
	}
	if len(entries) == 0 {
		t.Fatal("no entries parsed from copyDistribution")
	}
	return entries
}

// The installer symlinks ~/.local/bin/wisp-deck -> <install>/bin/wisp-deck-config
// and seeds the default Claude configs from <install>/defaults. If the launcher
// doesn't copy them, the `wisp-deck` command is a dangling symlink and the
// default configs never appear.
func TestLauncher_copies_config_command_and_defaults(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, ".local", "share", "wisp-deck")

	env := append(os.Environ(),
		"HOME="+home,
		"WISP_DECK_INSTALL_DIR="+installDir,
		"WISP_DECK_SKIP_TUI_DOWNLOAD=1",
		"WISP_DECK_SKIP_EXEC=1",
	)

	_, stderr, code := runLauncher(t, env)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. stderr: %s", code, stderr)
	}

	for _, rel := range []string{
		"bin/wisp-deck-config",
		"defaults/claude-configs.list",
	} {
		if _, err := os.Stat(filepath.Join(installDir, rel)); err != nil {
			t.Errorf("expected %s in install dir: %v", rel, err)
		}
	}

	configs, err := filepath.Glob(filepath.Join(installDir, "defaults", "claude-configs", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) == 0 {
		t.Error("expected default claude-configs JSON files in install dir, found none")
	}
}

// The launcher can only copy what npm actually publishes. Anything in
// copyDistribution that package.json's "files" leaves out ships as a missing
// file to every npx user.
func TestNpmPack_publishes_everything_the_launcher_copies(t *testing.T) {
	root := projectRoot(t)

	cmd := exec.Command("npm", "pack", "--dry-run", "--json")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("npm pack --dry-run failed: %v", err)
	}

	var packed []struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &packed); err != nil {
		t.Fatalf("parsing npm pack output: %v", err)
	}
	if len(packed) == 0 {
		t.Fatal("npm pack reported no package")
	}

	for _, entry := range copyDistributionEntries(t) {
		found := false
		for _, f := range packed[0].Files {
			if f.Path == entry || strings.HasPrefix(f.Path, entry+"/") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("copyDistribution copies %q but package.json \"files\" does not publish it", entry)
		}
	}
}
