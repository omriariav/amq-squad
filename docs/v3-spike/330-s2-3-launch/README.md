# 3.3.0 S2-3: the launch family's 54 codes as constants, on the block and wait lists, and pi's launch check on the contract

Base: `origin/v3` at `811096cafa8cd031c270621f164edb7eb6794ee7` (S2-2
merged). The plan reviewer's ruling 4 on the S2/S3 scope is the contract this
note reports against: option (a), the 54 launch constants on the block and
wait lists, their words staying in `LAUNCH_WORDS`, and pi's launch check
moved onto the contract and made two-way. Line cites are at that base. No
behaviour changes: every value written is the same string as before.

## What changes

- Four declarations, each value the literal it replaced, byte for byte:
  - `internal/seatexec/reasons.go` (new): 31 constants, `ReasonArgvMissing`
    to `ReasonWrongOwner`.
  - `internal/seatlaunch/reasons.go` (new): 16 constants, the launcher's
    stages, codes and seat states and the proof server's reply codes; and
    three outside the family (below): `ReasonProofConnectionsFull`,
    `ReasonProofOrigin`, `ReasonProofReplayed`.
  - `internal/piruntime/session.go`: 5 constants, the pi resume check's
    codes, beside `ResumeRefusal`.
  - `internal/scheduler/launchfail.go`: `ReasonLaunchError` and
    `ReasonNotStarted`, which `schedproc`, `scheduler` and `seatlaunch`
    write (`scheduler` is the one of the three the other two import).
- 98 sites name them: the 89 sites of the 54 values in
  `docs/v3-spike/330-s1-2b-reasons/sites.md` 5a, all of them, and nine more
  of the same field: the stage and state comparisons in
  `seatlaunch/report.go:9` (two), `seatlaunch/roster.go:246`, `:248`, `:255`,
  `:257` and `seatlaunch/launcher.go:265`, the proof server's
  `reject(m, peer, "proof_token_invalid")` (`seatlaunch/proof_unix.go:346`),
  and the `not_started` default in `scheduler/roster.go:401`, which reaches
  the wait door `param scheduler.firstNonEmpty#1` and would otherwise fail
  `TestNoLiteralRepeatsAListedReason`.
- Seven more sites name the three outside constants: the proof server's
  `reject` (`proof_unix.go:277`) and `forged` calls (`:350`, `:366`, `:369`,
  `:376`, `:379`, `:392`). 105 sites in all.
- `seatexec/seatexec_unix.go:274`: `refusef(firstNonEmpty(reply.Code,
  "proof_refused"), ...)` becomes `code := firstNonEmpty(reply.Code)` and an
  `if` that refuses with `ReasonProofRefused` when that is empty, and with
  the code otherwise: a blank code (`" \t"`) is no code, as before (Codex
  pass 1 found the first draft's `code == ""` let a blank code through).
  `seatexec.firstNonEmpty` is variadic, so its argument is no door the writer
  test folds; the constant now reaches `param seatexec.refusef#0`.
  `internal/seatexec/proof_test.go`'s `TestSeatExecRefusesWithoutAnAcceptedProof`
  gains two cases: a refusal with no code and one with a blank code each
  refuse with `ReasonProofRefused`.
- One string, one declaration: `seatexec.PiNotEmbedded =
  piruntime.ReasonNotEmbedded` and `seatlaunch.ReasonPiShimMissing =
  seatexec.PiNotEmbedded` (both were the literal `"pi_not_embedded"`).
- `internal/statecontract/reasons.go`:
  - the 54 on the block list and on the wait list, each with its writers;
    137 writer rows (the wait list has three more: `ReasonLaunchError` twice
    and `ReasonNotStarted` once at `param scheduler.firstNonEmpty#1`, a wait
    door only);
  - twelve doors added to both lists, the launch path's own: `field
    launch.ProofReply.Code`, `field piruntime.ResumeRefusal.Code`, `field
    scheduler.RosterLaunchError.Stage`, `field scheduler.RosterOutcome.Stage`,
    `field seatlaunch.Result.Stage`, `field seatlaunch.SeatState.ReasonCode`,
    `field seatlaunch.SeatState.State`, `field seatlaunch.proofSeat.refused`,
    `param seatexec.asRefusal#0`, `param seatlaunch.firstNonEmptyStr#1`,
    `result seatlaunch.launchFailure#0` and `#1`. With `param
    seatexec.refusef#0`, already a door of both, they are the family's 13
    doors;
  - `LaunchReasons()`: the family. Its door `scheduler.(LaunchFailure).Code`,
    its 13 doors, its 62 constants (the 54, and the 8 launch codes listed
    before S2-3: `claudeseat.TokenInsecure`, `scheduler.AMQBindingPresent`,
    `scheduler.ReasonResourcePresent`, `seatexec.ClaudeHooksMissing`,
    `seatexec.ClaudePermissionMismatch`, `seatexec.IdentityIncomplete`,
    `seatexec.PiNotEmbedded`, `seatexec.SandboxPinsMissing`), and the four
    words it keeps as literals, each with its guard (sites.md 5c):
    `pending` (`seatlaunch.WaitSeats`), `up`
    (`seatlaunch.(*Launcher).settleOne`), `prepared` (`seatlaunch.Launch`),
    `applied` (`seatlaunch.launchFailure`). And `Outside`: the 13 launch
    codes that never reach a task, each a constant on no block or wait list,
    which pi words with the family (the 8 seat-prepare refusals of
    `schedproc`, `piruntime.ReasonRejected` and
    `piruntime.RuntimeMismatchReason`, and the proof server's three), with
    their two `OutsideDoors`: `param seatlaunch.(*proofServer).forged#2` and
    `reject#2`.
- `internal/statecontract/contract.go`: `reasons.launch` in the contract;
  `pi/test/state-contract.json` regenerated.
- `internal/statecontract/launch_test.go` (new), `TestLaunchFamily`:
  - the count row: 54 S2-3 constants, 8 listed before, 62 in the family, 4
    kept words;
  - a row per constant: `s23LaunchLiterals` holds each constant beside the
    literal it replaced, and the code of each on the block list and on the
    wait list is that literal; no two share one;
  - the record: the values of sites.md 5a are the 54 literals and the 4 kept
    words (58), and the guarded words of 5c are the kept words;
  - each family door is a door of both lists, and the family's door is a
    declared function;
  - each kept word is guarded at a declared function, says why, is no listed
    code, and is written at a family door;
  - every constant used at a family door is in the family, and every S2-3
    constant is used at one;
  - no string literal reaches a family door but an empty one or a kept word,
    read twice: by the survey's literal sites, and by S2-2's folded
    expressions (a concatenation, parentheses, a conversion, one local), so
    `refusef("proof_" + "custom", ...)` fails (Codex pass 1);
  - `Outside`: no code of it is a block, wait or kept code; it is every
    entry of `s2NotTaskReasons` but the 11 of `s23NotLaunch` (seven router
    skip reasons, the lead lock's refusal, an environment variable's name, a
    permission mode, an audit sentence), and
    `piruntime.RuntimeMismatchReason`, of no family; 13 in all; no literal
    and no folded expression reaches an outside door, and a constant used at
    one is in the family or `Outside`.
- `internal/statecontract/reasons_test.go`: the survey runs once
  (`cachedReasonSurvey`) for `TestReasonsAreTiedToTheirWriters` and
  `TestLaunchFamily`.
- `internal/statecontract/s2_constants_test.go` (S2-2's finder):
  - `s2DeclaringPackages` adds `piruntime` and `seatlaunch` (ten packages):
    the lists now name constants of both;
  - `s2NotTaskReasons` adds six, 23 in all: three the walk now finds in a
    family, `piruntime.ReasonRejected` (amq-squad up's refusal of the pi
    runtime), `seatlaunch.ClaudePermissionMode` (a Claude seat's permission
    mode) and `seatlaunch.PiWakeAuditReason` (a pi seat's disabled-wake audit
    sentence), and the three new proof constants;
  - a family constant that a listed alias names, or that names a covered
    constant, is covered for the family rule as it was for the value rule
    (`piruntime.ReasonNotEmbedded`, `seatlaunch.ReasonPiShimMissing`);
  - `s2OtherVocabulary` (new, one entry): `registry.StateRefused = "refused"`
    is a registry entry's state, equal to `seatlaunch.ReasonRefused` by the
    word only; the value rule skips it, and the entry fails if it is listed,
    gone, or no longer equal to a listed code.
- `pi/test/words.test.ts`: the launch scrape (`LAUNCH_FILES`,
  `LAUNCH_LITERALS`, `LAUNCH_CONSTS`, `goLaunchCodes`, `literals`) is gone.
  The new row reads `reasons.launch`: 62 constants, 4 kept words and 13
  outside codes. Each family and outside code is a key of `LAUNCH_WORDS`, and
  so is each kept word but the two on `KEPT_WITHOUT_WORDS` (`applied` and
  `up`, the success stage and state, which the scrape left out too; Codex
  pass 1 found the first draft required words for no kept word); no outside
  code is a block or wait code; each key of `LAUNCH_WORDS` is a family code,
  a kept word or an outside code; a `KEPT_WITHOUT_WORDS` entry that is no
  kept word, or has words, fails. `words.ts` is unchanged.

## What is left as a literal

Five literals of the 54 values stay, each a value of another vocabulary at
no door:

- `seatexec/seatexec_unix.go:54`: `launch.Exec{Outcome: "refused"}`, the
  exec record's outcome; and `seatlaunch/run.go:483`, its comparison;
- `schedproc/process.go:311`: `DecideReply{Code: "seat_invalid"}`, the
  scheduler process's decide reply;
- `scheduler/roster.go:401`: `Decision{Kind: "seat_not_up"}`, a decision
  kind;
- `seatlaunch/proof_unix.go:418`: `launch.Alarm{Kind: "identity_forged"}`,
  an alarm's kind.

The plan reviewer ruled (2026-11-16) that these five stay literals in S2-3,
which is the launch family; they are named for 3.3.x as other vocabularies
(constants, or a declaration in S4).

## Why three proof codes are outside the family

`proof_origin`, `proof_replayed` and `proof_connections_full` have words in
`LAUNCH_WORDS` and pi's old scrape found them, but S1-2b's trace did not,
and it was right, so they are `Outside`, not in the family: `seatlaunch.(*proofServer).forged`
(`proof_unix.go:417-426`) writes its code into the alarm and the detail and
refuses with `identity_forged`, and `reject` (`:431`) writes its code into
the dropped-proof log. Neither reaches a reply's code.

## Pi's check, before and after

The old scrape found 79 codes at this base. Each is a family code (62), a
kept word (`pending`, `prepared`), an outside code (13), or a block code with
`BLOCK_WORDS` (`seat_head_detached_unmerged`,
`seat_replacement_needs_relaunch`, which the block row checks both ways). A
new launch code now fails in Go before pi: a literal or folded expression at
a family or outside door fails `TestLaunchFamily`, a constant used at one
fails it unless it is in the family or `Outside`, a new entry of
`s2NotTaskReasons` fails it unless it is `Outside` or on `s23NotLaunch`,
and a family or outside code fails pi unless `LAUNCH_WORDS` words it.

## Limits

- A launch code written at a door outside the 13 (a new field, a new helper's
  parameter) is not found until the door is declared. The scrape had the
  same limit for a site outside its patterns.
- The kept words' guards are read, not run (sites.md 5c); the test checks
  each guard is a declared function, not that it guards.
- A launch code of no reason family with a new value, in no list and at no
  declared door, is found by neither side; the scrape found one only if its
  name began with `Reason` in a scanned file.
- Codex pass 2 found no behaviour defect and three gaps in the static checks,
  left as limits (static guards are capped at three passes):
  - an expression of constants alone at a family door
    (`ReasonProofUnknown + ReasonProofUnknown` at `proof_unix.go:364`) is
    a new code no check finds: S2-2's folded expressions are those with a
    literal in them, and the writer survey does not widen through a binary
    expression;
  - the writer rows are a (constant, function, door) set, so a constant
    swapped for another the same function already writes at the same door
    (`ReasonCodexApprovalMissing` for `ReasonCodexSandboxMissing` at
    `seatexec_unix.go:336`) still matches; the per-constant rows check each
    value, not each site's choice;
  - S2-2's alias coverage takes any alias of a covered constant, so
    `seatlaunch.ReasonPiShimMissing` retargeted to another listed constant
    (`seatexec.ReasonRuntimeMismatch`) passes; its uses are error texts at
    no door.

## Rows

### R1

```sh
cd /Users/omri/amq-squad-lead/T/s2s3/inputs-s23 && shasum -a 256 -c MANIFEST.sha256
```

```text
docs/v3-spike/330-s2-3-launch/README.md: OK
internal/piruntime/session.go: OK
internal/schedproc/roster.go: OK
internal/scheduler/launchfail.go: OK
internal/scheduler/roster.go: OK
internal/seatexec/anchor_unix.go: OK
internal/seatexec/env.go: OK
internal/seatexec/pi.go: OK
internal/seatexec/proof_test.go: OK
internal/seatexec/reasons.go: OK
internal/seatexec/seatexec_unix.go: OK
internal/seatlaunch/compile.go: OK
internal/seatlaunch/launcher.go: OK
internal/seatlaunch/proof_unix.go: OK
internal/seatlaunch/reasons.go: OK
internal/seatlaunch/report.go: OK
internal/seatlaunch/roster.go: OK
internal/seatlaunch/run.go: OK
internal/statecontract/contract.go: OK
internal/statecontract/launch_test.go: OK
internal/statecontract/reasons.go: OK
internal/statecontract/reasons_test.go: OK
internal/statecontract/s2_constants_test.go: OK
pi/test/state-contract.json: OK
pi/test/words.test.ts: OK
```

### R2

```sh
grep -v ' docs/v3-spike/330-s2-3-launch/README.md$' /Users/omri/amq-squad-lead/T/s2s3/inputs-s23/MANIFEST.sha256 | while read -r sum path; do cmp "/Users/omri/amq-squad-lead/T/s2s3/inputs-s23/$path" "$path" || echo "DIFFERS $path"; done; echo "cmp done"
```

```text
cmp done
```

### R2b

```sh
cmp <(sed '/^## Rows/q' /Users/omri/amq-squad-lead/T/s2s3/inputs-s23/docs/v3-spike/330-s2-3-launch/README.md) <(sed '/^## Rows/q' docs/v3-spike/330-s2-3-launch/README.md) && echo "the note above ## Rows is the input"
```

```text
the note above ## Rows is the input
```

### R3

```sh
git diff --numstat "$(git merge-base HEAD origin/v3)"
```

```text
18	5	internal/piruntime/session.go
3	3	internal/schedproc/roster.go
11	1	internal/scheduler/launchfail.go
3	3	internal/scheduler/roster.go
11	11	internal/seatexec/anchor_unix.go
5	5	internal/seatexec/env.go
3	3	internal/seatexec/pi.go
13	0	internal/seatexec/proof_test.go
36	31	internal/seatexec/seatexec_unix.go
1	1	internal/seatlaunch/compile.go
5	5	internal/seatlaunch/launcher.go
21	20	internal/seatlaunch/proof_unix.go
1	1	internal/seatlaunch/report.go
7	7	internal/seatlaunch/roster.go
8	7	internal/seatlaunch/run.go
2	1	internal/statecontract/contract.go
279	0	internal/statecontract/reasons.go
15	2	internal/statecontract/reasons_test.go
47	11	internal/statecontract/s2_constants_test.go
1535	153	pi/test/state-contract.json
74	47	pi/test/words.test.ts
```

```sh
git status --porcelain --untracked-files=all
```

```text
 M internal/piruntime/session.go
 M internal/schedproc/roster.go
 M internal/scheduler/launchfail.go
 M internal/scheduler/roster.go
 M internal/seatexec/anchor_unix.go
 M internal/seatexec/env.go
 M internal/seatexec/pi.go
 M internal/seatexec/proof_test.go
 M internal/seatexec/seatexec_unix.go
 M internal/seatlaunch/compile.go
 M internal/seatlaunch/launcher.go
 M internal/seatlaunch/proof_unix.go
 M internal/seatlaunch/report.go
 M internal/seatlaunch/roster.go
 M internal/seatlaunch/run.go
 M internal/statecontract/contract.go
 M internal/statecontract/reasons.go
 M internal/statecontract/reasons_test.go
 M internal/statecontract/s2_constants_test.go
 M pi/test/state-contract.json
 M pi/test/words.test.ts
?? docs/v3-spike/330-s2-3-launch/README.md
?? internal/seatexec/reasons.go
?? internal/seatlaunch/reasons.go
?? internal/statecontract/launch_test.go
```

### R4

```sh
go test ./internal/statecontract/ -run 'TestLaunchFamily' -count=1 -v -timeout 4m
```

```text
    launch_test.go:373: launch family: 62 constants (54 S2-3, 8 before), 13 doors, 4 kept words, 13 outside at 2 doors
--- PASS: TestLaunchFamily (34.02s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	34.278s
```

```sh
git grep -n -E '"(apply_ambiguous|apply_failed|argv_missing|codex_approval_missing|codex_sandbox_missing|decisions_pending|dir_writable_by_others|env_needs_invalid|identity_forged|identity_mismatch|identity_proof_failed|identity_proof_timeout|launch_error|ledger_unreadable|mailbox_missing|mkdir_failed|no_proof|not_observed|not_started|path_invalid|pi_resume_context_lost|pi_session_id_invalid|pi_session_id_missing|pi_session_unreadable|prepare_refused|proof_expired|proof_ledger_failed|proof_malformed|proof_marker_failed|proof_origin_unknown|proof_refused|proof_ticket_consumed|proof_ticket_missing|proof_token_invalid|proof_unacknowledged|proof_unknown|provider_missing|record_invalid|record_missing|refused|resource_present|role_mismatch|role_missing|root_invalid|root_unreadable|rules_mismatch|rules_missing|runtime_mismatch|seat_invalid|seat_not_up|stat_failed|symlink_in_path|write_failed|wrong_owner)"' -- internal/seatexec internal/seatlaunch internal/piruntime internal/schedproc internal/scheduler ':!*_test.go' | grep -v -E '^[^:]+:[0-9]+:[[:space:]]+Reason[A-Za-z]+ += "'
```

```text
internal/schedproc/process.go:311:			return DecideReply{Code: "seat_invalid", Detail: err.Error()}
internal/scheduler/roster.go:401:			log(Decision{Kind: "seat_not_up", Seat: st.Handle, Reason: redact.Block(strings.TrimSpace(firstNonEmpty(st.ReasonCode, ReasonNotStarted) + ": " + st.Detail))})
internal/scheduler/scheduler.go:1282:	Refused []RefusedSeat `json:"refused"`
internal/seatexec/seatexec_unix.go:54:			state.Exec = &launch.Exec{At: o.Now().UTC(), Outcome: "refused", ReasonCode: r.Code, Detail: r.Detail}
internal/seatlaunch/proof_unix.go:419:	_ = launch.WriteAlarm(s.root, launch.Alarm{Kind: "identity_forged", Code: code, Handle: handle, LaunchID: s.launchID, Detail: detail, At: s.now().UTC()})
internal/seatlaunch/run.go:484:	if err == nil && rec.Exec != nil && rec.Exec.Outcome == "refused" && launchID != "" && rec.LaunchID == launchID && rec.RequestID == s.RequestID {
```

### R5

```sh
go test ./internal/statecontract/ -count=1 -v -timeout 7m
```

```text
--- PASS: TestContractFileIsCurrent (0.00s)
--- PASS: TestLaunchFamily (31.61s)
--- PASS: TestReasonsAreTiedToTheirWriters (0.00s)
--- PASS: TestEveryReasonConstantIsListed (0.00s)
--- PASS: TestNoLiteralRepeatsAListedReason (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/statecontract	46.886s
```

### R6

```sh
go test ./internal/seatexec/ -run 'TestSeatExecRefusesWithoutAnAcceptedProof' -count=1 -v -timeout 4m
```

```text
=== RUN   TestSeatExecRefusesWithoutAnAcceptedProof
--- PASS: TestSeatExecRefusesWithoutAnAcceptedProof (0.13s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/seatexec	0.459s
```

### R7

```sh
go test ./internal/seatexec/ -count=1 -timeout 7m
```

```text
ok  	github.com/omriariav/amq-squad/v3/internal/seatexec	8.429s
```

```sh
go test ./internal/seatlaunch/ -count=1 -timeout 7m
```

```text
ok  	github.com/omriariav/amq-squad/v3/internal/seatlaunch	21.397s
```

```sh
go test ./internal/piruntime/ -count=1 -timeout 7m
```

```text
ok  	github.com/omriariav/amq-squad/v3/internal/piruntime	2.434s
```

```sh
go test ./internal/scheduler/ -count=1 -timeout 7m
```

```text
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	21.344s
```

```sh
go test ./internal/schedproc/ -count=1 -timeout 7m
```

```text
ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	29.344s
```

### R8

```sh
make pi-test NPM_FLAGS=--offline
```

```text
✔ row 7: the state contract's launch family and LAUNCH_WORDS agree both ways (2.006875ms)
ℹ tests 156
ℹ pass 155
ℹ fail 0
```

### R9

```sh
go vet ./internal/statecontract/ ./internal/seatexec/ ./internal/seatlaunch/ ./internal/piruntime/ ./internal/scheduler/ ./internal/schedproc/
```

No output; exit 0.

```sh
gofmt -l internal/statecontract internal/seatexec internal/seatlaunch internal/piruntime internal/scheduler internal/schedproc
```

No output; exit 0.
