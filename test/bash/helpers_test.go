package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// projectRoot returns the absolute path to the repository root.
func projectRoot(t *testing.T) string {
	t.Helper()
	// This file is at test/bash/helpers_test.go, so root is ../..
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine project root")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

// runBashFunc sources a lib module and calls a function, returning stdout and exit code.
// The module path is relative to the project root (e.g., "lib/process.sh").
// Environment variables can be passed via env parameter (nil for default).
func runBashFunc(t *testing.T, module string, funcName string, args []string, env []string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	modulePath := filepath.Join(root, module)

	// Build bash command: source module, call function with args
	quotedArgs := make([]string, len(args))
	for i, a := range args {
		quotedArgs[i] = fmt.Sprintf("%q", a)
	}
	script := fmt.Sprintf("source %q && %s %s", modulePath, funcName, strings.Join(quotedArgs, " "))

	cmd := exec.Command("bash", "-c", script)
	if env != nil {
		cmd.Env = env
	}

	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run bash: %v", err)
		}
	}
	return string(out), code
}

// runBashFuncWithStdin is like runBashFunc but pipes stdin to the bash process.
func runBashFuncWithStdin(t *testing.T, module string, funcName string, args []string, env []string, stdin string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	modulePath := filepath.Join(root, module)

	quotedArgs := make([]string, len(args))
	for i, a := range args {
		quotedArgs[i] = fmt.Sprintf("%q", a)
	}
	script := fmt.Sprintf("source %q && %s %s", modulePath, funcName, strings.Join(quotedArgs, " "))

	cmd := exec.Command("bash", "-c", script)
	if env != nil {
		cmd.Env = env
	}
	cmd.Stdin = strings.NewReader(stdin)

	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run bash: %v", err)
		}
	}
	return string(out), code
}

// runBashScript executes a script directly, returning stdout and exit code.
// The scriptPath is relative to the project root.
func runBashScript(t *testing.T, scriptPath string, args []string, env []string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	fullPath := filepath.Join(root, scriptPath)

	cmd := exec.Command("bash", append([]string{fullPath}, args...)...)
	if env != nil {
		cmd.Env = env
	}

	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run script: %v", err)
		}
	}
	return string(out), code
}

// runBashSnippet runs an arbitrary bash snippet, returning stdout and exit code.
// Useful for sourcing multiple modules or complex setups.
func runBashSnippet(t *testing.T, script string, env []string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	if env != nil {
		cmd.Env = env
	}

	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run snippet: %v", err)
		}
	}
	return string(out), code
}

// mockCommand creates a mock executable script in dir/bin/ that runs the given bash body.
// Returns the bin directory path (caller should prepend to PATH).
// Example: mockCommand(t, dir, "brew", `echo "already installed"`)
func mockCommand(t *testing.T, dir string, name string, body string) string {
	t.Helper()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}
	script := fmt.Sprintf("#!/bin/bash\n%s\n", body)
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create mock %s: %v", name, err)
	}
	return binDir
}

// writeTempFile creates a file with given content in dir, returning the full path.
func writeTempFile(t *testing.T, dir string, name string, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// Create parent dirs if needed
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create parent dirs for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
	return path
}

// buildEnv creates an environment slice with PATH prepended with mockDirs.
// Inherits current environment and overrides/adds the provided key=value pairs.
//
// Ghost Tab runtime vars (GHOST_TAB_*) are stripped from the inherited base so
// tests are isolated from the surrounding session: running the suite from
// inside a live Ghost Tab tab would otherwise leak e.g. GHOST_TAB_CLAUDE_FILTER
// or GHOST_TAB_RESUME into the bash under test. Tests that need such a var set
// it explicitly via extra (re-added after the strip).
func buildEnv(t *testing.T, mockDirs []string, extra ...string) []string {
	t.Helper()
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GHOST_TAB") {
			continue
		}
		env = append(env, e)
	}

	// Build new PATH
	if len(mockDirs) > 0 {
		currentPath := os.Getenv("PATH")
		newPath := strings.Join(mockDirs, ":") + ":" + currentPath
		// Replace existing PATH
		found := false
		for i, e := range env {
			if strings.HasPrefix(e, "PATH=") {
				env[i] = "PATH=" + newPath
				found = true
				break
			}
		}
		if !found {
			env = append(env, "PATH="+newPath)
		}
	}

	// Add extra env vars
	for _, e := range extra {
		key := strings.SplitN(e, "=", 2)[0]
		found := false
		for i, existing := range env {
			if strings.HasPrefix(existing, key+"=") {
				env[i] = e
				found = true
				break
			}
		}
		if !found {
			env = append(env, e)
		}
	}

	return env
}

// assertContains checks that output contains the expected substring.
func assertContains(t *testing.T, output, expected string) {
	t.Helper()
	if !strings.Contains(output, expected) {
		t.Errorf("expected output to contain %q, got:\n%s", expected, output)
	}
}

// assertNotContains checks that output does NOT contain the substring.
func assertNotContains(t *testing.T, output, unexpected string) {
	t.Helper()
	if strings.Contains(output, unexpected) {
		t.Errorf("expected output NOT to contain %q, got:\n%s", unexpected, output)
	}
}

// assertExitCode checks the exit code matches expected.
func assertExitCode(t *testing.T, got, expected int) {
	t.Helper()
	if got != expected {
		t.Errorf("expected exit code %d, got %d", expected, got)
	}
}

// --- Smoke tests for the helpers themselves ---

func TestHelpers_projectRoot(t *testing.T) {
	root := projectRoot(t)
	// Should contain go.mod
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Errorf("projectRoot() = %q, expected to contain go.mod", root)
	}
}

func TestHelpers_runBashFunc_sources_and_calls(t *testing.T) {
	// Test with a real function from lib/projects.sh
	out, code := runBashFunc(t, "lib/projects.sh", "path_expand", []string{"~/test"}, nil)
	assertExitCode(t, code, 0)
	home := os.Getenv("HOME")
	expected := home + "/test"
	if strings.TrimSpace(out) != expected {
		t.Errorf("path_expand(~/test) = %q, want %q", strings.TrimSpace(out), expected)
	}
}

func TestHelpers_mockCommand(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "fakecmd", `echo "hello from mock"`)

	cmd := exec.Command(filepath.Join(binDir, "fakecmd"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("mock command failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello from mock" {
		t.Errorf("mock output = %q, want %q", string(out), "hello from mock")
	}
}

func TestHelpers_writeTempFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "sub/dir/file.txt", "content here")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read temp file: %v", err)
	}
	if string(data) != "content here" {
		t.Errorf("file content = %q, want %q", string(data), "content here")
	}
}

func TestHelpers_buildEnv_prepends_path(t *testing.T) {
	env := buildEnv(t, []string{"/mock/bin"})
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			if !strings.HasPrefix(e, "PATH=/mock/bin:") {
				t.Errorf("PATH should start with /mock/bin:, got %s", e)
			}
			return
		}
	}
	t.Error("PATH not found in env")
}
