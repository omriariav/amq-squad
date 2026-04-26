# Team Rules

Shared norms for this amq-squad team. Every fresh agent should read this before taking work.

## Team Members

- cpo (codex): handle `<handle>`, session `<session>`, project `<project>`. Owns product direction, priorities, and scope.
- cto (codex): handle `<handle>`, session `<session>`, project `<project>`. Owns technical direction, architecture, and final engineering sign-off.
- senior-dev (codex): handle `<handle>`, session `<session>`, project `<project>`. Owns complex implementation, code review, and technical mentorship.
- fullstack (claude): handle `<handle>`, session `<session>`, project `<project>`. Owns backend/dev implementation. Rename in prose if the user calls this "backend dev".
- frontend-dev (claude): handle `<handle>`, session `<session>`, project `<project>`. Owns browser UI implementation and frontend polish.
- backend-dev (codex): handle `<handle>`, session `<session>`, project `<project>`. Owns backend implementation, APIs, persistence, services, and integrations.
- mobile-dev (claude): handle `<handle>`, session `<session>`, project `<project>`. Owns mobile implementation, native flows, and device behavior.
- junior-dev (codex): handle `<handle>`, session `<session>`, project `<project>`. Owns narrow implementation tasks and needs review before work is ready.
- qa (claude): handle `<handle>`, session `<session>`, project `<project>`. Owns validation and regression checks. May run from a different project cwd.
- pm (claude): handle `<handle>`, session `<session>`, project `<project>`. Owns work ordering, clarification, coordination, and handoffs.
- designer (claude): handle `<handle>`, session `<session>`, project `<project>`. Owns product flows, UX, visual shape, and design assets.

Keep only active roster entries in the final rules file.

The current `.amq-squad/team.json` roster is authoritative for live routing.
Use old AMQ history only as context. Do not route new work to an inferred or
restorable legacy handle when it conflicts with the current roster.

## Role Scope

- Stay inside your assigned role. User feedback is not permission to pick up implementation work unless your role scope includes implementation.
- Non-implementation roles turn feedback into scope, acceptance criteria, decisions, or handoffs. They do not edit code unless the user explicitly assigns coding work to that role.
- PM, CPO, Designer, QA, and CTO should route implementation to the right developer role by default.
- Developer roles own code changes only after the work is scoped and routed to them.
- If a request crosses role boundaries, ask or hand off on AMQ instead of silently changing lanes.

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
- Route messages by the current roster's handle, project, and session.
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
