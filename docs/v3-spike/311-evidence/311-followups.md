# 3.1.1-2: four follow-ups from the 3.1 run

Brief: docs/v3-spike/briefs/brief-311-followups.md (copied verbatim in this
branch). Authority: the plan reviewer's follow-up lines of 2026-11-01 and the
3.1.0 day-4 record (docs/v3-spike/31-evidence/31-1-pr2-up-status-screen.md
section 4; docs/v3.1.0-release-notes.md "Recorded limits"). Branch
`v3-311-fu`, from origin/v3 `31bf161c` (#838 merged), with origin/v3 merged
in at `2a124ca8` (#844, t28: the typed widen request), at `745fea9e` (#845
to #848, the typed widen fixes; one conflict, the widen refusal text in
internal/cli/task_widen.go: v3's check with this branch's text) and at
`b67df349` (#850 to #853: the git-objects-only tree resolver,
link_target_refused, the typed widen's snapshot from the sealed ledger
archive; no conflict), never rebased. After the last merge the
completeness row failed on link_target_refused, widen_snapshot_missing and
widen_snapshot_attempt_mismatch, and on seatcommit.ReasonGrowthGate, now an
alias of task.ReasonGrowthGateRequired that the scan could not resolve; the
three are classified as policy and data refusals and the scan resolves an
alias of a constant it knows.
The lead added the plan reviewer's three additions on #844's lists and
items 5 and 6 (the merge train) during the work; sections 5 to 7.

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
  `widen_invalid`, `widen_check_failed`). From the merges: `link_target_refused`
  (policy), `widen_snapshot_missing` and `widen_snapshot_attempt_mismatch`
  (data).
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

## 5. The plan reviewer's additions on #844

**The merge.** #844 moved the lead's pinned team list from thirteen to
fourteen by adding `amq_task_block`. The two conflicts (pi/test/phase.test.ts,
pi/test/tools.test.ts) keep this branch's eleven and every other #844 hunk
(the block tool's argv rows, the widen of a `widen_requested` task, the
seat's registered list in keys.test.ts).

1. **`amq_task_block` is a seat verb.** It registers with claim and done
   (`seatReg`): active for a seat, inactive for a lead, refused `phase_team`
   after the yes. The lead's list is Go's eleven. Rows: tools.test.ts's
   exact eleven (block inactive for a lead, active for a seat) and
   phase.test.ts's refused list.
2. **The widen refusal names the typed widen request.** `validateBlockedWiden`
   (internal/cli/task_widen.go) and `refusedPaths` (pi/extensions/lib/tools.ts)
   now read: the task is not blocked for files outside its declared set or by
   a seat's widen request, so there is nothing to widen; to send it back to a
   seat, `amq-squad task reset ID --pool RUNTIME:MODEL:EFFORT`. Rows:
   TestWidenRefusalNamesTheTypedWidenRequest and tools.test.ts (no t20).
3. **Blocks that ask for a decision are classified.** `widen_requested` (a
   seat's typed widen request; the lead's widen returns the task to pending)
   and `task_branch_unprepared` (the harness's) are in the table as not work
   failures; a seat's own block with any other reason still counts. Row:
   TestWidenRequestedBlockIsNotAFailedAttempt.

## 6. Item 5: the drift check reads every session's gate store

**What the tip had.** `mergetrain.Drift` read one gate store, the squad's
session's, and counted a gate accepted by its decision file
(`gatestore.Store.Decision`: an answers file or a consumed mark). A release
cut in its own fresh session (v301, v303, v31rc1, v310) left its gates in
that session, so the train in v31 raised ungated tag and release alerts for
v3.0.1, v3.0.2, v3.1.0-rc.1 and v3.1.0.

**Design.** Drift reads the gate store of every session under the AMQ root,
the root's parent, enumerated with `os.ReadDir` and taken literally (no
glob; an entry that is not a directory, a symlink included, is skipped; a
directory with no gate store is skipped). A record counts only when it was
raised in the session whose store holds it (its squad identity) and the
`GateVerifier` accepts it: in production `releaseGateVerifier`
(internal/cli/merge_train.go), `gateverify.Verifier.Accepted` (the accepted
decision and its approval's owner signature, issuer and namespace) under
the namespace of the session that holds the gate (this squad's project and
profile, that session's name). Without the owner's gate binding or trust
store nothing verifies and every tag or release since the baseline stays
an alert. Another session's gate clears only through a target naming the
tag and its commit (`gatetarget.ReleaseTag`) or the release's tag
(`ReleaseCreate`); the squad's own session keeps its seat-gated commands
(`git tag <name>`, `git push refs/tags/<name>`, `gh release create <name>`),
each bound to the first commit or release id it covers, as before. A store
whose gate list cannot be read is an alert of its own,
`gate_store_unreadable`, naming it. No gate is copied between sessions.
doctor's drift check is the second caller and takes the same verifier
(`doctorGateVerifier`: the squad context resolved as a command resolves it,
when its root is doctor's).

**Rows.** internal/mergetrain/brief3112_test.go: a release gated in another
session clears (tag at its commit, release by its tag); a gate whose target
names another commit does not, nor another session's gated command; an
unaccepted record does not; no verifier clears nothing; an unsigned record
(accepted by its consumed mark only) does not verify under gateverify's own
check; an unreadable store raises `gate_store_unreadable` with its path.
internal/cli: without the binding the verifier refuses. The three older
drift rows (acceptance, fix1, squad) run with a stand-in verifier that reads
the accepted decision, since they cover the binding rules, not the
signature.

## 7. Item 6: a dry run reads the binding

**What the tip had.** A real run reads every PR's task from the harness
binding (`mergetrain.BindTasks`: the one task with that `pr_number`, its
declared files and `pr_head`); a dry run filled declared files only for a PR
that named its task, so a request naming the PR alone refused `task_scope`
"declares no files".

**Design.** The dry run's `fillDeclaredFiles` now binds a PR named with no
task and no files the same way (`mergetrain.BindUnnamed`, which shares the
real run's `bindItem`); a PR with no binding is `task_unbound` in both. Row:
TestMergeTrainDryRunReadsTheBindingOfAPROnlyRequest: the dry run's request
equals the real run's after `BindTasks`, for a bound PR and an unbound one.

## 8. Revert runs

`docs/v3-spike/311-evidence/311-followups-revert-runs.py`: each case
rewrites one change in the tree, runs its row, expects a failure, and
restores every touched file in a `finally`. 39 runs, 39 caught on the
merged tree; the tree was clean after.

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
| R6e | `amq_task_block` active for a lead (#844) | tools.test.ts: the exact eleven |
| R6f | the same | phase.test.ts: the owner's yes switches to exactly the eleven |
| R8a | the Go widen refusal points at t20 (the tip) | TestWidenRefusalNamesTheTypedWidenRequest |
| R8b | the pi widen refusal points at t20 (the tip) | tools.test.ts: the widen refusal row |
| R1g | `widen_requested` missing from the table | TestWidenRequestedBlockIsNotAFailedAttempt |
| R9a | the drift check reads the squad's own store only (the tip) | TestDriftReadsTheGateStoresOfEverySession |
| R9b | a gate clears on its decision file, no verifier (the tip) | TestDriftClearsOnlyOnTheOwnerSignedAcceptance, TestDriftReadsTheGateStoresOfEverySession |
| R9c | another session's gated command clears a tag | TestDriftReadsTheGateStoresOfEverySession |
| R9d | an unreadable store is skipped | TestDriftNamesAnUnreadableGateStore |
| R9e | without the binding every gate verifies | TestReleaseGateVerifierFailsClosedWithoutTheBinding |
| R10 | the dry run ignores the binding (the tip) | TestMergeTrainDryRunReadsTheBindingOfAPROnlyRequest |
| R1h | `link_target_refused` missing from the table | TestEveryCommitRefusalCodeIsClassified |
| R1i | the scan does not resolve a constant aliased from another package | TestEveryCommitRefusalCodeIsClassified |

The no-self-re-dispatch row (TestPolicyRefusedTaskIsNotRedispatchedWithoutTheLead)
is a pin: the store's block already gave it, and no change here sits behind
it to revert.

## 9. Gates

On `c331bb07` (origin/v3 `b67df349` merged), 2026-11-02:

- `go build ./... && go vet ./... && go test -count=1 ./...`: exit 0; 56
  packages `ok`, none failing; the tail is `ok .../scripts`,
  `ok .../scripts/pipack`, then three packages with no test files.
- `cd pi && npm ci --offline --ignore-scripts && npm test`: exit 0; tests 84,
  pass 84, fail 0.
- `make ci` (PyYAML venv on PATH): exit 0; the tail is the hook budget line
  `PERF outside a seat: 60 runs, p50 5.6 ms, p95 6.1 ms, max 6.5 ms (budget
  p95 < 20 ms: met)`.
- The revert script: 39 runs, 39 caught, the tree clean after.

## 10. Where the brief and the code disagreed

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

- **Items 5 and 6, outside their declared files.** doctor's drift check
  (internal/cli/doctor_release_drift.go and its test) is the second caller
  of `mergetrain.Drift` and had to take the verifier; the widen refusal text
  is in internal/cli/task_widen.go, which the lead named. The one line that
  hands merge-train's verifier to the train has no revert row of its own: a
  train with no verifier clears nothing, so the revert fails closed (more
  alerts, never fewer); the verifier's own fallback has a row (R9e). No row
  signs a gate end to end through `releaseGateVerifier`: the per-session
  namespace is read from the code, not exercised by a signed fixture.
- **The own session keeps its seat-gated commands.** The brief's rule (a
  target naming the tag and its commit) is applied to other sessions'
  gates; the squad's own session keeps the seat-gated command targets it
  had, which carry no commit, each bound to the first commit it covers.

## 11. For the 3.1.1 release notes

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
  list, Go's eleven: `amq_task_claim`, `amq_task_done` and `amq_task_block`
  are a seat's. A seat's `widen_requested` block does not escalate the
  retry, and the widen refusal names the typed widen request. The merge
  train's tag and release drift check reads the gate store of every session
  under the AMQ root, so a release cut in its own session no longer raises
  an ungated alert, and it clears an alert only on an owner-signed accepted
  gate; a store it cannot read is an alert of its own. A dry run of a
  request that names only the PR reads the task from the harness binding,
  as the real run does.

## The one asymmetry, on purpose (the plan reviewer)

An unnamed seat block still counts as a failed attempt. A seat's free-text block can be a real "I cannot do this", and counting it keeps a seat from dodging MaxAttempts by blocking. Tonight's free-text `declared_files_missing` blocks fell under it; the typed `widen_requested` block that /widen brings is classified as no failure, so the asymmetry fades once seats use it. An unnamed commit refusal, by contrast, never counts, and nothing that does not count re-dispatches by itself, so no misclassification can loop a seat; it can only spare it an escalation.
