package tui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func sizeMsg(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

func jpegBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	return buf.Bytes()
}

var pngMagicBytes = []byte("\x89PNG\r\n\x1a\n")

func TestKittyPayload_png_source_passes_through_verbatim(t *testing.T) {
	src := pngBytes(t, solidImage(64, 64, color.RGBA{9, 9, 9, 255}))
	img, _, _ := image.Decode(bytes.NewReader(src))
	got := kittyPayload(img, src)
	if !bytes.Equal(got, src) {
		t.Error("in-cap PNG source must be transmitted verbatim, not re-encoded")
	}
}

func TestKittyPayload_jpeg_source_reencodes_to_png(t *testing.T) {
	src := jpegBytes(t, solidImage(64, 64, color.RGBA{9, 9, 9, 255}))
	img, _, _ := image.Decode(bytes.NewReader(src))
	got := kittyPayload(img, src)
	if !bytes.HasPrefix(got, pngMagicBytes) {
		t.Error("non-PNG source must be converted to PNG (kitty accepts only PNG)")
	}
}

func TestKittyPayload_oversized_source_is_rescaled(t *testing.T) {
	big := solidImage(kittyMaxSide+400, 100, color.RGBA{9, 9, 9, 255})
	src := pngBytes(t, big)
	img, _, _ := image.Decode(bytes.NewReader(src))
	got := kittyPayload(img, src)
	out, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("payload is not PNG: %v", err)
	}
	if out.Bounds().Dx() > kittyMaxSide {
		t.Errorf("payload width = %d, want <= %d", out.Bounds().Dx(), kittyMaxSide)
	}
}

// noisyImage returns a w×h image with per-pixel variation so its PNG encoding
// is large enough that a direct-medium transmit would need many chunks.
func noisyImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x*7 + y*3), uint8(x*2 ^ y*5), uint8(x*13 + y*11), 255})
		}
	}
	return img
}

// transmitPayloadPath extracts the file path referenced by a t=f transmit
// sequence (the base64 payload after the APC's ';').
func transmitPayloadPath(t *testing.T, out string) string {
	t.Helper()
	start := strings.Index(out, "\x1b_G")
	end := strings.Index(out[start:], "\x1b\\")
	if start < 0 || end < 0 {
		t.Fatalf("no APC sequence in %q", out)
	}
	seq := out[start : start+end]
	b64 := seq[strings.Index(seq, ";")+1:]
	path, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	return string(path)
}

// The transmit tty is SHARED with the tmux client process, and macOS
// interleaves concurrent writers' bytes mid-stream (tmux frame bytes can split
// the APC introducer, printing the pixel payload as on-screen gibberish).
// So the pixels must travel via the kitty FILE medium (t=f): the tty write is
// a tiny file reference, never a megabyte base64 stream.
func TestKittyHires_transmit_uses_file_medium_and_stays_small(t *testing.T) {
	data := pngBytes(t, noisyImage(256, 256))
	m := NewImageView("photo.png", data, "modified")
	var buf bytes.Buffer
	m = m.WithKittyHires(&buf, false)
	updated, cmd := m.Update(sizeMsg(80, 30))
	m = updated.(DiffViewModel)
	cmd()
	out := buf.String()
	if len(out) > 600 {
		t.Fatalf("transmit wrote %d bytes to the shared tty, want a tiny file reference", len(out))
	}
	if !strings.Contains(out, "t=f") {
		t.Fatalf("transmit must use the file medium:\n%q", out)
	}
	path := transmitPayloadPath(t, out)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("payload file unreadable: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("payload file must hold the kittyPayload bytes (verbatim PNG here)")
	}
	m.KittyCleanup()
}

// The payload file is transient: once the popup closes it must be gone.
func TestKittyHires_cleanup_removes_payload_file(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("dot.png", data, "modified")
	var buf bytes.Buffer
	m = m.WithKittyHires(&buf, false)
	updated, cmd := m.Update(sizeMsg(80, 30))
	m = updated.(DiffViewModel)
	cmd()
	path := transmitPayloadPath(t, buf.String())
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("payload file must exist while the popup is open: %v", err)
	}
	m.KittyCleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("cleanup must remove the payload file, stat err = %v", err)
	}
}

// A transmit suppressed by an early close must not leak its temp file either.
func TestKittyHires_close_before_transmit_leaves_no_file(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	data := pngBytes(t, solidImage(8, 8, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("dot.png", data, "modified")
	var buf bytes.Buffer
	m = m.WithKittyHires(&buf, false)
	updated, cmd := m.Update(sizeMsg(80, 30))
	m = updated.(DiffViewModel)
	m.KittyCleanup()
	cmd()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("suppressed transmit leaked %d temp file(s)", len(entries))
	}
}

// The hi-res transmit must not block the first paint: arming stores the
// writer, the first WindowSizeMsg returns a tea.Cmd that does the encode and
// write off the event loop, and its completion message marks the image sent.
func TestKittyHires_first_size_returns_async_transmit_cmd(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()) // payload files land in an auto-cleaned dir
	data := pngBytes(t, solidImage(8, 8, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("dot.png", data, "modified")
	var buf bytes.Buffer
	m = m.WithKittyHires(&buf, false)

	updated, cmd := m.Update(sizeMsg(80, 30))
	m = updated.(DiffViewModel)
	if cmd == nil {
		t.Fatal("first WindowSizeMsg must return the transmit command")
	}
	if strings.Contains(buf.String(), "a=T") {
		t.Fatal("transmit must not happen synchronously in Update")
	}
	msg := cmd()
	if !strings.Contains(buf.String(), "a=T") {
		t.Fatal("running the command must write the transmit sequence")
	}
	if !strings.Contains(buf.String(), "\x1b[14;37H") {
		t.Errorf("transmit must be positioned at the placement cell:\n%q", buf.String()[:80])
	}
	updated, _ = m.Update(msg)
	m = updated.(DiffViewModel)

	// A later resize re-places synchronously using the stored image data.
	buf.Reset()
	m = sizeDiff(m, 120, 40)
	if out := buf.String(); !strings.Contains(out, "a=d") || !strings.Contains(out, "a=p") {
		t.Errorf("resize after transmit must delete + re-place:\n%q", out)
	}
}

// Closing the popup while the transmit is still in flight must suppress the
// write: a transmit landing after the alt screen exits would paint the image
// onto the user's NORMAL screen.
func TestKittyHires_close_before_transmit_suppresses_write(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("dot.png", data, "modified")
	var buf bytes.Buffer
	m = m.WithKittyHires(&buf, false)

	updated, cmd := m.Update(sizeMsg(80, 30))
	m = updated.(DiffViewModel)
	m.KittyCleanup() // popup closed before the transmit ran
	msg := cmd()
	if strings.Contains(buf.String(), "a=T") {
		t.Errorf("transmit after close must be suppressed:\n%q", buf.String())
	}
	if _, ok := msg.(kittySentMsg); !ok {
		t.Fatalf("transmit cmd must still complete with a kittySentMsg, got %T", msg)
	}
}

// After a completed transmit, closing deletes the image from the terminal's
// store so the next popup doesn't inherit it.
func TestKittyHires_cleanup_after_transmit_deletes_all(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("dot.png", data, "modified")
	var buf bytes.Buffer
	m = m.WithKittyHires(&buf, false)
	updated, cmd := m.Update(sizeMsg(80, 30))
	m = updated.(DiffViewModel)
	updated, _ = m.Update(cmd())
	m = updated.(DiffViewModel)
	buf.Reset()
	m.KittyCleanup()
	if !strings.Contains(buf.String(), "a=d,d=A") {
		t.Errorf("cleanup must delete all images:\n%q", buf.String())
	}
}

// A resize that lands while the transmit is still in flight must re-place the
// image at the new geometry once the transmit completes, not leave it at the
// stale rectangle.
func TestKittyHires_resize_during_transmit_replaces_at_new_size(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()) // payload files land in an auto-cleaned dir
	data := pngBytes(t, solidImage(8, 8, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("dot.png", data, "modified")
	var buf bytes.Buffer
	m = m.WithKittyHires(&buf, false)

	updated, cmd := m.Update(sizeMsg(80, 30))
	m = updated.(DiffViewModel)
	// Resize before the transmit command has run.
	updated, _ = m.Update(sizeMsg(120, 40))
	m = updated.(DiffViewModel)
	msg := cmd()
	buf.Reset()
	updated, _ = m.Update(msg)
	_ = updated.(DiffViewModel)
	if out := buf.String(); !strings.Contains(out, "a=p") {
		t.Errorf("completion after a mid-flight resize must re-place at the new size:\n%q", out)
	}
}
