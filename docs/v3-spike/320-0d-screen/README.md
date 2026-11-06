# 3.2.0-0d screen and store evidence

Recorded on 2026-11-05. The commands below use temporary one-line production
reverts in the seat worktree. Each production line was restored immediately
after its expected failing run. No test used a real AMQ session root.

This follow-up carries the complete 0d5 revision at `767e81d6` from
`origin/seat/reviewer-t95`.

The 0d7 ruling keeps brief 4-1 rule 2: an extension may name a pi package as
a type, but may not load one at run time. The 3.1.0 failure showed why: a
missing load-time dependency prevents the extension from registering and can
stop the lead from starting. Screen width handling therefore lives in
`pi/extensions/lib/width.ts`; only the test imports pi-tui, to pin the local
contract to its `visibleWidth` and `truncateToWidth` results. This follow-up
carries the complete #902 attempt at `184745f1`; its `N closed` tail resolves
the 3.2.x exact-open-fit item because the tail remains visible when open rows
consume the task area exactly.

## The local width contract is pinned to pi-tui

The corpus covers ASCII, CSI colour, an OSC 8 link, CJK, emoji including ZWJ
sequences, combining marks, and mixed text. It checks every width from one
column through one beyond the input, including cuts that land at a wide
character and beside escape sequences. For every case the local helpers equal
pi-tui, the result is no wider than the requested width, and every escape is
complete. Truncation never keeps half a wide grapheme, closes an active OSC 8
link, and resets styling before the ellipsis.

Command:

```sh
cd pi
node --test --test-reporter=spec --test-name-pattern='local width helpers' test/screen.test.ts
```

Reverted expression 1: the wide-range result in `eastAsianWidth` was changed
from two columns to one. First failing line:

```text
✖ the local width helpers stay pinned to pi-tui across ANSI, wide, emoji, combining, and mixed text
```

The first mismatch was the CSI-coloured CJK case, `11 !== 13`.

Reverted expression 2: `visibleWidth` added each escape's byte length instead
of skipping the complete sequence. The same row failed first; its invariant
reported `"\u001b[0m.\u001b[0m" is wider than 1`. Both reverted runs exited 1;
the restored run exited 0.

## Lint closes the dynamic-import and require gap

Rule 2 now checks static imports, dynamic `import()` calls, and `require()`
calls under `pi/extensions/`. Tests remain outside that production rule so the
corpus can import the dev dependency. The row copies the package, extensions,
and lint script to a temporary directory and adds a fixture with both forbidden
forms; it never runs the fixture against the real tree.

Command:

```sh
cd pi
node --test --test-reporter=spec --test-name-pattern='lint rejects dynamic' test/lint.test.ts
```

Reverted expression: the dynamic/require matcher was removed, restoring the
old static-import-only scan. First failing line:

```text
✖ lint rejects dynamic import() and require() of pi packages under extensions
```

The copied lint incorrectly printed `lint ok: 16 files, pi 1.3.0` and exited
0; the row expected exit 1. The restored row exited 0.

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

## `render(width)` keeps every physical line inside pi-tui's contract

Pi-tui defines a component as `render(width): string[]` and says each returned
line must not exceed `width` (`pi/node_modules/@earendil-works/pi-tui/README.md:260`).
The regular-screen differential path checks `visibleWidth(line) > width`, stops
the TUI, and throws (`dist/tui-main-screen.js:469`, `:484`, `:493`). A full
redraw instead writes the line directly (`tui-main-screen.js:263`), leaving the
terminal to wrap it, while the fullscreen path clips it
(`tui-alt-screen.js:1459`). Thus an over-wide line either crashes the lead's
regular-mode TUI or breaks the measured fit, depending on the redraw path.

The widget now passes every returned line through the local
`truncateToWidth` before counting it. That includes fixed lines, task lines,
record lines, closed and overflow tails, the stale-error line, and the
fixed-overflow message. Since truncation keeps a prefix, a cut task or record
keeps its id and plain words. The local helper is pinned to pi-tui by the corpus
above.

Command:

```sh
cd pi
HOME=/private/tmp/amq-squad-reviewer-t100-home node --test --test-reporter=spec --test-name-pattern='render\(width\)' test/screen.test.ts
```

Reverted expression: `return fitWidth(lines)` in the widget's `draw` function
was changed to `return lines`, making `render(width)` ignore the width.

First failing line:

```text
✖ render(width) keeps long task, record, stale-error, and truncation lines within pi-tui's contract
```

The local `acceptLikePiTui` test helper reproduced pi-tui's differential check
and threw `Rendered line 5 exceeds terminal width (51 > 50).` The fixture
constructs that task line to be exactly one column too wide. This is a local
reproduction of the check, not a direct pi-tui runtime throw. The reverted run
exited 1; the restored run exited 0.

The fixed-overflow message has its own narrow-width row. At width 12 the
message itself is longer than the pane and must pass through the same helper.
Removing the `fitWidth` call returned the full `… 35 more lines; enlarge the
pane`; the first failing line was:

```text
✖ the fixed-overflow message is width-fitted even when the pane is narrower than the message
```

## The measured scan starts at the pane-sized task cap

The scan starts at `min(task count, pane rows)`. A larger starting cap cannot
produce a distinct fitting screen. Redraws happen on keystrokes, streaming,
and the loader cadence, so bounding that repeated scan is material. A renderer
seam counts the actual attempts for 200 closed and 30 open tasks, and requires
at most `min(task count, pane rows) + 2` calls.

The fixed-plus-three fixture proves the bounded scan still admits the blocked
task, its record, and the rule when those three rows fit.

Command:

```sh
cd pi
node --test --test-reporter=spec --test-name-pattern='bounds its task-cap renders' test/screen.test.ts
```

Reverted expression: the descending cap loop started at the full task count
instead of `min(task count, pane rows)`.

First failing line:

```text
✖ the widget bounds its task-cap renders by the smaller of task count and pane rows
```

The reverted row observed `230 renders for 230 tasks in 37 rows`; it exited 1.
The fixed-plus-three row separately proves the bounded scan still shows the
task, rule, and record. The restored runs exited 0.

## An exact open fit still names folded closed work

When open tasks consume the requested task cap exactly and closed tasks remain,
the task block now appends `N closed`. Height remains governed by measurement,
not by treating the cap as a prediction.

Command:

```sh
cd pi
HOME=/private/tmp/amq-squad-reviewer-t100-home node --test --test-reporter=spec --test-name-pattern='task rows put actionable' test/screen.test.ts
```

Reverted expression: the exact-fit return was changed from
``{ tasks: open, tail: `${closed.length} closed` }`` to `{ tasks: open }`.

First failing line:

```text
✖ task rows put actionable work first and fold closed work into the available height
```

The reverted screen ended at the last open task instead of `73 closed`. It
exited 1; the restored run exited 0.

## Fixed overflow handles rows one, zero, and stale errors

Command for the one-row and zero-row boundary:

```sh
cd pi
HOME=/private/tmp/amq-squad-reviewer-t100-home node --test --test-reporter=spec --test-name-pattern='fixed-content overflow at' test/screen.test.ts
```

Reverted expression: the `if (fixed.length > rows)` fixed-overflow branch was
disabled.

First failing line:

```text
✖ fixed-content overflow at one row keeps the pane boundary
✖ fixed-content overflow at zero rows keeps the pane boundary
```

The reverted one-row render returned the entire fixed screen instead of its one
truncation line, and the zero-row render returned that same screen instead of
an empty array. The reverted run reported both failures and exited 1; the
restored run exited 0.

Command for the stale-error boundary:

```sh
cd pi
HOME=/private/tmp/amq-squad-reviewer-t100-home node --test --test-reporter=spec --test-name-pattern='counts a stale-error' test/screen.test.ts
```

Reverted expression: the stale-error splice in `draw` was disabled.

First failing line:

```text
✖ fixed-content overflow counts a stale-error line before truncating
```

The reverted truncation line reported 31 hidden lines instead of 32. It exited
1; the restored run exited 0.

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

The 0d6 verification reran every carried gate offline with temporary state.
These are the exact commands and recorded exit codes:

```sh
HOME=/private/tmp/amq-squad-reviewer-t100-home GOCACHE=/private/tmp/amq-squad-reviewer-t100-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/task -run 'TestFilesOverlap' -count=1                         # exit 0
HOME=/private/tmp/amq-squad-reviewer-t100-home GOCACHE=/private/tmp/amq-squad-reviewer-t100-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/cli -run '^TestTaskAddReportsFileWaitsInTextAndJSON$' -count=1 # exit 0
HOME=/private/tmp/amq-squad-reviewer-t100-home GOCACHE=/private/tmp/amq-squad-reviewer-t100-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/status -run 'TestAFileLockWaitNamesItsHolders|TestGoldenRoundTripAdditiveBothWays|TestFleetGoldenIsTheSquadGoldenWrapped' -count=1 # exit 0
HOME=/private/tmp/amq-squad-reviewer-t100-home GOCACHE=/private/tmp/amq-squad-reviewer-t100-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/routing -run '^TestLeadPin$' -count=1                    # exit 0
HOME=/private/tmp/amq-squad-reviewer-t100-home GOCACHE=/private/tmp/amq-squad-reviewer-t100-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/routing -run '^TestFix2HistoryIsCheckedNotTrusted$' -count=1 # exit 0
HOME=/private/tmp/amq-squad-reviewer-t100-home GOCACHE=/private/tmp/amq-squad-reviewer-t100-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/scheduler -run '^TestNoRosterSeatIsANamedWaitNotARepeatedLogLine$' -count=1 # exit 0
cd pi
HOME=/private/tmp/amq-squad-reviewer-t100-home node --test --test-reporter=spec test/screen.test.ts test/words.test.ts # exit 0, 36 passed
HOME=/private/tmp/amq-squad-reviewer-t100-home TMPDIR=/private/tmp/amq-squad-reviewer-t100-tmp npm test # exit 0, 125 passed, 1 skipped
HOME=/private/tmp/amq-squad-reviewer-t100-home node scripts/lint.mjs # exit 0
cd ..
HOME=/private/tmp/amq-squad-reviewer-t100-home GOCACHE=/private/tmp/amq-squad-reviewer-t100-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go run ./scripts/redact-evidence -check docs/v3-spike/320-0d-screen/ # exit 0
```

## 0d7 verification

The 0d7 gates used temporary HOME and Go caches, `GOPROXY=off`, and no real
session root. Exact commands and recorded exit codes:

```sh
HOME=/private/tmp/amq-squad-reviewer-t102-home GOCACHE=/private/tmp/amq-squad-reviewer-t102-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go build ./... # exit 0
HOME=/private/tmp/amq-squad-reviewer-t102-home GOCACHE=/private/tmp/amq-squad-reviewer-t102-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go vet ./... # exit 0
HOME=/private/tmp/amq-squad-reviewer-t102-home GOCACHE=/private/tmp/amq-squad-reviewer-t102-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/task -run 'TestFilesOverlap' -count=1 # exit 0
HOME=/private/tmp/amq-squad-reviewer-t102-home GOCACHE=/private/tmp/amq-squad-reviewer-t102-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/cli -run '^TestTaskAddReportsFileWaitsInTextAndJSON$' -count=1 # exit 0
HOME=/private/tmp/amq-squad-reviewer-t102-home GOCACHE=/private/tmp/amq-squad-reviewer-t102-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/status -run 'TestAFileLockWaitNamesItsHolders|TestGoldenRoundTripAdditiveBothWays|TestFleetGoldenIsTheSquadGoldenWrapped' -count=1 # exit 0
HOME=/private/tmp/amq-squad-reviewer-t102-home GOCACHE=/private/tmp/amq-squad-reviewer-t102-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/routing -run '^TestLeadPin$' -count=1 # exit 0
HOME=/private/tmp/amq-squad-reviewer-t102-home GOCACHE=/private/tmp/amq-squad-reviewer-t102-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/routing -run '^TestFix2HistoryIsCheckedNotTrusted$' -count=1 # exit 0
HOME=/private/tmp/amq-squad-reviewer-t102-home GOCACHE=/private/tmp/amq-squad-reviewer-t102-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go test ./internal/scheduler -run '^TestNoRosterSeatIsANamedWaitNotARepeatedLogLine$' -count=1 # exit 0
cd pi
HOME=/private/tmp/amq-squad-reviewer-t102-home TMPDIR=/private/tmp npm test # exit 0, 130 passed, 1 skipped
HOME=/private/tmp/amq-squad-reviewer-t102-home node scripts/lint.mjs # exit 0, lint ok: 31 files, pi 1.3.0
cd ..
HOME=/private/tmp/amq-squad-reviewer-t102-home GOCACHE=/private/tmp/amq-squad-reviewer-t102-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off go run ./scripts/redact-evidence -check docs/v3-spike/320-0d-screen/ # exit 0
git diff --check # exit 0
```

The repository-wide offline CI command was also run with an empty environment,
the original macOS per-user TMPDIR, and no AMQ pin variables:

```sh
env -i PATH="$PATH" HOME=/private/tmp/amq-squad-reviewer-t102-home TMPDIR=/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/ GOCACHE=/private/tmp/amq-squad-reviewer-t102-gocache GOMODCACHE=/private/tmp/amq-squad-reviewer-t95-gomodcache GOPROXY=off NPM_FLAGS=--offline make ci # exit 2
```

Every package except `internal/cli` passed. Its only failure was the unrelated,
host-specific `TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout`, which
received tmux pane identity `%0_5452`; this is the same environment-specific
residual already recorded in `docs/v3-spike/5-302-evidence/t27-typed-widen.md`.
The task-specific CLI row above passed, and the full pi suite, including all
new width, render-count, overflow, and lint rows, passed.
