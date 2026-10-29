# Team Archetypes

A short menu of common squad shapes to adapt to the goal. The roster is not fixed by the tool. Roles in *italics* are optional
add-ons.

## Solo

One agent doing implementation, one human steering.

| Role | Runtime | Handle | Owns |
| --- | --- | --- | --- |
| fullstack | claude | `fullstack` | All implementation in cwd. |

Useful when the human is acting as cto/qa/pm themselves.

## Pair (dev + reviewer)

| Role | Runtime | Handle | Owns |
| --- | --- | --- | --- |
| fullstack | claude | `fullstack` | Implementation. |
| cto | codex | `cto` | Architecture, code review, sign-off. |

Smallest team that benefits from AMQ handoffs. Typical p2p thread: `p2p/cto__fullstack`.

## Classic squad

| Role | Runtime | Handle | Owns |
| --- | --- | --- | --- |
| cto | codex | `cto` | Architecture, sign-off, escalation to user. |
| fullstack | claude | `fullstack` | End-to-end implementation. |
| qa | claude | `qa` | Validation, regression checks. Often a different cwd. |
| *pm* | claude | `pm` | Ordering, clarification, handoffs. |

Default shape for product workstreams. CTO is the user-escalation route.

## Design-led

| Role | Runtime | Handle | Owns |
| --- | --- | --- | --- |
| designer | claude | `designer` | Flows, UX, visual shape, design assets. |
| frontend-dev | claude | `frontend-dev` | Browser UI implementation. |
| backend-dev | codex | `backend-dev` | APIs, persistence, services. |
| cto | codex | `cto` | Architecture, sign-off. |
| *qa* | claude | `qa` | Validation. |

Useful when the workstream is UI-heavy and design must lead implementation.

## QA-led / hardening

| Role | Runtime | Handle | Owns |
| --- | --- | --- | --- |
| qa | claude | `qa` | Drives regression sweep and release readiness. |
| fullstack | claude | `fullstack` | Fixes routed by QA. |
| cto | codex | `cto` | Sign-off and risk calls. |

Useful for stabilization or release hardening workstreams.

## Research / spike

| Role | Runtime | Handle | Owns |
| --- | --- | --- | --- |
| senior-dev | codex | `senior-dev` | Investigation, prototype, write-up. |
| cto | codex | `cto` | Constraints, sign-off, decision capture. |

Short-lived; give it its own session name so its tasks and mailboxes stay apart from the product squad's.

## How to use these

1. Read the user's goal and pick the closest archetype.
2. Rename / drop / add roles as needed; a custom role needs a staged role file
   (`role draft`).
3. Decide each mutation-capable seat's worktree (`--worktree r=path`); review seats
   can share the lead's checkout.
4. Decide the runtime per seat (`--runtime r=claude|codex|pi`); a pi seat takes no
   model or effort.
5. Pass the choices to `team propose`, relay its digest, and apply only the digest
   the owner approved.
