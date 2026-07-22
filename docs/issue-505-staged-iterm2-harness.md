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

`TestPrivateTmuxExactPausedStateContinuesOnlyExactVerifiedPane` covers the
control-client pause that can leave an iTerm2 `tmux -CC` view unable to accept
input after a large output burst. The test creates a unique private `tmux -L`
server under a private `TMUX_TMPDIR`, loads `/dev/null` instead of user tmux
configuration, attaches a `-CC` client through a private `script(1)`-allocated
PTY with `pause-after=1`, enters the real tmux paused state explicitly, emits a
bounded output burst, observes a real `%pause`, and then observes the exact
`%continue`. Explicitly entering the paused state keeps the regression
deterministic; the test does not claim that its bounded burst itself crossed
tmux's timing-dependent automatic backpressure threshold. It validates tmux's
exact paused-state recovery correlated with the live incident; it does not
automate the iTerm2 GUI or keyboard input. The PTY harness is Darwin-specific
and skips on other operating systems; the product recovery command is not.

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
only:

```sh
tmux refresh-client -t EXACT_CLIENT -A EXACT_PANE:continue
```

Zero or multiple clients, malformed client rows, a changed client, wrong
session or pane, identity drift, and client exit all fail closed. The action
does not detach clients, change flags, select/focus panes, mutate topology,
touch siblings, retry mutations, or run periodically.

For iTerm2, enabling **Unpause Automatically** in the tmux integration settings
can prevent a `pause-after` event from remaining persistent. Bounded amq-squad
tmux read retries cover transient query failures only; they do not unpause a
control client.
