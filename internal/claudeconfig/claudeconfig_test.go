package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestProviderForConfig_marker_overrides_display_name(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "renamed.json"), []byte(
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"openai-chatgpt"}}`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	got := ProviderForConfig(cfgDir, Config{Name: "Zhipu GLM", File: "renamed.json"})
	if got.Key != "openai-chatgpt" {
		t.Fatalf("provider key = %q, want openai-chatgpt", got.Key)
	}
	if got.Auth != AuthCodexChatGPT {
		t.Fatalf("auth = %q, want %q", got.Auth, AuthCodexChatGPT)
	}
	if got.MirrorOpenCode {
		t.Fatal("ChatGPT provider must not be mirrored into OpenCode")
	}
}

func TestProviderForConfig_unknown_or_invalid_marker_falls_back_to_name(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "unknown.json"), []byte(
		`{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"not-a-provider"}}`,
	), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "invalid.json"), []byte(`{`), 0644); err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{"unknown.json", "invalid.json"} {
		got := ProviderForConfig(cfgDir, Config{Name: "Xiaomi MiMo", File: file})
		if got.Key != "mimo" {
			t.Errorf("%s: provider key = %q, want mimo", file, got.Key)
		}
	}
}

func TestConfigReady_depends_on_provider_auth_kind(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"gpt.json":   `{"env":{"WISP_DECK_SUBSCRIPTION_PROVIDER":"openai-chatgpt"}}`,
		"empty.json": `{"env":{}}`,
		"keyed.json": `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-test"}}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(cfgDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"ChatGPT needs no API key", Config{Name: "Renamed", File: "gpt.json"}, true},
		{"API provider needs key", Config{Name: "Zhipu GLM", File: "empty.json"}, false},
		{"API provider accepts key", Config{Name: "Zhipu GLM", File: "keyed.json"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConfigReady(cfgDir, tt.cfg); got != tt.want {
				t.Fatalf("ConfigReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOpenAIProviderModelsAndLimits(t *testing.T) {
	provider := ProviderForName("OpenAI GPT")
	if provider.Key != "openai-chatgpt" {
		t.Fatalf("provider key = %q, want openai-chatgpt", provider.Key)
	}
	want := []string{
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex-spark",
	}
	got := ModelsForConfig("OpenAI GPT")
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("model[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, id := range want {
		context, output, ok := ModelLimit(id)
		if !ok || context == 0 {
			t.Errorf("ModelLimit(%q) = (%d, %d, %v), want a context limit", id, context, output, ok)
		}
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
	if len(models) != 2 {
		t.Fatalf("expected 2 mimo models, got %d", len(models))
	}
	if models[0] != "mimo-v2.5-pro" {
		t.Fatalf("expected mimo-v2.5-pro, got %s", models[0])
	}
}

func TestCatalog_every_model_fully_described(t *testing.T) {
	// Single-source guarantee: every offered model has both a cost and a limit,
	// so OpenCode never shows $0 / unknown-context for an offered model.
	for _, m := range CatalogModels() {
		if _, _, ok := ModelCost(m.ID); !ok {
			t.Errorf("catalog model %q has no cost", m.ID)
		}
		if _, _, ok := ModelLimit(m.ID); !ok {
			t.Errorf("catalog model %q has no limit", m.ID)
		}
	}
}

func TestProviderResolution_never_skips(t *testing.T) {
	// The model list and the base URL must resolve to the SAME provider for any
	// name, so a config offered models is always mirrorable (no silent skip).
	for _, name := range []string{"GLM", "Z.ai work", "zhipu", "mimo", "Xiaomi", "random plan", ""} {
		if ProviderBaseURL(name) == "" {
			t.Errorf("ProviderBaseURL(%q) is empty", name)
		}
		if len(ModelsForConfig(name)) == 0 {
			t.Errorf("ModelsForConfig(%q) is empty", name)
		}
	}
}

func TestModelCost_and_ModelLimit_lookup(t *testing.T) {
	if in, out, ok := ModelCost("glm-4.6"); !ok || in != 0.6 || out != 2.2 {
		t.Errorf("ModelCost(glm-4.6) = %v, %v, %v; want 0.6, 2.2, true", in, out, ok)
	}
	if ctx, out, ok := ModelLimit("glm-5.2"); !ok || ctx != 1000000 || out != 128000 {
		t.Errorf("ModelLimit(glm-5.2) = %v, %v, %v; want 1000000, 128000, true", ctx, out, ok)
	}
	if _, _, ok := ModelCost("not-a-model"); ok {
		t.Errorf("ModelCost(unknown) ok=true, want false")
	}
}

func TestModelsForConfig_unknown_falls_back(t *testing.T) {
	models := ModelsForConfig("Unknown Provider")
	if len(models) == 0 {
		t.Fatal("expected fallback models, got empty")
	}
}

func TestWriteAPIKey_writes_file_with_0600_perms(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{}`), 0644)

	if err := WriteAPIKey(cfgDir, "zhipu.json", "sk-secret"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(cfgDir, "zhipu.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("config file perm = %o, want 0600", perm)
	}
}

func TestWriteModelMappings_writes_file_with_0600_perms(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "claude-configs")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "zhipu.json"), []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-secret"}}`), 0644)

	models := ProviderModels["zhipu"]
	if err := WriteModelMappings(cfgDir, "zhipu.json", [4]int{0, 1, 2, 3}, models); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(cfgDir, "zhipu.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("config file perm = %o, want 0600", perm)
	}
}

func TestProviderBaseURL(t *testing.T) {
	const zhipuURL = "https://api.z.ai/api/anthropic"
	const mimoURL = "https://api.xiaomimimo.com/anthropic"
	cases := map[string]string{
		"Work GLM zhipu":  zhipuURL,
		"my zhipu plan":   zhipuURL,
		"GLM coder":       zhipuURL, // common name, no literal "zhipu"
		"Z.ai production": zhipuURL,
		"work mimo plan":  mimoURL,
		"Xiaomi MiMo":     mimoURL,
		// Unmatched names default to the first provider, matching ModelsForConfig's
		// fallback, so a config is never offered models it cannot mirror.
		"random name": zhipuURL,
		"":            zhipuURL,
	}
	for name, want := range cases {
		if got := ProviderBaseURL(name); got != want {
			t.Errorf("ProviderBaseURL(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestProviders_haveDisplayNamesAndValidDefaults(t *testing.T) {
	for _, provider := range Providers {
		if provider.Name == "" {
			t.Errorf("provider %q has no display name", provider.Key)
		}
		models := make(map[string]bool, len(provider.Models))
		for _, model := range provider.Models {
			models[model.ID] = true
		}
		for i, id := range provider.DefaultModels {
			if id == "" || !models[id] {
				t.Errorf("provider %q default %s = %q, want catalog model",
					provider.Key, AnthropicAliases[i], id)
			}
		}
	}
}

func TestProviderByKey_returnsCatalogProvider(t *testing.T) {
	got, ok := ProviderByKey("openai-chatgpt")
	if !ok || got.Name != "OpenAI / ChatGPT" {
		t.Fatalf("ProviderByKey = (%+v, %v)", got, ok)
	}
}

func TestAddForProvider_writesInitializedProfile(t *testing.T) {
	for _, key := range []string{"zhipu", "mimo", "openai-chatgpt"} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			list := filepath.Join(dir, "claude-configs.list")
			configsDir := filepath.Join(dir, "claude-configs")

			file, err := AddForProvider(list, configsDir, "My profile", key)
			if err != nil {
				t.Fatal(err)
			}
			provider, _ := ProviderByKey(key)
			cfg := Config{Name: "My profile", File: file}
			if got := ProviderForConfig(configsDir, cfg).Key; got != key {
				t.Errorf("provider = %q, want %q", got, key)
			}
			got := ReadModelMappings(configsDir, file, ProviderModels[key])
			for i, modelID := range provider.DefaultModels {
				want := slices.Index(ProviderModels[key], modelID)
				if got[i] != want {
					t.Errorf("mapping[%d] = %d, want %d", i, got[i], want)
				}
			}
			if provider.Auth == AuthAPIKey && ConfigReady(configsDir, cfg) {
				t.Error("new API-key profile must need a key")
			}

			path := filepath.Join(configsDir, file)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0600 {
				t.Errorf("config mode = %o, want 600", got)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var settings struct {
				Model string            `json:"model"`
				Env   map[string]string `json:"env"`
			}
			if err := json.Unmarshal(data, &settings); err != nil {
				t.Fatal(err)
			}
			if got := settings.Env["WISP_DECK_SUBSCRIPTION_PROVIDER"]; got != key {
				t.Errorf("provider marker = %q, want %q", got, key)
			}
			if provider.BaseURL != "" && settings.Env["ANTHROPIC_BASE_URL"] != provider.BaseURL {
				t.Errorf("base URL = %q, want %q", settings.Env["ANTHROPIC_BASE_URL"], provider.BaseURL)
			}
			if provider.Auth == AuthCodexChatGPT && settings.Model != provider.DefaultModels[1] {
				t.Errorf("ChatGPT model = %q, want %q", settings.Model, provider.DefaultModels[1])
			}
		})
	}
}

func TestAddForProvider_rejectsUnknownProviderWithoutFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := AddForProvider(filepath.Join(dir, "list"), filepath.Join(dir, "cfg"), "Bad", "missing")
	if err == nil {
		t.Fatal("unknown provider accepted")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("unknown provider left artifacts: %v", entries)
	}
}
