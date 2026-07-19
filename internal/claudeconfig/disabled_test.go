package claudeconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisabledFile_isSiblingOfListFile(t *testing.T) {
	got := DisabledFile("/cfg/claude-configs.list")
	if got != "/cfg/claude-configs.disabled" {
		t.Errorf("DisabledFile = %q, want /cfg/claude-configs.disabled", got)
	}
}

func TestDisabledFile_emptyListPathIsEmpty(t *testing.T) {
	if got := DisabledFile(""); got != "" {
		t.Errorf("DisabledFile(\"\") = %q, want \"\"", got)
	}
}

func TestLoadDisabled_missingFileMeansNothingDisabled(t *testing.T) {
	got := LoadDisabled(filepath.Join(t.TempDir(), "absent"))
	if len(got) != 0 {
		t.Errorf("LoadDisabled(missing) = %v, want empty", got)
	}
}

func TestToggleDisabled_flipsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-configs.disabled")

	now, err := ToggleDisabled(path, "zhipu-glm.json")
	if err != nil {
		t.Fatal(err)
	}
	if !now {
		t.Error("first toggle must report disabled=true")
	}
	if !LoadDisabled(path)["zhipu-glm.json"] {
		t.Error("disabled state must persist to the file")
	}

	now, err = ToggleDisabled(path, "zhipu-glm.json")
	if err != nil {
		t.Fatal(err)
	}
	if now {
		t.Error("second toggle must report disabled=false")
	}
	if LoadDisabled(path)["zhipu-glm.json"] {
		t.Error("re-enabling must remove the file entry")
	}
}

func TestToggleDisabled_keepsOtherEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-configs.disabled")
	if _, err := ToggleDisabled(path, "a.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := ToggleDisabled(path, "b.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := ToggleDisabled(path, "a.json"); err != nil {
		t.Fatal(err)
	}
	disabled := LoadDisabled(path)
	if disabled["a.json"] || !disabled["b.json"] {
		t.Errorf("disabled = %v, want only b.json", disabled)
	}
}

func TestToggleDisabled_emptySetLeavesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-configs.disabled")
	if _, err := ToggleDisabled(path, "a.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := ToggleDisabled(path, "a.json"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("file after re-enabling everything = %q, want empty", data)
	}
}
