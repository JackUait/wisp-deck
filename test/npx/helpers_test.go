package npx_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// projectRoot returns the absolute path to the wisp-deck repo root.
func projectRoot(t *testing.T) string {
	t.Helper()
	// test/npx/ is two levels below root
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..")
}

// runLauncher executes the npx launcher with the given env overrides.
// Returns stdout, stderr, and exit code.
func runLauncher(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	root := projectRoot(t)
	launcher := filepath.Join(root, "bin", "npx-wisp-deck.js")
	cmdArgs := append([]string{launcher}, args...)
	cmd := exec.Command("node", cmdArgs...)
	cmd.Env = repositoryTestEnvironment(env)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run launcher: %v", err)
		}
	}
	return stdout.String(), stderr.String(), code
}

func repositoryTestEnvironment(env []string) []string {
	normalized := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, "WISP_DECK_TESTING=") {
			continue
		}
		normalized = append(normalized, entry)
	}
	return append(normalized, "WISP_DECK_TESTING=1")
}

func TestRepositoryTestEnvironment(t *testing.T) {
	input := []string{
		"HOME=/tmp/home",
		"WISP_DECK_TESTING=0",
		"PATH=/usr/bin",
		"WISP_DECK_TESTING=stale",
	}
	original := append([]string(nil), input...)

	got := repositoryTestEnvironment(input)

	if !reflect.DeepEqual(input, original) {
		t.Fatalf("repositoryTestEnvironment mutated input: got %v, want %v", input, original)
	}
	want := []string{"HOME=/tmp/home", "PATH=/usr/bin", "WISP_DECK_TESTING=1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repositoryTestEnvironment() = %v, want %v", got, want)
	}
}
