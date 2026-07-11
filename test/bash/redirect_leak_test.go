package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Bash applies redirections left-to-right, so in
//
//	pid="$(tr -d '[:space:]' < "$f" 2>/dev/null)"
//
// the input redirect is attempted BEFORE stderr is rerouted. When "$f" is
// missing or unreadable the shell's own "No such file or directory" therefore
// goes to the inherited stderr — the 2>/dev/null never applies to it. It only
// ever silences the *command's* stderr, which is not what the author wanted.
//
// This is not a cosmetic bug here. Most of these reads are background
// housekeeping (the keep-awake reaper, the session-restore heartbeat) whose
// stderr is the session terminal, the one the AI tool is painting a full-screen
// UI onto. Files under a shared config dir get deleted by other sessions
// mid-read, so the failure is routine — and it printed
//
//	keep-awake.sh: line 79: .../keep-awake.d/<session>: No such file or directory
//
// straight into Claude's input box.
//
// Write it so stderr is closed before the open is attempted:
//
//	{ pid="$(tr -d '[:space:]' < "$f")"; } 2>/dev/null || pid=""
//
// This is a source lint rather than a behavior test on purpose: the bug is
// invisible until a race fires in production, so the only way it "never comes
// back" is to make the shape itself unrepresentable.
func TestNoRedirectBeforeStderrSuppression(t *testing.T) {
	root := projectRoot(t)

	// `< target ... 2>/dev/null` on one line, with no protecting group. Matches
	// both quoted and bare targets. The safe orderings — `2>/dev/null < "$f"`,
	// or a `{ ...; } 2>/dev/null` wrapper — do not match.
	bad := regexp.MustCompile(`<[[:space:]]*("[^"]+"|\$[A-Za-z_{][^[:space:];)]*)[^;]*2>/dev/null`)

	var files []string
	for _, dir := range []string{"lib", "bin", "test/bash"} {
		filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".sh") || !strings.Contains(filepath.Base(path), ".") {
				files = append(files, path)
			}
			return nil
		})
	}
	files = append(files, filepath.Join(root, "wrapper.sh"))

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			code := line
			if idx := strings.Index(code, "#"); idx >= 0 {
				code = code[:idx] // a comment may legitimately show the bad form
			}
			// `<<` heredocs and `<(` process substitution are not input redirects.
			if strings.Contains(code, "<<") || strings.Contains(code, "<(") {
				continue
			}
			if bad.MatchString(code) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d: input redirect precedes 2>/dev/null, so a missing "+
					"or unreadable file prints to the terminal instead of /dev/null.\n"+
					"    %s\n"+
					"    Wrap it:  { cmd < \"$f\"; } 2>/dev/null || fallback",
					rel, i+1, strings.TrimSpace(line))
			}
		}
	}
}
