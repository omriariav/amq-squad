# 3.3.0 S3-1: `next` on task show and the status task rows

Base: `origin/v3` at `811096cafa8cd031c270621f164edb7eb6794ee7` (S2-2 merged). The plan
reviewer's rulings of 2026-11-16 on the S2/S3 scope (answers 6 and 7) are
the contract this note reports against. Line cites are at that base.

## The shape

`next` is a list of `{verb, who, may_refuse}`, one shape everywhere:

- `verb`: the command (`task done`, `task block`, `task fail`,
  `task release`, `task cancel`, `task reset`, `task widen`,
  `dispatch --task`), or `answer through Buzz` for the owner.
- `who`: `lead`, `holder` or `owner`. A task blocked for the owner (block
  route `owner`) lists the owner's answer through Buzz.
- `may_refuse` (omitted when empty): the conditions the move needs that the
  task record cannot show; the command may still refuse on them.

It is never `null`: a completed or cancelled task has `[]`.

## Where it is

- `task show --json`: `data.next`, beside `data.task`, never in it. With no
  `--me`, every role's entries; with `--me`, the roles that handle holds:
  `holder` when it holds the task's lease, `lead` when its launch record's
  role is `lead` (`status.LeadRole`, the rule of the status document's
  `lead_handle`); a handle that holds neither gets `[]`.
- `status --json`: each task row's `next`, every role's entries, each
  naming its `who`.
- The stored task record (`task.Task`) has no `next` field and its file is
  unchanged.
- pi does not read `next` in 3.3.0 (ruling 7): no pi code changes. The pi
  screen's status fixture (`pi/test/status-fixture.json`) gains `next` on
  its task rows, because `TestThePiScreensFixtureIsAStatusDocument` requires
  it to be additive against the golden.

## How it is derived (`internal/task/next.go`)

From the declared moves (`task.Moves`, `internal/task/contract.go:63-86`)
and the task record alone, never from a model or a heuristic:

1. The moves whose From is the task's state.
2. Each Via method a lead, holder or owner runs from the CLI, with its
   command (`nextMethods`). The methods only the scheduler, the harness or a
   seat claiming its offer runs (Grant, Claim, ClaimServing, Complete,
   CompleteBound, RefuseCommit, RefuseCommitPaths, ExpireDue, Reset,
   ResetIfBlocked) and the add methods have no entry.
3. Each role the row's Who stands for: `live_holder` is the holder;
   `proven_lead` and `creator` are the lead; `asserted_handle` is the lead
   for GrantClean (the lead's `dispatch --task`), Block and BlockWithPaths
   on a pending task, Cancel and ResetBy, and both the holder and the lead
   for Release; `scheduler` and `harness_shell` are no role.
4. The conditions the record shows are evaluated, and a move they refuse is
   not listed:
   - the holder's commands need a live lease (its expiry after now) held by
     the caller (any holder when no caller is named); `task done` also needs
     no done report; `task release` needs a lease, live or not;
   - `task reset` is not listed when the block route is the owner's, nor to
     a caller who is not the recorded creator; with no caller named and a
     creator recorded, its `may_refuse` names the creator;
   - `task widen` is listed only for a block the CLI widens
     (`validateBlockedWiden`, `internal/cli/task_widen.go:430-467`): a
     commit refusal that kept its report for `protected_path` or
     `growth_gate_required`; `outside_declared_files` with refused paths; or
     a seat's typed request (no report, its attempt bound, refused paths,
     `widen_requested` or `growth_gate_required`); and never to a caller who
     holds or reported the task (`self_widen`). `seatcommit` imports `task`,
     so `next.go` reads the two commit refusal codes from two new task
     constants, `task.WidenReasonOutside` and `task.WidenReasonProtected`,
     and `seatcommit.ReasonOutside` and `seatcommit.ReasonProtected` become
     those constants (`internal/seatcommit/policy.go`), as
     `seatcommit.ReasonGrowthGate` already is `task.ReasonGrowthGateRequired`:
     the same values, one declaration each, which S2-2's
     `TestEveryReasonConstantIsListed` reads as listed aliases, and which
     `internal/routing/brief3112_test.go`'s `TestEveryCommitRefusalCodeIsClassified`
     names among the foreign constants seatcommit refuses with.
5. Any other condition is the entry's `may_refuse`: for `dispatch --task`,
   the dependencies (completed, their PRs merged at the bound head), the
   file locks, and the named seat (free, cleaned, allowed to run the task's
   class, and given the verifier gate's command); for `task widen`, a readable grant that allows the paths, the
   paths, the file-lock exclusion, the growth gate a widening past a grant
   limit needs, the commit step's check of a re-armed commit, the self-widen
   rule when no caller is named, and, when the block route is the owner's,
   the owner's accepted gate.
6. One entry per `(verb, who)`; holder entries first, then the lead's, then
   the owner's, each by verb.

## Facts found

- Row `in_progress -> blocked` lists Done, Complete and CompleteBound with
  the Who `live_holder`, `scheduler` and `harness_shell` together; the table
  does not pair a method with a Who. Complete and CompleteBound are rows of
  `in_progress -> completed` for the scheduler and the harness only, so the
  test that every method a lead, holder or owner runs has an entry skips a
  method some row lists only for those two.

## Codex on the prototype

The first read-only pass found three defects in `task widen`'s entry, each
fixed and a row of `TestNextRows`: it was listed for a block the CLI does
not widen (`verdict_missing` with a report: `widen_not_requested`); it was
listed to the lead who reported the task (`self_widen`, which the record
shows); and its `may_refuse` did not name the growth gate a widening past a
grant limit needs while the route is still the lead's. The second pass found
`may_refuse` did not name the readable grant or the re-armed commit's check
(fixed, with the verifier gate's command for `dispatch --task`), and that
`task widen` on a pending or in-progress task is not listed: the CLI adds the
paths there without a state move (`internal/task/scope.go:47-103`), which no
declared move describes, so `next`, derived from the moves, does not offer it
(a question for the plan reviewer). The plan reviewer ruled (2026-11-16) that `next` stays derived from the declared moves only, so `task widen` on a pending or in-progress task, a scope change with no state move, is not offered; declaring such scope-only operations is a candidate for S4. The fourth pass, on the base after
S2-2, raised that question again and found `dispatch --task`'s `may_refuse`
did not name the dependencies' merge or the seat's permission to run the
class; it now names every check `dispatch` makes beyond the record.

## Rows

The worktree's HEAD at the precheck satisfied the later-HEAD form of the
precheck.

### Precheck

```console
$ git status --porcelain --untracked-files=all
```

No output; exit 0.

```console
$ git diff --stat 811096cafa8cd031c270621f164edb7eb6794ee7 HEAD -- internal/task internal/status internal/seatcommit/policy.go internal/routing/brief3112_test.go internal/cli/task.go internal/cli/task_widen.go internal/cli/task_show_next_test.go pi/test/status-fixture.json docs/status-contract.md docs/v3-spike/330-s3-1-next
```

No output; exit 0.

### R1

```console
$ cd /Users/omri/amq-squad-lead/T/s2s3/inputs-s31 && shasum -a 256 -c MANIFEST.sha256
docs/status-contract.md: OK
docs/v3-spike/330-s3-1-next/README.md: OK
internal/cli/task.go: OK
internal/cli/task_show_next_test.go: OK
internal/routing/brief3112_test.go: OK
internal/seatcommit/policy.go: OK
internal/status/classify.go: OK
internal/status/schema.go: OK
internal/status/testdata/status-fleet-v1.golden.json: OK
internal/status/testdata/status-v1.golden.json: OK
internal/task/next.go: OK
internal/task/next_test.go: OK
pi/test/status-fixture.json: OK
```

### R2

Each command printed nothing and exited 0.

```console
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/docs/status-contract.md docs/status-contract.md
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/internal/cli/task.go internal/cli/task.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/internal/cli/task_show_next_test.go internal/cli/task_show_next_test.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/internal/routing/brief3112_test.go internal/routing/brief3112_test.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/internal/seatcommit/policy.go internal/seatcommit/policy.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/internal/status/classify.go internal/status/classify.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/internal/status/schema.go internal/status/schema.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/internal/status/testdata/status-fleet-v1.golden.json internal/status/testdata/status-fleet-v1.golden.json
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/internal/status/testdata/status-v1.golden.json internal/status/testdata/status-v1.golden.json
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/internal/task/next.go internal/task/next.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/internal/task/next_test.go internal/task/next_test.go
$ cmp /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/pi/test/status-fixture.json pi/test/status-fixture.json
```

### R2b

```console
$ cmp <(sed '/^## Rows/q' /Users/omri/amq-squad-lead/T/s2s3/inputs-s31/docs/v3-spike/330-s3-1-next/README.md) <(sed '/^## Rows/q' docs/v3-spike/330-s3-1-next/README.md) && echo "the note above ## Rows is the input"
the note above ## Rows is the input
```

### R3

```console
$ git diff --numstat "$(git merge-base HEAD origin/v3)"
24	0	docs/status-contract.md
30	1	internal/cli/task.go
2	0	internal/routing/brief3112_test.go
10	10	internal/seatcommit/policy.go
1	0	internal/status/classify.go
5	0	internal/status/schema.go
39	2	internal/status/testdata/status-fleet-v1.golden.json
39	2	internal/status/testdata/status-v1.golden.json
3	3	pi/test/status-fixture.json
$ git status --porcelain --untracked-files=all
 M docs/status-contract.md
 M internal/cli/task.go
 M internal/routing/brief3112_test.go
 M internal/seatcommit/policy.go
 M internal/status/classify.go
 M internal/status/schema.go
 M internal/status/testdata/status-fleet-v1.golden.json
 M internal/status/testdata/status-v1.golden.json
 M pi/test/status-fixture.json
?? docs/v3-spike/330-s3-1-next/README.md
?? internal/cli/task_show_next_test.go
?? internal/task/next.go
?? internal/task/next_test.go
```

### R4

```console
$ go test ./internal/task/ -run '^(TestNextRows|TestNextNamesOnlyMethodsALeadHolderOrOwnerRuns|TestTheTaskRecordCarriesNoNext)$' -count=1 -v -timeout 4m
--- PASS: TestNextRows (0.00s)
--- PASS: TestNextNamesOnlyMethodsALeadHolderOrOwnerRuns (0.00s)
--- PASS: TestTheTaskRecordCarriesNoNext (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/task	3.422s
$ go test ./internal/task/ -run '^(TestNextRows|TestNextNamesOnlyMethodsALeadHolderOrOwnerRuns|TestTheTaskRecordCarriesNoNext)$' -count=1 -v -timeout 4m | grep -c -- '    --- PASS: TestNextRows/'
23
```

### R5

```console
$ go test ./internal/status/ -count=1 -timeout 8m
ok  	github.com/omriariav/amq-squad/v3/internal/status	238.329s
```

### R6

```console
$ go test ./internal/cli/ -run '^TestTaskShowJSONCarriesNextBesideTheTask$' -count=1 -v -timeout 4m
--- PASS: TestTaskShowJSONCarriesNextBesideTheTask (0.06s)
    --- PASS: TestTaskShowJSONCarriesNextBesideTheTask/no_--me:_every_role (0.00s)
    --- PASS: TestTaskShowJSONCarriesNextBesideTheTask/--me_the_holder (0.00s)
    --- PASS: TestTaskShowJSONCarriesNextBesideTheTask/--me_the_lead (0.00s)
    --- PASS: TestTaskShowJSONCarriesNextBesideTheTask/--me_another_seat (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.480s
```

### R7

```console
$ go test ./internal/task/ -count=1 -timeout 7m
ok  	github.com/omriariav/amq-squad/v3/internal/task	36.867s
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
ℹ duration_ms 43640.721375
```

The command exited 0.

### R9

```console
$ go vet ./internal/task/ ./internal/status/ ./internal/cli/
$ gofmt -l internal/task internal/status internal/cli
```

Each command printed nothing and exited 0.
