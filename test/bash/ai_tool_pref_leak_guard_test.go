package bash_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Regression guard against the mid-session AI-tool leak.
//
// The launcher's ai-tool preference file (~/.config/wisp-deck/ai-tool) is what
// wrapper.sh reads to choose the AI tool for the NEXT launch and for OTHER
// sessions (wrapper.sh:44 for the splash hue, wrapper.sh:122 for
// SELECTED_AI_TOOL). It must change ONLY when the user explicitly picks a
// default in the launcher — a flow that lives in bin/wisp-deck (the `echo >
// ai-tool` at bin/wisp-deck:72) and the Go menu (menu-tui.sh hands the binary
// --ai-tool-file; the binary, not a shell redirect, writes it).
//
// relaunch_switch_tool once ALSO wrote this file on every mid-session tool
// switch, so a momentary in-session choice silently became every future
// session's default. TestRelaunchSwitchTool_does_not_touch_global_ai_tool_pref
// and TestOpenAccountSwitcher_tool_result_switches_agent pin the two known
// entry points behaviorally; this static guard pins the PROPERTY, so a brand
// new mid-session code path (a new function, a new module) that reintroduces
// the leak fails here too — naming the exact file and line.
//
// The property: no runtime lib/ module contains a shell output redirection
// (`>` or `>>`) whose target is the ai-tool preference. Legitimate writers do
// not use a redirect from lib/ (bin/wisp-deck does the redirect; the Go binary
// does the persistence for the menu), so the correct count of matches is zero.

// prefRedirect matches an output redirection ( > or >> ) whose target token
// references the ai-tool preference — as a path ending in `ai-tool`, or via the
// module's handle for it (`_rc_tool_pref`) or the launcher's env var
// (`AI_TOOL_PREF*`). The target character class deliberately excludes spaces
// and `;`, so `foo >/dev/null; bar --ai-tool-file x` cannot match: only a
// redirect landing DIRECTLY on the preference is a leak.
var prefRedirect = regexp.MustCompile(`>>?\s*"?(\$\{?)?[A-Za-z0-9_./{}$-]*?(ai-tool|_rc_tool_pref|AI_TOOL_PREF)`)

// stripComment removes a trailing shell comment so an inline `# ... ai-tool ...`
// note can't trip the guard, while keeping code before a `#` that appears
// inside a quoted string intact enough for this coarse check. A line whose
// trimmed form starts with `#` is dropped entirely by the caller.
func codePortion(line string) string {
	// Only strip a `#` that is preceded by whitespace or is at column 0 — this
	// avoids clobbering things like `${x#foo}` while dropping real comments.
	for i := 0; i < len(line); i++ {
		if line[i] != '#' {
			continue
		}
		if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
			return line[:i]
		}
	}
	return line
}

func TestNoLibModuleWritesTheAiToolPreference(t *testing.T) {
	libDir := filepath.Join(projectRoot(t), "lib")
	var offenders []string

	err := filepath.WalkDir(libDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sh") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(projectRoot(t), path)
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if prefRedirect.MatchString(codePortion(line)) {
				offenders = append(offenders,
					rel+":"+itoa1(i+1)+"  "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk lib: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("a lib/ module writes the launcher's ai-tool preference — a "+
			"mid-session change to that file leaks this session's AI-tool choice "+
			"into every future/other session. The preference may only be set by "+
			"the explicit launcher menu (bin/wisp-deck / the Go binary via "+
			"--ai-tool-file), never from a runtime module:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// The other write vector: the ai-tool preference is also persisted by the Go
// binary when a caller hands it --ai-tool-file. That is how the LAUNCHER MENU
// saves the user's explicit default (menu-tui.sh) — legitimate. A mid-session
// module must never adopt the same trick to persist an in-session switch, so
// --ai-tool-file may appear in exactly one lib/ module: menu-tui.sh. The
// redirect guard above cannot see this vector (no `>` involved), so pin it
// here.
func TestAiToolFileFlagIsConfinedToTheLauncherMenu(t *testing.T) {
	libDir := filepath.Join(projectRoot(t), "lib")
	var offenders []string

	err := filepath.WalkDir(libDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sh") {
			return nil
		}
		if filepath.Base(path) == "menu-tui.sh" {
			return nil // the one legitimate persister of the launcher default
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(projectRoot(t), path)
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.Contains(codePortion(line), "--ai-tool-file") {
				offenders = append(offenders,
					rel+":"+itoa1(i+1)+"  "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk lib: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("a lib/ module other than menu-tui.sh passes --ai-tool-file to "+
			"the Go binary, which persists the launcher's ai-tool preference. Only "+
			"the launcher menu may set that default; persisting it from a "+
			"mid-session flow leaks this session's AI-tool choice into every "+
			"future/other session:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// itoa1 renders a positive line number (helpers_test has no int->string helper
// exported for this file; keep it local and trivial).
func itoa1(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
