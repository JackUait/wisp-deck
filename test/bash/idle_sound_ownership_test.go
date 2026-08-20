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
	"sort"
	"strconv"
	"strings"
	"testing"
)

type repositoryAuditFile struct {
	mode   string
	source []byte
}

// Runtime sound sites are deliberately few. Go process construction belongs
// to one typed owner, while preference playback stays under the shared lock.
func TestIdleSoundRuntimeSitesUseSharedLiveGate(t *testing.T) {
	root := projectRoot(t)
	const allowed = "cmd/wisp-deck-tui/host_effects.go"
	markers := []string{"afplay", "/System/Library/Sounds", "NSSound", "AudioServicesPlaySystemSound"}
	var violations []string
	shipped := repositoryShippedAssignments()
	for path, file := range trackedRepositoryAuditFiles(t) {
		if path == allowed || isTrackedTestSource(path) ||
			bytes.IndexByte(file.source, 0) >= 0 {
			continue
		}
		compiled := strings.HasSuffix(path, ".go") &&
			(strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "internal/"))
		if !compiled && !isProductionAuditTextPath(path, shipped) {
			continue
		}
		for _, marker := range markers {
			if strings.Contains(string(file.source), marker) {
				violations = append(violations, path)
				break
			}
		}
	}
	sort.Strings(violations)
	if len(violations) != 0 {
		t.Fatalf("new runtime audio sites bypass the live preference gate: %s", strings.Join(violations, ", "))
	}

	expectedCounts := map[string]map[string]int{
		filepath.Join(root, "cmd", "wisp-deck-tui", "host_effects.go"): {
			"afplay": 1, "/System/Library/Sounds": 1, "NSSound": 0, "AudioServicesPlaySystemSound": 0,
		},
	}
	for path, counts := range expectedCounts {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for marker, want := range counts {
			if got := strings.Count(string(data), marker); got != want {
				t.Fatalf("%s contains %d %q markers, want exactly %d audited occurrences", path, got, marker, want)
			}
		}
	}

	shell, err := os.ReadFile(filepath.Join(root, "lib", "notification-setup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateShellNotificationOwnership(string(shell)); err != nil {
		t.Fatal(err)
	}
	background, err := os.ReadFile(filepath.Join(root, "cmd", "wisp-deck-tui", "claude_background.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(background), "withConfiguredNotificationSound(features") ||
		!strings.Contains(string(background), "runHostEffect(soundContext, effect)") {
		t.Fatal("background playback must use the shared locked typed adapter")
	}
	notification, err := os.ReadFile(filepath.Join(root, "cmd", "wisp-deck-tui", "notification_sound.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(notification), "soundpref.WithExclusiveLock(features") ||
		!strings.Contains(string(notification), "sound := soundpref.Read(features)") {
		t.Fatal("notification playback must lock around the canonical live preference")
	}
	menu, err := os.ReadFile(filepath.Join(root, "cmd", "wisp-deck-tui", "main_menu.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMainMenuSoundPreviewOwnership(
		filepath.Join(root, "cmd", "wisp-deck-tui", "main_menu.go"),
		menu,
	); err != nil {
		t.Fatal(err)
	}
	hostEffects, err := os.ReadFile(filepath.Join(root, "cmd", "wisp-deck-tui", "host_effects.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateGoHostEffectOwnership(root, map[string][]byte{
		filepath.Join(root, "cmd", "wisp-deck-tui", "host_effects.go"): hostEffects,
	}); err != nil {
		t.Fatal(err)
	}
	productionBuildCapabilities := map[string]string{
		filepath.Join(root, "Makefile"):              "-X main.SoundPreviewCapability=$(HOST_EFFECTS_CAPABILITY)",
		filepath.Join(root, "scripts", "release.sh"): "-X main.SoundPreviewCapability=enabled",
	}
	for buildPath, capability := range productionBuildCapabilities {
		buildSource, err := os.ReadFile(buildPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(buildSource), capability) != 1 {
			t.Fatalf("%s must explicitly enable previews in production builds", buildPath)
		}
	}

	settings, err := os.ReadFile(filepath.Join(root, "lib", "settings-json.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), `settings["preferredNotifChannel"] = "notifications_disabled"`) {
		t.Fatal("Claude launch overlay must disable the agent's native notification channel")
	}
	codex, err := os.ReadFile(filepath.Join(root, "internal", "codexadapter", "supervisor.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codex), "var filter OSC9Filter") ||
		!strings.Contains(string(codex), "filtered, events := filter.Feed(chunk)") {
		t.Fatal("Codex PTY must consume its private OSC 9 notification before terminal output")
	}
	if strings.Contains(string(codex), "writeFull(s.output(), chunk)") {
		t.Fatal("Codex PTY still forwards the raw notification-bearing chunk")
	}
}

func TestShellNotificationOwnershipGuardRejectsBypasses(t *testing.T) {
	source := repositorySource(t, "lib", "notification-setup.sh")
	if err := validateShellNotificationOwnership(source); err != nil {
		t.Fatalf("current shell notification owner rejected: %v", err)
	}
	const delegate = `wisp-deck-tui notification-sound --features-file "$config_dir/${ai_tool}-features.json" >/dev/null 2>&1 &`
	mutations := map[string]string{
		"reintroduced afplay": strings.Replace(
			source,
			delegate,
			`afplay "$config_dir/chime.aiff" >/dev/null 2>&1 &`,
			1,
		),
		"reintroduced system sound": strings.Replace(
			source,
			delegate,
			`printf '%s\n' "/System/Library/Sounds/Glass.aiff" >/dev/null 2>&1 &`,
			1,
		),
	}
	for name, mutated := range mutations {
		t.Run(name, func(t *testing.T) {
			if mutated == source {
				t.Fatal("shell ownership mutation prerequisite was not found")
			}
			if err := validateShellNotificationOwnership(mutated); err == nil {
				t.Fatal("shell host-effect owner escaped ownership validation")
			}
		})
	}
}

func validateShellNotificationOwnership(source string) error {
	if stringHasHostEffectMarker(source) {
		return fmt.Errorf("notification shell contains a host-effect process literal")
	}
	if strings.Contains(strings.ToLower(source), "player") {
		return fmt.Errorf("notification shell contains a player variable or reference")
	}

	const declaration = "play_notification_sound() {"
	start := strings.Index(source, declaration)
	if start < 0 {
		return fmt.Errorf("notification shell is missing play_notification_sound")
	}
	body := source[start+len(declaration):]
	end := strings.Index(body, "\n}")
	if end < 0 {
		return fmt.Errorf("play_notification_sound has no closing boundary")
	}
	var statements []string
	for _, line := range strings.Split(body[:end], "\n") {
		statement := strings.TrimSpace(line)
		if statement == "" || strings.HasPrefix(statement, "#") {
			continue
		}
		statements = append(statements, statement)
	}
	want := []string{
		`[[ "${WISP_DECK_TESTING:-}" == "1" ]] && return 0`,
		`local ai_tool="$1" config_dir="$2"`,
		`wisp-deck-tui notification-sound --features-file "$config_dir/${ai_tool}-features.json" >/dev/null 2>&1 &`,
	}
	if len(statements) != len(want) {
		return fmt.Errorf("play_notification_sound statements = %d, want exactly %d", len(statements), len(want))
	}
	for index := range want {
		if statements[index] != want[index] {
			return fmt.Errorf(
				"play_notification_sound statement %d = %q, want %q",
				index+1,
				statements[index],
				want[index],
			)
		}
	}
	return nil
}

func TestHostEffectOwnershipInventoryRejectsBypasses(t *testing.T) {
	files := trackedRepositoryAuditFiles(t)
	if err := validateRepositoryHostEffectInventory(files); err != nil {
		t.Fatalf("current repository host-effect inventory rejected: %v", err)
	}

	productionMutations := map[string]string{
		"compiled command": `package future
import "os/exec"
func run() { _ = exec.Command("/usr/bin/afplay", "/tmp/chime.aiff") }
`,
		"compiled internal": `package future
import "os/exec"
func run() { _ = exec.Command("/usr/bin/say", "audit") }
`,
		"compiled direct BEL": `package future
import "fmt"
func run() { fmt.Print("\a") }
`,
		"compiled direct OSC": `package future
import "os"
func run() { _, _ = os.Stdout.Write([]byte("\x1b]9;audit\x07")) }
`,
		"command helper text": "afplay /tmp/chime.aiff\n",
		"shipped bin": `#!/usr/bin/env node
require("child_process").execFileSync("/usr/bin/afplay", ["/tmp/chime.aiff"]);
`,
		"shipped library":  "say audit\n",
		"shipped template": "printf '\\033]9;audit\\007'\n",
		"shipped defaults": "osascript -e 'display notification \"audit\" with sound name \"Glass\"'\n",
		"shipped ghostty":  "printf '\\a'\n",
		"shipped terminal": "printf '\\x07'\n",
		"build script":     "afplay /tmp/chime.aiff\n",
		"test driver":      "say audit\n",
		"workflow":         "run: osascript -e 'display notification \"audit\"'\n",
	}
	productionPaths := map[string]string{
		"compiled command":    "cmd/future/future.go",
		"compiled internal":   "internal/future/future.go",
		"compiled direct BEL": "cmd/ci-report/future_bell.go",
		"compiled direct OSC": "internal/future/output.go",
		"command helper text": "cmd/future/player.sh",
		"shipped bin":         "bin/npx-wisp-deck.js",
		"shipped library":     "lib/future-player.sh",
		"shipped template":    "templates/future-player.sh",
		"shipped defaults":    "defaults/future-player.sh",
		"shipped ghostty":     "ghostty/future-player.sh",
		"shipped terminal":    "terminals/future-player.sh",
		"build script":        "scripts/future-player.sh",
		"test driver":         "run-tests.sh",
		"workflow":            ".github/workflows/tests.yml",
	}
	for name, source := range productionMutations {
		t.Run(name, func(t *testing.T) {
			mutated := cloneRepositoryAuditFiles(files)
			path := productionPaths[name]
			if existing, ok := mutated[path]; ok {
				existing.source = append(append([]byte(nil), existing.source...), []byte(source)...)
				mutated[path] = existing
			} else {
				mutated[path] = repositoryAuditFile{
					mode:   "100755",
					source: []byte(source),
				}
			}
			if err := validateRepositoryHostEffectInventory(mutated); err == nil {
				t.Fatal("production host-effect mutation escaped the repository inventory")
			}
		})
	}

	for _, test := range []struct {
		name   string
		path   string
		source string
	}{
		{
			name: "Go test",
			path: "test/future_audio_test.go",
			source: `package future
import "os/exec"
func TestFuture() { _ = exec.Command("/usr/bin/afplay", "/tmp/chime.aiff") }
`,
		},
		{
			name: "Go test helper",
			path: "test/helpers/audio_helper.go",
			source: `package helpers
import "os/exec"
func Play() { _ = exec.Command("/usr/bin/afplay", "/tmp/chime.aiff").Run() }
`,
		},
		{
			name: "Go test helper alias",
			path: "test/future_audio_test.go",
			source: `package future
import "os/exec"
func run(path string) { _ = exec.Command(path).Run() }
func TestFuture() {
	alias := run
	alias("/usr/bin/afplay")
}
`,
		},
		{
			name: "Go test helper alias chain",
			path: "test/future_audio_test.go",
			source: `package future
import "os/exec"
func run(path string) { _ = exec.Command(path).Run() }
func TestFuture() {
	first := run
	second := first
	second("/usr/bin/say")
}
`,
		},
		{
			name:   "JavaScript spec",
			path:   "future.spec.js",
			source: `require("child_process").execFileSync("/usr/bin/afplay", ["/tmp/chime.aiff"]);`,
		},
		{
			name:   "JavaScript test helper",
			path:   "test/helpers/audio.js",
			source: `require("child_process").execFileSync("/usr/bin/afplay", ["/tmp/chime.aiff"]);`,
		},
		{
			name:   "JSX test",
			path:   "future.test.jsx",
			source: `require("child_process").spawnSync("/usr/bin/say", ["audit"]);`,
		},
		{
			name:   "TypeScript spec",
			path:   "future.spec.ts",
			source: `Bun.spawn(["/usr/bin/afplay", "/tmp/chime.aiff"]);`,
		},
		{
			name:   "TSX test",
			path:   "future.test.tsx",
			source: `Deno.Command("/usr/bin/say", { args: ["audit"] });`,
		},
		{
			name:   "MJS spec",
			path:   "future.spec.mjs",
			source: `process.stdout.write("\\x1b]9;audit\\x07");`,
		},
		{
			name:   "CJS test",
			path:   "future.test.cjs",
			source: `require("node:child_process").execSync("afplay /tmp/chime.aiff");`,
		},
		{
			name:   "relative speech",
			path:   "future.test.js",
			source: `require("child_process").execFileSync("say", ["audit"]);`,
		},
		{
			name:   "constructed player",
			path:   "future.spec.ts",
			source: `require("child_process").spawnSync("af" + "play", ["/tmp/chime.aiff"]);`,
		},
		{
			name:   "template player",
			path:   "future.spec.ts",
			source: "require(\"child_process\").spawnSync(`af${\"play\"}`, [\"/tmp/chime.aiff\"]);",
		},
		{
			name:   "destructured process alias",
			path:   "future.test.js",
			source: `const { execFileSync: run } = require("child_process"); run("say", ["audit"]);`,
		},
		{
			name:   "static namespace process import",
			path:   "future.test.mjs",
			source: `import * as processTools from "node:child_process"; processTools.spawnSync("/usr/bin/afplay", ["/tmp/chime.aiff"]);`,
		},
		{
			name:   "static named process import",
			path:   "future.test.mjs",
			source: `import { execFileSync as run } from "node:child_process"; run("/usr/bin/say", ["audit"]);`,
		},
		{
			name: "computed process method alias",
			path: "future.test.js",
			source: `const processTools = require("node:child_process");
const method = "spawn" + "Sync";
processTools[method]("/usr/bin/afplay", ["/tmp/chime.aiff"]);`,
		},
		{
			name: "JavaScript process helper",
			path: "future.test.js",
			source: `const cp = require("child_process");
function run(path) { cp.execFileSync(path, ["audit"]); }
run("/usr/bin/say");`,
		},
		{
			name: "JavaScript shadowed process helper argument",
			path: "future.test.js",
			source: `const processTools = require("node:child_process");
function run(processTools) {
  processTools.spawnSync("/usr/bin/afplay", []);
}
run(processTools);`,
		},
		{
			name: "JavaScript helper chain",
			path: "future.test.js",
			source: `const cp = require("child_process");
function run(path) { cp.execFileSync(path, ["audit"]); }
function relay(path) { run(path); }
relay("/usr/bin/afplay");`,
		},
		{
			name: "JavaScript helper alias",
			path: "future.test.js",
			source: `const cp = require("child_process");
function run(path) { cp.execFileSync(path, ["audit"]); }
const alias = run;
alias("/usr/bin/afplay");`,
		},
		{
			name: "JavaScript helper alias chain",
			path: "future.test.js",
			source: `const cp = require("child_process");
function run(path) { cp.execFileSync(path, ["audit"]); }
const first = run;
const second = first;
second("/usr/bin/say");`,
		},
		{
			name: "JavaScript arrow helper",
			path: "future.test.js",
			source: `const cp = require("child_process");
const run = path => cp.execFileSync(path, ["audit"]);
run("/usr/bin/afplay");`,
		},
		{
			name: "JavaScript function-expression helper",
			path: "future.test.js",
			source: `const cp = require("child_process");
const run = function(path) { cp.execFileSync(path, ["audit"]); };
run("/usr/bin/say");`,
		},
		{
			name: "JavaScript object method helper",
			path: "future.test.js",
			source: `const cp = require("child_process");
const runner = {
  run(path) { cp.execFileSync(path, ["audit"]); },
};
runner.run("/usr/bin/afplay");`,
		},
		{
			name: "JavaScript class method helper",
			path: "future.test.js",
			source: `const cp = require("child_process");
class Runner {
  run(path) { cp.execFileSync(path, ["audit"]); }
}
const runner = new Runner();
runner.run("/usr/bin/say");`,
		},
		{
			name:   "direct fs output",
			path:   "future.test.js",
			source: `require("fs").writeSync(1, "\x07");`,
		},
		{
			name:   "numeric Buffer BEL",
			path:   "future.test.js",
			source: `require("fs").writeSync(1, Buffer.from([7]));`,
		},
		{
			name:   "bracket stdout output",
			path:   "future.test.js",
			source: `process["stdout"].write("\x1b]9;audit\x07");`,
		},
		{
			name:   "aliased stdout output",
			path:   "future.test.js",
			source: `const output = process.stdout; output["write"]("\x07");`,
		},
		{
			name:   "aliased fs output",
			path:   "future.test.js",
			source: `const files = require("fs"); const emit = files.writeSync; emit(1, "\x07");`,
		},
		{
			name:   "console terminal output",
			path:   "future.test.js",
			source: `console.log("\x07");`,
		},
		{
			name:   "String fromCharCode BEL",
			path:   "future.test.js",
			source: `process.stdout.write(String.fromCharCode(7));`,
		},
		{
			name:   "shell test",
			path:   "test/future_audio_test.sh",
			source: "afplay /tmp/chime.aiff\n",
		},
		{
			name:   "extensionless shell test",
			path:   "test/future-audio",
			source: "#!/bin/bash\nsay audit\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneRepositoryAuditFiles(files)
			mutated[test.path] = repositoryAuditFile{
				mode:   "100644",
				source: []byte(test.source),
			}
			if err := validateRepositoryHostEffectInventory(mutated); err == nil {
				t.Fatal("test host-effect launch escaped the repository inventory")
			}
		})
	}

	t.Run("JavaScript comments and unrelated processes are harmless", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["cmd/future/safe.js"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`// afplay is a forbidden fixture
const fixture = "afplay";
require("child_process").spawnSync("git", ["-C", "/tmp/wisp-deck-tui", "status"]);
const object = { exec() {} };
object.exec("afplay");
const harmlessArrow = value => String(value);
harmlessArrow("afplay");
require("fs").writeSync(1, Buffer.alloc(7));
`),
		}
		if err := validateRepositoryHostEffectInventory(mutated); err != nil {
			t.Fatalf("harmless JavaScript audit fixture rejected: %v", err)
		}
	})

	t.Run("JavaScript lexical shadows and unrelated methods are harmless", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["cmd/future/shadowed.js"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`const processTools = require("node:child_process");
const { spawnSync: run } = processTools;
function shadowedModule(processTools) {
  processTools.spawnSync("/usr/bin/afplay", []);
}
function shadowedFunction(run) {
  run("/usr/bin/say", []);
}
const recorder = {
  run(value) { return String(value); },
};
class Recorder {
  run(value) { return String(value); }
}
shadowedModule({ spawnSync() {} });
shadowedFunction(() => {});
recorder.run("/usr/bin/afplay");
new Recorder().run("/usr/bin/say");
`),
		}
		if err := validateRepositoryHostEffectInventory(mutated); err != nil {
			t.Fatalf("harmless JavaScript shadow fixture rejected: %v", err)
		}
	})

	t.Run("JavaScript local binding shadows are harmless", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["cmd/future/local-shadowed.js"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`const processTools = require("node:child_process");
const { spawnSync: run } = processTools;
function locallyShadowedModule(path) {
  const processTools = { spawnSync() {} };
  processTools.spawnSync(path, []);
}
function locallyShadowedFunction(path) {
  const run = () => {};
  run(path, []);
}
const files = require("node:fs");
const emit = files.writeSync;
function locallyShadowedOutput(value) {
  const files = { writeSync() {} };
  const emit = () => {};
  files.writeSync(1, value);
  emit(1, value);
}
locallyShadowedModule("/usr/bin/afplay");
locallyShadowedFunction("/usr/bin/say");
locallyShadowedOutput("\x07");
`),
		}
		if err := validateRepositoryHostEffectInventory(mutated); err != nil {
			t.Fatalf("harmless JavaScript local shadow fixture rejected: %v", err)
		}
	})

	t.Run("JavaScript same-named object owners stay distinct", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["cmd/future/object-shadowed.js"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`const processTools = require("node:child_process");
function locallyShadowedOwner() {
  const runner = {
    run(value) { return String(value); },
  };
  runner.run("/usr/bin/afplay");
}
const runner = {
  run(path) { processTools.execFileSync(path, []); },
};
function parameterShadowedOwner(runner) {
  runner.run("/usr/bin/say");
}
const recorder = {
  relay(runner) {
    runner.run("/usr/bin/afplay");
  },
};
locallyShadowedOwner();
parameterShadowedOwner({ run() {} });
recorder.relay({ run() {} });
`),
		}
		if err := validateRepositoryHostEffectInventory(mutated); err != nil {
			t.Fatalf("same-named JavaScript object owners crossed provenance: %v", err)
		}
	})

	t.Run("Go helper parameter lexical shadow is harmless", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_shadow_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import "os/exec"
func run(path string) {
	{
		path := "git"
		_ = exec.Command(path, "status").Run()
	}
}
func TestFuture() { run("/usr/bin/afplay") }
`),
		}
		if err := validateRepositoryHostEffectInventory(mutated); err != nil {
			t.Fatalf("harmless Go helper parameter shadow rejected: %v", err)
		}
	})

	t.Run("JavaScript helper parameter lexical shadow is harmless", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["future.test.js"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`const cp = require("child_process");
function run(path) {
  {
    const path = "git";
    cp.execFileSync(path, ["status"]);
  }
}
run("/usr/bin/afplay");`),
		}
		if err := validateRepositoryHostEffectInventory(mutated); err != nil {
			t.Fatalf("harmless JavaScript helper parameter shadow rejected: %v", err)
		}
	})

	t.Run("new shipped root", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		var manifest map[string]any
		if err := json.Unmarshal(mutated["package.json"].source, &manifest); err != nil {
			t.Fatal(err)
		}
		filesValue, ok := manifest["files"].([]any)
		if !ok {
			t.Fatal("package.json files mutation prerequisite missing")
		}
		manifest["files"] = append(filesValue, "future/")
		source, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		packageFile := mutated["package.json"]
		packageFile.source = source
		mutated["package.json"] = packageFile
		mutated["future/safe.txt"] = repositoryAuditFile{
			mode:   "100644",
			source: []byte("safe\n"),
		}
		if err := validateRepositoryHostEffectInventory(mutated); err == nil {
			t.Fatal("new package.json shipped root escaped inventory assignment")
		}
	})

	t.Run("npm lifecycle host effect", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		var manifest map[string]any
		if err := json.Unmarshal(mutated["package.json"].source, &manifest); err != nil {
			t.Fatal(err)
		}
		manifest["scripts"] = map[string]string{
			"postinstall": "afplay /tmp/chime.aiff",
		}
		source, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		packageFile := mutated["package.json"]
		packageFile.source = source
		mutated["package.json"] = packageFile
		if err := validateRepositoryHostEffectInventory(mutated); err == nil {
			t.Fatal("npm lifecycle host effect escaped the repository inventory")
		}
	})

	for name, mutation := range map[string]repositoryAuditFile{
		"thin Mach-O": {
			mode:   "100755",
			source: append([]byte{0xcf, 0xfa, 0xed, 0xfe}, make([]byte, 32)...),
		},
		"non-executable fat Mach-O": {
			mode:   "100644",
			source: append([]byte{0xca, 0xfe, 0xba, 0xbe}, make([]byte, 32)...),
		},
		"executable binary": {
			mode:   "100755",
			source: []byte{0x01, 0x00, 0x02, 0x00},
		},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := cloneRepositoryAuditFiles(files)
			mutated["future-binary"] = mutation
			if err := validateRepositoryHostEffectInventory(mutated); err == nil {
				t.Fatal("tracked binary artifact escaped the repository inventory")
			}
		})
	}
}

func trackedRepositoryAuditFiles(t *testing.T) map[string]repositoryAuditFile {
	t.Helper()
	root := projectRoot(t)
	files, err := loadTrackedRepositoryAuditFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func loadTrackedRepositoryAuditFiles(
	root string,
) (map[string]repositoryAuditFile, error) {
	output, err := exec.Command(
		"git",
		"-C",
		root,
		"ls-files",
		"-s",
		"-z",
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf(
			"discover tracked repository inventory: %w\n%s",
			err,
			output,
		)
	}
	files := make(map[string]repositoryAuditFile)
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		header, pathBytes, ok := bytes.Cut(record, []byte{'\t'})
		fields := bytes.Fields(header)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf(
				"malformed git ls-files inventory record %q",
				record,
			)
		}
		path := string(pathBytes)
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf(
				"read tracked inventory file %s: %w",
				path,
				err,
			)
		}
		files[path] = repositoryAuditFile{
			mode:   string(fields[0]),
			source: source,
		}
	}
	return files, nil
}

func cloneRepositoryAuditFiles(
	files map[string]repositoryAuditFile,
) map[string]repositoryAuditFile {
	cloned := make(map[string]repositoryAuditFile, len(files))
	for path, file := range files {
		cloned[path] = repositoryAuditFile{
			mode:   file.mode,
			source: append([]byte(nil), file.source...),
		}
	}
	return cloned
}

func validateRepositoryHostEffectInventory(
	files map[string]repositoryAuditFile,
) error {
	for path, file := range files {
		if isMachO(file.source) {
			return fmt.Errorf("%s is a tracked Mach-O", path)
		}
		if file.mode == "100755" && bytes.IndexByte(file.source, 0) >= 0 {
			return fmt.Errorf("%s is a tracked executable binary artifact", path)
		}
	}

	packageFile, ok := files["package.json"]
	if !ok {
		return fmt.Errorf("tracked inventory is missing package.json")
	}
	var manifest struct {
		Files   []string          `json:"files"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(packageFile.source, &manifest); err != nil {
		return fmt.Errorf("parse package.json audit inventory: %w", err)
	}
	versionFile, ok := files["VERSION"]
	if !ok {
		return fmt.Errorf("tracked inventory is missing VERSION")
	}
	var packageMetadata struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(packageFile.source, &packageMetadata); err != nil {
		return fmt.Errorf("parse package.json version: %w", err)
	}
	if version := strings.TrimSpace(string(versionFile.source)); version == "" || version != packageMetadata.Version {
		return fmt.Errorf(
			"VERSION metadata %q does not equal package version %q",
			version,
			packageMetadata.Version,
		)
	}
	shippedAssignments := repositoryShippedAssignments()
	seenShipped := make(map[string]bool, len(manifest.Files))
	for _, shipped := range manifest.Files {
		if _, assigned := shippedAssignments[shipped]; !assigned {
			return fmt.Errorf("package.json ships unassigned inventory root %q", shipped)
		}
		if seenShipped[shipped] {
			return fmt.Errorf("package.json repeats shipped inventory root %q", shipped)
		}
		seenShipped[shipped] = true
	}
	for shipped := range shippedAssignments {
		if !seenShipped[shipped] {
			return fmt.Errorf("package.json omitted audited shipped root %q", shipped)
		}
	}
	for name, command := range manifest.Scripts {
		if shellProductionLineHasHostEffect(command) {
			return fmt.Errorf("package.json lifecycle script %q owns a host effect", name)
		}
	}

	productionText := make(map[string]string)
	for path, file := range files {
		if isTrackedTestSource(path) {
			if trackedTestSourceLaunchesHostEffect(path, file.source) {
				return fmt.Errorf("%s can launch a host effect from test source", path)
			}
			continue
		}
		if strings.HasSuffix(path, ".go") &&
			(strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "internal/")) {
			if path == "cmd/wisp-deck-tui/host_effects.go" {
				continue
			}
			if productionSourceLaunchesHostEffect(path, file.source) ||
				sourceHasHostEffectLiteral(path, file.source) {
				return fmt.Errorf("%s owns an unaudited compiled host effect", path)
			}
			continue
		}
		if isJavaScriptAuditPath(path) &&
			isProductionAuditTextPath(path, shippedAssignments) {
			if javascriptSourceLaunchesHostEffect(file.source) {
				return fmt.Errorf("%s owns an unaudited JavaScript host effect", path)
			}
			continue
		}
		if path == "VERSION" || !isProductionAuditTextPath(path, shippedAssignments) ||
			path == "package.json" ||
			bytes.IndexByte(file.source, 0) >= 0 {
			continue
		}
		productionText[path] = string(file.source)
	}
	if err := validateShellProductionHostEffectOwnership(productionText); err != nil {
		return err
	}
	return nil
}

func repositoryShippedAssignments() map[string]string {
	return map[string]string{
		"bin/wisp-deck":        "shipped-text",
		"bin/wisp-deck-config": "shipped-text",
		"bin/npx-wisp-deck.js": "shipped-text",
		"lib/":                 "shipped-text",
		"templates/":           "shipped-text",
		"defaults/":            "shipped-text",
		"ghostty/":             "shipped-text",
		"terminals/":           "shipped-text",
		"wrapper.sh":           "shipped-text",
		"VERSION":              "metadata-only",
	}
}

func isProductionAuditTextPath(
	path string,
	shippedAssignments map[string]string,
) bool {
	if strings.HasPrefix(path, "cmd/") ||
		strings.HasPrefix(path, "internal/") ||
		path == "package.json" || path == "Makefile" || path == "run-tests.sh" ||
		path == ".github/workflows/tests.yml" ||
		path == ".github/workflows/install.yml" ||
		strings.HasPrefix(path, "scripts/") {
		return true
	}
	for shipped, classification := range shippedAssignments {
		if classification == "metadata-only" {
			continue
		}
		if strings.HasSuffix(shipped, "/") {
			if strings.HasPrefix(path, shipped) {
				return true
			}
			continue
		}
		if path == shipped {
			return true
		}
	}
	return false
}

func isTrackedTestSource(path string) bool {
	slashPath := filepath.ToSlash(path)
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_test.go") ||
		(strings.HasPrefix(slashPath, "test/") &&
			strings.HasSuffix(base, ".go")) {
		return true
	}
	for _, extension := range []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"} {
		if strings.HasSuffix(base, ".spec"+extension) ||
			strings.HasSuffix(base, ".test"+extension) ||
			(strings.HasPrefix(slashPath, "test/") &&
				strings.HasSuffix(base, extension)) {
			return true
		}
	}
	shellTest := strings.HasSuffix(base, ".sh") ||
		strings.HasSuffix(base, ".bash") ||
		strings.HasSuffix(base, ".zsh") ||
		strings.HasSuffix(base, ".bats") ||
		!strings.Contains(base, ".")
	return shellTest &&
		(strings.HasPrefix(slashPath, "test/") ||
			strings.HasSuffix(base, "_test.sh") ||
			strings.HasSuffix(base, ".test.sh") ||
			strings.HasSuffix(base, ".spec.sh"))
}

func trackedTestSourceLaunchesHostEffect(path string, source []byte) bool {
	if strings.HasSuffix(path, ".go") {
		return testSourceLaunchesHostAudio(path, source)
	}
	if !isJavaScriptAuditPath(path) {
		for _, line := range strings.Split(string(source), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") &&
				shellProductionLineHasHostEffect(trimmed) {
				return true
			}
		}
		return false
	}
	return javascriptSourceLaunchesHostEffect(source)
}

type javascriptAuditToken struct {
	kind     byte
	value    string
	position int
}

func isJavaScriptAuditPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}

func javascriptSourceLaunchesHostEffect(source []byte) bool {
	if repositoryJavaScriptSourceLaunchesHostEffect(string(source)) {
		return true
	}
	tokens, ok := lexJavaScriptAudit(string(source))
	if !ok {
		return true
	}
	constants := javascriptStringConstants(tokens)
	bindings := collectJavaScriptAuditBindings(tokens, constants)
	methodOwners := javascriptAuditMethodOwners(tokens)
	parameterUses := collectJavaScriptHostEffectParameterUses(
		tokens,
		bindings,
		constants,
	)
	for index, token := range tokens {
		if token.kind != '(' {
			continue
		}
		if index >= 2 && tokens[index-2].kind == 'i' &&
			tokens[index-2].value == "function" {
			continue
		}
		end := matchingJavaScriptToken(tokens, index, '(', ')')
		if end < 0 {
			return true
		}
		method := javascriptCallMethod(tokens, index)
		arguments := splitJavaScriptArguments(tokens[index+1 : end])
		sensitive := javascriptCallInvokesSensitiveHostEffect(
			method,
			arguments,
			parameterUses,
			constants,
			tokens,
			index,
			bindings,
		)
		if binding := javascriptAuditBareFunctionKey(
			tokens,
			index,
			index,
			methodOwners,
		); binding != "" {
			sensitive = sensitive ||
				javascriptCallInvokesSensitiveHostEffect(
					binding,
					arguments,
					parameterUses,
					constants,
					tokens,
					index,
					bindings,
				)
		}
		if qualified := javascriptAuditQualifiedMethodKey(
			tokens,
			index,
			index,
			methodOwners,
		); qualified != "" {
			sensitive = sensitive ||
				javascriptCallInvokesSensitiveHostEffect(
					qualified,
					arguments,
					parameterUses,
					constants,
					tokens,
					index,
					bindings,
				)
		}
		if sensitive {
			return true
		}
		outputMethod, outputCall := javascriptScopedOutputMethod(
			tokens,
			index,
			method,
			bindings,
			nil,
		)
		if !outputCall {
			continue
		}
		outputArguments := arguments
		if outputMethod == "writesync" {
			if !javascriptOutputFileDescriptor(arguments) {
				continue
			}
			outputArguments = arguments[1:]
		}
		if javascriptArgumentsHaveTerminalEffect(
			outputArguments,
			constants,
		) {
			return true
		}
	}
	return false
}

func repositoryJavaScriptSourceLaunchesHostEffect(source string) bool {
	tokens, ok := lexRepositoryJavaScript(source)
	if !ok {
		return true
	}
	scope := &repositoryJavaScriptScope{
		bindings: map[string]repositoryJavaScriptValue{
			"Bun":  {module: true},
			"Deno": {module: true},
		},
	}
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
					scope.declare(
						tokens[index+1].value,
						repositoryJavaScriptValue{},
					)
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
					scope.assign(
						current.value,
						repositoryJavaScriptEvaluate(
							scope,
							tokens[index+2:end],
						),
					)
				}
			}
		}
		if current.kind != '(' {
			continue
		}
		end := matchingRepositoryJavaScriptToken(tokens, index, '(', ')')
		if end < 0 {
			return true
		}
		start := repositoryJavaScriptExpressionStart(tokens, index)
		callee := repositoryJavaScriptHostProcessCallee(
			scope,
			tokens[start:index],
		)
		if len(callee.processMethods) == 0 {
			continue
		}
		argumentTokens := splitRepositoryJavaScriptArguments(
			tokens[index+1 : end],
		)
		arguments := make(
			[]repositoryJavaScriptValue,
			len(argumentTokens),
		)
		for argumentIndex, argument := range argumentTokens {
			arguments[argumentIndex] = repositoryJavaScriptEvaluate(
				scope,
				argument,
			)
		}
		if repositoryJavaScriptHostProcessCallIsEffect(callee, arguments) {
			return true
		}
	}
	return false
}

func repositoryJavaScriptHostProcessCallee(
	scope *repositoryJavaScriptScope,
	tokens []repositoryJavaScriptToken,
) repositoryJavaScriptValue {
	value := repositoryJavaScriptEvaluate(scope, tokens)
	if len(value.processMethods) != 0 {
		return value
	}
	tokens = trimRepositoryJavaScriptTokens(tokens)
	if len(tokens) >= 3 && tokens[len(tokens)-2].kind == '.' &&
		tokens[len(tokens)-1].kind == 'i' {
		method := tokens[len(tokens)-1].value
		receiver := repositoryJavaScriptEvaluate(
			scope,
			tokens[:len(tokens)-2],
		)
		if receiver.module &&
			(javascriptProcessMethod(method) ||
				strings.EqualFold(method, "command")) {
			return repositoryJavaScriptValue{
				processMethods: map[string]bool{method: true},
			}
		}
	}
	if len(tokens) > 3 && tokens[len(tokens)-1].kind == ']' {
		open := matchingRepositoryJavaScriptTokenBackward(
			tokens,
			len(tokens)-1,
			'[',
			']',
		)
		if open > 0 {
			receiver := repositoryJavaScriptEvaluate(scope, tokens[:open])
			property := repositoryJavaScriptEvaluate(
				scope,
				tokens[open+1:len(tokens)-1],
			)
			if receiver.module {
				methods := make(map[string]bool)
				for method := range property.strings {
					if javascriptProcessMethod(method) ||
						strings.EqualFold(method, "command") {
						methods[method] = true
					}
				}
				if len(methods) != 0 {
					return repositoryJavaScriptValue{
						processMethods: methods,
					}
				}
			}
		}
	}
	return repositoryJavaScriptValue{}
}

func repositoryJavaScriptHostProcessCallIsEffect(
	callee repositoryJavaScriptValue,
	arguments []repositoryJavaScriptValue,
) bool {
	for method := range callee.processMethods {
		switch strings.ToLower(method) {
		case "exec", "execsync":
			if len(arguments) > 0 &&
				repositoryJavaScriptValueHasHostEffect(arguments[0]) {
				return true
			}
		case "execfile", "execfilesync", "spawn", "spawnsync", "command":
			if len(arguments) == 0 {
				continue
			}
			executable := arguments[0]
			if executable.sequenceKnown && len(executable.sequence) > 0 {
				executable = executable.sequence[0]
			}
			if repositoryJavaScriptValueHasHostEffect(executable) {
				return true
			}
			if repositoryJavaScriptValueNamesShell(executable) {
				for _, argument := range arguments[1:] {
					if repositoryJavaScriptValueHasHostEffect(argument) {
						return true
					}
				}
			}
		}
	}
	return false
}

func repositoryJavaScriptValueHasHostEffect(
	value repositoryJavaScriptValue,
) bool {
	for item := range value.strings {
		if javascriptStringHasHostEffectMarker(item) ||
			strings.ContainsRune(item, '\a') ||
			shellLineHasBellEscape(item) {
			return true
		}
	}
	for _, item := range value.sequence {
		if repositoryJavaScriptValueHasHostEffect(item) {
			return true
		}
	}
	return false
}

func repositoryJavaScriptValueNamesShell(
	value repositoryJavaScriptValue,
) bool {
	for item := range value.strings {
		switch strings.ToLower(filepath.Base(item)) {
		case "sh", "bash", "zsh", "env":
			return true
		}
	}
	return false
}

func lexJavaScriptAudit(source string) ([]javascriptAuditToken, bool) {
	tokens := make([]javascriptAuditToken, 0, len(source)/3)
	for index := 0; index < len(source); {
		character := source[index]
		if character == ' ' || character == '\t' ||
			character == '\r' || character == '\n' {
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
				continue
			case '*':
				end := strings.Index(source[index+2:], "*/")
				if end < 0 {
					return nil, false
				}
				index += end + 4
				continue
			}
		}
		if character == '\'' || character == '"' || character == '`' {
			value, next, ok := readJavaScriptString(source, index, character)
			if !ok {
				return nil, false
			}
			tokens = append(tokens, javascriptAuditToken{
				kind:     's',
				value:    value,
				position: len(tokens),
			})
			index = next
			continue
		}
		if isJavaScriptIdentifierStart(character) {
			start := index
			index++
			for index < len(source) && isJavaScriptIdentifierPart(source[index]) {
				index++
			}
			tokens = append(tokens, javascriptAuditToken{
				kind:     'i',
				value:    source[start:index],
				position: len(tokens),
			})
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
			tokens = append(tokens, javascriptAuditToken{
				kind:     'n',
				value:    source[start:index],
				position: len(tokens),
			})
			continue
		}
		tokens = append(tokens, javascriptAuditToken{
			kind:     character,
			value:    string(character),
			position: len(tokens),
		})
		index++
	}
	return tokens, true
}

func isJavaScriptIdentifierStart(character byte) bool {
	return character == '_' || character == '$' ||
		(character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z')
}

func isJavaScriptIdentifierPart(character byte) bool {
	return isJavaScriptIdentifierStart(character) ||
		(character >= '0' && character <= '9')
}

func readJavaScriptString(
	source string,
	start int,
	quote byte,
) (string, int, bool) {
	var value strings.Builder
	for index := start + 1; index < len(source); index++ {
		character := source[index]
		if character == quote {
			return value.String(), index + 1, true
		}
		if character != '\\' {
			value.WriteByte(character)
			continue
		}
		index++
		if index >= len(source) {
			return "", 0, false
		}
		escaped := source[index]
		switch escaped {
		case '\n':
			continue
		case '\r':
			if index+1 < len(source) && source[index+1] == '\n' {
				index++
			}
			continue
		case 'b':
			value.WriteByte('\b')
		case 'f':
			value.WriteByte('\f')
		case 'n':
			value.WriteByte('\n')
		case 'r':
			value.WriteByte('\r')
		case 't':
			value.WriteByte('\t')
		case 'v':
			value.WriteByte('\v')
		case 'x':
			decoded, next, ok := decodeJavaScriptHex(source, index+1, 2)
			if !ok {
				return "", 0, false
			}
			value.WriteRune(decoded)
			index = next - 1
		case 'u':
			decoded, next, ok := decodeJavaScriptHex(source, index+1, 4)
			if !ok {
				return "", 0, false
			}
			value.WriteRune(decoded)
			index = next - 1
		default:
			if escaped >= '0' && escaped <= '7' {
				end := index + 1
				for end < len(source) && end < index+3 &&
					source[end] >= '0' && source[end] <= '7' {
					end++
				}
				decoded, err := strconv.ParseInt(source[index:end], 8, 32)
				if err != nil {
					return "", 0, false
				}
				value.WriteRune(rune(decoded))
				index = end - 1
			} else {
				value.WriteByte(escaped)
			}
		}
	}
	return "", 0, false
}

func decodeJavaScriptHex(
	source string,
	start int,
	width int,
) (rune, int, bool) {
	if start+width > len(source) {
		return 0, 0, false
	}
	decoded, err := strconv.ParseInt(source[start:start+width], 16, 32)
	if err != nil {
		return 0, 0, false
	}
	return rune(decoded), start + width, true
}

func javascriptStringConstants(
	tokens []javascriptAuditToken,
) map[string]string {
	constants := make(map[string]string)
	for index := 0; index+3 < len(tokens); index++ {
		if tokens[index].kind != 'i' ||
			(tokens[index].value != "const" &&
				tokens[index].value != "let" &&
				tokens[index].value != "var") ||
			tokens[index+1].kind != 'i' ||
			tokens[index+2].kind != '=' {
			continue
		}
		end := index + 3
		depth := 0
		for end < len(tokens) {
			if tokens[end].kind == '(' || tokens[end].kind == '[' ||
				tokens[end].kind == '{' {
				depth++
			}
			if tokens[end].kind == ')' || tokens[end].kind == ']' ||
				tokens[end].kind == '}' {
				depth--
			}
			if depth == 0 && (tokens[end].kind == ';' || tokens[end].kind == ',') {
				break
			}
			end++
		}
		if value, ok := javascriptConstantString(
			tokens[index+3:end],
			constants,
		); ok {
			constants[tokens[index+1].value] = value
		}
	}
	return constants
}

func javascriptConstantString(
	tokens []javascriptAuditToken,
	constants map[string]string,
) (string, bool) {
	var value strings.Builder
	expectValue := true
	values := 0
	for _, token := range tokens {
		switch token.kind {
		case '(', ')':
			continue
		case '+':
			if expectValue || values == 0 {
				return "", false
			}
			expectValue = true
		case 's':
			if !expectValue {
				return "", false
			}
			value.WriteString(token.value)
			expectValue = false
			values++
		case 'i':
			if !expectValue {
				return "", false
			}
			resolved, ok := constants[token.value]
			if !ok {
				return "", false
			}
			value.WriteString(resolved)
			expectValue = false
			values++
		default:
			return "", false
		}
	}
	return value.String(), values > 0 && !expectValue
}

func matchingJavaScriptToken(
	tokens []javascriptAuditToken,
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

func javascriptCallMethod(
	tokens []javascriptAuditToken,
	open int,
) string {
	if open == 0 {
		return ""
	}
	if tokens[open-1].kind == 'i' {
		return tokens[open-1].value
	}
	if tokens[open-1].kind == ']' {
		for index := open - 2; index >= 0; index-- {
			if tokens[index].kind == '[' {
				if index+1 < open-1 &&
					(tokens[index+1].kind == 's' ||
						tokens[index+1].kind == 'i') {
					return tokens[index+1].value
				}
				break
			}
		}
	}
	return ""
}

func javascriptCallHasExplicitReceiver(
	tokens []javascriptAuditToken,
	open int,
) bool {
	if open < 2 {
		return false
	}
	if tokens[open-1].kind == ']' {
		return true
	}
	return tokens[open-2].kind == '.'
}

func javascriptIdentifierIsFunctionParameterAt(
	tokens []javascriptAuditToken,
	position int,
	name string,
) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(tokens); index++ {
		if tokens[index].kind == 'i' && tokens[index].value == "function" {
			parametersStart := index + 1
			if parametersStart < len(tokens) &&
				tokens[parametersStart].kind == 'i' {
				parametersStart++
			}
			if parametersStart >= len(tokens) ||
				tokens[parametersStart].kind != '(' {
				continue
			}
			parametersEnd := matchingJavaScriptToken(
				tokens,
				parametersStart,
				'(',
				')',
			)
			if parametersEnd < 0 || parametersEnd+1 >= len(tokens) ||
				tokens[parametersEnd+1].kind != '{' {
				continue
			}
			bodyEnd := matchingJavaScriptToken(
				tokens,
				parametersEnd+1,
				'{',
				'}',
			)
			if bodyEnd > position && parametersEnd+1 < position &&
				javascriptTokensContainIdentifier(
					tokens[parametersStart+1:parametersEnd],
					name,
				) {
				return true
			}
			continue
		}
		if tokens[index].kind != '>' || index == 0 ||
			tokens[index-1].kind != '=' || index+1 >= len(tokens) ||
			tokens[index+1].kind != '{' {
			continue
		}
		bodyEnd := matchingJavaScriptToken(tokens, index+1, '{', '}')
		if bodyEnd <= position || index+1 >= position {
			continue
		}
		parametersStart := index - 2
		parametersEnd := index - 1
		if parametersStart >= 0 && tokens[parametersStart].kind == ')' {
			parametersStart = matchingJavaScriptTokenBackward(
				tokens,
				parametersStart,
				'(',
				')',
			)
		}
		if parametersStart >= 0 &&
			javascriptTokensContainIdentifier(
				tokens[parametersStart:parametersEnd],
				name,
			) {
			return true
		}
	}
	return false
}

func matchingJavaScriptTokenBackward(
	tokens []javascriptAuditToken,
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

func splitJavaScriptArguments(
	tokens []javascriptAuditToken,
) [][]javascriptAuditToken {
	var arguments [][]javascriptAuditToken
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

func javascriptExpressionStrings(
	tokens []javascriptAuditToken,
	constants map[string]string,
) []string {
	values := make([]string, 0)
	if value, ok := javascriptConstantString(tokens, constants); ok {
		values = append(values, value)
	}
	for _, token := range tokens {
		switch token.kind {
		case 's':
			values = append(values, token.value)
		case 'i':
			if value, ok := constants[token.value]; ok {
				values = append(values, value)
			}
		}
	}
	return values
}

func javascriptExpressionHasHostEffect(
	tokens []javascriptAuditToken,
	constants map[string]string,
) bool {
	for _, value := range javascriptExpressionStrings(tokens, constants) {
		if javascriptStringHasHostEffectMarker(value) ||
			strings.ContainsRune(value, '\a') ||
			shellLineHasBellEscape(value) {
			return true
		}
	}
	return false
}

func javascriptProcessArgumentsHaveHostEffect(
	arguments [][]javascriptAuditToken,
	constants map[string]string,
) bool {
	if len(arguments) == 0 {
		return false
	}
	values := javascriptExpressionStrings(arguments[0], constants)
	if len(values) == 0 {
		return false
	}
	executable := strings.ToLower(filepath.Base(values[0]))
	if javascriptStringHasHostEffectMarker(values[0]) ||
		executable == "afplay" || executable == "say" ||
		executable == "osascript" {
		return true
	}
	switch executable {
	case "sh", "bash", "zsh", "env":
		for _, argument := range arguments {
			if javascriptExpressionHasHostEffect(argument, constants) {
				return true
			}
		}
	}
	return false
}

func javascriptStringHasHostEffectMarker(value string) bool {
	if stringHasHostEffectMarker(value) {
		return true
	}
	var compact strings.Builder
	for _, character := range strings.ToLower(value) {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '/' {
			compact.WriteRune(character)
		}
	}
	normalized := compact.String()
	for _, marker := range []string{
		"afplay",
		"osascript",
		"/usr/bin/say",
		"/system/library/sounds/",
		"nssound",
		"audioservicesplaysystemsound",
		"displaynotification",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return normalized == "say"
}

func javascriptArgumentsHaveTerminalEffect(
	arguments [][]javascriptAuditToken,
	constants map[string]string,
) bool {
	for _, argument := range arguments {
		if javascriptExpressionHasNumericBEL(argument) {
			return true
		}
		for _, value := range javascriptExpressionStrings(argument, constants) {
			if strings.ContainsRune(value, '\a') ||
				strings.Contains(value, "]9;") ||
				shellLineHasBellEscape(value) {
				return true
			}
		}
	}
	return false
}

func javascriptExpressionHasNumericBEL(tokens []javascriptAuditToken) bool {
	for index, token := range tokens {
		if token.kind != '(' {
			continue
		}
		end := matchingJavaScriptToken(tokens, index, '(', ')')
		if end < 0 {
			continue
		}
		method := strings.ToLower(javascriptCallMethod(tokens, index))
		byteSequence := false
		switch method {
		case "from", "of":
			receiver := javascriptCallReceiverTokens(tokens, index)
			byteSequence = javascriptTokensContainIdentifier(receiver, "Buffer") ||
				javascriptTokensContainIdentifier(receiver, "Uint8Array")
		case "uint8array", "buffer":
			byteSequence = index >= 2 &&
				tokens[index-2].kind == 'i' &&
				tokens[index-2].value == "new"
		case "fromcharcode", "fromcodepoint":
			receiver := javascriptCallReceiverTokens(tokens, index)
			byteSequence = javascriptTokensContainIdentifier(receiver, "String")
		}
		if !byteSequence {
			continue
		}
		for _, argument := range tokens[index+1 : end] {
			if argument.kind != 'n' {
				continue
			}
			value, err := strconv.ParseInt(argument.value, 0, 32)
			if err == nil && value == 7 {
				return true
			}
		}
	}
	return false
}

type javascriptAuditBindings struct {
	processObjects      map[string]bool
	terminalObjects     map[string]bool
	fsObjects           map[string]bool
	processFunctions    map[string]string
	outputFunctions     map[string]string
	fsMutationFunctions map[string]string
}

type javascriptAuditFunction struct {
	name       string
	parameters []string
	body       []javascriptAuditToken
	bodyStart  int
}

func collectJavaScriptHostEffectParameterUses(
	tokens []javascriptAuditToken,
	bindings javascriptAuditBindings,
	constants map[string]string,
) map[string]map[int]goHostEffectUse {
	functions := javascriptAuditFunctions(tokens)
	methodOwners := javascriptAuditMethodOwners(tokens)
	uses := make(map[string]map[int]goHostEffectUse, len(functions))
	processCalls := make(
		map[string]map[int]repositoryJavaScriptValue,
		len(functions),
	)
	for name := range functions {
		uses[name] = make(map[int]goHostEffectUse)
		processCalls[name] = javascriptScopedProcessCalls(
			functions[name],
			bindings,
		)
	}
	for range 32 {
		changed := false
		for name, function := range functions {
			parameterBindings := make([]int, len(function.parameters))
			for index, parameter := range function.parameters {
				parameterBindings[index] = -1
				if binding, ok := methodOwners.resolveBinding(
					parameter,
					function.bodyStart,
				); ok {
					parameterBindings[index] = binding.declaration
				}
			}
			mark := func(
				expression []javascriptAuditToken,
				use goHostEffectUse,
			) {
				for index, parameter := range function.parameters {
					if javascriptExpressionReferencesBinding(
						expression,
						parameter,
						parameterBindings[index],
						methodOwners,
					) && uses[name][index]&use == 0 {
						uses[name][index] |= use
						changed = true
					}
				}
			}
			for index, token := range function.body {
				if token.kind != '(' {
					continue
				}
				end := matchingJavaScriptToken(
					function.body,
					index,
					'(',
					')',
				)
				if end < 0 {
					continue
				}
				method := javascriptCallMethod(function.body, index)
				arguments := splitJavaScriptArguments(
					function.body[index+1 : end],
				)
				processMethod := method
				if alias := bindings.processFunctions[method]; alias != "" {
					processMethod = alias
				}
				if javascriptProcessMethod(processMethod) &&
					javascriptProcessArgumentsHaveHostEffect(
						arguments,
						constants,
					) {
					if javascriptCallHasExplicitReceiver(
						function.body,
						index,
					) {
						mark(
							javascriptCallReceiverTokens(
								function.body,
								index,
							),
							goProcessCapabilityHostEffectUse,
						)
					} else if index > 0 {
						mark(
							function.body[index-1:index],
							goProcessCapabilityHostEffectUse,
						)
					}
				}
				processCall := processCalls[name][index]
				if len(processCall.processMethods) != 0 {
					if len(arguments) > 0 {
						mark(arguments[0], goProcessHostEffectUse)
						dynamic := false
						for _, parameter := range function.parameters {
							if javascriptTokensContainIdentifier(
								arguments[0],
								parameter,
							) {
								dynamic = true
								break
							}
						}
						shellCommand := false
						for processMethod := range processCall.processMethods {
							if strings.EqualFold(processMethod, "exec") ||
								strings.EqualFold(processMethod, "execsync") {
								shellCommand = true
								break
							}
						}
						if dynamic || shellCommand {
							for _, argument := range arguments {
								mark(
									argument,
									goProcessHostEffectUse|
										goTerminalHostEffectUse,
								)
							}
						}
					}
				}
				outputMethod, outputCall := javascriptScopedOutputMethod(
					function.body,
					index,
					method,
					bindings,
					function.parameters,
				)
				if outputCall {
					outputArguments := arguments
					if outputMethod == "writesync" {
						if !javascriptOutputFileDescriptor(arguments) {
							outputArguments = nil
						} else {
							outputArguments = arguments[1:]
						}
					}
					for _, argument := range outputArguments {
						mark(argument, goTerminalHostEffectUse)
					}
				}
				callees := []string{method}
				if binding := javascriptAuditBareFunctionKey(
					function.body,
					index,
					function.bodyStart+index,
					methodOwners,
				); binding != "" {
					callees = append(callees, binding)
				}
				if qualified := javascriptAuditQualifiedMethodKey(
					function.body,
					index,
					function.bodyStart+index,
					methodOwners,
				); qualified != "" {
					callees = append(callees, qualified)
				}
				for _, callee := range callees {
					for argumentIndex, use := range uses[callee] {
						if argumentIndex < len(arguments) {
							mark(arguments[argumentIndex], use)
						}
					}
				}
			}
		}
		if !changed {
			break
		}
	}
	return uses
}

func javascriptExpressionReferencesBinding(
	expression []javascriptAuditToken,
	name string,
	declaration int,
	owners javascriptAuditMethodOwnerSet,
) bool {
	for _, current := range expression {
		if current.kind != 'i' || current.value != name {
			continue
		}
		if declaration < 0 {
			return true
		}
		binding, ok := owners.resolveBinding(name, current.position)
		if ok && binding.declaration == declaration {
			return true
		}
	}
	return false
}

func javascriptScopedProcessCalls(
	function javascriptAuditFunction,
	bindings javascriptAuditBindings,
) map[int]repositoryJavaScriptValue {
	tokens := make(
		[]repositoryJavaScriptToken,
		len(function.body),
	)
	for index, token := range function.body {
		tokens[index] = repositoryJavaScriptToken{
			kind:  token.kind,
			value: token.value,
		}
	}
	scope := &repositoryJavaScriptScope{
		bindings: make(map[string]repositoryJavaScriptValue),
	}
	for name := range bindings.processObjects {
		scope.declare(name, repositoryJavaScriptValue{module: true})
	}
	for name, method := range bindings.processFunctions {
		scope.declare(name, repositoryJavaScriptValue{
			processMethods: map[string]bool{method: true},
		})
	}
	for _, parameter := range function.parameters {
		scope.declare(parameter, repositoryJavaScriptValue{})
	}
	functionBodies := repositoryJavaScriptFunctionBodies(tokens)
	calls := make(map[int]repositoryJavaScriptValue)
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
					scope.declare(
						tokens[index+1].value,
						repositoryJavaScriptValue{},
					)
				}
			default:
				if index+1 < len(tokens) && tokens[index+1].kind == '=' &&
					(index+2 >= len(tokens) || tokens[index+2].kind != '=') {
					end := repositoryJavaScriptStatementEnd(tokens, index+2)
					scope.assign(
						current.value,
						repositoryJavaScriptEvaluate(
							scope,
							tokens[index+2:end],
						),
					)
				}
			}
		}
		if current.kind != '(' {
			continue
		}
		start := repositoryJavaScriptExpressionStart(tokens, index)
		callee := repositoryJavaScriptHostProcessCallee(
			scope,
			tokens[start:index],
		)
		if len(callee.processMethods) != 0 {
			calls[index] = callee
		}
	}
	return calls
}

func javascriptAuditFunctions(
	tokens []javascriptAuditToken,
) map[string]javascriptAuditFunction {
	functions := make(map[string]javascriptAuditFunction)
	for index := 0; index+4 < len(tokens); index++ {
		if tokens[index].kind != 'i' ||
			tokens[index].value != "function" ||
			tokens[index+1].kind != 'i' ||
			tokens[index+2].kind != '(' {
			continue
		}
		parametersEnd := matchingJavaScriptToken(
			tokens,
			index+2,
			'(',
			')',
		)
		if parametersEnd < 0 || parametersEnd+1 >= len(tokens) ||
			tokens[parametersEnd+1].kind != '{' {
			continue
		}
		bodyEnd := matchingJavaScriptToken(
			tokens,
			parametersEnd+1,
			'{',
			'}',
		)
		if bodyEnd < 0 {
			continue
		}
		var parameters []string
		for _, parameter := range splitJavaScriptArguments(
			tokens[index+3 : parametersEnd],
		) {
			for _, token := range parameter {
				if token.kind == 'i' {
					parameters = append(parameters, token.value)
					break
				}
			}
		}
		key := javascriptAuditFunctionKey(tokens[index+1].value, index)
		functions[key] = javascriptAuditFunction{
			name:       key,
			parameters: parameters,
			body:       tokens[parametersEnd+2 : bodyEnd],
			bodyStart:  parametersEnd + 2,
		}
		index = bodyEnd
	}
	for index := 0; index+3 < len(tokens); index++ {
		if tokens[index].kind != 'i' || tokens[index+1].kind != '=' {
			continue
		}
		name := tokens[index].value
		start := index + 2
		var parameters []string
		bodyStart := -1
		if tokens[start].kind == 'i' && tokens[start].value == "function" &&
			start+1 < len(tokens) && tokens[start+1].kind == '(' {
			parametersEnd := matchingJavaScriptToken(tokens, start+1, '(', ')')
			if parametersEnd < 0 {
				continue
			}
			parameters = javascriptAuditParameterNames(
				tokens[start+2 : parametersEnd],
			)
			bodyStart = parametersEnd + 1
		} else if tokens[start].kind == 'i' &&
			start+2 < len(tokens) &&
			tokens[start+1].kind == '=' && tokens[start+2].kind == '>' {
			parameters = []string{tokens[start].value}
			bodyStart = start + 3
		} else if tokens[start].kind == '(' {
			parametersEnd := matchingJavaScriptToken(tokens, start, '(', ')')
			if parametersEnd < 0 || parametersEnd+2 >= len(tokens) ||
				tokens[parametersEnd+1].kind != '=' ||
				tokens[parametersEnd+2].kind != '>' {
				continue
			}
			parameters = javascriptAuditParameterNames(
				tokens[start+1 : parametersEnd],
			)
			bodyStart = parametersEnd + 3
		}
		if bodyStart < 0 || bodyStart >= len(tokens) {
			continue
		}
		var body []javascriptAuditToken
		sourceStart := bodyStart
		if tokens[bodyStart].kind == '{' {
			bodyEnd := matchingJavaScriptToken(tokens, bodyStart, '{', '}')
			if bodyEnd < 0 {
				continue
			}
			body = tokens[bodyStart+1 : bodyEnd]
			sourceStart = bodyStart + 1
		} else {
			bodyEnd := javascriptAuditStatementEnd(tokens, bodyStart)
			body = tokens[bodyStart:bodyEnd]
		}
		functions[name] = javascriptAuditFunction{
			name:       name,
			parameters: parameters,
			body:       body,
			bodyStart:  sourceStart,
		}
	}
	collectJavaScriptAuditMethodFunctions(tokens, functions)
	return functions
}

func javascriptAuditFunctionKey(name string, declaration int) string {
	return fmt.Sprintf("@function:%s:%d", name, declaration)
}

func collectJavaScriptAuditMethodFunctions(
	tokens []javascriptAuditToken,
	functions map[string]javascriptAuditFunction,
) {
	owners := javascriptAuditMethodOwners(tokens)
	for index := 0; index+3 < len(tokens); index++ {
		if tokens[index].kind == 'i' &&
			(tokens[index].value == "const" ||
				tokens[index].value == "let" ||
				tokens[index].value == "var") &&
			tokens[index+1].kind == 'i' &&
			tokens[index+2].kind == '=' &&
			tokens[index+3].kind == '{' {
			owner := owners.byDeclaration[index]
			end := matchingJavaScriptToken(tokens, index+3, '{', '}')
			if owner != "" && end > index+3 {
				collectJavaScriptAuditMethodsInBody(
					tokens,
					index+3,
					end,
					owner,
					functions,
				)
			}
			continue
		}
		if tokens[index].kind == 'i' && tokens[index].value == "class" &&
			tokens[index+1].kind == 'i' &&
			tokens[index+2].kind == '{' {
			owner := owners.byDeclaration[index]
			end := matchingJavaScriptToken(tokens, index+2, '{', '}')
			if owner != "" && end > index+2 {
				collectJavaScriptAuditMethodsInBody(
					tokens,
					index+2,
					end,
					owner,
					functions,
				)
			}
		}
	}
}

func collectJavaScriptAuditMethodsInBody(
	tokens []javascriptAuditToken,
	start int,
	end int,
	owner string,
	functions map[string]javascriptAuditFunction,
) {
	for index := start + 1; index+3 < end; {
		if tokens[index].kind != 'i' || tokens[index+1].kind != '(' {
			index++
			continue
		}
		parametersEnd := matchingJavaScriptToken(
			tokens,
			index+1,
			'(',
			')',
		)
		if parametersEnd < 0 || parametersEnd+1 >= end ||
			tokens[parametersEnd+1].kind != '{' {
			index++
			continue
		}
		bodyEnd := matchingJavaScriptToken(
			tokens,
			parametersEnd+1,
			'{',
			'}',
		)
		if bodyEnd < 0 || bodyEnd > end {
			index++
			continue
		}
		key := owner + "." + tokens[index].value
		functions[key] = javascriptAuditFunction{
			name: key,
			parameters: javascriptAuditParameterNames(
				tokens[index+2 : parametersEnd],
			),
			body:      tokens[parametersEnd+2 : bodyEnd],
			bodyStart: parametersEnd + 2,
		}
		index = bodyEnd + 1
	}
}

type javascriptAuditMethodOwner struct {
	key         string
	declaration int
	scopeStart  int
	scopeEnd    int
}

type javascriptAuditMethodOwnerSet struct {
	byName        map[string][]javascriptAuditMethodOwner
	byDeclaration map[int]string
}

func javascriptAuditMethodOwners(
	tokens []javascriptAuditToken,
) javascriptAuditMethodOwnerSet {
	owners := javascriptAuditMethodOwnerSet{
		byName:        make(map[string][]javascriptAuditMethodOwner),
		byDeclaration: make(map[int]string),
	}
	for index := 0; index+2 < len(tokens); index++ {
		if tokens[index].kind == 'i' &&
			tokens[index].value == "function" &&
			tokens[index+1].kind == 'i' {
			owners.add(
				tokens,
				tokens[index+1].value,
				index,
				javascriptAuditFunctionKey(tokens[index+1].value, index),
			)
		}
		if tokens[index].kind == 'i' && tokens[index].value == "class" &&
			tokens[index+1].kind == 'i' &&
			tokens[index+2].kind == '{' {
			name := tokens[index+1].value
			owners.addShadow(tokens, name, index)
			owners.add(
				tokens,
				name,
				index,
				fmt.Sprintf("@class:%s:%d", name, index),
			)
		}
		if tokens[index].kind == 'i' &&
			(tokens[index].value == "const" ||
				tokens[index].value == "let" ||
				tokens[index].value == "var") &&
			tokens[index+1].kind == 'i' &&
			tokens[index+2].kind == '=' &&
			index+3 < len(tokens) {
			name := tokens[index+1].value
			owners.addShadow(tokens, name, index)
			if tokens[index+3].kind == '{' {
				owners.add(
					tokens,
					name,
					index,
					fmt.Sprintf("@object:%s:%d", name, index),
				)
			}
		}
	}
	owners.addFunctionParameterShadows(tokens)
	for range 16 {
		changed := false
		for index := 0; index+3 < len(tokens); index++ {
			if tokens[index].kind != 'i' ||
				(tokens[index].value != "const" &&
					tokens[index].value != "let" &&
					tokens[index].value != "var") ||
				tokens[index+1].kind != 'i' ||
				tokens[index+2].kind != '=' {
				continue
			}
			name := tokens[index+1].value
			var owner string
			switch {
			case index+5 < len(tokens) &&
				tokens[index+3].kind == 'i' &&
				tokens[index+3].value == "new" &&
				tokens[index+4].kind == 'i':
				owner = owners.resolve(tokens[index+4].value, index)
			case tokens[index+3].kind == 'i':
				owner = owners.resolve(tokens[index+3].value, index)
			}
			if owner != "" &&
				owners.byDeclaration[index] != owner {
				owners.add(tokens, name, index, owner)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return owners
}

func (owners *javascriptAuditMethodOwnerSet) add(
	tokens []javascriptAuditToken,
	name string,
	declaration int,
	key string,
) {
	for index := range owners.byName[name] {
		binding := &owners.byName[name][index]
		if binding.declaration == declaration {
			binding.key = key
			owners.byDeclaration[declaration] = key
			return
		}
	}
	scopeStart, scopeEnd := javascriptAuditLexicalScope(
		tokens,
		declaration,
	)
	owners.byName[name] = append(
		owners.byName[name],
		javascriptAuditMethodOwner{
			key:         key,
			declaration: declaration,
			scopeStart:  scopeStart,
			scopeEnd:    scopeEnd,
		},
	)
	owners.byDeclaration[declaration] = key
}

func (owners *javascriptAuditMethodOwnerSet) addShadow(
	tokens []javascriptAuditToken,
	name string,
	declaration int,
) {
	for _, binding := range owners.byName[name] {
		if binding.declaration == declaration {
			return
		}
	}
	scopeStart, scopeEnd := javascriptAuditLexicalScope(tokens, declaration)
	owners.addScopedShadow(
		name,
		declaration,
		scopeStart,
		scopeEnd,
	)
}

func (owners *javascriptAuditMethodOwnerSet) addScopedShadow(
	name string,
	declaration int,
	scopeStart int,
	scopeEnd int,
) {
	owners.byName[name] = append(
		owners.byName[name],
		javascriptAuditMethodOwner{
			declaration: declaration,
			scopeStart:  scopeStart,
			scopeEnd:    scopeEnd,
		},
	)
}

func (owners *javascriptAuditMethodOwnerSet) addFunctionParameterShadows(
	tokens []javascriptAuditToken,
) {
	for index := 0; index < len(tokens); index++ {
		if tokens[index].kind == 'i' && tokens[index].value == "function" {
			open := index + 1
			if open < len(tokens) && tokens[open].kind == 'i' {
				open++
			}
			if open >= len(tokens) || tokens[open].kind != '(' {
				continue
			}
			close := matchingJavaScriptToken(tokens, open, '(', ')')
			if close < 0 || close+1 >= len(tokens) ||
				tokens[close+1].kind != '{' {
				continue
			}
			bodyEnd := matchingJavaScriptToken(
				tokens,
				close+1,
				'{',
				'}',
			)
			if bodyEnd < 0 {
				continue
			}
			owners.addParameterShadows(
				tokens[open+1:close],
				open+1,
				close+1,
				bodyEnd,
			)
			continue
		}
		if tokens[index].kind == 'i' &&
			index+1 < len(tokens) &&
			tokens[index+1].kind == '(' &&
			(index == 0 || tokens[index-1].value != "function") &&
			!javascriptAuditControlKeyword(tokens[index].value) {
			close := matchingJavaScriptToken(
				tokens,
				index+1,
				'(',
				')',
			)
			if close >= 0 && close+1 < len(tokens) &&
				tokens[close+1].kind == '{' {
				bodyEnd := matchingJavaScriptToken(
					tokens,
					close+1,
					'{',
					'}',
				)
				if bodyEnd >= 0 {
					owners.addParameterShadows(
						tokens[index+2:close],
						index+2,
						close+1,
						bodyEnd,
					)
					continue
				}
			}
		}
		if tokens[index].kind != '>' || index == 0 ||
			tokens[index-1].kind != '=' || index+1 >= len(tokens) ||
			tokens[index+1].kind != '{' {
			continue
		}
		start := index - 2
		end := index - 1
		if start >= 0 && tokens[start].kind == ')' {
			start = matchingJavaScriptTokenBackward(
				tokens,
				start,
				'(',
				')',
			)
			if start >= 0 {
				start++
			}
		}
		if start < 0 {
			continue
		}
		bodyEnd := matchingJavaScriptToken(tokens, index+1, '{', '}')
		if bodyEnd < 0 {
			continue
		}
		owners.addParameterShadows(
			tokens[start:end],
			start,
			index+1,
			bodyEnd,
		)
	}
}

func javascriptAuditControlKeyword(name string) bool {
	switch name {
	case "catch", "for", "if", "switch", "while", "with":
		return true
	default:
		return false
	}
}

func (owners *javascriptAuditMethodOwnerSet) addParameterShadows(
	tokens []javascriptAuditToken,
	offset int,
	scopeStart int,
	scopeEnd int,
) {
	cursor := 0
	for _, parameter := range splitJavaScriptArguments(tokens) {
		for parameterIndex, token := range parameter {
			if token.kind != 'i' {
				continue
			}
			owners.addScopedShadow(
				token.value,
				offset+cursor+parameterIndex,
				scopeStart,
				scopeEnd,
			)
			break
		}
		cursor += len(parameter) + 1
	}
}

func (owners javascriptAuditMethodOwnerSet) resolve(
	name string,
	position int,
) string {
	selected, ok := owners.resolveBinding(name, position)
	if !ok {
		return ""
	}
	return selected.key
}

func (owners javascriptAuditMethodOwnerSet) resolveBinding(
	name string,
	position int,
) (javascriptAuditMethodOwner, bool) {
	var selected *javascriptAuditMethodOwner
	for index := range owners.byName[name] {
		candidate := &owners.byName[name][index]
		if position <= candidate.scopeStart ||
			position >= candidate.scopeEnd {
			continue
		}
		if selected == nil ||
			candidate.scopeStart > selected.scopeStart ||
			(candidate.scopeStart == selected.scopeStart &&
				candidate.declaration <= position &&
				(selected.declaration > position ||
					candidate.declaration > selected.declaration)) {
			selected = candidate
		}
	}
	if selected == nil {
		return javascriptAuditMethodOwner{}, false
	}
	return *selected, true
}

func javascriptAuditLexicalScope(
	tokens []javascriptAuditToken,
	position int,
) (int, int) {
	start := -1
	end := len(tokens)
	var stack []int
	for index := 0; index < position && index < len(tokens); index++ {
		switch tokens[index].kind {
		case '{':
			stack = append(stack, index)
		case '}':
			if len(stack) != 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(stack) == 0 {
		return start, end
	}
	start = stack[len(stack)-1]
	if close := matchingJavaScriptToken(tokens, start, '{', '}'); close >= 0 {
		end = close
	}
	return start, end
}

func javascriptAuditQualifiedMethodKey(
	tokens []javascriptAuditToken,
	open int,
	position int,
	owners javascriptAuditMethodOwnerSet,
) string {
	method := javascriptCallMethod(tokens, open)
	if method == "" || !javascriptCallHasExplicitReceiver(tokens, open) {
		return ""
	}
	receiver := javascriptCallReceiverTokens(tokens, open)
	for index := len(receiver) - 1; index >= 0; index-- {
		if receiver[index].kind != 'i' {
			continue
		}
		if owner := owners.resolve(receiver[index].value, position); owner != "" {
			return owner + "." + method
		}
	}
	return ""
}

func javascriptAuditBareFunctionKey(
	tokens []javascriptAuditToken,
	open int,
	position int,
	owners javascriptAuditMethodOwnerSet,
) string {
	if open == 0 || javascriptCallHasExplicitReceiver(tokens, open) ||
		tokens[open-1].kind != 'i' {
		return ""
	}
	return owners.resolve(tokens[open-1].value, position)
}

func javascriptAuditParameterNames(
	tokens []javascriptAuditToken,
) []string {
	var parameters []string
	for _, parameter := range splitJavaScriptArguments(tokens) {
		for _, token := range parameter {
			if token.kind == 'i' {
				parameters = append(parameters, token.value)
				break
			}
		}
	}
	return parameters
}

func javascriptCallInvokesSensitiveHostEffect(
	method string,
	arguments [][]javascriptAuditToken,
	uses map[string]map[int]goHostEffectUse,
	constants map[string]string,
	tokens []javascriptAuditToken,
	position int,
	bindings javascriptAuditBindings,
) bool {
	for index, use := range uses[method] {
		if index >= len(arguments) {
			continue
		}
		if use&goProcessHostEffectUse != 0 &&
			javascriptExpressionHasHostEffect(arguments[index], constants) {
			return true
		}
		if use&goTerminalHostEffectUse != 0 &&
			javascriptArgumentsHaveTerminalEffect(
				[][]javascriptAuditToken{arguments[index]},
				constants,
			) {
			return true
		}
		if use&goProcessCapabilityHostEffectUse != 0 &&
			javascriptExpressionReferencesProcessBinding(
				tokens,
				position,
				arguments[index],
				bindings,
				nil,
				make(map[string]bool),
			) {
			return true
		}
	}
	return false
}

func collectJavaScriptAuditBindings(
	tokens []javascriptAuditToken,
	constants map[string]string,
) javascriptAuditBindings {
	bindings := javascriptAuditBindings{
		processObjects:      map[string]bool{"Deno": true, "Bun": true},
		terminalObjects:     make(map[string]bool),
		fsObjects:           map[string]bool{"fs": true},
		processFunctions:    make(map[string]string),
		outputFunctions:     make(map[string]string),
		fsMutationFunctions: make(map[string]string),
	}
	for range 16 {
		changed := false
		for index := 0; index+2 < len(tokens); index++ {
			if tokens[index].kind == 'i' &&
				(tokens[index].value == "const" ||
					tokens[index].value == "let" ||
					tokens[index].value == "var") &&
				tokens[index+1].kind == '{' {
				close := matchingJavaScriptToken(tokens, index+1, '{', '}')
				if close > index+1 && close+1 < len(tokens) &&
					tokens[close+1].kind == '=' {
					end := javascriptAuditStatementEnd(tokens, close+2)
					rhs := tokens[close+2 : end]
					if javascriptTokensReferenceProcessObject(rhs, bindings) {
						changed = collectJavaScriptDestructuredFunctions(
							tokens[index+2:close],
							bindings.processFunctions,
							javascriptProcessMethod,
						) || changed
					}
					if javascriptTokensReferenceTerminalObject(rhs, bindings) {
						changed = collectJavaScriptDestructuredFunctions(
							tokens[index+2:close],
							bindings.outputFunctions,
							javascriptOutputMethod,
						) || changed
					}
					if javascriptTokensReferenceFSObject(rhs, bindings) {
						changed = collectJavaScriptDestructuredFunctions(
							tokens[index+2:close],
							bindings.fsMutationFunctions,
							javascriptFSMutationMethod,
						) || changed
					}
				}
				continue
			}
			if tokens[index].kind != 'i' || tokens[index+1].kind != '=' {
				continue
			}
			name := tokens[index].value
			end := javascriptAuditStatementEnd(tokens, index+2)
			rhs := tokens[index+2 : end]
			if javascriptTokensReferenceProcessObject(rhs, bindings) &&
				!bindings.processObjects[name] {
				bindings.processObjects[name] = true
				changed = true
			}
			if javascriptTokensReferenceTerminalObject(rhs, bindings) &&
				!bindings.terminalObjects[name] {
				bindings.terminalObjects[name] = true
				changed = true
			}
			if javascriptTokensReferenceFSObject(rhs, bindings) &&
				!bindings.fsObjects[name] {
				bindings.fsObjects[name] = true
				changed = true
			}
			member := strings.ToLower(javascriptLastMemberName(rhs))
			if javascriptProcessMethod(member) &&
				javascriptTokensReferenceProcessObject(rhs, bindings) &&
				bindings.processFunctions[name] != member {
				bindings.processFunctions[name] = member
				changed = true
			}
			if javascriptOutputMethod(member) &&
				(javascriptTokensReferenceTerminalObject(rhs, bindings) ||
					javascriptTokensReferenceFSObject(rhs, bindings) ||
					javascriptTokensContainIdentifier(rhs, "console")) &&
				bindings.outputFunctions[name] != member {
				bindings.outputFunctions[name] = member
				changed = true
			}
			mutationMethod := member
			if alias := bindings.fsMutationFunctions[member]; alias != "" {
				mutationMethod = alias
			}
			if javascriptFSMutationMethod(mutationMethod) &&
				(javascriptTokensReferenceFSObject(rhs, bindings) ||
					bindings.fsMutationFunctions[member] != "") &&
				bindings.fsMutationFunctions[name] != mutationMethod {
				bindings.fsMutationFunctions[name] = mutationMethod
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	_ = constants
	return bindings
}

func javascriptAuditStatementEnd(
	tokens []javascriptAuditToken,
	start int,
) int {
	depth := 0
	for index := start; index < len(tokens); index++ {
		switch tokens[index].kind {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				return index
			}
		}
	}
	return len(tokens)
}

func collectJavaScriptDestructuredFunctions(
	tokens []javascriptAuditToken,
	destination map[string]string,
	allowed func(string) bool,
) bool {
	changed := false
	for index := 0; index < len(tokens); index++ {
		if tokens[index].kind != 'i' {
			continue
		}
		original := strings.ToLower(tokens[index].value)
		if !allowed(original) {
			continue
		}
		alias := tokens[index].value
		if index+2 < len(tokens) && tokens[index+1].kind == ':' &&
			tokens[index+2].kind == 'i' {
			alias = tokens[index+2].value
			index += 2
		}
		if destination[alias] != original {
			destination[alias] = original
			changed = true
		}
	}
	return changed
}

func javascriptProcessMethod(method string) bool {
	switch strings.ToLower(method) {
	case "exec", "execsync", "execfile", "execfilesync",
		"spawn", "spawnsync", "command":
		return true
	default:
		return false
	}
}

func javascriptOutputMethod(method string) bool {
	switch strings.ToLower(method) {
	case "write", "writestring", "writeline", "writesync",
		"log", "error", "warn", "info":
		return true
	default:
		return false
	}
}

func javascriptFSMutationMethod(method string) bool {
	switch strings.ToLower(method) {
	case "appendfilesync", "copyfilesync", "cpsync", "linksync",
		"renamesync", "symlinksync", "truncatesync", "writefilesync":
		return true
	default:
		return false
	}
}

func javascriptLastMemberName(tokens []javascriptAuditToken) string {
	for index := len(tokens) - 1; index >= 0; index-- {
		if tokens[index].kind == 'i' || tokens[index].kind == 's' {
			return tokens[index].value
		}
	}
	return ""
}

func javascriptTokensContainIdentifier(
	tokens []javascriptAuditToken,
	want string,
) bool {
	for _, token := range tokens {
		if (token.kind == 'i' || token.kind == 's') &&
			token.value == want {
			return true
		}
	}
	return false
}

func javascriptTokensReferenceProcessObject(
	tokens []javascriptAuditToken,
	bindings javascriptAuditBindings,
) bool {
	for _, token := range tokens {
		if token.kind == 's' &&
			(token.value == "child_process" ||
				token.value == "node:child_process") {
			return true
		}
		if token.kind == 'i' && bindings.processObjects[token.value] {
			return true
		}
	}
	return false
}

func javascriptExpressionReferencesProcessBinding(
	tokens []javascriptAuditToken,
	position int,
	expression []javascriptAuditToken,
	bindings javascriptAuditBindings,
	parameters []string,
	seen map[string]bool,
) bool {
	for _, token := range expression {
		if token.kind == 's' &&
			(token.value == "child_process" ||
				token.value == "node:child_process") {
			return true
		}
		if token.kind != 'i' || seen[token.value] ||
			javascriptOutputIdentifierIsParameter(
				tokens,
				position,
				token.value,
				parameters,
			) {
			continue
		}
		if value, local := javascriptLocalDeclarationValueAt(
			tokens,
			position,
			token.value,
		); local {
			seen[token.value] = true
			found := javascriptExpressionReferencesProcessBinding(
				tokens,
				position,
				value,
				bindings,
				parameters,
				seen,
			)
			delete(seen, token.value)
			if found {
				return true
			}
			continue
		}
		if bindings.processObjects[token.value] ||
			bindings.processFunctions[token.value] != "" {
			return true
		}
	}
	return false
}

func javascriptTokensReferenceTerminalObject(
	tokens []javascriptAuditToken,
	bindings javascriptAuditBindings,
) bool {
	hasProcess := false
	hasStream := false
	for _, token := range tokens {
		if token.kind == 'i' && token.value == "process" {
			hasProcess = true
		}
		if (token.kind == 'i' || token.kind == 's') &&
			(token.value == "stdout" || token.value == "stderr") {
			hasStream = true
		}
		if token.kind == 'i' && bindings.terminalObjects[token.value] {
			return true
		}
	}
	return hasProcess && hasStream
}

func javascriptTokensReferenceFSObject(
	tokens []javascriptAuditToken,
	bindings javascriptAuditBindings,
) bool {
	for _, token := range tokens {
		if token.kind == 's' &&
			(token.value == "fs" || token.value == "node:fs") {
			return true
		}
		if token.kind == 'i' && bindings.fsObjects[token.value] {
			return true
		}
	}
	return false
}

func javascriptCallReceiverTokens(
	tokens []javascriptAuditToken,
	open int,
) []javascriptAuditToken {
	start := 0
	depth := 0
	for index := open - 2; index >= 0; index-- {
		switch tokens[index].kind {
		case ')', ']':
			depth++
		case '(', '[':
			if depth > 0 {
				depth--
			}
		case ';', ',', '{', '}', '=':
			if depth == 0 {
				start = index + 1
				index = -1
			}
		}
	}
	return tokens[start:open]
}

func javascriptScopedOutputMethod(
	tokens []javascriptAuditToken,
	open int,
	method string,
	bindings javascriptAuditBindings,
	parameters []string,
) (string, bool) {
	if method == "" {
		return "", false
	}
	if !javascriptCallHasExplicitReceiver(tokens, open) {
		if javascriptOutputIdentifierIsParameter(
			tokens,
			open,
			method,
			parameters,
		) {
			return "", false
		}
		if value, local := javascriptLocalDeclarationValueAt(
			tokens,
			open,
			method,
		); local {
			return javascriptExpressionOutputMethod(
				tokens,
				open,
				value,
				bindings,
				parameters,
				make(map[string]bool),
			)
		}
		canonical := strings.ToLower(bindings.outputFunctions[method])
		return canonical, javascriptOutputMethod(canonical)
	}
	receiver := javascriptCallReceiverTokens(tokens, open)
	canonical := strings.ToLower(method)
	switch canonical {
	case "writesync":
		if javascriptExpressionReferencesFSBinding(
			tokens,
			open,
			receiver,
			bindings,
			parameters,
			make(map[string]bool),
		) {
			return canonical, true
		}
	case "write", "writestring", "writeline":
		if javascriptExpressionReferencesTerminalBinding(
			tokens,
			open,
			receiver,
			bindings,
			parameters,
			make(map[string]bool),
		) {
			return canonical, true
		}
	case "log", "error", "warn", "info":
		if javascriptExpressionReferencesConsole(
			tokens,
			open,
			receiver,
			parameters,
		) {
			return canonical, true
		}
	}
	return "", false
}

func javascriptOutputFileDescriptor(
	arguments [][]javascriptAuditToken,
) bool {
	if len(arguments) == 0 || len(arguments[0]) != 1 {
		return false
	}
	target := arguments[0][0]
	return target.kind == 'n' &&
		(target.value == "1" || target.value == "2")
}

func javascriptExpressionOutputMethod(
	tokens []javascriptAuditToken,
	position int,
	expression []javascriptAuditToken,
	bindings javascriptAuditBindings,
	parameters []string,
	seen map[string]bool,
) (string, bool) {
	member := strings.ToLower(javascriptLastMemberName(expression))
	switch member {
	case "writesync":
		if javascriptExpressionReferencesFSBinding(
			tokens,
			position,
			expression,
			bindings,
			parameters,
			seen,
		) {
			return member, true
		}
	case "write", "writestring", "writeline":
		if javascriptExpressionReferencesTerminalBinding(
			tokens,
			position,
			expression,
			bindings,
			parameters,
			seen,
		) {
			return member, true
		}
	case "log", "error", "warn", "info":
		if javascriptExpressionReferencesConsole(
			tokens,
			position,
			expression,
			parameters,
		) {
			return member, true
		}
	}
	expression = trimJavaScriptAuditExpression(expression)
	if len(expression) != 1 || expression[0].kind != 'i' {
		return "", false
	}
	name := expression[0].value
	if seen[name] || javascriptOutputIdentifierIsParameter(
		tokens,
		position,
		name,
		parameters,
	) {
		return "", false
	}
	if value, local := javascriptLocalDeclarationValueAt(
		tokens,
		position,
		name,
	); local {
		seen[name] = true
		method, output := javascriptExpressionOutputMethod(
			tokens,
			position,
			value,
			bindings,
			parameters,
			seen,
		)
		delete(seen, name)
		return method, output
	}
	canonical := strings.ToLower(bindings.outputFunctions[name])
	return canonical, javascriptOutputMethod(canonical)
}

func javascriptExpressionReferencesFSBinding(
	tokens []javascriptAuditToken,
	position int,
	expression []javascriptAuditToken,
	bindings javascriptAuditBindings,
	parameters []string,
	seen map[string]bool,
) bool {
	for _, token := range expression {
		if token.kind == 's' &&
			(token.value == "fs" || token.value == "node:fs") {
			return true
		}
		if token.kind != 'i' || seen[token.value] ||
			javascriptOutputIdentifierIsParameter(
				tokens,
				position,
				token.value,
				parameters,
			) {
			continue
		}
		if value, local := javascriptLocalDeclarationValueAt(
			tokens,
			position,
			token.value,
		); local {
			seen[token.value] = true
			found := javascriptExpressionReferencesFSBinding(
				tokens,
				position,
				value,
				bindings,
				parameters,
				seen,
			)
			delete(seen, token.value)
			if found {
				return true
			}
			continue
		}
		if bindings.fsObjects[token.value] {
			return true
		}
	}
	return false
}

func javascriptExpressionReferencesTerminalBinding(
	tokens []javascriptAuditToken,
	position int,
	expression []javascriptAuditToken,
	bindings javascriptAuditBindings,
	parameters []string,
	seen map[string]bool,
) bool {
	hasStream := false
	for _, token := range expression {
		if (token.kind == 'i' || token.kind == 's') &&
			(token.value == "stdout" || token.value == "stderr") {
			hasStream = true
		}
	}
	for _, token := range expression {
		if token.kind != 'i' || seen[token.value] ||
			javascriptOutputIdentifierIsParameter(
				tokens,
				position,
				token.value,
				parameters,
			) {
			continue
		}
		if value, local := javascriptLocalDeclarationValueAt(
			tokens,
			position,
			token.value,
		); local {
			seen[token.value] = true
			found := javascriptExpressionReferencesTerminalBinding(
				tokens,
				position,
				value,
				bindings,
				parameters,
				seen,
			)
			delete(seen, token.value)
			if found {
				return true
			}
			continue
		}
		if token.value == "process" && hasStream {
			return true
		}
		if bindings.terminalObjects[token.value] {
			return true
		}
	}
	return false
}

func javascriptExpressionReferencesConsole(
	tokens []javascriptAuditToken,
	position int,
	expression []javascriptAuditToken,
	parameters []string,
) bool {
	for _, token := range expression {
		if token.kind == 'i' && token.value == "console" &&
			!javascriptOutputIdentifierIsParameter(
				tokens,
				position,
				token.value,
				parameters,
			) {
			if _, local := javascriptLocalDeclarationValueAt(
				tokens,
				position,
				token.value,
			); !local {
				return true
			}
		}
	}
	return false
}

func javascriptOutputIdentifierIsParameter(
	tokens []javascriptAuditToken,
	position int,
	name string,
	parameters []string,
) bool {
	for _, parameter := range parameters {
		if parameter == name {
			return true
		}
	}
	return len(parameters) == 0 &&
		javascriptIdentifierIsFunctionParameterAt(
			tokens,
			position,
			name,
		)
}

func javascriptLocalDeclarationValueAt(
	tokens []javascriptAuditToken,
	position int,
	name string,
) ([]javascriptAuditToken, bool) {
	selected := -1
	selectedScope := -2
	selectedEnd := 0
	for index := 0; index+3 < len(tokens); index++ {
		if tokens[index].kind != 'i' ||
			(tokens[index].value != "const" &&
				tokens[index].value != "let" &&
				tokens[index].value != "var") ||
			tokens[index+1].kind != 'i' ||
			tokens[index+1].value != name ||
			tokens[index+2].kind != '=' {
			continue
		}
		scopeStart, scopeEnd := javascriptAuditLexicalScope(tokens, index)
		if position <= scopeStart || position >= scopeEnd {
			continue
		}
		if selected >= 0 && (scopeStart < selectedScope ||
			(scopeStart == selectedScope &&
				index <= selected)) {
			continue
		}
		selected = index
		selectedScope = scopeStart
		selectedEnd = javascriptAuditStatementEnd(tokens, index+3)
	}
	if selected < 0 {
		return nil, false
	}
	return tokens[selected+3 : selectedEnd], true
}

func trimJavaScriptAuditExpression(
	tokens []javascriptAuditToken,
) []javascriptAuditToken {
	for len(tokens) > 0 && tokens[0].kind == '(' {
		end := matchingJavaScriptToken(tokens, 0, '(', ')')
		if end != len(tokens)-1 {
			break
		}
		tokens = tokens[1:end]
	}
	return tokens
}

func isMachO(source []byte) bool {
	if len(source) < 4 {
		return false
	}
	switch string(source[:4]) {
	case "\xfe\xed\xfa\xce", "\xce\xfa\xed\xfe",
		"\xfe\xed\xfa\xcf", "\xcf\xfa\xed\xfe",
		"\xca\xfe\xba\xbe", "\xbe\xba\xfe\xca",
		"\xca\xfe\xba\xbf", "\xbf\xba\xfe\xca":
		return true
	default:
		return false
	}
}

func TestApplicationTestChildrenRetainRepositoryMode(t *testing.T) {
	files := trackedRepositoryAuditFiles(t)
	if err := validateRepositoryApplicationTestEnvironments(files); err != nil {
		t.Fatalf("current application-child test environment rejected: %v", err)
	}

	for name, source := range map[string]string{
		"marker overridden": `package future
import "os/exec"
func TestFuture() {
	cmd := exec.Command("/tmp/wisp-deck-tui", "main-menu")
	cmd.Env = []string{"HOME=/tmp", "WISP_DECK_TESTING=0"}
	_ = cmd.Run()
}`,
		"complete environment replaced": `package future
import "os/exec"
func TestFuture() {
	cmd := exec.Command("node", "/repo/bin/npx-wisp-deck.js")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`,
		"marker explicitly unset": `package future
import "os/exec"
func TestFuture() {
	cmd := exec.Command("env", "-u", "WISP_DECK_TESTING", "/tmp/wisp-deck-tui")
	_ = cmd.Run()
}`,
		"wrapper shell environment replaced": `package future
import "os/exec"
func TestFuture() {
	cmd := exec.Command("bash", "/repo/wrapper.sh")
	cmd.Env = append([]string{}, "HOME=/tmp")
	_ = cmd.Run()
}`,
		"aliased process package": `package future
import process "os/exec"
func TestFuture() {
	cmd := process.Command("/tmp/wisp-deck-tui", "main-menu")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`,
		"dot-imported process package": `package future
import . "os/exec"
func TestFuture() {
	cmd := Command("/tmp/wisp-deck-tui", "main-menu")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`,
		"application parameter environment": `package future
import "os/exec"
func runBinary(binary string) {
	cmd := exec.Command(binary, "capabilities")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`,
		"normalized variable later poisoned": `package future
import "os/exec"
func TestFuture() {
	env := repositoryTestEnvironment([]string{"HOME=/tmp"})
	env = append(env, "WISP_DECK_TESTING=0")
	cmd := exec.Command("/tmp/wisp-deck-tui", "main-menu")
	cmd.Env = env
	_ = cmd.Run()
}`,
		"unsafe conditional environment": `package future
import "os/exec"
func TestFuture(unsafe bool) {
	cmd := exec.Command("/tmp/wisp-deck-tui", "main-menu")
	if unsafe {
		cmd.Env = []string{"HOME=/tmp"}
	} else {
		cmd.Env = repositoryTestEnvironment([]string{"HOME=/tmp"})
	}
	_ = cmd.Run()
}`,
		"application command alias": `package future
import "os/exec"
func TestFuture() {
	cmd := exec.Command("/tmp/wisp-deck-tui", "main-menu")
	alias := cmd
	alias.Env = []string{"HOME=/tmp"}
	_ = alias.Run()
}`,
		"exec Cmd composite environment": `package future
import "os/exec"
func TestFuture() {
	cmd := &exec.Cmd{
		Path: "/tmp/wisp-deck-tui",
		Args: []string{"/tmp/wisp-deck-tui", "main-menu"},
		Env:  []string{"HOME=/tmp"},
	}
	_ = cmd.Run()
}`,
		"os StartProcess environment": `package future
import "os"
func TestFuture() {
	_, _ = os.StartProcess(
		"/tmp/wisp-deck-tui",
		[]string{"/tmp/wisp-deck-tui", "main-menu"},
		&os.ProcAttr{Env: []string{"HOME=/tmp"}},
	)
}`,
		"indirect os StartProcess environment": `package future
import "os"
func TestFuture() {
	attr := &os.ProcAttr{Env: []string{"HOME=/tmp"}}
	_, _ = os.StartProcess(
		"/tmp/wisp-deck-tui",
		[]string{"/tmp/wisp-deck-tui", "main-menu"},
		attr,
	)
}`,
		"assigned indirect os StartProcess environment": `package future
import "os"
func TestFuture() {
	attr := &os.ProcAttr{}
	attr.Env = []string{"HOME=/tmp"}
	_, _ = os.StartProcess(
		"/tmp/wisp-deck-tui",
		[]string{"/tmp/wisp-deck-tui", "main-menu"},
		attr,
	)
}`,
		"syscall Exec environment": `package future
import "syscall"
func TestFuture() {
	_ = syscall.Exec(
		"/tmp/wisp-deck-tui",
		[]string{"/tmp/wisp-deck-tui", "main-menu"},
		[]string{"HOME=/tmp"},
	)
}`,
		"env ignore environment": `package future
import "os/exec"
func TestFuture() {
	_ = exec.Command(
		"env",
		"-i",
		"HOME=/tmp",
		"/tmp/wisp-deck-tui",
		"main-menu",
	).Run()
}`,
		"constructor alias environment": `package future
import "os/exec"
func TestFuture() {
	command := exec.Command
	cmd := command("/tmp/wisp-deck-tui", "main-menu")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`,
		"go run application environment": `package future
import "os/exec"
func TestFuture() {
	cmd := exec.Command("go", "run", "./cmd/wisp-deck-tui", "main-menu")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`,
		"Make application environment": `package future
import "os/exec"
func TestFuture() {
	cmd := exec.Command("make", "build")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`,
		"application subcommand parameter": `package future
import "os/exec"
func run(path string) {
	cmd := exec.Command(path, "capabilities")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`,
		"application helper parameter": `package future
import (
	"os/exec"
	"testing"
)
func launch(binary string) {
	cmd := exec.Command(binary)
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}
func TestFuture(t *testing.T) {
	launch(nativeLedgerBinary(t))
}`,
		"chained application helper parameter": `package future
import (
	"os/exec"
	"testing"
)
func launch(binary string) {
	cmd := exec.Command(binary)
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}
func relay(binary string) {
	launch(binary)
}
func TestFuture(t *testing.T) {
	relay(nativeLedgerBinary(t))
}`,
		"application receiver helper parameter": `package future
import (
	"os/exec"
	"testing"
)
type launcher struct{}
func (launcher) launch(binary string) {
	cmd := exec.Command(binary)
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}
func TestFuture(t *testing.T) {
	launcher{}.launch(nativeLedgerBinary(t))
}`,
		"application pointer receiver helper parameter": `package future
import (
	"os/exec"
	"testing"
)
type launcher struct{}
func (*launcher) launch(binary string) {
	cmd := exec.Command(binary)
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}
func TestFuture(t *testing.T) {
	(&launcher{}).launch(nativeLedgerBinary(t))
}`,
	} {
		t.Run(name, func(t *testing.T) {
			mutated := cloneRepositoryAuditFiles(files)
			mutated["test/future_application_test.go"] = repositoryAuditFile{
				mode:   "100644",
				source: []byte(source),
			}
			if err := validateRepositoryApplicationTestEnvironments(mutated); err == nil {
				t.Fatal("unsafe application child environment escaped validation")
			}
		})
	}

	for name, helper := range map[string]string{
		"shadowed repository normalizer": "repositoryTestEnvironment",
		"shadowed build environment":     "buildEnv",
	} {
		t.Run(name, func(t *testing.T) {
			mutated := cloneRepositoryAuditFiles(files)
			mutated["test/helpers/application.go"] = repositoryAuditFile{
				mode: "100644",
				source: []byte(fmt.Sprintf(`package helpers
import "os/exec"
func %[1]s(environment []string) []string { return environment }
func launch() {
	cmd := exec.Command("/tmp/wisp-deck-tui", "main-menu")
	cmd.Env = %[1]s([]string{"HOME=/tmp"})
	_ = cmd.Run()
}`, helper)),
			}
			if err := validateRepositoryApplicationTestEnvironments(mutated); err == nil {
				t.Fatal("noncanonical environment normalizer escaped validation")
			}
		})
	}

	t.Run("application helper Go source", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/helpers/application.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package helpers
import "os/exec"
func launch() {
	cmd := exec.Command("/tmp/wisp-deck-tui", "main-menu")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err == nil {
			t.Fatal("unsafe application helper Go source escaped validation")
		}
	})

	t.Run("allows normalized application helper Go source", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/helpers/application.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package helpers
import "os/exec"
func launch() {
	cmd := exec.Command("/tmp/wisp-deck-tui", "main-menu")
	cmd.Env = repositoryTestEnvironment([]string{"HOME=/tmp"})
	_ = cmd.Run()
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("normalized application helper Go source rejected: %v", err)
		}
	})

	t.Run("allows unrelated process helper Go source", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/helpers/git.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package helpers
import "os/exec"
func status() {
	cmd := exec.Command("git", "status")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("unrelated process helper Go source rejected: %v", err)
		}
	})

	t.Run("allows normalized application child", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_application_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import "os/exec"
func TestFuture() {
	cmd := exec.Command("/tmp/wisp-deck-tui", "main-menu")
	cmd.Env = repositoryTestEnvironment([]string{"HOME=/tmp", "WISP_DECK_TESTING=0"})
	_ = cmd.Run()
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("normalized application child rejected: %v", err)
		}
	})

	t.Run("allows normalized indirect StartProcess environment", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_application_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import "os"
func TestFuture() {
	attr := &os.ProcAttr{}
	attr.Env = repositoryTestEnvironment([]string{"HOME=/tmp"})
	_, _ = os.StartProcess(
		"/tmp/wisp-deck-tui",
		[]string{"/tmp/wisp-deck-tui", "main-menu"},
		attr,
	)
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("normalized indirect StartProcess environment rejected: %v", err)
		}
	})

	t.Run("allows normalized application helper", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_application_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import (
	"os/exec"
	"testing"
)
func launch(binary string) {
	cmd := exec.Command(binary)
	cmd.Env = repositoryTestEnvironment([]string{"HOME=/tmp"})
	_ = cmd.Run()
}
func TestFuture(t *testing.T) {
	launch(nativeLedgerBinary(t))
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("normalized application helper rejected: %v", err)
		}
	})

	t.Run("allows normalized application receiver helper", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_application_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import (
	"os/exec"
	"testing"
)
type launcher struct{}
func (launcher) launch(binary string) {
	cmd := exec.Command(binary)
	cmd.Env = repositoryTestEnvironment([]string{"HOME=/tmp"})
	_ = cmd.Run()
}
func TestFuture(t *testing.T) {
	launcher{}.launch(nativeLedgerBinary(t))
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("normalized application receiver helper rejected: %v", err)
		}
	})

	t.Run("allows unrelated application receiver helper", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_git_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import "os/exec"
type launcher struct{}
func (launcher) launch(binary string) {
	cmd := exec.Command(binary)
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}
func TestFuture() {
	launcher{}.launch("git")
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("unrelated application receiver helper rejected: %v", err)
		}
	})

	t.Run("allows same-named receiver helpers", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_receivers_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import (
	"os/exec"
	"testing"
)
type applicationLauncher struct{}
func (applicationLauncher) launch(binary string) {
	cmd := exec.Command(binary)
	cmd.Env = repositoryTestEnvironment([]string{"HOME=/tmp"})
	_ = cmd.Run()
}
type gitLauncher struct{}
func (*gitLauncher) launch(binary string) {
	cmd := exec.Command(binary)
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}
func TestFuture(t *testing.T) {
	applicationLauncher{}.launch(nativeLedgerBinary(t))
	(&gitLauncher{}).launch("git")
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("same-named receiver helpers cross-contaminated provenance: %v", err)
		}
	})

	t.Run("allows unrelated process helper", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_git_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import "os/exec"
func launch(binary string) {
	cmd := exec.Command(binary)
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}
func TestFuture() {
	launch("git")
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("unrelated process helper rejected: %v", err)
		}
	})

	t.Run("allows unrelated process environment", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_git_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import "os/exec"
func TestFuture() {
	cmd := exec.Command("git", "status")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("unrelated process environment rejected: %v", err)
		}
	})

	t.Run("allows unrelated generic process parameter", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_generic_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import "os/exec"
func run(path string) {
	cmd := exec.Command(path, "status")
	cmd.Env = []string{"HOME=/tmp"}
	_ = cmd.Run()
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("unrelated generic process environment rejected: %v", err)
		}
	})

	t.Run("allows unrelated exec Cmd environment", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_git_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import "os/exec"
func TestFuture() {
	cmd := &exec.Cmd{Path: "git", Args: []string{"git", "status"}, Env: []string{"HOME=/tmp"}}
	_ = cmd.Run()
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err != nil {
			t.Fatalf("unrelated exec Cmd environment rejected: %v", err)
		}
	})

	small := string(files["test/bash/small_test.go"].source)
	exceptionMutations := map[string]string{
		"renamed exception": strings.Replace(
			small,
			"func TestEnabledChildDetectsExactTestAncestorSentinel",
			"func TestRenamedEnabledChild",
			1,
		),
		"changed sentinel": strings.Replace(
			small,
			`helper.Args[0] = "__WISP_DECK_REPOSITORY_TEST_V1__.test"`,
			`helper.Args[0] = "__WISP_DECK_REPOSITORY_TEST_V2__.test"`,
			1,
		),
		"direct child": strings.Replace(
			small,
			`"$1" capabilities & child_pid=$!; wait "$child_pid"`,
			`exec "$1" capabilities`,
			1,
		),
		"changed denial reason": strings.Replace(
			small,
			`HostEffectsDenialReason: "test_ancestor_sentinel"`,
			`HostEffectsDenialReason: "test_ancestor_marker"`,
			1,
		),
		"child marker override": strings.Replace(
			small,
			"helper.Env = environmentWithoutTestMarker(os.Environ())",
			`helper.Env = append(environmentWithoutTestMarker(os.Environ()), "WISP_DECK_TESTING=0")`,
			1,
		),
		"child environment restored later": strings.Replace(
			small,
			"helper.Env = environmentWithoutTestMarker(os.Environ())",
			"helper.Env = environmentWithoutTestMarker(os.Environ())\n\thelper.Env = os.Environ()",
			1,
		),
		"child sentinel overwritten later": strings.Replace(
			small,
			`helper.Args[0] = "__WISP_DECK_REPOSITORY_TEST_V1__.test"`,
			"helper.Args[0] = \"__WISP_DECK_REPOSITORY_TEST_V1__.test\"\n\thelper.Args[0] = \"ordinary-helper\"",
			1,
		),
		"changed compiled expectation": strings.Replace(
			small,
			"HostEffectsCompiled:     true,",
			"HostEffectsCompiled:     false,",
			1,
		),
		"second application child": strings.Replace(
			small,
			"if capabilities != want {",
			`_ = exec.Command("/tmp/wisp-deck-tui", "main-menu").Run()
	if capabilities != want {`,
			1,
		),
	}
	for name, source := range exceptionMutations {
		t.Run(name, func(t *testing.T) {
			if source == small {
				t.Fatal("enabled-child exception mutation prerequisite missing")
			}
			mutated := cloneRepositoryAuditFiles(files)
			file := mutated["test/bash/small_test.go"]
			file.source = []byte(source)
			mutated["test/bash/small_test.go"] = file
			if err := validateRepositoryApplicationTestEnvironments(mutated); err == nil {
				t.Fatal("broadened enabled-child marker exception escaped validation")
			}
		})
	}

	for name, replacement := range map[string]string{
		"changed Make marker": "WISP_DECK_TESTING=stale",
		"environment unset":   "env -u WISP_DECK_TESTING make",
	} {
		t.Run(name, func(t *testing.T) {
			source := strings.Replace(
				small,
				"make WISP_DECK_TESTING=0",
				replacement,
				1,
			)
			if source == small {
				t.Fatal("Make marker exception mutation prerequisite missing")
			}
			mutated := cloneRepositoryAuditFiles(files)
			file := mutated["test/bash/small_test.go"]
			file.source = []byte(source)
			mutated["test/bash/small_test.go"] = file
			if err := validateRepositoryApplicationTestEnvironments(mutated); err == nil {
				t.Fatal("broadened Make marker exception escaped validation")
			}
		})
	}

	t.Run("Make marker override outside named regression", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_make_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import "os/exec"
func TestFuture() {
	_ = exec.Command("make", "WISP_DECK_TESTING=0", "build").Run()
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err == nil {
			t.Fatal("Make marker override escaped its named regression")
		}
	})

	t.Run("constructed Make marker override outside named regression", func(t *testing.T) {
		mutated := cloneRepositoryAuditFiles(files)
		mutated["test/future_make_test.go"] = repositoryAuditFile{
			mode: "100644",
			source: []byte(`package future
import "os/exec"
func TestFuture() {
	marker := "WISP_DECK_TESTING=" + "0"
	_ = exec.Command("make", marker, "build").Run()
}`),
		}
		if err := validateRepositoryApplicationTestEnvironments(mutated); err == nil {
			t.Fatal("constructed Make marker override escaped its named regression")
		}
	})

	t.Run("Make regression strips complete environment", func(t *testing.T) {
		source := strings.Replace(
			small,
			"out, code := runBashSnippet(t, command, nil)",
			"out, code := runBashSnippet(t, command, environmentWithoutTestMarker(os.Environ()))",
			1,
		)
		if source == small {
			t.Fatal("Make environment mutation prerequisite missing")
		}
		mutated := cloneRepositoryAuditFiles(files)
		file := mutated["test/bash/small_test.go"]
		file.source = []byte(source)
		mutated["test/bash/small_test.go"] = file
		if err := validateRepositoryApplicationTestEnvironments(mutated); err == nil {
			t.Fatal("Make marker exception stripped its complete environment")
		}
	})

	t.Run("Make regression adds application child", func(t *testing.T) {
		source := strings.Replace(
			small,
			"out, code := runBashSnippet(t, command, nil)",
			`_ = exec.Command("/tmp/wisp-deck-tui", "main-menu").Run()
	out, code := runBashSnippet(t, command, nil)`,
			1,
		)
		if source == small {
			t.Fatal("Make application mutation prerequisite missing")
		}
		mutated := cloneRepositoryAuditFiles(files)
		file := mutated["test/bash/small_test.go"]
		file.source = []byte(source)
		mutated["test/bash/small_test.go"] = file
		if err := validateRepositoryApplicationTestEnvironments(mutated); err == nil {
			t.Fatal("Make marker exception authorized an application child")
		}
	})
}

func validateRepositoryApplicationTestEnvironments(
	files map[string]repositoryAuditFile,
) error {
	for path, file := range files {
		slashPath := filepath.ToSlash(path)
		if !strings.HasSuffix(slashPath, ".go") ||
			(!strings.HasSuffix(slashPath, "_test.go") &&
				!strings.HasPrefix(slashPath, "test/")) {
			continue
		}
		if err := validateApplicationTestEnvironment(path, file.source); err != nil {
			return err
		}
	}
	return nil
}

func validateApplicationTestEnvironment(path string, source []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return fmt.Errorf("parse application-child audit %s: %w", path, err)
	}
	aliases, dotImports := processImportAliases(file)
	applicationParameters := collectApplicationAuditParameterValues(
		file,
		aliases,
		dotImports,
	)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		functionAliases := make(map[string]string, len(aliases))
		for name, importPath := range aliases {
			functionAliases[name] = importPath
		}
		collectApplicationAuditProcessAliases(
			function,
			functionAliases,
			dotImports,
		)
		staticStrings := collectApplicationAuditStaticStrings(function)
		if functionUsesMakeMarkerOverride(
			function,
			functionAliases,
			dotImports,
			staticStrings,
		) &&
			(path != "test/bash/small_test.go" ||
				function.Name.Name != "TestMakefile_buildTestSelectorsCannotBeOverridden") {
			return fmt.Errorf(
				"%s:%s uses the reserved Make marker override outside its named regression",
				path,
				function.Name.Name,
			)
		}
		if function.Name.Name == "TestEnabledChildDetectsExactTestAncestorSentinel" {
			if path != "test/bash/small_test.go" {
				return fmt.Errorf("%s moved the enabled-child marker exception", path)
			}
			if err := validateEnabledChildMarkerException(
				function,
				functionAliases,
				dotImports,
			); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if err := validateApplicationChildrenInFunction(
				path,
				function,
				functionAliases,
				dotImports,
				staticStrings,
				applicationParameters[function],
				map[string]bool{"helper": true},
			); err != nil {
				return err
			}
			continue
		}
		if function.Name.Name == "TestMakefile_buildTestSelectorsCannotBeOverridden" {
			if path != "test/bash/small_test.go" {
				return fmt.Errorf("%s moved the Make marker exception", path)
			}
			if err := validateMakeMarkerException(
				function,
				functionAliases,
				dotImports,
			); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
		if err := validateApplicationChildrenInFunction(
			path,
			function,
			functionAliases,
			dotImports,
			staticStrings,
			applicationParameters[function],
			nil,
		); err != nil {
			return err
		}
	}
	return nil
}

func collectApplicationAuditParameterValues(
	file *ast.File,
	aliases map[string]string,
	dotImports map[string]bool,
) map[*ast.FuncDecl]map[string]bool {
	functions := make(map[string][]*ast.FuncDecl)
	var allFunctions []*ast.FuncDecl
	receiverTypes := collectGoAuditReceiverTypes(file)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		for _, key := range goAuditFunctionKeys(function) {
			functions[key] = append(functions[key], function)
		}
		allFunctions = append(allFunctions, function)
	}

	parameters := make(map[*ast.FuncDecl]map[string]bool, len(allFunctions))
	for {
		changed := false
		for _, caller := range allFunctions {
			functionAliases := make(map[string]string, len(aliases))
			for name, importPath := range aliases {
				functionAliases[name] = importPath
			}
			collectApplicationAuditProcessAliases(
				caller,
				functionAliases,
				dotImports,
			)
			staticStrings := collectApplicationAuditStaticStrings(caller)
			callerValues := applicationValueVariables(
				caller,
				functionAliases,
				dotImports,
				staticStrings,
				parameters[caller],
			)
			ast.Inspect(caller.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				for _, calleeKey := range goAuditCalleeKeys(
					call.Fun,
					receiverTypes,
				) {
					for _, callee := range functions[calleeKey] {
						calleeParameters, variadic := applicationAuditFunctionParameters(callee)
						for index, argument := range call.Args {
							parameterIndex := index
							if parameterIndex >= len(calleeParameters) {
								if !variadic || len(calleeParameters) == 0 {
									continue
								}
								parameterIndex = len(calleeParameters) - 1
							}
							parameter := calleeParameters[parameterIndex]
							if parameter == nil ||
								!expressionIsApplicationValue(
									argument,
									callerValues,
									staticStrings,
								) {
								continue
							}
							if parameters[callee] == nil {
								parameters[callee] = make(map[string]bool)
							}
							if !parameters[callee][parameter.Name] {
								parameters[callee][parameter.Name] = true
								changed = true
							}
						}
					}
				}
				return true
			})
		}
		if !changed {
			return parameters
		}
	}
}

func applicationAuditFunctionParameters(
	function *ast.FuncDecl,
) ([]*ast.Ident, bool) {
	if function.Type.Params == nil {
		return nil, false
	}
	var parameters []*ast.Ident
	for _, field := range function.Type.Params.List {
		if len(field.Names) == 0 {
			parameters = append(parameters, nil)
			continue
		}
		parameters = append(parameters, field.Names...)
	}
	if len(function.Type.Params.List) == 0 {
		return parameters, false
	}
	_, variadic := function.Type.Params.List[len(function.Type.Params.List)-1].Type.(*ast.Ellipsis)
	return parameters, variadic
}

func validateEnabledChildMarkerException(
	function *ast.FuncDecl,
	aliases map[string]string,
	dotImports map[string]bool,
) error {
	var rendered bytes.Buffer
	if err := format.Node(&rendered, token.NewFileSet(), function); err != nil {
		return fmt.Errorf("render enabled-child marker exception: %w", err)
	}
	source := rendered.String()
	required := map[string]int{
		`build.Env = environmentWithoutTestMarker(os.Environ())`:   1,
		`helper.Env = environmentWithoutTestMarker(os.Environ())`:  1,
		`helper.Args[0] = "__WISP_DECK_REPOSITORY_TEST_V1__.test"`: 1,
		`"/bin/bash"`: 1,
		`"$1" capabilities & child_pid=$!; wait "$child_pid"`:                          1,
		`HostEffectsDenialReason: "test_ancestor_sentinel"`:                            1,
		`-X main.HostEffectsCapability=enabled -X main.SoundPreviewCapability=enabled`: 1,
	}
	for shape, want := range required {
		if got := strings.Count(source, shape); got != want {
			return fmt.Errorf(
				"enabled-child marker exception contains %d %q shapes, want %d",
				got,
				shape,
				want,
			)
		}
	}
	if !applicationAuditHasExactCompositeFields(
		function,
		"testBinaryCapabilities",
		map[string]string{
			"HostEffectsCompiled":     "true",
			"SoundPreviewCompiled":    "true",
			"HostEffectsBoundary":     "1",
			"HostEffectsAllowed":      "false",
			"HostEffectsDenialReason": `"test_ancestor_sentinel"`,
		},
	) {
		return fmt.Errorf("enabled-child marker exception changed its capability expectation")
	}
	for target, want := range map[string]int{
		"build.Env":      1,
		"helper.Env":     1,
		"helper.Args[0]": 1,
	} {
		if got := countApplicationAuditAssignmentTarget(function, target); got != want {
			return fmt.Errorf(
				"enabled-child marker exception assigns %s %d times, want %d",
				target,
				got,
				want,
			)
		}
	}
	if countApplicationAuditProcessCalls(function, aliases, dotImports) != 2 {
		return fmt.Errorf("enabled-child marker exception changed its two-process shape")
	}
	for _, forbidden := range []string{
		"WISP_DECK_TESTING=0",
		"WISP_DECK_TESTING=stale",
		"env -u WISP_DECK_TESTING",
		"helper.Start(",
		"exec \"$1\"",
	} {
		if strings.Contains(source, forbidden) {
			return fmt.Errorf("enabled-child marker exception contains %q", forbidden)
		}
	}
	return nil
}

func validateMakeMarkerException(
	function *ast.FuncDecl,
	aliases map[string]string,
	dotImports map[string]bool,
) error {
	var rendered bytes.Buffer
	if err := format.Node(&rendered, token.NewFileSet(), function); err != nil {
		return fmt.Errorf("render Make marker exception: %w", err)
	}
	source := rendered.String()
	const allowed = "make WISP_DECK_TESTING=0 HOST_EFFECTS_CAPABILITY=enabled SOUND_PREVIEW_CAPABILITY=enabled build"
	if strings.Count(source, allowed) != 1 {
		return fmt.Errorf("Make marker regression must contain one literal %q", allowed)
	}
	if countCalls(function, "runBashSnippet") != 2 ||
		countApplicationAuditRenderedCalls(
			function,
			`runBashSnippet(t, command, nil)`,
		) != 1 {
		return fmt.Errorf("Make marker regression changed its exact nil-environment invocation")
	}
	if countApplicationAuditProcessCalls(function, aliases, dotImports) != 0 {
		return fmt.Errorf("Make marker regression launches a process directly")
	}
	for _, forbidden := range []string{
		"cmd.Env",
		".Env =",
		"env -u",
		"WISP_DECK_TESTING=stale",
		"exec.Command(",
		"exec.CommandContext(",
		"os.StartProcess(",
		"syscall.Exec(",
		"environmentWithoutTestMarker(",
		"repositoryTestEnvironment(",
		"buildEnv(",
	} {
		if strings.Contains(source, forbidden) {
			return fmt.Errorf("Make marker regression contains %q", forbidden)
		}
	}
	return nil
}

func countApplicationAuditAssignmentTarget(
	node ast.Node,
	target string,
) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, candidate := range assignment.Lhs {
			if rendered, ok := renderApplicationAuditNode(candidate); ok &&
				rendered == target {
				count++
			}
		}
		return true
	})
	return count
}

func applicationAuditHasExactCompositeFields(
	node ast.Node,
	typeName string,
	want map[string]string,
) bool {
	matches := 0
	ast.Inspect(node, func(node ast.Node) bool {
		composite, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		typeIdentifier, typeOK := composite.Type.(*ast.Ident)
		if !typeOK || typeIdentifier.Name != typeName {
			return true
		}
		got := make(map[string]string, len(composite.Elts))
		for _, element := range composite.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			key, keyOK := field.Key.(*ast.Ident)
			if !ok || !keyOK {
				return true
			}
			rendered, valueOK := renderApplicationAuditNode(field.Value)
			if !valueOK {
				return true
			}
			got[key.Name] = rendered
		}
		if len(got) != len(want) {
			return true
		}
		for name, value := range want {
			if got[name] != value {
				return true
			}
		}
		matches++
		return true
	})
	return matches == 1
}

func countApplicationAuditRenderedCalls(
	node ast.Node,
	exact string,
) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if rendered, ok := renderApplicationAuditNode(call); ok &&
			rendered == exact {
			count++
		}
		return true
	})
	return count
}

func countApplicationAuditProcessCalls(
	node ast.Node,
	aliases map[string]string,
	dotImports map[string]bool,
) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if _, process := processExecutableArgument(
			call,
			aliases,
			dotImports,
		); process {
			count++
		}
		return true
	})
	return count
}

func renderApplicationAuditNode(node ast.Node) (string, bool) {
	var rendered bytes.Buffer
	if err := format.Node(&rendered, token.NewFileSet(), node); err != nil {
		return "", false
	}
	return rendered.String(), true
}

func functionUsesMakeMarkerOverride(
	function *ast.FuncDecl,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
) bool {
	usesOverride := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !applicationAuditCallInvokesMake(
			call,
			aliases,
			dotImports,
			staticStrings,
		) {
			return true
		}
		for _, argument := range call.Args {
			for _, value := range resolvedStrings(argument, staticStrings) {
				if strings.Contains(value, "WISP_DECK_TESTING=0") {
					usesOverride = true
					return false
				}
			}
		}
		return true
	})
	return usesOverride
}

func applicationAuditCallInvokesMake(
	call *ast.CallExpr,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
) bool {
	if expressionName(call.Fun) == "runBashSnippet" && len(call.Args) > 1 {
		for _, value := range resolvedStrings(call.Args[1], staticStrings) {
			for _, field := range strings.Fields(value) {
				if filepath.Base(strings.Trim(field, `"';&|()`)) == "make" {
					return true
				}
			}
		}
		return false
	}
	if _, process := processExecutableArgument(call, aliases, dotImports); !process {
		return false
	}
	for _, argument := range call.Args {
		for _, value := range resolvedStrings(argument, staticStrings) {
			if filepath.Base(value) == "make" {
				return true
			}
		}
	}
	return false
}

func collectApplicationAuditStaticStrings(
	function *ast.FuncDecl,
) map[string]map[string]bool {
	values := map[string]map[string]bool{}
	for range 16 {
		changed := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			var names []*ast.Ident
			var expressions []ast.Expr
			switch node := node.(type) {
			case *ast.ValueSpec:
				names = node.Names
				expressions = node.Values
			case *ast.AssignStmt:
				for _, target := range node.Lhs {
					name, _ := target.(*ast.Ident)
					names = append(names, name)
				}
				expressions = node.Rhs
			default:
				return true
			}
			for index, expression := range expressions {
				if index >= len(names) || names[index] == nil {
					continue
				}
				changed = addResolvedStrings(
					values,
					names[index].Name,
					resolvedStrings(expression, values),
				) || changed
			}
			return true
		})
		if !changed {
			break
		}
	}
	return values
}

func collectApplicationAuditProcessAliases(
	function *ast.FuncDecl,
	aliases map[string]string,
	dotImports map[string]bool,
) {
	for range 16 {
		changed := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			var names []*ast.Ident
			var values []ast.Expr
			switch node := node.(type) {
			case *ast.ValueSpec:
				names = node.Names
				values = node.Values
			case *ast.AssignStmt:
				for _, target := range node.Lhs {
					name, _ := target.(*ast.Ident)
					names = append(names, name)
				}
				values = node.Rhs
			default:
				return true
			}
			for index, value := range values {
				if index >= len(names) || names[index] == nil {
					continue
				}
				importPath, constructor := calledPackageFunction(
					value,
					aliases,
					dotImports,
				)
				if _, process := processConstructorExecutableIndex(
					importPath,
					constructor,
				); !process {
					continue
				}
				target := importPath +
					processConstructorAliasSeparator +
					constructor
				if aliases[names[index].Name] != target {
					aliases[names[index].Name] = target
					changed = true
				}
			}
			return true
		})
		if !changed {
			return
		}
	}
}

func validateApplicationChildrenInFunction(
	path string,
	function *ast.FuncDecl,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
	knownApplicationValues map[string]bool,
	allowedUnnormalized map[string]bool,
) error {
	applicationValues := applicationValueVariables(
		function,
		aliases,
		dotImports,
		staticStrings,
		knownApplicationValues,
	)
	applicationCommands := applicationCommandVariables(
		function,
		applicationValues,
		aliases,
		dotImports,
		staticStrings,
	)
	environmentAssignments := make(map[string][]ast.Expr)
	procAttrEnvironments := collectApplicationAuditProcAttrEnvironments(function)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for index, target := range assignment.Lhs {
			selector, ok := target.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			command, commandOK := selector.X.(*ast.Ident)
			if !commandOK || selector.Sel.Name != "Env" ||
				index >= len(assignment.Rhs) {
				continue
			}
			environmentAssignments[command.Name] = append(
				environmentAssignments[command.Name],
				assignment.Rhs[index],
			)
		}
		return true
	})
	for command := range applicationCommands {
		if allowedUnnormalized[command] {
			continue
		}
		for _, environment := range environmentAssignments[command] {
			if err := validateApplicationAuditEnvironment(
				path,
				function.Name.Name,
				environment,
			); err != nil {
				return err
			}
		}
	}
	var auditErr error
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if auditErr != nil {
			return false
		}
		composite, compositeOK := node.(*ast.CompositeLit)
		if compositeOK && applicationExecCmdComposite(
			composite,
			applicationValues,
			aliases,
			dotImports,
			staticStrings,
		) {
			if environment, exists := applicationAuditCompositeField(
				composite,
				"Env",
			); exists {
				auditErr = validateApplicationAuditEnvironment(
					path,
					function.Name.Name,
					environment,
				)
				if auditErr != nil {
					return false
				}
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || !testCommandIsApplication(
			call,
			applicationValues,
			aliases,
			dotImports,
			staticStrings,
		) {
			return true
		}
		if callRemovesRepositoryTestMarker(
			call,
			aliases,
			dotImports,
			staticStrings,
		) {
			auditErr = fmt.Errorf(
				"%s:%s explicitly removes the repository marker from an application child",
				path,
				function.Name.Name,
			)
			return false
		}
		for _, environment := range applicationAuditCallEnvironments(
			call,
			aliases,
			dotImports,
			procAttrEnvironments,
		) {
			if err := validateApplicationAuditEnvironment(
				path,
				function.Name.Name,
				environment,
			); err != nil {
				auditErr = err
				return false
			}
		}
		return true
	})
	return auditErr
}

func applicationValueVariables(
	function *ast.FuncDecl,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
	known map[string]bool,
) map[string]bool {
	values := make(map[string]bool, len(known))
	for name := range known {
		values[name] = true
	}
	for range 16 {
		changed := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for index, value := range assignment.Rhs {
				if index >= len(assignment.Lhs) ||
					!expressionIsApplicationValue(
						value,
						values,
						staticStrings,
					) {
					continue
				}
				name, ok := assignment.Lhs[index].(*ast.Ident)
				if ok && !values[name.Name] {
					values[name.Name] = true
					changed = true
				}
			}
			call, ok := assignmentCall(assignment)
			if !ok || !goBuildsWispDeck(
				call,
				aliases,
				dotImports,
				staticStrings,
			) {
				return true
			}
			for index, argument := range call.Args {
				if !hasStringLiteral(argument, "-o") || index+1 >= len(call.Args) {
					continue
				}
				if name, ok := call.Args[index+1].(*ast.Ident); ok && !values[name.Name] {
					values[name.Name] = true
					changed = true
				}
			}
			return true
		})
		if !changed {
			break
		}
	}
	return values
}

func assignmentCall(assignment *ast.AssignStmt) (*ast.CallExpr, bool) {
	if len(assignment.Rhs) != 1 {
		return nil, false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	return call, ok
}

func expressionIsApplicationValue(
	expression ast.Expr,
	known map[string]bool,
	staticStrings map[string]map[string]bool,
) bool {
	for _, value := range resolvedStrings(expression, staticStrings) {
		if stringNamesRepositoryApplication(value) {
			return true
		}
	}
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.Ident:
			if known[node.Name] ||
				node.Name == "ghosttyBinaryPath" ||
				node.Name == "nativeLedgerBinary" ||
				node.Name == "buildTUI" {
				found = true
				return false
			}
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(node.Value)
			if err == nil && stringNamesRepositoryApplication(value) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func applicationCommandVariables(
	function *ast.FuncDecl,
	applicationValues map[string]bool,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
) map[string]bool {
	execCommands := applicationExecCmdVariables(function, aliases, dotImports)
	applications := map[string]bool{}
	for range 16 {
		changed := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			var names []*ast.Ident
			var values []ast.Expr
			switch node := node.(type) {
			case *ast.ValueSpec:
				names = node.Names
				values = node.Values
			case *ast.AssignStmt:
				for _, target := range node.Lhs {
					name, _ := target.(*ast.Ident)
					names = append(names, name)
				}
				values = node.Rhs
				for index, target := range node.Lhs {
					if index >= len(node.Rhs) {
						continue
					}
					selector, ok := target.(*ast.SelectorExpr)
					if !ok ||
						(selector.Sel.Name != "Path" && selector.Sel.Name != "Args") {
						continue
					}
					command, ok := selector.X.(*ast.Ident)
					if !ok || !execCommands[command.Name] {
						continue
					}
					if expressionIsApplicationValue(
						node.Rhs[index],
						applicationValues,
						staticStrings,
					) || (selector.Sel.Name == "Args" &&
						applicationAuditExpressionHasSubcommand(node.Rhs[index])) {
						if !applications[command.Name] {
							applications[command.Name] = true
							changed = true
						}
					}
				}
			default:
				return true
			}
			for index, value := range values {
				if index >= len(names) || names[index] == nil ||
					!applicationAuditCommandExpression(
						value,
						applications,
						applicationValues,
						aliases,
						dotImports,
						staticStrings,
					) {
					continue
				}
				if !applications[names[index].Name] {
					applications[names[index].Name] = true
					changed = true
				}
			}
			return true
		})
		if !changed {
			break
		}
	}
	return applications
}

func applicationExecCmdVariables(
	function *ast.FuncDecl,
	aliases map[string]string,
	dotImports map[string]bool,
) map[string]bool {
	commands := map[string]bool{}
	for range 16 {
		changed := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			var names []*ast.Ident
			var values []ast.Expr
			switch node := node.(type) {
			case *ast.ValueSpec:
				if isExecCmdType(node.Type, aliases, dotImports) {
					for _, name := range node.Names {
						if !commands[name.Name] {
							commands[name.Name] = true
							changed = true
						}
					}
				}
				names = node.Names
				values = node.Values
			case *ast.AssignStmt:
				for _, target := range node.Lhs {
					name, _ := target.(*ast.Ident)
					names = append(names, name)
				}
				values = node.Rhs
			default:
				return true
			}
			for index, value := range values {
				if index >= len(names) || names[index] == nil ||
					!isExecCmdExpression(value, aliases, dotImports, commands) {
					continue
				}
				if !commands[names[index].Name] {
					commands[names[index].Name] = true
					changed = true
				}
			}
			return true
		})
		if !changed {
			break
		}
	}
	return commands
}

func applicationAuditCommandExpression(
	expression ast.Expr,
	applicationCommands map[string]bool,
	applicationValues map[string]bool,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
) bool {
	switch expression := expression.(type) {
	case *ast.Ident:
		return applicationCommands[expression.Name]
	case *ast.ParenExpr:
		return applicationAuditCommandExpression(
			expression.X,
			applicationCommands,
			applicationValues,
			aliases,
			dotImports,
			staticStrings,
		)
	case *ast.UnaryExpr:
		return expression.Op == token.AND &&
			applicationAuditCommandExpression(
				expression.X,
				applicationCommands,
				applicationValues,
				aliases,
				dotImports,
				staticStrings,
			)
	case *ast.CallExpr:
		return testCommandIsApplication(
			expression,
			applicationValues,
			aliases,
			dotImports,
			staticStrings,
		)
	case *ast.CompositeLit:
		return applicationExecCmdComposite(
			expression,
			applicationValues,
			aliases,
			dotImports,
			staticStrings,
		)
	default:
		return false
	}
}

func applicationExecCmdComposite(
	composite *ast.CompositeLit,
	applicationValues map[string]bool,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
) bool {
	if !isExecCmdType(composite.Type, aliases, dotImports) {
		return false
	}
	for _, fieldName := range []string{"Path", "Args"} {
		value, exists := applicationAuditCompositeField(composite, fieldName)
		if !exists {
			continue
		}
		if expressionIsApplicationValue(
			value,
			applicationValues,
			staticStrings,
		) || (fieldName == "Args" &&
			applicationAuditExpressionHasSubcommand(value)) {
			return true
		}
	}
	return false
}

func applicationAuditCompositeField(
	composite *ast.CompositeLit,
	name string,
) (ast.Expr, bool) {
	for _, element := range composite.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		value, valueOK := field.Value.(ast.Expr)
		if ok && valueOK && key.Name == name {
			return value, true
		}
	}
	return nil, false
}

func applicationAuditExpressionHasSubcommand(expression ast.Expr) bool {
	for _, subcommand := range []string{
		"capabilities",
		"main-menu",
		"notification-sound",
	} {
		if hasStringLiteral(expression, subcommand) {
			return true
		}
	}
	return false
}

func stringNamesRepositoryApplication(value string) bool {
	lower := strings.ToLower(filepath.ToSlash(value))
	for _, marker := range []string{
		"wisp-deck-tui",
		"npx-wisp-deck.js",
		"/wrapper.sh",
		"/scripts/release.sh",
		"/lib/install.sh",
		"ghosttybinarypath",
		"nativeledgerbinary",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func goBuildsWispDeck(
	call *ast.CallExpr,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
) bool {
	importPath, function := calledPackageFunction(
		call.Fun,
		aliases,
		dotImports,
	)
	if importPath != "os/exec" ||
		(function != "Command" && function != "CommandContext") {
		return false
	}
	for _, argument := range call.Args {
		if expressionContainsResolvedString(
			argument,
			"./cmd/wisp-deck-tui",
			staticStrings,
		) {
			return true
		}
	}
	return false
}

func testCommandIsApplication(
	call *ast.CallExpr,
	applicationValues map[string]bool,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
) bool {
	executableIndex, process := processExecutableArgument(
		call,
		aliases,
		dotImports,
	)
	if !process || executableIndex >= len(call.Args) {
		return false
	}
	executable := call.Args[executableIndex]
	if applicationAuditExpressionNamesExecutable(
		executable,
		"go",
		staticStrings,
	) {
		if !applicationAuditCallHasStringArgument(
			call,
			executableIndex+1,
			"run",
			staticStrings,
		) {
			return false
		}
	}
	if applicationAuditExpressionNamesExecutable(
		executable,
		"make",
		staticStrings,
	) {
		return true
	}
	for _, argument := range call.Args[executableIndex:] {
		if expressionIsApplicationValue(
			argument,
			applicationValues,
			staticStrings,
		) {
			return true
		}
	}
	if len(resolvedStrings(executable, staticStrings)) == 0 {
		for _, subcommand := range []string{
			"capabilities",
			"main-menu",
			"notification-sound",
		} {
			if applicationAuditCallHasStringArgument(
				call,
				executableIndex+1,
				subcommand,
				staticStrings,
			) {
				return true
			}
		}
	}
	return false
}

func applicationAuditExpressionNamesExecutable(
	expression ast.Expr,
	name string,
	staticStrings map[string]map[string]bool,
) bool {
	for _, value := range resolvedStrings(expression, staticStrings) {
		if filepath.Base(value) == name {
			return true
		}
	}
	return false
}

func applicationAuditCallHasStringArgument(
	call *ast.CallExpr,
	start int,
	want string,
	staticStrings map[string]map[string]bool,
) bool {
	for _, argument := range call.Args[start:] {
		for _, value := range resolvedStrings(argument, staticStrings) {
			if value == want {
				return true
			}
		}
		if hasStringLiteral(argument, want) {
			return true
		}
	}
	return false
}

func expressionUsesNormalizedEnvironment(path string, expression ast.Expr) bool {
	switch expression := expression.(type) {
	case *ast.ParenExpr:
		return expressionUsesNormalizedEnvironment(path, expression.X)
	case *ast.CallExpr:
		function, ok := expression.Fun.(*ast.Ident)
		if !ok {
			return false
		}
		slashPath := filepath.ToSlash(path)
		if function.Obj != nil {
			declaration, ok := function.Obj.Decl.(*ast.FuncDecl)
			if !ok || declaration.Name.Name != function.Name {
				return false
			}
			switch function.Name {
			case "repositoryTestEnvironment":
				return slashPath == "test/bash/helpers_test.go" ||
					slashPath == "test/npx/helpers_test.go"
			case "buildEnv":
				return slashPath == "test/bash/helpers_test.go"
			default:
				return false
			}
		}
		switch function.Name {
		case "repositoryTestEnvironment":
			return true
		case "buildEnv":
			return true
		}
	}
	return false
}

func expressionHasNonRepositoryTestMarker(expression ast.Expr) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && strings.HasPrefix(value, "WISP_DECK_TESTING=") &&
			value != "WISP_DECK_TESTING=1" {
			found = true
			return false
		}
		return true
	})
	return found
}

func validateApplicationAuditEnvironment(
	path string,
	function string,
	environment ast.Expr,
) error {
	if expressionUsesNormalizedEnvironment(path, environment) {
		return nil
	}
	if expressionHasNonRepositoryTestMarker(environment) {
		return fmt.Errorf(
			"%s:%s overrides the repository test marker",
			path,
			function,
		)
	}
	return fmt.Errorf(
		"%s:%s replaces an application child environment without propagation",
		path,
		function,
	)
}

func applicationAuditCallEnvironments(
	call *ast.CallExpr,
	aliases map[string]string,
	dotImports map[string]bool,
	procAttrs map[*ast.Object]ast.Expr,
) []ast.Expr {
	importPath, function := calledPackageFunction(call.Fun, aliases, dotImports)
	switch {
	case (importPath == "syscall" ||
		strings.HasSuffix(importPath, "/unix")) &&
		function == "Exec":
		if len(call.Args) > 2 {
			return []ast.Expr{call.Args[2]}
		}
	case (importPath == "os" && function == "StartProcess") ||
		(importPath == "syscall" &&
			(function == "ForkExec" || function == "StartProcess")) ||
		(strings.HasSuffix(importPath, "/unix") && function == "ForkExec"):
		if len(call.Args) > 2 {
			if environment, exists := applicationAuditProcAttrEnvironment(
				call.Args[2],
				procAttrs,
			); exists {
				return []ast.Expr{environment}
			}
		}
	}
	return nil
}

func applicationAuditProcAttrEnvironment(
	expression ast.Expr,
	known map[*ast.Object]ast.Expr,
) (ast.Expr, bool) {
	switch expression := expression.(type) {
	case *ast.ParenExpr:
		return applicationAuditProcAttrEnvironment(expression.X, known)
	case *ast.UnaryExpr:
		if expression.Op == token.AND {
			return applicationAuditProcAttrEnvironment(expression.X, known)
		}
	case *ast.CompositeLit:
		return applicationAuditCompositeField(expression, "Env")
	case *ast.Ident:
		if expression.Obj != nil {
			environment, ok := known[expression.Obj]
			return environment, ok
		}
	}
	return nil, false
}

func collectApplicationAuditProcAttrEnvironments(
	function *ast.FuncDecl,
) map[*ast.Object]ast.Expr {
	environments := make(map[*ast.Object]ast.Expr)
	for range 16 {
		changed := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.ValueSpec:
				for index, value := range node.Values {
					if index >= len(node.Names) || node.Names[index].Obj == nil {
						continue
					}
					environment, ok := applicationAuditProcAttrEnvironment(
						value,
						environments,
					)
					if ok && environments[node.Names[index].Obj] != environment {
						environments[node.Names[index].Obj] = environment
						changed = true
					}
				}
			case *ast.AssignStmt:
				for index, value := range node.Rhs {
					if index >= len(node.Lhs) {
						continue
					}
					if name, ok := node.Lhs[index].(*ast.Ident); ok &&
						name.Obj != nil {
						environment, exists := applicationAuditProcAttrEnvironment(
							value,
							environments,
						)
						if exists && environments[name.Obj] != environment {
							environments[name.Obj] = environment
							changed = true
						}
						continue
					}
					selected, ok := node.Lhs[index].(*ast.SelectorExpr)
					if !ok {
						continue
					}
					receiver, receiverOK := selected.X.(*ast.Ident)
					if receiverOK && selected.Sel.Name == "Env" &&
						receiver.Obj != nil &&
						environments[receiver.Obj] != value {
						environments[receiver.Obj] = value
						changed = true
					}
				}
			}
			return true
		})
		if !changed {
			break
		}
	}
	return environments
}

func callRemovesRepositoryTestMarker(
	call *ast.CallExpr,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
) bool {
	executableIndex, process := processExecutableArgument(
		call,
		aliases,
		dotImports,
	)
	if !process || executableIndex >= len(call.Args) ||
		!applicationAuditExpressionNamesExecutable(
			call.Args[executableIndex],
			"env",
			staticStrings,
		) {
		return false
	}
	var values []string
	for _, argument := range call.Args[executableIndex+1:] {
		resolved := resolvedStrings(argument, staticStrings)
		if len(resolved) == 0 {
			ast.Inspect(argument, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err == nil {
					values = append(values, value)
				}
				return true
			})
			continue
		}
		values = append(values, resolved...)
	}
	for index, value := range values {
		switch {
		case value == "-i" || value == "--ignore-environment":
			return true
		case value == "-u" || value == "--unset":
			if index+1 < len(values) &&
				values[index+1] == "WISP_DECK_TESTING" {
				return true
			}
		case value == "-uWISP_DECK_TESTING" ||
			value == "--unset=WISP_DECK_TESTING":
			return true
		case strings.HasPrefix(value, "WISP_DECK_TESTING=") &&
			value != "WISP_DECK_TESTING=1":
			return true
		}
	}
	return false
}

func TestInstallerAndReleaseHostEffectBoundaryRejectsBypasses(t *testing.T) {
	sources := map[string]string{
		"bash":    repositorySource(t, "lib", "install.sh"),
		"node":    repositorySource(t, "bin", "npx-wisp-deck.js"),
		"release": repositorySource(t, "scripts", "release.sh"),
	}
	if err := validateInstallerAndReleaseHostEffectBoundary(sources); err != nil {
		t.Fatalf("current installer/release host-effect boundary rejected: %v", err)
	}

	mutations := map[string]map[string]string{
		"Bash existing artifact bypass": mutateBoundarySource(
			t,
			sources,
			"bash",
			`if verify_wisp_deck_tui_artifact "$version" "$installed_bin"; then`,
			`if "$installed_bin" --version >/dev/null; then`,
		),
		"Bash downloaded artifact bypass": mutateBoundarySource(
			t,
			sources,
			"bash",
			`verify_wisp_deck_tui_artifact "$version" || return 1`,
			`verify_binary_runs || return 1`,
		),
		"Bash replacement before verification": mutateBoundarySource(
			t,
			sources,
			"bash",
			`if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
			"mv -f \"$tmp\" \"$dest\"\n"+
				`  if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
		),
		"Bash copy before verification": mutateBoundarySource(
			t,
			sources,
			"bash",
			`if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
			"cp -f \"$tmp\" \"$dest\"\n"+
				`  if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
		),
		"Bash install before verification": mutateBoundarySource(
			t,
			sources,
			"bash",
			`if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
			"install -m 0755 \"$tmp\" \"$dest\"\n"+
				`  if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
		),
		"Bash braced destination before verification": mutateBoundarySource(
			t,
			sources,
			"bash",
			`if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
			"mv -f \"$tmp\" \"${dest}\"\n"+
				`  if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
		),
		"Bash destination alias before verification": mutateBoundarySource(
			t,
			sources,
			"bash",
			`if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
			"local destination=\"$dest\"\n"+
				"  cp -f \"$tmp\" \"$destination\"\n"+
				`  if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
		),
		"Bash helper replacement before verification": mutateBoundarySourceSequence(
			t,
			sources,
			"bash",
			[][2]string{
				{
					"install_binary() {",
					"replace_before_verification() { mv -f \"$tmp\" \"$dest\"; }\n\ninstall_binary() {",
				},
				{
					`  if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
					"  replace_before_verification\n" +
						`  if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
				},
			},
		),
		"Node existing artifact bypass": mutateBoundarySource(
			t,
			sources,
			"node",
			`const existing = verifyTuiBinary(tuiBinPath, version);`,
			`const existing = { valid: true, reported: "" };`,
		),
		"Node downloaded artifact bypass": mutateBoundarySource(
			t,
			sources,
			"node",
			`const downloaded = verifyTuiBinary(tmpPath, version);`,
			`const downloaded = { valid: true, reported: "" };`,
		),
		"Node replacement before verification": mutateBoundarySource(
			t,
			sources,
			"node",
			`const downloaded = verifyTuiBinary(tmpPath, version);`,
			"fs.renameSync(tmpPath, tuiBinPath);\n"+
				`  const downloaded = verifyTuiBinary(tmpPath, version);`,
		),
		"Node copy before verification": mutateBoundarySource(
			t,
			sources,
			"node",
			`const downloaded = verifyTuiBinary(tmpPath, version);`,
			"fs.copyFileSync(tmpPath, tuiBinPath);\n"+
				`  const downloaded = verifyTuiBinary(tmpPath, version);`,
		),
		"Node write before verification": mutateBoundarySource(
			t,
			sources,
			"node",
			`const downloaded = verifyTuiBinary(tmpPath, version);`,
			"fs.writeFileSync(tuiBinPath, fs.readFileSync(tmpPath));\n"+
				`  const downloaded = verifyTuiBinary(tmpPath, version);`,
		),
		"Node destination alias before verification": mutateBoundarySource(
			t,
			sources,
			"node",
			`const downloaded = verifyTuiBinary(tmpPath, version);`,
			"const destination = tuiBinPath;\n"+
				"  fs.copyFileSync(tmpPath, destination);\n"+
				`  const downloaded = verifyTuiBinary(tmpPath, version);`,
		),
		"Node mutation function alias before verification": mutateBoundarySource(
			t,
			sources,
			"node",
			`const downloaded = verifyTuiBinary(tmpPath, version);`,
			"const replace = fs.renameSync;\n"+
				"  replace(tmpPath, tuiBinPath);\n"+
				`  const downloaded = verifyTuiBinary(tmpPath, version);`,
		),
		"Node mutation function alias chain before verification": mutateBoundarySource(
			t,
			sources,
			"node",
			`const downloaded = verifyTuiBinary(tmpPath, version);`,
			"const replace = fs.renameSync;\n"+
				"  const replaceAgain = replace;\n"+
				"  replaceAgain(tmpPath, tuiBinPath);\n"+
				`  const downloaded = verifyTuiBinary(tmpPath, version);`,
		),
		"Node helper replacement before verification": mutateBoundarySourceSequence(
			t,
			sources,
			"node",
			[][2]string{
				{
					"function ensureTuiBinary(version) {",
					"function replaceBeforeVerification(source, destination) {\n" +
						"  fs.renameSync(source, destination);\n" +
						"}\n\nfunction ensureTuiBinary(version) {",
				},
				{
					`  const downloaded = verifyTuiBinary(tmpPath, version);`,
					"  replaceBeforeVerification(tmpPath, tuiBinPath);\n" +
						`  const downloaded = verifyTuiBinary(tmpPath, version);`,
				},
			},
		),
		"Node replacement before failed verification returns": mutateBoundarySource(
			t,
			sources,
			"node",
			`if (!downloaded.valid) {`,
			"if (!downloaded.valid) {\n"+
				"    fs.renameSync(tmpPath, tuiBinPath);",
		),
		"release amd64 metadata bypass": mutateBoundarySource(
			t,
			sources,
			"release",
			`if ! verify_release_tui_metadata "$amd64_asset" "$expected_ldflags"; then`,
			`if false; then`,
		),
		"release host probe bypass": mutateBoundarySource(
			t,
			sources,
			"release",
			`if ! "$host_asset" capabilities --require-production >/dev/null; then`,
			`if false; then`,
		),
		"release mutation before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then
    echo "Error: release TUI artifact preflight failed; refusing to mutate release state" >&2
    exit 1
  fi
  codesign --sign - --force "$build_dir/wisp-deck-tui-darwin-arm64"`,
			`  codesign --sign - --force "$build_dir/wisp-deck-tui-darwin-arm64"
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then
    echo "Error: release TUI artifact preflight failed; refusing to mutate release state" >&2
	    exit 1
	  fi`,
		),
		"release alternate tag before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  git -C "$project_dir" tag -a "$tag" -m "Release $tag"
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release alternate push before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  git -C "$project_dir" push origin main --tags
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release alternate publish before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  npm --prefix "$project_dir" publish
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release alternate codesign before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  command /usr/bin/codesign -s - "$build_dir/wisp-deck-tui-darwin-arm64"
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release alternate GitHub release before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  command gh --repo JackUait/wisp-deck release create "$tag"
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release command-wrapped tag before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  command git --git-dir="$project_dir/.git" tag "$tag"
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release command-wrapped push before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  command git --git-dir="$project_dir/.git" push origin main --tags
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release command-wrapped publish before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  command npm --prefix "$project_dir" publish
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release alternate codesign inside failed preflight": mutateBoundarySource(
			t,
			sources,
			"release",
			`    echo "Error: release TUI artifact preflight failed; refusing to mutate release state" >&2`,
			`    command codesign -s - "$build_dir/wisp-deck-tui-darwin-arm64"
    echo "Error: release TUI artifact preflight failed; refusing to mutate release state" >&2`,
		),
		"release copy local binary before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  cp "$build_dir/wisp-deck-tui-darwin-arm64" "$HOME/.local/bin/wisp-deck-tui"
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release install local binary before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  install -m 0755 "$build_dir/wisp-deck-tui-darwin-arm64" "$HOME/.local/bin/wisp-deck-tui"
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release shell wrapped publish before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  bash -c 'npm publish'
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release eval publish before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  eval 'npm publish'
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release command substitution publish before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  published="$(npm publish)"
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release backtick publish before verification": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			"  published=`npm publish`\n"+
				`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release helper publish before verification": mutateBoundarySourceSequence(
			t,
			sources,
			"release",
			[][2]string{
				{
					`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
					`  publish_before_verification
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
				},
				{
					`# Only run main when executed directly (not sourced for testing)`,
					`publish_before_verification(){ npm publish; }
# Only run main when executed directly (not sourced for testing)`,
				},
			},
		),
	}
	for name, mutated := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := validateInstallerAndReleaseHostEffectBoundary(mutated); err == nil {
				t.Fatal("installer/release host-effect boundary mutation escaped validation")
			}
		})
	}

	harmless := map[string]map[string]string{
		"Bash comments and diagnostics": mutateBoundarySource(
			t,
			sources,
			"bash",
			`if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
			`# mv "$tmp" "${dest}" is intentionally delayed until verification
  info 'cp/install/mv destination operations are delayed'
  if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
		),
		"Node comments and diagnostics": mutateBoundarySource(
			t,
			sources,
			"node",
			`const downloaded = verifyTuiBinary(tmpPath, version);`,
			`// fs.copyFileSync(tmpPath, destination) is intentionally delayed.
  process.stdout.write('fs.renameSync and fs.writeFileSync stay after verification');
	  const downloaded = verifyTuiBinary(tmpPath, version);`,
		),
		"Node non-mutating function alias": mutateBoundarySource(
			t,
			sources,
			"node",
			`const downloaded = verifyTuiBinary(tmpPath, version);`,
			`const inspect = fs.statSync;
  inspect(tmpPath);
  const downloaded = verifyTuiBinary(tmpPath, version);`,
		),
		"release comments diagnostics and read-only commands": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  # codesign, git tag, git push, gh release create, and npm publish follow verification.
  echo "codesign git tag git push gh release create npm publish remain delayed"
  git status --porcelain >/dev/null
  gh auth status >/dev/null
  npm --version >/dev/null
	  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release read-only shell and eval wrappers": mutateBoundarySource(
			t,
			sources,
			"release",
			`  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
			`  bash -c 'git status --porcelain'
  eval 'npm --version'
  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
		),
		"release uncalled mutating helper": mutateBoundarySource(
			t,
			sources,
			"release",
			`# Only run main when executed directly (not sourced for testing)`,
			`publish_after_verification() { npm publish; }
# Only run main when executed directly (not sourced for testing)`,
		),
	}
	for name, mutated := range harmless {
		t.Run("allows "+name, func(t *testing.T) {
			if err := validateInstallerAndReleaseHostEffectBoundary(mutated); err != nil {
				t.Fatalf("harmless installer/release source rejected: %v", err)
			}
		})
	}

}

func validateInstallerAndReleaseHostEffectBoundary(
	sources map[string]string,
) error {
	bash := sources["bash"]
	for _, required := range []string{
		`reported="$("$bin" --version 2>/dev/null)" || return 1`,
		`[[ "$reported" == "wisp-deck-tui version $expected" ]] || return 1`,
		`capabilities="$("$bin" capabilities --require-production 2>/dev/null)" || return 1`,
		`(.host_effects_compiled | type == "boolean")`,
		`.host_effects_compiled == true`,
		`(.sound_preview_compiled | type == "boolean")`,
		`.sound_preview_compiled == true`,
		`(.host_effects_boundary | type == "number")`,
		`(.host_effects_boundary | floor == .)`,
		`.host_effects_boundary == 1`,
		`if verify_wisp_deck_tui_artifact "$version" "$installed_bin"; then`,
		`verify_wisp_deck_tui_artifact "$version" || return 1`,
	} {
		if strings.Count(bash, required) != 1 {
			return fmt.Errorf("Bash installer must contain exactly one %q", required)
		}
	}
	installBinary, err := extractAuditFunction(
		bash,
		"install_binary() {",
		false,
	)
	if err != nil {
		return fmt.Errorf("Bash installer: %w", err)
	}
	const bashVerificationGuard = `  if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then
    rm -f "$tmp"
    warn "Downloaded $display_name failed verification — keeping existing install"
    return 1
  fi`
	bashGuardEnd, err := exactAuditBoundaryEnd(
		installBinary.body,
		bashVerificationGuard,
		"Bash downloaded-artifact verification guard",
	)
	if err != nil {
		return err
	}
	const bashReplacement = `  mv -f "$tmp" "$dest"`
	bashReplacementPosition := strings.Index(
		installBinary.body,
		bashReplacement,
	)
	if strings.Count(installBinary.body, bashReplacement) != 1 ||
		bashReplacementPosition <= bashGuardEnd {
		return fmt.Errorf(
			"Bash installer must replace the artifact exactly once after successful verification",
		)
	}
	bashWithoutReplacement := strings.Replace(
		installBinary.body,
		bashReplacement,
		"",
		1,
	)
	bashFunctions, err := shellAuditFunctionDefinitions(bash)
	if err != nil {
		return fmt.Errorf("Bash installer helper audit: %w", err)
	}
	if mutation := installerArtifactMutationInFlow(
		bashWithoutReplacement,
		bashFunctions,
		make(map[string]bool),
	); mutation != "" {
		return fmt.Errorf(
			"Bash install_binary contains an extra %s artifact mutation",
			mutation,
		)
	}

	node := sources["node"]
	for _, required := range []string{
		`reported !== ` + "`wisp-deck-tui version ${expectedVersion}`",
		`['capabilities', '--require-production']`,
		`capabilities.host_effects_compiled === true`,
		`capabilities.sound_preview_compiled === true`,
		`Number.isInteger(capabilities.host_effects_boundary)`,
		`capabilities.host_effects_boundary === 1`,
		`const existing = verifyTuiBinary(tuiBinPath, version);`,
		`const downloaded = verifyTuiBinary(tmpPath, version);`,
	} {
		if strings.Count(node, required) != 1 {
			return fmt.Errorf("Node installer must contain exactly one %q", required)
		}
	}
	ensureTUI, err := extractAuditFunction(
		node,
		"function ensureTuiBinary(version) {",
		true,
	)
	if err != nil {
		return fmt.Errorf("Node installer: %w", err)
	}
	const nodeVerificationGuard = `  const downloaded = verifyTuiBinary(tmpPath, version);
  if (!downloaded.valid) {
    fs.rmSync(tmpPath, { force: true });
    process.stderr.write(` + "`" + `Downloaded wisp-deck-tui failed verification (expected version ${version}, got ${JSON.stringify(downloaded.reported)}).\n` + "`" + `);
    process.stderr.write('The existing install (if any) was left untouched. Please retry, and report this if it persists.\n');
    process.exit(1);
  }`
	nodeGuardEnd, err := exactAuditBoundaryEnd(
		ensureTUI.body,
		nodeVerificationGuard,
		"Node downloaded-artifact verification guard",
	)
	if err != nil {
		return err
	}
	const nodeReplacement = `  fs.renameSync(tmpPath, tuiBinPath);`
	nodeReplacementPosition := strings.Index(ensureTUI.body, nodeReplacement)
	if strings.Count(ensureTUI.body, nodeReplacement) != 1 ||
		nodeReplacementPosition <= nodeGuardEnd {
		return fmt.Errorf(
			"Node installer must replace the artifact exactly once after successful verification",
		)
	}
	nodeWithoutReplacement := strings.Replace(
		ensureTUI.body,
		nodeReplacement,
		"",
		1,
	)
	if method := javascriptAuditCallsMutationMethodInFlow(
		node,
		nodeWithoutReplacement,
	); method != "" {
		return fmt.Errorf(
			"Node ensureTuiBinary contains an extra fs.%s artifact mutation",
			method,
		)
	}

	release := sources["release"]
	for _, required := range []string{
		`if ! verify_release_tui_metadata "$arm64_asset" "$expected_ldflags"; then`,
		`if ! verify_release_tui_metadata "$amd64_asset" "$expected_ldflags"; then`,
		`if ! "$host_asset" capabilities --require-production >/dev/null; then`,
		`if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
	} {
		if strings.Count(release, required) != 1 {
			return fmt.Errorf("release preflight must contain exactly one %q", required)
		}
	}
	releaseMain, err := extractAuditFunction(release, "main() {", false)
	if err != nil {
		return fmt.Errorf("release script: %w", err)
	}
	armBuild := strings.Index(
		releaseMain.body,
		`GOOS=darwin GOARCH=arm64 go build -ldflags "$ldflags"`,
	)
	amdBuild := strings.Index(
		releaseMain.body,
		`GOOS=darwin GOARCH=amd64 go build -ldflags "$ldflags"`,
	)
	const releaseVerificationGuard = `  if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then
    echo "Error: release TUI artifact preflight failed; refusing to mutate release state" >&2
    exit 1
  fi`
	preflightEnd, err := exactAuditBoundaryEnd(
		releaseMain.body,
		releaseVerificationGuard,
		"release artifact verification guard",
	)
	if err != nil {
		return err
	}
	preflightStart := strings.Index(
		releaseMain.body,
		releaseVerificationGuard,
	)
	if armBuild < 0 || amdBuild <= armBuild || preflightStart <= amdBuild {
		return fmt.Errorf("release artifact verification does not follow both builds")
	}
	releaseFunctions, err := shellAuditFunctionDefinitions(release)
	if err != nil {
		return fmt.Errorf("release helper audit: %w", err)
	}
	if mutation := releaseStateMutationInFlow(
		releaseMain.body[:preflightEnd],
		releaseFunctions,
		make(map[string]bool),
	); mutation != "" {
		return fmt.Errorf(
			"release mutation %s precedes successful artifact verification",
			mutation,
		)
	}
	for _, mutation := range []string{
		`codesign --sign - --force "$build_dir/wisp-deck-tui-darwin-arm64"`,
		`git tag -a "$tag"`,
		`git push origin main --tags`,
		`gh release create "$tag"`,
		`local publish_cmd="npm publish"`,
		`go build -ldflags "$ldflags" -o "$local_bin"`,
	} {
		position := strings.Index(releaseMain.body, mutation)
		if position < 0 || position <= preflightEnd {
			return fmt.Errorf("release mutation %q precedes artifact verification", mutation)
		}
	}
	return nil
}

type auditFunctionSource struct {
	start int
	body  string
}

func extractAuditFunction(
	source string,
	signature string,
	javascript bool,
) (auditFunctionSource, error) {
	if strings.Count(source, signature) != 1 {
		return auditFunctionSource{}, fmt.Errorf(
			"function signature %q must occur exactly once",
			signature,
		)
	}
	start := strings.Index(source, signature)
	open := start + strings.LastIndex(signature, "{")
	depth := 1
	const (
		auditCode = iota
		auditSingleQuote
		auditDoubleQuote
		auditBacktick
		auditLineComment
		auditBlockComment
	)
	state := auditCode
	for index := open + 1; index < len(source); index++ {
		character := source[index]
		switch state {
		case auditSingleQuote:
			if character == '\'' {
				state = auditCode
			}
			continue
		case auditDoubleQuote:
			if character == '\\' && index+1 < len(source) {
				index++
			} else if character == '"' {
				state = auditCode
			}
			continue
		case auditBacktick:
			if character == '\\' && index+1 < len(source) {
				index++
			} else if character == '`' {
				state = auditCode
			}
			continue
		case auditLineComment:
			if character == '\n' {
				state = auditCode
			}
			continue
		case auditBlockComment:
			if character == '*' && index+1 < len(source) &&
				source[index+1] == '/' {
				index++
				state = auditCode
			}
			continue
		}
		switch {
		case character == '\'':
			state = auditSingleQuote
		case character == '"':
			state = auditDoubleQuote
		case character == '`':
			state = auditBacktick
		case javascript && character == '/' && index+1 < len(source) &&
			source[index+1] == '/':
			index++
			state = auditLineComment
		case javascript && character == '/' && index+1 < len(source) &&
			source[index+1] == '*':
			index++
			state = auditBlockComment
		case !javascript && character == '#' &&
			shellAuditCommentStarts(source, index):
			state = auditLineComment
		case character == '{':
			depth++
		case character == '}':
			depth--
			if depth == 0 {
				return auditFunctionSource{
					start: start,
					body:  source[open+1 : index],
				}, nil
			}
		}
	}
	return auditFunctionSource{}, fmt.Errorf(
		"function %q has no balanced closing brace",
		signature,
	)
}

func shellAuditCommentStarts(source string, index int) bool {
	if source[index] != '#' {
		return false
	}
	if index == 0 {
		return true
	}
	previous := source[index-1]
	if previous == '$' ||
		(previous == '{' && index >= 2 && source[index-2] == '$') {
		return false
	}
	return previous == ' ' || previous == '\t' || previous == '\n' ||
		previous == ';' || previous == '|' || previous == '&' ||
		previous == '(' || previous == ')'
}

func exactAuditBoundaryEnd(
	source string,
	boundary string,
	description string,
) (int, error) {
	if strings.Count(source, boundary) != 1 {
		return 0, fmt.Errorf(
			"%s must occur exactly once",
			description,
		)
	}
	return strings.Index(source, boundary) + len(boundary), nil
}

func javascriptAuditCallsMutationMethod(source string) string {
	tokens, ok := lexJavaScriptAudit(source)
	if !ok {
		return "unparseable-source"
	}
	bindings := collectJavaScriptAuditBindings(
		tokens,
		javascriptStringConstants(tokens),
	)
	for index, token := range tokens {
		if token.kind != '(' {
			continue
		}
		method := javascriptCallMethod(tokens, index)
		canonical := strings.ToLower(method)
		if alias := bindings.fsMutationFunctions[method]; alias != "" {
			canonical = alias
		}
		if javascriptFSMutationMethod(canonical) &&
			(bindings.fsMutationFunctions[method] != "" ||
				javascriptTokensReferenceFSObject(
					javascriptCallReceiverTokens(tokens, index),
					bindings,
				)) {
			return canonical
		}
	}
	return ""
}

func javascriptAuditCallsMutationMethodInFlow(
	completeSource string,
	entrySource string,
) string {
	completeTokens, ok := lexJavaScriptAudit(completeSource)
	if !ok {
		return "unparseable-source"
	}
	entryTokens, ok := lexJavaScriptAudit(entrySource)
	if !ok {
		return "unparseable-entry"
	}
	bindings := collectJavaScriptAuditBindings(
		completeTokens,
		javascriptStringConstants(completeTokens),
	)
	functions := javascriptAuditFunctions(completeTokens)
	methodOwners := javascriptAuditMethodOwners(completeTokens)
	var visit func([]javascriptAuditToken, map[string]bool) string
	visit = func(
		tokens []javascriptAuditToken,
		visiting map[string]bool,
	) string {
		for index, token := range tokens {
			if token.kind != '(' {
				continue
			}
			method := javascriptCallMethod(tokens, index)
			canonical := strings.ToLower(method)
			if alias := bindings.fsMutationFunctions[method]; alias != "" {
				canonical = alias
			}
			if javascriptFSMutationMethod(canonical) &&
				(bindings.fsMutationFunctions[method] != "" ||
					javascriptTokensReferenceFSObject(
						javascriptCallReceiverTokens(tokens, index),
						bindings,
					)) {
				return canonical
			}
			functionKey := method
			function, helper := functions[functionKey]
			if !helper {
				functionKey = javascriptAuditTopLevelFunctionKey(
					method,
					methodOwners,
					functions,
				)
				function, helper = functions[functionKey]
			}
			if !helper || visiting[functionKey] {
				continue
			}
			visiting[functionKey] = true
			mutation := visit(function.body, visiting)
			delete(visiting, functionKey)
			if mutation != "" {
				return method + " -> " + mutation
			}
		}
		return ""
	}
	return visit(entryTokens, make(map[string]bool))
}

func javascriptAuditTopLevelFunctionKey(
	name string,
	owners javascriptAuditMethodOwnerSet,
	functions map[string]javascriptAuditFunction,
) string {
	var selected string
	for _, binding := range owners.byName[name] {
		if binding.scopeStart != -1 || binding.key == "" {
			continue
		}
		if _, function := functions[binding.key]; !function {
			continue
		}
		if selected != "" && selected != binding.key {
			return ""
		}
		selected = binding.key
	}
	return selected
}

func installerArtifactMutationInFlow(
	source string,
	functions map[string]string,
	visiting map[string]bool,
) string {
	for _, substitution := range shellAuditCommandSubstitutions(source) {
		if mutation := installerArtifactMutationInFlow(
			substitution,
			functions,
			visiting,
		); mutation != "" {
			return "substitution -> " + mutation
		}
	}
	for _, command := range shellAuditCommands(source) {
		executable := strings.ToLower(filepath.Base(command.executable))
		switch executable {
		case "cp", "install", "mv":
			return executable
		case "bash", "sh", "zsh":
			for index, argument := range command.arguments {
				if argument != "-c" || index+1 >= len(command.arguments) {
					continue
				}
				if mutation := installerArtifactMutationInFlow(
					command.arguments[index+1],
					functions,
					visiting,
				); mutation != "" {
					return executable + " -c -> " + mutation
				}
			}
		case "eval":
			if mutation := installerArtifactMutationInFlow(
				strings.Join(command.arguments, " "),
				functions,
				visiting,
			); mutation != "" {
				return "eval -> " + mutation
			}
		}
		name := filepath.Base(command.executable)
		body, helper := functions[name]
		if !helper || visiting[name] {
			continue
		}
		visiting[name] = true
		mutation := installerArtifactMutationInFlow(body, functions, visiting)
		delete(visiting, name)
		if mutation != "" {
			return name + " -> " + mutation
		}
	}
	return ""
}

type shellAuditToken struct {
	value     string
	separator bool
}

type shellAuditCommand struct {
	executable string
	arguments  []string
}

func shellAuditCommands(source string) []shellAuditCommand {
	tokens := lexShellAudit(source)
	var commands []shellAuditCommand
	start := 0
	for index := 0; index <= len(tokens); index++ {
		if index < len(tokens) && !tokens[index].separator {
			continue
		}
		if command, ok := shellAuditCommandFromSegment(tokens[start:index]); ok {
			commands = append(commands, command)
		}
		start = index + 1
	}
	return commands
}

func shellAuditCommandSubstitutions(source string) []string {
	var substitutions []string
	const (
		substitutionCode = iota
		substitutionSingleQuote
		substitutionDoubleQuote
	)
	state := substitutionCode
	for index := 0; index < len(source); index++ {
		character := source[index]
		switch state {
		case substitutionSingleQuote:
			if character == '\'' {
				state = substitutionCode
			}
			continue
		case substitutionDoubleQuote:
			if character == '\\' && index+1 < len(source) {
				index++
				continue
			}
			if character == '"' {
				state = substitutionCode
				continue
			}
		default:
			if character == '\\' && index+1 < len(source) {
				index++
				continue
			}
			if character == '#' &&
				shellAuditCommentStarts(source, index) {
				for index < len(source) && source[index] != '\n' {
					index++
				}
				continue
			}
			if character == '\'' {
				state = substitutionSingleQuote
				continue
			}
			if character == '"' {
				state = substitutionDoubleQuote
				continue
			}
		}
		if character == '`' {
			end := index + 1
			for end < len(source) {
				if source[end] == '\\' && end+1 < len(source) {
					end += 2
					continue
				}
				if source[end] == '`' {
					break
				}
				end++
			}
			if end < len(source) {
				substitutions = append(
					substitutions,
					source[index+1:end],
				)
				index = end
			}
			continue
		}
		if character != '$' || index+1 >= len(source) ||
			source[index+1] != '(' {
			continue
		}
		open := index + 1
		depth := 1
		var quote byte
		end := open + 1
		for ; end < len(source); end++ {
			current := source[end]
			if quote != 0 {
				if current == '\\' && quote != '\'' && end+1 < len(source) {
					end++
					continue
				}
				if current == quote {
					quote = 0
				}
				continue
			}
			if current == '\'' || current == '"' || current == '`' {
				quote = current
				continue
			}
			if current == '\\' && end+1 < len(source) {
				end++
				continue
			}
			switch current {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					substitutions = append(
						substitutions,
						source[open+1:end],
					)
					index = end
					end = len(source)
				}
			}
		}
	}
	return substitutions
}

func lexShellAudit(source string) []shellAuditToken {
	var tokens []shellAuditToken
	for index := 0; index < len(source); {
		character := source[index]
		if character == ' ' || character == '\t' || character == '\r' {
			index++
			continue
		}
		if character == '\n' {
			tokens = append(tokens, shellAuditToken{
				value:     "\n",
				separator: true,
			})
			index++
			continue
		}
		if character == '#' && shellAuditCommentStarts(source, index) {
			for index < len(source) && source[index] != '\n' {
				index++
			}
			continue
		}
		if strings.ContainsRune(";|&(){}", rune(character)) {
			operator := string(character)
			if index+1 < len(source) && source[index+1] == character &&
				(character == '&' || character == '|') {
				operator += string(character)
				index++
			}
			tokens = append(tokens, shellAuditToken{
				value:     operator,
				separator: true,
			})
			index++
			continue
		}

		var word strings.Builder
		for index < len(source) {
			character = source[index]
			if character == ' ' || character == '\t' ||
				character == '\r' || character == '\n' ||
				strings.ContainsRune(";|&(){}", rune(character)) {
				break
			}
			if character == '\\' && index+1 < len(source) {
				index++
				word.WriteByte(source[index])
				index++
				continue
			}
			if character == '\'' || character == '"' || character == '`' {
				quote := character
				index++
				for index < len(source) && source[index] != quote {
					if source[index] == '\\' && quote != '\'' &&
						index+1 < len(source) {
						index++
					}
					word.WriteByte(source[index])
					index++
				}
				if index < len(source) {
					index++
				}
				continue
			}
			word.WriteByte(character)
			index++
		}
		if word.Len() > 0 {
			tokens = append(tokens, shellAuditToken{value: word.String()})
		}
	}
	return tokens
}

func shellAuditCommandFromSegment(
	segment []shellAuditToken,
) (shellAuditCommand, bool) {
	words := make([]string, 0, len(segment))
	for _, token := range segment {
		if !token.separator && token.value != "" {
			words = append(words, token.value)
		}
	}
	index := 0
	for index < len(words) {
		switch words[index] {
		case "!", "if", "then", "elif", "else", "while", "until", "do",
			"for", "select", "case", "time":
			index++
			continue
		}
		if shellAuditAssignment(words[index]) {
			index++
			continue
		}
		break
	}
	if index >= len(words) {
		return shellAuditCommand{}, false
	}
	for {
		executable := filepath.Base(words[index])
		switch executable {
		case "command", "builtin":
			index++
			for index < len(words) && strings.HasPrefix(words[index], "-") {
				index++
			}
		case "env":
			index++
			for index < len(words) &&
				(strings.HasPrefix(words[index], "-") ||
					shellAuditAssignment(words[index])) {
				if (words[index] == "-u" || words[index] == "--unset") &&
					index+1 < len(words) {
					index++
				}
				index++
			}
		case "sudo", "xcrun":
			index++
			for index < len(words) && strings.HasPrefix(words[index], "-") {
				index++
			}
		default:
			return shellAuditCommand{
				executable: words[index],
				arguments:  append([]string(nil), words[index+1:]...),
			}, true
		}
		if index >= len(words) {
			return shellAuditCommand{}, false
		}
	}
}

func shellAuditAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" {
		return false
	}
	for index, character := range name {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			character != '_' &&
			(index == 0 || character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func releaseStateMutation(command shellAuditCommand) string {
	executable := strings.ToLower(filepath.Base(command.executable))
	switch executable {
	case "codesign":
		return "codesign"
	case "git":
		if shellAuditHasArgument(command.arguments, "tag") {
			return "git tag"
		}
		if shellAuditHasArgument(command.arguments, "push") {
			return "git push"
		}
	case "gh":
		if shellAuditHasOrderedArguments(
			command.arguments,
			"release",
			"create",
		) {
			return "gh release create"
		}
	case "npm":
		if shellAuditHasArgument(command.arguments, "publish") {
			return "npm publish"
		}
	case "cp", "install", "mv":
		for _, argument := range command.arguments {
			if strings.Contains(argument, ".local/bin/wisp-deck-tui") ||
				argument == "$local_bin" ||
				argument == "${local_bin}" {
				return executable + " local binary"
			}
		}
	case "go":
		if shellAuditHasArgument(command.arguments, "build") &&
			(shellAuditHasArgument(command.arguments, "$local_bin") ||
				shellAuditHasArgument(command.arguments, "${local_bin}")) {
			return "go build local binary"
		}
	}
	return ""
}

func shellAuditFunctionDefinitions(source string) (map[string]string, error) {
	definitions := make(map[string]string)
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		open := strings.Index(trimmed, "{")
		if open <= 0 {
			continue
		}
		header := strings.TrimSpace(trimmed[:open])
		compact := strings.NewReplacer(" ", "", "\t", "").Replace(header)
		var name string
		switch {
		case strings.HasSuffix(compact, "()"):
			name = strings.TrimSuffix(compact, "()")
		case strings.HasPrefix(header, "function "):
			name = strings.TrimSpace(strings.TrimPrefix(header, "function "))
		default:
			continue
		}
		if !shellAuditIdentifier(name) {
			continue
		}
		signature := trimmed[:open+1]
		function, err := extractAuditFunction(
			source,
			signature,
			false,
		)
		if err != nil {
			return nil, err
		}
		definitions[name] = function.body
	}
	return definitions, nil
}

func shellAuditIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range name {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			character == '_' ||
			(index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func releaseStateMutationInFlow(
	source string,
	functions map[string]string,
	visiting map[string]bool,
) string {
	for _, substitution := range shellAuditCommandSubstitutions(source) {
		if mutation := releaseStateMutationInFlow(
			substitution,
			functions,
			visiting,
		); mutation != "" {
			return "substitution -> " + mutation
		}
	}
	for _, command := range shellAuditCommands(source) {
		if mutation := releaseStateMutation(command); mutation != "" {
			return mutation
		}
		executable := strings.ToLower(filepath.Base(command.executable))
		switch executable {
		case "bash", "sh", "zsh":
			for index, argument := range command.arguments {
				if argument != "-c" || index+1 >= len(command.arguments) {
					continue
				}
				if mutation := releaseStateMutationInFlow(
					command.arguments[index+1],
					functions,
					visiting,
				); mutation != "" {
					return executable + " -c " + mutation
				}
			}
		case "eval":
			if mutation := releaseStateMutationInFlow(
				strings.Join(command.arguments, " "),
				functions,
				visiting,
			); mutation != "" {
				return "eval " + mutation
			}
		}
		name := filepath.Base(command.executable)
		body, helper := functions[name]
		if !helper || visiting[name] {
			continue
		}
		visiting[name] = true
		mutation := releaseStateMutationInFlow(body, functions, visiting)
		delete(visiting, name)
		if mutation != "" {
			return name + " -> " + mutation
		}
	}
	return ""
}

func shellAuditHasArgument(arguments []string, want string) bool {
	for _, argument := range arguments {
		if argument == want {
			return true
		}
	}
	return false
}

func shellAuditHasOrderedArguments(
	arguments []string,
	required ...string,
) bool {
	next := 0
	for _, argument := range arguments {
		if next < len(required) && argument == required[next] {
			next++
		}
	}
	return next == len(required)
}

func TestShellProductionHostEffectOwnershipGuardRejectsBypasses(t *testing.T) {
	sources := trackedShellProductionSources(t)
	if err := validateShellProductionHostEffectOwnership(sources); err != nil {
		t.Fatalf("current shell production host-effect inventory rejected: %v", err)
	}
	t.Run("relocated wrapper OSC0 title", func(t *testing.T) {
		const title = `  printf '\033]0;󰊠  Wisp Deck\007'`
		wrapper := sources["wrapper.sh"]
		if strings.Count(wrapper, title) != 1 {
			t.Fatal("wrapper OSC0 relocation prerequisite missing")
		}
		mutated := addShellProductionSource(
			sources,
			"wrapper.sh",
			strings.Replace(wrapper, title, "", 1)+"\n"+title+"\n",
		)
		if err := validateShellProductionHostEffectOwnership(mutated); err == nil {
			t.Fatal("relocated wrapper OSC0 title escaped exact context audit")
		}
	})
	t.Run("wrapper OSC0 title moved into helper", func(t *testing.T) {
		const allowed = `  fi

  # Use TUI for project selection
  printf '\033]0;󰊠  Wisp Deck\007'

  # Stop loading animation before TUI takes over
  type stop_loading_screen &>/dev/null && stop_loading_screen`
		wrapper := sources["wrapper.sh"]
		if strings.Count(wrapper, allowed) != 1 {
			t.Fatal("wrapper OSC0 helper-move prerequisite missing")
		}
		mutated := addShellProductionSource(
			sources,
			"wrapper.sh",
			strings.Replace(
				wrapper,
				allowed,
				"future_wrapper_title() {\n"+allowed+"\n}",
				1,
			),
		)
		if err := validateShellProductionHostEffectOwnership(mutated); err == nil {
			t.Fatal("wrapper OSC0 title escaped its top-level structural owner")
		}
	})
	t.Run("allows unrelated wrapper helper", func(t *testing.T) {
		mutated := addShellProductionSource(
			sources,
			"wrapper.sh",
			sources["wrapper.sh"]+"\nfuture_safe_helper() { printf '%s\\n' safe; }\n",
		)
		if err := validateShellProductionHostEffectOwnership(mutated); err != nil {
			t.Fatalf("unrelated wrapper helper rejected: %v", err)
		}
	})
	mutations := map[string]string{
		"afplay":                      "afplay /tmp/chime.aiff\n",
		"system sound path":           "printf '%s\\n' /System/Library/Sounds/Glass.aiff\n",
		"NSSound":                     "printf '%s\\n' NSSound\n",
		"AudioServices":               "printf '%s\\n' AudioServicesPlaySystemSound\n",
		"speech":                      "say audit\n",
		"notification sound":          "osascript -e 'display notification \"audit\" with sound name \"Glass\"'\n",
		"OSC 9":                       "printf '\\033]9;audit\\007'\n",
		"escaped BEL":                 "printf '\\a'\n",
		"single octal BEL":            "printf '\\7'\n",
		"short octal BEL":             "printf '\\07'\n",
		"percent-b octal BEL":         "printf '%b' '\\0007'\n",
		"unquoted percent-b":          "printf %b '\\0007'\n",
		"option percent-b":            "printf -- %b '\\0007'\n",
		"reordered echo flags":        "echo -n -e '\\0007'\n",
		"ANSI-C control BEL":          "printf %s $'\\cG'\n",
		"double-quoted escaped octal": "printf \"\\\\7\"\n",
		"unquoted escaped octal":      "printf \\\\7\n",
		"double-quoted escaped BEL":   "printf \"\\\\a\"\n",
		"short hex BEL":               "printf '\\x7'\n",
		"raw BEL":                     "printf 'audit\a'\n",
	}
	for name, source := range mutations {
		t.Run(name, func(t *testing.T) {
			mutated := addShellProductionSource(
				sources,
				"lib/future-host-effect.sh",
				source,
			)
			if err := validateShellProductionHostEffectOwnership(mutated); err == nil {
				t.Fatal("shell host-effect mutation escaped production ownership validation")
			}
		})
	}

	for name, source := range map[string]string{
		"octal seventy": "printf '\\70'\n",
		"hex seventy":   "printf '\\x70'\n",
		"escaped slash": "printf '\\\\7'\n",
		"control-H":     "printf %s $'\\cH'\n",
	} {
		t.Run("allows "+name, func(t *testing.T) {
			mutated := addShellProductionSource(
				sources,
				"lib/future-terminal-output.sh",
				source,
			)
			if err := validateShellProductionHostEffectOwnership(mutated); err != nil {
				t.Fatalf("harmless shell escape rejected: %v", err)
			}
		})
	}
}

func trackedShellProductionSources(t *testing.T) map[string]string {
	t.Helper()
	files := trackedRepositoryAuditFiles(t)
	shipped := repositoryShippedAssignments()
	sources := make(map[string]string)
	for path, file := range files {
		if path == "VERSION" || isTrackedTestSource(path) ||
			path == "package.json" || strings.HasSuffix(path, ".go") ||
			isJavaScriptAuditPath(path) ||
			!isProductionAuditTextPath(path, shipped) ||
			bytes.IndexByte(file.source, 0) >= 0 {
			continue
		}
		sources[path] = string(file.source)
	}
	return sources
}

func addShellProductionSource(
	sources map[string]string,
	path string,
	source string,
) map[string]string {
	added := make(map[string]string, len(sources)+1)
	for existingPath, existingSource := range sources {
		added[existingPath] = existingSource
	}
	added[path] = source
	return added
}

func validateShellProductionHostEffectOwnership(
	sources map[string]string,
) error {
	sanitized := make(map[string]string, len(sources))
	for path, source := range sources {
		sanitized[path] = source
	}
	wrapper, ok := sanitized["wrapper.sh"]
	if !ok {
		return fmt.Errorf("shell host-effect inventory is missing wrapper.sh")
	}
	wrapper, err := sanitizeExactWrapperTitleOwnership(wrapper)
	if err != nil {
		return err
	}
	sanitized["wrapper.sh"] = wrapper

	allowlist := map[string][]string{
		"lib/session-restore.sh": {
			`restore_trigger_tab() {
  osascript \
    -e 'tell application "Ghostty" to activate' \
    -e 'tell application "System Events" to keystroke "t" using command down' \
    >/dev/null 2>&1
}`,
		},
		"lib/tui.sh": {
			`set_tab_title() {
  local project="$1"
  local tool="${2:-}"
  # Probed by opening it, not with ` + "`[ -w /dev/tty ]`" + `: the device node is there
  # and reports writable even for a process with no controlling terminal, where
  # the open then fails with "Device not configured" — printed, of course, to
  # the terminal this whole exercise is about keeping clean.
  local out=/dev/stdout
  { : > /dev/tty; } 2>/dev/null && out=/dev/tty
  if [ -n "$tool" ]; then
    printf '\033]0;%s · %s\007' "$project" "$tool" > "$out"
  else
    printf '\033]0;%s\007' "$project" > "$out"
  fi
}`,
		},
	}
	for path, allowedShapes := range allowlist {
		source, ok := sanitized[path]
		if !ok {
			return fmt.Errorf("shell host-effect inventory is missing %s", path)
		}
		for _, allowedShape := range allowedShapes {
			if count := strings.Count(source, allowedShape); count != 1 {
				return fmt.Errorf(
					"%s contains %d exact allowed host-control shapes %q, want 1",
					path,
					count,
					allowedShape,
				)
			}
			source = strings.Replace(source, allowedShape, "", 1)
		}
		sanitized[path] = source
	}

	paths := make([]string, 0, len(sanitized))
	for path := range sanitized {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		for lineNumber, line := range strings.Split(sanitized[path], "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if shellProductionLineHasHostEffect(trimmed) {
				return fmt.Errorf(
					"%s:%d contains an unaudited shell host effect: %q",
					path,
					lineNumber+1,
					trimmed,
				)
			}
		}
	}
	return nil
}

func sanitizeExactWrapperTitleOwnership(source string) (string, error) {
	const exactLine = `  printf '\033]0;󰊠  Wisp Deck\007'`
	const exactOwner = `  fi

  # Use TUI for project selection
  printf '\033]0;󰊠  Wisp Deck\007'

  # Stop loading animation before TUI takes over
  type stop_loading_screen &>/dev/null && stop_loading_screen`
	if strings.Count(source, exactLine) != 1 {
		return "", fmt.Errorf(
			"wrapper.sh contains %d exact picker-title commands, want 1",
			strings.Count(source, exactLine),
		)
	}
	if strings.Count(source, exactOwner) != 1 {
		return "", fmt.Errorf(
			"wrapper.sh picker-title command left its exact structural owner",
		)
	}
	functions, err := shellAuditFunctionDefinitions(source)
	if err != nil {
		return "", fmt.Errorf("wrapper title function audit: %w", err)
	}
	for name, body := range functions {
		if strings.Contains(body, strings.TrimSpace(exactLine)) {
			return "", fmt.Errorf(
				"wrapper.sh picker-title command moved into function %s",
				name,
			)
		}
	}
	commands := shellAuditCommands(exactLine)
	if len(commands) != 1 ||
		filepath.Base(commands[0].executable) != "printf" ||
		len(commands[0].arguments) != 1 ||
		commands[0].arguments[0] != `\033]0;󰊠  Wisp Deck\007` {
		return "", fmt.Errorf(
			"wrapper.sh picker-title command changed its exact OSC0 shape",
		)
	}
	return strings.Replace(source, exactLine, "", 1), nil
}

func shellProductionLineHasHostEffect(line string) bool {
	if stringHasHostEffectMarker(line) ||
		strings.ContainsRune(line, '\a') {
		return true
	}
	if shellLineHasBellEscape(line) {
		return true
	}
	for _, field := range strings.Fields(strings.ToLower(line)) {
		if strings.Trim(field, `"'(){}[];,`) == "say" {
			return true
		}
	}
	return false
}

func shellLineHasBellEscape(line string) bool {
	const (
		shellUnquoted = iota
		shellSingleQuoted
		shellDoubleQuoted
		shellANSIQuoted
	)
	quote := shellUnquoted
	for index := 0; index < len(line); {
		switch quote {
		case shellUnquoted:
			if line[index] == '$' && index+1 < len(line) && line[index+1] == '\'' {
				quote = shellANSIQuoted
				index += 2
				continue
			}
			if line[index] == '\'' {
				quote = shellSingleQuoted
				index++
				continue
			}
			if line[index] == '"' {
				quote = shellDoubleQuoted
				index++
				continue
			}
		case shellSingleQuoted, shellANSIQuoted:
			if line[index] == '\'' {
				quote = shellUnquoted
				index++
				continue
			}
		case shellDoubleQuoted:
			if line[index] == '"' {
				quote = shellUnquoted
				index++
				continue
			}
		}
		if line[index] != '\\' {
			index++
			continue
		}
		runEnd := index
		for runEnd < len(line) && line[runEnd] == '\\' {
			runEnd++
		}
		// Inside ordinary single quotes Bash preserves the run verbatim, so
		// printf sees an escape only after an odd number of backslashes.
		// Everywhere else shell processing can collapse a pair first (for
		// example "\\7" or unquoted \\7), so conservatively audit the
		// resulting escape regardless of raw parity.
		if (quote == shellSingleQuoted && (runEnd-index)%2 == 0) ||
			runEnd == len(line) {
			index = runEnd
			continue
		}
		escape := runEnd
		switch line[escape] {
		case 'a':
			return true
		case 'c':
			if escape+1 < len(line) &&
				(line[escape+1] == 'g' || line[escape+1] == 'G') {
				return true
			}
		case 'x':
			value, digits := shellEscapeValue(line, escape+1, 2, 16)
			if digits > 0 && value == 7 {
				return true
			}
		default:
			if line[escape] < '0' || line[escape] > '7' {
				index = runEnd
				continue
			}
			value, _ := shellEscapeValue(line, escape, 3, 8)
			if value == 7 {
				return true
			}
			// printf %b and echo -e accept \0ooo. Audit that interpretation
			// regardless of option/quoting layout; a conservative source
			// rejection is safer than trying to emulate Bash parsing.
			if line[escape] == '0' {
				value, digits := shellEscapeValue(line, escape+1, 3, 8)
				if digits > 0 && value == 7 {
					return true
				}
			}
		}
		index = runEnd
	}
	return false
}

func shellEscapeValue(
	line string,
	start int,
	limit int,
	base int,
) (int, int) {
	value := 0
	digits := 0
	for start+digits < len(line) && digits < limit {
		character := line[start+digits]
		digit := -1
		switch {
		case character >= '0' && character <= '9':
			digit = int(character - '0')
		case character >= 'a' && character <= 'f':
			digit = int(character-'a') + 10
		case character >= 'A' && character <= 'F':
			digit = int(character-'A') + 10
		}
		if digit < 0 || digit >= base {
			break
		}
		value = value*base + digit
		digits++
	}
	return value, digits
}

func TestMainMenuSoundPreviewOwnershipGuardRejectsBypasses(t *testing.T) {
	root := projectRoot(t)
	paths := map[string]string{
		"host":         filepath.Join(root, "cmd", "wisp-deck-tui", "host_effects.go"),
		"menu":         filepath.Join(root, "cmd", "wisp-deck-tui", "main_menu.go"),
		"background":   filepath.Join(root, "cmd", "wisp-deck-tui", "claude_background.go"),
		"notification": filepath.Join(root, "cmd", "wisp-deck-tui", "notification_sound.go"),
	}
	sources := make(map[string]string, len(paths))
	overrides := make(map[string][]byte, len(paths))
	for name, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sources[name] = string(data)
		overrides[path] = data
	}
	if err := validateMainMenuSoundPreviewOwnership(paths["menu"], overrides[paths["menu"]]); err != nil {
		t.Fatalf("current preview adapter rejected: %v", err)
	}
	if err := validateGoHostEffectOwnership(root, overrides); err != nil {
		t.Fatalf("current typed host-effect owner rejected: %v", err)
	}

	mutations := map[string]map[string]string{
		"policy no longer first": mutateBoundarySource(
			t, sources, "host",
			"\tif !currentHostEffectsDecision().Allowed {\n\t\treturn nil\n\t}\n",
			"",
		),
		"command constructed before policy": mutateBoundarySource(
			t, sources, "host",
			"\tif !currentHostEffectsDecision().Allowed {",
			"\t_ = exec.CommandContext(ctx, \"/usr/bin/afplay\")\n\tif !currentHostEffectsDecision().Allowed {",
		),
		"planner bypassed": mutateBoundarySource(
			t, sources, "host",
			"plan, ok := planHostEffect(effect, os.Environ())",
			"plan, ok := hostEffectPlan{executable: \"/usr/bin/afplay\"}, true",
		),
		"unwaited process": mutateBoundarySource(
			t, sources, "host",
			"return cmd.Run()",
			"return cmd.Start()",
		),
		"detached process": mutateBoundarySource(
			t, sources, "host",
			"return cmd.Run()",
			"_ = cmd.Start()\n\treturn nil",
		),
		"generic executable runner": mutateBoundarySource(
			t, sources, "host",
			"func runHostEffect(ctx context.Context, effect hostEffect) error {",
			"func runHostEffect(ctx context.Context, executable string, arguments []string) error {",
		),
		"generic executor callback": mutateBoundarySource(
			t, sources, "host",
			"func runHostEffect(ctx context.Context, effect hostEffect) error {",
			"func runHostEffect(ctx context.Context, effect hostEffect, run func(string, ...string) error) error {",
		),
		"constructor alias bypass": mutateBoundarySource(
			t, sources, "host",
			"\tcmd := exec.CommandContext(ctx, plan.executable, plan.arguments...)",
			"\trunner := exec.Command\n"+
				"\t_ = runner(\"/usr/bin/say\", \"audit\")\n"+
				"\tcmd := exec.CommandContext(ctx, plan.executable, plan.arguments...)",
		),
		"second exec Cmd path": mutateBoundarySource(
			t, sources, "host",
			"\tcmd := exec.CommandContext(ctx, plan.executable, plan.arguments...)",
			"\t_ = exec.Cmd{Path: \"/usr/bin/say\"}\n"+
				"\tcmd := exec.CommandContext(ctx, plan.executable, plan.arguments...)",
		),
		"allocated exec Cmd path": mutateBoundarySource(
			t, sources, "host",
			"\tcmd := exec.CommandContext(ctx, plan.executable, plan.arguments...)",
			"\textra := new(exec.Cmd)\n"+
				"\textra.Path = \"/usr/bin/say\"\n"+
				"\tcmd := exec.CommandContext(ctx, plan.executable, plan.arguments...)",
		),
		"missing process group": mutateBoundarySource(
			t, sources, "host",
			"cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}",
			"cmd.SysProcAttr = &syscall.SysProcAttr{}",
		),
		"wrong cancellation target": mutateBoundarySource(
			t, sources, "host",
			"syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)",
			"syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)",
		),
		"weak cancellation signal": mutateBoundarySource(
			t, sources, "host",
			"syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)",
			"syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)",
		),
		"missing ESRCH mapping": mutateBoundarySource(
			t, sources, "host",
			"if errors.Is(err, syscall.ESRCH) {\n\t\t\treturn os.ErrProcessDone\n\t\t}",
			"",
		),
		"missing nil process mapping": mutateBoundarySource(
			t, sources, "host",
			"if cmd.Process == nil {\n\t\t\treturn os.ErrProcessDone\n\t\t}",
			"",
		),
		"stdin inherited": mutateBoundarySource(
			t, sources, "host",
			"cmd.Stdin = nil",
			"cmd.Stdin = os.Stdin",
		),
		"stdout inherited": mutateBoundarySource(
			t, sources, "host",
			"cmd.Stdout = io.Discard",
			"cmd.Stdout = os.Stdout",
		),
		"stderr inherited": mutateBoundarySource(
			t, sources, "host",
			"cmd.Stderr = io.Discard",
			"cmd.Stderr = os.Stderr",
		),
		"unbounded wait delay": mutateBoundarySource(
			t, sources, "host",
			"cmd.WaitDelay = 100 * time.Millisecond",
			"cmd.WaitDelay = 0",
		),
		"preview-specific runner restored": mutateBoundarySource(
			t, sources, "menu",
			"func mainMenuSoundPreview(name string) tea.Cmd {",
			"type mainMenuSoundRunner func(string, ...string) error\n\nfunc runMainMenuSoundWith(string, mainMenuSoundRunner) error { return nil }\n\nfunc mainMenuSoundPreview(name string) tea.Cmd {",
		),
		"notifier runner restored": mutateBoundarySource(
			t, sources, "background",
			"\tTimeout       time.Duration\n",
			"\tTimeout       time.Duration\n\tRun           func(context.Context, string, []string, []string) error\n",
		),
		"fixed process arguments changed": mutateBoundarySource(
			t, sources, "background",
			`cmd := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=")`,
			`cmd := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "pid=")`,
		),
		"new fixed process owner": mutateBoundarySource(
			t, sources, "background",
			`cmd := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=")`,
			"_ = exec.Command(\"git\", \"status\")\n\t"+
				`cmd := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=")`,
		),
		"preference read outside lock": mutateBoundarySource(
			t, sources, "notification",
			"\treturn soundpref.WithExclusiveLock(features, func() error {\n\t\tsound := soundpref.Read(features)",
			"\tsound := soundpref.Read(features)\n\treturn soundpref.WithExclusiveLock(features, func() error {",
		),
		"player callback outside lock": mutateBoundarySource(
			t, sources, "notification",
			"\t\treturn play(sound)\n\t})",
			"\t\treturn nil\n\t})\n\treturn play(sound)",
		),
		"player callback detached under lock": mutateBoundarySource(
			t, sources, "notification",
			"\t\treturn play(sound)",
			"\t\tgo func() { _ = play(sound) }()\n\t\treturn nil",
		),
		"player callback error ignored": mutateBoundarySource(
			t, sources, "notification",
			"\t\treturn play(sound)",
			"\t\t_ = play(sound)\n\t\treturn nil",
		),
	}
	for name, mutated := range mutations {
		t.Run(name, func(t *testing.T) {
			mutatedOverrides := make(map[string][]byte, len(paths))
			for key, path := range paths {
				mutatedOverrides[path] = []byte(mutated[key])
			}
			if err := validateGoHostEffectOwnership(root, mutatedOverrides); err == nil {
				t.Fatal("unsafe typed host-effect layout passed the ownership guard")
			}
		})
	}
}

func TestIdleSoundProductionHostEffectGuardRejectsBypasses(t *testing.T) {
	tests := map[string]string{
		"absolute player":        `package p; import "os/exec"; func f() { _ = exec.Command("/usr/bin/afplay", "x") }`,
		"relative player":        `package p; import "os/exec"; func f() { _ = exec.Command("afplay", "x") }`,
		"speech":                 `package p; import "os/exec"; func f() { _ = exec.Command("/usr/bin/say", "x") }`,
		"sound AppleScript":      `package p; import "os/exec"; func f() { _ = exec.Command("/usr/bin/osascript", "-e", "display notification \"x\"") }`,
		"OSC notification shell": `package p; import "os/exec"; func f() { _ = exec.Command("/bin/sh", "-c", "printf '\\033]9;x\\007'") }`,
		"aliased player":         `package p; import process "os/exec"; func f() { _ = process.Command("afplay", "x") }`,
		"constructor alias":      `package p; import "os/exec"; func f() { runner := exec.Command; _ = runner("/usr/bin/say", "x") }`,
		"constructor alias chain": `package p; import "os/exec"; func f() {
			runner := exec.Command
			alias := runner
			_ = alias("/usr/bin/osascript", "-e", "display notification \"x\"")
		}`,
		"helper parameter player": `package p
import "os/exec"
func run(path string) { _ = exec.Command(path).Run() }
func f() { run("/usr/bin/afplay") }`,
		"aliased fmt BEL": `package p
import output "fmt"
func f() { output.Print("\x07") }`,
		"numeric stdout BEL": `package p
import output "os"
func f() { _, _ = output.Stdout.Write([]byte{7}) }`,
		"dot imported player":            `package p; import . "os/exec"; func f() { _ = Command("afplay", "x") }`,
		"dot imported constructor alias": `package p; import . "os/exec"; func f() { runner := Command; _ = runner("/usr/bin/say", "x") }`,
		"start process":                  `package p; import "os"; func f() { _, _ = os.StartProcess("/usr/bin/afplay", nil, nil) }`,
		"syscall exec":                   `package p; import "syscall"; func f() { _ = syscall.Exec("/usr/bin/afplay", nil, nil) }`,
		"exec cmd path":                  `package p; import "os/exec"; func f() { _ = exec.Cmd{Path: "/usr/bin/afplay"} }`,
		"exec cmd later path":            `package p; import "os/exec"; func f() { cmd := exec.Cmd{}; cmd.Path = "/usr/bin/say" }`,
		"allocated exec cmd later path":  `package p; import "os/exec"; func f() { cmd := new(exec.Cmd); cmd.Path = "/usr/bin/say" }`,
		"allocated aliased exec cmd":     `package p; import process "os/exec"; func f() { cmd := new(process.Cmd); cmd.Path = "/usr/bin/say" }`,
		"allocated dot imported cmd":     `package p; import . "os/exec"; func f() { cmd := new(Cmd); cmd.Path = "/usr/bin/say" }`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			if !productionSourceLaunchesHostEffect(name+".go", []byte(source)) {
				t.Fatal("host-effect process shape escaped the production scanner")
			}
		})
	}

	legitimate := map[string]string{
		"git":        `package p; import "os/exec"; func f() { _ = exec.Command("git", "status") }`,
		"agent":      `package p; import "os/exec"; func f(argv []string) { _ = exec.Command(argv[0], argv[1:]...) }`,
		"inspection": `package p; import "os/exec"; func f() { _ = exec.Command("/bin/ps", "-p", "1") }`,
		"screenshot": `package p; import "os/exec"; func f() { _ = exec.Command("open", "-a", "Preview", "/tmp/image.png") }`,
	}
	for name, source := range legitimate {
		t.Run("allows "+name, func(t *testing.T) {
			if productionSourceLaunchesHostEffect(name+".go", []byte(source)) {
				t.Fatal("legitimate non-effect process was classified as a host effect")
			}
		})
	}
}

func TestGlobalHostEffectsBoundaryOwnershipGuardRejectsBypasses(t *testing.T) {
	root := projectRoot(t)
	paths := map[string]string{
		"policy":       filepath.Join(root, "cmd", "wisp-deck-tui", "host_effects_policy.go"),
		"darwin":       filepath.Join(root, "cmd", "wisp-deck-tui", "host_effects_ancestry_darwin.go"),
		"capabilities": filepath.Join(root, "cmd", "wisp-deck-tui", "capabilities.go"),
		"make":         filepath.Join(root, "Makefile"),
		"release":      filepath.Join(root, "scripts", "release.sh"),
	}
	sources := make(map[string]string, len(paths))
	for name, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sources[name] = string(data)
	}
	if err := validateGlobalHostEffectsBoundary(sources); err != nil {
		t.Fatalf("current global host-effect boundary rejected: %v", err)
	}

	mutations := map[string]map[string]string{
		"default capability enabled": mutateBoundarySource(
			t,
			sources,
			"policy",
			`var HostEffectsCapability = "disabled"`,
			`var HostEffectsCapability = "enabled"`,
		),
		"Make CLI override defeats marked build": mutateBoundarySource(
			t,
			sources,
			"make",
			"override HOST_EFFECTS_CAPABILITY := disabled",
			"HOST_EFFECTS_CAPABILITY ?= disabled",
		),
		"missing global capability": mutateBoundarySource(
			t,
			sources,
			"policy",
			"capability := HostEffectsCapability",
			`capability := "enabled"`,
		),
		"missing Go test identity": mutateBoundarySource(
			t,
			sources,
			"policy",
			"testBinary := testing.Testing()",
			"testBinary := false",
		),
		"missing current marker": mutateBoundarySource(
			t,
			sources,
			"policy",
			"testEnvironment := os.Getenv(wispDeckTestingEnvironment)",
			`testEnvironment := ""`,
		),
		"changed repository sentinel": mutateBoundarySource(
			t,
			sources,
			"policy",
			`const wispDeckRepositoryTestSentinel = "__WISP_DECK_REPOSITORY_TEST_V1__.test"`,
			`const wispDeckRepositoryTestSentinel = "__WISP_DECK_REPOSITORY_TEST_V2__.test"`,
		),
		"missing ancestor sentinel scan": mutateBoundarySource(
			t,
			sources,
			"policy",
			"hostArgumentsHaveTestSentinel(info.Arguments)",
			"false",
		),
		"missing ancestor marker scan": mutateBoundarySource(
			t,
			sources,
			"policy",
			"hostEnvironmentHasTestMarker(info.Environment)",
			"false",
		),
		"missing ancestor executable path scan": mutateBoundarySource(
			t,
			sources,
			"policy",
			`strings.HasSuffix(filepath.Base(info.Executable), ".test")`,
			"false",
		),
		"lookup failure allowed": mutateBoundarySource(
			t,
			sources,
			"policy",
			"if err != nil {\n\t\t\treturn hostProcessAncestry{}\n\t\t}",
			"if err != nil {\n\t\t\treturn hostProcessAncestry{Known: true}\n\t\t}",
		),
		"boundary removed": mutateBoundarySource(
			t,
			sources,
			"policy",
			"const HostEffectsBoundaryVersion = 1",
			"const HostEffectsBoundaryVersion = 0",
		),
		"capabilities omit boundary": mutateBoundarySource(
			t,
			sources,
			"capabilities",
			"HostEffectsBoundary:     HostEffectsBoundaryVersion,",
			"HostEffectsBoundary:     0,",
		),
	}
	for name, mutated := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := validateGlobalHostEffectsBoundary(mutated); err == nil {
				t.Fatal("unsafe global host-effect boundary passed the ownership guard")
			}
		})
	}
}

func mutateBoundarySource(
	t *testing.T,
	sources map[string]string,
	file string,
	old string,
	replacement string,
) map[string]string {
	t.Helper()
	if strings.Count(sources[file], old) != 1 {
		t.Fatalf(
			"mutation prerequisite %q in %s occurs %d times, want exactly once",
			old,
			file,
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

func mutateBoundarySourceSequence(
	t *testing.T,
	sources map[string]string,
	file string,
	replacements [][2]string,
) map[string]string {
	t.Helper()
	mutated := make(map[string]string, len(sources))
	for name, source := range sources {
		mutated[name] = source
	}
	for _, replacement := range replacements {
		old := replacement[0]
		if strings.Count(mutated[file], old) != 1 {
			t.Fatalf(
				"mutation prerequisite %q in %s occurs %d times, want exactly once",
				old,
				file,
				strings.Count(mutated[file], old),
			)
		}
		mutated[file] = strings.Replace(
			mutated[file],
			old,
			replacement[1],
			1,
		)
	}
	return mutated
}

func validateGlobalHostEffectsBoundary(sources map[string]string) error {
	policy := sources["policy"]
	for _, required := range []string{
		`var HostEffectsCapability = "disabled"`,
		"const HostEffectsBoundaryVersion = 1",
		`const wispDeckTestingEnvironment = "WISP_DECK_TESTING"`,
		"func currentHostEffectsDecision() hostEffectsDecision",
		`const wispDeckRepositoryTestSentinel = "__WISP_DECK_REPOSITORY_TEST_V1__.test"`,
		`hostEffectsDeniedAncestorSentinel   = "test_ancestor_sentinel"`,
		"capability := HostEffectsCapability",
		"testBinary := testing.Testing()",
		"testEnvironment := os.Getenv(wispDeckTestingEnvironment)",
		"inspectHostProcessAncestry(os.Getpid(), lookupHostProcess)",
		"hostArgumentsHaveTestSentinel(info.Arguments)",
		"hostEnvironmentHasTestMarker(info.Environment)",
		"case ancestry.TestSentinel:",
		`strings.HasSuffix(filepath.Base(info.Executable), ".test")`,
		"if err != nil {\n\t\t\treturn hostProcessAncestry{}\n\t\t}",
	} {
		if !strings.Contains(policy, required) {
			return fmt.Errorf("policy is missing required fail-closed shape %q", required)
		}
	}

	darwin := sources["darwin"]
	for _, required := range []string{
		`unix.SysctlKinfoProc("kern.proc.pid", pid)`,
		"process.Proc.P_pid",
		"process.Eproc.Ppid",
		"if pid == 1",
		`unix.SysctlRaw("kern.procargs2", pid)`,
		"executable, arguments, environment, err := parseKernProcArgs2(raw)",
		"Arguments:   arguments,",
	} {
		if !strings.Contains(darwin, required) {
			return fmt.Errorf("Darwin ancestry lookup is missing %q", required)
		}
	}
	pid1 := strings.Index(darwin, "if pid == 1")
	procargs := strings.Index(darwin, `unix.SysctlRaw("kern.procargs2", pid)`)
	if pid1 < 0 || procargs < 0 || pid1 > procargs {
		return fmt.Errorf("Darwin lookup reads protected PID 1 procargs")
	}

	makefile := sources["make"]
	for _, required := range []string{
		"ifeq ($(origin WISP_DECK_TESTING),undefined)",
		"override HOST_EFFECTS_CAPABILITY := enabled",
		"override HOST_EFFECTS_CAPABILITY := disabled",
		"-X main.HostEffectsCapability=$(HOST_EFFECTS_CAPABILITY)",
		"-X main.SoundPreviewCapability=$(HOST_EFFECTS_CAPABILITY)",
	} {
		if strings.Count(makefile, required) != 1 {
			return fmt.Errorf("Makefile must contain exactly one %q", required)
		}
	}

	release := sources["release"]
	for _, required := range []string{
		"-X main.HostEffectsCapability=enabled",
		"-X main.SoundPreviewCapability=enabled",
	} {
		if strings.Count(release, required) != 1 {
			return fmt.Errorf("release build must contain exactly one %q", required)
		}
	}

	capabilities := sources["capabilities"]
	for _, required := range []string{
		"HostEffectsBoundary:     HostEffectsBoundaryVersion,",
		"\t\t\"require-production\",\n",
		"HostEffectsBoundary != 1",
	} {
		if !strings.Contains(capabilities, required) {
			return fmt.Errorf("capability diagnostics are missing %q", required)
		}
	}
	return nil
}

func TestTestSourcesCannotDirectlyLaunchHostAudio(t *testing.T) {
	var violations []string
	for path, file := range trackedRepositoryAuditFiles(t) {
		if !isTrackedTestSource(path) {
			continue
		}
		if trackedTestSourceLaunchesHostEffect(path, file.source) {
			violations = append(violations, path)
		}
	}
	sort.Strings(violations)
	if len(violations) != 0 {
		t.Fatalf("test sources can launch host audio directly: %s", strings.Join(violations, ", "))
	}
}

func TestTestSourceAudioLaunchGuardRejectsBypasses(t *testing.T) {
	tests := map[string]struct {
		source string
		want   bool
	}{
		"preview command": {
			source: `package p; func test() { preview := mainMenuSoundPreview("Glass"); _ = preview }`,
		},
		"quoted production symbols": {
			source: `package p; const example = "exec.Command(\"/usr/bin/afplay\"); runMainMenuSound("`,
		},
		"quoted host-effect fixtures": {
			source: `package p; const (
				notification = "exec.Command(\"/usr/bin/osascript\", \"display notification\")"
				speech = "/usr/bin/say"
				osc = "\\033]9;fixture\\007"
				frameworks = "NSSound AudioServicesPlaySystemSound"
			)`,
		},
		"unrelated process": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("git", "status") }`,
		},
		"afplay as benign argument": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("echo", "afplay") }`,
		},
		"absolute player": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("/usr/bin/afplay", "x") }`,
			want:   true,
		},
		"relative player": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("afplay", "x") }`,
			want:   true,
		},
		"command context": {
			source: `package p; import ("context"; "os/exec"); func test() { _ = exec.CommandContext(context.Background(), "afplay", "x") }`,
			want:   true,
		},
		"aliased exec": {
			source: `package p; import process "os/exec"; func test() { _ = process.Command("afplay", "x") }`,
			want:   true,
		},
		"dot imported exec": {
			source: `package p; import . "os/exec"; func test() { _ = Command("afplay", "x") }`,
			want:   true,
		},
		"constructed player literal": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("/usr/bin/" + "afplay", "x") }`,
			want:   true,
		},
		"player variable": {
			source: `package p; import "os/exec"; func test() { player := "/usr/bin/afplay"; _ = exec.Command(player, "x") }`,
			want:   true,
		},
		"player helper parameter": {
			source: `package p
import "os/exec"
func run(path string) { _ = exec.Command(path).Run() }
func test() { run("/usr/bin/afplay") }`,
			want: true,
		},
		"player helper chain": {
			source: `package p
import "os/exec"
func run(path string) { _ = exec.Command(path).Run() }
func relay(path string) { run(path) }
func test() { relay("/usr/bin/say") }`,
			want: true,
		},
		"player helper alias": {
			source: `package p
import "os/exec"
func run(path string) { _ = exec.Command(path).Run() }
func test() {
	alias := run
	alias("/usr/bin/afplay")
}`,
			want: true,
		},
		"player helper alias chain": {
			source: `package p
import "os/exec"
func run(path string) { _ = exec.Command(path).Run() }
func test() {
	first := run
	second := first
	second("/usr/bin/say")
}`,
			want: true,
		},
		"harmless helper parameter lexical shadow": {
			source: `package p
import "os/exec"
func run(path string) {
	{
		path := "git"
		_ = exec.Command(path, "status").Run()
	}
}
func test() { run("/usr/bin/afplay") }`,
		},
		"player receiver method": {
			source: `package p
import "os/exec"
type runner struct{}
func (runner) run(path string) { _ = exec.Command(path).Run() }
func test() { runner{}.run("/usr/bin/afplay") }`,
			want: true,
		},
		"player pointer receiver method": {
			source: `package p
import "os/exec"
type runner struct{}
func (*runner) run(path string) { _ = exec.Command(path).Run() }
func test() { (&runner{}).run("/usr/bin/say") }`,
			want: true,
		},
		"same-named harmless receiver method": {
			source: `package p
import "os/exec"
type audioRunner struct{}
func (audioRunner) run(path string) { _ = exec.Command(path).Run() }
type recorder struct{}
func (*recorder) run(value string) { _ = value }
func test() { (&recorder{}).run("/usr/bin/afplay") }`,
		},
		"player function literal": {
			source: `package p
import "os/exec"
func test() {
	run := func(path string) { _ = exec.Command(path).Run() }
	run("/usr/bin/say")
}`,
			want: true,
		},
		"harmless receiver method": {
			source: `package p
type recorder struct{}
func (recorder) record(value string) {}
func test() { recorder{}.record("/usr/bin/afplay") }`,
		},
		"harmless function literal": {
			source: `package p
func test() {
	record := func(value string) { _ = value }
	record("/usr/bin/afplay")
}`,
		},
		"shell script player": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("/bin/sh", "-c", "afplay x") }`,
			want:   true,
		},
		"notification AppleScript": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("/usr/bin/osascript", "-e", "display notification \"x\"") }`,
			want:   true,
		},
		"speech executable": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("/usr/bin/say", "x") }`,
			want:   true,
		},
		"OSC 9 shell": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("/bin/sh", "-c", "printf '\\033]9;x\\007'") }`,
			want:   true,
		},
		"NSSound shell": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("/bin/sh", "-c", "use NSSound to play x") }`,
			want:   true,
		},
		"AudioServices shell": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("/bin/sh", "-c", "AudioServicesPlaySystemSound 1") }`,
			want:   true,
		},
		"host marker as benign non-shell argument": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("echo", "NSSound", "]9;") }`,
		},
		"constructor alias": {
			source: `package p; import "os/exec"; func test() { runner := exec.Command; _ = runner("/usr/bin/say", "x") }`,
			want:   true,
		},
		"constructor alias chain": {
			source: `package p; import "os/exec"; func test() { runner := exec.Command; alias := runner; _ = alias("/usr/bin/osascript", "-e", "display notification \"x\"") }`,
			want:   true,
		},
		"dot-imported constructor alias": {
			source: `package p; import . "os/exec"; func test() { runner := Command; _ = runner("/usr/bin/say", "x") }`,
			want:   true,
		},
		"os start process": {
			source: `package p; import "os"; func test() { _, _ = os.StartProcess("/usr/bin/afplay", nil, nil) }`,
			want:   true,
		},
		"syscall exec": {
			source: `package p; import "syscall"; func test() { _ = syscall.Exec("/usr/bin/afplay", nil, nil) }`,
			want:   true,
		},
		"exec cmd literal": {
			source: `package p; import "os/exec"; func test() { _ = exec.Cmd{Path: "/usr/bin/afplay"} }`,
			want:   true,
		},
		"exec cmd later path": {
			source: `package p; import "os/exec"; func test() { cmd := exec.Cmd{}; cmd.Path = "/usr/bin/afplay" }`,
			want:   true,
		},
		"allocated exec cmd later path": {
			source: `package p; import "os/exec"; func test() { cmd := new(exec.Cmd); cmd.Path = "/usr/bin/say" }`,
			want:   true,
		},
		"allocated aliased exec cmd later path": {
			source: `package p; import process "os/exec"; func test() { cmd := new(process.Cmd); cmd.Path = "/usr/bin/say" }`,
			want:   true,
		},
		"allocated dot imported exec cmd later path": {
			source: `package p; import . "os/exec"; func test() { cmd := new(Cmd); cmd.Path = "/usr/bin/say" }`,
			want:   true,
		},
		"production runner argument": {
			source: `package p; func test(effect hostEffect) { _ = runHostEffect(context.Background(), effect) }`,
			want:   true,
		},
		"production runner callback": {
			source: `package p; func test() { callback := runHostEffect; _ = callback }`,
			want:   true,
		},
		"production command adapter": {
			source: `package p; func test() { _ = newNotificationSoundCommand(playNotificationSound) }`,
			want:   true,
		},
		"invalid typed effect runner": {
			source: `package p; func test() { _ = runHostEffect(context.Background(), hostEffect{}) }`,
		},
		"direct stdout BEL": {
			source: `package p; import "os"; func test() { _, _ = os.Stdout.Write([]byte("\a")) }`,
			want:   true,
		},
		"numeric stdout BEL": {
			source: `package p; import output "os"; func test() { _, _ = output.Stdout.Write([]byte{7}) }`,
			want:   true,
		},
		"aliased fmt output": {
			source: `package p; import output "fmt"; func test() { output.Print("\x1b]9;audit\x07") }`,
			want:   true,
		},
		"fmt function alias output": {
			source: `package p; import "fmt"; func test() { emit := fmt.Print; emit("\x07") }`,
			want:   true,
		},
		"terminal output helper": {
			source: `package p
import "os"
func emit(value []byte) { _, _ = os.Stdout.Write(value) }
func test() { emit([]byte{0x07}) }`,
			want: true,
		},
		"direct stderr OSC notification": {
			source: `package p
import (
	"fmt"
	"os"
)
func test() { fmt.Fprint(os.Stderr, "\x1b]9;audit\x07") }`,
			want: true,
		},
		"numeric string BEL": {
			source: `package p
import "fmt"
func test() { fmt.Print(string(7)) }`,
			want: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := testSourceLaunchesHostAudio(name+".go", []byte(test.source)); got != test.want {
				t.Fatalf("testSourceLaunchesHostAudio() = %t, want %t", got, test.want)
			}
		})
	}

	const filteredPTYFixture = `package main
import "os/exec"
func TestPumpTerminalOutputFiltersRealPTY() {
	cmd := exec.Command("/bin/sh", "-c", ` +
		"`printf 'before\\007\\033]9;plain\\007\\033Ptmux;\\033\\033]9;wrapped\\007\\033\\\\after'`" +
		`)
	_, _ = pty.Start(cmd)
	_ = cmd.Wait()
}`
	fixturePath := filepath.Join(
		"cmd",
		"wisp-deck-tui",
		"screenshot_filter_test.go",
	)
	if testSourceLaunchesHostAudio(fixturePath, []byte(filteredPTYFixture)) {
		t.Fatal("exact filtered PTY fixture was classified as a host effect")
	}
	mutatedFixture := strings.Replace(
		filteredPTYFixture,
		"before",
		"changed",
		1,
	)
	if !testSourceLaunchesHostAudio(fixturePath, []byte(mutatedFixture)) {
		t.Fatal("changed filtered PTY process shape escaped the test-source guard")
	}
	relocatedFixture := strings.Replace(
		filteredPTYFixture,
		"TestPumpTerminalOutputFiltersRealPTY",
		"TestRelocatedPTY",
		1,
	)
	if !testSourceLaunchesHostAudio(fixturePath, []byte(relocatedFixture)) {
		t.Fatal("relocated filtered PTY process escaped the test-source guard")
	}
	executedFixture := strings.Replace(
		filteredPTYFixture,
		"_, _ = pty.Start(cmd)",
		"_ = cmd.Run()",
		1,
	)
	if !testSourceLaunchesHostAudio(fixturePath, []byte(executedFixture)) {
		t.Fatal("directly executed filtered PTY command escaped the test-source guard")
	}
	aliasedExecutionFixture := strings.Replace(
		filteredPTYFixture,
		"_, _ = pty.Start(cmd)",
		"_, _ = pty.Start(cmd)\n\talias := cmd\n\tagain := alias\n\t_ = again.Run()",
		1,
	)
	if !testSourceLaunchesHostAudio(fixturePath, []byte(aliasedExecutionFixture)) {
		t.Fatal("aliased filtered PTY command execution escaped the test-source guard")
	}
	helperExecutionFixture := strings.Replace(
		filteredPTYFixture,
		"_, _ = pty.Start(cmd)",
		"_ = executeCommand(cmd)\n\t_, _ = pty.Start(cmd)",
		1,
	) + `
func executeCommand(cmd *exec.Cmd) error { return cmd.Run() }
`
	if !testSourceLaunchesHostAudio(fixturePath, []byte(helperExecutionFixture)) {
		t.Fatal("helper-executed filtered PTY command escaped the test-source guard")
	}

	const openCodePTYFixture = `package opencodeadapter
import (
	"bytes"
	"context"
	"os"
	"testing"
)
func TestRunDefaultPTYPreservesExitAndFiltersTerminalNotifications(t *testing.T) {
	var output bytes.Buffer
	supervisor := Supervisor{}
	_, _ = supervisor.runDefaultPTY(context.Background(), ptySpec{
		Argv: []string{"/bin/sh", "-c", "printf 'left\\007middle\\033]9;native\\007right'; exit 7"},
		Env: os.Environ(), CWD: t.TempDir(), Stdin: bytes.NewReader(nil), Stdout: &output,
	}, func() {})
}`
	const openCodeFixturePath = "internal/opencodeadapter/supervisor_test.go"
	if testSourceLaunchesHostAudio(
		openCodeFixturePath,
		[]byte(openCodePTYFixture),
	) {
		t.Fatal("exact OpenCode filtered PTY fixture was classified as a host effect")
	}
	relocatedOpenCodeFixture := strings.Replace(
		openCodePTYFixture,
		"TestRunDefaultPTYPreservesExitAndFiltersTerminalNotifications",
		"TestRelocatedOpenCodePTY",
		1,
	)
	if !testSourceLaunchesHostAudio(
		openCodeFixturePath,
		[]byte(relocatedOpenCodeFixture),
	) {
		t.Fatal("relocated OpenCode filtered PTY fixture escaped the test-source guard")
	}
	const detachedOpenCodeFixture = `package opencodeadapter
import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
)
func TestRunDefaultPTYPreservesExitAndFiltersTerminalNotifications(t *testing.T) {
	var output bytes.Buffer
	supervisor := Supervisor{}
	spec := ptySpec{
		Argv: []string{"/bin/sh", "-c", "printf 'left\\007middle\\033]9;native\\007right'; exit 7"},
		Env: os.Environ(), CWD: t.TempDir(), Stdin: bytes.NewReader(nil), Stdout: &output,
	}
	_ = exec.Command(spec.Argv[0], spec.Argv[1:]...).Run()
	_, _ = supervisor.runDefaultPTY(context.Background(), spec, func() {})
}`
	if !testSourceLaunchesHostAudio(
		openCodeFixturePath,
		[]byte(detachedOpenCodeFixture),
	) {
		t.Fatal("detached OpenCode PTY fixture launch escaped the test-source guard")
	}

	const codexOutputFixture = `package codexadapter
import "os"
func TestCodexPTYConsumesFragmentedOSCAndForwardsOrdinaryBytes() {
	if os.Getenv("WISP_DECK_CODEX_PTY_CHILD") == "1" {
		_, _ = os.Stdout.Write([]byte("\x1b]9;dynamic"))
		_, _ = os.Stdout.Write([]byte(" preview\x07\x1b\\"))
		os.Exit(0)
	}
}`
	codexFixturePath := filepath.Join(
		"internal",
		"codexadapter",
		"supervisor_test.go",
	)
	if testSourceLaunchesHostAudio(codexFixturePath, []byte(codexOutputFixture)) {
		t.Fatal("exact filtered Codex PTY output fixture was classified as a host effect")
	}
	relocatedCodexFixture := strings.Replace(
		codexOutputFixture,
		"TestCodexPTYConsumesFragmentedOSCAndForwardsOrdinaryBytes",
		"TestRelocatedOutput",
		1,
	)
	if !testSourceLaunchesHostAudio(
		codexFixturePath,
		[]byte(relocatedCodexFixture),
	) {
		t.Fatal("relocated Codex PTY output fixture escaped the test-source guard")
	}
	duplicatedCodexFixture := strings.Replace(
		codexOutputFixture,
		`_, _ = os.Stdout.Write([]byte(" preview\x07\x1b\\"))`,
		`_, _ = os.Stdout.Write([]byte(" preview\x07\x1b\\"))
	_, _ = os.Stdout.Write([]byte(" preview\x07\x1b\\"))`,
		1,
	)
	if !testSourceLaunchesHostAudio(
		codexFixturePath,
		[]byte(duplicatedCodexFixture),
	) {
		t.Fatal("duplicated Codex PTY output fixture escaped the exact-count guard")
	}
}

type goHostEffectUse uint8

const (
	goProcessHostEffectUse goHostEffectUse = 1 << iota
	goTerminalHostEffectUse
	goProcessCapabilityHostEffectUse
)

type goAuditFunction struct {
	name       string
	parameters []*ast.Object
	body       *ast.BlockStmt
}

type goAuditReceiverTypes map[*ast.Object]string

const goAuditMethodPrefix = "@method:"

func collectGoAuditReceiverTypes(file *ast.File) goAuditReceiverTypes {
	types := make(goAuditReceiverTypes)
	setType := func(identifier *ast.Ident, typeName string) bool {
		if identifier == nil || identifier.Obj == nil || typeName == "" ||
			types[identifier.Obj] == typeName {
			return false
		}
		types[identifier.Obj] = typeName
		return true
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || len(function.Recv.List) != 1 {
			continue
		}
		typeName := goAuditDeclaredTypeName(function.Recv.List[0].Type)
		for _, name := range function.Recv.List[0].Names {
			setType(name, typeName)
		}
	}
	for range 16 {
		changed := false
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.ValueSpec:
				declared := goAuditDeclaredTypeName(node.Type)
				for _, name := range node.Names {
					changed = setType(name, declared) || changed
				}
				for index, expression := range node.Values {
					if index < len(node.Names) {
						changed = setType(
							node.Names[index],
							goAuditExpressionType(expression, types),
						) || changed
					}
				}
			case *ast.AssignStmt:
				for index, expression := range node.Rhs {
					if index >= len(node.Lhs) {
						continue
					}
					name, _ := node.Lhs[index].(*ast.Ident)
					changed = setType(
						name,
						goAuditExpressionType(expression, types),
					) || changed
				}
			}
			return true
		})
		if !changed {
			break
		}
	}
	return types
}

func goAuditDeclaredTypeName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case nil:
		return ""
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		return expressionName(expression)
	case *ast.StarExpr:
		return goAuditDeclaredTypeName(expression.X)
	case *ast.ParenExpr:
		return goAuditDeclaredTypeName(expression.X)
	case *ast.IndexExpr:
		return goAuditDeclaredTypeName(expression.X)
	case *ast.IndexListExpr:
		return goAuditDeclaredTypeName(expression.X)
	default:
		return ""
	}
}

func goAuditExpressionType(
	expression ast.Expr,
	types goAuditReceiverTypes,
) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		if expression.Obj != nil {
			return types[expression.Obj]
		}
	case *ast.CompositeLit:
		return goAuditDeclaredTypeName(expression.Type)
	case *ast.UnaryExpr:
		if expression.Op == token.AND || expression.Op == token.MUL {
			return goAuditExpressionType(expression.X, types)
		}
	case *ast.ParenExpr:
		return goAuditExpressionType(expression.X, types)
	case *ast.CallExpr:
		if function, ok := expression.Fun.(*ast.Ident); ok &&
			function.Name == "new" && len(expression.Args) == 1 {
			return goAuditDeclaredTypeName(expression.Args[0])
		}
	}
	return ""
}

func goAuditMethodKey(typeName string, method string) string {
	if typeName == "" {
		return goAuditMethodPrefix + method
	}
	return typeName + "." + method
}

func goAuditFunctionKeys(function *ast.FuncDecl) []string {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return []string{function.Name.Name}
	}
	return []string{
		goAuditMethodKey(
			goAuditDeclaredTypeName(function.Recv.List[0].Type),
			function.Name.Name,
		),
		goAuditMethodKey("", function.Name.Name),
	}
}

func collectGoHostEffectParameterUses(
	file *ast.File,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
	receiverTypes goAuditReceiverTypes,
) map[string]map[int]goHostEffectUse {
	var functions []goAuditFunction
	uses := make(map[string]map[int]goHostEffectUse)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		for _, key := range goAuditFunctionKeys(function) {
			functions = append(functions, goAuditFunction{
				name:       key,
				parameters: goFunctionParameterObjects(function.Type.Params),
				body:       function.Body,
			})
			if uses[key] == nil {
				uses[key] = make(map[int]goHostEffectUse)
			}
		}
	}
	literalKeys := make(map[token.Pos]bool)
	addLiteral := func(name string, literal *ast.FuncLit) {
		if name == "" || literal == nil || literal.Body == nil {
			return
		}
		functions = append(functions, goAuditFunction{
			name:       name,
			parameters: goFunctionParameterObjects(literal.Type.Params),
			body:       literal.Body,
		})
		if uses[name] == nil {
			uses[name] = make(map[int]goHostEffectUse)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FuncLit:
			if !literalKeys[node.Pos()] {
				literalKeys[node.Pos()] = true
				addLiteral(goAuditFunctionLiteralKey(node), node)
			}
		case *ast.AssignStmt:
			for index, expression := range node.Rhs {
				if index >= len(node.Lhs) {
					continue
				}
				name, nameOK := node.Lhs[index].(*ast.Ident)
				literal, literalOK := expression.(*ast.FuncLit)
				if nameOK && literalOK {
					addLiteral(name.Name, literal)
				}
			}
		case *ast.ValueSpec:
			for index, expression := range node.Values {
				if index >= len(node.Names) {
					continue
				}
				literal, ok := expression.(*ast.FuncLit)
				if ok {
					addLiteral(node.Names[index].Name, literal)
				}
			}
		}
		return true
	})
	functionAliases := collectGoAuditFunctionAliases(file, receiverTypes)
	for range 32 {
		changed := false
		for _, function := range functions {
			name := function.name
			parameters := function.parameters
			markExpression := func(
				expression ast.Expr,
				use goHostEffectUse,
			) {
				for index, parameter := range parameters {
					if parameter != nil &&
						expressionReferencesObject(
							expression,
							parameter,
						) &&
						uses[name][index]&use == 0 {
						uses[name][index] |= use
						changed = true
					}
				}
			}
			ast.Inspect(function.body, func(node ast.Node) bool {
				if literal, ok := node.(*ast.FuncLit); ok &&
					literal.Body != function.body {
					return false
				}
				composite, ok := node.(*ast.CompositeLit)
				if ok {
					if execCmdLiteralHasPath(
						composite,
						aliases,
						dotImports,
					) {
						if path, exists := applicationAuditCompositeField(
							composite,
							"Path",
						); exists {
							markExpression(path, goProcessHostEffectUse)
						}
					}
					if expressionName(composite.Type) == "ptySpec" ||
						expressionName(composite.Type) == "processSpec" {
						if argv, exists := applicationAuditCompositeField(
							composite,
							"Argv",
						); exists {
							markExpression(
								argv,
								goProcessHostEffectUse|
									goTerminalHostEffectUse,
							)
						}
					}
				}
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if arguments, output := goTerminalOutputArguments(
					call,
					aliases,
					dotImports,
				); output {
					for _, argument := range arguments {
						markExpression(
							argument,
							goTerminalHostEffectUse,
						)
					}
				}
				if executableIndex, process := processExecutableArgument(
					call,
					aliases,
					dotImports,
				); process && executableIndex < len(call.Args) {
					executable := call.Args[executableIndex]
					markExpression(
						executable,
						goProcessHostEffectUse,
					)
					dynamicExecutable := false
					for _, parameter := range parameters {
						if parameter != nil &&
							expressionReferencesObject(
								executable,
								parameter,
							) {
							dynamicExecutable = true
							break
						}
					}
					if dynamicExecutable ||
						isShellExecutable(executable, staticStrings) {
						for _, argument := range call.Args[executableIndex+1:] {
							markExpression(
								argument,
								goProcessHostEffectUse|
									goTerminalHostEffectUse,
							)
						}
					}
				}
				for _, callee := range goAuditCalleeKeysWithAliases(
					call.Fun,
					receiverTypes,
					functionAliases,
				) {
					for index, use := range uses[callee] {
						if index < len(call.Args) {
							markExpression(call.Args[index], use)
						}
					}
				}
				return true
			})
		}
		if !changed {
			break
		}
	}
	return uses
}

func goFunctionParameterObjects(parameters *ast.FieldList) []*ast.Object {
	if parameters == nil {
		return nil
	}
	var objects []*ast.Object
	for _, field := range parameters.List {
		if len(field.Names) == 0 {
			objects = append(objects, nil)
			continue
		}
		for _, name := range field.Names {
			objects = append(objects, name.Obj)
		}
	}
	return objects
}

func goAuditFunctionLiteralKey(literal *ast.FuncLit) string {
	return fmt.Sprintf("@func:%d", literal.Pos())
}

func goAuditCalleeKeys(
	expression ast.Expr,
	receiverTypes goAuditReceiverTypes,
) []string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return []string{expression.Name}
	case *ast.SelectorExpr:
		return []string{goAuditMethodKey(
			goAuditExpressionType(expression.X, receiverTypes),
			expression.Sel.Name,
		)}
	case *ast.ParenExpr:
		return goAuditCalleeKeys(expression.X, receiverTypes)
	case *ast.FuncLit:
		return []string{goAuditFunctionLiteralKey(expression)}
	default:
		return nil
	}
}

func collectGoAuditFunctionAliases(
	file *ast.File,
	receiverTypes goAuditReceiverTypes,
) map[*ast.Object][]string {
	aliases := make(map[*ast.Object][]string)
	resolve := func(expression ast.Expr) []string {
		return goAuditCalleeKeysWithAliases(
			expression,
			receiverTypes,
			aliases,
		)
	}
	for range 32 {
		changed := false
		ast.Inspect(file, func(node ast.Node) bool {
			var names []*ast.Ident
			var values []ast.Expr
			switch node := node.(type) {
			case *ast.AssignStmt:
				for _, target := range node.Lhs {
					name, _ := target.(*ast.Ident)
					names = append(names, name)
				}
				values = node.Rhs
			case *ast.ValueSpec:
				names = node.Names
				values = node.Values
			default:
				return true
			}
			for index, value := range values {
				if index >= len(names) || names[index] == nil ||
					names[index].Obj == nil {
					continue
				}
				keys := resolve(value)
				if len(keys) == 0 ||
					equalGoAuditFunctionKeys(
						aliases[names[index].Obj],
						keys,
					) {
					continue
				}
				aliases[names[index].Obj] = append([]string(nil), keys...)
				changed = true
			}
			return true
		})
		if !changed {
			break
		}
	}
	return aliases
}

func goAuditCalleeKeysWithAliases(
	expression ast.Expr,
	receiverTypes goAuditReceiverTypes,
	aliases map[*ast.Object][]string,
) []string {
	switch expression := expression.(type) {
	case *ast.Ident:
		if expression.Obj == nil {
			return []string{expression.Name}
		}
		if keys := aliases[expression.Obj]; len(keys) != 0 {
			return append([]string(nil), keys...)
		}
		if function, ok := expression.Obj.Decl.(*ast.FuncDecl); ok {
			return goAuditFunctionKeys(function)
		}
		return nil
	case *ast.ParenExpr:
		return goAuditCalleeKeysWithAliases(
			expression.X,
			receiverTypes,
			aliases,
		)
	case *ast.FuncLit:
		return []string{goAuditFunctionLiteralKey(expression)}
	default:
		return goAuditCalleeKeys(expression, receiverTypes)
	}
}

func equalGoAuditFunctionKeys(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func expressionReferencesObject(
	expression ast.Expr,
	object *ast.Object,
) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Obj == object {
			found = true
			return false
		}
		return true
	})
	return found
}

func goCallInvokesSensitiveHostEffect(
	call *ast.CallExpr,
	uses map[string]map[int]goHostEffectUse,
	staticStrings map[string]map[string]bool,
	receiverTypes goAuditReceiverTypes,
	functionAliases map[*ast.Object][]string,
) bool {
	for _, callee := range goAuditCalleeKeysWithAliases(
		call.Fun,
		receiverTypes,
		functionAliases,
	) {
		for index, use := range uses[callee] {
			if index >= len(call.Args) {
				continue
			}
			argument := call.Args[index]
			if use&goProcessHostEffectUse != 0 &&
				expressionContainsHostEffectMarker(argument, staticStrings) {
				return true
			}
			if use&goTerminalHostEffectUse != 0 &&
				expressionContainsTerminalHostEffect(argument, staticStrings) {
				return true
			}
		}
	}
	return false
}

func auditedTestHostEffectAllowances(
	path string,
	file *ast.File,
	staticStrings map[string]map[string]bool,
) (map[token.Pos]bool, map[token.Pos]bool) {
	allowedCalls := make(map[token.Pos]bool)
	allowedComposites := make(map[token.Pos]bool)
	slashPath := filepath.ToSlash(path)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		switch {
		case slashPath == "cmd/wisp-deck-tui/screenshot_filter_test.go" &&
			function.Name.Name == "TestPumpTerminalOutputFiltersRealPTY":
			if call := auditedScreenshotFilterProcessCall(function); call != nil {
				allowedCalls[call.Pos()] = true
			}
		case slashPath == "internal/codexadapter/supervisor_test.go" &&
			function.Name.Name ==
				"TestCodexPTYConsumesFragmentedOSCAndForwardsOrdinaryBytes":
			for _, call := range auditedCodexFilterOutputCalls(function) {
				allowedCalls[call.Pos()] = true
			}
		case slashPath == "internal/opencodeadapter/supervisor_test.go" &&
			function.Name.Name ==
				"TestRunDefaultPTYPreservesExitAndFiltersTerminalNotifications":
			var candidates []*ast.CompositeLit
			ast.Inspect(function.Body, func(node ast.Node) bool {
				composite, ok := node.(*ast.CompositeLit)
				if ok && testProcessSpecLaunchesHostEffect(
					composite,
					staticStrings,
				) {
					candidates = append(candidates, composite)
				}
				return true
			})
			if len(candidates) == 1 &&
				auditedOpenCodeFilterPTYFixture(
					path,
					function,
					candidates[0],
				) {
				allowedComposites[candidates[0].Pos()] = true
			}
		}
	}
	return allowedCalls, allowedComposites
}

func auditedScreenshotFilterProcessCall(
	function *ast.FuncDecl,
) *ast.CallExpr {
	const exact = "exec.Command(\"/bin/sh\", \"-c\", `printf 'before\\007\\033]9;plain\\007\\033Ptmux;\\033\\033]9;wrapped\\007\\033\\\\after'`)"
	var effectCalls []*ast.CallExpr
	assignedToCmd := 0
	var commandObject *ast.Object
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok {
			rendered, renderedOK := renderApplicationAuditNode(call)
			if renderedOK && rendered == exact {
				effectCalls = append(effectCalls, call)
			}
		}
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 ||
			len(assignment.Rhs) != 1 {
			return true
		}
		left, leftOK := assignment.Lhs[0].(*ast.Ident)
		right, rightOK := assignment.Rhs[0].(*ast.CallExpr)
		if leftOK && rightOK && left.Name == "cmd" {
			rendered, renderedOK := renderApplicationAuditNode(right)
			if renderedOK && rendered == exact {
				assignedToCmd++
				commandObject = left.Obj
			}
		}
		return true
	})
	ptyStarts := 0
	commandWaits := 0
	forbiddenLifecycle := 0
	commandReferences := 0
	if commandObject != nil {
		ast.Inspect(function.Body, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && identifier.Obj == commandObject {
				commandReferences++
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			rendered, renderedOK := renderApplicationAuditNode(call)
			if renderedOK && rendered == "pty.Start(cmd)" &&
				len(call.Args) == 1 {
				command, commandOK := call.Args[0].(*ast.Ident)
				if commandOK && command.Obj == commandObject {
					ptyStarts++
				}
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			receiver, receiverOK := selector.X.(*ast.Ident)
			if !receiverOK || receiver.Obj != commandObject {
				return true
			}
			if selector.Sel.Name == "Wait" &&
				len(call.Args) == 0 {
				commandWaits++
				return true
			}
			forbiddenLifecycle++
			return true
		})
	}
	if len(effectCalls) != 1 || assignedToCmd != 1 ||
		ptyStarts != 1 || commandWaits != 1 ||
		forbiddenLifecycle != 0 || commandReferences != 3 {
		return nil
	}
	return effectCalls[0]
}

func auditedCodexFilterOutputCalls(
	function *ast.FuncDecl,
) []*ast.CallExpr {
	const condition = `os.Getenv("WISP_DECK_CODEX_PTY_CHILD") == "1"`
	var childBody *ast.BlockStmt
	ast.Inspect(function.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.IfStmt)
		if !ok || childBody != nil {
			return true
		}
		rendered, renderedOK := renderApplicationAuditNode(statement.Cond)
		if renderedOK && rendered == condition {
			childBody = statement.Body
			return false
		}
		return true
	})
	if childBody == nil {
		return nil
	}
	required := map[string]int{
		`os.Stdout.Write([]byte("\x1b]9;dynamic"))`:     1,
		`os.Stdout.Write([]byte(" preview\x07\x1b\\"))`: 1,
	}
	var allowed []*ast.CallExpr
	exits := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		rendered, renderedOK := renderApplicationAuditNode(call)
		if !renderedOK {
			return true
		}
		if _, expected := required[rendered]; expected {
			required[rendered]--
			if call.Pos() >= childBody.Pos() &&
				call.End() <= childBody.End() {
				allowed = append(allowed, call)
			}
		}
		if rendered == "os.Exit(0)" &&
			call.Pos() >= childBody.Pos() &&
			call.End() <= childBody.End() {
			exits++
		}
		return true
	})
	for _, remaining := range required {
		if remaining != 0 {
			return nil
		}
	}
	if len(allowed) != 2 || exits != 1 {
		return nil
	}
	return allowed
}

func testSourceLaunchesHostAudio(path string, source []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return true
	}
	aliases, dotImports := processImportAliases(file)
	collectProcessConstructorAliases(file, aliases, dotImports)
	collectTerminalOutputFunctionAliases(file, aliases, dotImports)
	staticStrings := collectStaticStrings(file)
	receiverTypes := collectGoAuditReceiverTypes(file)
	functionAliases := collectGoAuditFunctionAliases(file, receiverTypes)
	parameterUses := collectGoHostEffectParameterUses(
		file,
		aliases,
		dotImports,
		staticStrings,
		receiverTypes,
	)
	allowedCalls, allowedComposites := auditedTestHostEffectAllowances(
		path,
		file,
		staticStrings,
	)
	execCmdVariables := collectExecCmdVariables(file, aliases, dotImports)
	launchesAudio := false
	ast.Inspect(file, func(node ast.Node) bool {
		composite, ok := node.(*ast.CompositeLit)
		if ok && testProcessSpecLaunchesHostEffect(composite, staticStrings) {
			if !allowedComposites[composite.Pos()] {
				launchesAudio = true
				return false
			}
		}
		if ok && execCmdLiteralLaunchesHostEffect(
			composite,
			aliases,
			dotImports,
			staticStrings,
		) {
			launchesAudio = true
			return false
		}
		assignment, ok := node.(*ast.AssignStmt)
		if ok && execCmdPathAssignmentLaunchesHostEffect(
			assignment,
			execCmdVariables,
			staticStrings,
		) {
			launchesAudio = true
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			if expressionReferencesProductionHostEffectRunner(node) {
				launchesAudio = true
				return false
			}
			return true
		}
		if goCallInvokesSensitiveHostEffect(
			call,
			parameterUses,
			staticStrings,
			receiverTypes,
			functionAliases,
		) {
			launchesAudio = true
			return false
		}
		if !allowedCalls[call.Pos()] && testCallWritesHostEffect(
			call,
			staticStrings,
			aliases,
			dotImports,
		) {
			launchesAudio = true
			return false
		}
		switch expressionName(call.Fun) {
		case "runHostEffect":
			if len(call.Args) != 2 || !isZeroHostEffect(call.Args[1]) {
				launchesAudio = true
				return false
			}
		case "playNotificationSound":
			launchesAudio = true
			return false
		case "newNotificationSoundCommand":
			if len(call.Args) > 0 {
				adapter, ok := call.Args[0].(*ast.Ident)
				if ok && adapter.Name == "playNotificationSound" {
					launchesAudio = true
					return false
				}
			}
		}
		if allowedCalls[call.Pos()] {
			return true
		}
		executableIndex, ok := processExecutableArgument(call, aliases, dotImports)
		if !ok || executableIndex >= len(call.Args) {
			return true
		}
		executable := call.Args[executableIndex]
		if expressionContainsHostEffectMarker(executable, staticStrings) {
			launchesAudio = true
			return false
		}
		if isShellExecutable(executable, staticStrings) {
			for _, arg := range call.Args[executableIndex+1:] {
				if expressionContainsHostEffectMarker(arg, staticStrings) {
					launchesAudio = true
					return false
				}
			}
		}
		return true
	})
	return launchesAudio
}

func goTerminalOutputArguments(
	call *ast.CallExpr,
	aliases map[string]string,
	dotImports map[string]bool,
) ([]ast.Expr, bool) {
	importPath, function := calledPackageFunction(
		call.Fun,
		aliases,
		dotImports,
	)
	switch {
	case importPath == "fmt" &&
		(function == "Print" || function == "Printf" ||
			function == "Println"):
		return call.Args, true
	case importPath == "fmt" &&
		(function == "Fprint" || function == "Fprintf" ||
			function == "Fprintln"):
		if len(call.Args) == 0 ||
			!goExpressionIsTerminalOutput(
				call.Args[0],
				aliases,
				dotImports,
			) {
			return nil, false
		}
		return call.Args[1:], true
	case importPath == "io" && function == "WriteString":
		if len(call.Args) == 0 ||
			!goExpressionIsTerminalOutput(
				call.Args[0],
				aliases,
				dotImports,
			) {
			return nil, false
		}
		return call.Args[1:], true
	case importPath == "syscall" && function == "Write":
		if len(call.Args) < 2 ||
			(!hasIntegerLiteral(call.Args[0], "1") &&
				!hasIntegerLiteral(call.Args[0], "2")) {
			return nil, false
		}
		return call.Args[1:], true
	}
	if identifier, ok := call.Fun.(*ast.Ident); ok &&
		(identifier.Name == "print" || identifier.Name == "println") {
		return call.Args, true
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "Write" &&
		selector.Sel.Name != "WriteString") ||
		!goExpressionIsTerminalOutput(
			selector.X,
			aliases,
			dotImports,
		) {
		return nil, false
	}
	return call.Args, true
}

func goExpressionIsTerminalOutput(
	expression ast.Expr,
	aliases map[string]string,
	dotImports map[string]bool,
) bool {
	if identifier, ok := expression.(*ast.Ident); ok {
		return dotImports["os"] &&
			(identifier.Name == "Stdout" ||
				identifier.Name == "Stderr")
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "Stdout" &&
		selector.Sel.Name != "Stderr") {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && aliases[pkg.Name] == "os"
}

func testCallWritesHostEffect(
	call *ast.CallExpr,
	staticStrings map[string]map[string]bool,
	aliases map[string]string,
	dotImports map[string]bool,
) bool {
	arguments, writes := goTerminalOutputArguments(
		call,
		aliases,
		dotImports,
	)
	if !writes {
		return false
	}
	for _, argument := range arguments {
		if expressionContainsTerminalHostEffect(argument, staticStrings) {
			return true
		}
	}
	return false
}

func hasIntegerLiteral(node ast.Node, want string) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if ok && literal.Kind == token.INT && literal.Value == want {
			found = true
			return false
		}
		return true
	})
	return found
}

func expressionContainsTerminalHostEffect(
	expression ast.Expr,
	staticStrings map[string]map[string]bool,
) bool {
	if expressionContainsByteBEL(expression) {
		return true
	}
	for _, value := range resolvedStrings(expression, staticStrings) {
		if stringHasHostEffectMarker(value) ||
			strings.ContainsRune(value, '\a') ||
			shellLineHasBellEscape(value) {
			return true
		}
	}
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || (literal.Kind != token.STRING &&
			literal.Kind != token.CHAR) {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil &&
			(stringHasHostEffectMarker(value) ||
				strings.ContainsRune(value, '\a') ||
				shellLineHasBellEscape(value)) {
			found = true
			return false
		}
		return true
	})
	return found
}

func expressionContainsByteBEL(expression ast.Expr) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && len(call.Args) == 1 {
			function, functionOK := call.Fun.(*ast.Ident)
			if functionOK && function.Name == "string" &&
				goExpressionIsBELByte(call.Args[0]) {
				found = true
				return false
			}
		}
		composite, ok := node.(*ast.CompositeLit)
		if !ok || !goCompositeIsByteSequence(composite) {
			return true
		}
		for _, element := range composite.Elts {
			if goExpressionIsBELByte(element) {
				found = true
				return false
			}
		}
		return !found
	})
	return found
}

func goCompositeIsByteSequence(composite *ast.CompositeLit) bool {
	array, ok := composite.Type.(*ast.ArrayType)
	if !ok {
		return false
	}
	element, ok := array.Elt.(*ast.Ident)
	return ok && (element.Name == "byte" ||
		element.Name == "uint8" || element.Name == "rune")
}

func goExpressionIsBELByte(expression ast.Expr) bool {
	switch expression := expression.(type) {
	case *ast.BasicLit:
		switch expression.Kind {
		case token.INT:
			value, err := strconv.ParseInt(expression.Value, 0, 32)
			return err == nil && value == 7
		case token.CHAR:
			value, err := strconv.Unquote(expression.Value)
			return err == nil && len([]rune(value)) == 1 &&
				[]rune(value)[0] == '\a'
		}
	case *ast.CallExpr:
		if len(expression.Args) == 1 {
			if identifier, ok := expression.Fun.(*ast.Ident); ok &&
				(identifier.Name == "byte" ||
					identifier.Name == "uint8" ||
					identifier.Name == "rune") {
				return goExpressionIsBELByte(expression.Args[0])
			}
		}
	}
	return false
}

func testProcessSpecLaunchesHostEffect(
	literal *ast.CompositeLit,
	staticStrings map[string]map[string]bool,
) bool {
	name := expressionName(literal.Type)
	if name != "ptySpec" && name != "processSpec" {
		return false
	}
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || key.Name != "Argv" {
			continue
		}
		value, ok := field.Value.(ast.Expr)
		return ok && expressionContainsTerminalHostEffect(value, staticStrings)
	}
	return false
}

func auditedOpenCodeFilterPTYFixture(
	path string,
	function *ast.FuncDecl,
	literal *ast.CompositeLit,
) bool {
	if filepath.ToSlash(path) != "internal/opencodeadapter/supervisor_test.go" ||
		function == nil ||
		function.Name.Name !=
			"TestRunDefaultPTYPreservesExitAndFiltersTerminalNotifications" {
		return false
	}
	if expressionName(literal.Type) != "ptySpec" || len(literal.Elts) != 5 {
		return false
	}
	required := map[string]string{
		"Argv":   `[]string{"/bin/sh", "-c", "printf 'left\\007middle\\033]9;native\\007right'; exit 7"}`,
		"Env":    "os.Environ()",
		"CWD":    "t.TempDir()",
		"Stdin":  "bytes.NewReader(nil)",
		"Stdout": "&output",
	}
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return false
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok {
			return false
		}
		want, ok := required[key.Name]
		if !ok {
			return false
		}
		var rendered bytes.Buffer
		if err := format.Node(
			&rendered,
			token.NewFileSet(),
			field.Value,
		); err != nil || rendered.String() != want {
			return false
		}
		delete(required, key.Name)
	}
	if len(required) != 0 {
		return false
	}
	filterCalls := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || expressionName(call.Fun) != "supervisor.runDefaultPTY" ||
			len(call.Args) != 3 || call.Args[1] != literal {
			return true
		}
		background, backgroundOK := call.Args[0].(*ast.CallExpr)
		callback, callbackOK := call.Args[2].(*ast.FuncLit)
		if !backgroundOK ||
			expressionName(background.Fun) != "context.Background" ||
			len(background.Args) != 0 ||
			!callbackOK ||
			!auditedOpenCodeStartCallback(callback) {
			return true
		}
		filterCalls++
		return true
	})
	return filterCalls == 1
}

func auditedOpenCodeStartCallback(callback *ast.FuncLit) bool {
	if callback.Type.Params != nil &&
		len(callback.Type.Params.List) != 0 {
		return false
	}
	if callback.Body == nil {
		return false
	}
	if len(callback.Body.List) == 0 {
		return true
	}
	if len(callback.Body.List) != 1 {
		return false
	}
	assignment, ok := callback.Body.List[0].(*ast.AssignStmt)
	if !ok || assignment.Tok != token.ASSIGN ||
		len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}
	left, leftOK := assignment.Lhs[0].(*ast.Ident)
	right, rightOK := assignment.Rhs[0].(*ast.Ident)
	return leftOK && rightOK &&
		left.Name == "started" && right.Name == "true"
}

func expressionReferencesProductionHostEffectRunner(node ast.Node) bool {
	var expressions []ast.Expr
	switch node := node.(type) {
	case *ast.AssignStmt:
		expressions = node.Rhs
	case *ast.ValueSpec:
		expressions = node.Values
	default:
		return false
	}
	for _, expression := range expressions {
		identifier, ok := expression.(*ast.Ident)
		if ok && (identifier.Name == "runHostEffect" ||
			identifier.Name == "playNotificationSound") {
			return true
		}
	}
	return false
}

func isZeroHostEffect(expression ast.Expr) bool {
	literal, ok := expression.(*ast.CompositeLit)
	if !ok || len(literal.Elts) != 0 {
		return false
	}
	effect, ok := literal.Type.(*ast.Ident)
	return ok && effect.Name == "hostEffect"
}

func processExecutableArgument(
	call *ast.CallExpr,
	aliases map[string]string,
	dotImports map[string]bool,
) (int, bool) {
	importPath, function := calledPackageFunction(call.Fun, aliases, dotImports)
	return processConstructorExecutableIndex(importPath, function)
}

func calledPackageFunction(
	function ast.Expr,
	aliases map[string]string,
	dotImports map[string]bool,
) (string, string) {
	switch function := function.(type) {
	case *ast.ParenExpr:
		return calledPackageFunction(function.X, aliases, dotImports)
	case *ast.SelectorExpr:
		pkg, ok := function.X.(*ast.Ident)
		if !ok {
			return "", ""
		}
		importPath := aliases[pkg.Name]
		if strings.Contains(importPath, processConstructorAliasSeparator) {
			return "", ""
		}
		return importPath, function.Sel.Name
	case *ast.Ident:
		if importPath, name, ok := decodeProcessConstructorAlias(
			aliases[function.Name],
		); ok {
			return importPath, name
		}
		for importPath := range dotImports {
			switch importPath {
			case "os/exec":
				if function.Name == "Command" || function.Name == "CommandContext" {
					return importPath, function.Name
				}
			case "os":
				if function.Name == "StartProcess" {
					return importPath, function.Name
				}
			case "syscall":
				if function.Name == "Exec" || function.Name == "ForkExec" ||
					function.Name == "StartProcess" {
					return importPath, function.Name
				}
				if function.Name == "Write" {
					return importPath, function.Name
				}
			case "fmt":
				switch function.Name {
				case "Print", "Printf", "Println",
					"Fprint", "Fprintf", "Fprintln":
					return importPath, function.Name
				}
			case "io":
				if function.Name == "WriteString" {
					return importPath, function.Name
				}
			default:
				if strings.HasSuffix(importPath, "/unix") &&
					(function.Name == "Exec" || function.Name == "ForkExec") {
					return importPath, function.Name
				}
			}
		}
	}
	return "", ""
}

const processConstructorAliasSeparator = "\x00"

func collectProcessConstructorAliases(
	file *ast.File,
	aliases map[string]string,
	dotImports map[string]bool,
) {
	for range 16 {
		changed := false
		ast.Inspect(file, func(node ast.Node) bool {
			var names []*ast.Ident
			var values []ast.Expr
			switch node := node.(type) {
			case *ast.ValueSpec:
				names = node.Names
				values = node.Values
			case *ast.AssignStmt:
				for _, expression := range node.Lhs {
					name, ok := expression.(*ast.Ident)
					if !ok {
						names = append(names, nil)
						continue
					}
					names = append(names, name)
				}
				values = node.Rhs
			default:
				return true
			}
			for index, value := range values {
				if index >= len(names) || names[index] == nil {
					continue
				}
				importPath, function := calledPackageFunction(
					value,
					aliases,
					dotImports,
				)
				if _, ok := processConstructorExecutableIndex(
					importPath,
					function,
				); !ok {
					continue
				}
				target := importPath + processConstructorAliasSeparator + function
				if aliases[names[index].Name] != target {
					aliases[names[index].Name] = target
					changed = true
				}
			}
			return true
		})
		if !changed {
			return
		}
	}
}

func collectTerminalOutputFunctionAliases(
	file *ast.File,
	aliases map[string]string,
	dotImports map[string]bool,
) {
	for range 16 {
		changed := false
		ast.Inspect(file, func(node ast.Node) bool {
			var names []*ast.Ident
			var values []ast.Expr
			switch node := node.(type) {
			case *ast.ValueSpec:
				names = node.Names
				values = node.Values
			case *ast.AssignStmt:
				for _, expression := range node.Lhs {
					name, _ := expression.(*ast.Ident)
					names = append(names, name)
				}
				values = node.Rhs
			default:
				return true
			}
			for index, value := range values {
				if index >= len(names) || names[index] == nil {
					continue
				}
				importPath, function := calledPackageFunction(
					value,
					aliases,
					dotImports,
				)
				if !goPackageFunctionWritesTerminal(
					importPath,
					function,
				) {
					continue
				}
				target := importPath +
					processConstructorAliasSeparator +
					function
				if aliases[names[index].Name] != target {
					aliases[names[index].Name] = target
					changed = true
				}
			}
			return true
		})
		if !changed {
			return
		}
	}
}

func goPackageFunctionWritesTerminal(
	importPath string,
	function string,
) bool {
	switch importPath {
	case "fmt":
		switch function {
		case "Print", "Printf", "Println",
			"Fprint", "Fprintf", "Fprintln":
			return true
		}
	case "io":
		return function == "WriteString"
	case "syscall":
		return function == "Write"
	}
	return false
}

func decodeProcessConstructorAlias(target string) (string, string, bool) {
	importPath, function, ok := strings.Cut(
		target,
		processConstructorAliasSeparator,
	)
	return importPath, function, ok && importPath != "" && function != ""
}

func processConstructorExecutableIndex(
	importPath string,
	function string,
) (int, bool) {
	switch importPath {
	case "os/exec":
		switch function {
		case "Command":
			return 0, true
		case "CommandContext":
			return 1, true
		}
	case "os":
		if function == "StartProcess" {
			return 0, true
		}
	case "syscall":
		switch function {
		case "Exec", "ForkExec", "StartProcess":
			return 0, true
		}
	default:
		if strings.HasSuffix(importPath, "/unix") &&
			(function == "Exec" || function == "ForkExec") {
			return 0, true
		}
	}
	return 0, false
}

func collectExecCmdVariables(
	file *ast.File,
	aliases map[string]string,
	dotImports map[string]bool,
) map[string]bool {
	variables := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ValueSpec:
			if isExecCmdType(node.Type, aliases, dotImports) {
				for _, name := range node.Names {
					variables[name.Name] = true
				}
			}
			for index, value := range node.Values {
				if index < len(node.Names) &&
					isExecCmdExpression(value, aliases, dotImports, variables) {
					variables[node.Names[index].Name] = true
				}
			}
		case *ast.AssignStmt:
			for index, value := range node.Rhs {
				if index >= len(node.Lhs) ||
					!isExecCmdExpression(value, aliases, dotImports, variables) {
					continue
				}
				if name, ok := node.Lhs[index].(*ast.Ident); ok {
					variables[name.Name] = true
				}
			}
		}
		return true
	})
	return variables
}

func isExecCmdExpression(
	expression ast.Expr,
	aliases map[string]string,
	dotImports map[string]bool,
	known map[string]bool,
) bool {
	switch expression := expression.(type) {
	case *ast.Ident:
		return known[expression.Name]
	case *ast.ParenExpr:
		return isExecCmdExpression(expression.X, aliases, dotImports, known)
	case *ast.UnaryExpr:
		return expression.Op == token.AND &&
			isExecCmdExpression(expression.X, aliases, dotImports, known)
	case *ast.CompositeLit:
		return isExecCmdType(expression.Type, aliases, dotImports)
	case *ast.CallExpr:
		if function, ok := expression.Fun.(*ast.Ident); ok &&
			function.Name == "new" &&
			len(expression.Args) == 1 &&
			isExecCmdType(expression.Args[0], aliases, dotImports) {
			return true
		}
		importPath, function := calledPackageFunction(
			expression.Fun,
			aliases,
			dotImports,
		)
		return importPath == "os/exec" &&
			(function == "Command" || function == "CommandContext")
	default:
		return false
	}
}

func isExecCmdType(
	expression ast.Expr,
	aliases map[string]string,
	dotImports map[string]bool,
) bool {
	switch expression := expression.(type) {
	case *ast.StarExpr:
		return isExecCmdType(expression.X, aliases, dotImports)
	case *ast.SelectorExpr:
		pkg, ok := expression.X.(*ast.Ident)
		return ok && aliases[pkg.Name] == "os/exec" && expression.Sel.Name == "Cmd"
	case *ast.Ident:
		return dotImports["os/exec"] && expression.Name == "Cmd"
	default:
		return false
	}
}

func isShellExecutable(
	expression ast.Expr,
	staticStrings map[string]map[string]bool,
) bool {
	for _, value := range resolvedStrings(expression, staticStrings) {
		switch filepath.Base(value) {
		case "sh", "bash", "zsh":
			return true
		}
	}
	return false
}

func collectStaticStrings(file *ast.File) map[string]map[string]bool {
	values := map[string]map[string]bool{}
	for range 16 {
		changed := false
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.ValueSpec:
				for index, value := range node.Values {
					if index < len(node.Names) {
						changed = addResolvedStrings(
							values,
							node.Names[index].Name,
							resolvedStrings(value, values),
						) || changed
					}
				}
			case *ast.AssignStmt:
				for index, value := range node.Rhs {
					if index >= len(node.Lhs) {
						continue
					}
					name, ok := node.Lhs[index].(*ast.Ident)
					if ok {
						changed = addResolvedStrings(
							values,
							name.Name,
							resolvedStrings(value, values),
						) || changed
					}
				}
			}
			return true
		})
		if !changed {
			break
		}
	}
	return values
}

func addResolvedStrings(
	values map[string]map[string]bool,
	name string,
	resolved []string,
) bool {
	if values[name] == nil {
		values[name] = map[string]bool{}
	}
	changed := false
	for _, value := range resolved {
		if !values[name][value] {
			values[name][value] = true
			changed = true
		}
	}
	return changed
}

func resolvedStrings(
	expression ast.Expr,
	staticStrings map[string]map[string]bool,
) []string {
	switch expression := expression.(type) {
	case *ast.BasicLit:
		if expression.Kind != token.STRING {
			return nil
		}
		value, err := strconv.Unquote(expression.Value)
		if err != nil {
			return nil
		}
		return []string{value}
	case *ast.Ident:
		var resolved []string
		for value := range staticStrings[expression.Name] {
			resolved = append(resolved, value)
		}
		return resolved
	case *ast.ParenExpr:
		return resolvedStrings(expression.X, staticStrings)
	case *ast.BinaryExpr:
		if expression.Op != token.ADD {
			return nil
		}
		left := resolvedStrings(expression.X, staticStrings)
		right := resolvedStrings(expression.Y, staticStrings)
		var resolved []string
		for _, leftValue := range left {
			for _, rightValue := range right {
				if len(resolved) >= 64 {
					return resolved
				}
				resolved = append(resolved, leftValue+rightValue)
			}
		}
		return resolved
	default:
		return nil
	}
}

func expressionContainsResolvedString(
	expression ast.Expr,
	fragment string,
	staticStrings map[string]map[string]bool,
) bool {
	for _, value := range resolvedStrings(expression, staticStrings) {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && strings.Contains(value, fragment) {
			found = true
			return false
		}
		return true
	})
	return found
}

func validateMainMenuSoundPreviewOwnership(path string, source []byte) error {
	file, functions, err := parseGoFunctions(path, source)
	if err != nil {
		return err
	}
	if value, ok := packageStringVariable(
		file,
		"SoundPreviewCapability",
	); !ok || value != "disabled" {
		return fmt.Errorf("ordinary Go builds do not default sound previews to disabled")
	}
	capabilityPolicy, err := requiredFunction(
		functions,
		"mainMenuSoundProcessAllowed",
	)
	if err != nil {
		return err
	}
	if !isProductionCapabilityPolicy(capabilityPolicy) {
		return fmt.Errorf("sound preview process policy is not production-only")
	}
	for _, forbidden := range []string{
		"mainMenuSoundRunner",
		"runMainMenuSoundWith",
		"mainMenuSoundCommand",
		"exec.Command",
		"/usr/bin/afplay",
		"/System/Library/Sounds/",
	} {
		if strings.Contains(string(source), forbidden) {
			return fmt.Errorf("settings preview restored forbidden owner %q", forbidden)
		}
	}

	preview, err := requiredFunction(functions, "mainMenuSoundPreview")
	if err != nil {
		return err
	}
	deferred := returnedFunctionLiterals(preview)
	if !hasOneStringParameter(preview, "name") ||
		countCalls(preview, "newSystemSoundHostEffect") != 1 ||
		len(deferred) != 1 ||
		countCalls(preview, "runHostEffect") != 1 ||
		countCalls(deferred[0], "runHostEffect") != 1 ||
		countCalls(preview, "context.Background") != 1 {
		return fmt.Errorf("settings preview is not one validated deferred typed effect")
	}

	runMenu, err := requiredFunction(functions, "runMainMenu")
	if err != nil {
		return err
	}
	deferIndex := topLevelStatementIndex(runMenu, func(statement ast.Stmt) bool {
		deferStatement, ok := statement.(*ast.DeferStmt)
		return ok && expressionName(deferStatement.Call.Fun) == "cleanup"
	})
	injectIndex := topLevelStatementIndex(runMenu, isSoundPreviewInjection)
	if deferIndex < 0 || injectIndex <= deferIndex ||
		countCallsBySelector(file, "SetSoundPreview") != 1 {
		return fmt.Errorf("settings preview capability is not injected exactly once after TTY setup")
	}

	if reachableCallUsesSelector(functions, "buildMainMenuModel", "SetSoundPreview") {
		return fmt.Errorf("non-interactive main-menu builder can reach an audio capability")
	}
	return nil
}

func validateGoHostEffectOwnership(root string, overrides map[string][]byte) error {
	hostPath := filepath.Join(root, "cmd", "wisp-deck-tui", "host_effects.go")
	menuPath := filepath.Join(root, "cmd", "wisp-deck-tui", "main_menu.go")
	backgroundPath := filepath.Join(root, "cmd", "wisp-deck-tui", "claude_background.go")
	notificationPath := filepath.Join(root, "cmd", "wisp-deck-tui", "notification_sound.go")

	read := func(path string) ([]byte, error) {
		if source, ok := overrides[path]; ok {
			return source, nil
		}
		return os.ReadFile(path)
	}
	hostSource, err := read(hostPath)
	if err != nil {
		return fmt.Errorf("read typed host-effect owner: %w", err)
	}
	hostFile, hostFunctions, err := parseGoFunctions(hostPath, hostSource)
	if err != nil {
		return err
	}
	runner, err := requiredFunction(hostFunctions, "runHostEffect")
	if err != nil {
		return err
	}
	if !hasHostEffectRunnerSignature(runner) {
		return fmt.Errorf("host-effect runner accepts anything other than context and a typed effect")
	}
	if len(runner.Body.List) < 4 ||
		!isCurrentHostEffectsDenialGuard(runner.Body.List[0]) {
		return fmt.Errorf("host-effect runner does not apply current policy first")
	}
	if countCalls(runner, "currentHostEffectsDecision") != 1 ||
		countCalls(runner, "planHostEffect") != 1 ||
		countCalls(runner, "exec.CommandContext") != 1 ||
		countCalls(runner, "cmd.Run") != 1 ||
		countCalls(runner, "cmd.Start") != 0 {
		return fmt.Errorf("host-effect runner lost its single policy-plan-wait lifecycle")
	}
	policyPosition := firstCallPosition(runner, "currentHostEffectsDecision")
	planPosition := firstCallPosition(runner, "planHostEffect")
	commandPosition := firstCallPosition(runner, "exec.CommandContext")
	runPosition := firstCallPosition(runner, "cmd.Run")
	if policyPosition == token.NoPos || planPosition <= policyPosition ||
		commandPosition <= planPosition || runPosition <= commandPosition {
		return fmt.Errorf("host-effect policy, planning, construction, and wait are reordered")
	}
	if countCalls(hostFile, "exec.CommandContext") != 1 ||
		countCalls(hostFile, "exec.Command") != 0 ||
		countCalls(hostFile, "os.StartProcess") != 0 ||
		countCalls(hostFile, "syscall.Exec") != 0 ||
		countCalls(hostFile, "syscall.ForkExec") != 0 {
		return fmt.Errorf("host-effect owner has more than one process construction path")
	}
	hostText := string(hostSource)
	for _, required := range []string{
		`plan, ok := planHostEffect(effect, os.Environ())`,
		`cmd := exec.CommandContext(ctx, plan.executable, plan.arguments...)`,
		`cmd.Env = plan.environment`,
		`cmd.Stdin = nil`,
		`cmd.Stdout = io.Discard`,
		`cmd.Stderr = io.Discard`,
		`cmd.WaitDelay = 100 * time.Millisecond`,
		`cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`,
		`if cmd.Process == nil {`,
		`syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)`,
		`if errors.Is(err, syscall.ESRCH) {`,
		`return cmd.Run()`,
	} {
		if strings.Count(hostText, required) != 1 {
			return fmt.Errorf("host-effect owner must contain exactly one %q", required)
		}
	}
	if strings.Count(hostText, `return os.ErrProcessDone`) != 2 {
		return fmt.Errorf("host-effect cancellation must map nil Process and ESRCH")
	}
	if strings.Count(hostText, `"/usr/bin/afplay"`) != 1 ||
		strings.Count(hostText, `"/usr/bin/osascript"`) != 1 ||
		strings.Count(hostText, `"/System/Library/Sounds/"`) != 1 ||
		strings.Count(hostText, `display notification (system attribute "WISP_DECK_NOTIFICATION_BODY") with title (system attribute "WISP_DECK_NOTIFICATION_TITLE")`) != 1 {
		return fmt.Errorf("typed planner lost its exact audited host-effect literals")
	}
	if !hasClosedHostEffectStruct(hostFile) {
		return fmt.Errorf("typed host effect can encode arbitrary process or notification data")
	}
	for _, functionName := range []string{
		"newSystemSoundHostEffect",
		"newClaudeBackgroundNotificationHostEffect",
		"planHostEffect",
		"configureHostEffectProcessGroup",
	} {
		if _, err := requiredFunction(hostFunctions, functionName); err != nil {
			return err
		}
	}

	menuSource, err := read(menuPath)
	if err != nil {
		return err
	}
	if err := validateMainMenuSoundPreviewOwnership(menuPath, menuSource); err != nil {
		return err
	}

	backgroundSource, err := read(backgroundPath)
	if err != nil {
		return err
	}
	backgroundFile, _, err := parseGoFunctions(backgroundPath, backgroundSource)
	if err != nil {
		return err
	}
	backgroundText := string(backgroundSource)
	for _, forbidden := range []string{
		"claudeBackgroundExecFunc",
		"runClaudeBackgroundDetached",
		`"/usr/bin/afplay"`,
		`"/usr/bin/osascript"`,
		"exec.CommandContext(ctx, name, args...)",
	} {
		if strings.Contains(backgroundText, forbidden) {
			return fmt.Errorf("background notifier restored process owner %q", forbidden)
		}
	}
	notifierType := findStructType(backgroundFile, "claudeBackgroundNotifier")
	if notifierType == nil || structHasField(notifierType, "Run") {
		return fmt.Errorf("background notifier exposes a process runner")
	}
	notify := findMethod(backgroundFile, "claudeBackgroundNotifier", "Notify")
	if notify == nil {
		return fmt.Errorf("missing Claude background notifier")
	}
	if countCalls(notify, "runHostEffect") != 2 ||
		countCalls(notify, "context.WithTimeout") != 2 ||
		countCalls(notify, "withConfiguredNotificationSound") != 1 ||
		countCalls(notify, "newClaudeBackgroundNotificationHostEffect") != 1 ||
		countCalls(notify, "newSystemSoundHostEffect") != 1 {
		return fmt.Errorf("background notifier lost visual/sound typed effects or separate deadlines")
	}
	if !notifierStartsWithDarwinGuard(notify) {
		return fmt.Errorf("background notifier is not Darwin-only")
	}
	visualPosition := firstCallPosition(notify, "newClaudeBackgroundNotificationHostEffect")
	lockPosition := firstCallPosition(notify, "withConfiguredNotificationSound")
	if visualPosition == token.NoPos || lockPosition <= visualPosition {
		return fmt.Errorf("background visual notification no longer precedes locked sound")
	}

	notificationSource, err := read(notificationPath)
	if err != nil {
		return err
	}
	notificationFile, notificationFunctions, err := parseGoFunctions(notificationPath, notificationSource)
	if err != nil {
		return err
	}
	notificationText := string(notificationSource)
	for _, required := range []string{
		`rootCmd.AddCommand(newNotificationSoundCommand(playNotificationSound))`,
		`Use:           "notification-sound --features-file PATH"`,
		`Hidden:        true`,
		`Args:          cobra.NoArgs`,
		`cmd.Flags().StringVar(&features, "features-file", "", "notification sound features file")`,
		`_ = cmd.MarkFlagRequired("features-file")`,
		`return soundpref.WithExclusiveLock(features, func() error {`,
		`sound := soundpref.Read(features)`,
		`return play(sound)`,
	} {
		if strings.Count(notificationText, required) != 1 {
			return fmt.Errorf("notification command must contain exactly one %q", required)
		}
	}
	factory, err := requiredFunction(notificationFunctions, "newNotificationSoundCommand")
	if err != nil {
		return err
	}
	if !hasValidatedSoundFactorySignature(factory) {
		return fmt.Errorf("notification command factory accepts a generic or typed runner")
	}
	locked, err := requiredFunction(notificationFunctions, "withConfiguredNotificationSound")
	if err != nil {
		return err
	}
	if !hasConfiguredSoundSignature(locked) ||
		countCalls(locked, "soundpref.WithExclusiveLock") != 1 ||
		countCalls(locked, "soundpref.Read") != 1 ||
		countCalls(locked, "play") != 1 {
		return fmt.Errorf("notification preference is not one locked read-plus-play transaction")
	}
	transaction := callFunctionLiteralArgument(
		locked,
		"soundpref.WithExclusiveLock",
		1,
	)
	if transaction == nil ||
		countCalls(transaction, "soundpref.Read") != 1 ||
		countCalls(transaction, "play") != 1 {
		return fmt.Errorf("notification preference read or playback escaped the lock callback")
	}
	if countCalls(notificationFile, "exec.Command") != 0 ||
		countCalls(notificationFile, "exec.CommandContext") != 0 {
		return fmt.Errorf("notification command constructed a process outside the typed owner")
	}

	tracked, err := loadTrackedRepositoryAuditFiles(root)
	if err != nil {
		return err
	}
	for relative, file := range tracked {
		if (!strings.HasPrefix(relative, "cmd/") &&
			!strings.HasPrefix(relative, "internal/")) ||
			strings.HasSuffix(relative, "_test.go") ||
			!strings.HasSuffix(relative, ".go") {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		source := file.source
		if override, ok := overrides[path]; ok {
			source = override
		}
		if path != hostPath && productionSourceLaunchesHostEffect(path, source) {
			return fmt.Errorf("host-effect process construction escaped typed owner: %s", path)
		}
		if path != hostPath && sourceHasHostEffectLiteral(path, source) {
			return fmt.Errorf("host-effect process literal escaped typed owner: %s", path)
		}
		if unaudited := unauditedProductionProcessCalls(root, path, source); len(unaudited) != 0 {
			return fmt.Errorf(
				"production process site is not exact-audited: %s",
				strings.Join(unaudited, ", "),
			)
		}
	}
	return nil
}

func hasHostEffectRunnerSignature(function *ast.FuncDecl) bool {
	if function.Type.Params == nil || len(function.Type.Params.List) != 2 ||
		function.Type.Results == nil || len(function.Type.Results.List) != 1 {
		return false
	}
	contextType, ok := function.Type.Params.List[0].Type.(*ast.SelectorExpr)
	contextPackage, packageOK := contextType.X.(*ast.Ident)
	effectType, effectOK := function.Type.Params.List[1].Type.(*ast.Ident)
	resultType, resultOK := function.Type.Results.List[0].Type.(*ast.Ident)
	return ok && packageOK && contextPackage.Name == "context" &&
		contextType.Sel.Name == "Context" &&
		effectOK && effectType.Name == "hostEffect" &&
		resultOK && resultType.Name == "error"
}

func isCurrentHostEffectsDenialGuard(statement ast.Stmt) bool {
	guard, ok := statement.(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil || !blockReturnsNil(guard.Body) {
		return false
	}
	denied, ok := guard.Cond.(*ast.UnaryExpr)
	if !ok || denied.Op != token.NOT {
		return false
	}
	allowed, ok := denied.X.(*ast.SelectorExpr)
	if !ok || allowed.Sel.Name != "Allowed" {
		return false
	}
	decision, ok := allowed.X.(*ast.CallExpr)
	return ok && expressionName(decision.Fun) == "currentHostEffectsDecision" &&
		len(decision.Args) == 0
}

func firstCallPosition(node ast.Node, name string) token.Pos {
	position := token.NoPos
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && expressionName(call.Fun) == name &&
			(position == token.NoPos || call.Pos() < position) {
			position = call.Pos()
		}
		return true
	})
	return position
}

func callFunctionLiteralArgument(
	node ast.Node,
	name string,
	argument int,
) *ast.FuncLit {
	var found *ast.FuncLit
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || expressionName(call.Fun) != name ||
			argument >= len(call.Args) {
			return true
		}
		literal, ok := call.Args[argument].(*ast.FuncLit)
		if ok {
			found = literal
		}
		return false
	})
	return found
}

func hasClosedHostEffectStruct(file *ast.File) bool {
	effect := findStructType(file, "hostEffect")
	if effect == nil {
		return false
	}
	fields := map[string]string{}
	for _, field := range effect.Fields.List {
		if len(field.Names) != 1 {
			return false
		}
		fields[field.Names[0].Name] = expressionName(field.Type)
	}
	if len(fields) != 3 ||
		fields["kind"] != "hostEffectKind" ||
		fields["soundName"] != "string" ||
		fields["notificationKind"] != "claudeBackgroundNotificationKind" {
		return false
	}
	for _, forbidden := range []string{
		"title", "body", "executable", "arguments", "args", "run", "executor",
	} {
		if _, exists := fields[forbidden]; exists {
			return false
		}
	}
	return true
}

func findStructType(file *ast.File, name string) *ast.StructType {
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok || generic.Tok != token.TYPE {
			continue
		}
		for _, specification := range generic.Specs {
			typeSpecification, ok := specification.(*ast.TypeSpec)
			structure, structOK := typeSpecification.Type.(*ast.StructType)
			if ok && structOK && typeSpecification.Name.Name == name {
				return structure
			}
		}
	}
	return nil
}

func structHasField(structure *ast.StructType, name string) bool {
	for _, field := range structure.Fields.List {
		for _, candidate := range field.Names {
			if candidate.Name == name {
				return true
			}
		}
	}
	return false
}

func findMethod(file *ast.File, receiverType, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != name ||
			len(function.Recv.List) != 1 {
			continue
		}
		receiver := function.Recv.List[0].Type
		if pointer, ok := receiver.(*ast.StarExpr); ok {
			receiver = pointer.X
		}
		identifier, ok := receiver.(*ast.Ident)
		if ok && identifier.Name == receiverType {
			return function
		}
	}
	return nil
}

func notifierStartsWithDarwinGuard(function *ast.FuncDecl) bool {
	if len(function.Body.List) == 0 {
		return false
	}
	guard, ok := function.Body.List[0].(*ast.IfStmt)
	if !ok || !blockReturnsBare(guard.Body) {
		return false
	}
	comparison, ok := guard.Cond.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.NEQ {
		return false
	}
	left, leftOK := comparison.X.(*ast.SelectorExpr)
	right, rightOK := comparison.Y.(*ast.BasicLit)
	if !leftOK || !rightOK || expressionName(left) != "n.GOOS" ||
		right.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(right.Value)
	return err == nil && value == "darwin"
}

func blockReturnsBare(block *ast.BlockStmt) bool {
	if block == nil || len(block.List) != 1 {
		return false
	}
	statement, ok := block.List[0].(*ast.ReturnStmt)
	return ok && len(statement.Results) == 0
}

func hasValidatedSoundFactorySignature(function *ast.FuncDecl) bool {
	if function.Type.Params == nil || len(function.Type.Params.List) != 1 {
		return false
	}
	return isValidatedSoundCallback(function.Type.Params.List[0].Type)
}

func hasConfiguredSoundSignature(function *ast.FuncDecl) bool {
	if function.Type.Params == nil || len(function.Type.Params.List) != 2 {
		return false
	}
	features, ok := function.Type.Params.List[0].Type.(*ast.Ident)
	return ok && features.Name == "string" &&
		isValidatedSoundCallback(function.Type.Params.List[1].Type)
}

func isValidatedSoundCallback(expression ast.Expr) bool {
	callback, ok := expression.(*ast.FuncType)
	if !ok || callback.Params == nil || len(callback.Params.List) != 1 ||
		callback.Results == nil || len(callback.Results.List) != 1 {
		return false
	}
	parameter, parameterOK := callback.Params.List[0].Type.(*ast.Ident)
	result, resultOK := callback.Results.List[0].Type.(*ast.Ident)
	return parameterOK && parameter.Name == "string" &&
		resultOK && result.Name == "error"
}

func sourceHasHostEffectLiteral(path string, source []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return true
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && stringHasHostEffectMarker(value) {
			found = true
			return false
		}
		return true
	})
	return found
}

func productionSourceLaunchesHostEffect(path string, source []byte) bool {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, source, 0)
	if err != nil {
		return true
	}
	aliases, dotImports := processImportAliases(file)
	collectProcessConstructorAliases(file, aliases, dotImports)
	collectTerminalOutputFunctionAliases(file, aliases, dotImports)
	staticStrings := collectStaticStrings(file)
	receiverTypes := collectGoAuditReceiverTypes(file)
	functionAliases := collectGoAuditFunctionAliases(file, receiverTypes)
	parameterUses := collectGoHostEffectParameterUses(
		file,
		aliases,
		dotImports,
		staticStrings,
		receiverTypes,
	)
	execCmdVariables := collectExecCmdVariables(file, aliases, dotImports)
	launchesEffect := false
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.CompositeLit:
			if execCmdLiteralLaunchesHostEffect(
				node,
				aliases,
				dotImports,
				staticStrings,
			) {
				launchesEffect = true
				return false
			}
		case *ast.AssignStmt:
			if execCmdPathAssignmentLaunchesHostEffect(node, execCmdVariables, staticStrings) {
				launchesEffect = true
				return false
			}
		case *ast.CallExpr:
			if goCallInvokesSensitiveHostEffect(
				node,
				parameterUses,
				staticStrings,
				receiverTypes,
				functionAliases,
			) {
				launchesEffect = true
				return false
			}
			if testCallWritesHostEffect(
				node,
				staticStrings,
				aliases,
				dotImports,
			) {
				launchesEffect = true
				return false
			}
			executableIndex, ok := processExecutableArgument(node, aliases, dotImports)
			if !ok || executableIndex >= len(node.Args) {
				return true
			}
			executable := node.Args[executableIndex]
			if expressionContainsHostEffectMarker(executable, staticStrings) {
				launchesEffect = true
				return false
			}
			if isShellExecutable(executable, staticStrings) {
				for _, argument := range node.Args[executableIndex+1:] {
					if expressionContainsHostEffectMarker(argument, staticStrings) {
						launchesEffect = true
						return false
					}
				}
			}
		}
		return !launchesEffect
	})
	return launchesEffect
}

func unauditedProductionProcessCalls(root, path string, source []byte) []string {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return []string{path + ":parse-error"}
	}
	aliases, dotImports := processImportAliases(file)
	collectProcessConstructorAliases(file, aliases, dotImports)
	execCmdVariables := collectExecCmdVariables(file, aliases, dotImports)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return []string{path + ":relative-path-error"}
	}
	relative = filepath.ToSlash(relative)
	allowed := auditedProductionProcessCalls()
	seen := map[string]int{}
	type processOwner struct {
		identity string
		node     ast.Node
	}
	var owners []processOwner
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Body == nil {
				continue
			}
			identity := declaration.Name.Name
			if declaration.Recv != nil && len(declaration.Recv.List) == 1 {
				receiver := declaration.Recv.List[0].Type
				if pointer, ok := receiver.(*ast.StarExpr); ok {
					receiver = pointer.X
				}
				identity = expressionName(receiver) + "." + identity
			}
			owners = append(owners, processOwner{
				identity: identity,
				node:     declaration.Body,
			})
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				values, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, value := range values.Values {
					identity := "<package>"
					if index < len(values.Names) {
						identity = values.Names[index].Name
					}
					owners = append(owners, processOwner{
						identity: identity,
						node:     value,
					})
				}
			}
		}
	}
	for _, owner := range owners {
		ast.Inspect(owner.node, func(node ast.Node) bool {
			var processNode ast.Node
			switch node := node.(type) {
			case *ast.CallExpr:
				executableIndex, process := processExecutableArgument(
					node,
					aliases,
					dotImports,
				)
				if process && executableIndex < len(node.Args) {
					processNode = node
				}
			case *ast.CompositeLit:
				if execCmdLiteralHasPath(node, aliases, dotImports) {
					processNode = node
				}
			case *ast.AssignStmt:
				if execCmdPathAssignmentIsOwner(node, execCmdVariables) {
					processNode = node
				}
			}
			if processNode == nil {
				return true
			}
			var rendered bytes.Buffer
			if err := format.Node(
				&rendered,
				token.NewFileSet(),
				processNode,
			); err != nil {
				seen[relative+":"+owner.identity+":render-error"]++
				return true
			}
			seen[relative+":"+owner.identity+":"+rendered.String()]++
			return true
		})
	}
	var violations []string
	for descriptor, count := range seen {
		if allowed[descriptor] != count {
			violations = append(
				violations,
				fmt.Sprintf("%s[%d]", descriptor, count),
			)
		}
	}
	for descriptor, count := range allowed {
		if strings.HasPrefix(descriptor, relative+":") && seen[descriptor] != count {
			violations = append(
				violations,
				fmt.Sprintf("%s[got %d want %d]", descriptor, seen[descriptor], count),
			)
		}
	}
	sort.Strings(violations)
	return violations
}

func auditedProductionProcessCalls() map[string]int {
	return map[string]int{
		`cmd/wisp-deck-tui/claude_background.go:runClaudeBackgroundAgents:exec.CommandContext(ctx, claude, "agents", "--json", "--all")`:                    1,
		`cmd/wisp-deck-tui/claude_background.go:claudeBackgroundProcessStart:exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=")`: 1,
		`cmd/wisp-deck-tui/host_effects.go:runHostEffect:exec.CommandContext(ctx, plan.executable, plan.arguments...)`:                                      1,
		`cmd/wisp-deck-tui/select_branch.go:runSelectBranch:exec.Command("git", "-C", projectPathFlag, "worktree", "list", "--porcelain")`:                  1,
		`cmd/wisp-deck-tui/screenshot_filter.go:runScreenshotFilter:exec.Command(args[0], args[1:]...)`:                                                     2,
		`internal/attention/claude_registry.go:commandOutput:exec.CommandContext(ctx, name, args...)`:                                                       1,
		`internal/attention/claude_supervisor.go:ClaudeSupervisor.Run:exec.Command(name, args...)`:                                                          1,
		`internal/attention/claude_supervisor.go:claudeSupervisorSnapshot:exec.CommandContext(ctx, claudePSExecutable, "-axo", "pid=,ppid=,lstart=")`:       1,
		`internal/codexadapter/supervisor.go:CodexSupervisor.runPTYAttemptWithRouter:exec.Command(argv[0], argv[1:]...)`:                                    1,
		`internal/codexadapter/supervisor.go:startDefaultAppServer:exec.Command(argv[0], argv[1:]...)`:                                                      1,
		`internal/gptbridge/adapter.go:RunAdapter:exec.Command(options.ClaudeArgv[0], options.ClaudeArgv[1:]...)`:                                           1,
		`internal/gptbridge/adapter.go:OpenChatGPTAuthURL:exec.Command("open", authURL)`:                                                                    1,
		`internal/gptbridge/rpc.go:StartAppServer:exec.Command(options.CodexPath, "app-server")`:                                                            1,
		`internal/ledger/popup.go:ExecProcessRunner.Run:exec.CommandContext(ctx, name, args...)`:                                                            1,
		`internal/ledger/source.go:runGit:exec.CommandContext(ctx, "git", commandArgs...)`:                                                                  1,
		`internal/models/worktree.go:AddWorktree:exec.Command("git", "-C", projectPath, "worktree", "add", "-b", branch, wtPath)`:                           1,
		`internal/models/worktree.go:DeleteBranch:exec.Command("git", "-C", projectPath, "branch", "-D", branch)`:                                           1,
		`internal/models/worktree.go:DeleteBranch:exec.Command("git", "-C", projectPath, "push", "origin", "--delete", name)`:                               1,
		`internal/models/worktree.go:DetectWorktrees:exec.Command("git", "-C", projectPath, "worktree", "list", "--porcelain")`:                             1,
		`internal/models/worktree.go:ListBranches:exec.Command("git", "-C", projectPath, "branch", "-a", "--format=%(refname:short)")`:                      1,
		`internal/models/worktree.go:RemoveWorktree:exec.Command("git", args...)`:                                                                           1,
		`internal/opencodeadapter/supervisor.go:Supervisor.runDefaultPTY:exec.Command(spec.Argv[0], spec.Argv[1:]...)`:                                      1,
		`internal/opencodeadapter/supervisor.go:startManagedProcess:exec.Command(spec.Argv[0], spec.Argv[1:]...)`:                                           1,
		`internal/tui/ai_tools_panel.go:bashLibCmd:exec.Command("bash", "-c", script)`:                                                                      1,
		`internal/tui/imagedecode.go:sipsConvert:exec.CommandContext(ctx, "sips", args...)`:                                                                 1,
		`internal/tui/imagedecode.go:sipsExceedsPreviewCeiling:exec.CommandContext(ctx, "sips", "-g", "pixelWidth", "-g", "pixelHeight", path)`:             1,
		`internal/tui/cloneprogress.go:defaultGitClone:exec.Command("git", "clone", "--progress", "--", url, dest)`:                                         1,
		`internal/tui/imageview.go:openInPreview:exec.Command("open", "-a", "Preview", path)`:                                                               1,
		`internal/tui/mainmenu.go:MainMenuModel.Update:exec.Command("git", "-C", projectPath, "worktree", "add", worktreePath, branch)`:                     1,
		`internal/tui/mainmenu.go:MainMenuModel.selectCurrent:exec.Command("git", "-C", m.projects[projectIdx].Path, "worktree", "list", "--porcelain")`:    1,
	}
}

func processImportAliases(file *ast.File) (map[string]string, map[string]bool) {
	aliases := map[string]string{}
	dotImports := map[string]bool{}
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}
		if imported.Name != nil {
			if imported.Name.Name == "." {
				dotImports[path] = true
			} else if imported.Name.Name != "_" {
				aliases[imported.Name.Name] = path
			}
			continue
		}
		aliases[filepath.Base(path)] = path
	}
	return aliases, dotImports
}

func execCmdLiteralLaunchesHostEffect(
	literal *ast.CompositeLit,
	aliases map[string]string,
	dotImports map[string]bool,
	staticStrings map[string]map[string]bool,
) bool {
	if !isExecCmdType(literal.Type, aliases, dotImports) {
		return false
	}
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, keyOK := field.Key.(*ast.Ident)
		value, valueOK := field.Value.(ast.Expr)
		if keyOK && valueOK && key.Name == "Path" &&
			expressionContainsHostEffectMarker(value, staticStrings) {
			return true
		}
	}
	return false
}

func execCmdLiteralHasPath(
	literal *ast.CompositeLit,
	aliases map[string]string,
	dotImports map[string]bool,
) bool {
	if !isExecCmdType(literal.Type, aliases, dotImports) {
		return false
	}
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if ok && key.Name == "Path" {
			return true
		}
	}
	return false
}

func execCmdPathAssignmentLaunchesHostEffect(
	assignment *ast.AssignStmt,
	execCmdVariables map[string]bool,
	staticStrings map[string]map[string]bool,
) bool {
	for index, left := range assignment.Lhs {
		if index >= len(assignment.Rhs) {
			continue
		}
		selected, ok := left.(*ast.SelectorExpr)
		if !ok || selected.Sel.Name != "Path" {
			continue
		}
		receiver, ok := selected.X.(*ast.Ident)
		if ok && execCmdVariables[receiver.Name] &&
			expressionContainsHostEffectMarker(assignment.Rhs[index], staticStrings) {
			return true
		}
	}
	return false
}

func execCmdPathAssignmentIsOwner(
	assignment *ast.AssignStmt,
	execCmdVariables map[string]bool,
) bool {
	for _, left := range assignment.Lhs {
		selected, ok := left.(*ast.SelectorExpr)
		if !ok || selected.Sel.Name != "Path" {
			continue
		}
		receiver, ok := selected.X.(*ast.Ident)
		if ok && execCmdVariables[receiver.Name] {
			return true
		}
	}
	return false
}

func expressionContainsHostEffectMarker(
	expression ast.Expr,
	staticStrings map[string]map[string]bool,
) bool {
	for _, value := range resolvedStrings(expression, staticStrings) {
		if stringHasHostEffectMarker(value) {
			return true
		}
	}
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && stringHasHostEffectMarker(value) {
			found = true
			return false
		}
		return true
	})
	return found
}

func stringHasHostEffectMarker(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"afplay",
		"osascript",
		"/usr/bin/say",
		"/system/library/sounds/",
		"nssound",
		"audioservicesplaysystemsound",
		"display notification",
		"]9;",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return lower == "say"
}

func parseGoFunctions(path string, source []byte) (*ast.File, map[string]*ast.FuncDecl, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil {
			functions[function.Name.Name] = function
		}
	}
	return file, functions, nil
}

func requiredFunction(functions map[string]*ast.FuncDecl, name string) (*ast.FuncDecl, error) {
	function := functions[name]
	if function == nil {
		return nil, fmt.Errorf("missing %s function", name)
	}
	return function, nil
}

func packageStringVariable(file *ast.File, name string) (string, bool) {
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok || generic.Tok != token.VAR {
			continue
		}
		for _, specification := range generic.Specs {
			valueSpec, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, candidate := range valueSpec.Names {
				if candidate.Name != name || index >= len(valueSpec.Values) {
					continue
				}
				literal, ok := valueSpec.Values[index].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return "", false
				}
				value, err := strconv.Unquote(literal.Value)
				return value, err == nil
			}
		}
	}
	return "", false
}

func isProductionCapabilityPolicy(function *ast.FuncDecl) bool {
	if function.Body == nil || len(function.Body.List) != 1 {
		return false
	}
	returnStatement, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returnStatement.Results) != 1 {
		return false
	}
	and, ok := returnStatement.Results[0].(*ast.BinaryExpr)
	if !ok || and.Op != token.LAND {
		return false
	}
	enabled, ok := and.X.(*ast.BinaryExpr)
	if !ok || enabled.Op != token.EQL {
		return false
	}
	capability, leftOK := enabled.X.(*ast.Ident)
	value, rightOK := enabled.Y.(*ast.BasicLit)
	if !leftOK || !rightOK || capability.Name != "soundCapability" ||
		value.Kind != token.STRING {
		return false
	}
	unquoted, err := strconv.Unquote(value.Value)
	if err != nil || unquoted != "enabled" {
		return false
	}
	allowed, ok := and.Y.(*ast.SelectorExpr)
	if !ok || allowed.Sel.Name != "Allowed" {
		return false
	}
	decision, ok := allowed.X.(*ast.Ident)
	return ok && decision.Name == "decision"
}

func hasOneStringParameter(function *ast.FuncDecl, name string) bool {
	if function.Type.Params == nil || len(function.Type.Params.List) != 1 {
		return false
	}
	parameter := function.Type.Params.List[0]
	parameterType, ok := parameter.Type.(*ast.Ident)
	return ok && parameterType.Name == "string" &&
		len(parameter.Names) == 1 && parameter.Names[0].Name == name
}

func isProcessDeniedOrNilRunnerGuard(condition ast.Expr) bool {
	or, ok := condition.(*ast.BinaryExpr)
	if !ok || or.Op != token.LOR {
		return false
	}
	return (isDeniedProductionCapability(or.X) && isNilRunnerComparison(or.Y)) ||
		(isDeniedProductionCapability(or.Y) && isNilRunnerComparison(or.X))
}

func isDeniedProductionCapability(expression ast.Expr) bool {
	denied, ok := expression.(*ast.UnaryExpr)
	return ok && denied.Op == token.NOT &&
		isProductionCapabilityCall(denied.X)
}

func isTestingCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	return ok && expressionName(call.Fun) == "testing.Testing" &&
		len(call.Args) == 0
}

func isProductionCapabilityCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok || expressionName(call.Fun) != "mainMenuSoundProcessAllowed" ||
		len(call.Args) != 2 {
		return false
	}
	capability, ok := call.Args[0].(*ast.Ident)
	if !ok || capability.Name != "SoundPreviewCapability" {
		return false
	}
	decision, ok := call.Args[1].(*ast.CallExpr)
	return ok && expressionName(decision.Fun) == "currentHostEffectsDecision" &&
		len(decision.Args) == 0
}

func isNilRunnerComparison(expression ast.Expr) bool {
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL {
		return false
	}
	left, leftOK := comparison.X.(*ast.Ident)
	right, rightOK := comparison.Y.(*ast.Ident)
	return leftOK && rightOK &&
		((left.Name == "run" && right.Name == "nil") ||
			(left.Name == "nil" && right.Name == "run"))
}

func expressionName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		prefix := expressionName(expression.X)
		if prefix == "" {
			return expression.Sel.Name
		}
		return prefix + "." + expression.Sel.Name
	default:
		return ""
	}
}

func countCalls(node ast.Node, name string) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && expressionName(call.Fun) == name {
			count++
		}
		return true
	})
	return count
}

func hasStringLiteral(node ast.Node, want string) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && value == want {
			found = true
			return false
		}
		return true
	})
	return found
}

func returnedFunctionLiterals(function *ast.FuncDecl) []*ast.FuncLit {
	var literals []*ast.FuncLit
	for _, statement := range function.Body.List {
		returnStatement, ok := statement.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		for _, result := range returnStatement.Results {
			if literal, ok := result.(*ast.FuncLit); ok {
				literals = append(literals, literal)
			}
		}
	}
	return literals
}

func countWaitedExecCommands(node ast.Node) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		runCall, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		runSelector, ok := runCall.Fun.(*ast.SelectorExpr)
		if !ok || runSelector.Sel.Name != "Run" {
			return true
		}
		commandCall, ok := runSelector.X.(*ast.CallExpr)
		if ok && expressionName(commandCall.Fun) == "exec.Command" {
			count++
		}
		return true
	})
	return count
}

func blockReturnsNil(block *ast.BlockStmt) bool {
	if block == nil {
		return false
	}
	for _, statement := range block.List {
		returnStatement, ok := statement.(*ast.ReturnStmt)
		if len(block.List) == 1 && ok && len(returnStatement.Results) == 1 {
			identifier, ok := returnStatement.Results[0].(*ast.Ident)
			return ok && identifier.Name == "nil"
		}
	}
	return false
}

func topLevelStatementIndex(
	function *ast.FuncDecl,
	matches func(ast.Stmt) bool,
) int {
	for index, statement := range function.Body.List {
		if matches(statement) {
			return index
		}
	}
	return -1
}

func isSoundPreviewInjection(statement ast.Stmt) bool {
	guard, ok := statement.(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil ||
		!isProductionCapabilityCall(guard.Cond) ||
		len(guard.Body.List) != 1 {
		return false
	}
	return isSoundPreviewSetStatement(guard.Body.List[0])
}

func isSoundPreviewSetStatement(statement ast.Stmt) bool {
	expressionStatement, ok := statement.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expressionStatement.X.(*ast.CallExpr)
	if !ok || expressionName(call.Fun) != "model.SetSoundPreview" ||
		len(call.Args) != 1 {
		return false
	}
	preview, ok := call.Args[0].(*ast.Ident)
	return ok && preview.Name == "mainMenuSoundPreview"
}

func countCallsBySelector(node ast.Node, selector string) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selected, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selected.Sel.Name == selector {
			count++
		}
		return true
	})
	return count
}

func reachableCallUsesSelector(
	functions map[string]*ast.FuncDecl,
	start string,
	selector string,
) bool {
	visited := map[string]bool{}
	var visit func(string) bool
	visit = func(name string) bool {
		if visited[name] {
			return false
		}
		visited[name] = true
		function := functions[name]
		if function == nil {
			return false
		}
		var called []string
		found := false
		ast.Inspect(function, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selected, ok := call.Fun.(*ast.SelectorExpr); ok &&
				selected.Sel.Name == selector {
				found = true
				return false
			}
			if identifier, ok := call.Fun.(*ast.Ident); ok {
				called = append(called, identifier.Name)
			}
			return true
		})
		if found {
			return true
		}
		for _, calledName := range called {
			if visit(calledName) {
				return true
			}
		}
		return false
	}
	return visit(start)
}
