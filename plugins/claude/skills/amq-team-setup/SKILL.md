---
name: amq-team-setup
description: Wizard-style skill for first-time amq-squad team design and setup before `up`. Walks the user through capturing a work GOAL from any source (a one-line prompt, a local .md, a GitHub issue or PR, a Jira ticket, or a doc URL) and normalizing it into a canonical per-session brief, then picking roles/profile, optionally wiring the squad for lead-agent orchestration, and creating team.json + team-rules.md + the brief + pointer stubs. Covers team-home choice, persona/role drafting, profile model (default vs named), member cwds, the orchestration opt-in, `team sync` validation, and pre-launch dry-run checks. After the team is live, switch to the companion `amq-squad` skill.
allowed-tools: Bash, Read, Write, Edit, MultiEdit, Glob, Grep, WebFetch
argument-hint: "[goal | brief | roles | orchestrate | review | sync | preview]"
user-invocable: true
trigger: /amq-team-setup
---

# amq-team-setup

Use this skill once, before the team runs, to design and lock in the team's
shape: **what the work is** (the goal/brief), who is on the team, where they
work, what norms they share, and whether one agent leads the others. It is a
guided wizard: walk the steps in order and **confirm with the user at each
gate**. Setup ends when `amq-squad up --dry-run` looks right and
`amq-squad team sync` reports no drift. After that, switch to the `amq-squad`
skill for live ops.

This skill never replaces `amq-squad`. It targets the one-time decisions that
survive across workstreams, plus the per-session goal/brief that kicks off each
workstream.

## Context model

Setup creates and aligns four durable things; ongoing coordination consumes them.

- **Protocol** — the `amq-squad-orchestrator` skill (only when the squad is
  orchestrated): the lead-agent playbook over the runtime primitives.
- **Goal** — `.amq-squad/briefs/<session>.md`: the per-session brief. Lives at
  team-home so every member of the workstream reads the same file.
- **Norms** — `.amq-squad/team-rules.md`: durable team norms, the single source
  of truth. **Generated** by amq-squad; never hand-duplicated into other files.
- **Persona** — `<agent-dir>/role.md`: per-agent system prompt. Seeded on first
  `up`; the user can edit freely; later launches preserve user edits.

`CLAUDE.md` / `AGENTS.md` carry only a small managed pointer block:

```
<!-- amq-squad:managed:begin -->
... pointers to team-rules.md, role.md, briefs/<session>.md ...
<!-- amq-squad:managed:end -->
```

`amq-squad team sync --apply` writes and refreshes that block. Hand-editing
inside the markers is unsupported.

## The wizard

Run these five steps in order. Each step ends with an explicit confirmation
before moving on. Setup stays read-only until step 5 (no live launch).

### Step 1 — Capture the goal (any source)

Ask the user where the goal lives, then **fetch it with whatever tool you
have** and detect the source type. amq-squad core is tracker-neutral: all
fetching happens here in the skill, never as an amq-squad dependency.

| Source the user gives | How to fetch (agent-side) |
| --- | --- |
| A one-line prompt / inline text | use it directly as the Goal seed |
| A local file or path (`./design.md`, a path) | `Read` the file |
| A GitHub issue (`#96`, a URL, `owner/repo#96`) | `gh issue view <n> --json title,body` (Bash) |
| A GitHub PR (`#96`, a PR URL) | `gh pr view <n> --json title,body` (Bash) |
| A Jira key (`PROJ-123`) | Atlassian MCP `getJiraIssue` if available, else a `jira issue view PROJ-123` CLI, else ask the user to paste |
| A URL (Confluence / doc / spec) | Atlassian MCP `getConfluencePage` for Confluence, else `WebFetch`, else ask the user to paste |

If no integration is available for the source, say so plainly and ask the user
to paste the content rather than inventing it. Capture the source link/id — it
becomes the brief's `## Source` line.

### Step 2 — Draft and confirm the brief

Normalize whatever you fetched into the **canonical brief shape** from
`references/briefs-template.md`:

```md
# <session> brief
## Goal          # the outcome, 1-2 sentences
## Source        # JIRA PROJ-123 / gh#96 / URL / file: / "operator prompt"
## Scope
## Out of scope
## Acceptance     # how we know it's done; who signs off
```

A raw ticket description is **not** a brief — distill it. DRAFT the brief, then
SHOW it to the user and let them edit before accepting. Do not auto-accept a
raw ticket. Pick the workstream/session name now (lowercase `a-z`, `0-9`, `-`,
`_`; e.g. `issue-96`, `v1-7-0`); the brief is per-session and is saved to
`.amq-squad/briefs/<session>.md` in step 5.

You can also preview a candidate from a deterministic source with
`amq-squad up --dry-run --seed-from issue:<n>` / `file:./brief.md` /
`gh:owner/repo#<n>` (read-only) and paste it into the draft.

### Step 3 — Roles and profile

- Use `references/team-archetypes.md` as a starting menu (solo, pair, classic
  squad, design-led, qa-led).
- For each role decide binary (`codex` / `claude`), handle, cwd, and scope.
  Keep the roster minimal; you can add roles later by re-running `team init`.
- Custom (non-catalog) roles enter via `--role-file <path>` (comma-separated,
  or an inline path in `--roles`); author them with the
  `amq-squad-role-creator` skill. Two beats that matter in practice:
  - **Show, then create**: before creating the team, show the user every
    custom role's full body and let them edit — skill lists and hard rules
    get most of their fixes at this gate.
  - **Staging normalizes the file**: `new team --role-file` stages a copy
    under `.amq-squad/roles/<id>.md` with the YAML frontmatter absorbed into
    `team.json` and only the Markdown body kept. Expected behavior, not
    corruption; the authored source file is untouched.
- `--binary` takes ONE comma-separated list
  (`--binary copilot=claude,analyst=claude,...`). Repeating the flag does NOT
  accumulate — only one list survives, and the resulting error ("custom role X
  requires --binary x=<cli>") reads like a contradiction when you did pass it.
  A role file's `binary:` frontmatter satisfies the requirement for that role,
  so an all-role-file team usually needs no `--binary` at all.
- Choose the profile: default at `.amq-squad/team.json`, or a named profile at
  `.amq-squad/teams/<name>.json` for parallel team shapes (release vs research).
- **Per-member native args** (v1.8.0+): a member entry in `team.json` may
  carry `claude_args` / `codex_args` — extra native CLI args for that member
  only, appended after the team-level `binary_args` so the member value wins.
  The field must match the member's binary (`team sync` rejects a mismatch).
  Flagship use: a same-cwd squad where only the lead needs the full
  plugin/hook surface — give each worker
  `"claude_args": ["--settings", ".claude/agent-overlays/<role>.json"]`
  pointing at a Claude Code settings overlay (`enabledPlugins`,
  `disableAllHooks`, ...) that trims plugins and hooks the worker never uses,
  cutting its per-prompt context cost. Do not hand-edit this: step 5 generates
  and wires it with `amq-squad team overlay init` (v1.9.0+). Plan emission
  validates that every referenced `--settings` file exists.
- Pick the team-home (where `.amq-squad/` lives): the cwd by default; for a
  monorepo, usually the repo root. Confirm if the choice is non-obvious.

### Step 4 — Orchestrated? (opt-in, default off)

Ask: **"Run this as an orchestrated squad? If so, who leads?"**

- **Default is no.** A flat squad coordinates peer-to-peer over AMQ and needs no
  lead. Only opt in when one agent should spawn, dispatch, and monitor the
  others and own the deliverable.
- If **yes**, the user names exactly **one** lead role (a team member — commonly
  `cto`). The lead is an agent; it is **never** the operator/NOC handle.
- This is wired by a **structured flag**, not by hand-editing prose. In step 5
  you pass `--orchestrated --lead <role>` so amq-squad records the lead in
  `team.json` and the **generated** `team-rules.md` carries the orchestration
  reporting norm (the lead loads the `amq-squad-orchestrator` skill; children
  push `status`/`question`/`review_request` to the lead over AMQ). The norm is
  written when `team-rules.md` is first seeded; if the file already exists,
  `new team` leaves it untouched, so regenerate with `amq-squad team rules init
  --force` to pick up the orchestration norm. Do **not** hand-edit
  `team-rules.md` or `role.md` to describe orchestration — the flag owns it so
  it cannot drift.

### Step 5 — Review and create

Show the user a summary (team-home, roles + binaries, profile, workstream,
orchestrated?/lead, brief) and confirm. Then create:

1. **Team profile + rules** (one command writes both):

   ```sh
   amq-squad new team --roles cto,fullstack,qa --binary cto=codex \
     --session <workstream>
   # orchestrated variant:
   amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto \
     --session <workstream>
   ```

   Pass `--session <workstream>` explicitly — the session name you confirmed
   earlier does not apply itself. A brand-new profile has no member-session
   pin yet, so without the flag the workstream falls back to the sanitized
   team-home directory name, and the brief you write next lands under a
   session the team never boots into. (Once members carry a session pin,
   later resolution infers it from them.)

   `new team` writes `.amq-squad/team.json` and seeds the generated
   `.amq-squad/team-rules.md` **if it does not already exist** (an existing
   rules file is left untouched; use `amq-squad team rules init --force` to
   regenerate it). Preview first with `--dry-run` (add `--json` for
   the machine-readable `team_profile_plan`, which now carries
   `orchestrated`/`lead`). `--orchestrated [--lead ROLE]` is the orchestration
   opt-in from step 4; without `--lead`, a single-member team self-selects and a
   team with a `cto` defaults to `cto`.

2. **Worker overlays** (optional; ask whenever the team has two or more
   claude members — same-cwd squads are the flagship case): "Should the
   workers run with a trimmed plugin/hook surface?"
   Only the lead usually needs the full project configuration; workers on
   smaller-context models burn context on plugins and per-prompt hook output
   they never use. If yes, generate and wire the overlays in one command:

   ```sh
   amq-squad team overlay init --workers --disable-all-hooks \
     --disable-plugins <id@marketplace,...>
   # or one member at a time:
   amq-squad team overlay init --role <role> --disable-all-hooks
   ```

   This writes `.amq-squad/overlays/<role>.claude.json` (human-editable
   afterwards; re-runs never clobber it) and wires the member's
   `claude_args: ["--settings", <path>]` in `team.json`. `--workers` targets
   every claude member (on an orchestrated team the lead is excluded; a flat
   team has no lead to exclude). To find plugin ids for
   `--disable-plugins`, ask the user or check `claude plugin list` output.
   Codex members use the native equivalent instead: a
   `$CODEX_HOME/<name>.config.toml` profile wired via
   `codex_args: ["--profile", "<name>"]`.

3. **The brief**: save the confirmed step-2 draft to
   `.amq-squad/briefs/<session>.md` (a plain file write; the first live `up`
   preserves an existing brief). This kills the auto-stub the status board warns
   about.

4. **Pointer stubs**: `amq-squad team sync --apply` writes the managed block in
   `CLAUDE.md` / `AGENTS.md` (add `--sync` to `new team` to do it in one shot).

5. **Validate**: `amq-squad up --dry-run` (one launch command per member),
   `amq-squad team sync` (no drift), `amq-squad doctor` (AMQ / tmux / wake /
   markers).

6. **Print the next commands** and hand off:

   ```sh
   amq-squad up                 # bring all members up in the current tmux window
   amq-squad up --target new-window   # window-per-agent (preferred for a squad)
   ```

   First live launch belongs to the `amq-squad` skill (or, for an orchestrated
   squad, the lead drives spawn/dispatch/monitor via `amq-squad-orchestrator`).

   **Launch-name consistency (read before handing off):** the configured
   workstream applies only when the launch command carries NO `--session`
   override. Launching under a different free-typed name (e.g. in a NOC
   new-session form) boots a brand-new workstream with an auto-stub brief —
   the brief from this step is invisible to those agents. If the status board
   shows `(stub brief)` right after launch, the session name diverged: stop
   the squad, `rm` the accidental session, and relaunch with the configured
   workstream name explicitly (`amq-squad up <workstream>`).

## Verbs you will use

| Goal | Command |
| --- | --- |
| Create the default team profile + rules | `amq-squad new team --roles cto,fullstack,qa` |
| Create an orchestrated squad | `amq-squad new team --roles ... --orchestrated --lead cto` |
| Create a named profile | `amq-squad new profile release --roles cto` |
| Preview the profile without writing | `amq-squad new team --dry-run [--json] --roles ...` |
| Initialize interactively (prompts for roles/CLIs) | `amq-squad team init` |
| Seed/refresh `.amq-squad/team-rules.md` | `amq-squad team rules init` |
| Trim worker plugin/hook surface | `amq-squad team overlay init --workers [--disable-plugins id@market,...] [--disable-all-hooks]` |
| Preview pointer-stub drift | `amq-squad team sync` |
| Apply the pointer stub | `amq-squad team sync --apply` |
| Inspect the planned launch (dry-run) | `amq-squad up --dry-run [--json]` |
| Preview a candidate brief from a source | `amq-squad up --dry-run --seed-from issue:<n>` (or `file:./brief.md` / `gh:owner/repo#<n>`) |
| Diagnose environment / config / wake / markers | `amq-squad doctor` |

Most profile-aware commands accept `--profile NAME` to scope to
`.amq-squad/teams/<name>.json` (omit, or pass `--profile default`, for
`.amq-squad/team.json`). Exception: `new profile NAME` sets the profile via the
positional `NAME` and rejects an explicit `--profile`.

Global output flags work before or after the subcommand: `--quiet`,
`--verbose`, `--color auto|always|never`. `NO_COLOR` wins over `--color=always`.

## Rules

- One source of truth per layer. Team rules live only in
  `.amq-squad/team-rules.md`. Pointer stubs link, never copy.
- The brief is the goal layer and is **per-session**. Draft from the source,
  confirm with the user, then save to `.amq-squad/briefs/<session>.md`. A raw
  ticket is not a brief.
- amq-squad core is **tracker-neutral**: fetch Jira/Confluence/URLs with the
  agent's own tools here in the skill; amq-squad takes no tracker dependency.
- Orchestration is **opt-in, default off**. Exactly one lead; the lead is a team
  member, never the operator/NOC. Wire it with `--orchestrated --lead`, never by
  hand-editing `team-rules.md` / `role.md` prose.
- Workstream = AMQ `--session`. Session names are strict: lowercase `a-z`,
  digits, `-`, `_`. Use `v1-7-0`, not `v1.7.0`.
- Default profile is `default` (`.amq-squad/team.json`); named profiles live
  under `.amq-squad/teams/<name>.json` and are addressed with `--profile NAME`.
- New profiles default to an enabled virtual operator handle `user`. Use
  `--operator HANDLE` for a custom human-gate mailbox or `--no-operator` to opt
  out. Schema 1/2 profiles keep implicit `user` gates until rewritten.
- Setup never executes a live launch. Use `--dry-run` until the user explicitly
  approves going live; live launches happen via the `amq-squad` skill's `up`
  flow. (Writing the brief, the profile, and the overlays in step 5 are file
  writes, not a launch.)
- Codex trusted mode (`--trust trusted`) is the only path that prepends
  `--dangerously-bypass-approvals-and-sandbox`. The default `sandboxed` mode
  emits no implicit bypass; pick the mode deliberately if non-default.
- Do not touch `README.md`, `doc.html`, or unrelated repo files during setup.
  Stay inside `.amq-squad/`, `CLAUDE.md`, and `AGENTS.md`.

## References

- `references/briefs-template.md` - the canonical per-session brief shape
  (Goal / Source / Scope / Out of scope / Acceptance) the wizard normalizes to.
- `references/team-archetypes.md` - common team shapes (solo / pair / classic
  squad / design-led / qa-led).
- `references/pointer-stub-template.md` - the exact managed block written by
  `amq-squad team sync --apply` (for reference; do not hand-author).
- `../amq-squad/references/team-rules-template.md` - a static mirror of the
  team-rules content. The CLI generates `team-rules.md` from its own Go template
  (`amq-squad team rules init`); this file mirrors that output for reference.

## Exit codes

- `0` success
- `1` usage / user error (unknown flag, bad argument, missing required input)
- `2` system / runtime error (IO, process, config, environment)
- `3` partial success (some targets succeeded, some failed)

## Companion skills

- `amq-squad` - live coordination after setup: drains, routing, status
  board/console/history, up/stop/resume/fork/rm/archive (down is a deprecated
  alias), agent up/resume, doctor. Switch to it as soon as `up --dry-run` is
  clean and the user is ready to launch.
- `amq-squad-orchestrator` - the lead-agent playbook for an orchestrated squad
  (spawn / dispatch / monitor / the `[AGENT-EVENT]`-over-AMQ reporting protocol /
  recover). The lead loads it; this skill only wires the opt-in.
- `amq-squad-role-creator` - authoring a custom (non-catalog) role.
