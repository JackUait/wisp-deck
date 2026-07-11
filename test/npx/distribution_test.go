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

// packedFiles returns the paths npm would actually publish.
func packedFiles(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("npm", "pack", "--dry-run", "--json")
	cmd.Dir = projectRoot(t)
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

	var paths []string
	for _, f := range packed[0].Files {
		paths = append(paths, f.Path)
	}
	return paths
}

// isPublished reports whether rel is published, either as a file or as a
// directory holding published files.
func isPublished(rel string, published []string) bool {
	for _, p := range published {
		if p == rel || strings.HasPrefix(p, rel+"/") {
			return true
		}
	}
	return false
}

// The launcher can only copy what npm actually publishes. Anything in
// copyDistribution that package.json's "files" leaves out ships as a missing
// file to every npx user.
func TestNpmPack_publishes_everything_the_launcher_copies(t *testing.T) {
	published := packedFiles(t)

	for _, entry := range copyDistributionEntries(t) {
		if !isPublished(entry, published) {
			t.Errorf("copyDistribution copies %q but package.json \"files\" does not publish it", entry)
		}
	}
}

// Every $SHARE_DIR/... path the installed scripts reach for must exist in the
// published package. This is the general form of the dangling-`wisp-deck`
// symlink bug: bin/wisp-deck referenced $SHARE_DIR/bin/wisp-deck-config and
// $SHARE_DIR/defaults, neither of which npm shipped, and `ln -sf` reported
// success anyway.
func TestPublishedPackage_contains_every_path_the_installer_references(t *testing.T) {
	root := projectRoot(t)
	published := packedFiles(t)

	// SHARE_DIR is the install root only for the scripts that run from it.
	// (wrapper.sh reuses the name for the runtime config dir — different thing.)
	scripts := []string{"bin/wisp-deck", "bin/wisp-deck-config"}
	ref := regexp.MustCompile(`\$SHARE_DIR/([A-Za-z0-9._/-]+)`)

	checked := 0
	for _, script := range scripts {
		src, err := os.ReadFile(filepath.Join(root, script))
		if err != nil {
			t.Fatalf("reading %s: %v", script, err)
		}
		for _, m := range ref.FindAllSubmatch(src, -1) {
			rel := strings.TrimRight(string(m[1]), "/")
			// Paths built at runtime (session state, per-session files) aren't
			// shipped; only the static ones the package must carry are.
			if !isPublished(rel, published) {
				t.Errorf("%s references $SHARE_DIR/%s but npm does not publish it", script, rel)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no $SHARE_DIR references found — the guard is not actually checking anything")
	}
}
