package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "img", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// The formats with pure-Go decoders (stdlib png/jpeg/gif plus x/image
// webp/bmp/tiff) must decode IN PROCESS. Shelling out per preview would put a
// process spawn — and a macOS dependency — on the popup's first paint for the
// formats that are by far the most common in a repo.
func TestDecodeImage_never_shells_out_for_go_native_formats(t *testing.T) {
	restore := sipsConvert
	sipsConvert = func([]byte, string) ([]byte, error) {
		t.Errorf("decodeImage shelled out to sips for a Go-native format")
		return nil, errNoSips
	}
	t.Cleanup(func() { sipsConvert = restore })

	for _, name := range []string{
		"sample.png", "sample.apng", "sample.jpg", "sample.jpe", "sample.jfif",
		"sample.gif", "sample.webp", "sample.bmp", "sample.dib",
		"sample.tif", "sample.tiff",
	} {
		ext := strings.ToLower(filepath.Ext(name))
		img, err := decodeImage(fixture(t, name), ext)
		if err != nil {
			t.Errorf("decodeImage(%s) = %v, want an image", name, err)
			continue
		}
		if b := img.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
			t.Errorf("decodeImage(%s) bounds = %dx%d, want 8x8", name, b.Dx(), b.Dy())
		}
	}
}

// AVIF, HEIC, ICO/ICNS and SVG have no pure-Go decoder in this module's
// dependency set, and vendoring one for each is a large surface. macOS ships
// ImageIO decoders for all of them behind `sips`, so the fallback converts to
// PNG and decodes that — the preview is identical either way.
func TestDecodeImage_falls_back_to_sips_for_the_rest(t *testing.T) {
	for _, name := range []string{
		"sample.avif", "sample.heic", "sample.heif",
		"sample.ico", "sample.icns", "sample.svg",
	} {
		ext := strings.ToLower(filepath.Ext(name))
		called := false
		restore := sipsConvert
		sipsConvert = func(data []byte, e string) ([]byte, error) {
			called = true
			return restore(data, e)
		}
		img, err := decodeImage(fixture(t, name), ext)
		sipsConvert = restore
		if err != nil {
			t.Errorf("decodeImage(%s) = %v, want an image", name, err)
			continue
		}
		if !called {
			t.Errorf("decodeImage(%s) decoded without the sips fallback — unexpected, but the fallback test is now meaningless", name)
		}
		if b := img.Bounds(); b.Dx() < 1 || b.Dy() < 1 {
			t.Errorf("decodeImage(%s) bounds = %dx%d, want a real image", name, b.Dx(), b.Dy())
		}
	}
}

// An SVG has no pixels of its own — rasterizing it at its declared 8x8 would
// make a logo preview a speck, since the renderer never upscales. It must be
// rasterized at preview resolution instead, which is exactly what a vector
// format is for.
func TestDecodeImage_rasterizes_svg_at_preview_resolution(t *testing.T) {
	img, err := decodeImage(fixture(t, "sample.svg"), ".svg")
	if err != nil {
		t.Fatalf("decodeImage(sample.svg) = %v", err)
	}
	if b := img.Bounds(); b.Dx() != previewRasterMaxSide || b.Dy() != previewRasterMaxSide {
		t.Errorf("svg rasterized at %dx%d, want %d square", b.Dx(), b.Dy(), previewRasterMaxSide)
	}
}

// A phone photo is 24 megapixels, and converting one at full size took NINE
// SECONDS of popup-open latency — for pixels nothing downstream keeps: the
// preview is cell-sized and the kitty overlay caps its bitmap at the same
// ceiling. Converting straight to the ceiling is an order of magnitude faster
// and pixel-identical on screen.
func TestDecodeImage_converts_oversized_sources_at_the_preview_ceiling(t *testing.T) {
	img, err := decodeImage(fixture(t, "large.heic"), ".heic")
	if err != nil {
		t.Fatalf("decodeImage(large.heic) = %v", err)
	}
	b := img.Bounds()
	if b.Dx() > previewRasterMaxSide || b.Dy() > previewRasterMaxSide {
		t.Errorf("decoded %dx%d, want neither side above %d", b.Dx(), b.Dy(), previewRasterMaxSide)
	}
	// Capped, not squashed: a 3000x2000 source stays 3:2, up to the rounding
	// of one pixel row.
	if diff := b.Dx()*2 - b.Dy()*3; diff < -3 || diff > 3 {
		t.Errorf("decoded %dx%d, want the source's 3:2 aspect ratio", b.Dx(), b.Dy())
	}
}

// The other direction of the same flag: a SMALL source must never be blown up
// to the ceiling. The renderer refuses to upscale precisely so a tiny asset
// reads as tiny; pre-upscaling it in the decoder would smuggle a blurry
// enlargement past that rule. (An 8x8 heic staying 8x8 is asserted by
// TestNewImageView_decodes_a_sips_backed_format.)
func TestDecodeImage_never_upscales_a_small_raster(t *testing.T) {
	img, err := decodeImage(fixture(t, "sample.avif"), ".avif")
	if err != nil {
		t.Fatalf("decodeImage(sample.avif) = %v", err)
	}
	if b := img.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
		t.Errorf("decoded %dx%d, want the source's 8x8", b.Dx(), b.Dy())
	}
}

// Bytes nothing can decode must surface as an error the popup turns into
// "(no preview: …)", never as a silent blank.
func TestDecodeImage_reports_undecodable_bytes(t *testing.T) {
	if _, err := decodeImage([]byte("not an image at all"), ".png"); err == nil {
		t.Fatal("decodeImage(garbage) = nil error, want a decode failure")
	}
}

// NewImageView is the whole popup's entry point: it must route through the
// full decode pipeline, not image.Decode alone, or every sips-backed format
// opens on the fallback message.
func TestNewImageView_decodes_a_sips_backed_format(t *testing.T) {
	m := NewImageView("assets/icon.heic", fixture(t, "sample.heic"), "modified")
	if m.img == nil {
		t.Fatalf("NewImageView(heic) did not decode: %q", m.imgErr)
	}
	if got := m.imageDims(); got != "8×8" {
		t.Errorf("imageDims() = %q, want %q", got, "8×8")
	}
}
