---
name: amq-squad-team
description: Bootstrap or refresh an amq-squad team for the current project by default, including selecting personas, mapping agents across explicit project directories, discovering prior AMQ history in explicitly scoped folders, generating .amq-squad/team-rules.md, optionally syncing CLAUDE.md and AGENTS.md, and printing fresh launch commands. Use when the user asks to start a fresh agent team, set up cpo/cto/dev/qa personas, preserve context from old AMQ sessions, or generate team-rules for amq-squad. Default to the current working directory unless the user names one or more other folders.
allowed-tools: Bash, Read, Write, Edit, MultiEdit, Glob, Grep
argument-hint: "[team-home=current] [personas/cwds/history folders]"
user-invocable: true
trigger: /amq-squad-team
---

# AMQ Squad Team

Create a fresh amq-squad team without accidentally reusing old live sessions. Use old AMQ mailboxes as context sources only unless the user explicitly asks to restore an old session.

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
- `history folders`: one or more project directories to inspect with `amq-squad list` and mention in team rules.

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
   - Use fresh session names such as `fresh-cpo`, `fresh-cto`, `fresh-backend`, `fresh-qa`.

4. Generate team rules.
   - Run `amq-squad team rules init` if `.amq-squad/team-rules.md` does not exist.
   - Write a concise rules file tailored to the requested team.
   - Include exact active routes from `.amq-squad/team.json`: role, handle, session, project, and member cwd.
   - Include a startup-context section that names the old AMQ sessions to inspect.
   - State that old AMQ history is context only and must not override the active roster.
   - Preserve existing user rules unless the user asks to replace them.
   - Use `references/team-rules-template.md` as the starting template.

5. Sync only if requested or clearly desired.
   - `amq-squad team sync --apply` writes the managed block into `CLAUDE.md` and `AGENTS.md` in each member cwd.
   - If the user does not want sync, tell them to paste the startup context manually into each fresh agent.

6. Print fresh launch commands.
   - Run `amq-squad team show`.
   - Expect generated commands to include Codex `--dangerously-bypass-approvals-and-sandbox` and Claude `--permission-mode auto` after `--`.
   - Tell the user to paste one command into each terminal pane or tab.

## Command Pattern

For a four-agent team with CPO, CTO, backend/dev, and QA in a second project:

```sh
cd /path/to/team-home

amq-squad team init --force \
  --personas cpo,cto,fullstack,qa \
  --binary cpo=codex,cto=codex,fullstack=claude,qa=claude \
  --session cpo=fresh-cpo,cto=fresh-cto,fullstack=fresh-backend,qa=fresh-qa \
  --cwd qa=/path/to/qa-project

amq-squad team rules init
# edit .amq-squad/team-rules.md

amq-squad team show
```

## Team-Rules Content

Generate `.amq-squad/team-rules.md` with these sections:

- Team members, ownership, and exact active routes
- Startup context from previous AMQ history
- Workflow
- Approvals
- Communication
- Quality gates
- Style

Keep the generated file concrete. Name exact sessions and project paths when known. Use short bullets.

## Fresh Team Rule

For fresh teams, use old history for context, not execution:

- Good: `amq-squad list`, `amq list`, `amq read`, `amq thread --include-body`
- Good: `amq-squad restore` as a preview list
- Avoid: `amq-squad restore --exec` unless the user asks to resume an old agent
- Avoid: sending new work to a restorable legacy handle when `team.json` names a different current handle/session for that role

## Validation

After generating rules or team config:

```sh
amq-squad team show
amq-squad list
```

If repo files were edited, run the repo's normal checks if available.
