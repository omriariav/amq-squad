# Staged reviewer iTerm2 control-mode harness

Issue #505 is covered by
`TestPreparedStagedParentTransactionHundredIterationITerm2ControlModeHarness`.
The test exercises the parent-owned staged launch transaction through the tmux
backend used by iTerm2 `tmux -CC`; it does not automate the iTerm2 GUI.

Run the harness from the repository root:

```sh
go test ./internal/cli -run '^TestPreparedStagedParentTransactionHundredIterationITerm2ControlModeHarness$' -count=1
```

The matrix runs 100 transactions for each of the Codex and Claude reviewer
paths. It alternates `new-window` and `current-window` and crosses both targets
with every applicable boundary: topology creation, creation postcondition,
non-selecting metadata, result collection, command barrier, dispatch
postcondition, runtime postflight, consumption, and success.

The harness proves:

- staged current-window splits stay detached and launcher pane/window focus is
  active at every mutation and dispatch boundary;
- the exact child pane receives launch input, while simulated user continuation
  input remains on the launcher pane;
- `@amq_squad_title` supplies deterministic pane discovery without selecting
  the child;
- a replacement is rejected while the prior reviewer remains live and maximum
  concurrent reviewer targets never exceeds one;
- both directions of target-strategy mismatch fail closed;
- every failure removes only its owned pane/window after exact process death,
  retires its launch/bootstrap/wake artifacts, and preserves unrelated topology
  and files.

The focused rollback tests additionally model successful `SIGTERM` delivery
with a process that remains alive. In that case the authoritative artifacts are
retained and replacement stays blocked. A delayed-exit case verifies cleanup
only after both the exact agent and wake PIDs are observed dead.

## Persistent tmux -CC pause recovery

### Verified mechanism

The keyboard-loss root cause was investigated by cto against a live field
incident (see `.amq-squad/evidence/v2-23-1/505-root-cause-cto.md` for the full
investigation, reproductions, and a second live-incident validation during
this workstream). Summary:

- It is **not** tmux's own server-side `pause-after` age threshold. With
  iTerm2's real `pause-after=120` setting, reproductions of the incident
  sequence (window create, failed launch, join) under sustained multi-pane
  output storms never triggered a server-initiated `%pause`; tmux's
  offset-based upstream flow control absorbs a slow or stalled client instead
  of aging its queue.
- The dominant field trigger is **iTerm2's own buffer-size monitor explicitly
  pausing a pane**: `TmuxController.m`'s `pausePanes:` sends
  `refresh-client -A '%N:pause'` as a client-initiated action when it decides
  a pane has fallen too far behind, independent of tmux's own thresholds.
- Out-of-band topology mutation (creating, moving, or joining panes outside
  iTerm2's own control flow) can then strand that paused pane: iTerm2 must
  infer pane moves by diffing layout strings, and a pane paused by its monitor
  and then re-homed this way leaves iTerm2 holding a stale session mapping.
  The pane's output stream stays paused server-side and iTerm2's unpause path
  no longer reaches it - perceived as total keyboard loss. Detach/reattach
  rebuilds the mapping, which is exactly the observed recovery before this fix.
- This was confirmed live during this workstream: a 7+-pane concurrent output
  storm froze one control client's view of a real session; the narrow
  `continue` recovery action below restored it per-pane with no detach,
  focus change, or topology mutation.

The candidate fix's approach - preflight readiness before any topology
mutation, owned-only rollback that never touches user-visible panes, and this
narrow `continue` recovery - targets the correct mechanism: it minimizes the
out-of-band mutations that create the stale mapping, and recovers the pause
when the mapping is stranded anyway.

### Harness scope

`TestPrivateTmuxExactPausedStateContinuesOnlyExactVerifiedPane` covers the
control-client pause. The test creates a unique private `tmux -L` server under
a private `TMUX_TMPDIR`, loads `/dev/null` instead of user tmux configuration,
attaches a `-CC` client through a private `script(1)`-allocated PTY with
`pause-after=1`, **explicitly enters** the real tmux paused state (`refresh-client
-A '%N:pause'`), emits a bounded output burst, observes a real `%pause`, and
then observes the exact `%continue`. Per the verified mechanism above,
explicitly entering the paused state is not a shortcut around an untested
server-side path - it is representative of the dominant, client-initiated
trigger actually seen in the field. The test does not claim its bounded burst
itself crossed tmux's timing-dependent automatic backpressure threshold; it
validates tmux's exact paused-state recovery correlated with the live
incident. It does not automate the iTerm2 GUI or keyboard input. The PTY
harness is Darwin-specific and skips on other operating systems; the product
recovery command is not.

The recovery action is deliberately narrow:

```sh
amq-squad status --project /path/to/project --profile default --session WORKSTREAM --json
# Confirm the emitted control_continue action, which runs:
amq-squad team member control-continue ROLE --client EXACT_CLIENT \
  --project /path/to/project --profile default --session WORKSTREAM --json
```

Status emits `control_continue` only when authoritative managed liveidentity
matches the canonical project/profile/workstream/role and exact tmux terminal
identity, and exactly one control-mode client is attached to that terminal
session. Execution repeats those checks under namespace admission and invokes
only the following idempotent pane-scoped mutation, twice:

```sh
tmux refresh-client -t EXACT_CLIENT -A EXACT_PANE:continue
```

Zero or multiple clients, malformed client rows, a changed client, wrong
session or pane, identity drift, and client exit all fail closed. The action
does not detach clients, change flags, select/focus panes, mutate topology,
touch siblings, retry topology, or run periodically. It repeats the exact
`continue` once because a first call was empirically observed to return success
without emitting `%continue`; the immediate repeat recovered the pane safely.

For iTerm2, enabling **Unpause Automatically** in the tmux integration settings
can prevent a `pause-after` event from remaining persistent. Without it, pause
can recur after another large output burst. Bounded amq-squad tmux read retries
cover transient query failures only; they do not unpause a control client.
Capture long test output away from the live control pane when practical.

For a legacy session that fails managed identity verification, status
intentionally withholds `control_continue` even when the paused client and pane
are visible. Detach/reattach the client or relaunch with the current binary;
never weaken or bypass the verified identity gate.
