package bash_test

// The post-commit hook keeps ~/.local/bin/wisp-deck-tui in sync with HEAD.
// lib/ deploys itself through the live symlinks, but the Go TUI only reaches
// panes through the installed binary — bac1be4 landed at 03:15 and every tab
// opened before the next manual rebuild (22:16) still painted the old header.
// The hook closes that gap: a commit touching the TUI's inputs rebuilds from
// a clean `git archive HEAD` export (never the dirty shared checkout),
// installs, re-signs, and warms the binary.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const rebuildScript = "scripts/post-commit-rebuild-tui.sh"

// setupHookRepo creates a temp git repo with the minimal layout the hook
// inspects, plus `go`/`codesign` mocks that record into markDir. The mock go
// writes an executable stub at the -o path that logs to markDir/warm.log when
// exec'd, so the warm-up run is observable.
func setupHookRepo(t *testing.T) (repoDir, homeDir, markDir string, env []string) {
	t.Helper()
	dir := t.TempDir()
	repoDir = filepath.Join(dir, "repo")
	homeDir = filepath.Join(dir, "home")
	markDir = filepath.Join(dir, "mark")
	for _, d := range []string{repoDir, homeDir, markDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeTempFile(t, repoDir, "VERSION", "9.9.9\n")
	writeTempFile(t, repoDir, "go.mod", "module example.com/fake\n")
	writeTempFile(t, repoDir, "cmd/wisp-deck-tui/main.go", "package main\n")
	writeTempFile(t, repoDir, "internal/probe.txt", "clean\n")
	writeTempFile(t, repoDir, "lib/foo.sh", "# bash lib\n")

	gitIn(t, repoDir, "init", "-q", "-b", "main")
	gitIn(t, repoDir, "add", "-A")
	commitIn(t, repoDir, "init")

	goBody := `
out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then out="$a"; fi
  prev="$a"
done
mkdir -p "$(dirname "$out")"
printf '#!/bin/bash\necho warmed >> "%s/warm.log"\n' "$MARK" > "$out"
chmod +x "$out"
{ echo "go $*"; pwd; } >> "$MARK/go.log"
cp internal/probe.txt "$MARK/probe-seen.txt" 2>/dev/null || true
`
	binDir := mockCommand(t, dir, "go", goBody)
	mockCommand(t, dir, "codesign", `echo "codesign $*" >> "$MARK/codesign.log"`)

	env = buildEnv(t, []string{binDir},
		"HOME="+homeDir,
		"MARK="+markDir,
		"WISP_DECK_HOOK_SYNC=1",
	)
	return repoDir, homeDir, markDir, env
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commitIn(t *testing.T, dir, msg string) {
	t.Helper()
	gitIn(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", msg)
}

func runHook(t *testing.T, repoDir string, env []string) (string, int) {
	t.Helper()
	script := filepath.Join(projectRoot(t), rebuildScript)
	return runBashSnippet(t, fmt.Sprintf("cd %q && bash %q", repoDir, script), env)
}

func TestHookSkipsCommitsThatDoNotTouchTheTui(t *testing.T) {
	repoDir, _, markDir, env := setupHookRepo(t)
	writeTempFile(t, repoDir, "lib/foo.sh", "# edited bash lib\n")
	gitIn(t, repoDir, "add", "-A")
	commitIn(t, repoDir, "bash-only change")

	out, code := runHook(t, repoDir, env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(markDir, "go.log")); err == nil {
		t.Fatalf("bash-only commit must not trigger a build; output:\n%s", out)
	}
}

func TestHookRebuildsInstallsSignsAndWarmsOnGoChange(t *testing.T) {
	repoDir, homeDir, markDir, env := setupHookRepo(t)
	writeTempFile(t, repoDir, "internal/tui/new.go", "package tui\n")
	gitIn(t, repoDir, "add", "-A")
	commitIn(t, repoDir, "tui change")

	out, code := runHook(t, repoDir, env)
	assertExitCode(t, code, 0)

	goLog, err := os.ReadFile(filepath.Join(markDir, "go.log"))
	if err != nil {
		t.Fatalf("go build never ran; output:\n%s", out)
	}
	if !strings.Contains(string(goLog), "./cmd/wisp-deck-tui") {
		t.Fatalf("go build must target ./cmd/wisp-deck-tui, got:\n%s", goLog)
	}
	if !strings.Contains(string(goLog), "main.Version=9.9.9") {
		t.Fatalf("go build must stamp the VERSION file, got:\n%s", goLog)
	}

	installed := filepath.Join(homeDir, ".local", "bin", "wisp-deck-tui")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("binary not installed at %s; output:\n%s", installed, out)
	}

	signLog, err := os.ReadFile(filepath.Join(markDir, "codesign.log"))
	if err != nil || !strings.Contains(string(signLog), installed) {
		t.Fatalf("installed binary must be re-signed; log err %v, log:\n%s", err, signLog)
	}

	if _, err := os.Stat(filepath.Join(markDir, "warm.log")); err != nil {
		t.Fatalf("installed binary was never warmed (Gatekeeper first-run cost); output:\n%s", out)
	}
}

func TestHookBuildsFromHeadNotTheDirtyWorktree(t *testing.T) {
	repoDir, _, markDir, env := setupHookRepo(t)
	writeTempFile(t, repoDir, "internal/probe.txt", "clean\n")
	writeTempFile(t, repoDir, "internal/tui/new.go", "package tui\n")
	gitIn(t, repoDir, "add", "-A")
	commitIn(t, repoDir, "tui change")
	// Another concurrent session dirties the shared checkout after the commit.
	writeTempFile(t, repoDir, "internal/probe.txt", "dirty\n")

	out, code := runHook(t, repoDir, env)
	assertExitCode(t, code, 0)

	seen, err := os.ReadFile(filepath.Join(markDir, "probe-seen.txt"))
	if err != nil {
		t.Fatalf("build never saw the source tree; output:\n%s", out)
	}
	if strings.TrimSpace(string(seen)) != "clean" {
		t.Fatalf("build must use `git archive HEAD`, not the dirty worktree; saw %q", seen)
	}
}

func TestHookIsWiredIntoGithooks(t *testing.T) {
	root := projectRoot(t)
	hook := filepath.Join(root, ".githooks", "post-commit")
	info, err := os.Stat(hook)
	if err != nil {
		t.Fatalf(".githooks/post-commit missing: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal(".githooks/post-commit is not executable")
	}
	content, err := os.ReadFile(hook)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), rebuildScript) {
		t.Fatalf(".githooks/post-commit must invoke %s, got:\n%s", rebuildScript, content)
	}
}
