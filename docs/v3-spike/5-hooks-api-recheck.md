# Phase 5 release prep, row 4: Claude Code hooks API re-check

Date: 2026-10-25. Installed Claude Code: 2.1.310 (`claude --version`); it
ships no bundled hooks docs, so the public documentation is the source.
Sources: code.claude.com/docs/en/hooks (read in three windows, characters
0 to 200000 of 281770; the last 81770 were not read) and
code.claude.com/docs/en/permissions (dontAsk rows). No code changed in this
row. docs.anthropic.com redirects to code.claude.com for these pages.

| Fact | Where the plugin relies on it | Source checked | Status (2026-10-25) |
|---|---|---|---|
| Plugin hooks live in `hooks/hooks.json`: optional top-level `description`, top-level `hooks` object, event -> array of matcher groups, each with a `hooks` array of handlers | plugins/claude/hooks/hooks.json | hooks reference, plugin hooks section | confirmed |
| Handler `type: "command"` with a `command` string and `timeout` in seconds | hooks.json (`"timeout": 5` on all four) | hooks reference, handler fields ("Seconds before canceling") | confirmed. New since the spike: exec form (`command` plus `args`) is preferred for paths; the plugin's shell form quotes `"${CLAUDE_PLUGIN_ROOT}"` itself, which the docs say shell form requires. Not a break |
| `${CLAUDE_PLUGIN_ROOT}` is substituted in `command` (and `args`) and exported to the process | every hooks.json command | hooks reference, plugin hooks | confirmed |
| Event names `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse` | hooks.json keys | hooks reference lifecycle table | confirmed |
| `matcher: "*"` (also `""` or omitted) matches every occurrence; UserPromptSubmit and SessionStart ignore a matcher | PreToolUse and PostToolUse use `"*"`; the other two omit it | hooks reference, matcher table | confirmed |
| PreToolUse output is `hookSpecificOutput` with `hookEventName: "PreToolUse"`, `permissionDecision`, `permissionDecisionReason` | `guard --decision claude` (Go adapter) prints it; the hook script passes it through | hooks reference, PreToolUse decision control | confirmed |
| `permissionDecision` values `allow`, `deny` | the adapter emits only these two (needs_gate becomes deny) | hooks reference | confirmed. The docs now list four values: `allow`, `deny`, `ask`, `defer` (`defer` only with `-p`). The plugin never emits `ask` or `defer`; the test model in scripts/test_claude_hooks.py maps `ask` to a prompt, which matches |
| Top-level `decision`/`reason` are deprecated for PreToolUse (`approve`/`block` map to `allow`/`deny`) | the plugin does not use them | hooks reference, Deprecation | confirmed (nothing to change) |
| A hook `allow` does not override the owner's deny or ask rules, and skips the prompt except for actions no mode auto-approves | R36: guard's explicit allow is what lets a dontAsk seat run | hooks reference, PreToolUse decision control | changed in emphasis: the allow has these stated exceptions; the plan's dontAsk-plus-allow premise rests on the S4 and live rows, not on this text. Listed for the lead |
| `dontAsk` permission mode exists (`--permission-mode dontAsk`, `defaultMode`); it auto-denies every call that would otherwise prompt, and runs pre-approved tools (`/permissions`, `permissions.allow`) | `claudeseat.ClaudePermissionMode`, the seat argv, the config dir's settings.json, the launch probe | permissions page, mode table; hooks reference lists `dontAsk` among `permission_mode` values | confirmed that the mode exists. Unverifiable from the docs: whether a PreToolUse `allow` from a hook turns a would-prompt call into a run under dontAsk (the mode table lists allow rules, not hook allows; it adds that an installed "mod" can approve). Only the live Claude rows (owner token file) settle it |
| Exit code 2 from PreToolUse blocks the tool call, routed like `deny`, stderr is the reason | test model `claude_decision` (rc 2 blocks); the script avoids exit 2 | hooks reference, exit codes | confirmed |
| Exit code 2 from UserPromptSubmit blocks the prompt (it never reaches Claude) | hook script comment: exit 2 would erase the prompt, so every failure path exits 0 | hooks reference, exit codes | confirmed |
| Exit code 2 from PostToolUse does not block (tool already ran); stderr is shown to Claude | hook script comment: exit 2 would feed stderr to the model | hooks reference, exit codes | confirmed |
| Exit code 2 from SessionStart does not block; stderr to the user only | the script never exits 2 | hooks reference, exit codes | confirmed |
| Any other non-zero exit is a non-blocking error and the action proceeds (exit 1 explicitly warned about) | fail-open design, S4 fault table, `claude_decision` | hooks reference; S4-report.md (measured on 2.1.305) | confirmed |
| A timed-out command hook is cancelled, its output discarded, and PreToolUse is not blocked | 4.8 s watchdog inside the 5 s timeout; fail open | hooks reference; S4 (5.034 s, `hook_cancelled`) | confirmed. New field `onFailure: "block"` makes a failed hook block; the plugin does not set it, by design (fail open, enforcement stays in the Go core) |
| Default timeout 600 s for command hooks (30 s for UserPromptSubmit) | the plugin sets 5 s explicitly everywhere | hooks reference | confirmed (explicit timeout makes the default irrelevant) |
| SessionStart and UserPromptSubmit take `hookSpecificOutput.additionalContext` (system reminder, no visible transcript entry); SessionStart also takes plain stdout | the script prints `{"hookSpecificOutput":{"hookEventName":..., "additionalContext":...}}` for both | hooks reference | confirmed |
| SessionStart hooks run in the background for `--continue`, `--resume`, `/clear`, but the first response waits for them | context injected on resume | hooks reference | confirmed (new wording, no break) |
| Hook plugin loaded from the user-scope plugin cache keyed by plugin version | claudeseat reads plugin.json's version for the config dir's cache path | not in the pages read | unverifiable from the docs; covered by the plan's live plugin-load row, still waiting on the owner token |

Changed or unverifiable, for the lead:
1. Changed in emphasis: a hook `allow` is not an override of deny or ask
   rules and has stated exceptions; the dontAsk premise (R36) is not
   restated by the docs.
2. Unverifiable here: PreToolUse `allow` under dontAsk; plugin cache keyed
   by version. Both need the live Claude rows.
3. Not read: the last 81770 characters of the hooks page (later events and
   appendices); none of the facts above sit there.
