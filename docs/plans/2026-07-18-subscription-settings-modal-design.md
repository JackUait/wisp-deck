# Subscription Settings Modal Design

**Date:** 2026-07-18
**Status:** Approved through delegated product/design authority

## Goal

Replace the Settings tab's one-profile-at-a-time subscription control and
appended model-mapping panel with one polished overlay modal that:

- shows every subscription profile at once;
- identifies each profile's provider, active state, and readiness;
- exposes the settings relevant to that provider;
- selects, adds, renames, and deletes profiles in one place; and
- works equally well with keyboard and mouse input.

The modal manages Standard Claude, bundled provider profiles, and user-created
profile variants. It does not manage native Claude login accounts; the existing
Account modal remains responsible for those.

## Current Problems

The Settings tab currently renders a `Subscription` row whose left/right keys
cycle profiles immediately. Enter only opens settings for the active custom
profile, and those settings are appended below the main menu rather than
composited as a modal. This creates several problems:

- users cannot see the available profiles together;
- provider identity and authentication readiness are mostly hidden;
- selection, model mapping, API-key editing, and profile lifecycle use separate
  interfaces;
- an appended panel can push content past the terminal instead of preserving
  the surrounding context; and
- the click behavior differs depending on whether Standard or a custom profile
  is active.

## Considered Approaches

### 1. Two-pane overlay modal — selected

Keep the complete profile inventory visible on the left and show the focused
profile's details and settings on the right. This provides the best overview,
minimizes navigation, and makes provider differences legible.

### 2. Single-column wizard

Show the profile list, then drill into a separate details step. This is easier
to render in a very narrow terminal but hides the inventory while editing and
requires more back-and-forth navigation.

### 3. Separate list and settings popups

Add a profile list popup while retaining the existing model-map popup. This
would require the least code movement, but it would preserve the fragmented
workflow the redesign is intended to remove.

The selected design uses the two-pane layout at normal terminal widths and a
single-pane drill-in fallback only below 64 columns.

## Entry Points

The Settings tab's `Subscription` row becomes a manage action:

- Enter or click opens the modal.
- Its footer reads `⏎ manage`; it no longer advertises left/right cycling.
- Left/right on this Settings row does nothing, preventing accidental profile
  changes while users are trying to inspect settings.

The header's compact `PLAN` switcher remains a fast path:

- Left/right continues to cycle ready profiles.
- Enter opens the same modal, focused on the active profile.

Every entry point therefore reaches one authoritative management surface
without removing the existing quick switcher.

## Modal Composition

The modal floats over a faint-dimmed copy of the Settings screen using the same
overlay compositor as the About and folder-browser cards. It is centered with
at least two cells of terminal margin.

At 64 columns and wider, the body has two panes:

```text
╭─ Subscriptions ─────────────────────────────────────────────────────╮
│ PROFILES                    │ OPENAI GPT                             │
│ ▌ Standard Claude   Ready  │ Provider       OpenAI / ChatGPT        │
│   Zhipu GLM        Needs key│ Authentication codex login             │
│   Xiaomi MiMo      Ready   │ Endpoint       Local Codex bridge      │
│ ● OpenAI GPT       Ready   │                                       │
│   Work GPT         Ready   │ MODEL ROUTING                         │
│                            │ Opus   → gpt-5.6-sol                   │
│ + Add profile              │ Sonnet → gpt-5.6-terra                │
│                            │ Haiku  → gpt-5.6-luna                  │
│ [Use] [Rename] [Delete]    │ Fable  → gpt-5.6-luna                 │
│                            │                         [Save changes] │
├─────────────────────────────────────────────────────────────────────┤
│ ↑↓ profile · Tab pane · ←→ value · Enter action · Esc close        │
╰─────────────────────────────────────────────────────────────────────╯
```

The exact copy can contract to fit, but the hierarchy remains:

1. modal title;
2. profile inventory;
3. provider identity and authentication;
4. model routing;
5. explicit actions; and
6. context-sensitive help.

### Profile pane

The profile pane always starts with virtual `Standard Claude`, followed by the
configured profiles in list-file order and an `Add profile` row.

Each profile row shows:

- display name;
- an active marker for the currently persisted profile;
- a short readiness state: `Ready` or `Needs key`; and
- provider identity when it is not already evident from the display name.

The keyboard cursor is visually distinct from the persisted active marker.
Moving the cursor only previews details; it never changes the active profile.

### Detail pane

Standard Claude is read-only and explains that Claude Code uses its native
login and native model selection. Its only primary action is `Use profile` when
it is not already active.

Custom profiles show:

- friendly provider name;
- authentication kind and status;
- gateway endpoint, or `Local Codex bridge` for ChatGPT;
- the four Anthropic aliases (`opus`, `sonnet`, `haiku`, `fable`);
- each alias's mapped provider model or `(none)`; and
- a masked API-key row for API-key providers.

ChatGPT profiles show `codex login` as a read-only authentication source. Wisp
Deck continues not to read or store Codex credentials.

The detail pane has its own cursor. Left/right cycles a model mapping. Enter on
an API-key row opens a masked inline text input. Mouse clicks mirror these
actions.

## Responsive Behavior

At widths below 64 columns, the modal uses one pane at a time:

- it opens on the profile list;
- Enter or Right drills into the selected profile's details;
- Left or Esc returns from details to the list; and
- Esc from the list closes the modal.

The same model state and actions back both layouts, so compact rendering does
not create a second behavior implementation.

If the terminal is too short for the complete profile or settings list, the
active pane scrolls while its title, actions, and help footer remain visible.

## State and Persistence

Opening the modal captures:

- the persisted active profile;
- the profile under the list cursor;
- the selected provider's model mappings;
- API-key presence (never an unmasked rendered value); and
- a clean draft copy of editable settings.

Moving through profiles only changes the preview. Profile selection is changed
with `Use profile`, which writes the active pointer and updates OpenCode through
the existing sync hook.

Model mappings and API-key edits remain in a profile-local draft until `Save
changes` succeeds. A successful save refreshes readiness and the Settings row.
Closing or switching away from a dirty draft opens an inline
`Discard unsaved changes?` confirmation.

Errors render inside the modal and preserve the current cursor and draft. A
failed write never closes the modal or claims success.

## Profile Lifecycle

### Add

`Add profile` first replaces the detail pane with the built-in provider choices:

- Zhipu / GLM;
- Xiaomi MiMo; and
- OpenAI / ChatGPT.

After choosing a provider, the user enters a display name. Creation writes an
explicit provider marker and provider-appropriate initial settings, rather
than inferring the provider forever from the profile name.

Provider defaults initialize:

- the provider endpoint where applicable;
- sensible mappings for all four Claude aliases;
- the ChatGPT provider marker and bridge models; and
- no API key.

The new profile appears in the inventory and remains inactive until `Use
profile` is chosen. API-key profiles therefore cannot accidentally become
active before a key is entered.

### Rename

Rename uses an inline text input in the modal. It changes only the display name;
the explicit provider marker keeps provider identity stable.

### Delete

Delete requires confirmation and is unavailable for Standard Claude. Deleting
the active profile resets the active selection to Standard Claude, matching the
existing storage contract. The list, detail preview, Settings row, and OpenCode
mirror refresh after success.

## Data Model Changes

The provider catalog gains presentation and initialization metadata:

- friendly display name;
- default alias mappings; and
- the existing authentication, endpoint, and model catalog fields.

Profile creation moves to a provider-aware `claudeconfig` operation that writes
valid initialized JSON and an explicit provider marker. Existing generic
profile mutation APIs remain usable by the standalone command while the modal
uses the provider-aware path.

The main menu gains a cohesive subscription-modal state rather than extending
the legacy model-map flags. The state owns:

- open/closed status;
- responsive pane focus;
- profile and detail cursors;
- scroll offsets;
- draft mappings and key input;
- add/rename/delete/discard submodes;
- hover targets; and
- inline error/feedback state.

The legacy appended model-map entry points are redirected to the modal. After
tests are migrated, the obsolete appended renderer and its dedicated hit-test
state can be removed.

## Keyboard and Mouse Contract

Normal mode:

- `↑` / `↓` or `j` / `k`: move within the focused pane;
- `Tab` / `Shift+Tab`: switch panes;
- `←` / `→`: change the selected model value or navigate compact mode;
- `Enter`: run the focused action;
- `a`: add a profile;
- `r`: rename the focused custom profile;
- `d`: request deletion of the focused custom profile;
- `u`: use the focused profile;
- `s`: save the current settings draft;
- `Esc`: back out one level, then close; and
- `Ctrl+C`: close the modal without quitting Wisp Deck.

Mouse behavior:

- hover highlights without moving the keyboard cursor;
- profile click previews, and clicking the already-focused profile drills into
  details in compact mode;
- model-row click cycles its value;
- buttons have bounded glyph hit areas; and
- clicking outside the card closes it only when no dirty draft or confirmation
  is active.

## Visual Treatment

The modal follows the existing Wisp Deck palette and rounded box language:

- faintness dims the background without adding a muddy tint;
- one accent is reserved for the cursor and primary action;
- green indicates ready, amber indicates missing authentication, and red is
  reserved for destructive confirmation or errors;
- active status and keyboard focus use different symbols so the user never
  mistakes preview for persistence;
- values use aligned columns to make the four mappings scannable; and
- action labels have consistent padding and bounded pointer hit areas.

No animation is required. The TUI should feel immediate and stable, especially
over remote or slow terminal sessions.

## Testing

Implementation follows red-green-refactor cycles.

### Provider and persistence tests

- provider-aware add writes the explicit marker, endpoint, and default mappings;
- rename preserves provider identity;
- delete resets an active profile to Standard;
- API keys remain secure and model mappings preserve unrelated JSON; and
- failed writes do not partially update in-memory selection.

### Modal model tests

- Settings Enter and PLAN Enter open the overlay;
- opening focuses the active profile;
- all profiles and the add row are reachable;
- preview does not persist selection;
- `Use profile` persists only ready profiles;
- draft mappings save explicitly and discard confirmation protects changes;
- Standard cannot be renamed or deleted;
- add, rename, and confirmed delete refresh the inventory;
- API-key and ChatGPT authentication rows differ correctly; and
- compact navigation uses the same underlying actions.

### Rendering and mouse tests

- the background is faint-dimmed and the card is centered;
- normal width renders both panes;
- narrow width renders one pane without clipping;
- active, focused, ready, and needs-key states are visually distinct;
- height-constrained content scrolls while actions/footer remain visible;
- hit targets map to rendered glyph spans; and
- outside click obeys dirty-state protection.

### Regression and integration checks

- the top PLAN switcher still cycles ready profiles;
- Settings left/right no longer changes profiles;
- Account and AI Tools modals remain unaffected;
- OpenCode sync still excludes ChatGPT profiles;
- modified Go files pass `gofmt` and `go vet`;
- the complete repository test suite passes; and
- `make install` plus path, SHA-256, and code-signature verification completes
  before handoff.
