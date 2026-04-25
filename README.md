# amq-squad

Role-aware agent team launcher built on top of [AMQ](https://github.com/avivsinai/agent-message-queue).

AMQ owns messaging between agents. `amq-squad` owns the layer above: who is in the team, what role each agent plays, and how to bring the whole squad back up after a restart.

## Why

AMQ's `coop exec` is a generic launcher. It sets up a mailbox and execs into `claude` or `codex`, but it doesn't know:

- Which agent is the "CPO" vs "CTO" vs "Fullstack" vs "QA" vs "PM" vs "Designer"
- What command you originally used to launch each one (cwd, binary, flags, session)
- What slash commands or skills that role leans on
- Which peers a given agent actively talks to

`amq-squad` captures this at team-setup time (`.amq-squad/team.json`) and per-agent at launch time (`launch.json` + `role.md` inside the AMQ mailbox). AMQ itself stays unchanged.

## Install

```
go install github.com/omriariav/amq-squad/cmd/amq-squad@latest
```

Requires Go 1.25+ and the `amq` binary in `PATH` (v0.32+). Installing to `$GOBIN` (or `$HOME/go/bin`) is enough; the launch commands `team show` emits use the absolute path to whichever `amq-squad` binary is running, so nothing else needs to be on `PATH`.

## Quick start

```
cd ~/Code/my-project
amq coop init          # one time, sets up .amqrc and .agent-mail/
amq-squad team         # pick roles, writes .amq-squad/team.json, prints launch commands
```

First time: you're prompted to pick which of the built-in roles should be on the team. A `.amq-squad/team.json` is written. Every subsequent run prints the launch commands, one per role. Paste each into its own terminal tab.

Non-interactive setup:

```
amq-squad team init --roles cpo,cto,fullstack,qa,pm,designer
```

With per-role overrides:

```
amq-squad team init \
  --roles cpo,fullstack,qa \
  --binary fullstack=codex \
  --session cpo=stream1,fullstack=stream2,qa=stream3
```

Members don't have to share a working directory. The dir where you run `team init` becomes the team-home (where team.json and team-rules.md live); individual members can live in other projects:

```
cd ~/Code/project-a
amq-squad team init \
  --roles cpo,cto,fullstack,qa \
  --cwd qa=~/Code/project-b
```

`team show` emits a `cd <member-cwd>` per command so every agent boots in the right project. `team sync` walks each unique member cwd and syncs CLAUDE.md + AGENTS.md in all of them.

## Built-in roles

| ID          | Label                                | Default binary | Notable skills                      |
|-------------|--------------------------------------|----------------|-------------------------------------|
| `cpo`       | CPO                                  | codex          | `/product-strategy`                 |
| `cto`       | CTO                                  | codex          |                                     |
| `fullstack` | Fullstack Developer                  | claude         |                                     |
| `qa`        | QA Manager                           | claude         |                                     |
| `pm`        | Project Manager / Product Owner      | claude         |                                     |
| `designer`  | Product Designer                     | claude         | `/frontend-design`, `/canvas-design`|

Defaults are starting points. Override binary or session per role via flags at `team init` time, or edit `.amq-squad/team.json` directly.

## Shared team rules

Team-wide norms ("every change ships via a PR", "CTO approves before merge") live in a single file:

```
.amq-squad/team-rules.md
```

Claude reads `CLAUDE.md`, Codex reads `AGENTS.md`. Rather than maintaining both by hand, you edit `team-rules.md` and `amq-squad team sync` pushes the content into a managed block in both files. Everything outside the markers is yours and stays untouched.

```
amq-squad team rules init        Seed .amq-squad/team-rules.md with a stub
amq-squad team sync              Preview what would change (exit 1 if drift)
amq-squad team sync --apply      Write the managed block into CLAUDE.md and AGENTS.md
```

On first `--apply` with an existing CLAUDE.md, your content is adopted as the user region and the managed block is appended. Subsequent syncs only refresh the managed block.

## Walkthroughs

### Squad in a single project

Two agents in one repo: CTO on codex, Fullstack on claude.

```
cd ~/Code/my-project
amq coop init                              # one time; writes .amqrc and .agent-mail/
amq-squad team init --roles cto,fullstack  # writes .amq-squad/team.json
amq-squad team rules init                  # seeds .amq-squad/team-rules.md
```

Open `.amq-squad/team-rules.md` and replace the TODO sections with your team's actual workflow, approvals, and communication norms. Then push the managed block into the doc files each binary reads:

```
amq-squad team sync          # preview (exit 1 if anything would change)
amq-squad team sync --apply  # writes the managed block into CLAUDE.md and AGENTS.md
```

Boot the whole team in one iTerm2 window (macOS + iTerm2 only):

```
amq-squad team open --layout horizontal   # or --layout vertical for side-by-side
```

That opens a new window, splits it one pane per role, and pastes each launch command. Each agent boots through `amq-squad launch`, which writes `launch.json` + a catalog-seeded `role.md` into the mailbox before handing off to `amq coop exec`. From there both agents share the thread `p2p/cto__fullstack` for design escalations and review handoffs.

Prefer to handle the panes yourself? `amq-squad team` prints the same commands to stdout so you can paste them manually.

### Squad spanning two projects

One team, members in two repos: CTO and Fullstack in `project-a`, QA in `project-b`. The team-home (`team.json` + `team-rules.md`) lives in `project-a`.

Initialize AMQ in each project so the mailbox trees exist:

```
(cd ~/Code/project-a && amq coop init)
(cd ~/Code/project-b && amq coop init)
```

For cross-project messages to route, each project's `.amqrc` needs a `peers` entry pointing at the other project's `.agent-mail/`. Edit `~/Code/project-a/.amqrc`:

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

```
cd ~/Code/project-a
amq-squad team init --roles cto,fullstack,qa --cwd qa=~/Code/project-b
amq-squad team rules init
```

Edit `~/Code/project-a/.amq-squad/team-rules.md`. Then sync. Because one member lives elsewhere, sync walks both cwds and writes CLAUDE.md + AGENTS.md in each:

```
amq-squad team sync --apply
# Wrote 4 files: CLAUDE.md and AGENTS.md in project-a, same in project-b
```

Print the launch commands:

```
amq-squad team
```

You'll get three commands, each with the correct `cd <member-cwd>` so every agent boots in the right repo. Open three panes and paste one per pane. QA's codex or claude will live in `project-b`'s mailbox tree; CTO and Fullstack in `project-a`'s. AMQ uses the `peers` config you set above to route messages across.

## Commands

```
amq-squad team                      Smart default: show commands, or init if none exists
amq-squad team init [--roles ...]   Set up this project's team
amq-squad team show                 Print launch commands for the configured team
amq-squad team rules init           Seed .amq-squad/team-rules.md
amq-squad team sync [--apply]       Sync CLAUDE.md and AGENTS.md from team-rules.md
amq-squad team open [--layout h|v]  Open every team member in one iTerm2 window
                                    (macOS + iTerm2 only; --dry-run prints the osascript)

amq-squad launch --role <r> --session <s> --me <handle> <binary> [-- <flags>]
                                    Launch one agent. Writes launch.json + role.md
                                    in the AMQ mailbox, then execs 'amq coop exec'.
                                    Usually called by the output of 'team show'.

amq-squad restore [--project dir1,dir2,...]
                                    Reconstruct launch commands from existing
                                    launch.json files (post-boot evidence, in
                                    contrast to team.json which is pre-boot intent).

amq-squad list [--json]             List registered agents across known projects.
```

## Files it writes

```
<project>/.amq-squad/team.json           Team intent: which roles are on this squad.
<project>/.amq-squad/team-rules.md       Shared norms and workflow (user-edited).
<project>/CLAUDE.md, AGENTS.md           Managed block synced from team-rules.md;
                                         user content outside markers untouched.
<AM_ROOT>/agents/<handle>/launch.json    Per-agent invocation record, written at launch.
<AM_ROOT>/agents/<handle>/role.md        Per-agent role doc, seeded from the catalog
                                         and never overwritten once it exists.
```

`<AM_ROOT>` is resolved via `amq env --json` so amq-squad and `amq coop exec` always agree on where the mailbox lives.

## Known gaps

- Sending a cross-session message from a setup terminal (outside any `amq coop exec`) has no clean idiom upstream. Tracked in [avivsinai/agent-message-queue#96](https://github.com/avivsinai/agent-message-queue/issues/96) with a proposed `--from-session` flag. Current workaround: boot your own session first, then send from inside it.
- Multi-cwd teams still need manual `peers` config in each project's `.amqrc` for cross-project AMQ routing. `team sync` doesn't touch `.amqrc`; that's left to the user until we're sure of the shape.

## Requires

- Go 1.25+
- `amq` binary in PATH (v0.32+)
