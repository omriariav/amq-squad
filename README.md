# amq-squad

Role-aware agent team launcher built on top of [AMQ](https://github.com/avivsinai/agent-message-queue).

AMQ owns messaging between agents. `amq-squad` owns the layer above: who is in
the team, what role each agent plays, and how to bring the whole squad back up
after a restart.

## Why

AMQ's `coop exec` is a generic launcher. It sets up a mailbox and execs into `claude` or `codex`, but it doesn't know:

- Which agent is the "CPO" vs "CTO" vs "Fullstack" vs "QA" vs "PM" vs "Designer"
- What command you originally used to launch each one (cwd, binary, flags, session)
- What slash commands or skills that role leans on
- Which peers a given agent actively talks to

`amq-squad` captures this at team-setup time (`.amq-squad/team.json`) and
per-agent at launch time (`launch.json` + `role.md` inside the AMQ mailbox).
AMQ itself stays unchanged.

## Install

```sh
go install github.com/omriariav/amq-squad/cmd/amq-squad@v0.4.0
```

Use `@latest` if you intentionally want the newest published tag.

Requires Go 1.25+ and the `amq` binary in `PATH` (v0.32+). Installing to
`$GOBIN` (or `$HOME/go/bin`) is enough; the launch commands `team show` emits
use the absolute path to whichever `amq-squad` binary is running, so nothing
else needs to be on `PATH`.

## Quick start

```sh
cd ~/Code/my-project
amq-squad team
```

First run: pick personas from the squad market, then choose which CLI runs
each one. `amq-squad` writes `.amq-squad/team.json`, seeds
`.amq-squad/team-rules.md`, and prints launch commands. Later runs print the
same launch commands without asking again. If you are inside tmux, use
`amq-squad team launch` to open the squad automatically. Otherwise, paste one
command into each terminal pane or tab.

You do not need to run `amq coop init` for the normal single-project flow.
Generated launch commands include `--session`, and AMQ creates the needed
mailbox directories on first launch. Use `amq coop init` or a hand-written
`.amqrc` only when you want a custom root, explicit project name, or
cross-project peer routing.

Non-interactive setup:

```sh
amq-squad team init --personas cpo,cto,senior-dev,frontend-dev,mobile-dev,qa,pm,designer
```

With per-persona CLI overrides:

```sh
amq-squad team init \
  --personas cto,junior-dev,qa \
  --binary junior-dev=codex,qa=claude \
  --session cto=stream1,junior-dev=stream2,qa=stream3
```

Members don't have to share a working directory. The dir where you run
`team init` becomes the team-home (where team.json and team-rules.md live);
individual members can live in other projects:

```sh
cd ~/Code/project-a
amq-squad team init \
  --personas cpo,cto,fullstack,qa \
  --cwd qa=~/Code/project-b
```

`team show` emits a `cd <member-cwd>` per command so every agent boots in the
right project. `team sync` walks each unique member cwd and syncs CLAUDE.md +
AGENTS.md in all of them.

Generated launch commands include these agent defaults: Codex gets
`--dangerously-bypass-approvals-and-sandbox`, and Claude gets
`--permission-mode auto`. These defaults are passed through after `--` while
the generated bootstrap prompt is still added at launch time. Direct
`amq-squad launch` calls also prepend these defaults when they are missing, so
custom prompts still inherit the expected permission mode. Pass
`--no-default-args` to opt out.

## Launching a team in tmux

If you are already working inside tmux, `team launch` opens the configured team
in the current tmux window:

```sh
amq-squad team launch
```

Defaults:

- `--terminal tmux`
- `--target current-window`
- `--layout vertical`
- `--stagger 750ms`

The current pane becomes the first agent pane. Additional team members are
opened as vertical side-by-side panes, pane titles are set to role names, and
agent launches are staggered to avoid flooding terminal control channels.

Preview the exact tmux script without creating panes:

```sh
amq-squad team launch --dry-run
```

Alternative layouts:

```sh
amq-squad team launch --layout horizontal
amq-squad team launch --layout tiled
```

To create a separate detached tmux session instead:

```sh
amq-squad team launch --target new-session
tmux attach -t amq-squad-<project>
```

When iTerm2 `tmux -CC` control-mode clients are detected, `amq-squad` warns
about the known pause-after risk. If input stalls in iTerm2 cc-mode, recover
from a non-tmux shell:

```sh
tmux list-clients
tmux detach-client -t /dev/ttysXXX
```

### Terminal backend plugins

`team launch` is wired as a small backend registry. The CLI handles shared
team loading, bootstrap flags, and common launch options, then dispatches to a
terminal backend by name. v0.4.0 ships the `tmux` backend.

Adding another terminal should be isolated to a new
`internal/cli/team_launch_<name>.go` file that implements `teamLaunchBackend`
and registers itself with `registerTeamLaunchBackend` from `init`.

Representative generated commands look like this:

```sh
cd ~/Code/my-project && /path/to/amq-squad launch \
  --role cto \
  --session cto \
  --team-home /Users/you/Code/my-project \
  --me cto \
  codex -- --dangerously-bypass-approvals-and-sandbox

cd ~/Code/my-project && /path/to/amq-squad launch \
  --role fullstack \
  --session fullstack \
  --team-home /Users/you/Code/my-project \
  --me fullstack \
  claude -- --permission-mode auto
```

At launch time, each agent's bootstrap prompt includes a current team routing
block generated from `.amq-squad/team.json`. That block is the live routing
source of truth: role, handle, session, project, cwd, and the appropriate
`amq send` shape from the agent's current project. Restorable AMQ history is
still useful context, but it should not be used as the active roster when it
conflicts with `team.json`.

## Built-in personas

Think of personas as employees in a small internal market. The persona is the
job you are hiring for; the CLI is the tool that employee runs on.

| ID             | Label                                | Default binary | Profile                                            |
|----------------|--------------------------------------|----------------|----------------------------------------------------|
| `cpo`          | CPO                                  | codex          | Product direction, scope pressure, user value.     |
| `cto`          | CTO                                  | codex          | Architecture, tradeoffs, final technical review.   |
| `senior-dev`   | Senior Developer                     | codex          | Takes harder code paths and reviews junior work.   |
| `fullstack`    | Fullstack Developer                  | claude         | End-to-end feature builder across UI and backend.  |
| `frontend-dev` | Frontend Developer                   | claude         | Product UI, components, state, browser polish.     |
| `backend-dev`  | Backend Developer                    | codex          | APIs, data flow, services, integrations.           |
| `mobile-dev`   | Mobile Developer                     | claude         | Native and mobile app flows, device polish.        |
| `junior-dev`   | Junior Developer                     | codex          | Fast on scoped tasks, needs review before merge.   |
| `qa`           | QA Manager                           | claude         | Regression thinking, release risk, test coverage.  |
| `pm`           | Project Manager / Product Owner      | claude         | Keeps work ordered, unblocked, and shippable.      |
| `designer`     | Product Designer                     | claude         | Product flows, visual shape, UI polish.            |

Defaults are starting points. Override binary or session per persona via flags at
`team init` time, or edit `.amq-squad/team.json` directly.

## Shared team rules

Team-wide norms ("every change ships via a PR", "CTO approves before merge")
live in a single file:

```text
.amq-squad/team-rules.md
```

Claude reads `CLAUDE.md`, Codex reads `AGENTS.md`. Rather than maintaining
both by hand, you edit `team-rules.md` and `amq-squad team sync` pushes the
content into a managed block in both files. Everything outside the markers is
yours and stays untouched.

```text
amq-squad team rules init        Seed missing .amq-squad/team-rules.md with a stub
amq-squad team sync              Preview what would change (exit 1 if drift)
amq-squad team sync --apply      Write the managed block into CLAUDE.md and AGENTS.md
```

On first `--apply` with an existing CLAUDE.md, your content is adopted as the
user region and the managed block is appended. Subsequent syncs only refresh
the managed block.

## Walkthroughs

### Squad in a single project

Two agents in one repo: CTO on codex, Fullstack on claude.

```sh
cd ~/Code/my-project
amq-squad team init --personas cto,fullstack
```

Open `.amq-squad/team-rules.md` and refine the generated role scope, workflow,
approvals, and communication norms for your team. Then push the
managed block into the doc files each binary reads:

```sh
amq-squad team sync          # preview (exit 1 if anything would change)
amq-squad team sync --apply  # writes the managed block into CLAUDE.md and AGENTS.md
```

Print the launch commands:

```sh
amq-squad team
```

You'll get two commands, one per role. Open separate terminal panes or tabs and
paste one command per pane, or run `amq-squad team launch` from inside tmux to
create the panes automatically. Each agent boots through `amq-squad launch`,
which writes `launch.json` + a catalog-seeded `role.md` into the mailbox before
handing off to `amq coop exec`. From there both agents share the thread
`p2p/cto__fullstack` for design escalations and review handoffs.

### Squad spanning two projects

One team, members in two repos: CTO and Fullstack in `project-a`, QA in
`project-b`. The team-home (`team.json` + `team-rules.md`) lives in
`project-a`.

Create AMQ project config in each project so cross-project peers have stable
project names. `amq coop init` is the easiest way to create the starter
`.amqrc` files:

```sh
(cd ~/Code/project-a && amq coop init)
(cd ~/Code/project-b && amq coop init)
```

For cross-project messages to route, each project's `.amqrc` needs a `peers`
entry pointing at the other project's `.agent-mail/`. Edit
`~/Code/project-a/.amqrc`:

```json
{
  "root": ".agent-mail",
  "project": "project-a",
  "peers": {
    "project-b": "/Users/you/Code/project-b/.agent-mail"
  }
}
```

Mirror it in `~/Code/project-b/.amqrc`:

```json
{
  "root": ".agent-mail",
  "project": "project-b",
  "peers": {
    "project-a": "/Users/you/Code/project-a/.agent-mail"
  }
}
```

This peers step is manual today; see the **Known gaps** section below.

Now pick the team from the team-home project:

```sh
cd ~/Code/project-a
amq-squad team init --personas cto,fullstack,qa --cwd qa=~/Code/project-b
```

Edit `~/Code/project-a/.amq-squad/team-rules.md`. Then sync. Because one member
lives elsewhere, sync walks both cwds and writes CLAUDE.md + AGENTS.md in each:

```sh
amq-squad team sync --apply
# Wrote 4 files: CLAUDE.md and AGENTS.md in project-a, same in project-b
```

Print the launch commands:

```sh
amq-squad team
```

You'll get three commands, each with the correct `cd <member-cwd>` so every
agent boots in the right repo. Open three panes and paste one per pane. QA's
codex or claude will live in `project-b`'s mailbox tree; CTO and Fullstack in
`project-a`'s. AMQ uses the `peers` config you set above to route messages
across.

The generated bootstrap for each role prints send commands relative to that
role's project. For example, a `project-a` agent sending to QA in `project-b`
will see a route shaped like:

```sh
amq send --to qa --project project-b --session qa
```

## Commands

```text
amq-squad team                      Smart default: show commands, or init if none exists
amq-squad team init [--personas ...]
                                    Pick personas, choose CLIs, and seed rules
amq-squad team show [--no-bootstrap]
                                    Print launch commands for the configured team
amq-squad team launch [--terminal tmux] [--target current-window|new-session]
                      [--layout vertical|horizontal|tiled] [--stagger 750ms]
                      [--no-bootstrap] [--dry-run]
                                    Open the configured team in tmux panes
amq-squad team rules init [--force] Seed or refresh .amq-squad/team-rules.md
amq-squad team sync [--apply]       Sync CLAUDE.md and AGENTS.md from team-rules.md

amq-squad launch --role <r> --session <s> --me <handle> [--conversation ref] [--no-bootstrap] [--no-default-args] <binary> [-- <flags>]
                                    Launch one agent. Writes launch.json + role.md
                                    in the AMQ mailbox, adds a bootstrap prompt,
                                    then execs 'amq coop exec'.
                                    Usually called by the output of 'team show'.
                                    Codex and Claude default permission flags
                                    are prepended when missing.
                                    --conversation stores a restore ref and
                                    translates it to Codex or Claude resume args.

amq-squad restore [--project dir1,dir2,...] [--conversation ref]
                                    Reconstruct launch commands from local
                                    launch.json history and nearby role.md
                                    persona files. Falls back to older AMQ
                                    mailbox history when launch.json is absent
                                    and the binary can be inferred.
amq-squad restore --exec --role cto Exec one selected local launch through
                                    amq coop exec.

amq-squad list [--json]             List restorable amq-squad records and
                                    AMQ-only inferred history across known
                                    projects.
```

## Files it writes

```text
<project>/.amq-squad/team.json           Team intent: which roles are on this squad.
<project>/.amq-squad/team-rules.md       Shared norms and workflow (user-edited).
<project>/CLAUDE.md, AGENTS.md           Managed block synced from team-rules.md;
                                         user content outside markers untouched.
<AM_ROOT>/agents/<handle>/launch.json    Per-agent invocation record, written at launch.
                                         Includes conversation ref when supplied.
<AM_ROOT>/agents/<handle>/role.md        Per-agent role doc, seeded from the catalog
                                         and never overwritten once it exists.
```

`<AM_ROOT>` is resolved via `amq env --json` so amq-squad and `amq coop exec` always agree on where the mailbox lives.

## Known gaps

- Sending a cross-session message from a setup terminal (outside any
  `amq coop exec`) has no clean idiom upstream. Tracked in
  [avivsinai/agent-message-queue#96](https://github.com/avivsinai/agent-message-queue/issues/96)
  with a proposed `--from-session` flag. Current workaround: boot your own
  session first, then send from inside it.
- Multi-cwd teams still need manual `peers` config in each project's `.amqrc`
  for cross-project AMQ routing. `team sync` doesn't touch `.amqrc`; that's
  left to the user until we're sure of the shape.

## Requires

- Go 1.25+
- `amq` binary in PATH (v0.32+)
- `tmux` in PATH for `amq-squad team launch`
