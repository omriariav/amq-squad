# Issue #393 layout manual smoke

Run this smoke only in a disposable project and a private tmux server. The
former public `tmux-harness` wrapper was removed in v2.28; the operator owns the
test server and its cleanup.

Start a private server from outside the live development squad:

```sh
tmux -L amq-squad-layout-smoke new-session -A -s issue-393 \
  -c /path/to/disposable/project
```

Inside that private session, create the roster and preview one Simple Mode
launch:

```sh
amq-squad new team --roles cto,qa --binary qa=claude \
  --orchestrated --lead cto --sync

amq-squad start issue-393-smoke --project . --target current-window \
  --layout vertical --goal "report READY only"

# Run only after approving the displayed plan.
amq-squad start issue-393-smoke --project . --target current-window \
  --layout vertical --goal "report READY only" --yes
```

Verify:

1. Both configured agents remain live after `start` returns.
2. The layout is vertical and the configured lead is reachable through its
   recorded pane ID.
3. The Claude worker receives the normal bootstrap and the optional goal is
   sent only after both agents verify live.
4. Renaming the window or panes does not change runtime identity.
5. `amq-squad status --session issue-393-smoke --json` reports no launch
   postcondition warning.

For failure injection, make a captured pane unavailable before launch
finalization. Surviving agents must remain running, and status must preserve a
precise warning. Rerun `start` to roll the partial launch forward.

After verification, detach and remove only the explicitly named private test
server from an operator shell:

```sh
tmux -L amq-squad-layout-smoke kill-server
```
