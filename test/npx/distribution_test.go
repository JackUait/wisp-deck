package npx_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// copyDistributionEntries extracts the entry list that npx-wisp-deck.js copies
// into the install dir, so the tests below stay in sync when it grows.
func copyDistributionEntries(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(projectRoot(t), "bin", "npx-wisp-deck.js"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)const entries = \[(.*?)\]`).FindSubmatch(src)
	if block == nil {
		t.Fatal("could not find copyDistribution entries in npx-wisp-deck.js")
	}
	var entries []string
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(block[1], -1) {
		entries = append(entries, string(m[1]))
	}
	if len(entries) == 0 {
		t.Fatal("no entries parsed from copyDistribution")
	}
	return entries
}

// The installer symlinks ~/.local/bin/wisp-deck -> <install>/bin/wisp-deck-config
// and seeds the default Claude configs from <install>/defaults. If the launcher
// doesn't copy them, the `wisp-deck` command is a dangling symlink and the
// default configs never appear.
func TestLauncher_copies_config_command_and_defaults(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, ".local", "share", "wisp-deck")

	env := append(os.Environ(),
		"HOME="+home,
		"WISP_DECK_INSTALL_DIR="+installDir,
		"WISP_DECK_SKIP_TUI_DOWNLOAD=1",
		"WISP_DECK_SKIP_EXEC=1",
	)

	_, stderr, code := runLauncher(t, env)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. stderr: %s", code, stderr)
	}

	for _, rel := range []string{
		"bin/wisp-deck-config",
		"defaults/claude-configs.list",
	} {
		if _, err := os.Stat(filepath.Join(installDir, rel)); err != nil {
			t.Errorf("expected %s in install dir: %v", rel, err)
		}
	}

	configs, err := filepath.Glob(filepath.Join(installDir, "defaults", "claude-configs", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) == 0 {
		t.Error("expected default claude-configs JSON files in install dir, found none")
	}
}

func TestDefaults_include_keyless_OpenAI_GPT_subscription(t *testing.T) {
	root := projectRoot(t)
	list, err := os.ReadFile(filepath.Join(root, "defaults", "claude-configs.list"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(list), "OpenAI GPT:openai-gpt.json") {
		t.Fatalf("default list is missing OpenAI GPT:\n%s", list)
	}

	data, err := os.ReadFile(filepath.Join(root, "defaults", "claude-configs", "openai-gpt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Model                     string            `json:"model"`
		DisableClaudeAiConnectors bool              `json:"disableClaudeAiConnectors"`
		Env                       map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("invalid OpenAI GPT settings JSON: %v", err)
	}
	want := map[string]string{
		"WISP_DECK_SUBSCRIPTION_PROVIDER": "openai-chatgpt",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":    "gpt-5.6-sol",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":  "gpt-5.6-terra",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":   "gpt-5.6-luna",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":   "gpt-5.6-luna",
	}
	for key, value := range want {
		if settings.Env[key] != value {
			t.Errorf("%s = %q, want %q", key, settings.Env[key], value)
		}
	}
	if settings.Model != "gpt-5.6-terra" {
		t.Errorf("model = %q, want gpt-5.6-terra", settings.Model)
	}
	if !settings.DisableClaudeAiConnectors {
		t.Error("OpenAI GPT default must disable unavailable claude.ai connectors")
	}
	if settings.Env["ANTHROPIC_AUTH_TOKEN"] != "" || settings.Env["ANTHROPIC_BASE_URL"] != "" {
		t.Fatal("OpenAI GPT default must not persist a key or fixed base URL")
	}
}

// The Moonshot Kimi preset is an API-key subscription: it carries the provider
// marker (the robust resolution path) and the four model mappings, but never a
// key — the modal prompts for that and writes it 0600.
func TestDefaults_include_Moonshot_Kimi_subscription(t *testing.T) {
	root := projectRoot(t)
	list, err := os.ReadFile(filepath.Join(root, "defaults", "claude-configs.list"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(list), "Moonshot Kimi:kimi.json") {
		t.Fatalf("default list is missing Moonshot Kimi:\n%s", list)
	}

	data, err := os.ReadFile(filepath.Join(root, "defaults", "claude-configs", "kimi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Model                     string            `json:"model"`
		DisableClaudeAiConnectors bool              `json:"disableClaudeAiConnectors"`
		Env                       map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("invalid Moonshot Kimi settings JSON: %v", err)
	}
	want := map[string]string{
		"WISP_DECK_SUBSCRIPTION_PROVIDER": "moonshot",
		"ANTHROPIC_BASE_URL":              "https://api.moonshot.ai/anthropic",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":    "kimi-k3",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":  "kimi-k2.7-code",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":   "kimi-k2.7-code",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":   "kimi-k3",
	}
	for key, value := range want {
		if settings.Env[key] != value {
			t.Errorf("%s = %q, want %q", key, settings.Env[key], value)
		}
	}
	if settings.Env["ANTHROPIC_AUTH_TOKEN"] != "" {
		t.Fatal("Moonshot Kimi default must not persist an API key")
	}
	// `model` / disableClaudeAiConnectors are the Codex-ChatGPT carve-out only.
	if settings.Model != "" || settings.DisableClaudeAiConnectors {
		t.Error("API-key preset must not pin a model or disable claude.ai connectors")
	}
}

// packedFiles returns the paths npm would actually publish.
func packedFiles(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("npm", "pack", "--dry-run", "--json")
	cmd.Dir = projectRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("npm pack --dry-run failed: %v", err)
	}

	var packed []struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &packed); err != nil {
		t.Fatalf("parsing npm pack output: %v", err)
	}
	if len(packed) == 0 {
		t.Fatal("npm pack reported no package")
	}

	var paths []string
	for _, f := range packed[0].Files {
		paths = append(paths, f.Path)
	}
	return paths
}

// isPublished reports whether rel is published, either as a file or as a
// directory holding published files.
func isPublished(rel string, published []string) bool {
	for _, p := range published {
		if p == rel || strings.HasPrefix(p, rel+"/") {
			return true
		}
	}
	return false
}

// The launcher can only copy what npm actually publishes. Anything in
// copyDistribution that package.json's "files" leaves out ships as a missing
// file to every npx user.
func TestNpmPack_publishes_everything_the_launcher_copies(t *testing.T) {
	published := packedFiles(t)

	for _, entry := range copyDistributionEntries(t) {
		if !isPublished(entry, published) {
			t.Errorf("copyDistribution copies %q but package.json \"files\" does not publish it", entry)
		}
	}
}

// Every $SHARE_DIR/... path the installed scripts reach for must exist in the
// published package. This is the general form of the dangling-`wisp-deck`
// symlink bug: bin/wisp-deck referenced $SHARE_DIR/bin/wisp-deck-config and
// $SHARE_DIR/defaults, neither of which npm shipped, and `ln -sf` reported
// success anyway.
func TestPublishedPackage_contains_every_path_the_installer_references(t *testing.T) {
	root := projectRoot(t)
	published := packedFiles(t)

	// SHARE_DIR is the install root only for the scripts that run from it.
	// (wrapper.sh reuses the name for the runtime config dir — different thing.)
	scripts := []string{"bin/wisp-deck", "bin/wisp-deck-config"}
	ref := regexp.MustCompile(`\$SHARE_DIR/([A-Za-z0-9._/-]+)`)

	checked := 0
	for _, script := range scripts {
		src, err := os.ReadFile(filepath.Join(root, script))
		if err != nil {
			t.Fatalf("reading %s: %v", script, err)
		}
		for _, m := range ref.FindAllSubmatch(src, -1) {
			rel := strings.TrimRight(string(m[1]), "/")
			// Paths built at runtime (session state, per-session files) aren't
			// shipped; only the static ones the package must carry are.
			if !isPublished(rel, published) {
				t.Errorf("%s references $SHARE_DIR/%s but npm does not publish it", script, rel)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no $SHARE_DIR references found — the guard is not actually checking anything")
	}
}
