# S1-2b: declared block and wait reasons

Base: `1a3db2f13466502a8de7948fc7e1fa07574843d9`.

This is S1-2b, the second of S1-2's two tasks, after #980. It adds five files:

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
4401bc38220f1a22e24ba5aff90b47dfd687b0b3cbcddc0937533e4f4b925da2  MANIFEST.sha256
```

```console
$ (cd $IN && shasum -a 256 -c MANIFEST.sha256)
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
data/D3.go: OK
data/D4.go: OK
data/D5.go: OK
data/D6.go: OK
data/D7.go: OK
data/D8.go: OK
data/D9.go: OK
probes/C1.go: OK
probes/C2.go: OK
probes/C3.go: OK
probes/F1.go: OK
probes/F10.go: OK
probes/F11.go: OK
probes/F12.go: OK
probes/F13.go: OK
probes/F14.go: OK
probes/F15.go: OK
probes/F16.go: OK
probes/F17.go: OK
probes/F2.go: OK
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
probes/TARGETS.tsv: OK
product/E1.pins.go: OK
product/E2.policy.go: OK
rows.sh: OK
tree/docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md: OK
tree/docs/v3-spike/330-s1-2b-reasons/sites.md: OK
tree/internal/statecontract/reasons.go: OK
```

`$IN` is the brief's input directory, and its value is not printed because this note holds no absolute user path.

## What is declared

`reasons.go` declares `ReasonWriter` (`reasons.go:20-24`), `ReasonEntry` (`:26-32`), `OpenDoor` (`:34-38`), and `ReasonList` (`:40-46`). It exposes `BlockReasons` (`:48-143`), `WaitReasons` (`:145-200`), and `NotReasons` (`:202-212`).

The block vocabulary has 59 entries, 58 distinct writer rows, 14 doors, 11 open doors, 83 uses by name at declared doors, and 56 literal sites at declared doors. The wait vocabulary has 24 entries, 27 distinct writer rows, 9 doors, 10 open doors, 36 uses by name at declared doors, and 57 literal sites at declared doors. The not-a-reason list has six entries.

`Contract` gains `Reasons` at `contract.go:11-16`; `ReasonsContract` declares `block`, `wait`, and `not_reasons` at `contract.go:28-33`; and `Build` fills it at `contract.go:35-44`. The generated JSON has `reasons` at `pi/test/state-contract.json:405`, `block` at `:406`, `wait` at `:1073`, and `not_reasons` at `:1403`. The schema remains `amq-squad-state-contract/1` (`contract.go:38`).

The list is not every value a task can show: both vocabularies are open, and the data names the doors.

`reasons.go` is the lead's measurement, landed byte for byte, and `sites.md` is its evidence. `sites.md` sections 1 and 2 list the block and wait rows and sites; section 3 lists marks; section 4 lists refusal codes; section 5 lists literal-only values; section 6 lists open doors; section 7 lists the door sets; section 8 lists equal strings with distinct constants; and section 9 lists empty-string sites.

## What the test checks

`TestReasonsAreTiedToTheirWriters` is at `reasons_test.go:47-59`.

The survey (`reasons_test.go:61-143`) reuses the module walk and parser from `statewrite_test.go`, applies this host's build constraints, reads no test file, type-checks each resulting package with one source importer, reports type errors without stopping, records every declared function and method, and collects module string constants and string literals. Package, function, method, struct, and constant names come from `go/types`; receiver aliases are resolved and one receiver pointer is retained in the method spelling (`reasons_test.go:162-227`). A use's expression widens over its selector, parentheses, and one-argument conversions (`reasons_test.go:172-193`); its function is the containing declaration, including uses in its function literals (`:195-204`).

The five door forms are:

1. A declared function or method call parameter, including method-expression receiver adjustment and variadic folding (`reasons_test.go:245-305`).
2. A named struct field in a keyed or unkeyed literal, including an omitted pointer literal type, or the selected field on an equal-width assignment, with promoted fields attributed to their declaring struct (`reasons_test.go:307-386`).
3. A local identifier on an equal-width assignment or local `var` declaration (`reasons_test.go:388-421`).
4. A result expression returned directly from its function declaration, excluding returns from a function literal (`reasons_test.go:423-449`).
5. An array or slice literal element, excluding struct and map literals (`reasons_test.go:451-471`).

The diagnostics are character-for-character contracts:

| Check | Message | Match | Code |
|---|---|---|---|
| M1 | `not-a-reason list: <constant> is declared twice` | A repeated not-a-reason constant. | `reasons_test.go:473-485` |
| M2 | `not-a-reason list: <constant> carries a mark or a writer` | A not-a-reason entry with either. | `reasons_test.go:473-485` |
| M3 | `<list> list: door <door> is declared twice` | A repeated door. | `reasons_test.go:488-498` |
| M4 | `<list> list: the list is not declared open with its open doors` | `Open` is false or there are no open doors. | `reasons_test.go:499-501` |
| M5 | `<list> list: the open door "<name>" is declared twice` | A repeated open-door name. | `reasons_test.go:502-507` |
| M6 | `<list> list: the open door "<name>" names no function` | An open door has no functions. | `reasons_test.go:508-510` |
| M7 | `<list> list: the open door "<name>" names <function>, and no such function is declared` | A named function is absent. | `reasons_test.go:511-515` |
| M8 | `<list> list: <constant> is declared twice` | A repeated list constant. | `reasons_test.go:519-528` |
| M9 | `<list> list: <constant> is also on the not-a-reason list` | A vocabulary overlaps the refusal list. | `reasons_test.go:529-531` |
| M10 | `<list> list: <constant> carries the mark "<mark>", which is not a declared mark` | A mark is neither empty, `no writer`, nor a guarded mark. | `reasons_test.go:532-535` |
| M11 | `<list> list: <constant> is marked guarded at <function>, and no such function is declared` | A guarded mark names no declaration. | `reasons_test.go:536-541` |
| M12 | `<list> list: <constant> declares no writer and does not carry the no writer mark` | An unmarked entry has no writer. | `reasons_test.go:542-544` |
| M13 | `<list> list: <constant> carries the no writer mark and declares a writer` | A `no writer` entry has a writer. | `reasons_test.go:545-547` |
| M14 | `<list> list: <constant> declares the writer <function> at <door>, a door outside the declared door set` | A writer names an undeclared door. | `reasons_test.go:548-552` |
| M15 | `<list> list: <constant> declares the writer <function> at <door> twice` | A repeated constant/function/door row. | `reasons_test.go:553-558` |
| M16 | `<site>: <constant> is used at the <list> door <door> in <function>, and the list does not declare that writer` | A named constant stands at a declared door without a declared row. | `reasons_test.go:563-579` |
| M17 | `<site>: <constant> has the value "<value>" and the <list> list declares "<code>"` | A counted use's value differs from its entry's code. | `reasons_test.go:580-585` |
| M18 | `<list> list: <constant> declares the writer <function> at <door>, and no use by name is there` | A distinct declared row has no counted use. | `reasons_test.go:587-591` |
| M19 | `<list> list: the door <door> has no listed use` | A distinct declared door has no counted use. | `reasons_test.go:592-596` |
| M20 | `<site>: a reason's Code is the string literal "<value>", never a constant's name` | A literal is used for `ReasonEntry.Code`. | `reasons_test.go:600-612` |
| M21 | `the lists hold <entries> entries and <n> of their Code fields are a constant used by name` | The typed `Code` use count differs from the entry count. | `reasons_test.go:613-615` |
| M22 | `reasons.go:<line>: Code is <code> and Constant is "<constant>"` | The exact source qualifier and constant string differ. | `reasons_test.go:617-674` |
| M23 | `the lists hold <entries> entries and reasons.go holds <n> literals with a Code or a Constant` | The keyed source-literal count differs from the entry count. | `reasons_test.go:675-677` |

Duplicates are diagnosed but counted distinctly for rows, doors, and open-door names. A use counts only when its constant/function/door row is declared. The test logs, on pass or fail, one line per vocabulary containing entry, distinct-writer, distinct-door, distinct-open-door, counted-use, and literal-at-door counts, followed by the typed-package and total-entry counts (`reasons_test.go:55-58`, `:488-598`).

## The marks

Fourteen block entries and four wait entries carry `no writer`. `sites.md` section 3a records every non-test Go line naming each. A use by name of a marked constant at a door of its list is refused by M16, while the test does not find a constant that is missing from the lists.

Three constants carry `traced, guarded at <function>`:

- `scheduler.GoneProcessExited` is written into a `SeatGone` event at `internal/scheduler/launchfail.go:156-158`. `scheduler.(*Scheduler).applyEvents` removes the seat and uses the code only in the `seat_gone` decision at `internal/scheduler/scheduler.go:620-652`; it does not pass it into either task field.
- `seatcommit.ReasonPRPending` is written only after `defer_` has set `Deferred` (`internal/seatcommit/commit.go:134-137`, `:1481-1484`, `:1517-1520`). The scheduler returns while a deferred count remains below its limit, or replaces the whole result with `ReasonCommitFailed` or `ReasonVerifierInterrupted`, before applying it (`internal/scheduler/scheduler.go:756-784`).
- `seatcommit.ReasonStepCancelled` is likewise written only on deferred results (`internal/seatcommit/commit.go:408-411`, `internal/seatcommit/verifier.go:228-234`) and is stopped by the same scheduler branch (`internal/scheduler/scheduler.go:756-784`).

The four guarded launch literals are:

- `pending`: every seat starts pending at `internal/seatlaunch/run.go:468-474`, but `seatlaunch.WaitSeats` rewrites every remaining pending state to `not_started` with `no_proof` before returning (`:448-463`).
- `up`: the state is set only together with `Up` at `internal/seatlaunch/run.go:468-474`, and `seatlaunch.(*Launcher).settleOne` settles an up seat as up or closing, never as a failure (`internal/seatlaunch/launcher.go:190-194`).
- `prepared`: `seatlaunch.Launch` sets it only in the dry-run arm (`internal/seatlaunch/run.go:268-273`); the two non-test `seatlaunch.Options` literals omit `DryRun` (`internal/cli/scheduler_run.go:298`, `internal/cli/fleet_adapters.go:174`).
- `applied`: `seatlaunch.launchFailure` returns the roster stage only when it is not applied (`internal/seatlaunch/launcher.go:227-238`).

`task.block` rejects a reason that is empty after trimming and stores only the trimmed non-empty reason (`internal/task/lease.go:808-820`). `Store.SetWait` has no equivalent non-empty check; the empty string is the value of a task that is not waiting (`internal/task/lease.go:1034-1047`). `sites.md` section 9 is the lead's measurement of all empty-string sites found by the trace.

## Stated limits

- The test does not follow a constant through a local (L1), a method value (L2), a wrapper function (L3), a concatenation (L5), or an interface method (L6). The lead's gate trace follows those flows; no test rebuilds it.
- A file excluded by this host's build constraints is not read (L4), so the test ties the sites of the host on which it runs. `internal/seatexec/seatexec_unix.go` contains writer sites listed in `sites.md` sections 1 and 2 and would be excluded on Windows. CI runs on macOS and Linux. The host values are recorded under Verification.
- A test file is not read (L7).
- A string literal at a door is counted but not refused (L9). On the clean tree the block and wait literal-at-door counts are 56 and 57; run P raises them to 59 and 58.
- A constant elsewhere in a door's function is not at the door (C2). A comparison, `case`, map key, or argument to a different function is not a door use (C1).
- The lists remain open: block has 11 named open doors and wait has 10. `sites.md` section 6 records their lines.
- M22 compares the qualifier exactly as `reasons.go` writes it. Renaming an import there would over-refuse every entry from that package even if the typed code remained correct; the file currently aliases no import.
- As `corrections-to-s1-2a.md` states for S1-2a's tests, a build overlay is not read. That limit applies here too.

## Named for later, not done here

For S2:

- The launch family: 58 literal values at 93 sites through `scheduler.(LaunchFailure).Code`.
- The two router literals `class_unknown` and `retries_exhausted`.
- A test that finds a reason constant missing from the lists; until then, `no writer` entries are a hand list.

For S4, the equal-string constant pairs in `sites.md` section 8 remain separate entries.

## Rows


### Row 0

```console
$ bash $IN/rows.sh 0
$ git status --porcelain --untracked-files=all | wc -l
7
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (13.39s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	13.836s
exit 0
```

### Row F

```console
$ bash $IN/rows.sh F
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
$ git status --porcelain --untracked-files=all | wc -l
24
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: internal/routing/zz_p_f10.go:10: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF10, and the list does not declare that writer
    reasons_test.go:51: internal/routing/zz_p_f14.go:12: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF14, and the list does not declare that writer
    reasons_test.go:51: internal/routing/zz_p_f16.go:9: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF16, and the list does not declare that writer
    reasons_test.go:51: internal/routing/zz_p_f7.go:8: task.BlockVerdictFailed is used at the block door field scheduler.Route.Block in routing.zzF7, and the list does not declare that writer
    reasons_test.go:51: internal/scheduler/zz_p_f3.go:10: task.ReasonWidenRequested is used at the block door param task.(Store).Block#2 in scheduler.zzF3, and the list does not declare that writer
    reasons_test.go:51: internal/scheduler/zz_p_f4.go:10: task.RefusalFileLock is used at the block door param task.(Store).Block#2 in scheduler.zzF4, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/zz_p_f1.go:5: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF1, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/zz_p_f11.go:6: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF11, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/zz_p_f12.go:5: seatcommit.ReasonNested is used at the block door field seatcommit.Result.ReasonCode in seatcommit.zzF12, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/zz_p_f13.go:5: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF13, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/zz_p_f5.go:5: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF5, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/zz_p_f6.go:5: seatcommit.ReasonNested is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.zzF6, and the list does not declare that writer
    reasons_test.go:51: internal/seatexec/zz_p_f8.go:5: claudeseat.TokenInsecure is used at the block door param seatexec.refusef#0 in seatexec.zzF8, and the list does not declare that writer
    reasons_test.go:52: internal/routing/zz_p_f17.go:9: task.WaitNoRoute is used at the wait door field scheduler.Route.Wait in routing.zzF17, and the list does not declare that writer
    reasons_test.go:52: internal/routing/zz_p_f9.go:8: task.WaitNoRoute is used at the wait door field scheduler.Route.Wait in routing.zzF9, and the list does not declare that writer
    reasons_test.go:52: internal/scheduler/zz_p_f15.go:10: task.WaitFileLock is used at the wait door param scheduler.(*Scheduler).wait#1 in scheduler.zzF15, and the list does not declare that writer
    reasons_test.go:52: internal/scheduler/zz_p_f2.go:11: scheduler.zzF2 is used at the wait door param scheduler.(*Scheduler).wait#1 in scheduler.zzF2f, and the list does not declare that writer
    reasons_test.go:52: internal/seatexec/zz_p_f8.go:5: claudeseat.TokenInsecure is used at the wait door param seatexec.refusef#0 in seatexec.zzF8, and the list does not declare that writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=58
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.35s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.732s
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
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row P

```console
$ bash $IN/rows.sh P
in C1 internal/seatcommit/zz_p_c1.go
in C2 internal/seatexec/zz_p_c2.go
in C3 internal/scheduler/zz_p_c3.go
in L1 internal/seatcommit/zz_p_l1.go
in L2 internal/seatcommit/zz_p_l2.go
in L3 internal/seatcommit/zz_p_l3.go
in L4 internal/seatcommit/zz_p_l4_windows.go
in L5 internal/seatcommit/zz_p_l5.go
in L6 internal/seatcommit/zz_p_l6.go
in L7 internal/seatcommit/zz_p_l7_test.go
in L9 internal/seatcommit/zz_p_l9.go
$ git status --porcelain --untracked-files=all | wc -l
18
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=59
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=58
    reasons_test.go:58: packages=62 entries=89
--- PASS: TestReasonsAreTiedToTheirWriters (13.29s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	13.661s
exit 0
out C1
out C2
out C3
out L1
out L2
out L3
out L4
out L5
out L6
out L7
out L9
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D1

```console
$ bash $IN/rows.sh D1
in D1 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: internal/routing/router.go:521: task.ReasonGrantsInvalid is used at the block door field scheduler.Route.Block in routing.(*Router).decide, and the list does not declare that writer
    reasons_test.go:51: internal/scheduler/scheduler.go:964: task.ReasonGrantsInvalid is used at the block door param task.(Store).Block#2 in scheduler.(*Scheduler).dispatch, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/commit.go:247: seatcommit.ReasonGrants is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.run, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/verifier.go:140: seatcommit.ReasonGrants is used at the block door result seatcommit.planVerifier#1 in seatcommit.planVerifier, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/verifier.go:144: seatcommit.ReasonGrants is used at the block door result seatcommit.planVerifier#1 in seatcommit.planVerifier, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/verifier.go:147: seatcommit.ReasonGrants is used at the block door result seatcommit.planVerifier#1 in seatcommit.planVerifier, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/verifier.go:152: seatcommit.ReasonGrants is used at the block door result seatcommit.planVerifier#1 in seatcommit.planVerifier, and the list does not declare that writer
    reasons_test.go:51: block list: task.ReasonGrantsInvalid declares the writer seatcommit.run at param seatcommit.(Result).refuse#0, and no use by name is there
    reasons_test.go:51: block list: task.ReasonGrantsInvalid declares the writer seatcommit.planVerifier at result seatcommit.planVerifier#1, and no use by name is there
    reasons_test.go:51: block list: seatcommit.ReasonGrants declares the writer routing.(*Router).decide at field scheduler.Route.Block, and no use by name is there
    reasons_test.go:51: block list: seatcommit.ReasonGrants declares the writer scheduler.(*Scheduler).dispatch at param task.(Store).Block#2, and no use by name is there
    reasons_test.go:671: reasons.go:96: Code is seatcommit.ReasonGrants and Constant is "task.ReasonGrantsInvalid"
    reasons_test.go:671: reasons.go:125: Code is task.ReasonGrantsInvalid and Constant is "seatcommit.ReasonGrants"
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=76 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.30s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.666s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D1
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D2

```console
$ bash $IN/rows.sh D2
in D2 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
--- PASS: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:54: internal/statecontract/reasons.go:101: a reason's Code is the string literal "nested_repository", never a constant's name
    reasons_test.go:54: the lists hold 89 entries and 88 of their Code fields are a constant used by name
    reasons_test.go:671: reasons.go:101: Code is (not a qualified name) and Constant is "seatcommit.ReasonNested"
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.37s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.636s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D2
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D3

```console
$ bash $IN/rows.sh D3
in D3 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: internal/seatcommit/commit.go:150: seatcommit.ReasonWorktree is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.Run, and the list does not declare that writer
    reasons_test.go:51: internal/seatcommit/commit.go:155: seatcommit.ReasonWorktree is used at the block door param seatcommit.(Result).refuse#0 in seatcommit.Run, and the list does not declare that writer
    reasons_test.go:56: block: entries=59 writers=57 doors=14 open_doors=11 uses_at_doors=81 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.44s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.819s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D3
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D4

```console
$ bash $IN/rows.sh D4
in D4 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: seatcommit.ReasonNested declares the writer seatcommit.Run at param seatcommit.(Result).refuse#0, and no use by name is there
    reasons_test.go:56: block: entries=59 writers=59 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.35s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.726s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D4
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D5

```console
$ bash $IN/rows.sh D5
in D5 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: seatcommit.ReasonLinkTarget declares the writer seatcommit.check at field seatcommit.Violation.Reason, a door outside the declared door set
    reasons_test.go:51: block list: seatcommit.ReasonNested declares the writer seatcommit.check at field seatcommit.Violation.Reason, a door outside the declared door set
    reasons_test.go:51: block list: seatcommit.ReasonOutside declares the writer seatcommit.check at field seatcommit.Violation.Reason, a door outside the declared door set
    reasons_test.go:51: block list: seatcommit.ReasonProtected declares the writer seatcommit.check at field seatcommit.Violation.Reason, a door outside the declared door set
    reasons_test.go:51: block list: seatcommit.ReasonSizeCap declares the writer seatcommit.check at field seatcommit.Violation.Reason, a door outside the declared door set
    reasons_test.go:51: block list: seatcommit.ReasonLinkTarget declares the writer seatcommit.check at field seatcommit.Violation.Reason, and no use by name is there
    reasons_test.go:51: block list: seatcommit.ReasonNested declares the writer seatcommit.check at field seatcommit.Violation.Reason, and no use by name is there
    reasons_test.go:51: block list: seatcommit.ReasonOutside declares the writer seatcommit.check at field seatcommit.Violation.Reason, and no use by name is there
    reasons_test.go:51: block list: seatcommit.ReasonProtected declares the writer seatcommit.check at field seatcommit.Violation.Reason, and no use by name is there
    reasons_test.go:51: block list: seatcommit.ReasonSizeCap declares the writer seatcommit.check at field seatcommit.Violation.Reason, and no use by name is there
    reasons_test.go:56: block: entries=59 writers=58 doors=13 open_doors=11 uses_at_doors=78 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.40s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.771s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D5
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D6

```console
$ bash $IN/rows.sh D6
in D6 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: the door field seatcommit.Violation.Detail has no listed use
    reasons_test.go:56: block: entries=59 writers=58 doors=15 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.36s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.725s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D6
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D7

```console
$ bash $IN/rows.sh D7
in D7 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: task.ReasonWidenRequested declares no writer and does not carry the no writer mark
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.35s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.748s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D7
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D8

```console
$ bash $IN/rows.sh D8
in D8 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: seatcommit.ReasonNested carries the no writer mark and declares a writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.34s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.728s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D8
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D9

```console
$ bash $IN/rows.sh D9
in D9 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:52: wait list: task.WaitNoRoute is also on the not-a-reason list
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=90
--- FAIL: TestReasonsAreTiedToTheirWriters (13.37s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.762s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D9
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D10

```console
$ bash $IN/rows.sh D10
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
7
```

### Row D12

```console
$ bash $IN/rows.sh D12
in D12 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:50: not-a-reason list: task.RefusalFileLock is declared twice
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=90
--- FAIL: TestReasonsAreTiedToTheirWriters (13.32s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.699s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D12
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D13

```console
$ bash $IN/rows.sh D13
in D13 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: task.ReasonWidenRequested carries the mark "no writers", which is not a declared mark
    reasons_test.go:51: block list: task.ReasonWidenRequested declares no writer and does not carry the no writer mark
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.34s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.731s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D13
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D14

```console
$ bash $IN/rows.sh D14
in D14 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: the open door "the --reason flag of task block" names cli.runTaskTransitionX, and no such function is declared
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.33s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.713s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D14
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D15

```console
$ bash $IN/rows.sh D15
in D15 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: the open door "the --reason flag of task block" is declared twice
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.34s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.739s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D15
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D16

```console
$ bash $IN/rows.sh D16
in D16 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: the list is not declared open with its open doors
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.37s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.761s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D16
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D17

```console
$ bash $IN/rows.sh D17
in D17 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: scheduler.GoneProcessExited is marked guarded at scheduler.(*Scheduler).applyEvent, and no such function is declared
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.30s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.673s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D17
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D18

```console
$ bash $IN/rows.sh D18
in D18 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: task.ReasonWidenRequested is declared twice
    reasons_test.go:56: block: entries=60 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=90
--- FAIL: TestReasonsAreTiedToTheirWriters (13.48s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.863s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D18
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D19

```console
$ bash $IN/rows.sh D19
in D19 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:50: not-a-reason list: task.RefusalFileLock carries a mark or a writer
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.92s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.317s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D19
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D20

```console
$ bash $IN/rows.sh D20
in D20 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: door field seatcommit.Violation.Reason is declared twice
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.49s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.877s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D20
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D21

```console
$ bash $IN/rows.sh D21
in D21 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: the open door "the --reason flag of task block" names no function
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.35s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.734s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D21
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D22

```console
$ bash $IN/rows.sh D22
in D22 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: seatcommit.ReasonNested declares the writer seatcommit.check at field seatcommit.Violation.Reason twice
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.39s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.765s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D22
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D23

```console
$ bash $IN/rows.sh D23
in D23 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: internal/seatcommit/commit.go:1159: seatcommit.ReasonOutside has the value "outside_declared_files" and the block list declares "nested_repository"
    reasons_test.go:51: internal/seatcommit/commit.go:1162: seatcommit.ReasonNested has the value "nested_repository" and the block list declares "outside_declared_files"
    reasons_test.go:671: reasons.go:101: Code is seatcommit.ReasonNested and Constant is "seatcommit.ReasonOutside"
    reasons_test.go:671: reasons.go:103: Code is seatcommit.ReasonOutside and Constant is "seatcommit.ReasonNested"
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.40s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.787s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D23
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D24

```console
$ bash $IN/rows.sh D24
in D24 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
--- PASS: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:54: the lists hold 89 entries and reasons.go holds 88 literals with a Code or a Constant
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.36s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.739s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D24
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row D25

```console
$ bash $IN/rows.sh D25
in D25 internal/statecontract/reasons.go
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters|TestContractFileIsCurrent' -count=1 -v -timeout 120s
=== RUN   TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
--- FAIL: TestContractFileIsCurrent (0.00s)
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:54: the lists hold 89 entries and 88 of their Code fields are a constant used by name
    reasons_test.go:671: reasons.go:128: Code is  and Constant is "task.ReasonWidenRequested"
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=83 literal_sites_at_doors=56
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.38s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.766s
FAIL
exit 1
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go
exit 0
out D25
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row E1

```console
$ bash $IN/rows.sh E1
in E1 internal/registry/pins.go
$ git status --porcelain --untracked-files=all | wc -l
8
$ go build ./...
exit 0
$ go test ./internal/statecontract/ -run 'TestReasonsAreTiedToTheirWriters' -count=1 -v -timeout 120s
=== RUN   TestReasonsAreTiedToTheirWriters
    reasons_test.go:51: block list: registry.PinMissing declares the writer registry.OpenPinned at result registry.OpenPinned#1, and no use by name is there
    reasons_test.go:56: block: entries=59 writers=58 doors=14 open_doors=11 uses_at_doors=82 literal_sites_at_doors=57
    reasons_test.go:57: wait: entries=24 writers=27 doors=9 open_doors=10 uses_at_doors=36 literal_sites_at_doors=57
    reasons_test.go:58: packages=62 entries=89
--- FAIL: TestReasonsAreTiedToTheirWriters (13.37s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.755s
FAIL
exit 1
$ git diff --quiet -- internal/registry/pins.go
exit 0
out E1
$ git status --porcelain --untracked-files=all | wc -l
7
```

### Row E2

```console
$ bash $IN/rows.sh E2
in E2 internal/seatcommit/policy.go
$ git status --porcelain --untracked-files=all | wc -l
8
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
--- PASS: TestReasonsAreTiedToTheirWriters (13.37s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.757s
FAIL
exit 1
$ git diff --quiet -- internal/seatcommit/policy.go
exit 0
out E2
$ git status --porcelain --untracked-files=all | wc -l
7
```

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
--- PASS: TestReasonsAreTiedToTheirWriters (13.30s)
=== RUN   TestOnlyTheListedFilesImportUnsafeOrC
    statewrite_test.go:72: directories=62 files=421
--- PASS: TestOnlyTheListedFilesImportUnsafeOrC (0.02s)
=== RUN   TestNoPackageOutsideTaskWritesTaskState
    statewrite_test.go:227: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
--- PASS: TestNoPackageOutsideTaskWritesTaskState (13.22s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	26.986s
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
 M internal/statecontract/contract.go
 M pi/test/state-contract.json
?? docs/v3-spike/330-s1-2b-reasons/README.md
?? docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md
?? docs/v3-spike/330-s1-2b-reasons/sites.md
?? internal/statecontract/reasons.go
?? internal/statecontract/reasons_test.go
exit 0
```

### `git diff --stat`

```console
$ git diff --stat
 internal/statecontract/contract.go |   13 +-
 pi/test/state-contract.json        | 1025 ++++++++++++++++++++++++++++++++++++
 2 files changed, 1036 insertions(+), 2 deletions(-)
exit 0
```

### `cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go`

```console
$ cmp $IN/tree/internal/statecontract/reasons.go internal/statecontract/reasons.go

exit 0
```

### `cmp $IN/tree/docs/v3-spike/330-s1-2b-reasons/sites.md docs/v3-spike/330-s1-2b-reasons/sites.md`

```console
$ cmp $IN/tree/docs/v3-spike/330-s1-2b-reasons/sites.md docs/v3-spike/330-s1-2b-reasons/sites.md

exit 0
```

### `cmp $IN/tree/docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md`

```console
$ cmp $IN/tree/docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md

exit 0
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

### `git diff --check`

```console
$ git diff --check

exit 0
```

### `sed -n '49,143p' internal/statecontract/reasons.go | grep -c 'Mark: "no writer"'`

```console
$ sed -n '49,143p' internal/statecontract/reasons.go | grep -c 'Mark: "no writer"'
14
exit 0
```

### `sed -n '146,200p' internal/statecontract/reasons.go | grep -c 'Mark: "no writer"'`

```console
$ sed -n '146,200p' internal/statecontract/reasons.go | grep -c 'Mark: "no writer"'
4
exit 0
```

### `awk '/^### 5a/{on=1;next} /^### 5b/{on=0} on && /^\\| `/{values++; sites+=gsub(/internal\\//,"&")} END{printf "values=%d sites=%d\\n", values, sites}' docs/v3-spike/330-s1-2b-reasons/sites.md`

```console
$ awk '/^### 5a/{on=1;next} /^### 5b/{on=0} on && /^\\| `/{values++; sites+=gsub(/internal\\//,"&")} END{printf "values=%d sites=%d\\n", values, sites}' docs/v3-spike/330-s1-2b-reasons/sites.md
values=58 sites=93
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
