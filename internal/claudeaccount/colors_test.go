package claudeaccount

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func inPalette(c int) bool {
	for _, p := range Palette {
		if p == c {
			return true
		}
	}
	return false
}

func TestColorFor_assigns_a_palette_member(t *testing.T) {
	file := filepath.Join(t.TempDir(), "claude-account-colors")
	got := ColorFor(file, "work")
	if !inPalette(got) {
		t.Fatalf("assigned color %d is not in the palette %v", got, Palette)
	}
}

func TestColorFor_is_stable_across_calls(t *testing.T) {
	file := filepath.Join(t.TempDir(), "claude-account-colors")
	first := ColorFor(file, "work")
	for i := 0; i < 5; i++ {
		if again := ColorFor(file, "work"); again != first {
			t.Fatalf("color must be stable: first %d, call %d got %d", first, i, again)
		}
	}
}

func TestColorFor_persists_assignment_to_file(t *testing.T) {
	file := filepath.Join(t.TempDir(), "claude-account-colors")
	c := ColorFor(file, "work")
	if got := LoadColors(file)["work"]; got != c {
		t.Fatalf("persisted color %d != assigned %d", got, c)
	}
}

// Distinct accounts must get distinct colors — the whole point of "non-repeating"
// — for as many accounts as the palette can cover.
func TestColorFor_distinct_accounts_get_distinct_colors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "claude-account-colors")
	seen := map[int]string{}
	for i := 0; i < len(Palette); i++ {
		dir := "acct-" + string(rune('a'+i))
		c := ColorFor(file, dir)
		if other, dup := seen[c]; dup {
			t.Fatalf("color %d assigned to both %q and %q", c, other, dir)
		}
		seen[c] = dir
	}
}

// The empty dir is the implicit Default login; it keys under "default" so bash
// and Go agree on the same slot.
func TestColorFor_empty_dir_keys_as_default(t *testing.T) {
	file := filepath.Join(t.TempDir(), "claude-account-colors")
	c := ColorFor(file, "")
	if got := LoadColors(file)["default"]; got != c {
		t.Fatalf("empty dir should persist under \"default\": file has %d, returned %d", got, c)
	}
}

// Past palette exhaustion the assignment still yields a usable palette color
// (repeats are allowed only as a last resort) rather than failing or returning 0.
func TestColorFor_beyond_palette_still_returns_palette_member(t *testing.T) {
	file := filepath.Join(t.TempDir(), "claude-account-colors")
	for i := 0; i < len(Palette)+3; i++ {
		dir := "acct-" + string(rune('a'+i))
		if c := ColorFor(file, dir); !inPalette(c) {
			t.Fatalf("account %q got non-palette color %d", dir, c)
		}
	}
}

func TestLoadColors_parses_dir_index_skips_junk(t *testing.T) {
	file := filepath.Join(t.TempDir(), "claude-account-colors")
	if err := os.WriteFile(file, []byte("# header\n\nwork:39\nnocolon\npersonal:208\nbad:notanumber\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadColors(file)
	if got["work"] != 39 || got["personal"] != 208 {
		t.Fatalf("parsed map wrong: %v", got)
	}
	if _, ok := got["bad"]; ok {
		t.Errorf("non-numeric index must be skipped, got %v", got["bad"])
	}
}

func TestLoadColors_missing_file_is_empty(t *testing.T) {
	if got := LoadColors(filepath.Join(t.TempDir(), "absent")); len(got) != 0 {
		t.Fatalf("missing file should be empty map, got %v", got)
	}
}

func TestColorForAvoidsColorsFromOtherFiles(t *testing.T) {
	dir := t.TempDir()
	// Subscriptions already wear every palette color except one — the new
	// account must take the remaining hue so logins and subscriptions never
	// collide (the mirror of claudeconfig.ColorFor's avoid set).
	free := Palette[len(Palette)-1]
	lines := ""
	for i, c := range Palette[:len(Palette)-1] {
		lines += fmt.Sprintf("cfg%d.json:%d\n", i, c)
	}
	configColors := filepath.Join(dir, "claude-config-colors")
	if err := os.WriteFile(configColors, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	colors := filepath.Join(dir, "claude-account-colors")

	if got := ColorFor(colors, "work", configColors); got != free {
		t.Fatalf("color = %d, want the only free palette member %d", got, free)
	}
}
