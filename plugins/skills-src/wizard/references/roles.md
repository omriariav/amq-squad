# Roles, seats, and the squad.yaml draft

## Selecting roles

`amq-squad roles` lists the catalog roles with their default runtime, and the custom
roles staged under `.amq-squad/roles/`. Setup needs an explicit roster from the
operator: do not infer one from the goal text.

**The catalog is a menu, not a whitelist.** Name the role the workstream actually
needs; do not shoehorn work into the nearest catalog persona. One seat per role:
`--roles a,b` makes seats whose handle equals the role.

Actor mode changes what a seat may do: `implementation` seats edit files and count
toward the worktree-isolation rule; `review` seats do not. A squad where every seat
is mutation-capable needs one worktree per seat even when only one was going to
write code.

## Custom role personas

A custom role's persona lives at `.amq-squad/roles/<id>.md`. At launch it reaches
the seat only through the seat's `role.md`; the initial input carries a pointer to
it. Draft and stage one with:

```sh
amq-squad role draft researcher --purpose "Investigate ambiguous behavior" --project P
```

It uses the shared drafter (user config, else in-session), validates the draft and
stages it without overwriting; without a backend it prints the prompt only and stages
nothing. It never adds a seat: name the role in `team propose --roles` afterwards.

Keep a persona version-neutral and session-neutral. What a seat is pinned to changes
every session and belongs in the brief and the task, not in the role. A contract that
names issues, branches, or sessions goes stale the moment the next workstream starts.

## The squad.yaml draft form

The flags of `team propose` cover the seats, runtimes, models, efforts, worktrees,
tools, actor modes, lead, lead mode, execution mode, session and template. Every
other key of schema 1 goes in a draft file proposed with `--from`:

```yaml
schema: 1
session: feature-x
lead: lead
lead_mode: planner
mode: manual                  # narrows the owner's auto
max_seats: 3                  # at or under the owner cap
protected_paths:
  - docs/adr/
shared_cwd_exception: single checkout accepted for this short run
seats:
  - handle: lead
    role: cto
    runtime: claude
    actor_mode: review
  - handle: dev
    role: senior-dev
    runtime: codex
    worktree: ../feature-x-dev
    tools: coding
```

```sh
amq-squad team propose --from FILE --project P
```

The full key table, the narrowing rules and the owner-only keys are in
`docs/squad-yaml.md`. A pi seat takes no model or effort. A key the owner alone sets
(`merge`, `default_branch`, `verifiers`, `pins`, ...) is refused at parse with
`grants_invalid`, so a draft can never carry a push target or a verifier command.

## Templates

- `team-archetypes.md` — ready-made roster shapes and when each fits
- `briefs-template.md` — the workstream brief structure
- `pointer-stub-template.md` — the managed block `team apply` writes into
  `CLAUDE.md` and `AGENTS.md`

`team-rules.md` is rendered from squad.yaml by `team propose` (template `auto`,
`dev-only`, `product-squad`, `scrum` or `custom`). `team draft-rules` drafts the prose
fragment for a custom template; pass it back with `--rules-prose FILE`. The shape of
the rendered file is shown in `../../amq-squad/references/team-rules-template.md`.

## Ordering

Roles and their role files come before the proposal, because squad.yaml records each
seat's role, runtime, model, effort, tools, actor mode and worktree. Changing a seat
later is a new proposal through the same digest flow.
