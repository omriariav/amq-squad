# A1-2: the one open gate

## What changed

A bare `yes` or `no` outside every thread answers the one open gate when its question is the latest thing asked in the DM: no other gate question or re-ask, and no lead reply that may end in a question, was posted at or after it. The answer must be dated neither before that question nor after this host's clock. Otherwise the existing re-ask and refusal paths remain. The worktree's HEAD at the precheck was `482b6c72dfdeedaff6bf8a707eac62b72d0b1505`.

The core exposes the one-call hook at `internal/gateverify/core.go:39-45`, distinguishes a bare answer at `internal/gateverify/core.go:178-190`, accepts only a thread-only rejection before a re-ask at `internal/gateverify/core.go:213-223`, and applies the event timestamp and no-thread bounds at `internal/gateverify/core.go:308-321`.

The relay records question endings in a sidecar whose suffix and failure code are at `internal/buzz/relay.go:77-83`. A failed sidecar write is attached to the posted result and surfaced by the relay step at `internal/buzz/relay.go:247-256` and `internal/buzz/relay.go:469-486`. The deterministic ending rule and sidecar reads and writes are at `internal/buzz/relay.go:489-545`. The shared-channel decision reads every served store and distinct served relay root, and fails closed on later questions, re-asks, open-count drift, unreadable records, in-flight posts, missing endings and question endings at `internal/buzz/relay.go:719-811`.

The handler lends and restores the hook for one verification at `internal/buzz/inbound.go:322-369`. The owner-visible CLI guidance is at `internal/cli/gate_store.go:36-36` and `internal/cli/verify_action.go:686-686`. The former one-gate behavior pin now asserts direct acceptance at `internal/buzz/plain311_test.go:15-33`; the handler matrix is at `internal/buzz/a12_test.go:19-434`, and the core matrix is at `internal/gateverify/a12_test.go:11-104`.

The owner documentation states the rule at `README.md:604-616` and `docs/v3-buzz/README.md:159-169`. The A0 record is extended at `docs/v3-spike/330-a0-sender-auth/README.md:377-379`. `internal/status/relay.go` is unchanged: a new field in its strict 3.2.0 relay record would make rollback binaries refuse every touched request, so the compatibility flag is the non-`.json` endings sidecar.

The precheck was:

```text
$ git status --porcelain --untracked-files=all
$ git rev-parse HEAD
482b6c72dfdeedaff6bf8a707eac62b72d0b1505
$ git diff --stat 482b6c72dfdeedaff6bf8a707eac62b72d0b1505 HEAD -- internal/gateverify internal/gatestore internal/buzz internal/status/relay.go internal/cli/gate_store.go internal/cli/verify_action.go internal/cli/operator.go internal/cli/remote.go internal/routing/router.go internal/routing/table.go README.md README.html docs/v3-buzz/README.md docs/v3-spike/330-a0-sender-auth docs/v3-spike/330-a1-2-one-gate
```

## The rule, sentence by sentence

| the rule's part | rows | result |
| --- | --- | --- |
| a bare yes or no outside every thread binds the one open gate | R2 R9 R10 C4 | pass |
| counted across every served store | R4 U1 G1b | pass |
| no other gate's question or re-ask was posted at or after its question | G1 G1b S2 | pass |
| no lead reply that may end in a question was posted at or after it, in any served root | R5 R6 R15 R16 R18 S1 G2 | pass |
| a post whose ending is not known reads as a question | R13 R14 R7 F1 | pass |
| the answer is not older than the question, equal seconds bind, and it is not after this host's clock | R8 R9 R17 C3 C4 C8 | pass |
| two or more open, or none, work as before | R1 R3 R4 | pass |
| a typed tuple, and an answer after the gate's re-ask, work as before | R11 R12 C5 C6 | pass |
| the bound answer passes the same checks as a thread answer | I1 | pass |
| the relay record keeps 3.2.0's keys and the endings file is not `*.json` | K1 | pass |
| the reply keeps today's form | R2 R6 R10 | pass |

## Known differences

1. A lead reply ends in a question when, after its trailing characters that are not a letter, a digit or a question mark are cut, its last character is `?`, `？` (U+FF1F) or `؟` (U+061F): `**Shall I go on?**`, `Shall I go on? 🙂` and `Go on?.` read as questions; a reply whose last question mark is followed by a letter or a digit (`Is it? It is.`, `v2?3`) does not, and neither does a question asked with no question mark (`Tell me if I should go on.`). No model reads the text.
2. A lead reply, or another gate's question or re-ask, posted in the same second as the gate's question counts as later: the answer is re-asked.
3. The endings file `<request id>.endings` sits beside the relay record and no reader of the records reads it: a 3.2.x binary relays as before. A lead reply recorded by 3.2.x has no entry: when it was posted at or after an open gate's question, an unthreaded answer to that gate is re-asked; a gate raised after the upgrade is not affected.
4. A failed write of the endings file does not stop the post: the post stands, the relay step names `relay_endings_failed` with the request id, and an unthreaded answer after it is re-asked.
5. The gate's question time is this host's clock (the body signs it); the answer's time is the owner's device clock. A device clock behind this host makes a fresh answer read as older than the question, and it is re-asked; a device clock ahead of this host makes it read as after this host's clock, and on the unthreaded path it is not bound but re-asked (a thread reply keeps the 60 s allowance). What remains is an answer typed before the question whose delivery was delayed by more than the device clock's lead: it reads as after the question, and the timestamp cannot tell it apart.
6. No test pins the words of the two CLI strings of section 4; the gate compares them with this brief character for character.
7. `docs/v3-buzz/README.md` still quotes the pre-3.1.1 words "Reply in this thread with one line:" and "Reply here to confirm"; they are corrected in the lead's next docs PR.
8. No handler row reaches the open count of `unthreadedBinds`: the routing refuses two open gates first (`bareGate`, `bareCore`). Row U1 calls the check directly.
9. One unreadable relay record, or one gate record that does not load, in any served root re-asks every unthreaded answer until it is repaired; status names the unreadable record.

## Rows

### B0

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh B0
row B0
buzz exit 0, subtests passed 27
gateverify exit 0, subtests passed 8
failed: none
as the brief says: yes (every subtest passes)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V1

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V1
row V1
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 24
gateverify exit 0, subtests passed 8
failed: G1 G1b S2
named: G1 G1b S2
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V2

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V2
row V2
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 0, subtests passed 8
failed: G2
named: G2
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V3

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V3
row V3
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 0, subtests passed 8
failed: R13
named: R13
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V4

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V4
row V4
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 0, subtests passed 8
failed: R14
named: R14
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V5

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V5
row V5
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 0, subtests passed 8
failed: U1
named: U1
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V6

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V6
row V6
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 22
gateverify exit 0, subtests passed 8
failed: G2 R16 R18 R5 S1
named: R5 R16 S1 G2 R18
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V7

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V7
row V7
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 0, subtests passed 8
failed: R7
named: R7
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V8

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V8
row V8
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 0, subtests passed 8
failed: R15
named: R15
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V9

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V9
row V9
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 0, subtests passed 8
failed: S2
named: S2
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V10

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V10
row V10
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 0, subtests passed 8
failed: F1
named: F1
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V11

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V11
row V11
edit: internal/buzz/relay.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 0, subtests passed 8
failed: S1
named: S1
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V12

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V12
row V12
edit: internal/gateverify/core.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 1, subtests passed 7
failed: R8 C3
named: R8 C3
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V13

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V13
row V13
edit: internal/gateverify/core.go: 1 replacement
buzz exit 1, subtests passed 23
gateverify exit 1, subtests passed 6
failed: I1 R10 R15 R9 C2 C4
named: R9 C4
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V14

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V14
row V14
edit: internal/gateverify/core.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 1, subtests passed 7
failed: R12 C6
named: R12 C6
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V15

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V15
row V15
edit: internal/gateverify/core.go: 1 replacement
buzz exit 1, subtests passed 25
gateverify exit 1, subtests passed 7
failed: R11 R12 C5
named: R11 C5
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V16

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V16
row V16
edit: internal/buzz/inbound.go: 1 replacement
buzz exit 1, subtests passed 21
gateverify exit 0, subtests passed 8
failed: I1 R10 R15 R2 R6 R9
named: R2 R6 R9 R10 R15 I1
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

### V17

```text
$ bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V17
row V17
edit: internal/gateverify/core.go: 1 replacement
buzz exit 1, subtests passed 26
gateverify exit 1, subtests passed 7
failed: R17 C8
named: R17 C8
as the brief says: yes (every named row failed)
restore: internal/buzz/relay.go cmp exit 0
restore: internal/gateverify/core.go cmp exit 0
restore: internal/buzz/inbound.go cmp exit 0
restore: git status as before the row: cmp exit 0
restore: keep directory removed: yes
```

## What ran

The seat ran these commands; every block this section names appears above under `## Rows`. The seat did not run whole `internal/cli`, `go test ./...`, or repository-wide `make ci`.

```text
go build ./...                                                        exit 0
go vet ./internal/gateverify/ ./internal/buzz/ ./internal/cli/        exit 0
gofmt -l internal/gateverify internal/buzz internal/cli               exit 0; no output
go test ./internal/gatestore/ -count=1 -timeout 240s                  exit 0; ok
go test ./internal/gateverify/ -count=1 -timeout 240s                 exit 0; ok
go test ./internal/status/ -count=1 -timeout 240s                     exit 0; ok, 119.240s
go test ./internal/buzz/ -count=1 -timeout 240s                       exit 0; ok, 99.642s
go test ./internal/cli/ -count=1 -timeout 240s -run '^(TestTheLeadWitnessesABareYesRepliedToThePlanPost|TestTypedGateCLIRoundTripOnTheStore)$'   exit 0; ok
grep -rn 'answered only by\|signed Buzz\|Resolve with: the owner' internal --include='*_test.go'   exit 1; no output
git diff --stat -- internal/status/relay.go                           exit 0; no output
make readme-html                                                      exit 0; regenerated README.html
make readme-html-check                                                exit 0; README.html is in sync with README.md
python3 ../../../../amq-squad-lead/T/a12/inputs/cites.py docs/v3-spike/330-a1-2-one-gate/README.md   exit 0; last line: cites 18, short 0
go run ./scripts/redact-evidence -check docs/v3-spike/330-a1-2-one-gate/   exit 0; no output
go run ./scripts/redact-evidence -check docs/v3-spike/330-a0-sender-auth/   exit 0; no output
git diff --check                                                      exit 0; no output
git diff --no-index --check /dev/null internal/buzz/a12_test.go       exit 1; no output
git diff --no-index --check /dev/null internal/gateverify/a12_test.go exit 1; no output
git diff --no-index --check /dev/null docs/v3-spike/330-a1-2-one-gate/README.md   exit 1; no output
git diff --numstat -- docs/v3-spike/330-a0-sender-auth/README.md      exit 0; 4, 0 and the path
git diff -U0 -- docs/v3-spike/330-a0-sender-auth/README.md | grep -c '^@@'   exit 0; 1
grep -cE '/Us''ers/|/priv''ate/tmp|\.rows-a12''-keep' docs/v3-spike/330-a1-2-one-gate/README.md docs/v3-spike/330-a0-sender-auth/README.md   exit 1; each 0
grep -cE '[[:space:]]+$' docs/v3-spike/330-a1-2-one-gate/README.md    exit 1; 0
bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh B0          exit 0; block B0 above
bash ../../../../amq-squad-lead/T/a12/inputs/rows-a12.sh V1 through V17, each separately   exit 0; blocks V1 through V17 above
git status --porcelain --untracked-files=all                          exit 0; the thirteen declared paths, ten modified and three new
```

The seat ran the two row tests by name through the driver and the four named package runs; the whole `internal/cli` run, locked `make ci`, and `make docs-html-check` are the lead's on the PR head.
