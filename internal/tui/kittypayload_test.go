package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
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

// The hi-res transmit must not block the first paint: arming stores the
// writer, the first WindowSizeMsg returns a tea.Cmd that does the encode and
// write off the event loop, and its completion message marks the image sent.
func TestKittyHires_first_size_returns_async_transmit_cmd(t *testing.T) {
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
