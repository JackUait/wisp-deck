package bash_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Runtime sound sites are deliberately few and every non-preview owner must
// make its read-plus-play transaction under the shared preference lock.
func TestIdleSoundRuntimeSitesUseSharedLiveGate(t *testing.T) {
	root := projectRoot(t)
	allowed := map[string]bool{
		filepath.Join(root, "lib", "notification-setup.sh"):                 true,
		filepath.Join(root, "cmd", "wisp-deck-tui", "claude_background.go"): true,
		filepath.Join(root, "cmd", "wisp-deck-tui", "main_menu.go"):         true,
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
		filepath.Join(root, "lib", "notification-setup.sh"): {
			"afplay": 2, "/System/Library/Sounds": 1, "NSSound": 0, "AudioServicesPlaySystemSound": 0,
		},
		filepath.Join(root, "cmd", "wisp-deck-tui", "claude_background.go"): {
			"afplay": 1, "/System/Library/Sounds": 1, "NSSound": 0, "AudioServicesPlaySystemSound": 0,
		},
		filepath.Join(root, "cmd", "wisp-deck-tui", "main_menu.go"): {
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
	if !strings.Contains(string(shell), `/usr/bin/lockf -k "$lock_file"`) ||
		!strings.Contains(string(shell), `sound_name="$(get_sound_name`) {
		t.Fatal("foreground idle playback must lock and re-read its live preference")
	}
	background, err := os.ReadFile(filepath.Join(root, "cmd", "wisp-deck-tui", "claude_background.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(background), "soundpref.WithExclusiveLock(features") ||
		!strings.Contains(string(background), "claudeBackgroundSoundPreference(features)") {
		t.Fatal("background playback must use the same live preference transaction")
	}
	menu, err := os.ReadFile(filepath.Join(root, "cmd", "wisp-deck-tui", "main_menu.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMainMenuSoundPreviewOwnership(string(menu)); err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(background), `"/usr/bin/afplay"`) != 1 {
		t.Fatal("background notifier must have exactly one locked afplay site")
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

func TestMainMenuSoundPreviewOwnershipGuardRejectsBypasses(t *testing.T) {
	root := projectRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "cmd", "wisp-deck-tui", "main_menu.go"))
	if err != nil {
		t.Fatal(err)
	}
	safe := string(data)
	if err := validateMainMenuSoundPreviewOwnership(safe); err != nil {
		t.Fatalf("current preview adapter rejected: %v", err)
	}

	movedBeforeTTY := strings.Replace(
		safe,
		"\tmodel.SetSoundPreview(mainMenuSoundPreview(runMainMenuSound))\n",
		"",
		1,
	)
	movedBeforeTTY = strings.Replace(
		movedBeforeTTY,
		"\tttyOpts, cleanup, err := util.TUITeaOptions()\n",
		"\tmodel.SetSoundPreview(mainMenuSoundPreview(runMainMenuSound))\n\n"+
			"\tttyOpts, cleanup, err := util.TUITeaOptions()\n",
		1,
	)

	mutations := map[string]string{
		"missing allowlist": strings.Replace(
			safe,
			"!slices.Contains(tui.SystemSounds, name)",
			"false",
			1,
		),
		"relative player": strings.Replace(
			safe,
			`run("/usr/bin/afplay", path)`,
			`run("afplay", path)`,
			1,
		),
		"eager callback": strings.Replace(
			safe,
			"return func() tea.Msg {",
			"func() tea.Msg {",
			1,
		),
		"unwaited player": strings.Replace(
			safe,
			"exec.Command(name, args...).Run()",
			"exec.Command(name, args...).Start()",
			1,
		),
		"missing injection": strings.Replace(
			safe,
			"\tmodel.SetSoundPreview(mainMenuSoundPreview(runMainMenuSound))\n",
			"",
			1,
		),
		"injection before TTY": movedBeforeTTY,
		"builder has capability": strings.Replace(
			safe,
			"func buildMainMenuModel() (*tui.MainMenuModel, error) {",
			"func buildMainMenuModel() (*tui.MainMenuModel, error) {\n"+
				"\tmodel.SetSoundPreview(mainMenuSoundPreview(runMainMenuSound))",
			1,
		),
	}
	for name, source := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := validateMainMenuSoundPreviewOwnership(source); err == nil {
				t.Fatal("unsafe preview layout passed the ownership guard")
			}
		})
	}
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
		"fake runner": {
			source: `package p; func test() { preview := mainMenuSoundPreview(fakeRunner); _ = preview }`,
		},
		"quoted production symbols": {
			source: `package p; const example = "exec.Command(\"/usr/bin/afplay\"); runMainMenuSound("`,
		},
		"unrelated process": {
			source: `package p; import "os/exec"; func test() { _ = exec.Command("git", "status") }`,
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
		"production runner argument": {
			source: `package p; func test() { _ = mainMenuSoundPreview(runMainMenuSound) }`,
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
}

func testSourceLaunchesHostAudio(path string, source []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return true
	}
	execPackages := map[string]bool{}
	dotExec := false
	for _, imported := range file.Imports {
		importPath, err := strconv.Unquote(imported.Path.Value)
		if err != nil || importPath != "os/exec" {
			continue
		}
		switch {
		case imported.Name == nil:
			execPackages["exec"] = true
		case imported.Name.Name == ".":
			dotExec = true
		case imported.Name.Name != "_":
			execPackages[imported.Name.Name] = true
		}
	}
	launchesAudio := false
	ast.Inspect(file, func(node ast.Node) bool {
		if name, ok := node.(*ast.Ident); ok && name.Name == "runMainMenuSound" {
			launchesAudio = true
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		isExecCommand := false
		switch command := call.Fun.(type) {
		case *ast.Ident:
			isExecCommand = dotExec &&
				(command.Name == "Command" || command.Name == "CommandContext")
		case *ast.SelectorExpr:
			pkg, ok := command.X.(*ast.Ident)
			isExecCommand = ok && execPackages[pkg.Name] &&
				(command.Sel.Name == "Command" || command.Sel.Name == "CommandContext")
		}
		if !isExecCommand {
			return true
		}
		for _, arg := range call.Args {
			if expressionContainsString(arg, "afplay") {
				launchesAudio = true
				return false
			}
		}
		return true
	})
	return launchesAudio
}

func expressionContainsString(expression ast.Expr, fragment string) bool {
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

func validateMainMenuSoundPreviewOwnership(source string) error {
	preview, err := sourceFunction(source, "mainMenuSoundPreview")
	if err != nil {
		return err
	}
	for label, required := range map[string]string{
		"allowlist":        "!slices.Contains(tui.SystemSounds, name)",
		"deferred command": "return func() tea.Msg {",
		"absolute player":  `run("/usr/bin/afplay", path)`,
		"fixed sound root": `path := "/System/Library/Sounds/" + name + ".aiff"`,
	} {
		if !strings.Contains(preview, required) {
			return fmt.Errorf("Settings preview lost its %s boundary", label)
		}
	}
	if strings.Index(preview, "return func() tea.Msg {") >
		strings.Index(preview, `run("/usr/bin/afplay", path)`) {
		return fmt.Errorf("Settings preview runs before its Bubble Tea command")
	}

	runner, err := sourceFunction(source, "runMainMenuSound")
	if err != nil {
		return err
	}
	if !strings.Contains(runner, "exec.Command(name, args...).Run()") {
		return fmt.Errorf("Settings preview player is not waited")
	}

	runMenu, err := sourceFunction(source, "runMainMenu")
	if err != nil {
		return err
	}
	tty := strings.Index(runMenu, "defer cleanup()")
	inject := strings.Index(
		runMenu,
		"model.SetSoundPreview(mainMenuSoundPreview(runMainMenuSound))",
	)
	if tty < 0 || inject < 0 || inject < tty ||
		strings.Count(runMenu, "model.SetSoundPreview(") != 1 {
		return fmt.Errorf("Settings preview capability is not injected exactly once after TTY setup")
	}

	builder, err := sourceFunction(source, "buildMainMenuModel")
	if err != nil {
		return err
	}
	if strings.Contains(builder, "SetSoundPreview") {
		return fmt.Errorf("non-interactive main-menu builder received an audio capability")
	}
	return nil
}

func sourceFunction(source, name string) (string, error) {
	start := strings.Index(source, "func "+name+"(")
	if start < 0 {
		return "", fmt.Errorf("missing %s function", name)
	}
	rest := source[start+1:]
	if next := strings.Index(rest, "\nfunc "); next >= 0 {
		return source[start : start+1+next], nil
	}
	return source[start:], nil
}
