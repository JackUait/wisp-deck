package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func TestMainMenuSoundCommand_UsesAuditedPlayerAndPath(t *testing.T) {
	gotName, gotArgs, ok := mainMenuSoundCommand("Glass")
	if !ok {
		t.Fatal("allowlisted sound returned no command")
	}
	if gotName != "/usr/bin/afplay" {
		t.Fatalf("executable = %q, want /usr/bin/afplay", gotName)
	}
	wantArgs := []string{"/System/Library/Sounds/Glass.aiff"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
}

func TestMainMenuSoundPreview_ReturnsDeferredCommandForAllowlistedSound(t *testing.T) {
	cmd := mainMenuSoundPreview("Glass")
	if cmd == nil {
		t.Fatal("allowlisted sound returned no preview command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("preview command returned unexpected message: %#v", msg)
	}
}

func TestMainMenuSoundPreview_RejectsEveryNonAllowlistedName(t *testing.T) {
	for _, name := range []string{"", "../../tmp/evil", "glass", "NotASystemSound"} {
		if cmd := mainMenuSoundPreview(name); cmd != nil {
			t.Errorf("preview(%q) returned a command", name)
		}
		if _, _, ok := mainMenuSoundCommand(name); ok {
			t.Errorf("mainMenuSoundCommand(%q) accepted an invalid name", name)
		}
	}
}

func TestRunMainMenuSound_TestBinaryCannotLaunchProcesses(t *testing.T) {
	var calls int
	err := runMainMenuSoundWith("Glass", func(string, ...string) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("runMainMenuSoundWith: %v", err)
	}
	if calls != 0 {
		t.Fatalf("test binary invoked a host process runner %d times", calls)
	}
}

func TestMainMenuSoundProcessAllowed_RequiresProductionBuildOutsideTests(t *testing.T) {
	tests := map[string]struct {
		testBinary bool
		capability string
		want       bool
	}{
		"ordinary go build": {
			capability: "disabled",
		},
		"go test with production capability": {
			testBinary: true,
			capability: "enabled",
		},
		"production build": {
			capability: "enabled",
			want:       true,
		},
		"unknown capability": {
			capability: "anything-else",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := mainMenuSoundProcessAllowed(test.testBinary, test.capability); got != test.want {
				t.Fatalf("mainMenuSoundProcessAllowed() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestMainMenuSoundPreviewCapability_LinkerValue(t *testing.T) {
	want := os.Getenv("WISP_DECK_EXPECT_SOUND_PREVIEW_CAPABILITY")
	if want == "" {
		want = "disabled"
	}
	if SoundPreviewCapability != want {
		t.Fatalf(
			"SoundPreviewCapability = %q, want %q",
			SoundPreviewCapability,
			want,
		)
	}
}

func TestBuildMainMenuModel_SoundCyclingHasNoPreviewCapability(t *testing.T) {
	projectsFile := filepath.Join(t.TempDir(), "projects")
	if err := os.WriteFile(projectsFile, []byte("proj:/tmp/proj\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldProjects := mainMenuProjectsFile
	oldTools := mainMenuAITools
	oldTool := mainMenuAITool
	oldGhost := mainMenuGhostDisplay
	oldSound := mainMenuSoundName
	oldSoundFile := mainMenuSoundFile
	t.Cleanup(func() {
		mainMenuProjectsFile = oldProjects
		mainMenuAITools = oldTools
		mainMenuAITool = oldTool
		mainMenuGhostDisplay = oldGhost
		mainMenuSoundName = oldSound
		mainMenuSoundFile = oldSoundFile
	})
	mainMenuProjectsFile = projectsFile
	mainMenuAITools = "claude"
	mainMenuAITool = "claude"
	mainMenuGhostDisplay = "none"
	mainMenuSoundName = ""
	mainMenuSoundFile = ""

	model, err := buildMainMenuModel()
	if err != nil {
		t.Fatalf("buildMainMenuModel: %v", err)
	}
	model.EnterSettings()
	// Idle Sound is the sixth visual row (after Appearance and Tools).
	for range 5 {
		if _, cmd := model.Update(tea.KeyMsg{Type: tea.KeyDown}); cmd != nil {
			t.Fatal("settings navigation unexpectedly returned a command")
		}
	}
	before := model.SoundName()
	if _, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("non-interactive builder returned a sound preview command")
	}
	if model.SoundName() == before {
		t.Fatal("sound setting did not cycle, so builder silence was not exercised")
	}
}
