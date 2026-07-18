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
	if err := validateMainMenuSoundPreviewOwnership(
		filepath.Join(root, "cmd", "wisp-deck-tui", "main_menu.go"),
		menu,
	); err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(background), `"/usr/bin/afplay"`) != 1 {
		t.Fatal("background notifier must have exactly one locked afplay site")
	}
	for _, buildPath := range []string{
		filepath.Join(root, "Makefile"),
		filepath.Join(root, "scripts", "release.sh"),
	} {
		buildSource, err := os.ReadFile(buildPath)
		if err != nil {
			t.Fatal(err)
		}
		const capability = "-X main.SoundPreviewCapability=enabled"
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

func TestMainMenuSoundPreviewOwnershipGuardRejectsBypasses(t *testing.T) {
	root := projectRoot(t)
	path := filepath.Join(root, "cmd", "wisp-deck-tui", "main_menu.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	safe := string(data)
	if err := validateMainMenuSoundPreviewOwnership(path, data); err != nil {
		t.Fatalf("current preview adapter rejected: %v", err)
	}

	previewInjection := "\tif mainMenuSoundProcessAllowed(\n" +
		"\t\ttesting.Testing(),\n" +
		"\t\tSoundPreviewCapability,\n" +
		"\t) {\n" +
		"\t\tmodel.SetSoundPreview(mainMenuSoundPreview)\n" +
		"\t}\n"
	movedBeforeTTY := strings.Replace(
		safe,
		previewInjection,
		"",
		1,
	)
	movedBeforeTTY = strings.Replace(
		movedBeforeTTY,
		"\tttyOpts, cleanup, err := util.TUITeaOptions()\n",
		previewInjection+"\n"+
			"\tttyOpts, cleanup, err := util.TUITeaOptions()\n",
		1,
	)
	helperBuilderInjection := strings.Replace(
		safe,
		"\t\tmodel.SetSoundPreview(mainMenuSoundPreview)\n",
		"\t\twireMainMenuSoundPreview(model)\n",
		1,
	)
	helperBuilderInjection = strings.Replace(
		helperBuilderInjection,
		"func buildMainMenuModel() (*tui.MainMenuModel, error) {",
		"func wireMainMenuSoundPreview(model *tui.MainMenuModel) {\n"+
			"\tmodel.SetSoundPreview(mainMenuSoundPreview)\n"+
			"}\n\n"+
			"func buildMainMenuModel() (*tui.MainMenuModel, error) {",
		1,
	)
	helperBuilderInjection = strings.Replace(
		helperBuilderInjection,
		"\tmodel := tui.NewMainMenu(projects, aiTools, mainMenuAITool, mainMenuGhostDisplay)\n",
		"\tmodel := tui.NewMainMenu(projects, aiTools, mainMenuAITool, mainMenuGhostDisplay)\n"+
			"\twireMainMenuSoundPreview(model)\n",
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
			`return "/usr/bin/afplay", []string{`,
			`return "afplay", []string{`,
			1,
		),
		"eager callback": strings.Replace(
			safe,
			"\treturn func() tea.Msg {\n",
			"\t_ = runMainMenuSound(name)\n\treturn func() tea.Msg {\n",
			1,
		),
		"unwaited player": strings.Replace(
			safe,
			"exec.Command(executable, args...).Run()",
			"exec.Command(executable, args...).Start()",
			1,
		),
		"missing injection": strings.Replace(
			safe,
			previewInjection,
			"",
			1,
		),
		"unguarded injection": strings.Replace(
			safe,
			previewInjection,
			"\tmodel.SetSoundPreview(mainMenuSoundPreview)\n",
			1,
		),
		"injection before TTY": movedBeforeTTY,
		"builder has capability": strings.Replace(
			safe,
			"\tmodel := tui.NewMainMenu(projects, aiTools, mainMenuAITool, mainMenuGhostDisplay)\n",
			"\tmodel := tui.NewMainMenu(projects, aiTools, mainMenuAITool, mainMenuGhostDisplay)\n"+
				"\tmodel.SetSoundPreview(mainMenuSoundPreview)\n",
			1,
		),
		"builder helper has capability": helperBuilderInjection,
		"missing test boundary": strings.Replace(
			safe,
			"return !testBinary && capability == \"enabled\"",
			"return capability == \"enabled\"",
			1,
		),
		"comment-only test boundary": strings.Replace(
			safe,
			"return !testBinary && capability == \"enabled\"",
			"return capability == \"enabled\" // !testBinary",
			1,
		),
		"negated test boundary": strings.Replace(
			safe,
			"return !testBinary && capability == \"enabled\"",
			"return testBinary && capability == \"enabled\"",
			1,
		),
		"runner skips capability": strings.Replace(
			safe,
			"if !mainMenuSoundProcessAllowed(\n"+
				"\t\ttesting.Testing(),\n"+
				"\t\tSoundPreviewCapability,\n"+
				"\t) || run == nil {",
			"if run == nil {",
			1,
		),
		"ordinary build enables preview": strings.Replace(
			safe,
			`var SoundPreviewCapability = "disabled"`,
			`var SoundPreviewCapability = "enabled"`,
			1,
		),
		"runner parameter restored": strings.Replace(
			safe,
			"func mainMenuSoundPreview(name string) tea.Cmd {",
			"func mainMenuSoundPreview(run mainMenuSoundRunner, name string) tea.Cmd {",
			1,
		),
	}
	for name, source := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := validateMainMenuSoundPreviewOwnership(path, []byte(source)); err == nil {
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
		"preview command": {
			source: `package p; func test() { preview := mainMenuSoundPreview("Glass"); _ = preview }`,
		},
		"quoted production symbols": {
			source: `package p; const example = "exec.Command(\"/usr/bin/afplay\"); runMainMenuSound("`,
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
	aliases := map[string]string{}
	dotImports := map[string]bool{}
	for _, imported := range file.Imports {
		importPath, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}
		switch {
		case imported.Name == nil:
			slash := strings.LastIndex(importPath, "/")
			aliases[importPath[slash+1:]] = importPath
		case imported.Name.Name == ".":
			dotImports[importPath] = true
		case imported.Name.Name != "_":
			aliases[imported.Name.Name] = importPath
		}
	}
	staticStrings := collectStaticStrings(file)
	execCmdVariables := collectExecCmdVariables(file, aliases, dotImports)
	launchesAudio := false
	ast.Inspect(file, func(node ast.Node) bool {
		if name, ok := node.(*ast.Ident); ok && name.Name == "runMainMenuSound" {
			launchesAudio = true
			return false
		}
		composite, ok := node.(*ast.CompositeLit)
		if ok && execCmdLiteralLaunchesAudio(composite, aliases, staticStrings) {
			launchesAudio = true
			return false
		}
		assignment, ok := node.(*ast.AssignStmt)
		if ok && execCmdPathAssignmentLaunchesAudio(
			assignment,
			execCmdVariables,
			staticStrings,
		) {
			launchesAudio = true
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		executableIndex, ok := processExecutableArgument(call, aliases, dotImports)
		if !ok || executableIndex >= len(call.Args) {
			return true
		}
		executable := call.Args[executableIndex]
		if expressionContainsResolvedString(executable, "afplay", staticStrings) {
			launchesAudio = true
			return false
		}
		if isShellExecutable(executable, staticStrings) {
			for _, arg := range call.Args[executableIndex+1:] {
				if expressionContainsResolvedString(arg, "afplay", staticStrings) {
					launchesAudio = true
					return false
				}
			}
		}
		return true
	})
	return launchesAudio
}

func processExecutableArgument(
	call *ast.CallExpr,
	aliases map[string]string,
	dotImports map[string]bool,
) (int, bool) {
	importPath, function := calledPackageFunction(call.Fun, aliases, dotImports)
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
		if strings.HasSuffix(importPath, "/unix") {
			switch function {
			case "Exec", "ForkExec":
				return 0, true
			}
		}
	}
	return 0, false
}

func calledPackageFunction(
	function ast.Expr,
	aliases map[string]string,
	dotImports map[string]bool,
) (string, string) {
	switch function := function.(type) {
	case *ast.SelectorExpr:
		pkg, ok := function.X.(*ast.Ident)
		if !ok {
			return "", ""
		}
		return aliases[pkg.Name], function.Sel.Name
	case *ast.Ident:
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

func execCmdLiteralLaunchesAudio(
	literal *ast.CompositeLit,
	aliases map[string]string,
	staticStrings map[string]map[string]bool,
) bool {
	selected, ok := literal.Type.(*ast.SelectorExpr)
	if !ok || selected.Sel.Name != "Cmd" {
		return false
	}
	pkg, ok := selected.X.(*ast.Ident)
	if !ok || aliases[pkg.Name] != "os/exec" {
		return false
	}
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		value, valueOK := field.Value.(ast.Expr)
		if ok && valueOK && key.Name == "Path" &&
			expressionContainsResolvedString(value, "afplay", staticStrings) {
			return true
		}
	}
	return false
}

func execCmdPathAssignmentLaunchesAudio(
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
			expressionContainsResolvedString(
				assignment.Rhs[index],
				"afplay",
				staticStrings,
			) {
			return true
		}
	}
	return false
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
	command, err := requiredFunction(functions, "mainMenuSoundCommand")
	if err != nil {
		return err
	}
	if countCalls(command, "slices.Contains") != 1 ||
		!hasStringLiteral(command, "/usr/bin/afplay") ||
		!hasStringLiteral(command, "/System/Library/Sounds/") {
		return fmt.Errorf("settings preview command lost its allowlist or fixed host paths")
	}

	preview, err := requiredFunction(functions, "mainMenuSoundPreview")
	if err != nil {
		return err
	}
	deferred := returnedFunctionLiterals(preview)
	if !hasOneStringParameter(preview, "name") ||
		countCalls(preview, "mainMenuSoundCommand") != 1 ||
		len(deferred) != 1 ||
		countCalls(preview, "runMainMenuSound") != 1 ||
		countCalls(deferred[0], "runMainMenuSound") != 1 {
		return fmt.Errorf("settings preview is not one validated deferred command")
	}

	runner, err := requiredFunction(functions, "runMainMenuSound")
	if err != nil {
		return err
	}
	if countCalls(runner, "runMainMenuSoundWith") != 1 ||
		countWaitedExecCommands(runner) != 1 ||
		countWaitedExecCommands(file) != 1 {
		return fmt.Errorf("settings preview lost its sole waited process owner")
	}

	testSafeRunner, err := requiredFunction(functions, "runMainMenuSoundWith")
	if err != nil {
		return err
	}
	if len(testSafeRunner.Body.List) == 0 {
		return fmt.Errorf("settings preview runner has no test boundary")
	}
	firstGuard, ok := testSafeRunner.Body.List[0].(*ast.IfStmt)
	if !ok || !isProcessDeniedOrNilRunnerGuard(firstGuard.Cond) ||
		!blockReturnsNil(firstGuard.Body) {
		return fmt.Errorf("settings preview runner bypasses its test/build capability boundary")
	}
	if countCalls(testSafeRunner, "mainMenuSoundCommand") != 1 ||
		countCalls(testSafeRunner, "run") != 1 {
		return fmt.Errorf("settings preview runner bypasses its validated command")
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
	notTest, ok := and.X.(*ast.UnaryExpr)
	if !ok || notTest.Op != token.NOT {
		return false
	}
	testBinary, ok := notTest.X.(*ast.Ident)
	if !ok || testBinary.Name != "testBinary" {
		return false
	}
	enabled, ok := and.Y.(*ast.BinaryExpr)
	if !ok || enabled.Op != token.EQL {
		return false
	}
	capability, leftOK := enabled.X.(*ast.Ident)
	value, rightOK := enabled.Y.(*ast.BasicLit)
	if !leftOK || !rightOK || capability.Name != "capability" ||
		value.Kind != token.STRING {
		return false
	}
	unquoted, err := strconv.Unquote(value.Value)
	return err == nil && unquoted == "enabled"
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
		len(call.Args) != 2 || !isTestingCall(call.Args[0]) {
		return false
	}
	capability, ok := call.Args[1].(*ast.Ident)
	return ok && capability.Name == "SoundPreviewCapability"
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
