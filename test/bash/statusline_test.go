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

	env := buildEnv(t, nil, "GHOST_TAB_BASELINE_FILE="+baselineFile)
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
	// Explicitly unset GHOST_TAB_BASELINE_FILE
	script := fmt.Sprintf(`unset GHOST_TAB_BASELINE_FILE; echo '%s' | bash '%s'`, stdinData, cmdPath)

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

	env := buildEnv(t, nil, "GHOST_TAB_BASELINE_FILE=/tmp/ghost-tab-nonexistent-baseline")
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

func TestStatusline_statusline_command_omits_branch_name(t *testing.T) {
	repoDir := statuslineCmdSetupGitRepo(t)
	repoBasename := filepath.Base(repoDir)

	// Check out a uniquely-named branch so its presence is unambiguous.
	branchName := "ghost-tab-omit-branch-check"
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
	ctxIcon = "\U000F09D1" // nf-md-brain       — context window
	memIcon = "\U000F035B" // nf-md-memory      — memory load (chip)
	cpuIcon = "\U0000F0E4" // nf-fa-tachometer  — CPU load (gauge; distinct from the memory chip)
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
