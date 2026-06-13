# Slice D — eval results (Codex-led composition)

Run: 5 **Codex** leads (one per `docs/eval-strategy.md` reference goal), each given
the goal + the orchestrator "Compose the team from the goal" playbook, composing a
team and exercising the real mechanics (`team member add`, `task add` with a
dependency, `task claim`/`done`, prune) on an isolated scratch project.

## Scores

| # | Goal | Final roster (after prune) | Critical coverage | Size | Verdict |
| --- | --- | --- | --- | --- | --- |
| 1 | React component library | cto · frontend-dev · qa | frontend ✅, qa ✅ | 3 | **pass** |
| 2 | Fix flaky Python test | cto · senior-dev · qa | backend (=senior-dev) ✅, qa ✅ | 3 | **pass** |
| 3 | Vector-DB research | cpo · cto | research (as evaluators) ✅, **no implementers** ✅ | 2 | **pass** |
| 4 | API reference docs | pm · backend-dev · qa | docs ✅; spawned backend-dev | 3 | **borderline** |
| 5 | REST→gRPC migration | cto · backend-dev · qa | backend ✅, qa ✅ | 3 | **pass** |

**Mechanics: 5/5 succeeded** — every lead ran `new team` (lead-only seed),
`team member add` ×N, `task add` ×2–3 with `--depends-on`, `task claim`+`done`,
and `team member rm` (prune), all without errors. Dependency gating behaved
correctly (a task stayed blocked until its dep completed).

**Rubric bar (≥4/5, no critical miss on #3/#4): MET.** Four clear passes plus one
borderline; #3 and #4 both cover their critical category.

## Judgment signal (the point of the gate)

- **#3 (research)** is the strongest evidence: the Codex lead explicitly refused
  to spawn the engineering catalog ("the full engineering catalog would be pure
  overhead") and composed a 2-agent research team. Not over-building a research
  task into an eng squad is exactly the discipline the gate tests.
- Binary choices were deliberate (codex for code-heavy/iterative editing, claude
  for review/prose), and every lead decomposed the goal into a correctly-ordered
  dependency chain.

## Findings (real, surfaced by the dogfood)

1. **No `team member list` / roster-read command.** All 5 leads tried
   `team member list` (or `team list`), found it invalid, and fell back to
   reading `.amq-squad/team.json` directly. Bare `amq-squad team` shows the
   roster, but a lead naturally reaches for `team member list`. **Recommended
   follow-up:** add `team member list` (or point the error at `amq-squad team`).
2. **Role library lacks a docs/writer role** (catalog is cpo, cto, senior-dev,
   fullstack, frontend-dev, backend-dev, mobile-dev, junior-dev, qa, pm,
   designer). For the docs goal (#4) the lead reached for `backend-dev` as the
   API SME — defensible, but it's why #4 is borderline. **Recommended
   follow-up:** add a `scribe`/`technical-writer` role so docs goals compose
   cleanly without an implementer.

Neither finding blocks the gate; both are good iteration before/with Phase 1.

## Gate status

The **automated rubric passes**, and the mechanics are solid end to end. The
Slice-D gate also requires a **human judgment** on a **live** Codex-led dogfood on
a real squad (os-omri-pm) — "does it *feel* right" — which is the operator's call
and must run in the real environment, not a scratch dir. **Phase 1 (the breaking
cut + `/v2` + release) holds for that explicit go/no-go.**
