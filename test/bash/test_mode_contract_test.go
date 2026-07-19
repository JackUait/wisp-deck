package bash_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		"cmd/",
		"internal/",
		"bin/wisp-deck",
		"bin/wisp-deck-config",
		"bin/npx-wisp-deck.js",
		"lib/",
		"templates/",
		"defaults/",
		"ghostty/",
		"terminals/",
		"wrapper.sh",
		"VERSION",
		"Makefile",
		"package.json",
		"run-tests.sh",
		"scripts/",
		".github/workflows/tests.yml",
		".github/workflows/install.yml",
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
		if strings.IndexByte(string(data), 0) >= 0 {
			continue
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
	for _, file := range []string{
		"cmd/wisp-deck-tui/main.go",
		"internal/tui/mainmenu.go",
		"bin/wisp-deck",
		"bin/wisp-deck-config",
		"bin/npx-wisp-deck.js",
		"lib/notification-setup.sh",
		"templates/statusline-command.sh",
		"defaults/claude-configs.list",
		"ghostty/config",
		"terminals/ghostty/config",
		"wrapper.sh",
		"VERSION",
		"package.json",
		"Makefile",
		"run-tests.sh",
		"scripts/release.sh",
		".github/workflows/tests.yml",
		".github/workflows/install.yml",
	} {
		if _, ok := entrypoints[file]; !ok {
			t.Errorf("repository test-entrypoint inventory is missing audited text %s", file)
		}
	}

	runTests := entrypoints["run-tests.sh"]
	requireSourceText(t, "run-tests.sh", runTests, "export WISP_DECK_TESTING=1")
	requireSourceText(t, "run-tests.sh", runTests,
		`exec "$SCRIPT_DIR/scripts/go-test.sh" ./cmd/wisp-deck-tui/... ./test/bash/... ./test/internal/... ./test/npx/... ./internal/... "$@"`)

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
	for _, test := range []struct {
		name   string
		path   string
		source string
	}{
		{
			name:   "shell inline comment",
			path:   "scripts/future-comment.sh",
			source: "#!/bin/bash\necho safe # go   test ./...\n",
		},
		{
			name: "Go comments",
			path: "internal/future/comment_fixture.go",
			source: `package future
// $GO test ./...
/*
${GO:-go}	test ./...
*/
`,
		},
		{
			name: "JavaScript comments",
			path: "bin/future-comment.js",
			source: `// go   test ./...
/*
$GO	test ./...
*/
`,
		},
	} {
		t.Run("allows "+test.name, func(t *testing.T) {
			safe := addRepositoryEntrypoint(
				entrypoints,
				test.path,
				test.source,
			)
			if err := validateRepositoryGoTestEntrypoints(safe); err != nil {
				t.Fatalf("comment-only raw Go-test fixture rejected: %v", err)
			}
		})
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
		"run-tests omits command package": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"run-tests.sh",
			" ./cmd/wisp-deck-tui/...",
			"",
		),
		"tests workflow omits command package": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			".github/workflows/tests.yml",
			" ./cmd/wisp-deck-tui/...",
			"",
		),
		"run-tests command package moved to comment": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"run-tests.sh",
			" ./cmd/wisp-deck-tui/...",
			"\n# ./cmd/wisp-deck-tui/...",
		),
		"tests workflow command package moved to comment": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			".github/workflows/tests.yml",
			" ./cmd/wisp-deck-tui/...",
			"\n        # ./cmd/wisp-deck-tui/...",
		),
		"tests workflow driver route moved to comment": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			".github/workflows/tests.yml",
			`        run: ./scripts/go-test.sh -json ./cmd/wisp-deck-tui/... ./internal/... ./test/internal/... ./test/npx/... 2>&1 | tee go-test-output.json`,
			"        run: true\n"+
				"        # ./scripts/go-test.sh -json ./cmd/wisp-deck-tui/... ./internal/... ./test/internal/... ./test/npx/... 2>&1 | tee go-test-output.json",
		),
		"Make driver route moved to comment": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"Makefile",
			"\tWISP_DECK_TESTING=1 ./scripts/go-test.sh -v ./...",
			"\t@true\n"+
				"# WISP_DECK_TESTING=1 ./scripts/go-test.sh -v ./...",
		),
		"raw tests workflow go test": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			".github/workflows/tests.yml",
			"./scripts/go-test.sh -json ./cmd/wisp-deck-tui/... ./internal/...",
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
		"raw future script spaced go test": addRepositoryEntrypoint(
			entrypoints,
			"scripts/future-check.sh",
			"#!/bin/bash\ngo   test ./...\n",
		),
		"raw future script tabbed go test": addRepositoryEntrypoint(
			entrypoints,
			"scripts/future-check.sh",
			"#!/bin/bash\ngo\ttest ./...\n",
		),
		"raw future script dollar GO": addRepositoryEntrypoint(
			entrypoints,
			"scripts/future-check.sh",
			"#!/bin/bash\n$GO test ./...\n",
		),
		"raw future script defaulted GO": addRepositoryEntrypoint(
			entrypoints,
			"scripts/future-check.sh",
			"#!/bin/bash\n${GO:-go} test ./...\n",
		),
		"raw compiled helper go test": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func check() { _ = exec.Command("sh", "-c", "go   test ./...") }
`,
		),
		"raw shipped JavaScript dollar GO": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`require("child_process").execSync("$GO test ./...");`,
		),
		"second raw driver go test": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"scripts/go-test.sh",
			`exec -a '__WISP_DECK_REPOSITORY_TEST_V1__.test' go test "$@"`,
			"exec -a '__WISP_DECK_REPOSITORY_TEST_V1__.test' go test \"$@\"\n"+
				"go test ./...",
		),
		"driver contains extra command": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"scripts/go-test.sh",
			"export WISP_DECK_TESTING=1",
			"export WISP_DECK_TESTING=1\ntrue",
		),
		"raw npm lifecycle go test": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"package.json",
			`  "engines": {`,
			"  \"scripts\": {\"test\": \"go test ./...\"},\n"+
				`  "engines": {`,
		),
		"raw npm lifecycle defaulted GO": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			"package.json",
			`  "engines": {`,
			"  \"scripts\": {\"test\": \"${GO:-go}   test ./...\"},\n"+
				`  "engines": {`,
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
	const exactDriver = `#!/usr/bin/env bash
set -euo pipefail

export WISP_DECK_TESTING=1
exec -a '__WISP_DECK_REPOSITORY_TEST_V1__.test' go test "$@"
`
	if driver != exactDriver {
		return fmt.Errorf("scripts/go-test.sh differs from the exact audited driver")
	}
	requiredRoutes := map[string][]string{
		"run-tests.sh": {
			`exec "$SCRIPT_DIR/scripts/go-test.sh" ./cmd/wisp-deck-tui/... ./test/bash/... ./test/internal/... ./test/npx/... ./internal/... "$@"`,
		},
		"Makefile": {
			`WISP_DECK_TESTING=1 ./scripts/go-test.sh ./...`,
			`WISP_DECK_TESTING=1 ./scripts/go-test.sh -v ./...`,
		},
		".github/workflows/tests.yml": {
			`run: ./scripts/go-test.sh -json ./cmd/wisp-deck-tui/... ./internal/... ./test/internal/... ./test/npx/... 2>&1 | tee go-test-output.json`,
			`run: ./scripts/go-test.sh -json -timeout 20m ./test/bash/... 2>&1 | tee bash-test-output.json`,
		},
		".github/workflows/install.yml": {
			`run: ./scripts/go-test.sh -json ./test/npx/... -count=1 2>&1 | tee install-test-output.json`,
			`run: ./scripts/go-test.sh -json ./test/npx/ -run TestInstall_e2e_from_npm_registry -count=1 2>&1 | tee npm-install-test-output.json`,
		},
		"scripts/release.sh": {
			`if ! out="$(cd "$project_dir" && ./scripts/go-test.sh ./test/npx/... -count=1 2>&1)"; then`,
		},
	}
	for file, routes := range requiredRoutes {
		executable := repositoryEntrypointExecutableLines(file, sources[file])
		for _, route := range routes {
			if got := countRepositoryEntrypointLine(executable, route); got != 1 {
				return fmt.Errorf(
					"%s executable route %q occurs %d times, want exactly 1",
					file,
					route,
					got,
				)
			}
		}
	}

	for file, source := range sources {
		if file == "scripts/go-test.sh" {
			continue
		}
		executable := repositoryEntrypointExecutableLines(file, source)
		for lineNumber, line := range strings.Split(executable, "\n") {
			if repositoryRawGoTestCommand.MatchString(line) {
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

var repositoryRawGoTestCommand = regexp.MustCompile(
	`(^|[^[:alnum:]_$])(?:go|\$GO|\$\{GO:-go\})[[:space:]]+test($|[^[:alnum:]_-])`,
)

func countRepositoryEntrypointLine(source string, want string) int {
	count := 0
	for _, line := range strings.Split(source, "\n") {
		if strings.TrimSpace(line) == want {
			count++
		}
	}
	return count
}

func repositoryEntrypointExecutableLines(path string, source string) string {
	switch {
	case strings.HasSuffix(path, ".go"),
		strings.HasSuffix(path, ".js"),
		strings.HasSuffix(path, ".jsx"),
		strings.HasSuffix(path, ".ts"),
		strings.HasSuffix(path, ".tsx"),
		strings.HasSuffix(path, ".mjs"),
		strings.HasSuffix(path, ".cjs"):
		return stripSlashRepositoryEntrypointComments(source)
	case strings.HasSuffix(path, ".lua"):
		return stripLuaRepositoryEntrypointComments(source)
	case path == "package.json":
		return source
	case path == "VERSION",
		strings.HasSuffix(path, ".md"),
		strings.HasSuffix(path, ".list"),
		strings.HasSuffix(path, ".json"),
		strings.HasSuffix(path, ".gitkeep"):
		return ""
	default:
		return stripHashRepositoryEntrypointComments(source)
	}
}

func stripSlashRepositoryEntrypointComments(source string) string {
	const (
		slashCode = iota
		slashSingleQuoted
		slashDoubleQuoted
		slashBacktickQuoted
		slashLineComment
		slashBlockComment
	)
	output := []byte(source)
	state := slashCode
	for index := 0; index < len(output); index++ {
		switch state {
		case slashLineComment:
			if output[index] == '\n' {
				state = slashCode
			} else {
				output[index] = ' '
			}
		case slashBlockComment:
			if output[index] == '*' && index+1 < len(output) && output[index+1] == '/' {
				output[index] = ' '
				output[index+1] = ' '
				index++
				state = slashCode
			} else if output[index] != '\n' {
				output[index] = ' '
			}
		case slashSingleQuoted, slashDoubleQuoted, slashBacktickQuoted:
			quote := byte('\'')
			if state == slashDoubleQuoted {
				quote = '"'
			} else if state == slashBacktickQuoted {
				quote = '`'
			}
			if output[index] == '\\' && state != slashBacktickQuoted &&
				index+1 < len(output) {
				index++
			} else if output[index] == quote {
				state = slashCode
			}
		default:
			switch {
			case output[index] == '/' && index+1 < len(output) &&
				output[index+1] == '/':
				output[index] = ' '
				output[index+1] = ' '
				index++
				state = slashLineComment
			case output[index] == '/' && index+1 < len(output) &&
				output[index+1] == '*':
				output[index] = ' '
				output[index+1] = ' '
				index++
				state = slashBlockComment
			case output[index] == '\'':
				state = slashSingleQuoted
			case output[index] == '"':
				state = slashDoubleQuoted
			case output[index] == '`':
				state = slashBacktickQuoted
			}
		}
	}
	return string(output)
}

func stripHashRepositoryEntrypointComments(source string) string {
	const (
		hashCode = iota
		hashSingleQuoted
		hashDoubleQuoted
		hashComment
	)
	output := []byte(source)
	state := hashCode
	for index := 0; index < len(output); index++ {
		switch state {
		case hashComment:
			if output[index] == '\n' {
				state = hashCode
			} else {
				output[index] = ' '
			}
		case hashSingleQuoted:
			if output[index] == '\'' {
				state = hashCode
			}
		case hashDoubleQuoted:
			if output[index] == '\\' && index+1 < len(output) {
				index++
			} else if output[index] == '"' {
				state = hashCode
			}
		default:
			switch {
			case output[index] == '\\' && index+1 < len(output):
				index++
			case output[index] == '\'':
				state = hashSingleQuoted
			case output[index] == '"':
				state = hashDoubleQuoted
			case output[index] == '#' &&
				(index == 0 || output[index-1] == '\n' ||
					output[index-1] == ' ' || output[index-1] == '\t'):
				output[index] = ' '
				state = hashComment
			}
		}
	}
	return string(output)
}

func stripLuaRepositoryEntrypointComments(source string) string {
	output := []byte(source)
	var quote byte
	for index := 0; index < len(output); index++ {
		if quote != 0 {
			if output[index] == '\\' && index+1 < len(output) {
				index++
			} else if output[index] == quote {
				quote = 0
			}
			continue
		}
		if output[index] == '\'' || output[index] == '"' {
			quote = output[index]
			continue
		}
		if output[index] != '-' || index+1 >= len(output) || output[index+1] != '-' {
			continue
		}
		block := index+3 < len(output) && output[index+2] == '[' && output[index+3] == '['
		for index < len(output) {
			if block && index+1 < len(output) &&
				output[index] == ']' && output[index+1] == ']' {
				output[index] = ' '
				output[index+1] = ' '
				index++
				break
			}
			if !block && output[index] == '\n' {
				break
			}
			if output[index] != '\n' {
				output[index] = ' '
			}
			index++
		}
	}
	return string(output)
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
