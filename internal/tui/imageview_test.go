package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// solidImage builds a w×h RGBA image filled with c.
func solidImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// pngBytes encodes img as PNG.
func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

func TestRenderImagePreview_small_image_not_upscaled(t *testing.T) {
	img := solidImage(2, 2, color.RGBA{255, 0, 0, 255})
	out := renderImagePreview(img, 40)
	rows := strings.Split(out, "\n")
	// 2 px tall = 1 half-block row; 2 px wide = 2 columns, not stretched to 40.
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1\n%q", len(rows), out)
	}
	if got := strings.Count(out, "▀"); got != 2 {
		t.Errorf("half-block cells = %d, want 2\n%q", got, out)
	}
}

func TestRenderImagePreview_scales_down_to_width(t *testing.T) {
	img := solidImage(100, 100, color.RGBA{0, 255, 0, 255})
	out := renderImagePreview(img, 10)
	rows := strings.Split(out, "\n")
	// Scaled to 10 px wide keeps aspect: 10 px tall -> 5 half-block rows.
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5\n%q", len(rows), out)
	}
	for i, r := range rows {
		if got := strings.Count(r, "▀"); got != 10 {
			t.Errorf("row %d cells = %d, want 10", i, got)
		}
	}
}

func TestRenderImagePreview_emits_truecolor_fg_and_bg(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 2))
	img.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255}) // top pixel: red -> fg
	img.SetRGBA(0, 1, color.RGBA{0, 0, 255, 255}) // bottom pixel: blue -> bg
	out := renderImagePreview(img, 10)
	if !strings.Contains(out, "\x1b[38;2;255;0;0m") {
		t.Errorf("missing red foreground: %q", out)
	}
	if !strings.Contains(out, "\x1b[48;2;0;0;255m") {
		t.Errorf("missing blue background: %q", out)
	}
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Errorf("preview should end in a reset: %q", out)
	}
}

func TestRenderImagePreview_odd_height_last_row_has_no_bg(t *testing.T) {
	img := solidImage(3, 3, color.RGBA{255, 0, 0, 255})
	out := renderImagePreview(img, 40)
	rows := strings.Split(out, "\n")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (ceil(3/2))\n%q", len(rows), out)
	}
	// The dangling last row has no bottom pixel, so no 48;2 background there.
	if strings.Contains(rows[1], "\x1b[48;2;") {
		t.Errorf("last row should carry no background: %q", rows[1])
	}
}

func TestNewImageView_header_shows_status_and_dimensions(t *testing.T) {
	data := pngBytes(t, solidImage(4, 2, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("assets/logo.png", data, "modified")
	m = sizeDiff(m, 80, 24)
	out := stripA(m.View())
	if !strings.Contains(out, "MODIFIED") {
		t.Errorf("missing status badge:\n%s", out)
	}
	if !strings.Contains(out, "assets/logo.png") {
		t.Errorf("missing title:\n%s", out)
	}
	if !strings.Contains(out, "4×2") {
		t.Errorf("missing dimensions:\n%s", out)
	}
}

func TestNewImageView_added_badge(t *testing.T) {
	data := pngBytes(t, solidImage(1, 1, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("new.png", data, "added")
	m = sizeDiff(m, 80, 24)
	if out := stripA(m.View()); !strings.Contains(out, "ADDED") {
		t.Errorf("missing ADDED badge:\n%s", out)
	}
}

func TestNewImageView_has_no_view_switcher(t *testing.T) {
	data := pngBytes(t, solidImage(1, 1, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("a.png", data, "modified")
	m = sizeDiff(m, 120, 40)
	out := stripA(m.View())
	if strings.Contains(out, diffTabInlineText) || strings.Contains(out, diffTabSxsText) {
		t.Errorf("image view must not show the layout switcher:\n%s", out)
	}
	if strings.Contains(out, diffTabChangesText) {
		t.Errorf("image view must not show the context switcher:\n%s", out)
	}
}

func TestNewImageView_body_shows_preview(t *testing.T) {
	data := pngBytes(t, solidImage(4, 4, color.RGBA{200, 10, 10, 255}))
	m := NewImageView("a.png", data, "modified")
	m = sizeDiff(m, 80, 24)
	if out := m.View(); !strings.Contains(out, "▀") {
		t.Errorf("body should contain half-block preview cells:\n%s", out)
	}
}

func TestNewImageView_bad_data_shows_fallback(t *testing.T) {
	m := NewImageView("broken.png", []byte("not an image"), "modified")
	m = sizeDiff(m, 80, 24)
	if out := stripA(m.View()); !strings.Contains(out, "no preview") {
		t.Errorf("missing decode-failure fallback:\n%s", out)
	}
}

func TestNewImageView_escape_quits(t *testing.T) {
	data := pngBytes(t, solidImage(1, 1, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("a.png", data, "modified")
	m = sizeDiff(m, 80, 24)
	updated, cmd := keyDiff(m, tea.KeyEscape)
	if !updated.quitting || !quits(cmd) {
		t.Error("Esc should quit the image view")
	}
}
