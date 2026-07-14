# Ledger Refresh Blink Design

## Problem

The native ledger briefly replaces a stable empty-state message (`no changes`)
with `loading changes…` on every periodic repository refresh. The recording
shows that transient frame lasting about 25 ms before the refreshed empty
snapshot restores `no changes`.

## Root cause

`LedgerModel.startLoad` sets `loading` for both the initial load and background
refreshes. The view uses that flag to choose the empty-state label, so an
otherwise invisible background refresh becomes visible whenever the current
snapshot contains no rows.

## Design

Treat `loading` as initial-load state. It starts from `LedgerOptions.Loading`
and is cleared after the first accepted result or error. Starting a later
refresh leaves the current display state intact while the generation-checked
asynchronous load runs. The existing snapshot remains visible until it is
atomically replaced.

Add a model regression test that starts from an accepted clean snapshot,
triggers a refresh tick, and asserts that the view still says `no changes`
without showing `loading changes`. Preserve the existing initial-loading test.

