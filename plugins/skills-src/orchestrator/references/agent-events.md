# Status attention and progress contract

## `status` is the operator projection

```sh
amq-squad status --project P --session S --json
```

`status` reads the task store, the seat mailboxes and launch records, the evidence
ledger, the gate store and the scheduler state, and prints the status contract
(schema 1). It never writes and is never mutation authority. Read the complete JSON
document rather than reconstructing state from pane titles or message prose. A read
problem is one line in `warnings[]`; a partial status is still valid.

## Progress travels over durable AMQ

Seats push ACK, meaningful progress, blockers, review requests, and DONE on the
durable task thread. Pane delivery is never the channel. Liveness comes from the
launch record, presence and the evidence ledger: a seat row is `online` when one of
them is live inside the freshness window, `stale` when signals exist but none is
live, and `unknown` with no signal at all. A claimed task without current evidence is
only context, not proof that the seat is healthy.

## Park instead of polling

After one status read, act or park/end the turn. Do not create a hand-written sleep
loop or block the lead waiting for change. AMQ wake delivers pending messages to the
seat; `doctor` diagnoses the host when wake health is suspect.

## Owner attention remains non-authorizing

Open typed gates and refusals routed to the owner land in the `needs_you` bucket and
in `attention[]`. The operator session sees every squad's open gates in one list
(`operator inbox`). Those signals never approve, answer or clear a gate. Only the
owner's signed answer, verified by the core, resolves a typed gate.
