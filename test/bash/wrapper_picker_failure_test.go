package bash_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A picker that DIES must not look like a picker that was quit. wrapper.sh
// used to treat any nonzero from select_project_interactive as ESC and exit 0
// — so when wisp-deck-tui failed at startup (the fresh-install
// missing-projects-file bug), the window just closed, with the binary's error
// message already discarded by menu-tui.sh. The user watched the terminal
// "not launch" with nothing to read; diagnosing it took an external AI.
//
// The contract now: the failure's message reaches the terminal, and the
// wrapper exits nonzero instead of pretending the user chose to leave.
func TestWrapper_surfaces_picker_failure_instead_of_silent_quit(t *testing.T) {
	root := projectRoot(t)
	home, bin, calls, env := wrapperSandbox(t)

	writeExecutable(t, filepath.Join(bin, "wisp-deck-tui"), "#!/bin/bash\n"+
		"echo \"wisp-deck-tui $*\" >> "+strconv.Quote(calls)+"\n"+
		"case \"$*\" in *main-menu*) echo \"Error: failed to load projects: kaboom\" >&2; exit 1;; esac\n"+
		"exit 0\n")

	outFile := filepath.Join(home, "wrapper.out")
	_, exit := runScriptTimed(t, filepath.Join(root, "wrapper.sh"), env, outFile)

	logged, _ := os.ReadFile(calls)
	if !strings.Contains(string(logged), "main-menu") {
		body, _ := os.ReadFile(outFile)
		t.Fatalf("wrapper.sh never reached the picker.\ncalls:\n%s\noutput:\n%s",
			logged, truncate(string(body), 2000))
	}

	body, _ := os.ReadFile(outFile)
	if !strings.Contains(string(body), "kaboom") {
		t.Errorf("the picker's error never reached the terminal — a startup failure "+
			"is invisible again.\noutput:\n%s", truncate(string(body), 2000))
	}
	if exit == 0 {
		t.Error("wrapper.sh exited 0 for a dead picker — that closes the window as if the user quit")
	}
}
