# v1-0-0-reshape

- Epic: #31 — `gh issue view 31`
- Milestone: 1.0.0 — `gh issue list --milestone "1.0.0"`
- Branch: release/v1.0.0-reshape

## Scope

Ship amq-squad 1.0.0: unified team lifecycle verbs, CLAUDE.md pointer-stub
model, workstream brief convention, conversation handoff primitive, AMQ
feature adoption. 13 folded issues total — see the milestone.

## CTO's first job

Lock the 8 decisions in #31's "Decisions to lock on this thread" section
before any code lands. Fullstack and senior-dev wait on this.

Five revisions came out of an independent DX review (see the epic's "Review
trail" section). Push back on anything that still reads wrong.

## Implementation order

Defined in #31's "Implementation order" section. Don't skip ahead. Step 1
is new verb scaffolding plus `up --dry-run`. Docs (#33, #34) land late.
