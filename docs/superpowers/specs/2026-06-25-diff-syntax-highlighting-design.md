# Diff Popup Syntax Highlighting — Design

**Date:** 2026-06-25
**Status:** Approved (design); pending implementation plan

## Problem

Clicking a file in the ledger's file list calls `open_diff_popup`
(`lib/compact-view.sh`), which runs:

```
git diff HEAD -U999999 --color=always -- <file> | awk 'f;/@@/{f=1}' | wisp-deck-tui diff-view ...
```

and pipes the result to the custom Go pager (`internal/tui/diffview.go`). The
only coloring is git's **diff-level** coloring: added lines are wholly green,
deleted lines wholly red, context plain. The code itself is never
syntax-highlighted. Worse, on changed lines — exactly where you want to read the
code — the green/red foreground overwrites any token meaning.

## Goal

Language-aware syntax highlighting of the code shown in the popup, with diff
add/delete status conveyed by a **background tint** so both the code *and* the
change status stay readable on every line. Pure-Go (no new runtime dependency),
with all existing pager features preserved: inline / side-by-side layout,
changes-only / full-file visibility, click-to-close, and per-tool chrome
theming.

## Decisions (confirmed with user)

- **Engine:** `github.com/alecthomas/chroma` — pure-Go, embedded into the
  `wisp-deck-tui` binary. Keeps the self-contained binary that installs from
  release assets and integrates with the existing custom pager rather than
  replacing it with `delta`/`bat`.
- **Diff styling:** keep language-aware foreground colors on **every** line;
  show add/delete via a subtle green/red **background** tint (the delta/bat
  approach). Code stays readable and diff status stays clear, even on changed
  lines.

## Approach

Move **all** coloring out of git and into the Go pager. git emits a *structural*
diff (markers, no color); the pager owns language detection, highlighting, and
diff tinting.

### 1. Pipeline change — `lib/compact-view.sh`

`--color=always` → `--color=never`. The body still carries the `+` / `-` /
leading-space structural markers the pager already classifies by; it simply
arrives uncolored. The `awk '/@@/'` header strip is unchanged (`--color=never`
makes the `@@` line plain, so the match is if anything more robust).

### 2. Highlighting module — `internal/tui/highlight.go` (new)

A pure function:

```go
func highlightDiff(body, filename string) string
```

- **Language detection** from the `--title` filename via chroma's
  lexer-by-filename match; fall back to chroma content analysis, then to a no-op
  plain lexer for unknown / non-code files.
- **Whole-document tokenization** — the part that makes highlighting *correct*.
  Reconstruct the two file versions from the unified diff:
  - *new* = context lines + added (`+`) lines, in order
  - *old* = context lines + removed (`-`) lines, in order

  Tokenize **each whole file** with chroma, then map the per-line colored output
  back onto the diff lines (context and `+` come from *new*; `-` comes from
  *old*). This is the delta/bat approach: it is why multi-line constructs (block
  comments, multi-line strings, here-docs) highlight correctly instead of
  bleeding, which is what naive line-by-line highlighting gets wrong.
- **Foreground only:** consume chroma token *foreground* colors and ignore each
  style's *background*, so chroma never paints the whole popup. Emit ANSI via
  termenv's detected color profile (truecolor → 256 → 16) so weaker terminals
  degrade gracefully. One tasteful dark style to start (e.g. `github-dark` or
  `catppuccin-mocha`); not user-configurable yet.

Each rebuilt line is `marker char` + ANSI-highlighted code. This preserves the
existing `classifyDiffLine` / `dropMarker` helpers, which strip ANSI and read
the leading marker — so collapse, line-numbering, and side-by-side keep working
untouched.

### 3. Diff tint — existing renderers in `internal/tui/diffview.go`

`numberLines` (inline) and `sbsCellStr` / `renderSideBySide` gain a subtle
full-width **green background** for added lines and **red background** for
removed lines; context lines stay untinted. Inline lines are padded to the
viewport width so the tint spans the whole row. These background colors stay
fixed (semantic, not themed), consistent with the existing fixed green/red
add/delete foreground styling that the design notes is "intentionally left
fixed — they carry meaning, not theme."

### Where highlighting runs

Once, in `NewDiffView` (which already receives the full body): detect language →
reconstruct old/new → tokenize → store the highlighted body as `m.content`.
Mode/visibility toggles re-render from `m.content` exactly as they do today, so
toggling stays instant — no re-tokenizing, no popup respawn. Plain / non-code
content passes through uncolored, so existing tests that feed hand-built diffs
are unaffected.

## Components / boundaries

- **`highlight.go`** — pure `highlightDiff(body, filename) string`. Input: a
  structural diff body + the filename. Output: the same body with per-line
  syntax ANSI and markers intact. No Bubbletea, no I/O — unit-testable in
  isolation.
- **`diffview.go`** — unchanged classification / collapse / layout logic; the
  renderers gain background-tint wrapping for added/removed rows.
- **`compact-view.sh`** — one-flag change (`--color=always` → `--color=never`).

## Error handling

Unknown language, empty diff, or any lexer/format error returns the body
unchanged (plain). Highlighting must never fail the popup.

## Testing (TDD — tests first, watch them fail, then implement)

- `highlight_test.go`:
  - extension → lexer mapping (e.g. `.go`, `.sh`, `.ts`, `.py`)
  - unknown extension → plain passthrough (body returned unchanged)
  - **multi-line string / block comment does not bleed across diff lines** (the
    correctness regression test)
  - marker preserved and still classifiable (`classifyDiffLine`) after highlight
  - foreground-only: no style background SGR emitted
- renderer tests in `diffview_test.go`:
  - added line carries a green background, removed a red background, context none
  - the background tint spans the full row width
- `test/bash/compact_view_test.go`:
  - flip the assertion `color=always` → `color=never` first, watch it fail, then
    change the script (bug-fix / change IRON RULE order)
- The full existing `internal/tui` diffview suite must stay green.

## Out of scope (YAGNI — future follow-ups)

- User-selectable / per-tool syntax themes
- Light-terminal background detection
- Syntax highlighting anywhere other than the diff popup
