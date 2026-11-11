# 3.2.0 control bytes at the root

Seat-authored controls could enter through a lease holder or status data and
reach a person's terminal. These paths were established by reading the code;
none was exercised on a live team. `SeatHandle` now refuses controls and
invalid UTF-8, and the status contract replaces every C0 and C1 control at its
single exit. Plain task list and show output use the same replacement.

## Seat handles

`SeatHandle` at `internal/task/unclean.go:89-96` trims first, then refuses an
empty or path-like handle, invalid UTF-8, and any rune below U+0020 or from
U+007F through U+009F. The existing safely quoted refusal is unchanged.
`TestSeatHandleRefusesControlsAndInvalidUTF8` covers ESC/BEL, C1 CSI, DEL, tab,
an invalid lone byte, ordinary ASCII, Unicode, and trimming.
`TestGrantRefusesControlHandleWithoutChangingTask` covers the store level a
seat reaches: grant is refused, its error contains no raw control, and the task
remains pending without a lease.

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
whose first plain line the screen displays. An argv string is treated like any
other string: a handle containing a control becomes a different, harmless name
that matches nothing.

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

The list row at `internal/cli/task.go:413-414` sanitizes holder, wait/block
reason, and title. `printTaskDetails` at lines 708-758 sanitizes every stored
string it prints; descriptions alone preserve newlines. JSON remains the
stored contract and relies on JSON escaping. `TestTaskListAndShowStripControls`
covers both plain outputs.

The CLI-only reserved-reason list is at `internal/cli/task.go:760-766` and
contains only `ledger_tampered`. The block case refuses it at lines 632-637
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
| Buzz phone replies, gate questions, and alerts | `internal/cli/remote.go:652-656` hands status and handlers to `internal/buzz`; replies carry received message text, questions carry gate text, and alert detail can contain stored errors. A seat can write a reply body and some status-backed text. | 3.2.x item: apply the shared plain-text rule at every phone-bound publish. |
| Seat doorbell and lead notice | `internal/schedproc/amq.go:72-122` builds AMQ subjects and bodies from task title, block detail, and scheduler text; a seat can write the title or detail. | 3.2.x item: sanitize both scheduler AMQ bodies before send. |
| Other task verb results and refusals | `internal/cli/task.go:691-705` prints title and lease holder; refusal construction also quotes stored title or reason. Seats can write reasons and evidence, while `task add` writes titles and `task done` writes evidence. | 3.2.x item: sanitize every remaining human task result/refusal exit rather than rejecting stored prose. |
| Seat last message | `internal/status/collect.go:513-536` copies the last AMQ subject/body into `Seat.LastMessage`. A seat can send it. Every status reader receives the document after `Plain`. | covered by step 2 |
| Scheduler stdout and `up` roster | `internal/cli/scheduler_run.go:348` prints configured root/session/lead; `internal/cli/up.go:1217` reads decisions for roster progress. Decision text can contain seat-written fields. | covered by step 2 for roster status; 3.2.x item: sanitize scheduler-run lines that bypass status. |
| Merge-train plan and refusals | `internal/cli/merge_train.go` prints stored plan/refusal text, including task and alert fields that a seat can influence. | 3.2.x item: sanitize merge-train human output at its writer. |
| Fleet document's own fields | Fleet registry name, project, profile, session, lead, and error fields are assembled outside each squad status; configuration can supply them. Squad documents pass through `buildStatusContract`. | 3.2.x item: sanitize the fleet envelope's own strings before JSON or table output. |
| Decisions log writer | `internal/schedproc/process.go:346` passes the decision to JSONL; `internal/schedproc/decisions.go:106` writes it. Besides status, `up` and recovery paths read lines and can print their text. | 3.2.x item: sanitize every human decisions-log reader while preserving the forensic record on disk. |

No path not read is claimed settled.

## Other block reason codes

| Code and writer | Verb use today | Disposition |
| --- | --- | --- |
| `outside_declared_files`, `internal/cli/seat_commit.go:172`, via `RefuseCommitPaths` | A lead-tools proof test also sends it through `task block`. | 3.2.x item: reserve it at the verb |
| `commit_failed` and `verifier_interrupted`, `internal/scheduler/hooks.go:212-213`, written by the scheduler commit path | No documented seat use found. | 3.2.x item: reserve it at the verb |
| `task_branch_unprepared`, `internal/scheduler/hooks.go:64`, written at `internal/scheduler/scheduler.go:991` | No documented seat use found. | 3.2.x item: reserve it at the verb |
| `grants_invalid`, `internal/task/verdict.go:46`, written at `internal/scheduler/scheduler.go:964` | No documented seat use found. | 3.2.x item: reserve it at the verb |
| `seat_replacement_needs_relaunch`, `internal/scheduler/roster.go:80`, written at line 547 | No documented seat use found. | 3.2.x item: reserve it at the verb |
| `growth_gate_required`, `internal/task/task.go:31` | The task scope flow uses the verb intentionally. | the verb's by design: growth-gate requests |
| `widen_requested`, `internal/task/task.go:27` | `task block` is the typed seat widen request. | the verb's by design: typed widen requests |
| `hold` | Lead task holding uses this free-form reason. | the verb's by design: lead holds |

## Gates

All commands used a temporary HOME, `GOPROXY=off`, and the populated offline
module cache. `go build ./...` exited 0. `go vet ./internal/task/
./internal/status/ ./internal/cli/` exited 0. The focused six-row command over
the three packages exited 0. Whole-package `internal/task` and
`internal/status` runs exited 0. The sandboxed `internal/cli` run exited 1
because local decision and lead sockets were denied. Its isolated unrestricted
rerun completed with only the permitted PTY residual:

```text
--- FAIL: TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout (0.38s)
    wait_posture_pty_test.go:88: tmux pane identity = "%0_98537\n"
```

No whole-repository test or host gate-lock script was run. The evidence
redactor and final diff checks are recorded by their successful gate results.
