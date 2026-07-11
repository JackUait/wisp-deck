# Image preview in the file list — design

Date: 2026-07-11
Status: approved (autonomous goal session)

## Problem

Clicking an image (png/jpg/gif) in the compact-view file ledger opens the diff
popup, but `git diff` emits only "Binary files … differ" — the popup is
useless. Images should be previewable from the file list.

## Approaches considered

1. **Half-block ANSI preview rendered by `wisp-deck-tui`** (chosen). Decode the
   image with the Go stdlib (`image/png`, `image/jpeg`, `image/gif`), scale it
   to the popup's content width, and render it as truecolor `▀` cells — each
   character cell carries two vertical pixels (foreground = top, background =
   bottom). Works anywhere truecolor works (Ghostty), survives tmux popups,
   reuses the existing modal chrome (backdrop, discard, close, scrolling), and
   is deterministic/testable.
2. Kitty graphics protocol — real pixels, but fragile through tmux popups +
   Bubbletea's alt screen, and untestable in CI. Rejected.
3. Shell out to `qlmanage -p` / `open` — leaves the terminal, macOS-only UX,
   no discard integration. Rejected.

## Design

### Bash (`lib/compact-view.sh`)

- `is_image_file <path>` — case-insensitive extension match on
  `png|jpg|jpeg|gif`. Only formats the Go stdlib decodes; anything else keeps
  the current diff behavior.
- `open_diff_popup` branches: when the clicked path is an image that exists on
  disk, the popup command becomes
  `cat <file> | wisp-deck-tui diff-view --image --status <status> …`
  (no awk header-strip; raw bytes on stdin keeps the existing pipe pattern).
  `<status>` is `added` for an untracked file (`git ls-files --error-unmatch`
  fails), `modified` otherwise — the pager can't derive status from a binary
  body. Backdrop, theme, and discard-file plumbing are unchanged, so
  discarding an image's changes still works.

### Go (`internal/tui`)

- `imageview.go`: `renderImagePreview(img image.Image, width int) string` —
  nearest-neighbor downscale to fit `width` columns (never upscales), rows of
  `▀` with truecolor fg/bg per pixel pair; alpha composited over a dark gray.
- `DiffViewModel` gains an image mode: `NewImageView(title string, data
  []byte, status string) DiffViewModel`. It behaves like `singleView` (no
  layout/context tabs), the header shows the status badge + path + `W×H`
  dimensions instead of `+N −N`, and the body re-renders at the content width
  on resize. Undecodable data shows "(no preview: <err>)" instead of crashing.
  Discard, backdrop, scrolling, and all close paths are inherited.

### CLI (`cmd/wisp-deck-tui/diff_view.go`)

- `diff-view --image` reads raw image bytes from stdin and builds the image
  model; `--status added|modified|deleted` sets the badge. Without `--image`
  nothing changes.

## Testing

- Go unit tests: preview geometry (scale-to-fit, no upscale, half-block
  count), truecolor output, decode-failure fallback, image-mode header
  (badge/dimensions, no tabs), resize re-render.
- Bash integration tests (`test/bash/compact_view_test.go`): `is_image_file`
  matrix; `open_diff_popup` on an image composes `--image --status …` and
  skips the awk strip; non-image path keeps the diff pipeline.

## Error handling

- Missing/unreadable file or undecodable bytes → in-popup fallback text.
- No tmux → `open_diff_popup` already no-ops.
