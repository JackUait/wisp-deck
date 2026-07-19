package bash_test

import (
	"bytes"
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

// Runtime sound sites are deliberately few. Go process construction belongs
// to one typed owner, while preference playback stays under the shared lock.
func TestIdleSoundRuntimeSitesUseSharedLiveGate(t *testing.T) {
	root := projectRoot(t)
	allowed := map[string]bool{
		filepath.Join(root, "cmd", "wisp-deck-tui", "host_effects.go"): true,
	}
	paths := []string{
		filepath.Join(root, "lib"),
		filepath.Join(root, "templates"),
		filepath.Join(root, "bin"),
		filepath.Join(root, "internal"),
		filepath.Join(root, "cmd"),
		filepath.Join(root, "wrapper.sh"),
	}
	markers := []string{"afplay", "/System/Library/Sounds", "NSSound", "AudioServicesPlaySystemSound"}
	var violations []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		visit := func(candidate string, entry os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || strings.HasSuffix(candidate, "_test.go") || allowed[candidate] {
				return nil
			}
			data, err := os.ReadFile(candidate)
			if err != nil {
				return err
			}
			// Local builds can leave ignored executables under bin/. Only text
			// source can introduce a new playback owner for this source guard.
			if bytes.IndexByte(data, 0) >= 0 {
				return nil
			}
			for _, marker := range markers {
				if strings.Contains(string(data), marker) {
					violations = append(violations, candidate)
					break
				}
			}
			return nil
		}
		if info.IsDir() {
			if err := filepath.Walk(path, visit); err != nil {
				t.Fatal(err)
			}
		} else if err := visit(path, info, nil); err != nil {
			t.Fatal(err)
		}
	}
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

func TestShellProductionHostEffectOwnershipGuardRejectsBypasses(t *testing.T) {
	sources := trackedShellProductionSources(t)
	if err := validateShellProductionHostEffectOwnership(sources); err != nil {
		t.Fatalf("current shell production host-effect inventory rejected: %v", err)
	}
	mutations := map[string]string{
		"afplay":             "afplay /tmp/chime.aiff\n",
		"system sound path":  "printf '%s\\n' /System/Library/Sounds/Glass.aiff\n",
		"NSSound":            "printf '%s\\n' NSSound\n",
		"AudioServices":      "printf '%s\\n' AudioServicesPlaySystemSound\n",
		"speech":             "say audit\n",
		"notification sound": "osascript -e 'display notification \"audit\" with sound name \"Glass\"'\n",
		"OSC 9":              "printf '\\033]9;audit\\007'\n",
		"escaped BEL":        "printf '\\a'\n",
		"short octal BEL":    "printf '\\07'\n",
		"long octal BEL":     "printf '\\0007'\n",
		"short hex BEL":      "printf '\\x7'\n",
		"raw BEL":            "printf 'audit\a'\n",
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
}

func trackedShellProductionSources(t *testing.T) map[string]string {
	t.Helper()
	root := projectRoot(t)
	output, err := exec.Command(
		"git",
		"-C",
		root,
		"ls-files",
		"-z",
		"--",
		"lib/",
		"templates/",
		"bin/",
		"wrapper.sh",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("discover shell production sources: %v\n%s", err, output)
	}
	sources := make(map[string]string)
	for _, relative := range strings.Split(string(output), "\x00") {
		if relative == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read shell production source %s: %v", relative, err)
		}
		if bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		sources[relative] = string(data)
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
			`printf '\033]0;󰊠  Wisp Deck\007'`,
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
	for _, bell := range []string{`\07`, `\007`, `\0007`, `\a`, `\x7`, `\x07`} {
		if strings.Contains(line, bell) {
			return true
		}
	}
	for _, field := range strings.Fields(strings.ToLower(line)) {
		if strings.Trim(field, `"'(){}[];,`) == "say" {
			return true
		}
	}
	return false
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
	root := projectRoot(t)
	var violations []string
	for _, dir := range []string{
		filepath.Join(root, "internal"),
		filepath.Join(root, "cmd"),
		filepath.Join(root, "test"),
	} {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if testSourceLaunchesHostAudio(path, data) {
				violations = append(violations, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
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
	launchesAudio := false
	ast.Inspect(file, func(node ast.Node) bool {
		composite, ok := node.(*ast.CompositeLit)
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
		if auditedFilteredPTYHostEffectFixture(path, call) {
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

func auditedFilteredPTYHostEffectFixture(path string, call *ast.CallExpr) bool {
	const fixturePath = "cmd/wisp-deck-tui/screenshot_filter_test.go"
	if !strings.HasSuffix(filepath.ToSlash(path), fixturePath) {
		return false
	}
	var rendered bytes.Buffer
	if err := format.Node(&rendered, token.NewFileSet(), call); err != nil {
		return false
	}
	const exact = "exec.Command(\"/bin/sh\", \"-c\", `printf 'before\\007\\033]9;plain\\007\\033Ptmux;\\033\\033]9;wrapped\\007\\033\\\\after'`)"
	return rendered.String() == exact
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

	for _, directory := range []string{
		filepath.Join(root, "cmd"),
		filepath.Join(root, "internal"),
	} {
		err := filepath.Walk(directory, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
				return nil
			}
			source, err := read(path)
			if err != nil {
				return err
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
			return nil
		})
		if err != nil {
			return err
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
