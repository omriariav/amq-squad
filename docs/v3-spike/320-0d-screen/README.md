# 3.2.0-0d screen and store evidence

Recorded on 2026-11-05. The commands below use temporary one-line production
reverts in the seat worktree. Each production line was restored immediately
after its expected failing run. No test used a real AMQ session root.

## Round 2 carry

The escalated `git update-index -q --refresh` exited 0 with no output. The
exact `fcd95493.patch` had sha256
`bb47d342c370e9f9c943d924e2fdd4c7a0833c1f5b4a218c50472d18a266d097`;
`git apply --3way` exited 0. The resulting 25 paths were exactly the entries
of `blobs.txt`, whose sha256 was
`42e0be2a98caa16bd32660e472bc5c4db146a3fecb8b5ffe0369fb69048da5c9`,
with no blob mismatch output, conflict marker, or `git diff --check` output.
The escalated `git add -A && git write-tree` printed
`43508553bffbfbd1e7c7b4db8e616f86eaf00d07`.

The 0d9 follow-up carries `fe6fdc2cd39fbb7b9eb40f620788f24630307be7`
onto `c65a709c690dd0ed5877076cfeebf470da339262`. Cherry-pick produced only the
five declared additive conflict hunks:

1. `internal/cli/task_test.go` retained the v3 claim/pull tests and the
   carried task-add tests.
2. `internal/scheduler/scheduler.go` retained the v3 state maps and the
   carried `tickLog`.
3. `pi/extensions/lib/words.ts` retained the writer and pin helpers.
4. `pi/extensions/lib/words.ts` retained the `blockNeedsRecord` reasons.
5. `pi/extensions/lib/words.ts` retained the routine decision kinds.

This follow-up carries the complete 0d5 revision at `767e81d6` from
`origin/seat/reviewer-t95`.

The 0d8 ruling carries #906 at `e060a4ef` and replaces its equality contract.
An extension may name a pi package as a type, but may not load one at run time,
so production width handling remains local to `pi/extensions/lib/width.ts` and
only the tests import the installed pi-tui. The complete #902 attempt at
`184745f1` remains carried too; its `N closed` tail resolves the exact-open-fit
item because the tail remains visible when open rows consume the task area.

## The local width measure is checked against the installed pin

The safety rule is: for every string `x`, local `visibleWidth(x)` is at least
pi-tui's width, and for every requested `w`, pi-tui's width of our truncated
output is at most `w`. Printable ASCII counts 1 and tab counts 3. The only
non-ASCII width-1 claims are Latin-1 U+00C0-U+00FF except multiplication and
division signs, and Hebrew letters U+05D0-U+05EA. Every other code point counts
2, per code point rather than per grapheme. This deliberately over-cuts text
outside the small allowlist; it avoids copying a Unicode table that could drift
at the next pi-tui upgrade.

The property row checks all 10,406 corpus and generated inputs at every width
from zero through the local input width plus one. Its classes include every CSI
final byte U+0040-U+007E, private-mode and incomplete CSI, OSC terminated by BEL
and ST, OSC 8 links, APC, ZWJ emoji including a seven-code-point family,
combining and spacing marks, Hebrew with niqqud, every allowlisted code point,
CJK, tabs, and lone/paired surrogates. It also includes the two #906 follow-up
classes: an SGR escape inside the keycap cluster `1` + U+FE0F U+20E3, and an
SGR escape between U+D83D and U+DE42. Pi-tui strips the escape and then segments
across it; the local measure never segments across an escape and counts the
remaining code points separately. The 0d9 corpus also covers DCS, SOS, PM, and
the generic `ESC(B` run. This is a compatibility test against the installed
pi-tui 1.3.0 pin. A dependency bump reruns it and may fail if pi-tui recognizes
different escapes or assigns a width above the local bound of 2.

Command:

```sh
cd pi
HOME=/private/tmp/amq-squad-t104-home node --test --test-reporter=spec --test-name-pattern='local measure' test/screen.test.ts
```

Reverted expression 1: CJK U+4E00-U+9FFF was admitted to the width-1 allowlist.
The run exited 1. First failure:

```text
AssertionError: "汉字かなカナ": ours 10, pi-tui 12
```

Reverted expression 2: the default per-code-point result changed from 2 to 1.
The run exited 1 at `"汉字かなカナ": ours 6, pi-tui 12`.

Reverted expression 3: the CSI skip accepted every final byte U+0040-U+007E
instead of only pi-tui's `m`, `G`, `K`, `H`, and `J`. The run exited 1 at
`"\\u001b[?25lprivate": ours 7, pi-tui 12`. Unknown finals remain visible text
for both measures. The quotes for expressions 2 and 3 are fragments of their
assertion lines; the complete lines are under `## 0d9 controlled reverts`.

The restored property row exited 0.

## A cut never lands inside an ESC-led run

Known CSI/OSC/APC sequences are skipped exactly as pi-tui skips them. A CSI
with an unknown final byte remains visible text, but truncation treats the
whole syntactic run atomically; if the cut would land inside it, the prefix
ends before ESC. The row checks `ESC[5 q` and `ESC[?25l` at every width.

```sh
cd pi
HOME=/private/tmp/amq-squad-t104-home node --test --test-reporter=spec --test-name-pattern='ESC-led CSI' test/screen.test.ts
```

Reverted expression, on the head this run was made on: the call to
`escapeRunLength` in `unitAt` was replaced by zero, restoring code-point cuts.
Round 2 replaced `unitAt` with `measureUnitAt` and `cutUnitAt`; its reverts
are under `### Measure and truncation model`. The run exited 1 first at:

```text
AssertionError: "A\u001b\u001b[0m...\u001b[0m" cut "\u001b[5 q" at 6
```

## Lint rule 2 is a source-text accident guard

Rule 2 scans Node's type-erased extension source without stripping comments.
It refuses any remaining `@earendil-works/` text and asks comments to call the
package `pi-tui`. Every Unicode-boundary `import` or `require` call head,
including whitespace or comments before `(`, must have exactly one quoted
relative `./` or `../` argument ending in `.ts`. Static relative imports still
name their `.ts` file. Type-only imports, type-position `import`, `typeof
import`, and a relative `await import("./lib/width.ts")` pass. F5, F11, and
f13 through f17 pin those decisions: a variable `require`, bare dynamic
import, built-in `require`, relative `.js` import, and non-erasable enum all
fail. The scan also reads call-like text inside strings, regular-expression
literals, template literals, comments, and object `.import` properties, and
passes it only with one quoted relative argument ending in `.ts`.

Deliberate over-refusals include a package scope in a comment and an object
method named `.import`. Named bypass limits are an escaped package specifier,
an aliased `require`, `require?.()`, `process.getBuiltinModule`, `eval`, and
`new Function`. The rule catches ordinary source-text accidents. It is not a
proof that extension code cannot arrange a runtime package load.

Command:

```sh
cd pi
HOME=/private/tmp/amq-squad-t104-home node --test --test-reporter=spec --test-name-pattern='lint permits' test/lint.test.ts
```

Reverted expression: the runtime scan read the original source instead of the
type-erased source. The run exited 1 at the type fixture's required success:

```text
AssertionError [ERR_ASSERTION]: extensions/amq-squad.ts: erased extension source contains @earendil-works/
```

The restored row and `node scripts/lint.mjs` both exited 0.

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

Pi-tui 1.3.0 defines a component as `render(width): string[]` and says each
returned line must not exceed `width` in the installed package's README.
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

First failing assertion and its reported message:

```text
AssertionError [ERR_ASSERTION]: Got unwanted exception.
Actual message: "Rendered line 5 exceeds terminal width (51 > 50)."
```

The local `acceptLikePiTui` test helper reproduced pi-tui's differential check
and threw `Rendered line 5 exceeds terminal width (51 > 50).` The fixture
constructs that task line to be exactly one column too wide. This is a local
reproduction of the check, not a direct pi-tui runtime throw. The reverted run
exited 1; the restored run exited 0.

The fixed-overflow message has its own narrow-width row. At width 12 the
message itself is longer than the pane and must pass through the same helper.
Removing the `fitWidth` call returned the full `… 35 more lines; enlarge the
pane`. The reverted command was:

```sh
cd pi
node --test --test-reporter=spec --test-name-pattern='fixed-overflow message is width-fitted' test/screen.test.ts
```

Reverted expression: `return fitWidth([` was changed to `return [`. The first
failing assertion and its reported line were:

```text
✖ the fixed-overflow message is width-fitted even when the pane is narrower than the message
AssertionError [ERR_ASSERTION]: Expected values to be strictly deep-equal:
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

## 0d9 controlled reverts

The lint mutations each used this command after changing only the named
production expression, and each exited 1:

```sh
cd pi
HOME=/tmp/amq-t149/home node --test --test-reporter=spec --test-name-pattern='lint permits' test/lint.test.ts
```

1. Replacing rule 2 with the carried regex pair first failed at
   `AssertionError [ERR_ASSERTION]: f01-side-effect.ts`, because side-effect
   imports were unnamed.
2. Disabling the scope-text check first failed at
   `AssertionError [ERR_ASSERTION]: f01-side-effect.ts`; F1, F2, F7, and F12
   were unnamed.
3. Letting the call argument start with only a quote and dot first failed at
   `AssertionError [ERR_ASSERTION]: f11-relative-concat.ts`.
4. Replacing the call-head iterator with an empty iterator first failed at
   `AssertionError [ERR_ASSERTION]: f05-variable.ts`.
5. Reading `src` instead of `runtimeSrc` in rule 2 first failed at
   `AssertionError [ERR_ASSERTION]: extensions/amq-squad.ts: erased extension source contains @earendil-works/`.

The escape-cut mutation used:

```sh
cd pi
HOME=/tmp/amq-t149/home node --test --test-reporter=spec --test-name-pattern='keeps every ESC-led run' test/screen.test.ts
```

Reverted expression: `escapeRunLength` recognized only CSI, OSC, and APC and
returned zero for generic ESC runs. It exited 1 first at:

```text
AssertionError [ERR_ASSERTION]: "\u001b\u001b[0m...\u001b[0m" cut "\u001b(B" at 5
```

The measurement-boundary mutation used:

```sh
cd pi
HOME=/tmp/amq-t149/home node --test --test-reporter=spec --test-name-pattern='local measure' test/screen.test.ts
```

Reverted expression: `ansiLength` treated DCS as a zero-width sequence. It
exited 1 first at:

```text
AssertionError [ERR_ASSERTION]: "\u001bPq#0\u001b\\tail": ours 4, pi-tui 9
```

The three carried property reverts still exited 1 on the same command. Their
complete first assertion lines were:

```text
AssertionError [ERR_ASSERTION]: "汉字かなカナ": ours 10, pi-tui 12
AssertionError [ERR_ASSERTION]: "汉字かなカナ": ours 6, pi-tui 12
AssertionError [ERR_ASSERTION]: "\u001b[?25lprivate": ours 7, pi-tui 12
```

The carried CSI cut revert used:

```sh
cd pi
HOME=/tmp/amq-t149/home node --test --test-reporter=spec --test-name-pattern='ESC-led CSI' test/screen.test.ts
```

Reverted expression, on the head this run was made on (round 2 replaced
`unitAt`): the `escapeRunLength` result in `unitAt` was replaced by zero. It
exited 1 first at:

```text
AssertionError [ERR_ASSERTION]: "A\u001b\u001b[0m...\u001b[0m" cut "\u001b[5 q" at 6
```

The carried-union mutations used these commands:

```sh
cd pi
HOME=/tmp/amq-t149/home node --test --test-reporter=spec --test-name-pattern='typed block reads' test/recovery.test.ts
HOME=/tmp/amq-t149/home node --test --test-reporter=spec --test-name-pattern='pin_unresolved uses the class-chain detail from task show' test/words.test.ts
```

Removing `ledger_tampered` from `blockNeedsRecord` exited 1 first at
`assert.ok(blockNeedsRecord("ledger_tampered"))`. Removing `pin_unresolved`
from the same expression exited 1 first at `false !== true` in
`pin_unresolved uses the class-chain detail from task show`. Every production
mutation was restored immediately after its expected failure.

## Round 2

The exact input `width-pins.json` was 7,390 bytes with sha256
`7cb9085af55a01f6fea2305bd21dd65d1d5e888ce5f79afed1d2f2acdd08e4e7`.
It was copied byte for byte to `pi/test/width-pins.json`; the final `cmp`
exited 0. The pinned row requires exactly 21 width cases and 14 truncation
cases. Every width must equal the carry and be an upper bound for pi-tui
1.3.0. Every truncation must equal the pin and remain within its requested
width under both the local measure and pi-tui. The follow-up replaced this
pins file; its size, hash and counts are under
`## Follow-up: words and clipFragment`.

### Measure and truncation model

Measurement uses the carry's narrower escape recognition. Truncation uses
extended ESC-led runs, joins an ESC chain to the run starting at its last ESC,
and extends a straddling cut unit through every measurement unit that starts
inside that run. The six screen mutations used:

```sh
cd pi
env HOME=/tmp/amq-t155/home node --test --test-reporter=spec test/screen.test.ts
```

R1a made measurement use the truncation run parser. It exited 1 first at:

```text
AssertionError [ERR_ASSERTION]: W01 double ESC then SGR: local width
```

R1a2 measured a truncation unit as plain text. It exited 1 first at:

```text
AssertionError [ERR_ASSERTION]: T02 ESC chain joins the SGR: truncation
```

R1b disabled ESC-chain joining. It exited 1 first at the same T02 assertion.
R1c disabled straddling measurement-unit extension. It exited 1 first at:

```text
AssertionError [ERR_ASSERTION]: T05 straddle W15: the unit ends after the CSI's final: truncation
```

R2 changed an incomplete generic run from the rest of the text to one byte.
It exited 1 first at:

```text
AssertionError [ERR_ASSERTION]: T09 G5: incomplete run, cut before the ESC: truncation
```

The separately required carried render mutation changed `return
fitWidth(lines)` to `return lines` and used:

```sh
cd pi
HOME=/private/tmp/amq-squad-reviewer-t100-home node --test --test-reporter=spec --test-name-pattern='render\(width\)' test/screen.test.ts
```

It exited 1 first with:

```text
AssertionError [ERR_ASSERTION]: Got unwanted exception.
Actual message: "Rendered line 5 exceeds terminal width (51 > 50)."
```

T08 is deliberately stricter than both historical implementations. Its
expected output is `"\u001b[0m...\u001b[0m"`; the carry produced
`"\u001bP\u001b[0m...\u001b[0m"`, and the head produced
`"\u001bP\u001b[31\u0007\u001b[0m...\u001b[0m"`. On this input the state
keeps less than the carry and less than the head. That is not a bound on every
input: in the lead's probe below the state never kept more visible width than
the carry, and it can keep zero-width text the carry dropped (T03 keeps the
chain's `ESC[31m`).

The lead's independent probe `probe950/probe8.ts`, the lead's run under the
gate lock from 06:19:25 to 06:19:48 IST on 2026-11-11 on round 2's `width.ts`,
drew 300,000 strings for each of the seeds 950, 955 and 9550 (236,731, 237,246
and 237,040 distinct). Every width equalled the carry's. In 2,100,000
truncation calls per seed no output differed from the lead's reference rule,
and none exceeded its width under the local measure or pi-tui. 36, 34 and 31
outputs kept more of the input than the carry did. The lead's
`probe950/probe9.ts` (06:20 IST, the same generator and seeds) classified
them: in each the extra text has width 0, and no output that ends in the
ellipsis kept more visible width than the carry's. This paragraph replaces an
earlier one that reported `probe950/probe4.ts` with 300,000 strings and 0
outputs longer than the carry: that probe's generator repeated its state, so
it drew 1,589 distinct strings, and its counts are withdrawn. The lead's
`probe950/suite-r2.sh`, run from 02:56:23 to 02:57:07, recorded `npm test`
exit 0 with 137 passed, 1 skipped, and 0 failed, and lint exit 0. These lead
probe files were evidence only and none landed.

Task and block text reaches the screen through `clean()` in
`pi/extensions/lib/words.ts`, which replaces U+0000 through U+001F and U+007F
through U+009F with a space. Other status text does not pass through it: a
warning's record, a stop decision's seat, reason and reason code, a train
alert's detail and a train refusal are shown as status wrote them, with
whitespace folded. Where that text comes from, and what the screen does with
control characters in it, is under `## Follow-up: words and clipFragment`.

### Lint rule 2 fixtures

The comment above f12 records why its inline type still becomes a run-time
side-effect import after erasure. F13 through f17 pin variable `require`, a
bare import, built-in `require`, a relative `.js` import, and TypeScript that
cannot be erased. Each lint mutation used:

```sh
cd pi
env HOME=/tmp/amq-t155/home node --test --test-reporter=spec test/lint.test.ts
```

R3a stopped recognizing `require` call heads. It exited 1 first with an
assertion for `f13-require-variable.ts`. R3b allowed any quoted import
specifier and exited 1 first with an assertion for `f14-bare-import.ts`.
R3c removed the `.ts` suffix requirement and exited 1 first with an assertion
for `f16-relative-js.ts`. R3d suppressed the erasure failure diagnostic and
exited 1 first with an assertion for `f17-not-erasable.ts`. Every mutation was
restored immediately after the expected failure.

### Status contract

`docs/status-contract.md` now matches the code: `waits_for` is empty except
for a pending `file_lock` wait, where it contains one `{task_id, path}` per
holder, sorted by `task_id`, and `path` is one declaration of the first
overlapping pair (`task.OverlapDeclaration`): the shared one when the two are
equal, else the literal one, else the pending task's.
The final paragraph is at lines 109 through 114. The measure and cut-unit
split is at `pi/extensions/lib/width.ts` lines 94 through 172, the exact pin
row at `pi/test/screen.test.ts` lines 131 through 148, and the added lint
fixtures and assertions at `pi/test/lint.test.ts` lines 44 through 71.

### Round 2 gate closeout

The first scratch-cache `npm ci` correctly stayed offline but exited 1 because
`yaml-2.9.0.tgz` was not cached there. Repeating the same offline install
against the populated local npm cache exited 0 and added 118 packages. The
final gates were:

```sh
cd pi
env HOME=<tmp>/home NPM_CONFIG_CACHE=<npm-cache> NPM_CONFIG_OFFLINE=true npm ci --ignore-scripts --offline # exit 0
env HOME=<tmp>/home TMPDIR=<native-tmp> NPM_CONFIG_CACHE=<npm-cache> NPM_CONFIG_OFFLINE=true npm test # exit 0, 138 passed, 1 skipped, 0 failed
env HOME=<tmp>/home node scripts/lint.mjs # exit 0, lint ok: 32 files, pi 1.3.0
cd ..
env HOME=<tmp>/home GOCACHE=<tmp>/gocache-build GOMODCACHE=<gomodcache> GOPROXY=off go build ./... # exit 0
env HOME=<tmp>/home GOCACHE=<tmp>/gocache-vet GOMODCACHE=<gomodcache> GOPROXY=off go vet ./... # exit 0
env HOME=<tmp>/home GOCACHE=<tmp>/gocache-task GOMODCACHE=<gomodcache> GOPROXY=off go test ./internal/task/... # exit 0
env -i PATH="$PATH" HOME=<tmp>/home TMPDIR=<native-tmp> GOCACHE=<tmp>/gocache-cli GOMODCACHE=<gomodcache> GOPROXY=off go test ./internal/cli/... # exit 1, one known PTY residual
env HOME=<tmp>/home GOCACHE=<tmp>/gocache-status GOMODCACHE=<gomodcache> GOPROXY=off go test ./internal/status/... # exit 0
env HOME=<tmp>/home GOCACHE=<tmp>/gocache-routing GOMODCACHE=<gomodcache> GOPROXY=off go test ./internal/routing/... # exit 0
env HOME=<tmp>/home GOCACHE=<tmp>/gocache-scheduler GOMODCACHE=<gomodcache> GOPROXY=off go test ./internal/scheduler/... # exit 0
env -i PATH="$PATH" HOME=<tmp>/home TMPDIR=<native-tmp> GOCACHE=<tmp>/gocache-ci GOMODCACHE=<gomodcache> GOPROXY=off NPM_CONFIG_CACHE=<npm-cache> NPM_CONFIG_OFFLINE=true NPM_FLAGS=--offline make ci # exit 2, the same one known PTY residual
HOME=<tmp>/home GOCACHE=<tmp>/gocache-redactor GOMODCACHE=<gomodcache> GOPROXY=off go run ./scripts/redact-evidence -check docs/v3-spike/320-0d-screen/ # exit 0
cmp <round-2-input>/width-pins.json pi/test/width-pins.json # exit 0
gofmt -l internal/cli/task_test.go # exit 0, no output
git diff --check # exit 0
```

The focused CLI run and repository-wide CI completed every requested package.
Their only failure was the host-specific
`TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout`. It ran in this seat's
environment while the lead's locked `go test ./...` on `fcd95493` exited 0.
Every other package and all Round 2 rows passed. The host gate script was not
run, as required by the brief.

## 0d9 gate closeout

The final gates used a scratch HOME and Go build cache, the populated local Go
module cache, `GOPROXY=off`, npm offline mode, and no inherited seat identity.
Socket integration tests used the native macOS temporary root.

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t149/home TMPDIR=<tmp> GOCACHE=/tmp/amq-t149/gocache GOMODCACHE=<gomodcache> GOPROXY=off go build ./... # exit 0
env -i PATH="$PATH" HOME=/tmp/amq-t149/home TMPDIR=<tmp> GOCACHE=/tmp/amq-t149/gocache GOMODCACHE=<gomodcache> GOPROXY=off go vet ./... # exit 0
env -i PATH="$PATH" HOME=/tmp/amq-t149/home TMPDIR=<tmp> GOCACHE=/tmp/amq-t149/gocache GOMODCACHE=<gomodcache> GOPROXY=off go test ./internal/task/... ./internal/cli/... ./internal/status/... ./internal/routing/... ./internal/scheduler/... # exit 1, one known PTY residual
env -i PATH="$PATH" HOME=/tmp/amq-t149/home TMPDIR=<tmp> GOCACHE=/tmp/amq-t149/gocache GOMODCACHE=<gomodcache> GOPROXY=off go test ./... # exit 1, the same one known PTY residual
gofmt -l internal/cli/task_test.go # exit 0, no output
cd pi
env -i PATH="$PATH" HOME=/tmp/amq-t149/home TMPDIR=<tmp> NPM_CONFIG_OFFLINE=true npm test # exit 0, 137 passed, 1 skipped
env -i PATH="$PATH" HOME=/tmp/amq-t149/home node scripts/lint.mjs # exit 0, lint ok: 32 files, pi 1.3.0
cd ..
HOME=/tmp/amq-t149/home GOCACHE=/tmp/amq-t149/gocache GOMODCACHE=<gomodcache> GOPROXY=off go run ./scripts/redact-evidence -check docs/v3-spike/320-0d-screen/ # exit 0
git diff --check # exit 0
```

Both Go test commands completed every requested package. Their only failure
was the host-specific `TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout`.
It ran in this seat's environment while the lead's locked `go test ./...` on
`fcd95493` exited 0. This is the same unrelated residual recorded by the
carried 0d7 evidence. All other packages and all task-specific rows passed.

The repository-wide offline CI command was also run with an empty environment,
the original macOS per-user TMPDIR, and no AMQ pin variables:

```sh
env -i PATH="$PATH" HOME=<tmp>/home TMPDIR=<tmp> GOCACHE=<tmp>/gocache GOMODCACHE=<gomodcache> GOPROXY=off NPM_FLAGS=--offline make ci # exit 2
```

Every package except `internal/cli` passed. Its only failure was the unrelated,
host-specific `TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout`; this is
the same environment-specific residual already recorded in
`docs/v3-spike/5-302-evidence/t27-typed-widen.md`.
The task-specific CLI row above passed, and the full pi suite, including all
new width, render-count, overflow, and lint rows, passed.

## 0d8 gate closeout

The 0d8 property, ESC-run, and lint rows passed together against the installed
pi-tui and the copied lint fixture:

```sh
cd pi
HOME=/private/tmp/amq-squad-t104-home node --test --test-reporter=spec --test-name-pattern='local measure|ESC-led CSI|lint permits' test/screen.test.ts test/lint.test.ts # exit 0, 3 passed
HOME=/private/tmp/amq-squad-t104-home node scripts/lint.mjs # exit 0, lint ok: 31 files, pi 1.3.0
HOME=/private/tmp/amq-squad-t104-home TMPDIR=/private/tmp npm test # exit 0, 131 passed, 1 skipped
```

All five one-expression reverts exited 1 on their named row; their first
failures are recorded above. After restoration, the carried Go gates used a
temporary HOME, build cache, and module cache seeded from the host cache with
`GOPROXY=off`: `go build ./...`, `go vet ./...`, and the focused `internal/task`,
`internal/cli`, `internal/status`, `internal/routing`, and `internal/scheduler`
commands from the 0d7 gate block all exited 0.

```sh
HOME=/private/tmp/amq-squad-t104-go/home GOCACHE=/private/tmp/amq-squad-t104-go/gocache GOMODCACHE=/private/tmp/amq-squad-t104-go/gomodcache GOPROXY=off go run ./scripts/redact-evidence -check docs/v3-spike/320-0d-screen/ # exit 0
git diff --check # exit 0
```

## Follow-up: words and clipFragment

Line numbers under `pi/` in this section are measured on this change's tree.
Go line numbers are as of `a855037d`. This section replaces the first
attempt's section of the same name (task t156, stopped at a gate): its words
were corrected by the lead and landed here as a lead input, and its code is
the first attempt's, unchanged.

The copied pin artifact is 9,348 bytes with SHA-256
`b8434636f1891893e777954903da6238f564b830e74d1d679611572a8a064e1d`.
It contains 23 width pins and 18 truncation pins. `cmp` against the supplied
artifact was silent. The appended clip row and control row are byte-for-byte
copies of their supplied fragments.

### The reverts

Each revert changed one expression, ran its command from `pi`, and was
restored. Each entry gives the file and line, the line's code before and
after, the first failing test line (its duration left out) with the test's
line, and the first assertion line, copied. The thirteen entries K1 through
R3d were run again by the seat on this tree for this section, with the
expressions as printed. The clipFragment, g1 and g2 entries, and 6b's record
in place under ``## `render(width)` keeps every physical line inside pi-tui's
contract``, stand on the first attempt's runs and on the lead's runs of 2026-11-11 on the same
tree; they were not run again by the seat.

The commands:

```sh
cd pi
node --test --test-reporter=spec test/screen.test.ts   # "screen"
node --test --test-reporter=spec test/lint.test.ts     # "lint"
node --test --test-reporter=spec --test-name-pattern='clipped custom ellipsis' test/screen.test.ts   # "clip"
node --test --test-reporter=spec --test-name-pattern='control characters in status text' test/screen.test.ts   # "control"
```

clipFragment, `pi/extensions/lib/width.ts:228`, the clip command:

```text
before: const unit = measureUnitAt(text, i);
after:  const unit = cutUnitAt(text, i);
✖ a clipped custom ellipsis is cut on measure units
test at test/screen.test.ts:511:1
AssertionError [ERR_ASSERTION]: the OSC 8 open is dropped
```

K1, `pi/extensions/lib/width.ts:149`, the screen command:

```text
before: const suffix = ansiLength(text, last) || escapeRunLength(text, last);
after:  const suffix = escapeRunLength(text, last);
✖ the pinned carry measure and round 2 truncations stay exact
test at test/screen.test.ts:131:1
AssertionError [ERR_ASSERTION]: T15 ESC chain then a CSI pi-tui skips and the cut parser calls incomplete: truncation
```

K3, `pi/extensions/lib/width.ts:157`, the screen command:

```text
before: if (measured.ansi || text.charCodeAt(pos) !== 0x1b) return measured;
after:  if (text.charCodeAt(pos) !== 0x1b) return measured;
✖ the pinned carry measure and round 2 truncations stay exact
test at test/screen.test.ts:131:1
AssertionError [ERR_ASSERTION]: T16 an SGR right before the cut is dropped: truncation
```

MR1, `pi/extensions/lib/width.ts:117` (the second of the file's two
occurrences, the one in `measureRunLength`), the screen command:

```text
before: if (next === "]" || next === "_") {
after:  if (false) {
✖ the pinned carry measure and round 2 truncations stay exact
test at test/screen.test.ts:131:1
AssertionError [ERR_ASSERTION]: W22 unterminated OSC then SGR: the SGR is inside the run: local width
```

PM1, `pi/extensions/lib/width.ts:72`, the screen command. The pinned test
fails too; the run row is the first:

```text
before: if (next === "]" || next === "_" || next === "P" || next === "X" || next === "^") {
after:  if (next === "]" || next === "_" || next === "P" || next === "X") {
✖ truncation keeps every ESC-led run whole or moves the cut before it
test at test/screen.test.ts:110:1
AssertionError [ERR_ASSERTION]: "\u001b^\u001b[0m...\u001b[0m" cut "\u001b^pm\u001b\\" at 6
```

R1a, `pi/extensions/lib/width.ts:130`, the screen command. The clip row
fails too; the pinned test is the first:

```text
before: const run = measureRunLength(text, pos);
after:  const run = escapeRunLength(text, pos);
✖ the pinned carry measure and round 2 truncations stay exact
test at test/screen.test.ts:131:1
AssertionError [ERR_ASSERTION]: W01 double ESC then SGR: local width
```

R1a2, `pi/extensions/lib/width.ts:171`, the screen command:

```text
before: return { text: text.slice(pos, end), width, ansi: false };
after:  return { text: text.slice(pos, end), width: plainWidth(text.slice(pos, end)), ansi: false };
✖ the pinned carry measure and round 2 truncations stay exact
test at test/screen.test.ts:131:1
AssertionError [ERR_ASSERTION]: T02 ESC chain joins the SGR: truncation
```

R1b, `pi/extensions/lib/width.ts:147`, the screen command:

```text
before: while (text.charCodeAt(last + 1) === 0x1b) last++;
after:  (the line removed)
✖ the pinned carry measure and round 2 truncations stay exact
test at test/screen.test.ts:131:1
AssertionError [ERR_ASSERTION]: T02 ESC chain joins the SGR: truncation
```

R1c, `pi/extensions/lib/width.ts:169`, the screen command:

```text
before: if (i > end) end = i;
after:  (the line removed)
✖ the pinned carry measure and round 2 truncations stay exact
test at test/screen.test.ts:131:1
AssertionError [ERR_ASSERTION]: T05 straddle W15: the unit ends after the CSI's final: truncation
```

R2, `pi/extensions/lib/width.ts:89` (`escapeRunLength`'s last return; the
same text appears earlier in the file, and those lines stay), the screen
command:

```text
before: return text.length - pos;
after:  return 1;
✖ the pinned carry measure and round 2 truncations stay exact
test at test/screen.test.ts:131:1
AssertionError [ERR_ASSERTION]: T09 G5: incomplete run, cut before the ESC: truncation
```

The four lint reverts fail the same test, each first on its own fixture. The
assertion's message goes on for one line per diagnostic; its first line is
copied.

R3a, `pi/scripts/lint.mjs:52`, the lint command:

```text
before: const callHead = new RegExp(String.raw`(?<![\p{ID_Continue}$])(?:import|require)${trivia}\(`, "gu");
after:  const callHead = new RegExp(String.raw`(?<![\p{ID_Continue}$])(?:import)${trivia}\(`, "gu");
✖ lint permits erased types and relative .ts calls but gives each refused extension one diagnostic
test at test/lint.test.ts:11:1
AssertionError [ERR_ASSERTION]: f13-require-variable.ts: extensions/f01-side-effect.ts: erased extension source contains @earendil-works/ (name the package as pi-tui in comments; pi supplies it at run time)
```

R3b, `pi/scripts/lint.mjs:53`, the lint command:

```text
before: const relativeOnly = new RegExp(String.raw`^${trivia}(?:"(?:\.\/|\.\.\/)(?:[^"\\\r\n]|\\.)*\.ts"|'(?:\.\/|\.\.\/)(?:[^'\\\r\n]|\\.)*\.ts')${trivia}\)`);
after:  const relativeOnly = new RegExp(String.raw`^${trivia}(?:"[^"]*"|'[^']*')${trivia}\)`);
✖ lint permits erased types and relative .ts calls but gives each refused extension one diagnostic
test at test/lint.test.ts:11:1
AssertionError [ERR_ASSERTION]: f14-bare-import.ts: extensions/f01-side-effect.ts: erased extension source contains @earendil-works/ (name the package as pi-tui in comments; pi supplies it at run time)
```

R3c, `pi/scripts/lint.mjs:53`, the lint command:

```text
before: const relativeOnly = new RegExp(String.raw`^${trivia}(?:"(?:\.\/|\.\.\/)(?:[^"\\\r\n]|\\.)*\.ts"|'(?:\.\/|\.\.\/)(?:[^'\\\r\n]|\\.)*\.ts')${trivia}\)`);
after:  const relativeOnly = new RegExp(String.raw`^${trivia}(?:"(?:\.\/|\.\.\/)(?:[^"\\\r\n]|\\.)*"|'(?:\.\/|\.\.\/)(?:[^'\\\r\n]|\\.)*')${trivia}\)`);
✖ lint permits erased types and relative .ts calls but gives each refused extension one diagnostic
test at test/lint.test.ts:11:1
AssertionError [ERR_ASSERTION]: f16-relative-js.ts: extensions/f01-side-effect.ts: erased extension source contains @earendil-works/ (name the package as pi-tui in comments; pi supplies it at run time)
```

R3d, `pi/scripts/lint.mjs:37`, the lint command:

```text
before: problems.push(`${rel}: not erasable TypeScript: ${err.message.split("\n")[0]}`);
after:  (the statement removed)
✖ lint permits erased types and relative .ts calls but gives each refused extension one diagnostic
test at test/lint.test.ts:11:1
AssertionError [ERR_ASSERTION]: f17-not-erasable.ts: extensions/f01-side-effect.ts: erased extension source contains @earendil-works/ (name the package as pi-tui in comments; pi supplies it at run time)
```

g1, `pi/extensions/lib/screen.ts:509`, the control command:

```text
before: const fitWidth = (lines: string[]): string[] => lines.map((line) => truncateToWidth(line.replace(controlChars, " "), width));
after:  const fitWidth = (lines: string[]): string[] => lines.map((line) => truncateToWidth(line, width));
✖ control characters in status text never reach a drawn line
test at test/screen.test.ts:522:1
AssertionError [ERR_ASSERTION]: "  seat dev\u001b]0;s\u0007's launch failed; ask the lead to relaunch the roster"
```

g2, `pi/extensions/lib/screen.ts:500`, the control command:

```text
before: ui.setWidget(SCREEN_KEY, this.expanded ? [`amq-squad: ${this.error.replace(controlChars, " ") || "reading the team's status"}`, commandsLine("team")] : undefined, { placement: "belowEditor" });
after:  ui.setWidget(SCREEN_KEY, this.expanded ? [`amq-squad: ${this.error || "reading the team's status"}`, commandsLine("team")] : undefined, { placement: "belowEditor" });
✖ control characters in status text never reach a drawn line
test at test/screen.test.ts:522:1
AssertionError [ERR_ASSERTION]: "amq-squad: status --json failed: exit 1: open /p/\u001b]0;n\u0007: no such file"
```

### T18

T18 is `"\u001b^;]m\u001b]\u0007\t汉q8"` cut at 9. The ellipsis takes 3, so
the budget is 6 (`pi/extensions/lib/width.ts:259`). At the first ESC
`cutUnitAt` (:155) asks `cutRunLength` (:140) for the cut run. No second ESC
follows, so it returns `escapeRunLength`'s answer (:152): the PM branch
(:72-74) scans to the first BEL and returns a run of 8 code units, the ESC
through the BEL. That function measures no width. `cutUnitAt`'s loop
(:165-170) then adds up the measure units inside the run: the ESC is 2, `^`,
`;`, `]` and `m` are 1 each, and the nested `ESC ] BEL` is a complete OSC, a
zero-width unit (`measureUnitAt`, :127-138). The cut unit is 6 wide and fits
the budget whole, with the nested OSC inside it. The tab is 3 (:9) and does
not fit, so the cut is there. The carry walked measure units one at a time:
it kept the same six columns, held the zero-width OSC pending, and dropped it
when the tab did not fit. So the pin keeps `ESC ^ ; ] m ESC ] BEL` where the
carry kept `ESC ^ ; ] m`: the same visible width and one zero-width sequence
more. The other cases the lead's probe classified were not traced here.

### Where status text comes from

Read on `a855037d`; nothing in this subsection was run on a live team.

A git dir's path in a decision's reason. A worktree's `.git` file names its
git dir on its `gitdir: ` line (`internal/seatcommit/git.go:176-183`). When
that dir's own `gitdir` file cannot be read or does not point back,
`worktreePaths` returns `the git dir %s does not belong to the worktree %s`
with the line's text in it (:186). `ArchiveAttemptReason` returns that error
(`internal/seatcommit/commit.go:590-593`; `ArchiveAttempt`, :557, calls it).
Two paths carry the same text into a decision:

- The attempts-log scan. For an attempt blocked with `widen_requested` or the
  growth gate, `scanAttemptsLog` calls the archiver and returns its error
  (`internal/scheduler/exits.go:442-448`; the archiver is
  `CleanSeats.ArchiveAttempt`, `internal/schedproc/prepare.go:194-201`). The
  tick returns it (`internal/scheduler/scheduler.go:461-462`) and the process
  logs a `tick_error` decision whose reason is the error's text
  (`internal/schedproc/process.go:254`).
- The clean after a seat's confirmed close. `CleanAttempt` returns the error
  (`internal/schedproc/prepare.go:123-130`) and its caller logs a
  `seat_clean_deferred` decision whose reason is the error's text
  (`internal/scheduler/exits.go:821-822`; `logCleanDeferred`, :671, :680).

Not settled: whether the Codex sandbox or the hook guard stops a seat writing
its own worktree's `.git` file. Neither path was run.

A lease holder's handle. `task claim --me H` takes the handle from `actor()`
(`internal/cli/task.go:196-211`), which trims it, falls back to `AM_ME`,
refuses an empty one, and checks that it is not the operator (:206); it
checks nothing about the handle's characters. With a live scheduler the verb
calls `ClaimOffered`
(:570, :582), which grants nothing: it accepts only a task already leased to
that holder and not yet claimed (`internal/task/lease.go:439-462`, the test at
:447). With no live scheduler it calls `ClaimServing` (:598), which reaches
`grant` (`internal/task/lease.go:389`, :424; `grant` is at :64) and there
`SeatHandle` (:65). `SeatHandle` (`internal/task/unclean.go:87-93`) refuses
the empty handle, `.`, `..` and any handle with `/`, `\` or NUL (:89),
after trimming white space from its ends (:88); an ESC, a BEL or any other
control character inside the handle is accepted. The exported `Store.Grant`
(`internal/task/lease.go:236-244`) is a separate entry to the same `grant`
(:242); `task claim` does not call it. A lease holder's handle is logged as
the seat of `task_budget_spent` (`internal/scheduler/scheduler.go:429`) and
`task_budget_warning` (:454). So a handle with a control character can become
a lease holder through `task claim` only when no scheduler is live. Not run.

The warnings. Status's warnings come from these producers:

| Line | The warning | Its detail |
|---|---|---|
| `internal/cli/status.go:93` | `gate store: <error>` | the error of `readHookGates` (:90) |
| `internal/cli/status.go:98` | `gate store: gate <id> is a legacy <kind> gate ...` | a stored gate's id and kind (`internal/gatestore/legacy.go:40-42`) |
| `internal/status/collect.go:70` | `task store: <error>` | the error of `task.Snapshot` |
| `internal/status/collect.go:73` | `scheduler state: <error>` | the error of `scheduler.ReadState` |
| `internal/status/collect.go:76` | `launch alarms: <error>` | the error of `launch.ReadAlarms` |
| `internal/status/collect.go:83` | `mailbox scan: <n> unreadable messages skipped` | a count only |
| `internal/status/collect.go:116` | `agents: <error>` | the error of reading the root's `agents` directory |
| `internal/status/collect.go:154` | `clears: <error>` | the error of `schedproc.ReadClears` |
| `internal/status/collect.go:167` | `seat <handle> launch record: <error>` | the handle and the error of `launch.Read` |
| `internal/status/collect.go:183` | `seat <handle> ledger: <error>` | the handle and the error of reading the seat's ledger file |
| `internal/status/collect.go:477` | `alert <name> unreadable` | the alert file's base name |
| `internal/status/screen.go:44` | `lead record: <error>` | the error of reading the lead's record |
| `internal/status/screen.go:53` | `scheduler launch record: <error>` | the error of reading the scheduler's launch record |
| `internal/status/screen.go:61` | `decisions log: <error>` | the error of `schedproc.ReadDecisionsTail` |
| `internal/status/screen.go:73` | `merge train audit log: <error>` | the error of the train's audit read |
| `internal/status/relay.go:255` | `relay record <name> unreadable (<code>)` | the record file's base name and a fixed code |
| `internal/status/collect.go:228` | a staged Claude seat's hooks are silent | fixed words and the proof's time |
| `internal/status/collect.go:236` | a staged Claude seat's worktree has allow rules, or a settings file that cannot be read | every allow string of the worktree's `.claude/settings.json` and `.claude/settings.local.json`, or `<file>: unreadable` for one that cannot be read or parsed (`internal/claudeseat/claudeseat.go:287-309`) |
| `internal/status/collect.go:239` | a staged Claude seat has external includes pending | the paths of imported or linked memory files outside the worktree (`internal/claudeseat/externalincludes.go:143`) |
| `internal/status/screen.go:228` | a seat's launch went over its ceiling | numbers and the slowest segment's name from the seat's launch timing |
| `internal/status/screen.go:232` | a seat's wake is off | the wake audit's reason, or fixed words |
| `internal/status/screen.go:237` | a pi seat's wake | fixed words |
| `internal/status/screen.go:240` | a seat waits at a harness dialog | the marker file's time and its age |
| `internal/status/screen.go:243` | a pi seat's provider has no key | the reason from `piKeyMissing` |

The seat rows are `seatWarnings` (`internal/status/collect.go:216-241`) and
`launchWarnings` (`internal/status/screen.go:219-245`), joined at
`internal/status/collect.go:536`. `buildStatusContract` puts the gate store's
two in front of the rest (`internal/cli/status.go:114`).

What a seat can write. Two rows carry text read from the seat's own worktree:
`internal/status/collect.go:236` and :239. In the `<error>` rows the detail is
an error's text from a read or a decode; whether a seat can put text into any
of them was not settled, row by row. The wake audit's reason
(`internal/status/screen.go:232`) is copied at the seat's launch from AMQ's
prepare preview (`internal/seatlaunch/compile.go:343`) into the seat's staged
wake record (:214), which status reads (`internal/status/screen.go:142-143`);
whether a seat can rewrite that record was not settled.

What status does to the text. `status.Build` copies the warnings
(`internal/status/classify.go:126`) and passes them and each seat's through
`redactAll` (:351, :378, :455). That redacts credentials: the span each
redaction rule matches is replaced with a placeholder, and the text outside it
is kept, control characters included (`internal/redact/redact.go:484-505`).
It does no other replacement of control characters.

### What the two replacements cover

Covered: the line shown before any status is read
(`pi/extensions/lib/screen.ts:500`; the array's second element is
`commandsLine("team")`, fixed words) and every line of the expanded screen,
which leaves through `fitWidth` (:509, called at :513 and :518), the stale
line (:512) among them.

Not covered, each a place where status text or a verb's reply reaches the
lead's terminal without the replacement:

- the collapsed screen's key outcome notice
  (`pi/extensions/lib/screen.ts:446`);
- the counts line (`pi/extensions/lib/screen.ts:532`; `countsLine`, :367-371):
  the session's name and counts;
- the dialogs in `pi/extensions/lib/keys.ts`: reset's two selects (:233, :237)
  and its input (:243), dismiss's two selects (:271, :279), which show seat
  handles and a warning's words, answer's select (:300), which shows each
  gate's first question line, widen's select (:344) and widen's confirm
  (:368), which shows the refused paths as recorded. The title at :343 and
  :362 and the seat's reason at :360 pass through `clean()` first;
- the gate-question notice (`pi/extensions/lib/keys.ts:323`): the gate's plain
  question and its identifiers from `gate status --json`, shown whether or not
  the screen is expanded;
- plain `amq-squad status`: `writeStatusContractTable`
  (`internal/cli/status.go:118-147`) prints the table and the warnings
  (:146-147) as they are.

The other `ui.notify` calls (`pi/extensions/amq-squad.ts:297`,
`pi/extensions/lib/commands.ts:55`, `pi/extensions/lib/team.ts:464`,
`pi/extensions/lib/amq-bridge.ts:688`) are not handed the status document.
Two of them carry a key outcome's line (`commands.ts:78`, `team.ts:464`), as
`screen.ts:446` does; a key outcome's line can carry a verb's refusal.
Whether text a seat wrote reaches a refusal was not settled.

### What ran

The seat applied the lead's two patches with plain `git apply` (exit 0 and 0).
After each, `git status --porcelain` listed the six declared files and `git
hash-object` equalled the lead's list for all six. The thirteen reverts K1
through R3d were run with the commands above: each exited 1 with the test line
and the assertion line printed in its entry, and each file was restored to its
listed blob before the next. From `pi`: `npm ci --ignore-scripts --offline`
exited 0; `npm test` exited 0 with 140 passing, 1 skipped and 0 failing; `node
scripts/lint.mjs` exited 0. From the root: `go build ./...` exited 0; `go run
./scripts/redact-evidence -check docs/v3-spike/320-0d-screen/` exited 0; `cmp`
of `pi/test/width-pins.json` against the supplied artifact exited 0; `git diff
--check` exited 0. The whole-repository runs (`make ci`, `go test ./...`) were
not run in the seat's sandbox; they are the lead's, on the PR head.
