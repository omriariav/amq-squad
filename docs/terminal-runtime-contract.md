# Terminal runtime context and capability contract

`status --json` publishes a session-level `data.terminal_context` object and an
authoritative `data.records[].terminal` object for each agent with recorded
terminal identity. The contract is additive and backend-neutral.

## Session context

`terminal_context.schema_version` is `1`. The context fields are:

| Field | Meaning |
| --- | --- |
| `backend` | Controller selected for the current shell: `tmux`, `iterm2`, `terminal_app`, or `unknown`. Inside tmux this remains `tmux`, including under iTerm2 control mode. |
| `host_program` | Allowlisted `TERM_PROGRAM` evidence such as `iterm2`, `terminal_app`, `vscode`, or `unknown`; every other non-empty value is the bounded sentinel `other`. |
| `inside_tmux` | Whether a tmux socket identity is present. The socket path is never serialized. |
| `control_mode` | Whether a tmux control-mode client was observed. This is bounded observation, not proof that every client is in control mode. |
| `remote` | Whether an SSH environment marker is present. Endpoints and usernames are never serialized. |
| `tier` | `A` for tmux, `B` for iTerm2, `C` for Terminal.app, or `unsupported`. |
| `capabilities` | Raw terminal primitives: `focus`, `send_prompt`, `capture_output`, `busy_detect`, and `local_input_detect`. |
| `operations` | Session topology operations only: launch in the current window, a new window, or a new session. |
| `evidence` | A bounded list of normalized sources and `present`/`absent`/`observed` values. |
| `legacy_tmux_json` | Migration policy for the compatibility `records[].tmux` block. |

Capability state is the stable enum `supported`, `force_only`, `unsupported`,
or `unknown`. Every state other than `supported` carries both `reason_code` and
human-readable `reason`. `available` remains an additive compatibility boolean
and is true only for `supported`.

The three contract layers are deliberately separate:

1. `terminal_context.capabilities` and `records[].terminal.capabilities` report raw host/controller primitives.
2. `terminal_context.operations` reports session topology operations.
3. `records[].actions` reports effective per-member squad actions.

Effective `goal_deliver` is supported only when the current command can resolve
a live native prompt target. The existing claim-once durable fallback occurs
only after an ambiguous native submit, so a mailbox alone does not make the
action available. Effective `dispatch` is supported only when the row proves an
exact namespace root, handle, and initialized mailbox route. Unknown hosts and
native rows without executable-path evidence fail closed.

## Per-agent terminal identity

`records[].terminal` is authoritative. It carries the recorded backend identity,
tier, liveness, and the same explicit capability-state vocabulary. The legacy
`records[].tmux` block remains byte-for-field compatible throughout v2 and is
not eligible for removal before v3. New consumers should read `terminal` first
and fall back to `tmux` only for older records.

`local_input_detect` prevents an important ambiguity:

- `supported` plus no `local_input` object means no blocker was observed;
- `unsupported` means the backend cannot inspect local input and includes why;
- `unknown` means the backend or evidence is unknown.

An unsupported or unknown detector must never be rendered as “not blocked.”

## Native terminal evaluations (#374, #375)

iTerm2 retains safe focus when a stable recorded window id and verified process
liveness are available. The #374 evaluation did not establish an atomic
send-and-submit primitive together with read-only capture and busy detection
that can be tested without driving a user's live GUI session. Native
`send_prompt`, `capture_output`, `busy_detect`, and `local_input_detect`
therefore remain unsupported. Effective goal delivery also remains unavailable
without a live native prompt target; dispatch requires an exact durable AMQ
member route.

Terminal.app launch remains available on macOS. The #375 evaluation found that
Accessibility-based keystroke injection requires user-scoped permission and
does not provide a permission-independent, stable output or busy-state API for
an exact tab. Focus, send, capture, busy detection, and local-input detection
remain unsupported. Effective goal delivery also remains unavailable without a
live native prompt target; dispatch requires an exact durable AMQ member route.

These are fail-closed product decisions, not claims that the host applications
have no scripting APIs. A future capability may become supported only with
target-identity, permission, atomicity, capture, busy-state, and manual smoke
evidence appropriate to that primitive.
