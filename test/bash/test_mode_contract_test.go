package bash_test

import (
	"fmt"
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
	requireSourceText(t, "run-tests.sh", runTests,
		`exec "$SCRIPT_DIR/scripts/go-test.sh" ./test/bash/... ./test/internal/... ./test/npx/... ./internal/... "$@"`)

	makefile := repositorySource(t, "Makefile")
	requireSourceText(t, "Makefile", makefile,
		"\tWISP_DECK_TESTING=1 ./scripts/go-test.sh ./...")
	requireSourceText(t, "Makefile", makefile,
		"\tWISP_DECK_TESTING=1 ./scripts/go-test.sh -v ./...")
	if got := strings.Count(makefile, "\tWISP_DECK_TESTING=1 ./run-tests.sh"); got != 2 {
		t.Errorf("Makefile marked run-tests.sh commands = %d, want 2", got)
	}

	testsWorkflow := repositorySource(t, ".github", "workflows", "tests.yml")
	installWorkflow := repositorySource(t, ".github", "workflows", "install.yml")
	requireWorkflowJobsMarked(t, ".github/workflows/tests.yml", testsWorkflow)
	requireWorkflowJobsMarked(t, ".github/workflows/install.yml", installWorkflow)

	for _, file := range []string{"test/bash/main_test.go", "test/npx/main_test.go"} {
		parts := strings.Split(file, "/")
		source := repositorySource(t, parts...)
		requireSourceText(t, file, source,
			`const repositoryTestArgv0 = "__WISP_DECK_REPOSITORY_TEST_V1__.test"`)
		requireSourceText(t, file, source,
			`if os.Getenv("WISP_DECK_TESTING") != "1" ||`)
		requireSourceText(t, file, source, "len(os.Args) == 0 ||")
		requireSourceText(t, file, source, "os.Args[0] != repositoryTestArgv0 {")
		requireSourceText(t, file, source,
			"environment := repositoryTestEnvironment(os.Environ())")
		requireSourceText(t, file, source,
			"arguments := repositoryTestArguments(os.Args)")
		requireSourceText(t, file, source,
			"syscall.Exec(executable, arguments, environment)")
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
	requireSourceText(t, "test/bash/helpers_test.go", bashHelpers,
		"func repositoryTestArguments(arguments []string) []string")

	npxHelpers := repositorySource(t, "test", "npx", "helpers_test.go")
	requireSourceText(t, "test/npx/helpers_test.go", npxHelpers,
		"cmd.Env = repositoryTestEnvironment(env)")
	requireSourceText(t, "test/npx/helpers_test.go", npxHelpers,
		"func repositoryTestArguments(arguments []string) []string")

	installE2E := repositorySource(t, "test", "npx", "install_e2e_test.go")
	requireSourceText(t, "test/npx/install_e2e_test.go", installE2E,
		"cmd.Env = repositoryTestEnvironment(append(append([]string{}, s.env...), extraEnv...))")

	entrypoints := map[string]string{
		"scripts/go-test.sh":            repositorySource(t, "scripts", "go-test.sh"),
		"run-tests.sh":                  runTests,
		"Makefile":                      makefile,
		".github/workflows/tests.yml":   testsWorkflow,
		".github/workflows/install.yml": installWorkflow,
		"scripts/release.sh":            repositorySource(t, "scripts", "release.sh"),
	}
	driverInfo, err := os.Stat(filepath.Join(projectRoot(t), "scripts", "go-test.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if driverInfo.Mode()&0o111 == 0 {
		t.Fatal("scripts/go-test.sh is not executable")
	}
	if err := validateRepositoryGoTestEntrypoints(entrypoints); err != nil {
		t.Fatal(err)
	}

	mutations := map[string]map[string]string{
		"changed sentinel": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"scripts/go-test.sh",
			"__WISP_DECK_REPOSITORY_TEST_V1__.test",
			"__WISP_DECK_REPOSITORY_TEST_V2__.test",
		),
		"raw Make go test": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"Makefile",
			"./scripts/go-test.sh ./...",
			"go test ./...",
		),
		"raw run-tests go test": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"run-tests.sh",
			`exec "$SCRIPT_DIR/scripts/go-test.sh"`,
			"go test",
		),
		"raw tests workflow go test": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			".github/workflows/tests.yml",
			"./scripts/go-test.sh -json ./internal/...",
			"go test -json ./internal/...",
		),
		"raw install workflow go test": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			".github/workflows/install.yml",
			"./scripts/go-test.sh -json ./test/npx/... -count=1",
			"go test -json ./test/npx/... -count=1",
		),
		"raw release go test": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"scripts/release.sh",
			"./scripts/go-test.sh ./test/npx/...",
			"go test ./test/npx/...",
		),
	}
	for name, sources := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := validateRepositoryGoTestEntrypoints(sources); err == nil {
				t.Fatal("unsafe direct go-test entrypoint passed inventory validation")
			}
		})
	}
}

func mutateRepositoryEntrypoint(
	t *testing.T,
	sources map[string]string,
	file string,
	old string,
	replacement string,
) map[string]string {
	t.Helper()
	if strings.Count(sources[file], old) != 1 {
		t.Fatalf(
			"%s mutation prerequisite %q occurs %d times, want exactly one",
			file,
			old,
			strings.Count(sources[file], old),
		)
	}
	mutated := make(map[string]string, len(sources))
	for name, source := range sources {
		mutated[name] = source
	}
	mutated[file] = strings.Replace(sources[file], old, replacement, 1)
	return mutated
}

func validateRepositoryGoTestEntrypoints(sources map[string]string) error {
	driver := sources["scripts/go-test.sh"]
	for _, required := range []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"export WISP_DECK_TESTING=1",
		`exec -a '__WISP_DECK_REPOSITORY_TEST_V1__.test' go test "$@"`,
	} {
		if !strings.Contains(driver, required) {
			return fmt.Errorf("scripts/go-test.sh is missing %q", required)
		}
	}

	requiredRoutes := map[string]int{
		"run-tests.sh":                  1,
		"Makefile":                      2,
		".github/workflows/tests.yml":   2,
		".github/workflows/install.yml": 2,
		"scripts/release.sh":            1,
	}
	for file, want := range requiredRoutes {
		source := sources[file]
		if got := strings.Count(source, "scripts/go-test.sh"); got != want {
			return fmt.Errorf(
				"%s go-test driver routes = %d, want exactly %d",
				file,
				got,
				want,
			)
		}
		for lineNumber, line := range strings.Split(source, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.Contains(trimmed, "go test") {
				return fmt.Errorf(
					"%s:%d contains executable raw go test",
					file,
					lineNumber+1,
				)
			}
		}
	}
	return nil
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
