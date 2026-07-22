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
