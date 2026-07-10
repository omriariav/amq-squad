# Terminal.app Tier C Manual Smoke Checklist

This checklist covers the Tier C macOS Terminal.app backend added for
`--terminal terminal`. CI verifies only the emitted AppleScript argv shape; this
flow needs a macOS desktop with Terminal.app available.

1. From a project with a configured team, start a fresh workstream:

   ```sh
   amq-squad up --session terminal-smoke --terminal terminal --target new-window --no-bootstrap
   ```

2. Confirm Terminal.app opens a visible window or tab for each launched agent.
   Terminal.app controls whether `do script` opens a window or tab; either is
   acceptable for this Tier C backend as long as each agent is visible.

3. Confirm each agent's `launch.json` has a `terminal` block with
   `backend: "terminal_app"`, `target: "new-window"`, `window_name:
   "amq:<workstream>:<role>"`, and whatever Terminal.app exposed for
   `window_id`, `tab_id`, and `tty`. Empty values are omitted.

4. Run status and inspect the runtime actions:

   ```sh
   amq-squad status --session terminal-smoke --json
   ```

   Expected:

   - `records[].terminal.backend` is `terminal_app`.
   - `records[].terminal.pid_alive` is true for live agent processes.
   - `records[].terminal.tty` is present when Terminal.app exposes the tab TTY.
   - `focus` is unavailable with the stable-addressing/manual-focus reason.
   - `send` and `goal_deliver` are unavailable with the #375 Accessibility
     safety reason.
   - `dispatch` is available because durable AMQ dispatch does not require pane
     injection. If the wake sidecar is not live, the optional pane nudge is
     skipped with the same #375 safety reason.

5. Manually focus the Terminal.app window or tab if you need to inspect it.

6. Stop the smoke session:

   ```sh
   amq-squad stop --session terminal-smoke --all
   ```

   Expected: agent PIDs stop; Terminal.app windows/tabs may remain open because
   native safe close is outside this Tier C scope.
