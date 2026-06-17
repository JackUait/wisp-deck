package claudeconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Work":      "my-work",
		"  Spaces  ":   "spaces",
		"Foo/Bar baz!": "foo-bar-baz",
		"UPPER":        "upper",
		"a--b":         "a-b",
		"---":          "",
		"123 abc":      "123-abc",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoad_skips_comments_blanks_and_no_colon(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "list")
	os.WriteFile(list, []byte("# header\n\nWork:work.json\nbogus-no-colon\nPersonal:personal.json\n"), 0644)
	got := Load(list)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0] != (Config{Name: "Work", File: "work.json"}) {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1] != (Config{Name: "Personal", File: "personal.json"}) {
		t.Errorf("entry 1 = %+v", got[1])
	}
}

func TestLoad_missing_file_returns_nil(t *testing.T) {
	if got := Load(filepath.Join(t.TempDir(), "nope")); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestActivePointer_get_set_resolve_and_standard_clears(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-config")
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "work.json"), []byte("{}"), 0644)

	if err := SetActive(ptr, "work.json"); err != nil {
		t.Fatal(err)
	}
	if got := GetActive(ptr); got != "work.json" {
		t.Fatalf("GetActive = %q", got)
	}
	if got := ResolvePath(cfgDir, ptr); got != filepath.Join(cfgDir, "work.json") {
		t.Fatalf("ResolvePath = %q", got)
	}
	// Missing file resolves to empty.
	SetActive(ptr, "missing.json")
	if got := ResolvePath(cfgDir, ptr); got != "" {
		t.Fatalf("ResolvePath missing = %q", got)
	}
	// "standard" clears pointer file.
	if err := SetActive(ptr, "standard"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatal("pointer should be removed for standard")
	}
	if got := GetActive(ptr); got != "" {
		t.Fatalf("GetActive after clear = %q", got)
	}
}

func TestGetActive_treats_standard_as_empty(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-config")
	os.WriteFile(ptr, []byte("standard\n"), 0644)
	if got := GetActive(ptr); got != "" {
		t.Fatalf("GetActive(standard) = %q", got)
	}
}

func TestAdd_creates_file_and_list_line(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	list := filepath.Join(dir, "list")
	file, err := Add(list, cfgDir, "My Work")
	if err != nil {
		t.Fatal(err)
	}
	if file != "my-work.json" {
		t.Fatalf("file = %q", file)
	}
	data, _ := os.ReadFile(filepath.Join(cfgDir, "my-work.json"))
	if strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("file content = %q", data)
	}
	listData, _ := os.ReadFile(list)
	if !strings.Contains(string(listData), "My Work:my-work.json") {
		t.Fatalf("list = %q", listData)
	}
}

func TestAdd_resolves_collisions(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	list := filepath.Join(dir, "list")
	f1, _ := Add(list, cfgDir, "My Work")
	f2, _ := Add(list, cfgDir, "My Work")
	f3, _ := Add(list, cfgDir, "My Work")
	if f1 != "my-work.json" || f2 != "my-work-2.json" || f3 != "my-work-3.json" {
		t.Fatalf("collision filenames: %q %q %q", f1, f2, f3)
	}
	for _, f := range []string{f1, f2, f3} {
		if _, err := os.Stat(filepath.Join(cfgDir, f)); err != nil {
			t.Errorf("%s missing", f)
		}
	}
}

func TestAdd_blank_name_falls_back_to_config(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	list := filepath.Join(dir, "list")
	file, err := Add(list, cfgDir, "!!!")
	if err != nil {
		t.Fatal(err)
	}
	if file != "config.json" {
		t.Fatalf("file = %q, want config.json", file)
	}
}

func TestRename_changes_name_only(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "list")
	os.WriteFile(list, []byte("Work:work.json\nPersonal:personal.json\n"), 0644)
	if err := Rename(list, "work.json", "Day Job"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(list)
	if !strings.Contains(string(data), "Day Job:work.json") {
		t.Fatalf("list = %q", data)
	}
	if strings.Contains(string(data), "Work:work.json") {
		t.Fatalf("old name still present: %q", data)
	}
	if !strings.Contains(string(data), "Personal:personal.json") {
		t.Fatalf("other entry dropped: %q", data)
	}
}

func TestRename_missing_file_returns_error(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "list")
	os.WriteFile(list, []byte("Work:work.json\n"), 0644)
	if err := Rename(list, "nonexistent.json", "New Name"); err == nil {
		t.Fatal("expected error for missing file")
	}
	data, _ := os.ReadFile(list)
	if !strings.Contains(string(data), "Work:work.json") || strings.Contains(string(data), "New Name") {
		t.Fatalf("list mutated on failed rename: %q", data)
	}
}

func TestDelete_removes_file_and_line(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "work.json"), []byte("{}"), 0644)
	list := filepath.Join(dir, "list")
	os.WriteFile(list, []byte("Work:work.json\nPersonal:personal.json\n"), 0644)
	ptr := filepath.Join(dir, "claude-config")

	if err := Delete(list, cfgDir, ptr, "work.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "work.json")); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}
	data, _ := os.ReadFile(list)
	if strings.Contains(string(data), "work.json") {
		t.Fatalf("line not removed: %q", data)
	}
	if !strings.Contains(string(data), "Personal:personal.json") {
		t.Fatalf("other entry dropped: %q", data)
	}
}

func TestDelete_active_resets_pointer(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "work.json"), []byte("{}"), 0644)
	list := filepath.Join(dir, "list")
	os.WriteFile(list, []byte("Work:work.json\n"), 0644)
	ptr := filepath.Join(dir, "claude-config")
	os.WriteFile(ptr, []byte("work.json\n"), 0644)

	if err := Delete(list, cfgDir, ptr, "work.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatal("pointer should be cleared when active config deleted")
	}
}

func TestDelete_inactive_keeps_pointer(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "work.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(cfgDir, "personal.json"), []byte("{}"), 0644)
	list := filepath.Join(dir, "list")
	os.WriteFile(list, []byte("Work:work.json\nPersonal:personal.json\n"), 0644)
	ptr := filepath.Join(dir, "claude-config")
	os.WriteFile(ptr, []byte("personal.json\n"), 0644)

	if err := Delete(list, cfgDir, ptr, "work.json"); err != nil {
		t.Fatal(err)
	}
	if got := GetActive(ptr); got != "personal.json" {
		t.Fatalf("active pointer changed = %q", got)
	}
}
