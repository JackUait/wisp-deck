package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// launchSiteRE matches a pane started from a built command: "$cmd; exec bash"
// or "$cmd$suffix; exec bash". When $cmd is empty, tmux runs "; exec bash", a
// syntax error that kills the pane and the agent in it.
var launchSiteRE = regexp.MustCompile(`"\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?[^"]*; exec bash"`)

// Every launch site must test its command for emptiness earlier in the same
// function (or, at top level, earlier in the file). A builder that fails
// prints nothing, and `cmd="$(builder)"` does not stop on that.
func TestEveryPaneLaunchRefusesAnEmptyCommand(t *testing.T) {
	root := projectRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "lib", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join(root, "wrapper.sh"))
	funcStart := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\(\)\s*\{`)
	sites := 0
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(body), "\n")
		start := 0
		for i, line := range lines {
			if funcStart.MatchString(line) {
				start = i
			}
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			match := launchSiteRE.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			sites++
			name := match[1]
			guard := regexp.MustCompile(`-[nz] "\$\{?` + name + `\}?"`)
			if !guard.MatchString(strings.Join(lines[start:i], "\n")) {
				t.Errorf("%s:%d launches \"$%s; exec bash\" without first testing $%s for emptiness",
					filepath.Base(file), i+1, name, name)
			}
		}
	}
	// The scan must keep seeing the real sites, or it guards nothing.
	if sites < 5 {
		t.Fatalf("found %d launch sites; the pattern no longer matches the code", sites)
	}
}
