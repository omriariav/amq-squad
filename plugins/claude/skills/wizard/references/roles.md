# Roles, contracts, and templates

## Selecting roles

Built-in role ids come from the binary's role catalog. The wizard does not
infer a roster from the goal text: creating a new profile requires an
explicit `--roles`, plus whatever `--binary`/`--actor-mode` details the
operator supplies on the same invocation. Suggesting a roster from a goal is
tracked separately as a future, off-launch-path verb (gh#790), not part of
wizard's own combined, launch-capable flow.

**The catalog is a menu, not a whitelist.** Any slug is a valid role when the
wizard receives an explicit binary for it. Invent the role the workstream
actually needs; do not shoehorn work into the nearest catalog persona.

Actor mode is the one that changes launch preflight: a member marked `implementation` is
mutation-capable and counts toward the worktree-isolation check, while `review` does
not. A roster where every member defaults to implementation will block on shared
directories even when only one member was ever going to write code.

## Custom role personas

A custom role's persona lives at `.amq-squad/roles/<id>.md` in the project, listed by
`amq-squad roles` under "Custom roles". Markdown with optional YAML frontmatter
(`id`, `label`, `binary`, `description`, `skills`, `peers`) read for roster
metadata; at launch the staged file seeds the agent's `role.md` verbatim,
frontmatter included. Metadata-only `.yaml`/`.json` shapes also parse for
externally supplied role files passed to `--roles`.

The file is optional. A role with no file launches with a generated neutral contract
that defers scope to team rules, the brief, and the durable task — so a missing
persona never blocks adding the seat. Write the file when the seat needs a real
persona, and keep it version-neutral and session-neutral: what a seat is pinned to
changes every session and belongs in the brief and the durable task, not in the
contract.

A contract that names issues, branches, or sessions goes stale the moment the next
workstream starts, and a stale contract is worse than a thin one because it reads as
current.

When a richer persona is worth the setup cost, author it BEFORE naming the
role in `--roles`, with the dedicated `amq-squad role draft` command --
wizard does not draft custom-role personas itself:

```sh
amq-squad role draft researcher --binary codex \
  --purpose "Investigate ambiguous product behavior" \
  --project P --profile R --session S
```

If an active brief already exists it is attached as untrusted context; the
prompt explicitly defers live scope to the future brief and durable task.
The complete profile `drafter` block overrides the global user block;
otherwise global config wins, then `in_session`. Preset backends select
yoetz, `claude -p`, or `codex exec`; custom argv is trusted from global
config only. The command owns the prompt, template, and validation: a draft
must have matching frontmatter, the Mission/Boundaries/Protocol shape, fewer
than 45 lines, and no active session, task id, version, or branch. It never
adds or launches the member -- review the staged file, then either name the
role in `--roles` for a new profile or run `team member add` for an existing
one.

If no external backend is configured, or a keyless backend falls back,
`role draft` prints the filled manual prompt and writes nothing. Successful,
fallback, fail-closed, and invalid-output paths report the config source and
complete ordered attempt evidence. Wherever wizard proceeds with a custom
role that has no staged persona doc yet, its own preview names this exact
command as a notice, so the replacement is discoverable from the run itself,
not just from docs. For a short-lived seat, the fastest persona remains none
at all: use the generated neutral contract and a precise durable task body.

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

Roles are selected before the profile bytes are rendered, because the profile
records per-member binary, model, effort, tool policy, actor mode, and working
directory. Changing a role on an existing roster routes to `amq-squad:cli`, not
back through new-profile setup.
