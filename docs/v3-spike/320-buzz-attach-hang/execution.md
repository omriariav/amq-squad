# 3.2.0-rc.2 Buzz attach hang release gate

Date: 2026-11-10

Verdict: **the rc.2 release gate closes with this follow-up to #946**, by the reviewer's ruling of 2026-11-10: (a) this note, (b) the Known limits sentence under "Release note", (c) the dry run and the rehearsal sending once more on a rejected row. The cause is a race inside amq-remote (finding 3). It is not fixed here and not worked around in the product.

## Evidence provenance and exclusions

The t142 carry was applied mechanically from `<lead-tmp>/t142-attempt1/`. Its `tracked.diff` SHA-256 was `1b61ab9d78021db00ff916f216505660cf8e3c0323bea3dd75a9a175cdcec7d7`; the saved execution note was `b6152863709eb290ecfa852b81f3bdbd5b52a2300df8dca8c63921a859a8121a`; and `count.sh` was `68241245d23d3ed399634df6e55b77a11de83b58cb857d3fb0449d7dea2b29be`. These values were re-measured with `shasum -a 256` in this task. The carried folder contained 61 files before this task's new captures.

This replacement carried `c5ed5d53` with patch SHA-256 `52bc0e1c69da77b48f9b86e01a75394f613818910f5fac43d215794eddb869a2`; its 100 files had zero blob mismatches and produced tree `b849293ee8930053b603a797f7dc187b3ec9b5f4` on the dispatch base.

Twelve of this folder's twenty tables have a `log` column. [`base-120-t141/B.tsv`](base-120-t141/B.tsv) has none and no log of its rows is kept; it is the first excluded observation below. The seven tables under [`lead-probe/`](lead-probe/) have no `log` column; each of their rows has its log beside the table, named `<table>-<run>.log`.

Three observations are excluded from behavioral counts:

- The saved t141 [`B.tsv`](base-120-t141/B.tsv) 0/10 result stopped at `online while the lead runs` because scratch commands inherited the live seat's AMQ identity. It measures test isolation, not the remote-attach answer hang.
- [`base-120/B-1.log`](base-120/B-1.log) exited during package setup in 0 seconds because the sandbox denied the Go build cache. It never ran the test.
- [`after/D-1.companion.log`](after/D-1.companion.log) deliberately retained the live seat identity and the companion refused harness-only tests. The main row passed, but the companion exit 1 makes it invalid as condition D.

The two 314-second identity-pin rows in [`base-120-host/B.tsv`](base-120-host/B.tsv) are not evidence that a 120-second wait ran for 314 seconds. The package performed roughly 194 seconds of other test/build work before the owner test entered its bounded 120-second wait.

The repository redactor was run over the whole evidence directory. A follow-up identity audit replaced eight live-seat values in the two saved host logs: pinned-root references on lines 33, 264, and 466 of each of `base-120-host/B-1.log` and `B-2.log` (six replacements), plus the `AM_SESSION` value on line 264 of each log (two replacements). The placeholders are `<live-amq-root>` and `<live-session>`; test-owned temporary roots were retained.

## Count tables

[`count.sh`](count.sh) records exit, seconds, load before and after, named hang point, companion exit, and log filename. Its evidence budget accepts 2 through 5 minutes; the ordinary default remains five minutes. With no third argument, its output goes to `${TMPDIR:-/tmp}/amq-squad-attach-hang-count`, outside this committed evidence folder.

| condition | table | pass / total | result |
| --- | --- | ---: | --- |
| A, carried post-fix | [`after/A.tsv`](after/A.tsv) | 10 / 10 | 30–32 s |
| B, carried post-fix | [`after/B.tsv`](after/B.tsv) | 10 / 10 | 43–44 s |
| D, earlier carried/diagnostic | [`after-loaded/D.tsv`](after-loaded/D.tsv), [`diagnostic/D.tsv`](diagnostic/D.tsv) | 9 / 12 | three answer hangs; every companion exited 0 |
| D, structured capture plus answer-again | [`capture/D.tsv`](capture/D.tsv) | 6 / 9 | rows 1, 2, and 9 hung at `the attach verifies the thread answer`; every companion exited 0 |
| A, final default behavior | [`final/A.tsv`](final/A.tsv) | 5 / 5 | 30–35 s, zero hangs |
| B, final default behavior | [`final/B.tsv`](final/B.tsv) | 5 / 5 | 43–45 s, zero hangs |

Condition C remains lead-owned under the addendum. D stopped immediately after the third captured hang, at row 9. Load did not separate the outcomes: both passes and hangs occurred across overlapping load averages.

## Finding 1: live-seat identity leaked into scratch AMQ

`AM_ME`, `AM_ROOT`, `AM_BASE_ROOT`, `AM_ROOT_ID`, `AM_BASE_ROOT_ID`, and `AM_SESSION` reached scratch AMQ commands and production seams inherited by the owner dry run. That pinned a throwaway root to the live seat session and produced the false B baseline hang.

The helper now removes all six names, and owner-dry-run processes scrub them before creating scratch state. The regression seeds all six names. The controlled revert was:

```text
go test ./internal/buzz -run '^TestTestsExecAMQOnlyThroughTheHelper$' -count=1
```

It failed at `phone_test.go:958` once for each variable, beginning with `the helper leaked AM_ME` and ending with `the helper leaked AM_SESSION`. Restoring the scrub makes the focused suite pass.

Classification: test isolation, in scope and fixed; a class (b) change (test-only), accepted by the reviewer on #945's packet, 2026-11-10.

## Finding 2: the fake relay had a replay/live loss window

The old fake websocket relay queried stored events and installed its live subscription in separate critical sections. An event published between them belonged to neither replay nor live delivery. The new helper snapshots replay and installs the subscription under the store lock. The trace records open/close, authentication, storage, replay, subscription, and live delivery.

The controlled revert restored the two critical sections and ran:

```text
go test ./internal/buzz -run '^TestRelayReplayAndLiveSubscriptionHaveNoLossWindow$' -count=1
```

It failed with `the stored replay and live subscription are not one critical section`, printed as `relay_test.go:337` in that round's numbering; as of `b7663f14` the assertion is at `relay_test.go:430` (re-measured under "Lines re-measured by the lead" below). Restoring the atomic section makes the focused test pass.

Classification: fake-relay behavior, in scope and fixed; a class (b) change (test-only), accepted by the reviewer on #945's packet, 2026-11-10. This can explain the earlier S3 route-delivery stop, but it does not explain the three newly captured answer hangs because their answers were visibly delivered live.

## Finding 3: a race inside amq-remote refuses an admission in flight

Classification: **(c), AMQ `amq-remote` 0.116.2**. The race is also measured on v0.138.0, released 2026-11-10 (the rows below), and the same code is in v0.134.0 (read, not run). No vendored patch and no product change is made here. [`upstream-draft.md`](upstream-draft.md) is the report, drafted for the owner to send or not.

### The mechanism (read in the AMQ source at v0.116.2)

1. Admission commits the request's record as Dispatching under the endpoint lock, releases the lock, and only then calls the adapter's Submit: `internal/remote/core/admission.go:267` (`e.mu.Unlock()`), `:271` (`t.att.Submit(...)`). The second admission path has the same shape (the unlock at `:344`, Submit at `:349`).
2. `amq-remote serve` runs a ticker at `--poll`, default 500 ms (`cmd/amq-remote/serve.go:65`, the loop at `:226-237`). Each tick reconciles every open record. A Dispatching record goes to `reconcileLive` (`internal/remote/core/reconcile.go:128-129`), which asks the adapter's Lookup (`:472`). Nothing marks an admission as in flight.
3. The mailbox adapter's Lookup, finding no journal entry for the request, writes one, `entry{Undelivered: "its delivery never started"}` (`internal/remote/mailbox/mailbox.go:342`; its comment at `:323-325`: "A request with no entry gets a tombstone first, so a Submit that has not run yet delivers nothing"), and answers that there is no evidence. Reconcile then refuses the record.
4. The Submit from step 1, arriving after the tick, finds that entry and answers `native_error` with "Not delivered to <handle>: its delivery never started. Nothing reached its inbox; send it again." (`mailbox.go:726-727`).

So a tick that lands between the unlock and the Submit refuses a request that was about to be delivered. The rate depends on the poll interval and on how long that window stays open, which host load stretches.

v0.134.0 (read): the unlock at `admission.go:268` and Submit at `:272`; the Dispatching case at `reconcile.go:115-116` and the Lookup at `:453`; the tombstone at `mailbox.go:347`; the 500 ms default at `serve.go:71`.

v0.138.0 (read, then measured, below): the unlock at `admission.go:268` and Submit at `:272`; the Dispatching case at `reconcile.go:115-116` and the Lookup at `:453`; the tombstone at `mailbox.go:348`; the 500 ms default at `serve.go:71`. On AMQ's main at 71ef0f47 (2026-11-10) `reconcile.go` and `mailbox.go` are byte for byte v0.138.0's (compared, not run). An upgrade to v0.138.0 does not close the race.

amq-squad starts the endpoint as `amq-remote up --root R --registry ...` with no `--poll` (`internal/buzz/endpoint.go:108`), so the default applies.

### Measured (the lead's runs, 2026-11-10, under the gate lock)

Tables and logs: [`lead-probe/`](lead-probe/). Every row is `TestOwnerScriptDryRun` on c5ed5d53's tests, alone (condition A, no companion load), with the 120 s evidence budget. The first table is amq-remote 0.116.2; the v0.138.0 rows are in the second. The 5 ms rows run the real binary through a wrapper that adds `--poll 5ms` to `up` and `serve` ([`lead-probe/poll-5ms-wrapper.sh`](lead-probe/poll-5ms-wrapper.sh)), put first on PATH through `AMQ_SQUAD_TEST_AMQ_BIN`. Three tables use temp edits to the tests that were never committed ([`lead-probe/temp-probe-edits.diff`](lead-probe/temp-probe-edits.diff)): a trace line with the content of each kind 9 and 40003 row, and an owner who sends again. The logs went through `scripts/redact-evidence` (no value replaced; `-check` exit 0). Times are in [`lead-probe/run-times.txt`](lead-probe/run-times.txt).

| table | poll | the test | runs | hung | what the rows show |
| --- | --- | --- | ---: | ---: | --- |
| [`poll5ms.tsv`](lead-probe/poll5ms.tsv), 21:29-21:36 IST | 5 ms | unmodified | 5 | 3 | hang points `serve delivers the answer into the attach inbox` twice and `serve delivers the route` once; each hung run has the journal line `undelivered="its delivery never started"` with empty `created` and `message_id` |
| [`content5ms.tsv`](lead-probe/content5ms.tsv), 21:39-21:45 IST | 5 ms | content trace | 3 | 3 | in each hung run the body's kind 9 one second after the owner's message reads `amq-squad: rejected (native_error)`, the whole content |
| [`contentbase.tsv`](lead-probe/contentbase.tsv), 21:45 IST | 500 ms | content trace | 1 | 0 | 31 s |
| [`resend5ms.tsv`](lead-probe/resend5ms.tsv), 21:48-21:50 IST | 5 ms | content trace, the owner sends again | 4 | 0 | the first answer was not delivered within 4 s in runs 1, 3 and 4 (this probe's trigger was a timer; the content trace prints only on a failure); the second answer was delivered each time, handled once, outcome approved, reply `accepted rel-dry tag`, the answer file read; 33-36 s |

On 0.116.2 at 5 ms without load, 6 of the 8 runs that did not send again hung. At the default 500 ms the rows of this folder are: conditions A and B (the dry run alone, and the whole package's run), 30 of 30 passing ([`after/A.tsv`](after/A.tsv), [`after/B.tsv`](after/B.tsv), [`final/A.tsv`](final/A.tsv), [`final/B.tsv`](final/B.tsv)); condition D (a `go test ./...` beside the dry run), 3 of 9 hung ([`capture/D.tsv`](capture/D.tsv)), and 3 of 12 in the earlier D rows. No rate is claimed for a real host: the rate is a property of the host's load.

#### v0.138.0 (the owner's question of 2026-11-10: is it fixed in a later AMQ?)

Measured, not read. `amq`, `amq-remote` and `amq-owner-sign` were built from the module at v0.138.0 into a temp directory (`go install ...@v0.138.0`, with `-ldflags "-X main.version=0.138.0"` so the test's floor check reads the version), never installed on the host, and put first on PATH through `AMQ_SQUAD_TEST_AMQ_BIN`. Each log's lines 2 to 4 print the three binaries as v0.138.0. The 5 ms rows use [`lead-probe/poll-5ms-wrapper-138.sh`](lead-probe/poll-5ms-wrapper-138.sh). The same tests, the same temp edits, the same budget.

| table | poll | the test | runs | failed | what the rows show |
| --- | --- | --- | ---: | ---: | --- |
| [`base138.tsv`](lead-probe/base138.tsv), 22:13-22:14 IST | 500 ms | unmodified | 1 | 0 | 31 s |
| [`poll138.tsv`](lead-probe/poll138.tsv), 22:14-22:18 IST | 5 ms | unmodified | 5 | 1 | run 5 hung at `serve delivers the answer into the attach inbox`, with the journal line `undelivered="its delivery never started"` and empty `created` and `message_id` ([`poll138-5.log`](lead-probe/poll138-5.log) line 179); runs 1 to 4 passed in 30-32 s |
| [`resend138.tsv`](lead-probe/resend138.tsv), 22:18-22:23 IST | 5 ms | content trace, the owner sends again (the dry run's two sends only) | 4 | 2 | below |

The four `resend138` runs:

- Run 1 ([`resend138-1.log`](lead-probe/resend138-1.log)): the answer was refused (the rejected row at 20.373 s, line 910) and delivered on the second send; the route was refused (26.374 s, line 936) and delivered on the second send; then the rehearsal's `@cto owner45 ping from the phone`, which this probe does not send again, was refused (37.527 s, line 1003) and the run hung at `the routed message`. Three journal tombstones.
- Run 2 ([`resend138-2.log`](lead-probe/resend138-2.log)): the answer and the route were delivered on the first send; the rehearsal's ping was refused (29.382 s, line 549) and the run hung at `the routed message`.
- Run 3 ([`resend138-3.log`](lead-probe/resend138-3.log)): the answer was not delivered within 4 s on the first send and again on the second, and was delivered on the third; handled once, outcome approved; the run passed in 39 s. (The probe's trigger is a timer and a passing run prints no content trace, so the two rejected rows are not in the log.)
- Run 4: no send was repeated; 31 s.

So v0.138.0 has the race: at 5 ms a request was refused in 4 of the 9 runs (poll138 run 5, resend138 runs 1 and 2 by the rejected row; resend138 run 3 by the timer). The samples are too small to compare rates between the two versions (0.116.2: 3 of 5 unmodified runs hung; v0.138.0: 1 of 5). One send needed a third attempt.

What a refused request looks like, and a delivered one, from the content trace (0.116.2):

- Refused ([`lead-probe/content5ms-1.log`](lead-probe/content5ms-1.log) lines 323 and 326): the owner's answer is stored at 19.247 s; the body's kind 9 at 20.247 s reads `amq-squad: rejected (native_error)`. No later row follows for that request. A refused route is in [`lead-probe/content5ms-3.log`](lead-probe/content5ms-3.log) lines 347 and 350.
- Delivered (`content5ms-3.log` lines 327 to 344): kind 9 `amq-squad: dispatching`, then kind 40003 `amq-squad: running`, then kind 40003 `amq-squad: completed` with the reply.

### What the D captures of this folder show, read again

- The body's kind 9 at 36.717 s in [`capture/D-9.log`](capture/D-9.log) (lines 926-927), 0.7 s after the owner's answer at 35.986 s, is by the rows above the terminal rejected row, not a running row. The D captures did not record content.
- The journal line, the free lock, and the empty `new` and dead-letter directories in D-1, D-2 and D-9 are the tombstone of step 3 and stand as recorded.
- The endpoint stopping at about 90 seconds was this repository's test fixture, not amq-remote. The rehearsal wrote the operator's presence once (`rehearsal_test.go:129`); presence is stale after 90 s (`internal/state/state.go:19`); attach then applies not-present and stops the endpoint (`internal/cli/remote.go:636`). `capture/D-9.log` lines 1624-1625 are attach's own output: presence `started`, state `online`, at 18:28:07Z, and presence `stopped`, state `stale`, at 18:29:37Z. This PR fixes the fixture (the presence refresh, below).
- The evidence-only second answer in those rows was sent after the two-minute wait, so after the endpoint had been stopped, and after the capture had sent SIGQUIT to the serve. Its "not handled" result measured a stopped endpoint. It is withdrawn; on a live endpoint sending again works (`resend5ms.tsv`: the second send each time; `resend138.tsv`: the second send twice and the third once).
- No serve stack was captured for those rows: the test-owned process had no reachable serve child at snapshot time, and the capture does not signal a process by name.

The `edit waits for the clock to pass the previous edit's second` line is correlated, not causal. It appears in D2 and D9 for an earlier ingest, while the later owner answer is demonstrably delivered; S3 also hung without the line.

## Real-owner impact

When the race hits, amq-remote refuses the owner's answer (or routed message) before anything is delivered.

- The owner is told. Within about a second the body posts, in the thread under the owner's message, a row whose whole content is `amq-squad: rejected (native_error)` (1.0 s in `lead-probe/content5ms-1.log`, lines 323 and 326). The sentence "send it again" that amq-remote builds does not reach the thread in 0.116.2.
- Nothing was delivered. The request's journal entry is the tombstone, with no message id; the attach inbox and the dead-letter directories stay empty; the gate records no outcome and stays open.
- Sending the same answer again works. On a live endpoint the second answer was delivered, handled once, the outcome approved and the answer file written, in each of the 3 runs whose first answer was not delivered; all 4 runs of that table ran to the end (`lead-probe/resend5ms.tsv`). No run produced two outcomes.
- The endpoint does not go away. The stop at about 90 seconds in the D captures was the test fixture's stale presence (Finding 3).

Not measured: a rate on a real phone and relay. A second answer can in principle meet the same race; in these rows none did (0 of 3).

## Changes retained

- Scratch AMQ identity scrubbing and regression coverage.
- Atomic replay/live setup in the fake relay, including a deterministic publish immediately after the replay lock is released.
- Failure-only relay trace and structured journal/lock/mailbox/up capture.
- Test-owned serve SIGQUIT when safely reachable; an explicit diagnostic when it is not.
- A shared owner-send helper that reacts to the exact event's rejected row, sends one new event, permits only one real resend, and reports `real resends: N`.
- A presence refresher that exits before deliberate shutdown, plus default-off two-minute evidence coverage.
- Bounded evidence budget and appendable count tables with per-row logs.
- The old timer-driven `AMQ_SQUAD_EVIDENCE_ANSWER_AGAIN` branch is removed.

## Validation

Focused positive and controlled-revert runs are recorded below. Final A and B from the carried packet are both 5/5. `go vet ./internal/buzz` and `go test ./internal/buzz -count=1` passed; the full package took 66.953 s before this follow-up and 67.080 s after it, with the latter saved as [`package-test.log`](package-test.log). `go run ./scripts/redact-evidence -check docs/v3-spike/320-buzz-attach-hang` exited 0. This replacement ran only its declared package checks; repository-wide and host gates remain lead-owned.

## Release note

For the rc.2 notes, under Known limits. The sentence is built from the measured rows above and cites no rate:

> On a busy host an answer or a routed message sent through Buzz can come back `rejected (native_error)`. Nothing was delivered; send it again. In the eight measured runs that could send again (four on each version, at a 5 ms poll), one message needed a third send. Cause: a race in amq-remote, measured on 0.116.2 and on v0.138.0, so upgrading amq-remote does not close it; the report is drafted for upstream.

The rc.2 test harness no longer inherits a live AMQ seat identity, no longer loses relay events between replay and live subscription, and keeps the operator's presence fresh during its waits. The owner dry run and the rehearsal do what the sentence tells the owner: on a rejected row for their own message they send once more, log it, and require exactly one approved outcome.

The rc.2 release gate on the attach hang closes on three things (the plan reviewer's ruling of 2026-11-10, a recorded use of the owner's standing approval, which the owner can overturn): this note merged, the sentence above in the rc.2 notes, and the two tests sending again on a rejected row. The alternative was to hold rc.2 until AMQ fixes the race.
## This PR's rows

- Carry: the `c5ed5d53687f27d09d18d98842d4be2592a062f5` replacement patch had SHA-256 `52bc0e1c69da77b48f9b86e01a75394f613818910f5fac43d215794eddb869a2`; all 100 paths and blobs matched, and the staged tree on dispatch base `6eed06ce` was `b849293ee8930053b603a797f7dc187b3ec9b5f4`.
- Shared helper (c0): done at once sent once; rejected once then done sent twice, logged once, and returned the second id; rejected twice sent exactly twice and called the failure function. The follow-up rows and their observed final-tree failures are below.
- Fake rejection (c1): answer and route rejections used the same detector as real rows, both new events reached serve once, planted rows counted as zero real resends, and the end-of-flow tally recorded one approved outcome. `TestOwnerScriptDryRunRejectsThenSendsAgain` passed in 24.41 s; removing the `rejectionWasPlanted` branch failed at `enroll_test.go:511` (re-measured below; `enroll_test.go:507` is the plant-count assertion, which that revert does not reach).
- Presence: [`presence/refresh.log`](presence/refresh.log) passed after the intentional two-minute wait in 152.152 s. With refresh removed, [`presence/no-refresh.log`](presence/no-refresh.log) failed in 150.41 s with the exact words `attach stopped before the operator deliberately stopped presence`; that saved log cites `enroll_test.go:499` and predates this follow-up's final line numbers. Its captured output records `"action":"stopped"` with state `stale` at about 90 s.
- Real 5 ms measurement: [`poll-5ms/poll-5ms.tsv`](poll-5ms/poll-5ms.tsv) records five separate commands and names every log. Four rows completed with exactly one end-of-flow approved outcome in both flows. Run 3 exited 1 at `rehearsal_test.go:241` after one answer was really rejected twice; the helper made two answer sends and no third ([`run-3.log`](poll-5ms/run-3.log) has one `answer rejected (native_error), sent again` line and then the `twice; not sent again` failure), and the rehearsal outcome count was therefore not reached. Across the four completed rows there were three answer rejections (runs 1, 2 and 4) and two route rejections (runs 2 and 4); run 3's two answer rejections are not in that count.
- Relay boundary: publishing synchronously immediately after replay-lock release was delivered live. The two install-after-unlock orders both failed the install-point lock row at `relay_test.go:493`. The fake relay may deliver an event both in replay and live, with clients deduplicating by id, and may deliver live before replay EOSE; it must not have a loss window.
- Package validation: `go vet ./internal/buzz` passed, and [`package-test.log`](package-test.log) records `go test ./internal/buzz -count=1` passing in 67.080 s. The lead owns repository-wide gates.
- Redaction: `go run ./scripts/redact-evidence -check docs/v3-spike/320-buzz-attach-hang` exited 0 after the last evidence file was added.

On 2026-11-10 at 23:27 IST the lead's baseline run of `TestOwnerScriptDryRunRejectsThenSendsAgain` on #946's head, at the default 500 ms poll with one seat working and a gate run on the host, logged the answer rejection twice and `real resends: 1`: after the plant, the real race rejected the second answer once, the helper sent a third, and the row passed in 27 s. The full log was not kept.

## The follow-up's rows

- Item a, `TestRelayPlantIsInvisibleToReplayQueryAndLiveClients` and `TestRelayPlantDecisionHoldsStoreLock`: `go test ./internal/buzz -run '^TestRelayPlantIsInvisibleToReplayQueryAndLiveClients$' -count=1` failed at `relay_test.go:367` when either the planted row or the owner event was exposed; `go test ./internal/buzz -run '^TestRelayPlantDecisionHoldsStoreLock$' -count=1` failed at the assertion at `relay_test.go:402` when the decision moved after the unlock (printed as `:403` by a run whose edit adds a line above it). Client replay, client `Query`, and a live subscription see neither hidden event; helper reads see both.
- Item b, `TestOwnerScriptDryRunRejectsThenSendsAgain`: `go test ./internal/buzz -run '^TestOwnerScriptDryRunRejectsThenSendsAgain$' -count=1` failed at `enroll_test.go:511` when the planted-verdict branch was removed. Each planted answer and route produced one classifier verdict and one log line after real resends were subtracted; the plants contributed zero real resends.
- Item c, `TestSendOwnerUntilDone`: with `go test ./internal/buzz -run '^TestSendOwnerUntilDone$' -count=1`, the same-id-check revert failed at `signal_test.go:270`; counting the plant as real failed at `signal_test.go:293`; removing the one-real-resend limit failed first at `signal_test.go:256` and then at `signal_test.go:312`; removing the resend (the carried (c0) revert) failed first at `signal_test.go:237`. The helper's common two-real-rejection wording is `rejected (native_error) twice; not sent again`, true whether the unsent event would have been the third or fourth.
- Item d, the end-of-flow counts: the dry run reads its test-owned tally of every `Handler.Step` record at `enroll_test.go:504`; the rehearsal reads the whole test-owned `aout` buffer at `rehearsal_test.go:293`. A named limit: nothing pins where either count sits. This item first said that an after-answer-count revert failed at `enroll_test.go:528` and at `enroll_test.go:547`; neither claim had a command, and neither holds. The lead's four forms of the move (below, "Not caught") all pass, because the dry run's plant is arithmetic on the count (`checkApprovedOutcomes("dry run", approvedOutcomes+1, ...)` at `enroll_test.go:525`), not a second record in the tally, and the rehearsal's plant writes into `aout` and counts again itself at `rehearsal_test.go:302`. A row that pins the position is a 3.2.x item. The plant shows the count is taken at the end in test-owned state; it does not show that a real second approval would reach `h.Step` or `aout`; the protection there is the handler's one-answer rule and the attach inbox assertions of length 1.
- Item e, `TestRelaySubscriptionInstallHoldsStoreLock`: `go test ./internal/buzz -run '^TestRelaySubscriptionInstallHoldsStoreLock$' -count=1` failed at `relay_test.go:493` for both unlock/seam/install and unlock/install/seam. The install hook observes the store lock held and writes nothing to the connection.

### Lines re-measured by the lead

Line numbers in this subsection are as of `b7663f14`; the four test files named here (`relay_test.go`, `signal_test.go`, `enroll_test.go`, `rehearsal_test.go`) are the same blobs there as on #948's head `6150ccc9`. The lead re-ran each revert on 2026-11-11 in a scratch worktree under the host's gate lock, one edit at a time, with `AMQ_SQUAD_EVIDENCE_RUN_BUDGET=120s` set and the live seat's `AM_*` and `AMQ_SQUAD_*` variables removed. Each quoted line is copied from the run; the first one named is the first failing line.

Relay rows, `go test ./internal/buzz -run '^TestRelay' -count=1 -v -timeout 400s`:

- `r.mem.hidden[rejected.ID] = true` removed (the planted row exposed): `relay_test.go:367: post-publish replay exposed 1 hidden events`.
- `r.mem.hidden[e.ID] = true` removed (the owner event exposed): the same line, `relay_test.go:367: post-publish replay exposed 1 hidden events`.
- The `r.onPublishLocked(e)` call moved below `r.mu.Unlock()` (the decision after the unlock): `plant decision called=true store lock held=false`, printed as `relay_test.go:403` because this edit adds one line above the assertion; the assertion is at `relay_test.go:402`.
- In the subscription path, `r.mem.mu.Unlock()` moved above the install, with the `afterReplayUnlock` call before the install: `relay_test.go:476: event published immediately after replay lock release was lost`, then `relay_test.go:493: subscription install called=true store lock held=false`.
- The same with the `afterReplayUnlock` call after the install: `relay_test.go:493: subscription install called=true store lock held=false`.
- The old order (`r.mem.mu.Unlock()`, then the `afterReplay` call, then the install): `relay_test.go:430: the stored replay and live subscription are not one critical section`, then `relay_test.go:493: subscription install called=true store lock held=false`.
- The `r.onSubscriptionInstall()` call removed: `relay_test.go:493: subscription install called=false store lock held=false`.

Helper rows, `go test ./internal/buzz -run '^TestSendOwnerUntilDone$' -count=1 -v -timeout 400s`:

- The `if next == id` block (`fail(kind + " sent again with the same event id")`) removed: `signal_test.go:270: sends=2 failures=[]`.
- `if rejection == ownerRejectedForReal {` became `if rejection != ownerNotRejected {` (the plant counted as real): `signal_test.go:293: id="event-2" sends=2 real resends=1 failures=[answer rejected (native_error) twice; not sent again] logs=[answer rejected (native_error), sent again]`, then `signal_test.go:312`.
- The `if realResends > 1` block (`fail(kind + " rejected (native_error) twice; not sent again")`) removed: `signal_test.go:256: sends=3 real resends=2 failures=[] logs=[answer rejected (native_error), sent again answer rejected (native_error), sent again]`, then `signal_test.go:312`.
- The resend (the log line, `next := send()`, the same-id check, `id = next`, `continue`) replaced by `return id, realResends`: `signal_test.go:237: id="event-1" sends=1 real resends=1 logs=[]`, then `signal_test.go:256`, `:270`, `:293` and `:312`.

Dry-run rows, `go test ./internal/buzz -run '^TestOwnerScriptDryRunRejectsThenSendsAgain$' -count=1 -v -timeout 400s`:

- The `if relay.rejectionWasPlanted(id)` block (`plantedVerdicts[kind]++`, `return ownerRejectedByPlant`) removed: `enroll_test.go:511: answer lines minus real resends = 0, want one planted line`, after the log line `enroll_test.go:503: real resends: 2`.
- `plantedVerdicts[kind]++` removed: `enroll_test.go:520: planted classifier verdicts = answer 0 route 0, want one each`.
- `logLines[kind]++` removed: `enroll_test.go:511: answer lines minus real resends = 0, want one planted line`.
- The helper's resend replaced by `return id, realResends`, as above: `enroll_test.go:443: the attach inbox after serve's delivery of the answer: [], <nil>`.

Not caught: each of these four edits exited 0 with the test passing.

- The dry run's `checkApprovedOutcomes("dry run", approvedOutcomes, ...)` line and its `t.Logf("approved outcomes: %d", approvedOutcomes)` moved to right after `stepTook := time.Since(tStep)`, the answer's step.
- The dry run's second `for _, record := range handled` loop (the one before the routed-message inbox check) removed, so the route step's records are not counted.
- `t.Logf("approved outcomes: %d", approvedOutcomes)` became `t.Logf("approved outcomes: %d", 1)`.
- The rehearsal's count of `"outcome":"approved"` in `aout`, its `checkApprovedOutcomes("rehearsal", ...)` line and its log moved to right after the `raise to verified through remote attach` log line; this one ran `go test ./internal/buzz -run '^TestOwnerScriptDryRun$' -count=1 -v -timeout 400s`.

## Limits

- The rc.2 release gate closes with this PR on the corrected note, its Known limits sentence, and both owner flows sending once more on a rejected row; the AMQ race itself is not fixed here and is not worked around in product code.
- No change is made to AMQ, vendored code, owner-visible prompts, or gate-answer acceptance semantics.
- The captured test-owned supervisor PID had no reachable serve child at snapshot time, so there is no safe serve stack for these three rows.
- Condition C and the locked host rerun remain lead-owned.
- The presence refresher has no row in `make ci`; its only row is behind the evidence variable. With the refresh removed, the ordinary run stays green in about 31 s, as measured by the lead.
- Evidence logs contain test fixtures and temporary paths. The redaction pass removes credentials and replaces live-seat identity values; it does not rewrite test-owned fixture identities.
