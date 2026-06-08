# Workstream Brief Template

Starter content for `.amq-squad/briefs/<session>.md`. `amq-squad up --seed-from REF` writes a fully-populated brief from `file:`, `issue:`, or `gh:`; this template is for hand-authoring or for `file:./brief.md` seeds.

The brief lives at team-home so every member of the workstream reads the same file. Keep it short and concrete; rotate stale sections out as the workstream evolves.

---

# Workstream: <session>

One-line summary of what this workstream is for.

## Goal

What "done" looks like. 2-4 bullet points, outcomes, not tasks.

## Scope

- In scope: list the concrete deliverables, code areas, or behaviors.
- Out of scope: name nearby work that is not in this workstream so members do not silently widen.

## Source of truth

- Primary: `gh:<owner>/<repo>#<n>` or `file:./design/...` link.
- Related: linked issues, PRs, docs.

Do not duplicate the source-of-truth content here. Point at it.

## Acceptance

- What must be true before this workstream is considered done.
- Who signs off (typically CTO, sometimes QA or CPO).
- Any explicit non-goals.

## Constraints

- Hard constraints from the user, the platform, or the codebase.
- Known risks and how the team should escalate them.

## Members and threads

- Pull the current roster from `.amq-squad/team.json`. Do not re-declare member metadata here.
- Note any non-default thread names you expect to use (`decision/<topic>`, `p2p/<a>__<b>`).

## Style and review bar

- Project quality gates (typically `make ci`).
- Review bar (e.g. "CTO sign-off on architectural decisions; senior-dev on shipped code").

## Escalation

- Human escalations follow the team rules/profile operator policy.
- Agents do not interrupt the user directly during active work.
