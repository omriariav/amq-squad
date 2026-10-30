# Brief Template

Canonical shape for a workstream brief. Every seat of the squad reads this same file,
so the brief stays short, concrete, and **uniform regardless of where the goal came
from**: a Jira ticket, a GitHub issue or PR, a doc URL, a local `.md`, or a one-line
operator prompt. A raw ticket description is not a brief.

The brief lives at `<repo root>/.amq-squad/briefs/<session>.md`, one per session.
Write it with `amq-squad brief`:

- `--goal TEXT` drafts it through the configured drafter and validates it; when the
  drafter needs in-session completion it prints the drafting prompt and stages
  nothing. Complete the prompt in the session, save the result, and seed it.
- `--seed-from file:<path>`, `issue:<n>` or `gh:owner/repo#<n>` seeds it without a
  drafter (the issue forms read GitHub through gh).
- `--dry-run` prints the candidate to stdout without writing; without `--force` an
  existing brief is never overwritten.

Keep the title and six level-two sections below, once each and in order. Point at the
source of truth; do not paste its full contents here.

---

# <session> brief

## Goal

The outcome in 1-2 sentences. What "done" looks like, not the task list.

## Source

Where this came from: `JIRA PROJ-123` / `gh#96` / `https://...` /
`file:./design.md` / "operator prompt". Link or id only; do not duplicate the source
body here.

## Scope

- The concrete deliverables, code areas, or behaviors this workstream covers.

## Out of scope

- Nearby work that is explicitly NOT in this workstream, so seats do not silently
  widen.

## Team shape

- One bullet per seat, preserving the exact role, handle, and runtime tuple:
  ``- `<role>` (`<handle>`, `<runtime>`): responsibility for this goal.``

## Acceptance

- How we know it is done (observable criteria).
- Who signs off (typically the lead; QA when present).
