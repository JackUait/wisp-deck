package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/ledger"
)

// The ledger decides between the image PREVIEW popup and the diff popup before
// any bytes are read. The gate is "an image extension we can decode, whose file
// is on disk right now" — the same pair of conditions the shell renderer uses
// (`is_image_file` + `[ -f ]`), so both renderers open the same popup.
func TestOpensImagePreview_gates_on_extension_and_presence(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"shot.png", "photo.HEIC", "icon.svg", "art.webp", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	cases := []struct {
		path string
		row  ledger.Row
		want bool
	}{
		{"shot.png", ledger.Row{Binary: true, NewBytes: 1}, true},
		{"photo.HEIC", ledger.Row{Binary: true, NewBytes: 1}, true},
		{"art.webp", ledger.Row{Binary: true, NewBytes: 1}, true},
		// An SVG is a TEXT file: git reports line counts and no byte size, yet it
		// is still an image and still previews.
		{"icon.svg", ledger.Row{}, true},
		{"notes.txt", ledger.Row{}, false},
		// Deleted: the extension still matches, but there are no bytes to show,
		// so it must fall back to the diff pipeline rather than cat a ghost.
		{"gone.png", ledger.Row{Binary: true, NewBytes: 12}, false},
	}
	for _, tc := range cases {
		tc.row.Path = tc.path
		if got := opensImagePreview(dir, tc.row); got != tc.want {
			t.Errorf("opensImagePreview(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
