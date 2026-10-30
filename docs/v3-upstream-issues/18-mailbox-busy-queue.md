# amq-remote: queue owner DMs on a mailbox target instead of rejecting them busy

## Problem

A strict relay share bound to a `mailbox` target runs one request at a time. An owner DM that arrives while the previous request is still open (delivered, and its reply not yet taken as the result) is not queued: it ends as a terminal `busy` result row, and it never reaches the handle. Because a redelivered DM never runs again after a busy rejection, the owner has to notice the row and send the message again.

For amq-squad this is the phone face of 3.0. The owner answers a typed gate, then sends a routed message seconds later; the second message is lost to `busy` unless he waits for the first result row. The handle replies within about 150 ms, but serve takes the reply only on its next poll (500 ms by default), so the busy window is about a second per message. A gate answer lost to busy costs a re-answer (the gate stays open); a routed message lost to busy has to be re-sent. The handle cannot help: the rejection happens in the endpoint's admission before the mailbox adapter is called, so the handle never learns of it.

## Evidence (AMQ v0.116.2)

- `internal/remote/buzzio/carrier.go:527`: the carrier submits every owner DM with `Busy: protocol.BusyReject`.
- `internal/remote/core/admission.go:25-32`: `busy=queue` (and `deliver=steer`) are refused as unsupported in v1 ("full ownership/evidence model deferred").
- `internal/remote/core/admission.go:151-183`: a second dispatch for a target with a request in flight (`runtimeInFlightLocked`) is persisted as a terminal busy tombstone and replied `busy`; `dispatchReceivedLocked` (the adapter's Submit) is never reached.
- `internal/remote/buzzio/ledger.go:114-119` and `cmd/amq-remote/README.md:386`: a redelivered DM with a settlement is never sent again, even after a busy rejection.
- `cmd/amq-remote/README.md:106` ("Manifest", the `mailbox` row): "One request runs at a time, so the handle ends its request before it asks the owner a question."
- `internal/remote/mailbox/mailbox.go` (observe, finalReply): the result is decided when the endpoint next looks; `serve --poll` defaults to `500ms` (README "`serve` and `up`").
- amq-squad brief 5-amq-B (`docs/v3-spike/5-amq-execution.md`, part B), measured against a v0.116.2 build and a local relay: handle reply 142 to 154 ms after delivery, owner answer to result row 970 to 994 ms; a routed DM sent right after a verified gate answer was never delivered.

## What amq-squad needs

One of, in order of preference:

1. Queue inbound requests on a mailbox target (`busy=queue` for the mailbox kind): the adapter journal already fixes each request's message before delivery, and the handle answers each message by its own id, so requests to one handle need no shared turn. The owner's DMs then reach the handle in order.
2. At least a retry hint in the busy result row: "busy: the previous message is still open; send this again", so the owner knows the message was not delivered and that sending it again is safe.

Either way, a local record the handle (or a layer) can read: the busy tombstone's request ref and event id in a form `amq-remote requests --json` documents, so a layer can raise its own attention row for a DM that was never delivered.
