# S1-2b: declared block and wait reasons

Base: `91081d70ea66e5c6c0f7747935328fc1467909a9`.

This PR is round 2 of S1-2b. It carries #981's head `b1910543f07a8703f29615123b670bbbdce464bb` and replaces #981.

#988 landed it as `65bb47442b37a744058f3095fb336646f4a8ddcc`. Its one follow-up, the section "The follow-up" below, was first #991, on `3bf07c72bccc352fd60a44d295d5c43306c18d56`, which did not merge; this PR carries #991's head `45428d7019a091e717c4cf59bf5ae38b7b5a9eda` on `f207c7d57189c97f254b16aeb69c0e1f95b080fa` and replaces it.

It adds five files:

- `internal/statecontract/reasons.go`
- `internal/statecontract/reasons_test.go`
- `docs/v3-spike/330-s1-2b-reasons/README.md`
- `docs/v3-spike/330-s1-2b-reasons/sites.md`
- `docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md`

It changes two files:

- `internal/statecontract/contract.go`
- `pi/test/state-contract.json`

## Inputs

```console
$ (cd $IN && shasum -a 256 MANIFEST.sha256)
88c017a5dc21af885e29e7bdf4685f1e9e76ba4d781d0120b30ef25e77a11d32  MANIFEST.sha256
```

```console
$ (cd $IN && shasum -a 256 -c MANIFEST.sha256)
carry/blobs-after.txt: OK
carry/t175.patch: OK
cites.py: OK
data/D1.go: OK
data/D10.go: OK
data/D12.go: OK
data/D13.go: OK
data/D14.go: OK
data/D15.go: OK
data/D16.go: OK
data/D17.go: OK
data/D18.go: OK
data/D19.go: OK
data/D2.go: OK
data/D20.go: OK
data/D21.go: OK
data/D22.go: OK
data/D23.go: OK
data/D24.go: OK
data/D25.go: OK
data/D26.go: OK
data/D27.go: OK
data/D28.go: OK
data/D29.go: OK
data/D3.go: OK
data/D30.go: OK
data/D31.go: OK
data/D32.go: OK
data/D33.go: OK
data/D34.go: OK
data/D4.go: OK
data/D5.go: OK
data/D6.go: OK
data/D7.go: OK
data/D8.go: OK
data/D9.go: OK
probes/C1.go: OK
probes/C2.go: OK
probes/C3.go: OK
probes/C4.go: OK
probes/C5.go: OK
probes/C6.go: OK
probes/C7.go: OK
probes/F1.go: OK
probes/F10.go: OK
probes/F11.go: OK
probes/F12.go: OK
probes/F13.go: OK
probes/F14.go: OK
probes/F15.go: OK
probes/F16.go: OK
probes/F17.go: OK
probes/F18.go: OK
probes/F19.go: OK
probes/F2.go: OK
probes/F20.go: OK
probes/F21.go: OK
probes/F22.go: OK
probes/F23.go: OK
probes/F24.go: OK
probes/F25.go: OK
probes/F26.go: OK
probes/F27.go: OK
probes/F3.go: OK
probes/F4.go: OK
probes/F5.go: OK
probes/F6.go: OK
probes/F7.go: OK
probes/F8.go: OK
probes/F9.go: OK
probes/L1.go: OK
probes/L2.go: OK
probes/L3.go: OK
probes/L4.go: OK
probes/L5.go: OK
probes/L6.go: OK
probes/L7.go: OK
probes/L9.go: OK
probes/T1.go: OK
probes/TARGETS.tsv: OK
product/E1.pins.go: OK
product/E2.policy.go: OK
product/E3.scheduler.go: OK
product/E4.scheduler.go: OK
product/E5.scheduler.go: OK
product/E6.router.go: OK
product/E7.router.go: OK
product/E8.router.go: OK
product/TARGETS.tsv: OK
rows.sh: OK
sentence-rows.md: OK
tree/docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md: OK
tree/docs/v3-spike/330-s1-2b-reasons/sites.md: OK
tree/internal/statecontract/reasons.go: OK
```

`$IN` is the brief's input directory, and its value is not printed because this note holds no absolute user path.

## What is declared

`reasons.go` declares `ReasonWriter` (`reasons.go:20-24`), `ReasonEntry` (`reasons.go:26-32`), `OpenDoor` (`reasons.go:34-38`), and `ReasonList` (`reasons.go:40-46`). It exposes `BlockReasons` (`reasons.go:48-143`), `WaitReasons` (`reasons.go:145-199`), and `NotReasons` (`reasons.go:201-212`).

The block vocabulary has 59 entries, 58 distinct writer rows, 14 doors, 11 open doors, 83 uses by name at declared doors, and 56 literal sites at declared doors. The wait vocabulary has 24 entries, 27 distinct writer rows, 9 doors, 10 open doors, 36 uses by name at declared doors, and 57 literal sites at declared doors. The not-a-reason list has six entries.

`Contract` gains `Reasons` at `contract.go:11-16`; `ReasonsContract` declares `block`, `wait`, and `not_reasons` at `contract.go:28-33`; and `Build` fills it at `contract.go:35-45`. The generated JSON has `reasons` at `pi/test/state-contract.json:405`, `block` at `:406`, `wait` at `:1072`, and `not_reasons` at `:1401`. The schema remains `amq-squad-state-contract/1` (`contract.go:38`).

Both vocabularies are open, and the data names each open door and its functions.

`sites.md` section 3a records every non-test Go line that named each at `1a3db2f13466502a8de7948fc7e1fa07574843d9`, the commit its lines are of; `reasons.go`, which #988 adds, names each as well.

`sites.md` sections 1 and 2 list the block and wait rows and sites; section 3 lists marks; section 4 lists refusal codes; section 5 lists literal-only values; section 6 lists open doors; section 7 lists the door sets; section 8 lists equal strings with distinct constants; and section 9 lists empty-string sites.

## What the test checks

`TestReasonsAreTiedToTheirWriters` is at `reasons_test.go:47-59`.

The survey (`reasons_test.go:61-146`) reuses the module walk and parser from `statewrite_test.go`, applies this host's build constraints, reads no test file, and type-checks each resulting package with one source importer. It reports a type error and keeps surveying. Missing type, object, or selection records produce no door form instead of a panic. It records every declared function and method with its signature, then collects module string constants and string literals.

Names come from `go/types`. A function is `<package>.<Name>`; methods retain value or pointer receiver spelling (`reasons_test.go:209-233`). A call of an interface's method keeps the interface in its name whatever receiver the selection names: the method's own receiver decides. A named interface method is `<package>.(<Interface>).<Name>`, while an anonymous interface method is `<package>.(interface).<Name>`; neither is a concrete function's name (`reasons_test.go:309-330`). Struct aliases resolve, one pointer is removed, and constants use their declaring package's name.

A use by name is an identifier resolved to a module string constant, whether package-level or local, typed or untyped. It keeps the declaring package name and constant value. String literals are counted separately. The expression widens over selectors, parentheses, and one-argument conversions (`reasons_test.go:175-196`); its function is the containing declaration, including a use inside a function literal.

The five door forms are:

1. `param <function>#<i>`: an argument to a declared function or method, with the method-expression receiver removed. The argument index stays as written. Calls through variables, builtins, function literals, and generic instantiations have no door. Interface calls use the interface method's own name (`reasons_test.go:251-330`).
2. `field <struct>.<Field>`: a keyed or positional struct literal field, or the matching selected field of an equal-width assignment. The declaring struct is recovered through named or anonymous embedding and through a defined pointer type (`reasons_test.go:332-434`).
3. `local <name> in <function>`: an equal-width assignment or local `var` declaration. Package variables and every `const` declaration are excluded (`reasons_test.go:436-473`).
4. `result <function>#<i>`: a return from the function declaration itself, excluding returns inside function literals (`reasons_test.go:475-501`).
5. `element in <function>`: an array or slice element, keyed or unkeyed. Struct and map values are excluded, and a missing checker type produces no door (`reasons_test.go:503-524`).

The diagnostics are character-for-character contracts:

| Check | Message | Match | Code |
|---|---|---|---|
| M1 | `not-a-reason list: <constant> is declared twice` | A repeated not-a-reason constant. | `reasons_test.go:530` |
| M2 | `not-a-reason list: <constant> carries a mark or a writer` | A not-a-reason entry with either. | `reasons_test.go:534` |
| M3 | `<list> list: door <door> is declared twice` | A repeated door. | `reasons_test.go:546` |
| M4 | `<list> list: the list is not declared open with its open doors` | `Open` is false or `OpenDoors` is empty. | `reasons_test.go:554` |
| M5 | `<list> list: the open door "<name>" is declared twice` | A repeated open-door name. | `reasons_test.go:559` |
| M6 | `<list> list: the open door "<name>" names no function` | An open door has no functions. | `reasons_test.go:563` |
| M7 | `<list> list: the open door "<name>" names <function>, and no such function is declared` | A named function is absent. | `reasons_test.go:567` |
| M24 | `<list> list: the door <door> is in a generic function, which the test does not walk` | A param, result, local, or element door names a function with type parameters. | `reasons_test.go:576` |
| M25 | `<list> list: the door <door> is a variadic function's last parameter, which the test does not fold` | A param door names the variadic function's final parameter. | `reasons_test.go:585` |
| M8 | `<list> list: <constant> is declared twice` | A repeated list constant. | `reasons_test.go:595` |
| M9 | `<list> list: <constant> is also on the not-a-reason list` | A vocabulary overlaps the refusal list. | `reasons_test.go:600` |
| M10 | `<list> list: <constant> carries the mark "<mark>", which is not a declared mark` | A mark is neither empty, `no writer`, nor a guarded mark. | `reasons_test.go:604` |
| M11 | `<list> list: <constant> is marked guarded at <function>, and no such function is declared` | A guarded mark names no declaration. | `reasons_test.go:609` |
| M12 | `<list> list: <constant> declares no writer and does not carry the no writer mark` | An entry has no writer and its mark is not `no writer`. | `reasons_test.go:613` |
| M13 | `<list> list: <constant> carries the no writer mark and declares a writer` | A `no writer` entry has a writer. | `reasons_test.go:616` |
| M14 | `<list> list: <constant> declares the writer <function> at <door>, a door outside the declared door set` | A writer names an undeclared door. | `reasons_test.go:621` |
| M15 | `<list> list: <constant> declares the writer <function> at <door> twice` | A repeated constant/function/door row. | `reasons_test.go:624` |
| M16 | `<site>: <constant> is used at the <list> door <door> in <function>, and the list does not declare that writer` | A named constant stands at a declared door without a declared row. | `reasons_test.go:647` |
| M17 | `<site>: <constant> has the value "<value>" and the <list> list declares "<code>"` | A counted use's value differs from the last entry of that constant. | `reasons_test.go:654` |
| M18 | `<list> list: <constant> declares the writer <function> at <door>, and no use by name is there` | A distinct declared row has no counted use. | `reasons_test.go:659` |
| M19 | `<list> list: the door <door> has no listed use` | A distinct declared door has no counted use. | `reasons_test.go:664` |
| M20 | `<site>: a reason's Code is the string literal "<value>", never a constant's name` | A literal is used for `ReasonEntry.Code`. | `reasons_test.go:717` |
| M21 | `the lists hold <entries> entries and <n> of their Code fields are a constant used by name` | The typed `Code` use count differs from the entry count. | `reasons_test.go:723` |
| M22 | `reasons.go:<line>: Code is <code> and Constant is "<constant>"` | The exact source qualifier and constant string differ. | `reasons_test.go:773` |
| M23 | `the lists hold <entries> entries and reasons.go holds <n> literals with a Code or a Constant` | The number of literals with a non-empty parsed code or constant differs from the entry count. | `reasons_test.go:778` |

A duplicate is refused once and then counted once for distinct rows, doors, and open-door names. A use counts only when its constant/function/door row is declared. M19 iterates the distinct door order, not every repeated declaration. M22 accepts only a bare qualified selector and a string literal. M23 ignores a literal when both parsed strings are empty, even if the keys are present.

The test logs, on pass or fail (`reasons_test.go:56-58`):

```text
<list>: entries=<a> writers=<b> doors=<c> open_doors=<d> uses_at_doors=<e> literal_sites_at_doors=<f>
packages=<n> entries=<m>
```

## The marks

Fourteen block entries and four wait entries carry `no writer`. `sites.md` section 3a records every non-test Go line that named each at `1a3db2f13466502a8de7948fc7e1fa07574843d9`, the commit its lines are of; `reasons.go`, which #988 adds, names each as well. A use by name of a marked constant at a door of its list is refused by M16, while the test does not find a constant that is missing from the lists.

Three constants carry `traced, guarded at <function>`:

- `scheduler.GoneProcessExited` is written into a `SeatGone` event at `internal/scheduler/launchfail.go:156-158`. `scheduler.(*Scheduler).applyEvents` removes the seat and uses the code only in the `seat_gone` decision at `internal/scheduler/scheduler.go:620-652`; it does not pass it into either task field.
- `seatcommit.ReasonPRPending` is written only after `defer_` has set `Deferred` (`internal/seatcommit/commit.go:134-137`, `:1481-1485`, `:1517-1521`). The scheduler returns while a deferred count remains below its limit, or replaces the whole result with `ReasonCommitFailed` or `ReasonVerifierInterrupted`, before applying it (`internal/scheduler/scheduler.go:756-780`).
- `seatcommit.ReasonStepCancelled` is likewise written only on deferred results (`internal/seatcommit/commit.go:408-412`, `internal/seatcommit/verifier.go:228-235`) and is stopped by the same scheduler branch (`internal/scheduler/scheduler.go:756-780`).

The four guarded launch literals are:

- `pending`: every seat starts pending at `internal/seatlaunch/run.go:468-475`, but `seatlaunch.WaitSeats` rewrites every remaining pending state to `not_started` with `no_proof` before returning (`:448-463`).
- `up`: the state is set only together with `Up` at `internal/seatlaunch/run.go:468-475`, and `seatlaunch.(*Launcher).settleOne` settles an up seat as up or closing, never as a failure (`internal/seatlaunch/launcher.go:190-202`).
- `prepared`: `seatlaunch.Launch` sets it only in the dry-run arm (`internal/seatlaunch/run.go:268-273`); the two non-test `seatlaunch.Options` literals omit `DryRun` (`internal/cli/scheduler_run.go:298`, `internal/cli/fleet_adapters.go:174`).
- `applied`: `seatlaunch.launchFailure` returns the roster stage only when it is not applied (`internal/seatlaunch/launcher.go:227-239`).

`task.block` rejects a reason that is empty after trimming and stores only the trimmed non-empty reason (`internal/task/lease.go:808-821`). `Store.SetWait` has no equivalent non-empty check; the empty string is the value of a task that is not waiting (`internal/task/lease.go:1034-1047`). `sites.md` section 9 is the lead's measurement of all empty-string sites found by the trace.

## Stated limits

- The test does not follow a constant through a local (L1), a method value (L2), a wrapper function (L3), a concatenation (L5), or an interface method (L6). The lead's trace follows those flows and is re-run at the gate; the one test this PR adds, `TestReasonsAreTiedToTheirWriters`, does not.
- A file excluded by this host's build constraints is not read (L4), so the test ties the sites of the host on which it runs. `internal/seatexec/seatexec_unix.go` contains writer sites listed in `sites.md` sections 1 and 2 and would be excluded on Windows. CI runs on macOS and Linux. The host values are recorded under Verification.
- A test file is not read (L7).
- A string literal at a door is counted but not refused (L9). On the clean tree the block and wait literal-at-door counts are 56 and 57; run P raises them to 59 and 58.
- A constant elsewhere in a door's function is not at the door (C2). A comparison, `case`, map key, or argument to a different function is not a door use (C1).
- An anonymous interface method call keeps the interface method's name, not a concrete door (C4). A constant from another module is not a use (C5). A builtin or function-literal call is not a declared call door (C6).
- No door may be in a generic function: M24 prints `<list> list: the door <door> is in a generic function, which the test does not walk` (D32).
- No door may be a variadic function's last parameter: M25 prints `<list> list: the door <door> is a variadic function's last parameter, which the test does not fold` (D33).
- The lists remain open: block has 11 named open doors and wait has 10. `sites.md` section 6 records their lines.
- M22 compares the qualifier exactly as `reasons.go` writes it. Renaming an import there would over-refuse every entry from that package even if the typed code remained correct; the file aliases no import (`reasons.go:9-18`).
- As `corrections-to-s1-2a.md` states for S1-2a's tests, a build overlay is not read. That limit applies here too.

## Named for later, not done here

For S2:

- The launch family: 58 literal values at 93 sites through `scheduler.(LaunchFailure).Code`.
- The two router literals `class_unknown` and `retries_exhausted`.
- A test that finds a reason constant missing from the lists; until then, `no writer` entries are a hand list.

For S4, the equal-string constant pairs in `sites.md` section 8 remain separate entries:

- block `commit_failed`: `scheduler.ReasonCommitFailed`, `seatcommit.ReasonCommitFailed`
- block `grants_invalid`: `seatcommit.ReasonGrants`, `task.ReasonGrantsInvalid`
- block `growth_gate_required`: `seatcommit.ReasonGrowthGate`, `task.ReasonGrowthGateRequired`
- block `ledger_tampered`: `scheduler.ReasonLedgerTampered`, `seatcommit.ReasonLedgerTampered`
- block `pr_bound_elsewhere`: `seatcommit.ReasonPRBoundElsewhere`, `task.RefusalPRBoundElsewhere`
- block `seat_head_detached_unmerged`: `schedproc.ReasonSeatHeadDetachedUnmerged`, `seatcommit.RefusalSeatHeadDetachedUnmerged`
- block `verifier_interrupted`: `scheduler.ReasonVerifierInterrupted`, `seatcommit.ReasonVerifierInterrupted`
- wait `concurrency_cap`: `routing.SkipConcurrencyCap`, `task.WaitConcurrencyCap`
- wait `cost_cap`: `routing.SkipCostCap`, `task.WaitCostCap`
- wait `privacy_class`: `routing.SkipPrivacyClass`, `task.WaitPrivacyClass`
- wait `reviewer_family`: `routing.SkipReviewerFamily`, `task.WaitReviewerFamily`

## Rows


### Row 0

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh 0
$ git status --porcelain --untracked-files=all | wc -l
2
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (14.61s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	14.970s
exit 0
```

### Row F

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh F
in F1 internal/seatcommit/zz_p_f1.go
in F2 internal/scheduler/zz_p_f2.go
in F3 internal/scheduler/zz_p_f3.go
in F4 internal/scheduler/zz_p_f4.go
in F5 internal/seatcommit/zz_p_f5.go
in F6 internal/seatcommit/zz_p_f6.go
in F7 internal/routing/zz_p_f7.go
in F8 internal/seatexec/zz_p_f8.go
in F9 internal/routing/zz_p_f9.go
in F10 internal/routing/zz_p_f10.go
in F11 internal/seatcommit/zz_p_f11.go
in F12 internal/seatcommit/zz_p_f12.go
in F13 internal/seatcommit/zz_p_f13.go
in F14 internal/routing/zz_p_f14.go
in F15 internal/scheduler/zz_p_f15.go
in F16 internal/routing/zz_p_f16.go
in F17 internal/routing/zz_p_f17.go
in F18 internal/routing/zz_p_f18.go
in F19 internal/routing/zz_p_f19.go
in F20 internal/routing/zz_p_f20.go
in F21 internal/seatcommit/zz_p_f21.go
in F22 internal/seatcommit/zz_p_f22.go
in F23 internal/routing/zz_p_f23.go
in F24 internal/seatcommit/zz_p_f24.go
in F25 internal/seatcommit/zz_p_f25.go
in F26 internal/seatcommit/zz_p_f26.go
in F27 internal/seatcommit/zz_p_f27.go
$ git status --porcelain --untracked-files=all | wc -l
29
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:647: internal/routing/zz_p_f10.go:10: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF10, and the list does not declare that writer
    reasons_test.go:647: internal/routing/zz_p_f14.go:12: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF14, and the list does not declare that writer
    reasons_test.go:647: internal/routing/zz_p_f16.go:9: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF16, and the list does not declare that writer
    reasons_test.go:647: internal/routing/zz_p_f18.go:10: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF18, and the list does not declare that writer
    reasons_test.go:647: internal/routing/zz_p_f19.go:10: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF19, and the list does not declare that writer
    reasons_test.go:647: internal/routing/zz_p_f20.go:10: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF20, and the list does not declare that writer
    reasons_test.go:647: internal/routing/zz_p_f23.go:8: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in package level, and the list does not declare that writer
    reasons_test.go:647: internal/routing/zz_p_f7.go:8: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF7, and the list does not declare that writer
    reasons_test.go:647: internal/scheduler/zz_p_f3.go:10: task.ReasonWidenRequested is used at the block door param task.(Store).Block#2 in scheduler.zzF3, and the list does not declare that writer
    reasons_test.go:647: internal/scheduler/zz_p_f4.go:10: task.RefusalFileLock is used at the block door param task.(Store).Block#2 in scheduler.zzF4, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f1.go:5: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF1, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f11.go:6: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF11, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f12.go:5: seatcommit.ReasonNested is used at the block door field seatcommit.Result.ReasonCode in seatcommit.zzF12, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f13.go:5: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF13, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f21.go:7: seatcommit.zzLocal is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF21, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f22.go:9: seatcommit.zzF22c is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF22, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f24.go:5: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF24, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f25.go:5: seatcommit.ReasonNested is used at the block door field seatcommit.Result.ReasonCode in seatcommit.zzF25, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f26.go:3: seatcommit.ReasonNested is used at the block door field seatcommit.Result.ReasonCode in seatcommit.zzF26, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f27.go:3: seatcommit.ReasonNested is used at the block door field seatcommit.Result.ReasonCode in seatcommit.zzF27, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f5.go:5: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF5, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/zz_p_f6.go:5: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF6, and the list does not declare that writer
    reasons_test.go:647: internal/seatexec/zz_p_f8.go:5: claudeseat.TokenInsecure is used at the block door param seatexec.refusef#0 in seatexec.zzF8, and the list does not declare that writer
    reasons_test.go:647: internal/routing/zz_p_f17.go:9: task.WaitNoRoute is used at the wait door field scheduler.Route.Wait in routing.zzF17, and the list does not declare that writer
    reasons_test.go:647: internal/routing/zz_p_f9.go:8: task.WaitNoRoute is used at the wait door field scheduler.Route.Wait in routing.zzF9, and the list does not declare that writer
    reasons_test.go:647: internal/scheduler/zz_p_f15.go:10: task.WaitFileLock is used at the wait door param scheduler.(*Scheduler).wait#1 in scheduler.zzF15, and the list does not declare that writer
    reasons_test.go:647: internal/scheduler/zz_p_f2.go:11: scheduler.zzF2 is used at the wait door param scheduler.(*Scheduler).wait#1 in scheduler.zzF2f, and the list does not declare that writer
    reasons_test.go:647: internal/seatexec/zz_p_f8.go:5: claudeseat.TokenInsecure is used at the wait door param seatexec.refusef#0 in seatexec.zzF8, and the list does not declare that writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=58
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.23s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.602s
FAIL
exit 1
out F1
out F2
out F3
out F4
out F5
out F6
out F7
out F8
out F9
out F10
out F11
out F12
out F13
out F14
out F15
out F16
out F17
out F18
out F19
out F20
out F21
out F22
out F23
out F24
out F25
out F26
out F27
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row P

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh P
in C1 internal/seatcommit/zz_p_c1.go
in C2 internal/seatexec/zz_p_c2.go
in C3 internal/scheduler/zz_p_c3.go
in C4 internal/seatexec/zz_p_c4.go
in C5 internal/seatcommit/zz_p_c5.go
in C6 internal/seatcommit/zz_p_c6.go
in C7 internal/seatcommit/zz_p_c7.go
in L1 internal/seatcommit/zz_p_l1.go
in L2 internal/seatcommit/zz_p_l2.go
in L3 internal/seatcommit/zz_p_l3.go
in L4 internal/seatcommit/zz_p_l4_windows.go
in L5 internal/seatcommit/zz_p_l5.go
in L6 internal/seatcommit/zz_p_l6.go
in L7 internal/seatcommit/zz_p_l7_test.go
in L9 internal/seatcommit/zz_p_l9.go
$ git status --porcelain --untracked-files=all | wc -l
17
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=59
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=58
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (14.06s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	14.430s
exit 0
out C1
out C2
out C3
out C4
out C5
out C6
out C7
out L1
out L2
out L3
out L4
out L5
out L6
out L7
out L9
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row T

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh T
in T1 cmd/amq-squad/zz_p_t1.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
# github.com/omriariav/amq-squad/v3/cmd/amq-squad
cmd/amq-squad/zz_p_t1.go:3:9: invalid composite literal type string
exit 1
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:89: cmd/amq-squad: cmd/amq-squad/zz_p_t1.go:3:9: invalid composite literal type string
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.93s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.200s
FAIL
exit 1
out T1
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D1

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D1
in D1 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:647: internal/routing/router.go:521: task.ReasonGrantsInvalid is used at the block door field scheduler.Route.Block in routing.(*Router).decide, and the list does not declare that writer
    reasons_test.go:647: internal/scheduler/scheduler.go:964: task.ReasonGrantsInvalid is used at the block door param task.(Store).Block#2 in scheduler.(*Scheduler).dispatch, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/commit.go:247: seatcommit.ReasonGrants is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.run, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/verifier.go:140: seatcommit.ReasonGrants is used at the block door result seatcommit.planVerifier#1 in seatcommit.planVerifier, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/verifier.go:144: seatcommit.ReasonGrants is used at the block door result seatcommit.planVerifier#1 in seatcommit.planVerifier, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/verifier.go:147: seatcommit.ReasonGrants is used at the block door result seatcommit.planVerifier#1 in seatcommit.planVerifier, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/verifier.go:152: seatcommit.ReasonGrants is used at the block door result seatcommit.planVerifier#1 in seatcommit.planVerifier, and the list does not declare that writer
    reasons_test.go:659: block list: task.ReasonGrantsInvalid declares the writer seatcommit.run at param seatcommit.(Result).refuse#0, and no use by name is there
    reasons_test.go:659: block list: task.ReasonGrantsInvalid declares the writer seatcommit.planVerifier at result seatcommit.planVerifier#1, and no use by name is there
    reasons_test.go:659: block list: seatcommit.ReasonGrants declares the writer routing.(*Router).decide at field scheduler.Route.Block, and no use by name is there
    reasons_test.go:659: block list: seatcommit.ReasonGrants declares the writer scheduler.(*Scheduler).dispatch at param task.(Store).Block#2, and no use by name is there
    reasons_test.go:773: reasons.go:96: Code is seatcommit.ReasonGrants and Constant is "task.ReasonGrantsInvalid"
    reasons_test.go:773: reasons.go:125: Code is task.ReasonGrantsInvalid and Constant is "seatcommit.ReasonGrants"
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=76 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.86s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.248s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D1
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D2

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D2
in D2 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
--- PASS: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:717: internal/statecontract/reasons.go:101: a reason's Code is the string literal "nested_repository", never a constant's name
    reasons_test.go:723: the lists hold 89 entries and 88 of their Code fields are a constant used by name
    reasons_test.go:773: reasons.go:101: Code is (not a qualified name) and Constant is "seatcommit.ReasonNested"
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.54s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.795s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D2
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D3

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D3
in D3 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:647: internal/seatcommit/commit.go:150: seatcommit.ReasonWorktree is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.Run, and the list does not declare that writer
    reasons_test.go:647: internal/seatcommit/commit.go:155: seatcommit.ReasonWorktree is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.Run, and the list does not declare that writer
    reasons_test.go:56: block: entries=59 writers=57 doors=14 open_doors=11 uses_at_doors=81 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.70s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.089s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D3
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D4

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D4
in D4 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:659: block list: seatcommit.ReasonNested declares the writer seatcommit.Run at param seatcommit.(Result).refuse#0, and no use by name is there
    reasons_test.go:56: block: entries=59 writers=59 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.52s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.908s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D4
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D5

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D5
in D5 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:621: block list: seatcommit.ReasonLinkTarget declares the writer seatcommit.check at field seatcommit.Violation.Reason, a door outside the declared door set
    reasons_test.go:621: block list: seatcommit.ReasonNested declares the writer seatcommit.check at field seatcommit.Violation.Reason, a door outside the declared door set
    reasons_test.go:621: block list: seatcommit.ReasonOutside declares the writer seatcommit.check at field seatcommit.Violation.Reason, a door outside the declared door set
    reasons_test.go:621: block list: seatcommit.ReasonProtected declares the writer seatcommit.check at field seatcommit.Violation.Reason, a door outside the declared door set
    reasons_test.go:621: block list: seatcommit.ReasonSizeCap declares the writer seatcommit.check at field seatcommit.Violation.Reason, a door outside the declared door set
    reasons_test.go:659: block list: seatcommit.ReasonLinkTarget declares the writer seatcommit.check at field seatcommit.Violation.Reason, and no use by name is there
    reasons_test.go:659: block list: seatcommit.ReasonNested declares the writer seatcommit.check at field seatcommit.Violation.Reason, and no use by name is there
    reasons_test.go:659: block list: seatcommit.ReasonOutside declares the writer seatcommit.check at field seatcommit.Violation.Reason, and no use by name is there
    reasons_test.go:659: block list: seatcommit.ReasonProtected declares the writer seatcommit.check at field seatcommit.Violation.Reason, and no use by name is there
    reasons_test.go:659: block list: seatcommit.ReasonSizeCap declares the writer seatcommit.check at field seatcommit.Violation.Reason, and no use by name is there
    reasons_test.go:56: block: entries=59 writers=58 doors=13 open_doors=11 uses_at_doors=78 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.57s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.967s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D5
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D6

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D6
in D6 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:664: block list: the door field seatcommit.Violation.Detail has no listed use
    reasons_test.go:56: block: entries=59 writers=58 doors=15 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.59s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.979s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D6
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D7

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D7
in D7 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:613: block list: task.ReasonWidenRequested declares no writer and does not carry the no writer mark
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.59s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.981s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D7
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D8

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D8
in D8 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:616: block list: seatcommit.ReasonNested carries the no writer mark and declares a writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.98s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.351s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D8
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D9

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D9
in D9 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:600: wait list: task.WaitNoRoute is also on the not-a-reason list
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=90
--- FAIL: TestReasonsAreTiedToTheirWriters (14.00s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.383s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D9
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D10

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D10
in D10 internal/statecontract/reasons.go
$ go build ./...
# github.com/omriariav/amq-squad/v3/internal/statecontract
internal/statecontract/reasons.go:101:22: undefined: seatcommit.ReasonNestedX
exit 1
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
# github.com/omriariav/amq-squad/v3/internal/statecontract [github.com/omriariav/amq-squad/v3/internal/statecontract.test]
internal/statecontract/reasons.go:101:22: undefined: seatcommit.ReasonNestedX
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract [build failed]
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D10
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D12

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D12
in D12 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:530: not-a-reason list: task.RefusalFileLock is declared twice
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=90
--- FAIL: TestReasonsAreTiedToTheirWriters (13.59s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.981s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D12
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D13

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D13
in D13 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:604: block list: task.ReasonWidenRequested carries the mark "no writers", which is not a declared mark
    reasons_test.go:613: block list: task.ReasonWidenRequested declares no writer and does not carry the no writer mark
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.73s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.126s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D13
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D14

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D14
in D14 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:567: block list: the open door "the --reason flag of task block" names cli.runTaskTransitionX, and no such function is declared
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.13s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.514s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D14
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D15

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D15
in D15 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:559: block list: the open door "the --reason flag of task block" is declared twice
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.72s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.113s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D15
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D16

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D16
in D16 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:554: block list: the list is not declared open with its open doors
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.56s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.956s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D16
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D17

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D17
in D17 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:609: block list: scheduler.GoneProcessExited is marked guarded at scheduler.(*Scheduler).applyEvent, and no such function is declared
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.16s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.742s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D17
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D18

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D18
in D18 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:595: block list: task.ReasonWidenRequested is declared twice
    reasons_test.go:56: block: entries=60 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=90
--- FAIL: TestReasonsAreTiedToTheirWriters (14.45s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.827s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D18
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D19

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D19
in D19 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:534: not-a-reason list: task.RefusalFileLock carries a mark or a writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.13s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.496s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D19
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D20

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D20
in D20 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:546: block list: door field seatcommit.Violation.Reason is declared twice
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.06s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.449s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D20
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D21

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D21
in D21 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:563: block list: the open door "the --reason flag of task block" names no function
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.21s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.596s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D21
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D22

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D22
in D22 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:624: block list: seatcommit.ReasonNested declares the writer seatcommit.check at field seatcommit.Violation.Reason twice
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.03s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.384s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D22
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D23

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D23
in D23 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:654: internal/seatcommit/commit.go:1159: seatcommit.ReasonOutside has the value "outside_declared_files" and the block list declares "nested_repository"
    reasons_test.go:654: internal/seatcommit/commit.go:1162: seatcommit.ReasonNested has the value "nested_repository" and the block list declares "outside_declared_files"
    reasons_test.go:773: reasons.go:101: Code is seatcommit.ReasonNested and Constant is "seatcommit.ReasonOutside"
    reasons_test.go:773: reasons.go:103: Code is seatcommit.ReasonOutside and Constant is "seatcommit.ReasonNested"
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.22s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.585s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D23
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D24

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D24
in D24 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
--- PASS: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:778: the lists hold 89 entries and reasons.go holds 88 literals with a Code or a Constant
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.47s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	15.097s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D24
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D25

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D25
in D25 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:723: the lists hold 89 entries and 88 of their Code fields are a constant used by name
    reasons_test.go:773: reasons.go:128: Code is  and Constant is "task.ReasonWidenRequested"
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.53s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.913s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D25
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D26

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D26
in D26 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
--- PASS: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (14.10s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	14.470s
exit 0
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D26
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D27

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D27
in D27 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:546: block list: door field seatcommit.Violation.Detail is declared twice
    reasons_test.go:664: block list: the door field seatcommit.Violation.Detail has no listed use
    reasons_test.go:56: block: entries=59 writers=58 doors=15 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.93s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.313s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D27
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D28

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D28
in D28 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:595: block list: seatcommit.ReasonNested is declared twice
    reasons_test.go:624: block list: seatcommit.ReasonNested declares the writer seatcommit.check at field seatcommit.Violation.Reason twice
    reasons_test.go:654: internal/seatcommit/commit.go:1162: seatcommit.ReasonNested has the value "nested_repository" and the block list declares "outside_declared_files"
    reasons_test.go:773: reasons.go:102: Code is seatcommit.ReasonOutside and Constant is "seatcommit.ReasonNested"
    reasons_test.go:56: block: entries=60 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=90
--- FAIL: TestReasonsAreTiedToTheirWriters (13.77s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.124s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D28
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D29

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D29
in D29 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:624: block list: seatcommit.ReasonNested declares the writer seatcommit.Run at param seatcommit.(Result).refuse#0 twice
    reasons_test.go:659: block list: seatcommit.ReasonNested declares the writer seatcommit.Run at param seatcommit.(Result).refuse#0, and no use by name is there
    reasons_test.go:56: block: entries=59 writers=59 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.12s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.484s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D29
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D30

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D30
in D30 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
--- PASS: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:773: reasons.go:101: Code is (not a qualified name) and Constant is "seatcommit.ReasonNested"
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.93s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.325s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D30
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D31

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D31
in D31 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
--- PASS: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:773: reasons.go:101: Code is seatcommit.ReasonNested and Constant is ""
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.99s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.242s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D31
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D32

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D32
in D32 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:576: block list: the door param cli.acquireRevalidatedWriter#3 is in a generic function, which the test does not walk
    reasons_test.go:664: block list: the door param cli.acquireRevalidatedWriter#3 has no listed use
    reasons_test.go:56: block: entries=59 writers=58 doors=15 open_doors=11 uses_at_doors=83 literal_sites_at_doors=58
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.77s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.126s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D32
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D33

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D33
in D33 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:585: block list: the door param seatexec.refusef#2 is a variadic function's last parameter, which the test does not fold
    reasons_test.go:664: block list: the door param seatexec.refusef#2 has no listed use
    reasons_test.go:56: block: entries=59 writers=58 doors=15 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.96s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.337s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D33
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row D34

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh D34
in D34 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:554: block list: the list is not declared open with its open doors
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=0 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.74s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.105s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D34
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row E1

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh E1
in E1 internal/registry/pins.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:659: block list: registry.PinMissing declares the writer registry.OpenPinned at result registry.OpenPinned#1, and no use by name is there
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=82 literal_sites_at_doors=57
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.54s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.822s
FAIL
exit 1
$ git diff --quiet -- internal/registry/pins.go
exit 0
out E1
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row E2

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh E2
in E2 internal/seatcommit/policy.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (13.64s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.016s
FAIL
exit 1
$ git diff --quiet -- internal/seatcommit/policy.go
exit 0
out E2
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row E3

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh E3
in E3 internal/scheduler/scheduler.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (13.64s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	14.027s
exit 0
$ git diff --quiet -- internal/scheduler/scheduler.go
exit 0
out E3
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row E4

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh E4
in E4 internal/scheduler/scheduler.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:647: internal/scheduler/scheduler.go:744: task.ReasonWidenRequested is used at the block door local code in scheduler.(*Scheduler).commit in scheduler.(*Scheduler).commit, and the list does not declare that writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.65s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.910s
FAIL
exit 1
$ git diff --quiet -- internal/scheduler/scheduler.go
exit 0
out E4
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row E5

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh E5
in E5 internal/scheduler/scheduler.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:647: internal/scheduler/scheduler.go:744: task.ReasonWidenRequested is used at the block door local code in scheduler.(*Scheduler).commit in scheduler.(*Scheduler).commit, and the list does not declare that writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.72s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.970s
FAIL
exit 1
$ git diff --quiet -- internal/scheduler/scheduler.go
exit 0
out E5
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row E6

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh E6
in E6 internal/routing/router.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (13.70s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	14.084s
exit 0
$ git diff --quiet -- internal/routing/router.go
exit 0
out E6
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row E7

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh E7
in E7 internal/routing/router.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (13.59s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	13.990s
exit 0
$ git diff --quiet -- internal/routing/router.go
exit 0
out E7
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Row E8

```console
$ bash ../../../../amq-squad-lead/T/s12b2-inputs/rows.sh E8
in E8 internal/routing/router.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:647: internal/routing/router.go:800: task.WaitDependencies is used at the wait door element in routing.waitReason in routing.waitReason, and the list does not declare that writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.64s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.033s
FAIL
exit 1
$ git diff --quiet -- internal/routing/router.go
exit 0
out E8
$ git status --porcelain --untracked-files=all | wc -l
2
```

### Rows G

#### G1 before the follow-up

```console
$ bash ../../../../amq-squad-lead/T/s12bfu/inputs/rows-g.sh G1
in G1 internal/seatexec/zz_p_g1.go
$ git status --porcelain --untracked-files=all | wc -l
1
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:643: internal/seatexec/zz_p_g1.go:6: seatexec.IdentityIncomplete is used at the block door param seatexec.refusef#0 in seatexec.zzG1, and the list does not declare that writer
    reasons_test.go:643: internal/seatexec/zz_p_g1.go:6: seatexec.IdentityIncomplete is used at the wait door param seatexec.refusef#0 in seatexec.zzG1, and the list does not declare that writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (14.65s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	15.171s
FAIL
exit 1
out G1
$ git status --porcelain --untracked-files=all | wc -l
0
```

This run is #991's, on `3bf07c72bccc352fd60a44d295d5c43306c18d56` before the change; it cannot be run again on a tree that holds the change.

#### G1

```console
$ bash ../../../../amq-squad-lead/T/s12bfu2/inputs/rows-g.sh G1
in G1 internal/seatexec/zz_p_g1.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (13.52s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	13.910s
exit 0
out G1
$ git status --porcelain --untracked-files=all | wc -l
2
```

#### G2

```console
$ bash ../../../../amq-squad-lead/T/s12bfu2/inputs/rows-g.sh G2
in G2 internal/seatexec/zz_p_g2.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (13.55s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	13.934s
exit 0
out G2
$ git status --porcelain --untracked-files=all | wc -l
2
```

#### G3

```console
$ bash ../../../../amq-squad-lead/T/s12bfu2/inputs/rows-g.sh G3
in G3 internal/scheduler/zz_p_g3.go
$ git status --porcelain --untracked-files=all | wc -l
3
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:647: internal/scheduler/zz_p_g3.go:12: task.ReasonWidenRequested is used at the block door param task.(Store).Block#2 in scheduler.zzG3, and the list does not declare that writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.59s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.952s
FAIL
exit 1
out G3
$ git status --porcelain --untracked-files=all | wc -l
2
```

#### G

```console
$ bash ../../../../amq-squad-lead/T/s12bfu2/inputs/rows-g.sh G
in G1 internal/seatexec/zz_p_g1.go
in G2 internal/seatexec/zz_p_g2.go
in G3 internal/scheduler/zz_p_g3.go
$ git status --porcelain --untracked-files=all | wc -l
5
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:647: internal/scheduler/zz_p_g3.go:12: task.ReasonWidenRequested is used at the block door param task.(Store).Block#2 in scheduler.zzG3, and the list does not declare that writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.55s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.921s
FAIL
exit 1
out G1
out G2
out G3
$ git status --porcelain --untracked-files=all | wc -l
2
```

## Round 2

- Anonymous embedding now walks through an unnamed outer struct to the declaring field type (`reasons_test.go:415-434`); F18 trips the rule.
- A defined pointer type is dereferenced before naming its struct (`reasons_test.go:393-404`); F19 trips the rule.
- Missing checker records no longer panic the survey (`reasons_test.go:209-233`, `:503-524`); run T supplies a type error and the survey continues.
- A local `const` declaration is excluded from the local-door form (`reasons_test.go:454-457`); E3 passes.
- A call of an interface's method keeps the interface in its name whatever receiver the selection names because the method's own receiver decides (`reasons_test.go:309-330`); C4, G1 and G2 pass; G3 is refused.
- A source literal with both parsed strings empty is omitted even when `Code` or `Constant` keys exist (`reasons_test.go:737-770`); D26 passes.
- Missing-door diagnostics iterate distinct doors, so a duplicated door produces M19 once (`reasons_test.go:543-551`, `:662-666`); D27 trips the rule.
- M24 refuses a door in a function with type parameters (`reasons_test.go:575-577`); D32 trips it.
- M25 refuses a variadic function's final parameter as a door (`reasons_test.go:579-586`); D33 trips it.
- N1, the round, replacement and follow-up statements, are at `README.md:3-7`.
- N2, the corrected 94-line input manifest evidence, starts at `README.md:22`.
- N3, declaration and generated-JSON ranges, starts at `README.md:129`.
- N4, the final test contract and M1 through M25, starts at `README.md:143`.
- N5, the corrected measurement sentence, is at `README.md:139`.
- N6, the re-read mark ranges, starts at `README.md:200`.
- N7, corrected limits plus C4 through C6, M24, and M25, starts at `README.md:219`.
- N8, all eleven equal-string pairs, starts at `README.md:233`.
- N9, every row command and its complete output, starts at `README.md:255`.

## The follow-up

- Selected-method naming now reads the method's own receiver rather than the selection receiver (`reasons_test.go:309-330`); G1 and G2 pass, while G3 is refused.
- Both measurement sentences now name #988 (`README.md:139`, `:202`); the manifest and immutable input checks show the measurement inputs are unchanged.
- The Round 2 source-literal, distinct-door and M24 ranges now end on their closing braces (`reasons_test.go:737-770`, `:662-666`, `:575-577`); D26, D27 and D32 show those checks.
- The interface-name contract at `README.md:149` and its Round 2 record at `:1754` now state that the method's own receiver decides; the limits sentence at `:226` remains true. C4, G1 and G2 pass; G3 is refused.
- The follow-up cite checker reads the prose before Rows and from Round 2 through this section; the Verification run records every resolved range and its final count.
- The Verification stat is scoped to `internal/statecontract`, and the sentence below it records why the note itself is excluded (`README.md:1856`).
- The four verification blocks from #988 are restored after the test-file trailing-whitespace check (`README.md:2028-2057`).
- The Round 2 local-const and door-collection ranges now name the exact guard and the whole loop (`reasons_test.go:454-457`, `:543-551`); E3 passes and D27 is refused once.
- Precheck: TIP and HEAD are both `f207c7d57189c97f254b16aeb69c0e1f95b080fa`; `git diff --stat f207c7d57189c97f254b16aeb69c0e1f95b080fa HEAD -- internal/statecontract docs/v3-spike/330-s1-2b-reasons pi/test/state-contract.json internal/cli/task_widen.go internal/registry/pins.go internal/routing/router.go internal/routing/tamper.go internal/scheduler/hooks.go internal/scheduler/launchfail.go internal/scheduler/roster.go internal/scheduler/scheduler.go internal/scheduler/tamper.go internal/seatcommit/commit.go internal/seatcommit/verifier.go internal/seatexec/claude.go internal/seatexec/pi.go internal/seatexec/seatexec_unix.go internal/seatlaunch/roster.go internal/task/lease.go` printed nothing.
- This section's work was carried from #991's head by `git apply`; the two files' blob hashes after the carry were `6f2b605499eb187197c7f8a7cc7eca6141003d9b` (this note) and `1b52cddfb7d5c408ecb6e80cc628aab6f672e732` (`reasons_test.go`).

## Verification

### `go build ./...`

```console
$ go build ./...
exit 0
```

### `go vet ./internal/statecontract/`

```console
$ go vet ./internal/statecontract/
exit 0
```

### `gofmt -l internal/statecontract`

```console
$ gofmt -l internal/statecontract
exit 0
```

### `go test ./internal/statecontract/ -count=1 -v -timeout 240s`

```console
$ go test ./internal/statecontract/ -count=1 -v -timeout 240s
=== RUN   TestContractFileIsCurrent
--- PASS: TestContractFileIsCurrent (0.00s)
=== RUN   TestCommittedContractIsSubstantial
--- PASS: TestCommittedContractIsSubstantial (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (13.90s)
=== RUN   TestOnlyTheListedFilesImportUnsafeOrC
    statewrite_test.go:72: directories=62 files=421
--- PASS: TestOnlyTheListedFilesImportUnsafeOrC (0.08s)
=== RUN   TestNoPackageOutsideTaskWritesTaskState
    statewrite_test.go:227: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
--- PASS: TestNoPackageOutsideTaskWritesTaskState (13.68s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	28.194s
exit 0
```

### `go env GOOS GOARCH`

```console
$ go env GOOS GOARCH
darwin
arm64
exit 0
```

### `git status --porcelain --untracked-files=all`

```console
$ git status --porcelain --untracked-files=all
 M docs/v3-spike/330-s1-2b-reasons/README.md
 M internal/statecontract/reasons_test.go
exit 0
```

### `git diff --stat -- internal/statecontract`

```console
$ git diff --stat -- internal/statecontract
 internal/statecontract/reasons_test.go | 10 +++++++---
 1 file changed, 7 insertions(+), 3 deletions(-)
exit 0
```

The stat names its path: without one the command counts this note, which changes after any run of it.

### `git hash-object internal/statecontract/contract.go`

```console
$ git hash-object internal/statecontract/contract.go
7e52ba6f52a57ee3e5aa7955a886eaca2008945b
exit 0
```

### `cmp ../../../../amq-squad-lead/T/s12b2-inputs/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go`

```console
$ cmp ../../../../amq-squad-lead/T/s12b2-inputs/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
```

### `cmp ../../../../amq-squad-lead/T/s12b2-inputs/tree/docs/v3-spike/330-s1-2b-reasons/sites.md docs/v3-spike/330-s1-2b-reasons/sites.md`

```console
$ cmp ../../../../amq-squad-lead/T/s12b2-inputs/tree/docs/v3-spike/330-s1-2b-reasons/sites.md docs/v3-spike/330-s1-2b-reasons/sites.md
exit 0
```

### `cmp ../../../../amq-squad-lead/T/s12b2-inputs/tree/docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md`

```console
$ cmp ../../../../amq-squad-lead/T/s12b2-inputs/tree/docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md
exit 0
```

### `git diff --quiet -- internal/statecontract/contract.go pi/test/state-contract.json; echo $?`

```console
$ git diff --quiet -- internal/statecontract/contract.go pi/test/state-contract.json; echo $?
0
exit 0
```

### `python3 ../../../../amq-squad-lead/T/s12bfu2/inputs/cites.py`

```console
$ python3 ../../../../amq-squad-lead/T/s12bfu2/inputs/cites.py
note:131 internal/statecontract/reasons.go:20-24 | first: // ReasonWriter is one place a listed constant is passed as  | last: } | next:
note:131 internal/statecontract/reasons.go:26-32 | first: // ReasonEntry is one declared constant. | last: } | next:
note:131 internal/statecontract/reasons.go:34-38 | first: // OpenDoor is one place a reason enters that no list holds. | last: } | next:
note:131 internal/statecontract/reasons.go:40-46 | first: // ReasonList is one vocabulary: open, with its named doors. | last: } | next:
note:131 internal/statecontract/reasons.go:48-143 | first: // BlockReasons returns the declared block reasons. | last: } | next:
note:131 internal/statecontract/reasons.go:145-199 | first: // WaitReasons returns the declared wait reasons. | last: } | next:
note:131 internal/statecontract/reasons.go:201-212 | first: // NotReasons returns the constants that are refusal codes a | last: } | next:
note:135 internal/statecontract/contract.go:11-16 | first: // Contract is the generated state-contract document. | last: } | next:
note:135 internal/statecontract/contract.go:28-33 | first: // ReasonsContract is the reasons portion of the state-contr | last: } | next:
note:135 internal/statecontract/contract.go:35-45 | first: // Build builds the current state contract from package decl | last: } | next:
note:135 pi/test/state-contract.json:405-405 | first: "reasons": { | last: "reasons": { | next: "block": {
note:135 pi/test/state-contract.json:406-406 | first: "block": { | last: "block": { | next: "open": true,
note:135 pi/test/state-contract.json:1072-1072 | first: "wait": { | last: "wait": { | next: "open": true,
note:135 pi/test/state-contract.json:1401-1401 | first: "not_reasons": [ | last: "not_reasons": [ | next: {
note:135 internal/statecontract/contract.go:38-38 | first: Schema: "amq-squad-state-contract/1", | last: Schema: "amq-squad-state-contract/1", | next: Task: TaskContract{
note:145 internal/statecontract/reasons_test.go:47-59 | first: func TestReasonsAreTiedToTheirWriters(t *testing.T) { | last: } | next:
note:147 internal/statecontract/reasons_test.go:61-146 | first: func surveyReasonUses(t *testing.T) reasonSurvey { | last: } | next:
note:149 internal/statecontract/reasons_test.go:209-233 | first: func reasonFunctionName(function *types.Func) string { | last: } | next:
note:149 internal/statecontract/reasons_test.go:309-330 | first: func reasonSelectedFunctionName(function *types.Func) string | last: } | next:
note:151 internal/statecontract/reasons_test.go:175-196 | first: func widenReasonExpression(expression ast.Expr, parents map[ | last: } | next:
note:155 internal/statecontract/reasons_test.go:251-330 | first: func reasonCallPlace(expression ast.Expr, parents map[ast.No | last: } | next:
note:156 internal/statecontract/reasons_test.go:332-434 | first: func reasonFieldPlace(expression ast.Expr, parents map[ast.N | last: } | next:
note:157 internal/statecontract/reasons_test.go:436-473 | first: func reasonLocalPlace(expression ast.Expr, parents map[ast.N | last: } | next:
note:158 internal/statecontract/reasons_test.go:475-501 | first: func reasonResultPlace(expression ast.Expr, parents map[ast. | last: } | next:
note:159 internal/statecontract/reasons_test.go:503-524 | first: func reasonElementPlace(expression ast.Expr, parents map[ast | last: } | next:
note:165 internal/statecontract/reasons_test.go:530-530 | first: t.Errorf("not-a-reason list: %s is declared twice", entry.Co | last: t.Errorf("not-a-reason list: %s is declared twice", entry.Co | next: }
note:166 internal/statecontract/reasons_test.go:534-534 | first: t.Errorf("not-a-reason list: %s carries a mark or a writer", | last: t.Errorf("not-a-reason list: %s carries a mark or a writer", | next: }
note:167 internal/statecontract/reasons_test.go:546-546 | first: t.Errorf("%s list: door %s is declared twice", name, door) | last: t.Errorf("%s list: door %s is declared twice", name, door) | next: } else {
note:168 internal/statecontract/reasons_test.go:554-554 | first: t.Errorf("%s list: the list is not declared open with its op | last: t.Errorf("%s list: the list is not declared open with its op | next: }
note:169 internal/statecontract/reasons_test.go:559-559 | first: t.Errorf("%s list: the open door %q is declared twice", name | last: t.Errorf("%s list: the open door %q is declared twice", name | next: }
note:170 internal/statecontract/reasons_test.go:563-563 | first: t.Errorf("%s list: the open door %q names no function", name | last: t.Errorf("%s list: the open door %q names no function", name | next: }
note:171 internal/statecontract/reasons_test.go:567-567 | first: t.Errorf("%s list: the open door %q names %s, and no such fu | last: t.Errorf("%s list: the open door %q names %s, and no such fu | next: }
note:172 internal/statecontract/reasons_test.go:576-576 | first: t.Errorf("%s list: the door %s is in a generic function, whi | last: t.Errorf("%s list: the door %s is in a generic function, whi | next: }
note:173 internal/statecontract/reasons_test.go:585-585 | first: t.Errorf("%s list: the door %s is a variadic function's last | last: t.Errorf("%s list: the door %s is a variadic function's last | next: }
note:174 internal/statecontract/reasons_test.go:595-595 | first: t.Errorf("%s list: %s is declared twice", name, entry.Consta | last: t.Errorf("%s list: %s is declared twice", name, entry.Consta | next: }
note:175 internal/statecontract/reasons_test.go:600-600 | first: t.Errorf("%s list: %s is also on the not-a-reason list", nam | last: t.Errorf("%s list: %s is also on the not-a-reason list", nam | next: }
note:176 internal/statecontract/reasons_test.go:604-604 | first: t.Errorf("%s list: %s carries the mark %q, which is not a de | last: t.Errorf("%s list: %s carries the mark %q, which is not a de | next: }
note:177 internal/statecontract/reasons_test.go:609-609 | first: t.Errorf("%s list: %s is marked guarded at %s, and no such f | last: t.Errorf("%s list: %s is marked guarded at %s, and no such f | next: }
note:178 internal/statecontract/reasons_test.go:613-613 | first: t.Errorf("%s list: %s declares no writer and does not carry  | last: t.Errorf("%s list: %s declares no writer and does not carry  | next: }
note:179 internal/statecontract/reasons_test.go:616-616 | first: t.Errorf("%s list: %s carries the no writer mark and declare | last: t.Errorf("%s list: %s carries the no writer mark and declare | next: }
note:180 internal/statecontract/reasons_test.go:621-621 | first: t.Errorf("%s list: %s declares the writer %s at %s, a door o | last: t.Errorf("%s list: %s declares the writer %s at %s, a door o | next: }
note:181 internal/statecontract/reasons_test.go:624-624 | first: t.Errorf("%s list: %s declares the writer %s at %s twice", n | last: t.Errorf("%s list: %s declares the writer %s at %s twice", n | next: } else {
note:182 internal/statecontract/reasons_test.go:647-647 | first: t.Errorf("%s: %s is used at the %s door %s in %s, and the li | last: t.Errorf("%s: %s is used at the %s door %s in %s, and the li | next: continue
note:183 internal/statecontract/reasons_test.go:654-654 | first: t.Errorf("%s: %s has the value %s and the %s list declares % | last: t.Errorf("%s: %s has the value %s and the %s list declares % | next: }
note:184 internal/statecontract/reasons_test.go:659-659 | first: t.Errorf("%s list: %s declares the writer %s at %s, and no u | last: t.Errorf("%s list: %s declares the writer %s at %s, and no u | next: }
note:185 internal/statecontract/reasons_test.go:664-664 | first: t.Errorf("%s list: the door %s has no listed use", name, doo | last: t.Errorf("%s list: the door %s has no listed use", name, doo | next: }
note:186 internal/statecontract/reasons_test.go:717-717 | first: t.Errorf("%s: a reason's Code is the string literal %s, neve | last: t.Errorf("%s: a reason's Code is the string literal %s, neve | next: continue
note:187 internal/statecontract/reasons_test.go:723-723 | first: t.Errorf("the lists hold %d entries and %d of their Code fie | last: t.Errorf("the lists hold %d entries and %d of their Code fie | next: }
note:188 internal/statecontract/reasons_test.go:773-773 | first: t.Errorf("reasons.go:%d: Code is %s and Constant is %q", fse | last: t.Errorf("reasons.go:%d: Code is %s and Constant is %q", fse | next: }
note:189 internal/statecontract/reasons_test.go:778-778 | first: t.Errorf("the lists hold %d entries and reasons.go holds %d  | last: t.Errorf("the lists hold %d entries and reasons.go holds %d  | next: }
note:193 internal/statecontract/reasons_test.go:56-58 | first: t.Logf("block: entries=%d writers=%d doors=%d open_doors=%d  | last: t.Logf("packages=%d entries=%d", survey.packages, entries) | next: }
note:206 internal/scheduler/launchfail.go:156-158 | first: if gone { | last: } | next: }
note:206 internal/scheduler/scheduler.go:620-652 | first: case SeatGone: | last: } | next: case SeatActivity:
note:207 internal/seatcommit/commit.go:134-137 | first: func (r Result) defer_(detail string) Result { | last: } | next:
note:207 internal/seatcommit/commit.go:1481-1485 | first: pending := func(why string) (Result, bool) { | last: } | next: run, why := ghRunner(o, l, dir)
note:207 internal/seatcommit/commit.go:1517-1521 | first: pending := func(why string) Result { | last: } | next: if l.Repository == "" {
note:207 internal/scheduler/scheduler.go:756-780 | first: res := s.Committer.Commit(holder, t) | last: } | next: // The result is applied in the store fi
note:208 internal/seatcommit/commit.go:408-412 | first: if o.cancelled() { | last: } | next: out := commitAndPush(o, sh, ledger, base
note:208 internal/seatcommit/verifier.go:228-235 | first: if err != nil { | last: } | next: out.ExitCode, out.Reason, out.DurationMS
note:212 internal/seatlaunch/run.go:468-475 | first: func seatState(q Squad, s Seat, launchID string, srv *proofS | last: } | next: if refused != "" {
note:212 internal/seatlaunch/run.go:448-463 | first: done := true | last: } | next: time.Sleep(poll)
note:213 internal/seatlaunch/launcher.go:190-202 | first: switch { | last: } | next: state, ambiguous := r.state, r.ambiguous
note:214 internal/seatlaunch/run.go:268-273 | first: if o.DryRun { | last: } | next: if len(missing) > 0 {
note:214 internal/cli/scheduler_run.go:298-298 | first: opts := seatlaunch.Options{Wait: 90 * time.Second, Target: & | last: opts := seatlaunch.Options{Wait: 90 * time.Second, Target: & | next: var amqLauncher scheduler.SeatLauncher
note:214 internal/cli/fleet_adapters.go:174-174 | first: res, err := launch(ctx, q, seatlaunch.Options{Wait: wait}) | last: res, err := launch(ctx, q, seatlaunch.Options{Wait: wait}) | next: if !req.LongRunning {
note:215 internal/seatlaunch/launcher.go:227-239 | first: func launchFailure(res Result, err error, seat *SeatState, a | last: } | next:
note:217 internal/task/lease.go:808-821 | first: func block(t *Task, reason, detail, route string) error { | last: } | next:
note:217 internal/task/lease.go:1034-1047 | first: // SetWait records why a pending task waits; it writes only  | last: } | next:
note:230 internal/statecontract/reasons.go:9-18 | first: import ( | last: ) | next:
note:1750 internal/statecontract/reasons_test.go:415-434 | first: func reasonSelectedField(selection *types.Selection) (string | last: } | next:
note:1751 internal/statecontract/reasons_test.go:393-404 | first: func reasonDereferenceType(typ types.Type) types.Type { | last: } | next:
note:1753 internal/statecontract/reasons_test.go:454-457 | first: declaration, ok := parents[spec].(*ast.GenDecl) | last: } | next: for i, value := range spec.Values {
note:1755 internal/statecontract/reasons_test.go:737-770 | first: code := "" | last: } | next: literals++
note:1756 internal/statecontract/reasons_test.go:543-551 | first: var doorOrder []string | last: } | next: stats.doors = len(doors)
note:1756 internal/statecontract/reasons_test.go:662-666 | first: for _, door := range doorOrder { | last: } | next: return stats
note:1757 internal/statecontract/reasons_test.go:575-577 | first: if signature := survey.functions[functionName]; signature != | last: } | next: }
note:1758 internal/statecontract/reasons_test.go:579-586 | first: functionName, parameter, ok := reasonParameterDoor(door) | last: } | next: }
note:1759 docs/v3-spike/330-s1-2b-reasons/README.md:3-7 | first: Base: `91081d70ea66e5c6c0f7747935328fc1467909a9`. | last: #988 landed it as `65bb47442b37a744058f3095fb336646f4a8ddcc` | next:
note:1760 docs/v3-spike/330-s1-2b-reasons/README.md:22-22 | first: ## Inputs | last: ## Inputs | next:
note:1761 docs/v3-spike/330-s1-2b-reasons/README.md:129-129 | first: ## What is declared | last: ## What is declared | next:
note:1762 docs/v3-spike/330-s1-2b-reasons/README.md:143-143 | first: ## What the test checks | last: ## What the test checks | next:
note:1763 docs/v3-spike/330-s1-2b-reasons/README.md:139-139 | first: `sites.md` section 3a records every non-test Go line that na | last: `sites.md` section 3a records every non-test Go line that na | next:
note:1764 docs/v3-spike/330-s1-2b-reasons/README.md:200-200 | first: ## The marks | last: ## The marks | next:
note:1765 docs/v3-spike/330-s1-2b-reasons/README.md:219-219 | first: ## Stated limits | last: ## Stated limits | next:
note:1766 docs/v3-spike/330-s1-2b-reasons/README.md:233-233 | first: ## Named for later, not done here | last: ## Named for later, not done here | next:
note:1767 docs/v3-spike/330-s1-2b-reasons/README.md:255-255 | first: ## Rows | last: ## Rows | next:
note:1772 docs/v3-spike/330-s1-2b-reasons/README.md:202-202 | first: Fourteen block entries and four wait entries carry `no write | last: Fourteen block entries and four wait entries carry `no write | next:
note:1774 docs/v3-spike/330-s1-2b-reasons/README.md:149-149 | first: Names come from `go/types`. A function is `<package>.<Name>` | last: Names come from `go/types`. A function is `<package>.<Name>` | next:
note:1774 docs/v3-spike/330-s1-2b-reasons/README.md:1754-1754 | first: - A call of an interface's method keeps the interface in its | last: - A call of an interface's method keeps the interface in its | next: - A source literal with both parsed stri
note:1774 docs/v3-spike/330-s1-2b-reasons/README.md:226-226 | first: - An anonymous interface method call keeps the interface met | last: - An anonymous interface method call keeps the interface met | next: - No door may be in a generic function:
note:1776 docs/v3-spike/330-s1-2b-reasons/README.md:1856-1856 | first: The stat names its path: without one the command counts this | last: The stat names its path: without one the command counts this | next:
note:1777 docs/v3-spike/330-s1-2b-reasons/README.md:2028-2057 | first: ### `git diff --no-index --check /dev/null docs/v3-spike/330 | last: ``` | next:
cites 92
exit 0
```

### `grep -c 't\.Helper()' internal/statecontract/reasons_test.go`

```console
$ grep -c 't\.Helper()' internal/statecontract/reasons_test.go
0
exit 1
```

### `grep -c '/Us''ers/' docs/v3-spike/330-s1-2b-reasons/README.md`

```console
$ grep -c '/Us''ers/' docs/v3-spike/330-s1-2b-reasons/README.md
0
exit 1
```

### `grep -cE '[[:space:]]+$' docs/v3-spike/330-s1-2b-reasons/README.md`

```console
$ grep -cE '[[:space:]]+$' docs/v3-spike/330-s1-2b-reasons/README.md
0
exit 1
```

### `grep -cE '[[:space:]]+$' internal/statecontract/reasons_test.go`

```console
$ grep -cE '[[:space:]]+$' internal/statecontract/reasons_test.go
0
exit 1
```


### `git diff --no-index --check /dev/null docs/v3-spike/330-s1-2b-reasons/README.md`

```console
$ git diff --no-index --check /dev/null docs/v3-spike/330-s1-2b-reasons/README.md
exit 1
```

### `sed -n '48,143p' internal/statecontract/reasons.go | grep -c 'Mark: "no writer"'`

```console
$ sed -n '48,143p' internal/statecontract/reasons.go | grep -c 'Mark: "no writer"'
14
exit 0
```

### `sed -n '145,199p' internal/statecontract/reasons.go | grep -c 'Mark: "no writer"'`

```console
$ sed -n '145,199p' internal/statecontract/reasons.go | grep -c 'Mark: "no writer"'
4
exit 0
```

### `awk '/^### 5a/{on=1;next} /^### 5b/{on=0} on && /^\| `/{values++; sites+=gsub(/internal\//,"&")} END{printf "values=%d sites=%d\n", values, sites}' docs/v3-spike/330-s1-2b-reasons/sites.md`

```console
$ awk '/^### 5a/{on=1;next} /^### 5b/{on=0} on && /^\| `/{values++; sites+=gsub(/internal\//,"&")} END{printf "values=%d sites=%d\n", values, sites}' docs/v3-spike/330-s1-2b-reasons/sites.md
values=58 sites=93
exit 0
```

### `git diff --check`

```console
$ git diff --check
exit 0
```

### `go run ./scripts/redact-evidence -check docs/v3-spike/330-s1-2b-reasons/`

```console
$ go run ./scripts/redact-evidence -check docs/v3-spike/330-s1-2b-reasons/
exit 0
```

## Not run by the seat; the lead's, on the PR head

- Locked `make ci`.
- Locked `make docs-html-check`.
