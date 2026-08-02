package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Clicking any popular image format in the ledger must open the PREVIEW popup,
// not the useless "Binary files differ" diff. IsPreviewableImage is the gate,
// and it is keyed on extension because the decision is made before the bytes
// are ever read.
func TestIsPreviewableImage_covers_every_popular_format(t *testing.T) {
	yes := []string{
		"a.png", "a.apng", "b.jpg", "c.jpeg", "c.jpe", "c.jfif", "d.gif",
		"e.webp", "f.bmp", "f.dib", "g.tif", "g.tiff", "h.ico", "h.icns",
		"i.heic", "i.heif", "j.avif", "k.svg",
		"UP.PNG", "x/y/z.JpG", "deep/dir/Shot.HEIC", "logo.SVG",
	}
	for _, path := range yes {
		if !IsPreviewableImage(path) {
			t.Errorf("IsPreviewableImage(%q) = false, want true", path)
		}
	}
	no := []string{"a.txt", "b.sh", "png", "a.png.bak", "c.mp4", "d.pdf", ""}
	for _, path := range no {
		if IsPreviewableImage(path) {
			t.Errorf("IsPreviewableImage(%q) = true, want false", path)
		}
	}
}

// Every extension the gate admits must actually reach a decoder — an admitted
// format with no decoder is a popup that opens on "(no preview: …)".
func TestPreviewableImageExtensions_all_decode(t *testing.T) {
	byExt := map[string]string{}
	entries, err := os.ReadDir(filepath.Join("testdata", "img"))
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	for _, entry := range entries {
		byExt[strings.ToLower(filepath.Ext(entry.Name()))] = entry.Name()
	}
	for _, ext := range PreviewableImageExtensions() {
		name, ok := byExt[ext]
		if !ok {
			t.Errorf("extension %q has no testdata/img fixture: it is admitted but never proven decodable", ext)
			continue
		}
		data, err := os.ReadFile(filepath.Join("testdata", "img", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := decodeImage(data, ext); err != nil {
			t.Errorf("decodeImage(%s) = %v, want a decoded image", name, err)
		}
	}
}
