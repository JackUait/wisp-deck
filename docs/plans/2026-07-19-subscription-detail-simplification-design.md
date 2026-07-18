# Subscription Detail Simplification Design

## Goal

Remove redundant profile identity and activation controls from the subscription
detail pane while preserving the existing profile-management workflow.

## Detail Header

For an existing profile, the detail pane starts with the provider row. It no
longer repeats the focused profile name or renders a readiness badge. The left
profile inventory remains the single source of truth for profile identity,
focus, active state, and readiness.

The add-profile preview and lifecycle screens keep their own titles because
those titles describe a task rather than repeat a selected subscription.

## Actions

Custom profiles render one action row containing:

```text
[ Rename ]  [ Delete ]  [ Save changes ]
```

`Rename` becomes the first keyboard-focusable action. Left and Right move only
among these three visible actions, so focus can never land on removed content.
The existing `u` keyboard shortcut remains available for compatibility, but
`Use profile` has no rendered button or mouse target.

Standard Claude has no profile-management actions after `Use profile` is
removed. Its empty `ACTIONS` section is therefore omitted instead of showing
disabled controls.

## Scrolling and Responsive Layout

Removing the identity row shifts every existing detail row upward by one.
Cursor-to-line calculations follow the new geometry so focused model, API-key,
and action rows remain visible in short terminals.

At normal widths all three actions share one line. Narrow layouts wrap visible
actions without reintroducing removed controls.

## Verification

Rendering tests require:

- the detail pane to start with `PROVIDER`;
- no selected profile name or readiness badge in the detail header;
- no `[ Use profile ]` button in custom or Standard details;
- custom actions to retain Rename, Delete, and Save on one line when space
  allows; and
- Standard details to omit the empty actions section.

Navigation tests require Down from the final editable setting to focus Rename,
Left from Rename to return to the profile pane, and Right to reach Delete and
Save. Mouse tests require only the three rendered custom actions to have hit
targets.
