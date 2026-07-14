package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The launch critical path must not spawn python3 in bash to pre-read the
// sound preference, so main-menu resolves the sound name itself: an explicit
// --sound-name wins, otherwise the --sound-file document is read directly.
func TestResolveMainMenuSoundName_FlagWins(t *testing.T) {
	got := resolveMainMenuSoundName("Glass", "/nonexistent/file.json")
	if got != "Glass" {
		t.Errorf("resolveMainMenuSoundName with explicit flag = %q, want Glass", got)
	}
}

func TestResolveMainMenuSoundName_ReadsSoundFileWhenFlagEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-features.json")
	if err := os.WriteFile(path, []byte(`{"sound": true, "sound_name": "Ping"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := resolveMainMenuSoundName("", path)
	if got != "Ping" {
		t.Errorf("resolveMainMenuSoundName from sound file = %q, want Ping", got)
	}
}

func TestResolveMainMenuSoundName_EmptyWhenDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-features.json")
	if err := os.WriteFile(path, []byte(`{"sound": false, "sound_name": "Ping"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveMainMenuSoundName("", path); got != "" {
		t.Errorf("resolveMainMenuSoundName for disabled sound = %q, want empty", got)
	}
}

func TestResolveMainMenuSoundName_EmptyWhenNoFile(t *testing.T) {
	if got := resolveMainMenuSoundName("", filepath.Join(t.TempDir(), "missing.json")); got != "" {
		t.Errorf("resolveMainMenuSoundName with missing file = %q, want empty", got)
	}
}
