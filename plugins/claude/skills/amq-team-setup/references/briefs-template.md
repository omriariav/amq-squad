# Brief Template

Canonical shape for the selected namespace's brief. Every member of the
workstream namespace reads this same file, so the brief stays short, concrete, and
**uniform regardless of where the goal came from** — a Jira ticket, a GitHub
issue or PR, a doc URL, a local `.md`, or a one-line operator prompt. The wizard
DRAFTS this from your source and shows it for confirmation before saving: a raw
ticket description is not a brief.

The brief lives at team-home and is **per profile/session namespace** — author or
refresh it at the start of each workstream, not only at first team creation.
Point at the source of truth; do not paste its full contents here.

---

# <session> brief

## Goal

The outcome in 1-2 sentences. What "done" looks like, not the task list.

## Source

Where this came from: `JIRA PROJ-123` / `gh#96` / `https://...` /
`file:./design.md` / "operator prompt". Link or id only — do not duplicate the
source body here.

## Scope

- The concrete deliverables, code areas, or behaviors this workstream covers.

## Out of scope

- Nearby work that is explicitly NOT in this workstream, so members do not
  silently widen.

## Acceptance

- How we know it is done (observable criteria).
- Who signs off (typically CTO; QA or CPO when relevant).
