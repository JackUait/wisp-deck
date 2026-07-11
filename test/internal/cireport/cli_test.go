package cireport_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI builds and runs cmd/ci-report the way the workflows do.
func runCLI(t *testing.T, stream string, args ...string) (stdout string, summary string, code int) {
	t.Helper()

	dir := t.TempDir()
	input := filepath.Join(dir, "test-output.json")
	if err := os.WriteFile(input, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}
	summaryPath := filepath.Join(dir, "summary.md")

	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}

	argv := append([]string{"run", "./cmd/ci-report"}, args...)
	argv = append(argv, input)
	cmd := exec.Command("go", argv...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GITHUB_STEP_SUMMARY="+summaryPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("running ci-report: %v\n%s", err, out)
		}
	}
	b, _ := os.ReadFile(summaryPath)
	return string(out), string(b), code
}

func TestCLI_fails_and_explains_which_test_broke(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"output","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestRestore","Output":"    restore_test.go:31: pane layout not replayed\n"}`,
		`{"Action":"fail","Package":"github.com/jackuait/wisp-deck/test/bash","Test":"TestRestore"}`,
		`{"Action":"fail","Package":"github.com/jackuait/wisp-deck/test/bash"}`,
	}, "\n")

	out, summary, code := runCLI(t, stream, "--title", "Bash tests")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "TestRestore") || !strings.Contains(out, "pane layout not replayed") {
		t.Errorf("CI log must name the test and why it failed:\n%s", out)
	}
	if !strings.Contains(out, "::error ") {
		t.Errorf("CI log must emit a GitHub annotation:\n%s", out)
	}
	if !strings.Contains(summary, "Bash tests") || !strings.Contains(summary, "TestRestore") {
		t.Errorf("step summary must name the job and the failing test:\n%s", summary)
	}
}

func TestCLI_passes_quietly_when_all_tests_pass(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"pass","Package":"p","Test":"TestA"}`,
		`{"Action":"pass","Package":"p"}`,
	}, "\n")

	out, summary, code := runCLI(t, stream, "--title", "Go tests")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "::error") {
		t.Errorf("a passing run must not emit error annotations:\n%s", out)
	}
	if !strings.Contains(summary, "passed") {
		t.Errorf("step summary should confirm the pass:\n%s", summary)
	}
}

func TestCLI_fails_loudly_when_the_output_file_is_missing(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "./cmd/ci-report", "--title", "Go tests", "/nonexistent/nope.json")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("a missing test-output file must fail the job, got success:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(string(out)), "no test output") &&
		!strings.Contains(strings.ToLower(string(out)), "could not read") {
		t.Errorf("the failure must explain itself in English:\n%s", out)
	}
}
