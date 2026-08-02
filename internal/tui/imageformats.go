package tui

import (
	"path/filepath"
	"sort"
	"strings"
)

// previewableImageExts is every image extension the ledger opens as a PREVIEW
// instead of a diff. Membership means the pager can decode it (see
// decodeImage): pure Go for the raster formats with decoders in this module,
// macOS ImageIO via sips for the rest. The shell renderer keeps the same list
// as a case glob (`is_image_file` in lib/compact-view.sh) — a pane picks
// between the two renderers by binary capability, so the lists are pinned to
// each other by TestIsImageFile_matches_the_Go_renderers_list.
var previewableImageExts = map[string]bool{
	".png": true, ".apng": true,
	".jpg": true, ".jpeg": true, ".jpe": true, ".jfif": true,
	".gif":  true,
	".webp": true,
	".bmp":  true, ".dib": true,
	".tif": true, ".tiff": true,
	".ico": true, ".icns": true,
	".heic": true, ".heif": true,
	".avif": true,
	".svg":  true,
}

// textImageExts are the previewable formats git tracks as TEXT: they get a real
// line-count row in the ledger (and a real byte size only when hydrated as
// binary), so they must not be routed through the byte-delta row.
var textImageExts = map[string]bool{".svg": true}

// IsPreviewableImage reports whether path's extension names an image format the
// preview popup can decode. Extension, not content: the choice between the
// preview and the diff popup is made before a single byte is read.
func IsPreviewableImage(path string) bool {
	return previewableImageExts[strings.ToLower(filepath.Ext(path))]
}

// PreviewableImageExtensions returns every admitted extension (lowercase, with
// the leading dot) in a stable order, for tests and for the shell parity guard.
func PreviewableImageExtensions() []string {
	out := make([]string, 0, len(previewableImageExts))
	for ext := range previewableImageExts {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out
}
