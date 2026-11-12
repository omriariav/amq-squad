# 3.3.0 S1-2a state writes

This PR adds `internal/statecontract/statewrite_test.go` with two tests, this note, and `corrections-to-s1-1.md`. It changes no product file and no existing test: `git status --porcelain --untracked-files=all` in Verification lists exactly those three new paths. The base is `4d649b5d1d98015129d1eb566b2ae5e26ff8c746`.

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

## What the two tests check

- The shared walk starts at the module root, skips dot-prefixed and underscore-prefixed directories, `testdata`, `node_modules`, and nested modules, and reads non-test `.go` files in sorted directory and file order (`statewrite_test.go:222-280`). Parse errors are fatal (`:282-292`).
- `TestOnlyTheListedFilesImportUnsafeOrC` parses imports in each walked file. An `unsafe` or `C` import fails with `import of <path>: a write through it is not declared` unless the exact file/import pair is the package-level entry at `statewrite_test.go:21-23`; the import check and refusal are at `:36-58`. The listed entry also fails with `<file> is listed as importing <path> and does not` when its exact import is absent (`:61-64`). The directory floor and log are at `:66-69`.
- `TestNoPackageOutsideTaskWritesTaskState` imports `task.Task`, requires its underlying struct and `State` field (`statewrite_test.go:72-97`), skips `internal/task` (`:103-106`), and type-checks each host-included package with shared source importer data for types, definitions, and selections (`:107-139`). A type error is reported as `<directory>: <error>` at `:129-133` and scanning continues.
- A state selection must be a typed field selection named `State` whose selected field is `task.Task.State` or whose declaring struct is identical to the task struct with tags ignored (`statewrite_test.go:346-365`). Assignment sides fail as `a write of a task's state` (`:148-157`); range key or value selections use the same message (`:158-164`); and `&` fails as `the address of a task's state` (`:165-170`).
- A type declaration fails as `a named type over a task` when its alias-resolved underlying type is the task struct or a pointer, slice, array, or map whose element or value recursively meets that rule (`statewrite_test.go:171-174,429-454`). A one-argument type conversion fails as `a conversion of a task` when the types differ and one is a task type while the other is struct-like (`:175-185,393-427`).
- A non-empty composite literal of a type parameter after alias resolution and one pointer removal fails as `a literal of a type parameter` (`statewrite_test.go:186-194,456-468`). A non-empty task literal fails as `an unkeyed task literal` when its first element is unkeyed (`:195-200`), or once per `State` key as `a task literal that names its state` (`:202-210`). Empty literals pass these checks, and nested literals remain in the AST walk (`:186-213`).
- A host-excluded file is parsed without types. It refuses an `internal/task` import, a selector named `State`, a keyed element named `State`, or a declared struct field named `State`, using the four messages at `statewrite_test.go:295-328`.
- The typed scan requires at least two packages and one state selection, then logs package, selection, whole-task-assignment, and excluded-file counts (`statewrite_test.go:216-219`).

## Build constraints

`TestOnlyTheListedFilesImportUnsafeOrC` reads every non-test `.go` file the walk finds and applies no build constraint: a file for another platform or behind a tag is read like any other.

`TestNoPackageOutsideTaskWritesTaskState` type-checks the files this host's build constraints include (`go/build`'s default context: the host's GOOS and GOARCH, cgo as the host has it, no extra tags) and reads the files they exclude by spelling.

The excluded-file messages are `a file this host's build constraints exclude imports internal/task` (`statewrite_test.go:303-305`), `names a State field` (`:309-312`), `names a State key` (`:313-316`), and `declares a State field` (`:317-324`). Run 0 logged 31 excluded files on this host; `go env GOOS GOARCH` printed `darwin` and `arm64` in Verification.

The two tests of `internal/task` apply no build constraint, and a per-platform pair of files in that package that declares the same name fails `TestEveryStoreMethodIsTiedToItsMoves` with the type checker's redeclaration error.

Run B1 records that redeclaration and the three passing companion checks.

## Stated limits

- A whole task assigned by an `=` left-hand side is counted and not refused (`statewrite_test.go:148-156`): Run 0 logged `whole_task_assignments=16`, and Run C's L1/L4 increased it to 18. The counter does not include a task copied by `:=`, `copy`, `append`, a channel, or a call result, nor an assignment in a host-excluded file, because `:148-156` counts typed included-file left sides with token `=`.
- Reflection is not modeled. `git grep -lE '^([[:space:]]*|import )([A-Za-z_.]+ )?"reflect"$' HEAD -- '*.go' ':!*_test.go' | wc -l` exited 0 and printed `8`; Run C's L2 passed.
- Decoding is not modeled. `git grep -lE '^([[:space:]]*|import )([A-Za-z_.]+ )?"encoding/json"$' HEAD -- '*.go' ':!*_test.go' | wc -l` exited 0 and printed `149`; Run C's L3 passed.
- `_test.go` files are outside both walks. `git ls-files '*_test.go' | wc -l` exited 0 and printed `617`; Run C's C8 passed.
- A nested directory with `go.mod` is not entered (`statewrite_test.go:245-248`). `git ls-files | grep -E '(^|/)go\.mod$'` exited 0 and printed `go.mod`, `spike/embedpi/go.mod`, `spike/gateverify/go.mod`, `spike/piprobe/fixture/go.mod`, and `spike/seatexec/go.mod`; `git ls-files | grep -E '^(spike/embedpi|spike/gateverify|spike/piprobe/fixture|spike/seatexec)/.*\.go$' | grep -vcE '_test\.go$'` exited 0 and printed `13`.
- Host-excluded files are read by spelling, so Run W's X2 refused a non-task `fmt.State`; the excluded-file pass cannot distinguish that selector from a task selector (`statewrite_test.go:307-312`). The excluded count is host-specific, and `internal/piruntime/embed_amqpi.go:1` requires the `amqpi` tag.
- The unsafe/C test refuses imports and does not inspect what the listed file does (`statewrite_test.go:36-69`). `git grep -lE '^([[:space:]]*|import )([A-Za-z_.]+ )?"unsafe"$' HEAD -- '*.go' ':!*_test.go' | wc -l` printed `1`; the same command for `"C"` printed `0`. `git grep -n 'unsafe\.Offsetof' HEAD -- '*.go' ':!*_test.go' | wc -l` printed `0`. Until S1-2 merges the tests pass on an unsafe write with zero sites. `git ls-files | grep -E '\.(s|S|c|h|syso|cc|cpp|m)$' | wc -l` and `git grep -nE '^//(go:linkname|go:cgo_|export )' HEAD -- '*.go' | wc -l` each printed `0`; those forms are not scanned.
- A composite literal of a type parameter is refused without asking whether its constraint can be a task (`statewrite_test.go:186-194,456-468`); Run W's W8 can be a task.
- A non-task struct with `State` is not a task struct, and a wrapper struct, function, channel, or interface is not a type over a task (`statewrite_test.go:384-454`); Run C's C6 and C9-C12 passed, while Run W's W7 and W7d were refused at their promoted-field writes.
- `internal/task` is skipped at `statewrite_test.go:103-106`; Run B1 shows the two existing task tests' behavior for a per-platform pair.
- The test file's imports at `statewrite_test.go:3-17` are from the standard library. `grep -c 'x/tools' go.mod` printed `0` and exited 1. `git ls-files '*.go' | grep -E '(^|/)[._][^/]*\.go$' | wc -l` printed `0`, so the walk's filename rule has no hidden or underscore-prefixed Go-file case on this tree.

## Rows

### Run 0

`go test ./internal/statecontract/ -count=1 -v` exited 0. The requested lines were:

```text
--- PASS: TestContractFileIsCurrent (0.00s)
--- PASS: TestCommittedContractIsSubstantial (0.00s)
    statewrite_test.go:69: directories=62 files=420
--- PASS: TestOnlyTheListedFilesImportUnsafeOrC (0.03s)
    statewrite_test.go:219: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
--- PASS: TestNoPackageOutsideTaskWritesTaskState (13.01s)
```

### Run W

The copy command was:

```sh
IN=$IN
R=W
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match W; the landed-file check `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `42`. `go build ./internal/scheduler/` exited 0 with no output.

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1` exited 1 with this whole output:

```text
--- FAIL: TestNoPackageOutsideTaskWritesTaskState (13.04s)
    statewrite_test.go:115: internal/scheduler/zz_probe_x1_windows.go:3: a file this host's build constraints exclude imports internal/task
    statewrite_test.go:311: internal/scheduler/zz_probe_x1_windows.go:5: a file this host's build constraints exclude names a State field
    statewrite_test.go:311: internal/scheduler/zz_probe_x2_windows.go:5: a file this host's build constraints exclude names a State field
    statewrite_test.go:315: internal/scheduler/zz_probe_x3_windows.go:5: a file this host's build constraints exclude names a State key
    statewrite_test.go:321: internal/scheduler/zz_probe_x4_windows.go:3: a file this host's build constraints exclude declares a State field
    statewrite_test.go:173: internal/scheduler/zz_probe_t1.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_t10.go:9: a named type over a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_t2.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_t3.go:6: a named type over a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_t4.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_t5.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_t6.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_t7.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_t8.go:7: a named type over a task outside internal/task is not declared
    statewrite_test.go:152: internal/scheduler/zz_probe_w1.go:5: a write of a task's state outside internal/task is not declared
    statewrite_test.go:152: internal/scheduler/zz_probe_w11.go:47: a write of a task's state outside internal/task is not declared
    statewrite_test.go:208: internal/scheduler/zz_probe_w12.go:45: a task literal that names its state outside internal/task is not declared
    statewrite_test.go:152: internal/scheduler/zz_probe_w1b.go:5: a write of a task's state outside internal/task is not declared
    statewrite_test.go:152: internal/scheduler/zz_probe_w1c.go:5: a write of a task's state outside internal/task is not declared
    statewrite_test.go:152: internal/scheduler/zz_probe_w1d.go:5: a write of a task's state outside internal/task is not declared
    statewrite_test.go:168: internal/scheduler/zz_probe_w2.go:5: the address of a task's state outside internal/task is not declared
    statewrite_test.go:162: internal/scheduler/zz_probe_w3.go:6: a write of a task's state outside internal/task is not declared
    statewrite_test.go:152: internal/scheduler/zz_probe_w3b.go:7: a write of a task's state outside internal/task is not declared
    statewrite_test.go:208: internal/scheduler/zz_probe_w4.go:5: a task literal that names its state outside internal/task is not declared
    statewrite_test.go:208: internal/scheduler/zz_probe_w4b.go:5: a task literal that names its state outside internal/task is not declared
    statewrite_test.go:208: internal/scheduler/zz_probe_w4c.go:5: a task literal that names its state outside internal/task is not declared
    statewrite_test.go:199: internal/scheduler/zz_probe_w5.go:9: an unkeyed task literal outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_w6.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:183: internal/scheduler/zz_probe_w6.go:7: a conversion of a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_w6b.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:183: internal/scheduler/zz_probe_w6b.go:7: a conversion of a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_w6c.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:183: internal/scheduler/zz_probe_w6c.go:7: a conversion of a task outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_w6d.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:183: internal/scheduler/zz_probe_w6d.go:7: a conversion of a task outside internal/task is not declared
    statewrite_test.go:183: internal/scheduler/zz_probe_w6e.go:9: a conversion of a task outside internal/task is not declared
    statewrite_test.go:183: internal/scheduler/zz_probe_w6f.go:46: a conversion of a task outside internal/task is not declared
    statewrite_test.go:152: internal/scheduler/zz_probe_w7.go:7: a write of a task's state outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_w7b.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:152: internal/scheduler/zz_probe_w7b.go:7: a write of a task's state outside internal/task is not declared
    statewrite_test.go:173: internal/scheduler/zz_probe_w7c.go:5: a named type over a task outside internal/task is not declared
    statewrite_test.go:208: internal/scheduler/zz_probe_w7c.go:7: a task literal that names its state outside internal/task is not declared
    statewrite_test.go:152: internal/scheduler/zz_probe_w7d.go:7: a write of a task's state outside internal/task is not declared
    statewrite_test.go:192: internal/scheduler/zz_probe_w8.go:5: a literal of a type parameter outside internal/task is not declared
    statewrite_test.go:152: internal/scheduler/zz_probe_w9.go:5: a write of a task's state outside internal/task is not declared
    statewrite_test.go:219: packages=61 uses_of_state=54 whole_task_assignments=16 files_skipped_by_build_constraint=36
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	13.293s
FAIL
```

T9 produced no line of its own: this host excluded its positional literal, while T2's named type refused the pair; T9 spells none of the four excluded-file forms.

The removal command was:

```sh
IN=$IN
R=W
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match W. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run C

The copy command was:

```sh
IN=$IN
R=C
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match C; `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `19`. `go build ./internal/scheduler/` exited 0 with no output.

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$|TestOnlyTheListedFilesImportUnsafeOrC$' -count=1 -v` exited 0:

```text
=== RUN   TestOnlyTheListedFilesImportUnsafeOrC
    statewrite_test.go:69: directories=62 files=435
--- PASS: TestOnlyTheListedFilesImportUnsafeOrC (0.03s)
=== RUN   TestNoPackageOutsideTaskWritesTaskState
    statewrite_test.go:219: packages=61 uses_of_state=43 whole_task_assignments=18 files_skipped_by_build_constraint=32
--- PASS: TestNoPackageOutsideTaskWritesTaskState (14.15s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	14.415s
```

The copy changed Run 0 by files +15, packages +0, state uses +1, whole-task assignments +2, and excluded files +1.

The removal command was:

```sh
IN=$IN
R=C
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match C. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run U

The copy command was:

```sh
IN=$IN
R=U
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match U; `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `8`.

`go test ./internal/statecontract/ -run 'TestOnlyTheListedFilesImportUnsafeOrC$' -count=1` exited 1 with this whole output:

```text
--- FAIL: TestOnlyTheListedFilesImportUnsafeOrC (0.03s)
    statewrite_test.go:57: internal/scheduler/zz_probe_u1.go:3: import of unsafe: a write through it is not declared
    statewrite_test.go:57: internal/scheduler/zz_probe_u2.go:3: import of C: a write through it is not declared
    statewrite_test.go:57: internal/scheduler/zz_probe_u3.go:3: import of unsafe: a write through it is not declared
    statewrite_test.go:57: internal/scheduler/zz_probe_u4.go:3: import of unsafe: a write through it is not declared
    statewrite_test.go:57: internal/scheduler/zz_probe_u6_windows.go:3: import of unsafe: a write through it is not declared
    statewrite_test.go:69: directories=62 files=425
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	0.263s
FAIL
```

The removal command was:

```sh
IN=$IN
R=U
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match U. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run E1

The copy command was:

```sh
IN=$IN
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
out=$(go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1 2>&1); echo "exit=$?"; echo "$out" | grep -c 'statewrite_test.go:'; echo "$out" | grep -c 'could not import'; echo "$out" | grep -v 'could not import'
```

The command exited 0 and printed:

```text
exit=1
51
44
--- FAIL: TestNoPackageOutsideTaskWritesTaskState (75.09s)
    statewrite_test.go:132: internal/cli: internal/cli/operator.go:390:15: operator.Enabled undefined (type statusOperatorView has no field or method Enabled)
    statewrite_test.go:132: internal/cli: internal/cli/operator.go:390:53: operator.Handle undefined (type statusOperatorView has no field or method Handle)
    statewrite_test.go:132: internal/cli: internal/cli/operator.go:393:54: operator.Handle undefined (type statusOperatorView has no field or method Handle)
    statewrite_test.go:132: internal/cli: internal/cli/team.go:397:58: not enough arguments in call to wrap
        	have (unknown type)
        	want (unknown type, error)
    statewrite_test.go:132: internal/schedproc: internal/schedproc/decisions.go:93:74: d.Kind undefined (type DecisionLine has no field or method Kind)
    statewrite_test.go:132: internal/scheduler: internal/scheduler/zz_probe_e1.go:3:19: cannot use "x" (untyped string constant) as int value in variable declaration
    statewrite_test.go:219: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	75.496s
FAIL
```

The `grep -v` cut 44 `could not import` lines because they contained the worktree's absolute path; no other line was cut. Both counts match the lead's run.

The removal command was:

```sh
IN=$IN
R=E1
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match E1. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run E2

The copy command was:

```sh
IN=$IN
R=E2
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match E2; `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `4`.

`go build ./internal/scheduler/` exited 1:

```text
# github.com/omriariav/amq-squad/v3/internal/scheduler
internal/scheduler/zz_probe_e2.go:4:14: cannot use "x" (untyped string constant) as int value in variable declaration
```

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1` exited 1 with this whole output:

```text
--- FAIL: TestNoPackageOutsideTaskWritesTaskState (13.97s)
    statewrite_test.go:132: internal/scheduler: internal/scheduler/zz_probe_e2.go:4:14: cannot use "x" (untyped string constant) as int value in variable declaration
    statewrite_test.go:219: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	14.478s
FAIL
```

The removal command was:

```sh
IN=$IN
R=E2
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && rm -f "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match E2. `git status --porcelain --untracked-files=all` exited 0 and printed the three declared paths shown after Run U5.

### Run B1

The copy command was:

```sh
IN=$IN
R=B1
while IFS=$'\t' read -r id run target; do [ "$run" = "$R" ] && cp "$IN/probes/$id.go" "$target"; done < "$IN/probes/TARGETS.tsv"
```

Its shell exit was 1 because the loop's last test did not match B1; `git status --porcelain --untracked-files=all | wc -l` exited 0 and printed `5`.

`go build ./internal/task/` exited 0 with no output.

`go test ./internal/task/ -run 'TestEveryStoreMethodIsTiedToItsMoves$' -count=1` exited 1. Its first `contract_test.go:` line was:

```text
    contract_test.go:201: zz_probe_linux.go:3:6: probePair redeclared in this block
```

`go test ./internal/task/ -run 'TestEveryStateWriteIsDeclared$' -count=1` exited 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/task	0.236s
```

`go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$' -count=1` exited 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	13.548s
```

`go test ./internal/statecontract/ -run 'TestOnlyTheListedFilesImportUnsafeOrC$' -count=1` exited 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	0.278s
```

The removal command was:

```sh
IN=$IN
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

`go test ./internal/statecontract/ -run 'TestOnlyTheListedFilesImportUnsafeOrC$' -count=1` exited 1:

```text
--- FAIL: TestOnlyTheListedFilesImportUnsafeOrC (0.03s)
    statewrite_test.go:63: internal/cli/pathnorm_case_darwin.go is listed as importing unsafe and does not
    statewrite_test.go:69: directories=62 files=420
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract	0.280s
FAIL
```

The first sandboxed `git checkout -- internal/cli/pathnorm_case_darwin.go` attempt exited 128 because it could not create the worktree index lock. The same prescribed command was rerun with access to that index and exited 0 with no output. `git diff --quiet -- internal/cli/pathnorm_case_darwin.go` then exited 0 with no output.

`git status --porcelain --untracked-files=all` exited 0:

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
| `go test ./internal/statecontract/ -count=1 -v` | 0 | Run 0's pass lines and logs above; package `ok`, 13.553s |
| `go test ./internal/task/ -count=1` | 0 | `ok github.com/omriariav/amq-squad/v3/internal/task 24.791s` |
| `go env GOOS GOARCH` | 0 | `darwin`, then `arm64` |
| `git status --porcelain --untracked-files=all` | 0 | the three new paths printed after Run U5 |
| `git diff --stat` | 0 | no output |
| `cmp $IN/tree/docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md` | 0 | no output |

The two package result lines were:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	13.553s
ok  	github.com/omriariav/amq-squad/v3/internal/task	24.791s
```

The count commands used in Stated limits exited 0 except `grep -c 'x/tools' go.mod`, which printed `0` and exited 1. `git ls-files '*.go' | grep -E '(^|/)[._][^/]*\.go$' | wc -l`, `git ls-files | grep -E '\.(s|S|c|h|syso|cc|cpp|m)$' | wc -l`, and `git grep -nE '^//(go:linkname|go:cgo_|export )' HEAD -- '*.go' | wc -l` each exited 0 and printed `0`.

`grep -c '/''Users/' docs/v3-spike/330-s1-2a-state-writes/README.md` printed `0` and exited 1. `grep -cE '[[:space:]]+$' docs/v3-spike/330-s1-2a-state-writes/README.md` printed `0` and exited 1. `go run ./scripts/redact-evidence -check docs/v3-spike/330-s1-2a-state-writes/` and `git diff --check` each exited 0 with no output.

## Not run by the seat; the lead's, on the PR head

- Locked `make ci`.
- Locked `make docs-html-check`.

`corrections-to-s1-1.md` is a lead input landed byte for byte; `cmp $IN/tree/docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md docs/v3-spike/330-s1-2a-state-writes/corrections-to-s1-1.md` exited 0.
