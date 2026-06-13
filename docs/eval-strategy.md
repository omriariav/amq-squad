# Slice D — eval strategy for goal-first composition

The Slice-D gate has two parts: this **eval rubric** (does a lead compose a
*sensible* team from a goal?) and a **live Codex-led dogfood** on a real squad.
The rubric exists so the gate measures the lead's **judgment**, not just that the
verbs run — the hard 80% the v2.0 plan is gated on. Phase 1 does not start until
the rubric passes its bar **and** a human judges the live dogfood "right."

## Method

For each reference goal below, a **Codex lead** (running with the orchestrator
skill's "Compose the team from the goal" playbook) is given the goal and an empty
scratch project, and must propose a team and exercise the mechanics
(`team member add`, `task add` with a dependency, prune). We score the proposed
roster against the hand-authored reference team.

Run it with: `Workflow` over the reference set (one Codex composer per goal), or
manually — give a Codex agent the goal + `plugins/codex/skills/amq-squad-orchestrator/SKILL.md`
and capture its proposed roster + the commands it ran.

## Reference set (hand-authored ground truth)

Roles are scored by **category coverage**, not exact name (a "frontend-dev" and a
"frontend" both satisfy the frontend category). Categories: `lead`, `frontend`,
`backend`, `qa`, `research`, `docs`, `pm`, `design`, `ops`.

| # | Goal | Reference team (categories) | Size | Critical (must cover) |
| --- | --- | --- | --- | --- |
| 1 | Build a React component library with tests, docs, and CI/CD | lead, frontend, qa | 3 (±1) | frontend, qa |
| 2 | Fix a flaky Python test in the data pipeline and add regression coverage | lead, backend, qa | 2–3 | backend, qa |
| 3 | Research the top 3 vector databases for our use case and recommend one | lead, research | 1–2 | research; **no implementers** |
| 4 | Write API reference docs for the payments service | lead, docs | 1–2 | docs; **no implementers** |
| 5 | Migrate the auth service from REST to gRPC across backend and clients | lead, backend, frontend, qa | 3–4 | backend, qa |

## Scoring (per goal)

- **Coverage**: every *critical* category for the goal appears in the proposed
  roster. (Missing a critical = fail for that goal.)
- **No over-spawn**: proposed size ≤ reference-max + 1, and ≤ the hard cap of 5.
- **No wrong-kind spawn**: goals marked "no implementers" (#3, #4) must not spawn
  backend/frontend/mobile roles.
- **Lead present**: exactly one lead/orchestrator.

A goal **passes** when coverage holds, size is in budget, and no wrong-kind spawn.

## Passing bar

The rubric passes when **≥ 4 of 5** goals pass, with **no critical-coverage miss
on goals #3/#4** (the discipline cases — not over-building research/docs work into
a full eng squad is the clearest judgment signal).

## Results

See `docs/eval-results.md` for the latest run (Codex leads, scored against this
rubric) — captured as the Slice-D evidence before the Phase-1 go/no-go.
