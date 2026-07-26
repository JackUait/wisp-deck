# Wisp Deck README Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the README's opening screenshots with the supplied Wisp Deck logo and restructure the document so benefits, verified facts, and setup appear before advanced reference material.

**Architecture:** This is a documentation-only change. A stable, repository-owned PNG becomes the README hero, while `README.md` remains the single self-contained guide and is reordered as a progressive introduction rather than split into additional guides.

**Tech Stack:** GitHub-flavored Markdown, inline HTML for centered image sizing, PNG, shell-based verification, existing Go test suite.

## Global Constraints

- Work directly on the existing `main` branch; never create or use another branch or worktree.
- Copy `/Users/jackuait/Downloads/Redesign Ghost Logo Jul 26.png` to `docs/wisp-deck-logo.png` without modifying the source file.
- Center the logo above the H1 at approximately 240 pixels wide and give it meaningful alt text.
- Remove the opening references to `docs/screenshot-selector.png` and `docs/screenshot-session.png`; leave both files untouched.
- Keep the README comprehensive and self-contained.
- Do not add badges, benchmarks, popularity claims, size claims, or unsupported platform claims.
- Preserve the verified requirements: macOS, Node.js 16+, and Ghostty.
- Preserve the verified AI tools: Claude Code, OpenCode, and Codex.
- Preserve all operational warnings and authentication constraints already documented.
- Do not modify `run-tests.sh`, `.gitignore`, or `VERSION`.
- Stage only files created or modified by this plan; unrelated working-tree changes belong to other sessions.

## File Structure

- Create `docs/wisp-deck-logo.png`: repository-owned copy of the supplied logo used by the README.
- Modify `README.md`: progressive project introduction, getting-started guide, and complete user reference.
- Keep `docs/screenshot-selector.png`, `docs/screenshot-session.png`, and `docs/screenshot.png` unchanged.

---

### Task 1: Add the logo and progressive README

**Files:**
- Create: `docs/wisp-deck-logo.png`
- Modify: `README.md:1-203`

**Interfaces:**
- Consumes: `/Users/jackuait/Downloads/Redesign Ghost Logo Jul 26.png` and the verified facts in the existing `README.md`, `package.json`, and project guidance.
- Produces: a repository-local logo path referenced by a self-contained GitHub README.

- [ ] **Step 1: Confirm the two deliverable paths are not carrying unrelated edits**

Run:

```bash
git status --short -- README.md docs/wisp-deck-logo.png
```

Expected: no output before implementation. If either path is already changed, inspect it and preserve work that belongs to another session instead of overwriting it.

- [ ] **Step 2: Copy and verify the supplied image asset**

Run:

```bash
cp '/Users/jackuait/Downloads/Redesign Ghost Logo Jul 26.png' docs/wisp-deck-logo.png
cmp '/Users/jackuait/Downloads/Redesign Ghost Logo Jul 26.png' docs/wisp-deck-logo.png
file docs/wisp-deck-logo.png
```

Expected: `cmp` exits 0 and `file` reports a PNG image with 1024 × 1024 dimensions.

- [ ] **Step 3: Rewrite the opening block**

Replace the current title, paragraph, and two-image block with this hierarchy and copy:

```markdown
<p align="center">
  <img src="docs/wisp-deck-logo.png" alt="Wisp Deck ghost mascot in a terminal window" width="240" />
</p>

# Wisp Deck

Wisp Deck turns one command into a ready-to-use AI coding workspace on macOS: your chosen assistant, a live view of your git changes, and a spare shell, all arranged in tmux. Unlike a bare AI CLI launcher, it manages the whole workspace—from project selection and account routing to session restore and process cleanup—so you can start working quickly and close the window without leaving tools behind.

- **One command.** Run `npx wisp-deck`; Wisp Deck installs the supporting command-line tools it needs.
- **Three focused panes.** Work with your AI assistant, inspect live git changes, and keep a spare terminal ready.
- **Three AI tools.** Choose Claude Code, OpenCode, or Codex during setup or from the selector.
- **Live changes.** Review files, open full diffs, preview images, and discard selected changes from the ledger.
- **Session restore.** Reopen projects and supported AI conversations after a reboot.
- **Clean shutdown.** Closing the window stops the processes Wisp Deck started.

```sh
npx wisp-deck

# First run: choose Claude Code, OpenCode, or Codex
# Then: add a project and press Enter
# Result: AI assistant + live changes + spare terminal
```
```

Follow it with a `## Contents` list linking to every H2 section. Keep the caution about closing the window close to the getting-started or workspace explanation.

- [ ] **Step 4: Reorder and tighten the complete guide**

Use this H2 order exactly:

```markdown
## Getting Started
## Your Workspace
## Project Selector
## Settings
## Claude Accounts and Subscriptions
## Stats
## Screenshots, Videos, and Files
## Status Line
## Hotkeys
## Restore Your Sessions
## Update Wisp Deck
## Credits
```

Apply these exact content rules while reusing verified details from the current README:

- `Getting Started`: list macOS, Node.js 16+, and Ghostty; explain that the command-line dependencies are installed automatically, Ghostty may require manual installation, and first run asks for an AI tool before projects are added with **A**.
- `Your Workspace`: describe the assistant, live changes ledger, and tabbed spare shell; retain the force-stop caution.
- `Project Selector`: keep the selector text example and all documented keys, including worktree actions.
- `Settings`: keep every current setting and when changes take effect.
- `Claude Accounts and Subscriptions`: keep multiple-logins, account pill, auto-switch, subscription manager, ChatGPT sign-in steps, non-metered authentication guarantee, logout behavior, and compatibility constraints. Use short paragraphs and lead-first lists instead of one dense block.
- `Stats`: keep cross-tool monthly usage, estimated cost language, both journal paths, cache behavior, and the local-disk limitation.
- `Screenshots, Videos, and Files`: keep Finder drag-and-drop and `Ctrl+b` then `i` behavior; do not claim the removed screenshots are displayed in the README.
- `Status Line`: keep the sample and every field explanation without changing model or quota claims.
- `Hotkeys`: preserve both current tables.
- `Restore Your Sessions`: keep the exact Claude Code/OpenCode/Codex restore guarantees and the Codex resume-selector fallback.
- `Update Wisp Deck`: retain `npx wisp-deck` as the update command.
- `Credits`: preserve the author, GitHub, Telegram, and `CREDITS.md` links.

Use horizontal rules between major layers, bold lead-ins for scanability, and callouts for must-not-miss constraints. Do not add unsupported claims while polishing prose.

- [ ] **Step 5: Verify the asset, required facts, removed screenshot references, and relative links**

Run:

```bash
test -f docs/wisp-deck-logo.png
grep -F 'docs/wisp-deck-logo.png' README.md
grep -F 'Node.js 16+' README.md
grep -F 'Claude Code, OpenCode, or Codex' README.md
! grep -F 'docs/screenshot-selector.png' README.md
! grep -F 'docs/screenshot-session.png' README.md
python3 - <<'PY'
from pathlib import Path
import re

readme = Path('README.md').read_text()
targets = re.findall(r'!?(?:\[[^]]*\])\(([^)]+)\)|<img[^>]+src="([^"]+)"', readme)
missing = []
for pair in targets:
    target = next(value for value in pair if value)
    if target.startswith(('http://', 'https://', '#', 'mailto:')):
        continue
    path = target.split('#', 1)[0]
    if path and not Path(path).exists():
        missing.append(path)
if missing:
    raise SystemExit(f'missing README targets: {missing}')
PY
git diff --check -- README.md docs/wisp-deck-logo.png
```

Expected: all commands exit 0; the logo line and required facts print; no missing target or whitespace error is reported.

- [ ] **Step 6: Review the README as a skimmer**

Read only the title, opening paragraph, bold bullet leads, example, and headings. Confirm they communicate, in order: what Wisp Deck is, why it helps, how it differs, how to run it, what it opens, and where advanced workflows are documented.

Then compare every number and compatibility statement against repository sources:

```bash
grep -nE '[0-9]+|macOS|Node\.js|Ghostty|Claude Code|OpenCode|Codex' README.md
```

Expected: every factual claim is supported by the existing README, `package.json`, or project guidance.

- [ ] **Step 7: Run repository verification**

No new automated test is added because this is a prose/image-only change with no behavior to drive through TDD. No shell script changes, so shellcheck is not applicable.

Run the mandatory full suite:

```bash
./run-tests.sh
```

Expected: the full suite passes.

- [ ] **Step 8: Commit only the README deliverables**

Run:

```bash
git add README.md docs/wisp-deck-logo.png docs/superpowers/plans/2026-07-26-readme-refresh.md
git diff --cached --check
git diff --cached --stat
git commit -m "docs: refresh README" -m "Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

Expected: the commit contains the README, logo, and implementation plan only; unrelated working-tree changes remain unstaged.

- [ ] **Step 9: Synchronize and push `main`**

First inspect the working tree. Do not stash, reset, or include unrelated changes:

```bash
git status --short
git fetch origin main
git merge-base --is-ancestor origin/main HEAD
git push origin main
git status -sb
```

Expected: the ancestor check exits 0, the push succeeds, and branch status reports `main` up to date with `origin/main`. If the ancestor check fails, stop rather than rebasing across unrelated uncommitted work.
