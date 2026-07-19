package bash_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildTUI compiles wisp-deck-tui once into a temp dir and returns its path.
func buildTUI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "wisp-deck-tui")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/wisp-deck-tui")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// repoRoot walks up from CWD to the module root (where go.mod lives).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func TestCLIAdd_then_key_mirrors_into_opencode(t *testing.T) {
	bin := buildTUI(t)
	home := t.TempDir()
	cfgRoot := filepath.Join(home, ".config", "wisp-deck")
	configsDir := filepath.Join(cfgRoot, "claude-configs")
	os.MkdirAll(configsDir, 0755)
	list := filepath.Join(cfgRoot, "claude-configs.list")
	pointer := filepath.Join(cfgRoot, "claude-config")

	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME=")

	// add a zhipu-named config (so base URL resolves), make it active, give it a key+mapping.
	run := func(args ...string) {
		c := exec.Command(bin, args...)
		c.Env = repositoryTestEnvironment(env)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("claude-config", "add", "--list", list, "--dir", configsDir, "--pointer", pointer, "--name", "Work GLM zhipu")
	// add wrote work-glm-zhipu.json; give it a key + opus mapping, mark active.
	cfgFile := filepath.Join(configsDir, "work-glm-zhipu.json")
	os.WriteFile(cfgFile, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-abc","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-4.6"}}`), 0644)
	os.WriteFile(pointer, []byte("work-glm-zhipu.json\n"), 0644)
	// rename triggers a re-sync that now sees the key+mapping.
	run("claude-config", "rename", "--list", list, "--dir", configsDir, "--pointer", pointer, "--file", "work-glm-zhipu.json", "--name", "Work GLM zhipu")

	data, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("opencode.json not written: %v", err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	if !strings.Contains(string(data), "wisp-deck-work-glm-zhipu") {
		t.Errorf("provider not mirrored:\n%s", data)
	}
	if m["model"] != "wisp-deck-work-glm-zhipu/glm-4.6" {
		t.Errorf("model = %v", m["model"])
	}

	// delete removes the provider again.
	run("claude-config", "delete", "--list", list, "--dir", configsDir, "--pointer", pointer, "--file", "work-glm-zhipu.json")
	data, _ = os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.json"))
	if strings.Contains(string(data), "wisp-deck-work-glm-zhipu") {
		t.Errorf("provider not removed after delete:\n%s", data)
	}
}
