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
			name:   "JavaScript spec",
			path:   "future.spec.js",
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
			name:   "direct fs output",
			path:   "future.test.js",
			source: `require("fs").writeSync(1, "\x07");`,
		},
		{
			name:   "bracket stdout output",
			path:   "future.test.js",
			source: `process["stdout"].write("\x1b]9;audit\x07");`,
		},
		{
			name:   "console terminal output",
			path:   "future.test.js",
			source: `console.log("\x07");`,
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
`),
		}
		if err := validateRepositoryHostEffectInventory(mutated); err != nil {
			t.Fatalf("harmless JavaScript audit fixture rejected: %v", err)
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
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	for _, extension := range []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"} {
		if strings.HasSuffix(base, ".spec"+extension) ||
			strings.HasSuffix(base, ".test"+extension) {
			return true
		}
	}
	shellTest := strings.HasSuffix(base, ".sh") ||
		strings.HasSuffix(base, ".bash") ||
		strings.HasSuffix(base, ".zsh") ||
		strings.HasSuffix(base, ".bats") ||
		!strings.Contains(base, ".")
	return shellTest &&
		(strings.HasPrefix(filepath.ToSlash(path), "test/") ||
			strings.HasSuffix(base, "_test.sh") ||
			strings.HasSuffix(base, ".test.sh") ||
			strings.HasSuffix(base, ".spec.sh"))
}

func trackedTestSourceLaunchesHostEffect(path string, source []byte) bool {
	if strings.HasSuffix(path, "_test.go") {
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
	kind  byte
	value string
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
	tokens, ok := lexJavaScriptAudit(string(source))
	if !ok {
		return true
	}
	constants := javascriptStringConstants(tokens)
	for index, token := range tokens {
		if token.kind != '(' {
			continue
		}
		end := matchingJavaScriptToken(tokens, index, '(', ')')
		if end < 0 {
			return true
		}
		method := javascriptCallMethod(tokens, index)
		arguments := splitJavaScriptArguments(tokens[index+1 : end])
		switch strings.ToLower(method) {
		case "exec", "execsync":
			if len(arguments) > 0 &&
				javascriptExpressionHasHostEffect(arguments[0], constants) {
				return true
			}
		case "execfile", "execfilesync", "spawn", "spawnsync", "command":
			if javascriptProcessArgumentsHaveHostEffect(arguments, constants) {
				return true
			}
		case "write", "writestring", "writeline":
			if javascriptCallTargetsTerminal(tokens, index) &&
				javascriptArgumentsHaveTerminalEffect(arguments, constants) {
				return true
			}
		case "writesync":
			if javascriptWriteSyncTargetsTerminal(tokens, index, arguments) &&
				javascriptArgumentsHaveTerminalEffect(arguments, constants) {
				return true
			}
		case "log", "error", "warn", "info":
			if javascriptCallHasReceiver(tokens, index, "console") &&
				javascriptArgumentsHaveTerminalEffect(arguments, constants) {
				return true
			}
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
			tokens = append(tokens, javascriptAuditToken{kind: 's', value: value})
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
				kind:  'i',
				value: source[start:index],
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
				kind:  'n',
				value: source[start:index],
			})
			continue
		}
		tokens = append(tokens, javascriptAuditToken{
			kind:  character,
			value: string(character),
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
	if open == 0 || tokens[open-1].kind != 'i' {
		return ""
	}
	return tokens[open-1].value
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
		if stringHasHostEffectMarker(value) ||
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
	if stringHasHostEffectMarker(values[0]) ||
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

func javascriptArgumentsHaveTerminalEffect(
	arguments [][]javascriptAuditToken,
	constants map[string]string,
) bool {
	for _, argument := range arguments {
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

func javascriptCallHasReceiver(
	tokens []javascriptAuditToken,
	open int,
	receiver string,
) bool {
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
	for _, token := range tokens[start:open] {
		if token.kind == 'i' && token.value == receiver {
			return true
		}
		if token.kind == 's' && token.value == receiver {
			return true
		}
	}
	return false
}

func javascriptCallTargetsTerminal(
	tokens []javascriptAuditToken,
	open int,
) bool {
	return javascriptCallHasReceiver(tokens, open, "process") &&
		(javascriptCallHasReceiver(tokens, open, "stdout") ||
			javascriptCallHasReceiver(tokens, open, "stderr"))
}

func javascriptWriteSyncTargetsTerminal(
	tokens []javascriptAuditToken,
	open int,
	arguments [][]javascriptAuditToken,
) bool {
	if javascriptCallHasReceiver(tokens, open, "fs") {
		if len(arguments) == 0 || len(arguments[0]) != 1 {
			return false
		}
		target := arguments[0][0]
		return target.kind == 'n' &&
			(target.value == "1" || target.value == "2")
	}
	return javascriptCallTargetsTerminal(tokens, open)
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
		if !strings.HasSuffix(path, "_test.go") {
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
			nil,
		); err != nil {
			return err
		}
	}
	return nil
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
	allowedUnnormalized map[string]bool,
) error {
	applicationValues := applicationValueVariables(
		function,
		aliases,
		dotImports,
		staticStrings,
	)
	applicationCommands := applicationCommandVariables(
		function,
		applicationValues,
		aliases,
		dotImports,
		staticStrings,
	)
	environmentAssignments := make(map[string][]ast.Expr)
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
) map[string]bool {
	values := make(map[string]bool)
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

func expressionUsesNormalizedEnvironment(expression ast.Expr) bool {
	switch expression := expression.(type) {
	case *ast.ParenExpr:
		return expressionUsesNormalizedEnvironment(expression.X)
	case *ast.CallExpr:
		switch expressionName(expression.Fun) {
		case "repositoryTestEnvironment", "buildEnv":
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
	if expressionUsesNormalizedEnvironment(environment) {
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
			); exists {
				return []ast.Expr{environment}
			}
		}
	}
	return nil
}

func applicationAuditProcAttrEnvironment(
	expression ast.Expr,
) (ast.Expr, bool) {
	switch expression := expression.(type) {
	case *ast.ParenExpr:
		return applicationAuditProcAttrEnvironment(expression.X)
	case *ast.UnaryExpr:
		if expression.Op == token.AND {
			return applicationAuditProcAttrEnvironment(expression.X)
		}
	case *ast.CompositeLit:
		return applicationAuditCompositeField(expression, "Env")
	}
	return nil, false
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
	}
	for name, mutated := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := validateInstallerAndReleaseHostEffectBoundary(mutated); err == nil {
				t.Fatal("installer/release host-effect boundary mutation escaped validation")
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
	verification := strings.Index(
		bash,
		`if (( ${#verifier[@]} > 0 )) && ! "${verifier[@]}" "$tmp" >/dev/null 2>&1; then`,
	)
	replacement := strings.Index(bash, `mv -f "$tmp" "$dest"`)
	if verification < 0 || replacement <= verification {
		return fmt.Errorf("Bash installer replaces an artifact before verification")
	}
	bashBeforeVerification := bash[:verification]
	for _, command := range []string{"cp", "install"} {
		if shellSourceHasCommandWithArguments(
			bashBeforeVerification,
			command,
			"$dest",
		) {
			return fmt.Errorf(
				"Bash installer %s writes an artifact before verification",
				command,
			)
		}
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
	nodeVerification := strings.Index(
		node,
		`const downloaded = verifyTuiBinary(tmpPath, version);`,
	)
	nodeReplacement := strings.Index(node, `fs.renameSync(tmpPath, tuiBinPath);`)
	if nodeVerification < 0 || nodeReplacement <= nodeVerification {
		return fmt.Errorf("Node installer replaces an artifact before verification")
	}
	nodeBeforeVerification := node[:nodeVerification]
	for _, method := range []string{
		"fs.copyFileSync",
		"fs.writeFileSync",
		"fs.renameSync",
	} {
		if javascriptSourceCallsWithTarget(
			nodeBeforeVerification,
			method,
			"tuiBinPath",
		) {
			return fmt.Errorf(
				"Node installer %s writes an artifact before verification",
				method,
			)
		}
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
	armBuild := strings.Index(
		release,
		`GOOS=darwin GOARCH=arm64 go build -ldflags "$ldflags"`,
	)
	amdBuild := strings.Index(
		release,
		`GOOS=darwin GOARCH=amd64 go build -ldflags "$ldflags"`,
	)
	preflight := strings.Index(
		release,
		`if ! verify_release_tui_artifacts "$build_dir" "$ldflags"; then`,
	)
	if armBuild < 0 || amdBuild <= armBuild || preflight <= amdBuild {
		return fmt.Errorf("release artifact verification does not follow both builds")
	}
	releaseBeforePreflight := release[:preflight]
	for _, mutation := range []struct {
		command   string
		arguments []string
	}{
		{command: "git", arguments: []string{"-C", "tag"}},
		{command: "git", arguments: []string{"-C", "push"}},
		{command: "npm", arguments: []string{"--prefix", "publish"}},
		{command: "cp", arguments: []string{".local/bin/wisp-deck-tui"}},
		{command: "install", arguments: []string{".local/bin/wisp-deck-tui"}},
	} {
		if shellSourceHasCommandWithArguments(
			releaseBeforePreflight,
			mutation.command,
			mutation.arguments...,
		) {
			return fmt.Errorf(
				"release mutation %s %s precedes artifact verification",
				mutation.command,
				strings.Join(mutation.arguments, " "),
			)
		}
	}
	for _, mutation := range []string{
		`codesign --sign - --force "$build_dir/wisp-deck-tui-darwin-arm64"`,
		`git tag -a "$tag"`,
		`git push origin main --tags`,
		`gh release create "$tag"`,
		`local publish_cmd="npm publish"`,
		`go build -ldflags "$ldflags" -o "$local_bin"`,
	} {
		position := strings.Index(release, mutation)
		if position < 0 || position <= preflight {
			return fmt.Errorf("release mutation %q precedes artifact verification", mutation)
		}
	}
	return nil
}

func shellSourceHasCommandWithArguments(
	source string,
	command string,
	required ...string,
) bool {
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		commandIndex := -1
		for index, field := range fields {
			token := strings.Trim(field, `"'(){}[];,|&`)
			if filepath.Base(token) == command {
				commandIndex = index
				break
			}
		}
		if commandIndex < 0 {
			continue
		}
		matches := true
		for _, want := range required {
			found := false
			for _, field := range fields[commandIndex+1:] {
				token := strings.Trim(field, `"'(){}[];,|&`)
				if strings.Contains(token, want) {
					found = true
					break
				}
			}
			if !found {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func javascriptSourceCallsWithTarget(
	source string,
	method string,
	target string,
) bool {
	marker := method + "("
	for offset := 0; offset < len(source); {
		relative := strings.Index(source[offset:], marker)
		if relative < 0 {
			return false
		}
		start := offset + relative
		end := strings.Index(source[start:], ";")
		if end < 0 {
			end = len(source) - start
		}
		if strings.Contains(source[start:start+end], target) {
			return true
		}
		offset = start + len(marker)
	}
	return false
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
		"wrapper.sh": {
			`  fi

  # Use TUI for project selection
  printf '\033]0;󰊠  Wisp Deck\007'

  # Stop loading animation before TUI takes over
  type stop_loading_screen &>/dev/null && stop_loading_screen`,
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
		"direct stderr OSC notification": {
			source: `package p; import "fmt"; func test() { fmt.Fprint(os.Stderr, "\x1b]9;audit\x07") }`,
			want:   true,
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
	_ = cmd
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

	const codexOutputFixture = `package codexadapter
import "os"
func TestCodexPTYConsumesFragmentedOSCAndForwardsOrdinaryBytes() {
	_, _ = os.Stdout.Write([]byte("\x1b]9;dynamic"))
	_, _ = os.Stdout.Write([]byte(" preview\x07\x1b\\"))
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
}

func testSourceLaunchesHostAudio(path string, source []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return true
	}
	aliases, dotImports := processImportAliases(file)
	collectProcessConstructorAliases(file, aliases, dotImports)
	staticStrings := collectStaticStrings(file)
	execCmdVariables := collectExecCmdVariables(file, aliases, dotImports)
	callOwners := make(map[token.Pos]string)
	nodeOwners := make(map[token.Pos]string)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if node != nil {
				nodeOwners[node.Pos()] = function.Name.Name
			}
			if call, ok := node.(*ast.CallExpr); ok {
				callOwners[call.Pos()] = function.Name.Name
			}
			return true
		})
	}
	launchesAudio := false
	ast.Inspect(file, func(node ast.Node) bool {
		composite, ok := node.(*ast.CompositeLit)
		if ok && testProcessSpecLaunchesHostEffect(composite, staticStrings) {
			if !auditedOpenCodeFilterPTYFixture(
				path,
				nodeOwners[composite.Pos()],
				composite,
			) {
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
		if testCallWritesHostEffect(
			path,
			callOwners[call.Pos()],
			call,
			staticStrings,
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
		if auditedFilteredPTYHostEffectFixture(
			path,
			callOwners[call.Pos()],
			call,
		) {
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

func testCallWritesHostEffect(
	path string,
	owner string,
	call *ast.CallExpr,
	staticStrings map[string]map[string]bool,
) bool {
	name := expressionName(call.Fun)
	arguments := call.Args
	switch name {
	case "os.Stdout.Write", "os.Stdout.WriteString",
		"os.Stderr.Write", "os.Stderr.WriteString",
		"fmt.Print", "fmt.Printf", "fmt.Println",
		"print", "println":
	case "fmt.Fprint", "fmt.Fprintf", "fmt.Fprintln", "io.WriteString":
		if len(arguments) == 0 {
			return false
		}
		target := expressionName(arguments[0])
		if target != "os.Stdout" && target != "os.Stderr" {
			return false
		}
		arguments = arguments[1:]
	case "syscall.Write":
		if len(arguments) < 2 ||
			(!hasIntegerLiteral(arguments[0], "1") &&
				!hasIntegerLiteral(arguments[0], "2")) {
			return false
		}
		arguments = arguments[1:]
	default:
		return false
	}
	for _, argument := range arguments {
		if expressionContainsTerminalHostEffect(argument, staticStrings) {
			if auditedCodexFilterOutputFixture(path, owner, call) {
				continue
			}
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
		if !ok || literal.Kind != token.STRING {
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

func auditedCodexFilterOutputFixture(
	path string,
	owner string,
	call *ast.CallExpr,
) bool {
	if filepath.ToSlash(path) != "internal/codexadapter/supervisor_test.go" ||
		owner != "TestCodexPTYConsumesFragmentedOSCAndForwardsOrdinaryBytes" {
		return false
	}
	var rendered bytes.Buffer
	if err := format.Node(&rendered, token.NewFileSet(), call); err != nil {
		return false
	}
	switch rendered.String() {
	case `os.Stdout.Write([]byte("\x1b]9;dynamic"))`,
		`os.Stdout.Write([]byte(" preview\x07\x1b\\"))`:
		return true
	default:
		return false
	}
}

func auditedFilteredPTYHostEffectFixture(
	path string,
	owner string,
	call *ast.CallExpr,
) bool {
	const fixturePath = "cmd/wisp-deck-tui/screenshot_filter_test.go"
	if filepath.ToSlash(path) != fixturePath ||
		owner != "TestPumpTerminalOutputFiltersRealPTY" {
		return false
	}
	var rendered bytes.Buffer
	if err := format.Node(&rendered, token.NewFileSet(), call); err != nil {
		return false
	}
	const exact = "exec.Command(\"/bin/sh\", \"-c\", `printf 'before\\007\\033]9;plain\\007\\033Ptmux;\\033\\033]9;wrapped\\007\\033\\\\after'`)"
	return rendered.String() == exact
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
	owner string,
	literal *ast.CompositeLit,
) bool {
	if filepath.ToSlash(path) != "internal/opencodeadapter/supervisor_test.go" ||
		owner != "TestRunDefaultPTYPreservesExitAndFiltersTerminalNotifications" {
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
	return len(required) == 0
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
	staticStrings := collectStaticStrings(file)
	execCmdVariables := collectExecCmdVariables(file, aliases, dotImports)
	callOwners := make(map[token.Pos]string)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				callOwners[call.Pos()] = function.Name.Name
			}
			return true
		})
	}
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
			if testCallWritesHostEffect(
				path,
				callOwners[node.Pos()],
				node,
				staticStrings,
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
		`internal/tui/imageview.go:openInPreview:exec.Command("open", "-a", "Preview", path)`:                                                               1,
		`internal/tui/mainmenu.go:MainMenuModel.Update:exec.Command("git", "-C", projectPath, "worktree", "add", worktreePath, branch)`:                     1,
		`internal/tui/mainmenu.go:MainMenuModel.selectCurrent:exec.Command("git", "-C", m.projects[projectIdx].Path, "worktree", "list", "--porcelain")`:    1,
		`internal/tui/mainmenu.go:defaultGitClone:exec.Command("git", "clone", "--", url, dest)`:                                                            1,
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
