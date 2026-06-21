package opencodeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// seed writes a minimal ghost-tab config tree under root and returns Inputs.
func seed(t *testing.T, home string, active string) Inputs {
	t.Helper()
	cfgRoot := filepath.Join(home, ".config", "ghost-tab")
	configsDir := filepath.Join(cfgRoot, "claude-configs")
	if err := os.MkdirAll(configsDir, 0755); err != nil {
		t.Fatal(err)
	}
	listFile := filepath.Join(cfgRoot, "claude-configs.list")
	pointer := filepath.Join(cfgRoot, "claude-config")
	// "Work GLM zhipu" -> base URL resolves; map opus -> glm-4.6.
	os.WriteFile(listFile, []byte("Work GLM zhipu:work-glm-zhipu.json\n"), 0644)
	cfg := `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-abc","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-4.6"}}`
	os.WriteFile(filepath.Join(configsDir, "work-glm-zhipu.json"), []byte(cfg), 0644)
	if active != "" {
		os.WriteFile(pointer, []byte(active+"\n"), 0644)
	}
	return Inputs{ListFile: listFile, ConfigsDir: configsDir, PointerFile: pointer, Home: home}
}

func TestSync_writes_opencode_config(t *testing.T) {
	home := t.TempDir()
	in := seed(t, home, "work-glm-zhipu.json")
	if err := Sync(in); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("opencode.json not written: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["model"] != "ghost-tab-work-glm-zhipu/glm-4.6" {
		t.Errorf("model = %v", m["model"])
	}
	prov, ok := m["provider"].(map[string]any)["ghost-tab-work-glm-zhipu"].(map[string]any)
	if !ok {
		t.Fatalf("provider missing: %v", m["provider"])
	}
	if prov["options"].(map[string]any)["baseURL"] != "https://api.z.ai/api/anthropic" {
		t.Errorf("baseURL = %v", prov["options"])
	}
}

func TestSync_respects_xdg_config_home(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	in := seed(t, home, "work-glm-zhipu.json")
	if err := Sync(in); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(xdg, "opencode", "opencode.json")); err != nil {
		t.Errorf("expected config under XDG_CONFIG_HOME: %v", err)
	}
}

func TestSync_preserves_existing_user_file(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "") // ensure ~/.config path
	ocDir := filepath.Join(home, ".config", "opencode")
	os.MkdirAll(ocDir, 0755)
	os.WriteFile(filepath.Join(ocDir, "opencode.json"),
		[]byte(`{"theme":"tokyonight","provider":{"mine":{"name":"Mine"}}}`), 0644)
	in := seed(t, home, "work-glm-zhipu.json")
	if err := Sync(in); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(ocDir, "opencode.json"))
	var m map[string]any
	json.Unmarshal(data, &m)
	if m["theme"] != "tokyonight" {
		t.Errorf("theme lost: %v", m["theme"])
	}
	if _, ok := m["provider"].(map[string]any)["mine"]; !ok {
		t.Errorf("user provider lost: %v", m["provider"])
	}
}

func TestSync_file_mode_new_file(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "") // ensure ~/.config path
	in := seed(t, home, "work-glm-zhipu.json")
	if err := Sync(in); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("opencode.json not written: %v", err)
	}
	if got := info.Mode().Perm(); got != os.FileMode(0600) {
		t.Errorf("new file: mode = %04o, want 0600", got)
	}
}

func TestSync_file_mode_existing_0644(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "") // ensure ~/.config path
	ocDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(ocDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ocDir, "opencode.json")
	// Pre-create the file at 0644 — Sync must tighten it to 0600.
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	in := seed(t, home, "work-glm-zhipu.json")
	if err := Sync(in); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("opencode.json missing after Sync: %v", err)
	}
	if got := info.Mode().Perm(); got != os.FileMode(0600) {
		t.Errorf("pre-existing 0644 file: mode = %04o, want 0600", got)
	}
}

func TestBuildSubscriptions_marks_active_and_resolves(t *testing.T) {
	home := t.TempDir()
	in := seed(t, home, "work-glm-zhipu.json")
	subs := BuildSubscriptions(in)
	if len(subs) != 1 {
		t.Fatalf("got %d subs, want 1", len(subs))
	}
	s := subs[0]
	if !s.Active {
		t.Errorf("sub should be active")
	}
	if s.BaseURL != "https://api.z.ai/api/anthropic" {
		t.Errorf("baseURL = %q", s.BaseURL)
	}
	if s.OpusModel != "glm-4.6" {
		t.Errorf("opusModel = %q", s.OpusModel)
	}
	if s.APIKey != "sk-abc" {
		t.Errorf("apiKey = %q", s.APIKey)
	}
}
