---
name: amq-squad
description: Project-aware skill for amq-squad managed teams. Prefer over amq-cli when .amq-squad/team.json exists or the user is doing team coordination, drains, reviews, handoffs, or launches. Covers team init/show/launch, AMQ workstreams, threads, role routing, inbox drains, acknowledgements, receipt waits, review requests, handoffs, decision threads, and team-rules generation. Use amq-cli only for raw AMQ debugging or non-squad AMQ usage.
---

# AMQ Squad

Use this skill for amq-squad team setup and live team communication. It is the project-specific layer on top of AMQ. Use generic `amq-cli` only for raw AMQ debugging or non-squad AMQ usage.

Launch priming is automatic. Agents do not paste the bootstrap block by hand; `amq-squad launch` injects it.

This skill is named `amq-squad`; the binary is also named `amq-squad`. The skill tells agents how to operate, while the binary changes files or launches agents.

## Rules

- Project folder: the repository or folder that owns `.amq-squad/team.json`.
- Team roster: the roles, handles, binaries, and cwd mappings in `.amq-squad/team.json`.
- Workstream: the AMQ `--session` used for one issue, release, branch, or focused piece of work.
- Thread: the focused conversation inside a workstream, for example `p2p/cto__fullstack` or `decision/<topic>`.
- Team profile: future named rosters for one folder. This is tracked separately from AMQ sessions and is not implemented yet.
- Prefer this `amq-squad` skill for team messaging, launch, handoff, and review workflows.
- Fall back to `amq-cli` only when debugging raw AMQ behavior not specified here.
- Launch all members of one team run into the same workstream.
- Keep `--session` reserved for AMQ workstream names.
- Use `--terminal-session` for tmux session names.
- Do not use `--session-from branch|worktree|issue|release`; it is not implemented.
- AMQ session names are strict: lowercase `a-z`, digits, `-`, and `_`. Use `v0-5-0`, not `v0.5.0`.
- Explicit session names are rejected when unsafe. Do not silently rewrite user input.
- Use canonical p2p thread names from sorted handles, for example `p2p/cto__fullstack`.
- Treat sibling workstreams as history/context only. Do not load their message bodies unless the user asks.

## Scope Defaults

- Default team-home is the current working directory.
- Default history scope is the current working directory only.
- Do not inspect or configure other repositories just because they appeared in prior conversation.
- Expand scope only when the user explicitly names folders, projects, member cwd mappings, or history sources.
- If the requested team spans folders, treat the first named primary project as team-home unless the user says otherwise.
- If scope is ambiguous and acting on the wrong repo could write files, ask for the team-home path before running `team init`.

Supported user inputs:

- `team-home`: one project directory that owns `.amq-squad/team.json` and `.amq-squad/team-rules.md`.
- `member cwd`: per-role working directories, emitted as `--cwd role=/path`.
- `workstream`: one shared AMQ session name, emitted as `--session <workstream>`.
- `history folders`: one or more project directories to inspect with `amq-squad list`.

## Workflow

1. Choose the team-home directory.
   - Default to the current working directory.
   - Prefer the project where most agents will work only when the user explicitly names several projects.
   - Use `--cwd role=/path/to/project` for members that work elsewhere.
   - Verify `.amqrc` peer config if members span projects.

2. Discover local history.
   - Run `amq-squad list` in the current directory by default.
   - Also run it in explicitly named history folders.
   - Treat `SOURCE=amq-squad` as exact launch history.
   - Treat `SOURCE=amq` as legacy AMQ mailbox history. It can show session, handle, and inferred role, but may not know role, args, or persona.
   - Do not run `amq-squad restore --exec` for a fresh team.

3. Create or update the team.
   - Use built-in persona IDs when possible: `cpo`, `cto`, `senior-dev`, `fullstack`, `frontend-dev`, `backend-dev`, `mobile-dev`, `junior-dev`, `qa`, `pm`, `designer`.
   - Model "works fast but needs review" as `junior-dev`.
   - Model web UI work as `frontend-dev`; model mobile app work as `mobile-dev`; model APIs/services work as `backend-dev`.
   - Model backend/dev as `fullstack` unless the user wants a narrower persona.
   - Use `--binary persona=cli` when the user wants a persona on a different CLI, for example `--binary fullstack=codex`.
   - Use one shared workstream name with `--session <workstream>`. Do not use the removed comma-separated `role=name` session syntax.
   - If the user does not name a workstream, let amq-squad default to the sanitized team-home directory name.

4. Generate team rules.
   - Run `amq-squad team rules init` if `.amq-squad/team-rules.md` does not exist.
   - Write a concise rules file tailored to the requested team.
   - Include exact active routes from `.amq-squad/team.json`: role, handle, workstream, project, member cwd, and canonical thread when relevant.
   - Include a startup-context section that names old AMQ workstreams to inspect only when the user asked for old context.
   - State that old AMQ history is context only and must not override the active roster.
   - Preserve existing user rules unless the user asks to replace them.
   - Use `references/team-rules-template.md` as the starting template.

5. Sync only if requested or clearly desired.
   - `amq-squad team sync --apply` writes the managed block into `CLAUDE.md` and `AGENTS.md` in each member cwd.
   - If the user does not want sync, tell them to paste the startup context manually into each fresh agent.

6. Print launch commands.
   - Run `amq-squad team show --session <workstream>` when the user named a workstream.
   - Run `amq-squad team launch --session <workstream>` from tmux when the user wants panes created automatically.
   - Use `--fresh --session <workstream>` when accidental reuse should fail.
   - Expect generated commands to include `--team-workstream`, Codex `--dangerously-bypass-approvals-and-sandbox`, and Claude `--permission-mode auto`.
   - Tell the user to paste one command into each terminal pane or tab when not using `team launch`.

## Command Pattern

For a four-agent team with CPO, CTO, backend/dev, and QA in a second project:

```sh
cd /path/to/team-home

amq-squad team init --force \
  --personas cpo,cto,fullstack,qa \
  --binary cpo=codex,cto=codex,fullstack=claude,qa=claude \
  --session v0-5-0 \
  --cwd qa=/path/to/qa-project

amq-squad team rules init
# edit .amq-squad/team-rules.md if needed

amq-squad team show --session v0-5-0
```

## AMQ Usage Notes

- Inside `amq coop exec`, prefer bare `amq` commands. Do not override `--me` or `--root` unless intentionally changing identity or mailbox.
- `amq send` has `--body`, not `--body-file`. Omit `--body` to read the message body from stdin, or use `--body @file`.
- Same-project role handoffs should use the shared workstream and a canonical p2p thread:

```sh
amq send --to fullstack --thread p2p/cto__fullstack --kind review_request
```

- Cross-session sends need an explicit `--session` and `--thread`. Avoid them in normal amq-squad team flow unless the user is intentionally contacting another workstream.
- `.amq-squad/team.json` is authoritative for the current roster. `amq-squad restore` output is history until the user explicitly asks to resume it.

## Live Ops

Inbox triage:

```sh
amq list --new
amq read --id <id>
amq drain --include-body
```

For synchronous handoffs, wait for the peer to drain the message:

```sh
amq send --to fullstack --thread p2p/cto__fullstack --kind review_request --subject "Review: workstream session model" --body "Please review the branch, tests, and problem framing." --wait-for drained --wait-timeout 60s
```

The current AMQ CLI exposes receipt waits through `--wait-for drained` and does not expose an `amq ack` subcommand. Human acknowledgements are normal `amq send` or `amq reply` messages.

After acting, acknowledge only when useful. Keep the acknowledgement one line and factual:

```sh
amq send --to fullstack --thread p2p/cto__fullstack --subject ack --body "Reviewed the update and will run final checks."
```

Common send patterns:

```sh
amq send --to cto --thread p2p/cto__fullstack --kind review_request --subject "Review: workstream session model" --body "Please review the branch, tests, and problem framing."
# Swap --kind and --thread for task, decision, status, question, answer, or review_response.
```

When handling incoming messages:

- Read the message body before acting.
- If it asks for review, use review stance: findings first, then questions, then summary.
- If it asks for implementation, confirm scope against current user intent and code state.
- If it is only a wake or FYI, acknowledge briefly after incorporating it.
- If it conflicts with the latest user instruction, follow the user and tell the peer what changed.
- If a new message arrives mid-task, finish or pause cleanly, then acknowledge before redirecting.

## Team-Rules Content

Generate `.amq-squad/team-rules.md` with these sections:

- Skill policy: use `amq-squad` for team coordination; use `amq-cli` only for raw AMQ debugging or non-squad AMQ usage
- Team members, ownership, and exact active routes
- Role scope and boundaries for each selected persona
- Startup context from previous AMQ history, when requested
- Workflow
- Approvals
- Communication
- Quality gates
- Style

Keep the generated file concrete. Name exact workstreams and project paths when known. Use short bullets. Make role boundaries explicit: PM, CPO, Designer, QA, and CTO route implementation to developer roles by default instead of editing code themselves.

## Fresh Team Rule

For fresh teams, use old history for context, not execution:

- Good: `amq-squad list`, `amq list`, `amq read`, `amq thread --include-body`
- Good: `amq-squad restore` as a preview list
- Avoid: `amq-squad restore --exec` unless the user asks to resume an old agent
- Avoid: sending new work to a restorable legacy handle when `team.json` names a different current handle/session for that role
- Avoid: loading sibling workstream message bodies by default

## Validation

After generating rules or team config:

```sh
amq-squad team show --session <workstream>
amq-squad team launch --session <workstream> --dry-run
amq-squad list
```

`amq-squad list` has useful output only after at least one agent has launched.

If repo files were edited, run the repo's normal checks if available.
