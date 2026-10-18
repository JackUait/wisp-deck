package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Remote control row reads and writes Claude Code's own
// remoteControlAtStartup key, so the row and Claude's /config never disagree.

func writeTestSettings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadRemoteControl(t *testing.T) {
	cases := map[string]bool{
		`{"remoteControlAtStartup": true}`:  true,
		`{"remoteControlAtStartup": false}`: false,
		`{"model": "x"}`:                    false,
		`not json`:                          false,
	}
	for body, want := range cases {
		if got := readRemoteControl(writeTestSettings(t, body)); got != want {
			t.Errorf("readRemoteControl(%s) = %v, want %v", body, got, want)
		}
	}
	if readRemoteControl(filepath.Join(t.TempDir(), "missing.json")) {
		t.Error("a missing file must read as off")
	}
}

func TestWriteRemoteControl_flipsAnExistingKeyAndKeepsTheRestByteForByte(t *testing.T) {
	body := "{\n  \"statusLine\": {\"command\": \"a && b > /dev/null\"},\n  \"remoteControlAtStartup\": false,\n  \"model\": \"x\"\n}\n"
	path := writeTestSettings(t, body)
	if err := writeRemoteControl(path, true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := strings.Replace(body, `"remoteControlAtStartup": false`, `"remoteControlAtStartup": true`, 1)
	if string(got) != want {
		t.Fatalf("file rewritten beyond the one value:\n%s", got)
	}
}

func TestWriteRemoteControl_addsAMissingKey(t *testing.T) {
	for _, body := range []string{"{\n  \"model\": \"x\"\n}\n", "{}", "  {  }\n"} {
		path := writeTestSettings(t, body)
		if err := writeRemoteControl(path, true); err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if !readRemoteControl(path) {
			got, _ := os.ReadFile(path)
			t.Errorf("%q: key not added:\n%s", body, got)
		}
	}
}

func TestWriteRemoteControl_createsAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := writeRemoteControl(path, true); err != nil {
		t.Fatal(err)
	}
	if !readRemoteControl(path) {
		t.Fatal("new file does not turn Remote Control on")
	}
}

// A nested key of the same name must not be the one that gets flipped.
func TestWriteRemoteControl_ignoresANestedKey(t *testing.T) {
	body := `{"env": {"remoteControlAtStartup": false}, "remoteControlAtStartup": false}`
	path := writeTestSettings(t, body)
	if err := writeRemoteControl(path, true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	var parsed struct {
		Env map[string]bool `json:"env"`
		RC  bool            `json:"remoteControlAtStartup"`
	}
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatal(err)
	}
	if !parsed.RC || parsed.Env["remoteControlAtStartup"] {
		t.Fatalf("wrong key flipped:\n%s", got)
	}
}

func TestWriteRemoteControl_refusesAFileItCannotParse(t *testing.T) {
	body := `{"model": "x",}`
	path := writeTestSettings(t, body)
	if err := writeRemoteControl(path, true); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
	got, _ := os.ReadFile(path)
	if string(got) != body {
		t.Fatalf("an unparseable file was modified:\n%s", got)
	}
}

// Every account dir links settings.json to ~/.claude/settings.json; replacing
// the link with a file would cut that account off from shared settings.
func TestWriteRemoteControl_writesThroughASymlinkAndKeepsItsMode(t *testing.T) {
	target := writeTestSettings(t, `{"model": "x"}`)
	link := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeRemoteControl(link, true); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced")
	}
	if !readRemoteControl(target) {
		t.Fatal("the link target was not updated")
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestRemoteControlRow_togglesClaudeSettings(t *testing.T) {
	path := writeTestSettings(t, `{"remoteControlAtStartup": false}`)
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetClaudeSettingsFile(path)

	found := false
	for _, idx := range m.settingsItemOrder() {
		if idx == rowRemoteControl {
			found = true
		}
	}
	if !found {
		t.Fatal("rowRemoteControl missing from settingsItemOrder")
	}

	m.settingsSelected = rowRemoteControl
	m.settingsEnter()
	if !m.RemoteControlEnabled() || !readRemoteControl(path) {
		t.Fatal("enter did not turn Remote Control on")
	}
	m.settingsValueRight()
	if m.RemoteControlEnabled() || readRemoteControl(path) {
		t.Fatal("right did not turn Remote Control off")
	}
	m.settingsValueLeft()
	if !m.RemoteControlEnabled() || !readRemoteControl(path) {
		t.Fatal("left did not turn Remote Control on")
	}
}

func TestRemoteControlRow_loadsTheCurrentValue(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetClaudeSettingsFile(writeTestSettings(t, `{"remoteControlAtStartup": true}`))
	if !m.RemoteControlEnabled() {
		t.Fatal("row did not load the saved value")
	}
}

// With no file set the row must never write, so tests stay off ~/.claude.
func TestRemoteControlRow_withoutAFileStaysPut(t *testing.T) {
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.CycleRemoteControl()
	if m.RemoteControlEnabled() {
		t.Fatal("toggle with no settings file must not change state")
	}
}

// A failed write leaves the row showing what the file really holds.
func TestRemoteControlRow_keepsStateWhenTheWriteFails(t *testing.T) {
	path := writeTestSettings(t, `{"model": "x",}`)
	m := NewMainMenu(nil, []string{"claude"}, "claude", "animated")
	m.SetClaudeSettingsFile(path)
	m.CycleRemoteControl()
	if m.RemoteControlEnabled() {
		t.Fatal("row flipped although the write failed")
	}
}
