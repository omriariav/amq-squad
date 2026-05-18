---
name: amq-team-setup
description: Project-aware skill for first-time amq-squad team design and setup before `up`. Covers team-home choice, persona/role drafting, profile model (default vs named), member cwds, team rules, pointer stubs in CLAUDE.md/AGENTS.md, workstream brief authoring, `team sync` validation, and pre-launch dry-run checks. After the team is live, switch to the companion `amq-squad` skill for ongoing coordination.
allowed-tools: Bash, Read, Write, Edit, MultiEdit, Glob, Grep
argument-hint: "[design | init | rules | brief | stubs | sync | preview]"
user-invocable: true
trigger: /amq-team-setup
---

# amq-team-setup

Use this skill once, before the team runs, to design and lock in the team's shape: who is on the team, where they work, what norms they share, and what the first workstream is. Setup ends when `amq-squad up --dry-run` looks right and `amq-squad team sync` reports no drift. After that, switch to the `amq-squad` skill for live ops.

This skill never replaces `amq-squad`. It targets the one-time decisions that survive across workstreams.

## What this skill owns

- **Team-home choice.** Where `.amq-squad/` lives. By default the cwd; for monorepos with multiple workstreams the project root is usually right.
- **Personas and roles.** Picking which roles exist (cto, fullstack, qa, designer, etc.) and which binary (`codex` or `claude`) backs each.
- **Profile model.** Default profile at `.amq-squad/team.json` vs. named profiles at `.amq-squad/teams/<name>.json` for parallel team shapes (e.g. release vs. research).
- **Member cwds.** Each role runs its agent process from a specific directory; QA may live in a different repo.
- **Team rules.** Authoring `.amq-squad/team-rules.md` from the sibling `../amq-squad/references/team-rules-template.md`. Source of truth for the whole team.
- **Pointer stubs.** Writing/refreshing the managed block in `CLAUDE.md` / `AGENTS.md` via `amq-squad team sync --apply`.
- **Workstream brief.** Previewing candidate brief content with `amq-squad up --dry-run --seed-from file:` / `issue:` / `gh:` and, when staying read-only, hand-authoring `.amq-squad/briefs/<session>.md`. The live form (`up --seed-from REF`) writes the brief and brings the team up in one call, so it belongs to the first-launch handoff in step 8.
- **Pre-launch validation.** `amq-squad up --dry-run`, `amq-squad team sync`, `amq-squad doctor` before any live launch.

After all of that, hand off to the `amq-squad` skill for drains, routing, status/history, resume, fork.

## Context model

The 1.0 model is three durable layers; setup creates and aligns them, ongoing coordination consumes them.

- **`.amq-squad/team-rules.md`** - durable team norms. Single source of truth. Never duplicated into other files.
- **`<agent-dir>/role.md`** - per-agent persona / system prompt. Seeded on first `up`; the user can edit freely; later launches preserve user edits.
- **`.amq-squad/briefs/<session>.md`** - workstream brief for one AMQ session. Lives at team-home so every member points at the same file.

`CLAUDE.md` / `AGENTS.md` carry only a small managed pointer block:

```
<!-- amq-squad:managed:begin -->
... pointers to team-rules.md, role.md, briefs/<session>.md ...
<!-- amq-squad:managed:end -->
```

`amq-squad team sync --apply` writes and refreshes that block. Hand-editing the markers is unsupported.

## Verbs you will use

| Goal | Command |
| --- | --- |
| Initialize default profile (interactive) | `amq-squad team init` |
| Initialize a named profile | `amq-squad team init --profile NAME` |
| List configured profiles | `amq-squad team profiles` |
| Seed/refresh `.amq-squad/team-rules.md` | `amq-squad team rules init` |
| Preview pointer-stub drift in CLAUDE.md/AGENTS.md | `amq-squad team sync` |
| Apply the pointer stub | `amq-squad team sync --apply` |
| Inspect the planned launch (dry-run) | `amq-squad up --dry-run` |
| Inspect with machine-readable envelope | `amq-squad up --dry-run --json` |
| Preview a candidate workstream brief from a deterministic source | `amq-squad up --dry-run --seed-from file:./brief.md` (or `issue:31` / `gh:owner/repo#31`) |
| Author the brief by hand from `references/workstream-brief-template.md` | save to `.amq-squad/briefs/<session>.md` (no CLI write) |
| Diagnose environment / config / wake / markers | `amq-squad doctor` |

`--profile NAME` scopes any of the above to `.amq-squad/teams/<name>.json`. Omit (or pass `--profile default`) for `.amq-squad/team.json`.

Global output flags work before or after the subcommand: `--quiet`, `--verbose`, `--color auto|always|never`. `NO_COLOR` wins over `--color=always`.

## Setup workflow

1. **Pick team-home.**
   - Default: the cwd where you run `team init`.
   - Monorepo: usually the repo root, not a subpackage.
   - Confirm with the user before locking in if the choice is non-obvious.

2. **Sketch the team shape.**
   - Use `references/team-archetypes.md` as a starting menu (solo, pair, classic squad, design-led, qa-led).
   - For each role decide: binary (`codex` / `claude`), handle, cwd, scope.
   - Keep the roster minimal; you can add roles later by re-running `team init`.

3. **Initialize the profile.**
   - Default profile: `amq-squad team init` writes the team profile at `.amq-squad/team.json` and ensures `.amq-squad/team-rules.md` exists.
   - Named profile: `amq-squad team init --profile release` writes `.amq-squad/teams/release.json`.
   - Per-agent `role.md` is not created by `team init`. The first `amq-squad up` (or `agent up`) seeds `role.md` inside the AMQ agent directory (`.agent-mail/<session>/agents/<handle>/role.md`); later launches preserve user edits.

4. **Author team rules.**
   - `amq-squad team rules init` seeds `.amq-squad/team-rules.md` from the sibling `../amq-squad/references/team-rules-template.md` (no local copy lives under this skill).
   - Fill in concrete roster lines (do not leave `<role>` placeholders), declare role boundaries, and confirm escalation routes through CTO.

5. **Apply the pointer stub.**
   - `amq-squad team sync` previews drift between desired stub and `CLAUDE.md` / `AGENTS.md` (exit 1 on drift).
   - `amq-squad team sync --apply` writes the managed block. Do not hand-edit inside the markers.

6. **Prepare the workstream brief (read-only).**
   - Setup stays read-only: do not run `amq-squad up --seed-from REF` here, since the live form writes the brief and brings the team up in the same call.
   - Choose a deterministic source: `file:./brief.md`, `issue:<n>` (current repo), or `gh:<owner>/<repo>#<n>`.
   - Preview the candidate envelope: `amq-squad up --dry-run --seed-from issue:31` (or `--json` for the machine-readable shape).
   - If you want the brief saved before first live launch, hand-author `.amq-squad/briefs/<session>.md` from `references/workstream-brief-template.md` (you can paste the dry-run candidate content). The first live `up` preserves an existing brief.

7. **Pre-launch validation.**
   - `amq-squad team sync` - no drift expected.
   - `amq-squad up --dry-run` - inspect the launch plan: one launch command per member, expected cwd, role, workstream.
   - `amq-squad doctor` - AMQ version / tmux / wake / markers.

8. **Hand off to the `amq-squad` skill for first live launch.**
   - Once dry-run looks right and the user explicitly approves going live, the first live launch belongs to the `amq-squad` skill. `amq-squad up --seed-from REF` writes `.amq-squad/briefs/<session>.md` and brings the team up in one call (use `--force` to overwrite an existing brief). If a brief was hand-authored in step 6, plain `amq-squad up` preserves it.
   - Everything after first launch (drains, routing, status/history, down/resume/fork, agent up/resume, doctor) also lives in the `amq-squad` skill.

## Rules

- One source of truth per layer. Team rules live only in `.amq-squad/team-rules.md`. Pointer stubs link, never copy.
- Workstream = AMQ `--session`. Session names are strict: lowercase `a-z`, digits, `-`, `_`. Use `v0-5-0`, not `v0.5.0`.
- Default profile is `default` (`.amq-squad/team.json`); named profiles live under `.amq-squad/teams/<name>.json` and are addressed with `--profile NAME`.
- Setup never executes a live launch. Use `--dry-run` until the user explicitly approves going live; live launches happen via the `amq-squad` skill's `up` flow.
- Do not touch `README.md`, `doc.html`, or unrelated repo files during setup. Stay inside `.amq-squad/`, `CLAUDE.md`, and `AGENTS.md`.
- User escalations route through the role the team rules name (default: `cto`). Setup-time decisions that need user input also flow through CTO; do not ping the user directly during multi-agent setup.

## References

- `references/team-archetypes.md` - common team shapes (solo / pair / classic squad / design-led / qa-led).
- `references/workstream-brief-template.md` - starter content for `.amq-squad/briefs/<session>.md`.
- `references/pointer-stub-template.md` - the exact managed block written by `amq-squad team sync --apply` (for reference; do not hand-author).
- `../amq-squad/references/team-rules-template.md` - the team-rules template applied by `amq-squad team rules init`.

## Exit codes

- `0` success
- `1` usage / user error (unknown flag, bad argument, missing required input)
- `2` system / runtime error (IO, process, config, environment)
- `3` partial success (some targets succeeded, some failed)

## Companion skill

Live coordination after setup - drains, routing, status/history, up/down/resume/fork, agent up/resume, doctor - belongs to the `amq-squad` skill. Switch to it as soon as `up --dry-run` is clean and the user is ready to launch.
