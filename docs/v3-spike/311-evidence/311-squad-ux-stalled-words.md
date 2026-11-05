# 3.1.1-3 follow-up: the stalled-at-dialog warning in the words of its record

Authority: the plan reviewer's ruling, 3.1.1. Branch `v3-311-ux3`, from
origin/v3 `2dcff7e2d799beda78923f8a6cfd39a6cf82a4c7`.

## The finding

The seat warning `seat_stalled_at_dialog` is computed from AMQ's
`.wake.dialog` marker in the seat's mailbox (internal/status screen.go,
around :239). AMQ's Codex hook writes the marker on a PermissionRequest and
clears it only on Stop, so the field records "a permission request was made
this turn and the turn has not ended". An auto-approved prompt keeps the
marker while the seat goes on working, and the screen said "it is stopped
at a harness dialog open for 3m0s; answer the dialog in its pane" for the
rest of the turn: a false positive seen on the night of 2026-11-04. The Go
code stays for 3.1.2.

## The change

By the rule that a plain line states only what its record holds, the words
for that warning (pi/extensions/lib/words.ts `WARNING_WORDS`) now say what
the field holds, composed from the recorded seat and the marker's age in the
detail:

```
    ! seat scribe asked for a permission 3m0s ago this turn; if it is not moving, check its pane
```

Below the rule the code and the detail stay verbatim
(`scribe: seat_stalled_at_dialog: a harness dialog has been open since ...`).
The warning functions now receive the seat's handle. README's team screen
and its seat-warnings line say the same; the 311-squad-ux note's warning
paragraph has a line on why.

## Two names v3 lacked words for

At `2dcff7e2` the words table's completeness row (pi/test/words.test.ts,
row 7) failed before this change: #871 added the merge train rule
`strict_required_status_checks_policy` (internal/mergetrain checks.go
`RuleStrictUpToDate`) and the journal entry kind `reconcile` (train.go
`reconcileBinding`: a merge not recorded because the task's binding no
longer matches, whether its head, its repository or PR binding with the same
SHA, or a merge the server reported) had no words. Both are mapped here, so the pi gate is green again. The strict
rule's line is the lead's remedy, since a seat branch is never rebased by
hand (the plan reviewer on #873): "the merge train stopped at PR #9: its
branch is behind its base; the train brings it up to date and waits for its
checks; if the train cannot, reset the task so a seat redoes it on the
current base"; the `reconcile` entry reads "skipped recording a merge because its task
binding no longer matches", not a head mismatch alone (Codex on #873). The
row 4 stop test pins the strict line and row 7 the reconcile words. Every other scan (waits, blocks, launch
codes, seat codes, warnings, relay names, decision kinds) found nothing
unmapped at the tip.

## Row and revert run

The row is pi/test/screen.test.ts "a seat stalled at a dialog and a keyless
pi seat are screen lines with what the seat is doing, not refusals": the
scribe seat's warning line is the new words, and no seat line says
"stalled" or "stopped at". pi/test/keys.test.ts's dismiss row follows the
words.

Revert run (the old words put back in words.ts, the row run, the file
restored):

| revert | result | the failing output's first line |
| --- | --- | --- |
| `seat_stalled_at_dialog` reads "it is stopped at a harness dialog open for 3m0s; answer the dialog in its pane" again | caught | `not ok 1 - a seat stalled at a dialog and a keyless pi seat are screen lines with what the seat is doing, not refusals` (actual: `    ! it is stopped at a harness dialog open for 3m0s; answer the dialog in its pane`) |

## Gates

At code head `9416ca67`:

- `go build ./... && go vet ./... && go test -count=1 ./...`: exit 0, 56 packages ok, 0 FAIL lines. Tail:

```
?   	github.com/omriariav/amq-squad/v3/scripts/redact-evidence	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/claude-probes	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/pi-r19	[no test files]
exit 0
```

- `cd pi && npm ci --offline --ignore-scripts && npm test`: exit 0. Tail:

```
ℹ tests 110
ℹ suites 0
ℹ pass 110
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 16083.628375
exit 0
```

- `make ci` (PyYAML venv on PATH): exit 0. Tail:

```
PERF pre-tool allow in a seat: p50 21.1 ms, p95 22.3 ms
PERF outside a seat: 60 runs, p50 5.6 ms, p95 6.2 ms, max 6.2 ms (budget p95 < 20 ms: met)
exit 0
```
