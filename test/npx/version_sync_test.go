package npx_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageJSON_author_is_structured(t *testing.T) {
	root := projectRoot(t)
	pkgBytes, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Author struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"author"`
	}
	if err := json.Unmarshal(pkgBytes, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Author.Name != "Evgeniy Pyatkov" {
		t.Errorf("author.name = %q, want %q", pkg.Author.Name, "Evgeniy Pyatkov")
	}
	if pkg.Author.URL != "https://t.me/that_ai_guy" {
		t.Errorf("author.url = %q, want %q", pkg.Author.URL, "https://t.me/that_ai_guy")
	}
}

func TestPackageJSON_version_matches_VERSION_file(t *testing.T) {
	root := projectRoot(t)

	versionBytes, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.TrimSpace(string(versionBytes))

	pkgBytes, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pkgBytes, &pkg); err != nil {
		t.Fatal(err)
	}

	if pkg.Version != expected {
		t.Errorf("package.json version = %q, VERSION file = %q", pkg.Version, expected)
	}
}
