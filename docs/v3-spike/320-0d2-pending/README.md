# Why t72, t73 and t74 appeared as bare `pending`

## Finding

The store and status fields were correct. The missing wait line was a visibility
defect: v3.1.2 passed all 76 task rows to the Pi widget in status order, with
closed tasks first. At the height shown in the supplied lead capture, only three
task rows were visible:

```text
tasks (76)
  t7 cancelled: 3.0.x: redact linearity test as an in-process ratio (no w...
  t24 cancelled: 3.0.2 blocker: the commit step pushes the seat branch bef...
  t9 cancelled: 3.0.x: seats are launched with AMQ wake enabled so the sc...
  ... (widget truncated)
```

In three independent reconstructions, t72, t73 and t74 were task rows 41, 42
and 43. None of the three rows was inside the three-row visible region; that
holds for all three tasks. t74's file-lock wait was observed (`task show t74
--json` at 14:06:56Z: state pending, wait_reason file_lock); the row text
`waiting: t71 hold its files` is reconstructed for all three tasks
(screen-run-v3.1.2.txt), and for t72 and t73 the wait itself is inferred from
the same code path and their cancellation records. The lead's reported bare
`pending` came from `task show`, while the screen supplied no row for those
tasks. There is no captured screen row that lost a wait field.

This is still a 3.2.0 screen defect. It is an ordering and bounded-height
presentation defect, not a status-classification defect.

## Sources and integrity

Investigation inputs came only from
`/Users/omri/scratch-320-0/snapshot-0d2/` and detached scratch worktrees at:

- v3.1.2, commit `5bd394e6dcf9fd06254f80ebc846d92130dc9048`;
- PR #892 head `874ced4bcf0b361e0e7d1612f155440087930929`.

The live `.agent-mail/v31` task data was not read. No network was used. The
supplied [MANIFEST.txt](MANIFEST.txt) is copied verbatim, and every listed
SHA-256 digest matched. The full-widget fixture is
[fixture-v3.1.2-widget-order.json](fixture-v3.1.2-widget-order.json), filtered
from the snapshot's status shape to t1 through t76 while preserving recorded
order.

## Rebuilt state

t71's attempt was granted to `reviewer` at 13:50:14Z. It remained in progress
through all three target lifetimes and declared
`docs/v3-spike/320-evidence/`. Each target was class `plan`, attempt zero,
and declared exactly that same path.

| Task | Recorded lifetime | Store evidence | v3.1.2 status result | Widget row |
|---|---|---|---|---|
| t72 | 14:04:09.010Z to 14:04:56.950Z | cancellation later cleared its current wait value | `wait_reason=file_lock`, `wait_holders=[t71]` | 41 |
| t73 | 14:04:56.980Z to 14:06:45.491Z | cancellation later cleared its current wait value | `wait_reason=file_lock`, `wait_holders=[t71]` | 42 |
| t74 | 14:06:45.523Z to 14:07:16.525Z | the 14:06:56 task-show observation and cancellation reason both name t71's file lock | `wait_reason=file_lock`, `wait_holders=[t71]` | 43 |

The fixture cases are independent. They do not claim the three targets were
pending simultaneously. For each case, t71 and that target are overridden to
their recorded live states while the snapshot's t1 through t76 ordering is
retained.

## When the wait is written

At v3.1.2, `task add` creates a pending task at
`internal/task/task.go:222-230`. On a scheduler tick:

1. `internal/scheduler/scheduler.go:797-806` calls `task.Blocker`.
2. `internal/task/lease.go:38-42` returns `file_lock` for overlapping
   declarations held by an in-progress or blocked task.
3. `internal/scheduler/scheduler.go:997-1002` calls `Store.SetWait`.
4. `internal/task/lease.go:581-590` writes the wait without a scheduler
   decision-log row.

This explains why the 167-line scheduler window has no t72, t73 or t74 event.
The empty routing-decision file is also expected because the blocker stops the
tick before routing. Cancellation clears `WaitReason` at
`internal/task/lease.go:565-576`, explaining the final cancelled records.

## Classifier and per-task screen runs

The detached v3.1.2 test constructed t71 and each target solely from snapshot
facts and passed them to `status.Build`. The classifier fills holders at
`internal/status/classify.go:164-169`; `lockHolders` at lines 420-431
requires a pending file-lock waiter, an in-progress or blocked holder, and
overlapping declared files. All conditions hold.

The exact classifier output is in
[status-run-v3.1.2.txt](status-run-v3.1.2.txt). Rendering
[fixture-v3.1.2.json](fixture-v3.1.2.json) produces the lines in
[screen-run-v3.1.2.txt](screen-run-v3.1.2.txt).

At #892, the same classification also fills
`waits_for=[{task_id:t71,path:docs/v3-spike/320-evidence/}]`. See
[status-run-892.txt](status-run-892.txt),
[fixture-892.json](fixture-892.json), and
[screen-run-892.txt](screen-run-892.txt).

The reusable per-task harness is [render-screen.test.mjs](render-screen.test.mjs).
Both cases passed:

```sh
T86_SCREEN_SOURCE=<v3.1.2-worktree> \
  T86_SCREEN_FIXTURE=fixture-v3.1.2.json \
  T86_SCREEN_EXPECTED='waiting: t71 hold its files' \
  node --test --test-reporter=spec docs/v3-spike/320-0d2-pending/render-screen.test.mjs

T86_SCREEN_SOURCE=<892-worktree> \
  T86_SCREEN_FIXTURE=fixture-892.json \
  T86_SCREEN_EXPECTED='waits for t71: both declare docs/v3-spike/320-evidence/' \
  node --test --test-reporter=spec docs/v3-spike/320-0d2-pending/render-screen.test.mjs
```

## Full-widget run

[render-widget-order.test.mjs](render-widget-order.test.mjs) feeds all t1
through t76 rows to the v3.1.2 renderer in their recorded status order. It
overrides t71 and one target per independent case, applies the three-task-row
height established by the supplied capture, and asserts both the visible rows
and the target's full rendered line.

The run in [screen-run-v3.1.2-widget-order.txt](screen-run-v3.1.2-widget-order.txt)
proves:

- the visible rows are t7, t24 and t9, all closed;
- t72, t73 and t74 are below the cut at rows 41, 42 and 43;
- each hidden row already contains the correct file-lock explanation.

`renderScreen` itself does not sort or summarize the tasks. At
`pi/extensions/lib/screen.ts:218-224` it emits every row in the order received
from status. At lines 432-434, the screen passes that complete line array to
`setWidget`; bounded terminal space is what makes the tail unavailable in the
lead capture.

## Proposed fix

The next brief should change task presentation ordering so open tasks are shown
first and completed or cancelled tasks are last or folded, then reserve a final
`+N more` line whenever the widget cannot show the whole task list. Do not
change status classification for this incident: both v3.1.2 and #892 already
fill the file-lock fields. Keep CLI store state labeled separately from screen
wording so `state: pending` cannot be mistaken for a visible task row.
