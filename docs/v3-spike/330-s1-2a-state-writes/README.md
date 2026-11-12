# 3.3.0 S1-2a state writes

This PR adds `internal/statecontract/statewrite_test.go` with two tests, this note, and `corrections-to-s1-1.md`. It changes no product file and no existing test: `git status --porcelain --untracked-files=all` in Verification lists exactly those three new paths. The base is `f51bc07582dd035899baf429939ed94c30548107`. This is round 2 of S1-2a: it carries #979 head `e58e1da54459718f95a1aebc8ab17e65bc1a4290` and replaces #979.

## Inputs

`(cd $IN && shasum -a 256 MANIFEST.sha256)` exited 0:

```text
2c0cc17e35cfd4cfa7c2b849114c8c4fba49491248b73697ac253fcc22f6808b  MANIFEST.sha256
```

`(cd $IN && shasum -a 256 -c MANIFEST.sha256)` exited 0:

```text
probes/B1a.go: OK
probes/B1b.go: OK
probes/C1.go: OK
probes/C10.go: OK
probes/C11.go: OK
probes/C12.go: OK
probes/C2.go: OK
probes/C3.go: OK
probes/C4.go: OK
probes/C5.go: OK
probes/C6.go: OK
probes/C7.go: OK
probes/C8.go: OK
probes/C9.go: OK
probes/E1.go: OK
probes/E2.go: OK
probes/L1.go: OK
probes/L2.go: OK
probes/L3.go: OK
probes/L4.go: OK
probes/T1.go: OK
probes/T10.go: OK
probes/T2.go: OK
probes/T3.go: OK
probes/T4.go: OK
probes/T5.go: OK
probes/T6.go: OK
probes/T7.go: OK
probes/T8.go: OK
probes/T9.go: OK
probes/TARGETS.tsv: OK
probes/U1.go: OK
probes/U2.go: OK
probes/U3.go: OK
probes/U4.go: OK
probes/U5.go: OK
probes/U6.go: OK
probes/W1.go: OK
probes/W11.go: OK
probes/W12.go: OK
probes/W1b.go: OK
probes/W1c.go: OK
probes/W1d.go: OK
probes/W2.go: OK
probes/W3.go: OK
probes/W3b.go: OK
probes/W4.go: OK
probes/W4b.go: OK
probes/W4c.go: OK
probes/W5.go: OK
probes/W6.go: OK
probes/W6b.go: OK
probes/W6c.go: OK
probes/W6d.go: OK
probes/W6e.go: OK
probes/W6f.go: OK
probes/W7.go: OK
probes/W7b.go: OK
probes/W7c.go: OK
probes/W7d.go: OK
probes/W8.go: OK
probes/W9.go: OK
probes/X1.go: OK
probes/X2.go: OK
probes/X3.go: OK
probes/X4.go: OK
tree/docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md: OK
```

`(cd $IN2 && shasum -a 256 MANIFEST.sha256)` exited 0:

```text
bf9565a2b73ac8cc9eb4329656e814e662f604675419264af7e3f8cbfbc93136  MANIFEST.sha256
```

`(cd $IN2 && shasum -a 256 -c MANIFEST.sha256)` exited 0:

```text
carry/blobs-after.txt: OK
carry/t173.patch: OK
rows/G1/tree/internal/routing/go.mod/keep.txt: OK
rows/G1/tree/internal/routing/zz_r2_g1.go: OK
rows/M1/gomod.append: OK
rows/M1/tree/internal/scheduler/zz_r2_m1.go: OK
rows/M1/tree/spike/zzr2m1/go.mod: OK
rows/M1/tree/spike/zzr2m1/w/w.go: OK
rows/M3/gomod.append: OK
rows/M3/tree/internal/scheduler/zz_r2_m3.go: OK
rows/M3/tree/spike/zzr2d/d/d.go: OK
rows/M3/tree/spike/zzr2d/go.mod: OK
rows/M3/tree/spike/zzr2m1/go.mod: OK
rows/M3/tree/spike/zzr2m1/w/w.go: OK
rows/N1/tree/internal/scheduler/testdata/zzr2n1/w.go: OK
rows/N1/tree/internal/scheduler/zz_r2_n1.go: OK
rows/N2/tree/internal/scheduler/_zzr2/zzr2n2/w.go: OK
rows/N2/tree/internal/scheduler/zz_r2_n2.go: OK
rows/N3/tree/internal/scheduler/.zzr2/zzr2n3/w.go: OK
rows/N3/tree/internal/scheduler/zz_r2_n3.go: OK
rows/N4/tree/internal/scheduler/node_modules/zzr2n4/w.go: OK
rows/N4/tree/internal/scheduler/zz_r2_n4.go: OK
rows/N5/tree/internal/scheduler/testdata/zzr2n5/w.go: OK
rows/N6/tree/internal/scheduler/_zzr2/zzr2n6/w.go: OK
rows/N7/tree/internal/scheduler/.zzr2/zzr2n7/w.go: OK
rows/N8/tree/internal/scheduler/node_modules/zzr2n8/w.go: OK
rows/R1/tree/internal/scheduler/zz_r2_r1.go: OK
rows/R2/tree/internal/scheduler/zz_r2_r2.go: OK
rows/R3/tree/internal/scheduler/zz_r2_r3.go: OK
rows/S1/links.tsv: OK
rows/S1/tree/internal/scheduler/zz_r2_s1.go: OK
rows/S2/links.tsv: OK
rows/S3/links.tsv: OK
rows/S4/links.tsv: OK
rows/S6/links.tsv: OK
rows/S6/tree/internal/routing/zzr2s6/w.go: OK
rows/X5/tree/internal/routing/zz_r2_x5_windows.go: OK
rows/X6/tree/internal/routing/zz_r2_x6_windows.go: OK
rows2.sh: OK
targets/s1pkg/w.go: OK
targets/s2pkg/w.go: OK
targets/s3.go: OK
targets/s6.gomod: OK
```

`$IN` and `$IN2` are the brief's two input directories. Their values are not printed because this note holds no absolute user path.

`git apply - < $IN2/carry/t173.patch` exited 0 and printed five whitespace-warning lines; those carry-step lines are not reprinted here.

## What the two tests check

- The shared walk starts at the module root and judges every entry by five cases (`statewrite_test.go:230-319`). First, the root `.git` entry is passed over (`:246-251`). Second, a symbolic link is not followed: a directory target reports `a symbolic link to a directory: the walk does not follow it`; a Go-like name targeting a non-regular entry stops with `not a regular file and named like a Go file: the walk does not read it`; any other Go-like name reports `a symbolic link named like a Go file: the walk does not read it`; and `go.mod` reports `a symbolic link named go.mod: the walk does not read it` (`:252-269`). Third, a non-root directory is skipped as another module exactly when `os.Lstat` finds its `go.mod` is a regular file; every other directory is entered (`:270-282`). Fourth, a non-regular entry is passed over unless its name ends in `.go`, when the walk stops with the same non-regular-file message (`:283-292`). Fifth, a regular non-test `.go` file is read (`:293-307`). Walk and parse errors are fatal (`:309-332`). Directories and files are sorted (`:312-318`).
- `TestOnlyTheListedFilesImportUnsafeOrC` parses imports in each walked file. An `unsafe` or `C` import fails with `import of <path>: a write through it is not declared` unless the exact file/import pair is the package-level entry at `statewrite_test.go:24-26`; the import check and refusal are at `:39-63`. The listed entry also fails with `<file> is listed as importing <path> and does not` when its exact import is absent (`:64-67`). The directory floor and log are at `:69-72`.
- `TestNoPackageOutsideTaskWritesTaskState` imports `task.Task`, requires its underlying struct and `State` field (`statewrite_test.go:75-104`), skips `internal/task` (`:111-114`), and type-checks each host-included package with shared source importer data for types, definitions, and selections (`:115-147`). A type error is reported as `<directory>: <error>` at `:137-142` and scanning continues.
- After a type check returns a package, its import graph is followed once with a test-wide visited set. A package under this module path that is not a walked package path fails with `a package of this module's path that the walk did not read, imported by <importer>` (`statewrite_test.go:143-147,372-396`).
- A state selection must be a typed field selection named `State` whose selected field is `task.Task.State` or whose declaring struct is identical to the task struct with tags ignored (`statewrite_test.go:414-433`). Assignment sides fail as `a write of a task's state` (`:156-165`); range key or value selections use the same message (`:166-172`); and `&` fails as `the address of a task's state` (`:173-178`).
- A type declaration fails as `a named type over a task` when its alias-resolved underlying type is the task struct or a pointer, slice, array, or map whose element or value recursively meets that rule (`statewrite_test.go:179-182,512-537`). A one-argument type conversion fails as `a conversion of a task` when the types differ and one is a task type while the other is struct-like (`:183-193,466-510`). Struct-like means a type that, with aliases resolved and every pointer removed, is a type parameter or has a struct as its underlying type (`:487-510`).
- A non-empty composite literal of a type parameter after alias resolution and one pointer removal fails as `a literal of a type parameter` (`statewrite_test.go:194-202,539-551`). A non-empty task literal fails as `an unkeyed task literal` when its first element is unkeyed (`:203-208`), or once per `State` key as `a task literal that names its state` (`:210-217`). Empty literals pass these checks, and nested literals remain in the AST walk (`:194-222`).
- The three helpers that repeatedly remove pointer element types stop on a revisited alias-resolved type: no structure from `structureAfterPointers`, false from `isTaskType`, and false from `isStructLike` (`statewrite_test.go:435-455,466-485,487-510`). `isTypeOverTask` already has the same cycle guard (`:512-537`).
- A host-excluded file is parsed without types. Its five messages, in order, are `a file this host's build constraints exclude imports internal/task`, `a file this host's build constraints exclude imports <path>, a package of this module's path that the walk did not read`, `a file this host's build constraints exclude names a State field`, `a file this host's build constraints exclude names a State key`, and `a file this host's build constraints exclude declares a State field` (`statewrite_test.go:334-370`).
- The typed scan requires at least two packages and one state selection, then logs package, selection, whole-task-assignment, and excluded-file counts (`statewrite_test.go:224-227`).

## Build constraints

`TestOnlyTheListedFilesImportUnsafeOrC` reads every non-test `.go` file the walk finds and applies no build constraint: a file for another platform or behind a tag is read like any other.

`TestNoPackageOutsideTaskWritesTaskState` type-checks the files this host's build constraints include (`go/build`'s default context: the host's GOOS and GOARCH, cgo as the host has it, no extra tags) and reads the files they exclude by spelling.

The excluded-file messages are `a file this host's build constraints exclude imports internal/task` (`statewrite_test.go:342-344`), `a file this host's build constraints exclude imports <path>, a package of this module's path that the walk did not read` (`:345-347`), `a file this host's build constraints exclude names a State field` (`:351-354`), `a file this host's build constraints exclude names a State key` (`:355-358`), and `a file this host's build constraints exclude declares a State field` (`:359-366`). Run 0 logged 31 excluded files on this host; `go env GOOS GOARCH` printed `darwin` and `arm64` in Verification.

The two tests of `internal/task` apply no build constraint, and a per-platform pair of files in that package that declares the same name fails `TestEveryStoreMethodIsTiedToItsMoves` with the type checker's redeclaration error.

Run B1 records that redeclaration and the three passing companion checks.

## Stated limits

- A whole task assigned by an `=` left-hand side is counted and not refused (`statewrite_test.go:156-165`): Run 0 logged `whole_task_assignments=16`, and Run C's L1/L4 increased it to 18. The counter counts task-struct left-hand sides of `=` assignments regardless of their right-hand side; it does not count `:=`, copies performed by `copy` or `append`, returns, channel operations without such an assignment, or assignments in excluded files.
- Reflection is not modeled. `git grep -lE '^([[:space:]]*|import )([A-Za-z_.]+ )?"reflect"$' HEAD -- '*.go' ':!*_test.go' | wc -l` exited 0 and printed `8`; Run C's L2 passed.
- Decoding is not modeled. `git grep -lE '^([[:space:]]*|import )([A-Za-z_.]+ )?"encoding/json"$' HEAD -- '*.go' ':!*_test.go' | wc -l` exited 0 and printed `149`; Run C's L3 passed.
- `_test.go` files are outside both walks. `git ls-files '*_test.go' | wc -l` exited 0 and printed `617`; `git ls-files --others --exclude-standard '*_test.go' | wc -l` exited 0 and printed `1`; their sum is the files outside both walks. Run C's C8 passed.
- Another module is skipped only when `os.Lstat` finds its `go.mod` is a regular file (`statewrite_test.go:270-282`). `git ls-files | grep -E '(^|/)go\.mod$'` exited 0 and printed the root plus four nested module files; `git ls-files | grep -E '^(spike/embedpi|spike/gateverify|spike/piprobe/fixture|spike/seatexec)/.*\.go$' | grep -vcE '_test\.go$'` exited 0 and printed `13`. `go list -m all | wc -l` exited 0 and printed `39`, so 38 dependency modules are also not walked. The closer refuses a package of this module's path in another module when a type-checked package reaches it (M1 and M3, `statewrite_test.go:383-396`); a required nested module that nothing type-checked imports is not read.
- No directory is skipped for its name (`statewrite_test.go:270-282`). A non-test `.go` file under `testdata`, `node_modules`, `vendor`, or a dot- or underscore-prefixed directory is read like any other; Runs G/N1-N8 prove both imported and unimported forms. `git ls-files | grep -E '(^|/)(testdata|node_modules|vendor|[._][^/]*)/' | grep -E '\.go$' | grep -vcE '_test\.go$'` printed `0` and exited 1 on the clean tree.
- The root `.git` entry is passed over (`statewrite_test.go:246-251`). `test -f .git; echo $?` printed `0`. Git cannot track a path under `.git`, so no commit can carry one; the closer refuses a package there when a type-checked package imports it, while an unimported package there is read by neither test. Those two probes are the lead's at the gate.
- A symbolic link that is none of the four link cases is passed over (`statewrite_test.go:252-269`); `find . -type l | wc -l` printed `8`. A name ending in `_test.go` also ends in `.go`, so it is refused by the Go-like-name cases. A directory whose `go.mod` is a link is entered and the link refused, as Run G/S6 records.
- A non-regular entry named like a Go file, and a Go-named link to one, stop each test by name (`statewrite_test.go:258-260,283-292`); a test-file-like name is included. Other non-regular entries are passed over. The named-pipe and link-to-pipe probes are the lead's at the gate.
- Host-excluded files are read by spelling, so Run W's X2 refused a non-task `fmt.State`; the excluded-file pass cannot distinguish that selector from a task selector (`statewrite_test.go:349-369`). The excluded count is host-specific, and `internal/piruntime/embed_amqpi.go:1` requires the `amqpi` tag.
- The unsafe/C test refuses imports and does not inspect what the listed file does (`statewrite_test.go:39-73`). `git grep -lE '^([[:space:]]*|import )([A-Za-z_.]+ )?"unsafe"$' HEAD -- '*.go' ':!*_test.go' | wc -l` printed `1`; the same command for `"C"` printed `0`. The two existing `internal/task` tests still pass on the unsafe probes described in `corrections-to-s1-1.md`; the new import test refuses an unlisted unsafe import (`statewrite_test.go:53-60`). The printed `unsafe.Offsetof` search found 0 sites. `git ls-files | grep -E '\.(s|S|c|h|syso|cc|cpp|m)$' | wc -l` and `git grep -nE '^//(go:linkname|go:cgo_|export )' HEAD -- '*.go' | wc -l` each printed `0`; those forms are not scanned.
- A composite literal of a type parameter is refused without asking whether its constraint can be a task (`statewrite_test.go:194-202,539-551`); Run W's W8 can be a task.
- A non-task struct with `State` is not a task struct, and a wrapper struct, function, channel, or interface is not a type over a task (`statewrite_test.go:457-537`); Run C's C6 and C9-C12 passed, while Run W's W7 and W7d were refused at their promoted-field writes.
- `internal/task` is skipped at `statewrite_test.go:111-114`; Run B1 shows the two existing task tests' behavior for a per-platform pair.
- The test file's imports at `statewrite_test.go:3-18` are from the standard library. `grep -c 'x/tools' go.mod` printed `0` and exited 1. Hidden and underscore-prefixed directory names are entered under the measured walk; the clean-tree command above found 0 non-test tracked Go files under the named directory classes.

## Rows

### Run 0

`go test ./internal/statecontract/ -count=1 -v -timeout 120s` exited 0. The requested lines were:

```text
--- PASS: TestContractFileIsCurrent (0.00s)
--- PASS: TestCommittedContractIsSubstantial (0.00s)
    statewrite_test.go:72: directories=62 files=420
--- PASS: TestOnlyTheListedFilesImportUnsafeOrC (0.10s)
    statewrite_test.go:227: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
--- PASS: TestNoPackageOutsideTaskWritesTaskState (13.03s)
```

### Run W

The copy command was:

```sh
R=W
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match W; the landed-file check `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `42`. `go build ./internal/scheduler/` exited 0 with no output.

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1 -timeout 120s` exited 1 with this whole output:

```text
--- FAIL: TestNoPackageOutsideTaskWritesTaskState (13.11s)
    statewrite_test.go:123: internal/scheduler/zz_probe_x1_windows.go:3: a file this host's build constraints exclude imports internal/task
    statewrite_test.go:353: internal/scheduler/zz_probe_x1_windows.go:5: a file this host's build constraints exclude names a State field
    statewrite_test.go:353: internal/scheduler/zz_probe_x2_windows.go:5: a file this host's build constraints exclude names a State field
    statewrite_test.go:357: internal/scheduler/zz_probe_x3_windows.go:5: a file this host's build constraints exclude names a State key
    statewrite_test.go:363: internal/scheduler/zz_probe_x4_windows.go:3: a file this host's build constraints exclude declares a State field
    statewrite_test.go:181: internal/scheduler/zz_probe_t1.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_t10.go:9: a named type over a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_t2.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_t3.go:6: a named type over a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_t4.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_t5.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_t6.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_t7.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_t8.go:7: a named type over a task outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/zz_probe_w1.go:5: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/zz_probe_w11.go:47: a write of a task's state outside internal/task is not declared
    statewrite_test.go:216: internal/scheduler/zz_probe_w12.go:45: a task literal that names its state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/zz_probe_w1b.go:5: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/zz_probe_w1c.go:5: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/zz_probe_w1d.go:5: a write of a task's state outside internal/task is not declared
    statewrite_test.go:176: internal/scheduler/zz_probe_w2.go:5: the address of a task's state outside internal/task is not declared
    statewrite_test.go:170: internal/scheduler/zz_probe_w3.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/zz_probe_w3b.go:7: a write of a task's state outside internal/task is not declared
    statewrite_test.go:216: internal/scheduler/zz_probe_w4.go:5: a task literal that names its state outside internal/task is not declared
    statewrite_test.go:216: internal/scheduler/zz_probe_w4b.go:5: a task literal that names its state outside internal/task is not declared
    statewrite_test.go:216: internal/scheduler/zz_probe_w4c.go:5: a task literal that names its state outside internal/task is not declared
    statewrite_test.go:207: internal/scheduler/zz_probe_w5.go:9: an unkeyed task literal outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_w6.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:191: internal/scheduler/zz_probe_w6.go:7: a conversion of a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_w6b.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:191: internal/scheduler/zz_probe_w6b.go:7: a conversion of a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_w6c.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:191: internal/scheduler/zz_probe_w6c.go:7: a conversion of a task outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_w6d.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:191: internal/scheduler/zz_probe_w6d.go:7: a conversion of a task outside internal/task is not declared
    statewrite_test.go:191: internal/scheduler/zz_probe_w6e.go:9: a conversion of a task outside internal/task is not declared
    statewrite_test.go:191: internal/scheduler/zz_probe_w6f.go:46: a conversion of a task outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/zz_probe_w7.go:7: a write of a task's state outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_w7b.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/zz_probe_w7b.go:7: a write of a task's state outside internal/task is not declared
    statewrite_test.go:181: internal/scheduler/zz_probe_w7c.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:216: internal/scheduler/zz_probe_w7c.go:7: a task literal that names its state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/zz_probe_w7d.go:7: a write of a task's state outside internal/task is not declared
    statewrite_test.go:200: internal/scheduler/zz_probe_w8.go:5: a literal of a type parameter outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/zz_probe_w9.go:5: a write of a task's state outside internal/task is not declared
    statewrite_test.go:227: packages=61 uses_of_state=54 whole_task_assignments=16 files_skipped_by_build_constraint=36
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.364s
FAIL
```

T9 produced no line of its own: this host excluded its positional literal, while T2's named type refused the pair; T9 spells none of the four excluded-file forms.

The removal command was:

```sh
R=W
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match W. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run C

The copy command was:

```sh
R=C
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match C; `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `19`. `go build ./internal/scheduler/` exited 0 with no output.

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$|TestOnlyTheListedFilesImportUnsafeOrC$' -count=1 -v -timeout 120s` exited 0:

```text
=== RUN   TestOnlyTheListedFilesImportUnsafeOrC
    statewrite_test.go:72: directories=62 files=435
--- PASS: TestOnlyTheListedFilesImportUnsafeOrC (0.11s)
=== RUN   TestNoPackageOutsideTaskWritesTaskState
    statewrite_test.go:227: packages=61 uses_of_state=43 whole_task_assignments=18 files_skipped_by_build_constraint=32
--- PASS: TestNoPackageOutsideTaskWritesTaskState (13.11s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	13.466s
```

The copy changed Run 0 by files +15, packages +0, state uses +1, whole-task assignments +2, and excluded files +1.

The removal command was:

```sh
R=C
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match C. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run U

The copy command was:

```sh
R=U
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match U; `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `8`.

`go test ./internal/statecontract/ -run 'TestOnlyTheListedFilesImportUnsafeOrC$' -count=1 -timeout 120s` exited 1 with this whole output:

```text
--- FAIL: TestOnlyTheListedFilesImportUnsafeOrC (0.10s)
    statewrite_test.go:60: internal/scheduler/zz_probe_u1.go:3: import of unsafe: a write through it is not declared
    statewrite_test.go:60: internal/scheduler/zz_probe_u2.go:3: import of C: a write through it is not declared
    statewrite_test.go:60: internal/scheduler/zz_probe_u3.go:3: import of unsafe: a write through it is not declared
    statewrite_test.go:60: internal/scheduler/zz_probe_u4.go:3: import of unsafe: a write through it is not declared
    statewrite_test.go:60: internal/scheduler/zz_probe_u6_windows.go:3: import of unsafe: a write through it is not declared
    statewrite_test.go:72: directories=62 files=425
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	0.348s
FAIL
```

The removal command was:

```sh
R=U
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match U. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run E1

The copy command was:

```sh
R=E1
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match E1; `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `4`.

`go build ./internal/scheduler/` exited 1:

```text
# github.com/omriariav/amq-squad/v3/internal/scheduler
internal/scheduler/zz_probe_e1.go:3:19: cannot use "x" (untyped string constant) as int value in variable declaration
```

This prescribed command was run once while the E1 probe remained present:

```sh
out=$(go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1 -timeout 240s 2>&1); echo "exit=$?"; echo "$out" | grep -c 'statewrite_test.go:'; echo "$out" | grep -c 'could not import'; echo "$out" | grep -v 'could not import'
```

The command exited 0 and printed:

```text
exit=1
51
44
--- FAIL: TestNoPackageOutsideTaskWritesTaskState (78.70s)
    statewrite_test.go:140: internal/cli: internal/cli/operator.go:390:15: operator.Enabled undefined (type statusOperatorView has no field or method Enabled)
    statewrite_test.go:140: internal/cli: internal/cli/operator.go:390:53: operator.Handle undefined (type statusOperatorView has no field or method Handle)
    statewrite_test.go:140: internal/cli: internal/cli/operator.go:393:54: operator.Handle undefined (type statusOperatorView has no field or method Handle)
    statewrite_test.go:140: internal/cli: internal/cli/team.go:397:58: not enough arguments in call to wrap
        	have (unknown type)
        	want (unknown type, error)
    statewrite_test.go:140: internal/schedproc: internal/schedproc/decisions.go:93:74: d.Kind undefined (type DecisionLine has no field or method Kind)
    statewrite_test.go:140: internal/scheduler: internal/scheduler/zz_probe_e1.go:3:19: cannot use "x" (untyped string constant) as int value in variable declaration
    statewrite_test.go:227: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	78.924s
FAIL
```

The `grep -v` cut 44 `could not import` lines because they contained the worktree's absolute path; no other line was cut. Both counts match the lead's run.

`git diff --no-index --check /dev/null docs/v3-spike/330-s1-2a-state-writes/README.md` exited 3:

```text
docs/v3-spike/330-s1-2a-state-writes/README.md:383: space before tab in indent.
+        	have (unknown type)
docs/v3-spike/330-s1-2a-state-writes/README.md:384: space before tab in indent.
+        	want (unknown type, error)
```

Those two lines are Go's output kept as given; they are the note's only two lines named by the check.

The removal command was:

```sh
R=E1
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match E1. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run E2

The copy command was:

```sh
R=E2
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match E2; `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `4`.

`go build ./internal/scheduler/` exited 1:

```text
# github.com/omriariav/amq-squad/v3/internal/scheduler
internal/scheduler/zz_probe_e2.go:4:14: cannot use "x" (untyped string constant) as int value in variable declaration
```

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1 -timeout 120s` exited 1 with this whole output:

```text
--- FAIL: TestNoPackageOutsideTaskWritesTaskState (13.86s)
    statewrite_test.go:140: internal/scheduler: internal/scheduler/zz_probe_e2.go:4:14: cannot use "x" (untyped string constant) as int value in variable declaration
    statewrite_test.go:227: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.389s
FAIL
```

The removal command was:

```sh
R=E2
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match E2. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run B1

The copy command was:

```sh
R=B1
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match B1; `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `5`.

`go build ./internal/task/` exited 0 with no output.

`go test ./internal/task/ -run 'TestEveryStoreMethodIsTiedToItsMoves$' -count=1 -timeout 120s` exited 1. Its first `contract_test.go:` line was:

```text
    contract_test.go:201: zz_probe_linux.go:3:6: probePair redeclared in this block
```

`go test ./internal/task/ -run 'TestEveryStateWriteIsDeclared$' -count=1 -timeout 120s` exited 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/task	0.285s
```

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1 -timeout 120s` exited 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	15.645s
```

`go test ./internal/statecontract/ -run 'TestOnlyTheListedFilesImportUnsafeOrC$' -count=1 -timeout 120s` exited 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	0.306s
```

The removal command was:

```sh
R=B1
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match B1. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run U5

`cp $IN/probes/U5.go internal/cli/pathnorm_case_darwin.go` exited 0 with no output.

`git diff --stat -- internal/cli/pathnorm_case_darwin.go` exited 0:

```text
 internal/cli/pathnorm_case_darwin.go | 2 --
 1 file changed, 2 deletions(-)
```

`go test ./internal/statecontract/ -run 'TestOnlyTheListedFilesImportUnsafeOrC$' -count=1 -timeout 120s` exited 1:

```text
--- FAIL: TestOnlyTheListedFilesImportUnsafeOrC (0.19s)
    statewrite_test.go:66: internal/cli/pathnorm_case_darwin.go is listed as importing unsafe and does not
    statewrite_test.go:72: directories=62 files=420
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	0.441s
FAIL
```

`git show HEAD:internal/cli/pathnorm_case_darwin.go > internal/cli/pathnorm_case_darwin.go` exited 0 with no output. `git diff --quiet -- internal/cli/pathnorm_case_darwin.go; echo $?` then printed `0`.

`git status --porcelain --untracked-files=all` exited 0:

```text
?? docs/v3-spike/330-s1-2a-state-writes/README.md
?? docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md
?? internal/statecontract/statewrite_test.go
```

## Round 2

- Walk case 1 passes over the root `.git` entry (`statewrite_test.go:246-251`).
- Walk case 2 refuses the measured symbolic-link forms without following them (`statewrite_test.go:252-269`).
- Walk case 3 skips another module only for a regular-file `go.mod` and enters every other directory (`statewrite_test.go:270-282`).
- Walk case 4 stops on a non-regular Go-like entry and passes over other non-regular entries (`statewrite_test.go:283-292`).
- Walk case 5 reads regular non-test `.go` files (`statewrite_test.go:293-307`).
- The closer's typed import-graph message is at `statewrite_test.go:383-396`.
- The closer's excluded-file import message is at `statewrite_test.go:345-347`.
- `structureAfterPointers` has a visited set (`statewrite_test.go:435-455`).
- `isTaskType` has a visited set (`statewrite_test.go:466-485`).
- `isStructLike` has a visited set (`statewrite_test.go:487-510`).
- N1, the round-two base and carry identity, is at `README.md:3`.
- N2, both input manifests without an absolute user path, is at `README.md:7-139`.
- N3, the five-case walk description, is at `README.md:145`.
- N4, ranges ending on their closing braces, is at `README.md:145-152`.
- N5, the closer and pointer-cycle sets, is at `README.md:148,152`.
- N6, the definition of struct-like, is at `README.md:150`.
- N7, all five excluded-file messages in full, is at `README.md:162`.
- N8, the corrected whole-task counter statement, is at `README.md:170`.
- N9, tracked and untracked test-file counts, is at `README.md:173`.
- N10, nested-module and directory-name limits, is at `README.md:174-175`.
- N11, the corrected unsafe statement, is at `README.md:180`.
- N12, the `.git`, link, `go.mod` link, and non-regular-entry limits, begins at `README.md:176`.
- N13, the input-directory statement replacing assignment-only lines, is at `README.md:139`.
- N14, the preserved Go whitespace and diff check, is at `README.md:395`.
- N15, U5's index-free restore, is at `README.md:521`.

### Run G

`bash $IN2/rows2.sh in G1 N1 N2 N3 N4 N5 N6 N7 N8 S1 S2 S3 S6 X5` exited 0:

```text
in G1
in N1
in N2
in N3
in N4
in N5
in N6
in N7
in N8
in S1
in S2
in S3
in S6
in X5
```

`git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `24`. `go build ./...` exited 0 with no output.

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1 -timeout 120s` exited 1 with this whole output:

```text
--- FAIL: TestNoPackageOutsideTaskWritesTaskState (14.83s)
    statewrite_test.go:262: internal/routing/zz_r2_s3.go: a symbolic link named like a Go file: the walk does not read it
    statewrite_test.go:266: internal/routing/zzr2s6/go.mod: a symbolic link named go.mod: the walk does not read it
    statewrite_test.go:255: internal/zzr2s1: a symbolic link to a directory: the walk does not follow it
    statewrite_test.go:255: internal/zzr2s2: a symbolic link to a directory: the walk does not follow it
    statewrite_test.go:146: github.com/omriariav/amq-squad/v3/internal/zzr2s1: a package of this module's path that the walk did not read, imported by github.com/omriariav/amq-squad/v3/internal/scheduler
    statewrite_test.go:123: internal/routing/zz_r2_x5_windows.go:3: a file this host's build constraints exclude imports github.com/omriariav/amq-squad/v3/internal/zzr2nowhere, a package of this module's path that the walk did not read
    statewrite_test.go:160: internal/routing/zz_r2_g1.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/routing/zzr2s6/w.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/.zzr2/zzr2n3/w.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/.zzr2/zzr2n7/w.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/_zzr2/zzr2n2/w.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/_zzr2/zzr2n6/w.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/node_modules/zzr2n4/w.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/node_modules/zzr2n8/w.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/testdata/zzr2n1/w.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:160: internal/scheduler/testdata/zzr2n5/w.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:227: packages=70 uses_of_state=52 whole_task_assignments=16 files_skipped_by_build_constraint=32
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	15.056s
FAIL
```

`go test ./internal/statecontract/ -run 'TestOnlyTheListedFilesImportUnsafeOrC$' -count=1 -timeout 120s` exited 1 with this whole output:

```text
--- FAIL: TestOnlyTheListedFilesImportUnsafeOrC (0.21s)
    statewrite_test.go:262: internal/routing/zz_r2_s3.go: a symbolic link named like a Go file: the walk does not read it
    statewrite_test.go:266: internal/routing/zzr2s6/go.mod: a symbolic link named go.mod: the walk does not read it
    statewrite_test.go:255: internal/zzr2s1: a symbolic link to a directory: the walk does not follow it
    statewrite_test.go:255: internal/zzr2s2: a symbolic link to a directory: the walk does not follow it
    statewrite_test.go:72: directories=71 files=436
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	0.432s
FAIL
```

`bash $IN2/rows2.sh out G1 N1 N2 N3 N4 N5 N6 N7 N8 S1 S2 S3 S6 X5` exited 0:

```text
out G1
out N1
out N2
out N3
out N4
out N5
out N6
out N7
out N8
out S1
out S2
out S3
out S6
out X5
```

`git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run M1

`bash $IN2/rows2.sh in M1` exited 0 and printed `in M1`. `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `7`. `go build ./...` exited 0 with no output.

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1 -timeout 120s` exited 1 with this whole output:

```text
--- FAIL: TestNoPackageOutsideTaskWritesTaskState (13.85s)
    statewrite_test.go:146: github.com/omriariav/amq-squad/v3/spike/zzr2m1/w: a package of this module's path that the walk did not read, imported by github.com/omriariav/amq-squad/v3/internal/scheduler
    statewrite_test.go:227: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.099s
FAIL
```

`go test ./internal/statecontract/ -run 'TestOnlyTheListedFilesImportUnsafeOrC$' -count=1 -timeout 120s` exited 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	0.329s
```

`bash $IN2/rows2.sh out M1` exited 0 and printed `out M1`. `git diff --quiet -- go.mod; echo $?` printed `0`. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run M3

`bash $IN2/rows2.sh in M3` exited 0 and printed `in M3`. `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `9`. `go build ./...` exited 0 with no output.

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1 -timeout 120s` exited 1 with this whole output:

```text
--- FAIL: TestNoPackageOutsideTaskWritesTaskState (13.48s)
    statewrite_test.go:146: github.com/omriariav/amq-squad/v3/spike/zzr2m1/w: a package of this module's path that the walk did not read, imported by example.com/zzr2d/d
    statewrite_test.go:227: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.717s
FAIL
```

`go test ./internal/statecontract/ -run 'TestOnlyTheListedFilesImportUnsafeOrC$' -count=1 -timeout 120s` exited 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	0.323s
```

`bash $IN2/rows2.sh out M3` exited 0 and printed `out M3`. `git diff --quiet -- go.mod; echo $?` printed `0`. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run R2

`bash $IN2/rows2.sh in R2` exited 0 and printed `in R2`. `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `4`.

`go build ./internal/scheduler/` exited 1:

```text
# github.com/omriariav/amq-squad/v3/internal/scheduler
internal/scheduler/zz_r2_r2.go:8:22: cannot convert p (variable of pointer type zzR2Loop) to type *task.Task
```

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1 -timeout 120s` exited 1 with this whole output:

```text
--- FAIL: TestNoPackageOutsideTaskWritesTaskState (14.51s)
    statewrite_test.go:140: internal/scheduler: internal/scheduler/zz_r2_r2.go:8:22: cannot convert p (variable of pointer type zzR2Loop) to type *task.Task
    statewrite_test.go:227: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.759s
FAIL
```

`bash $IN2/rows2.sh out R2` exited 0 and printed `out R2`. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run P

`bash $IN2/rows2.sh in R1 R3 S4 X6` exited 0:

```text
in R1
in R3
in S4
in X6
```

`git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `7`. `go build ./...` exited 0 with no output.

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$|TestOnlyTheListedFilesImportUnsafeOrC$' -count=1 -v -timeout 120s` exited 0:

```text
=== RUN   TestOnlyTheListedFilesImportUnsafeOrC
    statewrite_test.go:72: directories=62 files=423
--- PASS: TestOnlyTheListedFilesImportUnsafeOrC (0.11s)
=== RUN   TestNoPackageOutsideTaskWritesTaskState
    statewrite_test.go:227: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=32
--- PASS: TestNoPackageOutsideTaskWritesTaskState (13.86s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	14.210s
```

`bash $IN2/rows2.sh out R1 R3 S4 X6` exited 0:

```text
out R1
out R3
out S4
out X6
```

`git diff --quiet -- go.mod; echo $?` printed `0`. `git status --porcelain --untracked-files=all` exited 0 and printed:

```text
?? docs/v3-spike/330-s1-2a-state-writes/README.md
?? docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md
?? internal/statecontract/statewrite_test.go
```

## Verification

| Command | Exit | Output |
|---|---:|---|
| `go build ./...` | 0 | no output |
| `go vet ./internal/statecontract/` | 0 | no output |
| `gofmt -l internal/statecontract` | 0 | no output |
| `go test ./internal/statecontract/ -count=1 -v -timeout 120s` | 0 | Run 0's pass lines and logs above; package `ok`, 13.378s |
| `go test ./internal/task/ -count=1 -timeout 240s` | 0 | `ok github.com/omriariav/amq-squad/v3/internal/task 24.684s` |
| `go env GOOS GOARCH` | 0 | `darwin`, then `arm64` |
| `test -f .git; echo $?` | 0 | `0` |
| `find . -type l \| wc -l` | 0 | `8` |
| `git ls-files '*_test.go' \| wc -l` | 0 | `617` |
| `git ls-files --others --exclude-standard '*_test.go' \| wc -l` | 0 | `1` |
| `git status --porcelain --untracked-files=all` | 0 | the three new paths printed after Run U5 |
| `git diff --stat` | 0 | no output |
| `cmp $IN/tree/docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md` | 0 | no output |
| `git hash-object docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md` | 0 | `49a90b7a197845aa2632e2d5487ca30556bba231` |

The two package result lines were:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	13.378s
ok  	github.com/omriariav/amq-squad/v3/internal/task	24.684s
```

The count commands used in Stated limits exited 0 except `grep -c 'x/tools' go.mod`, which printed `0` and exited 1. `git ls-files '*.go' | grep -E '(^|/)[._][^/]*\.go$' | wc -l`, `git ls-files | grep -E '\.(s|S|c|h|syso|cc|cpp|m)$' | wc -l`, and `git grep -nE '^//(go:linkname|go:cgo_|export )' HEAD -- '*.go' | wc -l` each exited 0 and printed `0`.

`grep -c '/''Users/' docs/v3-spike/330-s1-2a-state-writes/README.md` printed `0` and exited 1. `grep -cE '[[:space:]]+$' docs/v3-spike/330-s1-2a-state-writes/README.md` printed `0` and exited 1. `grep -c 'IN=\\$IN' docs/v3-spike/330-s1-2a-state-writes/README.md` printed `0` and exited 1. `git diff --no-index --check /dev/null docs/v3-spike/330-s1-2a-state-writes/README.md` exited 3 and named only the two Go-output lines recorded under Run E1. `go run ./scripts/redact-evidence -check docs/v3-spike/330-s1-2a-state-writes/` and `git diff --check` each exited 0 with no output.

## Not run by the seat; the lead's, on the PR head

- Locked `make ci`.
- Locked `make docs-html-check`.

`corrections-to-s1-1.md` is a lead input landed byte for byte; `cmp $IN/tree/docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md` exited 0.
