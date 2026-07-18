package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

func TestMainMenuSoundPreview_UsesAuditedPlayerAfterCommandRuns(t *testing.T) {
	var calls int
	var gotName string
	var gotArgs []string
	preview := mainMenuSoundPreview(func(name string, args ...string) error {
		calls++
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	})

	cmd := preview("Glass")

	if cmd == nil {
		t.Fatal("allowlisted sound returned no preview command")
	}
	if calls != 0 {
		t.Fatalf("runner called while constructing preview command: %d calls", calls)
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("preview command returned unexpected message: %#v", msg)
	}
	if calls != 1 {
		t.Fatalf("runner calls = %d, want 1", calls)
	}
	if gotName != "/usr/bin/afplay" {
		t.Fatalf("runner executable = %q, want /usr/bin/afplay", gotName)
	}
	wantArgs := []string{"/System/Library/Sounds/Glass.aiff"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("runner args = %v, want %v", gotArgs, wantArgs)
	}
}

func TestMainMenuSoundPreview_RejectsEveryNonAllowlistedName(t *testing.T) {
	var calls int
	preview := mainMenuSoundPreview(func(string, ...string) error {
		calls++
		return nil
	})

	for _, name := range []string{"", "../../tmp/evil", "glass", "NotASystemSound"} {
		if cmd := preview(name); cmd != nil {
			t.Errorf("preview(%q) returned a command", name)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid names reached runner: %d calls", calls)
	}
}

func TestMainMenuSoundPreview_ContainsRunnerErrors(t *testing.T) {
	wantErr := errors.New("player unavailable")
	preview := mainMenuSoundPreview(func(string, ...string) error {
		return wantErr
	})

	cmd := preview("Tink")

	if cmd == nil {
		t.Fatal("allowlisted sound returned no preview command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("runner error escaped as message: %#v", msg)
	}
}
