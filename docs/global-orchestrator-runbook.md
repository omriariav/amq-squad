# Global orchestrator runbook

How to stand up an orchestrator from scratch, and the scripts that type the
create sequence for you. Two modes:

| Mode | You are | Wake | Script |
| --- | --- | --- | --- |
| **Global / root** | a multi-run supervisor at a neutral root (e.g. `~/Code`) | none — you poll | `scripts/orchestrator/start-global-orchestrator.sh` |
| **Project** | driving one run (managed spawn) or its lead (external-lead) | yes | `scripts/orchestrator/start-project-orchestrator.sh` |

## Preconditions (both modes)

- Inside **tmux** — visible spawns refuse outside it, and the default wake
  injector needs a pane to send keys to.
- `amq-squad` + `amq` on `PATH`; AMQ floor is **0.40.0**. `amq-squad doctor`
  warns on version skew (children inherit the `amq-squad` on `PATH`).
- In the orchestrator conversation, invoke the **`amq-squad-orchestrator`** skill.

Being inside tmux is **necessary but not sufficient**: a manually started
`claude`/`codex` pane has no `AM_ROOT`/`AM_ME`/launch record, so the control
plane can't see it and wake has nothing to bind. The **register** step is what
binds pane → handle → mailbox root.

## Global / root mode (poller)

A supervisor that owns many runs across repos. It never `cd`s into a project and
never mutates code; it drives each run by explicit `--project/--profile/--session`
and keeps the multi-run board (see the orchestrator skill). `--no-wake` is normal
here — there is no single inbox to wake on.

```sh
scripts/orchestrator/start-global-orchestrator.sh          # ~/Code, claude
scripts/orchestrator/start-global-orchestrator.sh -C ~/work -a codex -n noc
```

It preflights, opens a tmux window at the root, launches the agent, and prints
the poll/steer/approve command cheatsheet.

## Project mode (create a run)

The friction in orchestration is typing the create sequence with the namespace
repeated correctly. This script fills it once. **Default is preview** — it prints
the exact commands and runs the read-only `--dry-run` variants; add `--go` to
create for real.

**Managed** (default) — amq-squad spawns the whole team, incl. the lead, into
sibling tabs; panes are registered + wake-live automatically:

```sh
# preview (no mutation): roster + spawn plan, validated via --dry-run
scripts/orchestrator/start-project-orchestrator.sh \
  -p ~/Code/app -s issue-96 -P release -r cto \
  --roles "cto,fullstack,qa" --binary fullstack=codex -g "fix issue 96"

# create it
scripts/orchestrator/start-project-orchestrator.sh \
  -p ~/Code/app -s issue-96 -P release -r cto \
  --roles "cto,fullstack,qa" --binary fullstack=codex -g "fix issue 96" --go
```

**External-lead** — your current agent pane *is* the lead (e.g. a Claude Code
conversation). Pane binding must happen in that pane, so this shell can't do it
for you; the script emits a filled-in paste block to run from the lead pane
(with the `! ` prefix if you're in Claude Code):

```sh
scripts/orchestrator/start-project-orchestrator.sh \
  -p ~/Code/app -s issue-96 -P release --roles "cto,fullstack,qa" \
  -g "fix issue 96" --external-lead
```

The emitted sequence: `new team … --orchestrated --lead` (once) →
`lead register … --wake` (bind this pane) → `up … --visibility sibling-tabs`
(spawn workers) → optional `goal start … --register-orchestrator --yes`.

## Wake outside a managed pane

If the lead/orchestrator runs in a plain terminal **outside tmux**, the default
send-keys injector has no pane to hit. Use AMQ's external injector:

```sh
amq-squad lead register --role <r> --session <s> --wake \
  --wake-inject-via /abs/path/to/injector --wake-inject-arg ...
```

There is no bundled injector — supply one that pokes your terminal
(notify / focus / send-keys to a specific pane). Inside tmux this is unnecessary.
