# CLAUDE.md

Project guidance for Claude Code (claude.ai/code) working with this repository.

---

## IMMEDIATE COMPLETION CHECKLIST

**STOP! Before saying "done" or "complete", verify ALL of the following:**

### For ANY Code Change (No Exceptions)

```
[ ] 1. Did I write tests FIRST, watch them FAIL, THEN write code? (IRON RULE)
[ ] 2. Did I run shellcheck on modified scripts? (MANDATORY)
[ ] 3. Did I run final verification with full test suite? (MANDATORY)
[ ] 4. Did I `git push` successfully? (Work NOT complete until push succeeds)
```

**If ANY box is unchecked:** Work is NOT complete. Do it NOW.

**No rationalizations:**
- "Chat is too long, instructions are far down" → INVALID. You're reading them right now.
- "User is in a hurry" → INVALID. Half-done work wastes MORE time later.
- "It's just a small change" → INVALID. Small changes break things too.
- "I'll do it in next session" → INVALID. That leaves work stranded.
- "Tests already cover it" → INVALID. Write test FIRST, watch it FAIL.

### For Session End

```
[ ] 1. All code tested (test first → fail → code → pass)
[ ] 2. shellcheck run on modified scripts
[ ] 3. Full test suite run and passing
[ ] 4. `git push` succeeded
[ ] 5. Issues updated/closed
[ ] 6. `git status` shows "up to date with origin"
```

**Work is DEFINITELY NOT complete if:**
- Changes exist only locally (not pushed)
- shellcheck was never run
- Tests were skipped
- No test was written before code

### Bug Fix IRON RULE

```
[ ] 1. Write regression test FIRST
[ ] 2. Run test → watch it FAIL (proves bug exists)
[ ] 3. Fix bug
[ ] 4. Run test → watch it PASS
[ ] 5. Re-run full test suite
```

**Write code before test?** Delete it. Start over.

### Session End Commands

Run full verification:

```bash
# Run shellcheck on all modified scripts
find lib bin -name '*.sh' -exec shellcheck {} + && shellcheck wrapper.sh

# Run full test suite
./run-tests.sh

# Push changes
git pull --rebase
git push
git status  # MUST show "up to date with origin"
```

**This checklist is ALWAYS executed. NO MATTER how long the chat is.**

### Red Flags - You're About to Violate The Rules

If you catch yourself thinking ANY of these, STOP and DO THE CHECKLIST:

- "Chat is too long, I can't find the instructions"
- "User is in a hurry, I'll skip verification this time"
- "It's just a small change, doesn't need full process"
- "I'll do shellcheck in the next session"
- "Tests already exist, I don't need to write one first"
- "I already manually verified it works"
- "The push can wait, user can do it"
- "Full test suite takes too long"

**ALL of these mean: You're rationalizing. Run the checklist NOW.**

---

## Landing the Plane (Session Completion)

**⚠️ CRITICAL: The completion checklist at the TOP of this file MUST be followed.**

Scroll up to "IMMEDIATE COMPLETION CHECKLIST" and verify ALL items before declaring work done.

**If you're reading this section instead of the checklist:** Go to the TOP of the file.

**Summary (detail is at top):**
1. File issues for remaining work
2. Run quality gates (shellcheck, tests)
3. Full test suite verification
4. Update issue status
5. **PUSH TO REMOTE** (MANDATORY)
6. Clean up
7. Verify `git status` shows "up to date with origin"
8. Hand off context

**Remember:** Every code change needs shellcheck → tests → push. No exceptions.

## Project Overview

Ghost Tab is a terminal + tmux wrapper that launches a three-pane dev session with AI coding tools (Claude Code, Codex CLI, OpenCode), lazygit, and a spare terminal. It supports multiple terminal emulators (Ghostty, iTerm2, WezTerm, kitty) and handles complete process cleanup when windows close (no zombie processes).

**Key Features:**
- Interactive project selector with TUI
- Multi-AI tool support (Claude Code, Codex CLI, OpenCode)
- Custom status lines showing git info and context usage
- Sound notifications on AI idle
- Auto-cleanup of entire process trees

## Commands

```bash
./run-tests.sh                          # Run full Go test suite
go test ./test/bash/... -run TestFoo    # Run specific test group
go test ./test/bash/... -run "test_name" # Filter by name
go test ./... -v -count=1              # Verbose with no caching
shellcheck lib/*.sh lib/terminals/*.sh bin/ghost-tab wrapper.sh  # Lint all scripts
./bin/ghost-tab                         # Run main installer/setup
make release                            # Create a new release
```

### Creating Releases

**EVERY release MUST include fresh `ghost-tab-tui` binaries. NO EXCEPTIONS.**
- The installer downloads binaries from release assets — missing binaries = 404 on install
- The developer's local binary MUST be rebuilt — stale local binary = developer sees old UI
- **NEVER create releases manually via GitHub UI or bare `gh release create`**
- **ALWAYS use `make release`** — it handles binaries (GitHub + local), tagging, and release creation

Run `make release` to automate the full release process. Before running:

1. Bump the version in `VERSION` (semver format: `X.Y.Z`)
2. Commit and push all changes — working tree must be clean
3. Must be on `main` branch
4. `gh` CLI must be installed and authenticated (`brew install gh && gh auth login`)

The script will:
- Run preflight checks (clean tree, main branch, valid version, tag doesn't exist, gh auth)
- Show a confirmation prompt (skip with `--yes` flag)
- Build `ghost-tab-tui` binaries for darwin/arm64 and darwin/amd64
- Create annotated git tag `vX.Y.Z` and push
- Create a GitHub release with binaries attached as assets
- Rebuild the local `~/.local/bin/ghost-tab-tui` binary so the developer sees changes immediately

```bash
make release              # Interactive (with confirmation prompt)
bash scripts/release.sh --yes  # Non-interactive (skip confirmation)
```

**Gotcha:** `gh release create FILE#LABEL` uses the file's **basename** as the download name (not the label). If you build to a mktemp path, users get assets named `tmp.XXXX`. The release script builds to a temp directory with correct filenames to avoid this.

**Post-release verification (MANDATORY):**
```bash
# Verify binaries are downloadable (users get 404 if this fails)
gh release view v$(cat VERSION) --json assets --jq '.assets[].name'
# Must show: ghost-tab-tui-darwin-arm64, ghost-tab-tui-darwin-amd64
```

## Architecture

Ghost Tab uses a **hybrid architecture** combining Go for interactive TUI and bash for orchestration.

**Layer 1: Go TUI Binary (`ghost-tab-tui`)**
- Interactive terminal UI components built with Bubbletea
- Project selector, AI tool selector, terminal selector, settings menu, input forms
- Outputs structured JSON for bash consumption
- Binary: `~/.local/bin/ghost-tab-tui`
- Source: `cmd/ghost-tab-tui/`, `internal/tui/`, `internal/models/`, `internal/util/`

**Layer 2: Bash Orchestration**

**Entry Points:**
- `bin/ghost-tab` - Main installer script, sources all modules
- `wrapper.sh` - Terminal-agnostic runtime wrapper

**Module System:**
All reusable functionality lives in `lib/` as sourced shell scripts:

- **tui.sh**: Terminal UI helpers (header, success, error, info, warn)
- **install.sh**: Package installation (Homebrew, casks, commands)
- **ai-tools.sh**: AI tool detection and management
- **ai-select-tui.sh**: Interactive AI tool selection wrapper (Go TUI)
- **projects.sh**: Project file parsing and validation
- **project-actions.sh**: Add/delete project operations
- **project-actions-tui.sh**: Interactive project input wrapper (Go TUI)
- **menu-tui.sh**: Project selection wrapper (Go TUI)
- **settings-menu-tui.sh**: Settings menu wrapper (Go TUI)
- **terminal-select-tui.sh**: Interactive terminal selection wrapper (Go TUI)
- **terminals/registry.sh**: Terminal list, display names, preference management
- **terminals/adapter.sh**: Dynamic terminal adapter loader
- **terminals/ghostty.sh**: Ghostty terminal adapter
- **terminals/kitty.sh**: kitty terminal adapter
- **terminals/wezterm.sh**: WezTerm terminal adapter
- **terminals/iterm2.sh**: iTerm2 terminal adapter (Dynamic Profiles)
- **tmux-session.sh**: tmux session creation and pane setup
- **process.sh**: Process tree management and cleanup
- **statusline.sh**: Status line generation logic
- **statusline-setup.sh**: Claude Code status line installation
- **notification-setup.sh**: Sound notification setup
- **settings-json.sh**: JSON manipulation for settings files
- **input.sh**: User input helpers with validation
- **update.sh**: Self-update functionality

**Data Files:**
- `~/.config/ghost-tab/projects` - Project list (name:path format)
- `~/.config/ghost-tab/ai-tool` - Default AI tool preference
- `~/.config/ghost-tab/terminal` - Selected terminal preference
- `~/.config/ghost-tab/*-features.json` - AI tool feature flags
- `~/.claude/settings.json` - Claude Code settings
- `~/.codex/config.toml` - Codex CLI config
- `~/.config/ghostty/config` - Ghostty terminal config

**Process Hierarchy:**
```
Terminal window (Ghostty/iTerm2/WezTerm/kitty)
└─ wrapper.sh (shell command)
   └─ tmux session
      ├─ AI tool (Claude Code/Codex/etc)
      ├─ lazygit
      └─ spare shell
```

On window close, wrapper recursively kills entire process tree.

## Code Conventions

### Avoid Over-Engineering
- Don't add features beyond what's asked
- Don't create helpers for one-time operations
- Three similar lines > premature abstraction
- Only comment where logic isn't self-evident

### Shell Scripting Best Practices

**Strict Mode:**
```bash
set -e  # Exit on error (use in scripts)
set -u  # Exit on undefined variable (optional, use carefully)
set -o pipefail  # Pipe failures propagate (optional)
```

**Quoting:**
```bash
# ✅ CORRECT - Always quote variables
"$var"
"${array[@]}"
mkdir -p "$dir/subdir"

# ❌ WRONG - Unquoted (word splitting, glob expansion)
$var
${array[@]}
mkdir -p $dir/subdir
```

**Command Substitution:**
```bash
# ✅ CORRECT - Use $() for nesting and readability
result="$(command)"
outer="$(inner "$(innermost)")"

# ❌ WRONG - Backticks are legacy
result=`command`
```

**Conditionals:**
```bash
# ✅ CORRECT - Use [[ ]] for advanced features
if [[ "$var" == "value" ]]; then
  # Supports &&, ||, =~, <, >
  # No word splitting inside [[ ]]
fi

# ✅ CORRECT - Use [ ] for POSIX compatibility
if [ "$var" = "value" ]; then
  # More portable
fi

# ❌ WRONG - Don't use `test` command directly
if test "$var" = "value"; then
  # Verbose, no benefit
fi
```

**Error Handling:**
```bash
# ✅ CORRECT - Check command success
if command_that_might_fail; then
  success "Operation completed"
else
  error "Operation failed"
  return 1
fi

# ✅ CORRECT - Use || for fallback
result="$(brew --prefix 2>/dev/null || echo "/usr/local")"

# ❌ WRONG - Ignoring errors
command_that_might_fail  # What if it fails?
```

**shellcheck Compliance:**
- **ALWAYS** run `shellcheck` before committing
- Fix ALL warnings (SC1091 source directive is OK if verified)
- Use `# shellcheck disable=SCXXXX` ONLY when necessary with comment explaining why

**File Operations:**
```bash
# ✅ CORRECT - Check file existence
if [ -f "$file" ]; then
  # File exists and is regular file
fi

if [ -d "$dir" ]; then
  # Directory exists
fi

# ✅ CORRECT - Safe file reading
while IFS=: read -r name path; do
  echo "$name -> $path"
done < "$projects_file"

# ❌ WRONG - Cat abuse (useless use of cat)
cat file | grep pattern  # Use: grep pattern file
```

**Functions:**
```bash
# ✅ CORRECT - Clear function declarations
function_name() {
  local var1="$1"
  local var2="$2"

  # Always use local for function variables
  # Return 0 for success, non-zero for failure
  return 0
}

# ❌ WRONG - Global variables in functions
bad_function() {
  result="$1"  # Pollutes global scope
}
```

**Array Handling:**
```bash
# ✅ CORRECT - Proper array operations
array=("item1" "item2" "item3")
echo "${array[0]}"  # First element
echo "${array[@]}"  # All elements
echo "${#array[@]}"  # Length

# Iterate over array
for item in "${array[@]}"; do
  echo "$item"
done

# ❌ WRONG - Word splitting
for item in ${array[@]}; do  # Missing quotes
  echo "$item"
done
```

### Go Code Conventions

**Project Structure:**
```
cmd/ghost-tab-tui/     # CLI entry point and subcommands
internal/tui/          # Bubbletea UI components
internal/models/       # Data types (Project, Config)
internal/util/         # Utilities (path, JSON)
```

**Testing:**
- Unit tests alongside implementation: `*_test.go`
- Run with: `go test ./...`
- Mock external dependencies in tests

**Bubbletea Patterns:**
- Each TUI component implements tea.Model interface
- Init() for initialization
- Update() for message handling
- View() for rendering

**JSON Output:**
- All subcommands output JSON to stdout
- Errors go to stderr
- Use util.OutputJSON() helper for consistency

### Project-Specific Patterns

**TUI Output:**
```bash
# Use standardized TUI functions from tui.sh
header "Section Title"
success "Operation succeeded"
error "Something failed"
info "FYI message"
warn "Warning message"
```

**Configuration Files:**
```bash
# Read project file (name:path format)
while IFS=: read -r name path; do
  [[ "$name" =~ ^#.*$ ]] && continue  # Skip comments
  [[ -z "$name" ]] && continue  # Skip empty
  # Process $name and $path
done < "$PROJECTS_FILE"
```

**AI Tool Integration:**
```bash
# Check if command exists
if command -v claude &>/dev/null; then
  # claude is available
fi

# Install with verification
ensure_command "claude" \
  "curl -fsSL https://claude.ai/install.sh | bash" \
  "Run 'claude' to authenticate" \
  "Claude Code"
```

**Process Management:**
```bash
# Get process tree recursively
get_process_tree() {
  local pid="$1"
  local children
  children=$(pgrep -P "$pid" 2>/dev/null || true)

  echo "$pid"
  for child in $children; do
    get_process_tree "$child"
  done
}

# Kill with grace period then force
kill -TERM "$pid" 2>/dev/null || true
sleep 0.5
kill -KILL "$pid" 2>/dev/null || true
```

## Testing

### IRON RULE: No Code Without Tests

**⚠️ This is also in the completion checklist at the TOP of this file.**

**ALL code changes require behavior tests.**

**Bug fixes MUST follow this exact order:**
1. Write regression test
2. Run it → watch it FAIL (proves bug exists)
3. Fix the bug
4. Run it → watch it PASS
5. Only THEN is the fix complete

**No exceptions. No "I'll test later". No "it's obvious".**

Write test first. If you write code before test, delete it and start over.

**See "IMMEDIATE COMPLETION CHECKLIST" at TOP of file for the full workflow.**

### Commands
```bash
./run-tests.sh                               # Full suite
go test ./test/bash/... -run TestFoo -v       # Single test group
go test ./test/bash/... -run "test_name" -v   # Filter by name
```

### Go Test Structure

**Test Files:**
- Go unit tests: `internal/**/*_test.go`, `test/internal/**/*_test.go`
- Bash integration tests: `test/bash/*_test.go` (call bash functions via `os/exec`)

**Bash Integration Test (test/bash/):**
```go
package bash_test

func TestLoadProjects_reads_name_path_lines(t *testing.T) {
    dir := t.TempDir()
    writeTempFile(t, dir, "projects", "app:/path/to/app\nweb:~/code/web\n")
    out, code := runBashFunc(t, "lib/projects.sh", "load_projects",
        []string{filepath.Join(dir, "projects")}, nil)
    assertExitCode(t, code, 0)
    assertContains(t, out, "app:/path/to/app")
}
```

**Shared helpers** in `test/bash/helpers_test.go`:
- `runBashFunc(t, module, funcName, args, env)` — source module, call function
- `runBashFuncWithStdin(t, module, funcName, args, env, stdin)` — with stdin
- `runBashSnippet(t, script, env)` — run arbitrary bash
- `runBashScript(t, scriptPath, args, env)` — run script directly
- `mockCommand(t, dir, name, body)` — create mock executable in dir/bin/
- `writeTempFile(t, dir, name, content)` — create temp file
- `buildEnv(t, mockDirs, extra...)` — build env with PATH prepended
- `assertContains/assertNotContains/assertExitCode` — assertion helpers

**Critical Rules:**

**Setup/Cleanup:**
- Use `t.TempDir()` for auto-cleaned temp directories
- Use `t.Cleanup()` for deferred cleanup
- Use `t.Setenv()` for environment variable isolation

**Mocking External Commands:**
```go
// Create mock brew that reports "already installed"
dir := t.TempDir()
binDir := mockCommand(t, dir, "brew", `echo "already installed"`)
env := buildEnv(t, []string{binDir})
out, code := runBashFunc(t, "lib/install.sh", "ensure_brew_pkg",
    []string{"pkg"}, env)
```

**Table-Driven Tests:**
```go
func TestParseEscSequence(t *testing.T) {
    tests := []struct {
        name  string
        stdin string
        want  string
    }{
        {"up arrow", "[A", "A"},
        {"down arrow", "[B", "B"},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            out, _ := runBashFuncWithStdin(t, "lib/input.sh",
                "parse_esc_sequence", nil, nil, tt.stdin)
            if strings.TrimSpace(out) != tt.want {
                t.Errorf("got %q, want %q", out, tt.want)
            }
        })
    }
}
```

### What to Test vs Not

**DO Test:**
- Public function contracts
- User-facing behavior
- File operations (create, modify, delete)
- Error conditions (bad input, missing files)
- Integration between modules
- Edge cases (empty input, special chars)

**DO NOT Test:**
- Private helper functions (unless complex)
- Third-party commands (brew, tmux, etc)
- Obvious shell behavior
- Visual formatting (unless critical)

### Common Pitfalls

| Pitfall | Solution |
|---------|----------|
| Temp files leak between tests | Use `t.TempDir()` for auto-cleanup |
| Tests depend on order | Each test should be independent |
| Missing assertions | Every test needs assertion checks |
| Not testing error paths | Test both success and failure |
| Assuming clean environment | Set up everything in test setup |
| Not mocking external commands | Use `mockCommand` + `buildEnv` for PATH isolation |
| Hardcoded HOME paths | Use `t.TempDir()` and env overrides |

### Red Flags - You're About to Violate The Rules

**⚠️ More red flags are in the completion checklist at the TOP of this file.**

If you catch yourself thinking ANY of these, STOP:
- "This is too simple to test"
- "I'll test it after"
- "Tests would just duplicate the code"
- "It's about the spirit, not the letter"
- "This case is different"
- "I already verified it manually"

**These thoughts mean you're rationalizing. Write the test first.**

## Configuration

**DO NOT modify** without explicit request: `run-tests.sh`, `.gitignore`, `VERSION`

## Important Patterns

1. **Modularity**: Each `lib/*.sh` file is independently sourceable
2. **Error Propagation**: Use `set -e` and proper exit codes
3. **User Feedback**: Consistent TUI output (header/success/error/info/warn)
4. **Graceful Degradation**: Detect and adapt to missing optional features
5. **Process Cleanup**: Recursive tree killing with grace period
6. **Config Management**: Support both merge and replace for existing configs
7. **Cross-Shell Compatibility**: Source user's profile (bash/zsh) for environment
8. **Symlink Management**: Use `ln -sf` for idempotent linking
9. **Path Expansion**: Always expand `~` to `$HOME` for validation
10. **Sound Notification**: Pluggable hook system for AI idle events

## JSON Interface Schemas

### select-project
```json
{"name": "ghost-tab", "path": "/path/to/ghost-tab", "selected": true}
{"selected": false}  // Cancelled
```

### select-ai-tool
```json
{"tool": "claude", "command": "claude", "selected": true}
{"selected": false}  // Cancelled
```

### add-project
```json
{"name": "new-project", "path": "/path/to/project", "confirmed": true}
{"confirmed": false}  // Cancelled
```

### confirm
```json
{"confirmed": true}
{"confirmed": false}
```

### settings-menu
```json
{"action": "toggle-ghost"}
{"action": "quit"}
```
