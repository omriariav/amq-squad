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
- `amq-squad` + `amq` on `PATH`; AMQ **0.49.x is the supported series**, with
  0.49.9 as the minimum supported release. Both real-AMQ matrices validate
  pinned v0.49.9 and `latest`; `latest` remains a forward-compatibility canary
  and is not a support claim. Releases older
  than 0.49.9 are rejected fail-closed; upgrade to v0.49.9 or newer and
  stop/resume agents so the parent shell refreshes the complete AMQ identity
  tuple. A child command cannot repair stale injected environment.
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
amq-squad global status --root ~/work                    # read-only board
amq-squad global status --root ~/work --json             # schema-versioned data
```

`global status` reads the stamped NOC registry exactly once and projects only
its registered namespaces. Each row exposes the canonical lead, registration
state and provenance, open operator gates and ages, watcher/backstop state,
readiness, source errors, and inspect/repair action objects. The command never
scans arbitrary repositories and never executes an action; every mutation is
confirmation-gated.

For registered project runs, watcher state includes the managed AMQ backend,
its exact operator mailbox binding, running state, lifetime watch restart
count, current failure streak, pending-collect state, collect retry count, and
last watch/collect timestamps. `amq watch` is only a non-consuming wake signal;
its JSON event is validated before the scoped watcher performs one root-correct
safe collect and drives the existing attention notifier. Failed collects
replay independently of later signals, including one safe replay pass at
watcher startup. Exhausted watch retries render the backend not running and
degraded while the fsnotify/periodic-rescan path remains active. Use bounded
`amq-squad monitor --once` as the explicit manual fallback.

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
- **Effort** — `--effort "role=level,..."`; amq-squad normalizes it into the
  selected binary's native form. Keep unrelated native flags in
  `--codex-args`/`--claude-args` instead of duplicating effort there.

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

Use the same proposal, default-No preparation, readiness, and separate
default-No launch sequence for deterministic layouts. Keep the layout and
launcher contract identical at every stage:

```sh
# proposal only
amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --roles "cto,fullstack,qa" --layout-preset lead-left \
  --launcher-pane close-after-start --launch-shape working-team-together \
  --goal "fix issue 96" --prepare-plan

# default-No preparation approval; no panes launch
amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --roles "cto,fullstack,qa" --layout-preset lead-left \
  --launcher-pane close-after-start --launch-shape working-team-together \
  --goal "fix issue 96" --prepare

# readiness is read-only and must preserve the accepted topology
amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --layout-preset lead-left --launcher-pane close-after-start \
  --launch-shape working-team-together --readiness-json

# separate default-No launch approval; copy the accepted digest exactly
amq-squad run start -p ~/Code/app -s issue-96 -P release \
  --layout-preset lead-left --launcher-pane close-after-start \
  --launch-shape working-team-together --goal "fix issue 96" \
  --goal-source operator_goal --goal-digest 'sha256:<accepted-digest>' --go
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

## Read-only native goal supervision

`status --json`, board session rows, and the doctor check with
`kind: "goal_supervision"` project one shared assessment of the visible lead's
native goal state. The object is schema-versioned, time-bounded, and
fingerprinted across the exact project/profile/session namespace, lead and
launch identity, recorded managed pane, prepared run, native goal attempt,
pause generation, fresh lifecycle, blocker/gate/local-input evidence,
invariants, policy revision, claim projection, and retry budget. A managed
pane requires the canonical launch target `current-window`, `new-window`, or
`new-session`, plus exact pane ID, window ID, tmux session, and deterministic
lead title. PID liveness, process-binary identity, and pane identity must all
be positive; no one signal substitutes for the others.

The state vocabulary is deliberately closed:

- `running`, `parked_waiting_amq`, and `goal_terminal` are non-attention
  lifecycle states.
- `native_goal_paused_eligible` means every positive eligibility fact is
  present.
- `native_goal_blocked_human` means a known human decision, local input, open
  gate, or unresolved known blocker remains.
- `native_goal_blocked_unknown` means evidence is missing, stale,
  contradictory, or ambiguous.
- `lead_down` and `pane_busy_or_unverified` require restoration or inspection,
  never inferred delivery.

Policy modes are `manual`, `notify-only`, and `safe-auto`; missing policy is
compatibility-safe `manual` revision 1, and cloned profiles discard the source
policy. This assessment layer is read-only: it cannot claim an attempt, send a
message, or inject `/goal resume`. A later delivery layer must consume the
same fresh fingerprint and atomically establish claim-once state before any
mutation. Fuzzy pane discovery, legacy name matching, raw tmux send-keys, and
unknown source states are not eligible fallbacks.

PR4 intentionally leaves durable claim, retry-budget, and blocker-resolution
observations unknown in production. Therefore its production assessment cannot
reach `native_goal_paused_eligible` by absence. PR5 must populate all three
from durable stores, revalidate the exact typed goal and attempt against the
generated command and accepted prepared-run goal, and use
`namespace_id + pause_generation + attempt_id` as the durable claim key.
`pause_generation` is stable across unrelated launch-record drift and binds
the launch ID, goal-binding digest, attempt ID, and mode. The claim record
stores the preclaim fingerprint as a staleness check; the fingerprint plus
attempt ID remains the action binding, not the durable claim identity. The
projected resume action stays unavailable until that claim-once execution path
exists; every mutating action still carries its fingerprint, attempt ID, and
confirmation wording.

## Wake outside a managed pane

If a lead/orchestrator runs in a plain terminal **outside tmux**, the default
send-keys injector has no pane to hit. Use AMQ's external injector:

```sh
amq-squad lead register --role <r> --session <s> --wake \
  --wake-inject-via /abs/path/to/injector --wake-inject-arg ...
```

There is no bundled injector — supply one that pokes your terminal. Inside tmux
this is unnecessary.
