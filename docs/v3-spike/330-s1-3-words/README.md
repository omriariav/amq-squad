# 3.3.0 S1-3: state-contract reason words

The words test now reads the wait and block reasons from `pi/test/state-contract.json` and checks them against `WAIT_WORDS` and `BLOCK_WORDS` in both directions. The maps that retain their Go-source scans are `LAUNCH_WORDS`, `SEAT_DOING`, `TRAIN_RULE_WORDS`, `TRAIN_KIND_WORDS`, `RELAY_WORDS`, `DECISION_WORDS` and `ROUTINE_DECISIONS`, and `WARNING_WORDS`. The dispatched TIP and this tree's HEAD at the precheck are both `e1dceeec784c9d756ec8a7938c0503c51161abae`. The named lists are at `pi/test/words.test.ts:53-66`, the state-contract reader at `pi/test/words.test.ts:68-82`, the bidirectional comparison at `pi/test/words.test.ts:84-110`, the retained Go-source-map test at `pi/test/words.test.ts:177-215`, the three new contract tests at `pi/test/words.test.ts:217-239`, and the claim-refusal contract check at `pi/test/words.test.ts:241-253`.

## The two lists

`NO_WORDS_YET` records contract codes for which no words table has words yet:

```ts
const NO_WORDS_YET: { code: string; lists: ReasonList[]; why: string }[] = [
	{ code: "process_exited", lists: ["block", "wait"], why: "a seat's process exit (internal/scheduler GoneProcessExited); no table has words for it; S2" },
	{ code: "tool_unpinned", lists: ["block"], why: "the pin check's missing pin (internal/registry PinMissing); no table has words for it; S2" },
];
```

`NOT_IN_THE_CONTRACT` records word-map keys that are not state-contract reason codes:

```ts
const NOT_IN_THE_CONTRACT: { key: string; list: ReasonList; why: string }[] = [
	{ key: "", list: "wait", why: "not a code: the wait field is empty when a task only waits its turn" },
	{ key: "class_unknown", list: "block", why: "a Go string literal with no constant; S2" },
	{ key: "retries_exhausted", list: "block", why: "a Go string literal with no constant; S2" },
];
```

## The allowance

Its words are in `LAUNCH_WORDS`: `waitWords` and `blockWords` read a launch failure's code there, after their own table.

The waiting path reads its own table and then the launch table at `pi/extensions/lib/words.ts:109-115`:

```ts
	const f = own(WAIT_WORDS, r);
	if (f) return { line: `${t.id} waiting: ${f(t, c)}` };
	// a launch failure's code (internal/scheduler launchfail.go waitForSeat)
	const l = own(LAUNCH_WORDS, r);
	if (l) return { line: `${t.id} waiting: its seat's launch failed, ${l}; ${LAUNCH_RETRY}` };
	if (c.launch?.has(r)) return { line: `${t.id} waiting: its seat's launch failed (AMQ's reason is below the line); ${LAUNCH_RETRY}`, record: `${t.id} waits: ${clean(r, 120)}` };
	return { line: `${t.id} waiting`, record: `${t.id} waits: ${clean(r, 120)}` };
```

The blocked path reads its own table and then the launch table at `pi/extensions/lib/words.ts:343-365`:

```ts
	const f = own(BLOCK_WORDS, r);
	if (f) {
		const [happened, todo] = f(d);
		const record = r === "ledger_tampered" && d.block_detail ? `${t.id}: ${tamperRecord(d.block_detail)}` : undefined;
		return { line: `${t.id} blocked: ${happened}; ${todo}`, record };
	}
	const rec = launchBlock(d.block_detail, r);
	if (rec && !own(LAUNCH_WORDS, r)) {
		const at = own(LAUNCH_WORDS, rec.stage);
		return { line: `${t.id} blocked: its seat's launch failed twice${at ? `, ${at}` : ""} (AMQ's reason is below the line); ${LAUNCH_FIX}`, record: `${t.id} blocked: ${clean(r, 120)} at stage ${clean(rec.stage, 60)}` };
	}
	const l = own(LAUNCH_WORDS, r);
	if (l) return { line: `${t.id} blocked: its seat's launch failed twice, ${l}; ${LAUNCH_FIX}` };
	if (c.launch?.has(r)) return { line: `${t.id} blocked: its seat's launch failed twice (AMQ's reason is below the line); ${LAUNCH_FIX}`, record: `${t.id} blocked: ${clean(r, 120)}` };
	return { line: `${t.id} blocked; its reason is below the line`, record: `${t.id} blocked: ${clean(r, 120) || "no reason recorded"}` };
```

For `identity_incomplete`, the screen prints `  t9 waiting: its seat's launch failed, its AMQ identity is incomplete; the scheduler tries once more, then blocks it for the lead` and `  t9 blocked: its seat's launch failed twice, its AMQ identity is incomplete; fix the cause, then /reset it (or relaunch the roster)`.

## Staleness

A `NO_WORDS_YET` entry fails when its code leaves a contract list it names, gains a key in that list's own word map, or gains words in `LAUNCH_WORDS`. A `NOT_IN_THE_CONTRACT` entry fails when its key enters the contract list it names or leaves that list's own word map.

No row shows that the check compares sets rather than entry counts. No row exercises `Object.hasOwn` in the new check with a key named like an `Object.prototype` member.

## The JSON does not ship

The JSON does not ship. The runtime build stages the package with these lines at `scripts/build-pi-runtime.sh:91-93`:

```sh
  cp pi/package.json pi/package-lock.json "$stage/pi/"
  cp -R pi/extensions "$stage/pi/extensions"
  cp -R pi/preload "$stage/pi/preload"
```

The package is private at `pi/package.json:4`:

```json
  "private": true,
```

This search has empty output:

```console
$ git grep -n '"files"' HEAD -- pi/package.json
```

## Named for S2

S2 gives `process_exited` and `tool_unpinned` words in `pi/extensions/lib/words.ts`. It also gives `class_unknown` and `retries_exhausted` Go constants so they enter the generated contract.

This task does not edit the three comments in `pi/extensions/lib/words.ts` that still say the test reads Go reason constants. At `pi/extensions/lib/words.ts:4-7`:

```ts
// a task's own record) and never guessed (the 3.1.1-1 rule). ONE table maps
// every wait and block reason the scheduler and the task store can emit to
// its words (pi/test/words.test.ts reads Go's reason constants and fails on
// any reason the table does not map); a reason the table does not know at
```

At `pi/extensions/lib/words.ts:81-83`:

```ts
// WAIT_WORDS maps each wait reason of a pending task (internal/task
// class.go's Wait* and the scheduler's no_roster_seat) to its words, the
// text after "<id> waiting: ".
```

At `pi/extensions/lib/words.ts:165-167`:

```ts
// BLOCK_WORDS maps each block reason the scheduler, the commit step, the
// router and the task store write (the table words.test.ts checks against
// Go's constants) to what happened and what to do.
```

## Rows

The final matrix was one call of the driver. The exact command is repeated beside each row because every block below came from that single call.

### R0

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R0: the control, no edit
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 0
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✔ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 23
ℹ fail 0
the message(s):
```

### R1

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R1: the contract's block list gains a code no table knows
edit pi/test/state-contract.json:494 (after)
  before:       "entries": [
  added pi/test/state-contract.json:495:         {
  added pi/test/state-contract.json:496:           "code": "zz_row_code",
  added pi/test/state-contract.json:497:           "constant": "row.Made"
  added pi/test/state-contract.json:498:         },
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: contract to words: the block code "zz_row_code" of pi/test/state-contract.json is no key of BLOCK_WORDS, its words are not in LAUNCH_WORDS, and it is not on NO_WORDS_YET for block
$ go test ./internal/statecontract/ -run TestContractFileIsCurrent -count=1 -timeout 120s
exit 1
--- FAIL: TestContractFileIsCurrent
    contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/statecontract
FAIL
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R2

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R2: the contract's wait list gains a code no table knows
edit pi/test/state-contract.json:1149 (after)
  before:       "entries": [
  added pi/test/state-contract.json:1150:         {
  added pi/test/state-contract.json:1151:           "code": "zz_row_code",
  added pi/test/state-contract.json:1152:           "constant": "row.Made"
  added pi/test/state-contract.json:1153:         },
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✔ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: contract to words: the wait code "zz_row_code" of pi/test/state-contract.json is no key of WAIT_WORDS, its words are not in LAUNCH_WORDS, and it is not on NO_WORDS_YET for wait
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R3

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R3: BLOCK_WORDS gains a key the contract does not list
edit pi/extensions/lib/words.ts:168 (after)
  before: export const BLOCK_WORDS: Record<string, (d: BlockDetail) => [string, string]> = {
  added pi/extensions/lib/words.ts:169: 	zz_row_key: () => ["x", "y"],
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: words to contract: the BLOCK_WORDS key "zz_row_key" is no block code of pi/test/state-contract.json and is not on NOT_IN_THE_CONTRACT for block
$ git show HEAD:pi/extensions/lib/words.ts > pi/extensions/lib/words.ts
$ git diff --quiet -- pi/extensions/lib/words.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R4

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R4: WAIT_WORDS gains a key the contract does not list
edit pi/extensions/lib/words.ts:84 (after)
  before: export const WAIT_WORDS: Record<string, (t: TaskWords, c: WaitContext) => string> = {
  added pi/extensions/lib/words.ts:85: 	zz_row_key: () => "x",
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✔ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: words to contract: the WAIT_WORDS key "zz_row_key" is no wait code of pi/test/state-contract.json and is not on NOT_IN_THE_CONTRACT for wait
$ git show HEAD:pi/extensions/lib/words.ts > pi/extensions/lib/words.ts
$ git diff --quiet -- pi/extensions/lib/words.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R5

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R5: a launch code of both lists loses its LAUNCH_WORDS entry
edit pi/extensions/lib/words.ts:283 (delete)
  before: 	identity_incomplete: "its AMQ identity is incomplete",
  after: (the line is removed)
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✖ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 20
ℹ fail 3
the message(s):
✖ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
  AssertionError [ERR_ASSERTION]: launch code identity_incomplete has no words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: contract to words: the block code "identity_incomplete" of pi/test/state-contract.json is no key of BLOCK_WORDS, its words are not in LAUNCH_WORDS, and it is not on NO_WORDS_YET for block
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: contract to words: the wait code "identity_incomplete" of pi/test/state-contract.json is no key of WAIT_WORDS, its words are not in LAUNCH_WORDS, and it is not on NO_WORDS_YET for wait
$ git show HEAD:pi/extensions/lib/words.ts > pi/extensions/lib/words.ts
$ git diff --quiet -- pi/extensions/lib/words.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R6

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R6: NO_WORDS_YET loses tool_unpinned
edit pi/test/words.test.ts:58 (delete)
  before: 	{ code: "tool_unpinned", lists: ["block"], why: "the pin check's missing pin (internal/registry PinMissing); no table has words for it; S2" },
  after: (the line is removed)
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: contract to words: the block code "tool_unpinned" of pi/test/state-contract.json is no key of BLOCK_WORDS, its words are not in LAUNCH_WORDS, and it is not on NO_WORDS_YET for block
$ cmp <the test file as it was> pi/test/words.test.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R7

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R7: NO_WORDS_YET is stale: tool_unpinned gets a BLOCK_WORDS key
edit pi/extensions/lib/words.ts:168 (after)
  before: export const BLOCK_WORDS: Record<string, (d: BlockDetail) => [string, string]> = {
  added pi/extensions/lib/words.ts:169: 	tool_unpinned: () => ["x", "y"],
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: NO_WORDS_YET is stale: the block code "tool_unpinned" is now a key of BLOCK_WORDS; take block off its entry
$ git show HEAD:pi/extensions/lib/words.ts > pi/extensions/lib/words.ts
$ git diff --quiet -- pi/extensions/lib/words.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R7L

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R7L: NO_WORDS_YET is stale: tool_unpinned gets words in LAUNCH_WORDS
edit pi/extensions/lib/words.ts:247 (after)
  before: export const LAUNCH_WORDS: Record<string, string> = {
  added pi/extensions/lib/words.ts:248: 	tool_unpinned: "x",
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: NO_WORDS_YET is stale: the block code "tool_unpinned" now has its words in LAUNCH_WORDS; take block off its entry
$ git show HEAD:pi/extensions/lib/words.ts > pi/extensions/lib/words.ts
$ git diff --quiet -- pi/extensions/lib/words.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R8

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R8: NOT_IN_THE_CONTRACT loses class_unknown
edit pi/test/words.test.ts:64 (delete)
  before: 	{ key: "class_unknown", list: "block", why: "a Go string literal with no constant; S2" },
  after: (the line is removed)
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: words to contract: the BLOCK_WORDS key "class_unknown" is no block code of pi/test/state-contract.json and is not on NOT_IN_THE_CONTRACT for block
$ cmp <the test file as it was> pi/test/words.test.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R8w

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R8w: NOT_IN_THE_CONTRACT loses the empty wait key
edit pi/test/words.test.ts:63 (delete)
  before: 	{ key: "", list: "wait", why: "not a code: the wait field is empty when a task only waits its turn" },
  after: (the line is removed)
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✔ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: words to contract: the WAIT_WORDS key "" is no wait code of pi/test/state-contract.json and is not on NOT_IN_THE_CONTRACT for wait
$ cmp <the test file as it was> pi/test/words.test.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R9

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R9: NOT_IN_THE_CONTRACT is stale: the contract's block list gains class_unknown
edit pi/test/state-contract.json:494 (after)
  before:       "entries": [
  added pi/test/state-contract.json:495:         {
  added pi/test/state-contract.json:496:           "code": "class_unknown",
  added pi/test/state-contract.json:497:           "constant": "row.Made"
  added pi/test/state-contract.json:498:         },
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: NOT_IN_THE_CONTRACT is stale: the BLOCK_WORDS key "class_unknown" is now a block code of pi/test/state-contract.json; take it off the list
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R10

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R10: a not_reasons code (file_lock_conflict) gets a BLOCK_WORDS key
edit pi/extensions/lib/words.ts:168 (after)
  before: export const BLOCK_WORDS: Record<string, (d: BlockDetail) => [string, string]> = {
  added pi/extensions/lib/words.ts:169: 	file_lock_conflict: () => ["x", "y"],
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 21
ℹ fail 2
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: words to contract: the BLOCK_WORDS key "file_lock_conflict" is no block code of pi/test/state-contract.json and is not on NOT_IN_THE_CONTRACT for block
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
  AssertionError [ERR_ASSERTION]: the not_reasons code "file_lock_conflict" of pi/test/state-contract.json is a key of BLOCK_WORDS: it is no task's reason, so it has no words there
$ git show HEAD:pi/extensions/lib/words.ts > pi/extensions/lib/words.ts
$ git diff --quiet -- pi/extensions/lib/words.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R10w

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R10w: a not_reasons code (self_widen) gets a WAIT_WORDS key
edit pi/extensions/lib/words.ts:84 (after)
  before: export const WAIT_WORDS: Record<string, (t: TaskWords, c: WaitContext) => string> = {
  added pi/extensions/lib/words.ts:85: 	self_widen: () => "x",
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✔ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 21
ℹ fail 2
the message(s):
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: words to contract: the WAIT_WORDS key "self_widen" is no wait code of pi/test/state-contract.json and is not on NOT_IN_THE_CONTRACT for wait
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
  AssertionError [ERR_ASSERTION]: the not_reasons code "self_widen" of pi/test/state-contract.json is a key of WAIT_WORDS: it is no task's reason, so it has no words there
$ git show HEAD:pi/extensions/lib/words.ts > pi/extensions/lib/words.ts
$ git diff --quiet -- pi/extensions/lib/words.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R10c

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R10c: a not_reasons claim code (claim_offer_required) gets a BLOCK_WORDS key
edit pi/extensions/lib/words.ts:168 (after)
  before: export const BLOCK_WORDS: Record<string, (d: BlockDetail) => [string, string]> = {
  added pi/extensions/lib/words.ts:169: 	claim_offer_required: () => ["x", "y"],
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✖ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 20
ℹ fail 3
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: words to contract: the BLOCK_WORDS key "claim_offer_required" is no block code of pi/test/state-contract.json and is not on NOT_IN_THE_CONTRACT for block
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
  AssertionError [ERR_ASSERTION]: the not_reasons code "claim_offer_required" of pi/test/state-contract.json is a key of BLOCK_WORDS: it is no task's reason, so it has no words there
✖ claim refusal names are CLI-only and their plain words stay above the rule
  AssertionError [ERR_ASSERTION]: claim_offer_required is not a persisted block reason
$ git show HEAD:pi/extensions/lib/words.ts > pi/extensions/lib/words.ts
$ git diff --quiet -- pi/extensions/lib/words.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R11

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R11: the contract's schema string changes
edit pi/test/state-contract.json:2 (sub)
  before:   "schema": "amq-squad-state-contract/1",
  after:    "schema": "amq-squad-state-contract/2",
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✖ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 19
ℹ fail 4
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json has the schema "amq-squad-state-contract/2"; this test reads "amq-squad-state-contract/1" only
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json has the schema "amq-squad-state-contract/2"; this test reads "amq-squad-state-contract/1" only
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json has the schema "amq-squad-state-contract/2"; this test reads "amq-squad-state-contract/1" only
✖ claim refusal names are CLI-only and their plain words stay above the rule
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json has the schema "amq-squad-state-contract/2"; this test reads "amq-squad-state-contract/1" only
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R12

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R12: process_exited, a code of both lists, is excused for wait only
edit pi/test/words.test.ts:57 (sub)
  before: 	{ code: "process_exited", lists: ["block", "wait"], why: "a seat's process exit (internal/scheduler GoneProcessExited); no table has words for it; S2" },
  after:  	{ code: "process_exited", lists: ["wait"], why: "a seat's process exit (internal/scheduler GoneProcessExited); no table has words for it; S2" },
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: contract to words: the block code "process_exited" of pi/test/state-contract.json is no key of BLOCK_WORDS, its words are not in LAUNCH_WORDS, and it is not on NO_WORDS_YET for block
$ cmp <the test file as it was> pi/test/words.test.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R13

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R13: the contract has no not_reasons
edit pi/test/state-contract.json:1401 (sub)
  before:     "not_reasons": [
  after:      "not_reasons_x": [
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✖ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 19
ℹ fail 4
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.not_reasons is missing or empty
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.not_reasons is missing or empty
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.not_reasons is missing or empty
✖ claim refusal names are CLI-only and their plain words stay above the rule
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.not_reasons is missing or empty
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R14

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R14: NO_WORDS_YET is stale: tool_unpinned is excused for wait, where it is no code
edit pi/test/words.test.ts:58 (sub)
  before: 	{ code: "tool_unpinned", lists: ["block"], why: "the pin check's missing pin (internal/registry PinMissing); no table has words for it; S2" },
  after:  	{ code: "tool_unpinned", lists: ["block", "wait"], why: "the pin check's missing pin (internal/registry PinMissing); no table has words for it; S2" },
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✔ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: NO_WORDS_YET is stale: "tool_unpinned" is no wait code of pi/test/state-contract.json any more; take wait off its entry
$ cmp <the test file as it was> pi/test/words.test.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R15

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R15: NOT_IN_THE_CONTRACT is stale: BLOCK_WORDS loses class_unknown
edit pi/extensions/lib/words.ts:228 (delete)
  before: 	class_unknown: () => ["its class is not in the routing table", "add it again with a known class"],
  after: (the line is removed)
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: NOT_IN_THE_CONTRACT is stale: "class_unknown" is no key of BLOCK_WORDS any more; take it off the list
$ git show HEAD:pi/extensions/lib/words.ts > pi/extensions/lib/words.ts
$ git diff --quiet -- pi/extensions/lib/words.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R16

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R16: an entry of the contract's wait list has no code
edit pi/test/state-contract.json:1149 (after)
  before:       "entries": [
  added pi/test/state-contract.json:1150:         {
  added pi/test/state-contract.json:1151:           "code": "",
  added pi/test/state-contract.json:1152:           "constant": "row.Made"
  added pi/test/state-contract.json:1153:         },
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✖ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 19
ℹ fail 4
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.wait.entries has no code: {"code":"","constant":"row.Made"}
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.wait.entries has no code: {"code":"","constant":"row.Made"}
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.wait.entries has no code: {"code":"","constant":"row.Made"}
✖ claim refusal names are CLI-only and their plain words stay above the rule
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.wait.entries has no code: {"code":"","constant":"row.Made"}
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R17

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R17: the contract's wait list has no entries
edit pi/test/state-contract.json:1149 (sub)
  before:       "entries": [
  after:        "entries_x": [
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✖ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 19
ℹ fail 4
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.wait.entries is missing or empty
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.wait.entries is missing or empty
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.wait.entries is missing or empty
✖ claim refusal names are CLI-only and their plain words stay above the rule
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.wait.entries is missing or empty
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R18

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R18: a claim code leaves the contract's not_reasons
edit pi/test/state-contract.json:1411 (sub)
  before:         "code": "claim_offer_required",
  after:          "code": "claim_offer_required_x",
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✔ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✖ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ claim refusal names are CLI-only and their plain words stay above the rule
  AssertionError [ERR_ASSERTION]: claim_offer_required must stay in the not_reasons of pi/test/state-contract.json
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R19

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R19: the contract's block list has no entries
edit pi/test/state-contract.json:494 (sub)
  before:       "entries": [
  after:        "entries_x": [
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✖ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 19
ℹ fail 4
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.block.entries is missing or empty
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.block.entries is missing or empty
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.block.entries is missing or empty
✖ claim refusal names are CLI-only and their plain words stay above the rule
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: reasons.block.entries is missing or empty
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R20

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R20: an entry of the contract's block list has an empty code
edit pi/test/state-contract.json:494 (after)
  before:       "entries": [
  added pi/test/state-contract.json:495:         {
  added pi/test/state-contract.json:496:           "code": "",
  added pi/test/state-contract.json:497:           "constant": "row.Made"
  added pi/test/state-contract.json:498:         },
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✖ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 19
ℹ fail 4
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.block.entries has no code: {"code":"","constant":"row.Made"}
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.block.entries has no code: {"code":"","constant":"row.Made"}
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.block.entries has no code: {"code":"","constant":"row.Made"}
✖ claim refusal names are CLI-only and their plain words stay above the rule
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.block.entries has no code: {"code":"","constant":"row.Made"}
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R21

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R21: an entry of the contract's not_reasons has no code
edit pi/test/state-contract.json:1403 (delete)
  before:         "code": "file_lock_conflict",
  after: (the line is removed)
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✖ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 19
ℹ fail 4
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.not_reasons has no code: {"constant":"task.RefusalFileLock"}
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.not_reasons has no code: {"constant":"task.RefusalFileLock"}
✖ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.not_reasons has no code: {"constant":"task.RefusalFileLock"}
✖ claim refusal names are CLI-only and their plain words stay above the rule
  AssertionError [ERR_ASSERTION]: pi/test/state-contract.json: an entry of reasons.not_reasons has no code: {"constant":"task.RefusalFileLock"}
$ git show HEAD:pi/test/state-contract.json > pi/test/state-contract.json
$ git diff --quiet -- pi/test/state-contract.json
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R22

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R22: process_exited, a code of both lists, is excused for block only
edit pi/test/words.test.ts:57 (sub)
  before: 	{ code: "process_exited", lists: ["block", "wait"], why: "a seat's process exit (internal/scheduler GoneProcessExited); no table has words for it; S2" },
  after:  	{ code: "process_exited", lists: ["block"], why: "a seat's process exit (internal/scheduler GoneProcessExited); no table has words for it; S2" },
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✔ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: contract to words: the wait code "process_exited" of pi/test/state-contract.json is no key of WAIT_WORDS, its words are not in LAUNCH_WORDS, and it is not on NO_WORDS_YET for wait
$ cmp <the test file as it was> pi/test/words.test.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R23

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R23: NOT_IN_THE_CONTRACT loses retries_exhausted
edit pi/test/words.test.ts:65 (delete)
  before: 	{ key: "retries_exhausted", list: "block", why: "a Go string literal with no constant; S2" },
  after: (the line is removed)
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: words to contract: the BLOCK_WORDS key "retries_exhausted" is no block code of pi/test/state-contract.json and is not on NOT_IN_THE_CONTRACT for block
$ cmp <the test file as it was> pi/test/words.test.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

### R24

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all`

```text
=== R24: BLOCK_WORDS gains two keys the contract does not list, in one edit
edit pi/extensions/lib/words.ts:168 (after)
  before: export const BLOCK_WORDS: Record<string, (d: BlockDetail) => [string, string]> = {
  added pi/extensions/lib/words.ts:169: 	zz_row_b: () => ["x", "y"],
  added pi/extensions/lib/words.ts:170: 	zz_row_a: () => ["x", "y"],
$ cd pi && node --test --test-reporter=spec test/words.test.ts
exit 1
✔ row 7: every launch code, seat code, train rule and kind and relay name in Go's sources maps to plain words
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
✔ row 7: the state contract's wait reasons and WAIT_WORDS agree both ways
✔ row 7: no code of the state contract's not_reasons is a key of BLOCK_WORDS or WAIT_WORDS
✔ claim refusal names are CLI-only and their plain words stay above the rule
✔ row 7: a launch failure's code as a wait or block reason reads in words
✔ pin_unresolved uses the class-chain detail from task show
✔ row 7: the decision scan reads Kind past braces in strings, and only the decision's own
✔ row 7: every decision kind Go writes is a stop with words or a routine record
✔ row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop
✔ row 7: a stop followed by its resolving decision no longer shows
✔ row 7: a launch failure's block reads its guidance from the task's own record
✔ row 7: a reason named constructor or __proto__ renders the fallback line
✔ row 7: the size cap's numbers come from the recorded detail through task show
✔ row 7: the dependency-merge wait shows verbatim
✔ row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do
✔ row 7: a title with a canary and a control character shows redacted and stripped
✔ row 7: each empty block says so in one plain line
✔ row 4: every seat warning code in Go's list has plain words; the code is below the rule
✔ row 4: a seat's stop and the train's stop say what happened and what to do
✔ 0e: ledger compatibility words name the recorded writer and install command
✔ row 4: no line above the rule carries a snake_case code or a hex run; the code is below it
✔ row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule
ℹ tests 23
ℹ pass 22
ℹ fail 1
the message(s):
✖ row 7: the state contract's block reasons and BLOCK_WORDS agree both ways
  AssertionError [ERR_ASSERTION]: words to contract: the BLOCK_WORDS key "zz_row_a" is no block code of pi/test/state-contract.json and is not on NOT_IN_THE_CONTRACT for block
  words to contract: the BLOCK_WORDS key "zz_row_b" is no block code of pi/test/state-contract.json and is not on NOT_IN_THE_CONTRACT for block
$ git show HEAD:pi/extensions/lib/words.ts > pi/extensions/lib/words.ts
$ git diff --quiet -- pi/extensions/lib/words.ts
exit 0
$ git status --porcelain -- pi/test/state-contract.json pi/extensions/lib/words.ts
```

## What ran

`cd pi && npm ci --ignore-scripts --offline` exited 0; it added 118 packages and found 0 vulnerabilities.

`cd pi && node --test --test-reporter=spec test/words.test.ts` exited 0: `tests 23`, `pass 23`, `fail 0`, `skipped 0`.

`cd pi && npm test` exited 0: `tests 150`, `pass 149`, `fail 0`, `skipped 1`.

`cd pi && node scripts/lint.mjs` exited 0 with `lint ok: 32 files, pi 1.3.0`.

`go build ./...` exited 0 with no output.

`grep -cE 'goWaits|goBlocks|REASON_PACKAGES|REASON_LITERALS|NOT_BLOCKS' pi/test/words.test.ts` printed `0` and exited 1, as expected.

`grep -c 'assert.deepEqual(problems' pi/test/words.test.ts` printed `0` and exited 1, as expected.

`git diff --quiet -- pi/extensions/lib/words.ts pi/test/state-contract.json; echo $?` printed `0` and exited 0.

`bash ../../../../amq-squad-lead/T/s13/inputs/rows-w.sh all` exited 0; its 29 complete row transcripts are above.

`go run ./scripts/redact-evidence -check docs/v3-spike/330-s1-3-words/` exited 0 with no output.

`git diff --check` exited 0 with no output.

`grep -c '/Us''ers/' docs/v3-spike/330-s1-3-words/README.md` printed `0` and exited 1, as expected.

`grep -cE '[[:space:]]+$' docs/v3-spike/330-s1-3-words/README.md` printed `0` and exited 1, as expected.

`grep -cE '[[:space:]]+$' pi/test/words.test.ts` printed `0` and exited 1, as expected.

`git status --porcelain --untracked-files=all` printed ` M pi/test/words.test.ts` and `?? docs/v3-spike/330-s1-3-words/README.md`, the two declared paths.

`git diff --stat` exited 0 and reported `pi/test/words.test.ts`, with 90 insertions and 53 deletions in one file.
