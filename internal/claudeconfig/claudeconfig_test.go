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

func TestReadAPIKey_returns_key_from_env(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-test123"}}`), 0644)

	got := ReadAPIKey(cfgDir, "zhipu.json")
	if got != "sk-test123" {
		t.Fatalf("ReadAPIKey = %q, want sk-test123", got)
	}
}

func TestReadAPIKey_empty_when_no_env(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "plain.json"), []byte(`{}`), 0644)

	if got := ReadAPIKey(cfgDir, "plain.json"); got != "" {
		t.Fatalf("ReadAPIKey = %q, want empty", got)
	}
}

func TestReadAPIKey_empty_when_no_key(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "env.json"), []byte(`{"env":{"OTHER":"val"}}`), 0644)

	if got := ReadAPIKey(cfgDir, "env.json"); got != "" {
		t.Fatalf("ReadAPIKey = %q, want empty", got)
	}
}

func TestReadAPIKey_empty_when_file_missing(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)

	if got := ReadAPIKey(cfgDir, "missing.json"); got != "" {
		t.Fatalf("ReadAPIKey = %q, want empty", got)
	}
}

func TestWriteAPIKey_creates_env_section(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{}`), 0644)

	if err := WriteAPIKey(cfgDir, "zhipu.json", "sk-new-key"); err != nil {
		t.Fatal(err)
	}
	got := ReadAPIKey(cfgDir, "zhipu.json")
	if got != "sk-new-key" {
		t.Fatalf("after write, ReadAPIKey = %q", got)
	}
}

func TestWriteAPIKey_overwrites_existing_key(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"old-key"}}`), 0644)

	if err := WriteAPIKey(cfgDir, "zhipu.json", "new-key"); err != nil {
		t.Fatal(err)
	}
	got := ReadAPIKey(cfgDir, "zhipu.json")
	if got != "new-key" {
		t.Fatalf("after overwrite, ReadAPIKey = %q", got)
	}
}

func TestWriteAPIKey_preserves_other_env_vars(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://localhost:4000","ANTHROPIC_AUTH_TOKEN":"old"}}`), 0644)

	if err := WriteAPIKey(cfgDir, "zhipu.json", "new"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(cfgDir, "zhipu.json"))
	if !strings.Contains(string(data), "http://localhost:4000") {
		t.Fatalf("base URL lost after write: %s", data)
	}
	if !strings.Contains(string(data), `"new"`) {
		t.Fatalf("new key not in file: %s", data)
	}
}

func TestWriteAPIKey_preserves_non_env_fields(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{"permissions":{"allow":["Bash(ls)"]},"env":{"ANTHROPIC_AUTH_TOKEN":"old"}}`), 0644)

	if err := WriteAPIKey(cfgDir, "zhipu.json", "new"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(cfgDir, "zhipu.json"))
	if !strings.Contains(string(data), "Bash(ls)") {
		t.Fatalf("permissions lost after write: %s", data)
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

func TestReadModelMappings_returns_indices(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5.2","ANTHROPIC_DEFAULT_HAIKU_MODEL":"glm-4.5-air"}}`), 0644)

	models := ProviderModels["zhipu"]
	got := ReadModelMappings(cfgDir, "zhipu.json", models)
	if got[0] != 0 {
		t.Fatalf("opus = %d, want 0", got[0])
	}
	if got[1] != -1 {
		t.Fatalf("sonnet = %d, want -1", got[1])
	}
	if got[2] != 5 {
		t.Fatalf("haiku = %d, want 5", got[2])
	}
	if got[3] != -1 {
		t.Fatalf("fable = %d, want -1", got[3])
	}
}

func TestReadModelMappings_empty_when_no_env(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "plain.json"), []byte(`{}`), 0644)

	got := ReadModelMappings(cfgDir, "plain.json", ProviderModels["zhipu"])
	for i, v := range got {
		if v != -1 {
			t.Fatalf("slot %d = %d, want -1", i, v)
		}
	}
}

func TestReadModelMappings_empty_when_file_missing(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)

	got := ReadModelMappings(cfgDir, "missing.json", ProviderModels["zhipu"])
	for i, v := range got {
		if v != -1 {
			t.Fatalf("slot %d = %d, want -1", i, v)
		}
	}
}

func TestWriteModelMappings_creates_env_vars(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{}`), 0644)

	models := ProviderModels["zhipu"]
	mappings := [4]int{0, 0, 3, 0}
	if err := WriteModelMappings(cfgDir, "zhipu.json", mappings, models); err != nil {
		t.Fatal(err)
	}
	got := ReadModelMappings(cfgDir, "zhipu.json", models)
	if got != mappings {
		t.Fatalf("after write, got %v, want %v", got, mappings)
	}
}

func TestWriteModelMappings_minus1_clears_key(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5.2"}}`), 0644)

	mappings := [4]int{-1, -1, -1, -1}
	if err := WriteModelMappings(cfgDir, "zhipu.json", mappings, ProviderModels["zhipu"]); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(cfgDir, "zhipu.json"))
	if strings.Contains(string(data), "ANTHROPIC_DEFAULT_OPUS_MODEL") {
		t.Fatalf("key should be cleared: %s", data)
	}
}

func TestWriteModelMappings_preserves_other_env_vars(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://localhost:4000","ANTHROPIC_AUTH_TOKEN":"sk-test"}}`), 0644)

	mappings := [4]int{0, -1, -1, -1}
	if err := WriteModelMappings(cfgDir, "zhipu.json", mappings, ProviderModels["zhipu"]); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(cfgDir, "zhipu.json"))
	if !strings.Contains(string(data), "http://localhost:4000") {
		t.Fatalf("base URL lost: %s", data)
	}
	if !strings.Contains(string(data), "sk-test") {
		t.Fatalf("API key lost: %s", data)
	}
}

func TestModelsForConfig_zhipu(t *testing.T) {
	models := ModelsForConfig("Zhipu GLM")
	if len(models) != 6 {
		t.Fatalf("expected 6 zhipu models, got %d", len(models))
	}
	if models[0] != "glm-5.2" {
		t.Fatalf("expected glm-5.2, got %s", models[0])
	}
}

func TestModelsForConfig_mimo(t *testing.T) {
	models := ModelsForConfig("Xiaomi MiMo")
	if len(models) != 5 {
		t.Fatalf("expected 5 mimo models, got %d", len(models))
	}
	if models[0] != "mimo-v2.5-pro" {
		t.Fatalf("expected mimo-v2.5-pro, got %s", models[0])
	}
}

func TestModelsForConfig_unknown_falls_back(t *testing.T) {
	models := ModelsForConfig("Unknown Provider")
	if len(models) == 0 {
		t.Fatal("expected fallback models, got empty")
	}
}
