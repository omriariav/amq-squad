# Multi-workstream operator runbook (retired in 3.0)

This was the v2 runbook for one operator driving several workstreams by hand
with `init`, `start`, `dispatch`, `down` and a self-kept board. amq-squad 3.0
has none of those verbs, and the job itself moved into the product: one lead
session program serves both roles (`amq-squad lead --team --scheduler-seats
FILE` for a squad lead, with the scheduler beside it; the same program with a
fleet grant is the operator session), the fleet registry `~/.amq-squad/fleet.yaml`
(`amq-squad fleet`) replaces the hand-kept board, `amq-squad status --fleet
--json` and `amq-squad operator inbox` are the fleet's one view of every squad
and every open gate, and in auto mode the mayor (`amq-squad operator
start-squad`) starts a squad inside the owner's fleet grant and never answers a
gate. A separate runbook would restate those verbs' help, so this page is a
pointer: read the [README](../README.md) ("First run", "Running a squad", "The
owner's Buzz face"), the `amq-squad:orchestrator` skill ([docs/skills.md](skills.md)),
and plan sections 3.5 and 16.3 in [docs/v3-plan.md](v3-plan.md).
