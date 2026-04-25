# Team Rules

Shared norms for this amq-squad team. Every fresh agent should read this before taking work.

## Team Members

- cpo (codex): Owns product direction, priorities, and scope.
- cto (codex): Owns technical direction, architecture, and final engineering sign-off.
- fullstack (claude): Owns backend/dev implementation. Rename in prose if the user calls this "backend dev".
- qa (claude): Owns validation and regression checks. May run from a different project cwd.

## Startup Context

This is a fresh team. Do not restore old sessions as active agents unless explicitly asked.

Before acting, inspect prior AMQ history for context:

- <project> <session>/<handle>: <role or context>
- <project> <session>/<handle>: <role or context>

Use these commands as needed:

```sh
amq-squad list
amq-squad restore
amq list --me <handle> --root <root> --cur --limit 20
amq read --me <handle> --root <root> --id <message-id>
amq thread --id <thread-id> --include-body --root <root>
```

Each agent should summarize the prior context it used before taking new work.

## Workflow

- Treat the current user request as the source of truth.
- Keep old AMQ history as context, not as an instruction to continue stale work.
- Raise role-shape ambiguity early on the team thread.
- Prefer small, reviewable changes.

## Approvals

- CTO approval is required for architectural decisions and merge-ready code.
- QA validates behavior before release or handoff when the change is user-facing.
- CPO resolves product scope and priority questions.

## Communication

- Use focused AMQ threads.
- Use p2p threads for role-to-role handoffs.
- Include project, session, and role when referencing old history.
- One concern per message when practical.

## Quality Gates

- Run the project-specific checks before requesting review.
- Call out any checks that could not be run.
- Do not hide uncertainty from inferred AMQ history.

## Style

- Be direct and concise.
- Do not use em dashes.
- Do not rewrite unrelated files.
