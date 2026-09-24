# attention — gotchas

Gotchas for attention/status detection. Loaded when Claude opens a file in this package.

### A parked Claude turn runs outside the session, and the session's status freezes

The 🔔 on a tab is `phase=attention` in the attention state file, and for Claude
that phase has ONE source: the `status` field of the account-local record Claude
writes at `<config-dir>/sessions/<pid>.json`. An `idle` following a `busy` means
the turn ended — it rings the bell and plays the sound.

Claude 2.1.220 can **park** a turn: it hands the work to a background job process
claimed from its own daemon pool and stamps `parkedJobId` on the interactive
record. Two consequences, and both broke the bell:

- The job process is a child of the daemon, **never** a descendant of the
  supervised launch root, so `ClaudeRegistryMapper`'s tree-scoped discovery
  cannot reach the work at all.
- Parking writes only `parkedJobId` and unparking only clears it — **neither ever
  touches `status`**. The interactive record's `status` therefore freezes at
  whatever it held when the turn moved away, and stays frozen for every turn the
  job then runs.

Read literally, that reports a working agent as `idle`: the turn is declared
done, the bell rings, and then — because attention is sticky and only a `busy`
observation clears it — nothing ever clears it. This shipped; a session sat at 🔔
through 40+ minutes of continuous work while its record read
`status:"idle", statusUpdatedAt:<40 minutes ago>, parkedJobId:"…"`.

So `resolveParkedJob` follows `parkedJobId` to the `kind:"bg"` record carrying
that `jobId` and takes the status from there. That record is outside the tree, so
it is trusted no further than a launch-tree one: it must name the job, and it
must describe its own live process (PID present in the point-in-time `ps` table,
with a matching `procStart`), so a leftover file for a recycled PID can never
speak for the session. Ambiguity or an unresolvable job is uncertainty
(`found=false`), never a guess.

**Follow the job — don't just suppress the false `idle`.** Parking persists
across turns, so a session that merely ignored a parked `idle` would never ring
when the work actually finished, trading a false bell for no bell at all.

Guarded by `TestClaudeRegistryMapperReportsParkedSessionAsTheJobRunningItsTurn`
and its neighbours in `internal/attention/claude_registry_test.go`.

### A cleared chat retires the attention it threw away

Attention is sticky by design: once `phase=attention` is published, only a `busy`
observation clears it. Clearing the chat (`/new`, `/clear`, picking another
conversation with `/resume`) never produces one — the turn the 🔔 (or the 👀 it
decayed into) was raised about is simply gone, and the tab keeps ringing at a
conversation that no longer exists.

The only thing that moves is the registry record's **`sessionId`**: Claude
replaces it the moment the chat is cleared, while `status` stays `idle`. So
`ClaudeRegistryMapper` reports the conversation alongside the status, and
`ClaudeReducer.observeConversation` retires every pending request — question,
permission, error, and the sticky attention phase itself — when it changes.

- **The interactive session owns the conversation.** A parked session's job
  record speaks for the status only (see above); `SessionID` always comes from
  the launch-tree record.
- **An empty conversation is not a change.** A failed registry read, or a Claude
  too old to report `sessionId`, must leave attention alone — reading absence as
  a change would silence a real request on the next transient miss.
- **Arming survives the change.** A conversation replaced mid-turn is a fork or a
  compaction, not a user clear, and the work still running still owes the user
  its completion bell — so only pending attention is cleared, never `armed`.
- **A present `sessionId` is validated like a job id.** It decides whether
  attention is retired, so a non-string, control-character or oversized value
  rejects the record rather than being guessed at.

Guarded by `TestClaudeReducerClearedConversationRetiresAttention`,
`TestClaudeReducerKeepsAttentionWithoutAConversationChange`,
`TestClaudeReducerClearedConversationKeepsTheRunningTurnArmed`,
`TestClaudeRegistryMapperReportsTheSessionsCurrentConversation`, and
`TestClaudeRegistryObservation_carries_the_conversation`.

### A `claude -p` inside the pane is not the session

A hook or a tool can run `claude -p`. The security-guidance plugin does it to
review a commit, from inside the repository the commit landed in. That child
writes its own registry record: `kind` "interactive", the same `tmux` pane,
`entrypoint` "sdk-py"/"sdk-cli", and the reviewed checkout as its `cwd`.

The mapper takes the shallowest valid record, and it skips an invalid one. So
whenever the agent's own record is missed, the child answers for the tab. Its
cwd then reaches the worktree follow, and the tab moves into whatever checkout
the reviewer ran in. So a record whose `entrypoint` starts with `sdk` is never a
candidate. The filter is deny-by-name: older records and the test fixtures have
no `entrypoint` at all, and they must still count.

Guarded by `TestClaudeRegistryMapperNeverReportsAnSDKChildAsTheSession`.
