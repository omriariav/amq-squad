# 3.2.0-0d screen and store evidence

Recorded on 2026-11-05. The commands below use temporary one-line production
reverts in the seat worktree. Each production line was restored immediately
after its expected failing run. No test used a real AMQ session root.

This follow-up carries the previous attempt, PR #895 at
`c5827b93e2c56bc34b1746134d0567eebfdb2d00`.

## A file-lock wait names the holder and declaration

Command:

```sh
cd pi
node --test --test-reporter=spec --test-name-pattern='file lock' test/screen.test.ts
```

Reverted line: the `waitWords` branch for `file_lock` with populated
`waits_for` was disabled in `pi/extensions/lib/words.ts`.

First failing line:

```text
✖ a pending task waiting on a file lock names its holder and shared declaration
```

The failure showed the old shipped text, `t26 waiting: t71 hold its files`, in
place of `t26 waits for t71: both declare docs/v3-spike/320-evidence/`.

## `task add` reports overlap in text and JSON

Command:

```sh
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/cli -run '^TestTaskAddReportsFileWaitsInTextAndJSON$' -count=1
```

Reverted line: the `AddWithFileWaits` append guarded by
`task.OverlapDeclaration` was disabled in `internal/task/task.go`.

First failing line:

```text
--- FAIL: TestTaskAddReportsFileWaitsInTextAndJSON (0.07s)
```

The assertion received only `Added t2 "overlap": pending` and required
`t2 will wait for t1: both declare docs/x.md`. The fixture deliberately uses
`docs/**` against `docs/x.md`, a pair for which a prefix-only test disagrees
with `task.FilesOverlap`.

## `pin_unresolved` names the class chain

Command:

```sh
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/routing -run '^TestLeadPin$' -count=1
```

Reverted line: the `pinUnresolvedDetail` result in
`internal/routing/router.go` was replaced with the bare `pin_unresolved`
detail.

First failing line:

```text
--- FAIL: TestLeadPin (0.00s)
```

The route carried `Detail:pin_unresolved` instead of the pinned class chain.

## A retry and a first pin use the same diagnostic

Command:

```sh
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/routing -run '^TestFix2HistoryIsCheckedNotTrusted$' -count=1
```

Reverted expression: the retry branch's
`r.pinUnresolvedDetail(t.Class, entry)` call was replaced with its former
ad-hoc `routing: the first attempt ...` formatter.

First failing line:

```text
--- FAIL: TestFix2HistoryIsCheckedNotTrusted (0.00s)
```

The route still blocked with `pin_unresolved`, but its detail did not start
with the pinned entry or use the shared class-chain form.

## A long class chain keeps the complete pin

Command:

```sh
cd pi
node --test --test-reporter=spec --test-name-pattern='pin_unresolved uses' test/words.test.ts
```

Reverted expression: `clean(detail, 4096)` in `pinUnresolved` was changed
back to `clean(detail, max)`, cutting the whole detail before the chain
could be shortened from its end.

First failing line:

```text
✖ pin_unresolved uses the class-chain detail from task show
```

The old result ended in `vendor:mod...`; the required result keeps the pin
whole and ends the shortened chain with `, +N more`.

## The bounded task window orders live work before history

Command:

```sh
cd pi
node --test --test-reporter=spec --test-name-pattern='task rows put actionable' test/screen.test.ts
```

Reverted expression: the `taskRank` comparator was replaced by source-order
sorting.

First failing line:

```text
✖ task rows put actionable work first and fold closed work into the available height
```

The three-row window rendered `t74, t73, t72`; it must render blocked
`t72`, in-progress `t73`, then pending `t74`. A separate row checks
that an open overflow reserves the last line for
`+N more (/team for all)`.

## The widget measures the rendered screen

Command:

```sh
cd pi
HOME=/private/tmp/amq-squad-reviewer-t95-home node --test --test-reporter=spec --test-name-pattern='the widget measures' test/screen.test.ts
```

The widget measures rather than predicts: it renders task caps from largest to
smallest and keeps the largest rendered screen that fits. One fixture has no
fixed record, one blocked task with an unknown reason, no seat stop, no train
run, and no warning with a record. The other is the existing fixture. Each is
probed at the fixed height and with one and two spare lines, and none exceeds
the pane.

Revert command:

```sh
cd pi
HOME=/private/tmp/amq-squad-reviewer-t95-home node --test --test-reporter=spec --test-name-pattern='the widget measures the rendered screen with no fixed records' test/screen.test.ts
```

Reverted expression: the measured high-to-low cap loop was removed and the old
`rowCost` callback plus `taskHeight = terminal.rows - fixed` choice was restored.

First failing line:

```text
✖ the widget measures the rendered screen with no fixed records and never exceeds the pane
```

The first assertion was `12 lines in a 11-row pane`: the old callback charged
two lines for the blocked task, but drawing the first record also introduced
the rule, so the actual cost was three.

## Fixed-content overflow ends with a truncation line

Command:

```sh
cd pi
HOME=/private/tmp/amq-squad-reviewer-t95-home node --test --test-reporter=spec --test-name-pattern='the widget replaces fixed-content overflow with a truncation line' test/screen.test.ts
```

When fixed content alone exceeds the pane, the widget draws the first
`rows - 1` lines and ends with `… N more lines; enlarge the pane`, where `N`
is the number of source lines cut. The row checks that a five-row pane gets
exactly five lines and the exact count in its last line.

Reverted expression: the `if (fixed.length > rows)` truncation branch was
removed, leaving the measured cap loop to fall back to the full fixed screen.

First failing line:

```text
✖ the widget replaces fixed-content overflow with a truncation line
```

The first assertion was `35 !== 5`.

## Tick decision durability boundary

tick decisions are written after the tick; a stop between the store write and the log write loses the line, the stored state stands

## Row 3 catches the old file-lock wording on its own

Command:

```sh
cd pi
node --test --test-reporter=spec --test-name-pattern='row 3: a seat says' test/words.test.ts
```

Reverted expression: the `waitWords` special case for a `file_lock` with
`waits_for` was disabled, restoring the old `WAIT_WORDS.file_lock` rendering.

First failing line:

```text
✖ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
```

The standalone failure said the screen had no line matching
`t32 waits for t71: both declare docs/v3-spike/320-evidence/`; the reverted
output was `t32 waiting: t71 hold its files`.

## A wait change writes one decision, not one per tick

Command:

```sh
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/scheduler -run '^TestNoRosterSeatIsANamedWaitNotARepeatedLogLine$' -count=1
```

Reverted expression: the successful `SetWait` transition's
`wait_changed` append was disabled.

First failing line:

```text
--- FAIL: TestNoRosterSeatIsANamedWaitNotARepeatedLogLine (0.05s)
```

The first tick returned no decision. The green row also changes the stored
reason, observes exactly one new decision, and observes none on the next
unchanged tick.

## Green verification

```sh
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/task -run 'TestFilesOverlap' -count=1
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/cli -run '^TestTaskAddReportsFileWaitsInTextAndJSON$' -count=1
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/status -run 'TestAFileLockWaitNamesItsHolders|TestGoldenRoundTripAdditiveBothWays|TestFleetGoldenIsTheSquadGoldenWrapped' -count=1
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/routing -run '^TestLeadPin$' -count=1
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/routing -run '^TestFix2HistoryIsCheckedNotTrusted$' -count=1
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/scheduler -run '^TestNoRosterSeatIsANamedWaitNotARepeatedLogLine$' -count=1
cd pi
node --test --test-reporter=spec --test-name-pattern='file lock|pin_unresolved|class chain|task rows|task overflow|widget measures|fixed-content overflow|row 3' test/screen.test.ts test/words.test.ts
node scripts/lint.mjs
cd ..
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go run ./scripts/redact-evidence -check docs/v3-spike/320-0d-screen/
```
