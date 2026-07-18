# Subscription Modal Visual Polish Design

**Date:** 2026-07-18
**Status:** Approved through delegated product/design authority

## Goal

Refine the existing subscription overlay so its hierarchy, focus, and add flow
feel deliberate at a glance without changing its storage model, keyboard
contract, or responsive two-pane behavior.

## Direction

Use a structured terminal-card treatment:

- keep the profile inventory persistent on the left;
- turn the selected profile's identity and readiness into the detail header;
- group connection data, model routing, and actions into named sections;
- use the existing neutral selection surface and theme accent consistently;
- make the add-profile preview useful instead of leaving most of the pane empty;
- show provider authentication type in the provider chooser; and
- retain the active green dot as a separate persisted-state indicator.

This direction is preferred over a minimal color-only pass because the add
screen currently lacks useful structure, and over a dense metadata view because
subscription settings should remain quick to scan.

## Profile Details

The generic `PROFILE DETAILS` heading and duplicate `Profile` row become one
identity header:

```text
OPENAI / GPT                                      ● READY
OpenAI / ChatGPT

CONNECTION
Authentication  codex login
Endpoint        Local Codex bridge

MODEL ROUTING ─────────────────────────────────────────
Opus            → gpt-5.6-sol
Sonnet          → gpt-5.6-terra
Haiku           → gpt-5.6-luna
Fable           → gpt-5.6-luna

ACTIONS
[ Use profile ]  [ Rename ]  [ Delete ]  [ Save changes ]
```

The profile name remains the primary accent. Readiness moves to a compact,
right-aligned badge so status is visible without consuming a full metadata row.
Connection fields retain semantic authentication colors, model values remain
green, and section rules use a quiet neutral.

Standard Claude uses the same hierarchy with its native-login explanation and
the existing full action row. Unsupported actions remain visible but subdued.

## Add Flow

Focusing `Add profile` shows a useful preview:

```text
ADD PROFILE
Connect another provider to Claude-compatible model routes.

AVAILABLE PROVIDERS
Zhipu / GLM                                      API KEY
Xiaomi MiMo                                      API KEY
OpenAI / ChatGPT                             CODEX LOGIN

[ Choose provider ]
Name it, configure routes, then make it active.
```

The choose-provider button is both keyboard- and mouse-actionable. Entering the
chooser preserves this row structure, adds the keyboard cursor and full-row
selection wash, and keeps authentication type right-aligned. The remaining
name, rename, key, and confirmation screens retain their existing behavior.

## Focus and Help

The active pane heading uses the theme accent while the inactive pane heading
uses the existing dim neutral. Profile focus continues to use the full-row
selection wash introduced for the inventory. Detail settings and actions keep
their existing cursor and reverse-video focus treatment.

The footer keeps its current words and hit-testable back labels, but renders key
names brighter than their descriptions to improve scanning.

## Vertical Rhythm

Use targeted blank rows at the boundaries that otherwise read as collisions:

- one row between the `PROFILES` heading and the first profile;
- one row between `MODEL ROUTING` and the first model mapping;
- one row between the last editable/detail row and `ACTIONS`; and
- one row before Standard Claude's action section.

Do not add a blank row after every heading. A uniform spacing system would push
API-key and dirty profiles into unnecessary scrolling at normal modal height.
Do not widen pane padding because endpoint and model identifiers benefit more
from the available horizontal space.

Profile hit geometry and the list viewport must account for the new fixed
heading gap. Detail cursor-to-line calculations must account for the two new
right-pane rows so keyboard focus and automatic scrolling stay aligned with the
rendered setting or action.

## Lifecycle Action Navigation

Rename, add-name, API-key, delete, and discard screens use a dedicated
two-action cursor:

- `Left` and `Right` move between the confirm and cancel actions;
- `Enter` executes the selected action;
- `Esc` and `Ctrl+C` remain direct cancel shortcuts; and
- the selected action uses reverse-video focus, independently of pointer hover.

This intentionally gives horizontal arrows to the visible action row while a
lifecycle form is open. Text entry still supports normal character insertion,
Backspace/Delete, Home/End, and mouse placement; the arrow contract prioritizes
the modal's explicit action navigation.

Provider selection remains a vertical list controlled by `Up` and `Down`, with
its footer describing that distinct contract.

## Profile Pane Inset

Reserve one blank cell at the right edge of every profile and add-profile row.
The readiness status and selection wash stop before this inset, preventing them
from touching the pane divider. Keep the pane at 28 cells and preserve the
existing name truncation rules within the reduced row content width.

## Responsive and Interaction Constraints

- The wide modal remains two-pane and the compact modal remains single-pane.
- Existing line counts around editable rows stay stable where possible so
  scrolling and cursor visibility do not regress.
- Mouse targets are derived from rendered text and must follow any new button.
- Long profile, provider, endpoint, and model names remain width-truncated.
- No provider, persistence, authentication, or model-routing behavior changes.

## Verification

Rendering tests will require:

- an identity header with a right-aligned readiness badge;
- named connection, routing, and action sections;
- an add preview containing every provider and its authentication type;
- a full-row provider-chooser focus wash;
- a clickable choose-provider preview action;
- unchanged active-versus-focused profile semantics; and
- unchanged card geometry at wide, compact, and short terminal sizes.
