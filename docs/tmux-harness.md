# Disposable tmux test harness

`amq-squad tmux-harness` runs terminal smoke tests against a private tmux
server. It is safe to invoke from a live tmux pane: inherited `TMUX` identity is
discarded, every client is routed through a unique named socket, and cleanup
can address only that server.

## Run an automated smoke

```sh
amq-squad tmux-harness exec --cwd /path/to/disposable/project -- sh ./smoke.sh
```

The command runs from `--cwd` with a captured disposable launcher identity.
Its stdout is passed through unchanged, so JSON and other machine-readable
results remain parseable. Harness identity and cleanup notices go to stderr.

The command environment contains real `TMUX`, `TMUX_PANE`, and `TMUX_TMPDIR`
values for the isolated server, plus:

- `AMQ_SQUAD_TMUX_HARNESS_SOCKET_PATH`
- `AMQ_SQUAD_TMUX_HARNESS_SESSION_ID`
- `AMQ_SQUAD_TMUX_HARNESS_WINDOW_ID`
- `AMQ_SQUAD_TMUX_HARNESS_PANE_ID`

Plain `tmux` invocations are routed to the private server. This also applies to
commands launched later by `tmux run-shell`, whose process environment may not
retain the original client's `TMUX` variable. Starting another
`amq-squad tmux-harness` inside the harness creates a second private server;
the nested harness bypasses the outer routing wrapper and reuses only its
recorded canonical tmux binary.

## Run an interactive smoke

```sh
amq-squad tmux-harness shell --cwd /path/to/disposable/project
```

This attaches to the exact disposable launcher pane. Run the smoke normally,
inspect the resulting windows and panes, then detach with the usual tmux prefix
followed by `d`. Detaching returns to the caller and tears down the server.

## Isolation and cleanup contract

The harness:

- creates a private mode-0700 temporary root and a random `-L` socket name;
- starts tmux with `-f /dev/null`, so user configuration and hooks cannot alter
  the test server;
- removes inherited `TMUX`, `TMUX_PANE`, and `TMUX_TMPDIR` before creation;
- captures and verifies exact `$session`, `@window`, and `%pane` IDs before
  giving control to the smoke;
- keeps a watchdog pane alive if the launcher closes, and self-terminates if
  the controlling `amq-squad` process disappears;
- verifies the recorded socket path, exact session ID, and original server PID
  before teardown; and
- invokes `kill-server` only through the recorded tmux binary, private
  `TMUX_TMPDIR`, and unique `-L` socket.

It never issues a bare `tmux kill-server`, targets the default socket, or uses
a rename-sensitive session/window label for cleanup. If teardown identity does
not match, or the bounded `kill-server` operation fails, it fails closed and
leaves the private root for inspection.

The harness isolates tmux routing, not arbitrary commands: a smoke that
deliberately invokes an absolute tmux binary with a different socket can still
reach that socket. Combine it with `amq-squad review-worktree exec` when the
test also needs exact-commit and AMQ/Git environment isolation.
