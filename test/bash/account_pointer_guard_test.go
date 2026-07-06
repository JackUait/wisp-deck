package bash_test

// Guard tests for the account-pointer invariant.
//
// The global pointer file (~/.config/wisp-deck/claude-account) is LAUNCH-TIME
// state only: it says which login the NEXT session should start under. What a
// RUNNING session's pane uses lives in its tmux session env
// (WISP_DECK_CLAUDE_ACCOUNT) and in the restore snapshot's account field. Any
// code that resolves the pointer inside a relaunch/restore/runtime path
// reintroduces the "account changed by itself" bug (fixed 2026-07-06,
// commits 69c617c + 89b9c1c): the pointer is shared mutable state, so another
// session's switch — or a reboot restore — silently flips this session's login.
//
// These tests hard-code every reviewed call site that is allowed to read the
// pointer. If one of them fails, a new pointer read was added somewhere:
//   - launching something NEW (menu, plain terminal, fresh session)? Add the
//     site to the allowlist below with a justification comment.
//   - relaunching/restoring/inspecting an EXISTING session? Do NOT read the
//     pointer. Use current_session_account (lib/account-switch.sh), the
//     explicit choice handed to relaunch_ai_pane, or the snapshot account via
//     resolve_restore_claude_account_dir (lib/claude-accounts.sh).

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// pointerSite identifies one reviewed occurrence group: `count` matches of
// `what` inside function `fn` ("" = top-level) of `file`.
type pointerSite struct {
	file  string
	fn    string
	what  string
	count int
}

func (s pointerSite) key() string {
	return fmt.Sprintf("%s\t%s\t%s", s.file, s.fn, s.what)
}

// guardedScripts returns every bash file that could plausibly grow a pointer
// read: the runtime wrapper, the installer entry point, and all lib modules.
func guardedScripts(t *testing.T) []string {
	t.Helper()
	root := projectRoot(t)
	files := []string{"wrapper.sh", "bin/wisp-deck"}
	for _, glob := range []string{"lib/*.sh", "lib/terminals/*.sh", "scripts/*.sh"} {
		matches, err := filepath.Glob(filepath.Join(root, glob))
		if err != nil {
			t.Fatalf("glob %s: %v", glob, err)
		}
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				t.Fatalf("rel %s: %v", m, err)
			}
			files = append(files, rel)
		}
	}
	return files
}

var (
	funcDefRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\(\) \{`)
	commentRe = regexp.MustCompile(`^\s*#`)
)

// scanPointerSites counts, per (file, enclosing function), how many times each
// pattern occurs on non-comment lines. Function definition lines themselves are
// skipped so defining a reader is not flagged as calling one.
func scanPointerSites(t *testing.T, patterns map[string]*regexp.Regexp) map[string]pointerSite {
	t.Helper()
	root := projectRoot(t)
	found := map[string]pointerSite{}
	for _, rel := range guardedScripts(t) {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		fn := ""
		for _, line := range strings.Split(string(data), "\n") {
			if m := funcDefRe.FindStringSubmatch(line); m != nil {
				fn = m[1]
				continue
			}
			if strings.HasPrefix(line, "}") {
				fn = ""
				continue
			}
			if commentRe.MatchString(line) {
				continue
			}
			for what, re := range patterns {
				n := len(re.FindAllString(line, -1))
				if n == 0 {
					continue
				}
				site := pointerSite{file: rel, fn: fn, what: what}
				if prev, ok := found[site.key()]; ok {
					site.count = prev.count + n
				} else {
					site.count = n
				}
				found[site.key()] = site
			}
		}
	}
	return found
}

// diffSites renders a stable, readable diff between found and allowed sites.
func diffSites(found map[string]pointerSite, allowed []pointerSite) []string {
	allowedMap := map[string]pointerSite{}
	for _, s := range allowed {
		allowedMap[s.key()] = s
	}
	var problems []string
	for k, f := range found {
		a, ok := allowedMap[k]
		loc := f.fn
		if loc == "" {
			loc = "(top-level)"
		}
		if !ok {
			problems = append(problems,
				fmt.Sprintf("NEW pointer read: %s in %s %s (x%d)", f.what, f.file, loc, f.count))
		} else if a.count != f.count {
			problems = append(problems,
				fmt.Sprintf("count changed: %s in %s %s: allowed %d, found %d", f.what, f.file, loc, a.count, f.count))
		}
	}
	for k, a := range allowedMap {
		if _, ok := found[k]; !ok {
			loc := a.fn
			if loc == "" {
				loc = "(top-level)"
			}
			problems = append(problems,
				fmt.Sprintf("stale allowlist entry (update this test): %s in %s %s", a.what, a.file, loc))
		}
	}
	sort.Strings(problems)
	return problems
}

const guardExplanation = `
The global claude-account pointer is LAUNCH-TIME state only. Resolving it in a
relaunch/restore/runtime path reintroduces the "account changed by itself" bug:
the pointer is rewritten by any session's switch and by the launcher, so a
running session must never derive its login from it. Use instead:
  - current_session_account <tmux> <pointer>   (lib/account-switch.sh) to ask
    which login THIS session runs,
  - the explicit choice arg of relaunch_ai_pane for mid-session switches,
  - resolve_restore_claude_account_dir with the snapshot/queue account field
    for restored sessions.
If the new read really is a launch-time decision for a NEW session, add it to
the allowlist in test/bash/account_pointer_guard_test.go with a justification.`

// TestAccountPointerGuard_reader_calls_are_allowlisted pins every call of the
// pointer-reading helpers to the reviewed set below.
func TestAccountPointerGuard_reader_calls_are_allowlisted(t *testing.T) {
	readers := []string{
		"get_active_claude_account",
		"get_active_claude_account_name",
		"resolve_claude_account_dir",
		"resolve_restore_claude_account_dir",
		"apply_plain_terminal_claude_account",
	}
	patterns := map[string]*regexp.Regexp{}
	for _, r := range readers {
		patterns[r] = regexp.MustCompile(`\b` + r + `\b`)
	}

	allowed := []pointerSite{
		// lib/claude-accounts.sh — the reader helpers compose each other.
		{"lib/claude-accounts.sh", "get_active_claude_account_name", "get_active_claude_account", 1},
		{"lib/claude-accounts.sh", "resolve_claude_account_dir", "get_active_claude_account", 1},
		// Pointer fallback ONLY for pre-account-field snapshots (acct == "").
		{"lib/claude-accounts.sh", "resolve_restore_claude_account_dir", "resolve_claude_account_dir", 1},
		{"lib/claude-accounts.sh", "apply_plain_terminal_claude_account", "resolve_claude_account_dir", 1},

		// lib/account-switch.sh — documented fallbacks, never the primary path.
		// Fallback ONLY for an UNSTAMPED session (pre-stamp launch); a stamped
		// empty value (Default) must not reach it.
		{"lib/account-switch.sh", "current_session_account", "get_active_claude_account", 1},
		// Legacy caller path without a tmux_cmd; pill callers pass tmux.
		{"lib/account-switch.sh", "account_current", "get_active_claude_account", 1},
		// Legacy path when no explicit choice is handed down (stale TUI binary
		// without the result-file contract).
		{"lib/account-switch.sh", "relaunch_ai_pane", "resolve_claude_account_dir", 1},
		// before/after pointer-diff, used ONLY when the binary lacks
		// --result-file (switcher_supports_session_flags said no).
		{"lib/account-switch.sh", "open_account_switcher", "get_active_claude_account", 2},

		// wrapper.sh — genuine launch-time decisions for a NEW session.
		{"wrapper.sh", "", "apply_plain_terminal_claude_account", 1}, // plain-terminal menu action
		{"wrapper.sh", "", "resolve_restore_claude_account_dir", 1},  // restore: snapshot account decides
		{"wrapper.sh", "", "resolve_claude_account_dir", 1},          // fresh launch: pointer decides
	}

	problems := diffSites(scanPointerSites(t, patterns), allowed)
	if len(problems) > 0 {
		t.Errorf("account-pointer reader calls drifted from the reviewed allowlist:\n  %s\n%s",
			strings.Join(problems, "\n  "), guardExplanation)
	}
}

// TestAccountPointerGuard_pointer_path_literals_are_allowlisted pins every
// place that spells out the pointer file's path (…/claude-account), so a new
// direct read (bypassing the helpers) is caught too. The regex rejects longer
// tokens (claude-accounts, claude-account-colors, claude-account-switch, …).
func TestAccountPointerGuard_pointer_path_literals_are_allowlisted(t *testing.T) {
	patterns := map[string]*regexp.Regexp{
		"claude-account path literal": regexp.MustCompile(`claude-account($|[^A-Za-z0-9_.-])`),
	}

	allowed := []pointerSite{
		// Serialized into the relaunch ctx file so the popup can WRITE the
		// pointer (steering future launches); the relaunch decision itself
		// travels via --result-file, not the pointer.
		{"lib/account-switch.sh", "write_relaunch_context", "claude-account path literal", 1},
		// Passed to the launcher menu binary, which shows/sets the login for
		// the NEXT session — launch-time by definition.
		{"lib/menu-tui.sh", "select_project_interactive", "claude-account path literal", 1},
		// The three launch-time resolution calls allowlisted in the reader
		// test above spell out the path as an argument.
		{"wrapper.sh", "", "claude-account path literal", 3},
	}

	problems := diffSites(scanPointerSites(t, patterns), allowed)
	if len(problems) > 0 {
		t.Errorf("pointer-file path literals drifted from the reviewed allowlist:\n  %s\n%s",
			strings.Join(problems, "\n  "), guardExplanation)
	}
}
