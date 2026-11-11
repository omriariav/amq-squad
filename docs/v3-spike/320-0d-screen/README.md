# 3.2.0-0d screen and store evidence

Recorded on 2026-11-05. The commands below use temporary one-line production
reverts in the seat worktree. Each production line was restored immediately
after its expected failing run. No test used a real AMQ session root.

The 0d9 follow-up carries `fe6fdc2cd39fbb7b9eb40f620788f24630307be7`
onto `c65a709c690dd0ed5877076cfeebf470da339262`. Cherry-pick produced only the
five declared additive conflict hunks: the v3 claim/pull and carried task-add
tests in `internal/cli/task_test.go`; the v3 scheduler state maps and carried
`tickLog` in `internal/scheduler/scheduler.go`; and the writer/pin helpers,
`blockNeedsRecord` reasons, and routine decision kinds in
`pi/extensions/lib/words.ts`.

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
for both measures.

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

Reverted expression: the call to `escapeRunLength` in `unitAt` was replaced by
zero, restoring code-point cuts. The run exited 1 first at:

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
import`, and a relative `await import("./lib/width.ts")` pass.

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

The three carried property reverts still exited 1 on the same command. Adding
CJK to the width-1 allowlist first reported `"汉字かなカナ": ours 10,
pi-tui 12`; changing the default width from 2 to 1 first reported the same
input at `ours 6, pi-tui 12`; and accepting every CSI final byte first reported
the DCS input above. The carried CSI cut revert used:

```sh
cd pi
HOME=/tmp/amq-t149/home node --test --test-reporter=spec --test-name-pattern='ESC-led CSI' test/screen.test.ts
```

Reverted expression: the `escapeRunLength` result in `unitAt` was replaced by
zero. It exited 1 first at:

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

## 0d9 gate closeout

The final gates used a scratch HOME and Go build cache, the populated local Go
module cache, `GOPROXY=off`, npm offline mode, and no inherited seat identity.
Socket integration tests used the native macOS temporary root.

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t149/home TMPDIR=/private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/ GOCACHE=/tmp/amq-t149/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go build ./... # exit 0
env -i PATH="$PATH" HOME=/tmp/amq-t149/home TMPDIR=/private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/ GOCACHE=/tmp/amq-t149/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go vet ./... # exit 0
env -i PATH="$PATH" HOME=/tmp/amq-t149/home TMPDIR=/private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/ GOCACHE=/tmp/amq-t149/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go test ./internal/task/... ./internal/cli/... ./internal/status/... ./internal/routing/... ./internal/scheduler/... # exit 1, one known PTY residual
env -i PATH="$PATH" HOME=/tmp/amq-t149/home TMPDIR=/private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/ GOCACHE=/tmp/amq-t149/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go test ./... # exit 1, the same one known PTY residual
gofmt -l internal/cli/task_test.go # exit 0, no output
cd pi
env -i PATH="$PATH" HOME=/tmp/amq-t149/home TMPDIR=/private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/ NPM_CONFIG_OFFLINE=true npm test # exit 0, 137 passed, 1 skipped
env -i PATH="$PATH" HOME=/tmp/amq-t149/home node scripts/lint.mjs # exit 0, lint ok: 32 files, pi 1.3.0
cd ..
HOME=/tmp/amq-t149/home GOCACHE=/tmp/amq-t149/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go run ./scripts/redact-evidence -check docs/v3-spike/320-0d-screen/ # exit 0
git diff --check # exit 0
```

Both Go test commands completed every requested package. Their only failure
was the host-specific `TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout`,
which received tmux pane identity `%0_77304` in the focused run and `%0_99475`
in the repository-wide run. This is the same unrelated residual recorded by
the carried 0d7 evidence. All other packages and all task-specific rows passed.

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
