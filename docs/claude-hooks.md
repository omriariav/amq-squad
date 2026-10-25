# Claude Code seat hooks (amq-squad 3.0)

The `amq-squad` Claude Code plugin ships hooks in `plugins/claude/hooks/`. They turn
the three hook verbs (`context`, `guard`, `evidence ingest`) into Claude Code
hook events for a seat. They are UX and evidence, not enforcement: enforcement
stays in the Go core (the harness-side commit and the core merge, plan 4.4, R32).

## Install

Install at user scope only. The hooks read the seat environment (`AM_ME`,
`AM_ROOT`, `AM_SESSION`) that `amq-squad seat-exec` sets, and the Claude Code
grammar has no plugin or settings flag, so there is no project-scope variant and
nothing is written under a project's `.claude/`:

```
claude plugin marketplace add omriariav/amq-squad --scope user
claude plugin install amq-squad@amq-squad --scope user
```

`claude plugin details amq-squad@amq-squad` lists the four hooks. The `amq-squad`
binary must be on the seat's `PATH` (a seat launched by `seat-exec` inherits it).

## What each event does

| Event | Command | Effect |
| --- | --- | --- |
| SessionStart, UserPromptSubmit | `context --me $AM_ME --json` | The result is injected as `additionalContext`: role text and digest, rules digest, current task, open gates. Every turn, not only on a change (R35). |
| PreToolUse (all tools) | `guard --me $AM_ME --stdin` | The verdict becomes the hook decision (table below). |
| PostToolUse (all tools) | `evidence ingest --me $AM_ME` | One ledger line per tool result, `seq` 1, 2, 3... |

Outside a seat (`AM_ME` unset or empty) every event exits 0 with no output, before
anything else runs. Measured cost on this Mac: p50 6 ms, p95 7 ms per call.

## Verdict mapping

`guard` exits 0 for every verdict. The hook hands the JSON line to the Go
adapter, `amq-squad guard --decision claude`, which parses the complete verdict
object and prints the PreToolUse decision (the shell never reads the verdict;
anything that does not parse is no decision, logged). guard runs in its own
process group: the watchdog kills the group, and the reap's report goes to the
hook log, never to stderr.

| guard verdict (reason codes) | hook output |
| --- | --- |
| `allow` (`allowed`) | `permissionDecision: allow` |
| `deny` (`seat_merge`, `force_push`, `push_delete`, `push_other_branch`, `protected_path`, `grants_invalid`) | `permissionDecision: deny`, reason `amq-squad guard: <reason>` |
| `needs_gate` (`gate_release`, `gate_send`, `gate_credential`, `gate_grow`) | `permissionDecision: deny`, reason `raise the gate for <action_class> <target>` |
| guard exit 1, output not parsable, binary missing | no decision, exit 0, one line in the hook log |

An explicit `allow` replaces Claude Code's own permission prompt for the hooked
tool call: in a seat, guard's verdict is the policy, because an unattended seat
that stopped on a prompt would hang. Outside a seat nothing changes.

`needs_gate` never maps to `ask`. An unattended seat cannot answer a prompt, so the
call is denied and the seat tells its lead to raise the gate; the lead raises it,
the owner signs the answer, and the same call is allowed after that
(`docs/v3-spike/2a3-notes.md`, open question 9). The hook never asks, so the seat
never stops on a prompt.

## When the hooks fail open

Claude Code does not stop a tool call for a hook that fails. Only an explicit
decision of `deny` (or exit 2) blocks. These cases let the call run (R36):

| Case | What happens | S4 measurement (Claude 2.1.305, live seat) | This plugin, measured |
| --- | --- | --- | --- |
| Explicit deny | the call is blocked | 0.17 s | 0.02 s |
| Timeout | Claude kills the hook after exactly the configured timeout (5 s here) and the call runs. The plugin's own watchdog ends a hung `guard` first, at 4.8 s, writes a `guard timeout` log line and exits 0 with no decision | 5.11 s | 4.8 s (watchdog) |
| Exit 1 without a deny | non-blocking error, the call runs | 0.36 s | 0.02 s |
| Missing command | the shell exits 127, the call runs | 0.44 s | 0.01 s |

The S4 times are tool call to tool result in a live seat (they include the model
side and the owner's other hooks). The plugin times are the hook process alone,
run the way Claude runs it, so only the shape (what blocks, what proceeds, the
timeout costing the configured value) compares, not the absolute numbers. A
timeout costs the seat the whole 5 s (4.8 s with the watchdog) on every call that hits it; the
verbs run in 7 to 10 ms (guard p95 8.7 ms, context 6.9 ms, ingest 10.4 ms), so it
takes a hung disk or a stuck lock to get there.

The log is opened once, with /dev/null as the fallback, so a log path that cannot be written (a directory in its place) never stops a verb from running.

The plugin never turns a failure into a block. Every internal failure (`guard`
exit 1, `context` or `evidence ingest` exit 2, a missing `amq-squad`) exits 0 with
no decision, because exit 2 from UserPromptSubmit would erase the prompt and exit 2
from PostToolUse would feed stderr to the model. Each failure appends a line to
`$AM_ROOT/extensions/amq-squad/hooklog/$AM_ME.log` (for example
`2026-10-24T12:00:00Z pre-tool fail-open: guard exit 1`). A seat whose hooks
fail open still runs under the harness-side commit and the core merge; the hook is
not what keeps it inside its grant.

Hooks do not exist in Codex, Grok Build and Cursor seats; they receive role and
rules at launch only (plan 4.4).
