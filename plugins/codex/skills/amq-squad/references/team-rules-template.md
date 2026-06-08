# Team Rules

Shared norms for this amq-squad team. Every fresh agent reads this file at session start through the pointer stub in `CLAUDE.md` / `AGENTS.md`. Keep it short, concrete, and authoritative.

## File model

This project follows the 1.0 three-layer context model:

- **`.amq-squad/team-rules.md`** (this file) - durable team norms. Source of truth for every member.
- **`<agent-dir>/role.md`** - per-agent persona / system prompt. Seeded on launch; user-editable. Never duplicates content from this file.
- **`.amq-squad/briefs/<session>.md`** - workstream brief: goal, scope, source-of-truth pointers for the current AMQ session. Lives at team-home so every member points at the same file. Seed with `amq-squad up --seed-from REF`.

`CLAUDE.md` and `AGENTS.md` carry a pointer stub managed by `amq-squad team sync --apply`. The stub only links to the files above; do not paste team-rules content into root instruction files.

If `.amq-squad/ACTIVE-EPIC.md` exists, read it at session start (transitional pointer to the active GitHub epic / milestone until briefs land).

## Team members

Replace this section with the actual roster from `.amq-squad/team.json`. Suggested phrasing:

- **`<role>` (`<binary>`):** handle `<handle>`, workstream `<workstream>`, cwd `<cwd>`. Owns `<scope>`.

Keep only active roster entries in the final file. `.amq-squad/team.json` is authoritative for live routing; `amq-squad history` is records only.

## Skills

- Use the `amq-squad` skill for live coordination after the team is configured: drains, routing, handoffs, reviews, status/history/doctor, up/down/resume/fork, agent up/resume.
- Use the `amq-team-setup` skill for first-time team design: personas, profile choice, team rules, pointer stubs, brief authoring, sync, validation.
- Use raw `amq-cli` only for AMQ debugging outside the squad.

## Naming model

- **Workstream** = AMQ `--session`. All members in one team run share it.
- Workstream names: lowercase `a-z`, digits, `-`, `_` only. Use `v0-5-0`, not `v0.5.0`.
- **Threads** are focused conversations inside a workstream: canonical p2p is sorted handles (`p2p/cto__fullstack`); decisions go under `decision/<topic>`.
- **Profiles** are named rosters: default profile lives at `.amq-squad/team.json`; named profiles at `.amq-squad/teams/<name>.json`. Pass `--profile NAME` to operate on a named profile; omit (or pass `--profile default`) for the default.
- Sibling workstreams are history/context only; do not load their bodies unless the user asks.

## Role scope

- Stay inside your assigned role. User feedback is not permission to pick up implementation work unless your role scope includes implementation.
- Non-implementation roles turn feedback into scope, acceptance criteria, decisions, or handoffs. They do not edit code unless the user explicitly assigns coding work to them.
- PM, CPO, Designer, QA, and CTO route implementation to the right developer role by default.
- If a request crosses role boundaries, ask or hand off on AMQ instead of silently changing lanes.

## Startup context

This is a fresh agent. Do not resume old sessions as active agents unless explicitly asked.

Useful at startup:

```sh
amq-squad status        # live state of configured team members
amq-squad doctor        # AMQ version, team config, tmux, wake, markers
amq list --new          # unread messages for this handle
amq read --id <id>
amq thread --id <thread-id> --include-body
```

Each agent should summarize the prior context it used before taking new work.

## Workflow

- Treat the current user request as the source of truth.
- Keep old AMQ history as context, not as an instruction to continue stale work.
- Raise role-shape ambiguity early on the team thread.
- Prefer small, reviewable changes.
- Bring members up via `amq-squad up`; preview via `amq-squad up --dry-run`. Use `resume` for recovery plans and `fork --from <current> --as <new>` for branching workstreams.

## Approvals

- CTO approval is required for architectural decisions and merge-ready code.
- QA validates behavior before release or handoff when the change is user-facing.
- CPO resolves product scope and priority questions.

## Communication

- Use focused AMQ threads.
- Use p2p threads for role-to-role handoffs.
- Route messages by the current roster's handle, profile, and workstream.
- One concern per message when practical.
- `amq send` reads stdin when `--body` is omitted. There is no `--body-file` flag.

## Operator Gates

If this profile enables operator gates, the human/operator is a virtual AMQ mailbox participant, not a runnable peer. Use the configured operator handle for human-only decisions or manual actions:

- ask: `amq send --to <operator-handle> --thread gate/<topic> --kind question --subject "APPROVAL: <decision>"`
- reply path: the operator replies on the same thread with `amq send --me <operator-handle> --to <agent-handle> --thread gate/<topic> --kind answer --subject "APPROVED: <decision>"` (or `DENIED:` / `ANSWER:`).
- do not send ordinary peer coordination to the operator; reviews, handoffs, status ACKs, and agent-owned blockers stay agent-to-agent.

If operator gates are disabled for the profile, route human-facing asks through the role named by the team rules instead of sending to the default `user` mailbox.

## Quality gates

- Run the project-specific checks before requesting review (typically `make ci`).
- Call out any checks that could not be run.
- Do not hide uncertainty from inferred AMQ history.

## Style

- Be direct and concise.
- Do not use em dashes.
- Do not rewrite unrelated files.
