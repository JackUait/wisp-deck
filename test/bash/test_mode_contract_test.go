package bash_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
		".github/workflows/",
		"test/bash/main_test.go",
		"test/npx/main_test.go",
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

func validateRepositoryWorkflowTestMode(file, source string) error {
	lines := strings.Split(source, "\n")
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
		steps := repositoryWorkflowRunSteps(block)
		testRoute := false
		for _, step := range steps {
			if !repositoryCommandRunsTests(step.command) {
				continue
			}
			testRoute = true
			if step.disabled {
				return fmt.Errorf(
					"%s job %s disables a repository test step with if: false",
					file,
					name,
				)
			}
		}
		if testRoute && repositoryWorkflowJobDisabled(block) {
			return fmt.Errorf(
				"%s job %s disables repository tests with if: false",
				file,
				name,
			)
		}
		if testRoute && !strings.Contains(
			block,
			"    env:\n      WISP_DECK_TESTING: \"1\"",
		) {
			return fmt.Errorf(
				"%s job %s runs repository tests without exact test-mode env",
				file,
				name,
			)
		}
	}
	return nil
}

type repositoryWorkflowRunStep struct {
	command  string
	disabled bool
}

func repositoryWorkflowJobDisabled(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		if repositoryLeadingSpaces(line) != 4 {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "if:") &&
			repositoryWorkflowConditionIsFalse(
				strings.TrimSpace(strings.TrimPrefix(trimmed, "if:")),
			) {
			return true
		}
	}
	return false
}

func repositoryWorkflowRunSteps(source string) []repositoryWorkflowRunStep {
	lines := strings.Split(source, "\n")
	var steps []repositoryWorkflowRunStep
	for index := 0; index < len(lines); {
		line := lines[index]
		indent := repositoryLeadingSpaces(line)
		trimmed := strings.TrimSpace(line)
		if indent != 6 || !strings.HasPrefix(trimmed, "- ") {
			index++
			continue
		}
		end := len(lines)
		for candidate := index + 1; candidate < len(lines); candidate++ {
			candidateTrimmed := strings.TrimSpace(lines[candidate])
			if candidateTrimmed == "" {
				continue
			}
			candidateIndent := repositoryLeadingSpaces(lines[candidate])
			if candidateIndent < indent ||
				(candidateIndent == indent &&
					strings.HasPrefix(candidateTrimmed, "- ")) {
				end = candidate
				break
			}
		}
		stepLines := lines[index:end]
		disabled := false
		var commands []string
		for stepIndex := 0; stepIndex < len(stepLines); stepIndex++ {
			stepLine := stepLines[stepIndex]
			stepTrimmed := strings.TrimSpace(stepLine)
			if stepIndex == 0 {
				stepTrimmed = strings.TrimSpace(
					strings.TrimPrefix(stepTrimmed, "- "),
				)
			}
			if strings.HasPrefix(stepTrimmed, "if:") &&
				repositoryWorkflowConditionIsFalse(
					strings.TrimSpace(
						strings.TrimPrefix(stepTrimmed, "if:"),
					),
				) {
				disabled = true
			}
			if !strings.HasPrefix(stepTrimmed, "run:") {
				continue
			}
			value := strings.TrimSpace(strings.TrimPrefix(stepTrimmed, "run:"))
			if value != "|" && value != ">" && value != "|-" && value != ">-" {
				commands = append(commands, value)
				continue
			}
			runIndent := repositoryLeadingSpaces(stepLine)
			var block []string
			for candidate := stepIndex + 1; candidate < len(stepLines); candidate++ {
				if strings.TrimSpace(stepLines[candidate]) != "" &&
					repositoryLeadingSpaces(stepLines[candidate]) <= runIndent {
					break
				}
				block = append(block, strings.TrimSpace(stepLines[candidate]))
				stepIndex = candidate
			}
			commands = append(commands, strings.Join(block, "\n"))
		}
		for _, command := range commands {
			steps = append(steps, repositoryWorkflowRunStep{
				command:  command,
				disabled: disabled,
			})
		}
		index = end
	}
	return steps
}

func repositoryLeadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func repositoryWorkflowConditionIsFalse(condition string) bool {
	normalized := strings.ToLower(strings.TrimSpace(condition))
	normalized = strings.Trim(normalized, `"'`)
	if strings.HasPrefix(normalized, "${{") &&
		strings.HasSuffix(normalized, "}}") {
		normalized = strings.TrimSpace(
			strings.TrimSuffix(
				strings.TrimPrefix(normalized, "${{"),
				"}}",
			),
		)
	}
	return normalized == "false"
}

var repositoryWorkflowMakeTestCommand = regexp.MustCompile(
	`(?:^|&&|\|\||;)[[:space:]]*` +
		`(?:[[:alpha:]_][[:alnum:]_]*=[^[:space:]]+[[:space:]]+)*` +
		`(?:env[[:space:]]+)?make` +
		`(?:[[:space:]]+-[^[:space:]]+)*` +
		`[[:space:]]+test(?:-go|-bash)?(?:[[:space:]]|$)`,
)

var repositoryWorkflowDriverCommand = regexp.MustCompile(
	`(?:^|&&|\|\||;)[[:space:]]*` +
		`(?:exec[[:space:]]+)?(?:env[[:space:]]+)?` +
		`(?:[[:alpha:]_][[:alnum:]_]*=[^[:space:]]+[[:space:]]+)*` +
		`(?:\./)?(?:scripts/go-test\.sh|run-tests\.sh)` +
		`(?:[[:space:]]|$)`,
)

func repositoryCommandRunsTests(command string) bool {
	return repositoryWorkflowDriverCommand.MatchString(command) ||
		repositoryWorkflowMakeTestCommand.MatchString(command) ||
		repositoryShellSourceLaunchesRawTest(command)
}

func isRepositoryWorkflow(path string) bool {
	if !strings.HasPrefix(path, ".github/workflows/") {
		return false
	}
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".yml" || extension == ".yaml"
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
		"test/bash/main_test.go",
		"test/npx/main_test.go",
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

	for path, source := range entrypoints {
		if !isRepositoryWorkflow(path) {
			continue
		}
		if err := validateRepositoryWorkflowTestMode(path, source); err != nil {
			t.Error(err)
		}
	}

	for _, path := range []string{"test/bash/main_test.go", "test/npx/main_test.go"} {
		source := entrypoints[path]
		if err := validateRepositoryTestMain(path, source); err != nil {
			t.Errorf("%s repository TestMain rejected: %v", path, err)
		}
		mutations := map[string]string{
			"renamed with comment decoy": mutateRepositoryTestMainSource(
				t,
				source,
				"func TestMain(m *testing.M) {",
				"// func TestMain(m *testing.M) {\nfunc removedTestMain(m *testing.M) {",
			),
			"guard moved to comment": mutateRepositoryTestMainSource(
				t,
				source,
				`	if os.Getenv("WISP_DECK_TESTING") != "1" ||
		len(os.Args) == 0 ||
		os.Args[0] != repositoryTestArgv0 {`,
				`	/*
	if os.Getenv("WISP_DECK_TESTING") != "1" ||
		len(os.Args) == 0 ||
		os.Args[0] != repositoryTestArgv0 {
	*/
	if false {`,
			),
			"reexec moved to comment": mutateRepositoryTestMainSource(
				t,
				source,
				`		if err := syscall.Exec(executable, arguments, environment); err != nil {`,
				`		// if err := syscall.Exec(executable, arguments, environment); err != nil {
		if false {`,
			),
			"final exit removed": mutateRepositoryTestMainSource(
				t,
				source,
				"\tos.Exit(m.Run())",
				"\t// os.Exit(m.Run())\n\treturn",
			),
		}
		for name, mutated := range mutations {
			t.Run(filepath.Base(filepath.Dir(path))+" TestMain "+name, func(t *testing.T) {
				if err := validateRepositoryTestMain(path, mutated); err == nil {
					t.Fatal("unsafe TestMain mutation escaped the AST contract")
				}
			})
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
		{
			name: "Go help literal",
			path: "internal/future/help.go",
			source: `package future
const help = "run go test ./... while developing"
`,
		},
		{
			name:   "JavaScript help literal",
			path:   "bin/future-help.js",
			source: `console.log("run go test ./... while developing");`,
		},
		{
			name:   "shell help literal",
			path:   "scripts/future-help.sh",
			source: "#!/bin/bash\nprintf '%s\\n' 'run go test ./... while developing'\n",
		},
		{
			name: "package metadata help literal",
			path: "package.json",
			source: `{
  "name": "future",
  "description": "run go test ./... while developing",
  "scripts": {
    "help": "printf '%s\n' 'run go test ./... while developing'"
  }
}
`,
		},
		{
			name: "unrelated JavaScript method",
			path: "bin/future-helper.js",
			source: `const helper = {
  spawnSync() {
    return ["go", "test"];
  },
};
helper.spawnSync("go", ["test", "./..."]);
`,
		},
		{
			name: "non-test workflow without repository marker",
			path: ".github/workflows/docs.yml",
			source: `name: Docs
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - run: echo docs
`,
		},
		{
			name: "documentation workflow mentioning test driver",
			path: ".github/workflows/docs.yml",
			source: `name: Document scripts/go-test.sh
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - run: echo './scripts/go-test.sh is the canonical test driver'
`,
		},
		{
			name: "marked future test workflow",
			path: ".github/workflows/future.yml",
			source: `name: Future
jobs:
  test:
    runs-on: ubuntu-latest
    env:
      WISP_DECK_TESTING: "1"
    steps:
      - run: ./scripts/go-test.sh ./...
`,
		},
		{
			name: "disabled non-test workflow",
			path: ".github/workflows/docs.yml",
			source: `name: Docs
jobs:
  publish:
    if: false
    runs-on: ubuntu-latest
    steps:
      - name: Explain tests
        if: false
        run: echo 'run go test ./... while developing'
`,
		},
		{
			name: "Go variables from unrelated scopes",
			path: "internal/future/safe.go",
			source: `package future
import "os/exec"
func mentionsGo() {
	executable := "go"
	_ = executable
}
func checksNode() {
	executable := "node"
	_ = exec.Command(executable, "test")
}
`,
		},
		{
			name: "Go executable reassigned before use",
			path: "internal/future/safe.go",
			source: `package future
import "os/exec"
func check() {
	executable := "go"
	executable = "node"
	_ = exec.Command(executable, "test")
}
`,
		},
		{
			name: "Go shadowed executable",
			path: "internal/future/safe.go",
			source: `package future
import "os/exec"
func check() {
	executable := "go"
	{
		executable := "node"
		_ = exec.Command(executable, "test")
	}
}
`,
		},
		{
			name: "Go argv with non-test subcommand",
			path: "internal/future/safe.go",
			source: `package future
import "os/exec"
func check() {
	argv := append([]string{"go"}, "vet", "test")
	_ = exec.Command(argv[0], argv[1:]...)
}
`,
		},
		{
			name: "Go constructor alias reassigned before use",
			path: "internal/future/safe.go",
			source: `package future
import "os/exec"
func check() {
	runner := exec.Command
	runner = helper.Command
	_ = runner("go", "test", "./...")
}
`,
		},
		{
			name: "Go argument slice reassigned before use",
			path: "internal/future/safe.go",
			source: `package future
import "os/exec"
func check() {
	arguments := []string{"test", "./..."}
	arguments = []string{"vet", "./..."}
	_ = exec.Command("go", arguments...)
}
`,
		},
		{
			name: "harmless Go helper arguments",
			path: "internal/future/safe.go",
			source: `package future
import "os/exec"
func run(executable, subcommand string) {
	_ = exec.Command(executable, subcommand, "./...")
}
func check() { run("node", "test") }
`,
		},
		{
			name: "harmless variadic Go helper arguments",
			path: "internal/future/safe.go",
			source: `package future
import "os/exec"
func run(executable string, arguments ...string) {
	_ = exec.Command(executable, arguments...)
}
func check() { run("go", "vet", "test") }
`,
		},
		{
			name: "harmless Go shell command",
			path: "internal/future/safe.go",
			source: `package future
import "os/exec"
func check() {
	_ = exec.Command("sh", "-c", "printf '%s\n' 'run go test ./...'")
}
`,
		},
		{
			name: "harmless Go alternate process APIs",
			path: "internal/future/safe.go",
			source: `package future
import (
	"os"
	"syscall"
	"golang.org/x/sys/unix"
)
func check() {
	_ = syscall.Exec("node", []string{"node", "test"}, nil)
	_ = unix.Exec("go", []string{"go", "vet", "test"}, nil)
	_, _ = unix.ForkExec("go", []string{"go", "vet", "test"}, nil)
	_, _ = os.StartProcess("go", []string{"go", "vet", "test"}, nil)
}
`,
		},
		{
			name: "harmless exec Cmd",
			path: "internal/future/safe.go",
			source: `package future
import "os/exec"
func check() {
	cmd := exec.Cmd{Path: "go", Args: []string{"go", "vet", "test"}}
	_ = cmd.Run()
}
`,
		},
		{
			name: "JavaScript process alias used as object method",
			path: "bin/future-helper.js",
			source: `const { spawnSync: run } = require("child_process");
const helper = { run() {} };
helper.run("go", ["test", "./..."]);
`,
		},
		{
			name: "JavaScript process alias shadowed in function",
			path: "bin/future-helper.js",
			source: `const child = require("child_process");
const run = child.spawnSync;
function safe(helper) {
  const run = helper.run;
  run("go", ["test", "./..."]);
}
`,
		},
		{
			name: "JavaScript process alias shadowed by parameters",
			path: "bin/future-helper.js",
			source: `const child = require("child_process");
const run = child.spawnSync;
function safe(run) {
  run("go", ["test", "./..."]);
}
const alsoSafe = (run) => {
  run("go", ["test", "./..."]);
};
`,
		},
		{
			name: "JavaScript module alias shadowed in function",
			path: "bin/future-helper.js",
			source: `const child = require("child_process");
function safe(helper) {
  const child = helper;
  child.spawnSync("go", ["test", "./..."]);
}
`,
		},
		{
			name: "JavaScript process alias reassigned before use",
			path: "bin/future-helper.js",
			source: `const child = require("child_process");
let run = child.spawnSync;
run = helper.run;
run("go", ["test", "./..."]);
`,
		},
		{
			name: "unrelated JavaScript dynamic import",
			path: "bin/future-helper.js",
			source: `const helper = await import("./helper.js");
helper.spawnSync("go", ["test", "./..."]);
`,
		},
		{
			name: "semicolonless unrelated JavaScript method",
			path: "bin/future-helper.js",
			source: `const helper = {}
const arguments = ["test", "./..."]
helper.spawnSync("go", arguments)
`,
		},
		{
			name: "JavaScript destructuring from unrelated object",
			path: "bin/future-helper.js",
			source: `const { spawnSync: run } = helper;
run("go", ["test", "./..."]);
`,
		},
		{
			name: "harmless JavaScript helper arguments",
			path: "bin/future-helper.js",
			source: `const child = require("node:child_process");
function run(executable, arguments) {
  child.spawnSync(executable, arguments);
}
run("node", ["test", "./..."]);
`,
		},
		{
			name: "harmless JavaScript arrow helper arguments",
			path: "bin/future-helper.js",
			source: `const child = require("node:child_process");
const run = (executable, arguments) => {
  child.spawnSync(executable, arguments);
};
run("node", ["test", "./..."]);
`,
		},
		{
			name: "harmless JavaScript function-expression alias arguments",
			path: "bin/future-helper.js",
			source: `const child = require("node:child_process");
const run = function(executable, arguments) {
  child.spawnSync(executable, arguments);
};
const alias = run;
alias("go", ["vet", "test"]);
`,
		},
		{
			name: "harmless nested same-name JavaScript helper alias",
			path: "bin/future-helper.js",
			source: `const child = require("node:child_process");
const run = (executable, arguments) => {};
{
  const run = function(executable, arguments) {
    child.spawnSync(executable, arguments);
  };
}
const alias = run;
alias("go", ["test", "./..."]);
`,
		},
		{
			name: "harmless JavaScript helper receiver",
			path: "bin/future-helper.js",
			source: `function run(executable, arguments) {
  helper.spawnSync(executable, arguments);
}
run("go", ["test", "./..."]);
`,
		},
		{
			name: "harmless JavaScript shell command",
			path: "bin/future-helper.js",
			source: `require("node:child_process").execSync(
  "printf '%s\n' 'run go test ./... while developing'"
);`,
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
		"future workflow test job missing marker": addRepositoryEntrypoint(
			entrypoints,
			".github/workflows/future.yml",
			`name: Future
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/go-test.sh ./...
`,
		),
		"future workflow Make test job missing marker": addRepositoryEntrypoint(
			entrypoints,
			".github/workflows/future.yml",
			`name: Future
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: make test-go
`,
		),
		"tests workflow test job disabled": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			".github/workflows/tests.yml",
			"  go-tests:\n",
			"  go-tests:\n    if: false\n",
		),
		"tests workflow test step disabled": mutateRepositoryEntrypoint(
			t,
			entrypoints,
			".github/workflows/tests.yml",
			"      - name: Run Go tests\n        run:",
			"      - name: Run Go tests\n        if: false\n        run:",
		),
		"raw compiled helper go test": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func check() { _ = exec.Command("sh", "-c", "go   test ./...") }
`,
		),
		"raw structured Go test": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func check() { _ = exec.Command("go", "test", "./...") }
`,
		),
		"raw variable structured Go test": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func check() {
	runner := exec.Command
	executable := "go"
	subcommand := "test"
	_ = runner(executable, subcommand, "./...")
}
`,
		),
		"raw structured Go test after reassignment": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func check() {
	executable := "node"
	executable = "go"
	_ = exec.Command(executable, "test", "./...")
}
`,
		),
		"raw indexed Go argv": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func check() {
	argv := []string{"go", "test", "./..."}
	_ = exec.Command(argv[0], argv[1:]...)
}
`,
		),
		"raw indexed Go argv with context": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import (
	"context"
	"os/exec"
)
func check() {
	argv := []string{"go", "test", "./..."}
	_ = exec.CommandContext(context.Background(), argv[0], argv[1:]...)
}
`,
		),
		"raw appended Go argv": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func check() {
	arguments := append([]string{}, "test", "./...")
	_ = exec.Command("go", arguments...)
}
`,
		),
		"raw appended combined Go argv": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func check() {
	argv := append([]string{"go"}, "test", "./...")
	_ = exec.Command(argv[0], argv[1:]...)
}
`,
		),
		"raw Go helper": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func run(executable, subcommand string) {
	_ = exec.Command(executable, subcommand, "./...")
}
func check() { run("go", "test") }
`,
		),
		"raw aliased Go helper": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func run(executable string, arguments []string) {
	_ = exec.Command(executable, arguments...)
}
func check() {
	alias := run
	alias("go", []string{"test", "./..."})
}
`,
		),
		"raw variadic Go helper": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func run(executable string, arguments ...string) {
	_ = exec.Command(executable, arguments...)
}
func check() { run("go", "test", "./...") }
`,
		),
		"raw syscall Exec": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "syscall"
func check() {
	_ = syscall.Exec("go", []string{"go", "test", "./..."}, nil)
}
`,
		),
		"raw aliased syscall Exec": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "syscall"
func check() {
	run := syscall.Exec
	_ = run("go", []string{"go", "test", "./..."}, nil)
}
`,
		),
		"raw unix Exec": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "golang.org/x/sys/unix"
func check() {
	_ = unix.Exec("go", []string{"go", "test", "./..."}, nil)
}
`,
		),
		"raw unix ForkExec": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "golang.org/x/sys/unix"
func check() {
	_, _ = unix.ForkExec("go", []string{"go", "test", "./..."}, nil)
}
`,
		),
		"raw os StartProcess": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os"
func check() {
	_, _ = os.StartProcess("go", []string{"go", "test", "./..."}, nil)
}
`,
		),
		"raw exec Cmd literal": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func check() {
	cmd := exec.Cmd{Path: "go", Args: []string{"go", "test", "./..."}}
	_ = cmd.Run()
}
`,
		),
		"raw exec Cmd assigned fields": addRepositoryEntrypoint(
			entrypoints,
			"internal/future/check.go",
			`package future
import "os/exec"
func check() {
	cmd := new(exec.Cmd)
	cmd.Path = "go"
	cmd.Args = []string{"go", "test", "./..."}
	_ = cmd.Run()
}
`,
		),
		"raw shipped JavaScript dollar GO": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`require("child_process").execSync("$GO test ./...");`,
		),
		"raw structured JavaScript test": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`require("child_process").spawnSync("go", ["test", "./..."]);`,
		),
		"raw variable structured JavaScript test": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const child = require("node:child_process");
const executable = "go";
const arguments = ["test", "./..."];
child.spawnSync(executable, arguments);
`,
		),
		"raw aliased structured JavaScript test": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const { execFileSync: run } = require("child_process");
const executable = "go";
run(executable, ["test", "./..."]);
`,
		),
		"raw semicolonless structured JavaScript test": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const child = require("node:child_process")
const executable = "go"
const arguments = ["test", "./..."]
child.spawnSync(executable, arguments)
`,
		),
		"raw propagated JavaScript module alias": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const childProcess = require("node:child_process");
const child = childProcess;
child.spawnSync("go", ["test", "./..."]);
`,
		),
		"raw destructured JavaScript alias from binding": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const child = require("node:child_process");
const { spawnSync: run } = child;
run("go", ["test", "./..."]);
`,
		),
		"raw dynamic import JavaScript test": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const child = await import("node:child_process");
child.spawnSync("go", ["test", "./..."]);
`,
		),
		"raw destructured dynamic import JavaScript test": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const { spawnSync: run } = await import("node:child_process");
run("go", ["test", "./..."]);
`,
		),
		"raw static JavaScript namespace import": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.mjs",
			`import * as child from "node:child_process";
child.spawnSync("go", ["test", "./..."]);
`,
		),
		"raw static JavaScript named import": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.mjs",
			`import { spawnSync as run } from "node:child_process";
run("go", ["test", "./..."]);
`,
		),
		"raw continued semicolonless JavaScript call": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const child = require("node:child_process")
child
  .spawnSync("go", ["test", "./..."])
`,
		),
		"raw JavaScript helper": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const child = require("node:child_process");
function run(executable, arguments) {
  child.spawnSync(executable, arguments);
}
run("go", ["test", "./..."]);
`,
		),
		"raw JavaScript arrow helper": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const child = require("node:child_process");
const run = (executable, arguments) => {
  child.spawnSync(executable, arguments);
};
run("go", ["test", "./..."]);
`,
		),
		"raw JavaScript function-expression helper alias": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const child = require("node:child_process");
const run = function(executable, arguments) {
  child.spawnSync(executable, arguments);
};
const alias = run;
alias("go", ["test", "./..."]);
`,
		),
		"raw nested same-name JavaScript helper alias": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const child = require("node:child_process");
const run = function(executable, arguments) {
  child.spawnSync(executable, arguments);
};
{
  const run = (executable, arguments) => {};
}
const alias = run;
alias("go", ["test", "./..."]);
`,
		),
		"raw aliased JavaScript helper": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`const child = require("node:child_process");
function run(executable, arguments) {
  child.spawnSync(executable, arguments);
}
const alias = run;
alias("go", ["test", "./..."]);
`,
		),
		"raw JavaScript shell process": addRepositoryEntrypoint(
			entrypoints,
			"bin/future-check.js",
			`require("node:child_process").spawnSync(
  "sh",
  ["-c", "go test ./..."]
);`,
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
		if isRepositoryWorkflow(file) {
			if err := validateRepositoryWorkflowTestMode(file, source); err != nil {
				return err
			}
			for _, step := range repositoryWorkflowRunSteps(source) {
				if repositoryShellSourceLaunchesRawTest(step.command) {
					return fmt.Errorf(
						"%s contains an executable raw go test workflow step",
						file,
					)
				}
			}
			continue
		}
		if file == "scripts/go-test.sh" {
			continue
		}
		switch {
		case strings.HasSuffix(file, ".go"):
			raw, err := repositoryGoSourceLaunchesRawTest(file, source)
			if err != nil {
				return err
			}
			if raw {
				return fmt.Errorf("%s contains a structured raw go test process", file)
			}
		case isRepositoryJavaScriptEntrypoint(file):
			raw, err := repositoryJavaScriptSourceLaunchesRawTest(file, source)
			if err != nil {
				return err
			}
			if raw {
				return fmt.Errorf("%s contains a structured raw go test process", file)
			}
		case file == "package.json":
			raw, err := repositoryPackageScriptsLaunchRawTest(source)
			if err != nil {
				return err
			}
			if raw {
				return fmt.Errorf("%s contains a raw go test lifecycle script", file)
			}
		default:
			if repositoryShellSourceLaunchesRawTest(source) {
				return fmt.Errorf("%s contains executable raw go test", file)
			}
		}
	}
	return nil
}

func repositoryPackageScriptsLaunchRawTest(source string) (bool, error) {
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(source), &manifest); err != nil {
		return false, fmt.Errorf("parse package.json test-entrypoint audit: %w", err)
	}
	for _, command := range manifest.Scripts {
		if repositoryShellSourceLaunchesRawTest(command) {
			return true, nil
		}
	}
	return false, nil
}

func repositoryShellSourceLaunchesRawTest(source string) bool {
	source = strings.ReplaceAll(source, "${GO:-go}", "$GO")
	for _, command := range shellAuditCommands(source) {
		if repositoryShellCommandLaunchesRawTest(command) {
			return true
		}
	}
	return false
}

func repositoryShellCommandLaunchesRawTest(command shellAuditCommand) bool {
	executable := strings.ToLower(filepath.Base(command.executable))
	switch executable {
	case "go", "$go", "${go:-go}":
		return repositoryGoArgumentsRunTests(command.arguments)
	case "bash", "sh", "zsh":
		for index, argument := range command.arguments {
			if argument == "-c" && index+1 < len(command.arguments) {
				return repositoryShellSourceLaunchesRawTest(
					command.arguments[index+1],
				)
			}
		}
	case "eval":
		return repositoryShellSourceLaunchesRawTest(
			strings.Join(command.arguments, " "),
		)
	}
	return false
}

func repositoryGoArgumentsRunTests(arguments []string) bool {
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "-C" {
			index++
			continue
		}
		if strings.HasPrefix(argument, "-") {
			continue
		}
		return argument == "test"
	}
	return false
}

func mutateRepositoryTestMainSource(
	t *testing.T,
	source string,
	old string,
	replacement string,
) string {
	t.Helper()
	if strings.Count(source, old) != 1 {
		t.Fatalf(
			"TestMain mutation prerequisite %q occurs %d times, want exactly one",
			old,
			strings.Count(source, old),
		)
	}
	return strings.Replace(source, old, replacement, 1)
}

func validateRepositoryTestMain(path string, source string) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return fmt.Errorf("parse repository TestMain %s: %w", path, err)
	}
	const sentinel = "__WISP_DECK_REPOSITORY_TEST_V1__.test"
	sentinelDeclarations := 0
	var testMain *ast.FuncDecl
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			if declaration.Tok != token.CONST {
				continue
			}
			for _, specification := range declaration.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 ||
					value.Names[0].Name != "repositoryTestArgv0" ||
					len(value.Values) != 1 {
					continue
				}
				literal, ok := value.Values[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				decoded, decodeErr := strconv.Unquote(literal.Value)
				if decodeErr == nil && decoded == sentinel {
					sentinelDeclarations++
				}
			}
		case *ast.FuncDecl:
			if declaration.Name.Name != "TestMain" {
				continue
			}
			if testMain != nil {
				return fmt.Errorf("%s contains more than one TestMain", path)
			}
			testMain = declaration
		}
	}
	if sentinelDeclarations != 1 {
		return fmt.Errorf(
			"%s repository sentinel declarations = %d, want exactly 1",
			path,
			sentinelDeclarations,
		)
	}
	if testMain == nil {
		return fmt.Errorf("%s is missing TestMain", path)
	}
	var rendered bytes.Buffer
	if err := format.Node(&rendered, token.NewFileSet(), testMain); err != nil {
		return fmt.Errorf("render repository TestMain %s: %w", path, err)
	}
	const exact = `func TestMain(m *testing.M) {
	if os.Getenv("WISP_DECK_TESTING") != "1" || len(os.Args) == 0 || os.Args[0] != repositoryTestArgv0 {
		executable, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		environment := repositoryTestEnvironment(os.Environ())
		arguments := repositoryTestArguments(os.Args)
		if err := syscall.Exec(executable, arguments, environment); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	os.Exit(m.Run())
}`
	if strings.TrimSpace(rendered.String()) != exact {
		return fmt.Errorf("%s TestMain differs from the exact re-exec contract", path)
	}
	return nil
}

func repositoryGoSourceLaunchesRawTest(
	path string,
	source string,
) (bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return false, fmt.Errorf("parse Go test-entrypoint audit %s: %w", path, err)
	}
	imports := make(map[string]string)
	dotImports := make(map[string]bool)
	for _, imported := range file.Imports {
		importPath, decodeErr := strconv.Unquote(imported.Path.Value)
		if decodeErr != nil {
			return false, fmt.Errorf(
				"decode Go test-entrypoint import in %s: %w",
				path,
				decodeErr,
			)
		}
		if importPath != "os/exec" &&
			importPath != "os" &&
			importPath != "syscall" &&
			!strings.HasSuffix(importPath, "/unix") {
			continue
		}
		if imported.Name != nil {
			switch imported.Name.Name {
			case ".":
				dotImports[importPath] = true
			case "_":
			default:
				imports[imported.Name.Name] = importPath
			}
			continue
		}
		imports[filepath.Base(importPath)] = importPath
	}
	if len(imports) == 0 && len(dotImports) == 0 {
		return false, nil
	}
	analyzer := &repositoryGoProcessAnalyzer{
		imports:    imports,
		dotImports: dotImports,
		values:     make(map[*ast.Object]repositoryGoAbstractValue),
		active:     make(map[ast.Node]bool),
	}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		analyzer.analyzeDeclaration(general)
	}
	packageValues := cloneRepositoryGoValues(analyzer.values)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		analyzer.values = cloneRepositoryGoValues(packageValues)
		analyzer.analyzeBlock(function.Body)
	}
	return analyzer.raw, nil
}

type repositoryGoAbstractValue struct {
	strings             map[string]bool
	sequence            []map[string]bool
	sequenceKnown       bool
	processConstructors map[repositoryGoProcessConstructor]bool
	functions           map[*ast.FuncDecl]bool
	functionLiterals    map[*ast.FuncLit]bool
	execCmd             bool
	execCmdPath         map[string]bool
	execCmdArgs         []map[string]bool
	execCmdArgsKnown    bool
}

type repositoryGoProcessConstructor uint8

const (
	repositoryGoCommandConstructor repositoryGoProcessConstructor = iota + 1
	repositoryGoCommandContextConstructor
	repositoryGoExecConstructor
	repositoryGoForkExecConstructor
	repositoryGoStartProcessConstructor
)

type repositoryGoProcessAnalyzer struct {
	imports    map[string]string
	dotImports map[string]bool
	values     map[*ast.Object]repositoryGoAbstractValue
	active     map[ast.Node]bool
	raw        bool
}

func cloneRepositoryGoStrings(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	cloned := make(map[string]bool, len(values))
	for value := range values {
		cloned[value] = true
	}
	return cloned
}

func cloneRepositoryGoValue(value repositoryGoAbstractValue) repositoryGoAbstractValue {
	cloned := repositoryGoAbstractValue{
		strings:          cloneRepositoryGoStrings(value.strings),
		sequenceKnown:    value.sequenceKnown,
		execCmd:          value.execCmd,
		execCmdPath:      cloneRepositoryGoStrings(value.execCmdPath),
		execCmdArgsKnown: value.execCmdArgsKnown,
	}
	if value.processConstructors != nil {
		cloned.processConstructors = make(
			map[repositoryGoProcessConstructor]bool,
			len(value.processConstructors),
		)
		for constructor := range value.processConstructors {
			cloned.processConstructors[constructor] = true
		}
	}
	if value.functions != nil {
		cloned.functions = make(map[*ast.FuncDecl]bool, len(value.functions))
		for function := range value.functions {
			cloned.functions[function] = true
		}
	}
	if value.functionLiterals != nil {
		cloned.functionLiterals = make(
			map[*ast.FuncLit]bool,
			len(value.functionLiterals),
		)
		for function := range value.functionLiterals {
			cloned.functionLiterals[function] = true
		}
	}
	if value.sequenceKnown {
		cloned.sequence = make([]map[string]bool, len(value.sequence))
		for index, element := range value.sequence {
			cloned.sequence[index] = cloneRepositoryGoStrings(element)
		}
	}
	if value.execCmdArgsKnown {
		cloned.execCmdArgs = make([]map[string]bool, len(value.execCmdArgs))
		for index, element := range value.execCmdArgs {
			cloned.execCmdArgs[index] = cloneRepositoryGoStrings(element)
		}
	}
	return cloned
}

func cloneRepositoryGoValues(
	values map[*ast.Object]repositoryGoAbstractValue,
) map[*ast.Object]repositoryGoAbstractValue {
	cloned := make(map[*ast.Object]repositoryGoAbstractValue, len(values))
	for object, value := range values {
		cloned[object] = cloneRepositoryGoValue(value)
	}
	return cloned
}

func mergeRepositoryGoValue(
	left repositoryGoAbstractValue,
	right repositoryGoAbstractValue,
) repositoryGoAbstractValue {
	merged := cloneRepositoryGoValue(left)
	if merged.strings == nil && right.strings != nil {
		merged.strings = make(map[string]bool)
	}
	for value := range right.strings {
		merged.strings[value] = true
	}
	if merged.processConstructors == nil && right.processConstructors != nil {
		merged.processConstructors = make(
			map[repositoryGoProcessConstructor]bool,
		)
	}
	for constructor := range right.processConstructors {
		merged.processConstructors[constructor] = true
	}
	if merged.functions == nil && right.functions != nil {
		merged.functions = make(map[*ast.FuncDecl]bool)
	}
	for function := range right.functions {
		merged.functions[function] = true
	}
	if merged.functionLiterals == nil && right.functionLiterals != nil {
		merged.functionLiterals = make(map[*ast.FuncLit]bool)
	}
	for function := range right.functionLiterals {
		merged.functionLiterals[function] = true
	}
	if left.sequenceKnown && right.sequenceKnown &&
		len(left.sequence) == len(right.sequence) {
		merged.sequenceKnown = true
		merged.sequence = make([]map[string]bool, len(left.sequence))
		for index := range left.sequence {
			merged.sequence[index] = cloneRepositoryGoStrings(left.sequence[index])
			if merged.sequence[index] == nil && right.sequence[index] != nil {
				merged.sequence[index] = make(map[string]bool)
			}
			for value := range right.sequence[index] {
				merged.sequence[index][value] = true
			}
		}
	} else {
		merged.sequenceKnown = false
		merged.sequence = nil
	}
	merged.execCmd = left.execCmd || right.execCmd
	if merged.execCmdPath == nil && right.execCmdPath != nil {
		merged.execCmdPath = make(map[string]bool)
	}
	for value := range right.execCmdPath {
		merged.execCmdPath[value] = true
	}
	if left.execCmdArgsKnown && right.execCmdArgsKnown &&
		len(left.execCmdArgs) == len(right.execCmdArgs) {
		merged.execCmdArgsKnown = true
		merged.execCmdArgs = make([]map[string]bool, len(left.execCmdArgs))
		for index := range left.execCmdArgs {
			merged.execCmdArgs[index] = cloneRepositoryGoStrings(
				left.execCmdArgs[index],
			)
			if merged.execCmdArgs[index] == nil &&
				right.execCmdArgs[index] != nil {
				merged.execCmdArgs[index] = make(map[string]bool)
			}
			for value := range right.execCmdArgs[index] {
				merged.execCmdArgs[index][value] = true
			}
		}
	} else {
		merged.execCmdArgsKnown = false
		merged.execCmdArgs = nil
	}
	return merged
}

func mergeRepositoryGoValues(
	left map[*ast.Object]repositoryGoAbstractValue,
	right map[*ast.Object]repositoryGoAbstractValue,
) map[*ast.Object]repositoryGoAbstractValue {
	merged := cloneRepositoryGoValues(left)
	for object, value := range right {
		if existing, ok := merged[object]; ok {
			merged[object] = mergeRepositoryGoValue(existing, value)
		} else {
			merged[object] = cloneRepositoryGoValue(value)
		}
	}
	return merged
}

func repositoryGoStringValue(value string) repositoryGoAbstractValue {
	return repositoryGoAbstractValue{strings: map[string]bool{value: true}}
}

func repositoryGoConstructorValue(
	constructor repositoryGoProcessConstructor,
) repositoryGoAbstractValue {
	return repositoryGoAbstractValue{
		processConstructors: map[repositoryGoProcessConstructor]bool{
			constructor: true,
		},
	}
}

func (analyzer *repositoryGoProcessAnalyzer) analyzeDeclaration(
	declaration *ast.GenDecl,
) {
	if declaration.Tok != token.CONST && declaration.Tok != token.VAR {
		return
	}
	for _, specification := range declaration.Specs {
		value, ok := specification.(*ast.ValueSpec)
		if !ok {
			continue
		}
		resolved := make([]repositoryGoAbstractValue, len(value.Values))
		for index, expression := range value.Values {
			resolved[index] = analyzer.evaluate(expression)
		}
		for index, name := range value.Names {
			if name.Obj == nil {
				continue
			}
			if index < len(resolved) {
				analyzer.values[name.Obj] = resolved[index]
			} else {
				analyzer.values[name.Obj] = repositoryGoAbstractValue{}
			}
		}
	}
}

func (analyzer *repositoryGoProcessAnalyzer) analyzeBlock(block *ast.BlockStmt) {
	if block == nil {
		return
	}
	for _, statement := range block.List {
		analyzer.analyzeStatement(statement)
	}
}

func (analyzer *repositoryGoProcessAnalyzer) analyzeBranch(
	statement ast.Stmt,
	values map[*ast.Object]repositoryGoAbstractValue,
) map[*ast.Object]repositoryGoAbstractValue {
	previous := analyzer.values
	analyzer.values = cloneRepositoryGoValues(values)
	analyzer.analyzeStatement(statement)
	result := analyzer.values
	analyzer.values = previous
	return result
}

func (analyzer *repositoryGoProcessAnalyzer) analyzeStatement(statement ast.Stmt) {
	switch statement := statement.(type) {
	case *ast.AssignStmt:
		resolved := make([]repositoryGoAbstractValue, len(statement.Rhs))
		for index, expression := range statement.Rhs {
			resolved[index] = analyzer.evaluate(expression)
		}
		for index, target := range statement.Lhs {
			value := repositoryGoAbstractValue{}
			if index < len(resolved) {
				value = resolved[index]
			}
			analyzer.assign(target, value)
		}
	case *ast.BlockStmt:
		analyzer.analyzeBlock(statement)
	case *ast.DeclStmt:
		if declaration, ok := statement.Decl.(*ast.GenDecl); ok {
			analyzer.analyzeDeclaration(declaration)
		}
	case *ast.DeferStmt:
		analyzer.evaluate(statement.Call)
	case *ast.ExprStmt:
		analyzer.evaluate(statement.X)
	case *ast.ForStmt:
		if statement.Init != nil {
			analyzer.analyzeStatement(statement.Init)
		}
		if statement.Cond != nil {
			analyzer.evaluate(statement.Cond)
		}
		before := cloneRepositoryGoValues(analyzer.values)
		body := analyzer.analyzeBranch(statement.Body, before)
		if statement.Post != nil {
			previous := analyzer.values
			analyzer.values = body
			analyzer.analyzeStatement(statement.Post)
			body = analyzer.values
			analyzer.values = previous
		}
		analyzer.values = mergeRepositoryGoValues(before, body)
	case *ast.GoStmt:
		analyzer.evaluate(statement.Call)
	case *ast.IfStmt:
		if statement.Init != nil {
			analyzer.analyzeStatement(statement.Init)
		}
		analyzer.evaluate(statement.Cond)
		before := cloneRepositoryGoValues(analyzer.values)
		thenValues := analyzer.analyzeBranch(statement.Body, before)
		elseValues := before
		if statement.Else != nil {
			elseValues = analyzer.analyzeBranch(statement.Else, before)
		}
		analyzer.values = mergeRepositoryGoValues(thenValues, elseValues)
	case *ast.LabeledStmt:
		analyzer.analyzeStatement(statement.Stmt)
	case *ast.RangeStmt:
		analyzer.evaluate(statement.X)
		before := cloneRepositoryGoValues(analyzer.values)
		body := cloneRepositoryGoValues(before)
		for _, expression := range []ast.Expr{statement.Key, statement.Value} {
			if name, ok := expression.(*ast.Ident); ok && name.Obj != nil {
				body[name.Obj] = repositoryGoAbstractValue{}
			}
		}
		body = analyzer.analyzeBranch(statement.Body, body)
		analyzer.values = mergeRepositoryGoValues(before, body)
	case *ast.ReturnStmt:
		for _, expression := range statement.Results {
			analyzer.evaluate(expression)
		}
	case *ast.SelectStmt:
		before := cloneRepositoryGoValues(analyzer.values)
		merged := before
		for _, clause := range statement.Body.List {
			communication, ok := clause.(*ast.CommClause)
			if !ok {
				continue
			}
			branch := cloneRepositoryGoValues(before)
			previous := analyzer.values
			analyzer.values = branch
			if communication.Comm != nil {
				analyzer.analyzeStatement(communication.Comm)
			}
			for _, nested := range communication.Body {
				analyzer.analyzeStatement(nested)
			}
			merged = mergeRepositoryGoValues(merged, analyzer.values)
			analyzer.values = previous
		}
		analyzer.values = merged
	case *ast.SendStmt:
		analyzer.evaluate(statement.Chan)
		analyzer.evaluate(statement.Value)
	case *ast.SwitchStmt:
		if statement.Init != nil {
			analyzer.analyzeStatement(statement.Init)
		}
		if statement.Tag != nil {
			analyzer.evaluate(statement.Tag)
		}
		before := cloneRepositoryGoValues(analyzer.values)
		merged := before
		for _, clause := range statement.Body.List {
			caseClause, ok := clause.(*ast.CaseClause)
			if !ok {
				continue
			}
			previous := analyzer.values
			analyzer.values = cloneRepositoryGoValues(before)
			for _, expression := range caseClause.List {
				analyzer.evaluate(expression)
			}
			for _, nested := range caseClause.Body {
				analyzer.analyzeStatement(nested)
			}
			merged = mergeRepositoryGoValues(merged, analyzer.values)
			analyzer.values = previous
		}
		analyzer.values = merged
	}
}

func (analyzer *repositoryGoProcessAnalyzer) assign(
	target ast.Expr,
	value repositoryGoAbstractValue,
) {
	if name, ok := target.(*ast.Ident); ok {
		if name.Obj != nil {
			analyzer.values[name.Obj] = cloneRepositoryGoValue(value)
		}
		return
	}
	selector, ok := target.(*ast.SelectorExpr)
	if !ok {
		return
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok || receiver.Obj == nil {
		return
	}
	command := cloneRepositoryGoValue(analyzer.values[receiver.Obj])
	if !command.execCmd {
		return
	}
	switch selector.Sel.Name {
	case "Path":
		command.execCmdPath = cloneRepositoryGoStrings(value.strings)
	case "Args":
		command.execCmdArgsKnown = value.sequenceKnown
		command.execCmdArgs = cloneRepositoryGoSequence(value.sequence)
	default:
		return
	}
	analyzer.values[receiver.Obj] = command
	analyzer.auditExecCmd(command)
}

func cloneRepositoryGoSequence(
	sequence []map[string]bool,
) []map[string]bool {
	if sequence == nil {
		return nil
	}
	cloned := make([]map[string]bool, len(sequence))
	for index, value := range sequence {
		cloned[index] = cloneRepositoryGoStrings(value)
	}
	return cloned
}

func (analyzer *repositoryGoProcessAnalyzer) evaluate(
	expression ast.Expr,
) repositoryGoAbstractValue {
	switch expression := expression.(type) {
	case *ast.BasicLit:
		if expression.Kind != token.STRING {
			return repositoryGoAbstractValue{}
		}
		value, err := strconv.Unquote(expression.Value)
		if err != nil {
			return repositoryGoAbstractValue{}
		}
		return repositoryGoStringValue(value)
	case *ast.BinaryExpr:
		left := analyzer.evaluate(expression.X)
		right := analyzer.evaluate(expression.Y)
		if expression.Op != token.ADD {
			return repositoryGoAbstractValue{}
		}
		combined := make(map[string]bool)
		for leftValue := range left.strings {
			for rightValue := range right.strings {
				combined[leftValue+rightValue] = true
			}
		}
		return repositoryGoAbstractValue{strings: combined}
	case *ast.CallExpr:
		return analyzer.evaluateCall(expression)
	case *ast.CompositeLit:
		if analyzer.isExecCmdType(expression.Type) {
			command := repositoryGoAbstractValue{
				execCmd: true,
			}
			for _, element := range expression.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				name, ok := field.Key.(*ast.Ident)
				if !ok {
					continue
				}
				value := analyzer.evaluate(field.Value)
				switch name.Name {
				case "Path":
					command.execCmdPath = cloneRepositoryGoStrings(value.strings)
				case "Args":
					command.execCmdArgsKnown = value.sequenceKnown
					command.execCmdArgs = cloneRepositoryGoSequence(value.sequence)
				}
			}
			analyzer.auditExecCmd(command)
			return command
		}
		sequence := make([]map[string]bool, 0, len(expression.Elts))
		for _, element := range expression.Elts {
			if keyed, ok := element.(*ast.KeyValueExpr); ok {
				element = keyed.Value
			}
			value, ok := element.(ast.Expr)
			if !ok {
				sequence = append(sequence, nil)
				continue
			}
			sequence = append(sequence, cloneRepositoryGoStrings(
				analyzer.evaluate(value).strings,
			))
		}
		return repositoryGoAbstractValue{
			sequence:      sequence,
			sequenceKnown: true,
		}
	case *ast.FuncLit:
		before := analyzer.values
		analyzer.values = cloneRepositoryGoValues(before)
		analyzer.analyzeBlock(expression.Body)
		analyzer.values = before
		return repositoryGoAbstractValue{
			functionLiterals: map[*ast.FuncLit]bool{expression: true},
		}
	case *ast.Ident:
		if expression.Obj != nil {
			if function, ok := expression.Obj.Decl.(*ast.FuncDecl); ok {
				return repositoryGoAbstractValue{
					functions: map[*ast.FuncDecl]bool{function: true},
				}
			}
			return cloneRepositoryGoValue(analyzer.values[expression.Obj])
		}
		for importPath := range analyzer.dotImports {
			if constructor, ok := repositoryGoImportedProcessConstructor(
				importPath,
				expression.Name,
			); ok {
				return repositoryGoConstructorValue(constructor)
			}
		}
	case *ast.IndexExpr:
		sequence := analyzer.evaluate(expression.X)
		index, ok := repositoryGoInteger(expression.Index)
		if ok && sequence.sequenceKnown &&
			index >= 0 && index < len(sequence.sequence) {
			return repositoryGoAbstractValue{
				strings: cloneRepositoryGoStrings(sequence.sequence[index]),
			}
		}
	case *ast.ParenExpr:
		return analyzer.evaluate(expression.X)
	case *ast.SelectorExpr:
		pkg, ok := expression.X.(*ast.Ident)
		if !ok || pkg.Obj != nil {
			analyzer.evaluate(expression.X)
			return repositoryGoAbstractValue{}
		}
		if constructor, ok := repositoryGoImportedProcessConstructor(
			analyzer.imports[pkg.Name],
			expression.Sel.Name,
		); ok {
			return repositoryGoConstructorValue(constructor)
		}
	case *ast.SliceExpr:
		sequence := analyzer.evaluate(expression.X)
		if !sequence.sequenceKnown {
			return repositoryGoAbstractValue{}
		}
		low := 0
		high := len(sequence.sequence)
		var ok bool
		if expression.Low != nil {
			low, ok = repositoryGoInteger(expression.Low)
			if !ok {
				return repositoryGoAbstractValue{}
			}
		}
		if expression.High != nil {
			high, ok = repositoryGoInteger(expression.High)
			if !ok {
				return repositoryGoAbstractValue{}
			}
		}
		if low < 0 || high < low || high > len(sequence.sequence) {
			return repositoryGoAbstractValue{}
		}
		sliced := make([]map[string]bool, high-low)
		for index := range sliced {
			sliced[index] = cloneRepositoryGoStrings(sequence.sequence[low+index])
		}
		return repositoryGoAbstractValue{
			sequence:      sliced,
			sequenceKnown: true,
		}
	case *ast.UnaryExpr:
		value := analyzer.evaluate(expression.X)
		if expression.Op == token.AND {
			return value
		}
	}
	return repositoryGoAbstractValue{}
}

func repositoryGoImportedProcessConstructor(
	importPath string,
	name string,
) (repositoryGoProcessConstructor, bool) {
	switch {
	case importPath == "os/exec" && name == "Command":
		return repositoryGoCommandConstructor, true
	case importPath == "os/exec" && name == "CommandContext":
		return repositoryGoCommandContextConstructor, true
	case (importPath == "syscall" || strings.HasSuffix(importPath, "/unix")) &&
		name == "Exec":
		return repositoryGoExecConstructor, true
	case strings.HasSuffix(importPath, "/unix") && name == "ForkExec":
		return repositoryGoForkExecConstructor, true
	case importPath == "os" && name == "StartProcess":
		return repositoryGoStartProcessConstructor, true
	default:
		return 0, false
	}
}

func (analyzer *repositoryGoProcessAnalyzer) isExecCmdType(
	expression ast.Expr,
) bool {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name == "Cmd" && analyzer.dotImports["os/exec"]
	case *ast.SelectorExpr:
		pkg, ok := expression.X.(*ast.Ident)
		return ok && pkg.Obj == nil &&
			analyzer.imports[pkg.Name] == "os/exec" &&
			expression.Sel.Name == "Cmd"
	case *ast.ParenExpr:
		return analyzer.isExecCmdType(expression.X)
	case *ast.StarExpr:
		return analyzer.isExecCmdType(expression.X)
	default:
		return false
	}
}

func repositoryGoInteger(expression ast.Expr) (int, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return 0, false
	}
	value, err := strconv.ParseInt(literal.Value, 0, 64)
	if err != nil {
		return 0, false
	}
	return int(value), true
}

func (analyzer *repositoryGoProcessAnalyzer) evaluateCall(
	call *ast.CallExpr,
) repositoryGoAbstractValue {
	function := analyzer.evaluate(call.Fun)
	arguments := make([]repositoryGoAbstractValue, len(call.Args))
	for index, argument := range call.Args {
		arguments[index] = analyzer.evaluate(argument)
	}
	for constructor := range function.processConstructors {
		analyzer.auditProcessCall(call, arguments, constructor)
	}
	for declaration := range function.functions {
		analyzer.invokeFunction(
			declaration,
			declaration.Type.Params,
			declaration.Body,
			arguments,
			call.Ellipsis.IsValid(),
		)
	}
	for literal := range function.functionLiterals {
		analyzer.invokeFunction(
			literal,
			literal.Type.Params,
			literal.Body,
			arguments,
			call.Ellipsis.IsValid(),
		)
	}
	if name, ok := call.Fun.(*ast.Ident); ok && name.Obj == nil {
		switch name.Name {
		case "append":
			if len(arguments) == 0 || !arguments[0].sequenceKnown {
				return repositoryGoAbstractValue{}
			}
			result := cloneRepositoryGoValue(arguments[0])
			for index := 1; index < len(arguments); index++ {
				if index == len(arguments)-1 && call.Ellipsis.IsValid() {
					if !arguments[index].sequenceKnown {
						return repositoryGoAbstractValue{}
					}
					for _, element := range arguments[index].sequence {
						result.sequence = append(
							result.sequence,
							cloneRepositoryGoStrings(element),
						)
					}
					continue
				}
				result.sequence = append(
					result.sequence,
					cloneRepositoryGoStrings(arguments[index].strings),
				)
			}
			return result
		case "make":
			if len(call.Args) >= 2 {
				if _, ok := call.Args[0].(*ast.ArrayType); ok {
					length, lengthOK := repositoryGoInteger(call.Args[1])
					if lengthOK && length == 0 {
						return repositoryGoAbstractValue{
							sequenceKnown: true,
						}
					}
				}
			}
		case "new":
			if len(call.Args) == 1 && analyzer.isExecCmdType(call.Args[0]) {
				return repositoryGoAbstractValue{execCmd: true}
			}
		}
	}
	return repositoryGoAbstractValue{}
}

func (analyzer *repositoryGoProcessAnalyzer) auditProcessCall(
	call *ast.CallExpr,
	arguments []repositoryGoAbstractValue,
	constructor repositoryGoProcessConstructor,
) {
	executableIndex := 0
	switch constructor {
	case repositoryGoCommandContextConstructor:
		executableIndex = 1
	case repositoryGoCommandConstructor,
		repositoryGoExecConstructor,
		repositoryGoForkExecConstructor,
		repositoryGoStartProcessConstructor:
	default:
		return
	}
	if executableIndex >= len(arguments) {
		return
	}
	switch constructor {
	case repositoryGoCommandConstructor,
		repositoryGoCommandContextConstructor:
		processArguments := repositoryGoExpandedArguments(
			call,
			arguments,
			executableIndex,
		)
		if len(processArguments) < 2 {
			return
		}
		if repositoryGoStringSetIsGo(processArguments[0]) &&
			processArguments[1]["test"] {
			analyzer.raw = true
			return
		}
		analyzer.auditShellProcess(processArguments)
	case repositoryGoExecConstructor,
		repositoryGoForkExecConstructor,
		repositoryGoStartProcessConstructor:
		if executableIndex+1 >= len(arguments) {
			return
		}
		argv := arguments[executableIndex+1]
		if repositoryGoStringSetIsGo(arguments[executableIndex].strings) &&
			argv.sequenceKnown && len(argv.sequence) > 1 &&
			argv.sequence[1]["test"] {
			analyzer.raw = true
			return
		}
		if argv.sequenceKnown {
			processArguments := []map[string]bool{
				cloneRepositoryGoStrings(arguments[executableIndex].strings),
			}
			processArguments = append(
				processArguments,
				cloneRepositoryGoSequence(argv.sequence[1:])...,
			)
			analyzer.auditShellProcess(processArguments)
		}
	}
}

func repositoryGoExpandedArguments(
	call *ast.CallExpr,
	arguments []repositoryGoAbstractValue,
	start int,
) []map[string]bool {
	processArguments := make([]map[string]bool, 0, len(arguments)-start)
	for index := start; index < len(arguments); index++ {
		if index == len(arguments)-1 && call.Ellipsis.IsValid() {
			if !arguments[index].sequenceKnown {
				return nil
			}
			processArguments = append(
				processArguments,
				cloneRepositoryGoSequence(arguments[index].sequence)...,
			)
			continue
		}
		processArguments = append(
			processArguments,
			cloneRepositoryGoStrings(arguments[index].strings),
		)
	}
	return processArguments
}

func repositoryGoStringSetIsGo(values map[string]bool) bool {
	for value := range values {
		if strings.EqualFold(filepath.Base(value), "go") {
			return true
		}
	}
	return false
}

func repositoryGoStringSetIsShell(values map[string]bool) bool {
	for value := range values {
		switch strings.ToLower(filepath.Base(value)) {
		case "bash", "sh", "zsh":
			return true
		}
	}
	return false
}

func (analyzer *repositoryGoProcessAnalyzer) auditShellProcess(
	processArguments []map[string]bool,
) {
	if len(processArguments) < 3 ||
		!repositoryGoStringSetIsShell(processArguments[0]) {
		return
	}
	for index := 1; index+1 < len(processArguments); index++ {
		if !processArguments[index]["-c"] {
			continue
		}
		for command := range processArguments[index+1] {
			if repositoryShellSourceLaunchesRawTest(command) {
				analyzer.raw = true
				return
			}
		}
	}
}

func (analyzer *repositoryGoProcessAnalyzer) auditExecCmd(
	command repositoryGoAbstractValue,
) {
	if !command.execCmd || !command.execCmdArgsKnown {
		return
	}
	if repositoryGoStringSetIsGo(command.execCmdPath) &&
		len(command.execCmdArgs) > 1 &&
		command.execCmdArgs[1]["test"] {
		analyzer.raw = true
		return
	}
	processArguments := []map[string]bool{
		cloneRepositoryGoStrings(command.execCmdPath),
	}
	if len(command.execCmdArgs) > 1 {
		processArguments = append(
			processArguments,
			cloneRepositoryGoSequence(command.execCmdArgs[1:])...,
		)
	}
	analyzer.auditShellProcess(processArguments)
}

func (analyzer *repositoryGoProcessAnalyzer) invokeFunction(
	key ast.Node,
	parameters *ast.FieldList,
	body *ast.BlockStmt,
	arguments []repositoryGoAbstractValue,
	callUsesEllipsis bool,
) {
	if key == nil || body == nil || analyzer.active[key] {
		return
	}
	analyzer.active[key] = true
	defer delete(analyzer.active, key)

	previous := analyzer.values
	analyzer.values = cloneRepositoryGoValues(previous)
	defer func() {
		analyzer.values = previous
	}()

	argumentIndex := 0
	if parameters != nil {
		for _, field := range parameters.List {
			names := field.Names
			if len(names) == 0 {
				argumentIndex++
				continue
			}
			if _, variadic := field.Type.(*ast.Ellipsis); variadic {
				value := repositoryGoAbstractValue{sequenceKnown: true}
				if callUsesEllipsis && argumentIndex < len(arguments) {
					value = cloneRepositoryGoValue(arguments[argumentIndex])
				} else {
					for ; argumentIndex < len(arguments); argumentIndex++ {
						value.sequence = append(
							value.sequence,
							cloneRepositoryGoStrings(
								arguments[argumentIndex].strings,
							),
						)
					}
				}
				for _, name := range names {
					if name.Obj != nil {
						analyzer.values[name.Obj] =
							cloneRepositoryGoValue(value)
					}
				}
				continue
			}
			for _, name := range names {
				value := repositoryGoAbstractValue{}
				if argumentIndex < len(arguments) {
					value = arguments[argumentIndex]
				}
				if name.Obj != nil {
					analyzer.values[name.Obj] = cloneRepositoryGoValue(value)
				}
				argumentIndex++
			}
		}
	}
	analyzer.analyzeBlock(body)
}

func isRepositoryJavaScriptEntrypoint(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}

func repositoryJavaScriptSourceLaunchesRawTest(
	path string,
	source string,
) (bool, error) {
	tokens, ok := lexRepositoryJavaScript(source)
	if !ok {
		return false, fmt.Errorf("parse JavaScript test-entrypoint audit %s", path)
	}
	scope := &repositoryJavaScriptScope{
		bindings: make(map[string]repositoryJavaScriptValue),
	}
	definitions := repositoryJavaScriptFunctionDefinitions(tokens)
	return repositoryJavaScriptTokensLaunchRawTest(
		path,
		tokens,
		scope,
		definitions,
		make(map[string]bool),
	)
}

func repositoryJavaScriptTokensLaunchRawTest(
	path string,
	tokens []repositoryJavaScriptToken,
	scope *repositoryJavaScriptScope,
	definitions map[string]repositoryJavaScriptFunctionDefinition,
	active map[string]bool,
) (bool, error) {
	functionBodies := repositoryJavaScriptFunctionBodies(tokens)
	for index := 0; index < len(tokens); index++ {
		current := tokens[index]
		if current.kind == '{' {
			if parameters, functionBody := functionBodies[index]; functionBody {
				scope = newRepositoryJavaScriptFunctionScope(scope, parameters)
			} else {
				scope = &repositoryJavaScriptScope{
					parent:   scope,
					bindings: make(map[string]repositoryJavaScriptValue),
				}
			}
			continue
		}
		if current.kind == '}' {
			if scope.parent != nil {
				scope = scope.parent
			}
			continue
		}
		if current.kind == 'i' {
			switch current.value {
			case "const", "let", "var":
				end := repositoryJavaScriptStatementEnd(tokens, index+1)
				repositoryJavaScriptDeclare(
					scope,
					current.value,
					tokens[index+1:end],
				)
			case "function":
				if index+1 < len(tokens) && tokens[index+1].kind == 'i' {
					name := tokens[index+1].value
					scope.declare(name, repositoryJavaScriptValue{
						functions: map[string]bool{name: true},
					})
				}
			case "import":
				if index+1 < len(tokens) && tokens[index+1].kind != '(' {
					end := repositoryJavaScriptStatementEnd(tokens, index+1)
					repositoryJavaScriptDeclareImport(
						scope,
						tokens[index+1:end],
					)
				}
			default:
				if index+1 < len(tokens) && tokens[index+1].kind == '=' &&
					(index+2 >= len(tokens) || tokens[index+2].kind != '=') {
					end := repositoryJavaScriptStatementEnd(tokens, index+2)
					expression := tokens[index+2 : end]
					value := repositoryJavaScriptEvaluate(
						scope,
						expression,
					)
					if _, helper := repositoryJavaScriptFunctionExpression(
						expression,
					); helper {
						value.functions = map[string]bool{
							repositoryJavaScriptFunctionBindingKey(current): true,
						}
					}
					scope.assign(current.value, value)
				}
			}
		}
		if current.kind != '(' {
			continue
		}
		end := matchingRepositoryJavaScriptToken(tokens, index, '(', ')')
		if end < 0 {
			return false, fmt.Errorf(
				"parse JavaScript call in test-entrypoint audit %s",
				path,
			)
		}
		start := repositoryJavaScriptExpressionStart(tokens, index)
		callee := repositoryJavaScriptEvaluate(scope, tokens[start:index])
		arguments := splitRepositoryJavaScriptArguments(tokens[index+1 : end])
		resolved := make([]repositoryJavaScriptValue, len(arguments))
		for argumentIndex, argument := range arguments {
			resolved[argumentIndex] = repositoryJavaScriptEvaluate(scope, argument)
		}
		if repositoryJavaScriptProcessCallIsRaw(callee, resolved) {
			return true, nil
		}
		for name := range callee.functions {
			if active[name] {
				continue
			}
			definition, ok := definitions[name]
			if !ok {
				continue
			}
			active[name] = true
			functionScope := newRepositoryJavaScriptFunctionScope(
				scope,
				definition.parameters,
			)
			for parameterIndex, parameter := range definition.parameters {
				if parameterIndex < len(resolved) {
					functionScope.bindings[parameter] =
						cloneRepositoryJavaScriptValue(resolved[parameterIndex])
				}
			}
			raw, err := repositoryJavaScriptTokensLaunchRawTest(
				path,
				definition.body,
				functionScope,
				definitions,
				active,
			)
			delete(active, name)
			if err != nil {
				return false, err
			}
			if raw {
				return true, nil
			}
		}
	}
	return false, nil
}

type repositoryJavaScriptFunctionDefinition struct {
	parameters []string
	body       []repositoryJavaScriptToken
}

func repositoryJavaScriptFunctionDefinitions(
	tokens []repositoryJavaScriptToken,
) map[string]repositoryJavaScriptFunctionDefinition {
	definitions := make(map[string]repositoryJavaScriptFunctionDefinition)
	for index := 0; index+4 < len(tokens); index++ {
		if tokens[index].kind != 'i' ||
			tokens[index].value != "function" ||
			tokens[index+1].kind != 'i' ||
			tokens[index+2].kind != '(' {
			continue
		}
		parametersEnd := matchingRepositoryJavaScriptToken(
			tokens,
			index+2,
			'(',
			')',
		)
		if parametersEnd < 0 || parametersEnd+1 >= len(tokens) ||
			tokens[parametersEnd+1].kind != '{' {
			continue
		}
		bodyEnd := matchingRepositoryJavaScriptToken(
			tokens,
			parametersEnd+1,
			'{',
			'}',
		)
		if bodyEnd < 0 {
			continue
		}
		definitions[tokens[index+1].value] =
			repositoryJavaScriptFunctionDefinition{
				parameters: repositoryJavaScriptParameterNames(
					tokens[index+3 : parametersEnd],
				),
				body: tokens[parametersEnd+2 : bodyEnd],
			}
		index = bodyEnd
	}
	for index, current := range tokens {
		if current.kind != 'i' ||
			(current.value != "const" &&
				current.value != "let" &&
				current.value != "var") {
			continue
		}
		end := repositoryJavaScriptStatementEnd(tokens, index+1)
		for _, declaration := range splitRepositoryJavaScriptArguments(
			tokens[index+1 : end],
		) {
			assignment := repositoryJavaScriptTopLevelToken(declaration, '=')
			if assignment < 0 {
				continue
			}
			target := trimRepositoryJavaScriptTokens(
				declaration[:assignment],
			)
			if len(target) != 1 || target[0].kind != 'i' {
				continue
			}
			definition, ok := repositoryJavaScriptFunctionExpression(
				declaration[assignment+1:],
			)
			if ok {
				definitions[repositoryJavaScriptFunctionBindingKey(
					target[0],
				)] = definition
			}
		}
	}
	return definitions
}

func repositoryJavaScriptFunctionBindingKey(
	target repositoryJavaScriptToken,
) string {
	return fmt.Sprintf("@function:%s:%d", target.value, target.position)
}

func repositoryJavaScriptFunctionExpression(
	tokens []repositoryJavaScriptToken,
) (repositoryJavaScriptFunctionDefinition, bool) {
	tokens = trimRepositoryJavaScriptTokens(tokens)
	if len(tokens) == 0 {
		return repositoryJavaScriptFunctionDefinition{}, false
	}
	if tokens[0].kind == 'i' && tokens[0].value == "function" {
		open := 1
		if open < len(tokens) && tokens[open].kind == 'i' {
			open++
		}
		if open >= len(tokens) || tokens[open].kind != '(' {
			return repositoryJavaScriptFunctionDefinition{}, false
		}
		close := matchingRepositoryJavaScriptToken(tokens, open, '(', ')')
		if close < 0 || close+1 >= len(tokens) ||
			tokens[close+1].kind != '{' {
			return repositoryJavaScriptFunctionDefinition{}, false
		}
		bodyEnd := matchingRepositoryJavaScriptToken(
			tokens,
			close+1,
			'{',
			'}',
		)
		if bodyEnd != len(tokens)-1 {
			return repositoryJavaScriptFunctionDefinition{}, false
		}
		return repositoryJavaScriptFunctionDefinition{
			parameters: repositoryJavaScriptParameterNames(
				tokens[open+1 : close],
			),
			body: tokens[close+2 : bodyEnd],
		}, true
	}

	arrow := repositoryJavaScriptTopLevelToken(tokens, '=')
	if arrow < 0 || arrow+1 >= len(tokens) ||
		tokens[arrow+1].kind != '>' {
		return repositoryJavaScriptFunctionDefinition{}, false
	}
	parameters := trimRepositoryJavaScriptTokens(tokens[:arrow])
	if len(parameters) >= 2 && parameters[0].kind == '(' &&
		matchingRepositoryJavaScriptToken(
			parameters,
			0,
			'(',
			')',
		) == len(parameters)-1 {
		parameters = parameters[1 : len(parameters)-1]
	}
	body := trimRepositoryJavaScriptTokens(tokens[arrow+2:])
	if len(body) == 0 {
		return repositoryJavaScriptFunctionDefinition{}, false
	}
	if body[0].kind == '{' {
		bodyEnd := matchingRepositoryJavaScriptToken(body, 0, '{', '}')
		if bodyEnd != len(body)-1 {
			return repositoryJavaScriptFunctionDefinition{}, false
		}
		body = body[1:bodyEnd]
	}
	return repositoryJavaScriptFunctionDefinition{
		parameters: repositoryJavaScriptParameterNames(parameters),
		body:       body,
	}, true
}

func repositoryJavaScriptProcessMethod(method string) bool {
	switch strings.ToLower(method) {
	case "exec", "execsync", "execfile", "execfilesync", "spawn", "spawnsync":
		return true
	default:
		return false
	}
}

type repositoryJavaScriptToken struct {
	kind            byte
	value           string
	lineBreakBefore bool
	position        int
}

type repositoryJavaScriptValue struct {
	strings        map[string]bool
	sequence       []repositoryJavaScriptValue
	sequenceKnown  bool
	module         bool
	processMethods map[string]bool
	functions      map[string]bool
}

type repositoryJavaScriptScope struct {
	parent       *repositoryJavaScriptScope
	bindings     map[string]repositoryJavaScriptValue
	functionBody bool
}

func cloneRepositoryJavaScriptValue(
	value repositoryJavaScriptValue,
) repositoryJavaScriptValue {
	cloned := repositoryJavaScriptValue{
		sequenceKnown: value.sequenceKnown,
		module:        value.module,
	}
	if value.strings != nil {
		cloned.strings = make(map[string]bool, len(value.strings))
		for item := range value.strings {
			cloned.strings[item] = true
		}
	}
	if value.processMethods != nil {
		cloned.processMethods = make(map[string]bool, len(value.processMethods))
		for method := range value.processMethods {
			cloned.processMethods[method] = true
		}
	}
	if value.functions != nil {
		cloned.functions = make(map[string]bool, len(value.functions))
		for function := range value.functions {
			cloned.functions[function] = true
		}
	}
	if value.sequenceKnown {
		cloned.sequence = make([]repositoryJavaScriptValue, len(value.sequence))
		for index, item := range value.sequence {
			cloned.sequence[index] = cloneRepositoryJavaScriptValue(item)
		}
	}
	return cloned
}

func (scope *repositoryJavaScriptScope) lookup(
	name string,
) (repositoryJavaScriptValue, bool) {
	for current := scope; current != nil; current = current.parent {
		value, ok := current.bindings[name]
		if ok {
			return cloneRepositoryJavaScriptValue(value), true
		}
	}
	return repositoryJavaScriptValue{}, false
}

func (scope *repositoryJavaScriptScope) declare(
	name string,
	value repositoryJavaScriptValue,
) {
	scope.bindings[name] = cloneRepositoryJavaScriptValue(value)
}

func (scope *repositoryJavaScriptScope) assign(
	name string,
	value repositoryJavaScriptValue,
) {
	for current := scope; current != nil; current = current.parent {
		if _, ok := current.bindings[name]; ok {
			current.bindings[name] = cloneRepositoryJavaScriptValue(value)
			return
		}
	}
	scope.bindings[name] = cloneRepositoryJavaScriptValue(value)
}

func newRepositoryJavaScriptFunctionScope(
	parent *repositoryJavaScriptScope,
	parameters []string,
) *repositoryJavaScriptScope {
	visible := make(map[string]repositoryJavaScriptValue)
	var chain []*repositoryJavaScriptScope
	for current := parent; current != nil; current = current.parent {
		chain = append(chain, current)
	}
	for index := len(chain) - 1; index >= 0; index-- {
		for name, value := range chain[index].bindings {
			visible[name] = cloneRepositoryJavaScriptValue(value)
		}
	}
	function := &repositoryJavaScriptScope{
		parent:       parent,
		bindings:     visible,
		functionBody: true,
	}
	for _, parameter := range parameters {
		function.bindings[parameter] = repositoryJavaScriptValue{}
	}
	return function
}

func lexRepositoryJavaScript(
	source string,
) ([]repositoryJavaScriptToken, bool) {
	tokens := make([]repositoryJavaScriptToken, 0, len(source)/3)
	lineBreak := false
	for index := 0; index < len(source); {
		character := source[index]
		if character == ' ' || character == '\t' ||
			character == '\r' || character == '\n' {
			if character == '\r' || character == '\n' {
				lineBreak = true
			}
			index++
			continue
		}
		if character == '/' && index+1 < len(source) {
			switch source[index+1] {
			case '/':
				index += 2
				for index < len(source) && source[index] != '\n' {
					index++
				}
				lineBreak = true
				continue
			case '*':
				end := strings.Index(source[index+2:], "*/")
				if end < 0 {
					return nil, false
				}
				commentEnd := index + end + 4
				if strings.ContainsAny(source[index:commentEnd], "\r\n") {
					lineBreak = true
				}
				index = commentEnd
				continue
			}
		}
		token := repositoryJavaScriptToken{
			lineBreakBefore: lineBreak,
			position:        len(tokens),
		}
		lineBreak = false
		if character == '\'' || character == '"' || character == '`' {
			value, next, valid := readJavaScriptString(source, index, character)
			if !valid {
				return nil, false
			}
			token.kind = 's'
			token.value = value
			tokens = append(tokens, token)
			index = next
			continue
		}
		if isJavaScriptIdentifierStart(character) {
			start := index
			index++
			for index < len(source) && isJavaScriptIdentifierPart(source[index]) {
				index++
			}
			token.kind = 'i'
			token.value = source[start:index]
			tokens = append(tokens, token)
			continue
		}
		if character >= '0' && character <= '9' {
			start := index
			index++
			for index < len(source) &&
				((source[index] >= '0' && source[index] <= '9') ||
					source[index] == 'x' || source[index] == 'X' ||
					(source[index] >= 'a' && source[index] <= 'f') ||
					(source[index] >= 'A' && source[index] <= 'F')) {
				index++
			}
			token.kind = 'n'
			token.value = source[start:index]
			tokens = append(tokens, token)
			continue
		}
		token.kind = character
		token.value = string(character)
		tokens = append(tokens, token)
		index++
	}
	return tokens, true
}

func matchingRepositoryJavaScriptToken(
	tokens []repositoryJavaScriptToken,
	start int,
	open byte,
	close byte,
) int {
	depth := 0
	for index := start; index < len(tokens); index++ {
		switch tokens[index].kind {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func matchingRepositoryJavaScriptTokenBackward(
	tokens []repositoryJavaScriptToken,
	start int,
	open byte,
	close byte,
) int {
	depth := 0
	for index := start; index >= 0; index-- {
		switch tokens[index].kind {
		case close:
			depth++
		case open:
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func splitRepositoryJavaScriptArguments(
	tokens []repositoryJavaScriptToken,
) [][]repositoryJavaScriptToken {
	var arguments [][]repositoryJavaScriptToken
	start := 0
	depth := 0
	for index, token := range tokens {
		switch token.kind {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				arguments = append(arguments, tokens[start:index])
				start = index + 1
			}
		}
	}
	if start < len(tokens) {
		arguments = append(arguments, tokens[start:])
	}
	return arguments
}

func repositoryJavaScriptStatementEnd(
	tokens []repositoryJavaScriptToken,
	start int,
) int {
	depth := 0
	for index := start; index < len(tokens); index++ {
		if depth == 0 && index > start && tokens[index].lineBreakBefore &&
			!repositoryJavaScriptContinuesAcrossLine(tokens[index-1], tokens[index]) {
			return index
		}
		switch tokens[index].kind {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return index
			}
			depth--
		case ';':
			if depth == 0 {
				return index
			}
		}
	}
	return len(tokens)
}

func repositoryJavaScriptContinuesAcrossLine(
	previous repositoryJavaScriptToken,
	current repositoryJavaScriptToken,
) bool {
	switch current.kind {
	case '.', '(', '[', ',', '?', ':', '+', '-', '*', '/', '%', '&', '|', '^':
		return true
	}
	switch previous.kind {
	case '.', '(', '[', '{', ',', '=', '?', ':', '+', '-', '*', '/', '%', '&', '|', '^', '!':
		return true
	}
	return false
}

func repositoryJavaScriptFunctionBodies(
	tokens []repositoryJavaScriptToken,
) map[int][]string {
	bodies := make(map[int][]string)
	for index, current := range tokens {
		if current.kind == 'i' && current.value == "function" {
			open := index + 1
			if open < len(tokens) && tokens[open].kind == 'i' {
				open++
			}
			if open >= len(tokens) || tokens[open].kind != '(' {
				continue
			}
			close := matchingRepositoryJavaScriptToken(tokens, open, '(', ')')
			if close < 0 || close+1 >= len(tokens) || tokens[close+1].kind != '{' {
				continue
			}
			bodies[close+1] = repositoryJavaScriptParameterNames(
				tokens[open+1 : close],
			)
			continue
		}
		if current.kind != '>' || index == 0 || tokens[index-1].kind != '=' ||
			index+1 >= len(tokens) || tokens[index+1].kind != '{' {
			continue
		}
		var parameters []repositoryJavaScriptToken
		if index >= 2 && tokens[index-2].kind == ')' {
			open := matchingRepositoryJavaScriptTokenBackward(
				tokens,
				index-2,
				'(',
				')',
			)
			if open >= 0 {
				parameters = tokens[open+1 : index-2]
			}
		} else if index >= 2 && tokens[index-2].kind == 'i' {
			parameters = tokens[index-2 : index-1]
		}
		bodies[index+1] = repositoryJavaScriptParameterNames(parameters)
	}
	return bodies
}

func repositoryJavaScriptParameterNames(
	tokens []repositoryJavaScriptToken,
) []string {
	var names []string
	for _, parameter := range splitRepositoryJavaScriptArguments(tokens) {
		for _, current := range parameter {
			if current.kind == 'i' {
				names = append(names, current.value)
				break
			}
		}
	}
	return names
}

func repositoryJavaScriptDeclare(
	scope *repositoryJavaScriptScope,
	declarationKind string,
	tokens []repositoryJavaScriptToken,
) {
	targetScope := scope
	if declarationKind == "var" {
		for targetScope.parent != nil && !targetScope.functionBody {
			targetScope = targetScope.parent
		}
	}
	for _, declaration := range splitRepositoryJavaScriptArguments(tokens) {
		assignment := repositoryJavaScriptTopLevelToken(declaration, '=')
		var target []repositoryJavaScriptToken
		value := repositoryJavaScriptValue{}
		if assignment >= 0 {
			target = declaration[:assignment]
			expression := declaration[assignment+1:]
			value = repositoryJavaScriptEvaluate(
				scope,
				expression,
			)
			trimmedTarget := trimRepositoryJavaScriptTokens(target)
			if len(trimmedTarget) == 1 &&
				trimmedTarget[0].kind == 'i' {
				if _, helper := repositoryJavaScriptFunctionExpression(
					expression,
				); helper {
					value.functions = map[string]bool{
						repositoryJavaScriptFunctionBindingKey(
							trimmedTarget[0],
						): true,
					}
				}
			}
		} else {
			target = declaration
		}
		repositoryJavaScriptDeclareTarget(targetScope, target, value)
	}
}

func repositoryJavaScriptDeclareTarget(
	scope *repositoryJavaScriptScope,
	target []repositoryJavaScriptToken,
	value repositoryJavaScriptValue,
) {
	target = trimRepositoryJavaScriptTokens(target)
	if len(target) == 1 && target[0].kind == 'i' {
		scope.declare(target[0].value, value)
		return
	}
	if len(target) < 2 || target[0].kind != '{' {
		return
	}
	close := matchingRepositoryJavaScriptToken(target, 0, '{', '}')
	if close < 0 {
		return
	}
	for _, property := range splitRepositoryJavaScriptArguments(target[1:close]) {
		property = trimRepositoryJavaScriptTokens(property)
		if len(property) == 0 || property[0].kind != 'i' {
			continue
		}
		imported := property[0].value
		local := imported
		colon := repositoryJavaScriptTopLevelToken(property, ':')
		if colon >= 0 && colon+1 < len(property) &&
			property[colon+1].kind == 'i' {
			local = property[colon+1].value
		}
		resolved := repositoryJavaScriptValue{}
		if value.module && repositoryJavaScriptProcessMethod(imported) {
			resolved.processMethods = map[string]bool{imported: true}
		}
		scope.declare(local, resolved)
	}
}

func repositoryJavaScriptDeclareImport(
	scope *repositoryJavaScriptScope,
	tokens []repositoryJavaScriptToken,
) {
	if !repositoryJavaScriptContainsChildProcessModule(tokens) {
		return
	}
	for index := 0; index < len(tokens); index++ {
		if tokens[index].kind == '*' &&
			index+2 < len(tokens) &&
			tokens[index+1].kind == 'i' &&
			tokens[index+1].value == "as" &&
			tokens[index+2].kind == 'i' {
			scope.declare(
				tokens[index+2].value,
				repositoryJavaScriptValue{module: true},
			)
			return
		}
		if tokens[index].kind != '{' {
			continue
		}
		close := matchingRepositoryJavaScriptToken(tokens, index, '{', '}')
		if close < 0 {
			return
		}
		for _, property := range splitRepositoryJavaScriptArguments(
			tokens[index+1 : close],
		) {
			property = trimRepositoryJavaScriptTokens(property)
			if len(property) == 0 || property[0].kind != 'i' ||
				!repositoryJavaScriptProcessMethod(property[0].value) {
				continue
			}
			local := property[0].value
			if len(property) >= 3 && property[1].kind == 'i' &&
				property[1].value == "as" && property[2].kind == 'i' {
				local = property[2].value
			}
			scope.declare(local, repositoryJavaScriptValue{
				processMethods: map[string]bool{property[0].value: true},
			})
		}
		return
	}
	for index, current := range tokens {
		if current.kind == 'i' && current.value == "from" {
			if index > 0 && tokens[0].kind == 'i' {
				scope.declare(
					tokens[0].value,
					repositoryJavaScriptValue{module: true},
				)
			}
			return
		}
	}
}

func repositoryJavaScriptTopLevelToken(
	tokens []repositoryJavaScriptToken,
	want byte,
) int {
	depth := 0
	for index, current := range tokens {
		switch current.kind {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		default:
			if current.kind == want && depth == 0 {
				return index
			}
		}
	}
	return -1
}

func trimRepositoryJavaScriptTokens(
	tokens []repositoryJavaScriptToken,
) []repositoryJavaScriptToken {
	for len(tokens) > 0 && tokens[0].kind == ';' {
		tokens = tokens[1:]
	}
	for len(tokens) > 0 && tokens[len(tokens)-1].kind == ';' {
		tokens = tokens[:len(tokens)-1]
	}
	for len(tokens) > 0 && tokens[0].kind == 'i' &&
		(tokens[0].value == "await" ||
			tokens[0].value == "return" ||
			tokens[0].value == "void") {
		tokens = tokens[1:]
	}
	for len(tokens) >= 2 && tokens[0].kind == '(' &&
		matchingRepositoryJavaScriptToken(tokens, 0, '(', ')') == len(tokens)-1 {
		tokens = tokens[1 : len(tokens)-1]
	}
	return tokens
}

func repositoryJavaScriptEvaluate(
	scope *repositoryJavaScriptScope,
	tokens []repositoryJavaScriptToken,
) repositoryJavaScriptValue {
	tokens = trimRepositoryJavaScriptTokens(tokens)
	if len(tokens) == 0 {
		return repositoryJavaScriptValue{}
	}
	if len(tokens) == 1 {
		switch tokens[0].kind {
		case 'i':
			value, _ := scope.lookup(tokens[0].value)
			return value
		case 's':
			return repositoryJavaScriptValue{
				strings: map[string]bool{tokens[0].value: true},
			}
		}
	}
	if repositoryJavaScriptExpressionIsChildProcessModule(tokens) {
		return repositoryJavaScriptValue{module: true}
	}
	if tokens[0].kind == '[' {
		close := matchingRepositoryJavaScriptToken(tokens, 0, '[', ']')
		if close == len(tokens)-1 {
			value := repositoryJavaScriptValue{sequenceKnown: true}
			for _, element := range splitRepositoryJavaScriptArguments(
				tokens[1:close],
			) {
				element = trimRepositoryJavaScriptTokens(element)
				if len(element) >= 3 &&
					element[0].kind == '.' &&
					element[1].kind == '.' &&
					element[2].kind == '.' {
					spread := repositoryJavaScriptEvaluate(scope, element[3:])
					if !spread.sequenceKnown {
						return repositoryJavaScriptValue{}
					}
					value.sequence = append(value.sequence, spread.sequence...)
					continue
				}
				value.sequence = append(
					value.sequence,
					repositoryJavaScriptEvaluate(scope, element),
				)
			}
			return value
		}
	}
	if plus := repositoryJavaScriptTopLevelToken(tokens, '+'); plus >= 0 {
		left := repositoryJavaScriptEvaluate(scope, tokens[:plus])
		right := repositoryJavaScriptEvaluate(scope, tokens[plus+1:])
		combined := make(map[string]bool)
		for leftValue := range left.strings {
			for rightValue := range right.strings {
				combined[leftValue+rightValue] = true
			}
		}
		return repositoryJavaScriptValue{strings: combined}
	}
	if len(tokens) >= 3 && tokens[len(tokens)-1].kind == 'i' &&
		tokens[len(tokens)-2].kind == '.' {
		method := tokens[len(tokens)-1].value
		receiver := repositoryJavaScriptEvaluate(
			scope,
			tokens[:len(tokens)-2],
		)
		if receiver.module && repositoryJavaScriptProcessMethod(method) {
			return repositoryJavaScriptValue{
				processMethods: map[string]bool{method: true},
			}
		}
		return repositoryJavaScriptValue{}
	}
	if tokens[len(tokens)-1].kind == ']' {
		open := matchingRepositoryJavaScriptTokenBackward(
			tokens,
			len(tokens)-1,
			'[',
			']',
		)
		if open > 0 {
			property := trimRepositoryJavaScriptTokens(
				tokens[open+1 : len(tokens)-1],
			)
			receiver := repositoryJavaScriptEvaluate(scope, tokens[:open])
			if receiver.module && len(property) == 1 &&
				(property[0].kind == 's' || property[0].kind == 'i') &&
				repositoryJavaScriptProcessMethod(property[0].value) {
				return repositoryJavaScriptValue{
					processMethods: map[string]bool{property[0].value: true},
				}
			}
		}
	}
	return repositoryJavaScriptValue{}
}

func repositoryJavaScriptExpressionIsChildProcessModule(
	tokens []repositoryJavaScriptToken,
) bool {
	tokens = trimRepositoryJavaScriptTokens(tokens)
	if len(tokens) < 3 || tokens[0].kind != 'i' ||
		(tokens[0].value != "require" && tokens[0].value != "import") ||
		tokens[1].kind != '(' {
		return false
	}
	close := matchingRepositoryJavaScriptToken(tokens, 1, '(', ')')
	if close != len(tokens)-1 {
		return false
	}
	arguments := splitRepositoryJavaScriptArguments(tokens[2:close])
	return len(arguments) == 1 &&
		repositoryJavaScriptContainsChildProcessModule(arguments[0])
}

func repositoryJavaScriptContainsChildProcessModule(
	tokens []repositoryJavaScriptToken,
) bool {
	for _, current := range tokens {
		if current.kind == 's' &&
			(current.value == "child_process" ||
				current.value == "node:child_process") {
			return true
		}
	}
	return false
}

func repositoryJavaScriptExpressionStart(
	tokens []repositoryJavaScriptToken,
	end int,
) int {
	start := end
	depth := 0
	for index := end - 1; index >= 0; index-- {
		current := tokens[index]
		switch current.kind {
		case ')', ']':
			depth++
		case '(', '[':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 {
			if current.kind == ';' || current.kind == ',' ||
				current.kind == '=' || current.kind == '{' ||
				current.kind == '}' {
				break
			}
			if current.lineBreakBefore &&
				!repositoryJavaScriptContinuesAcrossLine(
					repositoryJavaScriptToken{kind: 'i'},
					current,
				) {
				start = index
				break
			}
			if index < end-1 && tokens[index+1].lineBreakBefore &&
				!repositoryJavaScriptContinuesAcrossLine(current, tokens[index+1]) {
				break
			}
		}
		start = index
	}
	return start
}

func repositoryJavaScriptProcessCallIsRaw(
	callee repositoryJavaScriptValue,
	arguments []repositoryJavaScriptValue,
) bool {
	for method := range callee.processMethods {
		switch strings.ToLower(method) {
		case "exec", "execsync":
			if len(arguments) == 0 {
				continue
			}
			for command := range arguments[0].strings {
				if repositoryShellSourceLaunchesRawTest(command) {
					return true
				}
			}
		case "execfile", "execfilesync", "spawn", "spawnsync":
			if len(arguments) < 2 {
				continue
			}
			executableIsGo := false
			for executable := range arguments[0].strings {
				if filepath.Base(executable) == "go" {
					executableIsGo = true
					break
				}
			}
			if executableIsGo && arguments[1].sequenceKnown &&
				len(arguments[1].sequence) > 0 &&
				arguments[1].sequence[0].strings["test"] {
				return true
			}
			if !arguments[1].sequenceKnown {
				continue
			}
			executableIsShell := false
			for executable := range arguments[0].strings {
				switch strings.ToLower(filepath.Base(executable)) {
				case "bash", "sh", "zsh":
					executableIsShell = true
				}
			}
			if !executableIsShell {
				continue
			}
			for index, argument := range arguments[1].sequence {
				if !argument.strings["-c"] ||
					index+1 >= len(arguments[1].sequence) {
					continue
				}
				for command := range arguments[1].sequence[index+1].strings {
					if repositoryShellSourceLaunchesRawTest(command) {
						return true
					}
				}
			}
		}
	}
	return false
}

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
