# 3.1.1-2: four follow-ups from the 3.1 run

Brief: docs/v3-spike/briefs/brief-311-followups.md (copied verbatim in this
branch). Authority: the plan reviewer's follow-up lines of 2026-11-01 and the
3.1.0 day-4 record (docs/v3-spike/31-evidence/31-1-pr2-up-status-screen.md
section 4; docs/v3.1.0-release-notes.md "Recorded limits"). Branch
`v3-311-fu`, from origin/v3 `31bf161c` (#838 merged).

## 1. A policy refusal is not a failed attempt

**What the tip had.** `routing.failure(outcome)` counted every
`commit_refused` and every `blocked` attempt as a failed attempt of the work,
whatever its reason code. The commit step's refusal blocks the task
(`task.RefuseCommitPaths`), so nothing re-dispatched t27 by itself; at the
lead's reset the router saw one failure, moved attempt 2 one tier up to
`claude:opus:high`, and the scheduler, in roster mode, found no roster seat
for it. That path (`rosterNoIdleSeat`, internal/scheduler/roster.go) logged
`seat_request_failed` and returned, on every 2 s tick.

**Design.**

- **One table** (`routing.RefusalWorkFailure`, internal/routing/refusal.go):
  every reason code the commit step can end an attempt with, `true` for a
  work failure, `false` otherwise. Work failures: `verdict_failed`,
  `verdict_missing`, `verifier_timeout`, `verifier_output_exceeded`. Not work
  failures: the policy refusals (`size_cap_exceeded`,
  `outside_declared_files`, `protected_path`, `growth_gate_required`,
  `nested_repository`), the binding deferrals (`pr_pending`,
  `pr_bound_elsewhere`), the seat-branch data guards (`base_unrecorded`,
  `worktree_mismatch`, `recorded_diff_mismatch`, `lease_task_mismatch`,
  `ledger_tampered`, `push_target_unacknowledged`, `evidence_ref_symbolic`,
  `verdict_tree_mismatch`), and the grant, remote and harness codes
  (`grants_invalid`, `tool_unpinned`, `tool_pin_mismatch`, `push_rejected`,
  `commit_failed`, `commit_cancelled`, `verifier_start_failed`,
  `verifier_interrupted`, `verifier_cancelled`, and a widening's own
  `widen_invalid`, `widen_check_failed`).
- `failure(attempt)` reads the table: failed and expired attempts count; a
  commit refusal or a block counts only when its code is a work failure; a
  commit refusal with a code the table does not name never counts (a new code
  never falls into escalation); a block with an unnamed code (a seat's own
  block) still counts, as before. The tier escalation, MaxAttempts and the
  `used_before` skips all read it. The weekly report keeps its own
  failed-row rule (`failedOutcome`), unchanged.
- **No self re-dispatch** was already the store's rule (the refusal blocks
  the task until the lead's reset or widen; a widen re-arms the commit step
  without a dispatch). A pin row holds it.
- **`no_roster_seat`** (internal/scheduler): a route to a pool no roster seat
  serves sets the task's wait to `no_roster_seat` (once: `SetWait` writes
  only a change) and records the pool in the scheduler state file's
  `route_waits`; nothing is logged. status carries the pool as the task
  row's `wait_pool` and the attention item reads `t27 waits: no seat serves
  claude:opus:high: <title>` (severity medium); the screen's task line reads
  `t27  pending  -  PR -  waits: no seat serves claude:opus:high`.
- **The backoff** covers the transient path left: an on-demand seat request
  the launcher refuses (internal/scheduler/scheduler.go). Per task and pool,
  the failure is logged once with the next delay, and the request is not
  made again until the delay passes: 2 s, doubling, capped at 5 minutes. The
  backoff starts over when its key changes: the roster (seats and pools in
  the config, and the seats the scheduler holds), the task (its `updated_at`
  moves on any store mutation, and its attempt), and the grant's part the
  scheduler holds (the seat cap). The grant is read once per scheduler
  process, so a grant change is a new process, whose backoff starts empty.
  The backoff lives in internal/scheduler, where the request is made;
  internal/schedproc is unchanged.

**Rows.** internal/routing/brief3112_test.go: a size-cap-refused attempt
(and every policy, binding and data-guard code, and an unclassified one) is
not counted, past MaxAttempts, same tier and entry; a work failure
(`verdict_failed`, a verifier timeout, a failed attempt) still escalates and
three exhaust the task; every code seatcommit can emit is classified (the row
reads seatcommit's source: every `Reason*` constant and every expression
handed to `refuse`, assigned to `ReasonCode` or set as a `Violation`'s
`Reason`; a constant of another package or a variable must be one the row
knows, so a new emitter fails it); t27 end to end (a roster of one seat on
the task tier, the plan tier on a pool no seat serves: the refusal, twenty
ticks with no dispatch, the lead's reset, attempt 2 on the same seat).
internal/scheduler/brief3112_test.go: a no-seat tier is `no_roster_seat`
with the pool in the state file and no log line in 50 ticks; a transient
failure over 300 ticks of 2 s is requested and logged at 0, 2, 6, 14, 30,
62, 126, 254 and 510 s, and a task change starts it over; the pin: a
size-cap-refused task is not dispatched in 100 ticks and is dispatched once
after the lead's reset. TestRosterPoolRules, which pinned the old log line,
now pins the named wait. internal/status/brief3112_test.go: `wait_pool` and
the attention item. pi/test/screen.test.ts: the task line.

## 2. status shows the running roster

**Design.** "The roster as compiled by the running scheduler" is the
scheduler state file's `roster` (scheduler.json, written every tick from the
compiled squad.yaml and swapped by a relaunch's reload), with the lead (the
state's `lead` and the scheduler launch record's). `seats[]` holds only
those; every other seat record under the root goes to `stale_records[]`
(`handle`, `session` from its staged launch record, `launch_id`), with no
seat row, no count and no attention item. With no roster known (seats on
demand, or no scheduler state yet) the listing is as before and
`stale_records` is `[]`. The screen's seats block renders `seats[]`, so it
shows only the roster. docs/status-contract.md: `seats[]` now means the
running roster (shape unchanged, meaning narrowed), `stale_records[]` and
the task row's `wait_pool` are additive under schema 1; both golden fixtures
and pi/test/status-fixture.json carry them.

**Row.** TestStatusSeatsAreTheRunningRoster: a fixture root with day 4's
seven handles (lead, qa, scribe, senior-dev of session v30; cto, dev,
reviewer of v31) and a scheduler state naming dev and reviewer with lead cto:
`seats[]` is cto, dev, reviewer; `stale_records` the four with their session
and launch id; counts 3. Without the state file, seven seats and `[]`.

## 3. The local release check runs every validator CI runs

**Choice: the first option, the smaller diff.** `make release-check` now
also runs `scripts/test_detect_release_ci.py`, the new
`scripts/test_release_version_forms.py`, and `detect-release-ci.py
--check-version VERSION` (its own parse of VERSION and of the head
manifests). A shared module would have touched the three scripts' patterns
and imports for the same guarantee; the agreement row gives it without
moving code.

**The agreement row** loads the three readers (detect-release-ci.py's
manifest and branch parses, check-release-version.py's VERSION and
frontmatter parses, check-skill-frontmatter.py's frontmatter marker) and
runs 24 forms through each: all accept with the same version or all refuse.
It found a disagreement at the tip: detect-release-ci.py's `\d` takes any
Unicode digit (`٣.١.٠`, a fullwidth `３`), the other two take ASCII digits
only. detect-release-ci.py now reads `[0-9]`. CI runs the release check
before it installs PyYAML, and check-skill-frontmatter.py exits without it,
so the row reads that script's two patterns from its source (the
`re.compile` literals) rather than importing it. A second row reads
.github/workflows/ci.yml's release path (the steps from "Detect release
validation scope" to "Run release metadata check") and requires the
release-check recipe to run each script there and its test, and
release-validator-test (in `make ci`) to run the forms row.

## 4. One lead tool list, Go's eleven

**Design.** `TEAM_LEAD_TOOLS` (pi/extensions/lib/tools.ts) is Go's
`piruntime.TeamLeadTools`, name for name and in order; the extension's team
phase activates exactly it (`pi.setActiveTools`) and the `tool_call` hook
refuses anything else `phase_team`. `amq_task_claim` and `amq_task_done`
stay registered for every pi: active for a seat, registered inactive for a
lead (team or solo). No lead flow needs them: the only mentions are the
seat's context lines (pi/extensions/lib/hooks.ts:168-169).

**Rows.** pi/test/tools.test.ts: the lead's active tools are the eleven, claim
and done registered and inactive for a lead and active for a seat; the
extension's list equals Go's, read from internal/piruntime/launch.go.
pi/test/phase.test.ts: the rows from #835 and #837 that pinned thirteen
(`TEAM13`) pin eleven (`TEAM11`), and after the yes a model naming claim or
done meets `phase_team`. pi/test/keys.test.ts lists the lead's registered
tools (thirteen names) and is unchanged: claim and done stay registered.

## 5. Revert runs

`docs/v3-spike/311-evidence/311-followups-revert-runs.py`: each case
rewrites one change in the tree, runs its row, expects a failure, and
restores every touched file in a `finally`. 26 runs, 26 caught; the tree
was clean after.

| Run | Reverted | Row that failed |
| --- | --- | --- |
| R1a | `failure()` counts every commit refusal (the tip) | TestPolicyRefusalIsNotAFailedAttempt |
| R1b | the same | TestSizeCapRefusalRetriesOnTheSameSeat |
| R1c | an unclassified commit code counts | TestPolicyRefusalIsNotAFailedAttempt |
| R1d | `pr_pending` missing from the table | TestEveryCommitRefusalCodeIsClassified |
| R1e | `verdict_failed` not a work failure | TestWorkFailureStillEscalates |
| R1f | `size_cap_exceeded` a work failure | TestEveryCommitRefusalCodeIsClassified, TestPolicyRefusalIsNotAFailedAttempt |
| R2a | no roster seat logs every tick (the tip) | TestNoRosterSeatIsANamedWaitNotALogLine, TestRosterPoolRules |
| R2b | route waits not in the state file | TestNoRosterSeatIsANamedWaitNotALogLine |
| R2c | no backoff (the tip) | TestTransientSeatRequestFailureBacksOff |
| R2d | the backoff does not double | TestTransientSeatRequestFailureBacksOff |
| R2e | a task change does not reset it | TestTransientSeatRequestFailureBacksOff |
| R3a | no `wait_pool` | TestNoRosterSeatWaitNamesThePool |
| R3b | the attention item does not name the pool | TestNoRosterSeatWaitNamesThePool |
| R4a | `seats[]` lists every record (the tip) | TestStatusSeatsAreTheRunningRoster |
| R4b | the roster leaves out the lead | TestStatusSeatsAreTheRunningRoster |
| R4c | `stale_records` null when empty | TestStatusSeatsAreTheRunningRoster, TestBuiltStatusValidatesAgainstTheGolden |
| R4d | `stale_records` not in the golden | TestGoldenRoundTripAdditiveBothWays |
| R5 | the screen's task line does not name the wait | screen.test.ts: a task waiting no_roster_seat names the pool |
| R6a | the team list is every registered tool (the tip) | phase.test.ts: the owner's yes switches to exactly the eleven |
| R6b | claim and done active for a lead (the tip) | tools.test.ts: the exact eleven |
| R6c | the same | phase.test.ts: lead --team ... exactly the eleven |
| R6d | the extension's list drifts from Go's | tools.test.ts: the exact eleven |
| R7a | detect-release-ci.py's `\d` (the tip) | test_release_version_forms.py |
| R7b | `--check-version` checks nothing | test_detect_release_ci.py |
| R7c | release-check without the new validators (the tip) | test_release_version_forms.py |
| R7d | `make ci` without the forms row | test_release_version_forms.py |

The no-self-re-dispatch row (TestPolicyRefusedTaskIsNotRedispatchedWithoutTheLead)
is a pin: the store's block already gave it, and no change here sits behind
it to revert.

## 6. Gates

GATE_TAILS

## 7. Where the brief and the code disagreed

- **The 8000 lines were not a failing seat request.** In roster mode
  `seat_request_failed` came from `rosterNoIdleSeat` when no roster seat
  served the pool (roster.go:503), a deterministic answer logged per tick;
  the launcher's own refusal (scheduler.go:870) exists only in on-demand
  mode. So the named wait replaces the roster case, and the backoff covers
  the on-demand one, as the plan reviewer's addition (c) says.
- **The task was not re-dispatched on its own** before either: the commit
  refusal blocks it. Attempt 2 escalated at the lead's reset. Row (b) is a
  pin.
- **`growth_gate_required` is a widening's code** (`seatcommit.Widen`'s
  decision), not a commit-step refusal; it is in the table as a policy code,
  with the widening's `widen_invalid` and `widen_check_failed`, since the
  row requires every code seatcommit emits.
- **Codes from other packages** reach a commit refusal through seatcommit and
  the scheduler (`verdict_failed`, `verdict_tree_mismatch`,
  `tool_unpinned`, `tool_pin_mismatch`, the scheduler's `commit_failed` and
  `verifier_interrupted`, the store's `pr_bound_elsewhere`); the table
  classifies them too. `verdict_missing` (the store's completion gate, an
  `OutcomeBlocked`) stays a work failure, as before.
- **"Status shows the task's last route refusal by name"**: a policy-refused
  task already shows its code (`block_reason size_cap_exceeded`); the new
  part is the route wait, `wait_reason no_roster_seat` with `wait_pool`.
- **Declared files.** The wait reason is `scheduler.WaitNoRosterSeat`, not a
  `task` constant (internal/task is not declared; `SetWait` takes any
  reason). The brief's own copy is under docs/v3-spike/briefs/, at the
  lead's instruction. docs/v3-manifest.yaml has no row for
  internal/routing/refusal.go: the manifest is not kept for 3.x additions and
  no gate reads it.

## 8. For the 3.1.1 release notes

- A commit-step policy refusal (size cap, outside the declared files, a
  protected path, a growth gate), a binding deferral or a seat-branch data
  guard no longer counts as a failed attempt: after the lead's reset the
  task retries on the same tier, not one up. One table in the routing
  package classifies every commit refusal code. A route to a pool no roster
  seat serves is the named wait `no_roster_seat`, shown with the pool on the
  screen's task line and in status (`wait_pool`), not a log line every two
  seconds; an on-demand seat request that fails backs off from 2 s to 5
  minutes. `status` lists the running roster as `seats[]` and every other
  seat record under the root as `stale_records[]`. `make release-check` runs
  every validator CI runs on a version bump. The lead's tool list is one
  list, Go's eleven: `amq_task_claim` and `amq_task_done` are a seat's.
