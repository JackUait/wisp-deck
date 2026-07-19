package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// statusline.sh tests (TestStatusline_*)
// ============================================================

// --- get_tree_rss_kb ---

func TestStatusline_get_tree_rss_kb_sums_memory_of_process_and_its_children(t *testing.T) {
	dir := t.TempDir()

	// Mock pgrep: 100 -> [101, 102], 101 -> [103], others -> exit 1
	mockCommand(t, dir, "pgrep", `
pid="${@: -1}"
case "$pid" in
  100) printf '101\n102\n' ;;
  101) printf '103\n' ;;
  *) exit 1 ;;
esac
`)

	// Mock ps: return RSS per pid
	mockCommand(t, dir, "ps", `
pid="${@: -1}"
case "$pid" in
  100) echo "  51200" ;;
  101) echo "  25600" ;;
  102) echo "  10240" ;;
  103) echo "  5120" ;;
  *) echo "" ;;
esac
`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_rss_kb", []string{"100"}, env)
	assertExitCode(t, code, 0)
	// 51200 + 25600 + 10240 + 5120 = 92160
	if strings.TrimSpace(out) != "92160" {
		t.Errorf("expected 92160, got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_get_tree_rss_kb_handles_process_with_no_children(t *testing.T) {
	dir := t.TempDir()

	mockCommand(t, dir, "pgrep", `exit 1`)
	mockCommand(t, dir, "ps", `echo "  51200"`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_rss_kb", []string{"100"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "51200" {
		t.Errorf("expected 51200, got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_get_tree_rss_kb_handles_disappeared_process_gracefully(t *testing.T) {
	dir := t.TempDir()

	mockCommand(t, dir, "pgrep", `exit 1`)
	mockCommand(t, dir, "ps", `echo ""`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_rss_kb", []string{"999"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "0" {
		t.Errorf("expected 0, got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_get_tree_rss_kb_handles_child_that_disappears_mid_walk(t *testing.T) {
	dir := t.TempDir()

	// 100 -> [101, 102], others -> exit 1
	mockCommand(t, dir, "pgrep", `
pid="${@: -1}"
case "$pid" in
  100) printf '101\n102\n' ;;
  *) exit 1 ;;
esac
`)

	// 101 returns empty (disappeared), 102 returns value
	mockCommand(t, dir, "ps", `
pid="${@: -1}"
case "$pid" in
  100) echo "  51200" ;;
  101) echo "" ;;
  102) echo "  10240" ;;
  *) echo "" ;;
esac
`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_rss_kb", []string{"100"}, env)
	assertExitCode(t, code, 0)
	// 51200 + 0 + 10240 = 61440
	if strings.TrimSpace(out) != "61440" {
		t.Errorf("expected 61440, got %q", strings.TrimSpace(out))
	}
}

// --- statusline-command.sh: session line diff ---

// statuslineCmdSetupGitRepo creates a temp git repo with one initial commit.
// Returns (repo dir, cleanup func).
func statuslineCmdSetupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "-C", dir, "init", "-q"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git setup failed: %v\n%s", err, out)
		}
	}

	// Create initial file and commit
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("initial\n"), 0644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	for _, c := range [][]string{
		{"git", "-C", dir, "add", "file.txt"},
		{"git", "-C", dir, "commit", "-q", "-m", "initial"},
	} {
		cmd := exec.Command(c[0], c[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git setup failed: %v\n%s", err, out)
		}
	}

	return dir
}

// getBaselineSHA returns the current HEAD SHA for a git repo.
func getBaselineSHA(t *testing.T, repoDir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestStatusline_statusline_command_omits_diff_counts_even_with_baseline_set(t *testing.T) {
	repoDir := statuslineCmdSetupGitRepo(t)
	baselineSHA := getBaselineSHA(t, repoDir)
	repoBasename := filepath.Base(repoDir)

	// Change the working tree so a diff would exist if counts were rendered.
	f, err := os.OpenFile(filepath.Join(repoDir, "file.txt"), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	if _, err := f.WriteString("line1\nline2\nline3\n"); err != nil {
		t.Fatalf("write file: %v", err)
	}
	f.Close()

	baselineFile := filepath.Join(t.TempDir(), "baseline")
	if err := os.WriteFile(baselineFile, []byte(baselineSHA+"\n"), 0644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	root := projectRoot(t)
	cmdPath := filepath.Join(root, "templates", "statusline-command.sh")
	stdinData := fmt.Sprintf(`{"current_dir":"%s"}`, repoDir)
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, cmdPath)

	env := buildEnv(t, nil, "WISP_DECK_BASELINE_FILE="+baselineFile)
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, repoBasename)
	assertNotContains(t, out, "+3")
	assertNotContains(t, out, "/ -")
}

func TestStatusline_statusline_command_falls_back_to_repo_branch_only_without_baseline(t *testing.T) {
	repoDir := statuslineCmdSetupGitRepo(t)
	repoBasename := filepath.Base(repoDir)

	root := projectRoot(t)
	cmdPath := filepath.Join(root, "templates", "statusline-command.sh")
	stdinData := fmt.Sprintf(`{"current_dir":"%s"}`, repoDir)
	// Explicitly unset WISP_DECK_BASELINE_FILE
	script := fmt.Sprintf(`unset WISP_DECK_BASELINE_FILE; echo '%s' | bash '%s'`, stdinData, cmdPath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, repoBasename)
	assertNotContains(t, out, "+0")
	assertNotContains(t, out, "/ -")
}

func TestStatusline_statusline_command_falls_back_when_baseline_file_missing(t *testing.T) {
	repoDir := statuslineCmdSetupGitRepo(t)
	repoBasename := filepath.Base(repoDir)

	root := projectRoot(t)
	cmdPath := filepath.Join(root, "templates", "statusline-command.sh")
	stdinData := fmt.Sprintf(`{"current_dir":"%s"}`, repoDir)
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, cmdPath)

	env := buildEnv(t, nil, "WISP_DECK_BASELINE_FILE=/tmp/wisp-deck-nonexistent-baseline")
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, repoBasename)
	assertNotContains(t, out, "+0")
	assertNotContains(t, out, "/ -")
}

func TestStatusline_statusline_command_non_git_directory_shows_just_dirname(t *testing.T) {
	nonGitDir := t.TempDir()
	dirBasename := filepath.Base(nonGitDir)

	root := projectRoot(t)
	cmdPath := filepath.Join(root, "templates", "statusline-command.sh")
	stdinData := fmt.Sprintf(`{"current_dir":"%s"}`, nonGitDir)
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, cmdPath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, dirBasename)
	assertNotContains(t, out, "+0")
	assertNotContains(t, out, "/ -")
}

func TestStatusline_statusline_command_prefixes_worktree_icon_before_name(t *testing.T) {
	const worktreeIcon = "\U000F0645" // Nerd Font file-tree glyph 󰙅

	nonGitDir := t.TempDir()
	dirBasename := filepath.Base(nonGitDir)

	root := projectRoot(t)
	cmdPath := filepath.Join(root, "templates", "statusline-command.sh")
	stdinData := fmt.Sprintf(`{"current_dir":"%s"}`, nonGitDir)
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, cmdPath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, worktreeIcon)
	// The icon must come first, before the project name.
	if strings.Index(out, worktreeIcon) > strings.Index(out, dirBasename) {
		t.Errorf("worktree icon should precede the project name in %q", out)
	}
}

func TestStatusline_statusline_command_omits_branch_name(t *testing.T) {
	repoDir := statuslineCmdSetupGitRepo(t)
	repoBasename := filepath.Base(repoDir)

	// Check out a uniquely-named branch so its presence is unambiguous.
	branchName := "wisp-deck-omit-branch-check"
	cmd := exec.Command("git", "-C", repoDir, "checkout", "-q", "-b", branchName)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout failed: %v\n%s", err, out)
	}

	root := projectRoot(t)
	cmdPath := filepath.Join(root, "templates", "statusline-command.sh")
	stdinData := fmt.Sprintf(`{"current_dir":"%s"}`, repoDir)
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, cmdPath)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, repoBasename)
	assertNotContains(t, out, branchName)
}

// Nerd Font glyphs the wrapper prefixes onto each metric so context %, memory,
// and CPU are distinguishable at a glance. Kept in sync with statusline-wrapper.sh.
const (
	ctxIcon = "\U000F09D1" // nf-md-brain     — context window
	memIcon = "\U000F01BC" // nf-md-database  — memory load (cylinder; distinct from the CPU chip)
	cpuIcon = "\U0000F4BC" // nf-oct-cpu      — CPU load (chip)
)

// --- statusline-wrapper.sh: metric icons ---

func TestStatusline_wrapper_prefixes_memory_segment_with_icon(t *testing.T) {
	env := setupWrapperMemTest(t, "/Users/test/.local/bin/claude", "50")

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, memIcon+" 50M")
}

func TestStatusline_wrapper_prefixes_cpu_segment_with_icon(t *testing.T) {
	dir, fakeHome := wrapperHomeWithCmd(t)
	mockCommand(t, dir, "footprint", `printf '    phys_footprint: 30 MB\n'`)
	mockCommand(t, dir, "ps", `
case "$*" in
  *comm=*)  printf '%s\n' "/Users/test/.local/bin/claude" ;;
  *%cpu=*)  printf '%s\n' " 42.4" ;;
  *rss=*)   printf '%s\n' "51200" ;;
  *ppid=*)  printf '%s\n' "1" ;;
esac
`)
	env := buildEnv(t, []string{filepath.Join(dir, "bin")}, "HOME="+fakeHome)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, cpuIcon+" 42%")
}

func TestStatusline_wrapper_prefixes_context_segment_with_icon(t *testing.T) {
	env := setupWrapperTest(t)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	// The icon is colored (yellow) and reset before the uncolored context value
	// that ccstatusline emits, so a reset sequence sits between them.
	assertContains(t, out, "\x1b[01;33m"+ctxIcon+"\x1b[00m 12.3%")
}

// --- statusline-wrapper.sh: model segment ---

// setupWrapperTest creates a fake home with a mock statusline-command.sh and
// mock npx/ps commands so the wrapper can run hermetically.
// Returns the env to run the wrapper with.
func setupWrapperTest(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()

	fakeHome := filepath.Join(dir, "home")
	writeTempFile(t, fakeHome, ".claude/statusline-command.sh", `echo "GITINFO"`)

	// npx ccstatusline -> context percentage
	mockCommand(t, dir, "npx", `echo "12.3%"`)
	// ps: comm= -> non-claude name, ppid= -> 1 (terminates parent walk)
	mockCommand(t, dir, "ps", `
if [[ "$*" == *"comm="* ]]; then echo "sh"; else echo "1"; fi
`)

	binDir := filepath.Join(dir, "bin")
	return buildEnv(t, []string{binDir}, "HOME="+fakeHome)
}

// wrapperHome recovers the fake HOME that setupWrapperTest baked into the env so
// a test can seed files (e.g. the accounts list) under it.
func wrapperHome(env []string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			return strings.TrimPrefix(kv, "HOME=")
		}
	}
	return ""
}

// setupWrapperMemTest creates a hermetic env for the wrapper where the parent
// process walk resolves to a process whose `comm` is claudeComm. Both footprint
// and RSS report memMB megabytes (no children), so the rendered memory segment
// is "<memMB>M" regardless of which metric the wrapper uses.
func setupWrapperMemTest(t *testing.T, claudeComm, memMB string) []string {
	t.Helper()
	dir := t.TempDir()

	fakeHome := filepath.Join(dir, "home")
	writeTempFile(t, fakeHome, ".claude/statusline-command.sh", `echo "GITINFO"`)

	// npx ccstatusline -> context percentage
	mockCommand(t, dir, "npx", `echo "12.3%"`)
	// pgrep -P <pid> -> no children (deterministic single-process tree)
	mockCommand(t, dir, "pgrep", `exit 1`)
	// footprint -> phys_footprint = memMB MB, plus a peak line that must be ignored
	mockCommand(t, dir, "footprint", fmt.Sprintf(`
printf 'proc [1]: 64-bit Footprint: %s MB\n    phys_footprint: %s MB\n    phys_footprint_peak: 9999 MB\n'
`, memMB, memMB))
	// ps: comm= always reports the claude process (matches on first walk step);
	// rss= reports memMB*1024 KB; ppid= terminates the walk if comm ever misses.
	mockCommand(t, dir, "ps", fmt.Sprintf(`
case "$*" in
  *comm=*) printf '%%s\n' %q ;;
  *rss=*)  printf '%%s\n' "$(( %s * 1024 ))" ;;
  *ppid=*) printf '%%s\n' "1" ;;
esac
`, claudeComm, memMB))

	binDir := filepath.Join(dir, "bin")
	return buildEnv(t, []string{binDir}, "HOME="+fakeHome)
}

func TestStatusline_wrapper_shows_memory_segment_for_claude_ancestor(t *testing.T) {
	env := setupWrapperMemTest(t, "/Users/test/.local/bin/claude", "50")

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "50M")
}

// Regression: the `claude` launcher is a symlink to a versioned binary
// (e.g. ~/.local/share/claude/versions/2.1.185). If Claude Code execs the
// resolved path, `comm` has no `claude` basename — the memory segment must
// still render so the panel shows the memory load at ALL times.
func TestStatusline_wrapper_shows_memory_for_versioned_claude_path(t *testing.T) {
	env := setupWrapperMemTest(t, "/Users/test/.local/share/claude/versions/2.1.185", "50")

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "50M")
}

// A claude launcher path containing spaces must still resolve (the old
// `xargs basename` would word-split the path and mis-parse it).
func TestStatusline_wrapper_shows_memory_for_claude_path_with_spaces(t *testing.T) {
	env := setupWrapperMemTest(t, "/Users/test/My Tools/claude", "50")

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "50M")
}

// --- get_tree_footprint_kb: phys_footprint is the correct memory load ---
// macOS RSS overcounts shared dyld/framework pages 2-4x; phys_footprint
// (Activity Monitor's "Memory") is the accurate per-process figure.

func TestStatusline_get_tree_footprint_kb_sums_phys_footprint_excluding_peak(t *testing.T) {
	dir := t.TempDir()

	// 100 -> [101], others none
	mockCommand(t, dir, "pgrep", `
pid="${@: -1}"
case "$pid" in
  100) printf '101\n' ;;
  *) exit 1 ;;
esac
`)
	// footprint output for the tree; phys_footprint_peak lines must be ignored.
	mockCommand(t, dir, "footprint", `
printf 'claude [100]: 64-bit Footprint: 280 MB\n'
printf '    phys_footprint: 280 MB\n'
printf '    phys_footprint_peak: 3000 MB\n'
printf 'caffeinate [101]: 64-bit Footprint: 1632 KB\n'
printf '    phys_footprint: 1632 KB\n'
printf 'Summary Footprint: 281 MB\n'
`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_footprint_kb", []string{"100"}, env)
	assertExitCode(t, code, 0)
	// 280 MB = 286720 KB, + 1632 KB = 288352 KB
	if strings.TrimSpace(out) != "288352" {
		t.Errorf("expected 288352, got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_get_tree_footprint_kb_handles_GB_units(t *testing.T) {
	dir := t.TempDir()
	mockCommand(t, dir, "pgrep", `exit 1`)
	mockCommand(t, dir, "footprint", `printf '    phys_footprint: 1.5 GB\n'`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_footprint_kb", []string{"100"}, env)
	assertExitCode(t, code, 0)
	// 1.5 * 1024 * 1024 = 1572864 KB
	if strings.TrimSpace(out) != "1572864" {
		t.Errorf("expected 1572864, got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_get_tree_footprint_kb_passes_every_tree_pid_to_footprint(t *testing.T) {
	dir := t.TempDir()
	// 100 -> [101,102], 101 -> [103]  => tree is {100,101,102,103}
	mockCommand(t, dir, "pgrep", `
pid="${@: -1}"
case "$pid" in
  100) printf '101\n102\n' ;;
  101) printf '103\n' ;;
  *) exit 1 ;;
esac
`)
	// Emit one 10 MB phys_footprint line per pid argument, so the summed total
	// proves every collected pid was passed to footprint.
	mockCommand(t, dir, "footprint", `for _ in "$@"; do printf '    phys_footprint: 10 MB\n'; done`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_footprint_kb", []string{"100"}, env)
	assertExitCode(t, code, 0)
	// 4 pids * 10 MB = 40 MB = 40960 KB
	if strings.TrimSpace(out) != "40960" {
		t.Errorf("expected 40960 (4 pids x 10MB), got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_get_tree_footprint_kb_empty_when_footprint_yields_nothing(t *testing.T) {
	dir := t.TempDir()
	mockCommand(t, dir, "pgrep", `exit 1`)
	mockCommand(t, dir, "footprint", `exit 0`) // no output (e.g. sandboxed/unavailable)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_footprint_kb", []string{"100"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output so caller falls back to RSS, got %q", strings.TrimSpace(out))
	}
}

// Under a comma-locale, `footprint` emits "1,5 GB". The fractional part must be
// parsed — truncating at the comma would report 1 GB instead of 1.5 GB and
// under-state the memory load.
func TestStatusline_get_tree_footprint_kb_handles_comma_decimal_locale(t *testing.T) {
	dir := t.TempDir()
	mockCommand(t, dir, "pgrep", `exit 1`)
	mockCommand(t, dir, "footprint", `printf '    phys_footprint: 1,5 GB\n'`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_footprint_kb", []string{"100"}, env)
	assertExitCode(t, code, 0)
	// 1,5 GB = 1.5 * 1024 * 1024 = 1572864 KB (NOT 1 GB = 1048576 KB)
	if strings.TrimSpace(out) != "1572864" {
		t.Errorf("expected 1572864 (1,5 GB parsed), got %q", strings.TrimSpace(out))
	}
}

// --- wrapper: prefer phys_footprint, fall back to RSS ---

// wrapperHomeWithCmd creates a fake home + statusline-command.sh and returns the
// temp dir and fake home so a test can add its own ps/footprint/pgrep mocks.
func wrapperHomeWithCmd(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	fakeHome := filepath.Join(dir, "home")
	writeTempFile(t, fakeHome, ".claude/statusline-command.sh", `echo "GITINFO"`)
	mockCommand(t, dir, "npx", `echo "12.3%"`)
	mockCommand(t, dir, "pgrep", `exit 1`)
	return dir, fakeHome
}

func TestStatusline_wrapper_prefers_footprint_over_rss(t *testing.T) {
	// footprint says 30 MB, RSS says 50 MB. The panel must show the footprint
	// value — RSS overcounts shared memory and is the wrong "memory load".
	dir, fakeHome := wrapperHomeWithCmd(t)
	mockCommand(t, dir, "footprint", `printf '    phys_footprint: 30 MB\n'`)
	mockCommand(t, dir, "ps", `
case "$*" in
  *comm=*) printf '%s\n' "/Users/test/.local/bin/claude" ;;
  *rss=*)  printf '%s\n' "51200" ;;
  *ppid=*) printf '%s\n' "1" ;;
esac
`)
	env := buildEnv(t, []string{filepath.Join(dir, "bin")}, "HOME="+fakeHome)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "30M")
	assertNotContains(t, out, "50M")
}

func TestStatusline_wrapper_falls_back_to_rss_when_footprint_unavailable(t *testing.T) {
	// footprint produces nothing (sandboxed/missing); the memory load must still
	// render, using RSS as a fallback.
	dir, fakeHome := wrapperHomeWithCmd(t)
	mockCommand(t, dir, "footprint", `exit 0`) // no output
	mockCommand(t, dir, "ps", `
case "$*" in
  *comm=*) printf '%s\n' "/Users/test/.local/bin/claude" ;;
  *rss=*)  printf '%s\n' "51200" ;;
  *ppid=*) printf '%s\n' "1" ;;
esac
`)
	env := buildEnv(t, []string{filepath.Join(dir, "bin")}, "HOME="+fakeHome)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "50M")
}

func TestStatusline_wrapper_omits_memory_when_no_claude_ancestor(t *testing.T) {
	env := setupWrapperTest(t) // ps comm= -> "sh", ppid= -> 1

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "GITINFO | \x1b[01;33m"+ctxIcon+"\x1b[00m 12.3%" {
		t.Errorf("expected no memory segment without a claude ancestor, got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_wrapper_shows_model_display_name(t *testing.T) {
	env := setupWrapperTest(t)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Fable 5")
}

func TestStatusline_wrapper_omits_model_segment_when_model_missing(t *testing.T) {
	env := setupWrapperTest(t)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "GITINFO | \x1b[01;33m"+ctxIcon+"\x1b[00m 12.3%" {
		t.Errorf("expected output without model segment, got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_wrapper_appends_effort_level_to_model(t *testing.T) {
	env := setupWrapperTest(t)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"effort":{"level":"high"},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Fable 5 [high]")
}

func TestStatusline_wrapper_omits_effort_brackets_when_effort_absent(t *testing.T) {
	env := setupWrapperTest(t)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Fable 5")
	assertNotContains(t, out, "Fable 5 [")
}

// --- get_tree_cpu_pct: real CPU load of the session process tree ---
// Sums macOS `ps -o %cpu` across the Claude Code process and its descendants.
// `ps %cpu` is a fast recent-usage average — a `top` sample would block the
// statusline for ~1s. The sum can exceed 100 on multi-core machines.

func TestStatusline_get_tree_cpu_pct_sums_cpu_of_process_and_children(t *testing.T) {
	dir := t.TempDir()

	// 100 -> [101, 102], 101 -> [103]
	mockCommand(t, dir, "pgrep", `
pid="${@: -1}"
case "$pid" in
  100) printf '101\n102\n' ;;
  101) printf '103\n' ;;
  *) exit 1 ;;
esac
`)
	mockCommand(t, dir, "ps", `
pid="${@: -1}"
case "$pid" in
  100) echo " 10.4" ;;
  101) echo "  5.3" ;;
  102) echo "  2.0" ;;
  103) echo "  0.0" ;;
  *) echo "" ;;
esac
`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_cpu_pct", []string{"100"}, env)
	assertExitCode(t, code, 0)
	// 10.4 + 5.3 + 2.0 + 0.0 = 17.7 -> rounds to 18
	if strings.TrimSpace(out) != "18" {
		t.Errorf("expected 18, got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_get_tree_cpu_pct_rounds_to_nearest_integer(t *testing.T) {
	dir := t.TempDir()
	mockCommand(t, dir, "pgrep", `exit 1`)
	mockCommand(t, dir, "ps", `echo " 12.6"`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_cpu_pct", []string{"100"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "13" {
		t.Errorf("expected 13 (12.6 rounded), got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_get_tree_cpu_pct_reports_zero_for_idle_process(t *testing.T) {
	dir := t.TempDir()
	mockCommand(t, dir, "pgrep", `exit 1`)
	mockCommand(t, dir, "ps", `echo "  0.0"`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_cpu_pct", []string{"100"}, env)
	assertExitCode(t, code, 0)
	// An idle session is genuinely 0% — show it, don't omit it.
	if strings.TrimSpace(out) != "0" {
		t.Errorf("expected 0 for idle process, got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_get_tree_cpu_pct_empty_when_process_gone(t *testing.T) {
	dir := t.TempDir()
	mockCommand(t, dir, "pgrep", `exit 1`)
	mockCommand(t, dir, "ps", `echo ""`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_cpu_pct", []string{"999"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output when no pid yields a reading, got %q", strings.TrimSpace(out))
	}
}

// macOS `ps -o %cpu` honors LC_NUMERIC and emits a COMMA decimal under
// comma-locales (ru_RU, de_DE). The sum must still be parsed correctly — a
// naive awk would read "10,4" as 10 and silently under-report the CPU load.
func TestStatusline_get_tree_cpu_pct_handles_comma_decimal_locale(t *testing.T) {
	dir := t.TempDir()
	mockCommand(t, dir, "pgrep", `
pid="${@: -1}"
case "$pid" in
  100) printf '101\n' ;;
  *) exit 1 ;;
esac
`)
	mockCommand(t, dir, "ps", `
pid="${@: -1}"
case "$pid" in
  100) echo " 10,4" ;;
  101) echo "  5,3" ;;
  *) echo "" ;;
esac
`)

	binDir := filepath.Join(dir, "bin")
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/statusline.sh", "get_tree_cpu_pct", []string{"100"}, env)
	assertExitCode(t, code, 0)
	// 10,4 + 5,3 = 15,7 -> 16 (NOT 10+5=15 from truncating at the comma)
	if strings.TrimSpace(out) != "16" {
		t.Errorf("expected 16 (comma decimals parsed), got %q", strings.TrimSpace(out))
	}
}

// --- wrapper: CPU segment ---

func TestStatusline_wrapper_shows_cpu_segment_for_claude_ancestor(t *testing.T) {
	dir, fakeHome := wrapperHomeWithCmd(t)
	mockCommand(t, dir, "footprint", `printf '    phys_footprint: 30 MB\n'`)
	mockCommand(t, dir, "ps", `
case "$*" in
  *comm=*)  printf '%s\n' "/Users/test/.local/bin/claude" ;;
  *%cpu=*)  printf '%s\n' " 42.4" ;;
  *rss=*)   printf '%s\n' "51200" ;;
  *ppid=*)  printf '%s\n' "1" ;;
esac
`)
	env := buildEnv(t, []string{filepath.Join(dir, "bin")}, "HOME="+fakeHome)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "42%") // 42.4 rounds to 42
}

func TestStatusline_wrapper_omits_cpu_when_no_claude_ancestor(t *testing.T) {
	env := setupWrapperTest(t) // ps comm= -> "sh", ppid= -> 1

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "GITINFO | \x1b[01;33m"+ctxIcon+"\x1b[00m 12.3%" {
		t.Errorf("expected no cpu segment without a claude ancestor, got %q", strings.TrimSpace(out))
	}
}

// ============================================================
// statusline-setup.sh tests (TestStatuslineSetup_*)
// ============================================================

// statuslineSetupSnippet builds a bash snippet that sources tui.sh, settings-json.sh,
// and statusline-setup.sh, then runs the provided bash code.
func statuslineSetupSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	settingsJsonPath := filepath.Join(root, "lib", "settings-json.sh")
	statuslineSetupPath := filepath.Join(root, "lib", "statusline-setup.sh")
	return fmt.Sprintf("source %q && source %q && source %q && %s",
		tuiPath, settingsJsonPath, statuslineSetupPath, body)
}

// setupStatuslineTestDirs creates the fake share dir with template files and fake home dirs.
// Returns (shareDir, fakeHome).
func setupStatuslineTestDirs(t *testing.T) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()

	shareDir := filepath.Join(tmpDir, "share")
	writeTempFile(t, shareDir, "templates/ccstatusline-settings.json", "mock-settings")
	writeTempFile(t, shareDir, "templates/statusline-command.sh", "mock-command")
	writeTempFile(t, shareDir, "templates/statusline-wrapper.sh", "mock-wrapper")
	writeTempFile(t, shareDir, "lib/statusline.sh", "mock-helpers")

	fakeHome := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(filepath.Join(fakeHome, ".config", "ccstatusline"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(fakeHome, ".claude"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	return shareDir, fakeHome
}

func TestStatuslineSetup_copies_config_and_scripts_when_npm_available(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() { return 0; }
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	// Verify files were copied
	for _, path := range []string{
		filepath.Join(fakeHome, ".config", "ccstatusline", "settings.json"),
		filepath.Join(fakeHome, ".claude", "statusline-command.sh"),
		filepath.Join(fakeHome, ".claude", "statusline-wrapper.sh"),
		filepath.Join(fakeHome, ".claude", "statusline-helpers.sh"),
	} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file to exist: %s", path)
		}
	}

	// Verify scripts are executable
	for _, name := range []string{"statusline-command.sh", "statusline-wrapper.sh"} {
		info, err := os.Stat(filepath.Join(fakeHome, ".claude", name))
		if err != nil {
			t.Errorf("stat %s: %v", name, err)
			continue
		}
		if info.Mode()&0111 == 0 {
			t.Errorf("expected %s to be executable", name)
		}
	}
}

func TestStatuslineSetup_skips_when_npm_not_available_and_brew_fails(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 1; }
brew() { return 1; }
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(filepath.Join(fakeHome, ".claude", "statusline-command.sh")); !os.IsNotExist(err) {
		t.Error("statusline-command.sh should not exist when npm not available and brew fails")
	}
}

func TestStatuslineSetup_reports_already_installed(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$1" == "list" ]]; then echo "└── ccstatusline@2.2.21"; return 0; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "up to date")
}

func TestStatuslineSetup_warns_and_skips_when_npm_install_fails(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$1" == "list" ]]; then return 1; fi
  if [[ "$1" == "install" ]]; then return 1; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install")

	if _, err := os.Stat(filepath.Join(fakeHome, ".claude", "statusline-command.sh")); !os.IsNotExist(err) {
		t.Error("statusline-command.sh should not exist when npm install fails")
	}
	if _, err := os.Stat(filepath.Join(fakeHome, ".claude", "statusline-wrapper.sh")); !os.IsNotExist(err) {
		t.Error("statusline-wrapper.sh should not exist when npm install fails")
	}
}

func TestStatuslineSetup_installs_ccstatusline_and_copies_files_on_fresh_install(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)
	marker := filepath.Join(t.TempDir(), "installed")

	// Not installed until install runs; install drops a marker that list keys off.
	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$1" == "list" ]]; then
    if [[ -f %q ]]; then echo "└── ccstatusline@2.2.21"; return 0; fi
    return 1
  fi
  if [[ "$1" == "install" ]]; then touch %q; return 0; fi
  return 0
}
setup_statusline %q %q %q
`, marker, marker, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "ccstatusline installed")
	assertNotContains(t, out, "already installed")

	for _, path := range []string{
		filepath.Join(fakeHome, ".config", "ccstatusline", "settings.json"),
		filepath.Join(fakeHome, ".claude", "statusline-command.sh"),
		filepath.Join(fakeHome, ".claude", "statusline-wrapper.sh"),
	} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file to exist: %s", path)
		}
	}
}

// --- version pinning (per-model context window accuracy) ---

func TestStatuslineSetup_upgrades_stale_ccstatusline_to_pinned_version(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)
	argsFile := filepath.Join(t.TempDir(), "install-args")

	// ccstatusline 2.1.0 is installed (pre per-model-window fix). Capture install args.
	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$1" == "list" ]]; then echo "/usr/local/lib"; echo "└── ccstatusline@2.1.0"; return 0; fi
  if [[ "$1" == "install" ]]; then echo "$*" >> %q; return 0; fi
  return 0
}
setup_statusline %q %q %q
`, argsFile, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("install was never called: %v", err)
	}
	assertContains(t, string(data), "ccstatusline@2.2.21")
}

func TestStatuslineSetup_leaves_newer_ccstatusline_untouched(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)
	argsFile := filepath.Join(t.TempDir(), "install-args")

	// A newer version than pinned must NOT be downgraded.
	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$1" == "list" ]]; then echo "└── ccstatusline@2.3.0"; return 0; fi
  if [[ "$1" == "install" ]]; then echo "$*" >> %q; return 0; fi
  return 0
}
setup_statusline %q %q %q
`, argsFile, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "up to date")

	if data, err := os.ReadFile(argsFile); err == nil && strings.TrimSpace(string(data)) != "" {
		t.Errorf("install should NOT run for a newer version, but ran with: %q", data)
	}
}

func TestStatuslineSetup_upgrades_when_older_than_minimum(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)
	argsFile := filepath.Join(t.TempDir(), "install-args")

	// Older version must be upgraded to the pinned minimum.
	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$1" == "list" ]]; then echo "└── ccstatusline@2.2.9"; return 0; fi
  if [[ "$1" == "install" ]]; then echo "$*" >> %q; return 0; fi
  return 0
}
setup_statusline %q %q %q
`, argsFile, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("install was never called for older version: %v", err)
	}
	assertContains(t, string(data), "ccstatusline@2.2.21")
}

func TestStatuslineSetup_skips_install_when_already_at_pinned_version(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)
	argsFile := filepath.Join(t.TempDir(), "install-args")

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$1" == "list" ]]; then echo "└── ccstatusline@2.2.21"; return 0; fi
  if [[ "$1" == "install" ]]; then echo "$*" >> %q; return 0; fi
  return 0
}
setup_statusline %q %q %q
`, argsFile, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "up to date")

	if data, err := os.ReadFile(argsFile); err == nil && strings.TrimSpace(string(data)) != "" {
		t.Errorf("install should NOT run when already at pinned version, but ran with: %q", data)
	}
}

func TestStatuslineSetup_pins_exact_version_on_fresh_install(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)
	argsFile := filepath.Join(t.TempDir(), "install-args")

	// Not installed: list yields no ccstatusline@ line.
	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$1" == "list" ]]; then echo "(empty)"; return 1; fi
  if [[ "$1" == "install" ]]; then echo "$*" >> %q; return 0; fi
  return 0
}
setup_statusline %q %q %q
`, argsFile, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("install was never called: %v", err)
	}
	assertContains(t, string(data), "ccstatusline@2.2.21")
}

func TestStatuslineSetup_calls_merge_claude_settings_after_file_copy(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)
	claudeSettings := filepath.Join(t.TempDir(), "claude-settings", "settings.json")

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() { return 0; }
setup_statusline %q %q %q
`, shareDir, claudeSettings, fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(claudeSettings); os.IsNotExist(err) {
		t.Fatal("claude settings file should have been created by merge_claude_settings")
	}

	data, err := os.ReadFile(claudeSettings)
	if err != nil {
		t.Fatalf("read claude settings: %v", err)
	}
	assertContains(t, string(data), `"statusLine"`)
}

// --- npm install failure scenarios ---

func TestStatuslineSetup_handles_npm_install_network_timeout(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"install"* ]]; then
    echo "npm ERR! network timeout" >&2
    return 1
  fi
  if [[ "$*" == *"list"* ]]; then return 1; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install")
	if _, err := os.Stat(filepath.Join(fakeHome, ".claude", "statusline-command.sh")); !os.IsNotExist(err) {
		t.Error("statusline-command.sh should not exist on network timeout")
	}
}

func TestStatuslineSetup_handles_npm_install_ECONNREFUSED(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"install"* ]]; then
    echo "npm ERR! network request to https://registry.npmjs.org/ccstatusline failed, reason: connect ECONNREFUSED" >&2
    return 1
  fi
  if [[ "$*" == *"list"* ]]; then return 1; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install")
}

func TestStatuslineSetup_handles_npm_install_ETIMEDOUT(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"install"* ]]; then
    echo "npm ERR! network request timed out, reason: ETIMEDOUT" >&2
    return 1
  fi
  if [[ "$*" == *"list"* ]]; then return 1; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install")
}

func TestStatuslineSetup_handles_npm_registry_returning_404(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"install"* ]]; then
    echo "npm ERR! 404 Not Found - GET https://registry.npmjs.org/ccstatusline" >&2
    return 1
  fi
  if [[ "$*" == *"list"* ]]; then return 1; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install")
}

func TestStatuslineSetup_handles_npm_registry_returning_500(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"install"* ]]; then
    echo "npm ERR! 500 Internal Server Error - GET https://registry.npmjs.org/ccstatusline" >&2
    return 1
  fi
  if [[ "$*" == *"list"* ]]; then return 1; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install")
}

func TestStatuslineSetup_handles_npm_registry_returning_503_unavailable(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"install"* ]]; then
    echo "npm ERR! 503 Service Unavailable - GET https://registry.npmjs.org/ccstatusline" >&2
    return 1
  fi
  if [[ "$*" == *"list"* ]]; then return 1; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install")
}

func TestStatuslineSetup_handles_npm_install_hanging(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"install"* ]]; then
    sleep 5 &
    return 1
  fi
  if [[ "$*" == *"list"* ]]; then return 1; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install")
}

func TestStatuslineSetup_handles_npm_install_disk_full_error(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"install"* ]]; then
    echo "npm ERR! ENOSPC: no space left on device" >&2
    return 1
  fi
  if [[ "$*" == *"list"* ]]; then return 1; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install")
}

func TestStatuslineSetup_handles_npm_install_permission_denied(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"install"* ]]; then
    echo "npm ERR! EACCES: permission denied" >&2
    return 1
  fi
  if [[ "$*" == *"list"* ]]; then return 1; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Failed to install")
}

// --- npm list failure scenarios ---

func TestStatuslineSetup_handles_npm_list_returning_malformed_output(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"list"* ]]; then
    echo "CORRUPT@#$%%DATA"
    return 0
  fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	// Unparseable version -> safe (re)install rather than trusting a bad string.
	assertContains(t, out, "ccstatusline installed")
}

func TestStatuslineSetup_handles_npm_list_command_hanging(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"list"* ]]; then
    sleep 5 &
    return 0
  fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
}

func TestStatuslineSetup_handles_npm_returning_non_JSON_output(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"list"* ]]; then
    echo "This is not JSON"
    return 0
  fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
}

func TestStatuslineSetup_handles_npm_list_returning_empty_output(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() {
  if [[ "$*" == *"list"* ]]; then
    echo ""
    return 0
  fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
}

// --- npm not found scenarios ---

func TestStatuslineSetup_handles_npm_not_in_PATH_after_install(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 1; }
brew() {
  if [[ "$*" == *"install node"* ]]; then return 0; fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "ccstatusline installed")
}

func TestStatuslineSetup_handles_brew_node_install_failure(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 1; }
brew() {
  if [[ "$*" == *"install node"* ]]; then
    echo "Error: Failed to install node" >&2
    return 1
  fi
  return 0
}
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "Node.js installation failed")
}

func TestStatuslineSetup_handles_brew_not_available_for_node_install(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 1; }
brew() { return 127; }
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "ccstatusline")
}

// --- File operation failure scenarios ---

func TestStatuslineSetup_handles_missing_template_files(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	// Remove template files
	if err := os.RemoveAll(filepath.Join(shareDir, "templates")); err != nil {
		t.Fatalf("remove templates: %v", err)
	}

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() { return 0; }
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	// cp will fail but script should handle gracefully
	// Either non-zero exit OR the config file won't be created
	configFile := filepath.Join(fakeHome, ".config", "ccstatusline", "settings.json")
	if code == 0 {
		if _, err := os.Stat(configFile); err == nil {
			t.Error("config file should not exist when templates are missing, but it does")
		}
	}
	// Either failure or missing file is acceptable
}

func TestStatuslineSetup_handles_read_only_config_directory(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	// Make config dir read-only
	configDir := filepath.Join(fakeHome, ".config")
	if err := os.Chmod(configDir, 0444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(configDir, 0755)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() { return 0; }
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	// Function doesn't check mkdir errors, so it completes successfully
	assertExitCode(t, code, 0)
}

func TestStatuslineSetup_handles_chmod_failure_on_scripts(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() { return 0; }
chmod() { return 1; }
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	// Function doesn't check chmod errors, completes successfully
	assertExitCode(t, code, 0)
}

func TestStatuslineSetup_handles_config_file_copy_permission_denied(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	// Make ccstatusline dir read-only
	ccDir := filepath.Join(fakeHome, ".config", "ccstatusline")
	if err := os.Chmod(ccDir, 0444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(ccDir, 0755)

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() { return 0; }
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	// Function doesn't check cp errors, may succeed or fail - either is acceptable
	_ = code
}

func TestStatuslineSetup_handles_corrupted_template_file(t *testing.T) {
	shareDir, fakeHome := setupStatuslineTestDirs(t)

	// Create corrupted template (non-UTF8)
	corruptPath := filepath.Join(shareDir, "templates", "ccstatusline-settings.json")
	if err := os.WriteFile(corruptPath, []byte{0xff, 0xfe, 0xfd}, 0644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	snippet := statuslineSetupSnippet(t, fmt.Sprintf(`
_has_npm() { return 0; }
npm() { return 0; }
setup_statusline %q %q %q
`, shareDir, filepath.Join(fakeHome, ".claude", "settings.json"), fakeHome))

	_, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)

	// Should copy file even if corrupted
	configFile := filepath.Join(fakeHome, ".config", "ccstatusline", "settings.json")
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Error("config file should exist even with corrupted template")
	}
}

// --- gt_stamp_claude_session ---

func TestStatusline_stamp_claude_session_sets_tmux_session_env(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=/tmp/sock,1,0")
	transcript := filepath.Join(dir, "x.jsonl")
	if err := os.WriteFile(transcript,
		[]byte("{\"type\":\"user\"}\n{\"type\":\"assistant\"}\n"), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	json := `{"session_id":"sid-42","transcript_path":"` + transcript + `","cwd":"/p/app"}`
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session",
		[]string{json}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("tmux not invoked: %v", err)
	}
	assertContains(t, string(data), "set-environment WISP_DECK_CLAUDE_SESSION sid-42")
}

func TestStatusline_stamp_claude_session_noop_when_transcript_missing(t *testing.T) {
	// A freshly-launched or just-/clear'd session shows a session_id before
	// any transcript exists on disk. Stamping that id would make restore run
	// `claude --resume <id>`, which fails ("No conversation found") and dumps
	// the tab to a bare shell. Keep the previous (resumable) id instead.
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=/tmp/sock,1,0")
	json := `{"session_id":"sid-42","transcript_path":"` + filepath.Join(dir, "nope.jsonl") + `","cwd":"/p/app"}`
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session",
		[]string{json}, env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("must not stamp a session whose transcript does not exist yet")
	}
}

func TestStatusline_stamp_claude_session_noop_without_model_turn(t *testing.T) {
	// Claude marks sessions with no model turn as non-resumable; --resume
	// fails on them exactly like on a missing transcript.
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=/tmp/sock,1,0")
	transcript := filepath.Join(dir, "x.jsonl")
	if err := os.WriteFile(transcript, []byte("{\"type\":\"user\"}\n"), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	json := `{"session_id":"sid-42","transcript_path":"` + transcript + `","cwd":"/p/app"}`
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session",
		[]string{json}, env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("must not stamp a session whose transcript has no model turn")
	}
}

func TestStatusline_stamp_claude_session_stamps_when_no_transcript_path(t *testing.T) {
	// Older claude versions omit transcript_path from the statusline payload —
	// keep the pre-guard behavior there (restore re-validates the id anyway).
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=/tmp/sock,1,0")
	json := `{"session_id":"sid-42","cwd":"/p/app"}`
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session",
		[]string{json}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("tmux not invoked: %v", err)
	}
	assertContains(t, string(data), "set-environment WISP_DECK_CLAUDE_SESSION sid-42")
}

func TestStatusline_stamp_claude_session_noop_outside_tmux(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=")
	json := `{"session_id":"sid-42"}`
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session",
		[]string{json}, env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("must not touch tmux when not inside a tmux pane")
	}
}

func TestStatusline_stamp_claude_session_noop_without_session_id(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=/tmp/sock,1,0")
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_session",
		[]string{`{"cwd":"/p/app"}`}, env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("must not stamp when the payload has no session_id")
	}
}

// --- gt_stamp_claude_live_session ---

func TestStatusline_stamp_live_session_stamps_unconditionally(t *testing.T) {
	// The live stamp records claude's CURRENTLY-active conversation id with NO
	// durability gate — a fresh /new session (no transcript / no model turn yet)
	// must still be recorded, so a mid-session account switch can tell the
	// durable (lagging) id no longer names the live conversation.
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=/tmp/sock,1,0")
	// No transcript on disk at all — gt_stamp_claude_session would no-op here.
	json := `{"session_id":"sid-42","transcript_path":"` + filepath.Join(dir, "nope.jsonl") + `","cwd":"/p/app"}`
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_live_session",
		[]string{json}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("tmux not invoked: %v", err)
	}
	assertContains(t, string(data), "set-environment WISP_DECK_CLAUDE_LIVE_SESSION sid-42")
}

func TestStatusline_stamp_live_session_noop_outside_tmux(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=")
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_live_session",
		[]string{`{"session_id":"sid-42"}`}, env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("must not touch tmux when not inside a tmux pane")
	}
}

func TestStatusline_stamp_live_session_noop_without_session_id(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "tmux", fmt.Sprintf(`echo "$@" >> %q`, rec))
	env := buildEnv(t, []string{binDir}, "TMUX=/tmp/sock,1,0")
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_stamp_claude_live_session",
		[]string{`{"cwd":"/p/app"}`}, env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("must not stamp when the payload has no session_id")
	}
}

// The stamping function is worthless if the statusline wrapper never calls it.
// This guards the wiring: the shipped template must invoke
// gt_stamp_claude_live_session on every render, so the mid-session switch always
// has a fresh live id to compare against (preventing a /new-closed conversation
// from being resumed on an account switch).
func TestStatuslineWrapperTemplate_invokes_live_session_stamp(t *testing.T) {
	root := projectRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "templates", "statusline-wrapper.sh"))
	if err != nil {
		t.Fatalf("read wrapper template: %v", err)
	}
	assertContains(t, string(body), "gt_stamp_claude_live_session")
}

// --- gt_claude_account_label ---
//
// The statusline runs as a child of `claude`, which wrapper.sh launches with the
// active account's isolated CLAUDE_CONFIG_DIR exported (unset for the Keychain
// Default). gt_claude_account_label maps that config dir to its display label so
// the statusline can show which account this tab is using.

func TestStatusline_account_label_default_when_config_dir_empty(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_claude_account_label",
		[]string{"", list}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Default" {
		t.Fatalf("got %q, want %q", strings.TrimSpace(out), "Default")
	}
}

func TestStatusline_account_label_maps_config_dir_basename_to_list_label(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	writeTempFile(t, dir, "claude-accounts.list", "Work Max:work\nPersonal:personal\n")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_claude_account_label",
		[]string{"/some/root/claude-accounts/personal", list}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Personal" {
		t.Fatalf("got %q, want %q", strings.TrimSpace(out), "Personal")
	}
}

func TestStatusline_account_label_unknown_dir_falls_back_to_default(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_claude_account_label",
		[]string{"/some/root/claude-accounts/ghost", list}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Default" {
		t.Fatalf("got %q, want %q", strings.TrimSpace(out), "Default")
	}
}

func TestStatusline_account_label_default_when_list_missing(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "does-not-exist.list")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_claude_account_label",
		[]string{"/some/root/claude-accounts/work", list}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Default" {
		t.Fatalf("got %q, want %q", strings.TrimSpace(out), "Default")
	}
}

// The Default (Keychain) login can be renamed via the account menu; the custom
// label is persisted in the claude-account-default-label file. When gt_claude_
// account_label falls back to Default, it must honor that renamed value.

func TestStatusline_account_label_default_uses_renamed_label(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\n")
	def := filepath.Join(dir, "claude-account-default-label")
	writeTempFile(t, dir, "claude-account-default-label", "Work\n")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_claude_account_label",
		[]string{"", list, def}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Work" {
		t.Fatalf("renamed Default must show %q, got %q", "Work", strings.TrimSpace(out))
	}
}

func TestStatusline_account_label_unknown_dir_uses_renamed_default_label(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\n")
	def := filepath.Join(dir, "claude-account-default-label")
	writeTempFile(t, dir, "claude-account-default-label", "Work\n")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_claude_account_label",
		[]string{"/some/root/claude-accounts/ghost", list, def}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Work" {
		t.Fatalf("unknown dir must fall back to renamed Default %q, got %q", "Work", strings.TrimSpace(out))
	}
}

func TestStatusline_account_label_default_literal_when_label_file_absent(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\n")
	def := filepath.Join(dir, "does-not-exist")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_claude_account_label",
		[]string{"", list, def}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Default" {
		t.Fatalf("absent label file must read as %q, got %q", "Default", strings.TrimSpace(out))
	}
}

func TestStatusline_account_label_default_literal_when_label_file_blank(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\n")
	def := filepath.Join(dir, "claude-account-default-label")
	writeTempFile(t, dir, "claude-account-default-label", "   \n")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_claude_account_label",
		[]string{"", list, def}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Default" {
		t.Fatalf("blank label file must read as %q, got %q", "Default", strings.TrimSpace(out))
	}
}

func TestStatusline_account_label_registered_dir_ignores_default_label_file(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	writeTempFile(t, dir, "claude-accounts.list", "Personal:personal\n")
	def := filepath.Join(dir, "claude-account-default-label")
	writeTempFile(t, dir, "claude-account-default-label", "Work\n")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_claude_account_label",
		[]string{"/some/root/claude-accounts/personal", list, def}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "Personal" {
		t.Fatalf("a registered account keeps its list label, got %q", strings.TrimSpace(out))
	}
}

// --- gt_proxy_active_dir ---
//
// When the account-rotation proxy is active, `claude` keeps one (Default) config
// dir while the proxy swaps accounts per-request, writing the current account's
// dir name to a file. gt_proxy_active_dir reads that file so the statusline can
// override CLAUDE_CONFIG_DIR with the account rotation actually landed on.

func TestStatusline_proxy_active_dir_reads_file(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "proxy-account")
	writeTempFile(t, dir, "proxy-account", "work\n")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_proxy_active_dir",
		[]string{f}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "work" {
		t.Fatalf("got %q, want %q", strings.TrimSpace(out), "work")
	}
}

func TestStatusline_proxy_active_dir_empty_when_file_absent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "nope")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_proxy_active_dir",
		[]string{f}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("absent file must yield empty, got %q", strings.TrimSpace(out))
	}
}

func TestStatusline_proxy_active_dir_empty_when_arg_blank(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_proxy_active_dir",
		[]string{""}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("blank path must yield empty, got %q", strings.TrimSpace(out))
	}
}

// --- gt_multiple_claude_accounts ---
//
// The account segment is only worth showing when the user actually juggles
// multiple accounts. gt_multiple_claude_accounts gates it. The accounts list
// holds only the *managed* logins; the implicit Default (Keychain) login always
// exists on top of them (mirrors the Go account menu: row 0 = Default, rows
// 1..len = managed). So the user has 2+ accounts as soon as the list holds a
// single managed entry — the segment must show then, disambiguating Default from
// that one managed login. Exit 0 iff the list holds >= 1 managed label:dir entry.

func TestStatusline_multiple_accounts_false_when_list_missing(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "does-not-exist.list")
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_multiple_claude_accounts",
		[]string{list}, nil)
	if code == 0 {
		t.Fatalf("missing list means only the Default login exists — not 2+ accounts (got exit %d)", code)
	}
}

func TestStatusline_multiple_accounts_false_when_list_empty(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	// A list with only comments and blanks registers no managed logins, so only
	// the implicit Default remains — a single account, segment stays hidden.
	writeTempFile(t, dir, "claude-accounts.list", "# accounts\n\n")
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_multiple_claude_accounts",
		[]string{list}, nil)
	if code == 0 {
		t.Fatalf("no managed logins means only the Default — not 2+ accounts (got exit %d)", code)
	}
}

func TestStatusline_multiple_accounts_true_when_single_entry(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	// One managed login plus the always-present implicit Default = 2 accounts,
	// so the segment must show to tell them apart.
	writeTempFile(t, dir, "claude-accounts.list", "# accounts\nPersonal:personal\n")
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_multiple_claude_accounts",
		[]string{list}, nil)
	assertExitCode(t, code, 0)
}

func TestStatusline_multiple_accounts_true_when_two_entries(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	writeTempFile(t, dir, "claude-accounts.list", "Work:work\nPersonal:personal\n")
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_multiple_claude_accounts",
		[]string{list}, nil)
	assertExitCode(t, code, 0)
}

func TestStatusline_multiple_accounts_ignores_comments_blanks_and_malformed(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	// Two comment lines, a blank, and a malformed (no colon) line surround a
	// single real managed entry — that entry plus the implicit Default is 2
	// accounts, so the segment shows (exit 0) without the junk inflating anything.
	writeTempFile(t, dir, "claude-accounts.list", "# a\n\nnotanaccount\nWork:work\n# b\n")
	_, code := runBashFunc(t, "lib/statusline.sh", "gt_multiple_claude_accounts",
		[]string{list}, nil)
	assertExitCode(t, code, 0)
}

// --- gt_account_color ---
// Assigns each Claude account a distinct 256-color index (random, non-repeating)
// keyed by its dir, persisted in a colors file so the statusline and the TUI menu
// agree. Must stay in lock-step with claudeaccount.Palette / ColorFor in Go.

// accountPalette mirrors GT_ACCOUNT_PALETTE in lib/statusline.sh (and
// claudeaccount.Palette in Go). A test failure here means the three drifted.
var accountPalette = map[string]bool{
	"39": true, "208": true, "170": true, "78": true, "203": true, "141": true,
	"43": true, "220": true, "205": true, "75": true, "156": true, "214": true,
}

func TestStatusline_account_color_assigns_a_palette_member(t *testing.T) {
	dir := t.TempDir()
	colors := filepath.Join(dir, "claude-account-colors")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_account_color",
		[]string{colors, "work"}, nil)
	assertExitCode(t, code, 0)
	if !accountPalette[strings.TrimSpace(out)] {
		t.Fatalf("assigned color %q is not a palette member", strings.TrimSpace(out))
	}
}

func TestStatusline_account_color_is_stable_across_calls(t *testing.T) {
	dir := t.TempDir()
	colors := filepath.Join(dir, "claude-account-colors")
	first, code := runBashFunc(t, "lib/statusline.sh", "gt_account_color",
		[]string{colors, "work"}, nil)
	assertExitCode(t, code, 0)
	again, code := runBashFunc(t, "lib/statusline.sh", "gt_account_color",
		[]string{colors, "work"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(first) != strings.TrimSpace(again) {
		t.Fatalf("color must be stable: first %q, again %q", strings.TrimSpace(first), strings.TrimSpace(again))
	}
}

// Two accounts must never share a color while the palette still has room.
func TestStatusline_account_color_distinct_accounts_distinct_colors(t *testing.T) {
	dir := t.TempDir()
	colors := filepath.Join(dir, "claude-account-colors")
	work, _ := runBashFunc(t, "lib/statusline.sh", "gt_account_color", []string{colors, "work"}, nil)
	personal, _ := runBashFunc(t, "lib/statusline.sh", "gt_account_color", []string{colors, "personal"}, nil)
	if strings.TrimSpace(work) == strings.TrimSpace(personal) {
		t.Fatalf("distinct accounts got the same color %q", strings.TrimSpace(work))
	}
}

// An empty dir is the implicit Default login; it keys under "default" so bash and
// Go land on the same slot.
func TestStatusline_account_color_empty_dir_keys_as_default(t *testing.T) {
	dir := t.TempDir()
	colors := filepath.Join(dir, "claude-account-colors")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_account_color",
		[]string{colors, ""}, nil)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(colors)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "default:"+strings.TrimSpace(out)) {
		t.Fatalf("empty dir should persist under \"default\", file:\n%s", string(data))
	}
}

func TestStatusline_account_color_reads_existing_assignment(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-account-colors", "work:141\n")
	colors := filepath.Join(dir, "claude-account-colors")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_account_color",
		[]string{colors, "work"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "141" {
		t.Fatalf("should read persisted 141, got %q", strings.TrimSpace(out))
	}
}

// --- gt_usage_bar ---
// Renders a percentage (0-100) as a 10-cell segmented pill bar — one cell per
// 10% — as filled (◼), half (◧), or empty (◻) squares. The cell count rounds to
// the nearest HALF, so the bar reads to 5% at a glance without a number.
// Out-of-range input clamps to empty/full.

const (
	barFull  = "◼" // ◼ full cell
	barHalf  = "◧" // ◧ half cell (5%)
	barEmpty = "◻" // ◻ empty cell
	barWidth = 10  // one cell per 10%
)

// bar builds an expected pill of `full` filled squares then empties.
func bar(full int) string {
	return strings.Repeat(barFull, full) + strings.Repeat(barEmpty, barWidth-full)
}

// barH builds an expected pill of `full` filled squares, one half square, then empties.
func barH(full int) string {
	return strings.Repeat(barFull, full) + barHalf + strings.Repeat(barEmpty, barWidth-full-1)
}

func TestStatusline_usage_bar_zero_is_all_empty(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"0", "10"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != bar(0) {
		t.Fatalf("expected %q, got %q", bar(0), strings.TrimSpace(out))
	}
}

func TestStatusline_usage_bar_hundred_is_all_full(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"100", "10"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != bar(10) {
		t.Fatalf("expected %q, got %q", bar(10), strings.TrimSpace(out))
	}
}

func TestStatusline_usage_bar_half_fills_five_cells(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"50", "10"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != bar(5) {
		t.Fatalf("expected %q, got %q", bar(5), strings.TrimSpace(out))
	}
}

// 5% is exactly half of one 10% cell → a lone half square leads the bar.
func TestStatusline_usage_bar_five_percent_is_a_half_cell(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"5", "10"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != barH(0) {
		t.Fatalf("expected %q, got %q", barH(0), strings.TrimSpace(out))
	}
}

// 45% → four full cells and a half (4.5 cells).
func TestStatusline_usage_bar_forty_five_is_four_and_a_half(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"45", "10"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != barH(4) {
		t.Fatalf("expected %q, got %q", barH(4), strings.TrimSpace(out))
	}
}

// 95% → nine full cells and a half.
func TestStatusline_usage_bar_ninety_five_is_nine_and_a_half(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"95", "10"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != barH(9) {
		t.Fatalf("expected %q, got %q", barH(9), strings.TrimSpace(out))
	}
}

// Rounding is to the nearest half-cell (each cell is 10%, so the split sits at
// 2.5% into a cell): 22% (4.4 halves) → 2 full; 23% (4.6) → 2½.
func TestStatusline_usage_bar_rounds_to_nearest_half(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"22", "10"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != bar(2) {
		t.Fatalf("22%% should round to 2 full cells, expected %q, got %q", bar(2), strings.TrimSpace(out))
	}
	out, code = runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"23", "10"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != barH(2) {
		t.Fatalf("23%% should round to 2½ cells, expected %q, got %q", barH(2), strings.TrimSpace(out))
	}
}

func TestStatusline_usage_bar_defaults_to_width_ten(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"100"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != bar(10) {
		t.Fatalf("expected %q, got %q", bar(10), strings.TrimSpace(out))
	}
}

// The width stays a parameter — a caller can still ask for a different size.
func TestStatusline_usage_bar_honors_custom_width(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"100", "6"}, nil)
	assertExitCode(t, code, 0)
	if want := strings.Repeat(barFull, 6); strings.TrimSpace(out) != want {
		t.Fatalf("expected %q, got %q", want, strings.TrimSpace(out))
	}
}

func TestStatusline_usage_bar_clamps_above_hundred(t *testing.T) {
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_usage_bar", []string{"150", "10"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != bar(10) {
		t.Fatalf("expected %q, got %q", bar(10), strings.TrimSpace(out))
	}
}

// --- gt_weekly_used_pct ---
// Pulls the 7-day (weekly) window's used_percentage out of the statusline JSON
// as a rounded whole number, or nothing when that window is absent. The wrapper
// feeds it to gt_usage_bar for the pill shape; the pill wears the account's
// profile color (not a severity grade).

func TestStatusline_weekly_used_pct_extracts_seven_day_percentage(t *testing.T) {
	input := `{"rate_limits":{"five_hour":{"used_percentage":10,"resets_at":1},"seven_day":{"used_percentage":42,"resets_at":2}}}`
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_weekly_used_pct",
		[]string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "42" {
		t.Fatalf("expected %q, got %q", "42", strings.TrimSpace(out))
	}
}

func TestStatusline_weekly_used_pct_rounds_decimal_percentage(t *testing.T) {
	input := `{"rate_limits":{"seven_day":{"used_percentage":42.7,"resets_at":2}}}`
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_weekly_used_pct",
		[]string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "43" {
		t.Fatalf("expected %q, got %q", "43", strings.TrimSpace(out))
	}
}

func TestStatusline_weekly_used_pct_zero_percentage(t *testing.T) {
	input := `{"rate_limits":{"seven_day":{"used_percentage":0,"resets_at":2}}}`
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_weekly_used_pct",
		[]string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "0" {
		t.Fatalf("expected %q, got %q", "0", strings.TrimSpace(out))
	}
}

func TestStatusline_weekly_used_pct_empty_when_rate_limits_absent(t *testing.T) {
	input := `{"model":{"display_name":"Fable 5"}}`
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_weekly_used_pct",
		[]string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty output, got %q", strings.TrimSpace(out))
	}
}

// seven_day may be missing even when five_hour is present (the API omits a
// window that has no data yet). The weekly figure must not fall through to the
// 5-hour number in that case.
func TestStatusline_weekly_used_pct_empty_when_only_five_hour(t *testing.T) {
	input := `{"rate_limits":{"five_hour":{"used_percentage":88,"resets_at":1}}}`
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_weekly_used_pct",
		[]string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty output, got %q", strings.TrimSpace(out))
	}
}

// --- gt_five_hour_used_pct ---
// Mirror of gt_weekly_used_pct for the 5-hour window: pulls
// rate_limits.five_hour.used_percentage out of the statusline JSON as a rounded
// whole number, anchored inside the five_hour object so it never crosses into
// seven_day, and yields nothing when the window is absent.

func TestStatusline_five_hour_used_pct_extracts_five_hour_percentage(t *testing.T) {
	input := `{"rate_limits":{"five_hour":{"used_percentage":25,"resets_at":1},"seven_day":{"used_percentage":42,"resets_at":2}}}`
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_five_hour_used_pct",
		[]string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "25" {
		t.Fatalf("expected %q, got %q", "25", strings.TrimSpace(out))
	}
}

func TestStatusline_five_hour_used_pct_rounds_decimal_percentage(t *testing.T) {
	input := `{"rate_limits":{"five_hour":{"used_percentage":88.6,"resets_at":1}}}`
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_five_hour_used_pct",
		[]string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "89" {
		t.Fatalf("expected %q, got %q", "89", strings.TrimSpace(out))
	}
}

func TestStatusline_five_hour_used_pct_empty_when_rate_limits_absent(t *testing.T) {
	input := `{"model":{"display_name":"Fable 5"}}`
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_five_hour_used_pct",
		[]string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty output, got %q", strings.TrimSpace(out))
	}
}

// five_hour must not fall through to seven_day's number when only seven_day is
// present (the API omits a window with no data yet).
func TestStatusline_five_hour_used_pct_empty_when_only_seven_day(t *testing.T) {
	input := `{"rate_limits":{"seven_day":{"used_percentage":42,"resets_at":2}}}`
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_five_hour_used_pct",
		[]string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty output, got %q", strings.TrimSpace(out))
	}
}

// The wrapper renders the weekly (7-day) usage pill but NOT the account profile
// itself: no account glyph, no account label. The pill still wears the account's
// profile color (its only remaining tie to the login), painted from the palette
// — not a severity grade.
func TestStatusline_wrapper_weekly_bar_wears_account_color(t *testing.T) {
	env := setupWrapperTest(t)
	// Force the Keychain Default login so the color key is deterministic even
	// when the test host itself runs under an isolated CLAUDE_CONFIG_DIR.
	env = append(env, "CLAUDE_CONFIG_DIR=")
	fakeHome := wrapperHome(env)
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, cfg, "claude-accounts.list", "Personal:personal\n")
	// Seed Default's color so the pill color is predictable.
	writeTempFile(t, cfg, "claude-account-colors", "default:141\n")

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	// 90% weekly usage → a 9-of-10 filled pill bar (one cell per 10%).
	stdinData := `{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"rate_limits":{"seven_day":{"used_percentage":90,"resets_at":2}},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "7d")
	// The pill wears the account's profile color (141). (Severity codes like
	// 01;33 aren't asserted-against here because unrelated segments — the context
	// icon, CPU — legitimately use them.)
	assertContains(t, out, "\x1b[38;5;141m"+bar(9))
	// The profile itself is NOT rendered: no account glyph, no account label.
	assertNotContains(t, out, "\U000f0004") // account glyph 󰀄
	assertNotContains(t, out, "Default")    // the Default login's label
}

// The account profile is no longer rendered in the statusline — no glyph, no
// label — but the login's persistent palette color is STILL assigned and written
// to the shared colors file, because the weekly pill and the TUI/ledger account
// pill still paint the login in that color.
func TestStatusline_wrapper_omits_profile_but_persists_account_color(t *testing.T) {
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=")
	fakeHome := wrapperHome(env)
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, cfg, "claude-accounts.list", "Personal:personal\n")

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"model":{"id":"x","display_name":"Fable 5"},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	// The account profile must NOT be rendered: neither the glyph nor a label.
	assertNotContains(t, out, "\U000f0004") // account glyph 󰀄
	assertNotContains(t, out, "Default")
	// But the Default login's palette color is still assigned and persisted under
	// "default" so the weekly pill and the account menu paint it consistently.
	data, err := os.ReadFile(filepath.Join(cfg, "claude-account-colors"))
	if err != nil {
		t.Fatalf("colors file must be written even without a rendered profile: %v", err)
	}
	if !strings.Contains(string(data), "default:") {
		t.Fatalf("Default color not persisted, file:\n%s", string(data))
	}
}

// With only one login there is no account segment, so there is nothing to hang
// the weekly figure off of — it stays hidden to avoid clutter for solo users.
func TestStatusline_wrapper_omits_weekly_limit_without_account_segment(t *testing.T) {
	env := setupWrapperTest(t)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"rate_limits":{"seven_day":{"used_percentage":42,"resets_at":2}},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "7d ")
}

// --- usage-bars mode (7d / 5h / both / none) ---
// The `usage_bars` preference in ~/.config/wisp-deck/settings chooses which
// usage pills the statusline renders. runUsageBarsWrapper drives the wrapper with
// a 2-account setup (so the bars are eligible), 5h usage 30% (→ 3-cell bar) and
// 7d usage 90% (→ 9-cell bar), both in the Default login's color (141).
func runUsageBarsWrapper(t *testing.T, mode string) string {
	t.Helper()
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=")
	fakeHome := wrapperHome(env)
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, cfg, "claude-accounts.list", "Personal:personal\n")
	writeTempFile(t, cfg, "claude-account-colors", "default:141\n")
	if mode != "" {
		writeTempFile(t, cfg, "settings", "usage_bars="+mode+"\n")
	}
	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"rate_limits":{"five_hour":{"used_percentage":30,"resets_at":1},"seven_day":{"used_percentage":90,"resets_at":2}},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)
	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	return out
}

func TestStatusline_usage_bars_mode_7d_only(t *testing.T) {
	out := runUsageBarsWrapper(t, "7d")
	assertContains(t, out, "7d")
	assertContains(t, out, bar(9)) // seven_day 90%
	assertNotContains(t, out, "5h")
	assertNotContains(t, out, bar(3)) // five_hour bar absent
}

func TestStatusline_usage_bars_mode_5h_only(t *testing.T) {
	out := runUsageBarsWrapper(t, "5h")
	assertContains(t, out, "5h")
	assertContains(t, out, bar(3)) // five_hour 30%
	assertNotContains(t, out, "7d")
	assertNotContains(t, out, bar(9)) // seven_day bar absent
}

func TestStatusline_usage_bars_mode_both(t *testing.T) {
	out := runUsageBarsWrapper(t, "both")
	assertContains(t, out, "7d")
	assertContains(t, out, "5h")
	assertContains(t, out, bar(9)) // seven_day 90%
	assertContains(t, out, bar(3)) // five_hour 30%
}

func TestStatusline_usage_bars_mode_none(t *testing.T) {
	out := runUsageBarsWrapper(t, "none")
	assertNotContains(t, out, "7d")
	assertNotContains(t, out, "5h")
}

// An unset preference preserves today's behavior: the 7d bar only.
func TestStatusline_usage_bars_default_is_7d(t *testing.T) {
	out := runUsageBarsWrapper(t, "")
	assertContains(t, out, "7d")
	assertContains(t, out, bar(9))
	assertNotContains(t, out, "5h")
}

// Right after a mid-session account switch the relaunched claude has no
// rate_limits yet (they arrive with the first API response), so the usage bar —
// the ONLY thing that carries the account's color — would be hidden and the
// switch would show no account identity until the first response lands. To make
// the switch instantly visible, when the account is eligible but a window's usage
// figure isn't available yet, the wrapper renders that window's tag with a "…"
// placeholder painted in the account color, upgrading to the real bar once usage
// arrives. No profile glyph/label is reintroduced.
func TestStatusline_wrapper_colored_placeholder_before_usage_data(t *testing.T) {
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=")
	fakeHome := wrapperHome(env)
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, cfg, "claude-accounts.list", "Personal:personal\n")
	writeTempFile(t, cfg, "claude-account-colors", "default:141\n")

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	// No rate_limits: exactly the JSON right after the switch/resume.
	stdinData := `{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "7d")
	// The account color (141) is shown INSTANTLY via the "…" placeholder.
	assertContains(t, out, "\x1b[38;5;141m…")
	// No profile reintroduced.
	assertNotContains(t, out, "\U000f0004") // account glyph 󰀄
	assertNotContains(t, out, "Default")
}

// The placeholder is gated on account eligibility (2+ logins). A solo user with a
// single login must still see NOTHING — no bar, no placeholder, no clutter.
func TestStatusline_wrapper_no_placeholder_for_single_login(t *testing.T) {
	env := setupWrapperTest(t) // setupWrapperTest seeds a single-login setup
	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "7d")
	assertNotContains(t, out, "5h")
	assertNotContains(t, out, "…")
}

// ============================================================
// Overall metrics must cover the processes the agent starts
// ============================================================

// setupWrapperSpawnedTest builds a hermetic env where the claude ancestor has
// two spawned children (201, 202). footprint emits 10 MB per pid argument, so
// the whole session tree (root+201+202) reads 30M. CPU: root 27%, children
// 5% + 10% => 42%.
func setupWrapperSpawnedTest(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()

	fakeHome := filepath.Join(dir, "home")
	writeTempFile(t, fakeHome, ".claude/statusline-command.sh", `echo "GITINFO"`)
	mockCommand(t, dir, "npx", `echo "12.3%"`)
	// The claude root pid is dynamic ($PPID of the wrapper), so the default case
	// answers it; the children are terminal.
	mockCommand(t, dir, "pgrep", `
pid="${@: -1}"
case "$pid" in
  201|202) exit 1 ;;
  *) printf '201\n202\n' ;;
esac
`)
	mockCommand(t, dir, "footprint", `for _ in "$@"; do printf '    phys_footprint: 10 MB\n'; done`)
	mockCommand(t, dir, "ps", `
case "$*" in
  *comm=*) printf '/Users/test/.local/bin/claude\n' ;;
  *ppid=*) printf '1\n' ;;
  *%cpu=*)
    pid="${@: -1}"
    case "$pid" in
      201) printf ' 5.0\n' ;;
      202) printf ' 10.0\n' ;;
      *) printf ' 27.0\n' ;;
    esac ;;
  *rss=*)
    pid="${@: -1}"
    case "$pid" in
      201) printf '10240\n' ;;
      202) printf '10240\n' ;;
      *) printf '20480\n' ;;
    esac ;;
esac
`)

	return buildEnv(t, []string{filepath.Join(dir, "bin")}, "HOME="+fakeHome)
}

// The agent-spawned processes are counted INSIDE the overall memory/CPU
// figures (the tree walk covers every descendant) — they get no segment of
// their own.
func TestStatusline_wrapper_overall_metrics_include_agent_spawned_processes(t *testing.T) {
	env := setupWrapperSpawnedTest(t)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	// Overall figures cover claude + its two spawned children: 3 x 10MB, 27+5+10.
	assertContains(t, out, memIcon+" 30M")
	assertContains(t, out, cpuIcon+" 42%")
	// No dedicated spawned-processes segment (nf-fa-sitemap glyph).
	assertNotContains(t, out, "")
}

// --- per-subscription colors (gt_config_color) ---
// Subscriptions wear persistent identity colors exactly like accounts: same
// palette, same file format (claude-config-colors, keyed by config filename),
// mirrored by claudeconfig.ColorFor in Go.

func TestStatusline_config_color_reads_existing_assignment(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "claude-config-colors", "glm.json:205\n")
	colors := filepath.Join(dir, "claude-config-colors")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_config_color",
		[]string{colors, "glm.json"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "205" {
		t.Fatalf("color = %q, want 205", strings.TrimSpace(out))
	}
}

func TestStatusline_config_color_assigns_palette_member_and_persists(t *testing.T) {
	dir := t.TempDir()
	colors := filepath.Join(dir, "claude-config-colors")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_config_color",
		[]string{colors, "glm.json"}, nil)
	assertExitCode(t, code, 0)
	if !accountPalette[strings.TrimSpace(out)] {
		t.Fatalf("assigned color %q is not a palette member", strings.TrimSpace(out))
	}
	data, err := os.ReadFile(colors)
	if err != nil {
		t.Fatalf("assignment not persisted: %v", err)
	}
	if !strings.Contains(string(data), "glm.json:") {
		t.Fatalf("colors file missing assignment:\n%s", string(data))
	}
}

func TestStatusline_config_color_avoids_account_colors(t *testing.T) {
	dir := t.TempDir()
	// Accounts wear every palette color but one; the subscription must take the
	// single remaining hue so identities stay visually distinct.
	palette := []string{"39", "208", "170", "78", "203", "141", "43", "220", "205", "75", "156"}
	lines := ""
	for i, c := range palette {
		lines += fmt.Sprintf("acct%d:%s\n", i, c)
	}
	writeTempFile(t, dir, "claude-account-colors", lines)
	accountColors := filepath.Join(dir, "claude-account-colors")
	colors := filepath.Join(dir, "claude-config-colors")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_config_color",
		[]string{colors, "glm.json", accountColors}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "214" {
		t.Fatalf("color = %q, want the only free palette member 214", strings.TrimSpace(out))
	}
}

// When this pane runs a subscription backend (WISP_DECK_CLAUDE_CONFIG names the
// config), the usage bars wear the SUBSCRIPTION's color, not the account's —
// the account is overridden while the backend runs.
func TestStatusline_wrapper_usage_bar_wears_subscription_color(t *testing.T) {
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=glm.json")
	fakeHome := wrapperHome(env)
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, cfg, "claude-accounts.list", "Personal:personal\n")
	writeTempFile(t, cfg, "claude-account-colors", "default:141\n")
	writeTempFile(t, cfg, "claude-config-colors", "glm.json:205\n")

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"rate_limits":{"seven_day":{"used_percentage":90,"resets_at":2}},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[38;5;205m"+bar(9))
	assertNotContains(t, out, "\x1b[38;5;141m"+bar(9))
}

// Standard Claude (WISP_DECK_CLAUDE_CONFIG stamped empty) keeps the account
// color on the usage bars.
func TestStatusline_wrapper_usage_bar_keeps_account_color_when_standard(t *testing.T) {
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=", "WISP_DECK_CLAUDE_CONFIG=")
	fakeHome := wrapperHome(env)
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, cfg, "claude-accounts.list", "Personal:personal\n")
	writeTempFile(t, cfg, "claude-account-colors", "default:141\n")
	writeTempFile(t, cfg, "claude-config-colors", "glm.json:205\n")

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"model":{"id":"claude-fable-5","display_name":"Fable 5"},"rate_limits":{"seven_day":{"used_percentage":90,"resets_at":2}},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	out, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	assertContains(t, out, "\x1b[38;5;141m"+bar(9))
}

// The mirror direction: a NEW account color assignment must avoid colors
// already worn by subscriptions, so logins and subscriptions never collide.
func TestStatusline_account_color_avoids_config_colors(t *testing.T) {
	dir := t.TempDir()
	palette := []string{"39", "208", "170", "78", "203", "141", "43", "220", "205", "75", "156"}
	lines := ""
	for i, c := range palette {
		lines += fmt.Sprintf("cfg%d.json:%s\n", i, c)
	}
	writeTempFile(t, dir, "claude-config-colors", lines)
	configColors := filepath.Join(dir, "claude-config-colors")
	colors := filepath.Join(dir, "claude-account-colors")
	out, code := runBashFunc(t, "lib/statusline.sh", "gt_account_color",
		[]string{colors, "work", configColors}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "214" {
		t.Fatalf("color = %q, want the only free palette member 214", strings.TrimSpace(out))
	}
}

// The statusline wrapper feeds the subscription colors into a fresh account
// assignment as the avoid set, so first-render assignment cannot collide.
func TestStatusline_wrapper_account_assignment_avoids_config_colors(t *testing.T) {
	env := setupWrapperTest(t)
	env = append(env, "CLAUDE_CONFIG_DIR=")
	fakeHome := wrapperHome(env)
	cfg := filepath.Join(fakeHome, ".config", "wisp-deck")
	writeTempFile(t, cfg, "claude-accounts.list", "Personal:personal\n")
	palette := []string{"39", "208", "170", "78", "203", "141", "43", "220", "205", "75", "156"}
	lines := ""
	for i, c := range palette {
		lines += fmt.Sprintf("cfg%d.json:%s\n", i, c)
	}
	writeTempFile(t, cfg, "claude-config-colors", lines)

	root := projectRoot(t)
	wrapperPath := filepath.Join(root, "templates", "statusline-wrapper.sh")
	stdinData := `{"model":{"id":"x","display_name":"Fable 5"},"workspace":{"current_dir":"/tmp"}}`
	script := fmt.Sprintf(`echo '%s' | bash '%s'`, stdinData, wrapperPath)

	_, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(filepath.Join(cfg, "claude-account-colors"))
	if err != nil {
		t.Fatalf("account color not persisted: %v", err)
	}
	if !strings.Contains(string(data), "default:214") {
		t.Fatalf("account assignment did not avoid subscription colors, file:\n%s", string(data))
	}
}
