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

## Quick start

```
cd ~/Code/my-project
amq-squad team
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

## Commands

```
amq-squad team                      Smart default: show commands, or init if none exists
amq-squad team init [--roles ...]   Set up this project's team
amq-squad team show                 Print launch commands for the configured team
amq-squad team rules init           Seed .amq-squad/team-rules.md
amq-squad team sync [--apply]       Sync CLAUDE.md and AGENTS.md from team-rules.md

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

## Requires

- Go 1.25+
- `amq` binary in PATH (v0.32+)
