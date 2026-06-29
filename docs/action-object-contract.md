# Action-Object Contract

## Overview

Every JSON surface in amq-squad that emits "what you can do next" must produce
action objects conforming to this contract. The canonical shape lets amq-noc,
CLI skills, and future surfaces consume, render, and execute actions without
per-surface parsers.

Surfaces in scope for the full contract:

- `amq-squad status --json` (`member_actions`, `session_actions`)
- `amq-squad operator status --json`
- `amq-squad next --json`
- repair fault objects
- mutation follow-up actions (`dispatch --json`, `up --json`, etc.) use the
  documented follow-up subset below.

## Canonical Fields

All action objects must include these fields:

| Field | Type | Description |
|---|---|---|
| `id` | string | Stable action-type identifier (e.g. `focus`, `send`, `status`). Unique within a surface per response. Do not reuse across action types. |
| `label` | string | Human-readable one-line description. Safe to display without further escaping. |
| `action_kind` | string | Execution category. One of `run`, `display`, `gate_answer`, `repair`. |
| `command` | string | CLI command, ready to copy and run. Present for `run`, `gate_answer`, and `repair` kinds. May be empty for `display`. |
| `available` | bool | Whether the action can currently be taken. When `false`, `unavailable_reason` explains why. |
| `unavailable_reason` | string | Non-empty when `available` is `false`. Human-readable explanation; matches `reason` on the same action object. |

## Legacy Fields

The following fields are present on `runtimeaction.Action` (member and session
actions) for backward compatibility with existing consumers. New code should
read the canonical fields; existing consumers may continue to read either.

| Legacy Field | Canonical Equivalent | Notes |
|---|---|---|
| `kind` | `id` | `kind` is the action-type identifier (focus/send/status/...); `id` is the canonical mirror. Do not confuse with `action_kind`. |
| `needs_confirmation` | (no direct equivalent) | `true` implies the action requires user confirmation before execution. Informational; `action_kind` = `run` is not a substitute. |
| `mutates` | (no direct equivalent) | `true` means the action may modify state. Informational only. |
| `reason` | `unavailable_reason` | `reason` is the legacy field; `unavailable_reason` mirrors it when `available` is `false`. |
| `scope` | (no canonical equivalent) | `agent` or `session`. Kept for display grouping. |
| `namespace_id` | (no canonical equivalent) | AMQ namespace the action applies to. |

## Action Kinds

### `run`

An executable CLI command. The `command` field is always non-empty.
May require user confirmation (`needs_confirmation = true`) before execution.
May or may not mutate state (`mutates` field).

Examples: `send`, `goal_deliver`, `dispatch`, `resume`, `stop`.

### `display`

A read-only or navigational action. Never mutates state. `needs_confirmation`
is always `false`. `command` may be provided for copy/paste convenience.

Examples: `focus`, `status`, `resume_preview`, `task_list`, `thread`,
`attach_control`.

### `gate_answer`

An operator gate answer action. Always includes a `command` that sends the
answer on the gate thread.

### `repair`

A structured recovery action emitted alongside a fault object. Always includes
a `command` that resolves the fault.

## Availability Semantics

- `available: true` means the action can be taken now.
- `available: false` means the action is structurally present (the surface
  knows about it) but is currently blocked.
- When `available` is `false`, `unavailable_reason` and `reason` both explain
  why. A client should display the reason alongside the action rather than
  hiding the action entirely.
- A client may offer an override path (with explicit confirmation) for
  `available: false` actions that carry an `--override-boundary` variant.

## Copy and Execute Semantics

- `command` is a complete, copy-safe CLI invocation. It uses shell quoting so
  it can be pasted directly into a shell without modification.
- A client that executes `command` programmatically must parse it as a shell
  word list, not a raw exec call.
- `command` placeholders (e.g. `--goal <goal>`) indicate fields the user must
  supply before execution. A client may render these as editable form fields.

## Legacy Field Mapping Table

The `kind` field on `runtimeaction.Action` historically served as both the
action-type identifier (what action this is) and a rough execution category.
v2.12.0 separates these concerns:

```
kind="focus"              -> id="focus",   action_kind="display"
kind="send"               -> id="send",    action_kind="run"
kind="goal_deliver"       -> id="goal_deliver", action_kind="run"
kind="dispatch"           -> id="dispatch", action_kind="run"
kind="resume"             -> id="resume",  action_kind="run"
kind="resume_preview"     -> id="resume_preview", action_kind="display"
kind="resume_current_window" -> id="resume_current_window", action_kind="run"
kind="resume_new_session" -> id="resume_new_session", action_kind="run"
kind="status"             -> id="status",  action_kind="display"
kind="stop"               -> id="stop",    action_kind="run"
kind="stop_close_panes"   -> id="stop_close_panes", action_kind="run"
kind="task_list"          -> id="task_list", action_kind="display"
kind="thread"             -> id="thread",  action_kind="display"
kind="attach_control"     -> id="attach_control", action_kind="display"
```

## Mutation Follow-Up Actions

`dispatch --json`, `up --json`, and similar mutation commands emit a
`mutationResult` envelope with an `actions` array. These follow-up actions
conform to the canonical identity and availability fields, but omit mutability
details because they are post-success observe/follow-up commands:

| Field | Description |
|---|---|
| `kind` | Legacy action-type identifier (same as `id`). |
| `id` | Canonical stable identifier (mirrors `kind`). |
| `label` | Human-readable description. |
| `command` | CLI command. |
| `action_kind` | `run` or `display`. |
| `available` | Always `true` for emitted follow-up actions. |
| `unavailable_reason` | Omitted because follow-up actions are emitted only when available. |

Follow-up actions do not include `needs_confirmation` or `mutates` because the
originating mutation already succeeded and their intent is observe/follow-up.

## Adopted v2.12.0 Surfaces

The following surfaces adopted the canonical contract in v2.12.0:

- **`operator status --json`** (#267): operator gate and directive actions use
  canonical action fields and may include surface-specific extension fields.
- **`amq-squad next --json`** (#269): returns a single canonical action object
  for the highest-priority operator action.
- **Repair fault objects** (#265): each fault includes a `remedy` field that is
  a canonical action object with `action_kind = repair`.

## Stability Guarantee

Fields in this contract are considered stable as of v2.12.0. New fields must
be additive and `omitempty`. Existing fields must not be removed or renamed.
The `kind` (legacy) and `id` (canonical) fields will remain in sync
indefinitely for backward compatibility.
