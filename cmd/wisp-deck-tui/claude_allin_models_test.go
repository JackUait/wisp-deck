package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexModelsCache_sits_in_the_codex_home(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	if got, want := codexModelsCache(), filepath.Join(home, ".codex", "models_cache.json"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	custom := t.TempDir()
	t.Setenv("CODEX_HOME", custom)
	if got, want := codexModelsCache(), filepath.Join(custom, "models_cache.json"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCodexModelLister_asks_the_bridge_codex(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	codex := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf '%s ' \"$@\" > " + argsFile + "\n" +
		`printf '%s' '{"models":[{"slug":"gpt-6.1-sol","visibility":"list","context_window":272000},{"slug":"gpt-reserve","visibility":"hide"}]}'` + "\n"
	if err := os.WriteFile(codex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	list := codexModelLister(codex)
	if list == nil {
		t.Fatal("lister = nil for an absolute codex path")
	}
	models, err := list()
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gpt-6.1-sol" || models[0].Context != 272000 {
		t.Fatalf("models = %+v", models)
	}
	if args, _ := os.ReadFile(argsFile); string(args) != "debug models " {
		t.Fatalf("argv = %q, want \"debug models \"", args)
	}
	if codexModelLister("") != nil {
		t.Fatal("no codex path must leave the lister unset, so the cache file is read")
	}
}

// A pane launched while Codex was mid-reinstall carries an empty
// WISP_DECK_CODEX_CMD. The bridge then looks Codex up itself, the way
// resolve_agent_cmd does: PATH first, then the path setup cached.
func TestLookupCodexPath_finds_codex_without_the_session_env(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(bin, "codex")
	if err := os.WriteFile(codex, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	lookup := lookupCodexPath(root)

	t.Setenv("PATH", t.TempDir())
	if got := lookup(); got != "" {
		t.Fatalf("no codex anywhere: got %q", got)
	}

	if err := os.WriteFile(filepath.Join(root, "codex-cmd"), []byte(codex+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := lookup(); got != codex {
		t.Fatalf("cached path: got %q, want %q", got, codex)
	}

	if err := os.Remove(filepath.Join(root, "codex-cmd")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if got := lookup(); got != codex {
		t.Fatalf("PATH lookup: got %q, want %q", got, codex)
	}
}
