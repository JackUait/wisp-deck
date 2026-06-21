# Ghost Tab Test Plan

## Application Overview

Ghost Tab is a terminal and tmux wrapper application with no web-facing UI. It consists of two layers: a Go TUI binary (ghost-tab-tui) built with Bubbletea that drives interactive terminal menus, and a bash orchestration layer (bin/ghost-tab, lib/*.sh) that manages process lifecycle, config files, and tmux sessions. The application has no HTML pages, HTTP server, browser UI, REST API, or documentation site. There is therefore no surface area for Playwright browser automation in the conventional sense. This test plan documents the mismatch, describes every interactive TUI screen and bash-driven flow in the application, and provides a blueprint for the functional test scenarios that the existing Go and bash test suites should cover. If a web front-end is added to Ghost Tab in the future (for example a settings dashboard served over localhost, or a GitHub Pages documentation site), this plan can be re-activated and the steps below can be directly translated into Playwright tests.

## Test Scenarios

### 1. Applicability Assessment

**Seed:** `seed.spec.ts`

#### 1.1. Confirm no web UI is present

**File:** `tests/applicability/no-web-ui.spec.ts`

**Steps:**
  1. Verify that the repository contains no HTML, CSS, or client-side JavaScript files other than the Playwright seed stub
    - expect: A search of the repository for *.html, *.htm, *.css, *.jsx, *.tsx, *.vue files returns zero results
    - expect: The only TypeScript file present is the empty seed.spec.ts stub, which contains no test logic
  2. Verify that no HTTP server is started by any script or Go binary in the project
    - expect: Searching lib/*.sh, bin/ghost-tab, and wrapper.sh for 'http', 'serve', 'listen', ':8080', ':3000', 'localhost' returns no matches
    - expect: The ghost-tab-tui binary subcommands (main-menu, select-project, add-project, confirm, settings-menu, select-ai-tool, multi-select-ai-tool, select-terminal, select-branch, show-logo) all write to stdout and read from a TTY — they expose no TCP port
  3. Verify that no Playwright configuration file exists in the repository
    - expect: No playwright.config.ts or playwright.config.js file is present
    - expect: The package.json does not exist, confirming there is no Node.js dependency graph beyond the npx-invoked MCP server

### 2. Go TUI: Main Menu

**Seed:** `seed.spec.ts`

#### 2.1. Display project list on launch

**File:** `tests/main-menu/display-project-list.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui main-menu with a projects file containing two entries: 'my-app:/home/user/my-app' and 'web-service:/home/user/web'
    - expect: The terminal renders the Ghost Tab header with the animated ghost ASCII art
    - expect: Both project names 'my-app' and 'web-service' appear in the list
    - expect: The first project is highlighted as the selected item
    - expect: The action footer shows the key hints: A (add), D (delete), O (open once), P (plain terminal)
    - expect: The hint line shows: up/down navigate, Enter select
  2. Verify the ghost animation cycles between awake and sleeping states
    - expect: The ghost image changes between the open-eyed and closed-eyed variants on a timer
    - expect: The animation does not produce visual artifacts or break the layout

#### 2.2. Navigate projects with arrow keys

**File:** `tests/main-menu/keyboard-navigation.spec.ts`

**Steps:**
  1. With a project list containing three projects, press the Down arrow key twice
    - expect: After the first press the cursor moves to the second project
    - expect: After the second press the cursor moves to the third project
  2. Press the Up arrow key once
    - expect: The cursor returns to the second project
  3. Press the Down arrow key when the cursor is on the last project
    - expect: The cursor wraps around to the first project
  4. Press the Up arrow key when the cursor is on the first project
    - expect: The cursor wraps around to the last project

#### 2.3. Jump to project by number key

**File:** `tests/main-menu/number-key-jump.spec.ts`

**Steps:**
  1. With a list of five projects, press the '3' key
    - expect: The cursor jumps directly to the third project without pressing Enter
  2. Press the '1' key
    - expect: The cursor jumps to the first project
  3. Press a number higher than the project count, for example '9' when only five projects exist
    - expect: No crash occurs
    - expect: The cursor does not move to a non-existent position

#### 2.4. Select a project with Enter

**File:** `tests/main-menu/select-project-enter.spec.ts`

**Steps:**
  1. Navigate to the second project and press Enter
    - expect: The TUI exits
    - expect: The process writes JSON to stdout: {"action":"select","project":{"name":"web-service","path":"/home/user/web"}} (exact field names per MainMenuResult)
    - expect: The bash orchestration layer receives the JSON and launches the tmux session for that project

#### 2.5. Quit with Escape or Ctrl-C

**File:** `tests/main-menu/quit-escape.spec.ts`

**Steps:**
  1. Press Escape while the project list is visible
    - expect: The TUI exits
    - expect: JSON output contains action: 'quit'
    - expect: No tmux session is launched
  2. Press Ctrl-C while the project list is visible
    - expect: The TUI exits immediately
    - expect: JSON output contains action: 'quit'

#### 2.6. Cycle AI tool with Tab key

**File:** `tests/main-menu/cycle-ai-tool.spec.ts`

**Steps:**
  1. Launch the main menu with ai-tools set to 'claude,opencode' and current tool 'claude', then press Tab
    - expect: The ghost changes color from orange (Claude) to gray/silver (OpenCode)
    - expect: The displayed tool indicator updates to OpenCode
  2. Press Tab again
    - expect: The ghost wraps back to orange (Claude)
    - expect: The tool indicator updates to Claude
  4. Exit the menu and inspect the AI tool file
    - expect: The file at the path specified by --ai-tool-file has been updated to reflect the last-selected tool

#### 2.7. Empty project list shows no-projects state

**File:** `tests/main-menu/empty-project-list.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui main-menu with an empty projects file
    - expect: The list area shows no project entries
    - expect: The action footer is still visible with A (add) and other options
    - expect: No crash or panic occurs
  2. Press Enter on the empty list
    - expect: Nothing is selected
    - expect: The TUI remains open or exits gracefully depending on implementation

### 3. Go TUI: Add Project Form

**Seed:** `seed.spec.ts`

#### 3.1. Submit a valid project name and path

**File:** `tests/add-project/submit-valid-project.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui add-project
    - expect: The form renders with a 'Project Name:' field focused and a 'Project Path:' field below it
    - expect: The cursor is in the Project Name field
  2. Type 'my-new-project' into the Project Name field and press Enter
    - expect: The cursor moves to the Project Path field
    - expect: The name field is now blurred
  3. Type a valid existing directory path and press Enter
    - expect: The TUI exits
    - expect: JSON output: {"name":"my-new-project","path":"/expanded/path/to/dir","confirmed":true}
  4. Verify the path is expanded
    - expect: If the user typed '~/code/project', the output JSON contains the fully expanded absolute path (e.g., '/home/user/code/project')

#### 3.2. Reject empty project name

**File:** `tests/add-project/reject-empty-name.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui add-project, leave the name field empty, and press Enter
    - expect: The form does not advance to the path field
    - expect: An error message 'project name cannot be empty' is displayed in red
  2. Type a non-empty name and press Enter
    - expect: The form advances to the path field
    - expect: The error message disappears

#### 3.3. Reject empty project path

**File:** `tests/add-project/reject-empty-path.spec.ts`

**Steps:**
  1. Enter a valid project name and advance to the path field, then press Enter without typing a path
    - expect: The form does not submit
    - expect: An error message 'project path cannot be empty' is displayed
  2. Type a valid path and press Enter
    - expect: The form submits successfully

#### 3.4. Reject non-existent project path

**File:** `tests/add-project/reject-nonexistent-path.spec.ts`

**Steps:**
  1. Enter a valid project name, then type a path that does not exist on disk (e.g., '/does/not/exist/anywhere') and press Enter
    - expect: The TUI displays a path validation error
    - expect: The form does not exit
    - expect: JSON is not written

#### 3.5. Tab autocomplete for path field

**File:** `tests/add-project/tab-autocomplete.spec.ts`

**Steps:**
  1. Enter a project name, advance to the path field, and type the first few characters of a known directory
    - expect: A dropdown box appears below the path field listing matching subdirectories
    - expect: Each suggestion ends with a trailing slash
  2. Press the Down arrow key
    - expect: The second suggestion is highlighted
  3. Press Tab
    - expect: The path field is populated with the highlighted suggestion
    - expect: The dropdown updates to show subdirectories of the accepted path
  4. Press Escape
    - expect: The autocomplete dropdown closes
    - expect: The typed value remains in the path field
    - expect: The form remains open

#### 3.6. Cancel form with Escape or Ctrl-C

**File:** `tests/add-project/cancel-form.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui add-project and press Escape immediately
    - expect: The TUI exits
    - expect: JSON output: {"confirmed":false}
  2. Launch ghost-tab-tui add-project, type a partial name, then press Ctrl-C
    - expect: The TUI exits immediately
    - expect: JSON output: {"confirmed":false}

### 4. Go TUI: Confirm Dialog

**Seed:** `seed.spec.ts`

#### 4.1. Accept confirmation with Y key

**File:** `tests/confirm/accept-with-y.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui confirm 'Delete project my-app?'
    - expect: The dialog renders the message 'Delete project my-app?' in bold
    - expect: A '[y/n]' hint is shown below
  2. Press the 'y' key
    - expect: The TUI exits
    - expect: JSON output: {"confirmed":true}
  3. Verify uppercase Y also works
    - expect: Press 'Y' in a fresh invocation
    - expect: JSON output: {"confirmed":true}

#### 4.2. Reject confirmation with N key

**File:** `tests/confirm/reject-with-n.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui confirm 'Delete project my-app?' and press 'n'
    - expect: JSON output: {"confirmed":false}
  2. Press 'N' in a fresh invocation
    - expect: JSON output: {"confirmed":false}

#### 4.3. Cancel confirmation with Escape

**File:** `tests/confirm/cancel-with-escape.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui confirm 'Delete project my-app?' and press Escape
    - expect: JSON output: {"confirmed":false}
  2. Press Ctrl-C in a fresh invocation
    - expect: JSON output: {"confirmed":false}

### 5. Go TUI: Settings Menu

**Seed:** `seed.spec.ts`

#### 5.1. Display all settings options

**File:** `tests/settings-menu/display-options.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui settings-menu
    - expect: The menu renders with title 'Settings'
    - expect: Four items are listed: 'Add Project', 'Delete Project', 'Select AI Tool', 'Quit'
    - expect: Each item shows its description below its title

#### 5.2. Select Add Project

**File:** `tests/settings-menu/select-add-project.spec.ts`

**Steps:**
  1. Navigate to 'Add Project' in the settings menu and press Enter
    - expect: JSON output: {"action":"add-project"}

#### 5.3. Select Delete Project

**File:** `tests/settings-menu/select-delete-project.spec.ts`

**Steps:**
  1. Navigate to 'Delete Project' in the settings menu and press Enter
    - expect: JSON output: {"action":"delete-project"}

#### 5.4. Select AI Tool

**File:** `tests/settings-menu/select-ai-tool.spec.ts`

**Steps:**
  1. Navigate to 'Select AI Tool' in the settings menu and press Enter
    - expect: JSON output: {"action":"select-ai-tool"}

#### 5.5. Quit settings menu

**File:** `tests/settings-menu/quit-settings-menu.spec.ts`

**Steps:**
  1. Navigate to 'Quit' and press Enter
    - expect: JSON output: {"action":"quit"}
  2. Press Escape on any item in a fresh invocation
    - expect: JSON output: {"action":"quit"}

### 6. Go TUI: AI Tool Selector

**Seed:** `seed.spec.ts`

#### 6.1. Display installed and uninstalled tools

**File:** `tests/ai-selector/display-tools.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui select-ai-tool with a mix of installed and uninstalled tools
    - expect: All available tools are listed
    - expect: Installed tools can be selected
    - expect: Uninstalled tools are visible but selecting them does not confirm a selection
  2. Select an installed tool and press Enter
    - expect: JSON output: {"tool":"opencode","command":"npx opencode-ai@latest","selected":true}
  3. Press Escape
    - expect: JSON output: {"selected":false}

#### 6.2. Multi-select AI tools for installer

**File:** `tests/ai-selector/multi-select-tools.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui multi-select-ai-tool
    - expect: Claude is pre-checked because it is always required
    - expect: Other installed tools are pre-checked
    - expect: Uninstalled tools are unchecked
  2. Press Space to uncheck a currently checked tool
    - expect: The checkbox changes from [x] to [ ]
  3. Press Space again
    - expect: The checkbox returns to [x]
  4. Uncheck all tools and press Enter
    - expect: An error message 'Select at least one AI tool' is displayed
    - expect: The form does not submit
  5. Check at least one tool and press Enter
    - expect: JSON output: {"tools":["claude"],"confirmed":true}

### 7. Bash Layer: Project File Management

**Seed:** `seed.spec.ts`

#### 7.1. Load projects from file

**File:** `tests/bash-projects/load-projects.spec.ts`

**Steps:**
  1. Create a projects file with three name:path entries and one comment line starting with #, then source lib/projects.sh and call load_projects
    - expect: Three projects are returned
    - expect: The comment line is ignored
    - expect: Each project has the correct name and path
  2. Call load_projects with a projects file that contains blank lines
    - expect: Blank lines are skipped
    - expect: Only valid name:path entries are returned

#### 7.2. Add project via bash

**File:** `tests/bash-projects/add-project-bash.spec.ts`

**Steps:**
  1. Source lib/project-actions.sh and call add_project with a valid name and path against a temp projects file
    - expect: The new entry is appended to the file in name:path format
    - expect: The file ends with a newline
  2. Call add_project when the projects file does not yet exist
    - expect: The file is created
    - expect: The entry is written correctly

#### 7.3. Delete project via bash

**File:** `tests/bash-projects/delete-project-bash.spec.ts`

**Steps:**
  1. Call delete_project with an existing project line
    - expect: The exact line is removed from the file
    - expect: All other lines remain intact
  2. Call delete_project with a line that does not exist in the file
    - expect: The file is unchanged
    - expect: The function exits without error

### 8. Bash Layer: Process Cleanup

**Seed:** `seed.spec.ts`

#### 8.1. Recursively kill process tree on window close

**File:** `tests/bash-process/recursive-kill.spec.ts`

**Steps:**
  1. Set up a parent process that spawns a child and grandchild process, then invoke wrapper.sh cleanup logic
    - expect: All three processes (parent, child, grandchild) receive SIGTERM
    - expect: After the grace period all three receive SIGKILL
    - expect: No zombie processes remain in the process table
  2. Verify the tmux session is destroyed after cleanup
    - expect: tmux ls does not show the session after cleanup completes

#### 8.2. SIGHUP is ignored by main-menu process

**File:** `tests/bash-process/sighup-ignored.spec.ts`

**Steps:**
  1. Launch ghost-tab-tui main-menu and send SIGHUP to its PID
    - expect: The process does not terminate on SIGHUP
    - expect: The TUI remains fully interactive
    - expect: Bubbletea detects TTY EOF and exits gracefully when the terminal closes

### 9. Bash Layer: Status Line

**Seed:** `seed.spec.ts`

#### 9.1. Status line shows git branch and file counts

**File:** `tests/bash-statusline/git-info.spec.ts`

**Steps:**
  1. In a git repository with 2 staged, 1 unstaged, and 3 untracked files, run the status line script
    - expect: Output contains the repository name
    - expect: Output contains the current branch name
    - expect: Output contains S: 2
    - expect: Output contains U: 1
    - expect: Output contains A: 3
  2. Run the status line in a directory that is not a git repository
    - expect: The script does not crash
    - expect: Branch and file count fields are absent or shown as empty

#### 9.2. Context percentage is included

**File:** `tests/bash-statusline/context-percentage.spec.ts`

**Steps:**
  1. Run the status line script with a CLAUDE_CONTEXT_PERCENT environment variable set to 23.5
    - expect: Output contains '23.5%' or equivalent formatted percentage

### 10. Bash Layer: Configuration Files

**Seed:** `seed.spec.ts`

#### 10.1. Ghostty config is created when absent

**File:** `tests/bash-config/ghostty-config-created.spec.ts`

**Steps:**
  1. Run the Ghostty config setup function against a temp directory that contains no existing config
    - expect: A config file is created at the expected path
    - expect: The file contains the command line pointing to the wrapper script
    - expect: Key tab bindings are present in the file

#### 10.2. Ghostty config is merged when present

**File:** `tests/bash-config/ghostty-config-merged.spec.ts`

**Steps:**
  1. Place an existing Ghostty config file at the target path, then run the config setup with 'merge' option
    - expect: The existing config lines are preserved
    - expect: The ghost-tab-specific lines are appended or merged without duplication
  2. Run the config setup with 'replace' option
    - expect: The existing config is replaced entirely with the ghost-tab config

#### 10.3. Settings JSON is updated correctly

**File:** `tests/bash-config/settings-json.spec.ts`

**Steps:**
  1. Run settings-json.sh to set a key in an existing JSON file
    - expect: The key is updated in the file
    - expect: All other keys remain unchanged
    - expect: The file is valid JSON after the operation
  2. Run settings-json.sh against a file that does not exist
    - expect: The file is created with the single key-value pair
    - expect: The file is valid JSON
