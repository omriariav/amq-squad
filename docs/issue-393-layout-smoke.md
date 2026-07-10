# Issue #393 Slice 5 manual smoke

This smoke is intentionally run from a disposable tmux window, not from a live
amq-squad control pane.

## Managed lead + launcher close + Claude worker

```sh
tmux new-window -n issue-393-smoke
cd /path/to/disposable/project
amq-squad run start \
  --project . \
  --session issue-393-smoke \
  --roles cto,qa \
  --binary qa=claude \
  --lead cto \
  --layout-preset lead-left \
  --launcher-pane close-after-start \
  --goal "report READY only" \
  --go
```

Verify after the command prints its final `done` line:

1. The disposable launcher pane closes; neither agent is stopped.
2. The configured `cto` pane is the main left pane, approximately 60% wide.
3. The Claude `qa` pane remains live and receives its normal bootstrap.
4. Renaming the window or either pane before finalization does not change the
   result because control uses exact tmux IDs.
5. `amq-squad status --session issue-393-smoke --json` has no
   `layout_finalization` warning after success.

Failure injection: make the captured lead pane unavailable before the helper
runs. The launcher must remain safe when applicable, all surviving agents must
remain running, and status must retain a `layout_finalization` warning.

This checklist is documented for release/manual verification. It was not run
inside the active development squad, because doing so would close or rearrange
shared control panes and start an additional Claude process.
