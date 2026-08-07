package bash_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tmux runs every `run-shell` command with /bin/sh -- NOT with default-shell,
// and not with bash. On macOS /bin/sh is bash in POSIX sh mode, where process
// substitution (`done < <(...)`) is a syntax error, so a bash-only lib sourced
// straight from a bind fails to parse and the whole payload exits 1 before the
// handler ever runs. That is what the user saw on every [ + ] click:
//
//	'. "…/lib/spare-tabs.sh" && spare_tabs_dispatch "gtspare_…" "new"' returned 1
//
// So the payload a status click runs must work under /bin/sh, exactly as tmux
// will run it.
func TestSpareTabs_statusClickPayloadRunsUnderSh(t *testing.T) {
	root := projectRoot(t)
	lib := filepath.Join(root, "lib/spare-tabs.sh")

	out, code := runBashFunc(t, "lib/spare-tabs.sh", "spare_tabs_config",
		[]string{"wisp-deck", "/proj/dir", lib, "gtspare_x"}, nil)
	assertExitCode(t, code, 0)

	for _, bind := range []string{
		"MouseDown1Status",
		"MouseDown1StatusLeft",
		"MouseDown1StatusRight",
	} {
		t.Run(bind, func(t *testing.T) {
			payload := runShellPayload(t, out, bind)
			payload = strings.ReplaceAll(payload, "#{mouse_status_range}", "new")

			dir := t.TempDir()
			rec := filepath.Join(dir, "rec")
			binDir := mockCommand(t, dir, "tmux", spareTabsMockTmux)
			env := buildEnv(t, []string{binDir}, "GT_REC="+rec, "GT_WINCOUNT=2")

			// /bin/sh -c "<payload>" is precisely how tmux runs a bind.
			stdout, code := runShWithEnv(t, payload, env)
			if code != 0 {
				t.Fatalf("payload exited %d under /bin/sh (a click would do nothing)\npayload: %s\noutput: %s",
					code, payload, stdout)
			}
			data, _ := os.ReadFile(rec)
			assertContains(t, string(data), "new-window")
		})
	}
}

// The guard for the whole class: tmux's /bin/sh cannot parse this project's
// bash libs, so no run-shell payload may source one directly -- it must go
// through `bash -c`, the pattern every wrapper.sh bind already uses.
func TestRunShellPayloads_sourceLibsThroughBash(t *testing.T) {
	root := projectRoot(t)
	files := []string{filepath.Join(root, "wrapper.sh")}
	libs, err := filepath.Glob(filepath.Join(root, "lib", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, libs...)

	sourcing := regexp.MustCompile(`(^|[^-\w])(source|\.)\s+\\?"`)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			idx := strings.Index(line, "run-shell")
			if idx < 0 {
				continue
			}
			rest := line[idx:]
			if !sourcing.MatchString(rest) || strings.Contains(rest, "bash -c") {
				continue
			}
			t.Errorf("%s:%d sources a lib inside run-shell without `bash -c`; "+
				"tmux runs it with /bin/sh, which cannot parse this project's bash:\n  %s",
				filepath.Base(f), i+1, strings.TrimSpace(line))
		}
	}
}

// runShWithEnv runs a command with /bin/sh -c, the interpreter tmux uses for
// every run-shell payload.
func runShWithEnv(t *testing.T, script string, env []string) (string, int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	if env == nil {
		env = buildEnv(t, nil)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("failed to run sh: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return string(out), code
}

// runShellPayload pulls the shell command out of a generated tmux `bind ... key
// run-shell "<payload>"` line, undoing tmux's double-quote escaping the same
// way tmux's own parser does.
func runShellPayload(t *testing.T, config, key string) string {
	t.Helper()
	for _, line := range strings.Split(config, "\n") {
		if !strings.Contains(line, " "+key+" ") {
			continue
		}
		i := strings.Index(line, "run-shell ")
		if i < 0 {
			continue
		}
		arg := strings.TrimSpace(line[i+len("run-shell "):])
		arg = strings.TrimPrefix(arg, `"`)
		arg = strings.TrimSuffix(arg, `"`)
		return strings.ReplaceAll(arg, `\"`, `"`)
	}
	t.Fatalf("no run-shell bind for %s in config:\n%s", key, config)
	return ""
}
