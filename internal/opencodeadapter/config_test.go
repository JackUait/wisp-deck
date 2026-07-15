package opencodeadapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteSilentTUIConfigIsPrivateAtomicAndExact(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "generation.OpenCode1")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := WriteSilentTUIConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "opencode-tui.json"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want private regular file", info.Mode())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"attention": map[string]any{
		"enabled": false, "notifications": false, "sound": false,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %#v, want %#v", got, want)
	}

	if err := os.WriteFile(path, []byte(`{"attention":{"enabled":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSilentTUIConfig(dir); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(path)
	data, _ = os.ReadFile(path)
	if info.Mode().Perm() != 0o600 || !json.Valid(data) {
		t.Fatalf("replacement mode/data = %o %q", info.Mode().Perm(), data)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".opencode-tui.*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files = %v, %v", matches, err)
	}
}

func TestWriteSilentTUIConfigRejectsUnsafeGenerationPaths(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "file")
	if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "relative", path: "generation.Relative"},
		{name: "wrong basename", path: root},
		{name: "regular file", path: regular},
		{name: "missing", path: filepath.Join(root, "generation.Missing")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := WriteSilentTUIConfig(test.path); err == nil {
				t.Fatalf("unsafe path %q accepted", test.path)
			}
		})
	}
}
