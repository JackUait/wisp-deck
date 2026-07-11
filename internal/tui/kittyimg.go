package tui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"strings"
)

// Kitty graphics protocol support: on terminals that implement it (Ghostty,
// kitty), the image preview is ALSO transmitted as real pixels and placed over
// the half-block cells, so the user sees the actual image instead of a 2-px-
// per-cell mosaic. The half-block render stays underneath as the fallback: if
// the graphics sequences are swallowed anywhere (old tmux, unsupported
// terminal), the popup still shows the pixelated preview instead of nothing.

// kittyImageID identifies the preview image inside the terminal's image store
// so resize re-placements and deletion can address it. Arbitrary but fixed;
// the pager shows one image at a time.
const kittyImageID = 4242

// kittyChunkSize is the protocol's maximum payload per APC escape; larger
// base64 data must be split into m=1 continuation chunks.
const kittyChunkSize = 4096

// kittyMaxSide caps the transmitted bitmap's longest side. The popup can't
// show more pixels than a retina full-screen anyway, and an unbounded photo
// would push a giant base64 stream through the tmux passthrough on open.
const kittyMaxSide = 2048

// supportsKittyGraphics reports whether the terminal (from its environment)
// implements the kitty graphics protocol. Ghostty — wisp-deck's only supported
// terminal — does; so does kitty itself.
func supportsKittyGraphics(getenv func(string) string) bool {
	if getenv("TERM_PROGRAM") == "ghostty" || getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return true
	}
	if getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	term := getenv("TERM")
	return strings.Contains(term, "kitty") || strings.Contains(term, "ghostty")
}

// SupportsKittyGraphics is the exported entry for the cmd layer.
func SupportsKittyGraphics(getenv func(string) string) bool {
	return supportsKittyGraphics(getenv)
}

// wrapTmuxPassthrough wraps a terminal escape in tmux's DCS passthrough
// (\ePtmux;...\e\\, inner ESCs doubled) so tmux forwards it to the outer
// terminal instead of eating it. Requires the pane's allow-passthrough option.
func wrapTmuxPassthrough(seq string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// kittyWrap finalizes one APC chunk, passthrough-wrapped when inside tmux.
func kittyWrap(body string, tmux bool) string {
	seq := "\x1b_G" + body + "\x1b\\"
	if tmux {
		return wrapTmuxPassthrough(seq)
	}
	return seq
}

// kittyTransmitDisplay builds the transmit-and-display sequence for PNG data:
// the image is stored under id and shown at the CURRENT cursor position,
// scaled to cols×rows cells. Payloads beyond kittyChunkSize are split into
// continuation chunks (m=1 … m=0), each wrapped for tmux individually. q=2
// suppresses the terminal's responses (nobody is reading them).
func kittyTransmitDisplay(data []byte, id, cols, rows int, tmux bool) string {
	b64 := base64.StdEncoding.EncodeToString(data)
	params := "a=T,f=100,q=2,i=" + itoa(id) + ",c=" + itoa(cols) + ",r=" + itoa(rows)
	if len(b64) <= kittyChunkSize {
		return kittyWrap(params+";"+b64, tmux)
	}
	var b strings.Builder
	b.WriteString(kittyWrap(params+",m=1;"+b64[:kittyChunkSize], tmux))
	rest := b64[kittyChunkSize:]
	for len(rest) > kittyChunkSize {
		b.WriteString(kittyWrap("m=1;"+rest[:kittyChunkSize], tmux))
		rest = rest[kittyChunkSize:]
	}
	b.WriteString(kittyWrap("m=0;"+rest, tmux))
	return b.String()
}

// kittyPlace shows the already-transmitted image id at the current cursor
// position, scaled to cols×rows cells.
func kittyPlace(id, cols, rows int, tmux bool) string {
	return kittyWrap("a=p,q=2,i="+itoa(id)+",c="+itoa(cols)+",r="+itoa(rows), tmux)
}

// kittyDeletePlacements removes the visible placements of image id, keeping
// its transmitted data so a resize can re-place without retransmitting.
func kittyDeletePlacements(id int, tmux bool) string {
	return kittyWrap("a=d,d=i,q=2,i="+itoa(id), tmux)
}

// kittyDeleteAll deletes every image and placement — the close-time sweep.
func kittyDeleteAll(tmux bool) string {
	return kittyWrap("a=d,d=A,q=2", tmux)
}

// cupTo is a 1-based absolute cursor move.
func cupTo(row, col int) string {
	return "\x1b[" + itoa(row) + ";" + itoa(col) + "H"
}

// scaleImage box-filters img down to w×h: each output pixel is the average of
// its source rectangle, so downscaled previews stay smooth instead of aliasing
// the way nearest-neighbor sampling does.
func scaleImage(img image.Image, w, h int) *image.RGBA {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy0, sy1 := y*sh/h, (y+1)*sh/h
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < w; x++ {
			sx0, sx1 := x*sw/w, (x+1)*sw/w
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, bl, a, n uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					pr, pg, pb, pa := img.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r += uint64(pr)
					g += uint64(pg)
					bl += uint64(pb)
					a += uint64(pa)
					n++
				}
			}
			i := out.PixOffset(x, y)
			out.Pix[i+0] = uint8(r / n >> 8)
			out.Pix[i+1] = uint8(g / n >> 8)
			out.Pix[i+2] = uint8(bl / n >> 8)
			out.Pix[i+3] = uint8(a / n >> 8)
		}
	}
	return out
}

// ImagePlacementAt returns the 1-based screen cell rectangle (top row, left
// col, cols, rows) the preview occupies on a termW×termH screen — the same
// centered geometry renderImageBody draws the half-block fallback with, so
// the hi-res placement lands exactly over it. ok is false when there is no
// decodable image to place.
func (m DiffViewModel) ImagePlacementAt(termW, termH int) (row, col, cols, rows int, ok bool) {
	if !m.isImage || m.img == nil {
		return 0, 0, 0, 0, false
	}
	m.width, m.height = termW, termH
	mh, mv, cw, ch := m.layout()
	vh := ch - m.headerHeight() - diffFooterHeight
	if vh < 1 {
		vh = 1
	}
	b := m.img.Bounds()
	outW, outH := fitImage(b.Dx(), b.Dy(), cw, vh*2)
	imgRows := (outH + 1) / 2
	// Screen rows: box border at mv, header rows, then the centered top pad.
	row = mv + 1 + m.headerHeight() + (vh-imgRows)/2 + 1
	col = mh + 1 + (cw-outW)/2 + 1
	return row, col, outW, imgRows, true
}

// WithKittyHires arms the hi-res path: the image is (down)scaled within
// kittyMaxSide, PNG-encoded once, and remembered along with the writer to the
// terminal. Nothing is written yet — bubbletea enters the ALT screen only at
// Run(), and a placement made on the main screen wouldn't show there — the
// first WindowSizeMsg does the transmit (see Update). An encode failure just
// leaves hi-res unarmed; the half-block preview stands alone.
func (m DiffViewModel) WithKittyHires(w io.Writer, tmux bool) DiffViewModel {
	if m.img == nil {
		return m
	}
	img := m.img
	b := img.Bounds()
	if b.Dx() > kittyMaxSide || b.Dy() > kittyMaxSide {
		fw, fh := fitImage(b.Dx(), b.Dy(), kittyMaxSide, kittyMaxSide)
		img = scaleImage(img, fw, fh)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return m
	}
	m.kittyOut = w
	m.kittyTmux = tmux
	m.kittyPNG = buf.Bytes()
	return m
}

// placeKittyImage writes the hi-res placement for the current size: the first
// call transmits the PNG and displays it; later calls (resizes) drop the old
// placement and re-place the stored image at the new rectangle. The cursor is
// saved/restored around the move so the text frame isn't disturbed.
func (m *DiffViewModel) placeKittyImage() {
	if m.kittyOut == nil {
		return
	}
	row, col, cols, rows, ok := m.ImagePlacementAt(m.width, m.height)
	if !ok {
		return
	}
	var b strings.Builder
	if m.kittySent {
		b.WriteString(kittyDeletePlacements(kittyImageID, m.kittyTmux))
	}
	b.WriteString("\x1b7")
	b.WriteString(cupTo(row, col))
	if m.kittySent {
		b.WriteString(kittyPlace(kittyImageID, cols, rows, m.kittyTmux))
	} else {
		b.WriteString(kittyTransmitDisplay(m.kittyPNG, kittyImageID, cols, rows, m.kittyTmux))
		m.kittySent = true
	}
	b.WriteString("\x1b8")
	io.WriteString(m.kittyOut, b.String())
}

// KittyCleanup deletes the transmitted image and its placements. The cmd layer
// calls it after the program exits, before the TTY closes; leaving the alt
// screen usually clears placements anyway, but terminals keep the DATA until
// it's deleted, and the next popup shouldn't inherit a stale store.
func (m DiffViewModel) KittyCleanup() {
	if m.kittyOut == nil || !m.kittySent {
		return
	}
	io.WriteString(m.kittyOut, kittyDeleteAll(m.kittyTmux))
}
