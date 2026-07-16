# Global orchestrator runbook

How to stand up an orchestrator from scratch. The create sequence is wrapped by
two native CLI verbs so the `--project/--profile/--session` namespace is typed
once, not per command.

| Mode | You are | Wake | Command |
| --- | --- | --- | --- |
| **Global / root** | a multi-run supervisor at a neutral root (e.g. `~/Code`) | none — you poll | `amq-squad global start` |
| **Project run** | driving one orchestrated run in a repo | yes (managed spawn registers panes) | `amq-squad run start` |
| **Project run, external lead** | your current project pane is the lead | yes (current pane is registered as lead) | `amq-squad run start --external-lead` |

The `scripts/orchestrator/*.sh` files are thin forwarders to these verbs; the
verbs are the source of truth.

## Preconditions

- Inside **tmux** for visible spawns (`global start --go`, and `run start --go`
  with the default `--visibility sibling-tabs` or `--visibility current`).
  Hidden spawns (`run start --visibility detached --go`) do not require a
  visible pane.
- `amq-squad` + `amq` on `PATH`; AMQ floor is **0.42.1**. The minimum 0.42.1
  compatibility floor is unchanged. This release is explicitly validated
  against pinned 0.43.1; latest remains a forward-compatibility canary. After
  upgrading AMQ, stop and resume/relaunch agents so the parent shell refreshes
  the complete AMQ identity tuple; a child command cannot repair stale injected
  environment.
  `amq-squad doctor` reports legacy/inconsistent pins and version skew
  (children inherit the `amq-squad` on `PATH`).
- In the verified live-lead conversation, invoke **`amq-squad:orchestrator`**. The old `amq-squad-orchestrator` name is a compatibility redirect only.

Being inside tmux is **necessary but not sufficient**: a manually started
`claude`/`codex` pane has no `AM_ROOT`/`AM_ME`/launch record, so the control
plane can't see it and wake has nothing to bind. Spawning **through** amq-squad
(`run start`, `up`) is what records the pane → handle → root contract.

## Global / root mode (poller)

Supervises many runs across repos; never `cd`s into a project, never mutates
code. `--no-wake` is normal — there is no single inbox to wake on. Preview by
default; `--go` opens the window and launches the agent.

```sh
amq-squad global start                                   # ~/Code, claude, preview
amq-squad global start --root ~/work --agent codex --go  # launch a codex supervisor
amq-squad global start --agent claude --model claude-opus-4-8 --go
```

Then drive each run by explicit namespace (`goal draft`/`goal start`,
`monitor --once`, `status`, `next`, `operator answer`). See the skill's
multi-workstream board protocol.

## Project run mode (create a run)

The shipped contract is proposal → default-No preparation → readiness → a
separate default-No launch. A generic preview validates only the current spawn
plan; it is not preparation approval and must not jump directly to `--go`.

The interactive wizard can create either this project-run preview or a
Global/NOC preview. In a TTY, run `amq-squad wizard` (or zero-argument
`amq-squad run start`), choose the scope, and review the two canonical commands
it prints. The wizard first asks `Prepare coordination artifacts? [y/N]` after
the read-only proposal. Only explicit `y`/`yes` writes preparation artifacts.
After readiness passes, `Launch now? [y/N]` is a separate default-No gate whose
live argv carries the exact accepted launch shape, goal source, and goal digest.

Global/NOC scope collects a neutral root, one `claude` or `codex` agent, model,
validated effort, extra native arguments excluding effort, and a window name.
Effort is normalized into the selected binary's existing native argument form
(`--effort` for Claude or `model_reasoning_effort` for Codex); inactive-binary
arguments and project roster flags are never serialized. The scope selector and
Global/NOC questions use the accessible line prompt on the same TTY; project
answers use Bubble Tea when enabled. Both return to the same default-No consent
boundary after canonical preview.

```sh
# proposal (no mutation)
amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --roles "cto,fullstack,qa" --binary "fullstack=codex" \
  --launch-shape working-team-together --goal "fix issue 96" --prepare-plan

# default-No preparation approval; no panes launch
amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --roles "cto,fullstack,qa" --binary "fullstack=codex" \
  --launch-shape working-team-together --goal "fix issue 96" --prepare

amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --launch-shape working-team-together --readiness-json

# separate default-No launch approval; copy the accepted digest exactly
amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --launch-shape working-team-together --goal "fix issue 96" \
  --goal-source operator_goal --goal-digest 'sha256:<accepted-digest>' --go
```

### External lead mode

Use `--external-lead` when the agent conversation already open in the current
tmux pane should become the project lead. The command binds the current pane as
the configured lead, starts or repairs lead wake, then spawns only the remaining
workers. It does not run `goal start --register-orchestrator`, add an
`orchestrator` member, or change the profile's configured lead.

```sh
amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --roles "cto,fullstack,qa" --external-lead \
  --launch-shape working-team-together --goal "fix issue 96" --prepare-plan

# Default No: prepare only after accepting the proposal.
amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --roles "cto,fullstack,qa" --external-lead \
  --launch-shape working-team-together --goal "fix issue 96" --prepare

amq-squad run start -p ~/Code/app -s issue-96 -P release --external-lead \
  --launch-shape working-team-together --readiness-json

# Separate default-No launch approval.
amq-squad run start -p ~/Code/app -s issue-96 -P release --external-lead \
  --launch-shape working-team-together --goal "fix issue 96" \
  --goal-source operator_goal --goal-digest 'sha256:<accepted-digest>' --go
```

Requirements:

- Run from the lead member's project root. Passing `--project` from some other
  cwd is refused, because the current pane is what is being adopted.
- Run inside the lead tmux pane (`TMUX` and `TMUX_PANE` set). Preview is
  read-only and validates this instead of printing a false OK.
- Existing profiles keep their configured lead. If you need a different lead,
  run `amq-squad team lead set <role>` first.
- A lead-only roster is valid: the command binds the current pane and reports
  that there are no remaining workers to spawn.

### Choosing binary / model / effort

- **Binary** — `--binary "role=bin,..."` (per role). `global start` uses `--agent`.
- **Model** — `--model "role=model,..."` (forwarded to `new team` and `up`).
- **Effort** — no first-class flag; ride `--codex-args`/`--claude-args`
  (e.g. `--codex-args "..."`). Same convention as the rest of the CLI.

### Visibility (do I see the agents?)

`--visibility` controls the spawn topology; default is **sibling-tabs
(visible)**:

- `sibling-tabs` (default) — one visible tmux tab per agent in the current tmux
  session. Preview works outside tmux; `--go` requires a visible tmux pane.
- `detached` — agents run in a separate tmux session you don't see.
  Supervise via `status`/`console`/`monitor` + wake; attach only to intervene
  (`amq-squad focus`, or the `attach_control` action in `status --json`).
- `current` — split panes in the current window.

Note: this sets the **initial** spawn. Later dynamic spawns by the lead
(`team member add` → `resume`/`up`) carry their own visibility.

### Deterministic layout presets

`run start` can map a user-facing preset to the spawn topology and final tmux
layout:

| Preset | Spawn | Final arrangement |
| --- | --- | --- |
| `lead-left` | current window, vertical splits | lead in the main left pane at 60% width |
| `lead-top` | current window, horizontal splits | lead in the main top pane at 60% height |
| `even-grid` | current window, tiled splits | tiled panes |
| `one-window-per-agent` | sibling windows | one agent per window, focused on the configured lead |

Example:

```sh
amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --layout-preset lead-left --launcher-pane close-after-start --go
```

A preset defaults to `--launcher-pane close-after-start`. Pass `keep` when the
launching pane should remain. External-lead and detached runs force `keep` and
reject an explicit close request before spawning. Without either new flag,
legacy visibility and launcher behavior are unchanged.

Finalization is scheduled only after the agents start, optional goal delivery
succeeds, and final output is printed. It waits a bounded time for the parent
CLI process to exit, then uses the exact pane/window IDs returned synchronously
by the spawn backend. Missing IDs or tmux failures leave every agent running
and surface a persistent `layout_finalization` warning in text and JSON status.

## Wake outside a managed pane

If a lead/orchestrator runs in a plain terminal **outside tmux**, the default
send-keys injector has no pane to hit. Use AMQ's external injector:

```sh
amq-squad lead register --role <r> --session <s> --wake \
  --wake-inject-via /abs/path/to/injector --wake-inject-arg ...
```

There is no bundled injector — supply one that pokes your terminal. Inside tmux
this is unnecessary.
