# Issue #393 Slice 5 manual smoke

This smoke is intentionally run in a disposable tmux server, not in the live
amq-squad server. The harness routes nested clients explicitly and tears down
only its own server when you detach. See
[Disposable tmux test harness](tmux-harness.md) for the isolation contract.

Start an isolated interactive launcher in the disposable project:

```sh
amq-squad tmux-harness shell --cwd /path/to/disposable/project
```

## Managed lead + launcher close + Claude worker

From the attached harness shell:

```sh
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
shared control panes and start an additional Claude process. Detach from the
harness after verification; its private tmux server is then removed
automatically.
