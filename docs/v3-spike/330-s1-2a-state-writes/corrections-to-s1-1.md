# Corrections to the S1-1 note

Written by the lead and landed byte for byte. The plan reviewer ruled, on PR
#976, that the lines of `docs/v3-spike/330-s1-1-task-contract/README.md`
listed here are NOT QUOTABLE as they stand and are corrected by S1-2's note.
That note is not edited; this file is the correction. A line number with no
file is a line of that note at `4d649b5d1d98015129d1eb566b2ae5e26ff8c746`. A
`contract_test.go` line is a line of `internal/task/contract_test.go` at the
same commit (665 lines). "The lead's run" is a run at the gate of PR #976, on
its head `cdb73422886899560bc9449f7cbdf556fc6338d8`, in a scratch worktree.
"The round 3 brief" is the brief of that PR as dispatched, sha256
`a36dd56d83dcd5607de73648ae86a2ce106259f1a07a2bc93b18fdbdc3300b40`.

## The limit sentence

1. `:243` names three writes outside the guard: a whole-struct copy,
   reflection and decoding. It does not name a fourth, a write through
   `unsafe`. The lead's runs: with `"unsafe"` added to `lease.go`'s imports and
   `func ProbeUnsafe(t *Task) { *(*string)(unsafe.Add(unsafe.Pointer(t), unsafe.Offsetof(t.State))) = StateBlocked }`
   appended, `gofmt -l` printed nothing, `go build` and `go vet` exited 0 and
   both tests of `internal/task` exited 0. The same statement inside `Release`
   after its write, and the form with `unsafe.Offsetof(Task{}.State)`, also
   exited 0. From this PR on, an import of `unsafe` or `C` in a non-test file
   of the module fails `TestOnlyTheListedFilesImportUnsafeOrC` by name, with
   one listed file. The two tests of `internal/task` are unchanged and still
   pass on that write.

## The revert table's edit cells

The exit codes and first failing lines of these rows are not corrected: the
lead's run of each brief edit printed the row's words. The cells that
describe the edits are corrected. The note does not show which edit text the
seat ran, and neither does this file.

2. `:300`, Y7, reads "write state from a receiver expression". The round 3
   brief's Y7 appends `func probeSig(_ [len([1]Task{{State: StateBlocked}})]int) {}`:
   a keyed literal in the array length of a parameter's type. The same
   expression as a receiver does not build. A run by the lead at
   `6dc265323928136ab9bd4993d09c0243927ca3a5`, where `lease.go` has 1084
   lines: with `func (_ [len([1]Task{{State: StateBlocked}})]int) probeSig() {}`
   appended after a blank line, `go build ./internal/task/` printed
   `internal/task/lease.go:1086:9: invalid receiver type [1]int` and exited 1.
3. `:301`, Y8, reads "write state from a parameter expression". The brief's
   Y8 appends `var probeSigTask Task` and
   `func probeSigAddr(_ [len([1]*string{&probeSigTask.State})]int) {}`: the
   address of the field in the array length of a parameter's type.
4. `:302`, Y9, reads "write state from a result expression". The brief's Y9
   appends `func probeSigRes() (_ [len([1]Task{{State: StateBlocked}})]int) { return }`:
   the keyed literal in the array length of a result's type.
5. `:294`, Y1, reads "package `init` with a body and package-scope state
   write". The brief's Y1 appends
   `func init() { var t Task; _ = block(&t, "probe", "", "") }`: an `init`
   that calls the writer `block`. No state is written at package scope.
6. `:295`, Y2, reads "package `init` with a body". The brief's Y2 appends an
   empty `func init() {}` and then Y1's `init`, in that order: two `init`
   functions, the first empty.
7. `:304`, Y11, reads "unkeyed alias-of-`Task` literal". The brief's Y11 adds
   `type probeT Task` and an unkeyed `probeT` literal converted to `Task`:
   `probeT` is a defined type, not an alias.
8. `:311` reads "The shadow probe assigned a local named `StateBlocked`". The
   brief's shadow probe adds `StatePending := StateBlocked`: the local is
   named `StatePending`.

## Outputs and sentences

9. `:247` prints the command's output with a backslash and a `t` between the
   line number and `State`. The command prints a tab there. `:390` (J9) calls
   that line the command's "exact single output line"; it is not.
10. `:196` reads "unkeyed element of a struct literal … refused by the
    method-set test". The refusal is narrower: an unkeyed literal fails when
    it has no recorded type, when its type is a type parameter, or when its
    type is a struct with a field named `State` (`contract_test.go:249-284`).
    An unkeyed literal of any other type passes (the lead's run: an unkeyed
    `[]string` literal, exit 0).
11. `:205-206` reads "a package function or method use outside an indexed
    function body (F2 …)". The words "of this package" are missing: a use
    that resolves to another package's function or method is skipped
    (`contract_test.go:336`, and `:306` inside a body), as `:242` says.
12. `:214-216` and `:218-219` cite "the compile probes" without saying whose.
    They are the lead's two compile probes on go1.27.1, named in the round 3
    brief. `:266-268` says the two paths "have no row because their compile
    probes were refused before either test could observe them", and `:385`
    (J4) says the missing-type branch "is bounded by compile probes". For the
    receiver that is not a named type, the lead's two forms did not build,
    with the two messages `:215-216` prints. For the missing recorded type
    (`contract_test.go:259-263`), the brief's fact is that the lead found no
    form that compiles and reaches it; the brief names no probe of that path,
    and it does not say that no such form exists.
13. `:374` (H3) cites `:217-235` for "bodyless indexing". The refusal of a
    declaration without a body is `contract_test.go:232-235`; the indexing is
    `contract_test.go:240-246`.
14. `:378-392`: the eleven lines J1 to J11 are the seat's own numbering, not
    the round 3 brief's, whose items are J1 to J12. The section has no line
    for: the two limit sentences the brief's J8 asked for (`:244`, `:249`);
    the interface sentence of the brief's J10 (`:242`); the build-constraints
    fact of the brief's J11 (`:179`); the `reset_by.go` cites of the brief's
    J9; the tip shift of the brief's J12 (`:22`).

## Cite ends

15. These cites end one line before the closing brace of the block they
    name, or start somewhere else than the block does. They are the ones the
    lead found at #976's gate and while writing this file, not a survey of
    the note's cites. `contract_test.go:217-220` is `:217-221`; `:222-226` is
    `:222-227`; `:343-352` is `:343-353`; `:424-438` is `:424-439`;
    `:471-480` is `:471-481` (cited at `:190`, `:195` and `:389`); `:249-283`
    (cited at `:385`) is `:249-284`; `:258-262` (cited at `:218`) is
    `:259-263`; `:264-270` (cited at `:219-220`) is the unaliasing at
    `:264-267` and the refusal at `:268-271`; `:334-341` (cited at `:206`) is
    `:334-342`; and `:181-438` (cited at `:354`
    and `:364`) starts at a field of `parsedFunction`, where the test it
    describes is `contract_test.go:186-422` and `functionReceiver` is
    `:424-439`.
