# Roles, contracts, and templates

## Selecting roles

Built-in role ids come from `amq-squad roles`, which prints the id, default binary, and
what each role is for. Pass them with `--roles`, and override binaries, models, effort,
or actor mode per role rather than accepting defaults you did not choose.

**The catalog is a menu, not a whitelist.** Any slug is a valid role: pass it to
`--roles` (with `--binary <role>=<cli>`, or via a role file carrying a `binary:`
field) or to `team member add <role> --binary <cli>`, and it becomes a first-class
member. `--roles` also accepts a role-file path directly (`--roles ./researcher.md`).
Invent the role the workstream actually needs; do not shoehorn work into the nearest
catalog persona.

Actor mode is the one that changes launch preflight: a member marked `implementation` is
mutation-capable and counts toward the worktree-isolation check, while `review` does
not. A roster where every member defaults to implementation will block on shared
directories even when only one member was ever going to write code.

## Custom role personas

A custom role's persona lives at `.amq-squad/roles/<id>.md` in the project, listed by
`amq-squad roles` under "Custom roles". Markdown with optional YAML frontmatter
(`id`, `label`, `binary`, `description`, `skills`, `peers`); the body becomes the
agent's `role.md` verbatim. Metadata-only `.yaml`/`.json` shapes also parse.

The file is optional. A role with no file launches with a generated neutral contract
that defers scope to team rules, the brief, and the durable task — so a missing
persona never blocks adding the seat. Write the file when the seat needs a real
persona, and keep it version-neutral and session-neutral: what a seat is pinned to
changes every session and belongs in the brief and the durable task, not in the
contract.

A contract that names issues, branches, or sessions goes stale the moment the next
workstream starts, and a stale contract is worse than a thin one because it reads as
current.

## Templates

- `team-archetypes.md` — ready-made roster shapes and when each fits
- `briefs-template.md` — the workstream brief structure preparation writes
- `pointer-stub-template.md` — the managed block `team sync --apply` writes into
  `CLAUDE.md` and `AGENTS.md`, shown so a reviewer knows the expected shape

One more template lives at `../../amq-squad/references/team-rules-template.md`: the
team-rules starting point. It stays there because the binary's tests pin that exact
path, so moving it would require changing Go. It is linked from here so it is reachable
from the skill that actually composes team rules.

## Ordering

Roles are selected before the profile stage, because the profile records per-member
binary, model, effort, tool policy, and working directory. Changing a role after the
profile is written is a `team member update`, not a re-run of the roles stage.
