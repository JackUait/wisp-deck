package bash_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpencodePluginExecutableSpec runs the installed TypeScript template as
// plain ESM. OpenCode accepts TypeScript filenames, but the template deliberately
// contains only JavaScript so the same bytes can be exercised by stock Node.
func TestOpencodePluginExecutableSpec(t *testing.T) {
	root := projectRoot(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("node is required to execute the OpenCode plugin contract")
	}

	source := filepath.Join(root, "templates", "opencode-plugin.ts")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read plugin template: %v", err)
	}
	plugin := filepath.Join(t.TempDir(), "wisp-deck.mjs")
	if err := os.WriteFile(plugin, data, 0600); err != nil {
		t.Fatalf("copy plugin template: %v", err)
	}

	script := filepath.Join(root, "test", "js", "opencode_plugin_test.mjs")
	cmd := exec.Command(node, script, plugin)
	env := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "WISP_DECK_ATTENTION_") ||
			strings.HasPrefix(item, "OPENCODE_SERVER_") {
			continue
		}
		env = append(env, item)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("OpenCode executable contract failed: %v\n%s", err, out)
	}
}
