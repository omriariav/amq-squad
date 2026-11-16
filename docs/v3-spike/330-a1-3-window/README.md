# A1-3: the window re-ask

This record describes the worktree's HEAD at the precheck. A gate whose answer window lapses is asked again once in the same Buzz thread. The re-ask creates a new window, and an answer dated at or before that re-ask approves nothing.

## What changed

- `internal/gatestore/store.go` stores the one window re-ask in `windows/<id>.json`, derives its end from the signed re-ask time and the gate's original window length, publishes it once, and checks the verified window again under the gate lock before commit.
- `internal/gateverify/verify.go` verifies the exact signed window-reask text, accepts the question, history re-ask, or window re-ask as thread anchors, and distinguishes `gate_lapsed` from `answered_before_reask` and `outside_window`.
- `internal/gateverify/core.go` publishes a valid stored window re-ask before judging another event, checks its history, retries once when the stored window changes, and leaves a failed publish unhandled for redelivery.
- Buzz, hook context, lead anchors, the verifier consumers, uninstall, and the store temp sweep all read the same window state. `gatestore.TopDirs` is the directory source for both the sweep and uninstall retention.

## The rule, sentence by sentence

| Row | Result | Rule exercised |
| --- | --- | --- |
| W1 | passed | A late in-thread answer creates one window re-ask; the trigger and same-second answer approve nothing; a later root reply approves and every consumer agrees. |
| W2 | passed | A gate presented after the original end is re-asked; a reply to the window re-ask approves. |
| W3 | passed | An answer created inside the first window but delivered after the re-ask is refused; a fresh answer approves. |
| W4 | passed | The new window ends at re-ask time plus the original window length; a second lapse stays outside the window and creates no chain. |
| W5 | passed | A late answer outside the thread is `not_in_gate_thread`; another thread is `wrong_gate_thread`; neither posts. |
| W7 | passed | The question, history re-ask, and window re-ask remain valid anchors on one gate. |
| W8 | passed | The trigger event is always refused even when its device time is ahead of the host. |
| W9 | passed | An accepted answer and every later event are consumed across the history re-ask. |
| W10 | passed | An invalid stored window re-ask opens nothing and is never published. |
| W11 | passed | A failed window publish is retried by redelivery of the trigger, using the same event id. |
| W12 | passed | A fresh answer publishes a stored window re-ask before it is judged. |
| W13 | passed | An edited window re-ask is refused as `question_altered`. |
| W14 | passed | A history re-ask copied into `windows/` is not a window re-ask. |
| W15 | passed | A window re-ask stored before commit prevents the stale verified answer from committing. |
| W16 | passed | A window re-ask stored after the first read is used on the bounded second pass. |
| W17 | passed | A decided gate cannot reserve a window re-ask. |
| H1 | passed | The handler receipt names the window re-ask and a bare reply to it approves. |
| H2 | passed | With two squads, bare and typed answers route through the correct squad's window re-ask. |
| H3 | passed | A failed publish is not recorded handled, and redelivery publishes the same re-ask. |
| K1 | passed | Hook context and lead anchors use the derived window end and window re-ask id. |
| S1 | passed | The store reserves one window re-ask, marks publication, derives state, and returns every re-ask id. |
| S2 | passed | Temp recovery includes `windows/` without removing the stored re-ask. |
| S3 | passed | Every store writer creates exactly the directories in `gatestore.TopDirs`. |
| T1 | passed | Reply-table receipts distinguish lapsed, answered-before-reask, and outside-window. |
| U4 | passed | Uninstall without purge keeps `windows/` with the sealed gate store. |
| V | passed | The verifier returns the decided code for every late-input class. |

## Codes that change

| Input | Before | After |
| --- | --- | --- |
| A thread answer presented after `not_after`, created inside it | `outside_window` | `gate_lapsed`, then re-asked |
| A thread answer created after `not_after` | `outside_window` | `gate_lapsed`, then re-asked |
| A late answer outside every thread | `outside_window` | `not_in_gate_thread`, nothing posted |
| A late answer in another thread | `outside_window` | `wrong_gate_thread` |
| A late answer with a bad question record or another squad's tag | `outside_window` | the record's `bad_gate_question` or `no_squad_identity` code |
| A late thread answer whose window re-ask cannot publish | none | `reask_publish_failed`, not recorded handled |
| An answer dated at or before the window re-ask | none | `answered_before_reask` |
| Created before `not_before` | `outside_window` | unchanged |
| Created over 60 seconds in the future | `created_in_future` | unchanged |
| The R55 team door's window | `outside_window` | unchanged |

## Known differences

1. The new window rests on a host-written file (`windows/<id>.json`), as the record's own not_after already does; the owner's signed in-thread answer still binds the gate, so R32 is unchanged.
2. The answer must be dated strictly after the window re-ask, by the owner's device clock against this host's: a device clock behind this host by more than the time the owner takes to reply makes a fresh answer read as before the re-ask; it is refused `answered_before_reask`, consumes nothing, and the owner answers again (Q4, no allowance: the trigger itself is dated seconds before its re-ask).
3. One window re-ask per gate: an answer after the asked-again window is `outside_window` as before, and the gate stays lapsed.
4. A late answer outside every thread, or in another thread, now gets `not_in_gate_thread` or `wrong_gate_thread` instead of `outside_window`, and one with a bad question record gets that record's code: the thread and the question are checked before the window.
5. `gate status` keeps its `reasked` field for the thread re-ask only; a window re-asked gate shows as open until its new end, with no field of its own.
6. The history check of an answer after a window re-ask starts from the question (or the history re-ask), as before: a deletion since the question whose target is hidden still re-asks with the question as raised.
7. A history re-ask's trigger, delivered again before any answer, can still approve (since is the history re-ask): unchanged by this task (Q5); it is A1-3c, a separate small brief after this one merges.
8. The new window is derived from the window re-ask's signed created_at, set when it is reserved. If its first publish fails, the trigger is refused `reask_publish_failed` and is not recorded as handled; serve delivering it again, or the next owner answer that names the gate and is handled after the reserve (in its thread or outside it, whatever its verdict), publishes the same event before anything is judged. Only when no owner event for the gate arrives, and serve does not deliver the trigger again, is the window re-ask never published: `gate status` and the hook context show the gate open, the owner's phone shows no new question, and the new window runs out (stranded open). An in-thread answer created after the reserve approves once the publish succeeds.
9. The window re-ask's trigger id, its publish mark and the window file itself are host-written. A same-UID process that alters them (a changed `trigger_event_id`, a deleted window file beside its old mark, a window re-ask dated before not_after by the 60 s allowance) can make a late answer the owner signed for this gate count, never an answer the owner did not sign; this is the threat model of known difference 1. Such a change between an answer's two passes can also give it a second `ErrWindowChanged` (F11): that second refusal returns `error`, which is acceptable, because it is bounded (one re-run), fails closed (nothing is committed), and needs host tampering. `gate status` and the hook context read the window file's created_at without checking its signature; the core and every consumer check it.
10. The hook context counts an accepted gate by its issuer envelope alone (`hookAnswerVerified`, unchanged): it does not re-read the window file, as it never re-ran the owner's signature check. `Verifier.Accepted`, which the train, the release flow, verify action and growth use, does.
11. The thread re-ask and the history re-ask have no history check of their own (unchanged); the window re-ask does.
12. Without `windows/<id>.json` a consumer refuses an approval dated or verified after the gate's original not_after; an approval dated and verified inside the original window still verifies without it, because it is valid under that window (the envelope does not name the window re-ask; Q7).
13. A rollback to 3.2.0 ignores every window re-ask: nothing in 3.2.0 reads `windows/` (its gate store's directory scans are `IDs`, `internal/gatestore/store.go:278-295`, which reads `gates/`, and `sweepTemps`, `:739-762`, whose list at `:742` is gates, answers, denials, reasks, consumed/gates, consumed/events; its `State`, `:313-321`, expires on the record's not_after). A gate asked again and still unanswered shows `expired`; an approval accepted through a window re-ask was created after the original not_after, so every 3.2.0 consumer, which re-runs `Verify` against the original window (`internal/gateverify/answer.go:197`, `verify.go:169-179`), refuses it: it fails closed, while `gate status` shows it `accepted`. The installed 3.2.0 binary read a store written by this change (two gates, one approved through a window re-ask, one asked again and open) with no error: `amq-squad gate status --project <tmp> --session rb1` printed `rb-approved accepted` and `rb-open expired`, exit 0 (this change's build prints `rb-open open`). No other 3.2.0 reader walks the extension tree: the hook context reads `gates/` and `answers/` by name (`internal/cli/hook_store.go:54`, `:95`), status reads `gates/<id>.json` (`internal/cli/status.go:111`), the guard protects the whole extension directory by prefix (`internal/cli/hook_guard_env.go:150`, so `windows/` is protected too), the one 3.2.0 walk that can enter a store is the worktree residue check, which reads a seat worktree's own `.agent-mail` and keeps any file it has no canonical copy of (`internal/worktreeplan/residue.go:28`, `:44-69`), so a `windows/` file is kept as a `reasks/` file is, and `uninstall` classifies its entries by name (`internal/uninstall/plan.go:83`, `:230-237`), where `windows/` is session state.
14. pi's first-turn host line (`pi/extensions/lib/firstturn.ts:110`, `:202-203`) reads a hand-copied list of gate-store directories without `windows/` and counts expiry from the record's not_after, so a gate asked again after its window can show as expired on that line until its cache key changes; the screen's waiting list, status, the core and every consumer read the window re-ask. Fixed by window-pi (#1016), before the 3.3.0 rc.
15. Two owner events for the same gate handled at the same time can each publish the stored window re-ask: `retryWindowPublish` (`internal/gateverify/core.go:459`) checks the publish mark and `publishWindow` publishes and then marks it, as separate steps. Both publish the same signed event, the same id, which is idempotent at the relay. This predates window-fix (Codex on #1018).

## Not in this task

A1-3c, as the brief named it: "the history re-ask's trigger, re-delivered before any answer, can still approve; A1-3c, its own brief after this one merges" (known difference 7); the plan reviewer moved A1-3c to 3.3.x on 2026-11-16, and known difference 7 stands for 3.3.0. A second window re-ask or a chain; a sweep at the lapse or the raiser's retry posting it (3.3.x if wanted); an envelope change; `gate status`'s fields; the history check's start; any change to the typed tuple, the team door, the receipt's words beyond the two codes.

## Rows

### Precheck

```text
git status --porcelain --untracked-files=all
(no output)

git rev-parse HEAD
1992061fe0486bf9fb24c60d7a3a0cc170d0e314

git diff --stat f56af9c8b07239fa5a52910e0c42f20df180ba59 HEAD -- internal/gatestore internal/gateverify internal/buzz internal/cli/hook_store.go internal/cli/lead_start.go internal/cli/hook_a13_test.go internal/cli/merge_train.go internal/mergetrain internal/uninstall docs/v3-buzz/README.md docs/v3-spike/330-a0-sender-auth docs/v3-spike/330-a1-3-window
(no output)
```

### R1

```text
INW=/Users/omri/amq-squad-lead/T/a13/inputs-window; git apply --check "$INW/product.diff"
(no output; exit 0)
```

### R2

After intent-to-add entries for the five new files:

```text
git diff --numstat "$(git merge-base HEAD origin/v3)"
3	0	docs/v3-buzz/README.md
4	0	docs/v3-spike/330-a0-sender-auth/README.md
316	0	docs/v3-spike/330-a1-3-window/README.md
146	0	internal/buzz/a13_test.go
16	2	internal/buzz/handler_test.go
15	8	internal/buzz/inbound.go
2	2	internal/buzz/relay.go
50	0	internal/cli/hook_a13_test.go
11	0	internal/cli/hook_store.go
2	2	internal/cli/lead_start.go
141	0	internal/gatestore/a13_test.go
1	0	internal/gatestore/record.go
155	19	internal/gatestore/store.go
592	0	internal/gateverify/a13_test.go
9	1	internal/gateverify/answer.go
128	6	internal/gateverify/core.go
1	1	internal/gateverify/core_test.go
5	1	internal/gateverify/growth.go
68	12	internal/gateverify/verify.go
13	1	internal/uninstall/plan.go
12	2	internal/uninstall/uninstall_test.go
```

### R3

```text
go test ./internal/gatestore/ ./internal/gateverify/ -count=1 -timeout 4m
ok  	github.com/omriariav/amq-squad/v3/internal/gatestore	15.538s
ok  	github.com/omriariav/amq-squad/v3/internal/gateverify	39.454s
```

### R4

```text
go test ./internal/buzz/ -count=1 -timeout 6m
ok  	github.com/omriariav/amq-squad/v3/internal/buzz	116.840s
```

### R5

```text
go test ./internal/cli/ -run '^(TestA13HookContextReadsTheWindowReask|TestHungVerificationIsInTheStatusAttentionQueue|TestRehearsalThroughServe)$' -count=1 -v -timeout 4m
=== RUN   TestA13HookContextReadsTheWindowReask
--- PASS: TestA13HookContextReadsTheWindowReask (0.06s)
=== RUN   TestHungVerificationIsInTheStatusAttentionQueue
--- PASS: TestHungVerificationIsInTheStatusAttentionQueue (0.47s)
=== RUN   TestRehearsalThroughServe
--- PASS: TestRehearsalThroughServe (0.30s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/cli	1.272s
```

### R6

```text
go vet ./internal/gatestore/ ./internal/gateverify/ ./internal/buzz/ ./internal/cli/
(no output; exit 0)

gofmt -l internal
(no output; exit 0)

go build ./...
(no output; exit 0)
```

### R7: V1-V32

Each command below was run separately from the repository root. Every row restored the changed file byte-for-byte and printed `rows with a difference: 0`.

```text
python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V1
row V1	store.go: State reads the window re-ask	failed: TestA13ASecondLapseIsOutsideWindow TestA13WindowReaskInTheStore TestA13WindowReaskThroughTheCoreAndTheTrain	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V2
row V2	store.go: ReaskEventIDs names the window re-ask	failed: TestA13HookContextReadsTheWindowReask TestA13PresentedAfterAndAReplyToTheWindowReask TestA13TheWindowReaskNamesItsSquad TestA13WindowReaskAtTheHandler TestA13WindowReaskInTheStore	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V3
row V3	verify.go: a reply may name the window re-ask	failed: TestA13PresentedAfterAndAReplyToTheWindowReask	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V4
row V4	verify.go: strictly after the window re-ask	failed: TestA13WindowReaskThroughTheCoreAndTheTrain	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V5
row V5	verify.go: the trigger never approves	failed: TestA13ATriggerAheadOfTheClockNeverApproves	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V6
row V6	verify.go: the asked-again window ends	failed: TestA13ASecondLapseIsOutsideWindow	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V7
row V7	verify.go: a late thread answer is gate_lapsed, not outside_window	failed: TestA13ATriggerAheadOfTheClockNeverApproves TestA13VerifyCodes TestA13YesterdaysAnswer	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V8
row V8	verify.go: presented after not_after is gate_lapsed	failed: TestA13PresentedAfterAndAReplyToTheWindowReask TestA13VerifyCodes TestConformanceRules TestHandlerReplyTable	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V9
row V9	core.go: gate_lapsed posts the window re-ask	failed: TestA13PresentedAfterAndAReplyToTheWindowReask TestA13WindowReaskAtTheHandler TestA13WindowReaskThroughTheCoreAndTheTrain TestA13YesterdaysAnswer	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V10
row V10	core.go: one window re-ask per gate	failed: TestA13ABadWindowReaskOpensNothing	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V11
row V11	core.go: bareGate names the window re-ask	failed: TestA13PresentedAfterAndAReplyToTheWindowReask TestA13WindowReaskAtTheHandler	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V12
row V12	answer.go: the consumer re-verifies with the window re-ask	failed: TestA13BothReasksOnOneGate TestA13PresentedAfterAndAReplyToTheWindowReask TestA13WindowReaskThroughTheCoreAndTheTrain	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V13
row V13	growth.go: Accepted loads the window re-ask	failed: TestA13BothReasksOnOneGate TestA13PresentedAfterAndAReplyToTheWindowReask TestA13WindowReaskThroughTheCoreAndTheTrain	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V14
row V14	inbound.go: answered_before_reask names the window re-ask	failed: TestHandlerReplyTable	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V15
row V15	inbound.go: bareCore names the window re-ask	failed: TestA13TheWindowReaskNamesItsSquad	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V16
row V16	inbound.go: coreFor names the window re-ask	failed: TestA13TheWindowReaskNamesItsSquad	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V17
row V17	relay.go: the gate threads hold the window re-ask	failed: TestA13WindowReaskAtTheHandler	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V18
row V18	lead_start.go: the gate anchors hold the window re-ask	failed: TestA13HookContextReadsTheWindowReask	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V19
row V19	hook_store.go: the hook context reads the window re-ask	failed: TestA13HookContextReadsTheWindowReask	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V20
row V20	core.go: an unpublished window re-ask is published before the verdict	failed: TestA13AFailedWindowPublishIsRetriedBeforeTheAnswer TestA13AFailedWindowPublishIsRetriedByTheTrigger	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V21
row V21	core.go: only a window re-ask that verifies is published	failed: TestA13ABadWindowReaskOpensNothing TestA13AThreadReaskIsNoWindowReask	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V22
row V22	core.go: the window re-ask's own history is checked	failed: TestA13AnEditedWindowReaskRefusesTheAnswer	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V23
row V23	verify.go: only the window re-ask's own text opens a window	failed: TestA13AThreadReaskIsNoWindowReask	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V24
row V24	store.go: the commit refuses a window re-ask reserved since the verifier read	failed: TestA13AWindowReaskBeforeTheCommitStopsIt	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V25
row V25	core.go: a reserve that finds another handler's window re-ask judges against it	failed: TestA13AWindowReaskAfterTheReadIsUsed	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V26
row V26	inbound.go: a failed window re-ask publish is not recorded as handled	failed: TestA13AFailedWindowPublishIsRetriedThroughTheHandler	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V27
row V27	core.go: a refused commit is judged again once	failed: TestA13AWindowReaskBeforeTheCommitStopsIt	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V28
row V28	core.go: the publish failure is typed reask_publish_failed	failed: TestA13AFailedWindowPublishIsRetriedThroughTheHandler	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V29
row V29	store.go: no window re-ask on a decided gate	failed: TestA13NoWindowReaskAfterAnApproval	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V30
row V30	plan.go: uninstall keeps windows/ with the gate store (G1)	failed: TestSealedLedgersAndEvidenceAreKeptByDefault	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V31
row V31	store.go: the temp sweep covers windows/ (G2)	failed: TestA13TheSweepCoversWindows	as named: yes
rows with a difference: 0

python3 /Users/omri/amq-squad-lead/T/a13/inputs-window/revert_a13w.py V32
row V32	store.go: TopDirs names every directory the store writes (structural)	failed: TestA13TheStoreWritesExactlyItsTopDirs TestA13TheSweepCoversWindows TestSealedLedgersAndEvidenceAreKeptByDefault	as named: yes
rows with a difference: 0
```

The final `git status --porcelain` matched the status captured before V1 exactly.

## What ran

- `go test ./internal/gatestore/ ./internal/gateverify/ -count=1 -timeout 4m`
- `go test ./internal/buzz/ -count=1 -timeout 6m`
- The three named `internal/cli` integration tests in R5
- `go vet` on `internal/gatestore`, `internal/gateverify`, `internal/buzz`, and `internal/cli`
- `gofmt -l internal`
- `go build ./...`
- All 32 revert rows V1 through V32, each separately, with every named failure observed and every file restored
