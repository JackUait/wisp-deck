package tui

import (
	"bytes"
	"encoding/base64"
	"image/color"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestSupportsKittyGraphics_detects_ghostty_and_kitty(t *testing.T) {
	yes := []map[string]string{
		{"TERM_PROGRAM": "ghostty"},
		{"GHOSTTY_RESOURCES_DIR": "/Applications/Ghostty.app/..."},
		{"KITTY_WINDOW_ID": "1"},
		{"TERM": "xterm-kitty"},
		{"TERM": "xterm-ghostty"},
	}
	for _, env := range yes {
		if !supportsKittyGraphics(envMap(env)) {
			t.Errorf("env %v should support kitty graphics", env)
		}
	}
	no := []map[string]string{
		{"TERM": "xterm-256color", "TERM_PROGRAM": "Apple_Terminal"},
		{},
	}
	for _, env := range no {
		if supportsKittyGraphics(envMap(env)) {
			t.Errorf("env %v should NOT support kitty graphics", env)
		}
	}
}

func TestWrapTmuxPassthrough_doubles_escapes_inside_dcs(t *testing.T) {
	got := wrapTmuxPassthrough("\x1b_Gx\x1b\\")
	want := "\x1bPtmux;\x1b\x1b_Gx\x1b\x1b\\\x1b\\"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestKittyTransmitFile_references_path_in_one_small_apc(t *testing.T) {
	got := kittyTransmitFile("/tmp/wisp-deck-preview-1.png", 7, 20, 10, false)
	if !strings.HasPrefix(got, "\x1b_G") || !strings.HasSuffix(got, "\x1b\\") {
		t.Fatalf("not an APC sequence: %q", got)
	}
	for _, param := range []string{"a=T", "f=100", "t=f", "q=2", "i=7", "c=20", "r=10"} {
		if !strings.Contains(got, param) {
			t.Errorf("missing %s in %q", param, got)
		}
	}
	b64 := base64.StdEncoding.EncodeToString([]byte("/tmp/wisp-deck-preview-1.png"))
	if !strings.Contains(got, ";"+b64+"\x1b\\") {
		t.Errorf("payload should be the base64 path: %q", got)
	}
	if strings.Count(got, "\x1b_G") != 1 {
		t.Errorf("file transmit must be a single APC: %q", got)
	}
}

func TestKittyTransmitFile_tmux_wraps_the_sequence(t *testing.T) {
	got := kittyTransmitFile("/tmp/p.png", 1, 20, 10, true)
	if n := strings.Count(got, "\x1bPtmux;"); n != 1 {
		t.Errorf("passthrough wrappers = %d, want 1", n)
	}
	if !strings.Contains(got, "\x1b\x1b_G") {
		t.Error("inner escapes must be doubled inside the tmux DCS")
	}
}

func TestScaleImage_box_filter_averages(t *testing.T) {
	img := solidImage(4, 4, color.RGBA{100, 100, 100, 255})
	img.SetRGBA(0, 0, color.RGBA{200, 200, 200, 255})
	out := scaleImage(img, 2, 2)
	if got := out.Bounds(); got.Dx() != 2 || got.Dy() != 2 {
		t.Fatalf("size = %v, want 2x2", got)
	}
	// Top-left out pixel averages 200,100,100,100 -> 125.
	r, _, _, _ := out.At(0, 0).RGBA()
	if got := int(r >> 8); got != 125 {
		t.Errorf("averaged pixel = %d, want 125", got)
	}
	// A pure region stays exact.
	r, _, _, _ = out.At(1, 1).RGBA()
	if got := int(r >> 8); got != 100 {
		t.Errorf("solid pixel = %d, want 100", got)
	}
}

func TestImagePlacementAt_matches_body_centering(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("dot.png", data, "modified")
	row, col, cols, rows, ok := m.ImagePlacementAt(80, 30)
	if !ok {
		t.Fatal("placement not available")
	}
	// Geometry from layout(): mh=4, mv=1, cw=70, ch=26, viewport=23 rows.
	// 8x8 image -> 8 cols x 4 rows; topPad=(23-4)/2=9, leftPad=(70-8)/2=31.
	// 1-based: row = mv+1(border)+2(header)+9+1 = 14; col = mh+1+31+1 = 37.
	if row != 14 || col != 37 || cols != 8 || rows != 4 {
		t.Errorf("placement = row %d col %d %dx%d, want row 14 col 37 8x4", row, col, cols, rows)
	}
}

func TestImagePlacementAt_unavailable_without_image(t *testing.T) {
	m := NewImageView("broken.png", []byte("junk"), "modified")
	if _, _, _, _, ok := m.ImagePlacementAt(80, 24); ok {
		t.Error("undecodable image must report no placement")
	}
	d := NewDiffView("a.go", "+x\n")
	if _, _, _, _, ok := d.ImagePlacementAt(80, 24); ok {
		t.Error("diff mode must report no placement")
	}
}

func TestWithKittyHires_transmit_carries_placement_params(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()) // payload files land in an auto-cleaned dir
	data := pngBytes(t, solidImage(8, 8, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("dot.png", data, "modified")
	var buf bytes.Buffer
	m = m.WithKittyHires(&buf, false)
	// Arming writes nothing: placements land on the ALT screen, which bubbletea
	// only enters at Run(); the first WindowSizeMsg schedules the transmit.
	if buf.Len() != 0 {
		t.Fatalf("arming must not write; got %q", buf.String())
	}
	updated, cmd := m.Update(sizeMsg(80, 30))
	m = updated.(DiffViewModel)
	if cmd == nil {
		t.Fatal("first size must schedule the transmit command")
	}
	cmd()
	out := buf.String()
	if !strings.Contains(out, "\x1b[14;37H") {
		t.Errorf("missing CUP to image cell:\n%q", out)
	}
	for _, param := range []string{"a=T", "f=100", "c=8", "r=4"} {
		if !strings.Contains(out, param) {
			t.Errorf("missing %s in transmit:\n%q", param, out)
		}
	}
	// Cursor is saved/restored around the placement so the frame isn't disturbed.
	if !strings.Contains(out, "\x1b7") || !strings.Contains(out, "\x1b8") {
		t.Error("placement must save/restore the cursor")
	}
	_ = m
}
