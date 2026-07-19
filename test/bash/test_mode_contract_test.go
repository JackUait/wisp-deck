package bash_test

import (
	"fmt"
	"os"
	"os/exec"
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

func repositoryGoTestEntrypointSources(t *testing.T) map[string]string {
	t.Helper()
	root := projectRoot(t)
	output, err := exec.Command(
		"git",
		"-C",
		root,
		"ls-files",
		"-z",
		"--",
		"Makefile",
		"run-tests.sh",
		"scripts/",
		".github/workflows/",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("discover repository test entrypoints: %v\n%s", err, output)
	}

	sources := make(map[string]string)
	for _, file := range strings.Split(string(output), "\x00") {
		if file == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatalf("read tracked test entrypoint %s: %v", file, err)
		}
		sources[file] = string(data)
	}
	if _, ok := sources["scripts/go-test.sh"]; !ok {
		t.Fatal("tracked test entrypoints are missing scripts/go-test.sh")
	}
	return sources
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
	entrypoints := repositoryGoTestEntrypointSources(t)

	runTests := entrypoints["run-tests.sh"]
	requireSourceText(t, "run-tests.sh", runTests, "export WISP_DECK_TESTING=1")
	requireSourceText(t, "run-tests.sh", runTests,
		`exec "$SCRIPT_DIR/scripts/go-test.sh" ./test/bash/... ./test/internal/... ./test/npx/... ./internal/... "$@"`)

	makefile := entrypoints["Makefile"]
	requireSourceText(t, "Makefile", makefile,
		"\tWISP_DECK_TESTING=1 ./scripts/go-test.sh ./...")
	requireSourceText(t, "Makefile", makefile,
		"\tWISP_DECK_TESTING=1 ./scripts/go-test.sh -v ./...")
	if got := strings.Count(makefile, "\tWISP_DECK_TESTING=1 ./run-tests.sh"); got != 2 {
		t.Errorf("Makefile marked run-tests.sh commands = %d, want 2", got)
	}

	testsWorkflow := entrypoints[".github/workflows/tests.yml"]
	installWorkflow := entrypoints[".github/workflows/install.yml"]
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
	commentOnly := addRepositoryEntrypoint(
		entrypoints,
		"scripts/future-check.sh",
		"#!/bin/bash\n# go test must run through the repository driver\n",
	)
	if err := validateRepositoryGoTestEntrypoints(commentOnly); err != nil {
		t.Fatalf("comment-only go test mention failed inventory validation: %v", err)
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
		"raw future script go test": addRepositoryEntrypoint(
			entrypoints,
			"scripts/future-check.sh",
			"#!/bin/bash\ngo test ./...\n",
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

func addRepositoryEntrypoint(
	sources map[string]string,
	file string,
	source string,
) map[string]string {
	added := make(map[string]string, len(sources)+1)
	for name, existingSource := range sources {
		added[name] = existingSource
	}
	added[file] = source
	return added
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
	driver, ok := sources["scripts/go-test.sh"]
	if !ok {
		return fmt.Errorf("repository test entrypoints are missing scripts/go-test.sh")
	}
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
	}

	for file, source := range sources {
		if file == "scripts/go-test.sh" {
			continue
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

func TestWrapperTestingMarkerSourceContract(t *testing.T) {
	source := repositorySource(t, "wrapper.sh")
	if err := validateWrapperTestingMarkerSource(source); err != nil {
		t.Fatalf("current wrapper testing marker contract rejected: %v", err)
	}

	mutations := map[string]string{
		"unconditional marker": mutateWrapperTestingMarkerSource(
			t,
			source,
			`if [[ "${WISP_DECK_TESTING:-}" == "1" ]]; then`,
			"if true; then",
		),
		"zero marker": mutateWrapperTestingMarkerSource(
			t,
			source,
			`if [[ "${WISP_DECK_TESTING:-}" == "1" ]]; then`,
			`if [[ "${WISP_DECK_TESTING:-}" == "0" ]]; then`,
		),
		"stale marker": mutateWrapperTestingMarkerSource(
			t,
			source,
			`if [[ "${WISP_DECK_TESTING:-}" == "1" ]]; then`,
			`if [[ -n "${WISP_DECK_TESTING:-}" ]]; then`,
		),
		"arbitrary marker": mutateWrapperTestingMarkerSource(
			t,
			source,
			"_wisp_deck_testing_tmux_args=(-e WISP_DECK_TESTING=1)",
			`_wisp_deck_testing_tmux_args=(-e "WISP_DECK_TESTING=${WISP_DECK_TESTING:-}")`,
		),
		"missing environment flag": mutateWrapperTestingMarkerSource(
			t,
			source,
			"_wisp_deck_testing_tmux_args=(-e WISP_DECK_TESTING=1)",
			"_wisp_deck_testing_tmux_args=(WISP_DECK_TESTING=1)",
		),
		"unquoted array expansion": mutateWrapperTestingMarkerSource(
			t,
			source,
			`"${_wisp_deck_testing_tmux_args[@]}"`,
			`${_wisp_deck_testing_tmux_args[@]}`,
		),
		"client marker not sanitized": mutateWrapperTestingMarkerSource(
			t,
			source,
			`env -u WISP_DECK_TESTING "$TMUX_CMD" new-session`,
			`"$TMUX_CMD" new-session`,
		),
		"spare command unsets marker": mutateWrapperTestingMarkerSource(
			t,
			source,
			"env -u TMUX -u TMUX_PANE",
			"env -u WISP_DECK_TESTING -u TMUX -u TMUX_PANE",
		),
	}
	for name, mutated := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := validateWrapperTestingMarkerSource(mutated); err == nil {
				t.Fatal("unsafe wrapper testing marker mutation passed source validation")
			}
		})
	}
}

func mutateWrapperTestingMarkerSource(
	t *testing.T,
	source string,
	old string,
	replacement string,
) string {
	t.Helper()
	if count := strings.Count(source, old); count < 1 {
		t.Fatalf("wrapper mutation prerequisite %q occurs %d times", old, count)
	}
	return strings.Replace(source, old, replacement, 1)
}

func validateWrapperTestingMarkerSource(source string) error {
	const setup = `_wisp_deck_testing_tmux_args=()
if [[ "${WISP_DECK_TESTING:-}" == "1" ]]; then
  _wisp_deck_testing_tmux_args=(-e WISP_DECK_TESTING=1)
fi

env -u WISP_DECK_TESTING "$TMUX_CMD" new-session`
	if strings.Count(source, setup) != 1 {
		return fmt.Errorf("testing marker array is not initialized immediately before new-session")
	}
	const expansion = `"${_wisp_deck_testing_tmux_args[@]}"`
	if strings.Count(source, expansion) != 1 {
		return fmt.Errorf("testing marker array must have one quoted argv expansion")
	}
	const sanitizedLaunch = `env -u WISP_DECK_TESTING "$TMUX_CMD" new-session`
	commandStart := strings.Index(source, sanitizedLaunch)
	if commandStart < 0 {
		return fmt.Errorf("wrapper does not sanitize the outer tmux client environment")
	}
	commandEnd := strings.Index(source[commandStart:], "2>&3")
	if commandEnd < 0 {
		return fmt.Errorf("outer tmux new-session command has no boundary")
	}
	command := source[commandStart : commandStart+commandEnd]
	if strings.Count(command, expansion) != 1 {
		return fmt.Errorf("testing marker array is not expanded exactly once in outer new-session")
	}
	if strings.Count(source, "WISP_DECK_TESTING") != 3 {
		return fmt.Errorf("wrapper contains a stale or arbitrary testing marker path")
	}
	remaining := strings.Replace(source, sanitizedLaunch, `"$TMUX_CMD" new-session`, 1)
	for _, forbidden := range []string{
		"unset WISP_DECK_TESTING",
		"env -u WISP_DECK_TESTING",
		"-u WISP_DECK_TESTING",
	} {
		if strings.Contains(remaining, forbidden) {
			return fmt.Errorf("wrapper resets the inherited testing marker with %q", forbidden)
		}
	}
	return nil
}
