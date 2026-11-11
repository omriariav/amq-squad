# 3.2.0 control bytes at the root

Seat-authored controls could enter through a lease holder or status data and
reach a person's terminal. These paths were established by reading the code;
none was exercised on a live team. `SeatHandle` now refuses controls and
invalid UTF-8, and the status contract replaces every C0 and C1 control at its
single exit. Plain task list and show output use the same replacement.

## Seat handles

`SeatHandle` at `internal/task/unclean.go:89-95` trims first, then refuses an
empty or path-like handle, invalid UTF-8, and any rune below U+0020 or from
U+007F through U+009F. The existing safely quoted refusal is unchanged.
`TestSeatHandleRefusesControlsAndInvalidUTF8` covers ESC/BEL, C1 CSI, DEL, tab,
an invalid lone byte, ordinary ASCII, Unicode, and trimming.
`TestGrantRefusesControlHandleWithoutChangingTask` covers the store level a
seat reaches: grant is refused, its error contains no raw control, and the task
remains pending without a lease.

The nine records below are as of 8e373fe3 (the replaced PR's head): commands,
expressions and first-failure lines are that head's; the lead re-ran all nine
on it, and re-runs them on this PR's head at the gate.

Both reverts used:

```sh
HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 -run 'TestSeatHandleRefusesControlsAndInvalidUTF8|TestGrantRefusesControlHandleWithoutChangingTask' ./internal/task/
```

- Removing `strings.ContainsFunc(h, func(r rune) bool { return r < 0x20 || r
  >= 0x7f && r <= 0x9f })` exited 1 first at `--- FAIL:
  TestSeatHandleRefusesControlsAndInvalidUTF8`.
- Removing `|| !utf8.ValidString(h)` and its then-unused import exited 1 at
  `control_handle_test.go:13: SeatHandle("dev\x9b") = <nil>`.

## The status exit

`status.Plain` in `internal/status/plain.go:10-19` reflects through exported
struct fields, pointers, slices, and arrays. `status.PlainText` at lines 24-34
maps each C0 or C1 rune to one space; invalid UTF-8 becomes U+FFFD through
`strings.Map`. Gate questions alone preserve U+000A while replacing their
other controls. This exception preserves the deliberately multi-line question
whose first plain line the screen displays. An argv string is treated like any other string. The replacement
is not collision-free: a root that differs from another only in a control
character maps onto it. A root is the operator's own path; a seat cannot
choose it.

`buildStatusContract` calls `status.Plain` once at
`internal/cli/status.go:115-117`, after launch failures and after credential
redaction. `TestPlainCoversEveryStatusStringAndKeepsQuestionLines` fills every
settable string by reflection and covers C0, C1, invalid UTF-8, slices, and the
question exception. `TestStatusExitsStripControlsFromJSONAndPlain` chains a
typed worktree-path error sentence through a real decision record; the real
unexported `worktreePaths` function was not reachable from `internal/cli`, so
the test types its exact sentence. It also adds the task through `task.Store`.

The status reverts and copied first failing lines were:

```sh
HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 -run 'TestStatusExitsStripControlsFromJSONAndPlain' ./internal/cli/
HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 -run 'TestPlainCoversEveryStatusStringAndKeepsQuestionLines' ./internal/status/
```

- R1 removed `status.Plain(&result)`: `--- FAIL:
  TestStatusExitsStripControlsFromJSONAndPlain/JSON` and `--- FAIL:
  TestStatusExitsStripControlsFromJSONAndPlain/plain`.
- R2 changed `r < 0x20 || r >= 0x7f && r <= 0x9f` to `r < 0x20`:
  `--- FAIL: TestPlainCoversEveryStatusStringAndKeepsQuestionLines`.
- R3 changed `if keepNewlines && r == '\n'` to `if false`: `--- FAIL:
  TestPlainCoversEveryStatusStringAndKeepsQuestionLines`.
- R4 changed `case reflect.Slice, reflect.Array` to `case reflect.Array`:
  `--- FAIL: TestPlainCoversEveryStatusStringAndKeepsQuestionLines`.

## Task text and the reserved reason

The list row at `internal/cli/task.go:413-415` passes every store string through
`status.PlainText`: ID, state, class, holder, pool, wait/block reason and title.
Its constants, numeric priority and tab layout are not passed. `printTaskDetails`
at lines 709-765 applies the same rule to every stored string, including pool,
dependency IDs, declared files, the lease holder, attempt fields, evidence,
report author and description. Its constants, `time.Time`, `time.Duration` and
numbers are not passed. Description alone preserves U+000A. JSON remains the
stored contract and relies on JSON escaping. `TestTaskListAndShowStripControls`
at `internal/cli/control_bytes_test.go:56-184` writes pool and declared files
via `AddInput`, evidence via `Done`, and cancellation via `Cancel`. It changes
the dependency, legacy lease holder, control-bearing attempt fields and legacy
report author in stored task files because current store calls refuse or do
not accept those values. Separate tasks keep block, live lease, completed
evidence, cancellation and pending-report fields in valid states. It covers
both plain outputs and the description's two lines.

The CLI-only reserved-reason list is at `internal/cli/task.go:768-774` and
contains only `ledger_tampered`. The block case refuses it at lines 633-638
before calling the store. `TestTaskBlockRefusesReservedSchedulerReason` proves
the literal and leading-space forms leave the live task unchanged, direct
scheduler `Store.Block` still lands, and `test_failed` still blocks.

```sh
HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 -run 'TestTaskListAndShowStripControls/list' ./internal/cli/
HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 -run 'TestTaskListAndShowStripControls/show' ./internal/cli/
HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 -run 'TestTaskBlockRefusesReservedSchedulerReason' ./internal/cli/
```

- Replacing the list row's three `status.PlainText` calls with its raw holder,
  reason, and title exited 1 at `--- FAIL:
  TestTaskListAndShowStripControls/list`.
- Changing `plain := func(s string) string { return status.PlainText(s, false)
  }` to return `s` exited 1 at `--- FAIL:
  TestTaskListAndShowStripControls/show`.
- Removing the `reservedTaskBlockReason(*reason)` check exited 1 at `--- FAIL:
  TestTaskBlockRefusesReservedSchedulerReason`.

## Other exits read on this head

| Exit | Source and seat control | Disposition |
| --- | --- | --- |
| Buzz phone replies, gate questions, and alerts | `remoteMode` builds the status closure at `internal/cli/remote.go:383-388` and `404-406`; `internal/cli/remote.go:652-656` only builds the handler. The publishers read were `internal/buzz/relay.go:435` (`Poster.Post`) and `internal/buzz/route.go:158,189` (`Mailer.Send`). No other phone-bound publisher was read. | 3.2.x item: apply the shared plain-text rule at those publish boundaries. |
| Seat doorbell and lead notice | `internal/schedproc/amq.go:72-122` builds AMQ subjects and bodies from task title, block detail, and scheduler text; a seat can write the title or detail. | 3.2.x item: sanitize both scheduler AMQ bodies before send. |
| Other task verb results and refusals | `internal/cli/task.go:697-705` prints title and lease holder; refusal construction also quotes stored title or reason. Seats can write reasons and evidence, while `task add` writes titles and `task done` writes evidence. | 3.2.x item: sanitize every remaining human task result/refusal exit rather than rejecting stored prose. |
| Seat last message | The collect step copies subject and body into `Seat.LastMessage` at `internal/status/collect.go:197-204`. A seat can send both. Every status reader receives the document after `Plain`. | covered by the status exit |
| `up` seat rows | `writeUpText` prints status-derived seat strings at `internal/cli/up.go:1273-1284`; those rows came through `buildStatusContract`. | covered by the status exit |
| `up` decision outcome and refusal | `UpRosterOutcome` reads decision reason/seat at `internal/cli/up.go:1217-1226`, and `writeUpText` prints the returned step outcome/refusal/detail at lines 1262-1271. | 3.2.x item: sanitize decision-derived text before `writeUpText`. |
| Scheduler stdout | `internal/cli/scheduler_run.go:348` prints configured root/session/lead outside the status document. | 3.2.x item: sanitize scheduler-run lines that bypass status. |
| Merge-train plan and refusals | The human writer is `printMergeTrain` at `internal/cli/merge_train.go:320-386`; it prints stored notes, tasks, rules, refusals, gate errors and stop text. | 3.2.x item: sanitize merge-train human output at this writer. |
| Fleet document's own fields | `status.Fleet` at `internal/status/fleet.go:24-39` has the string fields `Kind`, `Producer` and `ProducerVersion`; it also contains squad documents, waiting rows and an operator seat. The root-unavailable warning appended after a squad's status exit is covered at `internal/cli/fleet.go:148` by this task. | 3.2.x item: sanitize the remaining fleet envelope strings and nested waiting/operator inputs. |
| Decisions log writer and readers | `internal/schedproc/process.go:346-352` encodes and writes a decision; `internal/schedproc/decisions.go:105-107` calls that writer. Product human readers are status at `internal/status/screen.go:58-65`, `up` at `internal/cli/up.go:1217-1226`, and recovery replies at `internal/schedproc/recover.go:213-227`. | 3.2.x item: sanitize each human reader while preserving the forensic JSONL record. |

No path not read is claimed settled.

## Other block reason codes

This search found 17 product writes or scheduler/router routes:

```sh
grep -RInE --include='*.go' --exclude='*_test.go' '((Store|\.store)\.(Block|RefuseCommit|RefuseCommitPaths)\(|Route\{Block:)' internal/cli internal/routing internal/scheduler internal/task | sort
```

The direct dynamic commit result reaches `RefuseCommitPaths` at
`internal/cli/seat_commit.go:172` and `internal/scheduler/scheduler.go:816`;
`internal/routing/refusal.go:13-67` is the exhaustive product classification
of the codes it can emit. "No" below means no product test or documented step
was found using that code through the `task block` verb.

| Code | Declaration or spelling and product write | `task block` today | Disposition |
| --- | --- | --- | --- |
| `protected_path` | `internal/seatcommit/policy.go:29`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `link_target_refused` | `internal/seatcommit/policy.go:30`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `outside_declared_files` | `internal/seatcommit/policy.go:31`; dynamic refusal write above | documented harness refusal at `docs/v3-spike/4-1-execution.md:164` | 3.2.x item: reserve it at the verb |
| `size_cap_exceeded` | `internal/seatcommit/policy.go:32`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `nested_repository` | `internal/seatcommit/policy.go:33`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `base_unrecorded` | `internal/seatcommit/policy.go:34`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `worktree_mismatch` | `internal/seatcommit/policy.go:35`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `push_rejected` | `internal/seatcommit/policy.go:36`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `grants_invalid` | `internal/seatcommit/policy.go:37` and `internal/task/verdict.go:46`; direct write `internal/scheduler/scheduler.go:964` | no | 3.2.x item: reserve it at the verb |
| `commit_failed` | `internal/seatcommit/policy.go:38`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `pr_bound_elsewhere` | `internal/seatcommit/policy.go:42`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `recorded_diff_mismatch` | `internal/seatcommit/policy.go:45`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `lease_task_mismatch` | `internal/seatcommit/policy.go:49`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `push_target_unacknowledged` | `internal/seatcommit/ledger.go:170`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `ledger_tampered` | `internal/seatcommit/ledger.go:123`; direct write `internal/scheduler/tamper.go:197` | refused by the verb in `internal/cli/control_bytes_test.go:186-223` | the verb's by design: scheduler-only and reserved here |
| `newer_writer` | `internal/seatcommit/ledger.go:125`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `schema_mismatch` | `internal/seatcommit/ledger.go:128`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `evidence_ref_symbolic` | `internal/seatcommit/evidence_purge.go:15`; classified dynamic refusal | no | 3.2.x item: reserve it at the verb |
| `widen_snapshot_missing` | `internal/seatcommit/commit.go:176`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `widen_snapshot_attempt_mismatch` | `internal/seatcommit/commit.go:177`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `pr_pending` | `internal/seatcommit/commit.go:1331`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `commit_cancelled` | `internal/seatcommit/commit.go:173`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `tool_unpinned` | `internal/registry/pins.go:30`; classified dynamic refusal | no | 3.2.x item: reserve it at the verb |
| `tool_pin_mismatch` | `internal/registry/pins.go:31`; dynamic refusal at `internal/seatcommit/commit.go:1311` | no | 3.2.x item: reserve it at the verb |
| `verifier_start_failed` | `internal/seatcommit/verifier.go:65`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `verifier_interrupted` | `internal/seatcommit/verifier.go:70`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `verifier_cancelled` | `internal/seatcommit/verifier.go:73`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `verifier_timeout` | `internal/seatcommit/verifier.go:63`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `verifier_output_exceeded` | `internal/seatcommit/verifier.go:64`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `verdict_missing` | `internal/task/class.go:58`; completion writes it at `internal/task/lease.go:732-733` | no | 3.2.x item: reserve it at the verb |
| `verdict_failed` | `internal/task/class.go:59`; completion writes it at `internal/task/lease.go:735-737` | no | 3.2.x item: reserve it at the verb |
| `verdict_tree_mismatch` | `internal/task/class.go:62`; dynamic refusal write above | no | 3.2.x item: reserve it at the verb |
| `task_branch_unprepared` | `internal/scheduler/hooks.go:64`; direct write `internal/scheduler/scheduler.go:991` | no | 3.2.x item: reserve it at the verb |
| `seat_replacement_needs_relaunch` | `internal/scheduler/roster.go:80`; direct write at line 547 | no | 3.2.x item: reserve it at the verb |
| `class_unknown` | spelled in `internal/routing/router.go:518`; route reaches `Store.Block` at `internal/scheduler/scheduler.go:897` | no | 3.2.x item: reserve it at the verb |
| `retries_exhausted` | spelled in `internal/scheduler/hooks.go:115` and `internal/routing/router.go:537,653`; route write at `internal/scheduler/scheduler.go:897` | no | 3.2.x item: reserve it at the verb |
| `declared_tree_unchecked` | `internal/routing/canon.go:41`; route at `internal/routing/router.go:542`, written at `internal/scheduler/scheduler.go:897` | no | 3.2.x item: reserve it at the verb |
| `pin_unresolved` | `internal/routing/router.go:217`; routes at lines 579 and 593, written at `internal/scheduler/scheduler.go:897` | no | 3.2.x item: reserve it at the verb |
| `history_tampered` | `internal/routing/outcome.go:41`; route at `internal/routing/tamper.go:227`, written at `internal/scheduler/scheduler.go:897` | no | 3.2.x item: reserve it at the verb |
| `growth_gate_required` | `internal/task/task.go:31`; `task widen` raises and escalates it at `internal/cli/task_widen.go:361` | documented widening flow at `docs/task-store-design.md:164` | the verb's by design: the widen verb raises it, not `task block` |
| `widen_requested` | `internal/task/task.go:27`; the CLI block path calls `BlockWithPaths` | documented typed request at `docs/v3-spike/311-evidence/t40-typed-widen-attempt-snapshot.md:19` | the verb's by design: typed widen requests |
| dynamic launch failure code | `LaunchFailure.Code` at `internal/scheduler/launchfail.go:60`; direct write at line 259 | no fixed code to reserve | the verb's by design: AMQ stage/reason is the scheduler's repeated-launch block |

## Gates

Paths in commands are evidence-redacted as `<tmp>` and `<gomodcache>`. Every
run used a temporary HOME, `GOPROXY=off`, and the populated offline module
cache. No whole-repository test or host gate-lock script was run.

```text
env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go build ./...
exit 0
```

```text
env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go vet ./internal/task/ ./internal/status/ ./internal/cli/
exit 0
```

```text
env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 -run 'TestSeatHandleRefusesControlsAndInvalidUTF8|TestGrantRefusesControlHandleWithoutChangingTask|TestPlainCoversEveryStatusStringAndKeepsQuestionLines|TestStatusExitsStripControlsFromJSONAndPlain|TestTaskListAndShowStripControls|TestTaskBlockRefusesReservedSchedulerReason|TestStatusFleetMissingRootWarns' ./internal/task/ ./internal/status/ ./internal/cli/
ok  github.com/omriariav/amq-squad/v3/internal/task
ok  github.com/omriariav/amq-squad/v3/internal/status
ok  github.com/omriariav/amq-squad/v3/internal/cli
exit 0
```

The three whole-package commands ran as background jobs and were polled:

```text
env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/task/
ok  github.com/omriariav/amq-squad/v3/internal/task  57.411s
exit 0
```

```text
env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/status/
ok  github.com/omriariav/amq-squad/v3/internal/status  110.193s
exit 0
```

```text
env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/
--- FAIL: TestLeadToolVerbsAreProvedFromTheLeadsDescendant
    lead_tools_proof_test.go:95: listen unix <tmp>/scheduler-decide.sock: bind: operation not permitted
--- FAIL: TestTaskWidenPathIsExact
    lead_tools_proof_test.go:160: listen unix <tmp>/scheduler-decide.sock: bind: operation not permitted
FAIL  github.com/omriariav/amq-squad/v3/internal/cli  175.694s
exit 1
```

The CLI sandbox failure is the permitted local-socket residual. The isolated
clean-environment rerun exercised those sockets and left only the permitted PTY
residual:

```text
env -i HOME=<tmp> TMPDIR=<tmp> PATH=<path> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/
--- FAIL: TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout (0.36s)
    wait_posture_pty_test.go:88: tmux pane identity = "%0_31175\n"
FAIL  github.com/omriariav/amq-squad/v3/internal/cli  62.809s
exit 1
```

```text
env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go run ./scripts/redact-evidence -check docs/v3-spike/320-control-bytes/
exit 0
```

```text
git diff --check
exit 0

grep -c '/User[s]/' docs/v3-spike/320-control-bytes/README.md
0
exit 1 (no match)

LC_ALL=C grep -c '<ESC byte>' docs/v3-spike/320-control-bytes/README.md
0
exit 1 (no match)

git status --porcelain --untracked-files=all
 M internal/cli/fleet.go
 M internal/cli/fleet_test.go
 M internal/cli/status.go
 M internal/cli/task.go
 M internal/task/unclean.go
?? docs/v3-spike/320-control-bytes/README.md
?? internal/cli/control_bytes_test.go
?? internal/status/plain.go
?? internal/status/plain_test.go
?? internal/task/control_handle_test.go
exit 0
```

## Round 2: the raw prints, the fleet warning and the words

`TestTaskListAndShowStripControls` is the task-output row. Pool and declared
files enter through `AddInput`; evidence and cancel reason enter through their
store calls. Dependency ID, the legacy holder, attempt holder/outcome/detail
and legacy report author are changed in the separate stored fixtures at
`internal/cli/control_bytes_test.go:79-143`. Each tag survives as printable
text while every C0, DEL and C1 rune is absent, and the description keeps its
one newline. These nine single-expression reverts were restored after their
red run:

- List pool. Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/ -run '^TestTaskListAndShowStripControls/list$'`. Reverted `status.PlainText(pool, false)` to `pool`. First failing line: `control_bytes_test.go:179: [list] output has control 0x1b`.
- Show pool. Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/ -run '^TestTaskListAndShowStripControls/show$'`. Reverted `plain(t.Pool.Key())` to `t.Pool.Key()`. First failing line: `control_bytes_test.go:179: [show t1] output has control 0x1b`.
- Dependency IDs. Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/ -run '^TestTaskListAndShowStripControls/show$'`. Reverted `deps[i] = plain(id)` to `deps[i] = id`. First failing line: `control_bytes_test.go:179: [show t1] output has control 0x1b`.
- Declared files. Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/ -run '^TestTaskListAndShowStripControls/show$'`. Reverted `files[i] = plain(file)` to `files[i] = file`. First failing line: `control_bytes_test.go:179: [show t1] output has control 0x9b`.
- Lease holder. Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/ -run '^TestTaskListAndShowStripControls/show$'`. Reverted `plain(t.Lease.Holder)` to `t.Lease.Holder`. First failing line: `control_bytes_test.go:179: [show t1] output has control 0x1b`.
- Attempt fields. Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/ -run '^TestTaskListAndShowStripControls/show$'`. Reverted `plain(a.Holder), plain(a.Outcome), plain(a.Detail)` to the three raw fields. First failing line: `control_bytes_test.go:179: [show t1] output has control 0x1b`.
- Evidence loop. Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/ -run '^TestTaskListAndShowStripControls/show$'`. Reverted the shared `plain(kv[1])` to `kv[1]`. First failing line: `control_bytes_test.go:179: [show t1] output has control 0x1b`.
- Report author. Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/ -run '^TestTaskListAndShowStripControls/show$'`. Reverted `plain(t.Report.By)` to `t.Report.By`. First failing line: `control_bytes_test.go:179: [show t1] output has control 0x1b`.
- Description. Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/ -run '^TestTaskListAndShowStripControls/show$'`. Reverted `status.PlainText(t.Description, true)` to `t.Description`. First failing line: `control_bytes_test.go:171: description did not keep its newline`.

`TestStatusFleetMissingRootWarns` is the fleet-warning row. The registry
refuses `gone` followed by ESC/BEL as a squad name at
`internal/fleet/registry.go:297`; the row therefore keeps the accepted name and
uses a missing root ending in C1 CSI, whose path is repeated by the stat error.
Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/cli/ -run '^TestStatusFleetMissingRootWarns$'`.
Reverted `status.PlainText(fleet.WarningRootUnavailable+..., false)` to the raw
warning expression. First failing line: `fleet_test.go:128: warning has control 0x9b`.

`TestPlainCoversEveryStatusStringAndKeepsQuestionLines` is the reflection row.
It changes only the question of the gate made by `fillStatusStrings`, leaving
the gate ID, action class, target digest, state and raiser filled with controls.
Command: `env HOME=<tmp> GOCACHE=<tmp> GOMODCACHE=<gomodcache> GOPROXY=off go test -count=1 ./internal/status/ -run '^TestPlainCoversEveryStatusStringAndKeepsQuestionLines$'`.
Reverted the `plainValue` struct loop by adding `if
v.Type().Field(i).Name == "Gates" { continue }`. First failing line:
`plain_test.go:24: plain document = {`.

Out of scope for 3.2.x: refusing controls at pool, declared-file or root entry;
`actor()`; holders already on disk; `printTaskResult` and other task verbs;
the fleet envelope's own fields; changes to `status.Plain` or `PlainText`; pi/;
schema or store changes; and any newly reserved code beyond
`ledger_tampered`.
