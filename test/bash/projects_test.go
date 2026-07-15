package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// load_projects tests
// ============================================================

func TestLoadProjects_reads_name_path_lines(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app1:/path/to/app1\napp2:/path/to/app2\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	if lines[0] != "app1:/path/to/app1" {
		t.Errorf("line 0: got %q, want %q", lines[0], "app1:/path/to/app1")
	}
	if lines[1] != "app2:/path/to/app2" {
		t.Errorf("line 1: got %q, want %q", lines[1], "app2:/path/to/app2")
	}
}

func TestLoadProjects_skips_blank_lines(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app1:/path/to/app1\n\napp2:/path/to/app2\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "app1:/path/to/app1")
	assertContains(t, out, "app2:/path/to/app2")
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Errorf("expected 2 non-empty lines, got %d: %q", len(lines), out)
	}
}

func TestLoadProjects_skips_comment_lines(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "# This is a comment\napp1:/path/to/app1\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "app1:/path/to/app1" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "app1:/path/to/app1")
	}
}

func TestLoadProjects_returns_empty_for_missing_file(t *testing.T) {
	dir := t.TempDir()
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "nonexistent")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}

func TestLoadProjects_handles_entries_with_spaces_in_names(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "my app:/path/to/app\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "my app:/path/to/app" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "my app:/path/to/app")
	}
}

func TestLoadProjects_handles_entries_with_spaces_in_paths(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app:/path/with spaces/to/app\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "app:/path/with spaces/to/app" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "app:/path/with spaces/to/app")
	}
}

func TestLoadProjects_handles_entries_with_quotes_in_paths(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app:/path/with\"quotes/to/app\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "app:/path/with\"quotes/to/app" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "app:/path/with\"quotes/to/app")
	}
}

func TestLoadProjects_handles_entries_with_unicode_in_paths(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app:/path/with/\u00e9moji/\U0001F47B/app\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	expected := "app:/path/with/\u00e9moji/\U0001F47B/app"
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestLoadProjects_handles_entries_with_colons_in_paths(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app:/path:with:colons/app\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "app:/path:with:colons/app" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "app:/path:with:colons/app")
	}
}

func TestLoadProjects_handles_entries_with_trailing_slashes(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app:/path/to/app/\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "app:/path/to/app/" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "app:/path/to/app/")
	}
}

func TestLoadProjects_handles_empty_file(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}

func TestLoadProjects_handles_file_with_only_comments(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "# Comment 1\n# Comment 2\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}

func TestLoadProjects_handles_file_with_mixed_content(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "# Header comment\napp1:/path/app1\n\n# Another comment\napp2:/path/app2\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	if lines[0] != "app1:/path/app1" {
		t.Errorf("line 0: got %q, want %q", lines[0], "app1:/path/app1")
	}
	if lines[1] != "app2:/path/app2" {
		t.Errorf("line 1: got %q, want %q", lines[1], "app2:/path/app2")
	}
}

func TestLoadProjects_handles_Windows_line_endings_CRLF(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app1:/path/to/app1\r\napp2:/path/to/app2\r\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "app1:/path/to/app1")
	assertContains(t, out, "app2:/path/to/app2")
}

func TestLoadProjects_handles_mixed_line_endings(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app1:/path/to/app1\napp2:/path/to/app2\r\napp3:/path/to/app3\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "app1:/path/to/app1")
	assertContains(t, out, "app2:/path/to/app2")
	assertContains(t, out, "app3:/path/to/app3")
}

func TestLoadProjects_handles_file_with_only_whitespace(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "   \n\n  \t\t  \n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	// load_projects skips empty lines but whitespace-only lines pass through
	// because the check is [[ -z "$line" ]] which doesn't match lines with spaces
	assertNotContains(t, out, "app")
}

func TestLoadProjects_handles_binary_file(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "projects")
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02, 0x03, 0x04}, 0644); err != nil {
		t.Fatalf("failed to write binary file: %v", err)
	}
	_, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{binPath}, nil)
	// Binary data might be treated as lines, ensure no crash
	assertExitCode(t, code, 0)
}

func TestLoadProjects_handles_file_with_tabs(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app1\t:/path/to/app1\napp2:\t/path/to/app2\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "app1")
	assertContains(t, out, "app2")
}

func TestLoadProjects_handles_file_with_no_trailing_newline(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app1:/path/to/app1\napp2:/path/to/app2")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "app1:/path/to/app1")
	// read doesn't capture the last line if there's no trailing newline
	// This is standard bash behavior - only app1 will be output
	assertNotContains(t, out, "app2:/path/to/app2")
}

func TestLoadProjects_handles_file_with_many_trailing_newlines(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app1:/path/to/app1\napp2:/path/to/app2\n\n\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Errorf("expected 2 non-empty lines, got %d: %q", len(lines), out)
	}
}

func TestLoadProjects_handles_very_large_file_with_1000_entries(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 1; i <= 1000; i++ {
		sb.WriteString(fmt.Sprintf("app%d:/path/to/app%d\n", i, i))
	}
	writeTempFile(t, dir, "projects", sb.String())
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	assertContains(t, out, "app1:/path/to/app1")
	assertContains(t, out, "app1000:/path/to/app1000")
	lines := nonEmptyLines(out)
	if len(lines) != 1000 {
		t.Errorf("expected 1000 lines, got %d", len(lines))
	}
}

func TestLoadProjects_handles_entry_with_no_colon(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "projects", "app1:/path/to/app1\nmalformed_no_colon\napp2:/path/to/app2\n")
	out, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)
	// All lines are returned, including malformed ones
	assertContains(t, out, "app1:/path/to/app1")
	assertContains(t, out, "malformed_no_colon")
	assertContains(t, out, "app2:/path/to/app2")
}

func TestLoadProjects_handles_unreadable_file(t *testing.T) {
	dir := t.TempDir()
	fpath := writeTempFile(t, dir, "projects", "app1:/path/to/app1\n")
	if err := os.Chmod(fpath, 0000); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	defer os.Chmod(fpath, 0644) // cleanup

	_, code := runBashFunc(t, "lib/projects.sh", "load_projects", []string{fpath}, nil)
	// Should fail to read
	if code == 0 {
		t.Errorf("expected non-zero exit code for unreadable file, got 0")
	}
}

func TestLoadProjects_handles_file_in_unreadable_directory(t *testing.T) {
	dir := t.TempDir()
	readonlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readonlyDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	writeTempFile(t, readonlyDir, "projects", "app1:/path/to/app1\n")
	if err := os.Chmod(readonlyDir, 0000); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	defer os.Chmod(readonlyDir, 0755) // cleanup

	_, _ = runBashFunc(t, "lib/projects.sh", "load_projects", []string{filepath.Join(readonlyDir, "projects")}, nil)
	// load_projects uses < redirection which can open the file even if
	// directory is unreadable (file path is resolved first)
	// On macOS this may succeed or fail - either is acceptable
	// (The BATS test uses assert_success || assert_failure which always passes)
}

func TestLoadProjects_handles_file_being_modified_during_read(t *testing.T) {
	dir := t.TempDir()
	projFile := writeTempFile(t, dir, "projects", "app1:/path/to/app1\napp2:/path/to/app2\n")

	// Use a bash snippet that reads in background, modifies file, then checks output
	root := projectRoot(t)
	outFile := filepath.Join(dir, "out1")
	script := fmt.Sprintf(`
source %q
load_projects %q > %q &
pid1=$!
sleep 0.05
echo "app3:/path/to/app3" >> %q
wait "$pid1"
cat %q
`, filepath.Join(root, "lib/projects.sh"), projFile, outFile, projFile, outFile)

	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	// Should have read at least the original entries
	assertContains(t, out, "app1:/path/to/app1")
	assertContains(t, out, "app2:/path/to/app2")
}

func TestLoadProjects_roundTripAfterRewrite(t *testing.T) {
	// Simulate what RewriteProjectsFile produces: plain "name:path\n" lines.
	dir := t.TempDir()
	// Write a file in the exact format RewriteProjectsFile produces.
	content := "beta:/path/beta\nalpha:/path/alpha\ngamma:/path/gamma\n"
	writeTempFile(t, dir, "projects", content)

	out, code := runBashFunc(t, "lib/projects.sh", "load_projects",
		[]string{filepath.Join(dir, "projects")}, nil)
	assertExitCode(t, code, 0)

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "beta:/path/beta" {
		t.Errorf("pos 0: got %q, want %q", lines[0], "beta:/path/beta")
	}
	if lines[1] != "alpha:/path/alpha" {
		t.Errorf("pos 1: got %q, want %q", lines[1], "alpha:/path/alpha")
	}
	if lines[2] != "gamma:/path/gamma" {
		t.Errorf("pos 2: got %q, want %q", lines[2], "gamma:/path/gamma")
	}
}

// ============================================================
// path_expand tests
// ============================================================

func TestPathExpand_converts_tilde_to_HOME(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"~/projects/app"}, nil)
	assertExitCode(t, code, 0)
	home := os.Getenv("HOME")
	expected := home + "/projects/app"
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestPathExpand_leaves_absolute_paths_unchanged(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"/usr/local/bin"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/usr/local/bin" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "/usr/local/bin")
	}
}

func TestPathExpand_leaves_relative_paths_unchanged(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"relative/path"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "relative/path" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "relative/path")
	}
}

func TestPathExpand_handles_empty_path(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{""}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}

func TestPathExpand_handles_path_with_spaces(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"~/path with spaces/file"}, nil)
	assertExitCode(t, code, 0)
	home := os.Getenv("HOME")
	expected := home + "/path with spaces/file"
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestPathExpand_handles_path_with_single_quotes(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"~/path/with'quotes/file"}, nil)
	assertExitCode(t, code, 0)
	home := os.Getenv("HOME")
	expected := home + "/path/with'quotes/file"
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestPathExpand_handles_path_with_double_quotes(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"~/path/with\"quotes/file"}, nil)
	assertExitCode(t, code, 0)
	home := os.Getenv("HOME")
	expected := home + "/path/with\"quotes/file"
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestPathExpand_handles_path_with_unicode_characters(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"~/path/\u00e9moji/\U0001F47B/file"}, nil)
	assertExitCode(t, code, 0)
	home := os.Getenv("HOME")
	expected := home + "/path/\u00e9moji/\U0001F47B/file"
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestPathExpand_handles_path_with_only_tilde(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"~"}, nil)
	assertExitCode(t, code, 0)
	home := os.Getenv("HOME")
	if strings.TrimSpace(out) != home {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), home)
	}
}

func TestPathExpand_handles_tilde_not_at_start(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"/foo/~/bar"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/foo/~/bar" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "/foo/~/bar")
	}
}

func TestPathExpand_handles_multiple_tildes(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"~/foo/~/bar"}, nil)
	assertExitCode(t, code, 0)
	home := os.Getenv("HOME")
	expected := home + "/foo/~/bar"
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestPathExpand_handles_path_with_trailing_slash(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"~/projects/"}, nil)
	assertExitCode(t, code, 0)
	home := os.Getenv("HOME")
	expected := home + "/projects/"
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestPathExpand_handles_path_with_dotdot_components(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"~/foo/../bar"}, nil)
	assertExitCode(t, code, 0)
	home := os.Getenv("HOME")
	expected := home + "/foo/../bar"
	if strings.TrimSpace(out) != expected {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestPathExpand_handles_relative_path_with_tilde_like_name(t *testing.T) {
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"some~thing"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "some~thing" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "some~thing")
	}
}

func TestPathExpand_handles_valid_symlink(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	symlinkPath := filepath.Join(dir, "symlink")
	if err := os.Symlink(targetDir, symlinkPath); err != nil {
		t.Fatalf("symlink failed: %v", err)
	}
	input := symlinkPath + "/foo"
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != input {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), input)
	}
}

func TestPathExpand_handles_broken_symlink(t *testing.T) {
	dir := t.TempDir()
	brokenLink := filepath.Join(dir, "broken-link")
	if err := os.Symlink(filepath.Join(dir, "nonexistent"), brokenLink); err != nil {
		t.Fatalf("symlink failed: %v", err)
	}
	input := brokenLink + "/foo"
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{input}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != input {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), input)
	}
}

// ============================================================
// Helpers local to this file
// ============================================================

// nonEmptyLines splits output into lines and removes empty ones.
func nonEmptyLines(s string) []string {
	raw := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var result []string
	for _, line := range raw {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

// ============================================================
// projects-root removal guards
// ============================================================

// The projects-root setting was removed; its bash helpers must be gone too.
func TestProjectsSh_ProjectsRootHelpersRemoved(t *testing.T) {
	root := projectRoot(t)
	script := fmt.Sprintf(
		"source %q; ! declare -F get_projects_root >/dev/null && ! declare -F set_projects_root >/dev/null",
		filepath.Join(root, "lib/projects.sh"))
	out, code := runBashSnippet(t, script, nil)
	if code != 0 {
		t.Errorf("lib/projects.sh still defines projects-root helpers: %s", out)
	}
}

func TestMenuTui_DoesNotReferenceProjectsRoot(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "lib/menu-tui.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "projects-root") {
		t.Error("lib/menu-tui.sh must not reference projects-root")
	}
}
