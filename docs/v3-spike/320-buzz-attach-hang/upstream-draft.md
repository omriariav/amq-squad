# Draft upstream report: a reconcile tick refuses an admission in flight (mailbox adapter)

Not sent. This is a draft for the owner, who sends it or not. The message is the text between the two rules; it carries no session identifier, no prompt text and none of the owner's words.

---

**amq-remote: a reconcile tick between an admission's unlock and its Submit refuses the request (`native_error`, "its delivery never started")**

Version: amq-remote 0.116.2 and v0.138.0, both measured; a strict share bound to a mailbox, macOS, a local websocket relay. The same code is in v0.134.0 (read, not run).

What happens. A request sent through the share is sometimes refused within about a second: the body posts a kind 9 row whose whole content is `amq-squad: rejected (native_error)`. Nothing is delivered to the mailbox. The request's journal entry reads `undelivered: "its delivery never started"` and has no message id. The same request sent again is delivered.

Cause, as read in the source at v0.116.2:

1. Admission commits the record as Dispatching under the endpoint lock, releases the lock, and only then calls the adapter's Submit: `internal/remote/core/admission.go:267` (the unlock), `:271` (Submit). The second path has the same shape (`:344`, `:349`).
2. serve's ticker (`cmd/amq-remote/serve.go:226-237`, `--poll`, default 500 ms at `:65`) calls Tick, which reconciles every open record. A Dispatching record goes to `reconcileLive` (`internal/remote/core/reconcile.go:128-129`), which calls the adapter's Lookup (`:472`). Nothing marks an admission as in flight.
3. The mailbox adapter's Lookup finds no journal entry and writes the tombstone `Undelivered: "its delivery never started"` (`internal/remote/mailbox/mailbox.go:342`), and reconcile refuses the record.
4. The Submit from step 1 then finds the tombstone and answers `native_error` (`mailbox.go:726-727`).

In v0.134.0 the same lines are `admission.go:268` and `:272`, `reconcile.go:115-116` and `:453`, `mailbox.go:347`, `serve.go:71`. In v0.138.0 they are the same but for the tombstone at `mailbox.go:348`.

Reproduction. Start the endpoint with `--poll 5ms` (`amq-remote up ... --poll 5ms`, or the same on `serve`) and send requests one at a time through a mailbox share. With no other load on the host, 6 of 8 runs of a two-request scenario had a request refused this way. At the default 500 ms: 30 of 30 runs passed with the scenario alone or inside its own package's test run, and 3 of 9 had a refused request with a `go test ./...` of the whole module running beside them. Those rows are 0.116.2. Measured on v0.138.0 at `--poll 5ms`, with no other load: a request was refused in 4 of 9 runs; at the default, the one run passed.

Sending again. After a request that was not delivered, the same request sent again was delivered and completed exactly once: on 0.116.2 in 3 of 3 cases at the second send; on v0.138.0 in 3 of 3 cases, twice at the second send and once at the third (all at 5 ms).

What the sender is told. The rejected row carries the code only. The sentence `notDelivered` builds ("Nothing reached its inbox; send it again.") does not reach the thread.

Questions:

1. Should reconcile leave alone a Dispatching record whose admission has not yet returned from Submit, for example by a mark set before the unlock at `:267` and cleared after `:271`?
2. The tombstone's comment says it is there so that "a Submit that has not run yet delivers nothing". Is that needed for an admission that is running in this process, or only for a record found after a restart?
3. Could the rejected row carry the `notDelivered` sentence, so the sender is told to send again?

Evidence. The tables and logs (the journal line, a relay trace, and the content of each kind 9 and 40003 row) are in the amq-squad repository under `docs/v3-spike/320-buzz-attach-hang/lead-probe/`. They hold test fixtures only.

Sent by Claude on behalf of Omri

---

## Withdrawn from the earlier draft of this report

- "The body publishes its next kind-9/running row": that row is the rejected row.
- "The serve relay connection closes about 90 seconds after it opened": that was this repository's test fixture, whose presence file went stale and made attach stop the endpoint.
- "A second owner answer ... is not handled": it was sent after the endpoint had been stopped. On a live endpoint a second answer works.
