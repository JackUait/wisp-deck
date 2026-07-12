package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The compact-view pane — and the wrapper it runs under — execute these bash
// modules with the user's $SHELL, which is ZSH. In zsh a handful of lowercase
// parameter names are SPECIAL: `path` is an array bound to $PATH, `status` is
// $?, `cdpath`/`fpath`/`manpath`/… are search-path arrays, and so on. A bash
// `local path="$1"` (or a bare `path=…`, a `for path in …`, a `read … path`)
// silently REBINDS that special, so a later `wc`/`git`/`cd` inside the same
// scope breaks with no error. This exact bug shipped: format_image_row's
// `local path` clobbered $PATH under the pane's zsh and every image's size
// collapsed to "±0" (see TestFormatImageRow_zsh_does_not_clobber_path).
//
// Naming a variable after a zsh special is never necessary and always a latent
// landmine here, because any lib module can be sourced into the zsh pane. This
// guard forbids it across every shell script so the whole class stays dead.
func TestNoZshSpecialParameterNames(t *testing.T) {
	// Lowercase zsh special parameters that are realistic variable names AND
	// cause silent breakage when shadowed. Uppercase env vars (PATH, IFS, …) are
	// special in bash too and already avoided; this set is the zsh-only trap.
	forbidden := []string{
		"path", "cdpath", "fpath", "mailpath", "manpath", "module_path",
		"psvar", "watch", "fignore", "status", "argv", "pipestatus",
		"options", "commands", "functions", "aliases", "parameters",
		"dirstack", "signals", "funcstack", "nameddirs", "userdirs",
		"jobstates", "jobtexts", "jobdirs", "reswords", "histchars",
	}

	root := projectRoot(t)
	var files []string
	libDir := filepath.Join(root, "lib")
	_ = filepath.Walk(libDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".sh") {
			files = append(files, p)
		}
		return nil
	})
	files = append(files, filepath.Join(root, "wrapper.sh"))

	// Per forbidden name, the four ways a bash script can bind it. \b treats '_'
	// as a word char, so \bpath\b never matches fpath/module_path/WISP_DECK_PATH.
	type check struct{ kind string; re *regexp.Regexp }
	checksFor := func(name string) []check {
		return []check{
			// local/typeset/declare/export/readonly … name (=… | end)
			{"declared as local", regexp.MustCompile(`\b(?:local|typeset|declare|export|readonly)\b(?:\s+-{1,2}[A-Za-z]+)*(?:\s+[A-Za-z_][\w]*(?:=\S*)?)*\s+` + name + `\b`)},
			// bare assignment at the start of a statement:  name=…
			{"assigned to", regexp.MustCompile(`(?:^|[;&|{(]|\bthen\b|\bdo\b|\belse\b)\s*` + name + `=`)},
			// for name in …
			{"used as a for-loop var", regexp.MustCompile(`\bfor\s+` + name + `\b`)},
			// read [-flags] … name  (before any redirection / terminator)
			{"read into", regexp.MustCompile(`\bread\b[^<>;|#&]*\s` + name + `(?:\s|$|;)`)},
		}
	}

	// stripComment drops a trailing `# …` comment while leaving `#` inside a
	// single/double-quoted string alone, so the doc-comment "`local path=…`" in a
	// module never trips the scan.
	stripComment := func(line string) string {
		var q byte
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch {
			case q != 0:
				if c == q {
					q = 0
				}
			case c == '\'' || c == '"':
				q = c
			case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
				return line[:i]
			}
		}
		return line
	}

	var violations []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue // wrapper.sh always exists; a missing optional file is not a failure
		}
		rel, _ := filepath.Rel(root, f)
		for i, raw := range strings.Split(string(data), "\n") {
			code := stripComment(raw)
			if strings.TrimSpace(code) == "" {
				continue
			}
			for _, name := range forbidden {
				if !strings.Contains(code, name) {
					continue
				}
				for _, c := range checksFor(name) {
					if c.re.MatchString(code) {
						violations = append(violations,
							rel+":"+strconv.Itoa(i+1)+"  "+name+" "+c.kind+"  ->  "+strings.TrimSpace(raw))
						break
					}
				}
			}
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("shell variable(s) named after a zsh special parameter (they silently "+
			"rebind $PATH/$?/search-paths when the module is sourced into the zsh pane — "+
			"rename them, e.g. path->filepath):\n  %s", strings.Join(violations, "\n  "))
	}
}
