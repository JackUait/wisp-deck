package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ledger tints each file row's NAME with its section title's color —
// yellow under "modified", green under "staged", cyan under "new" — so a
// section reads as one colored block. The +/- counts keep their own
// green/red semantics.

func TestFormatLedgerRow_filename_takes_section_color(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_ledger_row",
		[]string{"5", "3", "file.go", "\x1b[33m"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[33mfile.go")
	assertNotContains(t, out, "\x1b[97m")
}

func TestFormatLedgerRow_defaults_to_bright_without_color(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_ledger_row",
		[]string{"5", "3", "file.go"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[97mfile.go")
}

func TestFormatLedgerSizeRow_filename_takes_section_color(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_ledger_size_row",
		[]string{"100", "300", "pic.png", "\x1b[36m"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[36mpic.png")
	assertNotContains(t, out, "\x1b[97m")
}

func TestFormatLedgerSizeRow_defaults_to_bright_without_color(t *testing.T) {
	out, code := runBashFunc(t, "lib/compact-view.sh", "format_ledger_size_row",
		[]string{"100", "300", "pic.png"}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[97mpic.png")
}

// format_image_row threads the section color through to the size row.
func TestFormatImageRow_threads_section_color(t *testing.T) {
	dir := t.TempDir()
	git := discardGitRepo(t, dir)
	git("init", "-q")
	writeTempFile(t, dir, "new.png", "PNGDATA")
	out, code := cvFuncArgv(t, "format_image_row", dir, "untracked", "new.png", "new.png", "\x1b[36m")
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[36mnew.png")
	assertNotContains(t, out, "\x1b[97m")
}

// render_group is nested inside compact_view, so guard statically that it
// hands its group color to both row formatters — otherwise the tint never
// reaches real rows.
func TestRenderGroup_passes_group_color_to_row_formatters(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(projectRoot(t), "lib", "compact-view.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if !strings.Contains(text, `format_ledger_row "$added" "$deleted" "$display" "$gcolor"`) {
		t.Errorf("render_group must pass its group color to format_ledger_row")
	}
	if !strings.Contains(text, `format_image_row "$project_dir" "$sizeref" "$file" "$display" "$gcolor"`) {
		t.Errorf("render_group must pass its group color to format_image_row")
	}
}
