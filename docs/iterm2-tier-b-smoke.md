# iTerm2 Tier B Manual Smoke Checklist

This checklist covers the Tier B iTerm2 backend added for `--terminal iterm2`.
CI verifies only the emitted AppleScript argv shape; this flow needs a macOS
desktop with iTerm2 installed.

The backend asks iTerm2 to type a shell-agnostic launch line into the new
session: `/bin/sh -c <quoted payload>`. The payload exports
`AMQ_SQUAD_TERMINAL_*` metadata before running the generated agent command. This
lets fish, nushell, and POSIX login shells hand off to `/bin/sh`; the terminal
metadata is captured by `amq-squad launch` and then stripped before the agent
process is exec'd.

1. From a project with a configured team, start a fresh workstream:

   ```sh
   amq-squad up --session iterm2-smoke --terminal iterm2 --target new-window --no-bootstrap
   ```

2. Confirm iTerm2 opens one visible window per launched agent.

3. Confirm each agent's `launch.json` has a `terminal` block with
   `backend: "iterm2"`, `target: "new-window"`, a non-empty `window_id`, and no
   `tmux` block unless the agent was itself launched inside tmux.

4. Run status and inspect the runtime actions:

   ```sh
   amq-squad status --session iterm2-smoke --json
   ```

   Expected:

   - `records[].terminal.backend` is `iterm2`.
   - `records[].terminal.pid_alive` is true for live agent processes.
   - `records[].terminal.tab_id` and `records[].terminal.session_id` are present
     when iTerm2 exposes those AppleScript ids. If iTerm2 returns an empty id,
     the field is absent because the launch record uses `omitempty`; absence is
     an iTerm2 metadata gap, not tmux identity.
   - `focus` is available when `window_id` is present and the agent PID/binary
     verifies live. A closed iTerm2 window can still fail at focus time; the
     command should report that the recorded window could not be raised.
   - raw `send` and effective `goal_deliver` are unavailable with the #374
     safety reason because the current goal command requires a live native
     prompt target.
   - `dispatch` is available only when status verifies the exact namespace,
     handle, and initialized durable mailbox; the optional pane nudge is skipped
     for iTerm2 with the #374 safety reason.

5. Focus a role:

   ```sh
   amq-squad focus --session iterm2-smoke --role <role>
   ```

   Expected: the matching iTerm2 window is raised. Closing that window first
   should make the command fail clearly instead of targeting another terminal.

6. Stop the smoke session:

   ```sh
   amq-squad stop --session iterm2-smoke --all
   ```
