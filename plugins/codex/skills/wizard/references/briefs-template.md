# Brief Template

Canonical shape for the selected namespace's brief. Every member of the
workstream namespace reads this same file, so the brief stays short, concrete, and
**uniform regardless of where the goal came from** — a Jira ticket, a GitHub
issue or PR, a doc URL, a local `.md`, or a one-line operator prompt. The wizard
DRAFTS this from your source and shows it for confirmation before saving: a raw
ticket description is not a brief.

The brief lives at team-home and is **per profile/session namespace** — author or
refresh it at the start of each workstream, not only at first team creation.
Whether the live skill drafts it in-session or `start --goal` uses the configured
drafter, keep exactly the title and six level-two sections below, once each and
in order. Point at the source of truth; do not paste its full contents here.

The exact paths are:

- named profile `R`, session `S`: `P/.amq-squad/briefs/R/S.md`;
- default profile, session `S`: `P/.amq-squad/briefs/S.md`.

Do not save named-profile work at the default path: `start` will not read it.
`status --json` prints the resolved canonical path at
`data.namespace.paths.brief` (`data.goal_binding.brief_path` should agree), and
the start plan prints it on the `brief:` line. A live in-session draft is saved
with the current file-edit tool only after its bytes are approved, then displayed
in full with `cat PATH` before start.

---

# <session> brief

## Goal

The outcome in 1-2 sentences. What "done" looks like, not the task list.

## Source

Where this came from: `JIRA PROJ-123` / `gh#96` / `https://...` /
`file:./design.md` / "operator prompt". Link or id only — do not duplicate the
source body here. A binary-generated draft also identifies that it came from
the operator goal through the configured drafter and names the selected profile.

## Scope

- The concrete deliverables, code areas, or behaviors this workstream covers.

## Out of scope

- Nearby work that is explicitly NOT in this workstream, so members do not
  silently widen.

## Team shape

- One bullet per configured seat, preserving the exact role, handle, and binary
  tuple: ``- `<role>` (`<handle>`, `<binary>`): responsibility for this goal.``

## Acceptance

- How we know it is done (observable criteria).
- Who signs off (typically CTO; QA or CPO when relevant).
