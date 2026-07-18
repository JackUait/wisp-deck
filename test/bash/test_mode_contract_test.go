package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repositorySource(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{projectRoot(t)}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(parts...), err)
	}
	return string(data)
}

func requireSourceText(t *testing.T, file, source, want string) {
	t.Helper()
	if !strings.Contains(source, want) {
		t.Errorf("%s is missing exact test-mode contract %q", file, want)
	}
}

func requireWorkflowJobsMarked(t *testing.T, file, source string) {
	t.Helper()
	lines := strings.Split(source, "\n")
	jobs := 0
	inJobs := false
	for i, line := range lines {
		if line == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if !strings.HasPrefix(line, "  ") ||
			strings.HasPrefix(line, "    ") ||
			!strings.HasSuffix(line, ":") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimSpace(line), ":")
		if name == "" {
			continue
		}
		jobs++
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "  ") &&
				!strings.HasPrefix(lines[j], "    ") &&
				strings.HasSuffix(lines[j], ":") {
				end = j
				break
			}
		}
		block := strings.Join(lines[i:end], "\n")
		requireSourceText(t, file+" job "+name, block,
			"    env:\n      WISP_DECK_TESTING: \"1\"")
	}
	if jobs == 0 {
		t.Fatalf("%s has no jobs to inspect", file)
	}
}

func TestRepositoryTestEntrypointsPropagateMode(t *testing.T) {
	runTests := repositorySource(t, "run-tests.sh")
	requireSourceText(t, "run-tests.sh", runTests, "export WISP_DECK_TESTING=1")

	makefile := repositorySource(t, "Makefile")
	requireSourceText(t, "Makefile", makefile, "\tWISP_DECK_TESTING=1 go test ./...")
	requireSourceText(t, "Makefile", makefile, "\tWISP_DECK_TESTING=1 go test -v ./...")
	if got := strings.Count(makefile, "\tWISP_DECK_TESTING=1 ./run-tests.sh"); got != 2 {
		t.Errorf("Makefile marked run-tests.sh commands = %d, want 2", got)
	}

	requireWorkflowJobsMarked(t, ".github/workflows/tests.yml",
		repositorySource(t, ".github", "workflows", "tests.yml"))
	requireWorkflowJobsMarked(t, ".github/workflows/install.yml",
		repositorySource(t, ".github", "workflows", "install.yml"))

	for _, file := range []string{"test/bash/main_test.go", "test/npx/main_test.go"} {
		parts := strings.Split(file, "/")
		source := repositorySource(t, parts...)
		requireSourceText(t, file, source, `if os.Getenv("WISP_DECK_TESTING") != "1" {`)
		requireSourceText(t, file, source,
			"environment := repositoryTestEnvironment(os.Environ())")
		requireSourceText(t, file, source,
			"syscall.Exec(executable, os.Args, environment)")
		if strings.Contains(source, "os.Setenv(") {
			t.Errorf("%s mutates test mode in process instead of re-execing", file)
		}
		if got := strings.Count(source, "syscall.Exec("); got != 1 {
			t.Errorf("%s syscall.Exec calls = %d, want exactly 1", file, got)
		}
	}

	bashHelpers := repositorySource(t, "test", "bash", "helpers_test.go")
	requireSourceText(t, "test/bash/helpers_test.go", bashHelpers,
		"return repositoryTestEnvironment(env)")

	npxHelpers := repositorySource(t, "test", "npx", "helpers_test.go")
	requireSourceText(t, "test/npx/helpers_test.go", npxHelpers,
		"cmd.Env = repositoryTestEnvironment(env)")

	installE2E := repositorySource(t, "test", "npx", "install_e2e_test.go")
	requireSourceText(t, "test/npx/install_e2e_test.go", installE2E,
		"cmd.Env = repositoryTestEnvironment(append(append([]string{}, s.env...), extraEnv...))")
}

func TestNotificationTestModeGuardSource(t *testing.T) {
	source := repositorySource(t, "lib", "notification-setup.sh")
	const declaration = "play_notification_sound() {"
	start := strings.Index(source, declaration)
	if start < 0 {
		t.Fatal("lib/notification-setup.sh is missing play_notification_sound")
	}

	for _, line := range strings.Split(source[start+len(declaration):], "\n") {
		statement := strings.TrimSpace(line)
		if statement == "" || strings.HasPrefix(statement, "#") {
			continue
		}
		const want = `[[ "${WISP_DECK_TESTING:-}" == "1" ]] && return 0`
		if statement != want {
			t.Fatalf("first play_notification_sound statement = %q, want %q", statement, want)
		}
		return
	}
	t.Fatal("play_notification_sound has no executable statement")
}
