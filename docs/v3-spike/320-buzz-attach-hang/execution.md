# 3.2.0-rc.2 Buzz attach hang release gate

Date: 2026-11-10

Verdict: **land this PR, but keep the rc.2 release gate closed**. The PR fixes two measured test-harness defects and preserves the capture needed upstream. It does not claim to repair the remaining `amq-remote 0.116.2` mailbox lifecycle failure: condition D reproduced that failure in 3 of 9 rows.

## Evidence provenance and exclusions

The t142 carry was applied mechanically from `/Users/omri/.claude/jobs/6dd424c9/tmp/t142-attempt1/`. Its `tracked.diff` SHA-256 was `1b61ab9a18c71892f34cd63a075e78a4cc9473075af11af65f8bf9dccec7d7`; the saved execution note was `b615b2834deaa6bb68069b56e629465765e107252d81f5315c3f25dd00858121`; and `count.sh` was `6824b2677392dc525ebc03f9706d550a7e2771acc97a6640e2ac67c5378529be`. The carried folder contained 61 files before this task's new captures.

Every table now has a `log` column. Passing rows retain their log; no row in the tables below relies on a discarded log.

Three observations are excluded from behavioral counts:

- The saved t141 B 0/10 result stopped at `online while the lead runs` because scratch commands inherited the live seat's AMQ identity. It measures test isolation, not the remote-attach answer hang.
- [`base-120/B-1.log`](base-120/B-1.log) exited during package setup in 0 seconds because the sandbox denied the Go build cache. It never ran the test.
- [`after/D-1.companion.log`](after/D-1.companion.log) deliberately retained the live seat identity and the companion refused harness-only tests. The main row passed, but the companion exit 1 makes it invalid as condition D.

The two 314-second identity-pin rows in [`base-120-host/B.tsv`](base-120-host/B.tsv) are not evidence that a 120-second wait ran for 314 seconds. The package performed roughly 194 seconds of other test/build work before the owner test entered its bounded 120-second wait.

The repository redactor was run over the whole evidence directory. A follow-up identity audit replaced eight live-seat values in the two saved host logs: pinned-root references on lines 33, 264, and 466 of each of `base-120-host/B-1.log` and `B-2.log` (six replacements), plus the `AM_SESSION` value on line 264 of each log (two replacements). The placeholders are `<live-amq-root>` and `<live-session>`; test-owned temporary roots were retained.

## Count tables

[`count.sh`](count.sh) records exit, seconds, load before and after, named hang point, companion exit, and log filename. Its evidence budget accepts 2 through 5 minutes; the ordinary default remains five minutes. `AMQ_SQUAD_EVIDENCE_ANSWER_AGAIN` is default-off and was unset in the final A/B rows.

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

Classification: test isolation, in scope and fixed.

## Finding 2: the fake relay had a replay/live loss window

The old fake websocket relay queried stored events and installed its live subscription in separate critical sections. An event published between them belonged to neither replay nor live delivery. The new helper snapshots replay and installs the subscription under the store lock. The trace records open/close, authentication, storage, replay, subscription, and live delivery.

The controlled revert restored the two critical sections and ran:

```text
go test ./internal/buzz -run '^TestRelayReplayAndLiveSubscriptionHaveNoLossWindow$' -count=1
```

It failed at `relay_test.go:260: the stored replay and live subscription are not one critical section`. Restoring the atomic section makes the focused test pass.

Classification: fake-relay behavior, in scope and fixed. This can explain the earlier S3 route-delivery stop, but it does not explain the three newly captured answer hangs because their answers were visibly delivered live.

## Finding 3: the AMQ boundary leaves no delivery as the live serve closes

The structured rows [`capture/D-1.log`](capture/D-1.log), [`capture/D-2.log`](capture/D-2.log), and [`capture/D-9.log`](capture/D-9.log) agree on the following sequence:

1. The remote serve has a live `dm-amq-squad` subscription.
2. The owner's first kind-9 answer is stored and delivered live to that subscription. In D9 this occurs at 35.986 seconds; the body emits its next kind-9 row at 36.717 seconds.
3. The expected attach `handled` row and approved gate outcome never appear.
4. Before the evidence resend, the request journal says `undelivered="its delivery never started"`, with empty `created` and `message_id`, `acked=false`, `prompt_present=false`, and a free nonblocking lock. The attach `new` and DLQ directories stay empty; `cur` contains only earlier messages.
5. The serve connection closes around 2m05s without a supervisor restart. The evidence-only second answer is stored later (D9: 2m46s), when no live serve subscription remains, so it is not handled. Each capture records `serve_restarted=false handled=false approved_outcomes=0`.
6. The same outcome-less journal and free lock remain after the resend. The test await is only observing the absent approved result; it is not the blocked actor.

The test-owned process handle could not safely reach the short-lived `amq-remote serve` child: D2 and D9 report `supervisor=<pid> has no reachable serve child`. The capture intentionally did not use a global name lookup or signal an unrelated process. The journal, lock, mailbox timeline, up output, and relay trace therefore provide the safe separation evidence; a serve goroutine dump is unavailable for these rows.

This separates the failure from the test await and the fake relay: the relay delivered the answer, but the AMQ-owned remote mailbox lifecycle left a pre-delivery tombstone, published no prompt, produced no terminal outcome, and closed the serve connection. The available AMQ 0.116.2 surfaces do not separate whether its internal trigger was a serve lifecycle transition or request-admission bookkeeping. That remaining distinction belongs inside AMQ, not this repository.

Classification: **(c), AMQ `amq-remote` 0.116.2**. No vendored patch or owner-acceptance behavior change is made here. [`upstream-draft.md`](upstream-draft.md) contains the evidence-only report draft.

The `edit waits for the clock to pass the previous edit's second` line is correlated, not causal. It appears in D2 and D9 for an earlier ingest, while the later owner answer is demonstrably delivered; S3 also hung without the line.

## Real-owner impact

On the measured bad path, the owner submits a valid answer and the relay accepts it, but the attach never reports `handled` and the gate records no approved outcome. The live serve disappears before a retry, so sending the same answer again merely stores another relay event. The gate remains unresolved until external recovery; the evidence does not establish safe automatic retry or exactly-once completion.

## Changes retained

- Scratch AMQ identity scrubbing and regression coverage.
- Atomic replay/live setup in the fake relay and deterministic boundary coverage.
- Failure-only relay trace and structured journal/lock/mailbox/up capture.
- Test-owned serve SIGQUIT when safely reachable; an explicit diagnostic when it is not.
- Default-off evidence-only answer-again branch with two full 120-second waits.
- Bounded evidence budget and appendable count tables with per-row logs.

## Validation

Focused positive and controlled-revert runs are described above. Final A and B are both 5/5. `go build ./internal/buzz`, `go vet ./internal/buzz`, the four-test focused suite, and `go test ./internal/buzz -count=1` all pass; the package run took 43.382 seconds.

The definitive repository gate ran seat-free, with the existing task-local PyYAML 6.0.3 environment first on `PATH`, the existing Node/npm toolchain present, and `NPM_FLAGS=--offline`: `make ci` exited 0. It passed every Go package, the HTML and generated-skill checks, command/invocation checks, release and hook suites, and the Pi suite (120 pass, 1 intentionally skipped). Two preliminary invocations are excluded: the first retained live seat metadata and correctly tripped harness-only CLI tests; the second passed all Go packages but stopped at the documented missing-PyYAML prerequisite. A third passed through the hook suite but used an overly narrow `PATH` that omitted the installed npm. No source change was made for any of those host/environment stops.

## Release note

The rc.2 test harness no longer inherits a live AMQ seat identity and no longer loses relay events between replay and live subscription. A known `amq-remote 0.116.2` mailbox lifecycle defect can still leave an accepted owner answer without a handled gate outcome under concurrent module load; rc.2 remains gated on the upstream repair or upgrade.

## Limits

- The PR lands the measured harness fixes and diagnostics; it does not open the rc.2 release gate.
- No change is made to AMQ, vendored code, owner-visible prompts, or gate-answer acceptance semantics.
- The captured test-owned supervisor PID had no reachable serve child at snapshot time, so there is no safe serve stack for these three rows.
- Condition C and the locked host rerun remain lead-owned.
- Evidence logs contain test fixtures and temporary paths. The redaction pass removes credentials and replaces live-seat identity values; it does not rewrite test-owned fixture identities.
