package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Runtime sound sites are deliberately few and every non-preview owner must
// make its read-plus-play transaction under the shared preference lock.
func TestIdleSoundRuntimeSitesUseSharedLiveGate(t *testing.T) {
	root := projectRoot(t)
	allowed := map[string]bool{
		filepath.Join(root, "lib", "notification-setup.sh"):                 true,
		filepath.Join(root, "internal", "tui", "mainmenu.go"):               true,
		filepath.Join(root, "cmd", "wisp-deck-tui", "claude_background.go"): true,
	}
	paths := []string{
		filepath.Join(root, "lib"),
		filepath.Join(root, "templates"),
		filepath.Join(root, "bin"),
		filepath.Join(root, "internal"),
		filepath.Join(root, "cmd"),
		filepath.Join(root, "wrapper.sh"),
	}
	markers := []string{"afplay", "/System/Library/Sounds", "NSSound", "AudioServicesPlaySystemSound"}
	var violations []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		visit := func(candidate string, entry os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || strings.HasSuffix(candidate, "_test.go") || allowed[candidate] {
				return nil
			}
			data, err := os.ReadFile(candidate)
			if err != nil {
				return err
			}
			for _, marker := range markers {
				if strings.Contains(string(data), marker) {
					violations = append(violations, candidate)
					break
				}
			}
			return nil
		}
		if info.IsDir() {
			if err := filepath.Walk(path, visit); err != nil {
				t.Fatal(err)
			}
		} else if err := visit(path, info, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(violations) != 0 {
		t.Fatalf("new runtime audio sites bypass the live preference gate: %s", strings.Join(violations, ", "))
	}

	expectedCounts := map[string]map[string]int{
		filepath.Join(root, "lib", "notification-setup.sh"): {
			"afplay": 2, "/System/Library/Sounds": 1, "NSSound": 0, "AudioServicesPlaySystemSound": 0,
		},
		filepath.Join(root, "internal", "tui", "mainmenu.go"): {
			"afplay": 2, "/System/Library/Sounds": 1, "NSSound": 0, "AudioServicesPlaySystemSound": 0,
		},
		filepath.Join(root, "cmd", "wisp-deck-tui", "claude_background.go"): {
			"afplay": 1, "/System/Library/Sounds": 1, "NSSound": 0, "AudioServicesPlaySystemSound": 0,
		},
	}
	for path, counts := range expectedCounts {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for marker, want := range counts {
			if got := strings.Count(string(data), marker); got != want {
				t.Fatalf("%s contains %d %q markers, want exactly %d audited occurrences", path, got, marker, want)
			}
		}
	}

	shell, err := os.ReadFile(filepath.Join(root, "lib", "notification-setup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shell), `/usr/bin/lockf -k "$lock_file"`) ||
		!strings.Contains(string(shell), `sound_name="$(get_sound_name`) {
		t.Fatal("foreground idle playback must lock and re-read its live preference")
	}
	background, err := os.ReadFile(filepath.Join(root, "cmd", "wisp-deck-tui", "claude_background.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(background), "soundpref.WithExclusiveLock(features") ||
		!strings.Contains(string(background), "claudeBackgroundSoundPreference(features)") {
		t.Fatal("background playback must use the same live preference transaction")
	}
	menu, err := os.ReadFile(filepath.Join(root, "internal", "tui", "mainmenu.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(menu), `exec.Command("afplay"`) != 1 {
		t.Fatal("Settings preview must remain the only direct TUI afplay site")
	}
	if strings.Count(string(background), `"/usr/bin/afplay"`) != 1 {
		t.Fatal("background notifier must have exactly one locked afplay site")
	}
}
