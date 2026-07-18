# Subscription Profile Footer Design

## Goal

Simplify the subscription inventory by removing the redundant `PROFILES`
heading and placing `+ Add profile` at the bottom of the left pane.

## Layout

The left pane uses three vertical regions:

1. one blank top-gutter row;
2. a scrolling viewport containing only subscription profiles; and
3. one fixed bottom row containing `+ Add profile`.

The add action remains visible while a long profile inventory scrolls. Its
focused selection wash and the existing one-cell right inset stay unchanged.
At the smallest usable height, the fixed add row takes priority over the top
gutter.

## Interaction

Keyboard navigation continues to treat Add profile as the item after the final
subscription. When Add profile is focused, the scrolling viewport keeps the
last subscription visible while the fixed action remains at the bottom.

Mouse hit-testing detects the fixed add row directly from its rendered label.
Profile hit-testing accounts for the removed heading: the first profile now
starts one card row earlier.

## Verification

Rendering tests require:

- no `PROFILES` text in the profile pane;
- a one-row top gutter before the first profile;
- `+ Add profile` on the final profile-pane row;
- the existing right inset on profile and add rows; and
- the focused add row to remain fixed at the bottom.

Mouse and long-inventory tests require the fixed action and scrolled profile
rows to remain correctly targetable.
