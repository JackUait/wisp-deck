package bash_test

// A dependency that fails to install must fail the install. jq and tmux are
// both load-bearing at runtime (the statusline and settings code shell out to
// jq; the whole session is a tmux session), so an install that silently
// continues without one produces a wisp-deck that breaks later, far from the
// cause.

import (
	"os"
	"path/filepath"
	"testing"
)

// restrictedDepsEnv builds a PATH with only the mock dir plus /bin, so neither
// a real jq nor a real tmux is reachable and the ensure_* functions must
// actually install them.
func restrictedDepsEnv(t *testing.T, dir, binDir string) (string, []string) {
	t.Helper()
	fakeHome := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(fakeHome, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkUsrBinTools(t, binDir, "grep", "sed", "tr", "mktemp", "tar", "unzip")
	return fakeHome, buildEnv(t, nil, "HOME="+fakeHome, "PATH="+binDir+":/bin")
}

func TestEnsureBaseRequirements_fails_when_jq_install_fails(t *testing.T) {
	dir := t.TempDir()
	// jq's download fails; tmux's succeeds. The overall result must still be a
	// failure — otherwise setup proceeds with no jq and breaks at runtime.
	binDir := mockCommand(t, dir, "curl", `
dest=""; prev=""
for a in "$@"; do [ "$prev" = "-o" ] && dest="$a"; prev="$a"; done
if [ "$1" = "-fsSI" ]; then printf "location: https://github.com/tmux/tmux-builds/releases/tag/v3.5\r\n"; exit 0; fi
case "$dest" in
  *jq*) exit 1 ;;
esac
printf 'tmuxbin' > "$dest"
exit 0
`)
	mockCommand(t, dir, "uname", `echo "arm64"`)
	// tar "extracts" a working tmux so only jq is the failure.
	mockCommand(t, dir, "tar", `
d=""; prev=""
for a in "$@"; do [ "$prev" = "-C" ] && d="$a"; prev="$a"; done
printf '#!/bin/bash\necho tmux 3.5\n' > "$d/tmux"
chmod +x "$d/tmux"
`)
	_, env := restrictedDepsEnv(t, dir, binDir)

	snippet := installSnippet(t, `ensure_base_requirements`)
	out, code := runBashSnippet(t, snippet, env)
	if code == 0 {
		t.Errorf("ensure_base_requirements returned 0 despite jq failing to install; setup would continue without jq.\noutput:\n%s", out)
	}
}

func TestEnsureBaseRequirements_fails_when_tmux_install_fails(t *testing.T) {
	dir := t.TempDir()
	// jq installs fine; tmux's download fails.
	binDir := mockCommand(t, dir, "curl", `
dest=""; prev=""
for a in "$@"; do [ "$prev" = "-o" ] && dest="$a"; prev="$a"; done
if [ "$1" = "-fsSI" ]; then printf "location: https://github.com/tmux/tmux-builds/releases/tag/v3.5\r\n"; exit 0; fi
case "$dest" in
  *tmux*) exit 1 ;;
esac
printf '#!/bin/bash\necho jq-1.7.1\n' > "$dest"
exit 0
`)
	mockCommand(t, dir, "uname", `echo "arm64"`)
	_, env := restrictedDepsEnv(t, dir, binDir)

	snippet := installSnippet(t, `ensure_base_requirements`)
	out, code := runBashSnippet(t, snippet, env)
	if code == 0 {
		t.Errorf("ensure_base_requirements returned 0 despite tmux failing to install; setup would continue without tmux.\noutput:\n%s", out)
	}
}

func TestEnsureBaseRequirements_succeeds_when_both_install(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "curl", `
dest=""; prev=""
for a in "$@"; do [ "$prev" = "-o" ] && dest="$a"; prev="$a"; done
if [ "$1" = "-fsSI" ]; then printf "location: https://github.com/tmux/tmux-builds/releases/tag/v3.5\r\n"; exit 0; fi
printf '#!/bin/bash\necho ok\n' > "$dest"
exit 0
`)
	mockCommand(t, dir, "uname", `echo "arm64"`)
	mockCommand(t, dir, "tar", `
d=""; prev=""
for a in "$@"; do [ "$prev" = "-C" ] && d="$a"; prev="$a"; done
printf '#!/bin/bash\necho tmux 3.5\n' > "$d/tmux"
chmod +x "$d/tmux"
`)
	fakeHome, env := restrictedDepsEnv(t, dir, binDir)

	snippet := installSnippet(t, `ensure_base_requirements`)
	out, code := runBashSnippet(t, snippet, env)
	assertExitCode(t, code, 0)
	for _, tool := range []string{"jq", "tmux"} {
		if _, err := os.Stat(filepath.Join(fakeHome, ".local", "bin", tool)); err != nil {
			t.Errorf("%s was not installed: %v\noutput:\n%s", tool, err, out)
		}
	}
}
