package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // stdlib decoders for the raster formats Go ships natively
	_ "image/jpeg" //
	_ "image/png"  //
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/bmp"  // pure-Go decoders for the next three formats:
	_ "golang.org/x/image/tiff" // no subprocess, no macOS dependency, no
	_ "golang.org/x/image/webp" // process spawn on the popup's first paint
)

// previewRasterMaxSide is the longest side the fallback converter produces. It
// is deliberately kittyMaxSide: nothing downstream keeps more than that (the
// half-block preview is cell-sized, the hi-res overlay caps its bitmap there),
// while converting a 24-megapixel phone photo at full size cost NINE SECONDS
// of popup-open latency.
//
// It cuts both ways for a VECTOR source, which has no pixels of its own: an SVG
// is rasterized AT this size rather than at its declared one, so a 16px icon
// fills the popup sharply instead of showing as a speck — the renderer never
// upscales, which is exactly what a vector format exists to avoid.
const previewRasterMaxSide = kittyMaxSide

// sipsDecodeTimeout bounds the fallback conversion. It is a local ImageIO
// decode of one file; a second is already pathological, and the popup must not
// hang on a malformed asset.
const sipsDecodeTimeout = 10 * time.Second

// errNoSips reports that the fallback converter is unavailable — the formats
// with no pure-Go decoder simply have no preview on that machine.
var errNoSips = errors.New("sips is unavailable")

// sipsConvert renders arbitrary image bytes to PNG through macOS's ImageIO, the
// fallback for the formats this module has no Go decoder for (AVIF, HEIC/HEIF,
// ICO/ICNS, SVG). ext names the source format: ImageIO sniffs most containers
// from their magic bytes, but SVG is plain text and is recognized only by the
// file's extension, so the scratch copy keeps it. Swappable so tests can
// observe (or suppress) the shell-out.
var sipsConvert = func(data []byte, ext string) ([]byte, error) {
	if _, err := exec.LookPath("sips"); err != nil {
		return nil, errNoSips
	}
	dir, err := os.MkdirTemp("", "wisp-deck-decode-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	in := filepath.Join(dir, "in"+ext)
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, err
	}
	out := filepath.Join(dir, "out.png")

	args := []string{"-s", "format", "png"}
	// --resampleHeightWidthMax resizes in BOTH directions, so it is applied only
	// where enlarging is right (a vector, which is re-rasterized rather than
	// resampled) or impossible (a raster already past the ceiling). A small
	// raster is converted untouched — blowing it up here would sneak a blurry
	// enlargement past the renderer's no-upscale rule. The size probe reads
	// container metadata only and costs ~30ms even on a 24-megapixel source.
	if ext == ".svg" || sipsExceedsPreviewCeiling(in) {
		args = append(args, "--resampleHeightWidthMax", itoa(previewRasterMaxSide))
	}
	args = append(args, in, "--out", out)

	ctx, cancel := context.WithTimeout(context.Background(), sipsDecodeTimeout)
	defer cancel()
	if combined, err := exec.CommandContext(ctx, "sips", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("sips: %w: %s", err, bytes.TrimSpace(combined))
	}
	return os.ReadFile(out)
}

// sipsExceedsPreviewCeiling reports whether the image at path is larger than
// the preview ceiling on either side. An unreadable size answers false: the
// conversion then runs unscaled, which is slower but never wrong.
func sipsExceedsPreviewCeiling(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), sipsDecodeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sips", "-g", "pixelWidth", "-g", "pixelHeight", path).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "pixelWidth:", "pixelHeight:":
			if side, convErr := strconv.Atoi(fields[1]); convErr == nil && side > previewRasterMaxSide {
				return true
			}
		}
	}
	return false
}

// decodeImage turns an image file's bytes into a picture the preview can draw.
// Registered Go decoders run first — they cover the formats a repo is mostly
// made of and cost nothing but the decode. Anything they reject (AVIF, HEIC,
// ICO/ICNS, SVG) is converted to PNG by macOS's ImageIO and decoded from there;
// the preview is identical either way. ext is the source path's extension,
// which the converter needs to recognize text-based formats.
func decodeImage(data []byte, ext string) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err == nil {
		return img, nil
	}
	converted, convErr := sipsConvert(data, ext)
	if convErr != nil {
		// Report the ORIGINAL decode failure: the fallback not being available
		// (or not recognizing the bytes either) says nothing the user can act on,
		// while "unknown format" names the actual problem.
		return nil, err
	}
	img, _, convErr = image.Decode(bytes.NewReader(converted))
	if convErr != nil {
		return nil, convErr
	}
	return img, nil
}
