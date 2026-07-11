package tui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"  // register decoders for the formats is_image_file routes here
	_ "image/jpeg" //
	_ "image/png"  //
	"strings"
)

// imagePreviewBg is the dark gray an image's transparent pixels are composited
// over, so alpha art reads against the popup's dark surface instead of
// whatever color happens to be premultiplied in.
var imagePreviewBg = [3]uint32{40, 40, 40}

// renderImagePreview renders img as rows of truecolor half-block cells: each
// "▀" carries two vertically-stacked pixels (foreground = top, background =
// bottom). The image is nearest-neighbor downscaled to fit width columns,
// keeping its aspect ratio; a small image is shown at native size (never
// upscaled — a blown-up preview misrepresents the asset). An odd-height image
// leaves the final row's lower half unpainted (no background escape), so the
// popup surface shows through. Every row ends in a reset so color can't bleed.
func renderImagePreview(img image.Image, width int) string {
	if width < 1 {
		width = 1
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 1 || h < 1 {
		return ""
	}
	outW := w
	if outW > width {
		outW = width
	}
	outH := (h*outW + w/2) / w
	if outH < 1 {
		outH = 1
	}

	px := func(x, y int) (r, g, b uint32) {
		sx := bounds.Min.X + x*w/outW
		sy := bounds.Min.Y + y*h/outH
		pr, pg, pb, pa := img.At(sx, sy).RGBA()
		// Composite the premultiplied color over the preview background.
		r = (pr + (0xffff-pa)*imagePreviewBg[0]/255) >> 8
		g = (pg + (0xffff-pa)*imagePreviewBg[1]/255) >> 8
		b = (pb + (0xffff-pa)*imagePreviewBg[2]/255) >> 8
		return r, g, b
	}

	var out strings.Builder
	for y := 0; y < outH; y += 2 {
		if y > 0 {
			out.WriteByte('\n')
		}
		for x := 0; x < outW; x++ {
			tr, tg, tb := px(x, y)
			fmt.Fprintf(&out, "\x1b[38;2;%d;%d;%dm", tr, tg, tb)
			if y+1 < outH {
				br, bg, bb := px(x, y+1)
				fmt.Fprintf(&out, "\x1b[48;2;%d;%d;%dm", br, bg, bb)
			}
			out.WriteString("▀")
		}
		out.WriteString("\x1b[0m")
	}
	return out.String()
}

// NewImageView builds the popup pager in IMAGE mode: instead of a diff body it
// shows a half-block truecolor preview of the image bytes, re-rendered to the
// box width on resize. There is only the one view (no layout/context tabs),
// like a whole-file add/delete. The status badge can't be derived from a
// binary body, so the caller passes it ("added" | "modified" | "deleted").
// Undecodable data falls back to an in-popup message rather than failing.
func NewImageView(title string, data []byte, status string) DiffViewModel {
	m := DiffViewModel{
		title:      title,
		status:     status,
		isImage:    true,
		singleView: true,
		hoverMode:  -1,
		hoverCtx:   -1,
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		m.imgErr = err.Error()
		return m
	}
	m.img = img
	return m
}

// imageDims is the "W×H" pixel-size caption shown in the image-mode header
// where a diff shows its +N −N line counts. Empty when the image didn't decode.
func (m DiffViewModel) imageDims() string {
	if m.img == nil {
		return ""
	}
	b := m.img.Bounds()
	return itoa(b.Dx()) + "×" + itoa(b.Dy())
}

// renderImageBody is the image-mode viewport content for a box interior cw
// columns wide: the scaled preview, or the decode-failure fallback.
func (m DiffViewModel) renderImageBody(cw int) string {
	if m.img == nil {
		return diffGapRowStyle.Render("(no preview: " + m.imgErr + ")")
	}
	return renderImagePreview(m.img, cw)
}
