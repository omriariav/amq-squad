# Draft upstream report: admitted owner answer has no mailbox delivery or outcome

Do not send from this task. This is an evidence draft for the owner/lead.

## Affected version

`amq-remote 0.116.2`, exercised by the amq-squad owner dry-run integration test on macOS with a local websocket relay.

## Symptom

Under condition D—one owner dry run beside `go test ./... -count=1`—3 of 9 structured rows stopped at `the attach verifies the thread answer`. Every companion module run exited 0. The same symptom was already present in 3 of 12 earlier loaded rows.

## Measured sequence

1. `amq-remote serve` authenticates and installs the `dm-amq-squad` live subscription.
2. The owner kind-9 answer is stored by the relay and delivered live to that subscription.
3. The body publishes its next kind-9/running row.
4. No attach `handled` row and no approved gate outcome follow.
5. The request journal remains a tombstone with `undelivered="its delivery never started"`, empty `created` and `message_id`, `acked=false`, and no prompt text. A nonblocking probe acquires and releases the journal lock, so it is not held at either snapshot.
6. Attach `inbox/new` and both DLQ directories remain empty. `inbox/cur` contains only earlier messages.
7. The serve relay connection closes about 90 seconds after it opened. No supervisor restart is observed.
8. A second owner answer, sent only after the first 120-second wait and the capture, is stored after the serve connection has closed. It is not handled and creates no outcome.

Representative evidence is in `capture/D-9.log`; the same journal/lock/result is in `capture/D-1.log` and `capture/D-2.log`. The row table is `capture/D.tsv`.

## Separation already performed

- The fake relay's replay-to-live gap was fixed and has a deterministic controlled-revert test.
- The failing owner answer is nevertheless present in the relay trace as a live delivery, so that gap is not this failure.
- The await is only polling for the absent approved result; it holds no mailbox lock.
- The AMQ journal lock is free before and after the evidence resend.
- The literal `edit waits for the clock to pass the previous edit's second` is not sufficient: it concerns an earlier ingest in two captures, the later answer is delivered, and an earlier sighting hung without that line.
- The test retained only its own attach process PID. At snapshot time it could not find a reachable `amq-remote serve` descendant, so it did not signal by global process name. There is no safe serve stack capture.

## Owner impact

The relay accepts a valid owner answer, but the gate remains unresolved and the attach emits no handled result. Resending after the serve disappears only stores another event; exactly-once completion and safe automatic retry are not demonstrated.

## Questions for AMQ

1. Which code path writes `undelivered="its delivery never started"` without `created`, `message_id`, prompt publication, or an outcome after a live DM event was received?
2. What causes the serve connection to close without the supervisor restarting it while an admitted request has no terminal outcome?
3. Should a repeated answer be replayed after serve restart, deduplicated against a pending request, or converted to a terminal retryable outcome?
4. What additional process-local trace can distinguish request-admission bookkeeping from serve lifecycle shutdown without logging prompt bodies?
