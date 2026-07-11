package tui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"  // register decoders for the formats is_image_file routes here
	_ "image/jpeg" //
	_ "image/png"  //
	"os/exec"
	"strings"
)

// openInPreview launches the image in the macOS Preview app. `-a Preview`
// (not the default handler) so the header button's promise is always true.
// Start, not Run: Preview opens beside the popup, which stays interactive.
// Swappable so tests can observe the launch without spawning an app.
var openInPreview = func(path string) error {
	return exec.Command("open", "-a", "Preview", path).Start()
}

// WithImagePath tells the image view where its bytes live on disk, enabling
// the [ Open in Preview ] control (the bytes themselves arrive on stdin).
func (m DiffViewModel) WithImagePath(path string) DiffViewModel {
	m.imgPath = path
	return m
}

// imagePreviewBg is the dark gray an image's transparent pixels are composited
// over, so alpha art reads against the popup's dark surface instead of
// whatever color happens to be premultiplied in.
var imagePreviewBg = [3]uint32{40, 40, 40}

// fitImage returns the largest w×h-proportioned size that fits inside
// maxW×maxHPx PIXELS ("contain"): capped by width first, then by height, so
// any aspect ratio ends up fully visible. Never upscales (a blown-up preview
// misrepresents the asset) and never returns a zero dimension.
func fitImage(w, h, maxW, maxHPx int) (int, int) {
	outW, outH := w, h
	if outW > maxW {
		outH = (outH*maxW + outW/2) / outW
		outW = maxW
	}
	if outH > maxHPx {
		outW = (outW*maxHPx + outH/2) / outH
		outH = maxHPx
	}
	if outW < 1 {
		outW = 1
	}
	if outH < 1 {
		outH = 1
	}
	return outW, outH
}

// renderImagePreview renders img as rows of truecolor half-block cells: each
// "▀" carries two vertically-stacked pixels (foreground = top, background =
// bottom). The image is nearest-neighbor downscaled to fit width columns AND
// maxRows rows (each row = 2 px), keeping its aspect ratio (see fitImage), so
// the whole image is visible whatever its shape. An odd-height image leaves
// the final row's lower half unpainted (no background escape), so the popup
// surface shows through. Every row ends in a reset so color can't bleed.
func renderImagePreview(img image.Image, width, maxRows int) string {
	if width < 1 {
		width = 1
	}
	if maxRows < 1 {
		maxRows = 1
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 1 || h < 1 {
		return ""
	}
	outW, outH := fitImage(w, h, width, maxRows*2)
	// Box-filter to the output size so the cells carry averaged detail instead
	// of nearest-neighbor aliasing; native-size images pass through untouched.
	if outW != w || outH != h {
		img = scaleImage(img, outW, outH)
		bounds = img.Bounds()
	}

	px := func(x, y int) (r, g, b uint32) {
		sx := bounds.Min.X + x
		sy := bounds.Min.Y + y
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
	m.imgSrc = data
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

// renderImageBody is the image-mode viewport content for a box interior of
// cw columns × ch rows: the preview scaled to fit BOTH dimensions and centered
// in the box (blank rows above, space padding left), or the decode-failure
// fallback. Fitting both ways means no aspect ratio ever needs scrolling.
func (m DiffViewModel) renderImageBody(cw, ch int) string {
	if m.img == nil {
		return diffGapRowStyle.Render("(no preview: " + m.imgErr + ")")
	}
	if ch < 1 {
		ch = 1
	}
	b := m.img.Bounds()
	outW, _ := fitImage(b.Dx(), b.Dy(), cw, ch*2)
	rows := strings.Split(renderImagePreview(m.img, cw, ch), "\n")
	leftPad := strings.Repeat(" ", maxInt((cw-outW)/2, 0))
	for i := range rows {
		rows[i] = leftPad + rows[i]
	}
	padded := make([]string, 0, ch)
	for i := 0; i < (ch-len(rows))/2; i++ {
		padded = append(padded, "")
	}
	return strings.Join(append(padded, rows...), "\n")
}
