# 3.3.0 S2-2: the router's two reason literals as constants, words for two reasons, the reason-constant finder, and exit_scan's cancelled entry

Base: `origin/v3` at `1992061fe0486bf9fb24c60d7a3a0cc170d0e314` with #1010
(S2-1 round 2) at its head `89ce75cf97c4c8e25cf1601499edd4e8e6e058f9`
applied on it. The plan reviewer's rulings of 2026-11-16 on the S2/S3 scope
and on S2-1 are the contract this note reports against. Line cites are at
that base. No behaviour changes: every value written to a task's file is the
same string as before.

## What changes

- `internal/task/task.go`: two block reasons as constants, beside
  `ReasonWidenRequested` and `ReasonGrowthGateRequired`:
  `ReasonClassUnknown = "class_unknown"` and
  `ReasonRetriesExhausted = "retries_exhausted"`.
- `internal/routing/router.go` (`routing.(*Router).decide`, three sites:
  the class missing from the routing table, the failed attempts at the bound,
  every entry of the tier used) and `internal/scheduler/hooks.go`
  (`scheduler.DefaultRouter.Route`, one site) name those constants where they
  wrote the literals. Both packages already import `internal/task`.
- `internal/statecontract/reasons.go`: the two constants on the block list,
  each with its writers (`routing.(*Router).decide`, and for
  `retries_exhausted` also `scheduler.(DefaultRouter).Route`, at the door
  `field scheduler.Route.Block`).
- `internal/task/contract.go`: `exit_scan` lists `cancelled`, conditional on
  "the scheduler's Preparer cleans attempts (AttemptCleaner) and the attempt
  is not recorded cleaned (CleanedAt unset); it is queued as a close-and-clean
  item". S2-1 named this a declaration error, not
  a behaviour defect (`internal/scheduler/exits.go:479-521`).
- `internal/scheduler/s2_outcome_sets_test.go`:
  `TestS2ExitScanCancelledWithACleaner` is folded into
  `TestS2ExitScanSetIsItsReader`, which drives each part of the conditional
  entry both ways (no Preparer or a Preparer that cleans attempts; the
  attempt recorded cleaned or not) and compares the condition's text with
  the one it drives. Codex's seventh pass found the first draft's condition
  left out the recorded clean (`exits.go:484`). The set helper takes the number
  of conditional entries each set must have (0, 1, 0).
- `internal/statecontract/s2_constants_test.go` (new). Both tests read the
  module type-checked, once: a constant is found by its resolved value, an
  expression by its folded value, never by the shape of its source.
  - `TestEveryReasonConstantIsListed`: every string constant of a reason
    family in the scope of the packages `reasons.go` imports, in any
    initializer form, is on the block, wait or not-a-reason list, or on
    `s2NotTaskReasons`. A family is a name's leading word followed by a
    capital or a digit: the fixed words `Reason`, `Block`, `Wait`, `Refusal`
    and `Skip`, and the leading word of every listed constant's name
    (`AMQ`, `Claude`, `Gone`, `History`, `Identity`, `Pi`, `Pin`, `Sandbox`,
    `Token` today), so a new constant of a family already listed is found
    wherever it is declared. It finds 100 constants: 83 listed, and the 17 of
    `s2NotTaskReasons`, each with what it is instead (seven router skip
    reasons, nine seat-prepare and lead lock refusal codes of `schedproc`,
    and `claudeseat.TokenVar`, an environment variable's name). An entry of
    `s2NotTaskReasons` that is listed, or is no such constant any more,
    fails, and so does a listed constant the walk does not find. The "no
    writer" entries of the lists stay; this test is what makes them a
    checked list rather than a hand list. A reason family's constant declared
    inside a function fails too (it is on no list), and so does any
    constant of a declaring package, of any name, at package scope or in a
    function, whose value is a listed reason's code and that is neither
    listed nor the constant a listed alias names (`seatexec.ClaudeHooksMissing
    = claudeseat.HooksMissing`, `seatexec.ClaudePermissionMismatch =
    claudeseat.PermissionMismatch`). Not found: a new reason constant of a
    new family with a new value, in a declaring package or outside them.
  - `TestNoLiteralRepeatsAListedReason`: no constant string expression with
    a literal in it (a literal, a parenthesized one, a concatenation, a
    conversion), outside a const declaration, reaches a declared block or
    wait door with the value of a listed reason, directly or through one
    local variable it is assigned to (every literal ever assigned to the
    variable is taken to reach each of its uses, and a use inside a
    concatenation, parentheses or a conversion reaches the door the whole
    expression reaches); the site must name the constant. The
    spike programs under `spike/` are records and are not read. Not
    followed: a literal that reaches a door through more than one assignment
    or through a function's parameter. A wider rule (any literal anywhere
    equal to a listed reason) finds 38 sites in the product that are other
    machines' codes or keys (grant keys, decision kinds, seal errors), so it
    is not this test.
  - Codex's seven read-only passes on the prototype found eleven forms the
    earlier drafts missed; each is now a row of the gate's revert runs: a
    parenthesized new reason constant, a concatenation of literals at a
    door, a new constant of an existing family in its own declaration, a
    literal through a local variable, the same with the variable assigned
    again after its use, a reason constant declared inside a function, a
    literal through a parenthesized assignment, a new constant of another
    name with a listed value, a local used inside a concatenation, two
    functions' local constants of one name (the walk now keys a local
    constant by its site), and a local through a conversion inside a
    concatenation. Its fourth pass also named the limit stated above.
- `pi/extensions/lib/words.ts`: `WAIT_WORDS.process_exited`,
  `BLOCK_WORDS.process_exited` and `BLOCK_WORDS.tool_unpinned`. The first
  line of each is the plan reviewer's words; the block entries' second lines
  are the lead's: `RESET` ("send it back with /reset") for `process_exited`,
  and "ask the owner to check the pin" for `tool_unpinned` (as
  `tool_pin_mismatch`).
- `pi/test/words.test.ts`: `NO_WORDS_YET` is empty, and `NOT_IN_THE_CONTRACT`
  keeps only the empty wait key.
- `pi/test/state-contract.json`: regenerated
  (`go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract`).

    The 17 entries of `s2NotTaskReasons`, each with its reason (the
    finder fails if one is not a string constant of a reason family, and
    an entry exempts it only from the block and wait lists):

    - `routing.SkipUsedBefore`: a router skip reason (routing.Skip.Reason), never a task field.
    - `routing.SkipProbation`: a router skip reason (routing.Skip.Reason), never a task field.
    - `routing.SkipInputOver32k`: a router skip reason (routing.Skip.Reason), never a task field.
    - `routing.SkipInputOver100k`: a router skip reason (routing.Skip.Reason), never a task field.
    - `routing.SkipRuntimeNotPermitted`: a router skip reason (routing.Skip.Reason), never a task field.
    - `routing.SkipNotInRegistry`: a router skip reason (routing.Skip.Reason), never a task field.
    - `routing.SkipUnverified`: a router skip reason (routing.Skip.Reason), never a task field.
    - `schedproc.ReasonBaseFetchUnauthenticated`: a seat-prepare refusal code (schedproc), never a task field.
    - `schedproc.ReasonBaseTipMoved`: a seat-prepare refusal code (schedproc), never a task field.
    - `schedproc.ReasonLeadRunning`: the lead lock's refusal code (schedproc), never a task field.
    - `schedproc.ReasonSeatBranchUnmerged`: a seat-prepare refusal code (schedproc), never a task field.
    - `schedproc.ReasonSeatBranchUnpushed`: a seat-prepare refusal code (schedproc), never a task field.
    - `schedproc.ReasonSeatGitdirForeign`: a seat-prepare refusal code (schedproc), never a task field.
    - `schedproc.ReasonSeatHeadUnreadable`: a seat-prepare refusal code (schedproc), never a task field.
    - `schedproc.ReasonSeatWorktreeCheckFailed`: a seat-prepare refusal code (schedproc), never a task field.
    - `schedproc.ReasonSeatWorktreeDirty`: a seat-prepare refusal code (schedproc), never a task field.
    - `claudeseat.TokenVar`: the name of the environment variable that carries the Claude token, not a code.

## Every constant equals its old literal

| constant | value | the literal it replaces |
|---|---|---|
| `task.ReasonClassUnknown` | `class_unknown` | `routing/router.go:518` |
| `task.ReasonRetriesExhausted` | `retries_exhausted` | `routing/router.go:537`, `:653`; `scheduler/hooks.go:115` |

## Rows

The worktree's HEAD at the precheck satisfied the later-HEAD form of the
precheck.

### Precheck

```console
$ git status --porcelain --untracked-files=all
```

No output; exit 0.

```console
$ git diff --stat 016da310774a80e368b888f3801691364e0274ce HEAD -- internal/task internal/routing internal/scheduler internal/statecontract pi/extensions/lib/words.ts pi/test/state-contract.json pi/test/words.test.ts docs/v3-spike/330-s2-2-constants
```

No output; exit 0.

### R1

```console
$ cd /Users/omri/amq-squad-lead/T/s2s3/inputs-s22 && shasum -a 256 -c MANIFEST.sha256
docs/v3-spike/330-s2-2-constants/README.md: OK
internal/routing/router.go: OK
internal/scheduler/hooks.go: OK
internal/scheduler/s2_outcome_sets_test.go: OK
internal/statecontract/reasons.go: OK
internal/statecontract/s2_constants_test.go: OK
internal/task/contract.go: OK
internal/task/task.go: OK
pi/extensions/lib/words.ts: OK
pi/test/state-contract.json: OK
pi/test/words.test.ts: OK
```

### R2

Each command printed nothing and exited 0.

```console
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/internal/routing/router.go internal/routing/router.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/internal/scheduler/hooks.go internal/scheduler/hooks.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/internal/scheduler/s2_outcome_sets_test.go internal/scheduler/s2_outcome_sets_test.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/internal/statecontract/reasons.go internal/statecontract/reasons.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/internal/statecontract/s2_constants_test.go internal/statecontract/s2_constants_test.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/internal/task/contract.go internal/task/contract.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/internal/task/task.go internal/task/task.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/pi/extensions/lib/words.ts pi/extensions/lib/words.ts
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/pi/test/state-contract.json pi/test/state-contract.json
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/pi/test/words.test.ts pi/test/words.test.ts
```

### R2b

```console
$ cmp <(sed '/^## Rows/q' /Users/omri/amq-squad-lead/T/s2s3/inputs-s22/docs/v3-spike/330-s2-2-constants/README.md) <(sed '/^## Rows/q' docs/v3-spike/330-s2-2-constants/README.md) && echo "the note above ## Rows is the input"
the note above ## Rows is the input
```

### R3

```console
$ git diff --numstat "$(git merge-base HEAD origin/v3)"
3	3	internal/routing/router.go
1	1	internal/scheduler/hooks.go
43	41	internal/scheduler/s2_outcome_sets_test.go
2	0	internal/statecontract/reasons.go
1	1	internal/task/contract.go
8	0	internal/task/task.go
3	0	pi/extensions/lib/words.ts
32	1	pi/test/state-contract.json
1	6	pi/test/words.test.ts
$ git status --porcelain --untracked-files=all
 M internal/routing/router.go
 M internal/scheduler/hooks.go
 M internal/scheduler/s2_outcome_sets_test.go
 M internal/statecontract/reasons.go
 M internal/task/contract.go
 M internal/task/task.go
 M pi/extensions/lib/words.ts
 M pi/test/state-contract.json
 M pi/test/words.test.ts
?? docs/v3-spike/330-s2-2-constants/README.md
?? internal/statecontract/s2_constants_test.go
```

### R4

```console
$ grep -n -F 'const ReasonClassUnknown = "class_unknown"' internal/task/task.go
35:const ReasonClassUnknown = "class_unknown"
$ grep -n -F 'const ReasonRetriesExhausted = "retries_exhausted"' internal/task/task.go
39:const ReasonRetriesExhausted = "retries_exhausted"
$ git grep -n -E '"(class_unknown|retries_exhausted)"' -- 'internal/*.go' ':!*_test.go'
internal/task/task.go:35:const ReasonClassUnknown = "class_unknown"
internal/task/task.go:39:const ReasonRetriesExhausted = "retries_exhausted"
```

### R5

```console
$ go test ./internal/statecontract/ -count=1 -v -timeout 7m
--- PASS: TestContractFileIsCurrent (0.00s)
--- PASS: TestReasonsAreTiedToTheirWriters (14.29s)
--- PASS: TestEveryReasonConstantIsListed (13.98s)
--- PASS: TestNoLiteralRepeatsAListedReason (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	42.795s
```

### R6

```console
$ go test ./internal/scheduler/ -run '^TestS2' -count=1 -v -timeout 4m
--- PASS: TestS2RetryBudgetSetIsItsReader (0.00s)
--- PASS: TestS2ExitScanSetIsItsReader (0.00s)
--- PASS: TestS2SeatLapsedSetIsItsReader (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.344s
```

There was no output line for `TestS2ExitScanCancelledWithACleaner`.

### R7

```console
$ go test ./internal/task/ -count=1 -timeout 7m
ok  	github.com/omriariav/amq-squad/v3/internal/task	45.466s
$ go test ./internal/routing/ -count=1 -timeout 7m
ok  	github.com/omriariav/amq-squad/v3/internal/routing	11.132s
$ go test ./internal/scheduler/ -count=1 -timeout 7m
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	36.760s
```

### R8

```console
$ make pi-test NPM_FLAGS=--offline
ℹ tests 153
ℹ suites 0
ℹ pass 152
ℹ fail 0
ℹ cancelled 0
ℹ skipped 1
ℹ todo 0
ℹ duration_ms 44144.687666
```

The command exited 0.

### R9

```console
$ go vet ./internal/statecontract/ ./internal/task/ ./internal/routing/ ./internal/scheduler/
$ gofmt -l internal/statecontract internal/task internal/routing internal/scheduler
```

Each command printed nothing and exited 0.
