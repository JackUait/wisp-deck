package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── Ledger cost must not blow up with many files ────────────────────────────
//
// With a large changeset the ledger became unusable: apply_checkboxes ran on
// EVERY hover repaint and called nth_line — a front-to-back rescan of the
// body map, each behind a herestring temp file — once PER body line, i.e.
// O(N²) line reads per mouse-motion settle (~10s at 300 files). The build
// tick separately forked a $() subshell per file ($(format_file …) in
// render_group, $(numstat_path …) in body_path_map), so a 2s refresh burned
// >2s of CPU at 300 files. These tests pin the property: per-file work must
// stay fork-free and single-pass. The wall-clock bounds are deliberately
// generous (the broken versions exceed them ~5-10×, the fixed ones sit
// ~50-100× under), so parallel-suite load cannot flake them.

// makeLedgerBodyAndMap builds an n-row rendered body and its path map, the
// two lockstep inputs apply_checkboxes consumes.
func makeLedgerBodyAndMap(n int) (string, string) {
	var body, bodyMap strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&body, "   +12 −3  file_%d.go\n", i)
		fmt.Fprintf(&bodyMap, "src/dir/file_%d.go\n", i)
	}
	return strings.TrimSuffix(body.String(), "\n"), strings.TrimSuffix(bodyMap.String(), "\n")
}

// apply_checkboxes runs on every hover repaint, so it must make a single
// lockstep pass over body+map — never a per-line rescan of the whole map.
// The quadratic version needs ~30s for 400 rows; the linear one ~0.1s.
func TestApplyCheckboxes_hover_stays_fast_with_many_files(t *testing.T) {
	body, bodyMap := makeLedgerBodyAndMap(400)
	start := time.Now()
	out, code := cvFuncArgv(t, "apply_checkboxes", body, bodyMap, "src/dir/file_2.go", "5")
	elapsed := time.Since(start)
	assertExitCode(t, code, 0)
	if elapsed > 10*time.Second {
		t.Fatalf("apply_checkboxes took %v for 400 rows — per-row work is rescanning the map "+
			"(O(N²)); it must walk body and map in ONE lockstep pass", elapsed)
	}
	// Sanity: the pass still marks the selected row (☑) and the hovered row (☐).
	if !strings.Contains(out, "☑") || !strings.Contains(out, "☐") {
		t.Errorf("checkbox markers missing from output: %q", out[:min(len(out), 200)])
	}
}

// body_path_map runs on every build tick; resolving each numstat path must
// not fork a $() subshell per file (~4ms each — seconds at scale).
func TestBodyPathMap_stays_fast_with_many_files(t *testing.T) {
	var numstat strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&numstat, "12\t3\tsrc/dir/file_%d.go\n", i)
	}
	start := time.Now()
	out, code := cvFuncArgv(t, "body_path_map", "", strings.TrimSuffix(numstat.String(), "\n"), "")
	elapsed := time.Since(start)
	assertExitCode(t, code, 0)
	if elapsed > 4*time.Second {
		t.Fatalf("body_path_map took %v for 2000 rows — it is forking a subshell per file; "+
			"numstat_path must be called fork-free", elapsed)
	}
	if !strings.Contains(out, "src/dir/file_1999.go") {
		t.Errorf("map is missing the last file's path")
	}
}

// Static guard (deterministic, unlike the wall-clock checks above, so it holds
// even under heavy parallel-suite load): apply_checkboxes must never go back to
// nth_line — a front-to-back rescan of the map per body line is exactly the
// O(N²) that froze the pane. The map is read in lockstep on fd 3 instead.
func TestApplyCheckboxes_never_rescans_map_via_nth_line(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(projectRoot(t), "lib", "compact-view.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fn := extractBashFunc(string(src), "apply_checkboxes")
	if fn == "" {
		t.Fatal("apply_checkboxes not found in lib/compact-view.sh")
	}
	if strings.Contains(fn, "nth_line") {
		t.Errorf("apply_checkboxes calls nth_line: that rescans the whole map PER BODY LINE " +
			"(O(N²) — ~10s per hover repaint at 300 files); read the map in lockstep on fd 3 instead")
	}
}

// Static guard: the ledger source must never capture format_file or
// numstat_path with a $() subshell — both run once per file on hot paths, and
// a fork per file made the build tick cost scale with the changeset size.
// Both helpers publish their result in a global (FORMAT_FILE / NUMSTAT_PATH)
// exactly so callers can avoid the capture.
func TestLedger_no_per_file_command_substitution(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(projectRoot(t), "lib", "compact-view.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"$(format_file", "$(numstat_path"} {
		if strings.Contains(string(src), banned) {
			t.Errorf("lib/compact-view.sh contains %q: that forks a subshell PER FILE on a "+
				"per-tick/per-repaint path; read the %s global instead",
				banned, map[string]string{"$(format_file": "FORMAT_FILE", "$(numstat_path": "NUMSTAT_PATH"}[banned])
		}
	}
}

// The fork-free contract: format_file and numstat_path leave their result in
// FORMAT_FILE / NUMSTAT_PATH so hot-path callers skip the $() capture.
func TestFormatFile_publishes_FORMAT_FILE_global(t *testing.T) {
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")
	out, code := runBashSnippet(t,
		`source `+module+` && format_file "a/b/verylongfilename.go" 8 >/dev/null && printf '%s' "$FORMAT_FILE"`, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "verylon…" {
		t.Errorf("FORMAT_FILE = %q, want %q", got, "verylon…")
	}
}

func TestNumstatPath_publishes_NUMSTAT_PATH_global(t *testing.T) {
	module := filepath.Join(projectRoot(t), "lib", "compact-view.sh")
	out, code := runBashSnippet(t,
		`source `+module+` && numstat_path "src/{old => new}/f.go" >/dev/null && printf '%s' "$NUMSTAT_PATH"`, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != "src/new/f.go" {
		t.Errorf("NUMSTAT_PATH = %q, want %q", got, "src/new/f.go")
	}
}
