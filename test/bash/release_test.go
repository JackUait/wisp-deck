package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// releaseSnippet builds a bash snippet that sources scripts/release.sh
// (which has a source guard so main doesn't run), then runs the provided bash code.
func releaseSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	releasePath := filepath.Join(root, "scripts", "release.sh")
	return fmt.Sprintf("source %q && %s", releasePath, body)
}

// initGitRepo creates a minimal git repo in dir with one commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "init", "--initial-branch=main"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git setup failed: %v\n%s", err, out)
		}
	}
	writeTempFile(t, dir, "dummy", "init")
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "init"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit failed: %v\n%s", err, out)
		}
	}
}

// ============================================================
// check_clean_tree tests
// ============================================================

func TestCheckCleanTree_passes_on_clean_repo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	snippet := releaseSnippet(t, `cd "`+dir+`" && check_clean_tree`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	_ = out
}

func TestCheckCleanTree_fails_on_dirty_repo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTempFile(t, dir, "untracked.txt", "dirty")

	snippet := releaseSnippet(t, `cd "`+dir+`" && check_clean_tree`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 1)
	assertContains(t, out, "clean")
}

// ============================================================
// check_main_branch tests
// ============================================================

func TestCheckMainBranch_passes_on_main(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	snippet := releaseSnippet(t, `cd "`+dir+`" && check_main_branch`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	_ = out
}

func TestCheckMainBranch_fails_on_other_branch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	cmd := exec.Command("git", "checkout", "-b", "feature")
	cmd.Dir = dir
	cmd.CombinedOutput()

	snippet := releaseSnippet(t, `cd "`+dir+`" && check_main_branch`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 1)
	assertContains(t, out, "main")
}

// ============================================================
// read_version tests
// ============================================================

func TestReadVersion_reads_valid_semver(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "VERSION", "2.0.0\n")

	snippet := releaseSnippet(t, `read_version "`+filepath.Join(dir, "VERSION")+`"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "2.0.0" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "2.0.0")
	}
}

func TestReadVersion_fails_on_missing_file(t *testing.T) {
	snippet := releaseSnippet(t, `read_version "/nonexistent/VERSION"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 1)
	assertContains(t, out, "VERSION")
}

func TestReadVersion_fails_on_invalid_format(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "VERSION", "not-a-version\n")

	snippet := releaseSnippet(t, `read_version "`+filepath.Join(dir, "VERSION")+`"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 1)
	assertContains(t, out, "semver")
}

// ============================================================
// check_tag_not_exists tests
// ============================================================

func TestCheckTagNotExists_passes_when_tag_missing(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	snippet := releaseSnippet(t, `cd "`+dir+`" && check_tag_not_exists "v2.0.0"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	_ = out
}

func TestCheckTagNotExists_fails_when_tag_exists(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	cmd := exec.Command("git", "tag", "v1.0.0")
	cmd.Dir = dir
	cmd.CombinedOutput()

	snippet := releaseSnippet(t, `cd "`+dir+`" && check_tag_not_exists "v1.0.0"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 1)
	assertContains(t, out, "v1.0.0")
}

// ============================================================
// check_gh_auth tests
// ============================================================

func TestCheckGhAuth_passes_when_authenticated(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "gh", `
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then exit 0; fi
exit 0
`)
	snippet := releaseSnippet(t, `check_gh_auth`)
	env := buildEnv(t, []string{binDir})
	_, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
}

func TestCheckGhAuth_fails_when_not_authenticated(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "gh", `
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then exit 1; fi
exit 0
`)
	snippet := releaseSnippet(t, `check_gh_auth`)
	env := buildEnv(t, []string{binDir})
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 1)
	assertContains(t, out, "authenticated")
}

func TestCheckGhAuth_fails_when_not_installed(t *testing.T) {
	dir := t.TempDir()
	// Create an empty bin dir with no gh command
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)
	snippet := releaseSnippet(t, `check_gh_auth`)
	// Restrict PATH to the empty binDir so gh is not found (simulates "not installed")
	env := buildEnv(t, nil, "PATH="+binDir)
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 1)
	assertContains(t, out, "gh")
}

// ============================================================
// main / integration tests
// ============================================================

func TestRelease_main_fails_on_dirty_tree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTempFile(t, dir, "VERSION", "1.0.0\n")
	writeTempFile(t, dir, "untracked.txt", "dirty")

	root := projectRoot(t)
	scriptPath := filepath.Join(root, "scripts", "release.sh")
	cmd := exec.Command("bash", scriptPath, "--yes")
	cmd.Dir = dir
	cmd.Env = buildEnv(t, nil,
		"RELEASE_VERSION_FILE="+filepath.Join(dir, "VERSION"),
	)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
	}
	if code == 0 {
		t.Error("expected non-zero exit code for dirty tree")
	}
	assertContains(t, string(out), "clean")
}

func TestRelease_main_shows_confirmation_and_aborts_on_no(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTempFile(t, dir, "VERSION", "1.0.0\n")
	// Stage and commit VERSION so tree is clean
	cmd := exec.Command("git", "add", "VERSION")
	cmd.Dir = dir
	cmd.CombinedOutput()
	cmd = exec.Command("git", "commit", "-m", "add version")
	cmd.Dir = dir
	cmd.CombinedOutput()

	root := projectRoot(t)
	scriptPath := filepath.Join(root, "scripts", "release.sh")
	cmd = exec.Command("bash", scriptPath)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("n\n")

	// Mock gh as authenticated
	mockDir := t.TempDir()
	binDir := mockCommand(t, mockDir, "gh", `
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then exit 0; fi
exit 0
`)

	cmd.Env = buildEnv(t, []string{binDir},
		"RELEASE_VERSION_FILE="+filepath.Join(dir, "VERSION"),
	)

	out, _ := cmd.CombinedOutput()
	assertContains(t, string(out), "Release v1.0.0")
	assertContains(t, string(out), "Aborted")
}

// ============================================================
// Binary build / upload tests
// ============================================================

func TestRelease_does_not_check_for_formula(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "scripts", "release.sh"))
	if err != nil {
		t.Fatalf("failed to read release.sh: %v", err)
	}
	if strings.Contains(string(data), "check_formula_exists") {
		t.Errorf("release.sh still calls check_formula_exists")
	}
}

func TestRelease_builds_wisp_deck_tui_binaries(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "scripts", "release.sh"))
	if err != nil {
		t.Fatalf("failed to read release.sh: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "GOARCH=arm64") {
		t.Errorf("release.sh does not build arm64 binary")
	}
	if !strings.Contains(content, "GOARCH=amd64") {
		t.Errorf("release.sh does not build amd64 binary")
	}
}

func TestRelease_uploads_binaries_to_gh_release(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "scripts", "release.sh"))
	if err != nil {
		t.Fatalf("failed to read release.sh: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "wisp-deck-tui-darwin-arm64") {
		t.Errorf("release.sh does not upload arm64 binary asset")
	}
	if !strings.Contains(content, "wisp-deck-tui-darwin-amd64") {
		t.Errorf("release.sh does not upload amd64 binary asset")
	}
}

func TestRelease_builds_to_named_files_not_mktemp(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "scripts", "release.sh"))
	if err != nil {
		t.Fatalf("failed to read release.sh: %v", err)
	}
	content := string(data)
	// go build -o must target a file named wisp-deck-tui-darwin-arm64, not a mktemp path
	if !strings.Contains(content, `-o "$build_dir/wisp-deck-tui-darwin-arm64"`) &&
		!strings.Contains(content, `-o "${build_dir}/wisp-deck-tui-darwin-arm64"`) {
		t.Errorf("release.sh should build arm64 binary to a properly named file, not mktemp")
	}
	if !strings.Contains(content, `-o "$build_dir/wisp-deck-tui-darwin-amd64"`) &&
		!strings.Contains(content, `-o "${build_dir}/wisp-deck-tui-darwin-amd64"`) {
		t.Errorf("release.sh should build amd64 binary to a properly named file, not mktemp")
	}
}

func TestRelease_trap_does_not_reference_local_variables(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "scripts", "release.sh"))
	if err != nil {
		t.Fatalf("failed to read release.sh: %v", err)
	}
	content := string(data)
	// trap should not reference arm64_bin or amd64_bin (local to main)
	if strings.Contains(content, `trap`) && strings.Contains(content, `"$arm64_bin"`) {
		t.Errorf("trap references $arm64_bin which is local to main() and will be unbound at EXIT")
	}
}

const releaseArtifactTestVersion = "2.23.1"

func releaseArtifactTestLdflags(version string) string {
	return "-X main.Version=" + version +
		" -X main.HostEffectsCapability=enabled" +
		" -X main.SoundPreviewCapability=enabled"
}

func writeReleaseArtifactTestBinary(t *testing.T, buildDir, arch, marker string, capabilityExit int) string {
	t.Helper()
	path := writeTempFile(t, buildDir, "wisp-deck-tui-darwin-"+arch, fmt.Sprintf(`#!/bin/bash
printf '%%s\n' "$#" "$1" "$2" >> %q
if [[ "$1" == "capabilities" && "$2" == "--require-production" ]]; then
  exit %d
fi
exit 97
`, marker, capabilityExit))
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod release artifact: %v", err)
	}
	return path
}

func mockReleaseArtifactTools(
	t *testing.T,
	dir, machine, arm64Ldflags, amd64Ldflags, metadataMarker string,
) string {
	t.Helper()
	mockCommand(t, dir, "uname", fmt.Sprintf(`
if [[ "$1" == "-m" ]]; then
  printf '%%s\n' %q
  exit 0
fi
exit 97
`, machine))
	return mockCommand(t, dir, "go", fmt.Sprintf(`
if [[ "$1" != "version" || "$2" != "-m" ]]; then
  exit 97
fi
printf '%%s\n' "$1|$2|$3" >> %q
case "$3" in
  *-arm64) ldflags=%q ;;
  *-amd64) ldflags=%q ;;
  *) exit 98 ;;
esac
printf '\tbuild\t-ldflags="%%s"\n' "$ldflags"
`, metadataMarker, arm64Ldflags, amd64Ldflags))
}

func TestReleaseArtifactLdflagsHasOneEnabledSource(t *testing.T) {
	want := releaseArtifactTestLdflags(releaseArtifactTestVersion)
	snippet := releaseSnippet(t, `release_tui_ldflags "`+releaseArtifactTestVersion+`"`)
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if got := strings.TrimSpace(out); got != want {
		t.Fatalf("release_tui_ldflags() = %q, want %q", got, want)
	}

	data, err := os.ReadFile(filepath.Join(projectRoot(t), "scripts", "release.sh"))
	if err != nil {
		t.Fatalf("read release.sh: %v", err)
	}
	if got := strings.Count(string(data), "main.HostEffectsCapability=enabled"); got != 1 {
		t.Fatalf("enabled release ldflags must have one source, found %d", got)
	}
}

func TestReleaseArtifactHostArchMapsSupportedMachines(t *testing.T) {
	for _, tc := range []struct {
		machine string
		want    string
	}{
		{machine: "arm64", want: "arm64"},
		{machine: "x86_64", want: "amd64"},
	} {
		t.Run(tc.machine, func(t *testing.T) {
			mockDir := t.TempDir()
			binDir := mockCommand(t, mockDir, "uname", fmt.Sprintf(`
if [[ "$1" == "-m" ]]; then printf '%%s\n' %q; exit 0; fi
exit 97
`, tc.machine))
			out, code := runBashSnippet(t, releaseSnippet(t, `release_host_arch`), buildEnv(t, []string{binDir}))
			assertExitCode(t, code, 0)
			if got := strings.TrimSpace(out); got != tc.want {
				t.Fatalf("release_host_arch() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReleaseArtifactHostArchRejectsUnsupportedMachine(t *testing.T) {
	mockDir := t.TempDir()
	binDir := mockCommand(t, mockDir, "uname", `
if [[ "$1" == "-m" ]]; then printf '%s\n' "riscv64"; exit 0; fi
exit 97
`)
	out, code := runBashSnippet(t, releaseSnippet(t, `release_host_arch`), buildEnv(t, []string{binDir}))
	if code == 0 {
		t.Fatalf("release_host_arch unexpectedly accepted riscv64: %s", out)
	}
	assertContains(t, out, "Unsupported")
}

func TestReleaseArtifactPreflightVerifiesBothAndRunsOnlyHostCapability(t *testing.T) {
	for _, tc := range []struct {
		machine  string
		hostArch string
	}{
		{machine: "arm64", hostArch: "arm64"},
		{machine: "x86_64", hostArch: "amd64"},
	} {
		t.Run(tc.machine, func(t *testing.T) {
			buildDir := t.TempDir()
			arm64Marker := filepath.Join(t.TempDir(), "arm64-invocations")
			amd64Marker := filepath.Join(t.TempDir(), "amd64-invocations")
			arm64Asset := writeReleaseArtifactTestBinary(t, buildDir, "arm64", arm64Marker, 0)
			amd64Asset := writeReleaseArtifactTestBinary(t, buildDir, "amd64", amd64Marker, 0)
			metadataMarker := filepath.Join(t.TempDir(), "metadata-invocations")
			ldflags := releaseArtifactTestLdflags(releaseArtifactTestVersion)
			mockDir := t.TempDir()
			binDir := mockReleaseArtifactTools(t, mockDir, tc.machine, ldflags, ldflags, metadataMarker)

			snippet := releaseSnippet(t, fmt.Sprintf(
				`ldflags="$(release_tui_ldflags %q)"; verify_release_tui_artifacts %q "$ldflags"`,
				releaseArtifactTestVersion,
				buildDir,
			))
			out, code := runBashSnippet(t, snippet, buildEnv(t, []string{binDir}))
			assertExitCode(t, code, 0)

			metadataCalls, err := os.ReadFile(metadataMarker)
			if err != nil {
				t.Fatalf("read metadata calls: %v\noutput:\n%s", err, out)
			}
			wantMetadataCalls := strings.Join([]string{
				"version|-m|" + arm64Asset,
				"version|-m|" + amd64Asset,
			}, "\n")
			if got := strings.TrimSpace(string(metadataCalls)); got != wantMetadataCalls {
				t.Fatalf("metadata calls = %q, want %q", got, wantMetadataCalls)
			}

			hostMarker, otherMarker := arm64Marker, amd64Marker
			if tc.hostArch == "amd64" {
				hostMarker, otherMarker = amd64Marker, arm64Marker
			}
			hostCalls, err := os.ReadFile(hostMarker)
			if err != nil {
				t.Fatalf("read host capability call: %v", err)
			}
			if got, want := string(hostCalls), "2\ncapabilities\n--require-production\n"; got != want {
				t.Fatalf("host capability call = %q, want %q", got, want)
			}
			if _, err := os.Stat(otherMarker); !os.IsNotExist(err) {
				t.Fatalf("non-host release asset was executed; marker error = %v", err)
			}
		})
	}
}

func TestReleaseArtifactPreflightRejectsInvalidMetadataBeforeExecution(t *testing.T) {
	validLdflags := releaseArtifactTestLdflags(releaseArtifactTestVersion)
	invalidLdflags := strings.Replace(validLdflags, "HostEffectsCapability=enabled", "HostEffectsCapability=disabled", 1)
	for _, invalidArch := range []string{"arm64", "amd64"} {
		t.Run(invalidArch, func(t *testing.T) {
			buildDir := t.TempDir()
			arm64Marker := filepath.Join(t.TempDir(), "arm64-invocations")
			amd64Marker := filepath.Join(t.TempDir(), "amd64-invocations")
			arm64Asset := writeReleaseArtifactTestBinary(t, buildDir, "arm64", arm64Marker, 0)
			amd64Asset := writeReleaseArtifactTestBinary(t, buildDir, "amd64", amd64Marker, 0)
			arm64Ldflags, amd64Ldflags := validLdflags, validLdflags
			if invalidArch == "arm64" {
				arm64Ldflags = invalidLdflags
			} else {
				amd64Ldflags = invalidLdflags
			}
			metadataMarker := filepath.Join(t.TempDir(), "metadata-invocations")
			mockDir := t.TempDir()
			binDir := mockReleaseArtifactTools(t, mockDir, "x86_64", arm64Ldflags, amd64Ldflags, metadataMarker)

			snippet := releaseSnippet(t, fmt.Sprintf(
				`verify_release_tui_artifacts %q %q`,
				buildDir,
				validLdflags,
			))
			out, code := runBashSnippet(t, snippet, buildEnv(t, []string{binDir}))
			if code == 0 {
				t.Fatalf("invalid %s metadata unexpectedly passed", invalidArch)
			}
			assertContains(t, out, "linker metadata")

			metadataCalls, err := os.ReadFile(metadataMarker)
			if err != nil {
				t.Fatalf("read metadata calls: %v", err)
			}
			wantMetadataCalls := strings.Join([]string{
				"version|-m|" + arm64Asset,
				"version|-m|" + amd64Asset,
			}, "\n")
			if got := strings.TrimSpace(string(metadataCalls)); got != wantMetadataCalls {
				t.Fatalf("metadata calls = %q, want %q", got, wantMetadataCalls)
			}
			for _, marker := range []string{arm64Marker, amd64Marker} {
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("artifact executed before metadata passed; marker error = %v", err)
				}
			}
		})
	}
}

func TestReleaseCapabilityPreflightRejectsFailingHostProbe(t *testing.T) {
	buildDir := t.TempDir()
	arm64Marker := filepath.Join(t.TempDir(), "arm64-invocations")
	amd64Marker := filepath.Join(t.TempDir(), "amd64-invocations")
	writeReleaseArtifactTestBinary(t, buildDir, "arm64", arm64Marker, 0)
	writeReleaseArtifactTestBinary(t, buildDir, "amd64", amd64Marker, 1)
	metadataMarker := filepath.Join(t.TempDir(), "metadata-invocations")
	ldflags := releaseArtifactTestLdflags(releaseArtifactTestVersion)
	mockDir := t.TempDir()
	binDir := mockReleaseArtifactTools(t, mockDir, "x86_64", ldflags, ldflags, metadataMarker)

	snippet := releaseSnippet(t, fmt.Sprintf(
		`verify_release_tui_artifacts %q %q`,
		buildDir,
		ldflags,
	))
	out, code := runBashSnippet(t, snippet, buildEnv(t, []string{binDir}))
	if code == 0 {
		t.Fatal("failing host capability probe unexpectedly passed")
	}
	assertContains(t, out, "production capability")
	if _, err := os.Stat(arm64Marker); !os.IsNotExist(err) {
		t.Fatalf("non-host release asset was executed; marker error = %v", err)
	}
	amd64Calls, err := os.ReadFile(amd64Marker)
	if err != nil {
		t.Fatalf("read amd64 capability call: %v", err)
	}
	if got, want := string(amd64Calls), "2\ncapabilities\n--require-production\n"; got != want {
		t.Fatalf("host capability call = %q, want %q", got, want)
	}
}

func TestReleaseArtifactPreflightPrecedesReleaseMutations(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "scripts", "release.sh"))
	if err != nil {
		t.Fatalf("read release.sh: %v", err)
	}
	content := string(data)
	preflight := strings.Index(content, `if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`)
	if preflight < 0 {
		t.Fatal("release main lacks an explicit artifact-preflight failure gate")
	}
	for _, mutation := range []string{
		"\n  codesign --sign",
		"\n  git tag -a",
		"\n  git push origin",
		"\n  gh release create",
		"\n    (cd \"$project_dir\" && npm version",
		`-o "$local_bin"`,
	} {
		index := strings.Index(content, mutation)
		if index < 0 {
			t.Fatalf("release mutation %q not found", mutation)
		}
		if index < preflight {
			t.Fatalf("release mutation %q occurs before artifact preflight", mutation)
		}
	}
}

// ============================================================
// npm publish token tests
// ============================================================

func TestRelease_reads_npm_token_from_env_file(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, ".env", "NPM_PUBLISH_TOKEN=npm_abc123\n")

	snippet := releaseSnippet(t, fmt.Sprintf(`
		project_dir=%q
		npm_token=""
		if [[ -f "$project_dir/.env" ]]; then
			npm_token="$(grep '^NPM_PUBLISH_TOKEN=' "$project_dir/.env" | cut -d= -f2- | tr -d '[:space:]' || true)"
		fi
		echo "$npm_token"
	`, dir))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "npm_abc123" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "npm_abc123")
	}
}

func TestRelease_npm_token_empty_when_no_env_file(t *testing.T) {
	dir := t.TempDir()
	// No .env file

	snippet := releaseSnippet(t, fmt.Sprintf(`
		project_dir=%q
		npm_token=""
		if [[ -f "$project_dir/.env" ]]; then
			npm_token="$(grep '^NPM_PUBLISH_TOKEN=' "$project_dir/.env" | cut -d= -f2- | tr -d '[:space:]' || true)"
		fi
		echo "token=$npm_token"
	`, dir))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "token=" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "token=")
	}
}

func TestRelease_npm_token_empty_when_env_has_no_token_key(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, ".env", "OTHER_VAR=something\n")

	snippet := releaseSnippet(t, fmt.Sprintf(`
		project_dir=%q
		npm_token=""
		if [[ -f "$project_dir/.env" ]]; then
			npm_token="$(grep '^NPM_PUBLISH_TOKEN=' "$project_dir/.env" | cut -d= -f2- | tr -d '[:space:]' || true)"
		fi
		echo "token=$npm_token"
	`, dir))
	out, code := runBashSnippet(t, snippet, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "token=" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "token=")
	}
}

func TestRelease_npm_publish_uses_token_flag(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "scripts", "release.sh"))
	if err != nil {
		t.Fatalf("failed to read release.sh: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "NPM_PUBLISH_TOKEN") {
		t.Errorf("release.sh does not read NPM_PUBLISH_TOKEN from .env")
	}
	if !strings.Contains(content, "--//registry.npmjs.org/:_authToken=") {
		t.Errorf("release.sh does not pass auth token to npm publish")
	}
}

// ============================================================
// Makefile integration test
// ============================================================

func TestMakefile_has_release_target(t *testing.T) {
	root := projectRoot(t)
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("failed to read Makefile: %v", err)
	}
	assertContains(t, string(makefile), "release:")
	assertContains(t, string(makefile), "scripts/release.sh")
}

// ============================================================
// Install verification gate
// ============================================================

// commitVersion makes a clean repo with a committed VERSION file.
func commitVersion(t *testing.T, dir, version string) {
	t.Helper()
	initGitRepo(t, dir)
	writeTempFile(t, dir, "VERSION", version+"\n")
	for _, args := range [][]string{
		{"git", "add", "VERSION"},
		{"git", "commit", "-m", "add version"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
	}
}

// A release must not publish a package users cannot install: the install
// verification runs during preflight, before anything is tagged or pushed.
func TestRelease_runs_install_verification_during_preflight(t *testing.T) {
	dir := t.TempDir()
	commitVersion(t, dir, "1.0.0")

	mockDir := t.TempDir()
	marker := filepath.Join(dir, "go-invoked")
	mockCommand(t, mockDir, "gh", `exit 0`)
	binDir := mockCommand(t, mockDir, "go", `echo "$@" >> "`+marker+`"; exit 0`)

	cmd := exec.Command("bash", filepath.Join(projectRoot(t), "scripts", "release.sh"))
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("n\n") // abort at the confirmation prompt
	cmd.Env = buildEnv(t, []string{binDir},
		"RELEASE_VERSION_FILE="+filepath.Join(dir, "VERSION"),
	)
	out, _ := cmd.CombinedOutput()

	invoked, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("release.sh never ran the install verification: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(invoked), "test") || !strings.Contains(string(invoked), "test/npx") {
		t.Errorf("expected the npx install tests to run, got: %q", strings.TrimSpace(string(invoked)))
	}
	assertContains(t, string(out), "Aborted") // preflight ran before any tagging
}

// If the install is broken, the release is refused outright.
func TestRelease_aborts_when_install_verification_fails(t *testing.T) {
	dir := t.TempDir()
	commitVersion(t, dir, "1.0.0")

	mockDir := t.TempDir()
	mockCommand(t, mockDir, "gh", `exit 0`)
	binDir := mockCommand(t, mockDir, "go", `echo "FAIL github.com/jackuait/wisp-deck/test/npx"; exit 1`)

	cmd := exec.Command("bash", filepath.Join(projectRoot(t), "scripts", "release.sh"), "--yes")
	cmd.Dir = dir
	cmd.Env = buildEnv(t, []string{binDir},
		"RELEASE_VERSION_FILE="+filepath.Join(dir, "VERSION"),
	)
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("expected release to abort when the install is broken, got success:\n%s", out)
	}
	assertContains(t, string(out), "install")
}
