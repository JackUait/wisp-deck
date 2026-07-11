package tui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/draw"
	"image/png"
	"io"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
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

// toRGBA returns img as *image.RGBA, converting once via image/draw (which has
// optimized per-format paths, e.g. for JPEG's YCbCr) so pixel loops can read
// Pix directly instead of paying an interface call per pixel.
func toRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	return rgba
}

// scaleImage box-filters img down to w×h: each output pixel is the average of
// its source rectangle, so downscaled previews stay smooth instead of aliasing
// the way nearest-neighbor sampling does. Operates on raw RGBA bytes for speed.
func scaleImage(img image.Image, w, h int) *image.RGBA {
	src := toRGBA(img)
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
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
			var r, g, bl, a uint64
			for sy := sy0; sy < sy1; sy++ {
				row := src.Pix[sy*src.Stride+sx0*4 : sy*src.Stride+sx1*4]
				for i := 0; i < len(row); i += 4 {
					r += uint64(row[i])
					g += uint64(row[i+1])
					bl += uint64(row[i+2])
					a += uint64(row[i+3])
				}
			}
			n := uint64((sy1 - sy0) * (sx1 - sx0))
			i := out.PixOffset(x, y)
			out.Pix[i+0] = uint8(r / n)
			out.Pix[i+1] = uint8(g / n)
			out.Pix[i+2] = uint8(bl / n)
			out.Pix[i+3] = uint8(a / n)
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

// pngMagic is the PNG file signature, used to recognize sources kitty can
// display without a re-encode.
var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// kittyPayload returns the PNG bytes to transmit for an image whose original
// file bytes are src. A source that is ALREADY a PNG within kittyMaxSide is
// passed through verbatim — no decode/encode cost at all (screenshots, the
// common case). Anything else (JPEG, GIF, oversized) is (down)scaled within
// the cap and PNG-encoded at BestSpeed: PNG compression is lossless at every
// level, so speed costs bytes, never quality. Returns nil on encode failure.
func kittyPayload(img image.Image, src []byte) []byte {
	b := img.Bounds()
	if b.Dx() <= kittyMaxSide && b.Dy() <= kittyMaxSide {
		if bytes.HasPrefix(src, pngMagic) {
			return src
		}
	} else {
		fw, fh := fitImage(b.Dx(), b.Dy(), kittyMaxSide, kittyMaxSide)
		img = scaleImage(img, fw, fh)
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// kittySentMsg reports the async transmit finished. w/h carry the popup size
// the placement was computed for, so Update can re-place if a resize landed
// while the transmit was in flight. ok=false means the encode failed (or the
// popup closed first) and hi-res should stand down.
type kittySentMsg struct {
	w, h int
	ok   bool
}

// kittyShared is the mutable state shared between the model (a value that
// bubbletea copies) and the async transmit goroutine. The mutex serializes the
// terminal writes and lets close-time cleanup both suppress a not-yet-started
// transmit and WAIT OUT one that is mid-write — a transmit landing after the
// alt screen exits would paint the image onto the user's normal screen, and a
// write truncated by process exit would leave the terminal parser inside an
// APC sequence.
type kittyShared struct {
	mu     sync.Mutex
	closed bool
	sent   bool
}

// WithKittyHires arms the hi-res path: it remembers the terminal writer; the
// actual encode + transmit runs asynchronously off the first WindowSizeMsg
// (see Update) so the popup's first paint is never blocked — and because
// bubbletea enters the ALT screen only at Run(), where placements must live.
func (m DiffViewModel) WithKittyHires(w io.Writer, tmux bool) DiffViewModel {
	if m.img == nil {
		return m
	}
	m.kittyOut = w
	m.kittyTmux = tmux
	m.kittyShared = &kittyShared{}
	return m
}

// kittyTransmitCmd builds the one-shot async command that encodes the payload
// and writes the positioned transmit-and-display sequence. Geometry is
// captured now; if the popup resizes mid-flight, the kittySentMsg handler
// re-places at the fresh size.
func (m DiffViewModel) kittyTransmitCmd() tea.Cmd {
	out, tmux, shared := m.kittyOut, m.kittyTmux, m.kittyShared
	img, src := m.img, m.imgSrc
	w, h := m.width, m.height
	row, col, cols, rows, ok := m.ImagePlacementAt(w, h)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		payload := kittyPayload(img, src)
		if payload == nil {
			return kittySentMsg{ok: false}
		}
		shared.mu.Lock()
		defer shared.mu.Unlock()
		if shared.closed { // popup already closed: never touch the terminal
			return kittySentMsg{ok: false}
		}
		io.WriteString(out, "\x1b7"+cupTo(row, col)+
			kittyTransmitDisplay(payload, kittyImageID, cols, rows, tmux)+"\x1b8")
		shared.sent = true
		return kittySentMsg{w: w, h: h, ok: true}
	}
}

// rePlaceKitty moves the already-transmitted image to the current size's
// rectangle: drop the old placement, place anew. Small synchronous write.
func (m *DiffViewModel) rePlaceKitty() {
	row, col, cols, rows, ok := m.ImagePlacementAt(m.width, m.height)
	if !ok {
		return
	}
	m.kittyShared.mu.Lock()
	defer m.kittyShared.mu.Unlock()
	if m.kittyShared.closed || !m.kittyShared.sent {
		return
	}
	io.WriteString(m.kittyOut, kittyDeletePlacements(kittyImageID, m.kittyTmux)+
		"\x1b7"+cupTo(row, col)+kittyPlace(kittyImageID, cols, rows, m.kittyTmux)+"\x1b8")
}

// KittyCleanup closes the hi-res channel: it marks the popup closed (so an
// in-flight transmit is suppressed, waiting out one that is mid-write) and,
// if a transmit completed, deletes the image from the terminal's store.
// Leaving the alt screen usually clears placements, but terminals keep the
// DATA until it's deleted, and the next popup shouldn't inherit a stale store.
// The cmd layer calls it after the program exits, before the TTY closes.
func (m DiffViewModel) KittyCleanup() {
	if m.kittyOut == nil || m.kittyShared == nil {
		return
	}
	m.kittyShared.mu.Lock()
	defer m.kittyShared.mu.Unlock()
	m.kittyShared.closed = true
	if m.kittyShared.sent {
		io.WriteString(m.kittyOut, kittyDeleteAll(m.kittyTmux))
	}
}
