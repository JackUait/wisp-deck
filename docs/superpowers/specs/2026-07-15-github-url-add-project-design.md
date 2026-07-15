# Add project via GitHub URL — Design

Date: 2026-07-15

## Goal

When adding a project in the wisp-deck TUI, the user can paste a GitHub repo
link into the existing path field instead of a local path. Wisp-deck clones
the repo and registers it as a project.

## Accepted inputs

The path field detects a GitHub repo reference in any of these forms:

- `https://github.com/owner/repo` (optional `.git`, optional trailing `/`,
  optional extra segments like `/tree/main` which are ignored)
- `http://github.com/...`, `www.github.com/...`
- `github.com/owner/repo` (bare, no scheme)
- `git@github.com:owner/repo(.git)` (SSH form)

Anything else is treated as a local path exactly as today.

## Behavior

1. **Detection** — a pure helper `util.ParseGitHubRepo(input) (cloneURL,
   name string, ok bool)` recognizes the forms above. SSH input keeps the SSH
   clone URL (the user likely relies on SSH keys); all HTTP-ish/bare forms
   normalize to `https://github.com/owner/repo.git`. `name` is the repo name.
2. **Path step** — when the field holds a GitHub URL, `advanceToNameField`
   skips `util.ValidatePath` (a URL is not a directory) and path
   autocomplete is dismissed. The name auto-derives from the repo name via
   the existing `maybeAutoDeriveName` flow.
3. **Submit** — if the path value is a GitHub URL:
   - Destination = `<projects-root>/<name>` when a projects root is
     configured, else `~/<name>`.
   - If the destination directory already exists, or it is already a
     registered project, show an inline error (no clone).
   - Otherwise dispatch `git clone <cloneURL> <dest>` as a `tea.Cmd`
     (never inline — same rule as worktree add). The form stays open in a
     "cloning…" state; Enter is ignored while cloning.
   - On success: append `<name>:<dest>` to the projects file, reload the
     project list, exit input mode, show "Added <name>" feedback.
   - On failure: clear the cloning state and show the git error inline.
4. **Testability** — the clone call goes through an injectable
   `gitClone func(url, dest string) error` field on `MainMenuModel`
   (default: real `git clone`), so tests never touch the network.

## Out of scope

- Other hosts (GitLab, Bitbucket) — GitHub only, per the goal.
- Progress bars / clone output streaming — a static "cloning…" state is
  enough.
- Bash-side changes — the Go binary already writes the projects file and
  reloads in place.

## Testing

- Table-driven unit tests for `ParseGitHubRepo` (all accepted forms, plus
  rejections: plain paths, non-GitHub URLs, missing repo segment).
- `MainMenuModel` tests: URL skips dir validation and derives the name;
  submit dispatches a clone cmd to the right destination; clone success
  appends the project; clone failure surfaces the error; duplicate
  destination blocks the clone; Enter during cloning is a no-op.
