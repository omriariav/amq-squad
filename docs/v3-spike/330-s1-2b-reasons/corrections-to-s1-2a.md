# Corrections to the S1-2a note

Written by the lead and landed byte for byte. The plan reviewer ruled, on PR
#980, that these lines of `docs/v3-spike/330-s1-2a-state-writes/README.md`
are NOT QUOTABLE as they stand and are corrected by this file: `:146`,
`:151`, `:174`, `:175`, `:180`, `:546` and `:782`. That note is not edited;
this file is the correction. A line number with no file is a line of that
note at `1a3db2f13466502a8de7948fc7e1fa07574843d9` (791 lines). A
`statewrite_test.go` line is a line of
`internal/statecontract/statewrite_test.go` at the same commit (551 lines).
"The lead's run" is a run at the gate of PR #980, on its head
`27578be04b7adc2de7df109eb906cec60aa13593`, in a scratch worktree, on
darwin/arm64 with go1.27.1; that head and the commit above have the same
tree (`a4280e55c3c811cf5e21c9730cd2ee873e1cfbd9`). A command below that
names the commit was run by the lead while writing this file, and its count
is of that commit. This file holds the word `-overlay` itself, so the same
search over a later tree counts this file too.

## A limit the note does not state: a build overlay

1. The two tests read the disk. Go's `-overlay` flag gives one build a file
   that is not on the disk, or other contents for a file that is, and a test
   that reads the disk cannot read it. The note does not state this limit:
   `git show 1a3db2f1:docs/v3-spike/330-s1-2a-state-writes/README.md | grep -ci overlay`
   printed `0` and exited 1.

   The lead's runs. The overlay file `add.txt` is a file of package
   `scheduler` that imports `internal/task` and declares
   `func ZZReviewOverlayWrite(t *task.Task) { t.State = task.StateCompleted }`,
   the body on a line of its own. `add.json` is
   `{"Replace": {"<worktree>/internal/scheduler/zz_review_overlay.go": "<dir>/add.txt"}}`;
   no file of that name is on the disk. With `GOFLAGS=-overlay=<dir>/add.json`
   set for each command:

   - `go list -f '{{join .GoFiles "\n"}}' ./internal/scheduler/ | grep -c zz_review_overlay`
     printed `1`.
   - `go build ./...` exited 0.
   - `go test ./internal/statecontract/ -run 'TestNoPackageOutsideTaskWritesTaskState$|TestOnlyTheListedFilesImportUnsafeOrC$' -count=1 -v -timeout 120s`
     exited 0, and its two log lines are the lines of a run with no overlay:
     `statewrite_test.go:72: directories=62 files=420` and
     `statewrite_test.go:227: packages=61 uses_of_state=42 whole_task_assignments=16 files_skipped_by_build_constraint=31`.

   The same overlay with `task.StateCompleted + 1.5` in the function:
   `go build ./internal/scheduler/` exited 1 with go's line ending in
   `bad.txt:6:12: invalid operation: task.StateCompleted + 1.5 (mismatched types untyped string and untyped float)`,
   so the overlay's file is compiled. A third overlay that replaces
   `internal/scheduler/roster.go` with a copy that has the function appended:
   `go build ./...` exited 0 and the same `go test` command exited 0 with the
   same two log lines.

   So a build made with such a flag can hold a write of a task's state
   outside `internal/task` that neither test refuses. It is a limit of any
   test that reads the disk. The plan reviewer ruled on PR #980 that it is a
   limit to be stated and not a form the tests must refuse: no file a commit
   carries sets the flag.

   The counts at the commit:
   `git grep -n -e '-overlay' 1a3db2f1 -- Makefile .github scripts | wc -l`
   printed `0`, and `git grep -l -e '-overlay' 1a3db2f1 | wc -l` printed `0`:
   no file of the commit holds the flag. `GOFLAGS` is the other way to pass
   it: `git grep -n -e 'GOFLAGS' 1a3db2f1 -- Makefile .github scripts | wc -l`
   printed `0`, and `git grep -l -e 'GOFLAGS' 1a3db2f1 | wc -l` printed `12`,
   none of the 12 with `-overlay` by the count before it. `go env GOFLAGS`
   on the lead's host printed an empty line.

   The item that closes this limit, by name: the day the two tests gain a
   way to read the module's flags, a `-overlay` in any workflow or Makefile
   line is itself a failure by name. No task holds that item yet.

## Three cite ranges

2. Three ranges end one line before the closing brace of the block they
   name. `:146` cites `:64-67` for the listed entry's refusal; the loop is
   `statewrite_test.go:64-68`. `:151` cites `:203-208` for the unkeyed task
   literal; the two `if` statements are `statewrite_test.go:203-209`. `:151`
   cites `:210-217` for the `State` key; the loop is
   `statewrite_test.go:210-218`. These three were found by the Codex pass at
   #980's gate and re-read by the lead; they are not a survey of the note's
   cites.

3. `:546` reads "N4, ranges ending on their closing braces, is at
   `README.md:145-152`." N4 is a correction item of the round 2 brief. The
   sentence is false of the three ranges of item 2, which are inside
   `:145-152`. Corrected: N4 is done at `:145-152` except for those three
   ranges.

## The stated limits

4. `:782` says the count commands used in Stated limits "exited 0 except
   `grep -c 'x/tools' go.mod`". `:175`'s count command also exited 1, as
   `:175` itself says: a `grep -c` that counts nothing prints `0` and exits
   1. `:175` is the true one. Corrected: the count commands used in Stated
   limits exited 0 except the two that end in a `grep -c` and print `0`,
   `:175`'s and `:184`'s. The lead's runs at the commit:
   `git ls-tree -r --name-only 1a3db2f1 | grep -E '(^|/)(testdata|node_modules|vendor|[._][^/]*)/' | grep -E '\.go$' | grep -vcE '_test\.go$'`
   printed `0` and exited 1, and `git show 1a3db2f1:go.mod | grep -c 'x/tools'`
   printed `0` and exited 1.

5. `:174` says its `go.mod` search "printed the root plus four nested module
   files": the output is described and not printed. The same list read from
   the commit, `git ls-tree -r --name-only 1a3db2f1 | grep -E '(^|/)go\.mod$'`,
   exited 0 and printed:

   ```text
   go.mod
   spike/embedpi/go.mod
   spike/gateverify/go.mod
   spike/piprobe/fixture/go.mod
   spike/seatexec/go.mod
   ```

6. `:180` says "The printed `unsafe.Offsetof` search found 0 sites." The
   note prints no such search: the sentence was dictated by the round 2
   brief, which did not say that the command stays in the note. The lead's
   run at the commit:
   `git grep -n 'unsafe\.Offsetof' 1a3db2f1 -- '*.go' ':!*_test.go' | wc -l`
   exited 0 and printed `0`.

7. `:175` says a non-test `.go` file under `testdata`, `node_modules`,
   `vendor`, or a dot- or underscore-prefixed directory is read like any
   other, and leaves out what follows from that: one that is not Go source
   stops both tests at the parse. The lead's run: with
   `internal/scheduler/testdata/zzp/bad.go` holding the one line
   `this is not Go source`, `go build ./...` exited 0 and the `go test`
   command of item 1 exited 1 with one line per test:

   ```text
   statewrite_test.go:46: internal/scheduler/testdata/zzp/bad.go:1:1: expected 'package', found this
   statewrite_test.go:126: internal/scheduler/testdata/zzp/bad.go:1:1: expected 'package', found this
   ```

   The parse is `parseModuleFile`, `statewrite_test.go:321-332`: a read
   error and a parse error each end the test with `t.Fatal`. It calls
   `t.Helper()`, so the line printed is its caller's, `:46` in the import
   test and `:126` in the typed test.
