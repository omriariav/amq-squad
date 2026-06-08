# Team Archetypes

A short menu of common team shapes for `amq-squad team init`. Pick one as a starting point and adapt; the roster is not fixed by the tool. Roles in *italics* are optional add-ons.

## Solo

One agent doing implementation, one human steering.

| Role | Binary | Handle | Owns |
| --- | --- | --- | --- |
| fullstack | claude | `fullstack` | All implementation in cwd. |

Useful when the human is acting as cto/qa/pm themselves.

## Pair (dev + reviewer)

| Role | Binary | Handle | Owns |
| --- | --- | --- | --- |
| fullstack | claude | `fullstack` | Implementation. |
| cto | codex | `cto` | Architecture, code review, sign-off. |

Smallest team that benefits from AMQ handoffs. Typical p2p thread: `p2p/cto__fullstack`.

## Classic squad

| Role | Binary | Handle | Owns |
| --- | --- | --- | --- |
| cto | codex | `cto` | Architecture, sign-off, escalation to user. |
| fullstack | claude | `fullstack` | End-to-end implementation. |
| qa | claude | `qa` | Validation, regression checks. Often a different cwd. |
| *pm* | claude | `pm` | Ordering, clarification, handoffs. |

Default shape for product workstreams. CTO is the user-escalation route.

## Design-led

| Role | Binary | Handle | Owns |
| --- | --- | --- | --- |
| designer | claude | `designer` | Flows, UX, visual shape, design assets. |
| frontend-dev | claude | `frontend-dev` | Browser UI implementation. |
| backend-dev | codex | `backend-dev` | APIs, persistence, services. |
| cto | codex | `cto` | Architecture, sign-off. |
| *qa* | claude | `qa` | Validation. |

Useful when the workstream is UI-heavy and design must lead implementation.

## QA-led / hardening

| Role | Binary | Handle | Owns |
| --- | --- | --- | --- |
| qa | claude | `qa` | Drives regression sweep and release readiness. |
| fullstack | claude | `fullstack` | Fixes routed by QA. |
| cto | codex | `cto` | Sign-off and risk calls. |

Useful for stabilization or release hardening workstreams.

## Research / spike

| Role | Binary | Handle | Owns |
| --- | --- | --- | --- |
| senior-dev | codex | `senior-dev` | Investigation, prototype, write-up. |
| cto | codex | `cto` | Constraints, sign-off, decision capture. |

Short-lived; usually lives under a named profile (`--profile research`) so it does not collide with the default product team.

## How to use these

1. Read the user's goal and pick the closest archetype.
2. Rename / drop / add roles as needed; do not keep `<role>` placeholders in `team-rules.md`.
3. Decide each member's cwd. QA frequently lives in a different repo; senior-dev may live in the same cwd as fullstack.
4. Decide profile: default for the team's main workstreams, named profile for parallel shapes.
5. Run `amq-squad team init` (or `--profile NAME`) and continue with the setup workflow.
