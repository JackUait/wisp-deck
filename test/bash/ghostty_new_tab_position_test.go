package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runGhosttyNewTabPosition(t *testing.T, home, arg string) string {
	t.Helper()
	lib := filepath.Join(projectRoot(t), "lib", "terminals", "ghostty.sh")
	call := "ghostty_new_tab_position"
	if arg != "" {
		call += fmt.Sprintf(" %q", arg)
	}
	out, code := runBashSnippet(t, fmt.Sprintf("source %q && %s", lib, call),
		buildEnv(t, nil, "HOME="+home))
	assertExitCode(t, code, 0)
	return strings.TrimSpace(out)
}

func TestGhosttyNewTabPosition_parses_config(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config *string
		want   string
	}{
		{name: "no config file", config: nil, want: "current"},
		{name: "key absent", config: strp("font-size = 13\n"), want: "current"},
		{name: "end", config: strp("window-new-tab-position = end\n"), want: "end"},
		{name: "no spaces", config: strp("window-new-tab-position=end\n"), want: "end"},
		{name: "extra spaces", config: strp("  window-new-tab-position   =    end   \n"), want: "end"},
		{name: "quoted", config: strp("window-new-tab-position = \"end\"\n"), want: "end"},
		{name: "last one wins", config: strp("window-new-tab-position = end\nwindow-new-tab-position = current\n"), want: "current"},
		{name: "comment ignored", config: strp("# window-new-tab-position = end\n"), want: "current"},
		{name: "empty value resets to default", config: strp("window-new-tab-position = end\nwindow-new-tab-position =\n"), want: "current"},
		{name: "similar key ignored", config: strp("window-new-tab-position-x = end\n"), want: "current"},
		{name: "no trailing newline", config: strp("window-new-tab-position = end"), want: "end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.config != nil {
				dir := filepath.Join(home, ".config", "ghostty")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "config"), []byte(*tc.config), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := runGhosttyNewTabPosition(t, home, ""); got != tc.want {
				t.Fatalf("ghostty_new_tab_position = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGhosttyNewTabPosition_reads_an_explicit_path(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "other-config")
	if err := os.WriteFile(path, []byte("window-new-tab-position = end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runGhosttyNewTabPosition(t, home, path); got != "end" {
		t.Fatalf("ghostty_new_tab_position %s = %q, want end", path, got)
	}
}

func strp(s string) *string { return &s }
